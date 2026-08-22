package services

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

const maxSourceAssetExtraExtensions = 16

type MediaLibraryService struct {
	db          *gorm.DB
	audit       *AuditService
	log         zerolog.Logger
	mu          sync.Mutex
	supervisors map[uint]supervisorHandle
	scanLocks   map[uint]*sync.Mutex
	connections *ConnectionService
	metadata    *MetadataSettingsService
	ingest      MediaLibraryIngestEnqueuer
	artifacts   *MediaArtifactService
	closed      bool
}

// MediaLibraryIngestEnqueuer is the narrow boundary from provider directory
// reconciliation into the existing durable download pipeline.
type MediaLibraryIngestEnqueuer interface {
	AdoptProviderItem(context.Context, uint, string, string) (bool, error)
}

type supervisorHandle struct {
	cancel context.CancelFunc
	done   <-chan struct{}
	wake   chan struct{}
}

type MediaLibraryInput struct {
	Name                     string
	StorageID                uint
	ProfileID                uint
	RelativeRoot             string
	ProviderRootID           string
	Enabled                  bool
	Recursive                bool
	FullScanIntervalHours    int
	IncrementalMinutes       int
	VideoExtensions          []string
	STRMAssetExtraExtensions []string
	IgnorePatterns           []string
	MetadataLanguage         string
	MetadataRegion           string
	MatchStrategy            string
	ProviderRatePerSecond    int
	ProviderConcurrency      int
	MetadataRatePerSecond    int
	MetadataConcurrency      int
	STRMEnabled              bool
	STRMLocalRoot            string
	MetadataArtifactsEnabled *bool
	UploadSidecars           bool
	TransferMode             string
	ConflictPolicy           string
	MovieDirectoryTemplate   string
	MovieFilenameTemplate    string
	TVDirectoryTemplate      string
	TVFilenameTemplate       string
	IngestEnabled            bool
	IngestDownloaderID       string
	IngestProviderRootID     string
	IngestRelativeRoot       string
}
type UpdateMediaLibraryInput struct {
	MediaLibraryInput
	RevisionUpdated bool
}
type MediaLibraryDetail struct {
	models.MediaLibrary
	StorageName                  string   `json:"storage_name"`
	ProfileName                  string   `json:"profile_name"`
	VideoExtensions              []string `json:"video_extensions"`
	STRMAssetDefaultExtensions   []string `json:"strm_asset_default_extensions"`
	STRMAssetExtraExtensions     []string `json:"strm_asset_extra_extensions"`
	STRMAssetEffectiveExtensions []string `json:"strm_asset_effective_extensions"`
	IgnorePatterns               []string `json:"ignore_patterns"`
	EntryCount                   int64    `json:"entry_count"`
	IngestDownloaderName         string   `json:"ingest_downloader_name"`
	STRMLocalPath                string   `json:"strm_local_path"`
}

func NewMediaLibraryService(db *gorm.DB, audit *AuditService, log zerolog.Logger) *MediaLibraryService {
	return &MediaLibraryService{db: db, audit: audit, log: log, supervisors: map[uint]supervisorHandle{}, scanLocks: map[uint]*sync.Mutex{}}
}
func (s *MediaLibraryService) SetConnectionService(connections *ConnectionService) {
	s.connections = connections
}
func (s *MediaLibraryService) SetMetadataSettingsService(metadata *MetadataSettingsService) {
	s.metadata = metadata
}
func (s *MediaLibraryService) SetIngestEnqueuer(ingest MediaLibraryIngestEnqueuer) {
	s.ingest = ingest
}
func (s *MediaLibraryService) SetArtifactService(artifacts *MediaArtifactService) {
	s.artifacts = artifacts
}
func (s *MediaLibraryService) Start(ctx context.Context) error {
	var libraries []models.MediaLibrary
	if err := s.db.Where("enabled = ?", true).Find(&libraries).Error; err != nil {
		return err
	}
	for _, library := range libraries {
		s.startSupervisor(ctx, library.ID)
	}
	serverlog.OperationLibraryEventScan.Event(s.log.Info()).Int("library_count", len(libraries)).Msg(serverlog.OperationLibraryEventScan.Message("媒体库监听已启动"))
	return nil
}
func (s *MediaLibraryService) Close() {
	s.mu.Lock()
	s.closed = true
	handles := make([]supervisorHandle, 0, len(s.supervisors))
	for _, handle := range s.supervisors {
		handle.cancel()
		handles = append(handles, handle)
	}
	s.supervisors = map[uint]supervisorHandle{}
	s.mu.Unlock()
	for _, handle := range handles {
		<-handle.done
	}
}

func (s *MediaLibraryService) List(actor Actor) ([]MediaLibraryDetail, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	var records []models.MediaLibrary
	if err := s.db.Order("sort_order,id").Find(&records).Error; err != nil {
		return nil, err
	}
	out := make([]MediaLibraryDetail, 0, len(records))
	for _, record := range records {
		detail, err := s.detail(record)
		if err != nil {
			return nil, err
		}
		out = append(out, detail)
	}
	return out, nil
}
func (s *MediaLibraryService) Get(actor Actor, id uint) (MediaLibraryDetail, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return MediaLibraryDetail{}, appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	var record models.MediaLibrary
	if err := s.db.First(&record, id).Error; err != nil {
		return MediaLibraryDetail{}, mediaLibraryNotFound(err)
	}
	return s.detail(record)
}

func (s *MediaLibraryService) Create(ctx context.Context, actor Actor, input MediaLibraryInput, request RequestContext) (MediaLibraryDetail, error) {
	if !actor.Can(authz.PermissionMediaLibrariesCreate) {
		return MediaLibraryDetail{}, appError(CodePermissionDenied, "无权创建媒体库", nil)
	}
	record, err := s.validateInput(ctx, 0, actor, input)
	if err != nil {
		return MediaLibraryDetail{}, err
	}
	transactionErr := s.db.Transaction(func(tx *gorm.DB) error {
		var maxOrder int
		if err := tx.Model(&models.MediaLibrary{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
			return err
		}
		record.SortOrder = maxOrder + 1
		// Select writes explicit false/zero configuration values instead of
		// replacing them with GORM tag defaults (notably enabled=false).
		if err := tx.Select("*").Create(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_library.create", "media_library", uintID(record.ID), "success", map[string]any{"storage_id": record.StorageID, "profile_id": record.ProfileID, "relative_root": record.RelativeRoot, "enabled": record.Enabled}, request)
	})
	if transactionErr != nil {
		return MediaLibraryDetail{}, mediaLibraryConstraint(err)
	}
	if record.Enabled {
		s.startSupervisor(context.Background(), record.ID)
	}
	return s.detail(record)
}

func (s *MediaLibraryService) Update(ctx context.Context, actor Actor, id uint, input MediaLibraryInput, request RequestContext) (MediaLibraryDetail, error) {
	if !actor.Can(authz.PermissionMediaLibrariesUpdate) {
		return MediaLibraryDetail{}, appError(CodePermissionDenied, "无权编辑媒体库", nil)
	}
	var existing models.MediaLibrary
	if err := s.db.First(&existing, id).Error; err != nil {
		return MediaLibraryDetail{}, mediaLibraryNotFound(err)
	}
	if input.MetadataArtifactsEnabled == nil {
		value := existing.MetadataArtifactsEnabled
		input.MetadataArtifactsEnabled = &value
	}
	record, err := s.validateInput(ctx, id, actor, input)
	if err != nil {
		return MediaLibraryDetail{}, err
	}
	record.ID = id
	record.CreatedAt = existing.CreatedAt
	record.ArtifactGeneration = existing.ArtifactGeneration
	record.ArtifactAppliedGeneration = existing.ArtifactAppliedGeneration
	record.ArtifactStatus = existing.ArtifactStatus
	record.ArtifactError = existing.ArtifactError
	record.ArtifactUpdatedAt = existing.ArtifactUpdatedAt
	record.ArtifactCleanupRemoved = existing.ArtifactCleanupRemoved
	record.ArtifactCleanupError = existing.ArtifactCleanupError
	record.ArtifactCleanupAt = existing.ArtifactCleanupAt
	sourceChanged := mediaLibrarySourceChanged(existing, record)
	if !sourceChanged {
		record.BaselineGeneration = existing.BaselineGeneration
		record.DirtyGeneration = existing.DirtyGeneration
		record.LastScanAt = existing.LastScanAt
		record.LastSuccessfulScanAt = existing.LastSuccessfulScanAt
	}
	record.SortOrder = existing.SortOrder
	if record.Enabled {
		record.Status = models.MediaLibraryStatusInitializing
	} else {
		record.Status = models.MediaLibraryStatusDisabled
	}
	s.stopSupervisor(id)
	lock := s.scanLock(id)
	lock.Lock()
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if sourceChanged {
			if err := tx.Where("library_id = ?", id).Delete(&models.MediaLibraryEntry{}).Error; err != nil {
				return err
			}
			if err := tx.Where("library_id = ?", id).Delete(&models.MediaLibraryRecognition{}).Error; err != nil {
				return err
			}
			if err := tx.Where("library_id = ?", id).Delete(&models.MediaLibraryScanRun{}).Error; err != nil {
				return err
			}
			if err := tx.Where("library_id = ?", id).Delete(&models.MediaLibrarySourceAsset{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_library.update", "media_library", uintID(id), "success", map[string]any{"storage_id": record.StorageID, "profile_id": record.ProfileID, "relative_root": record.RelativeRoot, "enabled": record.Enabled}, request)
	}); err != nil {
		lock.Unlock()
		if existing.Enabled {
			s.startSupervisor(context.Background(), id)
		}
		return MediaLibraryDetail{}, mediaLibraryConstraint(err)
	}
	lock.Unlock()
	if record.Enabled {
		s.startSupervisor(context.Background(), id)
	}
	return s.detail(record)
}

func mediaLibrarySourceChanged(existing, replacement models.MediaLibrary) bool {
	return existing.StorageID != replacement.StorageID ||
		existing.RelativeRoot != replacement.RelativeRoot ||
		existing.ProviderRootID != replacement.ProviderRootID
}

func (s *MediaLibraryService) Reorder(actor Actor, ids []uint, request RequestContext) ([]MediaLibraryDetail, error) {
	if !actor.Can(authz.PermissionMediaLibrariesUpdate) {
		return nil, appError(CodePermissionDenied, "无权调整媒体库顺序", nil)
	}
	if len(ids) == 0 {
		return nil, appError(CodeInvalidRequest, "媒体库顺序不能为空", nil)
	}
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			return nil, appError(CodeInvalidRequest, "媒体库顺序无效", nil)
		}
		if _, exists := seen[id]; exists {
			return nil, appError(CodeInvalidRequest, "媒体库顺序包含重复项", nil)
		}
		seen[id] = struct{}{}
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.MediaLibrary{}).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return appError(CodeConflict, "媒体库列表已变化，请刷新后重试", nil)
		}
		for index, id := range ids {
			result := tx.Model(&models.MediaLibrary{}).Where("id = ?", id).Update("sort_order", index+1)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return appError(CodeConflict, "媒体库列表已变化，请刷新后重试", nil)
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "media_library.reorder", "media_library", "all", "success", map[string]any{"count": len(ids)}, request)
	})
	if err != nil {
		return nil, err
	}
	return s.List(actor)
}

func (s *MediaLibraryService) Delete(actor Actor, id uint, request RequestContext) error {
	if !actor.Can(authz.PermissionMediaLibrariesDelete) {
		return appError(CodePermissionDenied, "无权删除媒体库", nil)
	}
	s.stopSupervisor(id)
	lock := s.scanLock(id)
	lock.Lock()
	defer lock.Unlock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var record models.MediaLibrary
		if err := tx.First(&record, id).Error; err != nil {
			return mediaLibraryNotFound(err)
		}
		if err := tx.Delete(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_library.delete", "media_library", uintID(id), "success", map[string]any{"storage_id": record.StorageID, "relative_root": record.RelativeRoot}, request)
	})
	if err == nil {
		s.mu.Lock()
		delete(s.scanLocks, id)
		s.mu.Unlock()
	}
	return err
}
func (s *MediaLibraryService) Retry(actor Actor, id uint) error {
	if !actor.Can(authz.PermissionMediaLibrariesScan) {
		return appError(CodePermissionDenied, "无权扫描媒体库", nil)
	}
	var record models.MediaLibrary
	if err := s.db.First(&record, id).Error; err != nil {
		return mediaLibraryNotFound(err)
	}
	if !record.Enabled {
		return appError(CodeConflict, "媒体库已停用", nil)
	}
	s.stopSupervisor(id)
	s.startSupervisor(context.Background(), id)
	return nil
}
func (s *MediaLibraryService) ScanNow(ctx context.Context, actor Actor, id uint) (models.MediaLibraryScanRun, error) {
	if !actor.Can(authz.PermissionMediaLibrariesScan) {
		return models.MediaLibraryScanRun{}, appError(CodePermissionDenied, "无权扫描媒体库", nil)
	}
	return s.reconcile(ctx, id, "manual")
}

func (s *MediaLibraryService) ReconcileSTRM(ctx context.Context, id uint, mode string) (models.MediaLibraryScanRun, error) {
	kind := "strm_incremental_manual"
	if mode == "full" {
		kind = "strm_full_manual"
	} else if mode != "incremental" {
		return models.MediaLibraryScanRun{}, appError(CodeInvalidRequest, "STRM 刷新模式无效", nil)
	}
	var library models.MediaLibrary
	if err := s.db.First(&library, id).Error; err != nil {
		return models.MediaLibraryScanRun{}, mediaLibraryNotFound(err)
	}
	if !library.Enabled || !library.STRMEnabled {
		return models.MediaLibraryScanRun{}, appError(CodeConflict, "媒体库未启用 STRM", nil)
	}
	return s.reconcile(ctx, id, kind)
}
func (s *MediaLibraryService) Entries(actor Actor, id uint, limit int) ([]models.MediaLibraryEntry, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var items []models.MediaLibraryEntry
	if err := s.db.Where("library_id = ?", id).Order("relative_path").Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}
func (s *MediaLibraryService) Runs(actor Actor, id uint, limit int) ([]models.MediaLibraryScanRun, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看扫描记录", nil)
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var items []models.MediaLibraryScanRun
	if err := s.db.Where("library_id = ?", id).Order("id DESC").Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}
func (s *MediaLibraryService) References(profileID uint) ([]string, error) {
	var records []models.MediaLibrary
	if err := s.db.Where("profile_id = ?", profileID).Find(&records).Error; err != nil {
		return nil, err
	}
	names := make([]string, len(records))
	for i := range records {
		names[i] = records[i].Name
	}
	return names, nil
}
func (s *MediaLibraryService) StorageReferences(storageID uint) ([]string, error) {
	var records []models.MediaLibrary
	if err := s.db.Where("storage_id = ?", storageID).Find(&records).Error; err != nil {
		return nil, err
	}
	names := make([]string, len(records))
	for i := range records {
		names[i] = records[i].Name
	}
	return names, nil
}
func (s *MediaLibraryService) ProfileRevisionChanged(profileID uint, revision uint64) error {
	var profile models.MediaClassificationProfile
	if err := s.db.First(&profile, profileID).Error; err != nil {
		return err
	}
	organization, err := storedProfileOrganizationConfig(profile)
	if err != nil {
		return err
	}
	return s.db.Model(&models.MediaLibrary{}).Where("profile_id = ? AND profile_revision <> ?", profileID, revision).Updates(map[string]any{"reclassification_due": true, "movie_directory_template": organization.MovieDirectoryTemplate, "movie_filename_template": organization.MovieFilenameTemplate, "tv_directory_template": organization.TVDirectoryTemplate, "tv_filename_template": organization.TVFilenameTemplate}).Error
}

func (s *MediaLibraryService) validateInput(ctx context.Context, id uint, actor Actor, input MediaLibraryInput) (models.MediaLibrary, error) {
	name := strings.Join(strings.Fields(input.Name), " ")
	if name == "" {
		return models.MediaLibrary{}, appError(CodeMediaLibraryNameRequired, "请填写媒体库名称", nil)
	}
	if len([]rune(name)) > 128 {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "媒体库名称过长", nil)
	}
	relativeRoot, err := medialibrary.NormalizeRelativeRoot(input.RelativeRoot)
	if err != nil {
		return models.MediaLibrary{}, appError(CodeMediaLibraryPathInvalid, "媒体库相对路径无效", err)
	}
	var storage models.Storage
	if err := s.db.First(&storage, input.StorageID).Error; err != nil || !storage.Enabled || (storage.Type != models.StorageTypeLocal && storage.Type != models.StorageTypePan115) {
		return models.MediaLibrary{}, appError(CodeMediaLibraryStorageUnavailable, "来源 Storage 不可用", err)
	}
	if storage.Type == models.StorageTypeLocal {
		if _, err := medialibrary.ResolveRoot(storage.RootPath, relativeRoot); err != nil {
			return models.MediaLibrary{}, appError(CodeMediaLibraryPathInvalid, "媒体库目录不可读或越过 Storage 边界", err)
		}
	} else if storage.ConnectionID == nil || s.connections == nil || strings.TrimSpace(input.ProviderRootID) == "" {
		return models.MediaLibrary{}, appError(CodeMediaLibraryPathInvalid, "115 媒体库目录身份无效", nil)
	}
	if storage.Type == models.StorageTypeLocal && (input.STRMEnabled || input.STRMLocalRoot != "") {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "本地来源不能启用 STRM 投影", nil)
	}
	var capabilities storagefs.Capabilities
	if strings.TrimSpace(storage.Capabilities) != "" {
		if err := json.Unmarshal([]byte(storage.Capabilities), &capabilities); err != nil {
			return models.MediaLibrary{}, appError(CodeMediaLibraryStorageUnavailable, "来源 Storage 能力信息无效", err)
		}
	}
	metadataArtifactsEnabled := storage.Type == models.StorageTypeLocal || input.STRMEnabled
	if input.MetadataArtifactsEnabled != nil {
		metadataArtifactsEnabled = *input.MetadataArtifactsEnabled
	}
	strmLocalRoot := ""
	if storage.Type == models.StorageTypeLocal {
		if input.UploadSidecars {
			return models.MediaLibrary{}, appError(CodeInvalidRequest, "本地媒体库不使用云端旁挂上传", nil)
		}
	} else if input.STRMEnabled {
		if !capabilities.TemporaryDirectURL || !capabilities.SignedProxy {
			return models.MediaLibrary{}, appError(CodeInvalidRequest, "来源 Storage 尚不支持安全 STRM / 302", nil)
		}
		if input.UploadSidecars {
			return models.MediaLibrary{}, appError(CodeInvalidRequest, "STRM 模式的 NFO/JPG 只生成在本地投影目录", nil)
		}
		strmLocalRoot, err = (storagefs.LocalDriver{}).CanonicalizeRoot(input.STRMLocalRoot)
		if err != nil {
			return models.MediaLibrary{}, appError(CodeMediaLibraryPathInvalid, "STRM 本地投影目录不可用", err)
		}
	} else {
		if strings.TrimSpace(input.STRMLocalRoot) != "" {
			return models.MediaLibrary{}, appError(CodeInvalidRequest, "未启用 STRM 时不能保存本地投影目录", nil)
		}
		if input.UploadSidecars && (!metadataArtifactsEnabled || !capabilities.SmallFileUpload) {
			return models.MediaLibrary{}, appError(CodeInvalidRequest, "来源 Storage 不支持 NFO/JPG 旁挂上传", nil)
		}
	}
	ingestDownloaderID, ingestProviderRootID, ingestRelativeRoot := "", "", ""
	var ingestOwnerID *uint
	if input.IngestEnabled {
		if storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil || s.connections == nil {
			return models.MediaLibrary{}, appError(CodeInvalidRequest, "只有 115 媒体库可以启用自动摄取", nil)
		}
		ingestDownloaderID = strings.TrimSpace(input.IngestDownloaderID)
		ingestProviderRootID = strings.TrimSpace(input.IngestProviderRootID)
		var normalizeErr error
		ingestRelativeRoot, normalizeErr = medialibrary.NormalizeRelativeRoot(input.IngestRelativeRoot)
		if normalizeErr != nil || ingestDownloaderID == "" || ingestProviderRootID == "" {
			return models.MediaLibrary{}, appError(CodeInvalidRequest, "请选择 115 中转目录和绑定下载器", normalizeErr)
		}
		var downloader models.Downloader
		if err := s.db.First(&downloader, "id = ?", ingestDownloaderID).Error; err != nil || !downloader.Enabled || downloader.Type != models.DownloaderTypePan115Offline || downloader.StorageID == nil {
			return models.MediaLibrary{}, appError(CodeDownloaderUnavailable, "自动摄取下载器不存在、已停用或类型不匹配", err)
		}
		var downloaderStorage models.Storage
		if err := s.db.First(&downloaderStorage, *downloader.StorageID).Error; err != nil || downloaderStorage.Type != models.StorageTypePan115 || downloaderStorage.ConnectionID == nil || *downloaderStorage.ConnectionID != *storage.ConnectionID {
			return models.MediaLibrary{}, appError(CodeDownloaderUnavailable, "自动摄取下载器与媒体库不属于同一 115 账号", err)
		}
		_, driver, err := s.connections.driver(*storage.ConnectionID)
		if err != nil {
			return models.MediaLibrary{}, appError(CodeMediaLibraryStorageUnavailable, "115 连接不可用", err)
		}
		finalRoot, err := providerItemWithinRoot(ctx, driver, strings.TrimSpace(input.ProviderRootID), strings.TrimSpace(storage.RootPath))
		if err != nil || !finalRoot.IsDir {
			return models.MediaLibrary{}, appError(CodeMediaLibraryPathInvalid, "115 媒体库目录不可用", err)
		}
		ingestRoot, err := providerItemWithinRoot(ctx, driver, ingestProviderRootID, strings.TrimSpace(storage.RootPath))
		if err != nil || !ingestRoot.IsDir {
			return models.MediaLibrary{}, appError(CodeMediaLibraryPathInvalid, "115 中转目录不可用", err)
		}
		overlaps, err := providerDirectoriesOverlap(ctx, driver, finalRoot.ID, ingestRoot.ID)
		if err != nil {
			return models.MediaLibrary{}, appError(CodeMediaLibraryStorageUnavailable, "无法验证 115 中转目录边界", err)
		}
		if overlaps {
			return models.MediaLibrary{}, appError(CodeMediaLibraryOverlap, "115 中转目录不能与最终媒体库目录重叠", nil)
		}
		var otherRoots []string
		query := s.db.WithContext(ctx).Table("media_libraries").
			Select("media_libraries.ingest_provider_root_id").
			Joins("JOIN storages ON storages.id = media_libraries.storage_id").
			Where("media_libraries.ingest_enabled = ? AND storages.type = ? AND storages.connection_id = ?", true, models.StorageTypePan115, *storage.ConnectionID)
		if id != 0 {
			query = query.Where("media_libraries.id <> ?", id)
		}
		if err := query.Scan(&otherRoots).Error; err != nil {
			return models.MediaLibrary{}, err
		}
		for _, otherRootID := range otherRoots {
			otherRootID = strings.TrimSpace(otherRootID)
			if otherRootID == "" {
				continue
			}
			overlaps, err := providerDirectoriesOverlap(ctx, driver, ingestRoot.ID, otherRootID)
			if err != nil {
				return models.MediaLibrary{}, appError(CodeMediaLibraryStorageUnavailable, "无法验证现有 115 中转目录边界", err)
			}
			if overlaps {
				return models.MediaLibrary{}, appError(CodeMediaLibraryOverlap, "115 中转目录与现有媒体库中转目录重叠", nil)
			}
		}
		ownerID := actor.User.ID
		ingestOwnerID = &ownerID
	}
	var profile models.MediaClassificationProfile
	if err := s.db.First(&profile, input.ProfileID).Error; err != nil {
		return models.MediaLibrary{}, appError(CodeMediaLibraryProfileUnavailable, "媒体分类规则不可用", err)
	}
	organization, err := storedProfileOrganizationConfig(profile)
	if err != nil {
		return models.MediaLibrary{}, appError(CodeProfileValidation, "媒体规则的识别与命名配置无效", err)
	}
	// Keep validating legacy API fields so malformed clients still fail
	// explicitly, but Profile is the naming source for every new save.
	for _, template := range []struct {
		value     string
		directory bool
		message   string
	}{{input.MovieDirectoryTemplate, true, "电影目录模板无效"}, {input.MovieFilenameTemplate, false, "电影文件名模板无效"}, {input.TVDirectoryTemplate, true, "剧集目录模板无效"}, {input.TVFilenameTemplate, false, "剧集文件名模板无效"}} {
		if strings.TrimSpace(template.value) != "" {
			if err := validateImportTemplate(template.value, template.directory); err != nil {
				return models.MediaLibrary{}, appError(CodeInvalidRequest, template.message, err)
			}
		}
	}
	var overlaps []models.MediaLibrary
	query := s.db.Where("storage_id = ?", input.StorageID)
	if id != 0 {
		query = query.Where("id <> ?", id)
	}
	if err := query.Find(&overlaps).Error; err != nil {
		return models.MediaLibrary{}, err
	}
	for _, other := range overlaps {
		if rootsOverlap(relativeRoot, other.RelativeRoot) {
			return models.MediaLibrary{}, appError(CodeMediaLibraryOverlap, "媒体库扫描范围与现有媒体库重叠", nil)
		}
	}
	if input.FullScanIntervalHours == 0 {
		input.FullScanIntervalHours = 24
	}
	if input.IncrementalMinutes == 0 {
		input.IncrementalMinutes = 15
	}
	if input.FullScanIntervalHours < 1 || input.FullScanIntervalHours > 24*30 || input.IncrementalMinutes < 1 || input.IncrementalMinutes > 24*60 {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "扫描周期超出允许范围", nil)
	}
	extensions := append([]string(nil), defaultVideoExtensions...)
	extraAssetExtensions, assetErr := normalizeSourceAssetExtraExtensions(input.STRMAssetExtraExtensions)
	if assetErr != nil {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "自定义伴随文件扩展名无效", assetErr)
	}
	extJSON, _ := json.Marshal(extensions)
	assetExtJSON, _ := json.Marshal(extraAssetExtensions)
	ignoreJSON, _ := json.Marshal(input.IgnorePatterns)
	if input.MetadataLanguage == "" {
		input.MetadataLanguage = "zh-CN"
	}
	if input.MetadataRegion == "" {
		input.MetadataRegion = "CN"
	}
	if input.MatchStrategy == "" {
		input.MatchStrategy = "balanced"
	}
	if input.ProviderRatePerSecond == 0 {
		input.ProviderRatePerSecond = 100
	}
	if input.ProviderConcurrency == 0 {
		input.ProviderConcurrency = 2
	}
	if input.MetadataRatePerSecond == 0 {
		input.MetadataRatePerSecond = 5
	}
	if input.MetadataConcurrency == 0 {
		input.MetadataConcurrency = 1
	}
	if input.ProviderRatePerSecond < 1 || input.ProviderRatePerSecond > 1000 || input.ProviderConcurrency < 1 || input.ProviderConcurrency > 32 || input.MetadataRatePerSecond < 1 || input.MetadataRatePerSecond > 100 || input.MetadataConcurrency < 1 || input.MetadataConcurrency > 16 {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "媒体库限速或并发配置超出允许范围", nil)
	}
	if input.TransferMode == "" {
		input.TransferMode = models.MediaLibraryTransferMove
	}
	switch input.TransferMode {
	case models.MediaLibraryTransferMove, models.MediaLibraryTransferCopy, models.MediaLibraryTransferSymlink:
	default:
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "媒体库转移方式无效", nil)
	}
	if storage.Type == models.StorageTypePan115 && input.TransferMode == models.MediaLibraryTransferSymlink {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "115 网盘媒体库不支持软链接入库", nil)
	}
	if input.ConflictPolicy == "" {
		input.ConflictPolicy = models.MediaLibraryConflictAsk
	}
	switch input.ConflictPolicy {
	case models.MediaLibraryConflictAsk, models.MediaLibraryConflictOverwrite, models.MediaLibraryConflictSkip, models.MediaLibraryConflictRename:
	default:
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "媒体库冲突策略无效", nil)
	}
	input.MovieDirectoryTemplate = organization.MovieDirectoryTemplate
	input.MovieFilenameTemplate = organization.MovieFilenameTemplate
	input.TVDirectoryTemplate = organization.TVDirectoryTemplate
	input.TVFilenameTemplate = organization.TVFilenameTemplate
	if err := validateImportTemplate(input.MovieDirectoryTemplate, true); err != nil {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "电影目录模板无效", err)
	}
	if err := validateImportTemplate(input.TVDirectoryTemplate, true); err != nil {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "剧集目录模板无效", err)
	}
	if err := validateImportTemplate(input.MovieFilenameTemplate, false); err != nil {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "电影文件名模板无效", err)
	}
	if err := validateImportTemplate(input.TVFilenameTemplate, false); err != nil {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "剧集文件名模板无效", err)
	}
	status := models.MediaLibraryStatusDisabled
	if input.Enabled {
		status = models.MediaLibraryStatusInitializing
	}
	return models.MediaLibrary{Name: name, NameNormalized: strings.ToLower(name), StorageID: input.StorageID, ProfileID: input.ProfileID, ProfileRevision: profile.Revision, RelativeRoot: relativeRoot, ProviderRootID: strings.TrimSpace(input.ProviderRootID), Enabled: input.Enabled, Recursive: input.Recursive, FullScanIntervalHours: input.FullScanIntervalHours, IncrementalMinutes: input.IncrementalMinutes, VideoExtensionsJSON: string(extJSON), STRMAssetExtraExtensionsJSON: string(assetExtJSON), IgnorePatternsJSON: string(ignoreJSON), MetadataLanguage: input.MetadataLanguage, MetadataRegion: input.MetadataRegion, MatchStrategy: input.MatchStrategy, ProviderRatePerSecond: input.ProviderRatePerSecond, ProviderConcurrency: input.ProviderConcurrency, MetadataRatePerSecond: input.MetadataRatePerSecond, MetadataConcurrency: input.MetadataConcurrency, STRMEnabled: input.STRMEnabled, STRMLocalRoot: strmLocalRoot, SignedProxyEnabled: input.STRMEnabled, MetadataArtifactsEnabled: metadataArtifactsEnabled, UploadSidecars: input.UploadSidecars, ArtifactStatus: models.MediaArtifactStatusIdle, TransferMode: input.TransferMode, ConflictPolicy: input.ConflictPolicy, MovieDirectoryTemplate: input.MovieDirectoryTemplate, MovieFilenameTemplate: input.MovieFilenameTemplate, TVDirectoryTemplate: input.TVDirectoryTemplate, TVFilenameTemplate: input.TVFilenameTemplate, IngestEnabled: input.IngestEnabled, IngestDownloaderID: optionalString(ingestDownloaderID), IngestOwnerID: ingestOwnerID, IngestProviderRootID: ingestProviderRootID, IngestRelativeRoot: ingestRelativeRoot, Status: status}, nil
}

func normalizeSourceAssetExtraExtensions(values []string) ([]string, error) {
	if len(values) > maxSourceAssetExtraExtensions {
		return nil, errors.New("too many source asset extensions")
	}
	forbidden := make(map[string]struct{}, len(defaultSourceAssetExtensions)+len(defaultVideoExtensions))
	for _, value := range defaultSourceAssetExtensions {
		forbidden[value] = struct{}{}
	}
	for _, value := range defaultVideoExtensions {
		forbidden[strings.TrimPrefix(value, ".")] = struct{}{}
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.ToLower(raw)
		if value == "" || value != raw || len(value) > 10 {
			return nil, errors.New("extension must be 1-10 lowercase ASCII characters")
		}
		for index := 0; index < len(value); index++ {
			character := value[index]
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return nil, errors.New("extension contains unsupported characters")
			}
		}
		if _, blocked := forbidden[value]; blocked {
			return nil, errors.New("extension is reserved")
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func effectiveSourceAssetExtensions(extra []string) []string {
	result := append([]string(nil), defaultSourceAssetExtensions...)
	result = append(result, extra...)
	return result
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func providerDirectoriesOverlap(ctx context.Context, driver cloudpkg.Driver, leftID, rightID string) (bool, error) {
	leftID, rightID = strings.TrimSpace(leftID), strings.TrimSpace(rightID)
	if leftID == "" || rightID == "" {
		return false, errors.New("provider directory identity is incomplete")
	}
	if leftID == rightID {
		return true, nil
	}
	leftWithinRight, err := providerDirectoryWithin(ctx, driver, leftID, rightID)
	if err != nil {
		return false, err
	}
	if leftWithinRight {
		return true, nil
	}
	return providerDirectoryWithin(ctx, driver, rightID, leftID)
}

func validateImportTemplate(value string, directory bool) error {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 512 || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("template is empty or too long")
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "..") || strings.Contains(value, ":/") {
		return errors.New("template escapes the target root")
	}
	if !directory && strings.Contains(value, "/") {
		return errors.New("filename template contains a separator")
	}
	allowed := map[string]struct{}{"category": {}, "title": {}, "year": {}, "version": {}, "season:02": {}, "episode:02": {}, "season": {}, "episode": {}}
	for index := 0; index < len(value); {
		open := strings.IndexByte(value[index:], '{')
		if open < 0 {
			break
		}
		open += index
		closeOffset := strings.IndexByte(value[open+1:], '}')
		if closeOffset < 0 {
			return errors.New("template placeholder is not closed")
		}
		closeIndex := open + 1 + closeOffset
		if _, ok := allowed[value[open+1:closeIndex]]; !ok {
			return errors.New("template placeholder is not allowed")
		}
		index = closeIndex + 1
	}
	return nil
}

func (s *MediaLibraryService) detail(record models.MediaLibrary) (MediaLibraryDetail, error) {
	var storage models.Storage
	var profile models.MediaClassificationProfile
	if err := s.db.First(&storage, record.StorageID).Error; err != nil {
		return MediaLibraryDetail{}, err
	}
	if err := s.db.First(&profile, record.ProfileID).Error; err != nil {
		return MediaLibraryDetail{}, err
	}
	extensions := append([]string(nil), defaultVideoExtensions...)
	var extraAssetExtensions, ignores []string
	_ = json.Unmarshal([]byte(record.STRMAssetExtraExtensionsJSON), &extraAssetExtensions)
	_ = json.Unmarshal([]byte(record.IgnorePatternsJSON), &ignores)
	var count int64
	_ = s.db.Model(&models.MediaLibraryEntry{}).Where("library_id = ?", record.ID).Count(&count).Error
	ingestDownloaderName := ""
	if record.IngestDownloaderID != nil {
		var downloader models.Downloader
		if err := s.db.Select("name").First(&downloader, "id = ?", *record.IngestDownloaderID).Error; err == nil {
			ingestDownloaderName = downloader.Name
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return MediaLibraryDetail{}, err
		}
	}
	return MediaLibraryDetail{MediaLibrary: record, StorageName: storage.Name, ProfileName: profile.Name, VideoExtensions: extensions, STRMAssetDefaultExtensions: append([]string(nil), defaultSourceAssetExtensions...), STRMAssetExtraExtensions: extraAssetExtensions, STRMAssetEffectiveExtensions: effectiveSourceAssetExtensions(extraAssetExtensions), IgnorePatterns: ignores, EntryCount: count, IngestDownloaderName: ingestDownloaderName, STRMLocalPath: record.STRMLocalRoot}, nil
}
func (s *MediaLibraryService) startSupervisor(parent context.Context, id uint) {
	s.stopSupervisor(id)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	s.supervisors[id] = supervisorHandle{cancel: cancel, done: done, wake: wake}
	s.mu.Unlock()
	go func() {
		defer close(done)
		s.supervise(ctx, id)
	}()
}
func (s *MediaLibraryService) stopSupervisor(id uint) {
	s.mu.Lock()
	handle, ok := s.supervisors[id]
	if ok {
		handle.cancel()
		delete(s.supervisors, id)
	}
	s.mu.Unlock()
	if ok {
		<-handle.done
	}
}

func (s *MediaLibraryService) supervise(ctx context.Context, id uint) {
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		var library models.MediaLibrary
		if s.db.First(&library, id).Error != nil || !library.Enabled {
			return
		}
		if library.BaselineGeneration == 0 {
			_ = s.setStatus(id, models.MediaLibraryStatusInitializing, "", nil)
			if _, err := s.reconcile(ctx, id, "initial"); err != nil {
				next := time.Now().UTC().Add(delay)
				_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
				if !waitForRetry(ctx, delay) {
					return
				}
				delay = nextRetryDelay(delay)
				continue
			}
		}
		delay = time.Second
		_ = s.setStatus(id, models.MediaLibraryStatusAttachingListener, "", nil)
		var storage models.Storage
		if s.db.First(&storage, library.StorageID).Error != nil {
			return
		}
		if storage.Type == models.StorageTypePan115 {
			_ = s.setStatus(id, models.MediaLibraryStatusReconciling, "", nil)
			if _, err := s.reconcile(ctx, id, "catch_up"); err != nil {
				next := time.Now().UTC().Add(delay)
				_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
				if !waitForRetry(ctx, delay) {
					return
				}
				delay = nextRetryDelay(delay)
				continue
			}
			_ = s.setStatus(id, models.MediaLibraryStatusListening, "", nil)
			s.listenProvider(ctx, id, s.providerWake(id))
			if ctx.Err() != nil {
				return
			}
			continue
		}
		root, err := s.libraryRoot(id)
		if err != nil {
			next := time.Now().UTC().Add(delay)
			_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
			if !waitForRetry(ctx, delay) {
				return
			}
			delay = nextRetryDelay(delay)
			continue
		}
		watcher, err := newRecursiveWatcher(root)
		if err != nil {
			next := time.Now().UTC().Add(delay)
			_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
			if !waitForRetry(ctx, delay) {
				return
			}
			delay = nextRetryDelay(delay)
			continue
		}
		_ = s.setStatus(id, models.MediaLibraryStatusReconciling, "", nil)
		if _, err := s.reconcile(ctx, id, "catch_up"); err != nil {
			_ = watcher.Close()
			next := time.Now().UTC().Add(delay)
			_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
			if !waitForRetry(ctx, delay) {
				return
			}
			delay = nextRetryDelay(delay)
			continue
		}
		_ = s.setStatus(id, models.MediaLibraryStatusListening, "", nil)
		s.listen(ctx, id, watcher)
		_ = watcher.Close()
		if ctx.Err() != nil {
			return
		}
	}
}

func (s *MediaLibraryService) listenProvider(ctx context.Context, id uint, wake <-chan struct{}) {
	var library models.MediaLibrary
	if s.db.First(&library, id).Error != nil {
		return
	}
	incremental := time.NewTicker(time.Duration(library.IncrementalMinutes) * time.Minute)
	full := time.NewTicker(time.Duration(library.FullScanIntervalHours) * time.Hour)
	defer incremental.Stop()
	defer full.Stop()
	_ = s.sweepIngest(ctx, id)
	var debounce *time.Timer
	var debounceC <-chan time.Time
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			if debounce == nil {
				debounce = time.NewTimer(2 * time.Second)
			} else {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(2 * time.Second)
			}
			debounceC = debounce.C
		case <-debounceC:
			_, _ = s.reconcile(ctx, id, "event")
			_ = s.sweepIngest(ctx, id)
			debounceC = nil
		case <-incremental.C:
			_, _ = s.reconcile(ctx, id, "incremental")
			_ = s.sweepIngest(ctx, id)
		case <-full.C:
			_, _ = s.reconcile(ctx, id, "full")
			_ = s.sweepIngest(ctx, id)
		}
	}
}

const maxMediaLibraryIngestChildren = 5000

// sweepIngest reads the intake root as provider truth. Life events only wake
// this operation; direct children, not event payloads, decide what is adopted.
func (s *MediaLibraryService) sweepIngest(ctx context.Context, libraryID uint) error {
	if s.ingest == nil {
		return nil
	}
	var library models.MediaLibrary
	if err := s.db.WithContext(ctx).Where("id = ? AND enabled = ? AND ingest_enabled = ?", libraryID, true, true).First(&library).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var storage models.Storage
	if err := s.db.WithContext(ctx).First(&storage, library.StorageID).Error; err != nil || !storage.Enabled || storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil || s.connections == nil {
		return appError(CodeMediaLibraryStorageUnavailable, "115 自动摄取 Storage 不可用", err)
	}
	operation := serverlog.OperationPan115ShareIngest
	started := time.Now()
	operation.Event(s.log.Info()).Uint("library_id", library.ID).Uint("connection_id", *storage.ConnectionID).Msg(operation.Message("开始扫描中转目录"))
	_, driver, err := s.connections.driver(*storage.ConnectionID)
	if err != nil {
		operation.Event(s.log.Error()).Uint("library_id", library.ID).Uint("connection_id", *storage.ConnectionID).Str("error_code", CodeMediaLibraryStorageUnavailable).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("扫描中转目录失败"))
		return appError(CodeMediaLibraryStorageUnavailable, "115 自动摄取连接不可用", err)
	}
	root, err := providerItemWithinRoot(ctx, driver, library.IngestProviderRootID, storage.RootPath)
	if err != nil || !root.IsDir {
		operation.Event(s.log.Error()).Uint("library_id", library.ID).Uint("connection_id", *storage.ConnectionID).Str("error_code", CodeMediaLibraryPathInvalid).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("扫描中转目录失败"))
		return appError(CodeMediaLibraryPathInvalid, "115 自动摄取中转目录不可用", err)
	}
	discovered, created, skipped := 0, 0, 0
	var firstErr error
	for offset := int64(0); ; offset += 200 {
		if offset >= maxMediaLibraryIngestChildren {
			firstErr = errors.New("115 intake pagination exceeded its bounded limit")
			break
		}
		page, listErr := driver.List(ctx, root.ID, cloudpkg.PageRequest{Offset: offset, Limit: 200})
		if listErr != nil {
			firstErr = listErr
			break
		}
		for _, item := range page.Items {
			discovered++
			if discovered > maxMediaLibraryIngestChildren {
				firstErr = errors.New("115 intake contains too many direct children")
				break
			}
			if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ParentID) != root.ID {
				firstErr = errors.New("115 intake listing crossed its configured root")
				break
			}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.Name)), "omc-") {
				skipped++
				continue
			}
			wasCreated, adoptErr := s.ingest.AdoptProviderItem(ctx, library.ID, item.ID, item.Name)
			if adoptErr != nil {
				if firstErr == nil {
					firstErr = adoptErr
				}
				continue
			}
			if wasCreated {
				created++
			} else {
				skipped++
			}
		}
		if firstErr != nil || !page.HasMore {
			break
		}
	}
	if firstErr != nil {
		code := ErrorCode(firstErr)
		if code == "INTERNAL_ERROR" {
			code = CodeMediaLibraryScanFailed
		}
		operation.Event(s.log.Error()).Uint("library_id", library.ID).Uint("connection_id", *storage.ConnectionID).Int("discovered", discovered).Int("created", created).Int("skipped", skipped).Str("error_code", code).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("扫描中转目录失败"))
		return firstErr
	}
	operation.Event(s.log.Info()).Uint("library_id", library.ID).Uint("connection_id", *storage.ConnectionID).Int("discovered", discovered).Int("created", created).Int("skipped", skipped).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("中转目录扫描完成"))
	return nil
}

func (s *MediaLibraryService) providerWake(id uint) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if handle, ok := s.supervisors[id]; ok {
		return handle.wake
	}
	closed := make(chan struct{})
	close(closed)
	return closed
}

// ProviderEventsChanged coalesces a connection event batch into one immediate
// reconciliation per affected library. Libraries remain independently watched
// and no persistent queue slot is consumed.
func (s *MediaLibraryService) ProviderEventsChanged(ctx context.Context, connectionID uint, _ []models.ProviderEvent) error {
	var ids []uint
	if err := s.db.WithContext(ctx).Table("media_libraries").Select("media_libraries.id").Joins("JOIN storages ON storages.id = media_libraries.storage_id").Where("media_libraries.enabled = ? AND storages.type = ? AND storages.connection_id = ?", true, models.StorageTypePan115, connectionID).Scan(&ids).Error; err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		if handle, ok := s.supervisors[id]; ok {
			select {
			case handle.wake <- struct{}{}:
			default:
			}
		}
	}
	return nil
}
func (s *MediaLibraryService) listen(ctx context.Context, id uint, watcher *fsnotify.Watcher) {
	var library models.MediaLibrary
	if s.db.First(&library, id).Error != nil {
		return
	}
	incremental := time.NewTicker(time.Duration(library.IncrementalMinutes) * time.Minute)
	full := time.NewTicker(time.Duration(library.FullScanIntervalHours) * time.Hour)
	defer incremental.Stop()
	defer full.Stop()
	var debounce *time.Timer
	var debounceC <-chan time.Time
	pending := map[string]fsnotify.Op{}
	needsReconciliation := false
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := osStatDir(event.Name); err == nil && info {
					_ = addWatchTree(watcher, event.Name)
					needsReconciliation = true
				}
			}
			pending[event.Name] |= event.Op
			if debounce == nil {
				debounce = time.NewTimer(600 * time.Millisecond)
			} else {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(600 * time.Millisecond)
			}
			debounceC = debounce.C
		case <-debounceC:
			if needsReconciliation {
				_, _ = s.reconcile(ctx, id, "event")
			} else {
				_ = s.applyLocalEvents(ctx, id, pending)
			}
			pending = map[string]fsnotify.Op{}
			needsReconciliation = false
			debounceC = nil
		case <-incremental.C:
			_, _ = s.reconcile(ctx, id, "incremental")
		case <-full.C:
			_, _ = s.reconcile(ctx, id, "full")
		case <-watcher.Errors:
			return
		}
	}
}

// applyLocalEvents deliberately routes watcher changes through the same
// provider-neutral reconciliation pipeline as scheduled scans. Cache and
// fingerprints prevent unchanged units from reaching TMDB, while keeping all
// metadata calls outside a database transaction.
func (s *MediaLibraryService) applyLocalEvents(ctx context.Context, id uint, _ map[string]fsnotify.Op) error {
	_, err := s.reconcile(ctx, id, "event")
	return err
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextRetryDelay(delay time.Duration) time.Duration {
	if delay >= 5*time.Minute {
		return 5 * time.Minute
	}
	delay *= 2
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func (s *MediaLibraryService) reconcile(ctx context.Context, id uint, kind string) (models.MediaLibraryScanRun, error) {
	started := time.Now()
	operation := mediaLibraryScanOperation(kind)
	lock := s.scanLock(id)
	lock.Lock()
	defer lock.Unlock()
	var library models.MediaLibrary
	var storage models.Storage
	var profile models.MediaClassificationProfile
	if err := s.db.First(&library, id).Error; err != nil {
		return models.MediaLibraryScanRun{}, mediaLibraryNotFound(err)
	}
	if err := s.db.First(&storage, library.StorageID).Error; err != nil {
		return models.MediaLibraryScanRun{}, err
	}
	if err := s.db.First(&profile, library.ProfileID).Error; err != nil {
		return models.MediaLibraryScanRun{}, err
	}
	extensions := append([]string(nil), defaultVideoExtensions...)
	var extraAssetExtensions, ignores []string
	_ = json.Unmarshal([]byte(library.STRMAssetExtraExtensionsJSON), &extraAssetExtensions)
	_ = json.Unmarshal([]byte(library.IgnorePatternsJSON), &ignores)
	assetExtensions := effectiveSourceAssetExtensions(extraAssetExtensions)
	generation := library.DirtyGeneration + 1
	run := models.MediaLibraryScanRun{LibraryID: id, Kind: kind, Status: "running", Generation: generation, StartedAt: time.Now().UTC()}
	if err := s.db.Create(&run).Error; err != nil {
		return run, err
	}
	operation.Event(s.log.Info()).Uint("library_id", id).Uint("scan_run_id", run.ID).Str("scan_kind", kind).Msg(operation.Message("开始"))
	var result medialibrary.Result
	var scanErr error
	switch storage.Type {
	case models.StorageTypeLocal:
		result, scanErr = medialibrary.ScanLocal(ctx, storage.RootPath, library.RelativeRoot, library.Recursive, extensions, assetExtensions, ignores)
	case models.StorageTypePan115:
		if storage.ConnectionID == nil || s.connections == nil {
			scanErr = errors.New("provider connection is unavailable")
			break
		}
		_, driver, driverErr := s.connections.driver(*storage.ConnectionID)
		if driverErr != nil {
			scanErr = driverErr
			break
		}
		providerRootID := strings.TrimSpace(library.ProviderRootID)
		if providerRootID == "" {
			providerRootID = storage.RootPath
		}
		result, scanErr = medialibrary.ScanProvider(ctx, driver, providerRootID, library.Recursive, extensions, assetExtensions, ignores)
	default:
		scanErr = errors.New("storage provider is unsupported")
	}
	finished := time.Now().UTC()
	if scanErr != nil {
		run.Status = "failed"
		run.ErrorCode = CodeMediaLibraryScanFailed
		run.FinishedAt = &finished
		_ = s.db.Save(&run).Error
		// The provider error can contain a physical path. Persist/log only the
		// stable code and scoped identifiers; callers receive the safe envelope.
		operation.Event(s.log.Error()).Str("error_code", CodeMediaLibraryScanFailed).Uint("library_id", id).Uint("scan_run_id", run.ID).Str("scan_kind", kind).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("失败"))
		return run, appError(CodeMediaLibraryScanFailed, "媒体库扫描失败", scanErr)
	}
	units := medialibrary.GroupRecognitionUnits(result.Files)
	recognitionStarted := time.Now()
	serverlog.OperationMediaRecognition.Event(s.log.Info()).Uint("library_id", id).Uint("scan_run_id", run.ID).Int("unit_count", len(units)).Msg(serverlog.OperationMediaRecognition.Message("开始"))
	recognizedUnits, recognitionErr := s.recognizeLibraryUnits(ctx, library, profile, units)
	if recognitionErr != nil {
		run.Status = "failed"
		run.ErrorCode = CodeMediaLibraryScanFailed
		run.FinishedAt = &finished
		_ = s.db.Save(&run).Error
		serverlog.OperationMediaRecognition.Event(s.log.Error()).Uint("library_id", id).Uint("scan_run_id", run.ID).Str("error_code", CodeMediaLibraryScanFailed).Int64("duration_ms", time.Since(recognitionStarted).Milliseconds()).Msg(serverlog.OperationMediaRecognition.Message("失败"))
		return run, appError(CodeMediaLibraryScanFailed, "媒体识别准备失败", recognitionErr)
	}
	for _, recognized := range recognizedUnits {
		if recognized.Result.Status == mediaRecognitionStatusMatched {
			run.Matched++
		} else {
			run.Unrecognized++
			if recognized.Result.ErrorCode != "" {
				run.RecognitionFailed++
			}
		}
		if recognized.CacheHit {
			run.CacheHits++
		}
	}
	serverlog.OperationMediaRecognition.Event(s.log.Info()).Uint("library_id", id).Uint("scan_run_id", run.ID).Int("unit_count", len(units)).Int("matched", run.Matched).Int("unrecognized", run.Unrecognized).Int("cache_hits", run.CacheHits).Int("recognition_failed", run.RecognitionFailed).Int64("duration_ms", time.Since(recognitionStarted).Milliseconds()).Msg(serverlog.OperationMediaRecognition.Message("完成"))
	run.Discovered = len(result.Files)
	run.Partial = result.Partial
	finished = time.Now().UTC()
	transactionErr := s.db.Transaction(func(tx *gorm.DB) error {
		var currentLibrary models.MediaLibrary
		if err := tx.First(&currentLibrary, id).Error; err != nil {
			return err
		}
		var currentProfile models.MediaClassificationProfile
		if err := tx.First(&currentProfile, currentLibrary.ProfileID).Error; err != nil {
			return err
		}
		if mediaLibrarySourceChanged(library, currentLibrary) || currentLibrary.ProfileID != profile.ID || currentProfile.Revision != profile.Revision || currentLibrary.DirtyGeneration != library.DirtyGeneration {
			return errors.New("media library configuration changed during recognition")
		}
		var existing []models.MediaLibraryEntry
		if err := tx.Where("library_id = ?", id).Find(&existing).Error; err != nil {
			return err
		}
		byPath := map[string]models.MediaLibraryEntry{}
		byProvider := map[string]models.MediaLibraryEntry{}
		for _, entry := range existing {
			byPath[entry.RelativePath] = entry
			if storage.Type != models.StorageTypeLocal && entry.ProviderID != "" {
				byProvider[entry.ProviderID] = entry
			}
		}
		now := time.Now().UTC()
		var existingAssets []models.MediaLibrarySourceAsset
		if err := tx.Where("library_id = ?", id).Find(&existingAssets).Error; err != nil {
			return err
		}
		assetsByPath := make(map[string]models.MediaLibrarySourceAsset, len(existingAssets))
		for _, asset := range existingAssets {
			assetsByPath[asset.RelativePath] = asset
		}
		for _, source := range result.Assets {
			asset, exists := assetsByPath[source.RelativePath]
			if !exists {
				asset = models.MediaLibrarySourceAsset{LibraryID: id, RelativePath: source.RelativePath, CreatedAt: now}
			}
			asset.Generation, asset.ProviderID, asset.ParentProviderID = generation, source.ProviderID, source.ParentProviderID
			asset.Name, asset.Extension, asset.Size = source.Name, source.Extension, source.Size
			asset.ModifiedAt, asset.HashHint, asset.Active, asset.UpdatedAt = source.ModifiedAt, source.HashHint, true, now
			if err := tx.Save(&asset).Error; err != nil {
				return err
			}
			delete(assetsByPath, source.RelativePath)
		}
		type recognitionProjection struct {
			ID        uint
			SourceKey string
			Result    MediaRecognitionResult
		}
		byFile := make(map[string]recognitionProjection, len(result.Files))
		seenRecognitionIDs := make([]uint, 0, len(recognizedUnits))
		for _, recognized := range recognizedUnits {
			var record models.MediaLibraryRecognition
			findErr := tx.Where("library_id = ? AND source_key = ?", id, recognized.Unit.SourceKey).First(&record).Error
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				record = models.MediaLibraryRecognition{LibraryID: id, SourceKey: recognized.Unit.SourceKey, CreatedAt: now}
			} else if findErr != nil {
				return findErr
			}
			metadataJSON, marshalErr := marshalRecognitionMetadata(recognized.Result)
			if marshalErr != nil {
				return marshalErr
			}
			record.InputFingerprint = recognized.Unit.InputFingerprint
			record.ProfileID, record.ProfileRevision = profile.ID, profile.Revision
			record.Status, record.ErrorCode = recognized.Result.Status, recognized.Result.ErrorCode
			record.MediaType, record.Title = recognized.Result.MediaType, recognized.Result.Title
			record.ReleaseYear, record.TMDBID, record.Confidence = cloneInt(recognized.Result.ReleaseYear), cloneInt64(recognized.Result.TMDBID), cloneFloat64(recognized.Result.Confidence)
			record.CategoryName, record.MatchedRuleID = recognized.Result.CategoryName, recognized.Result.MatchedRuleID
			record.MetadataJSON, record.ManualOverride = metadataJSON, recognized.Manual
			record.LastGeneration, record.UpdatedAt = generation, now
			if err := tx.Save(&record).Error; err != nil {
				return err
			}
			seenRecognitionIDs = append(seenRecognitionIDs, record.ID)
			projection := recognitionProjection{ID: record.ID, SourceKey: record.SourceKey, Result: recognized.Result}
			for _, file := range recognized.Unit.Files {
				byFile[file.RelativePath] = projection
			}
		}
		for _, file := range result.Files {
			entry, exists := byPath[file.RelativePath]
			oldPath := file.RelativePath
			if !exists && storage.Type != models.StorageTypeLocal {
				if providerEntry, providerExists := byProvider[file.ProviderID]; providerExists {
					entry, exists, oldPath = providerEntry, true, providerEntry.RelativePath
				}
			}
			projection, hasRecognition := byFile[file.RelativePath]
			parsed := medialibrary.ParseMedia(filepath.Base(file.RelativePath), file.RelativePath)
			if !exists {
				run.Added++
				entry = models.MediaLibraryEntry{LibraryID: id, RelativePath: file.RelativePath, CreatedAt: now}
			} else if oldPath != file.RelativePath || entry.Size != file.Size || !entry.ModifiedAt.Equal(file.ModifiedAt) {
				run.Updated++
			}
			entry.RelativePath = file.RelativePath
			entry.ProviderID = file.ProviderID
			if hasRecognition {
				entry.RecognitionID = &projection.ID
			} else {
				entry.RecognitionID = nil
			}
			entry.Size = file.Size
			entry.ModifiedAt = file.ModifiedAt
			entry.MediaType, entry.Title = parsed.MediaType, parsed.Title
			entry.SeriesTitle, entry.Season, entry.Episode = parsed.SeriesTitle, parsed.Season, parsed.Episode
			entry.MatchStatus, entry.RecognitionErrorCode = mediaRecognitionStatusUnrecognized, tmdb.ErrorNoMatch
			entry.WorkKey = "file:" + projection.SourceKey
			entry.CategoryName, entry.MatchedRuleID = "", nil
			entry.TMDBID, entry.ReleaseYear, entry.MatchConfidence = nil, nil, nil
			if hasRecognition {
				recognized := projection.Result
				if recognized.MediaType != "" {
					entry.MediaType = recognized.MediaType
				}
				if recognized.Title != "" {
					entry.Title = recognized.Title
				}
				if entry.MediaType == "tv" {
					entry.SeriesTitle = entry.Title
				}
				if recognized.SeasonHint != nil {
					entry.Season = cloneInt(recognized.SeasonHint)
				}
				if recognized.EpisodeHint != nil {
					entry.Episode = cloneInt(recognized.EpisodeHint)
				}
				entry.MatchStatus, entry.RecognitionErrorCode = recognized.Status, recognized.ErrorCode
				entry.WorkKey = recognitionWorkKey(recognized, projection.SourceKey)
				entry.CategoryName, entry.MatchedRuleID = recognized.CategoryName, recognized.MatchedRuleID
				entry.TMDBID, entry.ReleaseYear, entry.MatchConfidence = cloneInt64(recognized.TMDBID), cloneInt(recognized.ReleaseYear), cloneFloat64(recognized.Confidence)
			}
			entry.LastGeneration = generation
			entry.UpdatedAt = now
			if err := tx.Save(&entry).Error; err != nil {
				return err
			}
			delete(byPath, oldPath)
			delete(byPath, file.RelativePath)
			delete(byProvider, file.ProviderID)
		}
		// A bounded partial enumeration is not proof of deletion. Preserve
		// unseen entries until a complete reconciliation can confirm absence.
		if !result.Partial {
			for _, entry := range byPath {
				run.Removed++
				if err := tx.Delete(&entry).Error; err != nil {
					return err
				}
			}
			deleteQuery := tx.Where("library_id = ?", id)
			if len(seenRecognitionIDs) > 0 {
				deleteQuery = deleteQuery.Where("id NOT IN ?", seenRecognitionIDs)
			}
			if err := deleteQuery.Delete(&models.MediaLibraryRecognition{}).Error; err != nil {
				return err
			}
			for _, asset := range assetsByPath {
				if err := tx.Delete(&asset).Error; err != nil {
					return err
				}
			}
		}
		updates := map[string]any{"dirty_generation": generation, "baseline_generation": generation, "last_scan_at": finished, "last_successful_scan_at": finished, "profile_revision": profile.Revision, "reclassification_due": false, "status_error_code": "", "next_retry_at": nil}
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		run.Status = "success"
		run.FinishedAt = &finished
		return tx.Save(&run).Error
	})
	if transactionErr != nil {
		finished := time.Now().UTC()
		run.Status = "failed"
		run.ErrorCode = CodeMediaLibraryScanFailed
		run.FinishedAt = &finished
		_ = s.db.Save(&run).Error
		operation.Event(s.log.Error()).Str("error_code", CodeMediaLibraryScanFailed).Uint("library_id", id).Uint("scan_run_id", run.ID).Str("scan_kind", kind).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("结果入库失败"))
		return run, transactionErr
	}
	operation.Event(s.log.Info()).Uint("library_id", id).Uint("scan_run_id", run.ID).Str("scan_kind", kind).Int("discovered", run.Discovered).Int("added", run.Added).Int("updated", run.Updated).Int("removed", run.Removed).Bool("partial", run.Partial).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("完成"))
	matched, snapshots, cacheHits := 0, 0, 0
	for _, recognized := range recognizedUnits {
		if recognized.Result.Status == mediaRecognitionStatusMatched {
			matched++
		}
		if recognized.Result.Snapshot.TMDBID > 0 {
			snapshots++
		}
		if recognized.CacheHit {
			cacheHits++
		}
	}
	serverlog.OperationMetadataSnapshot.Event(s.log.Info()).Uint("library_id", id).Uint("scan_run_id", run.ID).Uint64("generation", generation).Int("units", len(recognizedUnits)).Int("matched", matched).Int("snapshots", snapshots).Int("cache_hits", cacheHits).Msg(serverlog.OperationMetadataSnapshot.Message("提交"))
	if s.artifacts != nil {
		if err := s.artifacts.ScheduleGeneration(id, generation); err != nil {
			serverlog.OperationMediaArtifact.Event(s.log.Error()).Uint("library_id", id).Uint64("generation", generation).Str("error_code", "artifact_schedule_failed").Msg(serverlog.OperationMediaArtifact.Message("入队失败"))
		}
	}
	return run, nil
}

func mediaLibraryScanOperation(kind string) serverlog.Operation {
	switch kind {
	case "initial":
		return serverlog.OperationLibraryInitialScan
	case "full":
		return serverlog.OperationLibraryFullScan
	case "event":
		return serverlog.OperationLibraryEventScan
	default:
		return serverlog.OperationLibraryIncrementalScan
	}
}

func (s *MediaLibraryService) scanLock(id uint) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.scanLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.scanLocks[id] = lock
	}
	return lock
}

func (s *MediaLibraryService) setStatus(id uint, status, code string, next *time.Time) error {
	return s.db.Model(&models.MediaLibrary{}).Where("id = ?", id).Updates(map[string]any{"status": status, "status_error_code": code, "next_retry_at": next}).Error
}
func (s *MediaLibraryService) libraryRoot(id uint) (string, error) {
	var library models.MediaLibrary
	var storage models.Storage
	if err := s.db.First(&library, id).Error; err != nil {
		return "", err
	}
	if err := s.db.First(&storage, library.StorageID).Error; err != nil {
		return "", err
	}
	return medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)
}
func rootsOverlap(a, b string) bool {
	a = strings.TrimSuffix(a, "/")
	b = strings.TrimSuffix(b, "/")
	if a == "" {
		a = "/"
	}
	if b == "" {
		b = "/"
	}
	return a == b || a == "/" || b == "/" || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func mediaLibraryNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "媒体库不存在", err)
	}
	return err
}
func mediaLibraryConstraint(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique") && strings.Contains(err.Error(), "name_normalized") {
		return appError(CodeMediaLibraryNameConflict, "媒体库名称已存在", err)
	}
	return err
}

func newRecursiveWatcher(root string) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := addWatchTree(watcher, root); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	return watcher, nil
}
func addWatchTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && !medialibrary.IsUnsafeDirectory(path, entry) {
			return watcher.Add(path)
		}
		return nil
	})
}
func osStatDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}
