package mediarecognition

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// HanEquivalence is a replaceable local-only simplification layer. It exists
// at the comparison boundary so callers can later supply a maintained OpenCC-
// compatible implementation without changing parsing or remote-provider code.
type HanEquivalence interface {
	Fold(string) string
}

type staticHanEquivalence map[rune]rune

func (s staticHanEquivalence) Fold(value string) string {
	return strings.Map(func(r rune) rune {
		if replacement, ok := s[r]; ok {
			return replacement
		}
		return r
	}, value)
}

// BuiltInHanEquivalence is deliberately small and conservative. It covers the
// frozen corpus while the interface above keeps full conversion replaceable.
// These folds are used only for matching keys; display titles are untouched.
var BuiltInHanEquivalence HanEquivalence = staticHanEquivalence{
	'後': '后', '宮': '宫', '傳': '传', '國': '国', '風': '风', '雲': '云',
	'劍': '剑', '俠': '侠', '龍': '龙', '門': '门', '臺': '台', '灣': '湾',
	'華': '华', '語': '语', '劇': '剧', '電': '电', '視': '视', '體': '体',
}

var unicodeCaseFold = cases.Fold()

// comparisonKey applies NFC, Unicode case folding, optional Han equivalence,
// and punctuation/whitespace equivalence. It retains only letters and digits.
func comparisonKey(value string) string {
	return comparisonKeyWith(value, BuiltInHanEquivalence)
}

func comparisonKeyWith(value string, han HanEquivalence) string {
	value = unicodeCaseFold.String(norm.NFC.String(value))
	if han != nil {
		value = han.Fold(value)
	}
	var result strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func comparisonTokens(value string, han HanEquivalence) []string {
	value = unicodeCaseFold.String(norm.NFC.String(value))
	if han != nil {
		value = han.Fold(value)
	}
	var normalized strings.Builder
	lastSpace := true
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
			lastSpace = false
		} else if !lastSpace {
			normalized.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.Fields(normalized.String())
}

// TitleSimilarity returns a deterministic [0,1] score. Exact normalized keys
// score 1; otherwise it combines rune edit similarity, bigram Dice, and token
// overlap instead of collapsing all non-exact titles into one fixed bucket.
func TitleSimilarity(left, right string, han HanEquivalence) float64 {
	leftKey, rightKey := comparisonKeyWith(left, han), comparisonKeyWith(right, han)
	if leftKey == "" || rightKey == "" {
		return 0
	}
	if leftKey == rightKey {
		return 1
	}
	leftRunes, rightRunes := []rune(leftKey), []rune(rightKey)
	maximum := len(leftRunes)
	if len(rightRunes) > maximum {
		maximum = len(rightRunes)
	}
	edit := 1 - float64(levenshteinRunes(leftRunes, rightRunes))/float64(maximum)
	dice := bigramDice(leftRunes, rightRunes)
	tokens := tokenJaccard(comparisonTokens(left, han), comparisonTokens(right, han))
	return clamp01(maxFloat(edit, maxFloat(dice, tokens)))
}

func levenshteinRunes(left, right []rune) int {
	if len(left) == 0 {
		return len(right)
	}
	previous := make([]int, len(right)+1)
	current := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for i, l := range left {
		current[0] = i + 1
		for j, r := range right {
			cost := 1
			if l == r {
				cost = 0
			}
			current[j+1] = minInt(previous[j+1]+1, current[j]+1, previous[j]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(right)]
}

func bigramDice(left, right []rune) float64 {
	if len(left) < 2 || len(right) < 2 {
		return 0
	}
	leftCounts := make(map[string]int, len(left)-1)
	for index := 0; index < len(left)-1; index++ {
		leftCounts[string(left[index:index+2])]++
	}
	intersection := 0
	for index := 0; index < len(right)-1; index++ {
		key := string(right[index : index+2])
		if leftCounts[key] > 0 {
			intersection++
			leftCounts[key]--
		}
	}
	return 2 * float64(intersection) / float64(len(left)-1+len(right)-1)
}

func tokenJaccard(left, right []string) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	set := make(map[string]uint8, len(left)+len(right))
	for _, value := range left {
		set[value] |= 1
	}
	for _, value := range right {
		set[value] |= 2
	}
	intersection, union := 0, 0
	for _, mask := range set {
		union++
		if mask == 3 {
			intersection++
		}
	}
	return float64(intersection) / float64(union)
}

func minInt(values ...int) int {
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
