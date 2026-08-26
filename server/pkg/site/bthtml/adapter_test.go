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
func requestHost(t *testing.T, raw string) string {
	t.Helper()
	value := mustURL(t, raw)
	return value.Hostname()
}
