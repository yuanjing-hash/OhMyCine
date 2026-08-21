package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
)

const (
	maxRecognitionRules     = 64
	maxRecognitionPattern   = 512
	maxRecognitionReplace   = 512
	maxRecognitionRulesJSON = 48 * 1024
)

const (
	defaultMovieDirectoryTemplate = "{category}/{title} ({year})"
	defaultMovieFilenameTemplate  = "{title} ({year})"
	defaultTVDirectoryTemplate    = "{category}/{title} ({year})/Season {season:02}"
	defaultTVFilenameTemplate     = "{title} - S{season:02}E{episode:02}"
)

// RecognitionRule is a provider-neutral title preprocessor owned by one
// classification Profile. Rules are snapshotted into DownloadTask and applied
// before the built-in filename parser and TMDB lookup.
type RecognitionRule struct {
	Enabled     bool   `json:"enabled"`
	MediaType   string `json:"media_type"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

func canonicalRecognitionRules(raw []byte) (string, []RecognitionRule, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil, errors.New("识别预处理规则必须是 JSON 数组")
	}
	if len(raw) > maxRecognitionRulesJSON {
		return "", nil, errors.New("识别预处理规则过大")
	}
	var rules []RecognitionRule
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rules); err != nil {
		return "", nil, fmt.Errorf("识别预处理规则 JSON 无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", nil, errors.New("识别预处理规则只能包含一个数组")
	}
	if rules == nil {
		return "", nil, errors.New("识别预处理规则必须是 JSON 数组")
	}
	if len(rules) > maxRecognitionRules {
		return "", nil, fmt.Errorf("识别预处理规则不能超过 %d 条", maxRecognitionRules)
	}
	for index := range rules {
		rule := &rules[index]
		rule.MediaType = strings.ToLower(strings.TrimSpace(rule.MediaType))
		if rule.MediaType != "all" && rule.MediaType != "movie" && rule.MediaType != "tv" {
			return "", nil, fmt.Errorf("识别预处理规则 %d 的媒体类型无效", index+1)
		}
		if strings.TrimSpace(rule.Pattern) == "" || len([]rune(rule.Pattern)) > maxRecognitionPattern || strings.ContainsAny(rule.Pattern, "\x00\r\n") {
			return "", nil, fmt.Errorf("识别预处理规则 %d 的正则表达式无效", index+1)
		}
		if len([]rune(rule.Replacement)) > maxRecognitionReplace || strings.ContainsAny(rule.Replacement, "\x00\r\n") {
			return "", nil, fmt.Errorf("识别预处理规则 %d 的替换内容无效", index+1)
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return "", nil, fmt.Errorf("识别预处理规则 %d 无法编译: %w", index+1, err)
		}
	}
	payload, err := json.Marshal(rules)
	if err != nil {
		return "", nil, err
	}
	return string(payload), rules, nil
}

func canonicalBuiltinRecognitionPacks(raw []byte) (string, []string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil, errors.New("内置识别词包必须是 JSON 数组")
	}
	var codes []string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&codes); err != nil {
		return "", nil, fmt.Errorf("内置识别词包 JSON 无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", nil, errors.New("内置识别词包只能包含一个数组")
	}
	if codes == nil {
		return "", nil, errors.New("内置识别词包必须是 JSON 数组")
	}
	normalized, err := mediarecognition.NormalizePackCodes(codes)
	if err != nil {
		return "", nil, fmt.Errorf("内置识别词包无效: %w", err)
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", nil, err
	}
	return string(payload), normalized, nil
}

func applyRecognitionRules(value, mediaType string, rules []RecognitionRule) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	for _, rule := range rules {
		if !rule.Enabled || (rule.MediaType != "all" && rule.MediaType != mediaType) {
			continue
		}
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			continue // Persisted rules are validated; defensive for legacy corruption.
		}
		value = pattern.ReplaceAllString(value, rule.Replacement)
	}
	return value
}

type profileOrganizationConfig struct {
	BuiltinRecognitionPacksJSON string
	BuiltinRecognitionPacks     []string
	RecognitionRulesJSON        string
	RecognitionRules            []RecognitionRule
	MovieDirectoryTemplate      string
	MovieFilenameTemplate       string
	TVDirectoryTemplate         string
	TVFilenameTemplate          string
}

func defaultProfileOrganizationConfig() profileOrganizationConfig {
	builtinPacks := mediarecognition.DefaultPackCodes()
	builtinPacksJSON, _ := json.Marshal(builtinPacks)
	return profileOrganizationConfig{
		BuiltinRecognitionPacksJSON: string(builtinPacksJSON),
		BuiltinRecognitionPacks:     builtinPacks,
		RecognitionRulesJSON:        "[]",
		RecognitionRules:            []RecognitionRule{},
		MovieDirectoryTemplate:      defaultMovieDirectoryTemplate,
		MovieFilenameTemplate:       defaultMovieFilenameTemplate,
		TVDirectoryTemplate:         defaultTVDirectoryTemplate,
		TVFilenameTemplate:          defaultTVFilenameTemplate,
	}
}

func validateProfileTemplates(config profileOrganizationConfig) error {
	if err := validateImportTemplate(config.MovieDirectoryTemplate, true); err != nil {
		return fmt.Errorf("电影目录模板无效: %w", err)
	}
	if err := validateImportTemplate(config.MovieFilenameTemplate, false); err != nil {
		return fmt.Errorf("电影文件名模板无效: %w", err)
	}
	if err := validateImportTemplate(config.TVDirectoryTemplate, true); err != nil {
		return fmt.Errorf("剧集目录模板无效: %w", err)
	}
	if err := validateImportTemplate(config.TVFilenameTemplate, false); err != nil {
		return fmt.Errorf("剧集文件名模板无效: %w", err)
	}
	return nil
}
