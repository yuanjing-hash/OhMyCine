package tmdb

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

const maxSeasonEpisodeSnapshots = 1000

// EpisodeSnapshot is the bounded, credential-free episode metadata persisted
// beside a series recognition. Image fields contain TMDB file identities only.
type EpisodeSnapshot struct {
	TMDBID         int64   `json:"tmdb_id,omitempty"`
	SeasonNumber   int     `json:"season_number"`
	EpisodeNumber  int     `json:"episode_number"`
	Name           string  `json:"name,omitempty"`
	Overview       string  `json:"overview,omitempty"`
	AirDate        string  `json:"air_date,omitempty"`
	RuntimeMinutes int     `json:"runtime_minutes,omitempty"`
	StillPath      string  `json:"still_path,omitempty"`
	VoteAverage    float64 `json:"vote_average,omitempty"`
}

// GetTVSeasonEpisodes fetches one TV season. Callers are expected to request
// only seasons represented by their local catalog and persist the safe result.
func (c *Client) GetTVSeasonEpisodes(ctx context.Context, tvID int64, seasonNumber int, language string) ([]EpisodeSnapshot, error) {
	if tvID <= 0 || seasonNumber < 0 || seasonNumber > 10000 {
		return nil, clientError(ErrorInvalidRequest, nil)
	}
	values := url.Values{}
	if language = strings.TrimSpace(language); language != "" {
		values.Set("language", language)
	}
	var response struct {
		SeasonNumber int `json:"season_number"`
		Episodes     []struct {
			ID            int64   `json:"id"`
			Name          string  `json:"name"`
			Overview      string  `json:"overview"`
			AirDate       string  `json:"air_date"`
			EpisodeNumber int     `json:"episode_number"`
			SeasonNumber  int     `json:"season_number"`
			Runtime       int     `json:"runtime"`
			StillPath     string  `json:"still_path"`
			VoteAverage   float64 `json:"vote_average"`
		} `json:"episodes"`
	}
	endpoint := "/tv/" + strconv.FormatInt(tvID, 10) + "/season/" + strconv.Itoa(seasonNumber)
	if err := c.get(ctx, endpoint, values, &response); err != nil {
		return nil, err
	}
	result := make([]EpisodeSnapshot, 0, min(len(response.Episodes), maxSeasonEpisodeSnapshots))
	for _, episode := range response.Episodes {
		if episode.EpisodeNumber <= 0 || episode.EpisodeNumber > 100000 {
			continue
		}
		actualSeason := episode.SeasonNumber
		if actualSeason == 0 && seasonNumber != 0 {
			actualSeason = response.SeasonNumber
		}
		if actualSeason != seasonNumber {
			continue
		}
		result = append(result, EpisodeSnapshot{
			TMDBID:         max(int64(0), episode.ID),
			SeasonNumber:   actualSeason,
			EpisodeNumber:  episode.EpisodeNumber,
			Name:           cleanText(episode.Name, 512),
			Overview:       cleanText(episode.Overview, 8192),
			AirDate:        cleanDate(episode.AirDate),
			RuntimeMinutes: boundedRuntime(episode.Runtime),
			StillPath:      cleanImagePath(episode.StillPath),
			VoteAverage:    boundedRating(episode.VoteAverage),
		})
		if len(result) == maxSeasonEpisodeSnapshots {
			break
		}
	}
	return result, nil
}
