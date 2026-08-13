package classification

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	SchemaVersion = 1
	MinYear       = 1888
	MaxYear       = 2200
)

type MediaType string

const (
	MediaTypeMovie MediaType = "movie"
	MediaTypeTV    MediaType = "tv"
)

type SetCondition[T comparable] struct {
	Include []T `json:"include"`
	Exclude []T `json:"exclude"`
}

type YearRange struct {
	From *int `json:"from,omitempty"`
	To   *int `json:"to,omitempty"`
}

type ConditionsV1 struct {
	GenreIDs            SetCondition[int]     `json:"genre_ids"`
	OriginalLanguages   SetCondition[string]  `json:"original_languages"`
	ProductionCountries *SetCondition[string] `json:"production_countries,omitempty"`
	OriginCountries     *SetCondition[string] `json:"origin_countries,omitempty"`
	ReleaseYear         *YearRange            `json:"release_year"`
}

type CategoryRuleV1 struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Conditions ConditionsV1 `json:"conditions"`
}

type RuleGroupV1 struct {
	MediaType            MediaType        `json:"media_type"`
	Categories           []CategoryRuleV1 `json:"categories"`
	FallbackCategoryName string           `json:"fallback_category_name"`
}

type RulesV1 struct {
	Version int           `json:"version"`
	Groups  []RuleGroupV1 `json:"groups"`
}

var movieGenres = setOf(12, 14, 16, 18, 27, 28, 35, 36, 37, 53, 80, 99, 878, 9648, 10402, 10749, 10751, 10752, 10770)
var tvGenres = setOf(16, 18, 35, 37, 80, 99, 9648, 10751, 10759, 10762, 10763, 10764, 10765, 10766, 10767, 10768)
var languages = setOf("ZH", "CN", "EN", "JA", "KO", "FR", "DE", "ES", "IT", "RU", "TH", "HI")
var countries = setOf("CN", "TW", "HK", "JP", "KR", "US", "GB", "FR", "DE", "ES", "IT", "NL", "PT", "RU", "TH", "IN", "SG")

func setOf[T comparable](values ...T) map[T]struct{} {
	result := make(map[T]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func EmptyRules() RulesV1 {
	return RulesV1{Version: SchemaVersion, Groups: []RuleGroupV1{
		{MediaType: MediaTypeMovie, Categories: []CategoryRuleV1{}, FallbackCategoryName: "未分类"},
		{MediaType: MediaTypeTV, Categories: []CategoryRuleV1{}, FallbackCategoryName: "未分类"},
	}}
}

func DefaultRules() RulesV1 {
	movie := []CategoryRuleV1{
		category("default-movie-animation", "动画电影", conditions([]int{16}, nil, nil, nil, nil, nil)),
		category("default-movie-chinese", "华语电影", conditions(nil, nil, []string{"zh", "cn"}, nil, nil, nil)),
		category("default-movie-foreign", "外语电影", conditions(nil, nil, nil, []string{"zh", "cn"}, nil, nil)),
	}
	tv := []CategoryRuleV1{
		category("default-tv-chinese-animation", "国漫", conditions([]int{16}, nil, nil, nil, nil, []string{"CN", "TW", "HK"})),
		category("default-tv-japanese-animation", "日番", conditions([]int{16}, nil, nil, nil, nil, []string{"JP"})),
		category("default-tv-animation", "动漫", conditions([]int{16}, nil, nil, nil, nil, nil)),
		category("default-tv-documentary", "纪录片", conditions([]int{99}, nil, nil, nil, nil, nil)),
		category("default-tv-kids", "儿童", conditions([]int{10762}, nil, nil, nil, nil, nil)),
		category("default-tv-variety", "综艺", conditions([]int{10764, 10767}, nil, nil, nil, nil, nil)),
		category("default-tv-chinese", "国产剧", conditions(nil, nil, nil, nil, nil, []string{"CN", "TW", "HK"})),
		category("default-tv-western", "欧美剧", conditions(nil, nil, nil, nil, nil, []string{"US", "FR", "GB", "DE", "ES", "IT", "NL", "PT", "RU"})),
		category("default-tv-jp-kr", "日韩剧", conditions(nil, nil, nil, nil, nil, []string{"JP", "KR"})),
	}
	return RulesV1{Version: SchemaVersion, Groups: []RuleGroupV1{
		{MediaType: MediaTypeMovie, Categories: movie, FallbackCategoryName: "未分类"},
		{MediaType: MediaTypeTV, Categories: tv, FallbackCategoryName: "未分类"},
	}}
}

func category(id, name string, value ConditionsV1) CategoryRuleV1 {
	return CategoryRuleV1{ID: id, Name: name, Conditions: value}
}
func conditions(genreInclude, genreExclude []int, languageInclude, languageExclude, productionInclude, originInclude []string) ConditionsV1 {
	value := ConditionsV1{GenreIDs: SetCondition[int]{Include: nonNil(genreInclude), Exclude: nonNil(genreExclude)}, OriginalLanguages: SetCondition[string]{Include: nonNil(languageInclude), Exclude: nonNil(languageExclude)}}
	if productionInclude != nil {
		value.ProductionCountries = &SetCondition[string]{Include: nonNil(productionInclude), Exclude: []string{}}
	}
	if originInclude != nil {
		value.OriginCountries = &SetCondition[string]{Include: nonNil(originInclude), Exclude: []string{}}
	}
	return value
}
func nonNil[T any](value []T) []T {
	if value == nil {
		return []T{}
	}
	return value
}

func DecodeStrict(payload []byte) (RulesV1, error) {
	if err := validateRequiredJSONFields(payload); err != nil {
		return RulesV1{}, err
	}
	var rules RulesV1
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rules); err != nil {
		return RulesV1{}, fmt.Errorf("规则 JSON 无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return RulesV1{}, fmt.Errorf("规则 JSON 只能包含一个对象")
	}
	if err := Validate(rules); err != nil {
		return RulesV1{}, err
	}
	return rules, nil
}

func validateRequiredJSONFields(payload []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return fmt.Errorf("规则 JSON 无效: %w", err)
	}
	var groups []map[string]json.RawMessage
	if raw, ok := root["groups"]; !ok {
		return fmt.Errorf("groups 不能为空")
	} else if err := json.Unmarshal(raw, &groups); err != nil {
		return fmt.Errorf("groups 无效")
	}
	for groupIndex, group := range groups {
		var categories []map[string]json.RawMessage
		if raw, ok := group["categories"]; !ok {
			return fmt.Errorf("groups[%d].categories 不能为空", groupIndex)
		} else if string(raw) == "null" {
			return fmt.Errorf("groups[%d].categories 必须为数组", groupIndex)
		} else if err := json.Unmarshal(raw, &categories); err != nil {
			return fmt.Errorf("groups[%d].categories 无效", groupIndex)
		}
		for categoryIndex, category := range categories {
			var conditions map[string]json.RawMessage
			if raw, ok := category["conditions"]; !ok {
				return fmt.Errorf("groups[%d].categories[%d].conditions 不能为空", groupIndex, categoryIndex)
			} else if err := json.Unmarshal(raw, &conditions); err != nil {
				return fmt.Errorf("groups[%d].categories[%d].conditions 无效", groupIndex, categoryIndex)
			}
			for _, field := range []string{"genre_ids", "original_languages", "release_year"} {
				raw, ok := conditions[field]
				if !ok {
					return fmt.Errorf("groups[%d].categories[%d].conditions.%s 不能为空", groupIndex, categoryIndex, field)
				}
				if field == "release_year" || string(raw) == "null" {
					continue
				}
				if err := requireSetFields(raw, fmt.Sprintf("groups[%d].categories[%d].conditions.%s", groupIndex, categoryIndex, field)); err != nil {
					return err
				}
			}
			for _, field := range []string{"production_countries", "origin_countries"} {
				if raw, ok := conditions[field]; ok {
					if err := requireSetFields(raw, fmt.Sprintf("groups[%d].categories[%d].conditions.%s", groupIndex, categoryIndex, field)); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func requireSetFields(raw json.RawMessage, path string) error {
	var value map[string]json.RawMessage
	if string(raw) == "null" {
		return fmt.Errorf("%s 必须为对象", path)
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%s 无效", path)
	}
	if include, ok := value["include"]; !ok || string(include) == "null" {
		return fmt.Errorf("%s.include 不能为空", path)
	}
	if exclude, ok := value["exclude"]; !ok || string(exclude) == "null" {
		return fmt.Errorf("%s.exclude 不能为空", path)
	}
	return nil
}

func CanonicalJSON(rules RulesV1) (string, error) {
	if err := Validate(rules); err != nil {
		return "", err
	}
	payload, err := json.Marshal(rules)
	if err != nil {
		return "", fmt.Errorf("encode rules: %w", err)
	}
	return string(payload), nil
}

func Clone(rules RulesV1, regenerateIDs bool) (RulesV1, error) {
	payload, err := CanonicalJSON(rules)
	if err != nil {
		return RulesV1{}, err
	}
	clone, err := DecodeStrict([]byte(payload))
	if err != nil {
		return RulesV1{}, err
	}
	if regenerateIDs {
		for groupIndex := range clone.Groups {
			for categoryIndex := range clone.Groups[groupIndex].Categories {
				id, err := RandomID()
				if err != nil {
					return RulesV1{}, err
				}
				clone.Groups[groupIndex].Categories[categoryIndex].ID = id
			}
		}
	}
	return clone, nil
}

func RandomID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate category id: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func Validate(rules RulesV1) error {
	if rules.Version != SchemaVersion {
		return fmt.Errorf("version 必须为 1")
	}
	if len(rules.Groups) != 2 {
		return fmt.Errorf("groups 必须恰好包含 movie 和 tv")
	}
	seenGroups := map[MediaType]bool{}
	seenCategoryIDs := map[string]bool{}
	for groupIndex, group := range rules.Groups {
		if group.MediaType != MediaTypeMovie && group.MediaType != MediaTypeTV {
			return fmt.Errorf("groups[%d].media_type 无效", groupIndex)
		}
		if seenGroups[group.MediaType] {
			return fmt.Errorf("media_type %s 重复", group.MediaType)
		}
		seenGroups[group.MediaType] = true
		if strings.TrimSpace(group.FallbackCategoryName) == "" {
			return fmt.Errorf("groups[%d].fallback_category_name 不能为空", groupIndex)
		}
		seenNames := map[string]bool{}
		for categoryIndex, category := range group.Categories {
			path := fmt.Sprintf("groups[%d].categories[%d]", groupIndex, categoryIndex)
			if strings.TrimSpace(category.ID) == "" || seenCategoryIDs[category.ID] {
				return fmt.Errorf("%s.id 不能为空且必须在 Profile 内唯一", path)
			}
			seenCategoryIDs[category.ID] = true
			name := cases.Fold().String(norm.NFKC.String(strings.TrimSpace(category.Name)))
			if name == "" || seenNames[name] {
				return fmt.Errorf("%s.name 不能为空且组内必须唯一", path)
			}
			seenNames[name] = true
			if err := validateConditions(group.MediaType, category.Conditions); err != nil {
				return fmt.Errorf("%s.conditions: %w", path, err)
			}
		}
	}
	return nil
}

func validateConditions(mediaType MediaType, value ConditionsV1) error {
	genres := movieGenres
	if mediaType == MediaTypeTV {
		genres = tvGenres
	}
	if err := validateSet(value.GenreIDs, genres, "genre_ids"); err != nil {
		return err
	}
	if err := validateCodeSet(value.OriginalLanguages, languages, "original_languages"); err != nil {
		return err
	}
	if mediaType == MediaTypeMovie {
		if value.OriginCountries != nil {
			return fmt.Errorf("origin_countries 仅支持 tv")
		}
		if value.ProductionCountries != nil {
			if err := validateCodeSet(*value.ProductionCountries, countries, "production_countries"); err != nil {
				return err
			}
		}
	} else {
		if value.ProductionCountries != nil {
			return fmt.Errorf("production_countries 仅支持 movie")
		}
		if value.OriginCountries != nil {
			if err := validateCodeSet(*value.OriginCountries, countries, "origin_countries"); err != nil {
				return err
			}
		}
	}
	if value.ReleaseYear != nil {
		if value.ReleaseYear.From != nil && (*value.ReleaseYear.From < MinYear || *value.ReleaseYear.From > MaxYear) {
			return fmt.Errorf("release_year.from 超出范围")
		}
		if value.ReleaseYear.To != nil && (*value.ReleaseYear.To < MinYear || *value.ReleaseYear.To > MaxYear) {
			return fmt.Errorf("release_year.to 超出范围")
		}
		if value.ReleaseYear.From != nil && value.ReleaseYear.To != nil && *value.ReleaseYear.From > *value.ReleaseYear.To {
			return fmt.Errorf("release_year.from 不能大于 to")
		}
	}
	return nil
}

func validateCodeSet(value SetCondition[string], allowed map[string]struct{}, field string) error {
	normalized := SetCondition[string]{Include: make([]string, len(value.Include)), Exclude: make([]string, len(value.Exclude))}
	for i, item := range value.Include {
		normalized.Include[i] = strings.ToUpper(item)
	}
	for i, item := range value.Exclude {
		normalized.Exclude[i] = strings.ToUpper(item)
	}
	return validateSet(normalized, allowed, field)
}

func validateSet[T comparable](value SetCondition[T], allowed map[T]struct{}, field string) error {
	seen := map[T]string{}
	for _, item := range value.Include {
		if _, ok := allowed[item]; !ok {
			return fmt.Errorf("%s.include 包含不允许的值", field)
		}
		if _, ok := seen[item]; ok {
			return fmt.Errorf("%s 包含重复值", field)
		}
		seen[item] = "include"
	}
	for _, item := range value.Exclude {
		if _, ok := allowed[item]; !ok {
			return fmt.Errorf("%s.exclude 包含不允许的值", field)
		}
		if _, ok := seen[item]; ok {
			return fmt.Errorf("%s include/exclude 包含重复或冲突值", field)
		}
		seen[item] = "exclude"
	}
	return nil
}
