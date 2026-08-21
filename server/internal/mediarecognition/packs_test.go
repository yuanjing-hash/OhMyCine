package mediarecognition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dlclark/regexp2"
)

func TestMain(m *testing.M) {
	regexp2.SetTimeoutCheckPeriod(time.Millisecond)
	code := m.Run()
	regexp2.StopTimeoutClock()
	os.Exit(code)
}

func TestPackMetadataAndSnapshots(t *testing.T) {
	descriptors := Descriptors()
	if len(descriptors) != 2 {
		t.Fatalf("descriptor count = %d, want 2", len(descriptors))
	}
	wantCodes := []string{PackCodeTV, PackCodeAnime}
	if got := DefaultPackCodes(); !reflect.DeepEqual(got, wantCodes) {
		t.Fatalf("DefaultPackCodes() = %#v, want %#v", got, wantCodes)
	}

	wantHashes := map[string]string{
		PackCodeTV:    "a9b66a2eb7f5321ba79cee9404b255cbece80645e82b681258d89b3cd5dab8c8",
		PackCodeAnime: "a532a0ace2e9ea63ddfab6e5f5fe062c162ff47b1cc6a50fd00661e503968cc9",
	}
	for _, descriptor := range descriptors {
		text, err := snapshotForPack(descriptor.Code)
		if err != nil {
			t.Fatalf("snapshotForPack(%q): %v", descriptor.Code, err)
		}
		hash := sha256.Sum256([]byte(text))
		if got := hex.EncodeToString(hash[:]); got != wantHashes[descriptor.Code] {
			t.Errorf("snapshot %q hash = %s", descriptor.Code, got)
		}
		if got := countActiveLines(text); got != descriptor.RuleCount {
			t.Errorf("snapshot %q rules = %d, descriptor says %d", descriptor.Code, got, descriptor.RuleCount)
		}
	}

	// Returned descriptors and defaults are defensive copies.
	descriptors[0].Code = "mutated"
	defaults := DefaultPackCodes()
	defaults[0] = "mutated"
	if Descriptors()[0].Code != PackCodeTV || DefaultPackCodes()[0] != PackCodeTV {
		t.Fatal("pack metadata was mutable through returned slices")
	}
}

func TestSourcesManifestAndLicenseAreEmbedded(t *testing.T) {
	manifestBytes, err := snapshotFS.ReadFile("snapshots/sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SchemaVersion int `json:"schema_version"`
		Packs         []struct {
			Code      string `json:"code"`
			Commit    string `json:"commit"`
			RuleCount int    `json:"rule_count"`
		} `json:"packs"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse sources manifest: %v", err)
	}
	if manifest.SchemaVersion != 1 || len(manifest.Packs) != 2 {
		t.Fatalf("unexpected sources manifest: %#v", manifest)
	}
	if manifest.Packs[0].Commit != "f99c1b0bfd6721a727260e3e41e7d0bca73af8c7" ||
		manifest.Packs[1].Commit != "8f26b5b48ac1a863cae97dd67689d05433394349" {
		t.Fatalf("source commits are not pinned: %#v", manifest.Packs)
	}
	license, err := snapshotFS.ReadFile("snapshots/LICENSE.MoviePilot-Help")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(license)
	if got := hex.EncodeToString(hash[:]); got != "7d9ac26c5f09b108327e9fb83602ad62d8871c6dba0e122337f97ca16a07c665" {
		t.Fatalf("embedded upstream license hash = %s", got)
	}
}

func TestNormalizePackCodes(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    []string
		wantErr bool
	}{
		{name: "empty disables all", input: nil, want: []string{}},
		{name: "canonicalizes order", input: []string{PackCodeAnime, PackCodeTV}, want: []string{PackCodeTV, PackCodeAnime}},
		{name: "one pack", input: []string{PackCodeAnime}, want: []string{PackCodeAnime}},
		{name: "unknown", input: []string{"future-v1"}, wantErr: true},
		{name: "whitespace is not silently accepted", input: []string{" tv-v1"}, wantErr: true},
		{name: "duplicate", input: []string{PackCodeTV, PackCodeTV}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizePackCodes(test.input)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidPackCodes) {
					t.Fatalf("error = %v, want ErrInvalidPackCodes", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("NormalizePackCodes() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestEveryBuiltinRuleParsesAndCompiles(t *testing.T) {
	processor, err := NewBuiltinWordProcessor(DefaultPackCodes(), Limits{})
	if err != nil {
		t.Fatalf("compile all built-in rules: %v", err)
	}
	if len(processor.rules) != 322 {
		t.Fatalf("compiled rules = %d, want 322", len(processor.rules))
	}
	directHints := 0
	offsetRules := 0
	for index, rule := range processor.rules {
		wantPack := PackCodeTV
		if index >= 28 {
			wantPack = PackCodeAnime
		}
		if rule.packCode != wantPack {
			t.Fatalf("rule %d pack = %q, want %q", index, rule.packCode, wantPack)
		}
		if rule.directHint != nil {
			directHints++
		}
		if rule.offset != nil {
			offsetRules++
		}
	}
	if directHints != 72 {
		t.Fatalf("compiled direct hints = %d, want 72", directHints)
	}
	if offsetRules != 38 {
		t.Fatalf("compiled episode offsets = %d, want 38", offsetRules)
	}
}

func TestBuiltinLookaroundBackreferenceDirectHintAndOffset(t *testing.T) {
	processor, err := NewBuiltinWordProcessor(DefaultPackCodes(), Limits{})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("variable lookbehind and direct TMDB hint", func(t *testing.T) {
		result, err := processor.Apply(context.Background(), "ANi.group.精灵幻想记 2")
		if err != nil {
			t.Fatal(err)
		}
		if result.Title != "ANi.group.精灵幻想记" {
			t.Fatalf("title = %q", result.Title)
		}
		if result.Hint == nil || result.Hint.TMDBID != 113808 || result.Hint.MediaType != "tv" ||
			result.Hint.Season == nil || *result.Hint.Season != 2 {
			t.Fatalf("hint = %#v", result.Hint)
		}
	})

	t.Run("replacement backreference", func(t *testing.T) {
		result, err := processor.Apply(context.Background(), "转生贵族凭鉴定技能扭转人生 第二季 - 13")
		if err != nil {
			t.Fatal(err)
		}
		if result.Title != "转生贵族靠着鉴定技能一飞冲天 S01E13" {
			t.Fatalf("title = %q", result.Title)
		}
	})

	t.Run("upstream replacement without operator spaces", func(t *testing.T) {
		result, err := processor.Apply(context.Background(), "夜晚游玩生活！")
		if err != nil {
			t.Fatal(err)
		}
		if result.Title != "" || result.Hint == nil || result.Hint.TMDBID != 249891 {
			t.Fatalf("result = %#v", result)
		}
	})

	t.Run("combined replacement and episode offset", func(t *testing.T) {
		result, err := processor.Apply(context.Background(), "MF Ghost S02E13 2024")
		if err != nil {
			t.Fatal(err)
		}
		if result.Title != "MF Ghost.S01E25 2024" {
			t.Fatalf("title = %q", result.Title)
		}
	})
}

func TestCustomRuleOrderingAndRuleKinds(t *testing.T) {
	processor, err := CompileWordProcessor([]string{
		"remove-me",
		"Alpha => Beta",
		"Beta => Gamma",
		"E <> END >> EP+2",
	}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.Apply(context.Background(), "Alpha remove-me E03 END")
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "Gamma  E05 END" {
		t.Fatalf("title = %q", result.Title)
	}
	if len(result.Applied) != 4 {
		t.Fatalf("applied = %#v", result.Applied)
	}
}

func TestCombinedOffsetDoesNotRewriteDirectHintNumbers(t *testing.T) {
	processor, err := CompileWordProcessor([]string{
		`Show - (\d+) => Show E\1 {[tmdbid=82684;type=tv;s=3]} && Show E <>  >> EP-48`,
	}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.Apply(context.Background(), "Show - 49")
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "Show E1" {
		t.Fatalf("title = %q", result.Title)
	}
	if result.Hint == nil || result.Hint.TMDBID != 82684 || result.Hint.Season == nil || *result.Hint.Season != 3 {
		t.Fatalf("hint = %#v", result.Hint)
	}
}

func TestStrictCompileErrors(t *testing.T) {
	tests := []struct {
		name string
		rule string
		code ErrorCode
	}{
		{name: "invalid regex", rule: "[ => value", code: ErrorRegexCompile},
		{name: "partial combined", rule: "a => b && c", code: ErrorInvalidRule},
		{name: "missing capture", rule: "a => \\1", code: ErrorInvalidRule},
		{name: "invalid direct ID", rule: "a => {[tmdbid=nope;type=tv]}", code: ErrorInvalidDirectHint},
		{name: "unknown direct field", rule: "a => {[tmdbid=1;path=secret]}", code: ErrorInvalidDirectHint},
		{name: "implicit multiplication", rule: "E <> END >> 2EP", code: ErrorInvalidRule},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileWordProcessor([]string{test.rule}, Limits{})
			var processingErr *ProcessingError
			if !errors.As(err, &processingErr) || processingErr.Code != test.code {
				t.Fatalf("error = %v, want code %s", err, test.code)
			}
		})
	}
}

func TestExecutionBoundsAndStableErrors(t *testing.T) {
	t.Run("input length", func(t *testing.T) {
		processor, err := CompileWordProcessor([]string{"a"}, Limits{MaxInputRunes: 8})
		if err != nil {
			t.Fatal(err)
		}
		_, err = processor.Apply(context.Background(), "123456789")
		assertProcessingCode(t, err, ErrorInputTooLong)
	})

	t.Run("match applications", func(t *testing.T) {
		processor, err := CompileWordProcessor([]string{"a => b"}, Limits{MaxMatchesPerRule: 4})
		if err != nil {
			t.Fatal(err)
		}
		_, err = processor.Apply(context.Background(), "aaaaa")
		assertProcessingCode(t, err, ErrorApplyLimit)
	})

	t.Run("canceled context", func(t *testing.T) {
		processor, err := CompileWordProcessor([]string{"a => b"}, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = processor.Apply(ctx, "a")
		assertProcessingCode(t, err, ErrorContextCanceled)
	})

	t.Run("catastrophic backtracking timeout", func(t *testing.T) {
		processor, err := CompileWordProcessor([]string{"(a+)+$ => blocked"}, Limits{
			MaxInputRunes: 4096,
			MatchTimeout:  2 * time.Millisecond,
			TotalTimeout:  200 * time.Millisecond,
		})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		_, err = processor.Apply(context.Background(), strings.Repeat("a", 1024)+"!")
		assertProcessingCode(t, err, ErrorMatchTimeout)
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("catastrophic match took %s", elapsed)
		}
	})

	t.Run("conflicting direct hints", func(t *testing.T) {
		processor, err := CompileWordProcessor([]string{
			"A => A{[tmdbid=1;type=tv]}",
			"A => A{[tmdbid=2;type=tv]}",
		}, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = processor.Apply(context.Background(), "A")
		assertProcessingCode(t, err, ErrorInvalidDirectHint)
	})

	t.Run("media title cannot inject direct hint", func(t *testing.T) {
		processor, err := CompileWordProcessor(nil, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		_, err = processor.Apply(context.Background(), "Movie {[tmdbid=1;type=movie]}")
		assertProcessingCode(t, err, ErrorInvalidDirectHint)
	})
}

func assertProcessingCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var processingErr *ProcessingError
	if !errors.As(err, &processingErr) {
		t.Fatalf("error = %v, want ProcessingError", err)
	}
	if processingErr.Code != want {
		t.Fatalf("error code = %q, want %q", processingErr.Code, want)
	}
	if strings.Contains(processingErr.Error(), "123456789") {
		t.Fatalf("error leaked media title: %v", processingErr)
	}
}

func countActiveLines(text string) int {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			count++
		}
	}
	return count
}
