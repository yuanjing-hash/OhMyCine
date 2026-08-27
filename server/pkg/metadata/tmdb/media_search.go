package tmdb

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

const MaxMediaSearchPage = 500

// SearchMedia returns a bounded poster-search page for the default discovery
// experience. It accepts only TMDB movie/tv identities and discards people
// returned by the multi-search endpoint.
func (c *Client) SearchMedia(ctx context.Context, query, mediaType string, page int, language, region string) (DiscoveryPage, error) {
	query = strings.Join(strings.Fields(query), " ")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if query == "" || len([]rune(query)) > 256 || page < 1 || page > MaxMediaSearchPage || (mediaType != "" && mediaType != "all" && mediaType != "movie" && mediaType != "tv") {
		return DiscoveryPage{}, clientError(ErrorInvalidRequest, nil)
	}
	endpoint := "/search/multi"
	if mediaType == "movie" || mediaType == "tv" {
		endpoint = "/search/" + mediaType
	}
	values := url.Values{"query": {query}, "page": {strconv.Itoa(page)}, "include_adult": {"false"}}
	if language = normalizeTMDBLanguage(language); language != "" {
		values.Set("language", language)
	}
	if region = normalizeTMDBRegion(region); region != "" {
		values.Set("region", region)
	}
	var response struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
		Results    []struct {
			ID            int64   `json:"id"`
			MediaType     string  `json:"media_type"`
			Title         string  `json:"title"`
			OriginalTitle string  `json:"original_title"`
			Name          string  `json:"name"`
			OriginalName  string  `json:"original_name"`
			ReleaseDate   string  `json:"release_date"`
			FirstAirDate  string  `json:"first_air_date"`
			Overview      string  `json:"overview"`
			VoteAverage   float64 `json:"vote_average"`
			VoteCount     int     `json:"vote_count"`
			PosterPath    string  `json:"poster_path"`
			BackdropPath  string  `json:"backdrop_path"`
		} `json:"results"`
	}
	if err := c.get(ctx, endpoint, values, &response); err != nil {
		return DiscoveryPage{}, err
	}
	result := DiscoveryPage{Page: max(1, response.Page), TotalPages: min(MaxMediaSearchPage, max(1, response.TotalPages)), Items: make([]DiscoveryItem, 0, len(response.Results))}
	for _, raw := range response.Results {
		kind := mediaType
		if kind == "" || kind == "all" {
			kind = strings.ToLower(strings.TrimSpace(raw.MediaType))
		}
		if kind != "movie" && kind != "tv" {
			continue
		}
		title, original, date := raw.Title, raw.OriginalTitle, raw.ReleaseDate
		if kind == "tv" {
			title, original, date = raw.Name, raw.OriginalName, raw.FirstAirDate
		}
		title = cleanText(title, 512)
		if raw.ID <= 0 || title == "" {
			continue
		}
		var rating *float64
		if raw.VoteAverage >= 0 && raw.VoteAverage <= 10 {
			value := boundedRating(raw.VoteAverage)
			rating = &value
		}
		var votes *int
		if raw.VoteCount >= 0 {
			value := boundedCount(raw.VoteCount)
			votes = &value
		}
		result.Items = append(result.Items, DiscoveryItem{ID: raw.ID, MediaType: kind, Title: title, OriginalTitle: cleanText(original, 512), Year: parseYear(date), Overview: cleanText(raw.Overview, 32768), Rating: rating, VoteCount: votes, PosterPath: cleanImagePath(raw.PosterPath), BackdropPath: cleanImagePath(raw.BackdropPath)})
	}
	return result, nil
}
