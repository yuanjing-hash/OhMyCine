package tmdb

import (
	"context"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const (
	DefaultIdentitySearchNameLimit = 6
	MaxIdentitySearchNameLimit     = 8
	// SiteService's public title-search contract accepts at most 160 runes.
	// Keep identity aliases at the same boundary so a name is either rejected
	// before orchestration or is guaranteed to be a valid site query.
	MaxIdentitySearchNameRunes = 160
)

type SearchName struct {
	Value  string `json:"value"`
	Locale string `json:"locale,omitempty"`
	Kind   string `json:"kind"`
}

// IdentitySearchNames verifies the TMDB identity and produces an ordered,
// bounded set of localized/alias/original names for resource aggregation.
func (c *Client) IdentitySearchNames(ctx context.Context, mediaType string, id int64, language string, limit int) (Match, []SearchName, error) {
	match, err := c.GetByID(ctx, mediaType, id, language)
	if err != nil {
		return Match{}, nil, err
	}
	detail, enrichmentErr := c.getCandidateEnrichment(ctx, mediaType, id, language)
	if enrichmentErr != nil {
		if ctx.Err() != nil {
			return Match{}, nil, ctx.Err()
		}
		detail = candidateEnrichmentResponse{}
	}
	return match, buildIdentitySearchNames(match, detail, limit), nil
}

func buildIdentitySearchNames(match Match, detail candidateEnrichmentResponse, limit int) []SearchName {
	if limit <= 0 || limit > MaxIdentitySearchNameLimit {
		limit = DefaultIdentitySearchNameLimit
	}
	result := make([]SearchName, 0, limit)
	seen := make(map[string]struct{}, limit)
	add := func(value, locale, kind string) {
		value = strings.Join(strings.Fields(norm.NFC.String(value)), " ")
		if value == "" || len([]rune(value)) > MaxIdentitySearchNameRunes || len(result) >= limit {
			return
		}
		key := cases.Fold().String(norm.NFKC.String(value))
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, SearchName{Value: value, Locale: locale, Kind: kind})
	}
	add(match.Snapshot.Title, "zh-CN", "localized")
	regions := []string{"CN", "TW", "HK", "SG"}
	for _, region := range regions {
		for _, value := range append(append([]candidateAlternativeTitle(nil), detail.AlternativeTitles.Titles...), detail.AlternativeTitles.Results...) {
			if strings.EqualFold(value.ISO31661, region) {
				add(value.Title, "zh-"+region, "alias")
			}
		}
		for _, value := range detail.Translations.Translations {
			if strings.EqualFold(value.ISO6391, "zh") && strings.EqualFold(value.ISO31661, region) {
				add(value.Data.Title, "zh-"+region, "translation")
				add(value.Data.Name, "zh-"+region, "translation")
			}
		}
	}
	add(match.Snapshot.OriginalTitle, match.Snapshot.OriginalLanguage, "original")
	for _, value := range detail.Translations.Translations {
		if strings.EqualFold(value.ISO6391, "en") {
			add(value.Data.Title, "en", "english")
			add(value.Data.Name, "en", "english")
		}
	}
	for _, value := range append(append([]candidateAlternativeTitle(nil), detail.AlternativeTitles.Titles...), detail.AlternativeTitles.Results...) {
		add(value.Title, strings.ToUpper(value.ISO31661), "alias")
	}
	for _, value := range detail.Translations.Translations {
		add(value.Data.Title, value.ISO6391, "translation")
		add(value.Data.Name, value.ISO6391, "translation")
	}
	return result
}
