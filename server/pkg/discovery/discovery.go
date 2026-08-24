package discovery

import (
	"context"
	"errors"
	"time"
)

const (
	ProviderTMDB   = "tmdb"
	ProviderDouban = "douban"

	MediaTypeMovie = "movie"
	MediaTypeTV    = "tv"
)

var (
	ErrInvalidRequest = errors.New("discovery_invalid_request")
	ErrUnavailable    = errors.New("discovery_provider_unavailable")
	ErrInvalidReply   = errors.New("discovery_provider_invalid_response")
)

type Work struct {
	Provider      string   `json:"provider"`
	ProviderID    string   `json:"provider_id"`
	MediaType     string   `json:"media_type"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"original_title,omitempty"`
	Year          *int     `json:"year,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	Rating        *float64 `json:"rating,omitempty"`
	VoteCount     *int     `json:"vote_count,omitempty"`
	PosterURL     string   `json:"poster_url,omitempty"`
	BackdropURL   string   `json:"backdrop_url,omitempty"`
	TMDBID        *int64   `json:"tmdb_id,omitempty"`
	DoubanID      string   `json:"douban_id,omitempty"`
}

type Section struct {
	Provider  string    `json:"provider"`
	Code      string    `json:"code"`
	Title     string    `json:"title"`
	MediaType string    `json:"media_type,omitempty"`
	Category  string    `json:"category,omitempty"`
	Page      int       `json:"page"`
	TotalPage int       `json:"total_pages"`
	Items     []Work    `json:"items"`
	FetchedAt time.Time `json:"fetched_at"`
	Stale     bool      `json:"stale"`
	ErrorCode string    `json:"error_code,omitempty"`
}

type Request struct {
	Section  string
	Page     int
	Language string
	Region   string
}

type Provider interface {
	Code() string
	Sections() []SectionDefinition
	Fetch(context.Context, Request) (Section, error)
}

type SectionDefinition struct {
	Code      string `json:"code"`
	Title     string `json:"title"`
	MediaType string `json:"media_type,omitempty"`
	Category  string `json:"category,omitempty"`
}
