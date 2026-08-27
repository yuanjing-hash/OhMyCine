package site

import (
	"context"
	"errors"
	"time"
)

var (
	ErrAuthentication = errors.New("site_authentication_failed")
	ErrRateLimited    = errors.New("site_rate_limited")
	ErrUnavailable    = errors.New("site_unavailable")
	ErrInvalidReply   = errors.New("site_invalid_response")
	ErrNotFound       = errors.New("site_result_not_found")
)

type Config struct {
	BaseURL           string
	Cookie            string
	Passkey           string
	APIKey            string
	UserAgent         string
	Timeout           time.Duration
	BrowserEmulation  bool
	BrowserServiceURL string
	RenderedFetcher   RenderedFetcher
}

// Source is the server-only download material resolved from an opaque search
// result identity. Exactly one of Torrent or Magnet is populated.
type Source struct {
	Torrent  []byte
	Filename string
	Magnet   string
}

type Query struct {
	Keyword   string
	MediaType string
	Year      *int
	TMDBID    *int64
	Page      int
}

type Result struct {
	TorrentID string     `json:"-"`
	Title     string     `json:"title"`
	Subtitle  string     `json:"subtitle,omitempty"`
	SizeBytes int64      `json:"size_bytes,omitempty"`
	Published *time.Time `json:"published_at,omitempty"`
	Seeders   *int       `json:"seeders,omitempty"`
	Leechers  *int       `json:"leechers,omitempty"`
	Completed *int       `json:"completed,omitempty"`
	Promotion string     `json:"promotion,omitempty"`
	Quality   string     `json:"quality,omitempty"`
	Tags      []string   `json:"tags,omitempty"`
}

type Page struct {
	Page    int
	HasNext bool
	Items   []Result
	Skipped int
}

type Health struct {
	Username string
	Status   string
}

type Adapter interface {
	Kind() string
	Test(context.Context, Config) (Health, error)
	Search(context.Context, Config, Query) (Page, error)
	Download(context.Context, Config, string) ([]byte, string, error)
}

// SourceResolver is implemented by public BT and Torznab adapters whose
// search identities resolve to either a bounded torrent file or a normalized
// magnet. The identity never leaves the SiteService result vault.
type SourceResolver interface {
	ResolveSource(context.Context, Config, string) (Source, error)
}
