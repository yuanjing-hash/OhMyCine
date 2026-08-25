package btrss

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
)

func TestNyaaRSSSearchAndResolveTorrent(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			if request.URL.Query().Get("page") != "rss" || request.URL.Query().Get("q") != "Samurai" || request.URL.Query().Get("c") != "0_0" || request.URL.Query().Get("f") != "0" {
				t.Errorf("unexpected Nyaa query: %s", request.URL.RawQuery)
			}
			writer.Header().Set("Content-Type", "application/rss+xml")
			_, _ = fmt.Fprintf(writer, `<?xml version="1.0"?><rss xmlns:nyaa="https://nyaa.si/xmlns/nyaa"><channel><item><title>Seven.Samurai.1954.1080p</title><link>%s/download/123.torrent</link><pubDate>Mon, 25 Aug 2025 08:00:00 +0000</pubDate><nyaa:size>1.5 GiB</nyaa:size><nyaa:seeders>21</nyaa:seeders><nyaa:leechers>3</nyaa:leechers><nyaa:downloads>99</nyaa:downloads></item></channel></rss>`, server.URL)
		case "/download/123.torrent":
			writer.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = writer.Write([]byte("d4:infod4:name7:samuraiee"))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	adapter := NewForTest("nyaa", NyaaProfile(), server.Client(), server.URL)
	page, err := adapter.Search(context.Background(), site.Config{BaseURL: server.URL}, site.Query{Keyword: "Samurai", Page: 1})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("search failed: items=%+v err=%v", page.Items, err)
	}
	item := page.Items[0]
	if item.SizeBytes != 1610612736 || item.Seeders == nil || *item.Seeders != 21 || item.Leechers == nil || *item.Leechers != 3 || item.Completed == nil || *item.Completed != 99 || item.Published == nil {
		t.Fatalf("Nyaa fields were not parsed: %+v", item)
	}
	source, err := adapter.ResolveSource(context.Background(), site.Config{BaseURL: server.URL}, item.TorrentID)
	if err != nil || len(source.Torrent) == 0 || source.Filename != "123.torrent" {
		t.Fatalf("torrent resolution failed: %+v err=%v", source, err)
	}
}

func TestBuiltInProfilesGenerateExpectedQueryAndConstrainSources(t *testing.T) {
	tests := []struct {
		name, kind, path, parameter, prefix string
		profile                             Profile
	}{
		{"nyaa", "nyaa", "/", "q", "/download/", NyaaProfile()},
		{"animetosho", "animetosho", "/rss2", "q", "/storage/", AnimeToshoProfile()},
		{"tokyo", "tokyotoshokan", "/rss.php", "terms", "/download.php", TokyoToshokanProfile()},
		{"mikan", "mikan", "/RSS/Search", "searchstr", "/Download/", MikanProfile()},
		{"anidex", "anidex", "/rss/", "q", "/torrent/", AniDexProfile()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path || request.URL.Query().Get(test.parameter) != "probe" {
					t.Errorf("unexpected request %s?%s", request.URL.Path, request.URL.RawQuery)
				}
				_, _ = fmt.Fprintf(writer, `<rss><channel><item><title>probe</title><enclosure url="%s%s123.torrent" type="application/x-bittorrent" /></item></channel></rss>`, server.URL, test.prefix)
			}))
			defer server.Close()
			adapter := NewForTest(test.kind, test.profile, server.Client(), server.URL)
			page, err := adapter.Search(context.Background(), site.Config{BaseURL: server.URL}, site.Query{Keyword: "probe", Page: 1})
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("profile search failed: %+v err=%v", page, err)
			}
			foreign := "https://evil.example/download/123.torrent"
			if _, ok := adapter.sourceIdentity(server.URL, foreign); ok {
				t.Fatal("foreign torrent host was accepted")
			}
			if adapter.safeTorrentURL("https://nyaa.si", "https://nyaa.si:8443/download/123.torrent") {
				t.Fatal("allowlisted host with an unapproved port was accepted")
			}
		})
	}
}

func TestRSSCompatibilityElementsAndMagnetNormalization(t *testing.T) {
	feed := `<?xml version="1.0"?><rss><channel><item><title>AnimeTosho torrent</title><torrent_url>https://animetosho.org/storage/a.torrent</torrent_url></item><item><title>AnimeTosho magnet</title><magnet_uri>magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&amp;dn=private</magnet_uri></item></channel></rss>`
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { _, _ = writer.Write([]byte(feed)) }))
	defer server.Close()
	profile := AnimeToshoProfile()
	adapter := NewForTest("animetosho", profile, server.Client(), server.URL)
	page, err := adapter.Search(context.Background(), site.Config{BaseURL: server.URL, Timeout: 3 * time.Second}, site.Query{Keyword: "anime", Page: 1})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("compat feed failed: %+v err=%v", page, err)
	}
	torrentKind, _, torrentOK := DecodeIdentity(page.Items[0].TorrentID)
	if !torrentOK || torrentKind != "torrent" {
		t.Fatalf("torrent_url was not parsed: %+v", page.Items[0])
	}
	kind, value, ok := DecodeIdentity(page.Items[1].TorrentID)
	if !ok || kind != "magnet" || value != "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("magnet was not normalized: %q %q", kind, value)
	}
	if _, ok := NormalizeMagnet("magnet:?xt=urn:btih:bad"); ok {
		t.Fatal("invalid infohash accepted")
	}
	if _, ok := NormalizeMagnet((&url.URL{Scheme: "https", Host: "example.test"}).String()); ok {
		t.Fatal("non-magnet accepted")
	}
	if strings.Contains(page.Items[0].TorrentID, "private") {
		t.Fatal("private display name leaked into normalized identity")
	}
}
