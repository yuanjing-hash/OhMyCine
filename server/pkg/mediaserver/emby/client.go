package emby

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const maxProbeResponseBytes = 1 << 20

const maxAPIKeyBytes = 4096

// Config contains the privileged server-side credential used only for
// explicit Emby administration calls such as connection probes. It must not
// be reused by the client-facing reverse proxy.
type Config struct {
	Endpoint string
	APIKey   string
}

type ServerInfo struct {
	ID      string
	Name    string
	Version string
}

type ManagementSummary struct {
	Server       ServerInfo
	LibraryCount *int
	MovieCount   *int64
	SeriesCount  *int64
	EpisodeCount *int64
	Partial      bool
}

type Client struct {
	endpoint *url.URL
	apiKey   string
	http     *http.Client
}

func New(config Config) (*Client, error) {
	endpoint, err := ParseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	apiKey, err := NormalizeAPIKey(config.APIKey)
	if err != nil {
		return nil, err
	}
	return &Client{
		endpoint: endpoint,
		apiKey:   apiKey,
		http: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// NormalizeAPIKey validates the credential before it can enter either the
// encrypted connection envelope or an HTTP header. Emby keys are opaque, but
// they never need control characters or an unbounded header value.
func NormalizeAPIKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxAPIKeyBytes {
		return "", errors.New("emby api key is invalid")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return "", errors.New("emby api key is invalid")
		}
	}
	return value, nil
}

// ParseEndpoint validates and canonicalizes the fixed administrator-selected
// upstream. Query strings, fragments and embedded credentials are forbidden.
func ParseEndpoint(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 2048 {
		return nil, errors.New("emby endpoint is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("emby endpoint is invalid")
	}
	escapedPath := strings.ToLower(parsed.EscapedPath())
	if strings.Contains(parsed.Path, "\\") || strings.ContainsRune(parsed.Path, '\x00') || strings.Contains(escapedPath, "%00") || strings.Contains(escapedPath, "%25") || strings.Contains(escapedPath, "%2f") || strings.Contains(escapedPath, "%5c") {
		return nil, errors.New("emby endpoint path is invalid")
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(parsed.Path, "/"))
	if cleaned == "/." {
		cleaned = "/"
	}
	parsed.Path = strings.TrimRight(cleaned, "/")
	parsed.RawPath = ""
	return parsed, nil
}

func (c *Client) Probe(ctx context.Context) (ServerInfo, error) {
	var payload struct {
		ID         string `json:"Id"`
		ServerName string `json:"ServerName"`
		Version    string `json:"Version"`
	}
	if err := c.getJSON(ctx, "/System/Info", &payload); err != nil || strings.TrimSpace(payload.ID) == "" {
		return ServerInfo{}, errors.New("emby probe response is invalid")
	}
	return ServerInfo{ID: strings.TrimSpace(payload.ID), Name: strings.TrimSpace(payload.ServerName), Version: strings.TrimSpace(payload.Version)}, nil
}

// ManagementSummary returns only aggregate administration facts. It never
// returns library names, item identities, media paths, users, sessions, or the
// raw Emby response. Optional counters remain nil when an older or restricted
// Emby installation cannot provide that endpoint.
func (c *Client) ManagementSummary(ctx context.Context) (ManagementSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	server, err := c.Probe(ctx)
	if err != nil {
		return ManagementSummary{}, errors.New("emby management summary unavailable")
	}
	result := ManagementSummary{Server: server}
	var folders *[]struct {
		CollectionType string `json:"CollectionType"`
	}
	if err := c.getJSON(ctx, "/Library/VirtualFolders", &folders); err != nil {
		result.Partial = true
	} else if folders == nil {
		result.Partial = true
	} else {
		count := len(*folders)
		result.LibraryCount = &count
	}
	var counts struct {
		MovieCount   *int64 `json:"MovieCount"`
		SeriesCount  *int64 `json:"SeriesCount"`
		EpisodeCount *int64 `json:"EpisodeCount"`
	}
	if err := c.getJSON(ctx, "/Items/Counts", &counts); err != nil {
		result.Partial = true
	} else {
		result.MovieCount, result.SeriesCount, result.EpisodeCount = nonNegative(counts.MovieCount), nonNegative(counts.SeriesCount), nonNegative(counts.EpisodeCount)
		if result.MovieCount == nil || result.SeriesCount == nil || result.EpisodeCount == nil {
			result.Partial = true
		}
	}
	return result, nil
}

func nonNegative(value *int64) *int64 {
	if value == nil || *value < 0 {
		return nil
	}
	return value
}

func (c *Client) getJSON(ctx context.Context, suffix string, target any) error {
	requestURL := *c.endpoint
	requestURL.Path = joinPath(c.endpoint.Path, suffix)
	requestURL.RawPath, requestURL.RawQuery, requestURL.Fragment = "", "", ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return errors.New("build emby request")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Emby-Token", c.apiKey)
	response, err := c.http.Do(request)
	if err != nil {
		return errors.New("emby request unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("emby request returned status %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxProbeResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maxProbeResponseBytes {
		return errors.New("emby response is invalid")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("emby response is invalid")
	}
	return nil
}

func joinPath(base, suffix string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return suffix
	}
	return base + suffix
}
