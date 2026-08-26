package bthtml

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

	"golang.org/x/net/html"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/btrss"
)

const maxHTMLBytes = 4 << 20

type Profile struct {
	SearchPath         string
	PathSearch         bool
	QueryParameter     string
	PageParameter      string
	PageOffset         int
	AllowedHosts       []string
	ResultPathPrefixes []string
}

func X1337Profile() Profile {
	return Profile{SearchPath: "/search/{query}/{page}/", PathSearch: true, AllowedHosts: []string{"1337x.to"}, ResultPathPrefixes: []string{"/torrent/"}}
}
func PirateBayProfile() Profile {
	return Profile{SearchPath: "/search.php", QueryParameter: "q", PageParameter: "page", PageOffset: -1, AllowedHosts: []string{"thepiratebay.org"}, ResultPathPrefixes: []string{"/description.php", "/torrent/"}}
}
func EXTToProfile() Profile {
	return Profile{SearchPath: "/browse/", QueryParameter: "q", PageParameter: "page", AllowedHosts: []string{"ext.to"}, ResultPathPrefixes: []string{"/torrent/"}}
}
func LimeTorrentsProfile() Profile {
	return Profile{SearchPath: "/search/all/{query}/seeds/{page}/", PathSearch: true, AllowedHosts: []string{"www.limetorrents.lol", "limetorrents.lol"}, ResultPathPrefixes: []string{"/torrent/"}}
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
	if parsed, err := url.Parse(baseURL); err == nil {
		profile.AllowedHosts = append(profile.AllowedHosts, parsed.Hostname())
	}
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
	if a.profile.PathSearch {
		target.Path = strings.ReplaceAll(strings.ReplaceAll(a.profile.SearchPath, "{query}", url.PathEscape(keyword)), "{page}", strconv.Itoa(query.Page+a.profile.PageOffset))
	} else {
		target.Path = a.profile.SearchPath
		values := target.Query()
		values.Set(a.profile.QueryParameter, keyword)
		if a.profile.PageParameter != "" {
			values.Set(a.profile.PageParameter, strconv.Itoa(query.Page+a.profile.PageOffset))
		}
		target.RawQuery = values.Encode()
	}
	body, err := requestHTML(ctx, client, target.String())
	if err != nil {
		return site.Page{}, err
	}
	rows, hasNext, err := parseRows(body, base, a.profile)
	if err != nil {
		return site.Page{}, err
	}
	result := site.Page{Page: query.Page, HasNext: hasNext, Items: make([]site.Result, 0, len(rows))}
	for _, row := range rows {
		identity := ""
		for _, candidate := range row.sources {
			if magnet, ok := btrss.NormalizeMagnet(candidate); ok {
				identity = btrss.EncodeIdentity("magnet", magnet)
				break
			}
			if absolute, ok := a.safeDetailURL(base, candidate); ok {
				identity = btrss.EncodeIdentity("detail", absolute)
			}
		}
		if identity == "" || row.title == "" {
			result.Skipped++
			continue
		}
		result.Items = append(result.Items, site.Result{TorrentID: identity, Title: row.title, Subtitle: row.subtitle, SizeBytes: parseSize(row.size), Published: parseTime(row.published), Seeders: parseInt(row.seeders), Leechers: parseInt(row.leechers), Completed: parseInt(row.completed), Tags: row.tags})
	}
	return result, nil
}

func (a *Adapter) Download(context.Context, site.Config, string) ([]byte, string, error) {
	return nil, "", site.ErrNotFound
}

func (a *Adapter) ResolveSource(ctx context.Context, config site.Config, identity string) (site.Source, error) {
	kind, value, ok := btrss.DecodeIdentity(identity)
	if !ok {
		return site.Source{}, site.ErrNotFound
	}
	if kind == "magnet" {
		magnet, ok := btrss.NormalizeMagnet(value)
		if !ok {
			return site.Source{}, site.ErrInvalidReply
		}
		return site.Source{Magnet: magnet}, nil
	}
	if kind != "detail" {
		return site.Source{}, site.ErrNotFound
	}
	client, base, err := a.clientFactory(config)
	if err != nil {
		return site.Source{}, site.ErrUnavailable
	}
	detail, ok := a.safeDetailURL(base, value)
	if !ok {
		return site.Source{}, site.ErrNotFound
	}
	body, err := requestHTML(ctx, client, detail)
	if err != nil {
		return site.Source{}, err
	}
	magnet := firstMagnet(body)
	if magnet == "" {
		return site.Source{}, site.ErrInvalidReply
	}
	return site.Source{Magnet: magnet}, nil
}

type parsedRow struct {
	title, subtitle, size, published, seeders, leechers, completed string
	sources, tags                                                  []string
}

func parseRows(body []byte, base *url.URL, profile Profile) ([]parsedRow, bool, error) {
	if bytes.Contains(bytes.ToLower(body), []byte("cloudflare")) || bytes.Contains(bytes.ToLower(body), []byte("captcha")) {
		return nil, false, site.ErrUnavailable
	}
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, false, site.ErrInvalidReply
	}
	rows := []parsedRow{}
	hasNext := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "tr" || hasClass(node, "torrent-row")) {
			if row := parseRowNode(node, base, profile); row.title != "" && len(row.sources) != 0 {
				rows = append(rows, row)
			}
			return
		}
		if node.Type == html.ElementNode && node.Data == "a" {
			rel := strings.ToLower(attr(node, "rel"))
			label := strings.ToLower(strings.TrimSpace(textContent(node)))
			if strings.Contains(rel, "next") || label == "next" || label == "下一页" || label == ">" {
				hasNext = true
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return rows, hasNext, nil
}

func parseRowNode(node *html.Node, base *url.URL, profile Profile) parsedRow {
	row := parsedRow{seeders: dataAttr(node, "seeders"), leechers: dataAttr(node, "leechers"), completed: dataAttr(node, "completed"), size: dataAttr(node, "size"), published: dataAttr(node, "published")}
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode {
			class := strings.ToLower(attr(current, "class"))
			text := clean(textContent(current), 1024)
			switch {
			case containsClass(class, "seed"):
				if row.seeders == "" {
					row.seeders = text
				}
			case containsClass(class, "leech") || containsClass(class, "peer"):
				if row.leechers == "" {
					row.leechers = text
				}
			case containsClass(class, "completed") || containsClass(class, "download"):
				if row.completed == "" {
					row.completed = text
				}
			case containsClass(class, "size"):
				if row.size == "" {
					row.size = text
				}
			case containsClass(class, "date") || containsClass(class, "time") || containsClass(class, "age"):
				if row.published == "" {
					row.published = text
				}
			}
			if current.Data == "a" {
				href := strings.TrimSpace(attr(current, "href"))
				if href != "" {
					row.sources = append(row.sources, href)
					if row.title == "" && isResultPath(base, href, profile.ResultPathPrefixes) {
						row.title = clean(textContent(current), 512)
					}
				}
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	if row.title == "" {
		row.title = clean(dataAttr(node, "title"), 512)
	}
	return row
}

func firstMagnet(body []byte) string {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	var result string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if result != "" {
			return
		}
		if node.Type == html.ElementNode && node.Data == "a" {
			if magnet, ok := btrss.NormalizeMagnet(attr(node, "href")); ok {
				result = magnet
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return result
}

func (a *Adapter) safeDetailURL(base *url.URL, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if !parsed.IsAbs() {
		parsed = base.ResolveReference(parsed)
	}
	sameOrigin := strings.EqualFold(parsed.Host, base.Host)
	if parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || strings.Contains(raw, "#") || parsed.ForceQuery || len(raw) > 4096 || !containsHost(a.profile.AllowedHosts, parsed.Hostname()) || (!sameOrigin && parsed.Port() != "" && parsed.Port() != "443") {
		return "", false
	}
	for _, prefix := range a.profile.ResultPathPrefixes {
		if strings.HasPrefix(path.Clean(parsed.Path), path.Clean(prefix)) {
			return parsed.String(), true
		}
	}
	return "", false
}

func requestHTML(ctx context.Context, client *http.Client, target string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, site.ErrUnavailable
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("User-Agent", "OhMyCine/1.0 (+https://github.com/yuanjing-hash/OhMyCine)")
	response, err := client.Do(request)
	if err != nil {
		return nil, site.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, site.ErrRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return nil, site.ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxHTMLBytes+1))
	if err != nil || len(body) > maxHTMLBytes {
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

func attr(node *html.Node, name string) string {
	for _, item := range node.Attr {
		if strings.EqualFold(item.Key, name) {
			return item.Val
		}
	}
	return ""
}
func dataAttr(node *html.Node, name string) string { return attr(node, "data-"+name) }
func hasClass(node *html.Node, name string) bool   { return containsClass(attr(node, "class"), name) }
func containsClass(value, name string) bool {
	for _, item := range strings.Fields(strings.ToLower(value)) {
		if item == strings.ToLower(name) || strings.Contains(item, strings.ToLower(name)) {
			return true
		}
	}
	return false
}
func textContent(node *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			b.WriteString(current.Data)
			b.WriteByte(' ')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
}
func clean(value string, maximum int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if value == "" || len([]rune(value)) > maximum || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}
func containsHost(values []string, actual string) bool {
	for _, value := range values {
		if strings.EqualFold(value, actual) {
			return true
		}
	}
	return false
}
func isResultPath(base *url.URL, raw string, prefixes []string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if !parsed.IsAbs() {
		parsed = base.ResolveReference(parsed)
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(path.Clean(parsed.Path), path.Clean(prefix)) {
			return true
		}
	}
	return false
}
func parseInt(value string) *int {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", "")
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return nil
	}
	return &parsed
}
func parseSize(value string) int64 {
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(value)))
	if len(fields) != 2 {
		return 0
	}
	number, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || number < 0 {
		return 0
	}
	multiplier := float64(1)
	switch strings.TrimSuffix(fields[1], "B") {
	case "K", "KI":
		multiplier = 1 << 10
	case "M", "MI":
		multiplier = 1 << 20
	case "G", "GI":
		multiplier = 1 << 30
	case "T", "TI":
		multiplier = 1 << 40
	default:
		return 0
	}
	return int64(number * multiplier)
}
func parseTime(value string) *time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02", "Jan 02, 2006"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}
