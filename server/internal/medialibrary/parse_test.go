package medialibrary

import "testing"

func TestParseMediaGroupsPlayerCompatibleEpisodeLayouts(t *testing.T) {
	tests := []struct {
		name, path, title string
		season, episode   int
	}{
		{"Lycoris.Recoil.S01E01.mkv", "/Lycoris Recoil/Season 01/Lycoris.Recoil.S01E01.mkv", "Lycoris Recoil", 1, 1},
		{"EP02.mkv", "/Lycoris Recoil/S01/EP02.mkv", "Lycoris Recoil", 1, 2},
		{"Lycoris.Recoil.2x03.mkv", "/Lycoris Recoil/Lycoris.Recoil.2x03.mkv", "Lycoris Recoil", 2, 3},
		{"第十二话.mkv", "/动画/示例动画/第2季/第十二话.mkv", "示例动画", 2, 12},
		{"名侦探柯南.S01E1210.mkv", "/名侦探柯南/Season 01/名侦探柯南.S01E1210.mkv", "名侦探柯南", 1, 1210},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			parsed := ParseMedia(test.name, test.path)
			if parsed.MediaType != "tv" || parsed.SeriesTitle != test.title || parsed.Title != test.title || parsed.Season == nil || *parsed.Season != test.season || parsed.Episode == nil || *parsed.Episode != test.episode {
				t.Fatalf("parsed=%+v", parsed)
			}
		})
	}
}

func TestParseMediaUsesTitleYearFolderForMovie(t *testing.T) {
	parsed := ParseMedia("Movie.2024.2160p.WEB-DL.mkv", "/电影/Movie (2024)/Movie.2024.2160p.WEB-DL.mkv")
	if parsed.MediaType != "movie" || parsed.Title != "Movie" || parsed.SeriesTitle != "" || parsed.Season != nil || parsed.Episode != nil {
		t.Fatalf("parsed=%+v", parsed)
	}
}

func TestParseTrailingBracketEpisodeDefaultsToSeasonOne(t *testing.T) {
	name := `[jibaketa合成&音频压制][ViuTV粤语]超人 / 超人力霸王奥米加 / 奥美迦奥特曼 / Ultraman Omega - 09 [粤语+无字幕] (WEB 1920x1080 AVC AAC YUE).mkv`
	parsed := ParseMedia(name, "/"+name)
	if parsed.MediaType != "tv" || parsed.Season == nil || *parsed.Season != 1 || parsed.Episode == nil || *parsed.Episode != 9 {
		t.Fatalf("parsed=%+v", parsed)
	}

	negative := ParseMedia("Catch-22 (2019).mkv", "/Catch-22 (2019).mkv")
	if negative.MediaType != "movie" || negative.Episode != nil {
		t.Fatalf("negative=%+v", negative)
	}
}

func TestWorkKeyGroupsSeriesButKeepsMoviesByIdentity(t *testing.T) {
	first := ParseMedia("Lycoris.Recoil.S01E01.mkv", "/Lycoris Recoil/Season 01/Lycoris.Recoil.S01E01.mkv")
	second := ParseMedia("Lycoris.Recoil.S02E01.mkv", "/Lycoris Recoil/Season 02/Lycoris.Recoil.S02E01.mkv")
	if WorkKey(first, "episode-1") != WorkKey(second, "episode-2") {
		t.Fatal("same series produced different work keys")
	}
	movie := ParseMedia("Movie.mkv", "/Movie.mkv")
	if WorkKey(movie, "movie-1") == WorkKey(movie, "movie-2") {
		t.Fatal("different movie files produced the same work key")
	}
}
