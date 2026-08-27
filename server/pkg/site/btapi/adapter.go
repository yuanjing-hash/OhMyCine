package btapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/btrss"
)

const maxResponseBytes = 4 << 20

type Profile struct {
	Provider     string
	SearchPath   string
	AllowedHosts []string
}

func YTSProfile() Profile {
	return Profile{Provider: "yts", SearchPath: "/api/v2/list_movies.json", AllowedHosts: []string{"yts.mx"}}
}
func EZTVProfile() Profile {
	return Profile{Provider: "eztv", SearchPath: "/api/get-torrents", AllowedHosts: []string{"eztvx.to"}}
}

type Adapter struct {
	kind          string
	profile       Profile
	clientFactory func(site.Config) (*http.Client, *url.URL, error)
}

func NewForProfile(kind string, profile Profile) *Adapter {
	return &Adapter{kind: strings.ToLower(strings.TrimSpace(kind)), profile: profile, clientFactory: controlledClient}
}

func NewForTest(kind string, profile Profile, client *http.Client, baseURL string) *Adapter {
	return &Adapter{kind: kind, profile: profile, clientFactory: func(site.Config) (*http.Client, *url.URL, error) {
		base, err := url.Parse(strings.TrimRight(baseURL, "/"))
		return client, base, err
	}}
}

func (a *Adapter) Kind() string { return a.kind }

func (a *Adapter) Test(ctx context.Context, config site.Config) (site.Health, error) {
	if _, err := a.Search(ctx, config, site.Query{Keyword: "anime", Page: 1}); err != nil {
		return site.Health{}, err
	}
	return site.Health{Status: "online"}, nil
}

func (a *Adapter) Search(ctx context.Context, config site.Config, query site.Query) (site.Page, error) {
	keyword := strings.TrimSpace(query.Keyword)
	if keyword == "" || len([]rune(keyword)) > 160 || query.Page < 1 || query.Page > 20 {
		return site.Page{}, site.ErrInvalidReply
	}
	client, base, err := a.clientFactory(config)
	if err != nil || !containsHost(a.profile.AllowedHosts, base.Hostname()) {
		return site.Page{}, site.ErrUnavailable
	}
	target := *base
	target.Path = a.profile.SearchPath
	values := target.Query()
	values.Set("page", strconv.Itoa(query.Page))
	switch a.profile.Provider {
	case "yts":
		values.Set("limit", "50")
		values.Set("query_term", keyword)
	case "eztv":
		values.Set("limit", "100")
	}
	target.RawQuery = values.Encode()
	body, err := requestJSON(ctx, client, target.String())
	if err != nil {
		return site.Page{}, err
	}
	switch a.profile.Provider {
	case "yts":
		return parseYTS(body, query.Page)
	case "eztv":
		return parseEZTV(body, keyword, query.Page)
	default:
		return site.Page{}, site.ErrInvalidReply
	}
}

func (a *Adapter) Download(context.Context, site.Config, string) ([]byte, string, error) {
	return nil, "", site.ErrNotFound
}

func (a *Adapter) ResolveSource(_ context.Context, _ site.Config, identity string) (site.Source, error) {
	kind, magnet, ok := btrss.DecodeIdentity(identity)
	if !ok || kind != "magnet" {
		return site.Source{}, site.ErrNotFound
	}
	normalized, ok := btrss.NormalizeMagnet(magnet)
	if !ok {
		return site.Source{}, site.ErrInvalidReply
	}
	return site.Source{Magnet: normalized}, nil
}

type ytsResponse struct {
	Status string `json:"status"`
	Data   struct {
		MovieCount int `json:"movie_count"`
		Limit      int `json:"limit"`
		PageNumber int `json:"page_number"`
		Movies     []struct {
			TitleLong string `json:"title_long"`
			Torrents  []struct {
				Hash      string `json:"hash"`
				Quality   string `json:"quality"`
				Type      string `json:"type"`
				Seeds     int    `json:"seeds"`
				Peers     int    `json:"peers"`
				SizeBytes int64  `json:"size_bytes"`
				Uploaded  int64  `json:"date_uploaded_unix"`
			} `json:"torrents"`
		} `json:"movies"`
	} `json:"data"`
}

func parseYTS(body []byte, page int) (site.Page, error) {
	var payload ytsResponse
	if json.Unmarshal(body, &payload) != nil || !strings.EqualFold(payload.Status, "ok") {
		return site.Page{}, site.ErrInvalidReply
	}
	result := site.Page{Page: page, Items: []site.Result{}}
	for _, movie := range payload.Data.Movies {
		title := clean(movie.TitleLong, 512)
		if title == "" {
			continue
		}
		for _, torrent := range movie.Torrents {
			magnet, ok := magnetFromHash(torrent.Hash)
			if !ok {
				result.Skipped++
				continue
			}
			seeders, leechers := torrent.Seeds, torrent.Peers
			var published *time.Time
			if torrent.Uploaded > 0 {
				value := time.Unix(torrent.Uploaded, 0).UTC()
				published = &value
			}
			tags := compact([]string{torrent.Quality, torrent.Type})
			result.Items = append(result.Items, site.Result{TorrentID: btrss.EncodeIdentity("magnet", magnet), Title: title, SizeBytes: torrent.SizeBytes, Published: published, Seeders: &seeders, Leechers: &leechers, Quality: torrent.Quality, Tags: tags})
		}
	}
	limit := payload.Data.Limit
	if limit <= 0 {
		limit = 50
	}
	result.HasNext = page*limit < payload.Data.MovieCount
	return result, nil
}

type eztvResponse struct {
	TorrentCount int `json:"torrents_count"`
	Limit        int `json:"limit"`
	Page         int `json:"page"`
	Torrents     []struct {
		Title        string       `json:"title"`
		Hash         string       `json:"hash"`
		MagnetURL    string       `json:"magnet_url"`
		SizeBytes    boundedInt64 `json:"size_bytes"`
		Seeds        int          `json:"seeds"`
		Peers        int          `json:"peers"`
		DateReleased int64        `json:"date_released_unix"`
	} `json:"torrents"`
}

type boundedInt64 int64

func (value *boundedInt64) UnmarshalJSON(raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return err
		}
		text = decoded
	}
	if text == "" {
		return strconv.ErrSyntax
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return strconv.ErrSyntax
		}
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil || parsed < 0 {
		return strconv.ErrRange
	}
	*value = boundedInt64(parsed)
	return nil
}

func parseEZTV(body []byte, keyword string, page int) (site.Page, error) {
	var payload eztvResponse
	if json.Unmarshal(body, &payload) != nil || payload.Torrents == nil {
		return site.Page{}, site.ErrInvalidReply
	}
	needle := strings.ToLower(strings.Join(strings.Fields(keyword), " "))
	result := site.Page{Page: page, Items: []site.Result{}}
	for _, torrent := range payload.Torrents {
		title := clean(torrent.Title, 512)
		if title == "" || !strings.Contains(strings.ToLower(title), needle) {
			continue
		}
		magnet, ok := btrss.NormalizeMagnet(torrent.MagnetURL)
		if !ok {
			magnet, ok = magnetFromHash(torrent.Hash)
		}
		if !ok {
			result.Skipped++
			continue
		}
		seeders, leechers := torrent.Seeds, torrent.Peers
		var published *time.Time
		if torrent.DateReleased > 0 {
			value := time.Unix(torrent.DateReleased, 0).UTC()
			published = &value
		}
		result.Items = append(result.Items, site.Result{TorrentID: btrss.EncodeIdentity("magnet", magnet), Title: title, SizeBytes: int64(torrent.SizeBytes), Published: published, Seeders: &seeders, Leechers: &leechers})
	}
	limit := payload.Limit
	if limit <= 0 {
		limit = 100
	}
	result.HasNext = page*limit < payload.TorrentCount
	return result, nil
}

func magnetFromHash(hash string) (string, bool) {
	return btrss.NormalizeMagnet("magnet:?xt=urn:btih:" + strings.TrimSpace(hash))
}

func requestJSON(ctx context.Context, client *http.Client, target string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, site.ErrUnavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "OhMyCine/1.0 (+https://github.com/yuanjing-hash/OhMyCine)")
	response, err := client.Do(request)
	if err != nil {
		return nil, site.ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, site.ErrRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return nil, site.ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, site.ErrInvalidReply
	}
	return body, nil
}

func controlledClient(config site.Config) (*http.Client, *url.URL, error) {
	rawBase := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	base, err := url.Parse(rawBase)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || strings.Contains(rawBase, "#") {
		return nil, nil, site.ErrUnavailable
	}
	timeout := config.Timeout
	if timeout < 3*time.Second || timeout > 30*time.Second {
		timeout = 12 * time.Second
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 || next.URL.Scheme != "https" || !strings.EqualFold(next.URL.Host, via[0].URL.Host) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	return client, base, nil
}

func containsHost(values []string, actual string) bool {
	for _, value := range values {
		if strings.EqualFold(value, actual) {
			return true
		}
	}
	return false
}

func clean(value string, maximum int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if value == "" || len([]rune(value)) > maximum || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}

func compact(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = clean(value, 64); value != "" {
			result = append(result, value)
		}
	}
	return result
}
