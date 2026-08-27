package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildIdentitySearchNamesOrdersLocalesAndUnicodeDeduplicates(t *testing.T) {
	match := Match{Snapshot: Snapshot{Title: "后翼弃兵", OriginalTitle: "The Queen's Gambit", OriginalLanguage: "en"}}
	var detail candidateEnrichmentResponse
	detail.AlternativeTitles.Results = []candidateAlternativeTitle{{ISO31661: "TW", Title: "后翼棄兵"}, {ISO31661: "HK", Title: "后翼棄兵"}, {ISO31661: "US", Title: "Queen's Gambit"}}
	var english, duplicate candidateTranslation
	english.ISO6391, english.ISO31661, english.Data.Name = "en", "US", "The Queen's Gambit"
	duplicate.ISO6391, duplicate.ISO31661, duplicate.Data.Name = "zh", "CN", "  后翼弃兵  "
	detail.Translations.Translations = []candidateTranslation{duplicate, english}
	names := buildIdentitySearchNames(match, detail, 6)
	want := []string{"后翼弃兵", "后翼棄兵", "The Queen's Gambit", "Queen's Gambit"}
	if len(names) != len(want) {
		t.Fatalf("names=%+v", names)
	}
	for index, value := range want {
		if names[index].Value != value {
			t.Fatalf("names[%d]=%+v", index, names[index])
		}
	}
	if names[1].Locale != "zh-TW" || names[2].Kind != "original" {
		t.Fatalf("names=%+v", names)
	}
}

func TestBuildIdentitySearchNamesHonorsHardLimitAndLength(t *testing.T) {
	match := Match{Snapshot: Snapshot{Title: "标题", OriginalTitle: "Original"}}
	var detail candidateEnrichmentResponse
	detail.AlternativeTitles.Results = []candidateAlternativeTitle{{ISO31661: "TW", Title: "繁體"}, {ISO31661: "US", Title: "Alias"}}
	names := buildIdentitySearchNames(match, detail, 2)
	if len(names) != 2 || names[0].Value != "标题" || names[1].Value != "繁體" {
		t.Fatalf("names=%+v", names)
	}
	long := make([]rune, MaxIdentitySearchNameRunes+1)
	for index := range long {
		long[index] = '长'
	}
	match.Snapshot.Title = string(long)
	names = buildIdentitySearchNames(match, candidateEnrichmentResponse{}, 1)
	if len(names) != 1 || names[0].Value != "Original" {
		t.Fatalf("names=%+v", names)
	}
}

func TestIdentitySearchNamesKeepsVerifiedCoreNamesWhenEnrichmentFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/movie/346" {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Query().Get("append_to_response") == "alternative_titles,translations" {
			http.Error(writer, "upstream unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = writer.Write([]byte(`{"id":346,"title":"七武士","original_title":"Seven Samurai","original_language":"ja","release_date":"1954-04-26"}`))
	}))
	defer upstream.Close()
	client, err := NewForTest("token", upstream.URL, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	verified, names, err := client.IdentitySearchNames(context.Background(), "movie", 346, "zh-CN", DefaultIdentitySearchNameLimit)
	if err != nil || verified.ID != 346 || len(names) != 2 || names[0].Value != "七武士" || names[1].Value != "Seven Samurai" {
		t.Fatalf("verified=%+v names=%+v err=%v", verified, names, err)
	}
}
