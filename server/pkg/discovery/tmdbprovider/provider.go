package tmdbprovider

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/discovery"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

type ClientFactory func() (*tmdb.Client, error)

type Provider struct{ client ClientFactory }

func New(factory ClientFactory) *Provider { return &Provider{client: factory} }
func (p *Provider) Code() string          { return discovery.ProviderTMDB }

func (p *Provider) Sections() []discovery.SectionDefinition {
	return []discovery.SectionDefinition{
		{Code: "trending-movie", Title: "本周热门电影", MediaType: discovery.MediaTypeMovie, Category: "movie"},
		{Code: "now-playing", Title: "正在上映", MediaType: discovery.MediaTypeMovie, Category: "movie"},
		{Code: "upcoming", Title: "即将上映", MediaType: discovery.MediaTypeMovie, Category: "movie"},
		{Code: "top-rated-movie", Title: "高分电影", MediaType: discovery.MediaTypeMovie, Category: "movie"},
		{Code: "trending-tv", Title: "本周热门剧集", MediaType: discovery.MediaTypeTV, Category: "tv"},
		{Code: "top-rated-tv", Title: "高分剧集", MediaType: discovery.MediaTypeTV, Category: "tv"},
		{Code: "anime-movie", Title: "热门动漫电影", MediaType: discovery.MediaTypeMovie, Category: "anime"},
		{Code: "anime-tv", Title: "热门动漫剧集", MediaType: discovery.MediaTypeTV, Category: "anime"},
	}
}

func (p *Provider) Fetch(ctx context.Context, request discovery.Request) (discovery.Section, error) {
	definition, ok := sectionDefinition(p.Sections(), request.Section)
	if !ok || request.Page < 1 || request.Page > 5 {
		return discovery.Section{}, discovery.ErrInvalidRequest
	}
	client, err := p.client()
	if err != nil {
		return discovery.Section{}, discovery.ErrUnavailable
	}
	page, err := client.Discover(ctx, request.Section, request.Page, request.Language, request.Region)
	if err != nil {
		return discovery.Section{}, fmt.Errorf("%w: %s", discovery.ErrUnavailable, tmdb.ErrorCode(err))
	}
	items := make([]discovery.Work, 0, len(page.Items))
	for _, item := range page.Items {
		poster, _ := client.ImageURL(item.PosterPath, "w500")
		backdrop, _ := client.ImageURL(item.BackdropPath, "w780")
		id := item.ID
		items = append(items, discovery.Work{Provider: discovery.ProviderTMDB, ProviderID: strconv.FormatInt(id, 10), MediaType: item.MediaType, Title: item.Title, OriginalTitle: item.OriginalTitle, Year: item.Year, Overview: item.Overview, Rating: item.Rating, VoteCount: item.VoteCount, PosterURL: poster, BackdropURL: backdrop, TMDBID: &id})
	}
	return discovery.Section{Provider: discovery.ProviderTMDB, Code: definition.Code, Title: definition.Title, MediaType: definition.MediaType, Category: definition.Category, Page: page.Page, TotalPage: page.TotalPages, Items: items, FetchedAt: time.Now().UTC()}, nil
}

func sectionDefinition(definitions []discovery.SectionDefinition, code string) (discovery.SectionDefinition, bool) {
	code = strings.TrimSpace(code)
	for _, definition := range definitions {
		if definition.Code == code {
			return definition, true
		}
	}
	return discovery.SectionDefinition{}, false
}
