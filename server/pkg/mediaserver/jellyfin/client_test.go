package jellyfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdapterUsesJellyfinCompatibleManagementContractWithPrefix(t *testing.T) {
	var refreshPath, refreshQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "jellyfin-key" {
			t.Fatal("Jellyfin management credential missing")
		}
		switch r.URL.Path {
		case "/jellyfin/System/Info":
			_, _ = w.Write([]byte(`{"Id":"jellyfin-system","ServerName":"Jellyfin","Version":"10.10"}`))
		case "/jellyfin/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[{"ItemId":"stable-library-id","Name":"电视节目","CollectionType":"tvshows"}]`))
		case "/jellyfin/Items/stable-library-id/Refresh":
			refreshPath, refreshQuery = r.URL.Path, r.URL.RawQuery
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(Config{Endpoint: server.URL + "/jellyfin", APIKey: "jellyfin-key"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.Probe(context.Background())
	if err != nil || info.ID != "jellyfin-system" || info.Name != "Jellyfin" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	libraries, err := client.ListLibraries(context.Background())
	if err != nil || len(libraries) != 1 || libraries[0].ID != "stable-library-id" || libraries[0].ContentType != "tvshows" {
		t.Fatalf("libraries=%+v err=%v", libraries, err)
	}
	if err := client.RefreshLibrary(context.Background(), libraries[0].ID); err != nil {
		t.Fatal(err)
	}
	if refreshPath == "" || !strings.Contains(refreshQuery, "Recursive=true") {
		t.Fatalf("refresh path=%q query=%q", refreshPath, refreshQuery)
	}
}

func TestAdapterRejectsRedirectAndUnsafeLibraryIdentity(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("redirect target must not be reached")
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()

	client, err := New(Config{Endpoint: server.URL, APIKey: "jellyfin-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListLibraries(context.Background()); err == nil {
		t.Fatal("redirected Jellyfin library response was accepted")
	}
	if err := client.RefreshLibrary(context.Background(), "library/id"); err == nil {
		t.Fatal("unsafe Jellyfin library identity was accepted")
	}
}
