package douban

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/discovery"
)

const (
	defaultBaseURL   = "https://m.douban.com/rexxar/api/v2"
	maxResponseBytes = 2 << 20
)

type Provider struct {
	baseURL string
	http    *http.Client
}

func New() *Provider {
	return &Provider{baseURL: defaultBaseURL, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: sameOriginRedirect}}
}

func NewForTest(baseURL string, client *http.Client) *Provider {
	return &Provider{baseURL: strings.TrimRight(baseURL, "/"), http: client}
}

func (p *Provider) Code() string { return discovery.ProviderDouban }

func (p *Provider) Sections() []discovery.SectionDefinition {
	return []discovery.SectionDefinition{
		{Code: "hot-movie", Title: "豆瓣热门电影", MediaType: discovery.MediaTypeMovie, Category: "movie"},
		{Code: "top250", Title: "豆瓣电影 TOP250", MediaType: discovery.MediaTypeMovie, Category: "movie"},
		{Code: "hot-tv", Title: "豆瓣热门剧集", MediaType: discovery.MediaTypeTV, Category: "tv"},
		{Code: "anime-movie", Title: "豆瓣热门动漫电影", MediaType: discovery.MediaTypeMovie, Category: "anime"},
		{Code: "anime-tv", Title: "豆瓣热门动漫剧集", MediaType: discovery.MediaTypeTV, Category: "anime"},
	}
}

func (p *Provider) Fetch(ctx context.Context, input discovery.Request) (discovery.Section, error) {
	definition, ok := findSection(p.Sections(), input.Section)
	if !ok || input.Page < 1 || input.Page > 5 {
		return discovery.Section{}, discovery.ErrInvalidRequest
	}
	start := (input.Page - 1) * 20
	endpoint := ""
	query := url.Values{}
	switch input.Section {
	case "hot-movie", "anime-movie":
		endpoint = "/subject/recent_hot/movie"
		query.Set("start", strconv.Itoa(start))
		query.Set("limit", "20")
		category := "热门"
		if input.Section == "anime-movie" {
			category = "动漫"
		}
		query.Set("category", category)
		query.Set("type", "全部")
	case "hot-tv", "anime-tv":
		endpoint = "/subject/recent_hot/tv"
		query.Set("start", strconv.Itoa(start))
		query.Set("limit", "20")
		category := "热门"
		if input.Section == "anime-tv" {
			category = "动漫"
		}
		query.Set("category", category)
		query.Set("type", "全部")
	case "top250":
		endpoint = "/subject_collection/movie_top250/items"
		query.Set("start", strconv.Itoa(start))
		query.Set("count", "20")
		query.Set("items_only", "1")
		query.Set("for_mobile", "1")
	}
	payload, err := p.get(ctx, endpoint, query)
	if err != nil {
		return discovery.Section{}, err
	}
	items, total, err := parseItems(payload, definition.MediaType)
	if err != nil {
		return discovery.Section{}, err
	}
	totalPages := (total + 19) / 20
	if totalPages < input.Page {
		totalPages = input.Page
	}
	return discovery.Section{Provider: discovery.ProviderDouban, Code: definition.Code, Title: definition.Title, MediaType: definition.MediaType, Category: definition.Category, Page: input.Page, TotalPage: totalPages, Items: items, FetchedAt: time.Now().UTC()}, nil
}

func (p *Provider) get(ctx context.Context, endpoint string, query url.Values) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, discovery.ErrInvalidRequest
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Referer", "https://m.douban.com/")
	request.Header.Set("User-Agent", "OhMyCine/1.0 (+https://github.com/yuanjing-hash/OhMyCine)")
	response, err := p.http.Do(request)
	if err != nil {
		return nil, discovery.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, discovery.ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return nil, discovery.ErrInvalidReply
	}
	return body, nil
}

type responseEnvelope struct {
	Items    []rawItem `json:"items"`
	Subjects []rawItem `json:"subjects"`
	Total    int       `json:"total"`
}

type rawItem struct {
	ID       any                            `json:"id"`
	Title    string                         `json:"title"`
	CardSub  string                         `json:"card_subtitle"`
	Year     string                         `json:"year"`
	Intro    string                         `json:"intro"`
	CoverURL string                         `json:"cover_url"`
	Pic      struct{ Normal, Large string } `json:"pic"`
	Rating   struct {
		Value float64
		Count int
	} `json:"rating"`
}

func parseItems(payload []byte, mediaType string) ([]discovery.Work, int, error) {
	var envelope responseEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, 0, discovery.ErrInvalidReply
	}
	raw := envelope.Items
	if len(raw) == 0 {
		raw = envelope.Subjects
	}
	items := make([]discovery.Work, 0, len(raw))
	for _, item := range raw {
		id := cleanID(item.ID)
		title := cleanText(item.Title, 512)
		if id == "" || title == "" {
			continue
		}
		poster := item.CoverURL
		if poster == "" {
			poster = item.Pic.Large
		}
		if poster == "" {
			poster = item.Pic.Normal
		}
		poster = safeImageURL(poster)
		year := parseYear(item.Year)
		if year == nil {
			year = parseYear(item.CardSub)
		}
		var rating *float64
		var votes *int
		if item.Rating.Value >= 0 && item.Rating.Value <= 10 {
			value := item.Rating.Value
			rating = &value
		}
		if item.Rating.Count > 0 {
			value := item.Rating.Count
			votes = &value
		}
		items = append(items, discovery.Work{Provider: discovery.ProviderDouban, ProviderID: id, DoubanID: id, MediaType: mediaType, Title: title, Year: year, Overview: cleanText(item.Intro, 4096), Rating: rating, VoteCount: votes, PosterURL: poster})
	}
	if envelope.Total < len(items) {
		envelope.Total = len(items)
	}
	return items, envelope.Total, nil
}

func findSection(definitions []discovery.SectionDefinition, code string) (discovery.SectionDefinition, bool) {
	for _, item := range definitions {
		if item.Code == code {
			return item, true
		}
	}
	return discovery.SectionDefinition{}, false
}

func cleanID(value any) string {
	switch typed := value.(type) {
	case string:
		value := strings.TrimSpace(typed)
		if len(value) > 64 {
			return ""
		}
		if _, err := strconv.ParseUint(value, 10, 64); err == nil {
			return value
		}
	case float64:
		if typed > 0 && typed == float64(uint64(typed)) {
			return strconv.FormatUint(uint64(typed), 10)
		}
	}
	return ""
}

func cleanText(value string, limit int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func parseYear(value string) *int {
	for _, field := range strings.FieldsFunc(value, func(r rune) bool { return r < '0' || r > '9' }) {
		if len(field) != 4 {
			continue
		}
		year, err := strconv.Atoi(field)
		if err == nil && year >= 1888 && year <= 2200 {
			return &year
		}
	}
	return nil
}

func safeImageURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "doubanio.com" && !strings.HasSuffix(host, ".doubanio.com") {
		return ""
	}
	return parsed.String()
}

func sameOriginRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return fmt.Errorf("too many redirects")
	}
	if len(via) > 0 && !strings.EqualFold(request.URL.Hostname(), via[0].URL.Hostname()) {
		return http.ErrUseLastResponse
	}
	if request.URL.Scheme != "https" {
		return http.ErrUseLastResponse
	}
	return nil
}
