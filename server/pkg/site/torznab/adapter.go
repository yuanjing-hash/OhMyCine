package torznab

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/btrss"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/rssfeed"
)

const (
	Kind            = "torznab"
	maxXMLBytes     = 4 << 20
	maxTorrentBytes = 4 << 20
)

type Adapter struct {
	clientFactory func(site.Config) (*http.Client, *url.URL, error)
}

func New() *Adapter { return &Adapter{clientFactory: controlledClient} }

func NewForTest(client *http.Client, baseURL string) *Adapter {
	return &Adapter{clientFactory: func(site.Config) (*http.Client, *url.URL, error) {
		base, err := url.Parse(strings.TrimRight(baseURL, "/"))
		return client, base, err
	}}
}

func (a *Adapter) Kind() string { return Kind }

func (a *Adapter) Test(ctx context.Context, config site.Config) (site.Health, error) {
	body, err := a.request(ctx, config, url.Values{"t": {"caps"}})
	if err != nil {
		return site.Health{}, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	for {
		token, tokenErr := decoder.Token()
		if tokenErr == io.EOF {
			break
		}
		if tokenErr != nil {
			return site.Health{}, site.ErrInvalidReply
		}
		if start, ok := token.(xml.StartElement); ok {
			if strings.EqualFold(start.Name.Local, "caps") {
				return site.Health{Status: "online"}, nil
			}
			return site.Health{}, site.ErrInvalidReply
		}
	}
	return site.Health{}, site.ErrInvalidReply
}

func (a *Adapter) Search(ctx context.Context, config site.Config, query site.Query) (site.Page, error) {
	keyword := strings.TrimSpace(query.Keyword)
	if keyword == "" || len([]rune(keyword)) > 160 || query.Page < 1 || query.Page > 20 {
		return site.Page{}, site.ErrInvalidReply
	}
	if query.Year != nil {
		keyword += " " + strconv.Itoa(*query.Year)
	}
	values := url.Values{"t": {"search"}, "q": {keyword}, "limit": {"100"}, "offset": {strconv.Itoa((query.Page - 1) * 100)}}
	switch strings.ToLower(query.MediaType) {
	case "movie":
		values.Set("cat", "2000")
	case "tv":
		values.Set("cat", "5000")
	}
	body, err := a.request(ctx, config, values)
	if err != nil {
		return site.Page{}, err
	}
	parsed, err := rssfeed.Parse(body)
	if err != nil {
		return site.Page{}, err
	}
	result := site.Page{Page: query.Page, HasNext: len(parsed) >= 100, Items: make([]site.Result, 0, len(parsed))}
	for _, item := range parsed {
		identity := ""
		for _, candidate := range item.Sources {
			if magnet, ok := btrss.NormalizeMagnet(candidate); ok {
				identity = btrss.EncodeIdentity("magnet", magnet)
				break
			}
			if safeTorznabURL(config.BaseURL, candidate) {
				identity = btrss.EncodeIdentity("torrent", candidate)
				break
			}
		}
		if identity == "" {
			result.Skipped++
			continue
		}
		result.Items = append(result.Items, site.Result{TorrentID: identity, Title: item.Title, Subtitle: item.Subtitle, SizeBytes: item.SizeBytes, Published: item.Published, Seeders: item.Seeders, Leechers: item.Leechers, Completed: item.Completed})
	}
	return result, nil
}

func (a *Adapter) Download(context.Context, site.Config, string) ([]byte, string, error) {
	return nil, "", site.ErrNotFound
}

func (a *Adapter) ResolveSource(ctx context.Context, config site.Config, identity string) (site.Source, error) {
	kind, raw, ok := btrss.DecodeIdentity(identity)
	if !ok {
		return site.Source{}, site.ErrNotFound
	}
	if kind == "magnet" {
		magnet, valid := btrss.NormalizeMagnet(raw)
		if !valid {
			return site.Source{}, site.ErrInvalidReply
		}
		return site.Source{Magnet: magnet}, nil
	}
	if kind != "torrent" || !safeTorznabURL(config.BaseURL, raw) {
		return site.Source{}, site.ErrInvalidReply
	}
	client, _, err := a.clientFactory(config)
	if err != nil {
		return site.Source{}, site.ErrUnavailable
	}
	target, _ := url.Parse(raw)
	query := target.Query()
	// The feed-provided URL is untrusted input even though it is same-origin.
	// Always replace any embedded key with the encrypted configured value.
	query.Set("apikey", config.APIKey)
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return site.Source{}, site.ErrUnavailable
	}
	request.Header.Set("Accept", "application/x-bittorrent")
	response, err := client.Do(request)
	if err != nil {
		return site.Source{}, site.ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return site.Source{}, site.ErrAuthentication
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return site.Source{}, site.ErrRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return site.Source{}, site.ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTorrentBytes+1))
	if err != nil || len(body) < 16 || len(body) > maxTorrentBytes || body[0] != 'd' {
		return site.Source{}, site.ErrInvalidReply
	}
	filename := path.Base(target.Path)
	if !strings.EqualFold(path.Ext(filename), ".torrent") || len(filename) > 255 {
		filename = "torznab.torrent"
	}
	return site.Source{Torrent: body, Filename: filename}, nil
}

func (a *Adapter) request(ctx context.Context, config site.Config, query url.Values) ([]byte, error) {
	if strings.TrimSpace(config.APIKey) == "" || len(config.APIKey) > 2048 || strings.ContainsAny(config.APIKey, "\x00\r\n") {
		return nil, site.ErrAuthentication
	}
	client, base, err := a.clientFactory(config)
	if err != nil {
		return nil, site.ErrUnavailable
	}
	target := *base
	if !strings.HasSuffix(strings.TrimRight(target.Path, "/"), "/api") {
		target.Path = strings.TrimRight(target.Path, "/") + "/api"
	}
	query.Set("apikey", config.APIKey)
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, site.ErrUnavailable
	}
	request.Header.Set("Accept", "application/xml,application/rss+xml;q=0.9")
	response, err := client.Do(request)
	if err != nil {
		return nil, site.ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, site.ErrAuthentication
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, site.ErrRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return nil, site.ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxXMLBytes+1))
	if err != nil || len(body) > maxXMLBytes {
		return nil, site.ErrInvalidReply
	}
	return body, nil
}

func safeTorznabURL(baseURL, raw string) bool {
	base, baseErr := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	target, targetErr := url.Parse(strings.TrimSpace(raw))
	return baseErr == nil && targetErr == nil && base.Scheme == "https" && target.Scheme == "https" && base.Host != "" && strings.EqualFold(base.Host, target.Host) && target.User == nil && target.Fragment == "" && len(raw) <= 8192
}

func controlledClient(config site.Config) (*http.Client, *url.URL, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.BaseURL), "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, nil, site.ErrUnavailable
	}
	timeout := config.Timeout
	if timeout < 3*time.Second || timeout > 30*time.Second {
		timeout = 12 * time.Second
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 2 || next.URL.Scheme != "https" || !strings.EqualFold(next.URL.Host, base.Host) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	return client, base, nil
}
