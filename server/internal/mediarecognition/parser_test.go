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

func TestParseUntouchedConanEpisode1206Release(t *testing.T) {
	parsed, err := Parse(InputFacts{
		PackageName: "[银色子弹字幕组][名侦探柯南][第1206集 摔落的男人][WEBRIP][简日双语MP4][1080P]",
		SourceKind:  SourceDownload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CanonicalTitle != "名侦探柯南" || parsed.SuggestedType != MediaTypeTV || parsed.TypeConfidence < .90 {
		t.Fatalf("parsed=%+v", parsed)
	}
	if parsed.Episodes.EpisodeMin == nil || *parsed.Episodes.EpisodeMin != 1206 || parsed.Episodes.EpisodeMax == nil || *parsed.Episodes.EpisodeMax != 1206 {
		t.Fatalf("episodes=%+v", parsed.Episodes)
	}
	if len(parsed.Queries) == 0 || parsed.Queries[0].Title != "名侦探柯南" {
		t.Fatalf("queries=%+v", parsed.Queries)
	}
}

func TestParsePTAndNyaaReleaseShapesIntoCleanQueries(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		canonical    string
		query        string
		mediaType    MediaType
		season       *int
		episodeMin   *int
		episodeMax   *int
		releaseGroup string
	}{
		{
			name:         "spaced codecs and audio channels",
			input:        "Apartment of Love 2018 2160p WEB-DL H.265 DDP5.1-AilMWeb",
			canonical:    "Apartment of Love",
			query:        "Apartment of Love",
			releaseGroup: "AilMWeb",
		},
		{
			name:         "season complete pack",
			input:        "The Lord of Losers 2023 S02 Complete WEB-DL 4K EDR HEVC AAC-CMCTV",
			canonical:    "The Lord of Losers",
			query:        "The Lord of Losers",
			mediaType:    MediaTypeTV,
			season:       intRef(2),
			releaseGroup: "CMCTV",
		},
		{
			name:         "multilingual bracketed complete series",
			input:        "[DBD-Raws][迪迦奥特曼/Ultraman Tiga/ウルトラマンティガ][01-52TV全集+剧场+OV+特典][1080P][BDRip][HEVC-10bit][简体字幕外挂][FLAC][MKV]",
			canonical:    "迪迦奥特曼/Ultraman Tiga/ウルトラマンティガ",
			query:        "迪迦奥特曼",
			mediaType:    MediaTypeTV,
			episodeMin:   intRef(1),
			episodeMax:   intRef(52),
			releaseGroup: "DBD-Raws",
		},
		{
			name:         "technical bracket before release group",
			input:        "[1080P][DBD-Raws][迪迦奥特曼 OV 远古复苏的巨人/Ultraman Tiga Gaiden: Revival of the Ancient Giant/ウルトラマンティガ外伝 古代に蘇る巨人][普通版][HEVC-10bit][AC3][MKV]",
			canonical:    "迪迦奥特曼 OV 远古复苏的巨人/Ultraman Tiga Gaiden: Revival of the Ancient Giant/ウルトラマンティガ外伝 古代に蘇る巨人",
			query:        "迪迦奥特曼 OV 远古复苏的巨人",
			releaseGroup: "DBD-Raws",
		},
		{
			name:         "bracketed absolute episode",
			input:        "[未央阁-爱之夏字幕组&魔星字幕组][迪迦奥特曼][06][第二次接触][BDrip][X264(10-bit) PCM][MKV]",
			canonical:    "迪迦奥特曼",
			query:        "迪迦奥特曼",
			mediaType:    MediaTypeTV,
			episodeMin:   intRef(6),
			episodeMax:   intRef(6),
			releaseGroup: "未央阁-爱之夏字幕组&魔星字幕组",
		},
		{
			name:       "nyaa ep range",
			input:      "Ultraman Tiga (960x720 BDRip) - EP26-52",
			canonical:  "Ultraman Tiga",
			query:      "Ultraman Tiga",
			mediaType:  MediaTypeTV,
			episodeMin: intRef(26),
			episodeMax: intRef(52),
		},
		{
			name:       "title first bracketed complete range",
			input:      "【ウルトラマンティガ】【BDrip】【1-52】【1080p】【国台日三语】【简体字幕】",
			canonical:  "ウルトラマンティガ",
			query:      "ウルトラマンティガ",
			mediaType:  MediaTypeTV,
			episodeMin: intRef(1),
			episodeMax: intRef(52),
		},
		{
			name:         "fansub episode subtitle segment",
			input:        "【奥盟字幕组】【奥特曼列传第3集：复活的迪迦!超古代的光之战士!!】【MKV】",
			canonical:    "奥特曼列传",
			query:        "奥特曼列传",
			mediaType:    MediaTypeTV,
			episodeMin:   intRef(3),
			episodeMax:   intRef(3),
			releaseGroup: "奥盟字幕组",
		},
		{
			name:         "complete count and multilingual aliases",
			input:        "[PorterRAWS]迪迦奥特曼 / 超人迪加 / Ultraman Tiga [52全][DVD480P][爱子粤语][未调轴中字]",
			canonical:    "迪迦奥特曼 / 超人迪加 / Ultraman Tiga",
			query:        "Ultraman Tiga",
			mediaType:    MediaTypeTV,
			episodeMin:   intRef(1),
			episodeMax:   intRef(52),
			releaseGroup: "PorterRAWS",
		},
		{
			name:         "plus separated latin title",
			input:        "ULTRAMAN+TIGA 1996 BluRay X264 3Audio 1080p LGGZS",
			canonical:    "ULTRAMAN TIGA",
			query:        "ULTRAMAN TIGA",
			releaseGroup: "LGGZS",
		},
		{
			name:       "bare episode before language bracket",
			input:      "[jibaketa合成&音频压制][ViuTV粤语]超人 / 超人力霸王奥米加 / 奥美迦奥特曼 / Ultraman Omega - 09 [粤语+无字幕] (WEB 1920x1080 AVC AAC YUE)",
			canonical:  "超人 / 超人力霸王奥米加 / 奥美迦奥特曼 / Ultraman Omega",
			query:      "Ultraman Omega",
			mediaType:  MediaTypeTV,
			season:     intRef(1),
			episodeMin: intRef(9),
			episodeMax: intRef(9),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := Parse(InputFacts{PackageName: test.input, SourceKind: SourceDownload})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.CanonicalTitle != test.canonical {
				t.Fatalf("canonical=%q queries=%+v", parsed.CanonicalTitle, parsed.Queries)
			}
			if test.query != "" && !queryContains(parsed.Queries, test.query) {
				t.Fatalf("query %q missing from %+v", test.query, parsed.Queries)
			}
			if test.mediaType != MediaTypeUnknown && parsed.SuggestedType != test.mediaType {
				t.Fatalf("type=%q evidence=%+v", parsed.SuggestedType, parsed.TypeEvidence)
			}
			if !sameOptionalInt(parsed.Season, test.season) || !sameOptionalInt(parsed.Episodes.EpisodeMin, test.episodeMin) || !sameOptionalInt(parsed.Episodes.EpisodeMax, test.episodeMax) {
				t.Fatalf("season/episodes=%v/%+v", parsed.Season, parsed.Episodes)
			}
			if test.releaseGroup != "" && parsed.ReleaseGroup != test.releaseGroup {
				t.Fatalf("release group=%q", parsed.ReleaseGroup)
			}
		})
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

func TestParseDoesNotTreatLegalHyphenatedNumberTitleAsEpisode(t *testing.T) {
	for _, title := range []string{"Catch-22.2019.1080p.WEB-DL", "Catch-22 [Remastered]"} {
		parsed, err := Parse(InputFacts{PackageName: title, SourceKind: SourceDownload})
		if err != nil {
			t.Fatal(err)
		}
		if parsed.SuggestedType == MediaTypeTV || parsed.Episodes.EpisodeMin != nil {
			t.Fatalf("title=%q parsed=%+v", title, parsed)
		}
	}
}

func TestParseProductionEnglishAndPinyinReleaseNamesKeepSearchableWorkTitles(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		canonical  string
		query      string
		mediaType  MediaType
		season     *int
		year       *int
		seasonYear *int
	}{
		{name: "english original title", input: "ULTRAMAN TIGA", canonical: "ULTRAMAN TIGA", query: "ULTRAMAN TIGA"},
		{name: "remastered edition noise", input: "Ultraman.Tiga.Ultra.Resolution.Remastered.Version.1997.BluRay.1080p.x264.AAC", canonical: "Ultraman Tiga", query: "Ultraman Tiga", year: intRef(1997)},
		{name: "movie subtitle", input: "The Final Odyssey 1080p WEB-DL H264 AAC-Side", canonical: "The Final Odyssey", query: "The Final Odyssey"},
		{name: "gaiden english title", input: "Ultraman Tiga Gaiden Revival of the Ancient Giant WEB-DL 2160P HEVC AAC-Side", canonical: "Ultraman Tiga Gaiden Revival of the Ancient Giant", query: "Ultraman Tiga Gaiden Revival of the Ancient Giant"},
		{name: "zh language marker", input: "Ultraman Tiga 1996 WEB-DL 1080p H264 ZH-AAC-HDCTV", canonical: "Ultraman Tiga", query: "Ultraman Tiga", year: intRef(1996)},
		{name: "pinyin season", input: "Ai qing gong yu 2012 S03 2160p WEB-DL H.265 AAC-ZmWeb", canonical: "Ai qing gong yu", query: "Ai qing gong yu", mediaType: MediaTypeTV, season: intRef(3), seasonYear: intRef(2012)},
		{name: "localized english alias", input: "Apartment of Love 2018 2160p WEB-DL H.265 AAC-AilMWeb", canonical: "Apartment of Love", query: "Apartment of Love", year: intRef(2018)},
		{name: "official english alias", input: "Ipartment S05 2020 2160p WEB-DL H.265 DDP2.0-CSWEB", canonical: "Ipartment", query: "Ipartment", mediaType: MediaTypeTV, season: intRef(5), seasonYear: intRef(2020)},
		{name: "pinyin season with aac channels", input: "Wan Gu Long Shen S01 2022 2160p WEB-DL H265 10bit HDR10 AAC 2.0-CSWEB", canonical: "Wan Gu Long Shen", query: "Wan Gu Long Shen", mediaType: MediaTypeTV, season: intRef(1), seasonYear: intRef(2022)},
		{name: "franchise movie with aac channels", input: "Ultraman Tiga & Ultraman Dyna & Ultraman Gaia: Battle in Hyperspace 1999 2160p WEB-DL H.265 AAC 2.0-CSWEB", canonical: "Ultraman Tiga & Ultraman Dyna & Ultraman Gaia: Battle in Hyperspace", query: "Ultraman Tiga & Ultraman Dyna & Ultraman Gaia: Battle in Hyperspace", year: intRef(1999)},
		{name: "pinyin donghua with aac channels", input: "Du Shi Zui Qiang Fang Dong S01 2024 1080p WEB-DL H264 AAC 2.0-CSWEB", canonical: "Du Shi Zui Qiang Fang Dong", query: "Du Shi Zui Qiang Fang Dong", mediaType: MediaTypeTV, season: intRef(1), seasonYear: intRef(2024)},
		{name: "frame rate specification", input: "Apartment of Love 2018 1080p WEB-DL 60fps H.265 DDP5.1-AilMWeb", canonical: "Apartment of Love", query: "Apartment of Love", year: intRef(2018)},
		{name: "bit depth specification", input: "Ipartment 2018 2160p WEB-DL H.265 10bit AAC-UBWEB", canonical: "Ipartment", query: "Ipartment", year: intRef(2018)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := Parse(InputFacts{PackageName: test.input, SourceKind: SourceDownload})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.CanonicalTitle != test.canonical || !queryContains(parsed.Queries, test.query) {
				t.Fatalf("canonical=%q queries=%+v", parsed.CanonicalTitle, parsed.Queries)
			}
			if test.mediaType != MediaTypeUnknown && parsed.SuggestedType != test.mediaType {
				t.Fatalf("type=%q evidence=%+v", parsed.SuggestedType, parsed.TypeEvidence)
			}
			if !sameOptionalInt(parsed.Season, test.season) || !sameOptionalInt(parsed.Year, test.year) || !sameOptionalInt(parsed.SeasonYear, test.seasonYear) {
				t.Fatalf("season/year/season_year=%v/%v/%v", parsed.Season, parsed.Year, parsed.SeasonYear)
			}
			if test.seasonYear != nil {
				for _, query := range parsed.Queries {
					if query.Year != nil || !sameOptionalInt(query.SeasonYear, test.seasonYear) {
						t.Fatalf("season year leaked into work-year query: %+v", parsed.Queries)
					}
				}
			}
		})
	}
}

func TestParseAddsOnlyBoundedLatinTokenFallbacksForMultiwordTypoRecovery(t *testing.T) {
	parsed, err := Parse(InputFacts{PackageName: "ULRAMAN+TIGA 1996 BluRay 1080p", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	fallbacks := make([]string, 0, 2)
	for _, query := range parsed.Queries {
		if query.Reason == "latin_token_fallback" {
			fallbacks = append(fallbacks, query.Title)
			if query.Year == nil || *query.Year != 1996 {
				t.Fatalf("fallback lost bounded year evidence: %+v", query)
			}
		}
	}
	if len(fallbacks) != 2 || fallbacks[0] != "ulraman" || fallbacks[1] != "tiga" {
		t.Fatalf("fallbacks=%v queries=%+v", fallbacks, parsed.Queries)
	}

	for _, title := range []string{"TIGA 1080p", "A Very Long 中文 Title 1080p", "Spider-Man 2002 1080p"} {
		negative, parseErr := Parse(InputFacts{PackageName: title, SourceKind: SourceDownload})
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, query := range negative.Queries {
			if query.Reason == "latin_token_fallback" {
				t.Fatalf("unsafe token fallback for %q: %+v", title, query)
			}
		}
	}
}

func TestParseLongRunningAnimeReleaseMatrixKeepsIdentityAndStructureSeparate(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		canonical string
		queries   []string
		mediaType MediaType
		season    *int
		episode   *int
		spec      string
	}{
		{
			name:      "parenthesized absolute episode with bilingual aliases",
			input:     "[黒ネズミたち] 名侦探柯南 / Detective Conan - 1210 (CR 1920x1080 AVC AAC MKV)",
			canonical: "名侦探柯南 / Detective Conan",
			queries:   []string{"名侦探柯南", "Detective Conan"},
			mediaType: MediaTypeTV,
			season:    intRef(1),
			episode:   intRef(1210),
		},
		{
			name:      "franchise movie index and bilingual subtitle",
			input:     "[银色子弹字幕组&VCB-Studio] 名侦探柯南M21 唐红的恋歌 / Detective Conan M21: The Crimson Love Letter 10-bit 1080p HEVC BDRip [MOVIE Fin]",
			canonical: "名侦探柯南 唐红的恋歌 / Detective Conan : The Crimson Love Letter",
			queries:   []string{"名侦探柯南 唐红的恋歌", "Detective Conan : The Crimson Love Letter"},
			mediaType: MediaTypeMovie,
			spec:      "10-BIT",
		},
		{
			name:      "another franchise movie number uses the same rule",
			input:     "[VCB-Studio] 名侦探柯南M28 独眼的残像 / Detective Conan M28: One-eyed Flashback 10-bit 1080p HEVC BDRip [MOVIE Fin]",
			canonical: "名侦探柯南 独眼的残像 / Detective Conan : One-eyed Flashback",
			queries:   []string{"名侦探柯南 独眼的残像", "Detective Conan : One-eyed Flashback"},
			mediaType: MediaTypeMovie,
			spec:      "10-BIT",
		},
		{
			name:      "dub qualifier stays out of the title",
			input:     "[Doomdos] - 名侦探柯南（中配） - 第1262话 - [1080p]",
			canonical: "名侦探柯南",
			queries:   []string{"名侦探柯南"},
			mediaType: MediaTypeTV,
			episode:   intRef(1262),
			spec:      "中配",
		},
		{
			name:      "remastered split part keeps the original absolute episode",
			input:     "[银色子弹字幕组][名侦探柯南][数码重映第118-2集 浪花连续杀人事件（后篇）][字幕仅重映片头片尾][TVRIP][简繁日多语MKV][1080P]",
			canonical: "名侦探柯南",
			queries:   []string{"名侦探柯南"},
			mediaType: MediaTypeTV,
			episode:   intRef(118),
		},
		{
			name:      "plural audio count and channel layout are release noise",
			input:     "Lupin III vs Detective Conan 2009 1080p BluRay HEVC FLAC 2.0 3Audios-ADE",
			canonical: "Lupin III vs Detective Conan",
			queries:   []string{"Lupin III vs Detective Conan"},
		},
		{
			name:      "police headquarters special keeps full english identity",
			input:     "Detective Conan Love Story at Police Headquarters Wedding Eve 2022 1080p BluRay x265 10bit FLAC 2.0 2Audios-ADE",
			canonical: "Detective Conan Love Story at Police Headquarters Wedding Eve",
			queries:   []string{"Detective Conan Love Story at Police Headquarters Wedding Eve"},
		},
		{
			name:      "scarlet alibi keeps full english identity",
			input:     "Detective Conan The Scarlet Alibi 2021 1080p BluRay x265 10bit DTS 5.1 2Audios-ADE",
			canonical: "Detective Conan The Scarlet Alibi",
			queries:   []string{"Detective Conan The Scarlet Alibi"},
		},
		{
			name:      "lupin crossover movie keeps title movie token",
			input:     "Lupin the 3rd vs Detective Conan The Movie 2013 1080p BluRay x265 10bit DTS 5.1 3Audios-ADE",
			canonical: "Lupin the 3rd vs Detective Conan The Movie",
			queries:   []string{"Lupin the 3rd vs Detective Conan The Movie"},
		},
		{
			name:      "unknown feature remains untyped",
			input:     "Detective Conan The Scarlet Alibi 2021 1080p BluRay HEVC DTS 5.1 2Audios-ADE",
			canonical: "Detective Conan The Scarlet Alibi",
			queries:   []string{"Detective Conan The Scarlet Alibi"},
			mediaType: MediaTypeUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := Parse(InputFacts{PackageName: test.input, SourceKind: SourceDownload})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.CanonicalTitle != test.canonical || parsed.SuggestedType != test.mediaType || !sameOptionalInt(parsed.Season, test.season) || !sameOptionalInt(parsed.Episodes.EpisodeMax, test.episode) {
				t.Fatalf("parsed=%+v", parsed)
			}
			for _, expected := range test.queries {
				if !queryContains(parsed.Queries, expected) {
					t.Fatalf("missing query %q in %+v", expected, parsed.Queries)
				}
			}
			if test.spec != "" && !containsFold(parsed.Specifications, test.spec) {
				t.Fatalf("missing specification %q in %v", test.spec, parsed.Specifications)
			}
		})
	}

	legal, err := Parse(InputFacts{PackageName: "Scary Movie 2000 1080p BluRay", SourceKind: SourceDownload})
	if err != nil || legal.CanonicalTitle != "Scary Movie" {
		t.Fatalf("legal movie title was stripped: parsed=%+v err=%v", legal, err)
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
		name       string
		input      string
		expected   string
		year       *int
		seasonYear *int
	}{
		{name: "numeric movie title", input: "1917", expected: "1917"},
		{name: "numeric title plus release year", input: "1917.2019.2160p-GRP", expected: "1917", year: intRef(2019)},
		{name: "leading number title", input: "3.Body.Problem.S01E01.2024.1080p-GRP", expected: "3 Body Problem", seasonYear: intRef(2024)},
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
			if parsed.CanonicalTitle != test.expected || !sameOptionalInt(parsed.Year, test.year) || !sameOptionalInt(parsed.SeasonYear, test.seasonYear) {
				t.Fatalf("title/year/season_year=%q/%v/%v, want %q/%v/%v", parsed.CanonicalTitle, parsed.Year, parsed.SeasonYear, test.expected, test.year, test.seasonYear)
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
		{PackageName: `Movie\Season 01`},
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

func queryContains(values []QueryVariant, expected string) bool {
	for _, value := range values {
		if value.Title == expected {
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
