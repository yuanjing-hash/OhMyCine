package classification

import "strings"

type Metadata struct {
	MediaType           MediaType
	GenreIDs            []int
	OriginalLanguage    string
	ProductionCountries []string
	OriginCountries     []string
	ReleaseYear         *int
}

type MatchResult struct {
	CategoryName    string  `json:"category_name"`
	MatchedRuleID   *string `json:"matched_rule_id,omitempty"`
	MatchedRuleName *string `json:"matched_rule_name,omitempty"`
}

func Classify(metadata Metadata, rules RulesV1) MatchResult {
	for _, group := range rules.Groups {
		if group.MediaType != metadata.MediaType {
			continue
		}
		for _, category := range group.Categories {
			if matches(metadata, category.Conditions) {
				id, name := category.ID, category.Name
				return MatchResult{CategoryName: name, MatchedRuleID: &id, MatchedRuleName: &name}
			}
		}
		return MatchResult{CategoryName: group.FallbackCategoryName}
	}
	return MatchResult{}
}

func matches(metadata Metadata, conditions ConditionsV1) bool {
	if !matchSet(metadata.GenreIDs, conditions.GenreIDs, func(value int) int { return value }) {
		return false
	}
	if !matchSet(single(metadata.OriginalLanguage), conditions.OriginalLanguages, strings.ToUpper) {
		return false
	}
	if conditions.ProductionCountries != nil && !matchSet(metadata.ProductionCountries, *conditions.ProductionCountries, strings.ToUpper) {
		return false
	}
	if conditions.OriginCountries != nil && !matchSet(metadata.OriginCountries, *conditions.OriginCountries, strings.ToUpper) {
		return false
	}
	if conditions.ReleaseYear != nil {
		if metadata.ReleaseYear == nil {
			return false
		}
		if conditions.ReleaseYear.From != nil && *metadata.ReleaseYear < *conditions.ReleaseYear.From {
			return false
		}
		if conditions.ReleaseYear.To != nil && *metadata.ReleaseYear > *conditions.ReleaseYear.To {
			return false
		}
	}
	return true
}

func single(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{value}
}
func matchSet[T comparable](actual []T, condition SetCondition[T], normalize func(T) T) bool {
	actualSet := map[T]struct{}{}
	for _, item := range actual {
		actualSet[normalize(item)] = struct{}{}
	}
	for _, excluded := range condition.Exclude {
		if _, ok := actualSet[normalize(excluded)]; ok {
			return false
		}
	}
	if len(condition.Include) == 0 {
		return true
	}
	for _, included := range condition.Include {
		if _, ok := actualSet[normalize(included)]; ok {
			return true
		}
	}
	return false
}
