package services

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	sitepkg "github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/pttime"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

const (
	ptResultTTL       = 15 * time.Minute
	maxPTResultClaims = 5000
)

type SiteService struct {
	db          *gorm.DB
	audit       *AuditService
	credentials *credential.Store
	downloads   *DownloadService
	adapters    map[string]sitepkg.Adapter
	log         zerolog.Logger
	now         func() time.Time
	metadata    *MetadataSettingsService

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
}
type siteResultClaim struct {
	ActorID, SiteID  uint
	TorrentID, Title string
	ExpiresAt        time.Time
	InFlight         bool
}

type SiteInput struct {
	Name, Kind, BaseURL, Cookie, Passkey, UserAgent, BrowserServiceURL string
	Enabled                                                            bool
	BrowserEmulation                                                   bool
	Priority, TimeoutSeconds, RateLimitPerMinute                       int
}
type SiteUpdateInput struct {
	Name, BaseURL, Cookie, Passkey, UserAgent, BrowserServiceURL *string
	ClearPasskey                                                 bool
	Enabled                                                      *bool
	BrowserEmulation                                             *bool
	Priority, TimeoutSeconds, RateLimitPerMinute                 *int
	Revision                                                     uint64
}
type SiteHealthSummary struct {
	Status    string     `json:"status"`
	ErrorCode string     `json:"error_code"`
	Username  string     `json:"username"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
}
type SiteSummary struct {
	ID                   uint              `json:"id"`
	Name                 string            `json:"name"`
	Kind                 string            `json:"kind"`
	BaseURL              string            `json:"base_url"`
	UserAgent            string            `json:"user_agent"`
	BrowserEmulation     bool              `json:"browser_emulation"`
	BrowserServiceURL    string            `json:"browser_service_url"`
	Enabled              bool              `json:"enabled"`
	Priority             int               `json:"priority"`
	TimeoutSeconds       int               `json:"timeout_seconds"`
	RateLimitPerMinute   int               `json:"rate_limit_per_minute"`
	CredentialConfigured bool              `json:"credential_configured"`
	Health               SiteHealthSummary `json:"health"`
	Revision             uint64            `json:"revision"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}
type SiteSearchInput struct {
	Keyword, MediaType, SearchBy string
	Year                         *int
	TMDBID                       *int64
	Page                         int
	SiteID                       *uint
}
type SiteSearchResult struct {
	Token     string     `json:"token"`
	Title     string     `json:"title"`
	Subtitle  string     `json:"subtitle,omitempty"`
	SizeBytes int64      `json:"size_bytes,omitempty"`
	Published *time.Time `json:"published_at,omitempty"`
	Seeders   *int       `json:"seeders,omitempty"`
	Leechers  *int       `json:"leechers,omitempty"`
	Completed *int       `json:"completed,omitempty"`
	Promotion string     `json:"promotion,omitempty"`
	Quality   string     `json:"quality,omitempty"`
	Tags      []string   `json:"tags,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
}
type SiteSearchGroup struct {
	SiteID    uint               `json:"site_id"`
	SiteName  string             `json:"site_name"`
	Status    string             `json:"status"`
	ErrorCode string             `json:"error_code,omitempty"`
	Page      int                `json:"page"`
	HasNext   bool               `json:"has_next"`
	Skipped   int                `json:"skipped"`
	Items     []SiteSearchResult `json:"items"`
}
type SiteDownloadInput struct {
	ResultToken, DownloaderID string
	MediaLibraryID            *uint
	ProfileID                 uint
	Priority                  int
}

func NewSiteService(db *gorm.DB, audit *AuditService, credentials *credential.Store, downloads *DownloadService, log zerolog.Logger) *SiteService {
	return NewSiteServiceWithAdapters(db, audit, credentials, downloads, []sitepkg.Adapter{pttime.New()}, log)
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

func (s *SiteService) List(actor Actor) ([]SiteSummary, error) {
	if !actor.IsSystemAdmin() {
		return nil, appError(CodePermissionDenied, "仅管理员可以管理 PT 站点", nil)
	}
	var records []models.Site
	if err := s.db.Order("priority ASC, id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]SiteSummary, 0, len(records))
	for _, record := range records {
		items = append(items, siteSummary(record))
	}
	return items, nil
}

func (s *SiteService) Create(ctx context.Context, actor Actor, input SiteInput, request RequestContext) (SiteSummary, error) {
	if !actor.IsSystemAdmin() {
		return SiteSummary{}, appError(CodePermissionDenied, "仅管理员可以添加 PT 站点", nil)
	}
	name, normalized, err := normalizeSiteName(input.Name)
	if err != nil {
		return SiteSummary{}, err
	}
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	adapter := s.adapters[kind]
	if adapter == nil {
		return SiteSummary{}, appError(CodeSiteKindUnsupported, "当前 Server 不支持该 PT 站点类型", nil)
	}
	baseURL, err := normalizeSiteBaseURL(input.BaseURL)
	if err != nil {
		return SiteSummary{}, err
	}
	credential, err := normalizeSiteCredential(input.Cookie, input.Passkey)
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
	health, err := adapter.Test(ctx, sitepkg.Config{BaseURL: baseURL, Cookie: credential.Cookie, Passkey: credential.Passkey, UserAgent: userAgent, Timeout: time.Duration(timeout) * time.Second, BrowserEmulation: input.BrowserEmulation, BrowserServiceURL: browserURL})
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
	return siteSummary(record), nil
}

// createFromCookieCloud persists a site only after a supported adapter has
// accepted the discovered cookie. It is intentionally internal: CookieCloud
// discovery is an administrator-configured background operation and must not
// bypass the normal adapter allowlist or credential encryption boundary.
func (s *SiteService) createFromCookieCloud(ctx context.Context, kind, baseURL, cookie string) (SiteSummary, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	adapter := s.adapters[kind]
	if adapter == nil {
		return SiteSummary{}, appError(CodeSiteKindUnsupported, "当前 Server 不支持该站点类型", nil)
	}
	baseURL, err := normalizeSiteBaseURL(baseURL)
	if err != nil {
		return SiteSummary{}, err
	}
	credential, err := normalizeSiteCredential(cookie, "")
	if err != nil {
		return SiteSummary{}, err
	}
	priority, timeout, limit, userAgent, err := normalizeSitePolicy(100, 12, 12, "")
	if err != nil {
		return SiteSummary{}, err
	}
	health, err := adapter.Test(ctx, sitepkg.Config{BaseURL: baseURL, Cookie: credential.Cookie, UserAgent: userAgent, Timeout: time.Duration(timeout) * time.Second})
	if err != nil {
		return SiteSummary{}, siteAdapterError(err, "CookieCloud 中的站点凭据验证失败")
	}
	name, normalized, err := normalizeSiteName("PTTime")
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
	return siteSummary(record), nil
}

func (s *SiteService) Update(ctx context.Context, actor Actor, id uint, input SiteUpdateInput, request RequestContext) (SiteSummary, error) {
	if !actor.IsSystemAdmin() {
		return SiteSummary{}, appError(CodePermissionDenied, "仅管理员可以编辑 PT 站点", nil)
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
			return siteSummary(record), nil
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
		return siteSummary(record), nil
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
	credential, err = normalizeSiteCredential(credential.Cookie, credential.Passkey)
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
	health, err := adapter.Test(ctx, sitepkg.Config{BaseURL: record.BaseURL, Cookie: credential.Cookie, Passkey: credential.Passkey, UserAgent: userAgent, Timeout: time.Duration(timeout) * time.Second, BrowserEmulation: browserEmulation, BrowserServiceURL: browserURL})
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
	return siteSummary(record), nil
}

func siteUpdateDisablesOnly(input SiteUpdateInput) bool {
	return input.Enabled != nil && !*input.Enabled &&
		input.Name == nil && input.BaseURL == nil && input.Cookie == nil && input.Passkey == nil &&
		!input.ClearPasskey && input.UserAgent == nil && input.Priority == nil &&
		input.TimeoutSeconds == nil && input.RateLimitPerMinute == nil && input.BrowserEmulation == nil && input.BrowserServiceURL == nil
}

func (s *SiteService) Test(ctx context.Context, actor Actor, id uint, request RequestContext) (SiteSummary, error) {
	if !actor.IsSystemAdmin() {
		return SiteSummary{}, appError(CodePermissionDenied, "仅管理员可以测试 PT 站点", nil)
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
	return siteSummary(record), nil
}

func (s *SiteService) Delete(actor Actor, id uint, request RequestContext) error {
	if !actor.IsSystemAdmin() {
		return appError(CodePermissionDenied, "仅管理员可以删除 PT 站点", nil)
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

func (s *SiteService) SearchEach(ctx context.Context, actor Actor, input SiteSearchInput, emit func(SiteSearchGroup)) error {
	if !actor.Can(authz.PermissionDiscoveryRead) {
		return appError(CodePermissionDenied, "无权搜索 PT 资源", nil)
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
		return appError(CodeInvalidRequest, "PT 搜索页码无效", nil)
	}
	var records []models.Site
	query := s.db.Where("enabled = ?", true).Order("priority ASC,id ASC")
	if input.SiteID != nil {
		query = query.Where("id = ?", *input.SiteID)
	}
	if err := query.Find(&records).Error; err != nil {
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
		return appError(CodeSiteUnavailable, "PT 搜索已取消", nil)
	}
	return nil
}

func (s *SiteService) searchSite(ctx context.Context, actor Actor, record models.Site, input SiteSearchInput) SiteSearchGroup {
	group := SiteSearchGroup{SiteID: record.ID, SiteName: record.Name, Status: "success", Page: input.Page, Items: []SiteSearchResult{}}
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
		token, tokenErr := s.issueClaim(siteResultClaim{ActorID: actor.User.ID, SiteID: record.ID, TorrentID: item.TorrentID, Title: item.Title, ExpiresAt: expires})
		if tokenErr != nil {
			continue
		}
		group.Items = append(group.Items, SiteSearchResult{Token: token, Title: item.Title, Subtitle: item.Subtitle, SizeBytes: item.SizeBytes, Published: item.Published, Seeders: item.Seeders, Leechers: item.Leechers, Completed: item.Completed, Promotion: item.Promotion, Quality: item.Quality, Tags: item.Tags, ExpiresAt: expires})
	}
	serverlog.OperationDiscoverySearch.Event(s.log.Info()).Uint("site_id", record.ID).Int("results", len(group.Items)).Int("skipped", group.Skipped).Msg(serverlog.OperationDiscoverySearch.Message("PT 站点搜索完成"))
	return group
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
	record, config, adapter, err := s.runtimeConfig(claim.SiteID)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	if !record.Enabled {
		return DownloadTaskSummary{}, appError(CodeSiteUnavailable, "PT 站点已停用", nil)
	}
	if err := s.waitLimit(ctx, record); err != nil {
		return DownloadTaskSummary{}, err
	}
	torrent, filename, err := adapter.Download(ctx, config, claim.TorrentID)
	if err != nil {
		return DownloadTaskSummary{}, siteAdapterError(err, "无法获取种子文件")
	}
	result, err := s.downloads.Submit(ctx, actor, SubmitDownloadInput{DownloaderID: input.DownloaderID, MediaLibraryID: input.MediaLibraryID, ProfileID: input.ProfileID, DisplayName: claim.Title, Priority: input.Priority, Source: DownloadSourceInput{Kind: downloadpkg.SourceTorrent, Torrent: torrent, Filename: filename}}, request)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	completed = true
	_ = s.audit.Record(s.db, &actor.User.ID, "site.download", "site", uintID(record.ID), "success", map[string]any{"download_task_id": result.ID}, request)
	serverlog.OperationDiscoverySearch.Event(s.log.Info()).Uint("site_id", record.ID).Str("download_task_id", result.ID).Msg(serverlog.OperationDiscoverySearch.Message("PT 搜索结果已提交下载"))
	return result, nil
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
	return sitepkg.Config{BaseURL: record.BaseURL, Cookie: credential.Cookie, Passkey: credential.Passkey, UserAgent: record.UserAgent, Timeout: time.Duration(record.TimeoutSeconds) * time.Second, BrowserEmulation: record.BrowserEmulation, BrowserServiceURL: record.BrowserServiceURL}, nil
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
		return appError(CodeSiteRateLimited, "PT 站点请求受到限速", nil)
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
		return siteResultClaim{}, appError(CodeSiteResultExpired, "PT 搜索结果已过期，请重新搜索", nil)
	}
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	s.purgeClaimsLocked()
	claim, ok := s.vault[token]
	if !ok || claim.ActorID != actorID || !claim.ExpiresAt.After(s.now()) {
		return siteResultClaim{}, appError(CodeSiteResultExpired, "PT 搜索结果已过期，请重新搜索", nil)
	}
	return claim, nil
}
func (s *SiteService) reserveClaim(token string, actorID uint) (siteResultClaim, error) {
	if len(token) != 43 {
		return siteResultClaim{}, appError(CodeSiteResultExpired, "PT 搜索结果已过期，请重新搜索", nil)
	}
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	s.purgeClaimsLocked()
	claim, ok := s.vault[token]
	if !ok || claim.ActorID != actorID || claim.InFlight || !claim.ExpiresAt.After(s.now()) {
		return siteResultClaim{}, appError(CodeSiteResultExpired, "PT 搜索结果已过期，请重新搜索", nil)
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
		return "", appError(CodeSiteURLInvalid, "PT 站点地址必须是 HTTPS 根地址", nil)
	}
	return parsed.String(), nil
}
func normalizeSiteCredential(cookie, passkey string) (siteCredentialEnvelope, error) {
	cookie = strings.TrimSpace(cookie)
	passkey = strings.TrimSpace(passkey)
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
	if !enabled && raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", appError(CodeSiteURLInvalid, "浏览器仿真服务地址无效", nil)
	}
	return parsed.String(), nil
}
func siteSummary(record models.Site) SiteSummary {
	return SiteSummary{ID: record.ID, Name: record.Name, Kind: record.Kind, BaseURL: record.BaseURL, UserAgent: record.UserAgent, BrowserEmulation: record.BrowserEmulation, BrowserServiceURL: record.BrowserServiceURL, Enabled: record.Enabled, Priority: record.Priority, TimeoutSeconds: record.TimeoutSeconds, RateLimitPerMinute: record.RateLimitPerMinute, CredentialConfigured: record.CredentialCiphertext != "", Health: SiteHealthSummary{Status: record.LastHealthStatus, ErrorCode: record.LastHealthErrorCode, Username: record.LastHealthUsername, CheckedAt: record.LastHealthCheckedAt}, Revision: record.Revision, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}
func siteNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "PT 站点不存在", nil)
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
