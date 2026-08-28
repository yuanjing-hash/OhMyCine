package services

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/directory"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	"gorm.io/gorm"
)

type DownloadSettingsService struct {
	db    *gorm.DB
	audit *AuditService
}

func NewDownloadSettingsService(db *gorm.DB, audit *AuditService) *DownloadSettingsService {
	return &DownloadSettingsService{db: db, audit: audit}
}

type DownloadSettingsSummary struct {
	Configured   bool      `json:"configured"`
	AbsolutePath string    `json:"absolute_path"`
	Revision     uint64    `json:"revision"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type UpdateDownloadSettingsInput struct {
	AbsolutePath string
	Revision     uint64
}

type DownloadStagingSnapshot struct {
	AbsolutePath string
}

func (s *DownloadSettingsService) Get(actor Actor) (DownloadSettingsSummary, error) {
	if !actor.Can(authz.PermissionSettingsRead) {
		return DownloadSettingsSummary{}, appError(CodePermissionDenied, "无权查看下载设置", nil)
	}
	var record models.DownloadSettings
	if err := s.db.First(&record, 1).Error; err != nil {
		return DownloadSettingsSummary{}, err
	}
	absolute := record.AbsolutePath
	if absolute == "" && record.StorageID != nil {
		absolute, _ = s.resolveRecord(context.Background(), record)
	}
	return downloadSettingsSummary(record, absolute), nil
}

func (s *DownloadSettingsService) Update(ctx context.Context, actor Actor, input UpdateDownloadSettingsInput, request RequestContext) (DownloadSettingsSummary, error) {
	if !actor.Can(authz.PermissionSettingsUpdate) {
		return DownloadSettingsSummary{}, appError(CodePermissionDenied, "无权编辑下载设置", nil)
	}
	if input.Revision == 0 || input.Revision >= math.MaxInt64 {
		return DownloadSettingsSummary{}, appError(CodeConflict, "下载设置版本无效，请刷新", nil)
	}
	absolute, err := validateGlobalStaging(ctx, input.AbsolutePath)
	if err != nil {
		return DownloadSettingsSummary{}, err
	}
	now := time.Now().UTC()
	var updated models.DownloadSettings
	err = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.DownloadSettings{}).Where("id = ? AND revision = ?", 1, input.Revision).Updates(map[string]any{"absolute_path": absolute, "storage_id": nil, "relative_path": "/", "revision": gorm.Expr("revision + 1"), "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConflict, "下载设置已被其他会话更新，请刷新后重试", nil)
		}
		if err := tx.First(&updated, 1).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "download_settings.update", "download_settings", "1", "success", map[string]any{"configured": true}, request)
	})
	if err != nil {
		return DownloadSettingsSummary{}, err
	}
	return downloadSettingsSummary(updated, absolute), nil
}

var errDownloadStagingNotConfigured = errors.New("download staging not configured")

func (s *DownloadSettingsService) Snapshot(ctx context.Context, providerType string) (DownloadStagingSnapshot, error) {
	if providerType == models.DownloaderTypeFake || providerType == models.DownloaderTypePan115Offline {
		return DownloadStagingSnapshot{}, nil
	}
	var record models.DownloadSettings
	if err := s.db.First(&record, 1).Error; err != nil {
		return DownloadStagingSnapshot{}, err
	}
	absolute, err := s.resolveRecord(ctx, record)
	if err != nil {
		return DownloadStagingSnapshot{}, err
	}
	return DownloadStagingSnapshot{AbsolutePath: absolute}, nil
}

// SnapshotForRoute requires the Server-managed local working root when a
// provider-native download will cross into another data source. Same-source
// 115 organization keeps its cloud-native path and does not consume local
// staging space.
func (s *DownloadSettingsService) SnapshotForRoute(ctx context.Context, providerType, routeKind string) (DownloadStagingSnapshot, error) {
	if routeKind != models.TransferRouteCrossSource {
		return s.Snapshot(ctx, providerType)
	}
	var record models.DownloadSettings
	if err := s.db.First(&record, 1).Error; err != nil {
		return DownloadStagingSnapshot{}, err
	}
	absolute, err := s.resolveRecord(ctx, record)
	if err != nil {
		return DownloadStagingSnapshot{}, err
	}
	return DownloadStagingSnapshot{AbsolutePath: absolute}, nil
}

func (s *DownloadSettingsService) ResolveSnapshot(ctx context.Context, providerType, absolute string, storageID *uint, relative string) (string, error) {
	if providerType == models.DownloaderTypeFake || providerType == models.DownloaderTypePan115Offline {
		return "", nil
	}
	if absolute != "" {
		return validateGlobalStaging(ctx, absolute)
	}
	return s.resolveLegacy(storageID, relative)
}

func (s *DownloadSettingsService) resolveRecord(ctx context.Context, record models.DownloadSettings) (string, error) {
	if record.AbsolutePath != "" {
		return validateGlobalStaging(ctx, record.AbsolutePath)
	}
	return s.resolveLegacy(record.StorageID, record.RelativePath)
}

func (s *DownloadSettingsService) resolveLegacy(storageID *uint, relative string) (string, error) {
	if storageID == nil {
		return "", appError(CodeDownloadStagingRequired, "请先在系统设置中配置统一下载暂存目录", errDownloadStagingNotConfigured)
	}
	var storage models.Storage
	if err := s.db.First(&storage, *storageID).Error; err != nil || !storage.Enabled || storage.Type != models.StorageTypeLocal {
		return "", appError(CodeDownloadStagingUnavailable, "旧版下载暂存目录所属 Storage 不可用", err)
	}
	relative, err := medialibrary.NormalizeRelativeRoot(relative)
	if err != nil {
		return "", appError(CodeDownloadStagingUnavailable, "旧版下载暂存目录无效", err)
	}
	absolute, err := medialibrary.ResolveRoot(storage.RootPath, relative)
	if err != nil {
		return "", appError(CodeDownloadStagingUnavailable, "旧版下载暂存目录不存在、不可读或越过 Storage 边界", err)
	}
	return absolute, nil
}

func (s *DownloadSettingsService) StorageReferences(storageID uint) ([]string, error) {
	var count int64
	if err := s.db.Model(&models.DownloadSettings{}).Where("id = ? AND absolute_path = '' AND storage_id = ?", 1, storageID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return []string{"download_settings"}, nil
	}
	if err := s.db.Table("download_tasks").Joins("JOIN jobs ON jobs.id = download_tasks.job_id").Where("download_tasks.staging_absolute_path = '' AND download_tasks.staging_storage_id = ? AND jobs.status IN ?", storageID, activeJobStatuses()).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return []string{"active_download_task"}, nil
	}
	return nil, nil
}

func downloadSettingsSummary(record models.DownloadSettings, absolute string) DownloadSettingsSummary {
	return DownloadSettingsSummary{Configured: absolute != "" || record.StorageID != nil, AbsolutePath: absolute, Revision: record.Revision, UpdatedAt: record.UpdatedAt}
}

func validateGlobalStaging(ctx context.Context, path string) (string, error) {
	if err := (directory.NativeAdapter{}).Validate(ctx, path); err != nil {
		return "", appError(CodeDownloadStagingUnavailable, "下载暂存目录不存在、不可读或包含符号链接/Reparse Point", nil)
	}
	canonical, err := (storagefs.LocalDriver{}).CanonicalizeRoot(path)
	if err != nil {
		return "", appError(CodeDownloadStagingUnavailable, "下载暂存目录不存在、不可读或包含符号链接/Reparse Point", nil)
	}
	return canonical, nil
}
