package tmdb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchMediaProjectsMovieAndTVAndDropsPeople(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/multi" || r.URL.Query().Get("query") != "三体" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("include_adult") != "false" {
			t.Fatalf("request=%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"page":2,"total_pages":900,"results":[{"id":1,"media_type":"tv","name":"三体","original_name":"Three-Body","first_air_date":"2023-01-15","poster_path":"/tv.jpg"},{"id":2,"media_type":"movie","title":"三体","original_title":"The Three-Body Problem","release_date":"2028-01-01","poster_path":"/movie.jpg"},{"id":3,"media_type":"person","name":"不应返回"}]}`)
	}))
	defer server.Close()
	client, err := NewForTest("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.SearchMedia(context.Background(), "  三体  ", "all", 2, "zh-CN", "CN")
	if err != nil {
		t.Fatal(err)
	}
	if page.Page != 2 || page.TotalPages != MaxMediaSearchPage || len(page.Items) != 2 || page.Items[0].MediaType != "tv" || page.Items[1].MediaType != "movie" {
		t.Fatalf("page=%+v", page)
	}
}

func TestSearchMediaUsesTypedEndpointAndRejectsInvalidInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/movie" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"page":1,"total_pages":1,"results":[]}`)
	}))
	defer server.Close()
	client, err := NewForTest("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SearchMedia(context.Background(), "Alien", "movie", 1, "en-US", "US"); err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		query, kind string
		page        int
	}{{"", "all", 1}, {"Alien", "person", 1}, {"Alien", "all", 0}} {
		if _, err := client.SearchMedia(context.Background(), input.query, input.kind, input.page, "", ""); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
}
