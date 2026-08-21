package qbittorrent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

func TestClientSubmitTelemetryAndSafeControls(t *testing.T) {
	var mu sync.Mutex
	added := false
	stop, start, deleted := false, false, false
	ratio, seeded, uploaded := 1.25, int64(3600), int64(125)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/login" && r.Header.Get("Cookie") != "SID=session-value" {
			http.Error(w, "missing session", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_ = r.ParseForm()
			if r.Form.Get("username") != "admin" || r.Form.Get("password") != "secret" {
				http.Error(w, "bad auth", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session-value"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.4")
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("tags") == "omc-test" && r.FormValue("urls") != "magnet:?xt=urn:btih:abc" {
				t.Fatalf("unexpected URL source: %#v", r.MultipartForm.Value)
			}
			if r.FormValue("tags") == "omc-torrent" {
				files := r.MultipartForm.File["torrents"]
				if len(files) != 1 || files[0].Filename != "movie.torrent" {
					t.Fatalf("unexpected torrent upload: %#v", files)
				}
			}
			if r.FormValue("savepath") != `D:\Downloads` {
				t.Fatalf("unexpected add form: %#v", r.MultipartForm.Value)
			}
			mu.Lock()
			added = true
			mu.Unlock()
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/info":
			mu.Lock()
			ready := added
			mu.Unlock()
			if !ready {
				_, _ = io.WriteString(w, "[]")
				return
			}
			_ = json.NewEncoder(w).Encode([]torrentInfo{{Hash: "hash-1", Name: "Movie", State: "stalledUP", Progress: .5, Downloaded: 50, TotalSize: 100, DL: 10, UL: 2, ETA: 5, Ratio: &ratio, SeedTime: &seeded, Uploaded: &uploaded}})
		case "/api/v2/torrents/pause", "/api/v2/torrents/resume":
			http.NotFound(w, r)
		case "/api/v2/torrents/stop":
			stop = true
		case "/api/v2/torrents/start":
			start = true
		case "/api/v2/torrents/delete":
			_ = r.ParseForm()
			if r.Form.Get("deleteFiles") != "true" {
				t.Fatal("destructive cancel did not request provider data deletion")
			}
			deleted = true
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(downloader.Config{BaseURL: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.Test(context.Background())
	if err != nil || health.Version != "v5.0.4" {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	task, err := client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:abc"}, SavePath: `D:\Downloads`, Tag: "omc-test"})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "hash-1" || task.Progress == nil || *task.Progress != 50 || task.DownloadSpeed == nil || *task.DownloadSpeed != 10 || task.Ratio == nil || *task.Ratio != 1.25 || task.SeededSeconds == nil || *task.SeededSeconds != 3600 || task.UploadedBytes == nil || *task.UploadedBytes != 125 || !task.Seeding {
		t.Fatalf("task=%+v", task)
	}
	if _, err := client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceTorrent, Filename: "movie.torrent", Torrent: []byte("d4:infoe")}, SavePath: `D:\Downloads`, Tag: "omc-torrent"}); err != nil {
		t.Fatal(err)
	}
	if err := client.Pause(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := client.Resume(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := client.Cancel(context.Background(), task.ID, true); err != nil {
		t.Fatal(err)
	}
	if !stop || !start || !deleted {
		t.Fatalf("controls stop=%v start=%v delete=%v", stop, start, deleted)
	}
}

func TestClientRejectsCredentialedOrPathBaseURL(t *testing.T) {
	for _, value := range []string{"file:///tmp/qbit", "http://user:pass@example.test", "http://example.test/ui", "http://example.test?token=secret", "http://example.test/?", "http://example.test/#"} {
		if _, err := New(downloader.Config{BaseURL: value}); err == nil {
			t.Fatalf("accepted %s", strings.Split(value, "?")[0])
		}
	}
}

func TestClientAcceptsModernNoContentLoginAndPortScopedCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_7864", Value: "modern-session"})
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/app/version":
			if r.Header.Get("Cookie") != "QBT_SID_7864=modern-session" {
				http.Error(w, "missing modern session", http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, "v5.2.3")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := New(downloader.Config{BaseURL: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.Test(context.Background())
	if err != nil || health.Version != "v5.2.3" {
		t.Fatalf("health=%+v err=%v", health, err)
	}
}

func TestClientRejectsSuccessfulLoginWithoutRecognizedSessionCookie(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		cookieName string
		cookie     string
	}{
		{name: "legacy missing cookie", status: http.StatusOK, body: "Ok."},
		{name: "legacy arbitrary cookie", status: http.StatusOK, body: "Ok.", cookieName: "session", cookie: "unexpected"},
		{name: "modern missing cookie", status: http.StatusNoContent},
		{name: "modern malformed port cookie", status: http.StatusNoContent, cookieName: "QBT_SID_not-a-port", cookie: "unexpected"},
		{name: "modern empty cookie", status: http.StatusNoContent, cookieName: "QBT_SID_7864"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.cookieName != "" {
					http.SetCookie(w, &http.Cookie{Name: test.cookieName, Value: test.cookie})
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()

			client, err := New(downloader.Config{BaseURL: server.URL, Username: "admin", Password: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Test(context.Background()); err == nil {
				t.Fatal("accepted a successful login response without a recognized qBittorrent session cookie")
			}
		})
	}
}

func TestClientClassifiesLoginFailuresWithoutExposingResponseBody(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		code      string
		retryable bool
	}{
		{name: "credentials rejected", status: http.StatusOK, body: "Fails. password=do-not-expose", code: "downloader_auth_failed"},
		{name: "forbidden", status: http.StatusForbidden, body: "blocked", code: "downloader_auth_failed"},
		{name: "wrong web api endpoint", status: http.StatusNotFound, body: "missing", code: "downloader_request_failed"},
		{name: "temporarily unavailable", status: http.StatusServiceUnavailable, body: "maintenance", code: "downloader_unavailable", retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := New(downloader.Config{BaseURL: server.URL, Username: "admin", Password: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Test(context.Background())
			code, retryable := downloader.ErrorInfo(err)
			if code != test.code || retryable != test.retryable {
				t.Fatalf("code=%q retryable=%v err=%v", code, retryable, err)
			}
			if strings.Contains(err.Error(), test.body) {
				t.Fatal("login failure exposed the provider response body")
			}
		})
	}
}

func TestClientRejectsInvalidOrUnboundedSubmitBeforeLogin(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client, err := New(downloader.Config{BaseURL: server.URL, Username: "admin", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []downloader.SubmitRequest{
		{Source: downloader.Source{Kind: downloader.SourceURL, URL: "file:///private/media"}, SavePath: `D:\Downloads`, Tag: "omc-test"},
		{Source: downloader.Source{Kind: downloader.SourceURL, URL: "https://example.test/" + strings.Repeat("a", maxSourceURLBytes)}, SavePath: `D:\Downloads`, Tag: "omc-test"},
		{Source: downloader.Source{Kind: downloader.SourceTorrent, Filename: "movie.txt", Torrent: []byte("d1:ae")}, SavePath: `D:\Downloads`, Tag: "omc-test"},
		{Source: downloader.Source{Kind: downloader.SourceTorrent, Filename: "movie.torrent", Torrent: make([]byte, maxTorrentBytes+1)}, SavePath: `D:\Downloads`, Tag: "omc-test"},
		{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:abc"}, Tag: "omc-test"},
	}
	for index, request := range tests {
		if _, err := client.Submit(context.Background(), request); err == nil {
			t.Fatalf("invalid request %d was accepted", index)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid submissions made %d network request(s)", requests)
	}
}

func TestClientModernAddMetadataManifestCategoriesAndTagAdoption(t *testing.T) {
	const modernHash = "0123456789abcdef0123456789abcdef01234567"
	added := false
	addCount := 0
	setCategory := false
	setLocation := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_7864", Value: "session"})
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/torrents/info":
			if !added {
				_, _ = io.WriteString(w, "[]")
				return
			}
			_ = json.NewEncoder(w).Encode([]torrentInfo{{Hash: modernHash, Name: "Example.Movie.2026", State: "stoppedDL"}})
		case "/api/v2/torrents/add":
			addCount++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if r.FormValue("stopCondition") != "MetadataReceived" {
				t.Fatalf("stopCondition=%q", r.FormValue("stopCondition"))
			}
			added = true
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"success_count":1,"failure_count":0,"pending_count":0,"added_torrent_ids":["`+modernHash+`"]}`)
		case "/api/v2/torrents/files":
			_, _ = io.WriteString(w, `[{"name":"Example.Movie.2026/Example.Movie.2026.mkv","size":1234}]`)
		case "/api/v2/torrents/categories":
			_, _ = io.WriteString(w, `{"电影":{"name":"电影","savePath":"D:\\Staging\\电影"}}`)
		case "/api/v2/torrents/setCategory":
			_ = r.ParseForm()
			setCategory = r.Form.Get("hashes") == modernHash && r.Form.Get("category") == "电影"
		case "/api/v2/torrents/setLocation":
			_ = r.ParseForm()
			setLocation = r.Form.Get("hashes") == modernHash && r.Form.Get("location") == `D:\Staging\电影`
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(downloader.Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	request := downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:modern"}, SavePath: `D:\Staging`, Tag: "omc-stable", MetadataOnly: true}
	task, err := client.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != modernHash {
		t.Fatalf("task=%+v", task)
	}
	if _, err := client.Submit(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if addCount != 1 {
		t.Fatalf("duplicate add count=%d", addCount)
	}
	manifest, err := client.Manifest(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Complete || len(manifest.Files) != 1 || manifest.Files[0].RelativePath != "Example.Movie.2026/Example.Movie.2026.mkv" {
		t.Fatalf("manifest=%+v", manifest)
	}
	categories, err := client.Categories(context.Background())
	if err != nil || len(categories) != 1 || categories[0].Name != "电影" {
		t.Fatalf("categories=%+v err=%v", categories, err)
	}
	if err := client.SetCategory(context.Background(), task.ID, "电影", `D:\Staging\电影`); err != nil {
		t.Fatal(err)
	}
	if !setCategory || !setLocation {
		t.Fatalf("category route incomplete: category=%v location=%v", setCategory, setLocation)
	}
}

func TestClientFallsBackWhenMetadataStopConditionIsUnsupported(t *testing.T) {
	addCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/info":
			if addCount < 2 {
				_, _ = io.WriteString(w, "[]")
			} else {
				_ = json.NewEncoder(w).Encode([]torrentInfo{{Hash: "legacy-hash", State: "metaDL"}})
			}
		case "/api/v2/torrents/add":
			addCount++
			_ = r.ParseMultipartForm(1 << 20)
			if r.FormValue("stopCondition") != "" {
				http.Error(w, "unsupported", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(downloader.Config{BaseURL: server.URL})
	if _, err := client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:legacy"}, SavePath: `D:\Staging`, Tag: "omc-legacy", MetadataOnly: true}); err != nil {
		t.Fatal(err)
	}
	if addCount != 2 {
		t.Fatalf("add count=%d", addCount)
	}
}

func TestClientFallsBackWhenLegacyAddReturnsFailsForStopCondition(t *testing.T) {
	addCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/info":
			if addCount < 2 {
				_, _ = io.WriteString(w, "[]")
			} else {
				_ = json.NewEncoder(w).Encode([]torrentInfo{{Hash: "legacy-hash", State: "metaDL"}})
			}
		case "/api/v2/torrents/add":
			addCount++
			_ = r.ParseMultipartForm(1 << 20)
			if r.FormValue("stopCondition") != "" {
				_, _ = io.WriteString(w, "Fails.")
				return
			}
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(downloader.Config{BaseURL: server.URL})
	if _, err := client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:legacy"}, SavePath: `D:\Staging`, Tag: "omc-legacy", MetadataOnly: true}); err != nil {
		t.Fatal(err)
	}
	if addCount != 2 {
		t.Fatalf("add count=%d", addCount)
	}
}

func TestClientDoesNotTrustMalformedModernAddedTorrentIDs(t *testing.T) {
	tests := []string{
		`{"success_count":1,"failure_count":0,"pending_count":0,"added_torrent_ids":["not-a-hash"]}`,
		`{"success_count":1,"failure_count":0,"pending_count":0,"added_torrent_ids":["0123456789abcdef0123456789abcdef01234567","fedcba9876543210fedcba9876543210fedcba98"]}`,
		`{"success_count":2,"failure_count":0,"pending_count":0,"added_torrent_ids":["0123456789abcdef0123456789abcdef01234567"]}`,
	}
	for _, response := range tests {
		t.Run(response, func(t *testing.T) {
			added := false
			requestedByHash := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session"})
					_, _ = io.WriteString(w, "Ok.")
				case "/api/v2/torrents/info":
					if r.URL.Query().Get("hashes") != "" {
						requestedByHash = true
					}
					if added && r.URL.Query().Get("tag") == "omc-safe" {
						_ = json.NewEncoder(w).Encode([]torrentInfo{{Hash: "adopted-hash", State: "metaDL"}})
					} else {
						_, _ = io.WriteString(w, "[]")
					}
				case "/api/v2/torrents/add":
					added = true
					_, _ = io.WriteString(w, response)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, _ := New(downloader.Config{BaseURL: server.URL})
			task, err := client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:safe"}, SavePath: `D:\Staging`, Tag: "omc-safe"})
			if err != nil || task.ID != "adopted-hash" {
				t.Fatalf("task=%+v err=%v", task, err)
			}
			if requestedByHash {
				t.Fatal("malformed modern response ID was queried before stable-tag reconciliation")
			}
		})
	}
}

func TestClientRejectsUnsafeManifestPaths(t *testing.T) {
	unsafe := []string{"../movie.mkv", "folder/../movie.mkv", "/absolute/movie.mkv", `C:\absolute\movie.mkv`, `\\server\share\movie.mkv`, "folder/\x00movie.mkv", "folder//movie.mkv"}
	for _, name := range unsafe {
		t.Run(strings.ReplaceAll(name, "\\", "_"), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session"})
					_, _ = io.WriteString(w, "Ok.")
				case "/api/v2/torrents/info":
					_ = json.NewEncoder(w).Encode([]torrentInfo{{Hash: "hash", Name: "Movie", State: "stoppedDL"}})
				case "/api/v2/torrents/files":
					_ = json.NewEncoder(w).Encode([]map[string]any{{"name": name, "size": 1}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, _ := New(downloader.Config{BaseURL: server.URL})
			if _, err := client.Manifest(context.Background(), "hash"); err == nil {
				t.Fatalf("unsafe path %q was accepted", name)
			}
		})
	}
}

func TestClientRetryAdoptsTaskCreatedByEarlierFailedLocalRecord(t *testing.T) {
	addCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "session"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/info":
			if r.URL.Query().Get("tag") != "omc-old-failed-task" {
				t.Fatalf("retry did not reconcile by stable tag: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]torrentInfo{{Hash: "existing-provider-hash", Name: "Existing", State: "downloading"}})
		case "/api/v2/torrents/add":
			addCount++
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(downloader.Config{BaseURL: server.URL})
	task, err := client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:old"}, SavePath: `D:\Staging`, Tag: "omc-old-failed-task", MetadataOnly: true})
	if err != nil || task.ID != "existing-provider-hash" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	if addCount != 0 {
		t.Fatalf("retry duplicated provider submission %d time(s)", addCount)
	}
}
