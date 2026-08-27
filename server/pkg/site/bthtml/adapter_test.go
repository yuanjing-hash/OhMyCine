package bthtml

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
)

type renderedStub struct {
	requests []site.RenderedFetchRequest
	page     site.RenderedPage
}

func (f *renderedStub) Fetch(_ context.Context, request site.RenderedFetchRequest) (site.RenderedPage, error) {
	f.requests = append(f.requests, request)
	return f.page, nil
}

func (*renderedStub) Health(context.Context) error { return nil }

func TestHTMLProfilesSearchAndResolveOnlySameHostDetails(t *testing.T) {
	profiles := []struct {
		name    string
		profile Profile
	}{{"1337x", X1337Profile()}, {"tpb", PirateBayProfile()}, {"ext", EXTToProfile()}, {"lime", LimeTorrentsProfile()}}
	for _, test := range profiles {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.Contains(request.URL.Path, "/torrent/") {
					_, _ = writer.Write([]byte(`<a href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=private">download</a>`))
					return
				}
				_, _ = fmt.Fprintf(writer, `<table><tr data-seeders="31" data-leechers="4" data-size="1.5 GiB" data-published="2026-08-25"><td><a href="%s/torrent/42/example">Example.Show.S01E01.1080p</a></td></tr></table><a rel="next" href="/next">Next</a>`, server.URL)
			}))
			defer server.Close()
			profile := test.profile
			profile.AllowedHosts = append(profile.AllowedHosts, requestHost(t, server.URL))
			adapter := NewForTest(test.name, profile, server.Client(), server.URL)
			adapter.profile.AllowedHosts = profile.AllowedHosts
			page, err := adapter.Search(context.Background(), site.Config{BaseURL: server.URL}, site.Query{Keyword: "Example", Page: 1})
			if err != nil || len(page.Items) != 1 || !page.HasNext || page.Items[0].Seeders == nil || *page.Items[0].Seeders != 31 {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			source, err := adapter.ResolveSource(context.Background(), site.Config{BaseURL: server.URL}, page.Items[0].TorrentID)
			if err != nil || source.Magnet == "" {
				t.Fatalf("source=%+v err=%v", source, err)
			}
			if _, ok := adapter.safeDetailURL(mustURL(t, server.URL), "https://evil.example/torrent/42"); ok {
				t.Fatal("foreign detail accepted")
			}
		})
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	value, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPirateBayDetailAllowsBoundedSameHostQuery(t *testing.T) {
	adapter := NewForProfile("thepiratebay", PirateBayProfile())
	base := mustURL(t, "https://thepiratebay.org")
	if detail, ok := adapter.safeDetailURL(base, "/description.php?id=42"); !ok || detail != "https://thepiratebay.org/description.php?id=42" {
		t.Fatalf("query detail=%q ok=%v", detail, ok)
	}
	for _, raw := range []string{"/description.php?", "/description.php?id=42#", "https://evil.example/description.php?id=42"} {
		if detail, ok := adapter.safeDetailURL(base, raw); ok {
			t.Fatalf("unsafe detail accepted: raw=%q detail=%q", raw, detail)
		}
	}
}

func TestLimeTorrentsRedirectsStayInsideExplicitHostSet(t *testing.T) {
	client, base, err := controlledClient(site.Config{BaseURL: "https://limetorrents.lol"}, LimeTorrentsProfile().AllowedHosts)
	if err != nil || base.Hostname() != "limetorrents.lol" {
		t.Fatalf("client=%+v base=%+v err=%v", client, base, err)
	}
	previous, _ := http.NewRequest(http.MethodGet, "https://limetorrents.lol/search/all/example/seeds/1/", nil)
	allowed, _ := http.NewRequest(http.MethodGet, "https://www.limetorrents.fun/search/all/example/seeds/1/", nil)
	if err := client.CheckRedirect(allowed, []*http.Request{previous}); err != nil {
		t.Fatalf("explicit .lol -> .fun redirect rejected: %v", err)
	}
	foreign, _ := http.NewRequest(http.MethodGet, "https://limetorrents.evil.example/search", nil)
	if err := client.CheckRedirect(foreign, []*http.Request{previous}); err == nil {
		t.Fatal("foreign redirect accepted")
	}
}

func TestChallengeProfilesUseControlledRenderedFetch(t *testing.T) {
	tests := []struct {
		kind       string
		profile    Profile
		baseURL    string
		pathPrefix string
	}{
		{kind: "1337x", profile: X1337Profile(), baseURL: "https://1337x.to", pathPrefix: "/search/Example/1/"},
		{kind: "extto", profile: EXTToProfile(), baseURL: "https://ext.to", pathPrefix: "/browse/"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			fetcher := &renderedStub{page: site.RenderedPage{StatusCode: http.StatusOK, FinalURL: test.baseURL, HTML: []byte(`<table><tr data-seeders="7"><td><a href="/torrent/42/example">Example.Show.S01E01.1080p</a></td></tr></table>`)}}
			adapter := NewForProfile(test.kind, test.profile)
			page, err := adapter.Search(context.Background(), site.Config{BaseURL: test.baseURL, BrowserEmulation: true, RenderedFetcher: fetcher}, site.Query{Keyword: "Example", Page: 1})
			if err != nil || len(page.Items) != 1 || len(fetcher.requests) != 1 {
				t.Fatalf("page=%+v requests=%+v err=%v", page, fetcher.requests, err)
			}
			request := fetcher.requests[0]
			if request.ProfileID != test.kind || !strings.HasPrefix(mustURL(t, request.URL).Path, test.pathPrefix) {
				t.Fatalf("request=%+v", request)
			}
		})
	}
}

func TestNonChallengeHTMLProfileDoesNotEnterRenderedRoute(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`<table><tr><td><a href="/torrent/42/example">Example.Show.S01E01.1080p</a></td></tr></table>`))
	}))
	defer server.Close()
	profile := LimeTorrentsProfile()
	profile.AllowedHosts = append(profile.AllowedHosts, requestHost(t, server.URL))
	fetcher := &renderedStub{}
	adapter := NewForTest("limetorrents", profile, server.Client(), server.URL)
	adapter.profile.AllowedHosts = profile.AllowedHosts
	page, err := adapter.Search(context.Background(), site.Config{BaseURL: server.URL, BrowserEmulation: true, RenderedFetcher: fetcher}, site.Query{Keyword: "Example", Page: 1})
	if err != nil || len(page.Items) != 1 || len(fetcher.requests) != 0 {
		t.Fatalf("page=%+v rendered=%d err=%v", page, len(fetcher.requests), err)
	}
}

func requestHost(t *testing.T, raw string) string {
	t.Helper()
	value := mustURL(t, raw)
	return value.Hostname()
}
