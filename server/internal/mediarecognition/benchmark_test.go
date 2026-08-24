package mediarecognition

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestEmbeddedBenchmarkImprovesLegacyAndKeepsSafetyGate(t *testing.T) {
	corpus, err := LoadEmbeddedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	baseline := RunBenchmark(corpus, "legacy_first_result_v1", LegacyBaselineRecognizer)
	candidate := RunBenchmark(corpus, EngineVersion, CandidateBenchmarkRecognizer)
	if candidate.Metrics.Top1Accuracy <= baseline.Metrics.Top1Accuracy {
		t.Fatalf("candidate did not improve top-1: baseline=%+v candidate=%+v", baseline.Metrics, candidate.Metrics)
	}
	if candidate.Metrics.FalseMatches != 0 {
		t.Fatalf("candidate produced silent false matches: %+v", candidate.Metrics)
	}
	if candidate.Metrics.ParserAccuracy < .95 || candidate.Metrics.Top1Accuracy < .85 || candidate.Metrics.Top3RecallRate < .95 {
		t.Fatalf("candidate missed frozen quality gate: %+v", candidate.Metrics)
	}
	ming := findCaseResult(candidate, "tv-en-number-title-49-episodes-hq-release-group")
	if ming == nil || !ming.ParserCorrect || ming.MatchedID != 100 || ming.Decision != ReasonMatched {
		t.Fatalf("Ming Dynasty hard regression failed: %+v", ming)
	}
}

func TestBenchmarkReportsAreByteRepeatableAndOffline(t *testing.T) {
	corpus, err := LoadEmbeddedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	first := RunBenchmark(corpus, EngineVersion, CandidateBenchmarkRecognizer)
	second := RunBenchmark(corpus, EngineVersion, CandidateBenchmarkRecognizer)
	left, err := RenderBenchmarkJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := RenderBenchmarkJSON(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("same frozen corpus produced different reports")
	}
	if first.Metrics.ExternalRequests != 0 || first.Metrics.LatencyP50Micros != 0 || first.Metrics.LatencyP95Micros != 0 || first.Metrics.LatencyP99Micros != 0 {
		t.Fatalf("offline runner recorded external work: %+v", first.Metrics)
	}
	markdown := RenderBenchmarkMarkdown(first)
	if !strings.Contains(markdown, "MoviePilot v3") || !strings.Contains(markdown, "GPLv3 reference boundary") || !strings.Contains(markdown, "Current Server internals not claimed") {
		t.Fatalf("reference/license boundaries missing from report:\n%s", markdown)
	}
	for _, forbidden := range []string{`C:\`, "https://", "provider_id", "Authorization", "cookie"} {
		if strings.Contains(string(left), forbidden) {
			t.Fatalf("report contains forbidden diagnostic material %q", forbidden)
		}
	}
}

func TestBenchmarkMarkdownMatchesFrozenReports(t *testing.T) {
	corpus, err := LoadEmbeddedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		path       string
		engine     string
		recognizer BenchmarkRecognizer
	}{
		{path: "testdata/reports/baseline.v1.md", engine: "legacy_first_result_v1", recognizer: LegacyBaselineRecognizer},
		{path: "testdata/reports/candidate.v1.md", engine: EngineVersion, recognizer: CandidateBenchmarkRecognizer},
	}
	for _, check := range checks {
		golden, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatal(err)
		}
		actual := RenderBenchmarkMarkdown(RunBenchmark(corpus, check.engine, check.recognizer))
		if strings.TrimSpace(actual) != strings.TrimSpace(strings.ReplaceAll(string(golden), "\r\n", "\n")) {
			t.Fatalf("report %s drifted; rerun benchmark and review threshold/fixture changes\n--- actual ---\n%s", check.path, actual)
		}
	}
}

func findCaseResult(report BenchmarkReport, id string) *BenchmarkCaseResult {
	for index := range report.Cases {
		if report.Cases[index].ID == id {
			return &report.Cases[index]
		}
	}
	return nil
}
