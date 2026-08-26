package btapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
)

func TestYTSAndEZTVAPISearchProducePrivateMagnetIdentities(t *testing.T) {
	tests := []struct {
		kind    string
		profile Profile
		body    string
		keyword string
	}{
		{"yts", YTSProfile(), `{"status":"ok","data":{"movie_count":2,"limit":1,"page_number":1,"movies":[{"title_long":"Seven Samurai (1954)","torrents":[{"hash":"0123456789abcdef0123456789abcdef01234567","quality":"1080p","type":"bluray","seeds":42,"peers":3,"size_bytes":1234,"date_uploaded_unix":1700000000}]}]}}`, "Seven Samurai"},
		{"eztv", EZTVProfile(), `{"torrents_count":200,"limit":100,"page":1,"torrents":[{"title":"Example Show S01E01 1080p","hash":"0123456789abcdef0123456789abcdef01234567","size_bytes":4321,"seeds":20,"peers":2,"date_released_unix":1700000000}]}`, "Example Show"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			adapter := NewForTest(test.kind, test.profile, server.Client(), server.URL)
			adapter.profile.AllowedHosts = append(adapter.profile.AllowedHosts, requestHost(t, server.URL))
			page, err := adapter.Search(context.Background(), site.Config{BaseURL: server.URL}, site.Query{Keyword: test.keyword, Page: 1})
			if err != nil || len(page.Items) != 1 || page.Items[0].TorrentID == "" || page.Items[0].Seeders == nil {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			source, err := adapter.ResolveSource(context.Background(), site.Config{}, page.Items[0].TorrentID)
			if err != nil || source.Magnet == "" || source.Torrent != nil {
				t.Fatalf("source=%+v err=%v", source, err)
			}
		})
	}
}

func requestHost(t *testing.T, raw string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request.URL.Hostname()
}
