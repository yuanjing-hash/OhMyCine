package mediarecognition

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
)

const customRuleSetCode = "custom"

const maxRuleRunes = 4096

// Limits bounds every use of the backtracking-compatible regular-expression
// engine. Zero fields are filled from DefaultLimits.
type Limits struct {
	MaxInputRunes     int
	MatchTimeout      time.Duration
	TotalTimeout      time.Duration
	MaxMatchesPerRule int
	MaxAppliedRules   int
}

// DefaultLimits are deliberately conservative for filename-sized inputs.
func DefaultLimits() Limits {
	return Limits{
		MaxInputRunes:     4096,
		MatchTimeout:      50 * time.Millisecond,
		TotalTimeout:      2 * time.Second,
		MaxMatchesPerRule: 64,
		MaxAppliedRules:   512,
	}
}

// DirectTMDBHint is a trusted lookup hint, not metadata. Callers must still
// fetch and validate the item from TMDB before classifying it.
type DirectTMDBHint struct {
	TMDBID    int64
	MediaType string
	Season    *int
	Episode   *int
}

// AppliedRule identifies a rule that changed the title or supplied a direct
// hint. Raw rule text is intentionally not included.
type AppliedRule struct {
	PackCode string
	Line     int
}

// WordResult is the safe, provider-neutral output of pre-recognition.
type WordResult struct {
	Title   string
	Hint    *DirectTMDBHint
	Applied []AppliedRule
}

// WordProcessor is immutable after construction and safe for concurrent use.
type WordProcessor struct {
	rules  []compiledRule
	limits Limits
}

type ruleKind uint8

const (
	ruleBlock ruleKind = iota + 1
	ruleReplace
	ruleOffset
	ruleCombined
)

type compiledRule struct {
	packCode    string
	line        int
	kind        ruleKind
	pattern     *regexp2.Regexp
	replacement replacementTemplate
	front       *regexp2.Regexp
	back        *regexp2.Regexp
	offset      *episodeExpression
	directHint  *DirectTMDBHint
	directMark  string
}

// NewBuiltinWordProcessor compiles selected read-only packs in canonical
// TV-then-anime order. Passing an empty selection creates a no-op processor.
func NewBuiltinWordProcessor(codes []string, limits Limits) (*WordProcessor, error) {
	normalized, err := NormalizePackCodes(codes)
	if err != nil {
		return nil, err
	}
	normalizedLimits, err := normalizeLimits(limits)
	if err != nil {
		return nil, err
	}

	processor := &WordProcessor{limits: normalizedLimits}
	for _, code := range normalized {
		text, readErr := snapshotForPack(code)
		if readErr != nil {
			return nil, readErr
		}
		rules, compileErr := compileDocument(code, text, normalizedLimits)
		if compileErr != nil {
			return nil, compileErr
		}
		processor.rules = append(processor.rules, rules...)
	}
	return processor, nil
}

// CompileWordProcessor compiles caller-supplied rules with the same strict
// parser and execution limits. Built-in packs should use
// NewBuiltinWordProcessor so their source location remains attributable.
func CompileWordProcessor(rules []string, limits Limits) (*WordProcessor, error) {
	normalizedLimits, err := normalizeLimits(limits)
	if err != nil {
		return nil, err
	}
	text := strings.Join(rules, "\n")
	compiled, err := compileDocument(customRuleSetCode, text, normalizedLimits)
	if err != nil {
		return nil, err
	}
	return &WordProcessor{rules: compiled, limits: normalizedLimits}, nil
}

func normalizeLimits(limits Limits) (Limits, error) {
	defaults := DefaultLimits()
	if limits.MaxInputRunes == 0 {
		limits.MaxInputRunes = defaults.MaxInputRunes
	}
	if limits.MatchTimeout == 0 {
		limits.MatchTimeout = defaults.MatchTimeout
	}
	if limits.TotalTimeout == 0 {
		limits.TotalTimeout = defaults.TotalTimeout
	}
	if limits.MaxMatchesPerRule == 0 {
		limits.MaxMatchesPerRule = defaults.MaxMatchesPerRule
	}
	if limits.MaxAppliedRules == 0 {
		limits.MaxAppliedRules = defaults.MaxAppliedRules
	}
	if limits.MaxInputRunes < 1 || limits.MaxInputRunes > 32768 ||
		limits.MatchTimeout < time.Millisecond || limits.MatchTimeout > time.Second ||
		limits.TotalTimeout < limits.MatchTimeout || limits.TotalTimeout > 10*time.Second ||
		limits.MaxMatchesPerRule < 1 || limits.MaxMatchesPerRule > 1024 ||
		limits.MaxAppliedRules < 1 || limits.MaxAppliedRules > 4096 {
		return Limits{}, ErrInvalidLimits
	}
	return limits, nil
}

func compileDocument(packCode, text string, limits Limits) ([]compiledRule, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	rules := make([]compiledRule, 0, len(lines))
	for index, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if utf8.RuneCountInString(line) > maxRuleRunes {
			return nil, &ProcessingError{Code: ErrorInvalidRule, PackCode: packCode, Line: index + 1, Err: errors.New("rule exceeds maximum length")}
		}
		rule, err := compileRule(packCode, index+1, line, limits)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
		if len(rules) > limits.MaxAppliedRules {
			return nil, &ProcessingError{Code: ErrorApplyLimit, PackCode: packCode, Line: index + 1, Err: errors.New("too many compiled rules")}
		}
	}
	return rules, nil
}

func compileRule(packCode string, line int, value string, limits Limits) (compiledRule, error) {
	rule := compiledRule{packCode: packCode, line: line}
	wrap := func(code ErrorCode, err error) (compiledRule, error) {
		return compiledRule{}, &ProcessingError{Code: code, PackCode: packCode, Line: line, Err: err}
	}

	// A few pinned upstream lines omit spaces around =>. Treat operator
	// whitespace as cosmetic so those direct hints and replacements do not
	// silently degrade into ineffective block rules.
	hasReplace := strings.Contains(value, "=>")
	hasCombined := strings.Contains(value, "&&")
	hasFrontBack := strings.Contains(value, "<>")
	hasOffset := strings.Contains(value, ">>")

	var pattern, replacement, front, back, offset string
	switch {
	case hasReplace && hasCombined && hasFrontBack && hasOffset:
		if strings.Count(value, "=>") != 1 || strings.Count(value, "&&") != 1 ||
			strings.Count(value, "<>") != 1 || strings.Count(value, ">>") != 1 {
			return wrap(ErrorInvalidRule, errors.New("combined rule operators must occur once"))
		}
		leftRight := strings.SplitN(value, "=>", 2)
		replaceOffset := strings.SplitN(leftRight[1], "&&", 2)
		frontRest := strings.SplitN(replaceOffset[1], "<>", 2)
		backOffset := strings.SplitN(frontRest[1], ">>", 2)
		pattern, replacement = strings.TrimSpace(leftRight[0]), strings.TrimSpace(replaceOffset[0])
		front, back, offset = strings.TrimSpace(frontRest[0]), strings.TrimSpace(backOffset[0]), strings.TrimSpace(backOffset[1])
		rule.kind = ruleCombined
	case hasReplace && !hasCombined && !hasFrontBack && !hasOffset:
		if strings.Count(value, "=>") != 1 {
			return wrap(ErrorInvalidRule, errors.New("replacement operator must occur once"))
		}
		parts := strings.SplitN(value, "=>", 2)
		pattern, replacement = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		rule.kind = ruleReplace
	case !hasReplace && !hasCombined && hasFrontBack && hasOffset:
		if strings.Count(value, "<>") != 1 || strings.Count(value, ">>") != 1 {
			return wrap(ErrorInvalidRule, errors.New("offset rule operators must occur once"))
		}
		parts := strings.SplitN(value, "<>", 2)
		backOffset := strings.SplitN(parts[1], ">>", 2)
		front, back, offset = strings.TrimSpace(parts[0]), strings.TrimSpace(backOffset[0]), strings.TrimSpace(backOffset[1])
		rule.kind = ruleOffset
	case !hasReplace && !hasCombined && !hasFrontBack && !hasOffset:
		pattern = value
		replacement = ""
		rule.kind = ruleBlock
	default:
		return wrap(ErrorInvalidRule, errors.New("incomplete or misplaced rule operators"))
	}

	if rule.kind == ruleBlock || rule.kind == ruleReplace || rule.kind == ruleCombined {
		if pattern == "" {
			return wrap(ErrorInvalidRule, errors.New("empty replacement pattern"))
		}
		compiled, err := compileCompatibleRegex(pattern, limits.MatchTimeout)
		if err != nil {
			return wrap(ErrorRegexCompile, err)
		}
		rule.pattern = compiled
		template, err := parseReplacement(replacement, len(compiled.GetGroupNumbers())-1)
		if err != nil {
			return wrap(ErrorInvalidRule, err)
		}
		rule.replacement = template
		hint, marker, err := parseReplacementHint(replacement)
		if err != nil {
			return wrap(ErrorInvalidDirectHint, err)
		}
		rule.directHint, rule.directMark = hint, marker
	}

	if rule.kind == ruleOffset || rule.kind == ruleCombined {
		if front == "" {
			return wrap(ErrorInvalidRule, errors.New("empty episode front locator"))
		}
		compiledFront, err := compileCompatibleRegex(front, limits.MatchTimeout)
		if err != nil {
			return wrap(ErrorRegexCompile, fmt.Errorf("front locator: %w", err))
		}
		rule.front = compiledFront
		if back != "" {
			compiledBack, compileErr := compileCompatibleRegex(back, limits.MatchTimeout)
			if compileErr != nil {
				return wrap(ErrorRegexCompile, fmt.Errorf("back locator: %w", compileErr))
			}
			rule.back = compiledBack
		}
		expression, err := parseEpisodeExpression(offset)
		if err != nil {
			return wrap(ErrorInvalidRule, err)
		}
		rule.offset = expression
	}

	return rule, nil
}

// compileCompatibleRegex preserves the upstream Python-regex meaning of an
// escaped underscore. regexp2 follows .NET and rejects that otherwise harmless
// escape, while Python treats it as a literal underscore.
func compileCompatibleRegex(pattern string, timeout time.Duration) (*regexp2.Regexp, error) {
	var normalized strings.Builder
	for index := 0; index < len(pattern); {
		if pattern[index] != '\\' {
			normalized.WriteByte(pattern[index])
			index++
			continue
		}
		start := index
		for index < len(pattern) && pattern[index] == '\\' {
			index++
		}
		count := index - start
		if index < len(pattern) && pattern[index] == '_' && count%2 == 1 {
			normalized.WriteString(strings.Repeat("\\", count-1))
			normalized.WriteByte('_')
			index++
			continue
		}
		normalized.WriteString(strings.Repeat("\\", count))
	}
	compiled, err := regexp2.Compile(normalized.String(), 0)
	if err != nil {
		return nil, err
	}
	compiled.MatchTimeout = timeout
	return compiled, nil
}

// Apply executes rules in their compiled order. It never returns a media title
// in an error, and it checks both context and the total processing deadline
// between every bounded regex operation.
func (p *WordProcessor) Apply(ctx context.Context, title string) (WordResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := WordResult{Title: title, Applied: []AppliedRule{}}
	if utf8.RuneCountInString(title) > p.limits.MaxInputRunes {
		return result, &ProcessingError{Code: ErrorInputTooLong, PackCode: "processor", Err: fmt.Errorf("maximum %d runes", p.limits.MaxInputRunes)}
	}
	if strings.Contains(title, "{[") {
		return result, &ProcessingError{Code: ErrorInvalidDirectHint, PackCode: "processor", Err: errors.New("direct hint syntax is not accepted from media titles")}
	}
	deadline := time.Now().Add(p.limits.TotalTimeout)

	for _, rule := range p.rules {
		if err := checkExecutionBudget(ctx, deadline, rule); err != nil {
			return result, err
		}
		updated, applied, err := p.applyRule(ctx, deadline, result.Title, rule)
		if err != nil {
			return result, err
		}
		if !applied {
			continue
		}
		if utf8.RuneCountInString(updated) > p.limits.MaxInputRunes {
			return result, &ProcessingError{Code: ErrorInputTooLong, PackCode: rule.packCode, Line: rule.line, Err: errors.New("intermediate title exceeds maximum length")}
		}
		if len(result.Applied) >= p.limits.MaxAppliedRules {
			return result, &ProcessingError{Code: ErrorApplyLimit, PackCode: rule.packCode, Line: rule.line, Err: errors.New("too many applied rules")}
		}
		result.Title = updated
		result.Applied = append(result.Applied, AppliedRule{PackCode: rule.packCode, Line: rule.line})
		if rule.directHint != nil {
			if result.Hint != nil && !sameDirectHint(result.Hint, rule.directHint) {
				return result, &ProcessingError{Code: ErrorInvalidDirectHint, PackCode: rule.packCode, Line: rule.line, Err: errors.New("conflicting direct TMDB hints")}
			}
			result.Hint = cloneDirectHint(rule.directHint)
			result.Title = strings.TrimSpace(strings.ReplaceAll(result.Title, rule.directMark, ""))
		}
	}

	return result, nil
}

func (p *WordProcessor) applyRule(ctx context.Context, deadline time.Time, title string, rule compiledRule) (string, bool, error) {
	switch rule.kind {
	case ruleBlock, ruleReplace:
		return p.replace(ctx, deadline, title, rule)
	case ruleOffset:
		return p.applyOffset(ctx, deadline, title, rule)
	case ruleCombined:
		updated, replaced, err := p.replace(ctx, deadline, title, rule)
		if err != nil || !replaced {
			return updated, replaced, err
		}
		// Structured identity markers are not filename text and must never be
		// interpreted as episode numbers by a following offset operation.
		offsetInput := updated
		if rule.directMark != "" {
			offsetInput = strings.ReplaceAll(offsetInput, rule.directMark, "")
		}
		offsetTitle, _, err := p.applyOffset(ctx, deadline, offsetInput, rule)
		return offsetTitle, true, err
	default:
		return title, false, &ProcessingError{Code: ErrorInvalidRule, PackCode: rule.packCode, Line: rule.line, Err: errors.New("unknown compiled rule kind")}
	}
}

func (p *WordProcessor) replace(ctx context.Context, deadline time.Time, title string, rule compiledRule) (string, bool, error) {
	input := []rune(title)
	matches := make([]*regexp2.Match, 0, 1)
	match, err := rule.pattern.FindStringMatch(title)
	for match != nil {
		if err != nil {
			return title, false, regexRuntimeError(rule, err)
		}
		if len(matches) >= p.limits.MaxMatchesPerRule {
			return title, false, &ProcessingError{Code: ErrorApplyLimit, PackCode: rule.packCode, Line: rule.line, Err: errors.New("too many matches in one rule")}
		}
		matches = append(matches, match)
		if budgetErr := checkExecutionBudget(ctx, deadline, rule); budgetErr != nil {
			return title, false, budgetErr
		}
		match, err = rule.pattern.FindNextMatch(match)
	}
	if err != nil {
		return title, false, regexRuntimeError(rule, err)
	}
	if len(matches) == 0 {
		return title, false, nil
	}

	var builder strings.Builder
	previous := 0
	for _, current := range matches {
		builder.WriteString(string(input[previous:current.Index]))
		builder.WriteString(rule.replacement.expand(current))
		previous = current.Index + current.Length
	}
	builder.WriteString(string(input[previous:]))
	return builder.String(), true, nil
}

func checkExecutionBudget(ctx context.Context, deadline time.Time, rule compiledRule) error {
	select {
	case <-ctx.Done():
		return &ProcessingError{Code: ErrorContextCanceled, PackCode: rule.packCode, Line: rule.line, Err: ctx.Err()}
	default:
	}
	if time.Now().After(deadline) {
		return &ProcessingError{Code: ErrorMatchTimeout, PackCode: rule.packCode, Line: rule.line, Err: errors.New("total word processing deadline exceeded")}
	}
	return nil
}

func regexRuntimeError(rule compiledRule, err error) error {
	code := ErrorInvalidRule
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		code = ErrorMatchTimeout
	}
	return &ProcessingError{Code: code, PackCode: rule.packCode, Line: rule.line, Err: errors.New("regular expression execution failed")}
}

type replacementPart struct {
	literal string
	group   int
}

type replacementTemplate []replacementPart

func parseReplacement(value string, maxGroup int) (replacementTemplate, error) {
	parts := make(replacementTemplate, 0, 3)
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			parts = append(parts, replacementPart{literal: literal.String(), group: -1})
			literal.Reset()
		}
	}
	for index := 0; index < len(value); {
		if value[index] != '\\' {
			literal.WriteByte(value[index])
			index++
			continue
		}
		if index+1 >= len(value) {
			return nil, errors.New("trailing replacement escape")
		}
		next := value[index+1]
		if next == '\\' {
			literal.WriteByte('\\')
			index += 2
			continue
		}
		if next < '1' || next > '9' {
			return nil, fmt.Errorf("unsupported replacement escape \\%c", next)
		}
		end := index + 2
		for end < len(value) && value[end] >= '0' && value[end] <= '9' {
			end++
		}
		group, err := strconv.Atoi(value[index+1 : end])
		if err != nil || group > maxGroup {
			return nil, fmt.Errorf("replacement references missing group %d", group)
		}
		flush()
		parts = append(parts, replacementPart{group: group})
		index = end
	}
	flush()
	return parts, nil
}

func (r replacementTemplate) expand(match *regexp2.Match) string {
	groups := match.Groups()
	var builder strings.Builder
	for _, part := range r {
		if part.group < 0 {
			builder.WriteString(part.literal)
			continue
		}
		if part.group < len(groups) && len(groups[part.group].Captures) > 0 {
			builder.WriteString(groups[part.group].String())
		}
	}
	return builder.String()
}

func parseReplacementHint(value string) (*DirectTMDBHint, string, error) {
	start := strings.Index(value, "{[")
	if start < 0 {
		return nil, "", nil
	}
	endRelative := strings.Index(value[start+2:], "]}")
	if endRelative < 0 {
		return nil, "", errors.New("unterminated direct hint")
	}
	end := start + 2 + endRelative + 2
	if strings.Contains(value[end:], "{[") {
		return nil, "", errors.New("multiple direct hints in one rule")
	}
	marker := value[start:end]
	body := value[start+2 : end-2]
	hint := &DirectTMDBHint{}
	seen := map[string]bool{}
	for _, field := range strings.Split(body, ";") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			return nil, "", errors.New("direct hint field must contain equals")
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if seen[key] {
			return nil, "", fmt.Errorf("duplicate direct hint field %q", key)
		}
		seen[key] = true
		switch key {
		case "tmdbid":
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				return nil, "", errors.New("invalid tmdbid")
			}
			hint.TMDBID = id
		case "type":
			if value != "movie" && value != "tv" {
				return nil, "", errors.New("direct hint type must be movie or tv")
			}
			hint.MediaType = value
		case "s", "e":
			number, err := strconv.Atoi(value)
			if err != nil || number < 0 || number > 100000 {
				return nil, "", fmt.Errorf("invalid direct hint field %q", key)
			}
			if key == "s" {
				hint.Season = intPointer(number)
			} else {
				hint.Episode = intPointer(number)
			}
		default:
			return nil, "", fmt.Errorf("unsupported direct hint field %q", key)
		}
	}
	if hint.TMDBID == 0 {
		return nil, "", errors.New("direct hint requires tmdbid")
	}
	return hint, marker, nil
}

func intPointer(value int) *int { return &value }

func cloneDirectHint(hint *DirectTMDBHint) *DirectTMDBHint {
	if hint == nil {
		return nil
	}
	clone := *hint
	if hint.Season != nil {
		clone.Season = intPointer(*hint.Season)
	}
	if hint.Episode != nil {
		clone.Episode = intPointer(*hint.Episode)
	}
	return &clone
}

func sameDirectHint(left, right *DirectTMDBHint) bool {
	if left == nil || right == nil || left.TMDBID != right.TMDBID || left.MediaType != right.MediaType {
		return left == nil && right == nil
	}
	return equalOptionalInt(left.Season, right.Season) && equalOptionalInt(left.Episode, right.Episode)
}

func equalOptionalInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
