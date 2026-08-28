package services

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"gorm.io/gorm"
)

type DefaultIngestLibrarySummary struct {
	ConnectionID uint   `json:"connection_id"`
	LibraryID    uint   `json:"media_library_id"`
	LibraryName  string `json:"media_library_name"`
}

func (s *MediaLibraryService) GetDefaultIngestLibrary(ctx context.Context, actor Actor, connectionID uint) (DefaultIngestLibrarySummary, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return DefaultIngestLibrarySummary{}, appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	return s.defaultIngestLibrary(ctx, connectionID)
}

// SetDefaultIngestLibrary atomically replaces the one manual-life-event
// destination for a 115 Connection. Existing DownloadTasks retain their
// frozen target snapshots.
func (s *MediaLibraryService) SetDefaultIngestLibrary(ctx context.Context, actor Actor, libraryID uint, request RequestContext) (DefaultIngestLibrarySummary, error) {
	if !actor.Can(authz.PermissionMediaLibrariesUpdate) {
		return DefaultIngestLibrarySummary{}, appError(CodePermissionDenied, "无权编辑媒体库", nil)
	}
	library, storage, err := s.validateDefaultIngestCandidate(ctx, libraryID)
	if err != nil {
		return DefaultIngestLibrarySummary{}, err
	}
	connectionID := *storage.ConnectionID
	now := time.Now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MediaLibrary{}).Where("default_ingest_connection_id = ?", connectionID).Updates(map[string]any{"default_ingest_connection_id": nil, "updated_at": now}).Error; err != nil {
			return err
		}
		result := tx.Model(&models.MediaLibrary{}).
			Where("id = ? AND enabled = ? AND storage_id = ?", library.ID, true, storage.ID).
			Updates(map[string]any{"default_ingest_connection_id": connectionID, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConflict, "媒体库配置已变化，请刷新后重试", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "media_library.default_ingest.set", "media_library", strconv.FormatUint(uint64(library.ID), 10), "success", map[string]any{"connection_id": connectionID}, request)
	})
	if err != nil {
		return DefaultIngestLibrarySummary{}, err
	}
	return DefaultIngestLibrarySummary{ConnectionID: connectionID, LibraryID: library.ID, LibraryName: library.Name}, nil
}

// ClearDefaultIngestLibrary fails while any enabled listener still depends on
// the Connection. Callers must disable those listeners or select a replacement
// library first.
func (s *MediaLibraryService) ClearDefaultIngestLibrary(ctx context.Context, actor Actor, connectionID uint, request RequestContext) error {
	if !actor.Can(authz.PermissionMediaLibrariesUpdate) {
		return appError(CodePermissionDenied, "无权编辑媒体库", nil)
	}
	if connectionID == 0 {
		return appError(CodeInvalidRequest, "115 连接无效", nil)
	}
	if err := requireNoEnabledLifeEventListener(ctx, s.db, connectionID); err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.MediaLibrary{}).Where("default_ingest_connection_id = ?", connectionID).Updates(map[string]any{"default_ingest_connection_id": nil, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		return s.audit.Record(tx, &actor.User.ID, "media_library.default_ingest.clear", "connection", strconv.FormatUint(uint64(connectionID), 10), "success", map[string]any{"changed": result.RowsAffected > 0}, request)
	})
}

func (s *MediaLibraryService) validateDefaultIngestCandidate(ctx context.Context, libraryID uint) (models.MediaLibrary, models.Storage, error) {
	if libraryID == 0 {
		return models.MediaLibrary{}, models.Storage{}, appError(CodeInvalidRequest, "请选择默认入库媒体库", nil)
	}
	var library models.MediaLibrary
	if err := s.db.WithContext(ctx).Where("id = ? AND enabled = ?", libraryID, true).First(&library).Error; err != nil {
		return models.MediaLibrary{}, models.Storage{}, appError(CodeMediaLibraryStorageUnavailable, "默认入库媒体库不存在或已停用", err)
	}
	var storage models.Storage
	if err := s.db.WithContext(ctx).Where("id = ? AND enabled = ?", library.StorageID, true).First(&storage).Error; err != nil || storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil {
		return models.MediaLibrary{}, models.Storage{}, appError(CodeMediaLibraryStorageUnavailable, "默认入库媒体库必须属于有效的 115 数据源", err)
	}
	if strings.TrimSpace(library.ProviderRootID) == "" || library.TransferMode != models.MediaLibraryTransferMove && library.TransferMode != models.MediaLibraryTransferCopy {
		return models.MediaLibrary{}, models.Storage{}, appError(CodeTransferRouteUnsupported, "默认入库媒体库必须具有可移动或复制的 115 目录", nil)
	}
	if s.connections == nil {
		return models.MediaLibrary{}, models.Storage{}, appError(CodeConnectionUnavailable, "115 连接不可用", nil)
	}
	_, driver, err := s.connections.driver(*storage.ConnectionID)
	if err != nil {
		return models.MediaLibrary{}, models.Storage{}, appError(CodeConnectionUnavailable, "115 连接不可用", err)
	}
	capabilities := driver.Capabilities()
	if _, ok := driver.(cloudpkg.MutationDriver); !ok || !capabilities.CreateDirectory || !capabilities.Rename || !capabilities.Recycle || library.TransferMode == models.MediaLibraryTransferMove && !capabilities.Move || library.TransferMode == models.MediaLibraryTransferCopy && !capabilities.Copy {
		return models.MediaLibrary{}, models.Storage{}, appError(CodeTransferRouteUnsupported, "默认入库媒体库缺少所需的云端整理能力", nil)
	}
	root, err := providerItemWithinRoot(ctx, driver, library.ProviderRootID, storage.RootPath)
	if err != nil || !root.IsDir {
		return models.MediaLibrary{}, models.Storage{}, appError(CodeMediaLibraryPathInvalid, "默认入库媒体库目录不可用", err)
	}
	return library, storage, nil
}

func (s *MediaLibraryService) defaultIngestLibrary(ctx context.Context, connectionID uint) (DefaultIngestLibrarySummary, error) {
	if connectionID == 0 {
		return DefaultIngestLibrarySummary{}, appError(CodeInvalidRequest, "115 连接无效", nil)
	}
	var library models.MediaLibrary
	if err := s.db.WithContext(ctx).Where("default_ingest_connection_id = ? AND enabled = ?", connectionID, true).First(&library).Error; err != nil {
		return DefaultIngestLibrarySummary{}, appError(CodeMediaLibraryStorageUnavailable, "该 115 连接尚未设置自动监听默认入库媒体库", err)
	}
	return DefaultIngestLibrarySummary{ConnectionID: connectionID, LibraryID: library.ID, LibraryName: library.Name}, nil
}

func requireDefaultIngestForStorage(ctx context.Context, db *gorm.DB, storageID *uint) error {
	if storageID == nil {
		return appError(CodeDownloaderStorageUnavailable, "115 下载器 Storage 不可用", nil)
	}
	var storage models.Storage
	if err := db.WithContext(ctx).First(&storage, *storageID).Error; err != nil || !storage.Enabled || storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil {
		return appError(CodeDownloaderStorageUnavailable, "115 下载器 Storage 不可用", err)
	}
	var count int64
	if err := db.WithContext(ctx).Model(&models.MediaLibrary{}).Where("default_ingest_connection_id = ? AND enabled = ?", *storage.ConnectionID, true).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return appError(CodeMediaLibraryStorageUnavailable, "请先为该 115 连接设置自动监听默认入库媒体库", nil)
	}
	return nil
}

func requireNoEnabledLifeEventListener(ctx context.Context, db *gorm.DB, connectionID uint) error {
	var count int64
	err := db.WithContext(ctx).Table("downloaders").
		Joins("JOIN storages ON storages.id = downloaders.storage_id").
		Where("downloaders.enabled = ? AND downloaders.auto_listen_life_events = ? AND downloaders.type = ? AND storages.connection_id = ?", true, true, models.DownloaderTypePan115Offline, connectionID).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return appError(CodeConflict, "仍有下载器启用自动监听，请先选择替代媒体库或关闭监听", nil)
	}
	return nil
}

func isDefaultIngestConstraint(err error) bool {
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "idx_media_libraries_default_ingest_connection") || errors.Is(err, gorm.ErrDuplicatedKey))
}
