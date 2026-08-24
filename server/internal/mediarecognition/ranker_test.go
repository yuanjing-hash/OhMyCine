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

func episodePath(episode int) string {
	return "Ming Dynasty in 1566/Ming.Dynasty.in.1566.S01E" + twoDigits(episode) + ".mkv"
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + string(rune('0'+value))
	}
	return string([]rune{rune('0' + value/10), rune('0' + value%10)})
}
