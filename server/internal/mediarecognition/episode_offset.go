package mediarecognition

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
)

const maxEpisodeMagnitude = 1_000_000

var episodeNumberPattern = regexp.MustCompile(`[0-9一二三四五六七八九十百千]+`)

type episodeExpression struct {
	value string
}

func parseEpisodeExpression(value string) (*episodeExpression, error) {
	if value == "" || utf8.RuneCountInString(value) > 64 {
		return nil, errors.New("episode expression is empty or too long")
	}
	parser := arithmeticParser{input: []rune(value), episode: 1}
	if _, err := parser.parse(); err != nil {
		return nil, fmt.Errorf("invalid episode expression: %w", err)
	}
	return &episodeExpression{value: value}, nil
}

func (e *episodeExpression) evaluate(episode int) (int, error) {
	parser := arithmeticParser{input: []rune(e.value), episode: episode}
	return parser.parse()
}

func (p *WordProcessor) applyOffset(ctx context.Context, deadline time.Time, title string, rule compiledRule) (string, bool, error) {
	titleRunes := []rune(title)
	byteLocations := episodeNumberPattern.FindAllStringIndex(title, -1)
	if len(byteLocations) == 0 {
		return title, false, nil
	}

	type replacement struct {
		start int
		end   int
		value string
	}
	replacements := make([]replacement, 0, len(byteLocations))
	for _, byteLocation := range byteLocations {
		if len(replacements) >= p.limits.MaxMatchesPerRule {
			return title, false, &ProcessingError{Code: ErrorApplyLimit, PackCode: rule.packCode, Line: rule.line, Err: errors.New("too many episode candidates in one rule")}
		}
		if err := checkExecutionBudget(ctx, deadline, rule); err != nil {
			return title, false, err
		}

		start := utf8.RuneCountInString(title[:byteLocation[0]])
		end := start + utf8.RuneCountInString(title[byteLocation[0]:byteLocation[1]])
		frontMatched, err := boundedMatch(rule.front, string(titleRunes[:start]), rule)
		if err != nil {
			return title, false, err
		}
		if !frontMatched {
			continue
		}
		if rule.back != nil {
			backMatched, matchErr := boundedMatch(rule.back, string(titleRunes[end:]), rule)
			if matchErr != nil {
				return title, false, matchErr
			}
			if !backMatched {
				continue
			}
		}

		original := string(titleRunes[start:end])
		number, chinese, err := parseEpisodeNumber(original)
		if err != nil {
			return title, false, &ProcessingError{Code: ErrorInvalidRule, PackCode: rule.packCode, Line: rule.line, Err: errors.New("episode number conversion failed")}
		}
		shifted, err := rule.offset.evaluate(number)
		if err != nil {
			return title, false, &ProcessingError{Code: ErrorInvalidRule, PackCode: rule.packCode, Line: rule.line, Err: err}
		}
		formatted := formatEpisodeNumber(original, shifted, chinese)
		replacements = append(replacements, replacement{start: start, end: end, value: formatted})
	}
	if len(replacements) == 0 {
		return title, false, nil
	}

	var builder strings.Builder
	previous := 0
	for _, item := range replacements {
		builder.WriteString(string(titleRunes[previous:item.start]))
		builder.WriteString(item.value)
		previous = item.end
	}
	builder.WriteString(string(titleRunes[previous:]))
	return builder.String(), true, nil
}

func boundedMatch(pattern *regexp2.Regexp, value string, rule compiledRule) (bool, error) {
	match, err := pattern.FindStringMatch(value)
	if err != nil {
		return false, regexRuntimeError(rule, err)
	}
	return match != nil, nil
}

func parseEpisodeNumber(value string) (number int, chinese bool, err error) {
	if value == "" {
		return 0, false, errors.New("empty episode number")
	}
	if value[0] >= '0' && value[0] <= '9' {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed > maxEpisodeMagnitude {
			return 0, false, errors.New("invalid numeric episode")
		}
		return parsed, false, nil
	}

	digit := func(r rune) (int, bool) {
		switch r {
		case '一':
			return 1, true
		case '二':
			return 2, true
		case '三':
			return 3, true
		case '四':
			return 4, true
		case '五':
			return 5, true
		case '六':
			return 6, true
		case '七':
			return 7, true
		case '八':
			return 8, true
		case '九':
			return 9, true
		default:
			return 0, false
		}
	}
	runes := []rune(value)
	hasUnit := strings.ContainsAny(value, "十百千")
	if !hasUnit {
		result := 0
		for _, r := range runes {
			d, ok := digit(r)
			if !ok {
				return 0, true, errors.New("invalid Chinese episode")
			}
			result = result*10 + d
		}
		return result, true, nil
	}

	total, current := 0, 0
	for _, r := range runes {
		if d, ok := digit(r); ok {
			current = d
			continue
		}
		unit := 0
		switch r {
		case '十':
			unit = 10
		case '百':
			unit = 100
		case '千':
			unit = 1000
		default:
			return 0, true, errors.New("invalid Chinese episode unit")
		}
		if current == 0 {
			current = 1
		}
		total += current * unit
		current = 0
	}
	total += current
	if total > maxEpisodeMagnitude {
		return 0, true, errors.New("Chinese episode exceeds limit")
	}
	return total, true, nil
}

func formatEpisodeNumber(original string, value int, chinese bool) string {
	if chinese {
		return formatChineseNumber(value)
	}
	width := 0
	if len(original) > 1 && original[0] == '0' {
		width = len(original)
	}
	if value < 0 {
		return "-" + leftPad(strconv.Itoa(-value), width, '0')
	}
	return leftPad(strconv.Itoa(value), width, '0')
}

func leftPad(value string, width int, padding byte) string {
	if len(value) >= width {
		return value
	}
	return strings.Repeat(string(padding), width-len(value)) + value
}

func formatChineseNumber(value int) string {
	if value < 0 {
		return "负" + formatChineseNumber(-value)
	}
	if value == 0 {
		return "零"
	}
	if value > 9999 {
		return strconv.Itoa(value)
	}
	digits := []rune("零一二三四五六七八九")
	units := []string{"千", "百", "十", ""}
	divisors := []int{1000, 100, 10, 1}
	var builder strings.Builder
	zeroPending := false
	for index, divisor := range divisors {
		digit := value / divisor
		value %= divisor
		if digit == 0 {
			if builder.Len() > 0 && value > 0 {
				zeroPending = true
			}
			continue
		}
		if zeroPending {
			builder.WriteRune('零')
			zeroPending = false
		}
		if !(divisor == 10 && digit == 1 && builder.Len() == 0) {
			builder.WriteRune(digits[digit])
		}
		builder.WriteString(units[index])
	}
	return builder.String()
}

type arithmeticParser struct {
	input   []rune
	pos     int
	episode int
}

func (p *arithmeticParser) parse() (int, error) {
	value, err := p.parseExpression()
	if err != nil {
		return 0, err
	}
	p.skipSpaces()
	if p.pos != len(p.input) {
		return 0, errors.New("unexpected token")
	}
	return value, nil
}

func (p *arithmeticParser) parseExpression() (int, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) || (p.input[p.pos] != '+' && p.input[p.pos] != '-') {
			return left, nil
		}
		op := p.input[p.pos]
		p.pos++
		right, parseErr := p.parseTerm()
		if parseErr != nil {
			return 0, parseErr
		}
		if op == '+' {
			left, err = boundedArithmetic(left, right, func(a, b int) int { return a + b })
		} else {
			left, err = boundedArithmetic(left, right, func(a, b int) int { return a - b })
		}
		if err != nil {
			return 0, err
		}
	}
}

func (p *arithmeticParser) parseTerm() (int, error) {
	left, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpaces()
		if p.pos >= len(p.input) || (p.input[p.pos] != '*' && p.input[p.pos] != '/' && p.input[p.pos] != '%') {
			return left, nil
		}
		op := p.input[p.pos]
		p.pos++
		floorDivision := false
		if op == '/' && p.pos < len(p.input) && p.input[p.pos] == '/' {
			floorDivision = true
			p.pos++
		}
		right, parseErr := p.parseFactor()
		if parseErr != nil {
			return 0, parseErr
		}
		switch op {
		case '*':
			left, err = boundedArithmetic(left, right, func(a, b int) int { return a * b })
		case '/', '%':
			if right == 0 {
				return 0, errors.New("division by zero")
			}
			if op == '%' {
				left = left % right
			} else if floorDivision {
				quotient, remainder := left/right, left%right
				if remainder != 0 && (remainder < 0) != (right < 0) {
					quotient--
				}
				left = quotient
			} else {
				left = left / right
			}
		}
		if err != nil {
			return 0, err
		}
	}
}

func (p *arithmeticParser) parseFactor() (int, error) {
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return 0, errors.New("missing operand")
	}
	if p.input[p.pos] == '+' || p.input[p.pos] == '-' {
		op := p.input[p.pos]
		p.pos++
		value, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if op == '-' {
			value = -value
		}
		return value, nil
	}
	if p.input[p.pos] == '(' {
		p.pos++
		value, err := p.parseExpression()
		if err != nil {
			return 0, err
		}
		p.skipSpaces()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, errors.New("missing closing parenthesis")
		}
		p.pos++
		return value, nil
	}
	if p.pos+2 <= len(p.input) && string(p.input[p.pos:p.pos+2]) == "EP" {
		p.pos += 2
		return p.episode, nil
	}
	start := p.pos
	for p.pos < len(p.input) && unicode.IsDigit(p.input[p.pos]) && p.input[p.pos] <= unicode.MaxASCII {
		p.pos++
	}
	if start == p.pos {
		return 0, errors.New("only numbers, EP, parentheses and arithmetic operators are allowed")
	}
	value, err := strconv.Atoi(string(p.input[start:p.pos]))
	if err != nil || value > maxEpisodeMagnitude {
		return 0, errors.New("numeric operand exceeds limit")
	}
	return value, nil
}

func (p *arithmeticParser) skipSpaces() {
	for p.pos < len(p.input) && unicode.IsSpace(p.input[p.pos]) {
		p.pos++
	}
}

func boundedArithmetic(left, right int, operation func(int, int) int) (int, error) {
	if left > maxEpisodeMagnitude || left < -maxEpisodeMagnitude || right > maxEpisodeMagnitude || right < -maxEpisodeMagnitude {
		return 0, errors.New("episode arithmetic operand exceeds limit")
	}
	result := operation(left, right)
	if result > maxEpisodeMagnitude || result < -maxEpisodeMagnitude {
		return 0, errors.New("episode arithmetic result exceeds limit")
	}
	return result, nil
}
