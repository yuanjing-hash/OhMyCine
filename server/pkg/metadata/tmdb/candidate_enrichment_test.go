package tmdb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchCandidatesKeepsInternalRankingEvidenceOutOfJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/tv" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"results":[{"id":5,"name":"大明王朝1566","original_name":"Ming Dynasty in 1566","original_language":"zh","first_air_date":"2007-01-08","popularity":31.5,"vote_count":880}]}`)
	}))
	defer server.Close()
	client, err := NewForTest("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	year := 2007
	items, err := client.SearchCandidates(context.Background(), "tv", "Ming Dynasty in 1566", &year, "zh-CN", "CN", 10)
	if err != nil || len(items) != 1 || items[0].Popularity != 31.5 || items[0].VoteCount != 880 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	items[0].AlternativeTitles = []string{"内部别名"}
	items[0].SeasonCount = 1
	items[0].SeasonYears = map[int]int{1: 2007}
	payload, err := json.Marshal(items[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"内部别名", "SeasonCount", "season_count", "SeasonYears", "season_years", "Popularity", "popularity", "VoteCount", "vote_count"} {
		if strings.Contains(string(payload), private) {
			t.Fatalf("internal ranking evidence leaked: %s", payload)
		}
	}
}

func TestEnrichCandidatesAddsMovieAndTVAliasesWithinRequestBudget(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("append_to_response") != "alternative_titles,translations" {
			t.Fatalf("append_to_response=%q", r.URL.Query().Get("append_to_response"))
		}
		switch r.URL.Path {
		case "/tv/5":
			_, _ = io.WriteString(w, `{"id":5,"name":"大明王朝1566","original_name":"Ming Dynasty in 1566","original_language":"zh","first_air_date":"2007-01-08","popularity":31.5,"vote_count":880,"poster_path":"/poster.jpg","number_of_seasons":1,"number_of_episodes":46,"seasons":[{"season_number":0,"air_date":"2006-12-01"},{"season_number":1,"air_date":"2007-01-08"}],"alternative_titles":{"results":[{"title":"Da Ming Wang Chao 1566"}]},"translations":{"translations":[{"data":{"name":"The Ming Dynasty in 1566"}}]}}`)
		case "/movie/6":
			_, _ = io.WriteString(w, `{"id":6,"title":"一九一七","original_title":"1917","alternative_titles":{"titles":[{"title":"1917：逆战救兵"}]},"translations":{"translations":[{"data":{"title":"1917"}}]}}`)
		case "/movie/7":
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client, err := NewForTest("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	input := []Candidate{
		{ID: 5, MediaType: "tv", Title: "大明王朝1566"},
		{ID: 6, MediaType: "movie", Title: "一九一七", OriginalTitle: "1917"},
		{ID: 7, MediaType: "movie", Title: "Unavailable"},
		{ID: 8, MediaType: "movie", Title: "Outside budget"},
	}
	items, err := client.EnrichCandidates(context.Background(), input, "zh-CN", 99)
	if err != nil {
		t.Fatal(err)
	}
	if requests != DefaultCandidateEnrichmentLimit {
		t.Fatalf("requests=%d", requests)
	}
	if items[0].OriginalTitle != "Ming Dynasty in 1566" || items[0].OriginalLanguage != "zh" || items[0].ReleaseYear == nil || *items[0].ReleaseYear != 2007 || items[0].Popularity != 31.5 || items[0].VoteCount != 880 || items[0].PosterPath != "/poster.jpg" || items[0].SeasonCount != 1 || items[0].EpisodeCount != 46 || items[0].SeasonYears[0] != 2006 || items[0].SeasonYears[1] != 2007 || !containsString(items[0].AlternativeTitles, "Da Ming Wang Chao 1566") || !containsString(items[0].Translations, "The Ming Dynasty in 1566") {
		t.Fatalf("tv=%+v", items[0])
	}
	if !containsString(items[1].AlternativeTitles, "1917：逆战救兵") {
		t.Fatalf("movie=%+v", items[1])
	}
	if len(items[2].AlternativeTitles) != 0 || items[2].Title != "Unavailable" || len(items[3].AlternativeTitles) != 0 {
		t.Fatalf("partial fallback=%+v outside=%+v", items[2], items[3])
	}
}

func TestEnrichCandidatesDeduplicatesIdentityAndBoundsAliases(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = io.WriteString(w, `{"id":9,"title":"Canonical","alternative_titles":{"titles":[{"title":"Alias A"},{"title":"Alias A"},{"title":"Alias B"}]},"translations":{"translations":[{"data":{"title":"Alias C"}}]}}`)
	}))
	defer server.Close()
	client, err := NewForTest("token", server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.EnrichCandidates(context.Background(), []Candidate{{ID: 9, MediaType: "movie", Title: "Canonical"}, {ID: 9, MediaType: "movie", Title: "Canonical"}}, "en-US", 2)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(items[0].AlternativeTitles) != 2 || len(items[0].Translations) != 1 || len(items[1].AlternativeTitles) != 2 || len(items[1].Translations) != 1 {
		t.Fatalf("requests=%d items=%+v", requests, items)
	}
}
