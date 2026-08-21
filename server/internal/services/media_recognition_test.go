package services

import (
	"context"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

type recognitionLookupFake struct {
	searchTitle string
	directID    int64
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
