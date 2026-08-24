package douban

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yuanjing-hash/ohmycine/server/pkg/discovery"
)

func TestProviderParsesBoundedPublicWebResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/subject/recent_hot/movie" || r.URL.Query().Get("limit") != "20" || r.Header.Get("Referer") != "https://m.douban.com/" {
			t.Fatalf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"items":[{"id":"1292052","title":"肖申克的救赎","card_subtitle":"1994 / 美国","intro":"希望让人自由","cover_url":"https://img1.doubanio.com/view/photo/s_ratio_poster/public/p480747492.webp","rating":{"value":9.7,"count":3000000}},{"id":"bad","title":"跳过"},{"id":"42","title":"危险图片","cover_url":"https://evil.example/poster.jpg?token=secret"}],"total":100}`)
	}))
	defer server.Close()
	provider := NewForTest(server.URL, server.Client())
	section, err := provider.Fetch(context.Background(), discovery.Request{Section: "hot-movie", Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if section.Provider != "douban" || section.TotalPage != 5 || len(section.Items) != 2 {
		t.Fatalf("section=%+v", section)
	}
	if section.Items[0].DoubanID != "1292052" || section.Items[0].Year == nil || *section.Items[0].Year != 1994 || section.Items[0].PosterURL == "" {
		t.Fatalf("item=%+v", section.Items[0])
	}
	if section.Items[1].PosterURL != "" {
		t.Fatalf("unsafe poster accepted: %s", section.Items[1].PosterURL)
	}
}

func TestProviderRejectsOversizedAndInvalidSections(t *testing.T) {
	provider := NewForTest("https://m.douban.com", &http.Client{})
	if _, err := provider.Fetch(context.Background(), discovery.Request{Section: "arbitrary", Page: 1}); err == nil {
		t.Fatal("arbitrary section accepted")
	}
	if safeImageURL("http://img1.doubanio.com/a.jpg") != "" || safeImageURL("https://evil.example/a.jpg") != "" {
		t.Fatal("unsafe image host accepted")
	}
}

func TestCleanTextTruncatesChineseAtRuneBoundary(t *testing.T) {
	got := cleanText(strings.Repeat("影", 10), 7)
	if !utf8.ValidString(got) || got != strings.Repeat("影", 7) {
		t.Fatalf("truncated text=%q valid=%v", got, utf8.ValidString(got))
	}
}
