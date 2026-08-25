package services

import (
	"context"
	"fmt"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

type recognitionLookupFake struct {
	searchTitle string
	directID    int64
}

type rankedRecognitionLookupFake struct {
	selectedID int64
	items      []tmdb.Candidate
}

type enrichedRecognitionLookupFake struct {
	rankedRecognitionLookupFake
	enriched        map[int64]tmdb.Candidate
	enrichmentCalls int
}

type embyHintLookupFake struct {
	calls []string
}

type titleSensitiveRecognitionLookupFake struct {
	searches []string
	selected int64
}

type nyaaRecognitionLookupFake struct {
	searches []string
}

func (f *nyaaRecognitionLookupFake) Search(context.Context, string, string, *int, string, string) (tmdb.Match, error) {
	return tmdb.Match{}, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
}

func (f *nyaaRecognitionLookupFake) SearchCandidates(_ context.Context, mediaType, title string, _ *int, _, _ string, _ int) ([]tmdb.Candidate, error) {
	f.searches = append(f.searches, mediaType+":"+title)
	if mediaType == "tv" && title == "迪迦奥特曼" {
		return []tmdb.Candidate{{ID: 10820, Title: "迪迦奥特曼", OriginalTitle: "ウルトラマンティガ", MediaType: "tv", SeasonCount: 1, Popularity: 100}}, nil
	}
	return nil, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
}

func (f *nyaaRecognitionLookupFake) GetByID(_ context.Context, mediaType string, id int64, _ string) (tmdb.Match, error) {
	return tmdb.Match{ID: id, Title: "迪迦奥特曼", MediaType: mediaType, Confidence: 1, Snapshot: tmdb.Snapshot{TMDBID: id, Title: "迪迦奥特曼", MediaType: mediaType}}, nil
}

func (f *titleSensitiveRecognitionLookupFake) Search(context.Context, string, string, *int, string, string) (tmdb.Match, error) {
	return tmdb.Match{}, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
}

func (f *titleSensitiveRecognitionLookupFake) SearchCandidates(_ context.Context, mediaType, title string, _ *int, _, _ string, _ int) ([]tmdb.Candidate, error) {
	f.searches = append(f.searches, mediaType+":"+title)
	if mediaType != "tv" || title != "斗罗大陆" {
		return nil, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
	}
	return []tmdb.Candidate{{ID: 95557, Title: "斗罗大陆", MediaType: "tv", SeasonCount: 1, Popularity: 100}}, nil
}

func (f *titleSensitiveRecognitionLookupFake) GetByID(_ context.Context, mediaType string, id int64, _ string) (tmdb.Match, error) {
	f.selected = id
	return tmdb.Match{ID: id, Title: "斗罗大陆", MediaType: mediaType, Confidence: 1}, nil
}

func (f *embyHintLookupFake) Search(context.Context, string, string, *int, string, string) (tmdb.Match, error) {
	return tmdb.Match{}, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
}

func (f *embyHintLookupFake) GetByID(_ context.Context, mediaType string, id int64, _ string) (tmdb.Match, error) {
	f.calls = append(f.calls, fmt.Sprintf("%s:%d", mediaType, id))
	if mediaType != "movie" {
		return tmdb.Match{}, &tmdb.ClientError{Code: tmdb.ErrorRequestFailed}
	}
	year := 1954
	return tmdb.Match{ID: id, Title: "Seven Samurai", MediaType: mediaType, ReleaseYear: &year, Confidence: 1, Snapshot: tmdb.Snapshot{OriginalTitle: "七人の侍"}}, nil
}

func (f *rankedRecognitionLookupFake) Search(context.Context, string, string, *int, string, string) (tmdb.Match, error) {
	return tmdb.Match{}, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
}

func (f *rankedRecognitionLookupFake) SearchCandidates(context.Context, string, string, *int, string, string, int) ([]tmdb.Candidate, error) {
	return append([]tmdb.Candidate(nil), f.items...), nil
}

func (f *rankedRecognitionLookupFake) GetByID(_ context.Context, mediaType string, id int64, _ string) (tmdb.Match, error) {
	f.selectedID = id
	year := 2007
	return tmdb.Match{ID: id, Title: "大明王朝1566", MediaType: mediaType, ReleaseYear: &year, Confidence: 1}, nil
}

func (f *enrichedRecognitionLookupFake) EnrichCandidates(_ context.Context, candidates []tmdb.Candidate, _ string, limit int) ([]tmdb.Candidate, error) {
	f.enrichmentCalls++
	result := append([]tmdb.Candidate(nil), candidates...)
	for index := range result {
		if replacement, exists := f.enriched[result[index].ID]; exists {
			result[index] = replacement
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (f *recognitionLookupFake) Search(_ context.Context, mediaType, title string, year *int, _, _ string) (tmdb.Match, error) {
	f.searchTitle = title
	return tmdb.Match{ID: 346, Title: "七武士", MediaType: mediaType, ReleaseYear: year, Confidence: .98, Snapshot: tmdb.Snapshot{Version: 1, TMDBID: 346, MediaType: mediaType, Title: "七武士", PosterPath: "/poster.jpg"}}, nil
}

func TestRecognitionMetadataEnvelopePersistsSnapshotAndReadsLegacyClassification(t *testing.T) {
	result := MediaRecognitionResult{Metadata: classification.Metadata{MediaType: classification.MediaTypeMovie, OriginalLanguage: "ja"}, Snapshot: tmdb.Snapshot{Version: 1, TMDBID: 346, MediaType: "movie", Title: "七武士", PosterPath: "/poster.jpg"}}
	raw, err := marshalRecognitionMetadata(result)
	if err != nil {
		t.Fatal(err)
	}
	metadata, snapshot, err := decodeRecognitionMetadata(raw)
	if err != nil || metadata.OriginalLanguage != "ja" || snapshot.TMDBID != 346 || snapshot.PosterPath != "/poster.jpg" {
		t.Fatalf("metadata=%+v snapshot=%+v err=%v", metadata, snapshot, err)
	}
	legacy, emptySnapshot, err := decodeRecognitionMetadata(`{"MediaType":"movie","OriginalLanguage":"ja"}`)
	if err != nil || legacy.OriginalLanguage != "ja" || emptySnapshot.TMDBID != 0 {
		t.Fatalf("legacy=%+v snapshot=%+v err=%v", legacy, emptySnapshot, err)
	}
}

func (f *recognitionLookupFake) GetByID(_ context.Context, mediaType string, id int64, _ string) (tmdb.Match, error) {
	f.directID = id
	return tmdb.Match{ID: id, Title: "精灵幻想记", MediaType: mediaType, Confidence: 1}, nil
}

func TestRecognizeMediaUsesFullReleaseNameAndValidatesDirectHint(t *testing.T) {
	lookup := &recognitionLookupFake{}
	result := recognizeMedia(context.Background(), lookup, MediaRecognitionRequest{
		PackageName:      "Seven.Samurai.1954.1080p.BluRay.x264",
		Files:            []recognitionSourceFile{{RelativePath: "Seven.Samurai.1954.1080p.BluRay.x264.mkv", Size: 10}},
		BuiltinPackCodes: mediarecognition.DefaultPackCodes(),
		Classification:   classification.EmptyRules(),
		Language:         "zh-CN",
		Region:           "CN",
	})
	if result.Status != mediaRecognitionStatusMatched || lookup.searchTitle != "Seven Samurai" || result.TMDBID == nil || *result.TMDBID != 346 {
		t.Fatalf("result=%+v search_title=%q", result, lookup.searchTitle)
	}

	direct := recognizeMedia(context.Background(), lookup, MediaRecognitionRequest{
		PackageName:      "ANi 精靈幻想記 2 01",
		Files:            []recognitionSourceFile{{RelativePath: "ANi 精靈幻想記 2 01.mkv", Size: 10}},
		BuiltinPackCodes: mediarecognition.DefaultPackCodes(),
		Classification:   classification.EmptyRules(),
		Language:         "zh-CN",
	})
	if direct.Status != mediaRecognitionStatusMatched || lookup.directID != 113808 || direct.SeasonHint == nil || *direct.SeasonHint != 2 {
		t.Fatalf("direct=%+v id=%d", direct, lookup.directID)
	}
}

func TestRecognizeMediaUsesEmbyTMDBHintAndRejectsUnsafeAbsoluteFacts(t *testing.T) {
	lookup := &embyHintLookupFake{}
	result := recognizeMedia(context.Background(), lookup, MediaRecognitionRequest{
		PackageName:      "Seven Samurai (1954) {tmdb-346}",
		Files:            []recognitionSourceFile{{RelativePath: "Seven Samurai (1954)/Seven Samurai.mkv", Size: 10}},
		SourceKind:       mediarecognition.SourceLibraryScan,
		MediaTypeHint:    "movie",
		BuiltinPackCodes: mediarecognition.DefaultPackCodes(),
		Classification:   classification.DefaultRules(),
		Language:         "en-US",
	})
	if result.Status != mediaRecognitionStatusMatched || result.TMDBID == nil || *result.TMDBID != 346 || len(lookup.calls) != 2 {
		t.Fatalf("result=%+v calls=%v", result, lookup.calls)
	}
	unsafe := recognizeMedia(context.Background(), lookup, MediaRecognitionRequest{
		PackageName:      "Seven Samurai",
		Files:            []recognitionSourceFile{{RelativePath: "/private/media/Seven Samurai.mkv", Size: 10}},
		BuiltinPackCodes: mediarecognition.DefaultPackCodes(),
		Classification:   classification.DefaultRules(),
	})
	if unsafe.ErrorCode != tmdb.ErrorInvalidRequest {
		t.Fatalf("absolute fact was sanitized into recognizer: %+v", unsafe)
	}
}

func TestRecognizeMediaRanksAllCandidatesInsteadOfTakingFirstResult(t *testing.T) {
	lookup := &rankedRecognitionLookupFake{items: []tmdb.Candidate{
		{ID: 9, Title: "Ming Dynasty", MediaType: "tv", Confidence: .82},
		{ID: 100, Title: "大明王朝1566", OriginalTitle: "Ming Dynasty in 1566", MediaType: "tv", Confidence: .98},
	}}
	files := make([]recognitionSourceFile, 0, 49)
	for episode := 1; episode <= 49; episode++ {
		files = append(files, recognitionSourceFile{RelativePath: fmt.Sprintf("Ming Dynasty in 1566/Ming.Dynasty.in.1566.S01E%02d.mkv", episode), Size: 1 << 30})
	}
	result := recognizeMedia(context.Background(), lookup, MediaRecognitionRequest{
		PackageName:      "Ming Dynasty in 1566 HQ -BlackTV",
		Files:            files,
		BuiltinPackCodes: mediarecognition.DefaultPackCodes(),
		Classification:   classification.DefaultRules(),
		Language:         "zh-CN",
		Region:           "CN",
	})
	if result.Status != mediaRecognitionStatusMatched || result.TMDBID == nil || *result.TMDBID != 100 || lookup.selectedID != 100 || result.MediaType != "tv" {
		t.Fatalf("result=%+v selected=%d", result, lookup.selectedID)
	}
}

func TestRecognizeMediaMatchesChineseEpisodeNamesFromLibraryScan(t *testing.T) {
	lookup := &titleSensitiveRecognitionLookupFake{}
	result := recognizeMedia(context.Background(), lookup, MediaRecognitionRequest{
		PackageName: "斗罗大陆 - - 第2集",
		Files: []recognitionSourceFile{
			{RelativePath: "/斗罗大陆/斗罗大陆 - - 第1集.mp4", Size: 10},
			{RelativePath: "/斗罗大陆/斗罗大陆 - - 第2集.mp4", Size: 20},
		},
		SourceKind:       mediarecognition.SourceLibraryScan,
		MediaTypeHint:    "tv",
		BuiltinPackCodes: mediarecognition.DefaultPackCodes(),
		Classification:   classification.DefaultRules(),
		Language:         "zh-CN",
		Region:           "CN",
	})
	if result.Status != mediaRecognitionStatusMatched || result.Title != "斗罗大陆" || result.MediaType != "tv" || result.TMDBID == nil || *result.TMDBID != 95557 || lookup.selected != 95557 {
		t.Fatalf("result=%+v searches=%v selected=%d", result, lookup.searches, lookup.selected)
	}
}

func TestRecognizeMediaUsesBuiltinPacksAndDomainParserForNyaaCompleteSeries(t *testing.T) {
	lookup := &nyaaRecognitionLookupFake{}
	result := recognizeMedia(context.Background(), lookup, MediaRecognitionRequest{
		PackageName:      "[DBD-Raws][迪迦奥特曼/Ultraman Tiga/ウルトラマンティガ][01-52TV全集+剧场+OV+特典][1080P][BDRip][HEVC-10bit][简体字幕外挂][FLAC][MKV]",
		Files:            []recognitionSourceFile{{RelativePath: "迪迦奥特曼/Ultraman.Tiga.EP01.mkv", Size: 10}},
		BuiltinPackCodes: mediarecognition.DefaultPackCodes(),
		Classification:   classification.DefaultRules(),
		Language:         "zh-CN",
		Region:           "CN",
	})
	if result.Status != mediaRecognitionStatusMatched || result.MediaType != "tv" || result.Title != "迪迦奥特曼" || result.TMDBID == nil || *result.TMDBID != 10820 {
		t.Fatalf("result=%+v searches=%v", result, lookup.searches)
	}
	found := false
	for _, search := range lookup.searches {
		if search == "tv:迪迦奥特曼" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("clean multilingual query missing: %v", lookup.searches)
	}
}

func TestDomainRecognitionSearchQueriesPrioritizeCanonicalVariantsAcrossSources(t *testing.T) {
	parsed := mediarecognition.ParsedFacts{Queries: []mediarecognition.QueryVariant{
		{Title: "Noisy Canonical", SuggestedType: mediarecognition.MediaTypeTV, Source: "filename", Reason: "canonical"},
		{Title: "Noisy without group", SuggestedType: mediarecognition.MediaTypeTV, Source: "filename", Reason: "without_release_group"},
		{Title: "Noisy raw", SuggestedType: mediarecognition.MediaTypeTV, Source: "filename", Reason: "raw"},
		{Title: "Clean Parent", SuggestedType: mediarecognition.MediaTypeTV, Source: "parent", Reason: "canonical"},
	}}
	queries := domainRecognitionSearchQueries(parsed)
	found := false
	for _, query := range queries {
		if query.Title == "Clean Parent" && query.MediaType == "tv" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("clean canonical source was excluded from bounded query budget: %+v", queries)
	}
}

func TestRecognizeMediaReranksBoundedEnrichedAliases(t *testing.T) {
	year2011, year1990 := 2011, 1990
	lookup := &enrichedRecognitionLookupFake{
		rankedRecognitionLookupFake: rankedRecognitionLookupFake{items: []tmdb.Candidate{
			{ID: 1, Title: "後宮甄嬛傳", MediaType: "tv", ReleaseYear: &year1990},
			{ID: 2, Title: "Empresses in the Palace", MediaType: "tv", ReleaseYear: &year2011},
		}},
		enriched: map[int64]tmdb.Candidate{
			1: {ID: 1, Title: "後宮甄嬛傳", MediaType: "tv", ReleaseYear: &year1990},
			2: {ID: 2, Title: "Empresses in the Palace", MediaType: "tv", ReleaseYear: &year2011, Translations: []string{"後宮甄嬛傳", "后宫甄嬛传"}, SeasonCount: 1},
		},
	}
	result := recognizeMedia(context.Background(), lookup, MediaRecognitionRequest{
		PackageName:      "後宮甄嬛傳.2011.S01E01.1080p",
		Files:            []recognitionSourceFile{{RelativePath: "後宮甄嬛傳/Season 01/後宮甄嬛傳.S01E01.mkv", Size: 10}},
		BuiltinPackCodes: mediarecognition.DefaultPackCodes(),
		Classification:   classification.DefaultRules(),
		Language:         "zh-CN",
		Region:           "CN",
	})
	if result.Status != mediaRecognitionStatusMatched || lookup.selectedID != 2 || lookup.enrichmentCalls != 1 {
		t.Fatalf("result=%+v selected=%d enrich_calls=%d", result, lookup.selectedID, lookup.enrichmentCalls)
	}
}

func TestRecognizeMediaReturnsDistinctAutomaticRejectionReasons(t *testing.T) {
	year2005 := 2005
	files := []recognitionSourceFile{{RelativePath: "The Office/Season 01/The.Office.S01E01.mkv", Size: 10}}
	request := MediaRecognitionRequest{PackageName: "The Office.2005.S01E01", Files: files, BuiltinPackCodes: mediarecognition.DefaultPackCodes(), Classification: classification.DefaultRules(), Language: "en-US", Region: "US"}
	for _, test := range []struct {
		name  string
		items []tmdb.Candidate
		code  string
	}{
		{name: "no match", items: nil, code: tmdb.ErrorNoMatch},
		{name: "low confidence", items: []tmdb.Candidate{{ID: 10, Title: "Entirely Unrelated", MediaType: "tv", ReleaseYear: &year2005}}, code: mediaRecognitionLowConfidence},
		{name: "candidate conflict", items: []tmdb.Candidate{{ID: 11, Title: "The Office", MediaType: "tv", ReleaseYear: &year2005}, {ID: 12, Title: "The Office", MediaType: "tv", ReleaseYear: &year2005}}, code: mediaRecognitionCandidateConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			lookup := &rankedRecognitionLookupFake{items: test.items}
			result := recognizeMedia(context.Background(), lookup, request)
			if result.Status != mediaRecognitionStatusUnrecognized || result.ErrorCode != test.code || lookup.selectedID != 0 {
				t.Fatalf("result=%+v selected=%d", result, lookup.selectedID)
			}
		})
	}
}

func TestDomainRecognitionSearchQueriesReserveBoundedYearFallbacks(t *testing.T) {
	year := 2007
	parsed := mediarecognition.ParsedFacts{Queries: []mediarecognition.QueryVariant{
		{Title: "Ming Dynasty in 1566", Year: &year, SuggestedType: mediarecognition.MediaTypeTV},
		{Title: "大明王朝1566", Year: &year, SuggestedType: mediarecognition.MediaTypeTV},
		{Title: "Ming Dynasty", Year: &year, SuggestedType: mediarecognition.MediaTypeTV},
		{Title: "unused", Year: &year, SuggestedType: mediarecognition.MediaTypeTV},
	}}
	queries := domainRecognitionSearchQueries(parsed)
	if len(queries) > mediaRecognitionMaxQueries {
		t.Fatalf("queries=%d", len(queries))
	}
	want := map[string]bool{"tv:2007": false, "movie:2007": false, "tv:2006": false, "tv:2008": false, "tv:none": false}
	for _, query := range queries {
		if query.Title != "Ming Dynasty in 1566" {
			continue
		}
		yearKey := "none"
		if query.Year != nil {
			yearKey = fmt.Sprint(*query.Year)
		}
		key := query.MediaType + ":" + yearKey
		if _, exists := want[key]; exists {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Fatalf("missing fallback %s in %+v", key, queries)
		}
	}
}

func TestParseBuiltinPackCodesDistinguishesLegacyDefaultAndExplicitEmpty(t *testing.T) {
	legacy, err := parseBuiltinPackCodes("")
	if err != nil || len(legacy) != 2 {
		t.Fatalf("legacy=%v err=%v", legacy, err)
	}
	disabled, err := parseBuiltinPackCodes("[]")
	if err != nil || len(disabled) != 0 {
		t.Fatalf("disabled=%v err=%v", disabled, err)
	}
}
