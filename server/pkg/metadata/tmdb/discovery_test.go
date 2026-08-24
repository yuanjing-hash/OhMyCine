package tmdb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverUsesAllowlistedEndpointAndBoundedProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/trending/movie/week" || r.URL.Query().Get("include_adult") != "false" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("language") != "zh-CN" || r.URL.Query().Get("region") != "CN" {
			t.Fatalf("request=%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"page":2,"total_pages":900,"results":[{"id":42,"title":"七武士","original_title":"七人の侍","release_date":"1954-04-26","overview":"简介","vote_average":8.5,"vote_count":123,"poster_path":"/poster.jpg","backdrop_path":"/backdrop.jpg"},{"id":0,"title":"跳过"}]}`)
	}))
	defer server.Close()
	client, err := NewForTest("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Discover(context.Background(), "trending-movie", 2, "zh-CN", "CN")
	if err != nil {
		t.Fatal(err)
	}
	if page.Page != 2 || page.TotalPages != 500 || len(page.Items) != 1 || page.Items[0].Year == nil || *page.Items[0].Year != 1954 {
		t.Fatalf("page=%+v", page)
	}
	image, err := client.ImageURL(page.Items[0].PosterPath, "w500")
	if err != nil || image != server.URL+"/w500/poster.jpg" {
		t.Fatalf("image=%q err=%v", image, err)
	}
	if _, err := client.Discover(context.Background(), "/arbitrary", 1, "", ""); err == nil {
		t.Fatal("arbitrary endpoint accepted")
	}
}

func TestRelatedUsesFixedEndpointAndLocalizedProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/42/recommendations" || r.URL.Query().Get("language") != "zh-CN" || r.URL.Query().Get("page") != "1" {
			t.Fatalf("request=%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"page":1,"total_pages":1,"results":[{"id":43,"title":"影武者","original_title":"Kagemusha","release_date":"1980-04-26","poster_path":"/poster.jpg"}]}`)
	}))
	defer server.Close()
	client, err := NewForTest("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Related(context.Background(), "movie", 42, "recommendations", 1, "zh_cn")
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "影武者" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, err := client.Related(context.Background(), "movie", 42, "credits", 1, "zh-CN"); err == nil {
		t.Fatal("arbitrary related endpoint accepted")
	}
}
