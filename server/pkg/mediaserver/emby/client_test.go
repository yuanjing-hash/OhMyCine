package emby

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/pkg/mediaserver"
)

func TestProbeUsesHeaderCredentialAndNeverFollowsRedirect(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	defer target.Close()
	var token string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = r.Header.Get("X-Emby-Token")
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer upstream.Close()
	client, err := New(Config{Endpoint: upstream.URL, APIKey: "private-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Probe(context.Background()); err == nil {
		t.Fatal("redirected probe was accepted")
	}
	if token != "private-key" || redirected.Load() != 0 {
		t.Fatalf("token=%q redirected=%d", token, redirected.Load())
	}
}

func TestNormalizeAPIKeyRejectsUnboundedAndHeaderUnsafeValues(t *testing.T) {
	for _, value := range []string{"", "   ", "key\r\nX-Injected: value", "key\x00value", strings.Repeat("x", maxAPIKeyBytes+1)} {
		if _, err := NormalizeAPIKey(value); err == nil {
			t.Fatal("unsafe Emby API key was accepted")
		}
		if _, err := New(Config{Endpoint: "https://example.test", APIKey: value}); err == nil {
			t.Fatal("client accepted unsafe Emby API key")
		}
	}
	if normalized, err := NormalizeAPIKey("  safe-key  "); err != nil || normalized != "safe-key" {
		t.Fatalf("normalized=%q err=%v", normalized, err)
	}
}

func TestParseEndpointRejectsCredentialQueryAndEncodedSeparator(t *testing.T) {
	for _, value := range []string{
		"file:///tmp/emby",
		"https://user:pass@example.test",
		"https://example.test?api_key=secret",
		"https://example.test/base%2Fescape",
		"https://example.test/base%252Fescape",
		"https://example.test/base\\escape",
		"https://example.test/" + strings.Repeat("x", 2049),
	} {
		if _, err := ParseEndpoint(value); err == nil {
			t.Fatalf("unsafe endpoint accepted: %q", value)
		}
	}
}

func TestManagementSummaryReturnsOnlySafeAggregateFacts(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.Header.Get("X-Emby-Token") != "management-key" {
			t.Fatal("management credential missing from controlled request")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/emby/System/Info":
			_, _ = w.Write([]byte(`{"Id":"server-id","ServerName":"Living Room","Version":"4.9.1","Secret":"must-not-escape"}`))
		case "/emby/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[{"Name":"Private Movies","CollectionType":"movies"},{"Name":"Private TV","CollectionType":"tvshows"}]`))
		case "/emby/Items/Counts":
			_, _ = w.Write([]byte(`{"MovieCount":12,"SeriesCount":3,"EpisodeCount":48,"MusicCount":99}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL + "/emby", APIKey: "management-key"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := client.ManagementSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if requestCount.Load() != 3 || summary.Server.Version != "4.9.1" || summary.LibraryCount == nil || *summary.LibraryCount != 2 || summary.MovieCount == nil || *summary.MovieCount != 12 || summary.SeriesCount == nil || *summary.SeriesCount != 3 || summary.EpisodeCount == nil || *summary.EpisodeCount != 48 || summary.Partial {
		t.Fatalf("unexpected safe summary: %+v requests=%d", summary, requestCount.Load())
	}
	encoded := summary.Server.ID + summary.Server.Name + summary.Server.Version
	if strings.Contains(encoded, "Private") || strings.Contains(encoded, "must-not-escape") || strings.Contains(encoded, "management-key") {
		t.Fatalf("management summary leaked upstream data: %q", encoded)
	}
}

func TestManagementSummaryKeepsUnavailableOptionalCountersUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/System/Info" {
			_, _ = w.Write([]byte(`{"Id":"server-id","ServerName":"Emby","Version":"4.8"}`))
			return
		}
		http.Error(w, "private upstream failure", http.StatusForbidden)
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, APIKey: "management-key"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := client.ManagementSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Partial || summary.LibraryCount != nil || summary.MovieCount != nil || summary.SeriesCount != nil || summary.EpisodeCount != nil {
		t.Fatalf("optional failures were not represented as unknown: %+v", summary)
	}
}

func TestManagementSummaryTreatsMissingNullAndNegativeAggregatesAsUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/System/Info":
			_, _ = w.Write([]byte(`{"Id":"server-id","ServerName":"Emby","Version":"4.8"}`))
		case "/Library/VirtualFolders":
			_, _ = w.Write([]byte(`null`))
		case "/Items/Counts":
			_, _ = w.Write([]byte(`{"MovieCount":12,"SeriesCount":-1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, APIKey: "management-key"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := client.ManagementSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Partial || summary.LibraryCount != nil || summary.MovieCount == nil || *summary.MovieCount != 12 || summary.SeriesCount != nil || summary.EpisodeCount != nil {
		t.Fatalf("invalid aggregates were presented as facts: %+v", summary)
	}
}

func TestListLibrariesAndRefreshUseStableItemID(t *testing.T) {
	var refreshPath, refreshQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "management-key" {
			t.Fatal("management credential missing")
		}
		switch r.URL.Path {
		case "/prefix/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[{"ItemId":"stable-library-id","Name":"电影","CollectionType":"movies"}]`))
		case "/prefix/Items/stable-library-id/Refresh":
			refreshPath, refreshQuery = r.URL.Path, r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL + "/prefix", APIKey: "management-key"})
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := client.ListLibraries(context.Background())
	if err != nil || len(libraries) != 1 || libraries[0].ID != "stable-library-id" || libraries[0].Name != "电影" {
		t.Fatalf("libraries=%+v err=%v", libraries, err)
	}
	if err := client.RefreshLibrary(context.Background(), libraries[0].ID); err != nil {
		t.Fatal(err)
	}
	if refreshPath == "" || !strings.Contains(refreshQuery, "Recursive=true") {
		t.Fatalf("refresh path=%q query=%q", refreshPath, refreshQuery)
	}
}

func TestRefreshRejectsOversizedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", maxProbeResponseBytes+1)))
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, APIKey: "management-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RefreshLibrary(context.Background(), "library-id"); err == nil {
		t.Fatal("oversized refresh response was accepted")
	}
}

func TestProbePreservesStableProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private response", http.StatusUnauthorized)
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, APIKey: "management-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Probe(context.Background()); mediaserver.ErrorCode(err) != mediaserver.ErrorUnauthorized {
		t.Fatalf("probe error=%v code=%q", err, mediaserver.ErrorCode(err))
	}
}
