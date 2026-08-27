package site

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type renderedFetcherStub struct {
	page    RenderedPage
	err     error
	request RenderedFetchRequest
	calls   int
}

func (f *renderedFetcherStub) Fetch(_ context.Context, request RenderedFetchRequest) (RenderedPage, error) {
	f.calls++
	f.request = request
	return f.page, f.err
}

func (*renderedFetcherStub) Health(context.Context) error { return nil }

func TestCloakBrowserCompanionAcceptsOnlyLoopback(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:9222", "http://localhost:9222", "http://[::1]:9222"} {
		if _, err := NewCloakBrowserFetcher(raw); err != nil {
			t.Fatalf("loopback %q rejected: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://0.0.0.0:9222", "https://cloak.example.test", "http://localhost.evil.example:9222", "file:///tmp/cloak"} {
		if _, err := NewCloakBrowserFetcher(raw); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("non-loopback %q err=%v", raw, err)
		}
	}
}

func TestPublicRenderedFetchRejectsUnregisteredTargetsBeforeProvider(t *testing.T) {
	stub := &renderedFetcherStub{}
	cases := []RenderedFetchRequest{
		{ProfileID: "1337x", URL: "http://1337x.to/search/test/1/", AllowedHosts: []string{"1337x.to"}, MaxBytes: 1024},
		{ProfileID: "1337x", URL: "https://127.0.0.1/search/test/1/", AllowedHosts: []string{"1337x.to"}, MaxBytes: 1024},
		{ProfileID: "1337x", URL: "https://evil.example/search/test/1/", AllowedHosts: []string{"1337x.to"}, MaxBytes: 1024},
		{ProfileID: "private-pt", URL: "https://pt.example.test/torrents.php", AllowedHosts: []string{"pt.example.test"}, MaxBytes: 1024},
		{ProfileID: "1337x", URL: "https://1337x.to:444/search/test/1/", AllowedHosts: []string{"1337x.to"}, MaxBytes: 1024},
	}
	for _, request := range cases {
		if _, err := FetchRendered(context.Background(), Config{RenderedFetcher: stub}, request); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("request=%+v err=%v", request, err)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("provider called %d times for rejected targets", stub.calls)
	}
}

func TestFlareSolverrFetchIsBoundedAndCredentialFree(t *testing.T) {
	const secret = "pt-cookie-secret"
	var payload map[string]any
	flare := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1" || request.Method != http.MethodPost {
			t.Errorf("request=%s %s", request.Method, request.URL.Path)
		}
		raw, _ := io.ReadAll(request.Body)
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "cookie") || strings.Contains(string(raw), "passkey") {
			t.Fatalf("credential-like field reached solver: %s", raw)
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status": "ok", "solution": map[string]any{
			"status": 200, "url": "https://1337x.to/search/test/1/", "response": "<html>ok</html>",
		}})
	}))
	defer flare.Close()

	page, err := FetchRendered(context.Background(), Config{Cookie: secret, Passkey: secret, BrowserServiceURL: flare.URL}, RenderedFetchRequest{
		ProfileID: "1337x", URL: "https://1337x.to/search/test/1/", AllowedHosts: []string{"1337x.to"}, Timeout: 5 * time.Second, MaxBytes: 1024,
	})
	if err != nil || string(page.HTML) != "<html>ok</html>" || payload["cmd"] != "request.get" {
		t.Fatalf("page=%+v payload=%+v err=%v", page, payload, err)
	}
}

func TestRenderedFetchFallsBackFromUnavailableCloakToFlare(t *testing.T) {
	cloak := &renderedFetcherStub{err: ErrUnavailable}
	flareCalls := 0
	flare := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		flareCalls++
		_ = json.NewEncoder(writer).Encode(map[string]any{"status": "ok", "solution": map[string]any{
			"status": 200, "url": "https://ext.to/browse/?q=test&page=1", "response": "<html>fallback</html>",
		}})
	}))
	defer flare.Close()

	page, err := FetchRendered(context.Background(), Config{RenderedFetcher: cloak, BrowserServiceURL: flare.URL}, RenderedFetchRequest{
		ProfileID: "extto", URL: "https://ext.to/browse/?q=test&page=1", AllowedHosts: []string{"ext.to"}, MaxBytes: 1024,
	})
	if err != nil || string(page.HTML) != "<html>fallback</html>" || cloak.calls != 1 || flareCalls != 1 {
		t.Fatalf("page=%+v cloak=%d flare=%d err=%v", page, cloak.calls, flareCalls, err)
	}
}

func TestRenderedFetchRejectsForeignFinalURLAndOversizedHTML(t *testing.T) {
	t.Run("foreign final URL", func(t *testing.T) {
		flare := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(writer).Encode(map[string]any{"status": "ok", "solution": map[string]any{
				"status": 200, "url": "https://evil.example/result", "response": "<html>no</html>",
			}})
		}))
		defer flare.Close()
		fetcher, err := NewFlareSolverrFetcher(flare.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fetcher.Fetch(context.Background(), RenderedFetchRequest{ProfileID: "legacy", URL: "https://1337x.to/search/test/1/", AllowedHosts: []string{"1337x.to"}, MaxBytes: 1024})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("oversized HTML", func(t *testing.T) {
		flare := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(writer).Encode(map[string]any{"status": "ok", "solution": map[string]any{
				"status": 200, "url": "https://1337x.to/search/test/1/", "response": strings.Repeat("x", 33),
			}})
		}))
		defer flare.Close()
		fetcher, err := NewFlareSolverrFetcher(flare.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fetcher.Fetch(context.Background(), RenderedFetchRequest{ProfileID: "legacy", URL: "https://1337x.to/search/test/1/", AllowedHosts: []string{"1337x.to"}, MaxBytes: 32})
		if !errors.Is(err, ErrInvalidReply) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestCloakBrowserFetchUsesNarrowCredentialFreeRequest(t *testing.T) {
	const secret = "never-forward-this"
	companion := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/render" {
			t.Errorf("path=%s", request.URL.Path)
		}
		raw, _ := io.ReadAll(request.Body)
		if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "cookie") || strings.Contains(string(raw), "passkey") {
			t.Fatalf("credential-like field reached companion: %s", raw)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"status": 200, "html": "<html>cloak</html>", "final_url": "https://1337x.to/search/test/1/"})
	}))
	defer companion.Close()
	fetcher, err := NewCloakBrowserFetcher(companion.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, err := FetchRendered(context.Background(), Config{Cookie: secret, Passkey: secret, RenderedFetcher: fetcher}, RenderedFetchRequest{
		ProfileID: "1337x", URL: "https://1337x.to/search/test/1/", AllowedHosts: []string{"1337x.to"}, MaxBytes: 1024,
	})
	if err != nil || string(page.HTML) != "<html>cloak</html>" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}
