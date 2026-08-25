package mediarecognition

import (
	"math/rand"
	"testing"
)

func TestTitleSimilarityNormalizesUnicodePunctuationWhitespaceCaseAndHan(t *testing.T) {
	cases := []struct{ left, right string }{
		{"Cafe\u0301 Society", "CAFÉ-SOCIETY"},
		{"Spider-Man", "spider man"},
		{"后宫甄嬛传", "後宮甄嬛傳"},
		{"大明王朝 1566", "大明王朝1566"},
	}
	for _, test := range cases {
		if score := TitleSimilarity(test.left, test.right, BuiltInHanEquivalence); score != 1 {
			t.Fatalf("similarity(%q,%q)=%f", test.left, test.right, score)
		}
	}
}

func TestRankMingDynastySelectsOriginalTitleAcrossCandidateOrder(t *testing.T) {
	year := 2007
	files := make([]FileFact, 0, 49)
	for episode := 1; episode <= 49; episode++ {
		files = append(files, FileFact{RelativePath: episodePath(episode), Size: 1 << 30})
	}
	parsed, err := Parse(InputFacts{PackageName: "Ming Dynasty in 1566 HQ -BlackTV", YearHint: &year, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	candidates := []RemoteCandidate{
		{ID: 9, MediaType: MediaTypeTV, Title: "Ming Dynasty", ReleaseYear: intRef(2006), Popularity: 100},
		{ID: 100, MediaType: MediaTypeTV, Title: "大明王朝1566", OriginalTitle: "Ming Dynasty in 1566", ReleaseYear: intRef(2007), SeasonCount: intRef(1), Popularity: 5},
		{ID: 200, MediaType: MediaTypeMovie, Title: "Ming Dynasty in 1566", ReleaseYear: intRef(2007), Popularity: 500},
	}
	for iteration := 0; iteration < 20; iteration++ {
		rand.New(rand.NewSource(int64(iteration))).Shuffle(len(candidates), func(left, right int) { candidates[left], candidates[right] = candidates[right], candidates[left] })
		decision := Rank(parsed, candidates)
		if decision.Status != DecisionMatched || decision.Reason != ReasonMatched || decision.Match == nil || decision.Match.ID != 100 {
			t.Fatalf("iteration=%d decision=%+v", iteration, decision)
		}
	}
}

func TestRankBracketedFansubAbsoluteEpisodeAsConfidentTVMatch(t *testing.T) {
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
	decision := Rank(parsed, []RemoteCandidate{{ID: 30983, MediaType: MediaTypeTV, Title: "名侦探柯南", OriginalTitle: "名探偵コナン", SeasonCount: intRef(1), Popularity: 150}})
	if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 30983 || decision.Confidence < DefaultScoreConfig().MatchThreshold {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestRankLongRunningSeriesUsesStrongTVStructureWithoutTrustingLargeEpisodeNumber(t *testing.T) {
	parsed, err := Parse(InputFacts{
		PackageName: "[银色子弹字幕组][名侦探柯南][第1210集 被诅咒的邻居][WEBRIP][简日双语MP4][1080P]",
		SourceKind:  SourceDownload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.SuggestedType != MediaTypeTV || parsed.TypeConfidence < .90 || parsed.Episodes.EpisodeMax == nil || *parsed.Episodes.EpisodeMax != 1210 {
		t.Fatalf("parsed=%+v", parsed)
	}
	candidates := []RemoteCandidate{
		{ID: 30983, MediaType: MediaTypeTV, Title: "名侦探柯南", OriginalTitle: "名探偵コナン", SeasonCount: intRef(1), Popularity: 150},
		{ID: 90001, MediaType: MediaTypeMovie, Title: "名侦探柯南", Popularity: 300},
		{ID: 917496, MediaType: MediaTypeMovie, Title: "名侦探柯南：唐红的恋歌", Popularity: 50},
	}
	config := DefaultScoreConfig()
	// Exercise the explicit structured-type tie breaker even when scoring
	// weights leave the exact TV and movie identities inside conflict margin.
	config.TypeWeight = .01
	config.TypeConflict = 0
	config.StructureWeight = .01
	config.SeasonWeight = 0
	config.UniquenessWeight = 0
	config.PopularityWeight = 0
	decision := RankWithConfig(parsed, candidates, config)
	if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 30983 {
		t.Fatalf("decision=%+v", decision)
	}

	// The number 1210 itself is not an identity shortcut. Two exact TV
	// identities with the same structure still require explicit correction.
	ambiguous := Rank(parsed, []RemoteCandidate{
		{ID: 30983, MediaType: MediaTypeTV, Title: "名侦探柯南", SeasonCount: intRef(1)},
		{ID: 99999, MediaType: MediaTypeTV, Title: "名侦探柯南", SeasonCount: intRef(1)},
	})
	if ambiguous.Status != DecisionUnrecognized || ambiguous.Reason != ReasonCandidateConflict {
		t.Fatalf("same-type identity conflict was hidden: %+v", ambiguous)
	}
}

func TestRankUsesKnownEpisodeRangeToResolveExactTVIdentityConflict(t *testing.T) {
	parsed, err := Parse(InputFacts{
		PackageName: "[银色子弹字幕组][名侦探柯南][第1206集 摔落的男人][WEBRIP][简日双语MP4][1080P]",
		SourceKind:  SourceDownload,
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates := []RemoteCandidate{
		{ID: 318691, MediaType: MediaTypeTV, Title: "名侦探柯南", EpisodeCount: intRef(24)},
		{ID: 30983, MediaType: MediaTypeTV, Title: "名侦探柯南", OriginalTitle: "名探偵コナン", EpisodeCount: intRef(1300)},
	}
	for _, input := range [][]RemoteCandidate{candidates, {candidates[1], candidates[0]}} {
		decision := Rank(parsed, input)
		if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 30983 {
			t.Fatalf("decision=%+v", decision)
		}
		if decision.RunnerUpGap < DefaultScoreConfig().ConflictMargin {
			t.Fatalf("episode evidence did not resolve conflict: %+v", decision)
		}
	}
}

func TestRankTreatsUnknownEpisodeCountAsNeutralAndKeepsWeakEvidenceSafe(t *testing.T) {
	parsed, err := Parse(InputFacts{PackageName: "Example Series 第1206集", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	unknown := Rank(parsed, []RemoteCandidate{
		{ID: 1, MediaType: MediaTypeTV, Title: "Example Series", EpisodeCount: intRef(1300)},
		{ID: 2, MediaType: MediaTypeTV, Title: "Example Series"},
	})
	if unknown.Status != DecisionUnrecognized || unknown.Reason != ReasonCandidateConflict {
		t.Fatalf("missing episode count was treated as a conflict: %+v", unknown)
	}

	ordinary, ordinaryErr := Parse(InputFacts{PackageName: "Example Series 第2集", SourceKind: SourceDownload})
	if ordinaryErr != nil {
		t.Fatal(ordinaryErr)
	}
	ordinaryDecision := Rank(ordinary, []RemoteCandidate{
		{ID: 3, MediaType: MediaTypeTV, Title: "Example Series", EpisodeCount: intRef(12)},
		{ID: 4, MediaType: MediaTypeTV, Title: "Example Series", EpisodeCount: intRef(24)},
	})
	if ordinaryDecision.Status != DecisionUnrecognized || ordinaryDecision.Reason != ReasonCandidateConflict {
		t.Fatalf("ordinary low episode evidence fabricated uniqueness: %+v", ordinaryDecision)
	}

	unsafe := Rank(parsed, []RemoteCandidate{
		{ID: 5, MediaType: MediaTypeTV, Title: "Example Series", EpisodeCount: intRef(24)},
		{ID: 6, MediaType: MediaTypeTV, Title: "Completely Different", EpisodeCount: intRef(1300)},
	})
	if unsafe.Status == DecisionMatched || unsafe.Match != nil {
		t.Fatalf("episode count overrode title identity: %+v", unsafe)
	}
}

func TestRankFranchiseMovieSubtitleWinsWhileUntypedExactConflictStaysManual(t *testing.T) {
	movie, err := Parse(InputFacts{
		PackageName: "[银色子弹字幕组&VCB-Studio] 名侦探柯南M21 唐红的恋歌 / Detective Conan M21: The Crimson Love Letter 10-bit 1080p HEVC BDRip [MOVIE Fin]",
		SourceKind:  SourceDownload,
	})
	if err != nil {
		t.Fatal(err)
	}
	decision := Rank(movie, []RemoteCandidate{
		{ID: 917496, MediaType: MediaTypeMovie, Title: "名侦探柯南：唐红的恋歌", AlternativeTitles: []string{"Detective Conan: The Crimson Love Letter"}},
		{ID: 30983, MediaType: MediaTypeTV, Title: "名侦探柯南", OriginalTitle: "名探偵コナン"},
	})
	if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 917496 {
		t.Fatalf("movie decision=%+v parsed=%+v", decision, movie)
	}

	unknown, err := Parse(InputFacts{PackageName: "Detective Conan The Scarlet School Trip 2019 1080p BluRay HEVC FLAC 2.0 2Audios-ADE", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	manual := Rank(unknown, []RemoteCandidate{
		{ID: 1, MediaType: MediaTypeMovie, Title: "Detective Conan The Scarlet School Trip"},
		{ID: 2, MediaType: MediaTypeTV, Title: "Detective Conan The Scarlet School Trip"},
	})
	if manual.Status != DecisionUnrecognized || manual.Reason != ReasonCandidateConflict {
		t.Fatalf("untyped exact conflict was silently selected: %+v", manual)
	}
}

func TestRankReturnsStableUnrecognizedReasons(t *testing.T) {
	year := 2024
	parsed, err := Parse(InputFacts{PackageName: "Exact Title 2024", MediaTypeHint: MediaTypeMovie})
	if err != nil {
		t.Fatal(err)
	}
	if decision := Rank(parsed, nil); decision.Reason != ReasonNoMatch {
		t.Fatalf("no candidates reason=%q", decision.Reason)
	}
	if decision := Rank(parsed, []RemoteCandidate{{ID: 1, MediaType: MediaTypeMovie, Title: "Completely Different", ReleaseYear: &year}}); decision.Reason != ReasonLowConfidence {
		t.Fatalf("low-confidence decision=%+v", decision)
	}
	conflict := Rank(parsed, []RemoteCandidate{
		{ID: 2, MediaType: MediaTypeMovie, Title: "Exact Title", ReleaseYear: &year},
		{ID: 3, MediaType: MediaTypeMovie, Title: "Exact Title", ReleaseYear: &year},
	})
	if conflict.Reason != ReasonCandidateConflict || conflict.Status != DecisionUnrecognized {
		t.Fatalf("conflict decision=%+v", conflict)
	}
	wrongYear := 1990
	low := Rank(parsed, []RemoteCandidate{{ID: 4, MediaType: MediaTypeMovie, Title: "Exact Title", ReleaseYear: &wrongYear}})
	if low.Reason != ReasonLowConfidence {
		t.Fatalf("year conflict was silently matched: %+v", low)
	}
}

func TestRankUsesAlternativeTitlesTranslationsAndReplaceableHanLayer(t *testing.T) {
	year := 2011
	parsed, err := Parse(InputFacts{PackageName: "後宮甄嬛傳 2011", MediaTypeHint: MediaTypeTV})
	if err != nil {
		t.Fatal(err)
	}
	decision := Rank(parsed, []RemoteCandidate{{ID: 1, MediaType: MediaTypeTV, Title: "Empresses in the Palace", AlternativeTitles: []string{"后宫甄嬛传"}, Translations: []string{"The Legend of Zhen Huan"}, ReleaseYear: &year}})
	if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 1 {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestRankAcceptsOneExactOriginalAliasWithoutInventedYearOrTypeEvidence(t *testing.T) {
	parsed, err := Parse(InputFacts{PackageName: "ULTRAMAN TIGA", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	decision := Rank(parsed, []RemoteCandidate{{ID: 10820, MediaType: MediaTypeTV, Title: "迪迦奥特曼", OriginalTitle: "ウルトラマンティガ", AlternativeTitles: []string{"Ultraman Tiga"}, Popularity: 80}})
	if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 10820 || decision.Confidence < DefaultScoreConfig().ExactTitleThreshold {
		t.Fatalf("decision=%+v", decision)
	}

	conflict := Rank(parsed, []RemoteCandidate{
		{ID: 10820, MediaType: MediaTypeTV, Title: "迪迦奥特曼", AlternativeTitles: []string{"Ultraman Tiga"}},
		{ID: 99999, MediaType: MediaTypeMovie, Title: "Ultraman Tiga"},
	})
	if conflict.Status != DecisionUnrecognized || conflict.Match != nil {
		t.Fatalf("exact identity conflict was silently accepted: %+v", conflict)
	}
}

func TestRankUsesExplicitFranchiseSubtitleAliasButNotOneWordSuffix(t *testing.T) {
	parsed, err := Parse(InputFacts{PackageName: "The Final Odyssey 1080p WEB-DL", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	decision := Rank(parsed, []RemoteCandidate{{ID: 54321, MediaType: MediaTypeMovie, Title: "Ultraman Tiga: The Final Odyssey", Popularity: 30}})
	if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 54321 {
		t.Fatalf("decision=%+v", decision)
	}
	if aliases := candidateSubtitleAliases("Example: Finale"); len(aliases) != 0 {
		t.Fatalf("one-word subtitle became broad alias: %v", aliases)
	}
}

func TestRankPrefersUniqueExactIdentityAmongRelatedFranchiseResults(t *testing.T) {
	parsed, err := Parse(InputFacts{PackageName: "迪迦·奥特曼 1080p", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	decision := Rank(parsed, []RemoteCandidate{
		{ID: 113094, MediaType: MediaTypeMovie, Title: "迪迦奥特曼 剧场版：最终圣战", Popularity: 3.46},
		{ID: 318718, MediaType: MediaTypeMovie, Title: "迪迦奥特曼·戴拿奥特曼&盖亚奥特曼 剧场版：超时空大决战", Popularity: 2.53},
		{ID: 2253, MediaType: MediaTypeTV, Title: "迪迦奥特曼", OriginalTitle: "ウルトラマンティガ", Popularity: 23.74},
	})
	if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 2253 {
		t.Fatalf("decision=%+v", decision)
	}

	conflict := Rank(parsed, []RemoteCandidate{
		{ID: 2253, MediaType: MediaTypeTV, Title: "迪迦奥特曼"},
		{ID: 9999, MediaType: MediaTypeMovie, Title: "迪迦·奥特曼"},
	})
	if conflict.Status != DecisionUnrecognized || conflict.Reason != ReasonCandidateConflict {
		t.Fatalf("duplicate exact identities were not rejected: %+v", conflict)
	}
}

func TestRankFranchiseDecisionIsStableAcrossRemoteCandidateOrder(t *testing.T) {
	parsed, err := Parse(InputFacts{PackageName: "迪迦·奥特曼 1080p", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	candidates := []RemoteCandidate{
		{ID: 113094, MediaType: MediaTypeMovie, Title: "迪迦奥特曼 剧场版：最终圣战", Popularity: 3.46},
		{ID: 318718, MediaType: MediaTypeMovie, Title: "迪迦奥特曼·戴拿奥特曼&盖亚奥特曼 剧场版：超时空大决战", Popularity: 2.53},
		{ID: 2253, MediaType: MediaTypeTV, Title: "迪迦奥特曼", OriginalTitle: "ウルトラマンティガ", Popularity: 23.74},
	}
	for _, input := range [][]RemoteCandidate{candidates, {candidates[2], candidates[1], candidates[0]}} {
		decision := Rank(parsed, input)
		if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 2253 {
			t.Fatalf("order-dependent decision=%+v input=%+v", decision, input)
		}
	}
}

func TestRankAcceptsOneBoundedLatinTypoButIgnoresRecallOnlyTokenIdentity(t *testing.T) {
	parsed, err := Parse(InputFacts{PackageName: "ULRAMAN+TIGA 1996 1080p", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	year := 1996
	decision := Rank(parsed, []RemoteCandidate{
		{ID: 1377374, MediaType: MediaTypeMovie, Title: "Tiga", ReleaseYear: &year},
		{ID: 2253, MediaType: MediaTypeTV, Title: "Ultraman Tiga", ReleaseYear: &year, Popularity: 23.74},
		{ID: 123417, MediaType: MediaTypeTV, Title: "Ultraman Trigger: New Generation Tiga", ReleaseYear: intRef(2021), Popularity: 8.87},
	})
	if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 2253 {
		t.Fatalf("decision=%+v queries=%+v", decision, parsed.Queries)
	}

	short, shortErr := Parse(InputFacts{PackageName: "STREM 1080p", SourceKind: SourceDownload})
	if shortErr != nil {
		t.Fatal(shortErr)
	}
	if unsafe := Rank(short, []RemoteCandidate{{ID: 1, MediaType: MediaTypeMovie, Title: "Stream"}}); unsafe.Status != DecisionUnrecognized {
		t.Fatalf("short one-token typo was accepted: %+v", unsafe)
	}
}

func TestRankUsesSeasonAirYearWithoutConflictingWithSeriesPremiereYear(t *testing.T) {
	parsed, err := Parse(InputFacts{PackageName: "Ai qing gong yu 2012 S03 2160p WEB-DL H.265", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Year != nil || parsed.SeasonYear == nil || *parsed.SeasonYear != 2012 || parsed.Season == nil || *parsed.Season != 3 {
		t.Fatalf("parsed=%+v", parsed)
	}
	seriesYear := 2009
	matching := Rank(parsed, []RemoteCandidate{{ID: 12345, MediaType: MediaTypeTV, Title: "爱情公寓", AlternativeTitles: []string{"Ai qing gong yu"}, ReleaseYear: &seriesYear, SeasonCount: intRef(5), SeasonYears: map[int]int{3: 2012}}})
	if matching.Status != DecisionMatched || matching.Match == nil || matching.Match.ID != 12345 {
		t.Fatalf("season year was treated as series premiere conflict: %+v", matching)
	}
	conflicting := Rank(parsed, []RemoteCandidate{{ID: 12345, MediaType: MediaTypeTV, Title: "爱情公寓", AlternativeTitles: []string{"Ai qing gong yu"}, ReleaseYear: &seriesYear, SeasonCount: intRef(5), SeasonYears: map[int]int{3: 2015}}})
	if conflicting.Status != DecisionUnrecognized || conflicting.Reason != ReasonLowConfidence {
		t.Fatalf("known conflicting season year was ignored: %+v", conflicting)
	}
}

func TestRankAcceptsSeriesPremiereYearBesideLaterSeasonWithoutTitleHardcoding(t *testing.T) {
	parsed, err := Parse(InputFacts{PackageName: "Example Series 2011 S03 1080p WEB-DL", SourceKind: SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	seriesYear := 2011
	decision := Rank(parsed, []RemoteCandidate{{ID: 77, MediaType: MediaTypeTV, Title: "Example Series", ReleaseYear: &seriesYear, SeasonCount: intRef(8), SeasonYears: map[int]int{3: 2013}}})
	if decision.Status != DecisionMatched || decision.Match == nil || decision.Match.ID != 77 {
		t.Fatalf("series premiere year beside season was rejected: %+v", decision)
	}
}

func episodePath(episode int) string {
	return "Ming Dynasty in 1566/Ming.Dynasty.in.1566.S01E" + twoDigits(episode) + ".mkv"
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + string(rune('0'+value))
	}
	return string([]rune{rune('0' + value/10), rune('0' + value%10)})
}
