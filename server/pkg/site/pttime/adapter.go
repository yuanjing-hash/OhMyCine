package pttime

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"golang.org/x/net/html"
)

const (
	Kind            = "pttime"
	maxHTMLBytes    = 4 << 20
	maxTorrentBytes = 4 << 20
)

type Adapter struct {
	kind          string
	profile       Profile
	clientFactory func(site.Config) (*http.Client, *url.URL, error)
}

// Profile keeps the standard NexusPHP request contract configurable per
// built-in tracker without duplicating the bounded HTTP implementation.
// Trackers with a genuinely different contract must use their own adapter.
type Profile struct {
	HealthPath   string
	SearchPath   string
	DownloadPath string
}

func NexusPHPProfile() Profile {
	return Profile{HealthPath: "/index.php", SearchPath: "/torrents.php", DownloadPath: "/download.php"}
}

func New() *Adapter                   { return NewForKind(Kind) }
func NewForKind(kind string) *Adapter { return NewForProfile(kind, NexusPHPProfile()) }
func NewForProfile(kind string, profile Profile) *Adapter {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = Kind
	}
	profile = normalizedProfile(profile)
	return &Adapter{kind: kind, profile: profile, clientFactory: controlledClient}
}
func NewForTest(client *http.Client) *Adapter {
	return &Adapter{kind: Kind, profile: NexusPHPProfile(), clientFactory: func(config site.Config) (*http.Client, *url.URL, error) {
		base, err := url.Parse(strings.TrimRight(config.BaseURL, "/"))
		return client, base, err
	}}
}
func (a *Adapter) Kind() string { return a.kind }

func (a *Adapter) Test(ctx context.Context, config site.Config) (site.Health, error) {
	body, _, err := a.request(ctx, config, a.profile.HealthPath, nil, maxHTMLBytes)
	if err != nil {
		return site.Health{}, err
	}
	if looksLikeLogin(body) || !hasAuthenticatedProof(body) {
		return site.Health{}, site.ErrAuthentication
	}
	return site.Health{Status: "online", Username: parseUsername(body)}, nil
}

func (a *Adapter) Search(ctx context.Context, config site.Config, query site.Query) (site.Page, error) {
	keyword := strings.TrimSpace(query.Keyword)
	if keyword == "" || len([]rune(keyword)) > 160 || query.Page < 1 || query.Page > 20 {
		return site.Page{}, site.ErrInvalidReply
	}
	if query.Year != nil {
		keyword += " " + strconv.Itoa(*query.Year)
	}
	values := url.Values{"search": {keyword}, "notnewword": {"1"}, "page": {strconv.Itoa(query.Page - 1)}}
	body, _, err := a.request(ctx, config, a.profile.SearchPath, values, maxHTMLBytes)
	if err != nil {
		return site.Page{}, err
	}
	if looksLikeLogin(body) {
		return site.Page{}, site.ErrAuthentication
	}
	items, skipped, hasNext, err := parseTorrentPage(body)
	if err != nil {
		return site.Page{}, err
	}
	return site.Page{Page: query.Page, HasNext: hasNext, Items: items, Skipped: skipped}, nil
}

func (a *Adapter) Download(ctx context.Context, config site.Config, torrentID string) ([]byte, string, error) {
	if !numericID(torrentID) {
		return nil, "", site.ErrNotFound
	}
	values := url.Values{"id": {torrentID}}
	if passkey := strings.TrimSpace(config.Passkey); passkey != "" {
		values.Set("passkey", passkey)
	}
	body, response, err := a.request(ctx, config, a.profile.DownloadPath, values, maxTorrentBytes)
	if err != nil {
		return nil, "", err
	}
	if looksLikeLogin(body) {
		return nil, "", site.ErrAuthentication
	}
	if len(body) < 16 || body[0] != 'd' {
		return nil, "", site.ErrInvalidReply
	}
	filename := torrentID + ".torrent"
	if disposition := response.Header.Get("Content-Disposition"); disposition != "" {
		if _, params, parseErr := mimeHeader(disposition); parseErr == nil {
			candidate := path.Base(strings.ReplaceAll(params["filename"], "\\", "/"))
			if strings.EqualFold(path.Ext(candidate), ".torrent") && len(candidate) <= 255 {
				filename = candidate
			}
		}
	}
	return body, filename, nil
}

func (a *Adapter) request(ctx context.Context, config site.Config, endpoint string, query url.Values, limit int64) ([]byte, *http.Response, error) {
	client, base, err := a.clientFactory(config)
	if err != nil {
		return nil, nil, site.ErrUnavailable
	}
	requestURL := *base
	requestURL.Path = strings.TrimRight(base.Path, "/") + endpoint
	requestURL.RawQuery = query.Encode()
	if config.BrowserEmulation && endpoint != a.profile.DownloadPath {
		return renderedRequest(ctx, config, requestURL.String(), limit)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, nil, site.ErrUnavailable
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/x-bittorrent;q=0.9")
	if cookie := strings.TrimSpace(config.Cookie); cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	request.Header.Set("User-Agent", normalizedUserAgent(config.UserAgent))
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, site.ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if readErr != nil || int64(len(body)) > limit {
		return nil, response, site.ErrInvalidReply
	}
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, response, site.ErrAuthentication
	case http.StatusTooManyRequests:
		return nil, response, site.ErrRateLimited
	default:
		return nil, response, site.ErrUnavailable
	}
	return body, response, nil
}

func renderedRequest(ctx context.Context, config site.Config, target string, limit int64) ([]byte, *http.Response, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, nil, site.ErrUnavailable
	}
	// PT browser emulation is a legacy, explicitly configured FlareSolverr
	// compatibility path. It bypasses the public-BT Cloak routing and never
	// forwards the tracker's Cookie or passkey outside the Server.
	flare, err := site.NewFlareSolverrFetcher(config.BrowserServiceURL)
	if err != nil {
		return nil, nil, site.ErrUnavailable
	}
	page, err := flare.Fetch(ctx, site.RenderedFetchRequest{ProfileID: "private-pt-legacy", URL: target, AllowedHosts: []string{parsed.Hostname()}, Timeout: config.Timeout, MaxBytes: limit})
	response := &http.Response{StatusCode: page.StatusCode, Header: make(http.Header)}
	if err != nil {
		return nil, response, err
	}
	return page.HTML, response, nil
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
	client := &http.Client{Timeout: timeout, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !strings.EqualFold(request.URL.Host, base.Host) || request.URL.Scheme != "https" {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	return client, base, nil
}

func normalizedUserAgent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "OhMyCine/1.0 (+https://github.com/yuanjing-hash/OhMyCine)"
	}
	if len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
		return "OhMyCine/1.0 (+https://github.com/yuanjing-hash/OhMyCine)"
	}
	return value
}

func numericID(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

func normalizedProfile(profile Profile) Profile {
	defaults := NexusPHPProfile()
	if !safeEndpointPath(profile.HealthPath) {
		profile.HealthPath = defaults.HealthPath
	}
	if !safeEndpointPath(profile.SearchPath) {
		profile.SearchPath = defaults.SearchPath
	}
	if !safeEndpointPath(profile.DownloadPath) {
		profile.DownloadPath = defaults.DownloadPath
	}
	return profile
}

func safeEndpointPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "?#\\\x00\r\n") && !strings.Contains(value, "..")
}

func looksLikeLogin(body []byte) bool {
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "takelogin.php") ||
		strings.Contains(lower, "login.php") && strings.Contains(lower, "password") ||
		strings.Contains(lower, "name=\"password\"") ||
		strings.Contains(lower, "name='password'") ||
		strings.Contains(lower, "powered by nexusphp") && strings.Contains(lower, "login")
}

func hasAuthenticatedProof(body []byte) bool {
	// Match the same positive proof NexusPHP exposes to an authenticated
	// browser. A raw substring is insufficient because anonymous pages may
	// mention logout.php in comments, scripts or documentation.
	document, err := html.Parse(bytes.NewReader(body))
	if err == nil {
		for _, anchor := range findNodes(document, "a") {
			parsed, parseErr := urlParseRelative(attr(anchor, "href"))
			if parseErr == nil && strings.EqualFold(path.Base(parsed.Path), "logout.php") {
				return true
			}
		}
	}
	text := strings.ToLower(plainText(body))
	return strings.Contains(text, "欢迎回来") || strings.Contains(text, "welcome back")
}

func parseUsername(body []byte) string {
	text := plainText(body)
	for _, marker := range []string{"欢迎回来", "Welcome back"} {
		if index := strings.Index(text, marker); index >= 0 {
			value := strings.TrimSpace(text[index+len(marker):])
			if cut := strings.IndexAny(value, " |,，\n"); cut >= 0 {
				value = value[:cut]
			}
			if value != "" && len([]rune(value)) <= 64 {
				return value
			}
		}
	}
	return ""
}

// mimeHeader is a tiny indirection for tests and keeps Content-Disposition
// parsing out of the security-sensitive URL code.
var mimeHeader = func(value string) (string, map[string]string, error) { return mimeParse(value) }

func plainText(body []byte) string {
	return strings.TrimSpace(strings.Join(strings.Fields(string(body)), " "))
}
