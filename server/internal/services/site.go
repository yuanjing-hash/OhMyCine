package services

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	sitepkg "github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/builtin"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

const (
	ptResultTTL       = 15 * time.Minute
	maxPTResultClaims = 5000
)

type SiteService struct {
	db              *gorm.DB
	audit           *AuditService
	credentials     *credential.Store
	downloads       *DownloadService
	adapters        map[string]sitepkg.Adapter
	log             zerolog.Logger
	now             func() time.Time
	metadata        *MetadataSettingsService
	aiRecognition   *AIRecognitionSettingsService
	renderedFetcher sitepkg.RenderedFetcher

	limitMu sync.Mutex
	limits  map[uint]*siteLimiter
	vaultMu sync.Mutex
	vault   map[string]siteResultClaim
}

type siteLimiter struct {
	revision uint64
	limiter  *rate.Limiter
}
type siteCredentialEnvelope struct {
	Cookie  string `json:"cookie"`
	Passkey string `json:"passkey,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
}
type siteResultClaim struct {
	ActorID, SiteID   uint
	TorrentID, Title  string
	Subtitle          string
	MediaTypeHint     string
	ExpiresAt         time.Time
	InFlight          bool
	ManualTMDBID      *int64
	ManualMediaType   string
	RecognitionManual bool
	RecognitionSource string
	RecognitionStatus string
	RecognitionLocked bool
}

type SiteInput struct {
	Name, Kind, BaseURL, Cookie, Passkey, APIKey, UserAgent, BrowserServiceURL string
	Enabled                                                                    bool
	BrowserEmulation                                                           bool
	Priority, TimeoutSeconds, RateLimitPerMinute                               int
}
type SiteUpdateInput struct {
	Name, BaseURL, Cookie, Passkey, APIKey, UserAgent, BrowserServiceURL *string
	ClearPasskey                                                         bool
	ClearAPIKey                                                          bool
	Enabled                                                              *bool
	BrowserEmulation                                                     *bool
	Priority, TimeoutSeconds, RateLimitPerMinute                         *int
	Revision                                                             uint64
}
type SiteHealthSummary struct {
	Status    string     `json:"status"`
	ErrorCode string     `json:"error_code"`
	Username  string     `json:"username"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}
type SiteSummary struct {
	ID                       uint              `json:"id"`
	Name                     string            `json:"name"`
	Kind                     string            `json:"kind"`
	SiteType                 string            `json:"site_type"`
	CredentialKind           string            `json:"credential_kind"`
	Capabilities             SiteCapabilities  `json:"capabilities"`
	BaseURL                  string            `json:"base_url"`
	UserAgent                string            `json:"user_agent"`
	BrowserEmulation         bool              `json:"browser_emulation"`
	BrowserServiceConfigured bool              `json:"browser_service_configured"`
	Enabled                  bool              `json:"enabled"`
	Priority                 int               `json:"priority"`
	TimeoutSeconds           int               `json:"timeout_seconds"`
	RateLimitPerMinute       int               `json:"rate_limit_per_minute"`
	CredentialConfigured     bool              `json:"credential_configured"`
	CookieConfigured         bool              `json:"cookie_configured"`
	PasskeyConfigured        bool              `json:"passkey_configured"`
	APIKeyConfigured         bool              `json:"api_key_configured"`
	Health                   SiteHealthSummary `json:"health"`
	Revision                 uint64            `json:"revision"`
	CreatedAt                time.Time         `json:"created_at"`
	UpdatedAt                time.Time         `json:"updated_at"`
}
type SiteCapabilities struct {
	Search   bool `json:"search"`
	Download bool `json:"download"`
}
type SiteCatalogSummary struct {
	Key            string           `json:"key"`
	Name           string           `json:"name"`
	Engine         string           `json:"engine"`
	BaseURLs       []string         `json:"base_urls"`
	AutoDiscover   bool             `json:"auto_discover"`
	SiteType       string           `json:"site_type"`
	CredentialKind string           `json:"credential_kind"`
	Capabilities   SiteCapabilities `json:"capabilities"`
}
type SiteResolutionSummary struct {
	Kind             string           `json:"kind"`
	Name             string           `json:"name"`
	SiteType         string           `json:"site_type"`
	CredentialKind   string           `json:"credential_kind"`
	CanonicalBaseURL string           `json:"canonical_base_url"`
	Capabilities     SiteCapabilities `json:"capabilities"`
}
type SiteSearchInput struct {
	Keyword, MediaType, SearchBy string
	Year                         *int
	TMDBID                       *int64
	Page                         int
	SiteID                       *uint
	SiteIDs                      []uint
}
type SiteSearchOption struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	SiteType     string `json:"site_type"`
	HealthStatus string `json:"health_status"`
	Searchable   bool   `json:"searchable"`
	Reason       string `json:"reason,omitempty"`
}
type SiteSearchResult struct {
	Token               string                        `json:"token"`
	ResourceFingerprint string                        `json:"-"`
	MatchedName         string                        `json:"matched_name,omitempty"`
	Title               string                        `json:"title"`
	Subtitle            string                        `json:"subtitle,omitempty"`
	SizeBytes           int64                         `json:"size_bytes,omitempty"`
	Published           *time.Time                    `json:"published_at,omitempty"`
	Seeders             *int                          `json:"seeders,omitempty"`
	Leechers            *int                          `json:"leechers,omitempty"`
	Completed           *int                          `json:"completed,omitempty"`
	Promotion           string                        `json:"promotion,omitempty"`
	Quality             string                        `json:"quality,omitempty"`
	Tags                []string                      `json:"tags,omitempty"`
	Specifications      SiteRecognitionSpecifications `json:"specifications"`
	ExpiresAt           time.Time                     `json:"expires_at"`
}
type SiteSearchGroup struct {
	SiteID     uint               `json:"site_id"`
	SiteName   string             `json:"site_name"`
	SiteType   string             `json:"site_type"`
	Status     string             `json:"status"`
	ErrorCode  string             `json:"error_code,omitempty"`
	ErrorCount int                `json:"-"`
	Page       int                `json:"page"`
	HasNext    bool               `json:"has_next"`
	Skipped    int                `json:"skipped"`
	Items      []SiteSearchResult `json:"items"`
}
type SiteDownloadInput struct {
	ResultToken, DownloaderID string
	MediaLibraryID            *uint
	ProfileID                 uint
	Priority                  int
	FollowSubscriptionID      string
	FollowResourceFingerprint string
	Season                    *int
	Episode                   *int
	BeforeSubmit              func() error
	BeforePersist             func(*gorm.DB) error
}
type SiteManualRecognitionInput struct {
	ResultToken string
	TMDBID      int64
	MediaType   string
}
type SiteRecognitionCandidate struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"original_title,omitempty"`
	MediaType        string  `json:"media_type"`
	OriginalLanguage string  `json:"original_language,omitempty"`
	ReleaseYear      *int    `json:"release_year,omitempty"`
	Confidence       float64 `json:"confidence"`
	PosterURL        string  `json:"poster_url,omitempty"`
}
type SiteRecognitionSpecifications struct {
	Resolution   string `json:"resolution,omitempty"`
	Source       string `json:"source,omitempty"`
	VideoCodec   string `json:"video_codec,omitempty"`
	AudioCodec   string `json:"audio_codec,omitempty"`
	HDR          string `json:"hdr,omitempty"`
	ReleaseGroup string `json:"release_group,omitempty"`
}
type SiteRecognitionEpisodeFacts struct {
	Season     *int `json:"season,omitempty"`
	SeasonYear *int `json:"season_year,omitempty"`
	EpisodeMin *int `json:"episode_min,omitempty"`
	EpisodeMax *int `json:"episode_max,omitempty"`
	Count      int  `json:"count,omitempty"`
}
type SiteRecognitionSummary struct {
	EngineVersion  string                        `json:"engine_version"`
	Status         string                        `json:"status"`
	IdentitySource string                        `json:"identity_source,omitempty"`
	IdentityStatus string                        `json:"identity_status,omitempty"`
	Confidence     *float64                      `json:"confidence,omitempty"`
	ManualOverride bool                          `json:"manual_override,omitempty"`
	ErrorCode      string                        `json:"error_code,omitempty"`
	Title          string                        `json:"title"`
	OriginalTitle  string                        `json:"original_title,omitempty"`
	MediaType      string                        `json:"media_type,omitempty"`
	Year           *int                          `json:"year,omitempty"`
	TMDBID         *int64                        `json:"tmdb_id,omitempty"`
	PosterURL      string                        `json:"poster_url,omitempty"`
	Episodes       *SiteRecognitionEpisodeFacts  `json:"episodes,omitempty"`
	Specifications SiteRecognitionSpecifications `json:"specifications"`
}

func NewSiteService(db *gorm.DB, audit *AuditService, credentials *credential.Store, downloads *DownloadService, log zerolog.Logger) *SiteService {
	return NewSiteServiceWithAdapters(db, audit, credentials, downloads, builtin.Adapters(), log)
}
func NewSiteServiceWithAdapters(db *gorm.DB, audit *AuditService, credentials *credential.Store, downloads *DownloadService, adapters []sitepkg.Adapter, log zerolog.Logger) *SiteService {
	registry := map[string]sitepkg.Adapter{}
	for _, adapter := range adapters {
		if adapter != nil && adapter.Kind() != "" {
			registry[adapter.Kind()] = adapter
		}
	}
	return &SiteService{db: db, audit: audit, credentials: credentials, downloads: downloads, adapters: registry, log: log, now: func() time.Time { return time.Now().UTC() }, limits: map[uint]*siteLimiter{}, vault: map[string]siteResultClaim{}}
}

func (s *SiteService) SetMetadataSettings(service *MetadataSettingsService) { s.metadata = service }
func (s *SiteService) SetAIRecognitionSettings(service *AIRecognitionSettingsService) {
	s.aiRecognition = service
}
func (s *SiteService) SetRenderedFetcher(fetcher sitepkg.RenderedFetcher) {
	s.renderedFetcher = fetcher
}

func (s *SiteService) List(actor Actor) ([]SiteSummary, error) {
	if !actor.IsSystemAdmin() {
		return nil, appError(CodePermissionDenied, "仅管理员可以管理站点", nil)
	}
	var records []models.Site
	if err := s.db.Order("priority ASC, id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]SiteSummary, 0, len(records))
	for _, record := range records {
		items = append(items, s.siteSummary(record))
	}
	return items, nil
}

func (s *SiteService) Catalog(actor Actor) ([]SiteCatalogSummary, error) {
	if !actor.IsSystemAdmin() {
		return nil, appError(CodePermissionDenied, "仅管理员可以查看站点目录", nil)
	}
	definitions := builtin.CatalogDefinitions()
	items := make([]SiteCatalogSummary, 0, len(definitions))
	for _, definition := range definitions {
		if s.adapters[definition.Key] == nil {
			continue
		}
		items = append(items, SiteCatalogSummary{Key: definition.Key, Name: definition.Name, Engine: definition.Engine, BaseURLs: append([]string(nil), definition.BaseURLs...), AutoDiscover: definition.AutoDiscover, SiteType: definition.SiteType, CredentialKind: definition.CredentialKind, Capabilities: capabilitiesForDefinition(definition)})
	}
	return items, nil
}

func (s *SiteService) ResolveBT(actor Actor, raw string) (SiteResolutionSummary, error) {
	if !actor.IsSystemAdmin() {
		return SiteResolutionSummary{}, appError(CodePermissionDenied, "仅管理员可以识别 BT 站点", nil)
	}
	definition, canonical, err := builtin.ResolveBTBaseURL(raw)
	if err != nil {
		if errors.Is(err, builtin.ErrBTUnknown) {
			return SiteResolutionSummary{}, appError(CodeSiteBTHostUnsupported, "该 BT 官网暂未被当前 Server 原生支持", nil)
		}
		return SiteResolutionSummary{}, appError(CodeSiteURLInvalid, "BT 站点必须填写受支持的 HTTPS 官网根地址", nil)
	}
	if s.adapters[definition.Key] == nil {
		return SiteResolutionSummary{}, appError(CodeSiteKindUnsupported, "当前 Server 缺少该站点适配器", nil)
	}
	return SiteResolutionSummary{Kind: definition.Key, Name: definition.Name, SiteType: definition.SiteType, CredentialKind: definition.CredentialKind, CanonicalBaseURL: canonical, Capabilities: capabilitiesForDefinition(definition)}, nil
}

func (s *SiteService) Create(ctx context.Context, actor Actor, input SiteInput, request RequestContext) (SiteSummary, error) {
	if !actor.IsSystemAdmin() {
		return SiteSummary{}, appError(CodePermissionDenied, "仅管理员可以添加站点", nil)
	}
	name, normalized, err := normalizeSiteName(input.Name)
	if err != nil {
		return SiteSummary{}, err
	}
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	if kind == "auto_bt" {
		definition, canonical, resolveErr := builtin.ResolveBTBaseURL(input.BaseURL)
		if resolveErr != nil {
			if errors.Is(resolveErr, builtin.ErrBTUnknown) {
				return SiteSummary{}, appError(CodeSiteBTHostUnsupported, "该 BT 官网暂未被当前 Server 原生支持", nil)
			}
			return SiteSummary{}, appError(CodeSiteURLInvalid, "BT 站点必须填写受支持的 HTTPS 官网根地址", nil)
		}
		kind, input.BaseURL = definition.Key, canonical
	}
	adapter := s.adapters[kind]
	if adapter == nil {
		return SiteSummary{}, appError(CodeSiteKindUnsupported, "当前 Server 不支持该站点类型", nil)
	}
	baseURL, err := normalizeSiteBaseURL(input.BaseURL)
	if err != nil {
		return SiteSummary{}, err
	}
	if err := validateCatalogSiteBaseURL(kind, baseURL); err != nil {
		return SiteSummary{}, err
	}
	definition, _ := builtin.DefinitionForKey(kind)
	if definition.DiscoverableByURL {
		_, baseURL, _ = builtin.ResolveBTBaseURL(baseURL)
	}
	credential, err := normalizeSiteCredential(definition.CredentialKind, input.Cookie, input.Passkey, input.APIKey)
	if err != nil {
		return SiteSummary{}, err
	}
	priority, timeout, limit, userAgent, err := normalizeSitePolicy(input.Priority, input.TimeoutSeconds, input.RateLimitPerMinute, input.UserAgent)
	if err != nil {
		return SiteSummary{}, err
	}
	browserURL, err := normalizeBrowserService(input.BrowserEmulation, input.BrowserServiceURL)
	if err != nil {
		return SiteSummary{}, err
	}
	health, err := adapter.Test(ctx, sitepkg.Config{BaseURL: baseURL, Cookie: credential.Cookie, Passkey: credential.Passkey, APIKey: credential.APIKey, UserAgent: userAgent, Timeout: time.Duration(timeout) * time.Second, BrowserEmulation: input.BrowserEmulation, BrowserServiceURL: browserURL, RenderedFetcher: s.renderedFetcher})
	if err != nil {
		return SiteSummary{}, siteAdapterError(err, "站点连接测试失败，未保存配置")
	}
	ciphertext, err := s.encryptCredential(0, kind, credential)
	if err != nil {
		return SiteSummary{}, err
	}
	now := s.now()
	record := models.Site{Name: name, NameNormalized: normalized, Kind: kind, BaseURL: baseURL, CredentialCiphertext: ciphertext, UserAgent: userAgent, BrowserEmulation: input.BrowserEmulation, BrowserServiceURL: browserURL, Enabled: input.Enabled, Priority: priority, TimeoutSeconds: timeout, RateLimitPerMinute: limit, LastHealthStatus: "online", LastHealthUsername: safeLabel(health.Username, 128), LastHealthCheckedAt: &now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		// Rebind the ciphertext AAD to the stable database ID before commit.
		bound, err := s.encryptCredential(record.ID, kind, credential)
		if err != nil {
			return err
		}
		if err := tx.Model(&record).Update("credential_ciphertext", bound).Error; err != nil {
			return err
		}
		record.CredentialCiphertext = bound
		return s.audit.Record(tx, &actor.User.ID, "site.create", "site", uintID(record.ID), "success", map[string]any{"kind": kind, "enabled": record.Enabled}, request)
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return SiteSummary{}, appError(CodeSiteNameConflict, "站点名称已存在", nil)
		}
		return SiteSummary{}, err
	}
	serverlog.OperationPTSiteManagement.Event(s.log.Info()).Uint("site_id", record.ID).Str("kind", kind).Msg(serverlog.OperationPTSiteManagement.Message("站点连接已创建并通过测试"))
	return s.siteSummary(record), nil
}

// createFromCookieCloud persists a site only after a supported adapter has
// accepted the discovered cookie. It is intentionally internal: CookieCloud
// discovery is an administrator-configured background operation and must not
// bypass the normal adapter allowlist or credential encryption boundary.
func (s *SiteService) createFromCookieCloud(ctx context.Context, name, kind, baseURL, cookie string) (SiteSummary, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	adapter := s.adapters[kind]
	if adapter == nil {
		return SiteSummary{}, appError(CodeSiteKindUnsupported, "当前 Server 不支持该站点类型", nil)
	}
	baseURL, err := normalizeSiteBaseURL(baseURL)
	if err != nil {
		return SiteSummary{}, err
	}
	credential, err := normalizeSiteCredential(builtin.CredentialCookie, cookie, "", "")
	if err != nil {
		return SiteSummary{}, err
	}
	priority, timeout, limit, userAgent, err := normalizeSitePolicy(100, 12, 12, "")
	if err != nil {
		return SiteSummary{}, err
	}
	health, err := adapter.Test(ctx, sitepkg.Config{BaseURL: baseURL, Cookie: credential.Cookie, UserAgent: userAgent, Timeout: time.Duration(timeout) * time.Second, RenderedFetcher: s.renderedFetcher})
	if err != nil {
		return SiteSummary{}, siteAdapterError(err, "CookieCloud 中的站点凭据验证失败")
	}
	name, normalized, err := normalizeSiteName(name)
	if err != nil {
		return SiteSummary{}, err
	}
	ciphertext, err := s.encryptCredential(0, kind, credential)
	if err != nil {
		return SiteSummary{}, err
	}
	now := s.now()
	record := models.Site{Name: name, NameNormalized: normalized, Kind: kind, BaseURL: baseURL, CredentialCiphertext: ciphertext, UserAgent: userAgent, Enabled: true, Priority: priority, TimeoutSeconds: timeout, RateLimitPerMinute: limit, LastHealthStatus: "online", LastHealthUsername: safeLabel(health.Username, 128), LastHealthCheckedAt: &now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		bound, err := s.encryptCredential(record.ID, kind, credential)
		if err != nil {
			return err
		}
		if err := tx.Model(&record).Update("credential_ciphertext", bound).Error; err != nil {
			return err
		}
		record.CredentialCiphertext = bound
		return s.audit.Record(tx, nil, "site.cookiecloud.create", "site", uintID(record.ID), "success", map[string]any{"kind": kind}, RequestContext{})
	})
	if err != nil {
		return SiteSummary{}, err
	}
	serverlog.OperationPTSiteManagement.Event(s.log.Info()).Uint("site_id", record.ID).Str("kind", kind).Msg(serverlog.OperationPTSiteManagement.Message("CookieCloud 已发现并创建站点连接"))
	return s.siteSummary(record), nil
}

func (s *SiteService) Update(ctx context.Context, actor Actor, id uint, input SiteUpdateInput, request RequestContext) (SiteSummary, error) {
	if !actor.IsSystemAdmin() {
		return SiteSummary{}, appError(CodePermissionDenied, "仅管理员可以编辑站点", nil)
	}
	var record models.Site
	if err := s.db.First(&record, id).Error; err != nil {
		return SiteSummary{}, siteNotFound(err)
	}
	if input.Revision == 0 || input.Revision != record.Revision {
		return SiteSummary{}, appError(CodeConflict, "站点配置已变化，请刷新", nil)
	}
	// Disabling a broken or expired site must remain possible without making
	// another credential-bearing network request. Re-enabling and every
	// configuration change still pass through the candidate probe below.
	if siteUpdateDisablesOnly(input) {
		if !record.Enabled {
			return s.siteSummary(record), nil
		}
		now := s.now()
		nextRevision := record.Revision + 1
		err := s.db.Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&models.Site{}).Where("id = ? AND revision = ?", id, input.Revision).Updates(map[string]any{"enabled": false, "revision": nextRevision, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return appError(CodeConflict, "站点配置已变化，请刷新", nil)
			}
			return s.audit.Record(tx, &actor.User.ID, "site.update", "site", uintID(id), "success", map[string]any{"kind": record.Kind, "enabled": false}, request)
		})
		if err != nil {
			return SiteSummary{}, err
		}
		s.invalidateLimiter(id)
		if err := s.db.First(&record, id).Error; err != nil {
			return SiteSummary{}, err
		}
		return s.siteSummary(record), nil
	}
	credential, err := s.decryptCredential(record)
	if err != nil {
		return SiteSummary{}, appError(CodeSiteCredentialInvalid, "站点凭据不可用", nil)
	}
	if input.Name != nil {
		record.Name, record.NameNormalized, err = normalizeSiteName(*input.Name)
		if err != nil {
			return SiteSummary{}, err
		}
	}
	if input.BaseURL != nil {
		record.BaseURL, err = normalizeSiteBaseURL(*input.BaseURL)
		if err != nil {
			return SiteSummary{}, err
		}
		if err := validateCatalogSiteBaseURL(record.Kind, record.BaseURL); err != nil {
			return SiteSummary{}, err
		}
		if definition, ok := builtin.DefinitionForKey(record.Kind); ok && definition.DiscoverableByURL {
			_, record.BaseURL, _ = builtin.ResolveBTBaseURL(record.BaseURL)
		}
	}
	if input.Cookie != nil && strings.TrimSpace(*input.Cookie) != "" {
		credential.Cookie = *input.Cookie
	}
	if input.Passkey != nil {
		credential.Passkey = *input.Passkey
	}
	if input.ClearPasskey {
		credential.Passkey = ""
	}
	if input.APIKey != nil && strings.TrimSpace(*input.APIKey) != "" {
		credential.APIKey = *input.APIKey
	}
	if input.ClearAPIKey {
		credential.APIKey = ""
	}
	definition, _ := builtin.DefinitionForKey(record.Kind)
	credential, err = normalizeSiteCredential(definition.CredentialKind, credential.Cookie, credential.Passkey, credential.APIKey)
	if err != nil {
		return SiteSummary{}, err
	}
	priority, timeout, limit := record.Priority, record.TimeoutSeconds, record.RateLimitPerMinute
	if input.Priority != nil {
		priority = *input.Priority
	}
	if input.TimeoutSeconds != nil {
		timeout = *input.TimeoutSeconds
	}
	if input.RateLimitPerMinute != nil {
		limit = *input.RateLimitPerMinute
	}
	userAgent := record.UserAgent
	if input.UserAgent != nil {
		userAgent = *input.UserAgent
	}
	browserEmulation, browserURL := record.BrowserEmulation, record.BrowserServiceURL
	if input.BrowserEmulation != nil {
		browserEmulation = *input.BrowserEmulation
	}
	if input.BrowserServiceURL != nil {
		browserURL = *input.BrowserServiceURL
	}
	priority, timeout, limit, userAgent, err = normalizeSitePolicy(priority, timeout, limit, userAgent)
	if err != nil {
		return SiteSummary{}, err
	}
	browserURL, err = normalizeBrowserService(browserEmulation, browserURL)
	if err != nil {
		return SiteSummary{}, err
	}
	if input.Enabled != nil {
		record.Enabled = *input.Enabled
	}
	adapter := s.adapters[record.Kind]
	health, err := adapter.Test(ctx, sitepkg.Config{BaseURL: record.BaseURL, Cookie: credential.Cookie, Passkey: credential.Passkey, APIKey: credential.APIKey, UserAgent: userAgent, Timeout: time.Duration(timeout) * time.Second, BrowserEmulation: browserEmulation, BrowserServiceURL: browserURL, RenderedFetcher: s.renderedFetcher})
	if err != nil {
		return SiteSummary{}, siteAdapterError(err, "候选站点配置测试失败，原配置已保留")
	}
	ciphertext, err := s.encryptCredential(record.ID, record.Kind, credential)
	if err != nil {
		return SiteSummary{}, err
	}
	now := s.now()
	nextRevision := record.Revision + 1
	updates := map[string]any{"name": record.Name, "name_normalized": record.NameNormalized, "base_url": record.BaseURL, "credential_ciphertext": ciphertext, "user_agent": userAgent, "browser_emulation": browserEmulation, "browser_service_url": browserURL, "enabled": record.Enabled, "priority": priority, "timeout_seconds": timeout, "rate_limit_per_minute": limit, "last_health_status": "online", "last_health_error_code": "", "last_health_username": safeLabel(health.Username, 128), "last_health_checked_at": now, "revision": nextRevision, "updated_at": now}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.Site{}).Where("id = ? AND revision = ?", id, input.Revision).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConflict, "站点配置已变化，请刷新", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "site.update", "site", uintID(id), "success", map[string]any{"kind": record.Kind, "enabled": record.Enabled}, request)
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return SiteSummary{}, appError(CodeSiteNameConflict, "站点名称已存在", nil)
		}
		return SiteSummary{}, err
	}
	s.invalidateLimiter(id)
	if err := s.db.First(&record, id).Error; err != nil {
		return SiteSummary{}, err
	}
	return s.siteSummary(record), nil
}

func siteUpdateDisablesOnly(input SiteUpdateInput) bool {
	return input.Enabled != nil && !*input.Enabled &&
		input.Name == nil && input.BaseURL == nil && input.Cookie == nil && input.Passkey == nil && input.APIKey == nil &&
		!input.ClearPasskey && !input.ClearAPIKey && input.UserAgent == nil && input.Priority == nil &&
		input.TimeoutSeconds == nil && input.RateLimitPerMinute == nil && input.BrowserEmulation == nil && input.BrowserServiceURL == nil
}

func (s *SiteService) Test(ctx context.Context, actor Actor, id uint, request RequestContext) (SiteSummary, error) {
	if !actor.IsSystemAdmin() {
		return SiteSummary{}, appError(CodePermissionDenied, "仅管理员可以测试站点", nil)
	}
	record, config, adapter, err := s.runtimeConfig(id)
	if err != nil {
		return SiteSummary{}, err
	}
	if err := s.waitLimit(ctx, record); err != nil {
		return SiteSummary{}, err
	}
	health, testErr := adapter.Test(ctx, config)
	now := s.now()
	status, code, username := "online", "", safeLabel(health.Username, 128)
	if testErr != nil {
		status, code, username = "offline", siteErrorCode(testErr), ""
	}
	if err := s.db.Model(&models.Site{}).Where("id = ?", id).Updates(map[string]any{"last_health_status": status, "last_health_error_code": code, "last_health_username": username, "last_health_checked_at": now, "updated_at": now}).Error; err != nil {
		return SiteSummary{}, err
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "site.test", "site", uintID(id), map[bool]string{true: "failure", false: "success"}[testErr != nil], map[string]any{"kind": record.Kind, "status": status, "error_code": code}, request)
	if testErr != nil {
		return SiteSummary{}, siteAdapterError(testErr, "站点连接测试失败")
	}
	if err := s.db.First(&record, id).Error; err != nil {
		return SiteSummary{}, err
	}
	return s.siteSummary(record), nil
}

func (s *SiteService) Delete(actor Actor, id uint, request RequestContext) error {
	if !actor.IsSystemAdmin() {
		return appError(CodePermissionDenied, "仅管理员可以删除站点", nil)
	}
	var record models.Site
	if err := s.db.First(&record, id).Error; err != nil {
		return siteNotFound(err)
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.audit.Record(tx, &actor.User.ID, "site.delete", "site", uintID(id), "success", map[string]any{"kind": record.Kind}, request); err != nil {
			return err
		}
		return tx.Delete(&record).Error
	}); err != nil {
		return err
	}
	s.invalidateLimiter(id)
	s.deleteClaimsForSite(id)
	return nil
}

func (s *SiteService) Search(ctx context.Context, actor Actor, input SiteSearchInput) ([]SiteSearchGroup, error) {
	groups := []SiteSearchGroup{}
	var mu sync.Mutex
	err := s.SearchEach(ctx, actor, input, func(group SiteSearchGroup) { mu.Lock(); groups = append(groups, group); mu.Unlock() })
	return groups, err
}

const maxSiteSearchScope = 64

func normalizeSiteSearchScope(siteID *uint, siteIDs []uint) ([]uint, error) {
	if siteID != nil && len(siteIDs) > 0 {
		return nil, appError(CodeInvalidRequest, "单站筛选和多站筛选不能同时使用", nil)
	}
	if siteID != nil {
		if *siteID == 0 {
			return nil, appError(CodeInvalidRequest, "站点筛选无效", nil)
		}
		return []uint{*siteID}, nil
	}
	if len(siteIDs) == 0 {
		return nil, nil
	}
	if len(siteIDs) > maxSiteSearchScope {
		return nil, appError(CodeInvalidRequest, "一次最多选择 64 个站点", nil)
	}
	result := make([]uint, 0, len(siteIDs))
	seen := make(map[uint]struct{}, len(siteIDs))
	for _, id := range siteIDs {
		if id == 0 {
			return nil, appError(CodeInvalidRequest, "站点筛选无效", nil)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func (s *SiteService) searchSiteRecords(siteID *uint, siteIDs []uint) ([]models.Site, error) {
	scope, err := normalizeSiteSearchScope(siteID, siteIDs)
	if err != nil {
		return nil, err
	}
	var records []models.Site
	query := s.db.Where("enabled = ?", true).Order("priority ASC,id ASC")
	if len(scope) > 0 {
		query = query.Where("id IN ?", scope)
	}
	if err := query.Find(&records).Error; err != nil {
		return nil, err
	}
	selected := len(scope) > 0
	if selected && len(records) != len(scope) {
		return nil, appError(CodeInvalidRequest, "所选站点不存在、已停用或不可搜索，请重新选择", nil)
	}
	filtered := make([]models.Site, 0, len(records))
	for _, record := range records {
		definition, found := builtin.DefinitionForKey(record.Kind)
		if !found || !definition.Search {
			if selected {
				return nil, appError(CodeInvalidRequest, "所选站点不存在、已停用或不可搜索，请重新选择", nil)
			}
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered, nil
}

func (s *SiteService) SearchOptions(actor Actor) ([]SiteSearchOption, error) {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return nil, appError(CodePermissionDenied, "无权读取资源搜索站点", nil)
	}
	var records []models.Site
	if err := s.db.Where("enabled = ?", true).Order("priority ASC,id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	result := make([]SiteSearchOption, 0, len(records))
	for _, record := range records {
		definition, found := builtin.DefinitionForKey(record.Kind)
		if !found || !definition.Search {
			continue
		}
		searchable := record.LastHealthStatus != "offline"
		reason := ""
		if !searchable {
			reason = "最近一次连接测试失败"
			if record.LastHealthErrorCode != "" {
				reason += "（" + record.LastHealthErrorCode + "）"
			}
		}
		result = append(result, SiteSearchOption{ID: record.ID, Name: record.Name, SiteType: definition.SiteType, HealthStatus: firstNonEmpty(record.LastHealthStatus, "unknown"), Searchable: searchable, Reason: reason})
	}
	return result, nil
}

func (s *SiteService) SearchEach(ctx context.Context, actor Actor, input SiteSearchInput, emit func(SiteSearchGroup)) error {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return appError(CodePermissionDenied, "无权搜索种子资源", nil)
	}
	keyword := strings.TrimSpace(input.Keyword)
	input.SearchBy = strings.ToLower(strings.TrimSpace(input.SearchBy))
	if input.SearchBy == "" {
		input.SearchBy = "title"
	}
	if input.SearchBy != "title" && input.SearchBy != "tmdb_id" {
		return appError(CodeInvalidRequest, "资源搜索方式无效", nil)
	}
	if input.SearchBy == "tmdb_id" {
		if input.TMDBID == nil || *input.TMDBID <= 0 || s.metadata == nil || (input.MediaType != "movie" && input.MediaType != "tv") {
			return appError(CodeInvalidRequest, "TMDB 搜索身份无效", nil)
		}
		client, err := s.metadata.Client()
		if err != nil {
			return appError(CodeTMDBUnavailable, "TMDB 详情服务暂时不可用", nil)
		}
		match, err := client.GetByID(ctx, input.MediaType, *input.TMDBID, "zh-CN")
		if err != nil {
			return appError(CodeTMDBUnavailable, "TMDB 作品身份无法解析", nil)
		}
		keyword, input.Year = match.Title, match.ReleaseYear
	}
	if keyword == "" || len([]rune(keyword)) > 160 {
		return appError(CodeInvalidRequest, "请输入有效的资源关键词", nil)
	}
	input.Keyword = keyword
	if input.Page == 0 {
		input.Page = 1
	}
	if input.Page < 1 || input.Page > 20 {
		return appError(CodeInvalidRequest, "种子资源搜索页码无效", nil)
	}
	records, err := s.searchSiteRecords(input.SiteID, input.SiteIDs)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	semaphore := make(chan struct{}, 4)
	var wait sync.WaitGroup
	var emitMu sync.Mutex
	for _, record := range records {
		record := record
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			group := s.searchSite(ctx, actor, record, input)
			emitMu.Lock()
			emit(group)
			emitMu.Unlock()
		}()
	}
	wait.Wait()
	if ctx.Err() != nil {
		return appError(CodeSiteUnavailable, "种子资源搜索已取消", nil)
	}
	return nil
}

func (s *SiteService) searchSite(ctx context.Context, actor Actor, record models.Site, input SiteSearchInput) SiteSearchGroup {
	definition, _ := builtin.DefinitionForKey(record.Kind)
	group := SiteSearchGroup{SiteID: record.ID, SiteName: record.Name, SiteType: definition.SiteType, Status: "success", Page: input.Page, Items: []SiteSearchResult{}}
	config, err := s.config(record)
	if err != nil {
		group.Status, group.ErrorCode = "error", CodeSiteCredentialInvalid
		return group
	}
	if err := s.waitLimit(ctx, record); err != nil {
		group.Status, group.ErrorCode = "error", ErrorCode(err)
		return group
	}
	page, err := s.adapters[record.Kind].Search(ctx, config, sitepkg.Query{Keyword: input.Keyword, MediaType: input.MediaType, Year: input.Year, TMDBID: input.TMDBID, Page: input.Page})
	if err != nil {
		group.Status, group.ErrorCode = "error", siteErrorCode(err)
		return group
	}
	group.HasNext, group.Skipped = page.HasNext, page.Skipped
	expires := s.now().Add(ptResultTTL)
	for _, item := range page.Items {
		token, tokenErr := s.issueClaim(siteResultClaim{
			ActorID:       actor.User.ID,
			SiteID:        record.ID,
			TorrentID:     item.TorrentID,
			Title:         item.Title,
			Subtitle:      safeRecognitionClaimSubtitle(item.Subtitle),
			MediaTypeHint: safeRecognitionMediaTypeHint(input.MediaType),
			ExpiresAt:     expires,
		})
		if tokenErr != nil {
			continue
		}
		specifications := SiteRecognitionSpecifications{}
		if parsed, parseErr := mediarecognition.Parse(mediarecognition.InputFacts{PackageName: item.Title, SourceKind: mediarecognition.SourceDownload, MediaTypeHint: mediarecognition.MediaType(safeRecognitionMediaTypeHint(input.MediaType))}); parseErr == nil {
			specifications = siteRecognitionSpecifications(parsed.Specifications, parsed.ReleaseGroup)
		}
		group.Items = append(group.Items, SiteSearchResult{Token: token, Title: item.Title, Subtitle: item.Subtitle, SizeBytes: item.SizeBytes, Published: item.Published, Seeders: item.Seeders, Leechers: item.Leechers, Completed: item.Completed, Promotion: item.Promotion, Quality: item.Quality, Tags: item.Tags, Specifications: specifications, ExpiresAt: expires})
	}
	serverlog.OperationDiscoverySearch.Event(s.log.Info()).Uint("site_id", record.ID).Str("site_type", group.SiteType).Int("results", len(group.Items)).Int("skipped", group.Skipped).Msg(serverlog.OperationDiscoverySearch.Message("站点种子资源搜索完成"))
	return group
}

// RecognizeResult resolves only the actor-bound server-side title claim. It
// intentionally does not reserve or consume the claim, fetch a torrent, or
// submit a download task.
func (s *SiteService) RecognizeResult(ctx context.Context, actor Actor, resultToken string) (SiteRecognitionSummary, error) {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return SiteRecognitionSummary{}, appError(CodePermissionDenied, "无权识别种子资源", nil)
	}
	claim, err := s.resolveAvailableClaim(strings.TrimSpace(resultToken), actor.User.ID)
	if err != nil {
		return SiteRecognitionSummary{}, err
	}
	input := mediarecognition.InputFacts{PackageName: claim.Title, SourceKind: mediarecognition.SourceDownload, MediaTypeHint: mediarecognition.MediaType(claim.MediaTypeHint)}
	parsed, parseErr := mediarecognition.Parse(input)
	summary := SiteRecognitionSummary{EngineVersion: mediarecognition.EngineVersion, Status: mediaRecognitionStatusUnrecognized, Title: claim.Title}
	if parseErr == nil {
		summary.Title = parsed.CanonicalTitle
		summary.MediaType = string(parsed.SuggestedType)
		summary.Year = cloneInt(parsed.Year)
		summary.Episodes = siteRecognitionEpisodeFacts(parsed, summary.MediaType)
		summary.Specifications = siteRecognitionSpecifications(parsed.Specifications, parsed.ReleaseGroup)
	} else {
		summary.ErrorCode = tmdb.ErrorInvalidRequest
	}
	if s.metadata == nil {
		summary.ErrorCode = mediaRecognitionCredentialMissing
		return s.logRecognitionSummary(claim.SiteID, summary), nil
	}
	client, err := s.metadata.Client()
	if err != nil {
		summary.ErrorCode = CodeTMDBUnavailable
		return s.logRecognitionSummary(claim.SiteID, summary), nil
	}

	result := recognizeMedia(ctx, client, MediaRecognitionRequest{
		PackageName:      claim.Title,
		AuxiliaryNames:   []string{claim.Subtitle},
		SourceKind:       mediarecognition.SourceDownload,
		MediaTypeHint:    claim.MediaTypeHint,
		BuiltinPackCodes: mediarecognition.DefaultPackCodes(),
		Classification:   classification.DefaultRules(),
		Language:         "zh-CN",
		Region:           "CN",
		AIAssist:         s.aiRecognition,
	})
	summary = SiteRecognitionSummary{
		EngineVersion:  mediarecognition.EngineVersion,
		Status:         result.Status,
		IdentitySource: result.IdentitySource,
		IdentityStatus: result.IdentityStatus,
		Confidence:     cloneFloat64(result.Confidence),
		ErrorCode:      result.ErrorCode,
		Title:          strings.TrimSpace(result.Title),
		MediaType:      result.MediaType,
		Year:           cloneInt(result.ReleaseYear),
		TMDBID:         cloneInt64(result.TMDBID),
	}
	if parseErr == nil {
		summary.Specifications = siteRecognitionSpecifications(parsed.Specifications, parsed.ReleaseGroup)
		if summary.Title == "" {
			summary.Title = parsed.CanonicalTitle
		}
		if summary.Year == nil {
			summary.Year = cloneInt(parsed.Year)
		}
		if summary.MediaType == "" {
			summary.MediaType = string(parsed.SuggestedType)
		}
	}
	if summary.Title == "" {
		summary.Title = claim.Title
	}
	if parseErr == nil {
		summary.Episodes = siteRecognitionEpisodeFacts(parsed, summary.MediaType)
	}
	if result.Status == mediaRecognitionStatusMatched {
		summary.OriginalTitle = result.Snapshot.OriginalTitle
		if result.Snapshot.PosterPath != "" {
			if upstream, imageErr := client.ImageURL(result.Snapshot.PosterPath, "w500"); imageErr == nil {
				summary.PosterURL = proxyDiscoveryImage("tmdb", upstream)
			}
		}
		// A successful automatic preview has already passed the shared ranker and
		// GetByID verification. Bind that verified identity to the opaque claim so
		// the eventual download cannot regress to a weaker title-only decision.
		if result.TMDBID != nil && result.MediaType != "" {
			if bindErr := s.bindClaimRecognition(strings.TrimSpace(resultToken), actor.User.ID, *result.TMDBID, result.MediaType, result.IdentitySource, result.IdentityStatus, false); bindErr != nil {
				return SiteRecognitionSummary{}, bindErr
			}
		}
	}
	return s.logRecognitionSummary(claim.SiteID, summary), nil
}

// RecognitionCandidates is an explicit recovery tool for one actor-bound
// search result. It searches from a user-editable keyword but never accepts a
// torrent/provider identity from the browser.
func (s *SiteService) RecognitionCandidates(ctx context.Context, actor Actor, resultToken, title, mediaType string, year *int) ([]SiteRecognitionCandidate, error) {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return nil, appError(CodePermissionDenied, "无权识别种子资源", nil)
	}
	claim, err := s.resolveAvailableClaim(strings.TrimSpace(resultToken), actor.User.ID)
	if err != nil {
		return nil, err
	}
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		title = claim.Title
	}
	if title == "" || len([]rune(title)) > 256 {
		return nil, appError(CodeInvalidRequest, "TMDB 搜索关键词无效", nil)
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "" && mediaType != "movie" && mediaType != "tv" {
		return nil, appError(CodeInvalidRequest, "媒体类型无效", nil)
	}
	if year != nil && (*year < 1888 || *year > 2200) {
		return nil, appError(CodeInvalidRequest, "媒体年份无效", nil)
	}
	if s.metadata == nil {
		return nil, appError(CodeTMDBUnavailable, "TMDB 未配置", nil)
	}
	client, err := s.metadata.Client()
	if err != nil {
		return nil, err
	}
	types := []string{mediaType}
	if mediaType == "" {
		types = []string{"movie", "tv"}
	}
	items := make([]SiteRecognitionCandidate, 0, 10)
	seen := make(map[string]struct{}, 10)
	var firstFailure error
	for _, kind := range types {
		candidates, searchErr := client.SearchCandidates(ctx, kind, title, year, "zh-CN", "CN", 10)
		if searchErr != nil {
			if tmdb.ErrorCode(searchErr) != tmdb.ErrorNoMatch && firstFailure == nil {
				firstFailure = searchErr
			}
			continue
		}
		for _, candidate := range candidates {
			key := candidate.MediaType + ":" + strconv.FormatInt(candidate.ID, 10)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			item := SiteRecognitionCandidate{ID: candidate.ID, Title: candidate.Title, OriginalTitle: candidate.OriginalTitle, MediaType: candidate.MediaType, OriginalLanguage: candidate.OriginalLanguage, ReleaseYear: cloneInt(candidate.ReleaseYear), Confidence: candidate.Confidence}
			if candidate.PosterPath != "" {
				if upstream, imageErr := client.ImageURL(candidate.PosterPath, "w300"); imageErr == nil {
					item.PosterURL = proxyDiscoveryImage("tmdb", upstream)
				}
			}
			items = append(items, item)
		}
	}
	if len(items) == 0 {
		if firstFailure != nil {
			return nil, appError(tmdb.ErrorCode(firstFailure), classificationFallbackMessage(tmdb.ErrorCode(firstFailure)), nil)
		}
		return nil, appError(tmdb.ErrorNoMatch, "没有找到匹配的 TMDB 项目，请调整关键词后重试", nil)
	}
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].Confidence != items[right].Confidence {
			return items[left].Confidence > items[right].Confidence
		}
		if items[left].ReleaseYear != nil && items[right].ReleaseYear != nil && *items[left].ReleaseYear != *items[right].ReleaseYear {
			return *items[left].ReleaseYear > *items[right].ReleaseYear
		}
		if items[left].MediaType != items[right].MediaType {
			return items[left].MediaType < items[right].MediaType
		}
		return items[left].ID < items[right].ID
	})
	if len(items) > 10 {
		items = items[:10]
	}
	// Candidate lookup is intentionally read-only, but the result is useful
	// only while the same actor-bound claim can still be corrected. Recheck
	// after the upstream request so a concurrent download reservation cannot
	// leave the browser choosing an identity that can no longer be bound.
	if _, err := s.resolveAvailableClaim(strings.TrimSpace(resultToken), actor.User.ID); err != nil {
		return nil, err
	}
	return items, nil
}

// OverrideResultRecognition verifies the selected identity with TMDB before it
// is bound to the opaque claim. The browser-provided title, poster and category
// never enter the trusted download pipeline.
func (s *SiteService) OverrideResultRecognition(ctx context.Context, actor Actor, input SiteManualRecognitionInput) (SiteRecognitionSummary, error) {
	if !actor.Can(authz.PermissionDiscoveryRead) || !actor.Can(authz.PermissionDownloadsCreate) {
		return SiteRecognitionSummary{}, appError(CodePermissionDenied, "无权修正种子资源识别", nil)
	}
	token := strings.TrimSpace(input.ResultToken)
	claim, err := s.resolveAvailableClaim(token, actor.User.ID)
	if err != nil {
		return SiteRecognitionSummary{}, err
	}
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	if input.TMDBID <= 0 || (input.MediaType != "movie" && input.MediaType != "tv") {
		return SiteRecognitionSummary{}, appError(CodeInvalidRequest, "TMDB 匹配选择无效", nil)
	}
	if s.metadata == nil {
		return SiteRecognitionSummary{}, appError(CodeTMDBUnavailable, "TMDB 未配置", nil)
	}
	client, err := s.metadata.Client()
	if err != nil {
		return SiteRecognitionSummary{}, err
	}
	verified, err := client.GetByID(ctx, input.MediaType, input.TMDBID, "zh-CN")
	if err != nil {
		return SiteRecognitionSummary{}, appError(tmdb.ErrorCode(err), "TMDB 项目验证失败", nil)
	}
	if verified.ID != input.TMDBID || verified.MediaType != input.MediaType {
		return SiteRecognitionSummary{}, appError(CodeInvalidRequest, "TMDB 项目身份不一致", nil)
	}
	if err := s.bindClaimRecognition(token, actor.User.ID, verified.ID, verified.MediaType, mediaIdentitySourceManual, mediaIdentityStatusVerified, true); err != nil {
		return SiteRecognitionSummary{}, err
	}
	parsed, parseErr := mediarecognition.Parse(mediarecognition.InputFacts{PackageName: claim.Title, SourceKind: mediarecognition.SourceDownload, MediaTypeHint: mediarecognition.MediaType(claim.MediaTypeHint)})
	summary := SiteRecognitionSummary{EngineVersion: mediarecognition.EngineVersion, Status: mediaRecognitionStatusMatched, IdentitySource: mediaIdentitySourceManual, IdentityStatus: mediaIdentityStatusVerified, ManualOverride: true, Title: verified.Title, OriginalTitle: verified.Snapshot.OriginalTitle, MediaType: verified.MediaType, Year: cloneInt(verified.ReleaseYear), TMDBID: cloneInt64(&verified.ID)}
	if parseErr == nil {
		summary.Episodes = siteRecognitionEpisodeFacts(parsed, verified.MediaType)
		summary.Specifications = siteRecognitionSpecifications(parsed.Specifications, parsed.ReleaseGroup)
	}
	if verified.Snapshot.PosterPath != "" {
		if upstream, imageErr := client.ImageURL(verified.Snapshot.PosterPath, "w500"); imageErr == nil {
			summary.PosterURL = proxyDiscoveryImage("tmdb", upstream)
		}
	}
	serverlog.OperationDiscoverySearch.Event(s.log.Info()).Uint("site_id", claim.SiteID).Str("media_type", verified.MediaType).Int64("tmdb_id", verified.ID).Msg(serverlog.OperationDiscoverySearch.Message("种子资源搜索结果已人工确认媒体身份"))
	return summary, nil
}

func siteRecognitionEpisodeFacts(parsed mediarecognition.ParsedFacts, mediaType string) *SiteRecognitionEpisodeFacts {
	if mediaType != string(mediarecognition.MediaTypeTV) {
		return nil
	}
	if parsed.Season == nil && parsed.Episodes.EpisodeMin == nil && parsed.Episodes.EpisodeMax == nil && parsed.Episodes.Count == 0 {
		return nil
	}
	return &SiteRecognitionEpisodeFacts{
		Season:     cloneInt(parsed.Season),
		SeasonYear: cloneInt(parsed.SeasonYear),
		EpisodeMin: cloneInt(parsed.Episodes.EpisodeMin),
		EpisodeMax: cloneInt(parsed.Episodes.EpisodeMax),
		Count:      parsed.Episodes.Count,
	}
}

func safeRecognitionClaimSubtitle(value string) string {
	return safeRecognitionAuxiliaryName(value)
}

func safeRecognitionMediaTypeHint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "movie" || value == "tv" {
		return value
	}
	return ""
}

func (s *SiteService) logRecognitionSummary(siteID uint, summary SiteRecognitionSummary) SiteRecognitionSummary {
	event := serverlog.OperationDiscoverySearch.Event(s.log.Info()).Uint("site_id", siteID).Str("status", summary.Status)
	if summary.ErrorCode != "" {
		event = event.Str("error_code", summary.ErrorCode)
	}
	event.Msg(serverlog.OperationDiscoverySearch.Message("种子资源搜索结果识别完成"))
	return summary
}

func siteRecognitionSpecifications(specifications []string, releaseGroup string) SiteRecognitionSpecifications {
	contains := func(values ...string) bool {
		for _, actual := range specifications {
			for _, expected := range values {
				if strings.EqualFold(actual, expected) {
					return true
				}
			}
		}
		return false
	}
	first := func(candidates ...string) string {
		for index := 0; index+1 < len(candidates); index += 2 {
			if contains(candidates[index]) {
				return candidates[index+1]
			}
		}
		return ""
	}
	result := SiteRecognitionSpecifications{ReleaseGroup: strings.TrimSpace(releaseGroup)}
	result.Resolution = first("4320P", "4320p", "8K", "8K", "2160P", "2160p", "4K", "4K", "1080P", "1080p", "720P", "720p", "576P", "576p", "480P", "480p")
	sources := make([]string, 0, 2)
	source := first("BLURAY", "BluRay", "BLU-RAY", "BluRay", "WEB-DL", "WEB-DL", "WEBRIP", "WEBRip", "BDRIP", "BDRip", "HDTV", "HDTV", "DVDRIP", "DVDRip")
	if source == "BluRay" && contains("UHD") {
		source = "UHD BluRay"
	}
	if source != "" {
		sources = append(sources, source)
	} else if contains("UHD") {
		sources = append(sources, "UHD")
	}
	if contains("REMUX", "BDREMUX") {
		sources = append(sources, "REMUX")
	}
	result.Source = strings.Join(sources, " ")
	result.VideoCodec = first("X265", "H.265/HEVC", "H265", "H.265/HEVC", "H.265", "H.265/HEVC", "HEVC", "H.265/HEVC", "X264", "H.264/AVC", "H264", "H.264/AVC", "H.264", "H.264/AVC", "AV1", "AV1")
	audio := make([]string, 0, 3)
	seenAudio := map[string]struct{}{}
	for _, candidate := range []struct{ token, label string }{{"TRUEHD", "TrueHD"}, {"DTS-HD", "DTS-HD"}, {"DTS", "DTS"}, {"DDP", "Dolby Digital Plus"}, {"DD", "Dolby Digital"}, {"AAC", "AAC"}, {"ATMOS", "Atmos"}} {
		if _, exists := seenAudio[candidate.label]; contains(candidate.token) && !exists {
			audio = append(audio, candidate.label)
			seenAudio[candidate.label] = struct{}{}
		}
	}
	result.AudioCodec = strings.Join(audio, " / ")
	hdr := make([]string, 0, 2)
	if value := first("HDR10+", "HDR10+", "HDR10", "HDR10", "HDR", "HDR"); value != "" {
		hdr = append(hdr, value)
	}
	if contains("DOVI", "DOLBYVISION") {
		hdr = append(hdr, "Dolby Vision")
	}
	result.HDR = strings.Join(hdr, " / ")
	return result
}

func (s *SiteService) Download(ctx context.Context, actor Actor, input SiteDownloadInput, request RequestContext) (DownloadTaskSummary, error) {
	if !actor.Can(authz.PermissionDownloadsCreate) {
		return DownloadTaskSummary{}, appError(CodePermissionDenied, "无权创建下载任务", nil)
	}
	token := strings.TrimSpace(input.ResultToken)
	claim, err := s.reserveClaim(token, actor.User.ID)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	completed := false
	defer func() { s.finishClaim(token, completed) }()
	var selectedDownloader models.Downloader
	if err := s.db.First(&selectedDownloader, "id = ?", strings.TrimSpace(input.DownloaderID)).Error; err != nil {
		return DownloadTaskSummary{}, downloaderNotFound(err)
	}
	if !selectedDownloader.Enabled {
		return DownloadTaskSummary{}, appError(CodeDownloaderUnavailable, "下载器已停用", nil)
	}
	var claimedSite models.Site
	if err := s.db.Select("id", "kind", "enabled").First(&claimedSite, claim.SiteID).Error; err != nil {
		return DownloadTaskSummary{}, siteNotFound(err)
	}
	definition, definitionFound := builtin.DefinitionForKey(claimedSite.Kind)
	if selectedDownloader.Type == models.DownloaderTypePan115Offline && (!definitionFound || definition.SiteType != builtin.SiteTypeBT) {
		return DownloadTaskSummary{}, appError(CodeDownloadSourceInvalid, "只有已确认的公开 BT 资源可以提交到 115 离线下载", nil)
	}
	record, config, adapter, err := s.runtimeConfig(claim.SiteID)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	if !record.Enabled {
		return DownloadTaskSummary{}, appError(CodeSiteUnavailable, "站点已停用", nil)
	}
	if err := s.waitLimit(ctx, record); err != nil {
		return DownloadTaskSummary{}, err
	}
	var source DownloadSourceInput
	if resolver, ok := adapter.(sitepkg.SourceResolver); ok {
		resolved, resolveErr := resolver.ResolveSource(ctx, config, claim.TorrentID)
		if resolveErr != nil {
			return DownloadTaskSummary{}, siteAdapterError(resolveErr, "无法解析下载来源")
		}
		hasMagnet := strings.TrimSpace(resolved.Magnet) != ""
		hasTorrent := len(resolved.Torrent) > 0
		if hasMagnet == hasTorrent {
			return DownloadTaskSummary{}, siteAdapterError(sitepkg.ErrInvalidReply, "下载来源响应无效")
		}
		if hasMagnet {
			source = DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: resolved.Magnet}
		} else {
			source, err = siteTorrentDownloadSource(definition, definitionFound, selectedDownloader.Type, resolved.Torrent, resolved.Filename)
			if err != nil {
				return DownloadTaskSummary{}, err
			}
		}
	} else {
		torrent, filename, downloadErr := adapter.Download(ctx, config, claim.TorrentID)
		if downloadErr != nil {
			return DownloadTaskSummary{}, siteAdapterError(downloadErr, "无法获取种子文件")
		}
		source, err = siteTorrentDownloadSource(definition, definitionFound, selectedDownloader.Type, torrent, filename)
		if err != nil {
			return DownloadTaskSummary{}, err
		}
	}
	var recognitionOverride *DownloadRecognitionIdentity
	if claim.ManualTMDBID != nil && claim.ManualMediaType != "" {
		recognitionOverride = &DownloadRecognitionIdentity{TMDBID: *claim.ManualTMDBID, MediaType: claim.ManualMediaType, Source: claim.RecognitionSource, Status: claim.RecognitionStatus, Locked: claim.RecognitionLocked, Season: cloneInt(input.Season), Episode: cloneInt(input.Episode)}
	}
	if input.BeforeSubmit != nil {
		if err := input.BeforeSubmit(); err != nil {
			return DownloadTaskSummary{}, err
		}
	}
	result, err := s.downloads.Submit(ctx, actor, SubmitDownloadInput{DownloaderID: input.DownloaderID, MediaLibraryID: input.MediaLibraryID, ProfileID: input.ProfileID, DisplayName: claim.Title, Priority: input.Priority, Source: source, RecognitionOverride: recognitionOverride, FollowSubscriptionID: input.FollowSubscriptionID, FollowResourceFingerprint: input.FollowResourceFingerprint, ForceRecognitionOverride: input.FollowSubscriptionID != "", BeforePersist: input.BeforePersist}, request)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	completed = true
	_ = s.audit.Record(s.db, &actor.User.ID, "site.download", "site", uintID(record.ID), "success", map[string]any{"download_task_id": result.ID}, request)
	serverlog.OperationDiscoverySearch.Event(s.log.Info()).Uint("site_id", record.ID).Str("download_task_id", result.ID).Msg(serverlog.OperationDiscoverySearch.Message("种子资源搜索结果已提交下载"))
	return result, nil
}

// siteTorrentDownloadSource bridges only a torrent whose authoritative Site
// definition declares BT. Site provenance is authoritative here: torrent
// bytes alone can never prove that a result is public, while every PT result
// remains blocked before provider access.
func siteTorrentDownloadSource(definition builtin.Definition, definitionFound bool, downloaderType string, torrent []byte, filename string) (DownloadSourceInput, error) {
	if downloaderType != models.DownloaderTypePan115Offline {
		return DownloadSourceInput{Kind: downloadpkg.SourceTorrent, Torrent: torrent, Filename: filename}, nil
	}
	if !definitionFound || definition.SiteType != builtin.SiteTypeBT {
		return DownloadSourceInput{}, appError(CodeDownloadSourceInvalid, "该资源来源不能转换后提交到 115 离线下载", nil)
	}
	magnet, err := downloadpkg.TorrentMagnet(torrent)
	if err != nil {
		return DownloadSourceInput{}, appError(CodeSiteResponseInvalid, "公开 BT 站点返回的种子文件无效", err)
	}
	return DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: magnet}, nil
}

func (s *SiteService) runtimeConfig(id uint) (models.Site, sitepkg.Config, sitepkg.Adapter, error) {
	var record models.Site
	if err := s.db.First(&record, id).Error; err != nil {
		return record, sitepkg.Config{}, nil, siteNotFound(err)
	}
	adapter := s.adapters[record.Kind]
	if adapter == nil {
		return record, sitepkg.Config{}, nil, appError(CodeSiteKindUnsupported, "站点适配器不可用", nil)
	}
	config, err := s.config(record)
	return record, config, adapter, err
}
func (s *SiteService) config(record models.Site) (sitepkg.Config, error) {
	credential, err := s.decryptCredential(record)
	if err != nil {
		return sitepkg.Config{}, appError(CodeSiteCredentialInvalid, "站点凭据不可用", nil)
	}
	return sitepkg.Config{BaseURL: record.BaseURL, Cookie: credential.Cookie, Passkey: credential.Passkey, APIKey: credential.APIKey, UserAgent: record.UserAgent, Timeout: time.Duration(record.TimeoutSeconds) * time.Second, BrowserEmulation: record.BrowserEmulation, BrowserServiceURL: record.BrowserServiceURL, RenderedFetcher: s.renderedFetcher}, nil
}
func (s *SiteService) encryptCredential(id uint, kind string, value siteCredentialEnvelope) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return s.credentials.Encrypt(siteCredentialPurpose(id, kind), string(payload))
}
func (s *SiteService) decryptCredential(record models.Site) (siteCredentialEnvelope, error) {
	raw, err := s.credentials.Decrypt(siteCredentialPurpose(record.ID, record.Kind), record.CredentialCiphertext)
	if err != nil {
		return siteCredentialEnvelope{}, err
	}
	var value siteCredentialEnvelope
	err = json.Unmarshal([]byte(raw), &value)
	return value, err
}
func siteCredentialPurpose(id uint, kind string) string {
	return "site:" + kind + ":" + strconv.FormatUint(uint64(id), 10) + ":credential"
}

func (s *SiteService) waitLimit(ctx context.Context, record models.Site) error {
	s.limitMu.Lock()
	item := s.limits[record.ID]
	if item == nil || item.revision != record.Revision {
		every := time.Minute / time.Duration(max(1, record.RateLimitPerMinute))
		item = &siteLimiter{revision: record.Revision, limiter: rate.NewLimiter(rate.Every(every), 2)}
		s.limits[record.ID] = item
	}
	s.limitMu.Unlock()
	if err := item.limiter.Wait(ctx); err != nil {
		return appError(CodeSiteRateLimited, "站点请求受到限速", nil)
	}
	return nil
}
func (s *SiteService) invalidateLimiter(id uint) {
	s.limitMu.Lock()
	delete(s.limits, id)
	s.limitMu.Unlock()
}

func (s *SiteService) issueClaim(claim siteResultClaim) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	s.purgeClaimsLocked()
	if len(s.vault) >= maxPTResultClaims {
		oldestKey := ""
		var oldest time.Time
		for key, item := range s.vault {
			if oldestKey == "" || item.ExpiresAt.Before(oldest) {
				oldestKey, oldest = key, item.ExpiresAt
			}
		}
		delete(s.vault, oldestKey)
	}
	s.vault[token] = claim
	return token, nil
}
func (s *SiteService) resolveClaim(token string, actorID uint) (siteResultClaim, error) {
	if len(token) != 43 {
		return siteResultClaim{}, appError(CodeSiteResultExpired, "种子资源搜索结果已过期，请重新搜索", nil)
	}
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	s.purgeClaimsLocked()
	claim, ok := s.vault[token]
	if !ok || claim.ActorID != actorID || !claim.ExpiresAt.After(s.now()) {
		return siteResultClaim{}, appError(CodeSiteResultExpired, "种子资源搜索结果已过期，请重新搜索", nil)
	}
	return claim, nil
}
func (s *SiteService) resolveAvailableClaim(token string, actorID uint) (siteResultClaim, error) {
	if len(token) != 43 {
		return siteResultClaim{}, appError(CodeSiteResultExpired, "种子资源搜索结果已过期，请重新搜索", nil)
	}
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	s.purgeClaimsLocked()
	claim, ok := s.vault[token]
	if !ok || claim.ActorID != actorID || claim.InFlight || !claim.ExpiresAt.After(s.now()) {
		return siteResultClaim{}, appError(CodeSiteResultExpired, "种子资源搜索结果已过期，请重新搜索", nil)
	}
	return claim, nil
}
func (s *SiteService) reserveClaim(token string, actorID uint) (siteResultClaim, error) {
	if len(token) != 43 {
		return siteResultClaim{}, appError(CodeSiteResultExpired, "种子资源搜索结果已过期，请重新搜索", nil)
	}
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	s.purgeClaimsLocked()
	claim, ok := s.vault[token]
	if !ok || claim.ActorID != actorID || claim.InFlight || !claim.ExpiresAt.After(s.now()) {
		return siteResultClaim{}, appError(CodeSiteResultExpired, "种子资源搜索结果已过期，请重新搜索", nil)
	}
	claim.InFlight = true
	s.vault[token] = claim
	return claim, nil
}
func (s *SiteService) finishClaim(token string, completed bool) {
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	claim, ok := s.vault[token]
	if !ok {
		return
	}
	if completed || !claim.ExpiresAt.After(s.now()) {
		delete(s.vault, token)
		return
	}
	claim.InFlight = false
	s.vault[token] = claim
}
func (s *SiteService) bindClaimRecognition(token string, actorID uint, tmdbID int64, mediaType, source, status string, locked bool) error {
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	s.purgeClaimsLocked()
	claim, ok := s.vault[token]
	if !ok || claim.ActorID != actorID || claim.InFlight || !claim.ExpiresAt.After(s.now()) {
		return appError(CodeSiteResultExpired, "种子资源搜索结果已过期，请重新搜索", nil)
	}
	claim.ManualTMDBID = cloneInt64(&tmdbID)
	claim.ManualMediaType = mediaType
	claim.RecognitionManual = locked && source == mediaIdentitySourceManual
	claim.RecognitionSource = source
	claim.RecognitionStatus = status
	claim.RecognitionLocked = locked
	s.vault[token] = claim
	return nil
}
func (s *SiteService) purgeClaimsLocked() {
	now := s.now()
	for key, item := range s.vault {
		if !item.ExpiresAt.After(now) {
			delete(s.vault, key)
		}
	}
}
func (s *SiteService) deleteClaimsForSite(id uint) {
	s.vaultMu.Lock()
	for key, item := range s.vault {
		if item.SiteID == id {
			delete(s.vault, key)
		}
	}
	s.vaultMu.Unlock()
}

func normalizeSiteName(value string) (string, string, error) {
	name := strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if name == "" {
		return "", "", appError(CodeSiteNameRequired, "请输入站点名称", nil)
	}
	if len([]rune(name)) > 128 || validatePublicText(name) != nil {
		return "", "", appError(CodeSiteNameRequired, "站点名称无效", nil)
	}
	return name, strings.ToLower(name), nil
}
func normalizeSiteBaseURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", appError(CodeSiteURLInvalid, "站点地址必须是 HTTPS 根地址", nil)
	}
	return parsed.String(), nil
}
func normalizeSiteCredential(kind, cookie, passkey, apiKey string) (siteCredentialEnvelope, error) {
	cookie = strings.TrimSpace(cookie)
	passkey = strings.TrimSpace(passkey)
	apiKey = strings.TrimSpace(apiKey)
	switch kind {
	case builtin.CredentialNone:
		return siteCredentialEnvelope{}, nil
	case builtin.CredentialAPIKey:
		if apiKey == "" || len(apiKey) > 2048 || strings.ContainsAny(apiKey, "\x00\r\n") {
			return siteCredentialEnvelope{}, appError(CodeSiteCredentialInvalid, "Torznab API Key 无效", nil)
		}
		return siteCredentialEnvelope{APIKey: apiKey}, nil
	case builtin.CredentialCookie:
	default:
		return siteCredentialEnvelope{}, appError(CodeSiteCredentialInvalid, "站点凭据类型无效", nil)
	}
	if cookie == "" || len(cookie) > 32<<10 || strings.ContainsAny(cookie, "\x00\r\n") {
		return siteCredentialEnvelope{}, appError(CodeSiteCredentialInvalid, "PT Cookie 无效", nil)
	}
	if len(passkey) > 256 || strings.ContainsAny(passkey, "\x00\r\n &?=#") {
		return siteCredentialEnvelope{}, appError(CodeSiteCredentialInvalid, "PT passkey 无效", nil)
	}
	return siteCredentialEnvelope{Cookie: cookie, Passkey: passkey}, nil
}
func normalizeSitePolicy(priority, timeout, limit int, userAgent string) (int, int, int, string, error) {
	if priority < 1 || priority > 999 || timeout < 3 || timeout > 30 || limit < 1 || limit > 120 {
		return 0, 0, 0, "", appError(CodeInvalidRequest, "站点优先级、超时或限速无效", nil)
	}
	userAgent = strings.TrimSpace(userAgent)
	if len(userAgent) > 256 || strings.ContainsAny(userAgent, "\x00\r\n") {
		return 0, 0, 0, "", appError(CodeInvalidRequest, "站点 User-Agent 无效", nil)
	}
	return priority, timeout, limit, userAgent, nil
}
func normalizeBrowserService(enabled bool, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	// A configured loopback CloakBrowser companion is global. Public BT
	// challenge profiles may therefore enable rendering without a per-site
	// FlareSolverr fallback URL.
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", appError(CodeSiteURLInvalid, "浏览器仿真服务地址无效", nil)
	}
	return parsed.String(), nil
}
func (s *SiteService) siteSummary(record models.Site) SiteSummary {
	definition, _ := builtin.DefinitionForKey(record.Kind)
	var configured siteCredentialEnvelope
	if record.CredentialCiphertext != "" {
		if raw, err := s.credentials.Decrypt(siteCredentialPurpose(record.ID, record.Kind), record.CredentialCiphertext); err == nil {
			_ = json.Unmarshal([]byte(raw), &configured)
		}
	}
	return SiteSummary{ID: record.ID, Name: record.Name, Kind: record.Kind, SiteType: definition.SiteType, CredentialKind: definition.CredentialKind, Capabilities: capabilitiesForDefinition(definition), BaseURL: record.BaseURL, UserAgent: record.UserAgent, BrowserEmulation: record.BrowserEmulation, BrowserServiceConfigured: record.BrowserServiceURL != "", Enabled: record.Enabled, Priority: record.Priority, TimeoutSeconds: record.TimeoutSeconds, RateLimitPerMinute: record.RateLimitPerMinute, CredentialConfigured: definition.CredentialKind != builtin.CredentialNone && record.CredentialCiphertext != "", CookieConfigured: configured.Cookie != "", PasskeyConfigured: configured.Passkey != "", APIKeyConfigured: configured.APIKey != "", Health: SiteHealthSummary{Status: record.LastHealthStatus, ErrorCode: record.LastHealthErrorCode, Username: record.LastHealthUsername, CheckedAt: record.LastHealthCheckedAt}, Revision: record.Revision, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}

func capabilitiesForDefinition(definition builtin.Definition) SiteCapabilities {
	return SiteCapabilities{Search: definition.Search, Download: definition.Download}
}

func validateCatalogSiteBaseURL(kind, baseURL string) error {
	definition, ok := builtin.DefinitionForKey(kind)
	if !ok || !definition.DiscoverableByURL {
		return nil
	}
	resolved, canonical, err := builtin.ResolveBTBaseURL(baseURL)
	if err == nil && resolved.Key == kind && strings.EqualFold(canonical, strings.TrimRight(baseURL, "/")) {
		return nil
	}
	return appError(CodeSiteURLInvalid, "公开 BT 站点地址必须使用内建受控地址", nil)
}
func siteNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "站点不存在", nil)
	}
	return err
}
func siteErrorCode(err error) string {
	switch {
	case errors.Is(err, sitepkg.ErrAuthentication):
		return CodeSiteAuthentication
	case errors.Is(err, sitepkg.ErrRateLimited):
		return CodeSiteRateLimited
	case errors.Is(err, sitepkg.ErrInvalidReply):
		return CodeSiteResponseInvalid
	case errors.Is(err, sitepkg.ErrNotFound):
		return CodeNotFound
	default:
		return CodeSiteUnavailable
	}
}
func siteAdapterError(err error, message string) error {
	return appError(siteErrorCode(err), message, nil)
}
