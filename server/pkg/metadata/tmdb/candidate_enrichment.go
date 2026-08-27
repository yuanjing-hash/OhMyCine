package tmdb

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
)

const (
	// Automatic recognition enriches only a tiny shortlist. Search requests
	// remain bounded separately by the recognition query budget.
	DefaultCandidateEnrichmentLimit = 3
	maxCandidateAliases             = 32
)

type candidateTranslation struct {
	ISO6391  string `json:"iso_639_1"`
	ISO31661 string `json:"iso_3166_1"`
	Data     struct {
		Title string `json:"title"`
		Name  string `json:"name"`
	} `json:"data"`
}

type candidateAlternativeTitle struct {
	ISO31661 string `json:"iso_3166_1"`
	Title    string `json:"title"`
}

type candidateEnrichmentResponse struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"original_title"`
	Name             string  `json:"name"`
	OriginalName     string  `json:"original_name"`
	OriginalLanguage string  `json:"original_language"`
	ReleaseDate      string  `json:"release_date"`
	FirstAirDate     string  `json:"first_air_date"`
	Popularity       float64 `json:"popularity"`
	VoteCount        int     `json:"vote_count"`
	PosterPath       string  `json:"poster_path"`
	NumberOfSeasons  int     `json:"number_of_seasons"`
	NumberOfEpisodes int     `json:"number_of_episodes"`
	Seasons          []struct {
		SeasonNumber int    `json:"season_number"`
		AirDate      string `json:"air_date"`
	} `json:"seasons"`
	AlternativeTitles struct {
		Titles  []candidateAlternativeTitle `json:"titles"`
		Results []candidateAlternativeTitle `json:"results"`
	} `json:"alternative_titles"`
	Translations struct {
		Translations []candidateTranslation `json:"translations"`
	} `json:"translations"`
}

// EnrichCandidates adds bounded alternative-title, translation and TV
// structure evidence to at most limit candidates. A single malformed or
// unavailable detail response degrades to the original search summary; only
// caller cancellation aborts the whole operation.
func (c *Client) EnrichCandidates(ctx context.Context, candidates []Candidate, language string, limit int) ([]Candidate, error) {
	result := append([]Candidate(nil), candidates...)
	if len(result) == 0 {
		return result, nil
	}
	if limit <= 0 || limit > DefaultCandidateEnrichmentLimit {
		limit = DefaultCandidateEnrichmentLimit
	}
	if limit > len(result) {
		limit = len(result)
	}
	seen := make(map[string]candidateEnrichmentResponse, limit)
	for index := 0; index < limit; index++ {
		candidate := result[index]
		if candidate.ID <= 0 || (candidate.MediaType != "movie" && candidate.MediaType != "tv") {
			continue
		}
		key := candidate.MediaType + ":" + strconv.FormatInt(candidate.ID, 10)
		detail, exists := seen[key]
		if !exists {
			var err error
			detail, err = c.getCandidateEnrichment(ctx, candidate.MediaType, candidate.ID, language)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil, err
				}
				continue
			}
			seen[key] = detail
		}
		result[index] = mergeCandidateEnrichment(candidate, detail)
	}
	return result, nil
}

func (c *Client) getCandidateEnrichment(ctx context.Context, mediaType string, id int64, language string) (candidateEnrichmentResponse, error) {
	values := url.Values{"append_to_response": {"alternative_titles,translations"}}
	if language = strings.TrimSpace(language); language != "" && len(language) <= 32 && !strings.ContainsAny(language, "\r\n\t/?#\\") {
		values.Set("language", language)
	}
	var response candidateEnrichmentResponse
	if err := c.get(ctx, "/"+mediaType+"/"+strconv.FormatInt(id, 10), values, &response); err != nil {
		return candidateEnrichmentResponse{}, err
	}
	if response.ID != 0 && response.ID != id {
		return candidateEnrichmentResponse{}, clientError(ErrorInvalidResponse, nil)
	}
	response.ID = id
	return response, nil
}

func mergeCandidateEnrichment(candidate Candidate, detail candidateEnrichmentResponse) Candidate {
	alternatives := make([]string, 0, maxCandidateAliases)
	translations := make([]string, 0, maxCandidateAliases)
	seen := make(map[string]struct{}, maxCandidateAliases)
	add := func(target *[]string, value string) {
		value = cleanText(value, 512)
		key := strings.ToLower(value)
		if value == "" || len(alternatives)+len(translations) >= maxCandidateAliases {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		*target = append(*target, value)
	}
	for _, value := range []string{candidate.Title, candidate.OriginalTitle} {
		value = cleanText(value, 512)
		if value != "" {
			seen[strings.ToLower(value)] = struct{}{}
		}
	}
	add(&alternatives, detail.Title)
	add(&alternatives, detail.OriginalTitle)
	add(&alternatives, detail.Name)
	add(&alternatives, detail.OriginalName)
	for _, alternative := range detail.AlternativeTitles.Titles {
		add(&alternatives, alternative.Title)
	}
	for _, alternative := range detail.AlternativeTitles.Results {
		add(&alternatives, alternative.Title)
	}
	for _, translation := range detail.Translations.Translations {
		add(&translations, translation.Data.Title)
		add(&translations, translation.Data.Name)
	}
	candidate.AlternativeTitles = alternatives
	candidate.Translations = translations
	if candidate.OriginalTitle == "" {
		if candidate.MediaType == "tv" {
			candidate.OriginalTitle = cleanText(detail.OriginalName, 512)
		} else {
			candidate.OriginalTitle = cleanText(detail.OriginalTitle, 512)
		}
	}
	if candidate.OriginalLanguage == "" {
		candidate.OriginalLanguage = strings.ToLower(cleanCode(detail.OriginalLanguage))
	}
	if candidate.ReleaseYear == nil {
		if candidate.MediaType == "tv" {
			candidate.ReleaseYear = parseYear(detail.FirstAirDate)
		} else {
			candidate.ReleaseYear = parseYear(detail.ReleaseDate)
		}
	}
	if candidate.Popularity == 0 {
		candidate.Popularity = boundedPopularity(detail.Popularity)
	}
	if candidate.VoteCount == 0 && detail.VoteCount > 0 {
		candidate.VoteCount = boundedCount(detail.VoteCount)
	}
	if candidate.PosterPath == "" {
		candidate.PosterPath = cleanImagePath(detail.PosterPath)
	}
	if candidate.MediaType == "tv" {
		candidate.SeasonCount = boundedCount(detail.NumberOfSeasons)
		candidate.EpisodeCount = boundedCount(detail.NumberOfEpisodes)
		seasonYears := make(map[int]int, min(len(detail.Seasons), 200))
		for _, season := range detail.Seasons {
			if len(seasonYears) == 200 || season.SeasonNumber < 0 || season.SeasonNumber > 200 {
				continue
			}
			if year := parseYear(season.AirDate); year != nil {
				seasonYears[season.SeasonNumber] = *year
			}
		}
		if len(seasonYears) > 0 {
			candidate.SeasonYears = seasonYears
		}
	}
	return candidate
}

func boundedPopularity(value float64) float64 {
	if value <= 0 || value > 1_000_000_000 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}
