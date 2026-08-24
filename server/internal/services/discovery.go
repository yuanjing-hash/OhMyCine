package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/discovery"
	"github.com/yuanjing-hash/ohmycine/server/pkg/discovery/douban"
	"github.com/yuanjing-hash/ohmycine/server/pkg/discovery/tmdbprovider"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	discoveryFreshTTL           = 24 * time.Hour
	discoveryStaleTTL           = 7 * 24 * time.Hour
	discoveryCacheRevision      = "v2"
	discoveryOverviewConcurrent = 4
	discoveryImageMaxBytes      = 8 << 20
)

type DiscoveryService struct {
	db         *gorm.DB
	providers  map[string]discovery.Provider
	log        zerolog.Logger
	locksMu    sync.Mutex
	locks      map[string]*sync.Mutex
	now        func() time.Time
	tmdbClient func() (*tmdb.Client, error)
	imageHTTP  *http.Client
}

type DiscoveryProviderSummary struct {
	Code     string                        `json:"code"`
	Sections []discovery.SectionDefinition `json:"sections"`
}

type DiscoveryOverview struct {
	Providers []DiscoveryProviderSummary `json:"providers"`
	Sections  []discovery.Section        `json:"sections"`
	UpdatedAt time.Time                  `json:"updated_at"`
}

type DiscoveryPerson struct {
	TMDBID     int64  `json:"tmdb_id,omitempty"`
	Name       string `json:"name"`
	Role       string `json:"role,omitempty"`
	Character  string `json:"character,omitempty"`
	ProfileURL string `json:"profile_url,omitempty"`
}

type DiscoveryDetail struct {
	Work             discovery.Work    `json:"work"`
	Tagline          string            `json:"tagline,omitempty"`
	Status           string            `json:"status,omitempty"`
	IMDbID           string            `json:"imdb_id,omitempty"`
	RuntimeMinutes   int               `json:"runtime_minutes,omitempty"`
	SeasonCount      int               `json:"season_count,omitempty"`
	EpisodeCount     int               `json:"episode_count,omitempty"`
	Genres           []string          `json:"genres"`
	Countries        []string          `json:"countries"`
	SpokenLanguages  []string          `json:"spoken_languages"`
	Studios          []string          `json:"studios"`
	Directors        []DiscoveryPerson `json:"directors"`
	Writers          []DiscoveryPerson `json:"writers"`
	Cast             []DiscoveryPerson `json:"cast"`
	BackdropURLs     []string          `json:"backdrop_urls"`
	Recommendations  []discovery.Work  `json:"recommendations"`
	Similar          []discovery.Work  `json:"similar"`
	ResolvedFromTMDB bool              `json:"resolved_from_tmdb"`
}

func NewDiscoveryService(db *gorm.DB, metadata *MetadataSettingsService, log zerolog.Logger) *DiscoveryService {
	providers := []discovery.Provider{
		tmdbprovider.New(metadata.Client),
		douban.New(),
	}
	service := NewDiscoveryServiceWithProviders(db, providers, log)
	service.tmdbClient = metadata.Client
	return service
}

func NewDiscoveryServiceWithProviders(db *gorm.DB, providers []discovery.Provider, log zerolog.Logger) *DiscoveryService {
	items := make(map[string]discovery.Provider, len(providers))
	for _, provider := range providers {
		if provider != nil && provider.Code() != "" {
			items[provider.Code()] = provider
		}
	}
	return &DiscoveryService{db: db, providers: items, log: log, locks: map[string]*sync.Mutex{}, now: func() time.Time { return time.Now().UTC() }, imageHTTP: discoveryImageHTTPClient()}
}

func (s *DiscoveryService) Overview(ctx context.Context, actor Actor, providerFilter string, page int, refresh bool) (DiscoveryOverview, error) {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return DiscoveryOverview{}, appError(CodePermissionDenied, "无权使用影视发现", nil)
	}
	if page == 0 {
		page = 1
	}
	if page < 1 || page > 5 {
		return DiscoveryOverview{}, appError(CodeInvalidRequest, "推荐页码无效", nil)
	}
	providerFilter = strings.TrimSpace(providerFilter)
	if providerFilter != "" {
		if _, ok := s.providers[providerFilter]; !ok {
			return DiscoveryOverview{}, appError(CodeDiscoverySectionInvalid, "推荐来源无效", nil)
		}
	}
	type sectionPlan struct {
		provider string
		section  discovery.SectionDefinition
	}
	result := DiscoveryOverview{UpdatedAt: s.now()}
	plans := make([]sectionPlan, 0)
	for _, code := range []string{discovery.ProviderTMDB, discovery.ProviderDouban} {
		provider, ok := s.providers[code]
		if !ok || (providerFilter != "" && providerFilter != code) {
			continue
		}
		definitions := provider.Sections()
		result.Providers = append(result.Providers, DiscoveryProviderSummary{Code: code, Sections: definitions})
		for _, definition := range definitions {
			plans = append(plans, sectionPlan{provider: code, section: definition})
		}
	}
	result.Sections = make([]discovery.Section, len(plans))
	semaphore := make(chan struct{}, discoveryOverviewConcurrent)
	var wait sync.WaitGroup
	for index, plan := range plans {
		index, plan := index, plan
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				result.Sections[index] = discovery.Section{Provider: plan.provider, Code: plan.section.Code, Title: plan.section.Title, MediaType: plan.section.MediaType, Category: plan.section.Category, Page: page, TotalPage: page, Items: []discovery.Work{}, FetchedAt: s.now(), ErrorCode: CodeDiscoveryProviderUnavailable}
				return
			}
			section, err := s.Section(ctx, actor, plan.provider, plan.section.Code, page, "zh-CN", "CN", refresh)
			if err != nil {
				section = discovery.Section{Provider: plan.provider, Code: plan.section.Code, Title: plan.section.Title, MediaType: plan.section.MediaType, Category: plan.section.Category, Page: page, TotalPage: page, Items: []discovery.Work{}, FetchedAt: s.now(), ErrorCode: ErrorCode(err)}
			}
			result.Sections[index] = section
		}()
	}
	wait.Wait()
	return result, nil
}

func (s *DiscoveryService) Section(ctx context.Context, actor Actor, providerCode, sectionCode string, page int, language, region string, refresh bool) (discovery.Section, error) {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return discovery.Section{}, appError(CodePermissionDenied, "无权使用影视发现", nil)
	}
	provider, ok := s.providers[strings.TrimSpace(providerCode)]
	if !ok {
		return discovery.Section{}, appError(CodeDiscoverySectionInvalid, "推荐来源无效", nil)
	}
	if !validSection(provider.Sections(), sectionCode) || page < 1 || page > 5 {
		return discovery.Section{}, appError(CodeDiscoverySectionInvalid, "推荐栏目无效", nil)
	}
	language, region = safeLocalePart(language, "zh-CN"), safeLocalePart(region, "CN")
	locale := discoveryCacheRevision + ":" + language + ":" + region
	key := provider.Code() + ":" + sectionCode + ":" + locale + ":" + fmt.Sprint(page)
	lock := s.lockFor(key)
	lock.Lock()
	defer lock.Unlock()
	now := s.now()
	cached, cacheErr := s.cached(provider.Code(), sectionCode, locale, page)
	if cacheErr == nil && !refresh && cached.FreshUntil.After(now) {
		section, err := decodeDiscoveryCache(cached, false)
		return s.projectSectionImages(section), err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	section, err := provider.Fetch(fetchCtx, discovery.Request{Section: sectionCode, Page: page, Language: language, Region: region})
	if err == nil {
		section.Stale, section.ErrorCode = false, ""
		if section.FetchedAt.IsZero() {
			section.FetchedAt = now
		}
		if err := s.store(section, locale, now); err != nil {
			return discovery.Section{}, err
		}
		serverlog.OperationDiscoveryRecommendation.Event(s.log.Info()).Str("provider", provider.Code()).Str("section", sectionCode).Int("items", len(section.Items)).Msg(serverlog.OperationDiscoveryRecommendation.Message("推荐栏目刷新完成"))
		return s.projectSectionImages(section), nil
	}
	serverlog.OperationDiscoveryRecommendation.Event(s.log.Warn()).Str("provider", provider.Code()).Str("section", sectionCode).Msg(serverlog.OperationDiscoveryRecommendation.Message("推荐来源暂时不可用"))
	if cacheErr == nil && cached.StaleUntil.After(now) {
		stale, decodeErr := decodeDiscoveryCache(cached, true)
		if decodeErr == nil {
			stale.ErrorCode = CodeDiscoveryProviderUnavailable
			return s.projectSectionImages(stale), nil
		}
	}
	return discovery.Section{}, discoveryError(err)
}

func (s *DiscoveryService) Detail(ctx context.Context, actor Actor, provider, mediaType, providerID string) (DiscoveryDetail, error) {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return DiscoveryDetail{}, appError(CodePermissionDenied, "无权查看影视详情", nil)
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != discovery.MediaTypeMovie && mediaType != discovery.MediaTypeTV {
		return DiscoveryDetail{}, appError(CodeDiscoverySectionInvalid, "作品类型无效", nil)
	}
	if s.tmdbClient == nil {
		return DiscoveryDetail{}, appError(CodeDiscoveryProviderUnavailable, "TMDB 详情服务暂时不可用", nil)
	}
	client, err := s.tmdbClient()
	if err != nil {
		return DiscoveryDetail{}, appError(CodeDiscoveryProviderUnavailable, "TMDB 详情服务暂时不可用", nil)
	}
	var match tmdb.Match
	switch provider {
	case discovery.ProviderTMDB:
		id, parseErr := strconv.ParseInt(strings.TrimSpace(providerID), 10, 64)
		if parseErr != nil || id <= 0 {
			return DiscoveryDetail{}, appError(CodeDiscoverySectionInvalid, "作品身份无效", nil)
		}
		match, err = client.GetByID(ctx, mediaType, id, "zh-CN")
	case discovery.ProviderDouban:
		work, findErr := s.findCachedWork(provider, providerID, mediaType)
		if findErr != nil {
			return DiscoveryDetail{}, findErr
		}
		match, err = client.Search(ctx, mediaType, work.Title, work.Year, "zh-CN", "CN")
		if err != nil {
			work = s.projectWorkImages(work)
			return DiscoveryDetail{Work: work, Genres: []string{}, Countries: []string{}, SpokenLanguages: []string{}, Studios: []string{}, Directors: []DiscoveryPerson{}, Writers: []DiscoveryPerson{}, Cast: []DiscoveryPerson{}, BackdropURLs: []string{}, Recommendations: []discovery.Work{}, Similar: []discovery.Work{}}, nil
		}
	default:
		return DiscoveryDetail{}, appError(CodeDiscoverySectionInvalid, "作品来源无效", nil)
	}
	if err != nil {
		return DiscoveryDetail{}, discoveryError(err)
	}
	detail := s.detailFromMatch(provider, providerID, match)
	var recommendations, similar tmdb.DiscoveryPage
	relatedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		recommendations, _ = client.Related(relatedCtx, match.Snapshot.MediaType, match.Snapshot.TMDBID, "recommendations", 1, "zh-CN")
	}()
	go func() {
		defer wait.Done()
		similar, _ = client.Related(relatedCtx, match.Snapshot.MediaType, match.Snapshot.TMDBID, "similar", 1, "zh-CN")
	}()
	wait.Wait()
	detail.Recommendations = s.relatedWorks(client, recommendations)
	detail.Similar = s.relatedWorks(client, similar)
	return detail, nil
}

func (s *DiscoveryService) Image(ctx context.Context, actor Actor, provider, token string) ([]byte, string, error) {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return nil, "", appError(CodePermissionDenied, "无权读取推荐图片", nil)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil || len(raw) == 0 || len(raw) > 4096 {
		return nil, "", appError(CodeDiscoverySectionInvalid, "推荐图片身份无效", nil)
	}
	upstream := string(raw)
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case discovery.ProviderTMDB:
		return s.downloadTMDBImage(ctx, upstream)
	case discovery.ProviderDouban:
		return s.downloadDoubanImage(ctx, upstream)
	default:
		return nil, "", appError(CodeDiscoverySectionInvalid, "推荐图片来源无效", nil)
	}
}

func (s *DiscoveryService) detailFromMatch(provider, providerID string, match tmdb.Match) DiscoveryDetail {
	snapshot := match.Snapshot
	year := match.ReleaseYear
	rating, votes := snapshot.VoteAverage, snapshot.VoteCount
	work := discovery.Work{Provider: provider, ProviderID: providerID, MediaType: snapshot.MediaType, Title: snapshot.Title, OriginalTitle: snapshot.OriginalTitle, Year: year, Overview: snapshot.Overview, Rating: &rating, VoteCount: &votes, TMDBID: &snapshot.TMDBID}
	if poster, err := s.tmdbImageURL(snapshot.PosterPath, "w500"); err == nil {
		work.PosterURL = poster
	}
	if backdrop, err := s.tmdbImageURL(snapshot.BackdropPath, "w1280"); err == nil {
		work.BackdropURL = backdrop
	}
	detail := DiscoveryDetail{Work: s.projectWorkImages(work), Tagline: snapshot.Tagline, Status: snapshot.Status, IMDbID: snapshot.IMDbID, RuntimeMinutes: snapshot.RuntimeMinutes, SeasonCount: snapshot.SeasonCount, EpisodeCount: snapshot.EpisodeCount, Genres: []string{}, Countries: append([]string(nil), snapshot.ProductionCountries...), SpokenLanguages: append([]string(nil), snapshot.SpokenLanguages...), Studios: []string{}, Directors: []DiscoveryPerson{}, Writers: []DiscoveryPerson{}, Cast: []DiscoveryPerson{}, BackdropURLs: []string{}, Recommendations: []discovery.Work{}, Similar: []discovery.Work{}, ResolvedFromTMDB: true}
	for _, genre := range snapshot.Genres {
		if genre.Name != "" {
			detail.Genres = append(detail.Genres, genre.Name)
		}
	}
	for _, studio := range snapshot.Studios {
		if studio.Name != "" {
			detail.Studios = append(detail.Studios, studio.Name)
		}
	}
	for _, person := range snapshot.Directors {
		detail.Directors = append(detail.Directors, s.discoveryPerson(person))
	}
	for _, person := range snapshot.Writers {
		detail.Writers = append(detail.Writers, s.discoveryPerson(person))
	}
	for _, person := range snapshot.Cast {
		detail.Cast = append(detail.Cast, s.discoveryPerson(person))
		if len(detail.Cast) == 24 {
			break
		}
	}
	for _, path := range snapshot.BackdropPaths {
		if image, err := s.tmdbImageURL(path, "w1280"); err == nil {
			detail.BackdropURLs = append(detail.BackdropURLs, proxyDiscoveryImage(discovery.ProviderTMDB, image))
		}
	}
	return detail
}

func (s *DiscoveryService) discoveryPerson(person tmdb.Person) DiscoveryPerson {
	result := DiscoveryPerson{TMDBID: person.TMDBID, Name: person.Name, Role: person.Job, Character: person.Character}
	if image, err := s.tmdbImageURL(person.ProfilePath, "w300"); err == nil {
		result.ProfileURL = proxyDiscoveryImage(discovery.ProviderTMDB, image)
	}
	return result
}

func (s *DiscoveryService) relatedWorks(client *tmdb.Client, page tmdb.DiscoveryPage) []discovery.Work {
	result := make([]discovery.Work, 0, len(page.Items))
	for _, item := range page.Items {
		id := item.ID
		work := discovery.Work{Provider: discovery.ProviderTMDB, ProviderID: strconv.FormatInt(id, 10), MediaType: item.MediaType, Title: item.Title, OriginalTitle: item.OriginalTitle, Year: item.Year, Overview: item.Overview, Rating: item.Rating, VoteCount: item.VoteCount, TMDBID: &id}
		work.PosterURL, _ = client.ImageURL(item.PosterPath, "w500")
		work.BackdropURL, _ = client.ImageURL(item.BackdropPath, "w780")
		result = append(result, s.projectWorkImages(work))
		if len(result) == 20 {
			break
		}
	}
	return result
}

func (s *DiscoveryService) findCachedWork(provider, providerID, mediaType string) (discovery.Work, error) {
	var records []models.DiscoveryCache
	if err := s.db.Where("provider = ?", provider).Order("updated_at DESC").Limit(30).Find(&records).Error; err != nil {
		return discovery.Work{}, err
	}
	for _, record := range records {
		section, err := decodeDiscoveryCache(record, false)
		if err != nil {
			continue
		}
		for _, work := range section.Items {
			if work.ProviderID == providerID && work.MediaType == mediaType {
				return work, nil
			}
		}
	}
	return discovery.Work{}, appError(CodeNotFound, "推荐作品已失效，请刷新推荐页", nil)
}

func (s *DiscoveryService) projectSectionImages(section discovery.Section) discovery.Section {
	for index := range section.Items {
		section.Items[index] = s.projectWorkImages(section.Items[index])
	}
	return section
}

func (s *DiscoveryService) projectWorkImages(work discovery.Work) discovery.Work {
	if work.PosterURL != "" && !strings.HasPrefix(work.PosterURL, "/api/v1/discovery/images/") {
		work.PosterURL = proxyDiscoveryImage(work.Provider, work.PosterURL)
	}
	if work.BackdropURL != "" && !strings.HasPrefix(work.BackdropURL, "/api/v1/discovery/images/") {
		work.BackdropURL = proxyDiscoveryImage(work.Provider, work.BackdropURL)
	}
	return work
}

func proxyDiscoveryImage(provider, upstream string) string {
	return "/api/v1/discovery/images/" + provider + "/" + base64.RawURLEncoding.EncodeToString([]byte(upstream))
}

func (s *DiscoveryService) tmdbImageURL(identity, size string) (string, error) {
	if s.tmdbClient == nil {
		return "", errors.New("tmdb client unavailable")
	}
	client, err := s.tmdbClient()
	if err != nil {
		return "", err
	}
	return client.ImageURL(identity, size)
}

func (s *DiscoveryService) downloadTMDBImage(ctx context.Context, upstream string) ([]byte, string, error) {
	if s.tmdbClient == nil {
		return nil, "", appError(CodeDiscoveryProviderUnavailable, "推荐图片暂时不可用", nil)
	}
	client, err := s.tmdbClient()
	if err != nil {
		return nil, "", appError(CodeDiscoveryProviderUnavailable, "推荐图片暂时不可用", nil)
	}
	for _, size := range []string{"w300", "w500", "w780", "w1280", "original"} {
		marker := "/" + size + "/"
		index := strings.Index(upstream, marker)
		if index < 0 {
			continue
		}
		identity := "/" + strings.TrimPrefix(upstream[index+len(marker):], "/")
		expected, imageErr := client.ImageURL(identity, size)
		if imageErr != nil || expected != upstream {
			continue
		}
		body, imageErr := client.DownloadJPEG(ctx, identity, size, discoveryImageMaxBytes)
		if imageErr != nil {
			return nil, "", appError(CodeDiscoveryProviderUnavailable, "推荐图片暂时不可用", nil)
		}
		return body, "image/jpeg", nil
	}
	return nil, "", appError(CodeDiscoverySectionInvalid, "推荐图片身份无效", nil)
}

func (s *DiscoveryService) downloadDoubanImage(ctx context.Context, upstream string) ([]byte, string, error) {
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !isDoubanImageHost(parsed.Hostname()) {
		return nil, "", appError(CodeDiscoverySectionInvalid, "推荐图片身份无效", nil)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", appError(CodeDiscoverySectionInvalid, "推荐图片身份无效", nil)
	}
	request.Header.Set("Accept", "image/avif,image/webp,image/jpeg,image/png")
	request.Header.Set("Referer", "https://movie.douban.com/")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/124.0 Safari/537.36")
	client := s.imageHTTP
	if client == nil {
		client = discoveryImageHTTPClient()
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, "", appError(CodeDiscoveryProviderUnavailable, "推荐图片暂时不可用", nil)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", appError(CodeDiscoveryProviderUnavailable, "推荐图片暂时不可用", nil)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" && contentType != "image/avif" {
		return nil, "", appError(CodeDiscoveryResponseInvalid, "推荐图片格式无效", nil)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, discoveryImageMaxBytes+1))
	if err != nil || len(body) == 0 || len(body) > discoveryImageMaxBytes {
		return nil, "", appError(CodeDiscoveryResponseInvalid, "推荐图片响应无效", nil)
	}
	return body, contentType, nil
}

func discoveryImageHTTPClient() *http.Client {
	return &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 || request.URL.Scheme != "https" || !isDoubanImageHost(request.URL.Hostname()) {
			return http.ErrUseLastResponse
		}
		request.Header.Set("Referer", "https://movie.douban.com/")
		request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/124.0 Safari/537.36")
		return nil
	}}
}

func isDoubanImageHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "doubanio.com" || strings.HasSuffix(host, ".doubanio.com")
}

func (s *DiscoveryService) cached(provider, section, locale string, page int) (models.DiscoveryCache, error) {
	var record models.DiscoveryCache
	err := s.db.Where("provider = ? AND section = ? AND locale = ? AND page = ?", provider, section, locale, page).First(&record).Error
	return record, err
}

func (s *DiscoveryService) store(section discovery.Section, locale string, now time.Time) error {
	payload, err := json.Marshal(section)
	if err != nil {
		return appError(CodeDiscoveryResponseInvalid, "推荐结果无法保存", nil)
	}
	record := models.DiscoveryCache{Provider: section.Provider, Section: section.Code, Locale: locale, Page: section.Page, PayloadJSON: string(payload), FreshUntil: now.Add(discoveryFreshTTL), StaleUntil: now.Add(discoveryStaleTTL), CreatedAt: now, UpdatedAt: now}
	return s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider"}, {Name: "section"}, {Name: "locale"}, {Name: "page"}}, DoUpdates: clause.Assignments(map[string]any{"payload_json": record.PayloadJSON, "fresh_until": record.FreshUntil, "stale_until": record.StaleUntil, "updated_at": now})}).Create(&record).Error
}

func decodeDiscoveryCache(record models.DiscoveryCache, stale bool) (discovery.Section, error) {
	var section discovery.Section
	if err := json.Unmarshal([]byte(record.PayloadJSON), &section); err != nil {
		return section, appError(CodeDiscoveryResponseInvalid, "推荐缓存已损坏", nil)
	}
	section.Stale = stale
	if section.Items == nil {
		section.Items = []discovery.Work{}
	}
	return section, nil
}

func discoveryError(err error) error {
	if errors.Is(err, discovery.ErrInvalidRequest) {
		return appError(CodeDiscoverySectionInvalid, "推荐栏目无效", nil)
	}
	if errors.Is(err, discovery.ErrInvalidReply) {
		return appError(CodeDiscoveryResponseInvalid, "推荐来源返回无效数据", nil)
	}
	return appError(CodeDiscoveryProviderUnavailable, "推荐来源暂时不可用", nil)
}

func validSection(definitions []discovery.SectionDefinition, code string) bool {
	for _, definition := range definitions {
		if definition.Code == code {
			return true
		}
	}
	return false
}

func safeLocalePart(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if len(value) > 16 || strings.ContainsAny(value, "\x00\r\n/:\\") {
		return fallback
	}
	return value
}

func (s *DiscoveryService) lockFor(key string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	if lock := s.locks[key]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	s.locks[key] = lock
	return lock
}
