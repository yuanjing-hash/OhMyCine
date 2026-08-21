package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientUsesBoundedOfficialShapeWithoutLeakingTokenIntoURL(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if strings.Contains(r.URL.String(), "test-token") || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("token boundary violated")
		}
		switch r.URL.Path {
		case "/movie/550":
			_, _ = io.WriteString(w, `{"id":550}`)
		case "/search/movie":
			if r.URL.Query().Get("query") != "Example Movie" || r.URL.Query().Get("year") != "2026" {
				t.Fatalf("query=%s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"results":[{"id":42,"title":"Example Movie","original_language":"zh","genre_ids":[16],"release_date":"2026-01-01"}]}`)
		case "/movie/42":
			if r.URL.Query().Get("append_to_response") != "credits,external_ids" {
				t.Fatalf("append_to_response=%q", r.URL.Query().Get("append_to_response"))
			}
			_, _ = io.WriteString(w, `{"id":42,"title":"示例电影","original_title":"Example Movie","original_language":"zh","release_date":"2026-01-01","overview":"安全简介","tagline":"一句话简介","status":"Released","vote_average":8.5,"vote_count":1234,"runtime":123,"poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg","genres":[{"id":16,"name":"动画"}],"production_countries":[{"iso_3166_1":"CN"}],"spoken_languages":[{"iso_639_1":"zh"},{"iso_639_1":"en"}],"production_companies":[{"id":9,"name":"示例制片厂"}],"credits":{"cast":[{"id":1,"name":"演员","character":"角色"}],"crew":[{"id":2,"name":"导演","department":"Directing","job":"Director"},{"id":3,"name":"编剧","department":"Writing","job":"Screenplay"}]},"external_ids":{"imdb_id":"tt1234567"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewForTest("test-token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	year := 2026
	match, err := client.Search(context.Background(), "movie", "Example Movie", &year, "zh-CN", "CN")
	if err != nil {
		t.Fatal(err)
	}
	if match.ID != 42 || match.Confidence < .9 || len(match.ProductionCountries) != 1 || match.ProductionCountries[0] != "CN" {
		t.Fatalf("match=%+v", match)
	}
	if match.Snapshot.TMDBID != 42 || match.Snapshot.IMDbID != "tt1234567" || match.Snapshot.PosterPath != "/poster.jpg" || match.Snapshot.Tagline != "一句话简介" || match.Snapshot.Status != "Released" || match.Snapshot.VoteCount != 1234 || len(match.Snapshot.SpokenLanguages) != 2 || len(match.Snapshot.Studios) != 1 || match.Snapshot.Studios[0].Name != "示例制片厂" || len(match.Snapshot.Directors) != 1 || len(match.Snapshot.Writers) != 1 || len(match.Snapshot.Cast) != 1 {
		t.Fatalf("snapshot=%+v", match.Snapshot)
	}
	payload, err := json.Marshal(match.Snapshot)
	if err != nil || strings.Contains(string(payload), server.URL) || strings.Contains(string(payload), "test-token") || strings.Contains(string(payload), "api_key") {
		t.Fatalf("unsafe snapshot=%s err=%v", payload, err)
	}
	if requests != 3 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestClientRoutesAPIKeyThroughQueryWithoutAuthorizationHeader(t *testing.T) {
	const apiKey = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Fatal("API key request unexpectedly used Authorization")
		}
		if r.URL.Query().Get("api_key") != apiKey {
			t.Fatal("API key query parameter missing")
		}
		_, _ = io.WriteString(w, `{"id":550}`)
	}))
	defer server.Close()
	client, err := NewForCredentialTest(Credential{Kind: CredentialKindAPIKey, Value: apiKey}, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.TestAPI(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGetByIDBuildsSafeTVSnapshotWithSeasonImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tv/100" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"id":100,"name":"示例剧","original_name":"Example Show","original_language":"ja","first_air_date":"2020-04-01","overview":"简介","tagline":"剧集标语","status":"Returning Series","vote_average":7.8,"vote_count":88,"episode_run_time":[24],"number_of_seasons":1,"number_of_episodes":12,"origin_country":["JP"],"production_countries":[{"iso_3166_1":"JP"}],"spoken_languages":[{"iso_639_1":"ja"}],"production_companies":[{"id":10,"name":"动画工作室"}],"poster_path":"https://unsafe.example/poster.jpg?token=x","backdrop_path":"/safe-backdrop.jpg","genres":[{"id":16,"name":"Animation"}],"created_by":[{"id":3,"name":"原作"}],"seasons":[{"id":10,"season_number":1,"name":"Season 1","air_date":"2020-04-01","episode_count":12,"poster_path":"/season-1.jpg"}],"external_ids":{"imdb_id":"tt7654321"}}`)
	}))
	defer server.Close()
	client, err := NewForTest("test-token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	match, err := client.GetByID(context.Background(), "tv", 100, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if match.Snapshot.PosterPath != "" || match.Snapshot.BackdropPath != "/safe-backdrop.jpg" || match.Snapshot.SeasonCount != 1 || match.Snapshot.EpisodeCount != 12 || match.Snapshot.VoteCount != 88 || len(match.Snapshot.Studios) != 1 || len(match.Snapshot.Seasons) != 1 || match.Snapshot.Seasons[0].PosterPath != "/season-1.jpg" || len(match.Snapshot.Writers) != 1 {
		t.Fatalf("snapshot=%+v", match.Snapshot)
	}
}

func TestDownloadJPEGAcceptsOnlyBoundedSnapshotIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/w500/poster.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xff, 0xd8, 0xff, 0xdb, 1, 2, 3})
		case "/w500/redirect.jpg":
			http.Redirect(w, r, "/w500/poster.jpg", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewForTest("test-token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	image, err := client.DownloadJPEG(context.Background(), "/poster.jpg", "w500", 1024)
	if err != nil || len(image) != 7 {
		t.Fatalf("image=%v err=%v", image, err)
	}
	for _, test := range []struct {
		identity string
		size     string
		limit    int64
	}{
		{"https://unsafe.example/poster.jpg", "w500", 1024},
		{"/poster.jpg", "unsafe", 1024},
		{"/poster.jpg", "w500", 2},
		{"/redirect.jpg", "w500", 1024},
	} {
		if _, err := client.DownloadJPEG(context.Background(), test.identity, test.size, test.limit); err == nil {
			t.Fatalf("accepted identity=%q size=%q limit=%d", test.identity, test.size, test.limit)
		}
	}
}

func TestCredentialKindIsExplicitAndErrorsDoNotExposeAPIKey(t *testing.T) {
	if _, err := NewWithCredential(Credential{Kind: "automatic", Value: "secret"}); err == nil {
		t.Fatal("unknown credential kind accepted")
	}
	const apiKey = "private-api-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rejected", http.StatusUnauthorized)
	}))
	defer server.Close()
	client, err := NewForCredentialTest(Credential{Kind: CredentialKindAPIKey, Value: apiKey}, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.TestAPI(context.Background())
	if err == nil || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("unsafe error=%v", err)
	}
}

func TestDefaultRouteFallsBackOnlyOnNetworkFailure(t *testing.T) {
	fallbackRequests := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackRequests++
		if r.URL.Path != "/movie/550" {
			t.Fatal(r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":550}`)
	}))
	defer fallback.Close()
	client, err := NewForFallbackTest("token", "http://127.0.0.1:1", fallback.URL, fallback.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.TestAPI(context.Background()); err != nil || fallbackRequests != 1 {
		t.Fatalf("err=%v fallback=%d", err, fallbackRequests)
	}

	fallbackRequests = 0
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "unauthorized", http.StatusUnauthorized) }))
	defer primary.Close()
	client, err = NewForFallbackTest("token", primary.URL, fallback.URL, primary.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.TestAPI(context.Background()); err == nil || fallbackRequests != 0 {
		t.Fatalf("HTTP response incorrectly fell back: err=%v fallback=%d", err, fallbackRequests)
	}
}

func TestRoutesRejectUnsafePrefixesAndImageTestIsBounded(t *testing.T) {
	for _, value := range []string{"http://api.example.test/3", "https://user:pass@api.example.test/3", "https://api.example.test/3?token=x", "https://api.example.test/3?", "https://api.example.test/3#x", "https://api.example.test/3#", "https://api.example.test/a/../3"} {
		if _, err := ValidateBaseURL(value); err == nil {
			t.Fatalf("accepted unsafe route %q", value)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ".jpg") {
			t.Fatal(r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, "small-image")
	}))
	defer server.Close()
	if err := testImageBaseWithClient(context.Background(), server.URL, server.Client()); err != nil {
		t.Fatal(err)
	}
	for name, handler := range map[string]http.HandlerFunc{
		"content type": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "not-image")
		},
		"oversized": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = io.WriteString(w, strings.Repeat("x", maxImageTestBytes+1))
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://example.test/image.jpg", http.StatusFound)
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := httptest.NewServer(handler)
			defer invalid.Close()
			if err := testImageBaseWithClient(context.Background(), invalid.URL, controlledHTTPClient()); err == nil {
				t.Fatal("invalid image route response was accepted")
			}
		})
	}
}

func TestAPIRedirectIsRejectedWithoutFallback(t *testing.T) {
	fallbackRequests := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackRequests++
		_, _ = io.WriteString(w, `{"id":550}`)
	}))
	defer fallback.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fallback.URL+r.URL.Path, http.StatusFound)
	}))
	defer primary.Close()
	client, err := NewForFallbackTest("token", primary.URL, fallback.URL, controlledHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.TestAPI(context.Background()); err == nil || fallbackRequests != 0 {
		t.Fatalf("redirect was followed/fell back: err=%v fallback=%d", err, fallbackRequests)
	}
}

func TestTLSValidationFailureDoesNotFallback(t *testing.T) {
	fallbackRequests := 0
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackRequests++
		_, _ = io.WriteString(w, `{"id":550}`)
	}))
	defer fallback.Close()
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"id":550}`) }))
	defer untrusted.Close()
	client, err := NewForFallbackTest("token", untrusted.URL, fallback.URL, controlledHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.TestAPI(context.Background()); err == nil || fallbackRequests != 0 {
		t.Fatalf("TLS failure incorrectly fell back: err=%v fallback=%d", err, fallbackRequests)
	}
}

func TestFallbackClassifierRejectsNonConnectNetworkAndCancellationErrors(t *testing.T) {
	if !isNetworkFailure(&networkRequestError{cause: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}}) {
		t.Fatal("dial failure did not qualify for official fallback")
	}
	if isNetworkFailure(&networkRequestError{cause: &net.OpError{Op: "remote error", Net: "tcp", Err: errors.New("tls handshake failed")}}) {
		t.Fatal("non-connect/TLS-shaped net.OpError qualified for fallback")
	}
	if isNetworkFailure(&networkRequestError{cause: context.Canceled}) {
		t.Fatal("caller cancellation qualified for fallback")
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBytes+1))
	}))
	defer server.Close()
	client, _ := NewForTest("token", server.URL, server.Client())
	if err := client.Test(context.Background()); err == nil {
		t.Fatal("oversized response was accepted")
	}
}
