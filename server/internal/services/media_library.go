package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	db                *gorm.DB
	audit             *AuditService
	log               zerolog.Logger
	mu                sync.Mutex
	supervisors       map[uint]supervisorHandle
	scanLocks         map[uint]*sync.Mutex
	connections       *ConnectionService
	metadata          *MetadataSettingsService
	aiRecognition     *AIRecognitionSettingsService
	ingest            MediaLibraryIngestEnqueuer
	artifacts         *MediaArtifactService
	changes           *MediaChangeService
	closed            bool
	lifeEventCtx      context.Context
	lifeEventStop     context.CancelFunc
	lifeEventDone     <-chan struct{}
	lifeEventWG       sync.WaitGroup
	lifeEventRechecks map[uint]struct{}
	lifeEventMu       sync.Mutex
	lifeEvents        map[string]downloaderLifeEventCandidate
	backends          *MediaLibraryBackendRegistry
}

// MediaLibraryIngestEnqueuer is the narrow boundary from provider directory
// reconciliation into the existing durable download pipeline.
type MediaLibraryIngestEnqueuer interface {
	AdoptProviderItem(context.Context, uint, string, string) (bool, error)
}

type downloaderLifeEventIngestEnqueuer interface {
	AdoptDownloaderProviderItem(context.Context, string, uint, string, string) (bool, error)
}

type supervisorHandle struct {
	cancel context.CancelFunc
	done   <-chan struct{}
	wake   chan struct{}
}

type downloaderLifeEventCandidate struct {
	Snapshot  [sha256.Size]byte
	FirstSeen time.Time
	Claimed   bool
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
	ConnectionID                 *uint    `json:"connection_id,omitempty"`
	AutoListenDefault            bool     `json:"auto_listen_default"`
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
	service := &MediaLibraryService{db: db, audit: audit, log: log, supervisors: map[uint]supervisorHandle{}, scanLocks: map[uint]*sync.Mutex{}, lifeEventRechecks: map[uint]struct{}{}, lifeEvents: map[string]downloaderLifeEventCandidate{}}
	service.backends = NewMediaLibraryBackendRegistry(
		localMediaLibraryBackend{},
		pan115MediaLibraryBackend{driver: func(connectionID uint) (cloudpkg.Driver, error) {
			if service.connections == nil {
				return nil, errors.New("provider connection is unavailable")
			}
			_, driver, err := service.connections.driver(connectionID)
			return driver, err
		}},
	)
	return service
}
func (s *MediaLibraryService) SetConnectionService(connections *ConnectionService) {
	s.connections = connections
}
func (s *MediaLibraryService) SetMetadataSettingsService(metadata *MetadataSettingsService) {
	s.metadata = metadata
}
func (s *MediaLibraryService) SetAIRecognitionSettings(settings *AIRecognitionSettingsService) {
	s.aiRecognition = settings
}
func (s *MediaLibraryService) SetIngestEnqueuer(ingest MediaLibraryIngestEnqueuer) {
	s.ingest = ingest
}
func (s *MediaLibraryService) SetArtifactService(artifacts *MediaArtifactService) {
	s.artifacts = artifacts
}
func (s *MediaLibraryService) SetMediaChangeService(changes *MediaChangeService) {
	s.changes = changes
}
func (s *MediaLibraryService) Start(ctx context.Context) error {
	var libraries []models.MediaLibrary
	if err := s.db.Where("enabled = ?", true).Find(&libraries).Error; err != nil {
		return err
	}
	for _, library := range libraries {
		s.startSupervisor(ctx, library.ID)
	}
	s.startDownloaderLifeEventSupervisor(ctx)
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
	lifeEventStop, lifeEventDone := s.lifeEventStop, s.lifeEventDone
	if lifeEventStop != nil {
		lifeEventStop()
	}
	s.lifeEventStop, s.lifeEventDone, s.lifeEventCtx = nil, nil, nil
	s.mu.Unlock()
	for _, handle := range handles {
		<-handle.done
	}
	if lifeEventDone != nil {
		<-lifeEventDone
	}
	s.lifeEventWG.Wait()
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
	// The MediaLibrary-owned intake directory is a legacy, read-only fact.
	// New callers (including forged old WebUI payloads) cannot create a second
	// intake route beside the Downloader-owned life-event directory.
	clearLegacyMediaLibraryIngestInput(&input)
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
		return MediaLibraryDetail{}, mediaLibraryConstraint(transactionErr)
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
	// Legacy intake configuration belongs to already-running compatibility
	// routes. Ignore request fields and copy the persisted snapshot back after
	// validating the current MediaLibrary fields, so an unrelated edit neither
	// creates nor silently disables legacy intake work.
	clearLegacyMediaLibraryIngestInput(&input)
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
	record.IngestEnabled = existing.IngestEnabled
	record.IngestDownloaderID = cloneOptionalString(existing.IngestDownloaderID)
	record.IngestOwnerID = cloneOptionalUint(existing.IngestOwnerID)
	record.IngestProviderRootID = existing.IngestProviderRootID
	record.IngestRelativeRoot = existing.IngestRelativeRoot
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
	var replacementStorage models.Storage
	if err := s.db.Select("type", "connection_id").First(&replacementStorage, record.StorageID).Error; err != nil {
		return MediaLibraryDetail{}, appError(CodeMediaLibraryStorageUnavailable, "来源 Storage 不可用", err)
	}
	preservesDefault := func(connectionID uint) bool {
		return record.Enabled && replacementStorage.Type == models.StorageTypePan115 && replacementStorage.ConnectionID != nil && *replacementStorage.ConnectionID == connectionID
	}
	// Reject the expected destructive edit before stopping the active supervisor.
	// The same guard is repeated in the write transaction to close the race with
	// listener/default changes made after this read.
	if existing.DefaultIngestConnectionID != nil && !preservesDefault(*existing.DefaultIngestConnectionID) {
		if err := requireNoEnabledLifeEventListener(ctx, s.db, *existing.DefaultIngestConnectionID); err != nil {
			return MediaLibraryDetail{}, err
		}
	}
	s.stopSupervisor(id)
	lock := s.scanLock(id)
	lock.Lock()
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var current models.MediaLibrary
		if err := tx.Select("default_ingest_connection_id").First(&current, id).Error; err != nil {
			return mediaLibraryNotFound(err)
		}
		record.DefaultIngestConnectionID = current.DefaultIngestConnectionID
		if current.DefaultIngestConnectionID != nil && !preservesDefault(*current.DefaultIngestConnectionID) {
			if err := requireNoEnabledLifeEventListener(ctx, tx, *current.DefaultIngestConnectionID); err != nil {
				return err
			}
			record.DefaultIngestConnectionID = nil
		}
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
		// ContentRevision is the monotonic cursor for the durable media-change
		// outbox. It is not an editable field and may advance while input
		// validation is running, so omit it instead of writing the stale snapshot
		// loaded at the beginning of Update.
		if err := tx.Omit("content_revision").Save(&record).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", id).Select("content_revision").Scan(&record.ContentRevision).Error; err != nil {
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

func clearLegacyMediaLibraryIngestInput(input *MediaLibraryInput) {
	if input == nil {
		return
	}
	input.IngestEnabled = false
	input.IngestDownloaderID = ""
	input.IngestProviderRootID = ""
	input.IngestRelativeRoot = ""
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneOptionalUint(value *uint) *uint {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
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
	var existing models.MediaLibrary
	if err := s.db.First(&existing, id).Error; err != nil {
		return mediaLibraryNotFound(err)
	}
	if existing.DefaultIngestConnectionID != nil {
		if err := requireNoEnabledLifeEventListener(context.Background(), s.db, *existing.DefaultIngestConnectionID); err != nil {
			return err
		}
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
		if record.DefaultIngestConnectionID != nil {
			if err := requireNoEnabledLifeEventListener(context.Background(), tx, *record.DefaultIngestConnectionID); err != nil {
				return err
			}
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
	} else if existing.Enabled {
		s.startSupervisor(context.Background(), id)
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
	validateProviderOverlap := input.Enabled && storage.Type == models.StorageTypePan115
	if validateProviderOverlap && id != 0 {
		var current models.MediaLibrary
		if err := s.db.Select("storage_id", "provider_root_id", "enabled").First(&current, id).Error; err != nil {
			return models.MediaLibrary{}, mediaLibraryNotFound(err)
		}
		// The directory identity and listener-overlap proof were already made
		// authoritative when this enabled configuration was saved. Ordinary
		// policy edits must not repeat an ancestry walk against 115.
		validateProviderOverlap = !current.Enabled || current.StorageID != storage.ID || strings.TrimSpace(current.ProviderRootID) != strings.TrimSpace(input.ProviderRootID)
	}
	if validateProviderOverlap {
		if err := s.validateMediaLibraryLifeEventOverlap(ctx, storage, strings.TrimSpace(input.ProviderRootID)); err != nil {
			return models.MediaLibrary{}, err
		}
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

func (s *MediaLibraryService) validateMediaLibraryLifeEventOverlap(ctx context.Context, storage models.Storage, finalRootID string) error {
	if storage.ConnectionID == nil || s.connections == nil || finalRootID == "" {
		return appError(CodeMediaLibraryStorageUnavailable, "115 媒体库目录不可用", nil)
	}
	var listenerRoots []string
	if err := s.db.WithContext(ctx).Table("downloaders").
		Select("downloaders.provider_directory_id").
		Joins("JOIN storages ON storages.id = downloaders.storage_id").
		Where("downloaders.enabled = ? AND downloaders.auto_listen_life_events = ? AND downloaders.type = ? AND storages.enabled = ? AND storages.type = ? AND storages.connection_id = ?", true, true, models.DownloaderTypePan115Offline, true, models.StorageTypePan115, *storage.ConnectionID).
		Scan(&listenerRoots).Error; err != nil {
		return err
	}
	if len(listenerRoots) == 0 {
		return nil
	}
	_, driver, err := s.connections.driver(*storage.ConnectionID)
	if err != nil {
		return appError(CodeMediaLibraryStorageUnavailable, "115 连接不可用", err)
	}
	finalRoot, err := providerItemWithinRoot(ctx, driver, finalRootID, strings.TrimSpace(storage.RootPath))
	if err != nil || !finalRoot.IsDir {
		return appError(CodeMediaLibraryPathInvalid, "115 媒体库目录不可用", err)
	}
	for _, listenerRootID := range listenerRoots {
		listenerRootID = strings.TrimSpace(listenerRootID)
		if listenerRootID == "" {
			return appError(CodeDownloaderStorageUnavailable, "现有 115 自动监听目录边界不完整", nil)
		}
		overlaps, overlapErr := providerDirectoriesOverlap(ctx, driver, finalRoot.ID, listenerRootID)
		if overlapErr != nil {
			return appError(CodeMediaLibraryStorageUnavailable, "无法验证现有 115 自动监听目录边界", overlapErr)
		}
		if overlaps {
			return appError(CodeMediaLibraryOverlap, "115 媒体库目录不能与自动监听目录重叠", nil)
		}
	}
	return nil
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
	autoListenDefault := record.DefaultIngestConnectionID != nil && storage.ConnectionID != nil && *record.DefaultIngestConnectionID == *storage.ConnectionID
	return MediaLibraryDetail{MediaLibrary: record, StorageName: storage.Name, ConnectionID: storage.ConnectionID, AutoListenDefault: autoListenDefault, ProfileName: profile.Name, VideoExtensions: extensions, STRMAssetDefaultExtensions: append([]string(nil), defaultSourceAssetExtensions...), STRMAssetExtraExtensions: extraAssetExtensions, STRMAssetEffectiveExtensions: effectiveSourceAssetExtensions(extraAssetExtensions), IgnorePatterns: ignores, EntryCount: count, IngestDownloaderName: ingestDownloaderName, STRMLocalPath: record.STRMLocalRoot}, nil
}
func (s *MediaLibraryService) startSupervisor(parent context.Context, id uint) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	handle := supervisorHandle{cancel: cancel, done: done, wake: wake}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		close(done)
		return
	}
	previous, replaced := s.supervisors[id]
	s.supervisors[id] = handle
	s.mu.Unlock()
	go func() {
		defer close(done)
		if replaced {
			previous.cancel()
			<-previous.done
		}
		if ctx.Err() != nil {
			return
		}
		s.supervise(ctx, id, wake)
	}()
}
func (s *MediaLibraryService) stopSupervisor(id uint) {
	s.mu.Lock()
	handle, ok := s.supervisors[id]
	if ok {
		handle.cancel()
	}
	s.mu.Unlock()
	if ok {
		<-handle.done
		// Keep the canceled handle published until it has actually exited. A
		// concurrent start then chains behind this handle instead of observing an
		// empty registry slot and briefly running two listeners for one library.
		s.mu.Lock()
		if current, exists := s.supervisors[id]; exists && current.done == handle.done {
			delete(s.supervisors, id)
		}
		s.mu.Unlock()
	}
}

func (s *MediaLibraryService) supervise(ctx context.Context, id uint, wake <-chan struct{}) {
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
		backend, err := s.backends.Get(storage.Type)
		if err != nil {
			next := time.Now().UTC().Add(delay)
			_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
			if !waitForRetry(ctx, delay) {
				return
			}
			delay = nextRetryDelay(delay)
			continue
		}
		listener, err := backend.OpenListener(ctx, library, storage, wake)
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
			_ = listener.Close()
			next := time.Now().UTC().Add(delay)
			_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
			if !waitForRetry(ctx, delay) {
				return
			}
			delay = nextRetryDelay(delay)
			continue
		}
		_ = s.sweepIngest(ctx, id)
		_ = s.setStatus(id, models.MediaLibraryStatusListening, "", nil)
		_ = listener.Run(ctx, func(reconcileCtx context.Context, reason string) error {
			_, reconcileErr := s.reconcile(reconcileCtx, id, reason)
			if reconcileErr == nil {
				_ = s.sweepIngest(reconcileCtx, id)
			}
			return reconcileErr
		})
		_ = listener.Close()
		if ctx.Err() != nil {
			return
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

// ProviderEventsChanged coalesces a connection event batch into one immediate
// reconciliation per affected library. Libraries remain independently watched
// and no persistent queue slot is consumed.
func (s *MediaLibraryService) ProviderEventsChanged(ctx context.Context, connectionID uint, _ []models.ProviderEvent) error {
	var ids []uint
	if err := s.db.WithContext(ctx).Table("media_libraries").Select("media_libraries.id").Joins("JOIN storages ON storages.id = media_libraries.storage_id").Where("media_libraries.enabled = ? AND storages.type = ? AND storages.connection_id = ?", true, models.StorageTypePan115, connectionID).Scan(&ids).Error; err != nil {
		return err
	}
	s.mu.Lock()
	for _, id := range ids {
		if handle, ok := s.supervisors[id]; ok {
			select {
			case handle.wake <- struct{}{}:
			default:
			}
		}
	}
	s.mu.Unlock()
	err := s.sweepDownloaderLifeEvents(ctx, connectionID)
	if err == nil {
		// The first event normally arrives while 115 is still changing the
		// provider item. Recheck once after the stability window even when no
		// second life event is emitted.
		s.scheduleDownloaderLifeEventRecheck(connectionID)
	}
	return err
}

// scheduleDownloaderLifeEventRecheck coalesces event storms per Connection.
// One provider event batch may contain many entries for the same transfer; a
// bounded delayed sweep is sufficient to obtain the second stable snapshot.
func (s *MediaLibraryService) scheduleDownloaderLifeEventRecheck(connectionID uint) {
	s.mu.Lock()
	supervisorCtx := s.lifeEventCtx
	if s.closed || supervisorCtx == nil {
		s.mu.Unlock()
		return
	}
	if _, scheduled := s.lifeEventRechecks[connectionID]; scheduled {
		s.mu.Unlock()
		return
	}
	s.lifeEventRechecks[connectionID] = struct{}{}
	s.lifeEventWG.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.lifeEventWG.Done()
		defer func() {
			s.mu.Lock()
			delete(s.lifeEventRechecks, connectionID)
			s.mu.Unlock()
		}()
		timer := time.NewTimer(downloaderLifeEventStableWindow + time.Second)
		defer timer.Stop()
		select {
		case <-supervisorCtx.Done():
			return
		case <-timer.C:
			_ = s.sweepDownloaderLifeEvents(supervisorCtx, connectionID)
		}
	}()
}

const (
	downloaderLifeEventStableWindow  = 30 * time.Second
	downloaderLifeEventSweepInterval = 5 * time.Minute
)

func (s *MediaLibraryService) startDownloaderLifeEventSupervisor(parent context.Context) {
	s.mu.Lock()
	if s.closed || s.lifeEventStop != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.lifeEventCtx, s.lifeEventStop, s.lifeEventDone = ctx, cancel, done
	s.mu.Unlock()
	go func() {
		defer close(done)
		_ = s.sweepAllDownloaderLifeEvents(ctx, time.Now().UTC())
		ticker := time.NewTicker(downloaderLifeEventSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				_ = s.sweepAllDownloaderLifeEvents(ctx, now.UTC())
			}
		}
	}()
}

// sweepAllDownloaderLifeEvents is the bounded periodic compensation path for
// missing provider events. It discovers only Connections that currently own
// an enabled 115 listener and lets the same authoritative sweep handle each.
func (s *MediaLibraryService) sweepAllDownloaderLifeEvents(ctx context.Context, now time.Time) error {
	var connectionIDs []uint
	if err := s.db.WithContext(ctx).Table("downloaders").
		Distinct("storages.connection_id").
		Joins("JOIN storages ON storages.id = downloaders.storage_id").
		Where("downloaders.enabled = ? AND downloaders.auto_listen_life_events = ? AND downloaders.type = ? AND storages.enabled = ? AND storages.type = ? AND storages.connection_id IS NOT NULL", true, true, models.DownloaderTypePan115Offline, true, models.StorageTypePan115).
		Order("storages.connection_id").
		Pluck("storages.connection_id", &connectionIDs).Error; err != nil {
		return err
	}
	var firstErr error
	for _, connectionID := range connectionIDs {
		if err := s.sweepDownloaderLifeEventsAt(ctx, connectionID, now); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// sweepDownloaderLifeEvents treats each enabled 115 downloader directory as
// the only manual-ingest boundary. OMC-owned task directories are excluded so
// the durable Download Worker and the life-event listener cannot claim the
// same provider content.
func (s *MediaLibraryService) sweepDownloaderLifeEvents(ctx context.Context, connectionID uint) error {
	return s.sweepDownloaderLifeEventsAt(ctx, connectionID, time.Now().UTC())
}

func (s *MediaLibraryService) sweepDownloaderLifeEventsAt(ctx context.Context, connectionID uint, now time.Time) error {
	ingest, ok := s.ingest.(downloaderLifeEventIngestEnqueuer)
	if !ok || s.connections == nil || connectionID == 0 {
		return nil
	}
	var downloaders []models.Downloader
	if err := s.db.WithContext(ctx).Table("downloaders").Select("downloaders.*").Joins("JOIN storages ON storages.id = downloaders.storage_id").Where("downloaders.enabled = ? AND downloaders.auto_listen_life_events = ? AND downloaders.type = ? AND storages.enabled = ? AND storages.type = ? AND storages.connection_id = ?", true, true, models.DownloaderTypePan115Offline, true, models.StorageTypePan115, connectionID).Order("downloaders.created_at,downloaders.id").Scan(&downloaders).Error; err != nil {
		return err
	}
	if len(downloaders) == 0 {
		return nil
	}
	_, driver, err := s.connections.driver(connectionID)
	if err != nil {
		return err
	}
	var library models.MediaLibrary
	if err := s.db.WithContext(ctx).Table("media_libraries").Select("media_libraries.*").Joins("JOIN storages ON storages.id = media_libraries.storage_id").Where("media_libraries.enabled = ? AND storages.enabled = ? AND storages.type = ? AND storages.connection_id = ? AND media_libraries.transfer_mode IN ?", true, true, models.StorageTypePan115, connectionID, []string{models.MediaLibraryTransferMove, models.MediaLibraryTransferCopy}).Order("media_libraries.sort_order,media_libraries.id").First(&library).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var firstErr error
	for _, downloader := range downloaders {
		rootID := strings.TrimSpace(downloader.ProviderDirectoryID)
		if rootID == "" {
			continue
		}
		seen := make(map[string]struct{})
		complete := false
		for offset := int64(0); offset < maxMediaLibraryIngestChildren; offset += 200 {
			page, listErr := driver.List(ctx, rootID, cloudpkg.PageRequest{Offset: offset, Limit: 200})
			if listErr != nil {
				if firstErr == nil {
					firstErr = listErr
				}
				break
			}
			for _, item := range page.Items {
				name := strings.TrimSpace(item.Name)
				itemID := strings.TrimSpace(item.ID)
				if itemID == "" || strings.TrimSpace(item.ParentID) != rootID {
					continue
				}
				candidateKey := downloaderLifeEventCandidateKey(connectionID, downloader.ID, itemID)
				seen[candidateKey] = struct{}{}
				if strings.HasPrefix(strings.ToLower(name), "omc-") {
					if s.rememberReservedDownloaderLifeEventCandidate(candidateKey) {
						serverlog.OperationPan115ShareIngest.Event(s.log.Warn()).Uint("connection_id", connectionID).Str("downloader_id", downloader.ID).Str("error_code", "reserved_prefix_skipped").Msg(serverlog.OperationPan115ShareIngest.Message("跳过 OMC 保留目录"))
					}
					continue
				}
				snapshot, snapshotErr := snapshotDownloaderLifeEventItem(ctx, driver, item)
				if snapshotErr != nil {
					if firstErr == nil {
						firstErr = snapshotErr
					}
					continue
				}
				if !s.downloaderLifeEventCandidateReady(candidateKey, snapshot, item.ModifiedAt, now) {
					continue
				}
				if _, adoptErr := ingest.AdoptDownloaderProviderItem(ctx, downloader.ID, library.ID, itemID, name); adoptErr != nil && firstErr == nil {
					firstErr = adoptErr
				} else if adoptErr == nil {
					s.markDownloaderLifeEventCandidateClaimed(candidateKey)
				}
			}
			if !page.HasMore {
				complete = true
				break
			}
		}
		if complete {
			s.pruneDownloaderLifeEventCandidates(connectionID, downloader.ID, seen)
		} else if firstErr == nil {
			firstErr = errors.New("115 downloader directory exceeds the bounded life-event scan")
		}
	}
	return firstErr
}

func downloaderLifeEventCandidateKey(connectionID uint, downloaderID, itemID string) string {
	return strconv.FormatUint(uint64(connectionID), 10) + "\x00" + downloaderID + "\x00" + itemID
}

func downloaderLifeEventCandidatePrefix(connectionID uint, downloaderID string) string {
	return strconv.FormatUint(uint64(connectionID), 10) + "\x00" + downloaderID + "\x00"
}

func (s *MediaLibraryService) downloaderLifeEventCandidateReady(key string, snapshot [sha256.Size]byte, modifiedAt, now time.Time) bool {
	s.lifeEventMu.Lock()
	defer s.lifeEventMu.Unlock()
	candidate, exists := s.lifeEvents[key]
	if exists && candidate.Claimed {
		return false
	}
	if !exists || candidate.Snapshot != snapshot {
		s.lifeEvents[key] = downloaderLifeEventCandidate{Snapshot: snapshot, FirstSeen: now}
		return false
	}
	if now.Sub(candidate.FirstSeen) < downloaderLifeEventStableWindow {
		return false
	}
	return modifiedAt.IsZero() || now.Sub(modifiedAt) >= downloaderLifeEventStableWindow
}

func (s *MediaLibraryService) markDownloaderLifeEventCandidateClaimed(key string) {
	s.lifeEventMu.Lock()
	candidate, exists := s.lifeEvents[key]
	if exists {
		candidate.Claimed = true
		s.lifeEvents[key] = candidate
	}
	s.lifeEventMu.Unlock()
}

func (s *MediaLibraryService) rememberReservedDownloaderLifeEventCandidate(key string) bool {
	s.lifeEventMu.Lock()
	defer s.lifeEventMu.Unlock()
	if _, exists := s.lifeEvents[key]; exists {
		return false
	}
	s.lifeEvents[key] = downloaderLifeEventCandidate{Claimed: true}
	return true
}

func (s *MediaLibraryService) pruneDownloaderLifeEventCandidates(connectionID uint, downloaderID string, seen map[string]struct{}) {
	prefix := downloaderLifeEventCandidatePrefix(connectionID, downloaderID)
	s.lifeEventMu.Lock()
	for key := range s.lifeEvents {
		if strings.HasPrefix(key, prefix) {
			if _, exists := seen[key]; !exists {
				delete(s.lifeEvents, key)
			}
		}
	}
	s.lifeEventMu.Unlock()
}

// snapshotDownloaderLifeEventItem builds a bounded recursive provider
// manifest. Stable top-level metadata alone is insufficient for a directory:
// a 115 transfer may still be adding nested files without changing its name.
func snapshotDownloaderLifeEventItem(ctx context.Context, driver cloudpkg.Driver, root cloudpkg.Item) ([sha256.Size]byte, error) {
	facts := []string{downloaderLifeEventItemFact(root)}
	if root.IsDir {
		queue := []string{strings.TrimSpace(root.ID)}
		visited := make(map[string]struct{})
		itemCount := 0
		for len(queue) > 0 {
			parentID := queue[0]
			queue = queue[1:]
			if _, exists := visited[parentID]; exists {
				return [sha256.Size]byte{}, errors.New("provider directory cycle in life-event candidate")
			}
			visited[parentID] = struct{}{}
			complete := false
			for offset := int64(0); offset < maxMediaLibraryIngestChildren; offset += 200 {
				page, err := driver.List(ctx, parentID, cloudpkg.PageRequest{Offset: offset, Limit: 200})
				if err != nil {
					return [sha256.Size]byte{}, err
				}
				for _, item := range page.Items {
					if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ParentID) != parentID {
						return [sha256.Size]byte{}, errors.New("provider returned an invalid life-event candidate child")
					}
					itemCount++
					if itemCount > maxMediaLibraryIngestChildren {
						return [sha256.Size]byte{}, errors.New("life-event candidate manifest exceeds the bounded item limit")
					}
					facts = append(facts, downloaderLifeEventItemFact(item))
					if item.IsDir {
						queue = append(queue, strings.TrimSpace(item.ID))
					}
				}
				if !page.HasMore {
					complete = true
					break
				}
			}
			if !complete {
				return [sha256.Size]byte{}, errors.New("life-event candidate directory exceeds the bounded page limit")
			}
		}
	}
	sort.Strings(facts)
	return sha256.Sum256([]byte(strings.Join(facts, "\n"))), nil
}

func downloaderLifeEventItemFact(item cloudpkg.Item) string {
	return strings.Join([]string{
		strings.TrimSpace(item.ID),
		strings.TrimSpace(item.ParentID),
		strings.TrimSpace(item.Name),
		strconv.FormatBool(item.IsDir),
		strconv.FormatInt(item.Size, 10),
		strings.TrimSpace(item.SHA1),
		item.ModifiedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
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
	backend, backendErr := s.backends.Get(storage.Type)
	var result medialibrary.Result
	var scanErr error
	if backendErr != nil {
		scanErr = backendErr
	} else {
		result, scanErr = backend.Scan(ctx, MediaLibraryScanRequest{Library: library, Storage: storage, VideoExtensions: extensions, AssetExtensions: assetExtensions, IgnorePatterns: ignores})
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
	var committedChange models.MediaLibraryChange
	metadataProjectionChanged := false
	transactionErr := s.db.Transaction(func(tx *gorm.DB) error {
		var currentLibrary models.MediaLibrary
		if err := tx.First(&currentLibrary, id).Error; err != nil {
			return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageConfiguration, err)
		}
		var currentProfile models.MediaClassificationProfile
		if err := tx.First(&currentProfile, currentLibrary.ProfileID).Error; err != nil {
			return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageConfiguration, err)
		}
		if mediaLibrarySourceChanged(library, currentLibrary) || currentLibrary.ProfileID != profile.ID || currentProfile.Revision != profile.Revision || currentLibrary.DirtyGeneration != library.DirtyGeneration {
			return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageConfiguration, errMediaLibraryConfigurationChanged)
		}
		var existing []models.MediaLibraryEntry
		if err := tx.Where("library_id = ?", id).Find(&existing).Error; err != nil {
			return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageLoadEntries, err)
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
			return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageSourceAssets, err)
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
				return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageSourceAssets, err)
			}
			delete(assetsByPath, source.RelativePath)
		}
		type recognitionProjection struct {
			ID         uint
			SourceKey  string
			Result     MediaRecognitionResult
			SingleFile bool
		}
		byFile := make(map[string]recognitionProjection, len(result.Files))
		seenRecognitionIDs := make([]uint, 0, len(recognizedUnits))
		for _, recognized := range recognizedUnits {
			var record models.MediaLibraryRecognition
			findErr := tx.Where("library_id = ? AND source_key = ?", id, recognized.Unit.SourceKey).First(&record).Error
			recordExisted := findErr == nil
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				record = models.MediaLibraryRecognition{LibraryID: id, SourceKey: recognized.Unit.SourceKey, CreatedAt: now}
			} else if findErr != nil {
				return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageRecognition, findErr)
			} else {
				recognized.Result = preservePlayerEpisodeMetadata(recognized.Result, record.MetadataJSON, library.MetadataLanguage)
			}
			metadataJSON, marshalErr := marshalRecognitionMetadata(recognized.Result)
			if marshalErr != nil {
				return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageRecognition, marshalErr)
			}
			if recordExisted && mediaRecognitionProjectionChanged(record, recognized.Result, string(metadataJSON), recognized.Manual) {
				metadataProjectionChanged = true
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
				return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageRecognition, err)
			}
			seenRecognitionIDs = append(seenRecognitionIDs, record.ID)
			projection := recognitionProjection{ID: record.ID, SourceKey: record.SourceKey, Result: recognized.Result, SingleFile: len(recognized.Unit.Files) == 1}
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
			before := entry
			physicalChanged := exists && (oldPath != file.RelativePath || entry.Size != file.Size || !entry.ModifiedAt.Equal(file.ModifiedAt))
			projection, hasRecognition := byFile[file.RelativePath]
			parsed := medialibrary.ParseMedia(filepath.Base(file.RelativePath), file.RelativePath)
			if !exists {
				run.Added++
				entry = models.MediaLibraryEntry{LibraryID: id, RelativePath: file.RelativePath, CreatedAt: now}
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
				applyRecognitionEpisodeHints(&entry, recognized, projection.SingleFile)
				entry.MatchStatus, entry.RecognitionErrorCode = recognized.Status, recognized.ErrorCode
				entry.WorkKey = recognitionWorkKey(recognized, projection.SourceKey)
				entry.CategoryName, entry.MatchedRuleID = recognized.CategoryName, recognized.MatchedRuleID
				entry.TMDBID, entry.ReleaseYear, entry.MatchConfidence = cloneInt64(recognized.TMDBID), cloneInt(recognized.ReleaseYear), cloneFloat64(recognized.Confidence)
			}
			entry.LastGeneration = generation
			entry.UpdatedAt = now
			if exists && (physicalChanged || mediaLibraryEntryProjectionChanged(before, entry)) {
				run.Updated++
			}
			if err := tx.Save(&entry).Error; err != nil {
				return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageEntries, err)
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
					return wrapMediaLibraryPersistence(mediaLibraryPersistenceStagePrune, err)
				}
			}
			deleteQuery := tx.Where("library_id = ?", id)
			if len(seenRecognitionIDs) > 0 {
				deleteQuery = deleteQuery.Where("id NOT IN ?", seenRecognitionIDs)
			}
			if err := deleteQuery.Delete(&models.MediaLibraryRecognition{}).Error; err != nil {
				return wrapMediaLibraryPersistence(mediaLibraryPersistenceStagePrune, err)
			}
			for _, asset := range assetsByPath {
				if err := tx.Delete(&asset).Error; err != nil {
					return wrapMediaLibraryPersistence(mediaLibraryPersistenceStagePrune, err)
				}
			}
		}
		updates := map[string]any{"dirty_generation": generation, "baseline_generation": generation, "last_scan_at": finished, "last_successful_scan_at": finished, "profile_revision": profile.Revision, "reclassification_due": false, "status_error_code": "", "next_retry_at": nil}
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageGeneration, err)
		}
		run.Status = "success"
		run.FinishedAt = &finished
		if err := tx.Save(&run).Error; err != nil {
			return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageScanRun, err)
		}
		if s.changes != nil && !run.Partial && (run.Added > 0 || run.Updated > 0 || run.Removed > 0 || metadataProjectionChanged) {
			kind := models.MediaLibraryChangeCatalog
			if run.Added == 0 && run.Updated == 0 && run.Removed == 0 {
				kind = models.MediaLibraryChangeMetadata
			} else if run.Removed > 0 && run.Added == 0 && run.Updated == 0 {
				kind = models.MediaLibraryChangeRemoval
			}
			requiresArtifacts := mediaLibraryRequiresArtifacts(storage.Type, currentLibrary, s.artifacts != nil)
			change, err := s.changes.RecordTx(tx, id, generation, kind, !requiresArtifacts)
			if err != nil {
				return wrapMediaLibraryPersistence(mediaLibraryPersistenceStageChange, err)
			}
			committedChange = change
		}
		return nil
	})
	if transactionErr != nil {
		finished := time.Now().UTC()
		run.Status = "failed"
		run.ErrorCode = CodeMediaLibraryScanFailed
		run.FinishedAt = &finished
		_ = s.db.Save(&run).Error
		persistenceStage, databaseErrorClass := mediaLibraryPersistenceDiagnostics(transactionErr)
		operation.Event(s.log.Error()).Str("error_code", CodeMediaLibraryScanFailed).Str("persistence_stage", persistenceStage).Str("database_error_class", databaseErrorClass).Uint("library_id", id).Uint("scan_run_id", run.ID).Uint64("generation", generation).Str("scan_kind", kind).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("结果入库失败"))
		return run, appError(CodeMediaLibraryScanFailed, "扫描结果提交失败", transactionErr)
	}
	if committedChange.State == models.MediaLibraryChangeReady && s.changes != nil {
		s.changes.NotifyCommitted(committedChange.LibraryID, committedChange.Revision)
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
	if s.artifacts != nil && mediaLibraryArtifactGenerationRequired(kind, run, metadataProjectionChanged) {
		if err := s.artifacts.ScheduleGeneration(id, generation); err != nil {
			serverlog.OperationMediaArtifact.Event(s.log.Error()).Uint("library_id", id).Uint64("generation", generation).Str("error_code", "artifact_schedule_failed").Msg(serverlog.OperationMediaArtifact.Message("入队失败"))
		}
	}
	return run, nil
}

func mediaLibraryArtifactGenerationRequired(kind string, run models.MediaLibraryScanRun, metadataProjectionChanged bool) bool {
	if run.Partial || run.Added > 0 || run.Updated > 0 || run.Removed > 0 || metadataProjectionChanged {
		return true
	}
	// Initial/catch-up scans establish or replace an immutable artifact policy,
	// and explicit manual scans may intentionally rebuild it. Routine provider
	// events and periodic scans with a complete no-op diff must not create a new
	// artifact generation.
	switch kind {
	case "event", "incremental", "full":
		return false
	default:
		return true
	}
}

// applyRecognitionEpisodeHints preserves file-level season/episode facts. A
// work/package recognition may use E01 as a representative query hint even
// when the unit contains E01..E46, so an episode hint is valid only for a
// single-file unit and may only fill a missing value.
func applyRecognitionEpisodeHints(entry *models.MediaLibraryEntry, recognized MediaRecognitionResult, singleFile bool) {
	if entry.Season == nil && recognized.SeasonHint != nil {
		entry.Season = cloneInt(recognized.SeasonHint)
	}
	if entry.Episode == nil && singleFile && recognized.EpisodeHint != nil {
		entry.Episode = cloneInt(recognized.EpisodeHint)
	}
}

func mediaRecognitionProjectionChanged(record models.MediaLibraryRecognition, result MediaRecognitionResult, metadataJSON string, manual bool) bool {
	return record.Status != result.Status || record.ErrorCode != result.ErrorCode || record.MediaType != result.MediaType || record.Title != result.Title ||
		!sameOptional(record.ReleaseYear, result.ReleaseYear) || !sameOptional(record.TMDBID, result.TMDBID) || !sameOptional(record.Confidence, result.Confidence) ||
		record.CategoryName != result.CategoryName || !sameOptional(record.MatchedRuleID, result.MatchedRuleID) || record.MetadataJSON != metadataJSON || record.ManualOverride != manual
}

func mediaLibraryEntryProjectionChanged(before, after models.MediaLibraryEntry) bool {
	return before.MediaType != after.MediaType || before.Title != after.Title || before.SeriesTitle != after.SeriesTitle ||
		!sameOptional(before.Season, after.Season) || !sameOptional(before.Episode, after.Episode) || before.WorkKey != after.WorkKey ||
		before.MatchStatus != after.MatchStatus || !sameOptional(before.TMDBID, after.TMDBID) || !sameOptional(before.ReleaseYear, after.ReleaseYear) ||
		!sameOptional(before.MatchConfidence, after.MatchConfidence) || before.RecognitionErrorCode != after.RecognitionErrorCode ||
		before.CategoryName != after.CategoryName || !sameOptional(before.MatchedRuleID, after.MatchedRuleID)
}

func sameOptional[T comparable](left, right *T) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
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
