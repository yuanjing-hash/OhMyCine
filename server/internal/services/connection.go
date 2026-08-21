package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud/pan115"
	"github.com/yuanjing-hash/ohmycine/server/pkg/mediaserver/emby"
	"gorm.io/gorm"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type ConnectionService struct {
	db          *gorm.DB
	audit       *AuditService
	credentials *credential.Store
	registry    *cloudpkg.Registry
	log         zerolog.Logger
	mu          sync.Mutex
	drivers     map[uint]cloudpkg.Driver
}

func NewConnectionService(db *gorm.DB, audit *AuditService, credentials *credential.Store, registry *cloudpkg.Registry, log zerolog.Logger) *ConnectionService {
	return &ConnectionService{db: db, audit: audit, credentials: credentials, registry: registry, log: log, drivers: map[uint]cloudpkg.Driver{}}
}

type ConnectionInput struct {
	Name            string
	Provider        string
	Cookie          string
	RecyclePassword string
	Endpoint        string
	APIKey          string
	Enabled         bool
}

type UpdateConnectionInput struct {
	Name            *string
	Cookie          *string
	RecyclePassword *string
	Endpoint        *string
	APIKey          *string
	Enabled         *bool
	Revision        uint64
}

type ConnectionHealth struct {
	Status        string     `json:"status"`
	ErrorCode     string     `json:"error_code"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
}

type ConnectionAccount struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	VIP        bool    `json:"vip"`
	UsedBytes  *uint64 `json:"used_bytes"`
	TotalBytes *uint64 `json:"total_bytes"`
}

type ConnectionSummary struct {
	ID                        uint              `json:"id"`
	Name                      string            `json:"name"`
	Provider                  string            `json:"provider"`
	Endpoint                  string            `json:"endpoint"`
	Enabled                   bool              `json:"enabled"`
	CredentialConfigured      bool              `json:"credential_configured"`
	RecyclePasswordConfigured bool              `json:"recycle_password_configured"`
	Account                   ConnectionAccount `json:"account"`
	Health                    ConnectionHealth  `json:"health"`
	Revision                  uint64            `json:"revision"`
	CreatedAt                 time.Time         `json:"created_at"`
	UpdatedAt                 time.Time         `json:"updated_at"`
}

type EmbyManagementSummary struct {
	ConnectionID uint      `json:"connection_id"`
	ServerName   string    `json:"server_name"`
	Version      string    `json:"version"`
	LibraryCount *int      `json:"library_count"`
	MovieCount   *int64    `json:"movie_count"`
	SeriesCount  *int64    `json:"series_count"`
	EpisodeCount *int64    `json:"episode_count"`
	Status       string    `json:"status"`
	ErrorCode    string    `json:"error_code"`
	CheckedAt    time.Time `json:"checked_at"`
}

func (s *ConnectionService) List(actor Actor, providers ...string) ([]ConnectionSummary, error) {
	if !actor.Can(authz.PermissionConnectionsRead) {
		return nil, appError(CodePermissionDenied, "无权查看连接", nil)
	}
	query := s.db.Order("name_normalized,id")
	if len(providers) > 0 && strings.TrimSpace(providers[0]) != "" {
		provider := strings.ToLower(strings.TrimSpace(providers[0]))
		if provider != cloudpkg.ProviderPan115 && provider != models.ConnectionProviderEmby {
			return nil, appError(CodeConnectionProviderUnsupported, "当前 Server 不支持该连接类型", nil)
		}
		query = query.Where("provider = ?", provider)
	}
	var records []models.Connection
	if err := query.Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]ConnectionSummary, 0, len(records))
	for _, record := range records {
		items = append(items, connectionSummary(record))
	}
	return items, nil
}

func (s *ConnectionService) EmbyManagementSummary(ctx context.Context, actor Actor, id uint) (EmbyManagementSummary, error) {
	if !actor.Can(authz.PermissionConnectionsRead) {
		return EmbyManagementSummary{}, appError(CodePermissionDenied, "无权查看播放器摘要", nil)
	}
	var record models.Connection
	if err := s.db.First(&record, id).Error; err != nil {
		return EmbyManagementSummary{}, connectionNotFound(err)
	}
	if record.Provider != models.ConnectionProviderEmby {
		return EmbyManagementSummary{}, appError(CodeConnectionProviderUnsupported, "该连接不是 Emby 播放器", nil)
	}
	if !record.Enabled {
		return EmbyManagementSummary{}, appError(CodeConnectionUnavailable, "Emby 连接已停用", nil)
	}
	apiKey, err := s.credentials.Decrypt(connectionPurpose(id, record.Provider), record.CredentialCiphertext)
	if err != nil {
		return EmbyManagementSummary{}, appError(CodeConnectionUnavailable, "Emby 凭据不可用", err)
	}
	client, err := emby.New(emby.Config{Endpoint: record.Endpoint, APIKey: apiKey})
	if err != nil {
		return EmbyManagementSummary{}, appError(CodeEmbyEndpointInvalid, "Emby 地址无效", err)
	}
	summary, err := client.ManagementSummary(ctx)
	if err != nil {
		return EmbyManagementSummary{}, appError(CodeEmbyUnavailable, "无法读取 Emby 摘要", err)
	}
	result := EmbyManagementSummary{
		ConnectionID: id,
		ServerName:   safeLabel(summary.Server.Name, 256),
		Version:      safeLabel(summary.Server.Version, 64),
		LibraryCount: summary.LibraryCount,
		MovieCount:   summary.MovieCount,
		SeriesCount:  summary.SeriesCount,
		EpisodeCount: summary.EpisodeCount,
		Status:       "ready",
		CheckedAt:    time.Now().UTC(),
	}
	if summary.Partial {
		result.Status, result.ErrorCode = "partial", CodeEmbySummaryPartial
	}
	return result, nil
}

func (s *ConnectionService) Create(actor Actor, input ConnectionInput, request RequestContext) (ConnectionSummary, error) {
	if !actor.Can(authz.PermissionConnectionsCreate) {
		return ConnectionSummary{}, appError(CodePermissionDenied, "无权创建连接", nil)
	}
	name, normalized, err := normalizeConnectionName(input.Name)
	if err != nil {
		return ConnectionSummary{}, err
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider != cloudpkg.ProviderPan115 && provider != models.ConnectionProviderEmby {
		return ConnectionSummary{}, appError(CodeConnectionProviderUnsupported, "当前 Server 不支持该连接类型", nil)
	}
	endpoint, credentialValue, err := s.validateCreateConfig(provider, input)
	if err != nil {
		return ConnectionSummary{}, err
	}
	now := time.Now().UTC()
	record := models.Connection{Name: name, NameNormalized: normalized, Provider: provider, Endpoint: endpoint, Enabled: input.Enabled, LastHealthStatus: "unknown", Revision: 1, CreatedAt: now, UpdatedAt: now}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		ciphertext, err := s.credentials.Encrypt(connectionPurpose(record.ID, provider), credentialValue)
		if err != nil {
			return err
		}
		if err := tx.Model(&record).Update("credential_ciphertext", ciphertext).Error; err != nil {
			return err
		}
		record.CredentialCiphertext = ciphertext
		if provider == models.ConnectionProviderPan115 && strings.TrimSpace(input.RecyclePassword) != "" {
			recyclePassword, err := normalizeRecyclePassword(input.RecyclePassword)
			if err != nil {
				return err
			}
			recycleCiphertext, err := s.credentials.Encrypt(connectionRecyclePurpose(record.ID), recyclePassword)
			if err != nil {
				return err
			}
			if err := tx.Model(&record).Update("recycle_credential_ciphertext", recycleCiphertext).Error; err != nil {
				return err
			}
			record.RecycleCredentialCiphertext = recycleCiphertext
		}
		if provider == models.ConnectionProviderEmby {
			publicID, err := newGatewayAlias()
			if err != nil {
				return err
			}
			gateway := models.EmbyProxyGateway{ConnectionID: record.ID, PublicID: publicID, Enabled: false, PolicyRevision: 1, LastHealthStatus: "unknown", CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&gateway).Error; err != nil {
				return err
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "connection.create", "connection", uintID(record.ID), "success", map[string]any{"provider": provider, "enabled": record.Enabled}, request)
	})
	if err != nil {
		if conflict := connectionConstraintError(err); conflict != nil {
			return ConnectionSummary{}, conflict
		}
		return ConnectionSummary{}, err
	}
	return connectionSummary(record), nil
}

func (s *ConnectionService) Update(actor Actor, id uint, input UpdateConnectionInput, request RequestContext) (ConnectionSummary, error) {
	if !actor.Can(authz.PermissionConnectionsUpdate) {
		return ConnectionSummary{}, appError(CodePermissionDenied, "无权编辑连接", nil)
	}
	var record models.Connection
	if err := s.db.First(&record, id).Error; err != nil {
		return ConnectionSummary{}, connectionNotFound(err)
	}
	if input.Revision == 0 || input.Revision != record.Revision {
		return ConnectionSummary{}, appError(CodeConflict, "连接配置已变化，请刷新后重试", nil)
	}
	if input.Name != nil {
		name, normalized, err := normalizeConnectionName(*input.Name)
		if err != nil {
			return ConnectionSummary{}, err
		}
		record.Name, record.NameNormalized = name, normalized
	}
	if input.Enabled != nil {
		record.Enabled = *input.Enabled
	}
	pan115HasEmbyConfig := record.Provider == cloudpkg.ProviderPan115 && ((input.Endpoint != nil && strings.TrimSpace(*input.Endpoint) != "") || (input.APIKey != nil && strings.TrimSpace(*input.APIKey) != ""))
	if pan115HasEmbyConfig {
		return ConnectionSummary{}, appError(CodeInvalidRequest, "115 连接不接受 Emby 配置", nil)
	}
	if record.Provider == models.ConnectionProviderEmby && input.Cookie != nil && strings.TrimSpace(*input.Cookie) != "" {
		return ConnectionSummary{}, appError(CodeInvalidRequest, "Emby 连接不接受 Cookie", nil)
	}
	if record.Provider == models.ConnectionProviderEmby && input.RecyclePassword != nil {
		return ConnectionSummary{}, appError(CodeInvalidRequest, "Emby 连接不接受 115 回收站安全码", nil)
	}
	if input.Endpoint != nil {
		if record.Provider != models.ConnectionProviderEmby {
			return ConnectionSummary{}, appError(CodeInvalidRequest, "连接地址不适用于该类型", nil)
		}
		parsed, err := emby.ParseEndpoint(*input.Endpoint)
		if err != nil {
			return ConnectionSummary{}, appError(CodeEmbyEndpointInvalid, "Emby 地址无效", nil)
		}
		record.Endpoint = parsed.String()
	}
	if input.Cookie != nil && strings.TrimSpace(*input.Cookie) != "" {
		if _, err := s.registry.Build(record.Provider, cloudpkg.Config{ConnectionID: id, Cookie: *input.Cookie}); err != nil {
			return ConnectionSummary{}, connectionProviderError(err)
		}
		normalizedCookie, err := normalizeCredential(*input.Cookie, record.Provider)
		if err != nil {
			return ConnectionSummary{}, connectionProviderError(err)
		}
		ciphertext, err := s.credentials.Encrypt(connectionPurpose(id, record.Provider), normalizedCookie)
		if err != nil {
			return ConnectionSummary{}, err
		}
		record.CredentialCiphertext = ciphertext
	}
	if input.APIKey != nil && strings.TrimSpace(*input.APIKey) != "" {
		if record.Provider != models.ConnectionProviderEmby {
			return ConnectionSummary{}, appError(CodeInvalidRequest, "API Key 不适用于该连接类型", nil)
		}
		apiKey, err := emby.NormalizeAPIKey(*input.APIKey)
		if err != nil {
			return ConnectionSummary{}, appError(CodeEmbyAPIKeyInvalid, "Emby API Key 无效", nil)
		}
		ciphertext, err := s.credentials.Encrypt(connectionPurpose(id, record.Provider), apiKey)
		if err != nil {
			return ConnectionSummary{}, err
		}
		record.CredentialCiphertext = ciphertext
	}
	if input.RecyclePassword != nil {
		if record.Provider != models.ConnectionProviderPan115 {
			return ConnectionSummary{}, appError(CodeInvalidRequest, "回收站安全码不适用于该连接", nil)
		}
		value := strings.TrimSpace(*input.RecyclePassword)
		if value == "" {
			record.RecycleCredentialCiphertext = ""
		} else {
			normalized, normalizeErr := normalizeRecyclePassword(value)
			if normalizeErr != nil {
				return ConnectionSummary{}, normalizeErr
			}
			ciphertext, err := s.credentials.Encrypt(connectionRecyclePurpose(id), normalized)
			if err != nil {
				return ConnectionSummary{}, err
			}
			record.RecycleCredentialCiphertext = ciphertext
		}
	}
	record.LastHealthStatus, record.LastHealthErrorCode, record.LastHealthCheckedAt = "unknown", "", nil
	record.AccountID, record.AccountName, record.AccountVIP = "", "", false
	record.QuotaUsedBytes, record.QuotaTotalBytes = nil, nil
	record.Revision++
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.Connection{}).Where("id = ? AND revision = ?", id, input.Revision).Updates(map[string]any{
			"name": record.Name, "name_normalized": record.NameNormalized, "endpoint": record.Endpoint, "credential_ciphertext": record.CredentialCiphertext, "recycle_credential_ciphertext": record.RecycleCredentialCiphertext,
			"enabled": record.Enabled, "account_id": "", "account_name": "", "account_vip": false,
			"quota_used_bytes": nil, "quota_total_bytes": nil, "last_health_status": "unknown",
			"last_health_error_code": "", "last_health_checked_at": nil, "revision": record.Revision, "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConflict, "连接配置已变化，请刷新后重试", nil)
		}
		if record.Provider == models.ConnectionProviderEmby {
			if err := tx.Model(&models.EmbyProxyGateway{}).Where("connection_id = ?", id).Updates(map[string]any{"enabled": false, "policy_revision": gorm.Expr("policy_revision + 1"), "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "connection.update", "connection", uintID(id), "success", map[string]any{"provider": record.Provider, "enabled": record.Enabled}, request)
	}); err != nil {
		if conflict := connectionConstraintError(err); conflict != nil {
			return ConnectionSummary{}, conflict
		}
		return ConnectionSummary{}, err
	}
	s.invalidate(id)
	return connectionSummary(record), nil
}

func (s *ConnectionService) Test(ctx context.Context, actor Actor, id uint, request RequestContext) (ConnectionSummary, error) {
	if !actor.Can(authz.PermissionConnectionsTest) {
		return ConnectionSummary{}, appError(CodePermissionDenied, "无权测试连接", nil)
	}
	var record models.Connection
	err := s.db.First(&record, id).Error
	now := time.Now().UTC()
	status, errorCode, outcome := "offline", cloudpkg.CodeUnavailable, "failure"
	var account cloudpkg.Account
	started := time.Now()
	serverlog.OperationConnectionProbe.Event(s.log.Info()).Uint("connection_id", id).Msg(serverlog.OperationConnectionProbe.Message("开始检测"))
	if err == nil && !record.Enabled {
		err = appError(CodeConnectionUnavailable, "连接已停用", nil)
	}
	if err == nil && record.Provider == models.ConnectionProviderEmby {
		apiKey, decryptErr := s.credentials.Decrypt(connectionPurpose(id, record.Provider), record.CredentialCiphertext)
		if decryptErr != nil {
			err, errorCode = decryptErr, CodeConnectionUnavailable
		} else if client, clientErr := emby.New(emby.Config{Endpoint: record.Endpoint, APIKey: apiKey}); clientErr != nil {
			err, errorCode = clientErr, CodeEmbyEndpointInvalid
		} else if info, probeErr := client.Probe(ctx); probeErr != nil {
			err, errorCode = probeErr, CodeEmbyUnavailable
		} else {
			account = cloudpkg.Account{ID: info.ID, Name: info.Name}
			status, errorCode, outcome = "online", "", "success"
		}
	} else if err == nil {
		var driver cloudpkg.Driver
		_, driver, err = s.driver(id)
		if err == nil {
			account, err = driver.Probe(ctx)
			if err == nil {
				status, errorCode, outcome = "online", "", "success"
			} else {
				errorCode, _ = cloudpkg.ErrorInfo(err)
			}
		}
	}
	if record.ID != 0 {
		testedRevision := record.Revision
		record.LastHealthStatus, record.LastHealthErrorCode, record.LastHealthCheckedAt = status, safeLabel(errorCode, 96), &now
		if err == nil {
			record.AccountID, record.AccountName, record.AccountVIP = safeLabel(account.ID, 128), safeLabel(account.Name, 256), account.VIP
			record.QuotaUsedBytes, record.QuotaTotalBytes = account.UsedBytes, account.TotalBytes
		}
		if saveErr := s.db.Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&models.Connection{}).Where("id = ? AND revision = ?", record.ID, testedRevision).Updates(map[string]any{
				"account_id": record.AccountID, "account_name": record.AccountName, "account_vip": record.AccountVIP,
				"quota_used_bytes": record.QuotaUsedBytes, "quota_total_bytes": record.QuotaTotalBytes,
				"last_health_status": record.LastHealthStatus, "last_health_error_code": record.LastHealthErrorCode,
				"last_health_checked_at": record.LastHealthCheckedAt, "updated_at": now,
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return appError(CodeConflict, "连接配置已变化，请重新测试", nil)
			}
			return s.audit.Record(tx, &actor.User.ID, "connection.test", "connection", uintID(id), outcome, map[string]any{"provider": record.Provider, "error_code": errorCode}, request)
		}); saveErr != nil {
			return ConnectionSummary{}, saveErr
		}
	}
	event := s.log.Info()
	if err != nil {
		event = s.log.Warn()
	}
	serverlog.OperationConnectionProbe.Event(event).Uint("connection_id", id).Str("provider", record.Provider).Str("status", status).Str("error_code", errorCode).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationConnectionProbe.Message("检测完成"))
	if err != nil {
		return ConnectionSummary{}, appError(CodeConnectionUnavailable, connectionTestMessage(record.Provider, errorCode), err)
	}
	return connectionSummary(record), nil
}

func (s *ConnectionService) Delete(actor Actor, id uint, request RequestContext) error {
	if !actor.Can(authz.PermissionConnectionsDelete) {
		return appError(CodePermissionDenied, "无权删除连接", nil)
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var record models.Connection
		if err := tx.First(&record, id).Error; err != nil {
			return connectionNotFound(err)
		}
		var references int64
		if err := tx.Model(&models.Storage{}).Where("connection_id = ?", id).Count(&references).Error; err != nil {
			return err
		}
		if references > 0 {
			return appError(CodeConnectionInUse, "连接仍被 Storage 使用", nil)
		}
		if err := tx.Delete(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "connection.delete", "connection", uintID(id), "success", map[string]any{"provider": record.Provider}, request)
	})
	if err == nil {
		s.invalidate(id)
	}
	return err
}

func (s *ConnectionService) Driver(actor Actor, id uint) (models.Connection, cloudpkg.Driver, error) {
	if !actor.Can(authz.PermissionConnectionsRead) {
		return models.Connection{}, nil, appError(CodePermissionDenied, "无权使用连接", nil)
	}
	return s.driver(id)
}

func (s *ConnectionService) driver(id uint) (models.Connection, cloudpkg.Driver, error) {
	var record models.Connection
	if err := s.db.First(&record, id).Error; err != nil {
		return record, nil, connectionNotFound(err)
	}
	if !record.Enabled {
		return record, nil, appError(CodeConnectionUnavailable, "连接已停用", nil)
	}
	if record.Provider != cloudpkg.ProviderPan115 {
		return record, nil, appError(CodeConnectionProviderUnsupported, "该连接不是存储驱动", nil)
	}
	s.mu.Lock()
	cached := s.drivers[id]
	s.mu.Unlock()
	if cached != nil {
		return record, cached, nil
	}
	cookie, err := s.credentials.Decrypt(connectionPurpose(id, record.Provider), record.CredentialCiphertext)
	if err != nil {
		return record, nil, err
	}
	recyclePassword := ""
	if record.RecycleCredentialCiphertext != "" {
		recyclePassword, err = s.credentials.Decrypt(connectionRecyclePurpose(id), record.RecycleCredentialCiphertext)
		if err != nil {
			return record, nil, err
		}
	}
	driver, err := s.registry.Build(record.Provider, cloudpkg.Config{ConnectionID: id, Cookie: cookie, RecyclePassword: recyclePassword})
	if err != nil {
		return record, nil, connectionProviderError(err)
	}
	s.mu.Lock()
	if existing := s.drivers[id]; existing != nil {
		driver = existing
	} else {
		s.drivers[id] = driver
	}
	s.mu.Unlock()
	return record, driver, nil
}

func (s *ConnectionService) invalidate(id uint) {
	s.mu.Lock()
	delete(s.drivers, id)
	s.mu.Unlock()
}

func normalizeCredential(value, provider string) (string, error) {
	// The provider builder already parsed and allowlisted the credential. Keep
	// normalization provider-owned without exposing a generic raw config map.
	if provider == cloudpkg.ProviderPan115 {
		cookie, err := pan115.ParseCookie(value)
		if err != nil {
			return "", err
		}
		return cookie.String(), nil
	}
	return strings.TrimSpace(value), nil
}

func normalizeConnectionName(input string) (string, string, error) {
	name := strings.Join(strings.Fields(input), " ")
	if name == "" {
		return "", "", appError(CodeConnectionNameRequired, "请填写连接名称", nil)
	}
	if len([]rune(name)) > 128 {
		return "", "", appError(CodeInvalidRequest, "连接名称过长", nil)
	}
	return name, strings.ToLower(name), nil
}

func connectionPurpose(id uint, providers ...string) string {
	provider := models.ConnectionProviderPan115
	if len(providers) > 0 {
		provider = providers[0]
	}
	if provider == models.ConnectionProviderEmby {
		return "connection:" + uintID(id) + ":emby:api-key"
	}
	return "connection:" + uintID(id) + ":pan115:cookie"
}

func connectionRecyclePurpose(id uint) string {
	return "connection:" + uintID(id) + ":pan115:recycle-password"
}

func normalizeRecyclePassword(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || strings.ContainsAny(value, "\x00\r\n") {
		return "", appError(CodeInvalidRequest, "115 回收站安全码无效", nil)
	}
	return value, nil
}

func connectionSummary(record models.Connection) ConnectionSummary {
	return ConnectionSummary{ID: record.ID, Name: record.Name, Provider: record.Provider, Endpoint: record.Endpoint, Enabled: record.Enabled, CredentialConfigured: record.CredentialCiphertext != "", RecyclePasswordConfigured: record.RecycleCredentialCiphertext != "", Account: ConnectionAccount{ID: record.AccountID, Name: record.AccountName, VIP: record.AccountVIP, UsedBytes: record.QuotaUsedBytes, TotalBytes: record.QuotaTotalBytes}, Health: ConnectionHealth{Status: record.LastHealthStatus, ErrorCode: record.LastHealthErrorCode, LastCheckedAt: record.LastHealthCheckedAt}, Revision: record.Revision, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}

func connectionTestMessage(provider, code string) string {
	if provider == models.ConnectionProviderEmby {
		if code == CodeEmbyEndpointInvalid {
			return "Emby 地址无效"
		}
		return "无法连接 Emby，请检查地址、API Key 与网络"
	}
	switch code {
	case cloudpkg.CodeAuthExpired, cloudpkg.CodeCookieInvalid:
		return "115 Cookie 无效或已过期，请重新填写"
	case cloudpkg.CodeRateLimited:
		return "115 请求受到限速，请稍后重试"
	case cloudpkg.CodeResponseInvalid:
		return "115 返回了暂时无法识别的数据"
	default:
		return "无法连接 115，请检查网络或稍后重试"
	}
}

func (s *ConnectionService) validateCreateConfig(provider string, input ConnectionInput) (string, string, error) {
	if provider == models.ConnectionProviderEmby {
		parsed, err := emby.ParseEndpoint(input.Endpoint)
		if err != nil {
			return "", "", appError(CodeEmbyEndpointInvalid, "Emby 地址无效", nil)
		}
		apiKey, err := emby.NormalizeAPIKey(input.APIKey)
		if err != nil {
			return "", "", appError(CodeEmbyAPIKeyInvalid, "请填写 Emby API Key", nil)
		}
		return parsed.String(), apiKey, nil
	}
	if strings.TrimSpace(input.Cookie) == "" {
		return "", "", appError(CodePan115CookieInvalid, "请填写 115 Cookie", nil)
	}
	if _, err := s.registry.Build(provider, cloudpkg.Config{Cookie: input.Cookie}); err != nil {
		return "", "", connectionProviderError(err)
	}
	normalized, err := normalizeCredential(input.Cookie, provider)
	if err != nil {
		return "", "", connectionProviderError(err)
	}
	return "", normalized, nil
}

func newGatewayAlias() (string, error) {
	value := make([]byte, 5)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return "emby-" + hex.EncodeToString(value), nil
}

func connectionProviderError(err error) error {
	code, _ := cloudpkg.ErrorInfo(err)
	if code == cloudpkg.CodeCookieInvalid {
		return appError(CodePan115CookieInvalid, "115 Cookie 格式无效", nil)
	}
	return appError(CodeConnectionUnavailable, "连接配置不可用", err)
}

func connectionNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "连接不存在", err)
	}
	return err
}

func connectionConstraintError(err error) error {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return nil
	}
	if strings.Contains(sqliteErr.Error(), "connections.name_normalized") {
		return appError(CodeConnectionNameConflict, "连接名称已存在", err)
	}
	return appError(CodeConflict, "连接配置冲突", err)
}
