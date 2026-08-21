package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type DownloaderService struct {
	db          *gorm.DB
	audit       *AuditService
	credentials *credential.Store
	registry    *downloadpkg.Registry
	connections *ConnectionService
}

func (s *DownloaderService) SetConnectionService(connections *ConnectionService) {
	s.connections = connections
}

func NewDownloaderService(db *gorm.DB, audit *AuditService, credentials *credential.Store, registry *downloadpkg.Registry) *DownloaderService {
	return &DownloaderService{db: db, audit: audit, credentials: credentials, registry: registry}
}

type DownloaderInput struct {
	Name                   string
	Type                   string
	BaseURL                string
	Username               string
	Password               string
	Enabled                bool
	StorageID              *uint
	ProviderDirectoryToken string
}

type UpdateDownloaderInput struct {
	Name                   *string
	BaseURL                *string
	Username               *string
	Password               *string
	ClearUsername          bool
	ClearPassword          bool
	Enabled                *bool
	StorageID              *uint
	ProviderDirectoryToken *string
}

type DownloaderHealth struct {
	Status      string     `json:"status"`
	Version     string     `json:"version"`
	ErrorCode   string     `json:"error_code"`
	LastChecked *time.Time `json:"last_checked_at"`
}

type DownloaderSummary struct {
	ID                    string                   `json:"id"`
	Name                  string                   `json:"name"`
	Type                  string                   `json:"type"`
	BaseURL               string                   `json:"base_url"`
	Enabled               bool                     `json:"enabled"`
	UsernameConfigured    bool                     `json:"username_configured"`
	PasswordConfigured    bool                     `json:"password_configured"`
	Capabilities          downloadpkg.Capabilities `json:"capabilities"`
	Health                DownloaderHealth         `json:"health"`
	CreatedAt             time.Time                `json:"created_at"`
	UpdatedAt             time.Time                `json:"updated_at"`
	StorageID             *uint                    `json:"storage_id"`
	StorageName           string                   `json:"storage_name"`
	ProviderDirectoryPath string                   `json:"provider_directory_path"`
}

func (s *DownloaderService) List(actor Actor) ([]DownloaderSummary, error) {
	if !actor.Can(authz.PermissionDownloadersRead) {
		return nil, appError(CodePermissionDenied, "无权查看下载器", nil)
	}
	var records []models.Downloader
	if err := s.db.Order("name_normalized, id").Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]DownloaderSummary, 0, len(records))
	for _, record := range records {
		items = append(items, s.summary(record))
	}
	return items, nil
}

func (s *DownloaderService) Create(actor Actor, input DownloaderInput, request RequestContext) (DownloaderSummary, error) {
	return s.CreateContext(context.Background(), actor, input, request)
}

func (s *DownloaderService) CreateContext(ctx context.Context, actor Actor, input DownloaderInput, request RequestContext) (DownloaderSummary, error) {
	if !actor.Can(authz.PermissionDownloadersCreate) {
		return DownloaderSummary{}, appError(CodePermissionDenied, "无权创建下载器", nil)
	}
	name, normalized, err := normalizeDownloaderName(input.Name)
	if err != nil {
		return DownloaderSummary{}, err
	}
	providerType := strings.ToLower(strings.TrimSpace(input.Type))
	capabilities, ok := s.registry.Capabilities(providerType)
	if !ok {
		return DownloaderSummary{}, appError(CodeDownloaderTypeUnsupported, "当前 Server 不支持该下载器类型", nil)
	}
	baseURL, storage, err := s.validateDownloaderConfig(providerType, input.BaseURL, input.Username, input.Password, input.StorageID)
	if err != nil {
		return DownloaderSummary{}, err
	}
	providerDirectoryID, providerDirectoryPath := "", ""
	if providerType == models.DownloaderTypePan115Offline {
		selection, resolveErr := s.resolveProviderDirectory(ctx, actor, storage, input.ProviderDirectoryToken)
		if resolveErr != nil {
			return DownloaderSummary{}, resolveErr
		}
		providerDirectoryID, providerDirectoryPath = selection.ProviderID, selection.RelativeRoot
	}
	id := uuid.NewString()
	username, err := s.credentials.Encrypt(downloaderPurpose(id, "username"), input.Username)
	if err != nil {
		return DownloaderSummary{}, err
	}
	password, err := s.credentials.Encrypt(downloaderPurpose(id, "password"), input.Password)
	if err != nil {
		return DownloaderSummary{}, err
	}
	capabilitiesJSON, _ := json.Marshal(capabilities)
	now := time.Now().UTC()
	record := models.Downloader{ID: id, Name: name, NameNormalized: normalized, Type: providerType, BaseURL: baseURL, UsernameCiphertext: username, PasswordCiphertext: password, StorageID: storage, ProviderDirectoryID: providerDirectoryID, ProviderDirectoryPath: providerDirectoryPath, Enabled: input.Enabled, CapabilitiesJSON: string(capabilitiesJSON), LastHealthStatus: "unknown", CreatedAt: now, UpdatedAt: now}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "downloader.create", "downloader", record.ID, "success", map[string]any{"type": record.Type, "enabled": record.Enabled}, request)
	})
	if err != nil {
		if conflict := downloaderConstraintError(err); conflict != nil {
			return DownloaderSummary{}, conflict
		}
		return DownloaderSummary{}, err
	}
	return s.summary(record), nil
}

func (s *DownloaderService) Update(actor Actor, id string, input UpdateDownloaderInput, request RequestContext) (DownloaderSummary, error) {
	return s.UpdateContext(context.Background(), actor, id, input, request)
}

func (s *DownloaderService) UpdateContext(ctx context.Context, actor Actor, id string, input UpdateDownloaderInput, request RequestContext) (DownloaderSummary, error) {
	if !actor.Can(authz.PermissionDownloadersUpdate) {
		return DownloaderSummary{}, appError(CodePermissionDenied, "无权编辑下载器", nil)
	}
	if input.ClearUsername && input.Username != nil && *input.Username != "" || input.ClearPassword && input.Password != nil && *input.Password != "" {
		return DownloaderSummary{}, appError(CodeInvalidRequest, "不能同时保存并清除下载器凭据", nil)
	}
	var record models.Downloader
	if err := s.db.First(&record, "id = ?", id).Error; err != nil {
		return DownloaderSummary{}, downloaderNotFound(err)
	}
	if input.Name != nil {
		name, normalized, err := normalizeDownloaderName(*input.Name)
		if err != nil {
			return DownloaderSummary{}, err
		}
		record.Name, record.NameNormalized = name, normalized
	}
	if input.BaseURL != nil {
		record.BaseURL = strings.TrimSpace(*input.BaseURL)
	}
	if input.Enabled != nil {
		record.Enabled = *input.Enabled
	}
	if record.Type == models.DownloaderTypePan115Offline && (input.StorageID != nil || input.ProviderDirectoryToken != nil) {
		if input.StorageID == nil || input.ProviderDirectoryToken == nil {
			return DownloaderSummary{}, appError(CodeDownloaderStorageRequired, "请选择完整的 115 离线下载目录", nil)
		}
		_, storage, validateErr := s.validateDownloaderConfig(record.Type, record.BaseURL, "", "", input.StorageID)
		if validateErr != nil {
			return DownloaderSummary{}, validateErr
		}
		selection, resolveErr := s.resolveProviderDirectory(ctx, actor, storage, *input.ProviderDirectoryToken)
		if resolveErr != nil {
			return DownloaderSummary{}, resolveErr
		}
		record.StorageID = storage
		record.ProviderDirectoryID = selection.ProviderID
		record.ProviderDirectoryPath = selection.RelativeRoot
	}
	username, err := s.credentials.Decrypt(downloaderPurpose(record.ID, "username"), record.UsernameCiphertext)
	if err != nil {
		return DownloaderSummary{}, err
	}
	password, err := s.credentials.Decrypt(downloaderPurpose(record.ID, "password"), record.PasswordCiphertext)
	if err != nil {
		return DownloaderSummary{}, err
	}
	if input.ClearUsername {
		username = ""
	} else if input.Username != nil && *input.Username != "" {
		username = *input.Username
	}
	if input.ClearPassword {
		password = ""
	} else if input.Password != nil && *input.Password != "" {
		password = *input.Password
	}
	record.BaseURL, _, err = s.validateDownloaderConfig(record.Type, record.BaseURL, username, password, record.StorageID)
	if err != nil {
		return DownloaderSummary{}, err
	}
	record.UsernameCiphertext, err = s.credentials.Encrypt(downloaderPurpose(record.ID, "username"), username)
	if err != nil {
		return DownloaderSummary{}, err
	}
	record.PasswordCiphertext, err = s.credentials.Encrypt(downloaderPurpose(record.ID, "password"), password)
	if err != nil {
		return DownloaderSummary{}, err
	}
	record.LastHealthStatus, record.LastHealthErrorCode, record.LastHealthVersion, record.LastHealthCheckedAt = "unknown", "", "", nil
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "downloader.update", "downloader", record.ID, "success", map[string]any{"type": record.Type, "enabled": record.Enabled}, request)
	})
	if err != nil {
		if conflict := downloaderConstraintError(err); conflict != nil {
			return DownloaderSummary{}, conflict
		}
		return DownloaderSummary{}, err
	}
	return s.summary(record), nil
}

func (s *DownloaderService) Test(ctx context.Context, actor Actor, id string, request RequestContext) (DownloaderSummary, error) {
	if !actor.Can(authz.PermissionDownloadersTest) {
		return DownloaderSummary{}, appError(CodePermissionDenied, "无权测试下载器", nil)
	}
	record, client, err := s.client(id)
	now := time.Now().UTC()
	status, version, errorCode, outcome := "offline", "", "downloader_unavailable", "failure"
	if err == nil {
		health, testErr := client.Test(ctx)
		if testErr == nil {
			status, version, errorCode, outcome = "online", health.Version, "", "success"
		} else {
			errorCode, _ = downloadpkg.ErrorInfo(testErr)
			err = testErr
		}
	}
	if record.ID != "" {
		record.LastHealthStatus, record.LastHealthVersion, record.LastHealthErrorCode, record.LastHealthCheckedAt = status, safeLabel(version, 64), safeLabel(errorCode, 96), &now
		if saveErr := s.db.Transaction(func(tx *gorm.DB) error {
			if saveErr := tx.Save(&record).Error; saveErr != nil {
				return saveErr
			}
			return s.audit.Record(tx, &actor.User.ID, "downloader.test", "downloader", record.ID, outcome, map[string]any{"type": record.Type, "error_code": errorCode}, request)
		}); saveErr != nil {
			return DownloaderSummary{}, saveErr
		}
	}
	if err != nil {
		return DownloaderSummary{}, appError(CodeDownloaderUnavailable, downloaderTestMessage(record.Type, errorCode), err)
	}
	return s.summary(record), nil
}

func (s *DownloaderService) Delete(actor Actor, id string, request RequestContext) error {
	if !actor.Can(authz.PermissionDownloadersDelete) {
		return appError(CodePermissionDenied, "无权删除下载器", nil)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var record models.Downloader
		if err := tx.First(&record, "id = ?", id).Error; err != nil {
			return downloaderNotFound(err)
		}
		var active int64
		if err := tx.Table("download_tasks").Joins("JOIN jobs ON jobs.id = download_tasks.job_id").Where("download_tasks.downloader_id = ? AND jobs.status IN ?", id, activeJobStatuses()).Count(&active).Error; err != nil {
			return err
		}
		if active == 0 {
			if err := tx.Model(&models.SeedingTask{}).Where("downloader_id = ? AND phase IN ?", id, []string{models.SeedingTaskStatusQueued, models.SeedingTaskStatusSeeding, models.SeedingTaskStatusCleanup, models.SeedingTaskStatusRetained, models.SeedingTaskStatusFailed}).Count(&active).Error; err != nil {
				return err
			}
		}
		if active > 0 {
			return appError(CodeDownloaderInUse, "下载器仍有活跃任务", nil)
		}
		if err := tx.Model(&models.MediaLibrary{}).Where("ingest_enabled = ? AND ingest_downloader_id = ?", true, id).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return appError(CodeDownloaderInUse, "下载器正被媒体库自动摄取配置使用", nil)
		}
		if err := tx.Delete(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "downloader.delete", "downloader", record.ID, "success", map[string]any{"type": record.Type}, request)
	})
}

func (s *DownloaderService) client(id string) (models.Downloader, downloadpkg.Client, error) {
	var record models.Downloader
	if err := s.db.First(&record, "id = ?", id).Error; err != nil {
		return record, nil, downloaderNotFound(err)
	}
	client, err := s.clientFor(record)
	return record, client, err
}

func (s *DownloaderService) clientFor(record models.Downloader) (downloadpkg.Client, error) {
	config := downloadpkg.Config{BaseURL: record.BaseURL}
	if record.Type == models.DownloaderTypePan115Offline {
		if record.StorageID == nil || s.connections == nil {
			return nil, appError(CodeDownloaderStorageUnavailable, "115 离线下载目录不可用", nil)
		}
		var storage models.Storage
		if err := s.db.First(&storage, *record.StorageID).Error; err != nil || !storage.Enabled || storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil {
			return nil, appError(CodeDownloaderStorageUnavailable, "115 离线下载目录不可用", err)
		}
		_, driver, err := s.connections.driver(*storage.ConnectionID)
		if err != nil {
			return nil, err
		}
		native, ok := driver.(cloudpkg.NativeOfflineDriver)
		if !ok || !driver.Capabilities().NativeOfflineDownload {
			return nil, appError(CodeDownloaderUnavailable, "该 115 连接不支持原生离线下载", nil)
		}
		directoryID := strings.TrimSpace(record.ProviderDirectoryID)
		if directoryID == "" {
			directoryID = storage.RootPath
		}
		config.CloudDriver, config.ProviderStorageRootID, config.ProviderDirectoryID = native, strings.TrimSpace(storage.RootPath), directoryID
		client, err := s.registry.Build(record.Type, config)
		if err != nil {
			return nil, appError(CodeDownloaderUnavailable, "115 离线下载器配置不可用", err)
		}
		return client, nil
	}
	username, err := s.credentials.Decrypt(downloaderPurpose(record.ID, "username"), record.UsernameCiphertext)
	if err != nil {
		return nil, err
	}
	password, err := s.credentials.Decrypt(downloaderPurpose(record.ID, "password"), record.PasswordCiphertext)
	if err != nil {
		return nil, err
	}
	config.Username, config.Password = username, password
	client, err := s.registry.Build(record.Type, config)
	if err != nil {
		return nil, appError(CodeDownloaderUnavailable, "下载器配置不可用", err)
	}
	return client, nil
}

func (s *DownloaderService) validateDownloaderConfig(providerType, rawURL, username, password string, storageID *uint) (string, *uint, error) {
	rawURL = strings.TrimSpace(rawURL)
	if providerType == models.DownloaderTypeFake {
		return "", nil, nil
	}
	if providerType == models.DownloaderTypePan115Offline {
		if rawURL != "" || username != "" || password != "" {
			return "", nil, appError(CodeInvalidRequest, "115 离线下载器复用数据源凭据，无需填写地址或账号密码", nil)
		}
		if storageID == nil {
			return "", nil, appError(CodeDownloaderStorageRequired, "请选择 115 离线下载目录", nil)
		}
		var storage models.Storage
		if err := s.db.First(&storage, *storageID).Error; err != nil || !storage.Enabled || storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil {
			return "", nil, appError(CodeDownloaderStorageUnavailable, "115 离线下载目录不可用", err)
		}
		return "", &storage.ID, nil
	}
	if len(rawURL) > 2048 || len(username) > 512 || len(password) > 4096 || strings.ContainsAny(username, "\x00\r\n") || strings.Contains(password, "\x00") {
		return "", nil, appError(CodeInvalidRequest, "下载器连接信息无效或过长", nil)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(rawURL, "#") || parsed.RawPath != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", nil, appError(CodeInvalidRequest, "下载器地址必须是无凭据、路径和查询参数的 HTTP(S) origin", err)
	}
	parsed.Path = ""
	baseURL := strings.TrimRight(parsed.String(), "/")
	if _, err := s.registry.Build(providerType, downloadpkg.Config{BaseURL: baseURL, Username: username, Password: password}); err != nil {
		return "", nil, appError(CodeInvalidRequest, "下载器配置无效", err)
	}
	return baseURL, nil, nil
}

func (s *DownloaderService) resolveProviderDirectory(ctx context.Context, actor Actor, storageID *uint, token string) (ProviderDirectorySelection, error) {
	if storageID == nil || strings.TrimSpace(token) == "" {
		return ProviderDirectorySelection{}, appError(CodeDownloaderStorageRequired, "请浏览并选择 115 离线下载目录", nil)
	}
	if s.connections == nil {
		return ProviderDirectorySelection{}, appError(CodeDownloaderStorageUnavailable, "115 离线下载目录不可用", nil)
	}
	selection, err := NewProviderDirectoryService(s.connections, s.credentials).ResolveStorageSelection(ctx, actor, *storageID, token)
	if err != nil {
		return ProviderDirectorySelection{}, err
	}
	if len(selection.ProviderID) > 128 || len(selection.RelativeRoot) > 2048 {
		return ProviderDirectorySelection{}, appError(CodeDirectoryTokenInvalid, "115 离线下载目录无效", nil)
	}
	return selection, nil
}

func normalizeDownloaderName(input string) (string, string, error) {
	name := strings.Join(strings.Fields(input), " ")
	if name == "" {
		return "", "", appError(CodeDownloaderNameRequired, "请填写下载器名称", nil)
	}
	if len([]rune(name)) > 128 {
		return "", "", appError(CodeInvalidRequest, "下载器名称过长", nil)
	}
	return name, strings.ToLower(name), nil
}

func downloaderPurpose(id, field string) string { return "downloader:" + id + ":" + field }

func (s *DownloaderService) summary(record models.Downloader) DownloaderSummary {
	var capabilities downloadpkg.Capabilities
	_ = json.Unmarshal([]byte(record.CapabilitiesJSON), &capabilities)
	name := ""
	if record.StorageID != nil {
		var storage models.Storage
		if s.db.Select("name").First(&storage, *record.StorageID).Error == nil {
			name = storage.Name
		}
	}
	return DownloaderSummary{ID: record.ID, Name: record.Name, Type: record.Type, BaseURL: record.BaseURL, Enabled: record.Enabled, UsernameConfigured: record.UsernameCiphertext != "", PasswordConfigured: record.PasswordCiphertext != "", Capabilities: capabilities, Health: DownloaderHealth{Status: record.LastHealthStatus, Version: record.LastHealthVersion, ErrorCode: record.LastHealthErrorCode, LastChecked: record.LastHealthCheckedAt}, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, StorageID: record.StorageID, StorageName: name, ProviderDirectoryPath: record.ProviderDirectoryPath}
}

func downloaderTestMessage(providerType, code string) string {
	if providerType == models.DownloaderTypePan115Offline {
		switch code {
		case "downloader_auth_failed":
			return "115 登录态已失效，请到数据源页面更新 Cookie"
		case "downloader_rate_limited":
			return "115 当前触发访问保护，请稍后再测试"
		default:
			return "无法使用 115 离线下载目录，请检查数据源连接与目录状态"
		}
	}
	switch code {
	case "downloader_auth_failed":
		return "qBittorrent 认证失败，请检查 Web UI 用户名和密码"
	case "downloader_response_invalid":
		return "下载器响应不兼容，请确认填写的是 qBittorrent Web UI 地址"
	case "downloader_request_failed":
		return "qBittorrent Web API 拒绝了请求，请检查 Web UI 安全设置"
	default:
		return "无法连接下载器，请检查 Web UI 地址、端口以及 qBittorrent 是否正在运行"
	}
}

func downloaderNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "下载器不存在", err)
	}
	return err
}

func downloaderConstraintError(err error) error {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return nil
	}
	if strings.Contains(sqliteErr.Error(), "downloaders.name_normalized") {
		return appError(CodeDownloaderNameConflict, "下载器名称已存在", err)
	}
	return appError(CodeConflict, "下载器配置冲突", err)
}
