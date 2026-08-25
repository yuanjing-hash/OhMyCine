package torznab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
)

func TestTorznabCapsSearchAndTorrentResolution(t *testing.T) {
	const apiKey = "server-only-api-key"
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("apikey") != apiKey {
			t.Error("API key was not supplied to Torznab")
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch request.URL.Query().Get("t") {
		case "caps":
			_, _ = writer.Write([]byte(`<caps><searching><search available="yes" /></searching></caps>`))
		case "search":
			if request.URL.Query().Get("q") != "Seven Samurai 1954" || request.URL.Query().Get("cat") != "2000" {
				t.Errorf("unexpected search query: %s", request.URL.RawQuery)
			}
			_, _ = fmt.Fprintf(writer, `<?xml version="1.0"?><rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><item><title>Seven.Samurai.1954.1080p</title><enclosure url="%s/api?t=download&amp;id=42&amp;apikey=feed-supplied" type="application/x-bittorrent" length="2048"/><torznab:attr name="seeders" value="8"/><torznab:attr name="peers" value="2"/><torznab:attr name="grabs" value="5"/></item></channel></rss>`, server.URL)
		case "download":
			_, _ = writer.Write([]byte("d4:infod4:name7:samuraiee"))
		default:
			http.Error(writer, "bad request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	adapter := NewForTest(server.Client(), server.URL)
	config := site.Config{BaseURL: server.URL, APIKey: apiKey}
	if health, err := adapter.Test(context.Background(), config); err != nil || health.Status != "online" {
		t.Fatalf("caps failed: %+v err=%v", health, err)
	}
	year := 1954
	page, err := adapter.Search(context.Background(), config, site.Query{Keyword: "Seven Samurai", MediaType: "movie", Year: &year, Page: 1})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("search failed: %+v err=%v", page, err)
	}
	item := page.Items[0]
	if item.Seeders == nil || *item.Seeders != 8 || item.Leechers == nil || *item.Leechers != 2 || item.Completed == nil || *item.Completed != 5 || item.SizeBytes != 2048 {
		t.Fatalf("Torznab attrs were not parsed: %+v", item)
	}
	source, err := adapter.ResolveSource(context.Background(), config, item.TorrentID)
	if err != nil || len(source.Torrent) == 0 {
		t.Fatalf("resolve failed: %+v err=%v", source, err)
	}
	encoded := fmt.Sprintf("%+v", page)
	if strings.Contains(encoded, apiKey) {
		t.Fatal("API key leaked into parsed result")
	}
}

func TestTorznabRejectsCrossOriginTorrentAndMissingAPIKey(t *testing.T) {
	adapter := New()
	if _, err := adapter.Test(context.Background(), site.Config{BaseURL: "https://indexer.example.test"}); err != site.ErrAuthentication {
		t.Fatalf("missing API key error = %v", err)
	}
	if safeTorznabURL("https://indexer.example.test", "https://other.example.test/api?t=download") {
		t.Fatal("cross-origin torrent accepted")
	}
}
