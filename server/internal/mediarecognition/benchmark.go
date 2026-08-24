package mediarecognition

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

//go:embed testdata/corpus.v1.json
var embeddedCorpusV1 []byte

type FixturePolicy string

const (
	FixtureMustMatch  FixturePolicy = "must_match"
	FixtureMustReject FixturePolicy = "must_reject"
)

type BenchmarkCorpus struct {
	Version             string              `json:"version"`
	ReferenceBoundaries []ReferenceBoundary `json:"reference_boundaries"`
	Cases               []BenchmarkFixture  `json:"cases"`
}

type ReferenceBoundary struct {
	Name    string `json:"name"`
	Basis   string `json:"basis"`
	License string `json:"license,omitempty"`
}

type BenchmarkFixture struct {
	ID         string            `json:"id"`
	Input      InputFacts        `json:"input"`
	Candidates []RemoteCandidate `json:"candidates"`
	Expected   FixtureExpected   `json:"expected"`
	Policy     FixturePolicy     `json:"policy"`
}

type FixtureExpected struct {
	MediaType      MediaType `json:"media_type,omitempty"`
	CanonicalTitle string    `json:"canonical_title,omitempty"`
	Year           *int      `json:"year,omitempty"`
	RemoteID       int64     `json:"remote_id,omitempty"`
}

type BenchmarkOutcome struct {
	Parsed           ParsedFacts
	Decision         Decision
	TopCandidateIDs  []int64
	ExternalRequests int
	LatencyMicros    int64
}

type BenchmarkRecognizer func(BenchmarkFixture) BenchmarkOutcome

type BenchmarkMetrics struct {
	Cases                 int     `json:"cases"`
	MustMatch             int     `json:"must_match"`
	MustReject            int     `json:"must_reject"`
	ParserCorrect         int     `json:"parser_correct"`
	Top1Correct           int     `json:"top1_correct"`
	Top3Recall            int     `json:"top3_recall"`
	FalseMatches          int     `json:"false_matches"`
	UnrecognizedMustMatch int     `json:"unrecognized_must_match"`
	ParserAccuracy        float64 `json:"parser_accuracy"`
	Top1Accuracy          float64 `json:"top1_accuracy"`
	Top3RecallRate        float64 `json:"top3_recall_rate"`
	FalseMatchRate        float64 `json:"false_match_rate"`
	UnrecognizedRate      float64 `json:"unrecognized_rate"`
	ExternalRequests      int     `json:"external_requests"`
	LatencyP50Micros      int64   `json:"latency_p50_micros"`
	LatencyP95Micros      int64   `json:"latency_p95_micros"`
	LatencyP99Micros      int64   `json:"latency_p99_micros"`
}

type BenchmarkCaseResult struct {
	ID              string         `json:"id"`
	Policy          FixturePolicy  `json:"policy"`
	ParserCorrect   bool           `json:"parser_correct"`
	Decision        DecisionReason `json:"decision"`
	MatchedID       int64          `json:"matched_id,omitempty"`
	ExpectedID      int64          `json:"expected_id,omitempty"`
	TopCandidateIDs []int64        `json:"top_candidate_ids,omitempty"`
}

type BenchmarkReport struct {
	SchemaVersion        string                `json:"schema_version"`
	CorpusVersion        string                `json:"corpus_version"`
	Engine               string                `json:"engine"`
	OfflineDeterministic bool                  `json:"offline_deterministic"`
	Metrics              BenchmarkMetrics      `json:"metrics"`
	Cases                []BenchmarkCaseResult `json:"cases"`
	ReferenceBoundaries  []ReferenceBoundary   `json:"reference_boundaries"`
}

func LoadEmbeddedCorpus() (BenchmarkCorpus, error) {
	var corpus BenchmarkCorpus
	if err := json.Unmarshal(embeddedCorpusV1, &corpus); err != nil {
		return corpus, fmt.Errorf("decode recognition corpus: %w", err)
	}
	if corpus.Version == "" || len(corpus.Cases) == 0 {
		return corpus, errorsNewCorpus("missing version or cases")
	}
	seen := make(map[string]struct{}, len(corpus.Cases))
	for _, fixture := range corpus.Cases {
		if fixture.ID == "" || fixture.Policy != FixtureMustMatch && fixture.Policy != FixtureMustReject {
			return BenchmarkCorpus{}, errorsNewCorpus("invalid fixture identity or policy")
		}
		if _, exists := seen[fixture.ID]; exists {
			return BenchmarkCorpus{}, errorsNewCorpus("duplicate fixture ID")
		}
		seen[fixture.ID] = struct{}{}
		if _, err := Parse(fixture.Input); err != nil {
			return BenchmarkCorpus{}, fmt.Errorf("invalid fixture %s: %w", fixture.ID, err)
		}
	}
	return corpus, nil
}

func errorsNewCorpus(summary string) error {
	return fmt.Errorf("invalid recognition corpus: %s", summary)
}

func RunBenchmark(corpus BenchmarkCorpus, engine string, recognize BenchmarkRecognizer) BenchmarkReport {
	report := BenchmarkReport{
		SchemaVersion:        "recognition-benchmark-v1",
		CorpusVersion:        corpus.Version,
		Engine:               boundedCode(engine),
		OfflineDeterministic: true,
		Cases:                make([]BenchmarkCaseResult, 0, len(corpus.Cases)),
		ReferenceBoundaries:  append([]ReferenceBoundary(nil), corpus.ReferenceBoundaries...),
	}
	latencies := make([]int64, 0, len(corpus.Cases))
	for _, fixture := range corpus.Cases {
		outcome := recognize(fixture)
		caseResult := BenchmarkCaseResult{ID: fixture.ID, Policy: fixture.Policy, ExpectedID: fixture.Expected.RemoteID, Decision: outcome.Decision.Reason, TopCandidateIDs: boundedIDs(outcome.TopCandidateIDs, 5)}
		caseResult.ParserCorrect = parserMatchesExpected(outcome.Parsed, fixture.Expected)
		if outcome.Decision.Match != nil {
			caseResult.MatchedID = outcome.Decision.Match.ID
		}
		report.Cases = append(report.Cases, caseResult)
		report.Metrics.Cases++
		if caseResult.ParserCorrect {
			report.Metrics.ParserCorrect++
		}
		if fixture.Policy == FixtureMustMatch {
			report.Metrics.MustMatch++
			if caseResult.MatchedID == fixture.Expected.RemoteID && outcome.Decision.Status == DecisionMatched {
				report.Metrics.Top1Correct++
			}
			if containsID(caseResult.TopCandidateIDs, fixture.Expected.RemoteID, 3) {
				report.Metrics.Top3Recall++
			}
			if outcome.Decision.Status != DecisionMatched {
				report.Metrics.UnrecognizedMustMatch++
			} else if caseResult.MatchedID != fixture.Expected.RemoteID {
				report.Metrics.FalseMatches++
			}
		} else {
			report.Metrics.MustReject++
			if outcome.Decision.Status == DecisionMatched {
				report.Metrics.FalseMatches++
			}
		}
		report.Metrics.ExternalRequests += outcome.ExternalRequests
		latencies = append(latencies, maxInt64(0, outcome.LatencyMicros))
	}
	report.Metrics.ParserAccuracy = ratio(report.Metrics.ParserCorrect, report.Metrics.Cases)
	report.Metrics.Top1Accuracy = ratio(report.Metrics.Top1Correct, report.Metrics.MustMatch)
	report.Metrics.Top3RecallRate = ratio(report.Metrics.Top3Recall, report.Metrics.MustMatch)
	report.Metrics.FalseMatchRate = ratio(report.Metrics.FalseMatches, report.Metrics.Cases)
	report.Metrics.UnrecognizedRate = ratio(report.Metrics.UnrecognizedMustMatch, report.Metrics.MustMatch)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	report.Metrics.LatencyP50Micros = percentile(latencies, .50)
	report.Metrics.LatencyP95Micros = percentile(latencies, .95)
	report.Metrics.LatencyP99Micros = percentile(latencies, .99)
	return report
}

func CandidateBenchmarkRecognizer(fixture BenchmarkFixture) BenchmarkOutcome {
	parsed, err := Parse(fixture.Input)
	if err != nil {
		return BenchmarkOutcome{Decision: Decision{Status: DecisionUnrecognized, Reason: ReasonNoMatch}}
	}
	decision := Rank(parsed, fixture.Candidates)
	return BenchmarkOutcome{Parsed: parsed, Decision: decision, TopCandidateIDs: rankedIDs(decision.Ranked, 5)}
}

// LegacyBaselineRecognizer is a synthetic proxy for the former
// clean-and-first-result strategy. It is intentionally not reported as a
// byte-for-byte execution of the historical Server pipeline: that pipeline
// lived in the services package and also depended on TMDB responses. This
// proxy remains useful for deterministic local development comparisons only.
func LegacyBaselineRecognizer(fixture BenchmarkFixture) BenchmarkOutcome {
	parsed := ParsedFacts{EngineVersion: "legacy_first_result_v1", CanonicalTitle: legacyTitle(fixture.Input.PackageName), SuggestedType: fixture.Input.MediaTypeHint, Year: cloneDomainInt(fixture.Input.YearHint)}
	if parsed.SuggestedType == MediaTypeUnknown {
		if strings.Contains(strings.ToLower(fixture.Input.PackageName), "s01e") || len(fixture.Input.Files) >= 3 {
			parsed.SuggestedType = MediaTypeTV
		} else {
			parsed.SuggestedType = MediaTypeMovie
		}
	}
	decision := Decision{Status: DecisionUnrecognized, Reason: ReasonNoMatch}
	if len(fixture.Candidates) > 0 && TitleSimilarity(parsed.CanonicalTitle, fixture.Candidates[0].Title, nil) >= .90 {
		candidate := fixture.Candidates[0]
		decision = Decision{Status: DecisionMatched, Reason: ReasonMatched, Match: &candidate, Confidence: 1}
	}
	ids := make([]int64, 0, len(fixture.Candidates))
	for _, candidate := range fixture.Candidates {
		ids = append(ids, candidate.ID)
	}
	return BenchmarkOutcome{Parsed: parsed, Decision: decision, TopCandidateIDs: boundedIDs(ids, 5)}
}

func legacyTitle(value string) string {
	value = normalizeFilename(value)
	legacyTech := regexpLegacyTech.ReplaceAllString(value, " ")
	legacyTech = episodePattern.ReplaceAllString(legacyTech, " ")
	legacyTech = subtitleTokenPattern.ReplaceAllString(legacyTech, " ")
	return cleanTitleSurface(legacyTech)
}

var regexpLegacyTech = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|uhd|bluray|web[- .]?dl|webrip|hdtv|x264|x265|h\.?264|h\.?265|hevc|aac|dts|hdr|10bit|8bit)\b`)

func RenderBenchmarkJSON(report BenchmarkReport) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func RenderBenchmarkMarkdown(report BenchmarkReport) string {
	var output strings.Builder
	fmt.Fprintf(&output, "# Media recognition benchmark: %s\n\n", report.Engine)
	fmt.Fprintf(&output, "Corpus `%s`; offline deterministic: `%t`.\n\n", report.CorpusVersion, report.OfflineDeterministic)
	output.WriteString("| Metric | Value |\n|---|---:|\n")
	fmt.Fprintf(&output, "| Parser accuracy | %.4f |\n", report.Metrics.ParserAccuracy)
	fmt.Fprintf(&output, "| Top-1 accuracy | %.4f |\n", report.Metrics.Top1Accuracy)
	fmt.Fprintf(&output, "| Top-3 recall | %.4f |\n", report.Metrics.Top3RecallRate)
	fmt.Fprintf(&output, "| False-match rate | %.4f |\n", report.Metrics.FalseMatchRate)
	fmt.Fprintf(&output, "| Unrecognized rate | %.4f |\n", report.Metrics.UnrecognizedRate)
	fmt.Fprintf(&output, "| External requests | %d |\n", report.Metrics.ExternalRequests)
	fmt.Fprintf(&output, "| Latency p50/p95/p99 (µs) | %d / %d / %d |\n", report.Metrics.LatencyP50Micros, report.Metrics.LatencyP95Micros, report.Metrics.LatencyP99Micros)
	output.WriteString("\nReference boundaries:\n")
	for _, reference := range report.ReferenceBoundaries {
		fmt.Fprintf(&output, "- %s: %s", reference.Name, reference.Basis)
		if reference.License != "" {
			fmt.Fprintf(&output, " (%s)", reference.License)
		}
		output.WriteByte('\n')
	}
	return output.String()
}

func parserMatchesExpected(parsed ParsedFacts, expected FixtureExpected) bool {
	return comparisonKey(parsed.CanonicalTitle) == comparisonKey(expected.CanonicalTitle) && parsed.SuggestedType == expected.MediaType && sameOptionalBenchmarkInt(parsed.Year, expected.Year)
}

func sameOptionalBenchmarkInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func rankedIDs(ranked []RankedCandidate, limit int) []int64 {
	ids := make([]int64, 0, minInt(len(ranked), limit))
	for _, candidate := range ranked {
		if len(ids) >= limit {
			break
		}
		ids = append(ids, candidate.Candidate.ID)
	}
	return ids
}

func boundedIDs(ids []int64, limit int) []int64 {
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return append([]int64(nil), ids...)
}

func containsID(ids []int64, expected int64, limit int) bool {
	for index, id := range ids {
		if index >= limit {
			break
		}
		if id == expected {
			return true
		}
	}
	return false
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func percentile(sorted []int64, percentile float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*percentile)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
