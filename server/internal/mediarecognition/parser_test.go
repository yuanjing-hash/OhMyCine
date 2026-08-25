package mediarecognition

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseMingDynastyManifestUsesFortyNineEpisodeStructure(t *testing.T) {
	year := 2007
	files := make([]FileFact, 0, 49)
	for episode := 1; episode <= 49; episode++ {
		files = append(files, FileFact{
			RelativePath: fmt.Sprintf("Ming Dynasty in 1566 HQ -BlackTV/Ming.Dynasty.in.1566.S01E%02d.HQ-BlackTV.mkv", episode),
			Size:         6 << 30,
		})
	}
	parsed, err := parseAt(InputFacts{PackageName: "Ming Dynasty in 1566 HQ -BlackTV", SourceKind: SourceDownload, YearHint: &year, Files: files}, time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CanonicalTitle != "Ming Dynasty in 1566" || parsed.Year == nil || *parsed.Year != 2007 {
		t.Fatalf("parsed title/year=%q/%v", parsed.CanonicalTitle, parsed.Year)
	}
	if parsed.SuggestedType != MediaTypeTV || parsed.TypeConfidence < .95 || parsed.Episodes.Count != 49 {
		t.Fatalf("type=%q confidence=%f episodes=%+v", parsed.SuggestedType, parsed.TypeConfidence, parsed.Episodes)
	}
	if parsed.ReleaseGroup != "BlackTV" || !containsFold(parsed.Specifications, "HQ") {
		t.Fatalf("release/spec facts: group=%q specs=%v", parsed.ReleaseGroup, parsed.Specifications)
	}
	if len(parsed.Queries) == 0 || parsed.Queries[0].Title != "Ming Dynasty in 1566" {
		t.Fatalf("queries=%+v", parsed.Queries)
	}
}

func TestParseChineseSeasonEpisodeNamesWithoutLosingTheWorkTitle(t *testing.T) {
	files := []FileFact{
		{RelativePath: "斗罗大陆/第2季/斗罗大陆 - - 第1集.mp4", Size: 10},
		{RelativePath: "斗罗大陆/第2季/斗罗大陆 - - 第2集.mp4", Size: 20},
	}
	parsed, err := Parse(InputFacts{
		PackageName:   "斗罗大陆 - - 第2集",
		SourceKind:    SourceLibraryScan,
		Files:         files,
		MediaTypeHint: MediaTypeTV,
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CanonicalTitle != "斗罗大陆" {
		t.Fatalf("canonical title=%q, queries=%+v", parsed.CanonicalTitle, parsed.Queries)
	}
	if parsed.Season == nil || *parsed.Season != 2 || parsed.Episodes.Count != 2 || parsed.Episodes.EpisodeMin == nil || *parsed.Episodes.EpisodeMin != 1 || parsed.Episodes.EpisodeMax == nil || *parsed.Episodes.EpisodeMax != 2 {
		t.Fatalf("season/episodes=%v/%+v", parsed.Season, parsed.Episodes)
	}
	if len(parsed.Queries) == 0 || parsed.Queries[0].Title != "斗罗大陆" {
		t.Fatalf("queries=%+v", parsed.Queries)
	}
}

func TestParseBracketedFansubReleaseWithFourDigitAbsoluteEpisode(t *testing.T) {
	parsed, err := Parse(InputFacts{
		PackageName: "[银色子弹字幕组][名侦探柯南][第1210集 被诅咒的邻居家][WEBRIP][简繁日多语MKV][PGS][1080P]",
		SourceKind:  SourceDownload,
		Files: []FileFact{{
			RelativePath: "[银色子弹]名侦探柯南[S01E1210][JP][PGS]E5B7FCE8.mkv",
			Size:         427 << 20,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CanonicalTitle != "名侦探柯南" || len(parsed.Queries) == 0 || parsed.Queries[0].Title != "名侦探柯南" {
		t.Fatalf("title=%q queries=%+v", parsed.CanonicalTitle, parsed.Queries)
	}
	if parsed.SuggestedType != MediaTypeTV || parsed.TypeConfidence < .90 || parsed.Episodes.Count != 1 || parsed.Episodes.EpisodeMax == nil || *parsed.Episodes.EpisodeMax != 1210 {
		t.Fatalf("type=%q confidence=%f episodes=%+v", parsed.SuggestedType, parsed.TypeConfidence, parsed.Episodes)
	}
	if parsed.ReleaseGroup != "银色子弹字幕组" {
		t.Fatalf("release group=%q", parsed.ReleaseGroup)
	}
	for _, query := range parsed.Queries {
		if strings.Contains(query.Title, "E5B7FCE8") {
			t.Fatalf("CRC leaked into query: %+v", parsed.Queries)
		}
	}
}

func TestParsePreservesTitlesThatAreEntireChineseOrdinals(t *testing.T) {
	for _, title := range []string{"第八集", "第2季", "第二十条"} {
		t.Run(title, func(t *testing.T) {
			parsed, err := Parse(InputFacts{PackageName: title})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.CanonicalTitle != title || len(parsed.Queries) == 0 || parsed.Queries[0].Title != title {
				t.Fatalf("parsed=%+v", parsed)
			}
		})
	}
}

func TestBuildQueryVariantsPrioritizesCanonicalTitlesAcrossSources(t *testing.T) {
	parsed, err := Parse(InputFacts{PreparedNames: []PreparedName{
		{Value: "Wrong.Title.1080p-GRP", Source: "filename"},
		{Value: "Clean Parent", Source: "parent"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Queries) < 2 || parsed.Queries[0].Title != "Wrong Title" || parsed.Queries[1].Title != "Clean Parent" {
		t.Fatalf("canonical titles did not receive fair query priority: %+v", parsed.Queries)
	}
}

func TestParseProtectsNumericTitlesAndLegalHyphens(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
		year     *int
	}{
		{name: "numeric movie title", input: "1917", expected: "1917"},
		{name: "numeric title plus release year", input: "1917.2019.2160p-GRP", expected: "1917", year: intRef(2019)},
		{name: "leading number title", input: "3.Body.Problem.S01E01.2024.1080p-GRP", expected: "3 Body Problem", year: intRef(2024)},
		{name: "historic number inside title", input: "Ming.Dynasty.in.1566.HQ-BlackTV", expected: "Ming Dynasty in 1566"},
		{name: "edition and audio suffix", input: "Seven.Samurai.1954.CC.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA.2.0-SONYHD", expected: "Seven Samurai", year: intRef(1954)},
		{name: "legal single hyphen", input: "Spider-Man.2002.1080p-GRP", expected: "Spider-Man", year: intRef(2002)},
		{name: "legal repeated hyphens", input: "Tinker-Tailor-Soldier-Spy", expected: "Tinker-Tailor-Soldier-Spy"},
		{name: "legal bracketed title", input: "[REC].2007.1080p.BluRay-GRP", expected: "[REC]", year: intRef(2007)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := parseAt(InputFacts{PackageName: test.input}, time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if parsed.CanonicalTitle != test.expected || !sameOptionalInt(parsed.Year, test.year) {
				t.Fatalf("title/year=%q/%v, want %q/%v", parsed.CanonicalTitle, parsed.Year, test.expected, test.year)
			}
			if test.input == "Tinker-Tailor-Soldier-Spy" && parsed.ReleaseGroup != "" {
				t.Fatalf("legal title suffix was classified as release group: %+v", parsed)
			}
		})
	}
}

func TestParseKeepsHyphenatedTechnicalTokensOutOfReleaseGroup(t *testing.T) {
	tests := []struct {
		input string
		spec  string
		group string
	}{
		{input: "Seven.Samurai.1954.2160p.DTS-HD-GROUP", spec: "DTS-HD", group: "GROUP"},
		{input: "Seven.Samurai.1954.1080p.WEB-DL-GROUP-ABC", spec: "WEB-DL", group: "GROUP-ABC"},
	}
	for _, test := range tests {
		parsed, err := Parse(InputFacts{PackageName: test.input, SourceKind: SourceDownload})
		if err != nil {
			t.Fatalf("input=%q err=%v", test.input, err)
		}
		if !containsFold(parsed.Specifications, test.spec) || parsed.ReleaseGroup != test.group {
			t.Fatalf("input=%q specs=%v group=%q", test.input, parsed.Specifications, parsed.ReleaseGroup)
		}
	}
}

func TestParseIsIndependentOfWallClockYear(t *testing.T) {
	input := InputFacts{PackageName: "Future Film 2099 1080p", MediaTypeHint: MediaTypeMovie}
	oldClock, err := parseAt(input, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	newClock, err := parseAt(input, time.Date(2098, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if oldClock.CanonicalTitle != newClock.CanonicalTitle || !sameOptionalInt(oldClock.Year, newClock.Year) {
		t.Fatalf("wall clock changed parse result: old=%+v new=%+v", oldClock, newClock)
	}
}

func TestParseSupportsEmbyTMDBIdentitySyntaxWithoutTrustingMetadata(t *testing.T) {
	for _, input := range []string{"Seven Samurai (1954) [tmdbid=346]", "Seven Samurai (1954) [tmdb-346]", "Seven Samurai (1954) {tmdbid=346}", "Seven Samurai (1954) {tmdb-346}"} {
		parsed, err := parseAt(InputFacts{PackageName: input}, time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("input=%q err=%v", input, err)
		}
		if parsed.DirectHint == nil || parsed.DirectHint.Provider != "tmdb" || parsed.DirectHint.ID != 346 {
			t.Fatalf("input=%q hint=%+v", input, parsed.DirectHint)
		}
		if strings.Contains(strings.ToLower(parsed.CanonicalTitle), "tmdb") {
			t.Fatalf("identity marker leaked into title: %q", parsed.CanonicalTitle)
		}
	}
}

func TestParseRejectsPathsURLsAndUnboundedInputs(t *testing.T) {
	unsafe := []InputFacts{
		{PackageName: `C:\media\Movie`},
		{PackageName: "https://example.invalid/movie"},
		{PackageName: "Movie", Files: []FileFact{{RelativePath: "../Movie.mkv"}}},
		{PackageName: "Movie", Files: []FileFact{{RelativePath: `C:\Movie.mkv`}}},
		{PackageName: strings.Repeat("a", MaxPackageRunes+1)},
	}
	for index, input := range unsafe {
		if _, err := Parse(input); err == nil {
			t.Fatalf("case %d accepted unsafe input", index)
		}
	}
}

func TestParseDetectsDiscAndSeasonStructures(t *testing.T) {
	movie, err := Parse(InputFacts{PackageName: "Seven Samurai 1954", Files: []FileFact{{RelativePath: "Seven Samurai 1954/BDMV/STREAM/00000.m2ts", Size: 30 << 30}}})
	if err != nil || movie.SuggestedType != MediaTypeMovie || !movie.Structure.HasBDMV {
		t.Fatalf("movie=%+v err=%v", movie, err)
	}
	tv, err := Parse(InputFacts{PackageName: "The Show", Files: []FileFact{{RelativePath: "The Show/Season 01/The.Show.S01E01.mkv", Size: 1 << 30}}})
	if err != nil || tv.SuggestedType != MediaTypeTV || !tv.Structure.HasSeasonFolder {
		t.Fatalf("tv=%+v err=%v", tv, err)
	}
}

func containsFold(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(value, expected) {
			return true
		}
	}
	return false
}

func intRef(value int) *int { return &value }

func sameOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
