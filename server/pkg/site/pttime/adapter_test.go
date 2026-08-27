package pttime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAdapterAuthenticatedSearchAndDownload(t *testing.T) {
	var seenCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCookie = r.Header.Get("Cookie")
		switch r.URL.Path {
		case "/index.php":
			_, _ = w.Write(fixture(t, "index-authenticated.html"))
		case "/torrents.php":
			if got := r.URL.Query().Get("search"); got != "Seven Samurai 1954" {
				t.Errorf("search=%q", got)
			}
			if got := r.URL.Query().Get("page"); got != "1" {
				t.Errorf("page=%q", got)
			}
			_, _ = w.Write(fixture(t, "torrents.html"))
		case "/download.php":
			if got := r.URL.Query().Get("id"); got != "12345" {
				t.Errorf("id=%q", got)
			}
			if got := r.URL.Query().Get("passkey"); got != "safe-passkey" {
				t.Errorf("passkey=%q", got)
			}
			w.Header().Set("Content-Disposition", `attachment; filename="seven-samurai.torrent"`)
			_, _ = io.WriteString(w, "d4:infod4:name4:testee")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewForTest(server.Client())
	config := site.Config{BaseURL: server.URL, Cookie: "uid=1; token=secret", Passkey: "safe-passkey"}
	health, err := adapter.Test(context.Background(), config)
	if err != nil || health.Username != "测试用户" {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	year := 1954
	page, err := adapter.Search(context.Background(), config, site.Query{Keyword: "Seven Samurai", Year: &year, Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Skipped != 1 || !page.HasNext {
		t.Fatalf("page=%+v", page)
	}
	result := page.Items[0]
	if result.TorrentID != "12345" || result.SizeBytes != 13421772800 || result.Promotion != "free" || result.Seeders == nil || *result.Seeders != 42 || result.Leechers == nil || *result.Leechers != 3 || result.Completed == nil || *result.Completed != 108 {
		t.Fatalf("result=%+v", result)
	}
	if result.Published == nil || result.Published.Year() != 2026 || !strings.Contains(result.Quality, "1080p") {
		t.Fatalf("published/quality=%+v %q", result.Published, result.Quality)
	}
	body, filename, err := adapter.Download(context.Background(), config, "12345")
	if err != nil || filename != "seven-samurai.torrent" || len(body) == 0 || body[0] != 'd' {
		t.Fatalf("filename=%q body=%q err=%v", filename, body, err)
	}
	if seenCookie != config.Cookie {
		t.Fatalf("cookie header missing")
	}
}

func TestAdapterClassifiesLoginAndInvalidTorrent(t *testing.T) {
	t.Run("login", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fixture(t, "login.html")) }))
		defer server.Close()
		adapter := NewForTest(server.Client())
		if _, err := adapter.Test(context.Background(), site.Config{BaseURL: server.URL, Cookie: "expired=1"}); !errors.Is(err, site.ErrAuthentication) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("html instead of torrent", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<!doctype html><title>error</title>")
		}))
		defer server.Close()
		adapter := NewForTest(server.Client())
		if _, _, err := adapter.Download(context.Background(), site.Config{BaseURL: server.URL, Cookie: "ok=1"}, "123"); !errors.Is(err, site.ErrInvalidReply) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestAdapterRequiresPositiveAuthenticatedProof(t *testing.T) {
	t.Run("logout link", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `<html><body><a href="logout.php">退出</a></body></html>`)
		}))
		defer server.Close()
		health, err := NewForTest(server.Client()).Test(context.Background(), site.Config{BaseURL: server.URL, Cookie: "uid=1"})
		if err != nil || health.Status != "online" {
			t.Fatalf("health=%+v err=%v", health, err)
		}
	})
	t.Run("unproven landing", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `<html><body><!-- logout.php --><script>const path = "logout.php"</script><h1>NexusPHP Tracker</h1></body></html>`)
		}))
		defer server.Close()
		_, err := NewForTest(server.Client()).Test(context.Background(), site.Config{BaseURL: server.URL, Cookie: "expired=1"})
		if !errors.Is(err, site.ErrAuthentication) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestControlledClientRejectsCrossOriginRedirect(t *testing.T) {
	client, base, err := controlledClient(site.Config{BaseURL: "https://pt.example.test", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	request := &http.Request{URL: &url.URL{Scheme: "https", Host: "pt.example.test:8443"}}
	via := []*http.Request{{URL: base}}
	if err := client.CheckRedirect(request, via); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("cross-origin port redirect err=%v", err)
	}
}

func TestAdapterUsesFlareSolverrForPagesButNotTorrentDownload(t *testing.T) {
	pageRequests, directDownloads := 0, 0
	ptServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/download.php" {
			t.Errorf("unexpected direct page request %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		directDownloads++
		_, _ = io.WriteString(w, "d4:infod4:name4:testee")
	}))
	defer ptServer.Close()
	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageRequests++
		if r.URL.Path != "/v1" || r.Method != http.MethodPost {
			t.Errorf("render request=%s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "uid=1") || strings.Contains(string(raw), "token=secret") || strings.Contains(string(raw), "passkey-secret") || strings.Contains(string(raw), "cookies") || strings.Contains(string(raw), "passkey") {
			t.Errorf("PT credential reached solver: %s", raw)
		}
		var payload struct {
			Command string `json:"cmd"`
			URL     string `json:"url"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Error(err)
		}
		if payload.Command != "request.get" || !strings.Contains(payload.URL, "/torrents.php") {
			t.Errorf("payload=%+v", payload)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "solution": map[string]any{"status": 200, "response": string(fixture(t, "torrents.html"))}})
	}))
	defer flare.Close()

	adapter := NewForTest(ptServer.Client())
	config := site.Config{BaseURL: "https://pt.example.test", Cookie: "uid=1; token=secret", Passkey: "passkey-secret", BrowserEmulation: true, BrowserServiceURL: flare.URL, Timeout: 5 * time.Second}
	page, err := adapter.Search(context.Background(), config, site.Query{Keyword: "Seven Samurai", Page: 1})
	if err != nil || len(page.Items) != 1 || pageRequests != 1 {
		t.Fatalf("page=%+v rendered=%d err=%v", page, pageRequests, err)
	}
	downloadConfig := config
	downloadConfig.BaseURL = ptServer.URL
	if _, _, err := adapter.Download(context.Background(), downloadConfig, "12345"); err != nil {
		t.Fatal(err)
	}
	if pageRequests != 1 || directDownloads != 1 {
		t.Fatalf("rendered=%d directDownloads=%d", pageRequests, directDownloads)
	}
}

func TestFlareSolverrStatusIsMapped(t *testing.T) {
	flare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "solution": map[string]any{"status": 403, "response": "forbidden"}})
	}))
	defer flare.Close()
	_, _, err := renderedRequest(context.Background(), site.Config{BrowserServiceURL: flare.URL, Timeout: 5 * time.Second}, "https://pt.example.test/index.php", maxHTMLBytes)
	if !errors.Is(err, site.ErrAuthentication) {
		t.Fatalf("err=%v", err)
	}
}

func TestParserDoesNotDuplicateNestedNexusPHPRows(t *testing.T) {
	body := []byte(`<html><body><table><tr><td>电影</td><td><table><tr><td><a href="details.php?id=77" title="Nested.Movie.2026.1080p">Nested Movie</a></td></tr></table></td><td>1 GiB</td><td>9</td><td>1</td><td>20</td></tr></table></body></html>`)
	items, skipped, _, err := parseTorrentPage(body)
	if err != nil || skipped != 0 || len(items) != 1 || items[0].TorrentID != "77" {
		t.Fatalf("items=%+v skipped=%d err=%v", items, skipped, err)
	}
}

func TestParserSupportsSewerPTStandardMetadata(t *testing.T) {
	items, skipped, _, err := parseTorrentPage(fixture(t, "sewerpt-torrents.html"))
	if err != nil || skipped != 0 || len(items) != 1 {
		t.Fatalf("items=%+v skipped=%d err=%v", items, skipped, err)
	}
	result := items[0]
	if result.TorrentID != "8102" || result.Title != "Shichinin.no.Samurai.1954.1080p.BluRay.x265" || result.SizeBytes != 16106127360 || result.Promotion != "free" || result.Published == nil || result.Seeders == nil || *result.Seeders != 23 || result.Leechers == nil || *result.Leechers != 2 || result.Completed == nil || *result.Completed != 51 {
		t.Fatalf("result=%+v", result)
	}
}

func TestParserUsesPandaOuterRowAndAnchorTextFallback(t *testing.T) {
	items, skipped, _, err := parseTorrentPage(fixture(t, "panda-torrents.html"))
	if err != nil || skipped != 0 || len(items) != 1 {
		t.Fatalf("items=%+v skipped=%d err=%v", items, skipped, err)
	}
	result := items[0]
	if result.TorrentID != "9207" || result.Title != "Seven.Samurai.1954.2160p.UHD.BluRay.REMUX" || result.SizeBytes != 64424509440 || result.Promotion != "free" || result.Published == nil || result.Seeders == nil || *result.Seeders != 88 || result.Leechers == nil || *result.Leechers != 4 || result.Completed == nil || *result.Completed != 137 {
		t.Fatalf("result=%+v", result)
	}
}
