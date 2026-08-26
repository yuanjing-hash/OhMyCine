package services

import (
	"context"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/pkg/aiprovider"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

type recognitionAIAssistFake struct {
	arbitration aiprovider.CandidateArbitrationResult
	rewrite     aiprovider.TitleRewriteResult
	arbitrates  int
	rewrites    int
	basenames   bool
}

func (f *recognitionAIAssistFake) GenerateCandidateArbitration(_ context.Context, _ aiprovider.CandidateArbitrationPayload) (aiprovider.CandidateArbitrationResult, error) {
	f.arbitrates++
	return f.arbitration, nil
}

func (f *recognitionAIAssistFake) GenerateTitleRewrite(_ context.Context, _ aiprovider.TitleRewritePayload) (aiprovider.TitleRewriteResult, error) {
	f.rewrites++
	return f.rewrite, nil
}

func (f *recognitionAIAssistFake) RuntimeRelativeBasenamesEnabled() bool { return f.basenames }

func TestRecognitionAIAssistArbitratesOnlyProvisionalCandidates(t *testing.T) {
	parsed, err := mediarecognition.Parse(mediarecognition.InputFacts{PackageName: "Example Show S01E01", SourceKind: mediarecognition.SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	lookup := &rankedRecognitionLookupFake{items: []tmdb.Candidate{
		{ID: 10, Title: "Example Show", MediaType: "tv", SeasonCount: 1, EpisodeCount: 10},
		{ID: 20, Title: "Example Show", MediaType: "tv", SeasonCount: 1, EpisodeCount: 10},
	}}
	assist := &recognitionAIAssistFake{arbitration: aiprovider.CandidateArbitrationResult{Action: "select", CandidateRef: "c2"}}
	match, reason, source, err := recognizeFromDomainCandidatesAssisted(context.Background(), lookup, lookup, parsed, MediaRecognitionRequest{Language: "zh-CN", Region: "CN", AIAssist: assist})
	if err != nil || reason != mediarecognition.ReasonCandidateConflict || source != mediaIdentitySourceAI || match.ID != 20 || assist.arbitrates != 1 || assist.rewrites != 0 {
		t.Fatalf("match=%+v reason=%s source=%s err=%v calls=%d/%d", match, reason, source, err, assist.arbitrates, assist.rewrites)
	}
}

func TestRecognitionAIAssistRewritesNoCandidateOnce(t *testing.T) {
	parsed, err := mediarecognition.Parse(mediarecognition.InputFacts{PackageName: "[Group] Very Noisy Release 1080p", SourceKind: mediarecognition.SourceDownload})
	if err != nil {
		t.Fatal(err)
	}
	lookup := &rankedRecognitionLookupFake{searchItems: map[string][]tmdb.Candidate{
		"movie:Correct Title": {{ID: 30, Title: "Correct Title", MediaType: "movie"}},
	}}
	assist := &recognitionAIAssistFake{rewrite: aiprovider.TitleRewriteResult{Action: "search", PrimaryTitle: "Correct Title", MediaType: "movie", SearchQueries: []aiprovider.SearchQuery{{Title: "Correct Title", MediaType: "movie", LanguageHint: "zh-CN"}}}}
	match, _, source, err := recognizeFromDomainCandidatesAssisted(context.Background(), lookup, lookup, parsed, MediaRecognitionRequest{PackageName: "[Group] Very Noisy Release 1080p", Language: "zh-CN", Region: "CN", AIAssist: assist})
	if err != nil || source != mediaIdentitySourceAI || match.ID != 30 || assist.arbitrates != 0 || assist.rewrites != 1 {
		t.Fatalf("match=%+v source=%s err=%v calls=%d/%d", match, source, err, assist.arbitrates, assist.rewrites)
	}
}

func TestRecognitionAIAssistDoesNotRunForVerifiedDecision(t *testing.T) {
	parsed, err := mediarecognition.Parse(mediarecognition.InputFacts{PackageName: "Exact Movie 2024", SourceKind: mediarecognition.SourceDownload, MediaTypeHint: mediarecognition.MediaTypeMovie})
	if err != nil {
		t.Fatal(err)
	}
	year := 2024
	lookup := &rankedRecognitionLookupFake{items: []tmdb.Candidate{{ID: 40, Title: "Exact Movie", MediaType: "movie", ReleaseYear: &year}}}
	assist := &recognitionAIAssistFake{}
	_, reason, source, err := recognizeFromDomainCandidatesAssisted(context.Background(), lookup, lookup, parsed, MediaRecognitionRequest{Language: "zh-CN", Region: "CN", AIAssist: assist})
	if err != nil || reason != mediarecognition.ReasonMatched || source != mediaIdentitySourceAutomatic || assist.arbitrates != 0 || assist.rewrites != 0 {
		t.Fatalf("reason=%s source=%s err=%v calls=%d/%d", reason, source, err, assist.arbitrates, assist.rewrites)
	}
}
