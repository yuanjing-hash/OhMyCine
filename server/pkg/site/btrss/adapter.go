package btrss

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/rssfeed"
)

const (
	maxFeedBytes    = 4 << 20
	maxTorrentBytes = 4 << 20
)

var btihPattern = regexp.MustCompile(`(?i)^[a-f0-9]{40}$|^[a-z2-7]{32}$`)

type Profile struct {
	SearchPath           string
	QueryParameter       string
	FixedQuery           map[string]string
	AllowedFeedHosts     []string
	AllowedDownloadHosts []string
	AllowedPathPrefixes  []string
}

type Adapter struct {
	kind          string
	profile       Profile
	clientFactory func(site.Config) (*http.Client, *url.URL, error)
}

func NyaaProfile() Profile {
	return Profile{SearchPath: "/", QueryParameter: "q", FixedQuery: map[string]string{"page": "rss", "c": "0_0", "f": "0"}, AllowedFeedHosts: []string{"nyaa.si"}, AllowedDownloadHosts: []string{"nyaa.si"}, AllowedPathPrefixes: []string{"/download/"}}
}
func AnimeToshoProfile() Profile {
	return Profile{SearchPath: "/rss2", QueryParameter: "q", FixedQuery: map[string]string{"only_tor": "1"}, AllowedFeedHosts: []string{"feed.animetosho.org", "animetosho.org"}, AllowedDownloadHosts: []string{"feed.animetosho.org", "animetosho.org"}, AllowedPathPrefixes: []string{"/storage/", "/torrent/", "/download/"}}
}
func TokyoToshokanProfile() Profile {
	return Profile{SearchPath: "/rss.php", QueryParameter: "terms", FixedQuery: map[string]string{"filter": "1"}, AllowedFeedHosts: []string{"www.tokyotosho.info", "tokyotosho.info"}, AllowedDownloadHosts: []string{"www.tokyotosho.info", "tokyotosho.info"}, AllowedPathPrefixes: []string{"/download.php", "/torrent/"}}
}
func MikanProfile() Profile {
	return Profile{SearchPath: "/RSS/Search", QueryParameter: "searchstr", AllowedFeedHosts: []string{"mikanani.me"}, AllowedDownloadHosts: []string{"mikanani.me"}, AllowedPathPrefixes: []string{"/Download/"}}
}
func AniDexProfile() Profile {
	return Profile{SearchPath: "/rss/", QueryParameter: "q", AllowedFeedHosts: []string{"anidex.info"}, AllowedDownloadHosts: []string{"anidex.info"}, AllowedPathPrefixes: []string{"/torrent/", "/dl/"}}
}
func DMHYProfile() Profile {
	return Profile{SearchPath: "/topics/rss/rss.xml", QueryParameter: "keyword", AllowedFeedHosts: []string{"share.dmhy.org"}, AllowedDownloadHosts: []string{"share.dmhy.org"}, AllowedPathPrefixes: []string{"/topics/view/", "/topics/download/", "/torrent/"}}
}
func ACGRipProfile() Profile {
	return Profile{SearchPath: "/.xml", QueryParameter: "term", AllowedFeedHosts: []string{"acg.rip"}, AllowedDownloadHosts: []string{"acg.rip"}, AllowedPathPrefixes: []string{"/t/", "/torrent/"}}
}

func NewForProfile(kind string, profile Profile) *Adapter {
	return &Adapter{kind: strings.ToLower(strings.TrimSpace(kind)), profile: normalizedProfile(profile), clientFactory: controlledClient}
}

func NewForTest(kind string, profile Profile, client *http.Client, baseURL string) *Adapter {
	profile = normalizedProfile(profile)
	if parsed, err := url.Parse(baseURL); err == nil {
		profile.AllowedFeedHosts = append(profile.AllowedFeedHosts, parsed.Hostname())
	}
	return &Adapter{kind: kind, profile: profile, clientFactory: func(site.Config) (*http.Client, *url.URL, error) {
		base, err := url.Parse(strings.TrimRight(baseURL, "/"))
		return client, base, err
	}}
}

func (a *Adapter) Kind() string { return a.kind }

func (a *Adapter) Test(ctx context.Context, config site.Config) (site.Health, error) {
	body, err := a.requestFeed(ctx, config, "anime", 1)
	if err != nil {
		return site.Health{}, err
	}
	if _, err := rssfeed.Parse(body); err != nil {
		return site.Health{}, err
	}
	return site.Health{Status: "online"}, nil
}

func (a *Adapter) Search(ctx context.Context, config site.Config, query site.Query) (site.Page, error) {
	keyword := strings.TrimSpace(query.Keyword)
	if keyword == "" || len([]rune(keyword)) > 160 || query.Page < 1 || query.Page > 20 {
		return site.Page{}, site.ErrInvalidReply
	}
	if query.Year != nil {
		keyword += " " + strconv.Itoa(*query.Year)
	}
	body, err := a.requestFeed(ctx, config, keyword, query.Page)
	if err != nil {
		return site.Page{}, err
	}
	parsed, err := rssfeed.Parse(body)
	if err != nil {
		return site.Page{}, err
	}
	result := site.Page{Page: query.Page, Items: make([]site.Result, 0, len(parsed))}
	for _, item := range parsed {
		identity := ""
		for _, candidate := range item.Sources {
			if value, ok := a.sourceIdentity(config.BaseURL, candidate); ok {
				identity = value
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
	kind, value, ok := DecodeIdentity(identity)
	if !ok {
		return site.Source{}, site.ErrNotFound
	}
	if kind == "magnet" {
		magnet, ok := NormalizeMagnet(value)
		if !ok {
			return site.Source{}, site.ErrInvalidReply
		}
		return site.Source{Magnet: magnet}, nil
	}
	if kind != "torrent" {
		return site.Source{}, site.ErrNotFound
	}
	client, base, err := a.clientFactory(config)
	if err != nil || !a.safeTorrentURL(base.String(), value) {
		return site.Source{}, site.ErrUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
	if err != nil {
		return site.Source{}, site.ErrUnavailable
	}
	request.Header.Set("Accept", "application/x-bittorrent")
	request.Header.Set("User-Agent", normalizedUserAgent(config.UserAgent))
	response, err := client.Do(request)
	if err != nil {
		return site.Source{}, site.ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
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
	filename := path.Base(request.URL.Path)
	if !strings.EqualFold(path.Ext(filename), ".torrent") || len(filename) > 255 {
		filename = a.kind + ".torrent"
	}
	return site.Source{Torrent: body, Filename: filename}, nil
}

func (a *Adapter) requestFeed(ctx context.Context, config site.Config, keyword string, page int) ([]byte, error) {
	client, base, err := a.clientFactory(config)
	if err != nil {
		return nil, site.ErrUnavailable
	}
	if !containsHost(a.profile.AllowedFeedHosts, base.Hostname()) {
		return nil, site.ErrUnavailable
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + a.profile.SearchPath
	query := target.Query()
	for key, value := range a.profile.FixedQuery {
		query.Set(key, value)
	}
	query.Set(a.profile.QueryParameter, keyword)
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, site.ErrUnavailable
	}
	request.Header.Set("Accept", "application/rss+xml,application/xml;q=0.9")
	request.Header.Set("User-Agent", normalizedUserAgent(config.UserAgent))
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
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFeedBytes+1))
	if err != nil || len(body) > maxFeedBytes {
		return nil, site.ErrInvalidReply
	}
	return body, nil
}

func (a *Adapter) sourceIdentity(baseURL, candidate string) (string, bool) {
	if magnet, ok := NormalizeMagnet(candidate); ok {
		return EncodeIdentity("magnet", magnet), true
	}
	if a.safeTorrentURL(baseURL, candidate) {
		return EncodeIdentity("torrent", candidate), true
	}
	return "", false
}

func (a *Adapter) safeTorrentURL(baseURL, raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || len(raw) > 4096 {
		return false
	}
	allowed := false
	for _, host := range a.profile.AllowedDownloadHosts {
		if strings.EqualFold(parsed.Hostname(), host) && (parsed.Port() == "" || parsed.Port() == "443") {
			allowed = true
			break
		}
	}
	if !allowed {
		if base, baseErr := url.Parse(baseURL); baseErr == nil {
			allowed = strings.EqualFold(base.Host, parsed.Host)
		}
	}
	if !allowed {
		return false
	}
	if strings.EqualFold(path.Ext(parsed.Path), ".torrent") {
		return true
	}
	for _, prefix := range a.profile.AllowedPathPrefixes {
		if strings.HasPrefix(parsed.Path, prefix) {
			return true
		}
	}
	return false
}

func NormalizeMagnet(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(parsed.Scheme, "magnet") || parsed.Opaque != "" || parsed.User != nil || len(raw) > 16<<10 {
		return "", false
	}
	for _, xt := range parsed.Query()["xt"] {
		const prefix = "urn:btih:"
		if len(xt) > len(prefix) && strings.EqualFold(xt[:len(prefix)], prefix) {
			hash := strings.ToLower(xt[len(prefix):])
			if btihPattern.MatchString(hash) {
				return "magnet:?xt=" + prefix + hash, true
			}
		}
	}
	return "", false
}

type sourceIdentity struct {
	Kind  string `json:"k"`
	Value string `json:"v"`
}

func EncodeIdentity(kind, value string) string {
	raw, _ := json.Marshal(sourceIdentity{Kind: kind, Value: value})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeIdentity(value string) (string, string, bool) {
	if value == "" || len(value) > 8192 {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	var identity sourceIdentity
	if json.Unmarshal(raw, &identity) != nil || identity.Value == "" {
		return "", "", false
	}
	return identity.Kind, identity.Value, true
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

func normalizedUserAgent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
		return "OhMyCine/1.0 (+https://github.com/yuanjing-hash/OhMyCine)"
	}
	return value
}

func normalizedProfile(profile Profile) Profile {
	if !strings.HasPrefix(profile.SearchPath, "/") || strings.Contains(profile.SearchPath, "..") {
		profile.SearchPath = "/"
	}
	if profile.QueryParameter == "" || strings.ContainsAny(profile.QueryParameter, "&=?\x00\r\n") {
		profile.QueryParameter = "q"
	}
	profile.FixedQuery = cloneMap(profile.FixedQuery)
	profile.AllowedFeedHosts = append([]string(nil), profile.AllowedFeedHosts...)
	profile.AllowedDownloadHosts = append([]string(nil), profile.AllowedDownloadHosts...)
	profile.AllowedPathPrefixes = append([]string(nil), profile.AllowedPathPrefixes...)
	return profile
}

func cloneMap(input map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range input {
		result[key] = value
	}
	return result
}

func containsHost(values []string, actual string) bool {
	for _, value := range values {
		if strings.EqualFold(value, actual) {
			return true
		}
	}
	return false
}
