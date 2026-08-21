package nfo

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

func TestRenderMovieIsDeterministicAndCredentialFree(t *testing.T) {
	snapshot := tmdb.Snapshot{Version: 1, TMDBID: 346, IMDbID: "tt0047478", MediaType: "movie", Title: "七武士", OriginalTitle: "七人の侍", ReleaseDate: "1954-04-26", Overview: "武士与村民。", Tagline: "他们站了起来。", Status: "Released", VoteAverage: 8.5, VoteCount: 3210, RuntimeMinutes: 207, Genres: []tmdb.Genre{{ID: 18, Name: "剧情"}}, ProductionCountries: []string{"JP"}, OriginalLanguage: "JA", SpokenLanguages: []string{"JA"}, Studios: []tmdb.Company{{TMDBID: 5, Name: "东宝"}}, Directors: []tmdb.Person{{TMDBID: 1, Name: "黑泽明", Job: "Director"}}, Writers: []tmdb.Person{{TMDBID: 2, Name: "桥本忍", Job: "Screenplay"}}, Cast: []tmdb.Person{{TMDBID: 3, Name: "三船敏郎", Character: "菊千代"}}, PosterPath: "/poster.jpg", BackdropPath: "/backdrop.jpg"}
	first, err := Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(snapshot)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("deterministic render failed: %v", err)
	}
	content := string(first)
	for _, expected := range []string{"<movie>", "<title>七武士</title>", "<sorttitle>七武士</sorttitle>", "<outline>武士与村民。</outline>", "<tagline>他们站了起来。</tagline>", "<status>Released</status>", "<releasedate>1954-04-26</releasedate>", "<votes>3210</votes>", `<rating name="themoviedb" max="10" default="true">`, "<value>8.5</value>", "<tmdbid>346</tmdbid>", "<imdbid>tt0047478</imdbid>", `<uniqueid type="tmdb" default="true">346</uniqueid>`, "<language>JA</language>", "<studio>东宝</studio>", "<director>黑泽明</director>", "<writer>桥本忍</writer>", "<credits>桥本忍</credits>", "<role>菊千代</role>"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("missing %q in %s", expected, content)
		}
	}
	for _, forbidden := range []string{"api_key", "token=", "D:\\", "https://"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("unsafe NFO contains %q", forbidden)
		}
	}
}

func TestRenderTVIncludesDeterministicSeasonDisplayMetadata(t *testing.T) {
	snapshot := tmdb.Snapshot{Version: 1, TMDBID: 100, MediaType: "tv", Title: "示例剧", OriginalTitle: "Example Show", ReleaseDate: "2020-04-01", Overview: "简介", Status: "Returning Series", SeasonCount: 2, EpisodeCount: 24, Seasons: []tmdb.SeasonSnapshot{{SeasonNumber: 0, Name: "特别篇"}, {SeasonNumber: 1, Name: "第一季"}, {SeasonNumber: 2, Name: ""}}}
	content, err := Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<tvshow>", "<showtitle>示例剧</showtitle>", "<aired>2020-04-01</aired>", `<namedseason number="0">特别篇</namedseason>`, `<namedseason number="1">第一季</namedseason>`} {
		if !strings.Contains(string(content), expected) {
			t.Fatalf("missing %q in %s", expected, content)
		}
	}
	if strings.Contains(string(content), `number="2"`) {
		t.Fatalf("empty season display name was emitted: %s", content)
	}
}

func TestRenderRejectsIncompleteSnapshotAndPlansTVImages(t *testing.T) {
	if _, err := Render(tmdb.Snapshot{Version: 1, MediaType: "movie", Title: "missing id"}); !errors.Is(err, ErrSnapshotIncomplete) {
		t.Fatalf("err=%v", err)
	}
	season := 1
	images := Images(tmdb.Snapshot{MediaType: "tv", PosterPath: "/poster.jpg", BackdropPath: "/backdrop.jpg", Seasons: []tmdb.SeasonSnapshot{{SeasonNumber: season, PosterPath: "/season.jpg"}}})
	if len(images) != 3 || images[2].SeasonNumber == nil || *images[2].SeasonNumber != 1 || images[2].TMDBPath != "/season.jpg" {
		t.Fatalf("images=%+v", images)
	}
}
