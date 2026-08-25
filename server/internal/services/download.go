package services

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

const (
	maxCompletedManifestBytes          = 1 << 20
	maxCompletedManifestFiles          = 5000
	pan115DownloadFallbackPollInterval = 20 * time.Second
	pan115DownloadHeartbeatInterval    = 10 * time.Second
)

type providerEventWakeState struct {
	generation uint64
	wake       chan struct{}
}

type providerEventWakeHub struct {
	mu     sync.Mutex
	states map[uint]*providerEventWakeState
}

func newProviderEventWakeHub() *providerEventWakeHub {
	return &providerEventWakeHub{states: map[uint]*providerEventWakeState{}}
}

func (h *providerEventWakeHub) snapshot(connectionID uint) (uint64, <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.states[connectionID]
	if state == nil {
		state = &providerEventWakeState{wake: make(chan struct{})}
		h.states[connectionID] = state
	}
	return state.generation, state.wake
}

func (h *providerEventWakeHub) publish(connectionID uint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.states[connectionID]
	if state == nil {
		state = &providerEventWakeState{wake: make(chan struct{})}
		h.states[connectionID] = state
	}
	state.generation++
	close(state.wake)
	state.wake = make(chan struct{})
}

type DownloadService struct {
	db              *gorm.DB
	audit           *AuditService
	credentials     *credential.Store
	downloader      *DownloaderService
	settings        *DownloadSettingsService
	queue           *QueueService
	log             zerolog.Logger
	metadata        *MetadataSettingsService
	transfers       *TransferService
	seeding         *SeedingSettingsService
	providerEvents  *providerEventWakeHub
	pluginDownloads *PluginDownloadExecutor
}

func (s *DownloadService) SetPluginDownloadExecutor(executor *PluginDownloadExecutor) {
	s.pluginDownloads = executor
}

func (s *DownloadService) SubmitPluginDownload(ctx context.Context, actor Actor, input SubmitPluginDownloadInput, request RequestContext) (DownloadTaskSummary, error) {
	if s.pluginDownloads == nil {
		return DownloadTaskSummary{}, appError(CodePluginRuntimeUnavailable, "插件下载服务不可用", nil)
	}
	return s.pluginDownloads.Submit(ctx, actor, input, request)
}

func NewDownloadService(db *gorm.DB, audit *AuditService, credentials *credential.Store, downloader *DownloaderService, settings *DownloadSettingsService, queue *QueueService, log zerolog.Logger) *DownloadService {
	service := &DownloadService{db: db, audit: audit, credentials: credentials, downloader: downloader, settings: settings, queue: queue, log: log, providerEvents: newProviderEventWakeHub()}
	queue.SetInterruptAcknowledged(service.finalizeInterrupt)
	return service
}

func (s *DownloadService) SetMetadataSettings(settings *MetadataSettingsService) {
	s.metadata = settings
}

func (s *DownloadService) SetTransferService(transfers *TransferService) {
	s.transfers = transfers
	if transfers != nil {
		transfers.SetCompletedManifestVerifier(func(ctx context.Context, task *models.DownloadTask, manifest downloadpkg.Manifest) (downloadpkg.Manifest, error) {
			return NewDownloadWorker(s).verifyCompleted(ctx, task, manifest)
		})
	}
}

func (s *DownloadService) SetSeedingSettings(settings *SeedingSettingsService) {
	s.seeding = settings
}

// ProviderEventsChanged wakes every active 115 offline worker using this
// connection. The provider task API remains authoritative; life events are a
// low-latency signal and bounded polling still covers delayed or missed events.
func (s *DownloadService) ProviderEventsChanged(_ context.Context, connectionID uint, events []models.ProviderEvent) error {
	if connectionID != 0 && len(events) > 0 && s.providerEvents != nil {
		s.providerEvents.publish(connectionID)
	}
	return nil
}

type DownloadSourceInput struct {
	Kind     string
	URL      string
	Torrent  []byte
	Filename string
}

type SubmitDownloadInput struct {
	DownloaderID   string
	MediaLibraryID *uint
	ProfileID      uint
	DisplayName    string
	Priority       int
	Source         DownloadSourceInput
}

type downloadSourceEnvelope struct {
	Kind               string `json:"kind"`
	URL                string `json:"url,omitempty"`
	Torrent            []byte `json:"torrent,omitempty"`
	Filename           string `json:"filename,omitempty"`
	ProviderItemID     string `json:"provider_item_id,omitempty"`
	PluginConnectionID string `json:"plugin_connection_id,omitempty"`
	PluginItemID       string `json:"plugin_item_id,omitempty"`
	PluginSegmentID    string `json:"plugin_segment_id,omitempty"`
	PluginVersionID    string `json:"plugin_version_id,omitempty"`
	PluginVariantID    string `json:"plugin_variant_id,omitempty"`
}

type downloadJobPayload struct {
	DownloadTaskID string `json:"download_task_id"`
}

type DownloadTaskSummary struct {
	ID                string     `json:"id"`
	JobID             string     `json:"job_id"`
	OwnerID           uint       `json:"owner_id"`
	DownloaderID      *string    `json:"downloader_id"`
	DownloaderName    string     `json:"downloader_name"`
	ProviderType      string     `json:"provider_type"`
	DisplayName       string     `json:"display_name"`
	JobStatus         string     `json:"job_status"`
	ProviderStatus    string     `json:"provider_status"`
	Phase             string     `json:"phase"`
	Progress          *float64   `json:"progress"`
	BytesCompleted    *int64     `json:"bytes_completed"`
	BytesTotal        *int64     `json:"bytes_total"`
	DownloadSpeed     *int64     `json:"download_speed"`
	UploadSpeed       *int64     `json:"upload_speed"`
	ETASeconds        *int64     `json:"eta_seconds"`
	LastSampledAt     *time.Time `json:"last_sampled_at"`
	LastErrorCode     string     `json:"last_error_code"`
	LastErrorMessage  string     `json:"last_error_message"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	ProfileID         uint       `json:"profile_id"`
	ProfileRevision   uint64     `json:"profile_revision"`
	ScrapeStatus      string     `json:"scrape_status"`
	ScrapeTitle       string     `json:"scrape_title"`
	ScrapeMediaType   string     `json:"scrape_media_type"`
	ScrapeCategory    string     `json:"scrape_category"`
	ScrapeTMDBID      *int64     `json:"scrape_tmdb_id"`
	ScrapeConfidence  *float64   `json:"scrape_confidence"`
	ManifestFiles     int        `json:"manifest_file_count"`
	TargetLibraryID   *uint      `json:"target_library_id"`
	TargetLibraryName string     `json:"target_library_name"`
	TransferMode      string     `json:"transfer_mode"`
	ConflictPolicy    string     `json:"conflict_policy"`
	TransferPhase     string     `json:"transfer_phase"`
	TransferTaskID    string     `json:"transfer_task_id"`
	TransferJobID     string     `json:"transfer_job_id"`
	TransferJobStatus string     `json:"transfer_job_status"`
	SeedingTaskID     string     `json:"seeding_task_id"`
	SeedingJobID      string     `json:"seeding_job_id"`
	SeedingJobStatus  string     `json:"seeding_job_status"`
	SeedingPhase      string     `json:"seeding_phase"`
	LifecycleScope    string     `json:"lifecycle_scope"`
}

const (
	DownloadListScopeActive  = "active"
	DownloadListScopeHistory = "history"
	DownloadListScopeAll     = "all"
)

func (s *DownloadService) Submit(ctx context.Context, actor Actor, input SubmitDownloadInput, request RequestContext) (DownloadTaskSummary, error) {
	if !actor.Can(authz.PermissionDownloadsCreate) {
		return DownloadTaskSummary{}, appError(CodePermissionDenied, "无权创建下载任务", nil)
	}
	return s.submit(ctx, actor.User.ID, input, request, models.DownloadSourceOriginUser, "", "")
}

func (s *DownloadService) submit(ctx context.Context, ownerID uint, input SubmitDownloadInput, request RequestContext, sourceOrigin, ingestSourceKey, providerItemID string) (DownloadTaskSummary, error) {
	var downloaderRecord models.Downloader
	if err := s.db.First(&downloaderRecord, "id = ?", strings.TrimSpace(input.DownloaderID)).Error; err != nil {
		return DownloadTaskSummary{}, downloaderNotFound(err)
	}
	if !downloaderRecord.Enabled {
		return DownloadTaskSummary{}, appError(CodeDownloaderUnavailable, "下载器已停用", nil)
	}
	var source downloadSourceEnvelope
	var displayName string
	var err error
	if strings.TrimSpace(providerItemID) != "" {
		source = downloadSourceEnvelope{Kind: downloadpkg.SourceProviderItem, ProviderItemID: strings.TrimSpace(providerItemID)}
		displayName, err = normalizeDownloadDisplayName(input.DisplayName, "115 中转接管")
	} else {
		source, displayName, err = normalizeDownloadSource(input.Source, input.DisplayName)
	}
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	if source.Kind == downloadpkg.SourceProviderItem && sourceOrigin != models.DownloadSourceOriginProviderIngest {
		return DownloadTaskSummary{}, appError(CodeDownloadSourceInvalid, "内部摄取来源不能由用户提交", nil)
	}
	if source.Kind == downloadpkg.SourcePan115Share {
		sourceOrigin = models.DownloadSourceOriginShare
	}
	capabilities, capabilitiesKnown := s.downloader.registry.Capabilities(downloaderRecord.Type)
	if source.Kind == downloadpkg.SourcePan115Share && (!capabilitiesKnown || !capabilities.ShareReceive || downloaderRecord.Type != models.DownloaderTypePan115Offline) {
		return DownloadTaskSummary{}, appError(CodeDownloadSourceInvalid, "所选下载器不支持 115 分享转存", nil)
	}
	var profile models.MediaClassificationProfile
	var target *downloadTargetSnapshot
	if input.MediaLibraryID != nil || source.Kind == downloadpkg.SourcePan115Share || source.Kind == downloadpkg.SourceProviderItem {
		requested := uint(0)
		if input.MediaLibraryID != nil {
			requested = *input.MediaLibraryID
		}
		var err error
		target, profile, err = s.resolveDownloadTarget(ctx, downloaderRecord, requested, source.Kind)
		if err != nil {
			return DownloadTaskSummary{}, err
		}
	} else {
		profileQuery := s.db
		if input.ProfileID == 0 {
			profileQuery = profileQuery.Where("code = ?", "default-v1")
		} else {
			profileQuery = profileQuery.Where("id = ?", input.ProfileID)
		}
		if err := profileQuery.First(&profile).Error; err != nil {
			return DownloadTaskSummary{}, appError(CodeMediaLibraryProfileUnavailable, "媒体分类规则不存在", err)
		}
	}
	rules, err := classification.DecodeStrict([]byte(profile.RulesJSON))
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeProfileValidation, "媒体分类规则无效", err)
	}
	canonicalRules, err := classification.CanonicalJSON(rules)
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeProfileValidation, "媒体分类规则无效", err)
	}
	organization, err := storedProfileOrganizationConfig(profile)
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeProfileValidation, "媒体识别与命名配置无效", err)
	}
	staging, err := s.settings.Snapshot(ctx, downloaderRecord.Type)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	if input.Priority < -100 || input.Priority > 100 {
		return DownloadTaskSummary{}, appError(CodeInvalidRequest, "下载优先级无效", nil)
	}
	taskID := uuid.NewString()
	rawSource, err := json.Marshal(source)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	encryptedSource, err := s.credentials.Encrypt(downloadSourcePurpose(taskID), string(rawSource))
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	now := time.Now().UTC()
	seedingPolicy := SeedingPolicySnapshot{CompletionMode: models.SeedingCompletionAll}
	if capabilitiesKnown && capabilities.Seeding {
		seedingPolicy.MinimumSeedMinutes = 1440
		seedingPolicy.MinimumRatio = 1
	}
	if capabilitiesKnown && capabilities.Seeding && s.seeding != nil {
		seedingPolicy, err = s.seeding.Snapshot()
		if err != nil {
			return DownloadTaskSummary{}, err
		}
	}
	record := models.DownloadTask{ID: taskID, OwnerID: ownerID, DownloaderID: &downloaderRecord.ID, DownloaderName: downloaderRecord.Name, ProviderType: downloaderRecord.Type, ProviderTag: "omc-" + taskID, SourceCiphertext: encryptedSource, StagingAbsolutePath: staging.AbsolutePath, IngestSourceKey: strings.TrimSpace(ingestSourceKey), SourceOrigin: sourceOrigin, ProfileID: profile.ID, ProfileRevision: profile.Revision, ProfileRulesJSON: canonicalRules, ProfileBuiltinRecognitionPacksJSON: organization.BuiltinRecognitionPacksJSON, ProfileRecognitionRulesJSON: organization.RecognitionRulesJSON, SeedingCleanupEnabled: seedingPolicy.CleanupEnabled, SeedingMinimumMinutes: seedingPolicy.MinimumSeedMinutes, SeedingMinimumRatio: seedingPolicy.MinimumRatio, SeedingCompletionMode: seedingPolicy.CompletionMode, DisplayName: displayName, Phase: models.DownloadTaskStatusQueued, CreatedAt: now, UpdatedAt: now}
	if downloaderRecord.Type == models.DownloaderTypePan115Offline {
		record.StagingStorageID = downloaderRecord.StorageID
		record.StagingRelativePath = "/"
	}
	if target != nil {
		// The media-library intake directory is an override only for share
		// receive and internally adopted provider items. Ordinary 115 offline
		// downloads must keep using the downloader's own configured directory.
		if source.Kind == downloadpkg.SourcePan115Share || source.Kind == downloadpkg.SourceProviderItem {
			record.StagingProviderDirectoryID = target.IngestProviderRootID
		}
		record.TargetLibraryID = &target.LibraryID
		record.TargetLibraryName = target.LibraryName
		record.TargetStorageID = &target.StorageID
		record.TargetStorageType = target.StorageType
		record.TargetConnectionID = target.ConnectionID
		record.TargetProviderRootID = target.ProviderRootID
		record.TargetStorageRoot = target.StorageRoot
		record.TargetRelativeRoot = target.RelativeRoot
		record.TransferMode = target.TransferMode
		record.ConflictPolicy = target.ConflictPolicy
		record.MovieDirectoryTemplate = organization.MovieDirectoryTemplate
		record.MovieFilenameTemplate = organization.MovieFilenameTemplate
		record.TVDirectoryTemplate = organization.TVDirectoryTemplate
		record.TVFilenameTemplate = organization.TVFilenameTemplate
	}
	job, err := s.queue.EnqueueWith(EnqueueJobInput{OwnerID: ownerID, JobType: "download", Priority: input.Priority, DisplayName: displayName, Provider: downloaderRecord.Type, ResourceKey: downloadQueueResourceKey(downloaderRecord), Payload: downloadJobPayload{DownloadTaskID: taskID}}, func(tx *gorm.DB, job models.Job) error {
		record.JobID = job.ID
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		metadata := map[string]any{"downloader_id": downloaderRecord.ID, "provider_type": downloaderRecord.Type, "source_kind": source.Kind}
		if record.TargetLibraryID != nil {
			metadata["media_library_id"] = *record.TargetLibraryID
		}
		metadata["source_origin"] = sourceOrigin
		return s.audit.Record(tx, &ownerID, "download.create", "download_task", taskID, "success", metadata, request)
	})
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	return downloadTaskSummary(record, job.Status), nil
}

func downloadQueueResourceKey(record models.Downloader) string {
	if record.Type == models.DownloaderTypeQBittorrent {
		// Submission/monitoring workers remain bounded by the high global guard,
		// while qBittorrent owns actual active-download and queue limits.
		return ""
	}
	return "downloader:" + record.ID
}

// AdoptProviderItem creates one ordinary durable download task for a direct
// child discovered below a media-library intake root. The source identity is
// encrypted like every other download source and the hash is the only
// deduplication fact stored in plaintext.
func (s *DownloadService) AdoptProviderItem(ctx context.Context, libraryID uint, providerItemID, displayName string) (bool, error) {
	providerItemID = strings.TrimSpace(providerItemID)
	if libraryID == 0 || providerItemID == "" || len(providerItemID) > 128 || strings.ContainsAny(providerItemID, "\x00\r\n") {
		return false, appError(CodeDownloadSourceInvalid, "115 中转接管来源无效", nil)
	}
	var library models.MediaLibrary
	if err := s.db.WithContext(ctx).Where("id = ? AND enabled = ? AND ingest_enabled = ?", libraryID, true, true).First(&library).Error; err != nil {
		return false, appError(CodeMediaLibraryStorageUnavailable, "自动摄取媒体库不存在或已停用", err)
	}
	if library.IngestDownloaderID == nil || library.IngestOwnerID == nil || *library.IngestOwnerID == 0 {
		return false, appError(CodeMediaLibraryStorageUnavailable, "自动摄取媒体库配置不完整", nil)
	}
	var storage models.Storage
	if err := s.db.WithContext(ctx).First(&storage, library.StorageID).Error; err != nil || storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil {
		return false, appError(CodeMediaLibraryStorageUnavailable, "自动摄取媒体库 Storage 不可用", err)
	}
	keyBytes := sha256.Sum256([]byte(fmt.Sprintf("pan115:%d:%d:%s", *storage.ConnectionID, library.ID, providerItemID)))
	ingestKey := fmt.Sprintf("%x", keyBytes[:])
	var existing int64
	if err := s.db.WithContext(ctx).Model(&models.DownloadTask{}).Where("ingest_source_key = ?", ingestKey).Count(&existing).Error; err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil
	}
	name, err := normalizeDownloadDisplayName(displayName, "115 中转接管")
	if err != nil {
		return false, err
	}
	targetID := library.ID
	createdTask, err := s.submit(ctx, *library.IngestOwnerID, SubmitDownloadInput{DownloaderID: *library.IngestDownloaderID, MediaLibraryID: &targetID, DisplayName: name, Source: DownloadSourceInput{Kind: downloadpkg.SourceProviderItem}}, RequestContext{}, models.DownloadSourceOriginProviderIngest, ingestKey, providerItemID)
	if err != nil {
		// The partial unique index is the concurrency authority. If another
		// sweep won the race, treat this attempt as an idempotent no-op.
		if queryErr := s.db.WithContext(ctx).Model(&models.DownloadTask{}).Where("ingest_source_key = ?", ingestKey).Count(&existing).Error; queryErr == nil && existing > 0 {
			return false, nil
		}
		return false, err
	}
	serverlog.OperationPan115ShareIngest.Event(s.log.Info()).Str("task_id", createdTask.ID).Uint("library_id", library.ID).Uint("connection_id", *storage.ConnectionID).Msg(serverlog.OperationPan115ShareIngest.Message("已创建中转内容接管任务"))
	return true, nil
}

type downloadTargetSnapshot struct {
	LibraryID              uint
	LibraryName            string
	StorageID              uint
	StorageType            string
	ConnectionID           *uint
	ProviderRootID         string
	StorageRoot            string
	RelativeRoot           string
	TransferMode           string
	ConflictPolicy         string
	MovieDirectoryTemplate string
	MovieFilenameTemplate  string
	TVDirectoryTemplate    string
	TVFilenameTemplate     string
	IngestProviderRootID   string
}

func (s *DownloadService) resolveDownloadTarget(ctx context.Context, downloader models.Downloader, requested uint, sourceKind string) (*downloadTargetSnapshot, models.MediaClassificationProfile, error) {
	if requested != 0 {
		var library models.MediaLibrary
		if err := s.db.Where("id = ? AND enabled = ?", requested, true).First(&library).Error; err != nil {
			return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "目标媒体库不存在或已停用", err)
		}
		return s.snapshotDownloadTarget(ctx, downloader, library, sourceKind)
	}

	var libraries []models.MediaLibrary
	if err := s.db.Where("enabled = ?", true).Order("sort_order,id").Find(&libraries).Error; err != nil {
		return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "没有可用的目标媒体库", err)
	}
	for _, library := range libraries {
		target, profile, err := s.snapshotDownloadTarget(ctx, downloader, library, sourceKind)
		if err == nil {
			return target, profile, nil
		}
	}
	return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "没有可用的目标媒体库", nil)
}

func (s *DownloadService) snapshotDownloadTarget(ctx context.Context, downloader models.Downloader, library models.MediaLibrary, sourceKinds ...string) (*downloadTargetSnapshot, models.MediaClassificationProfile, error) {
	sourceKind := ""
	if len(sourceKinds) > 0 {
		sourceKind = sourceKinds[0]
	}
	var storage models.Storage
	if err := s.db.First(&storage, library.StorageID).Error; err != nil || !storage.Enabled {
		return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "目标媒体库 Storage 不可用", err)
	}
	var connectionID *uint
	providerRootID := ""
	switch storage.Type {
	case models.StorageTypeLocal:
		if _, err := medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot); err != nil {
			return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryPathInvalid, "目标媒体库目录不可用", err)
		}
	case models.StorageTypePan115:
		if storage.ConnectionID == nil || strings.TrimSpace(library.ProviderRootID) == "" || s.downloader == nil || s.downloader.connections == nil {
			return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "115 目标媒体库连接不可用", nil)
		}
		if library.TransferMode != models.MediaLibraryTransferMove && library.TransferMode != models.MediaLibraryTransferCopy {
			return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "115 媒体库仅支持移动或复制入库", nil)
		}
		_, driver, err := s.downloader.connections.driver(*storage.ConnectionID)
		if err != nil {
			return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "115 目标连接不可用", err)
		}
		capabilities := driver.Capabilities()
		if downloader.Type == models.DownloaderTypePluginHTTP && sourceKind == "plugin_plan" {
			if _, ok := driver.(cloudpkg.UploadDriver); !ok || !capabilities.FileUpload || !capabilities.CreateDirectory || !capabilities.Recycle {
				return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "115 目标缺少文件上传能力", nil)
			}
		} else {
			if downloader.Type != models.DownloaderTypePan115Offline || downloader.StorageID == nil {
				return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "115 目标媒体库只能接收同账号离线下载或受管站点下载", nil)
			}
			var sourceStorage models.Storage
			if err := s.db.First(&sourceStorage, *downloader.StorageID).Error; err != nil || sourceStorage.Type != models.StorageTypePan115 || sourceStorage.ConnectionID == nil || *sourceStorage.ConnectionID != *storage.ConnectionID {
				return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "115 离线下载与目标媒体库不属于同一账号", err)
			}
			_, ok := driver.(cloudpkg.MutationDriver)
			if !ok || !capabilities.CreateDirectory || !capabilities.Rename || !capabilities.Recycle || (library.TransferMode == models.MediaLibraryTransferMove && !capabilities.Move) || (library.TransferMode == models.MediaLibraryTransferCopy && !capabilities.Copy) {
				return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "115 目标缺少所需的云端整理能力", nil)
			}
		}
		root, err := providerItemWithinRoot(ctx, driver, library.ProviderRootID, storage.RootPath)
		if err != nil || !root.IsDir {
			return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryPathInvalid, "115 目标媒体库目录不可用", err)
		}
		value := *storage.ConnectionID
		connectionID, providerRootID = &value, strings.TrimSpace(library.ProviderRootID)
		if sourceKind == downloadpkg.SourcePan115Share || sourceKind == downloadpkg.SourceProviderItem {
			if !library.IngestEnabled || library.IngestDownloaderID == nil || *library.IngestDownloaderID != downloader.ID || library.IngestOwnerID == nil || strings.TrimSpace(library.IngestProviderRootID) == "" {
				return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "目标媒体库未启用与当前下载器绑定的 115 自动摄取", nil)
			}
			ingestRoot, err := providerItemWithinRoot(ctx, driver, library.IngestProviderRootID, storage.RootPath)
			if err != nil || !ingestRoot.IsDir {
				return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryPathInvalid, "115 中转目录不可用", err)
			}
		}
	default:
		return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "目标媒体库 Storage 类型不受支持", nil)
	}
	var profile models.MediaClassificationProfile
	if err := s.db.First(&profile, library.ProfileID).Error; err != nil {
		return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryProfileUnavailable, "目标媒体库分类规则不可用", err)
	}
	organization, err := storedProfileOrganizationConfig(profile)
	if err != nil {
		return nil, models.MediaClassificationProfile{}, appError(CodeProfileValidation, "目标媒体库识别与命名配置无效", err)
	}
	return &downloadTargetSnapshot{LibraryID: library.ID, LibraryName: library.Name, StorageID: storage.ID, StorageType: storage.Type, ConnectionID: connectionID, ProviderRootID: providerRootID, StorageRoot: storage.RootPath, RelativeRoot: library.RelativeRoot, TransferMode: library.TransferMode, ConflictPolicy: library.ConflictPolicy, MovieDirectoryTemplate: organization.MovieDirectoryTemplate, MovieFilenameTemplate: organization.MovieFilenameTemplate, TVDirectoryTemplate: organization.TVDirectoryTemplate, TVFilenameTemplate: organization.TVFilenameTemplate, IngestProviderRootID: strings.TrimSpace(library.IngestProviderRootID)}, profile, nil
}

func (s *DownloadService) List(actor Actor, limit int) ([]DownloadTaskSummary, error) {
	items, _, err := s.ListScoped(actor, DownloadListScopeAll, limit)
	return items, err
}

func (s *DownloadService) ListScoped(actor Actor, scope string, limit int) ([]DownloadTaskSummary, int64, error) {
	if !actor.Can(authz.PermissionDownloadsReadAll) && !actor.Can(authz.PermissionDownloadsReadOwn) {
		return nil, 0, appError(CodePermissionDenied, "无权查看下载任务", nil)
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = DownloadListScopeActive
	}
	if scope != DownloadListScopeActive && scope != DownloadListScopeHistory && scope != DownloadListScopeAll {
		return nil, 0, appError(CodeInvalidRequest, "下载任务范围无效", nil)
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	query := s.db.Model(&models.DownloadTask{}).
		Joins("JOIN jobs AS download_scope_job ON download_scope_job.id = download_tasks.job_id").
		Joins("LEFT JOIN transfer_tasks AS transfer_scope ON transfer_scope.download_task_id = download_tasks.id").
		Joins("LEFT JOIN jobs AS transfer_scope_job ON transfer_scope_job.id = transfer_scope.job_id").
		Joins("LEFT JOIN seeding_tasks AS seeding_scope ON seeding_scope.download_task_id = download_tasks.id").
		Joins("LEFT JOIN jobs AS seeding_scope_job ON seeding_scope_job.id = seeding_scope.job_id")
	if !actor.Can(authz.PermissionDownloadsReadAll) {
		query = query.Where("download_tasks.owner_id = ?", actor.User.ID)
	}
	historySQL := `(download_scope_job.status = ? OR (download_scope_job.status = ? AND (transfer_scope.id IS NULL OR (transfer_scope_job.status = ? AND (seeding_scope.id IS NULL OR seeding_scope_job.status = ?)))))`
	historyArgs := []any{models.JobStatusCancelled, models.JobStatusCompleted, models.JobStatusCompleted, models.JobStatusCompleted}
	if scope == DownloadListScopeHistory {
		query = query.Where(historySQL, historyArgs...)
	} else if scope == DownloadListScopeActive {
		query = query.Where("NOT "+historySQL, historyArgs...)
	}
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []models.DownloadTask
	if err := query.Select("download_tasks.*").Order("download_tasks.created_at DESC, download_tasks.id DESC").Limit(limit).Find(&records).Error; err != nil {
		return nil, 0, err
	}
	jobIDs := make([]string, 0, len(records))
	for _, record := range records {
		jobIDs = append(jobIDs, record.JobID)
	}
	jobs := map[string]string{}
	type transferSummary struct {
		ID             string
		DownloadTaskID string
		JobID          string
		Phase          string
		JobStatus      string
	}
	transfers := map[string]transferSummary{}
	type seedingSummary struct {
		ID             string
		DownloadTaskID string
		JobID          string
		Phase          string
		JobStatus      string
	}
	seeding := map[string]seedingSummary{}
	if len(jobIDs) > 0 {
		var rows []models.Job
		if err := s.db.Select("id", "status").Where("id IN ?", jobIDs).Find(&rows).Error; err != nil {
			return nil, 0, err
		}
		for _, job := range rows {
			jobs[job.ID] = job.Status
		}
	}
	if len(records) > 0 {
		taskIDs := make([]string, 0, len(records))
		for _, record := range records {
			taskIDs = append(taskIDs, record.ID)
		}
		var rows []transferSummary
		if err := s.db.Table("transfer_tasks AS transfer").
			Select("transfer.id, transfer.download_task_id, transfer.job_id, transfer.phase, jobs.status AS job_status").
			Joins("LEFT JOIN jobs ON jobs.id = transfer.job_id").
			Where("transfer.download_task_id IN ?", taskIDs).
			Find(&rows).Error; err != nil {
			return nil, 0, err
		}
		for _, transfer := range rows {
			transfers[transfer.DownloadTaskID] = transfer
		}
		var seedingRows []seedingSummary
		if err := s.db.Table("seeding_tasks AS seeding").
			Select("seeding.id, seeding.download_task_id, seeding.job_id, seeding.phase, jobs.status AS job_status").
			Joins("LEFT JOIN jobs ON jobs.id = seeding.job_id").
			Where("seeding.download_task_id IN ?", taskIDs).
			Find(&seedingRows).Error; err != nil {
			return nil, 0, err
		}
		for _, item := range seedingRows {
			seeding[item.DownloadTaskID] = item
		}
	}
	items := make([]DownloadTaskSummary, 0, len(records))
	for _, record := range records {
		item := downloadTaskSummary(record, jobs[record.JobID])
		if transfer, ok := transfers[record.ID]; ok {
			item.TransferPhase = transfer.Phase
			item.TransferTaskID = transfer.ID
			item.TransferJobID = transfer.JobID
			item.TransferJobStatus = transfer.JobStatus
		}
		if seed, ok := seeding[record.ID]; ok {
			item.SeedingTaskID = seed.ID
			item.SeedingJobID = seed.JobID
			item.SeedingJobStatus = seed.JobStatus
			item.SeedingPhase = seed.Phase
		}
		item.LifecycleScope = downloadLifecycleScope(item)
		items = append(items, item)
	}
	return items, total, nil
}

func downloadLifecycleScope(item DownloadTaskSummary) string {
	if item.JobStatus == models.JobStatusCancelled {
		return DownloadListScopeHistory
	}
	if item.JobStatus != models.JobStatusCompleted {
		return DownloadListScopeActive
	}
	if item.TransferTaskID == "" {
		return DownloadListScopeHistory
	}
	if item.TransferJobStatus != models.JobStatusCompleted {
		return DownloadListScopeActive
	}
	if item.SeedingTaskID != "" && item.SeedingJobStatus != models.JobStatusCompleted {
		return DownloadListScopeActive
	}
	return DownloadListScopeHistory
}

// Delete removes a terminal OhMyCine record. If the provider task still
// exists, provider data deletion must succeed first. A later database failure
// leaves the terminal local fact available for an idempotent retry.
func (s *DownloadService) Delete(ctx context.Context, actor Actor, id string, request RequestContext) error {
	var task models.DownloadTask
	if err := s.db.First(&task, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return downloadTaskNotFound(err)
	}
	var job models.Job
	if err := s.db.First(&job, "id = ?", task.JobID).Error; err != nil {
		return queueNotFound(err)
	}
	if !actor.Can(authz.PermissionDownloadsManageAll) && (task.OwnerID != actor.User.ID || !actor.Can(authz.PermissionJobsControlOwn)) {
		return appError(CodePermissionDenied, "无权删除该下载任务", nil)
	}
	if job.Status == models.JobStatusCompleted {
		return s.deleteCompletedHistoryRecord(actor, task.ID, request)
	}
	if job.Status != models.JobStatusFailed && job.Status != models.JobStatusCancelled {
		return appError(CodeQueueStateConflict, "仅失败、已取消或完整收口的下载历史可以删除", nil)
	}
	providerCleanup := "not_required"
	if task.ProviderType == models.DownloaderTypePluginHTTP {
		if _, err := cleanupPluginDownloadOutput(task); err != nil {
			return appError("plugin_download_cleanup_failed", "站点下载暂存清理失败，本地任务记录已保留", nil)
		}
		providerCleanup = "plugin_managed_output"
	}
	if task.ProviderTaskID != "" && task.DownloaderID != nil {
		_, client, err := s.downloader.client(*task.DownloaderID)
		if err != nil {
			return appError(CodeDownloaderUnavailable, "无法连接原下载器，未删除本地任务记录", nil)
		}
		if err := client.Cancel(ctx, task.ProviderTaskID, true); err != nil && !providerTaskMissing(err) {
			return appError(CodeDownloaderUnavailable, "下载器未能删除任务与下载数据，本地记录已保留", nil)
		}
		providerCleanup = "confirmed"
	} else if task.ProviderTaskID != "" {
		return appError(CodeDownloaderUnavailable, "原下载器配置已不存在，无法确认删除下载数据，本地记录已保留", nil)
	}
	return s.deleteLocalRecord(task.ID, job.ID, &actor.User.ID, "download.delete", providerCleanup, request)
}

// deleteCompletedHistoryRecord removes only durable pipeline history after all
// downstream jobs have completed. It deliberately performs no provider or file
// operation; qBittorrent, staging content and library media remain untouched.
func (s *DownloadService) deleteCompletedHistoryRecord(actor Actor, taskID string, request RequestContext) error {
	deletedJobs := make([]models.Job, 0, 3)
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var task models.DownloadTask
		if err := tx.First(&task, "id = ?", taskID).Error; err != nil {
			return downloadTaskNotFound(err)
		}
		if !actor.Can(authz.PermissionDownloadsManageAll) && (task.OwnerID != actor.User.ID || !actor.Can(authz.PermissionJobsControlOwn)) {
			return appError(CodePermissionDenied, "无权删除该下载历史记录", nil)
		}
		var downloadJob models.Job
		if err := tx.First(&downloadJob, "id = ?", task.JobID).Error; err != nil {
			return err
		}
		if downloadJob.Status != models.JobStatusCompleted {
			return appError(CodeQueueStateConflict, "下载流水线尚未完整收口，不能删除历史记录", nil)
		}

		var transfer models.TransferTask
		transferFound := true
		if err := tx.Where("download_task_id = ?", task.ID).First(&transfer).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			transferFound = false
		}
		var transferJob models.Job
		if transferFound {
			if err := tx.First(&transferJob, "id = ?", transfer.JobID).Error; err != nil {
				return err
			}
			if transferJob.Status != models.JobStatusCompleted {
				return appError(CodeQueueStateConflict, "媒体整理尚未成功完成，不能删除下载历史记录", nil)
			}
		}

		var seeding models.SeedingTask
		seedingFound := true
		if err := tx.Where("download_task_id = ?", task.ID).First(&seeding).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			seedingFound = false
		}
		var seedingJob models.Job
		if seedingFound {
			if err := tx.First(&seedingJob, "id = ?", seeding.JobID).Error; err != nil {
				return err
			}
			if seedingJob.Status != models.JobStatusCompleted {
				return appError(CodeQueueStateConflict, "做种管理尚未成功完成，不能删除下载历史记录", nil)
			}
		}

		metadata := map[string]any{"cleanup": "history_only", "transfer_history": transferFound, "seeding_history": seedingFound}
		if task.TargetLibraryID != nil {
			metadata["media_library_id"] = *task.TargetLibraryID
		}
		if err := s.audit.Record(tx, &actor.User.ID, "download.history_delete", "download_task", task.ID, "success", metadata, request); err != nil {
			return err
		}
		if transferFound {
			if err := tx.Delete(&transfer).Error; err != nil {
				return err
			}
			deletedJobs = append(deletedJobs, transferJob)
		}
		if seedingFound {
			if err := tx.Delete(&seeding).Error; err != nil {
				return err
			}
			deletedJobs = append(deletedJobs, seedingJob)
		}
		if err := tx.Delete(&task).Error; err != nil {
			return err
		}
		jobIDs := make([]string, 0, len(deletedJobs)+1)
		for _, child := range deletedJobs {
			jobIDs = append(jobIDs, child.ID)
		}
		jobIDs = append(jobIDs, downloadJob.ID)
		if err := tx.Where("id IN ?", jobIDs).Delete(&models.Job{}).Error; err != nil {
			return err
		}
		deletedJobs = append(deletedJobs, downloadJob)
		return nil
	})
	if err != nil {
		return err
	}
	if s.queue != nil {
		for _, job := range deletedJobs {
			s.queue.publish(job, "job.deleted")
		}
	}
	return nil
}

func (s *DownloadService) finalizeInterrupt(jobID, action string) error {
	if action != "cancel" {
		return nil
	}
	var task models.DownloadTask
	if err := s.db.First(&task, "job_id = ?", jobID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if task.ProviderType == models.DownloaderTypePluginHTTP && s.pluginDownloads != nil {
		root, err := s.pluginDownloads.taskRoot(task)
		if err != nil {
			return err
		}
		if err := removeManagedTaskRoot(task.StagingAbsolutePath, root); err != nil {
			return err
		}
	}
	var event models.JobStatusEvent
	var actorID *uint
	if err := s.db.Where("job_id = ? AND event_type = ?", jobID, "control.cancel").Order("id DESC").First(&event).Error; err == nil {
		actorID = event.ActorID
	}
	return s.deleteLocalRecord(task.ID, jobID, actorID, "download.cancel_delete", "confirmed", RequestContext{})
}

func (s *DownloadService) deleteLocalRecord(taskID, jobID string, actorID *uint, action, providerCleanup string, request RequestContext) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var task models.DownloadTask
		if err := tx.First(&task, "id = ? AND job_id = ?", taskID, jobID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var job models.Job
		if err := tx.First(&job, "id = ?", jobID).Error; err != nil {
			return err
		}
		if job.Status != models.JobStatusFailed && job.Status != models.JobStatusCancelled {
			return appError(CodeQueueStateConflict, "下载任务尚未进入可删除状态", nil)
		}
		if err := s.audit.Record(tx, actorID, action, "download_task", taskID, "success", map[string]any{"provider_cleanup": providerCleanup}, request); err != nil {
			return err
		}
		if err := tx.Delete(&task).Error; err != nil {
			return err
		}
		return tx.Delete(&job).Error
	})
}

func providerTaskMissing(err error) bool {
	code, _ := downloadpkg.ErrorInfo(err)
	return code == "downloader_task_not_found"
}

func downloadTaskNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "下载任务不存在", err)
	}
	return err
}

func normalizeDownloadSource(input DownloadSourceInput, requestedName string) (downloadSourceEnvelope, string, error) {
	kind := strings.ToLower(strings.TrimSpace(input.Kind))
	source := downloadSourceEnvelope{Kind: kind}
	switch kind {
	case downloadpkg.SourceURL:
		raw := strings.TrimSpace(input.URL)
		if len(raw) == 0 || len(raw) > 16*1024 {
			return source, "", appError(CodeDownloadSourceInvalid, "磁力或 URL 无效", nil)
		}
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "magnet" && parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Scheme != "magnet" && (parsed.Host == "" || parsed.User != nil)) {
			return source, "", appError(CodeDownloadSourceInvalid, "仅支持 magnet、HTTP 或 HTTPS 下载源", err)
		}
		if parsed.Scheme == "magnet" && parsed.Query().Get("xt") == "" {
			return source, "", appError(CodeDownloadSourceInvalid, "磁力链接缺少资源标识", nil)
		}
		source.URL = raw
	case downloadpkg.SourcePan115Share:
		raw := strings.TrimSpace(input.URL)
		parsed, err := url.Parse(raw)
		host := ""
		if parsed != nil {
			host = strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		}
		validHost := host == "115.com" || strings.HasSuffix(host, ".115.com") || host == "115cdn.com" || strings.HasSuffix(host, ".115cdn.com")
		if len(raw) == 0 || len(raw) > 4096 || err != nil || parsed.Scheme != "https" || parsed.User != nil || !validHost {
			return source, "", appError(CodeDownloadSourceInvalid, "115 分享链接无效", err)
		}
		source.URL = raw
	case downloadpkg.SourceTorrent:
		filename := filepath.Base(strings.ReplaceAll(strings.TrimSpace(input.Filename), "\\", "/"))
		if len(input.Torrent) == 0 || len(input.Torrent) > downloadpkg.MaxTorrentBytes || !strings.EqualFold(filepath.Ext(filename), ".torrent") || input.Torrent[0] != 'd' {
			return source, "", appError(CodeDownloadTorrentInvalid, "种子文件必须是 4 MiB 以内的有效 .torrent 文件", nil)
		}
		source.Torrent = append([]byte(nil), input.Torrent...)
		source.Filename = filename
	default:
		return source, "", appError(CodeDownloadSourceInvalid, "请选择下载来源类型", nil)
	}
	fallbackName := "URL 下载"
	if source.Kind == downloadpkg.SourcePan115Share {
		fallbackName = "115 分享转存"
	}
	name := strings.Join(strings.Fields(requestedName), " ")
	if name == "" {
		if source.Filename != "" {
			name = strings.TrimSuffix(source.Filename, filepath.Ext(source.Filename))
		} else if strings.HasPrefix(source.URL, "magnet:") {
			name = "磁力下载"
		} else {
			name = fallbackName
		}
	}
	name, err := normalizeDownloadDisplayName(name, fallbackName)
	return source, name, err
}

func normalizeDownloadDisplayName(requestedName, fallback string) (string, error) {
	name := strings.Join(strings.Fields(requestedName), " ")
	if name == "" {
		name = fallback
	}
	if len([]rune(name)) > 256 || validatePublicText(name) != nil {
		return "", appError(CodeInvalidRequest, "下载任务名称无效", nil)
	}
	return name, nil
}

func downloadSourcePurpose(id string) string { return "download-task:" + id + ":source" }

func downloadTaskSummary(record models.DownloadTask, jobStatus string) DownloadTaskSummary {
	return DownloadTaskSummary{ID: record.ID, JobID: record.JobID, OwnerID: record.OwnerID, DownloaderID: record.DownloaderID, DownloaderName: record.DownloaderName, ProviderType: record.ProviderType, DisplayName: record.DisplayName, JobStatus: jobStatus, ProviderStatus: record.ProviderStatus, Phase: record.Phase, Progress: record.Progress, BytesCompleted: record.BytesCompleted, BytesTotal: record.BytesTotal, DownloadSpeed: record.DownloadSpeed, UploadSpeed: record.UploadSpeed, ETASeconds: record.ETASeconds, LastSampledAt: record.LastSampledAt, LastErrorCode: record.LastErrorCode, LastErrorMessage: record.LastErrorMessage, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, FinishedAt: record.FinishedAt, ProfileID: record.ProfileID, ProfileRevision: record.ProfileRevision, ScrapeStatus: record.ScrapeStatus, ScrapeTitle: record.ScrapeTitle, ScrapeMediaType: record.ScrapeMediaType, ScrapeCategory: record.ScrapeCategory, ScrapeTMDBID: record.ScrapeTMDBID, ScrapeConfidence: record.ScrapeConfidence, ManifestFiles: record.ManifestFileCount, TargetLibraryID: record.TargetLibraryID, TargetLibraryName: record.TargetLibraryName, TransferMode: record.TransferMode, ConflictPolicy: record.ConflictPolicy}
}

type DownloadWorker struct {
	service            *DownloadService
	pollInterval       time.Duration
	pan115PollInterval time.Duration
	heartbeatInterval  time.Duration
}

func NewDownloadWorker(service *DownloadService) *DownloadWorker {
	return &DownloadWorker{service: service, pollInterval: 2 * time.Second, pan115PollInterval: pan115DownloadFallbackPollInterval, heartbeatInterval: pan115DownloadHeartbeatInterval}
}

func (w *DownloadWorker) Run(ctx context.Context, runtime JobRuntime, job ClaimedJob) WorkerResult {
	if w.service != nil && w.service.pluginDownloads != nil {
		var payload downloadJobPayload
		if json.Unmarshal([]byte(job.Job.PayloadJSON), &payload) == nil && payload.DownloadTaskID != "" {
			var provider struct{ ProviderType string }
			if w.service.db.Model(&models.DownloadTask{}).Select("provider_type").First(&provider, "id = ?", payload.DownloadTaskID).Error == nil && provider.ProviderType == models.DownloaderTypePluginHTTP {
				return w.service.pluginDownloads.Run(ctx, runtime, job)
			}
		}
	}
	if recoveryTask, recovery, recoveryErr := w.completedRecognitionRecoveryTask(job); recoveryErr != nil {
		return w.failure(recoveryTask, recoveryErr)
	} else if recovery {
		if manifest, exists, snapshotErr := completedDownloadManifest(recoveryTask.CompletedManifestJSON); snapshotErr != nil {
			return w.failure(recoveryTask, snapshotErr)
		} else if exists {
			return w.runCompletedRecognitionRecovery(ctx, runtime, recoveryTask, manifest)
		}
	}
	started := time.Now()
	task, downloaderRecord, client, source, savePath, err := w.load(ctx, job)
	if err != nil {
		return w.failure(task, err)
	}
	operation := downloadOperation(downloaderRecord.Type, task.SourceOrigin)
	operation.Event(w.service.log.Info()).Str("task_id", task.ID).Str("downloader_id", downloaderRecord.ID).Str("provider_type", downloaderRecord.Type).Msg(operation.Message("开始执行"))
	if isCompletedRecognitionRecovery(task) {
		manifestClient, ok := client.(downloadpkg.ManifestClient)
		if !ok {
			return w.failure(task, appError("download_completion_manifest_unavailable", "已完成下载的文件清单不可用", nil))
		}
		manifest, manifestErr := manifestClient.Manifest(ctx, task.ProviderTaskID)
		if manifestErr != nil {
			return w.failure(task, manifestErr)
		}
		if persistErr := w.persistCompletedManifest(&task, manifest); persistErr != nil {
			return w.failure(task, persistErr)
		}
		return w.runCompletedRecognitionRecovery(ctx, runtime, task, manifest)
	}
	if err := w.resetFailedPan115ForExplicitRetry(ctx, &task, client, downloaderRecord.Type); err != nil {
		return w.failure(task, err)
	}
	if task.Phase == models.DownloadTaskStatusPaused && task.ProviderTaskID != "" {
		if err := client.Resume(ctx, task.ProviderTaskID); err != nil {
			return w.failure(task, err)
		}
	}
	if task.ProviderTaskID == "" {
		operation.Event(w.service.log.Info()).Str("task_id", task.ID).Str("downloader_id", downloaderRecord.ID).Msg(operation.Message("正在提交到下载器"))
		_ = w.service.db.Model(&task).Updates(map[string]any{"phase": models.DownloadTaskStatusSubmitting, "updated_at": time.Now().UTC()}).Error
		metadataOnly := downloaderRecord.Type == models.DownloaderTypeQBittorrent && source.Kind == downloadpkg.SourceURL && strings.HasPrefix(strings.ToLower(strings.TrimSpace(source.URL)), "magnet:")
		providerTask, err := client.Submit(ctx, downloadpkg.SubmitRequest{Source: source, SavePath: savePath, Tag: task.ProviderTag, MetadataOnly: metadataOnly, ProviderDirectoryID: task.StagingProviderDirectoryID})
		if err != nil {
			return w.failure(task, err)
		}
		if providerTask.ID == "" {
			return w.failure(task, downloadpkg.Error("downloader_response_invalid", false, nil))
		}
		task.ProviderTaskID = providerTask.ID
		phase := models.DownloadTaskStatusDownloading
		if metadataOnly {
			phase = models.DownloadTaskStatusMetadata
		}
		task.Phase = phase
		if err := w.service.db.Model(&task).Updates(map[string]any{"provider_task_id": task.ProviderTaskID, "phase": phase, "provider_status": providerTask.Status, "updated_at": time.Now().UTC()}).Error; err != nil {
			return WorkerResult{ErrorCode: "download_state_persist_failed", ErrorMessage: "下载任务状态保存失败"}
		}
		operation.Event(w.service.log.Info()).Str("task_id", task.ID).Str("downloader_id", downloaderRecord.ID).Str("provider_status", providerTask.Status).Msg(operation.Message("已提交到下载器"))
	}
	if metadataClient, ok := client.(downloadpkg.MetadataClient); ok && task.ScrapeStatus != "classified" && task.ScrapeStatus != "fallback_unrecognized" {
		result := w.prepareClassification(ctx, runtime, &task, metadataClient, savePath)
		if result != nil {
			return *result
		}
	}
	connectionID, eventDriven := w.pan115ConnectionID(ctx, downloaderRecord)
	var eventGeneration uint64
	if eventDriven {
		eventGeneration, _ = w.service.providerEvents.snapshot(connectionID)
	}
	for {
		providerTask, err := client.Get(ctx, task.ProviderTaskID)
		if err != nil {
			if code, _ := downloadpkg.ErrorInfo(err); code == "downloader_task_not_found" && strings.HasPrefix(task.ProviderTaskID, "tag:") {
				return w.failureRetryable(task, err)
			}
			return w.failure(task, err)
		}
		if providerTask.ID != "" && providerTask.ID != task.ProviderTaskID {
			task.ProviderTaskID = providerTask.ID
		}
		if err := w.persistTelemetry(&task, providerTask); err != nil {
			return WorkerResult{ErrorCode: "download_state_persist_failed", ErrorMessage: "下载任务状态保存失败"}
		}
		progress := providerTask.Progress
		var speed *float64
		if providerTask.DownloadSpeed != nil {
			value := float64(*providerTask.DownloadSpeed)
			speed = &value
		}
		if err := runtime.Heartbeat(progress, providerTask.BytesCompleted, providerTask.BytesTotal, speed, providerTask.ETASeconds); err != nil {
			return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "下载任务租约已失效"}
		}
		if providerTask.Completed {
			operation.Event(w.service.log.Info()).Str("task_id", task.ID).Str("downloader_id", downloaderRecord.ID).Msg(operation.Message("下载器已报告完成，开始复核文件清单"))
			var completedManifest *downloadpkg.Manifest
			var completedSourceManifest *downloadpkg.Manifest
			if manifestClient, ok := client.(downloadpkg.ManifestClient); ok {
				_ = w.service.db.Model(&task).Updates(map[string]any{"phase": models.DownloadTaskStatusVerifying, "updated_at": time.Now().UTC()}).Error
				manifest, manifestErr := manifestClient.Manifest(ctx, task.ProviderTaskID)
				if manifestErr != nil {
					if _, retryable := downloadpkg.ErrorInfo(manifestErr); retryable {
						return w.failureRetryable(task, manifestErr)
					}
					_ = w.service.db.Model(&task).Updates(map[string]any{"scrape_status": "completed_unverified", "last_error_code": "downloader_manifest_invalid", "last_error_message": "下载完成，但文件清单复核失败", "updated_at": time.Now().UTC()}).Error
				} else if persistErr := w.persistCompletedManifest(&task, manifest); persistErr != nil {
					return w.failure(task, persistErr)
				} else if selectedManifest, verifyErr := w.verifyCompleted(ctx, &task, manifest); verifyErr != nil {
					if task.TargetLibraryID != nil {
						return w.failure(task, verifyErr)
					}
				} else {
					completedManifest = &selectedManifest
					completedSourceManifest = &manifest
				}
			}
			if task.TargetLibraryID != nil {
				if completedManifest == nil {
					return w.failure(task, appError("transfer_manifest_unavailable", "下载完成但无法取得入库文件清单", nil))
				}
				if w.service.transfers == nil {
					return w.failure(task, appError("transfer_service_unavailable", "入库服务不可用", nil))
				}
				if completedSourceManifest == nil {
					return w.failure(task, appError("transfer_manifest_unavailable", "下载完成但完整文件清单不可用", nil))
				}
				if err := w.service.transfers.EnqueuePackage(task, *completedManifest, *completedSourceManifest); err != nil {
					return w.failureRetryable(task, err)
				}
			}
			now := time.Now().UTC()
			_ = w.service.db.Model(&task).Updates(map[string]any{"phase": models.DownloadTaskStatusCompleted, "finished_at": now, "updated_at": now}).Error
			operation.Event(w.service.log.Info()).Str("task_id", task.ID).Str("downloader_id", downloaderRecord.ID).Bool("transfer_enqueued", task.TargetLibraryID != nil).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("完成"))
			return WorkerResult{}
		}
		if providerTask.Failed {
			code := providerTask.ErrorCode
			if code == "" {
				code = "downloader_provider_failed"
			}
			_ = w.markFailure(task, code, "下载器报告任务失败", true)
			operation.Event(w.service.log.Error()).Str("task_id", task.ID).Str("downloader_id", downloaderRecord.ID).Str("error_code", code).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(operation.Message("下载器报告任务失败"))
			return WorkerResult{ErrorCode: code, ErrorMessage: "下载器报告任务失败"}
		}
		if eventDriven {
			if err := w.waitForPan115Poll(ctx, runtime, connectionID, &eventGeneration, task.ID); err != nil {
				if ctx.Err() != nil {
					return WorkerResult{}
				}
				return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "下载任务租约已失效"}
			}
			continue
		}
		select {
		case <-ctx.Done():
			return WorkerResult{}
		case <-time.After(w.pollInterval):
		}
	}
}

func (w *DownloadWorker) completedRecognitionRecoveryTask(job ClaimedJob) (models.DownloadTask, bool, error) {
	var payload downloadJobPayload
	if err := json.Unmarshal([]byte(job.Job.PayloadJSON), &payload); err != nil || payload.DownloadTaskID == "" {
		return models.DownloadTask{}, false, appError(CodeInvalidRequest, "下载任务参数无效", err)
	}
	var task models.DownloadTask
	if err := w.service.db.First(&task, "id = ?", payload.DownloadTaskID).Error; err != nil {
		return task, false, err
	}
	return task, isCompletedRecognitionRecovery(task), nil
}

func isCompletedRecognitionRecovery(task models.DownloadTask) bool {
	return task.ScrapeStatus == "completed_unrecognized" && task.ProviderTaskID != "" && task.TargetLibraryID != nil
}

func (w *DownloadWorker) runCompletedRecognitionRecovery(ctx context.Context, runtime JobRuntime, task models.DownloadTask, manifest downloadpkg.Manifest) WorkerResult {
	if task.StagingCategory == "" && strings.TrimSpace(task.ScrapeCategory) != "" {
		task.StagingCategory = task.ScrapeCategory
		if err := w.service.db.Model(&task).Updates(map[string]any{"staging_category": task.StagingCategory, "updated_at": time.Now().UTC()}).Error; err != nil {
			return WorkerResult{ErrorCode: "download_state_persist_failed", ErrorMessage: "下载完成目录快照保存失败"}
		}
	}
	if err := runtime.Heartbeat(task.Progress, task.BytesCompleted, task.BytesTotal, nil, nil); err != nil {
		return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "下载任务租约已失效"}
	}
	selected, err := w.verifyCompleted(ctx, &task, manifest)
	if err != nil {
		return w.failure(task, err)
	}
	if w.service.transfers == nil {
		return w.failure(task, appError("transfer_service_unavailable", "入库服务不可用", nil))
	}
	if err := w.service.transfers.EnqueuePackage(task, selected, manifest); err != nil {
		return w.failureRetryable(task, err)
	}
	now := time.Now().UTC()
	if err := w.service.db.Model(&task).Updates(map[string]any{"phase": models.DownloadTaskStatusCompleted, "last_error_code": "", "last_error_message": "", "finished_at": now, "updated_at": now}).Error; err != nil {
		return WorkerResult{ErrorCode: "download_state_persist_failed", ErrorMessage: "识别恢复状态保存失败"}
	}
	serverlog.OperationDownloadClassification.Event(w.service.log.Info()).Str("task_id", task.ID).Int("manifest_files", len(manifest.Files)).Int("selected_files", len(selected.Files)).Msg(serverlog.OperationDownloadClassification.Message("已复用完成文件清单继续入库"))
	return WorkerResult{}
}

func (w *DownloadWorker) persistCompletedManifest(task *models.DownloadTask, manifest downloadpkg.Manifest) error {
	raw, err := encodeCompletedDownloadManifest(manifest)
	if err != nil {
		return err
	}
	if err := w.service.db.Model(task).Updates(map[string]any{"completed_manifest_json": raw, "updated_at": time.Now().UTC()}).Error; err != nil {
		return appError("download_state_persist_failed", "下载完成文件清单保存失败", err)
	}
	task.CompletedManifestJSON = raw
	return nil
}

func encodeCompletedDownloadManifest(manifest downloadpkg.Manifest) (string, error) {
	if !manifest.Complete || len(manifest.Files) == 0 || len(manifest.Files) > maxCompletedManifestFiles {
		return "", appError("download_completion_manifest_invalid", "下载完成文件清单无效", nil)
	}
	seen := make(map[string]struct{}, len(manifest.Files))
	for _, file := range manifest.Files {
		key := transferCleanupFileKey(file)
		if file.Size < 0 || key == "" {
			return "", appError("download_completion_manifest_invalid", "下载完成文件清单无效", nil)
		}
		if _, duplicate := seen[key]; duplicate {
			return "", appError("download_completion_manifest_invalid", "下载完成文件清单包含重复文件", nil)
		}
		seen[key] = struct{}{}
	}
	raw, err := json.Marshal(manifest)
	if err != nil || len(raw) > maxCompletedManifestBytes {
		return "", appError("download_completion_manifest_invalid", "下载完成文件清单过大", err)
	}
	return string(raw), nil
}

func completedDownloadManifest(raw string) (downloadpkg.Manifest, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return downloadpkg.Manifest{}, false, nil
	}
	if len(raw) > maxCompletedManifestBytes {
		return downloadpkg.Manifest{}, true, appError("download_completion_manifest_invalid", "下载完成文件清单过大", nil)
	}
	var manifest downloadpkg.Manifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return manifest, true, appError("download_completion_manifest_invalid", "下载完成文件清单无效", err)
	}
	canonical, err := encodeCompletedDownloadManifest(manifest)
	if err != nil {
		return manifest, true, err
	}
	if canonical != raw {
		return manifest, true, appError("download_completion_manifest_invalid", "下载完成文件清单不是规范格式", nil)
	}
	return manifest, true, nil
}

func (w *DownloadWorker) resetFailedPan115ForExplicitRetry(ctx context.Context, task *models.DownloadTask, client downloadpkg.Client, providerType string) error {
	if providerType != models.DownloaderTypePan115Offline || task.Phase != models.DownloadTaskStatusFailed || task.LastErrorCode != "downloader_provider_failed" || task.ProviderTaskID == "" {
		return nil
	}
	serverlog.OperationPan115OfflineDownload.Event(w.service.log.Info()).Str("task_id", task.ID).Msg(serverlog.OperationPan115OfflineDownload.Message("用户重试失败的离线任务，正在清理旧任务记录"))
	if err := client.Cancel(ctx, task.ProviderTaskID, false); err != nil && !providerTaskMissing(err) {
		return err
	}
	updates := map[string]any{
		"provider_task_id":   "",
		"provider_output_id": "",
		"provider_status":    "",
		"phase":              models.DownloadTaskStatusQueued,
		"progress":           nil,
		"bytes_completed":    nil,
		"bytes_total":        nil,
		"download_speed":     nil,
		"upload_speed":       nil,
		"eta_seconds":        nil,
		"last_sampled_at":    nil,
		"last_error_code":    "",
		"last_error_message": "",
		"finished_at":        nil,
		"updated_at":         time.Now().UTC(),
	}
	if err := w.service.db.Model(task).Updates(updates).Error; err != nil {
		return appError("download_state_persist_failed", "下载任务重试状态保存失败", err)
	}
	task.ProviderTaskID, task.ProviderOutputID, task.ProviderStatus = "", "", ""
	task.Phase, task.Progress, task.BytesCompleted, task.BytesTotal = models.DownloadTaskStatusQueued, nil, nil, nil
	task.DownloadSpeed, task.UploadSpeed, task.ETASeconds, task.LastSampledAt = nil, nil, nil, nil
	task.LastErrorCode, task.LastErrorMessage, task.FinishedAt = "", "", nil
	return nil
}

func (w *DownloadWorker) pan115ConnectionID(ctx context.Context, record models.Downloader) (uint, bool) {
	if record.Type != models.DownloaderTypePan115Offline || record.StorageID == nil || w.service == nil || w.service.providerEvents == nil {
		return 0, false
	}
	var storage models.Storage
	if err := w.service.db.WithContext(ctx).Select("connection_id").First(&storage, *record.StorageID).Error; err != nil || storage.ConnectionID == nil || *storage.ConnectionID == 0 {
		return 0, false
	}
	return *storage.ConnectionID, true
}

func (w *DownloadWorker) waitForPan115Poll(ctx context.Context, runtime JobRuntime, connectionID uint, generation *uint64, taskID string) error {
	pollInterval := w.pan115PollInterval
	if pollInterval <= 0 {
		pollInterval = pan115DownloadFallbackPollInterval
	}
	heartbeatInterval := w.heartbeatInterval
	if heartbeatInterval <= 0 || heartbeatInterval >= pollInterval {
		heartbeatInterval = pollInterval / 2
	}
	if heartbeatInterval <= 0 {
		heartbeatInterval = time.Second
	}
	poll := time.NewTimer(pollInterval)
	heartbeat := time.NewTicker(heartbeatInterval)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		current, wake := w.service.providerEvents.snapshot(connectionID)
		if current != *generation {
			*generation = current
			serverlog.OperationPan115OfflineDownload.Event(w.service.log.Info()).Str("task_id", taskID).Uint("connection_id", connectionID).Msg(serverlog.OperationPan115OfflineDownload.Message("收到生活事件，立即复核离线任务状态"))
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
			current, _ = w.service.providerEvents.snapshot(connectionID)
			*generation = current
			serverlog.OperationPan115OfflineDownload.Event(w.service.log.Info()).Str("task_id", taskID).Uint("connection_id", connectionID).Msg(serverlog.OperationPan115OfflineDownload.Message("收到生活事件，立即复核离线任务状态"))
			return nil
		case <-poll.C:
			serverlog.OperationPan115OfflineDownload.Event(w.service.log.Debug()).Str("task_id", taskID).Uint("connection_id", connectionID).Msg(serverlog.OperationPan115OfflineDownload.Message("补偿轮询到期，复核离线任务状态"))
			return nil
		case <-heartbeat.C:
			if err := runtime.Heartbeat(nil, nil, nil, nil, nil); err != nil {
				return err
			}
		}
	}
}

func (w *DownloadWorker) prepareClassification(ctx context.Context, runtime JobRuntime, task *models.DownloadTask, client downloadpkg.MetadataClient, savePath string) *WorkerResult {
	started := time.Now()
	serverlog.OperationDownloadClassification.Event(w.service.log.Info()).Str("task_id", task.ID).Msg(serverlog.OperationDownloadClassification.Message("等待下载器元数据"))
	for {
		manifest, err := client.Manifest(ctx, task.ProviderTaskID)
		if err != nil {
			if code, _ := downloadpkg.ErrorInfo(err); code == "downloader_task_not_found" && strings.HasPrefix(task.ProviderTaskID, "tag:") {
				reconcileErr := downloadpkg.Error("downloader_submission_unconfirmed", true, err)
				result := w.failureRetryable(*task, reconcileErr)
				return &result
			}
			result := w.failure(*task, err)
			return &result
		}
		if manifest.Complete && len(manifest.Files) > 0 {
			serverlog.OperationDownloadClassification.Event(w.service.log.Info()).Str("task_id", task.ID).Int("manifest_files", len(manifest.Files)).Msg(serverlog.OperationDownloadClassification.Message("文件清单已就绪，开始识别"))
			if err := client.Pause(ctx, task.ProviderTaskID); err != nil {
				result := w.failure(*task, err)
				return &result
			}
			_ = w.service.db.Model(task).Updates(map[string]any{"phase": models.DownloadTaskStatusClassifying, "manifest_file_count": len(manifest.Files), "updated_at": time.Now().UTC()}).Error
			match, matchErr := w.classify(ctx, *task, manifest)
			if matchErr == nil && match.Confident {
				// Persist the match before provider routing, but do not mark it as
				// classified until category assignment and resume both succeed. A
				// retry must re-enter this phase after any provider-side failure.
				if err := w.persistScrape(task, match, "matched", len(manifest.Files)); err != nil {
					result := WorkerResult{ErrorCode: "download_state_persist_failed", ErrorMessage: "刮削结果保存失败"}
					return &result
				}
				if err := w.routeCategory(ctx, task, client, savePath, match.Category, "classified", "", ""); err != nil {
					result := w.failure(*task, err)
					return &result
				}
				if err := runtime.Checkpoint(map[string]any{"stage": "categorized"}); err != nil {
					result := WorkerResult{ErrorCode: "download_state_persist_failed", ErrorMessage: "下载任务检查点保存失败"}
					return &result
				}
				serverlog.OperationDownloadClassification.Event(w.service.log.Info()).Str("task_id", task.ID).Str("media_type", match.MediaType).Str("category", match.Category).Int("manifest_files", len(manifest.Files)).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationDownloadClassification.Message("识别完成并已设置下载器分类"))
				return nil
			}
			code := classificationFallbackCode(matchErr, match)
			message := classificationFallbackMessage(code)
			match.Category = "未识别"
			if err := w.persistScrape(task, match, "fallback_unrecognized", len(manifest.Files)); err != nil {
				result := WorkerResult{ErrorCode: "download_state_persist_failed", ErrorMessage: "刮削降级结果保存失败"}
				return &result
			}
			serverlog.OperationDownloadClassification.Event(w.service.log.Warn()).Str("task_id", task.ID).Str("reason_code", code).Str("credential_source", match.CredentialSource).Str("credential_kind", match.CredentialKind).Int("manifest_files", len(manifest.Files)).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationDownloadClassification.Message("未达到可信阈值，已归入未识别分类"))
			if err := w.routeCategory(ctx, task, client, savePath, match.Category, "fallback_unrecognized", code, message); err != nil {
				result := w.failure(*task, err)
				return &result
			}
			if err := runtime.Checkpoint(map[string]any{"stage": "categorized", "fallback_reason": code}); err != nil {
				result := WorkerResult{ErrorCode: "download_state_persist_failed", ErrorMessage: "下载任务检查点保存失败"}
				return &result
			}
			return nil
		}
		if err := runtime.Heartbeat(nil, nil, nil, nil, nil); err != nil {
			result := WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "下载任务租约已失效"}
			return &result
		}
		select {
		case <-ctx.Done():
			result := WorkerResult{}
			return &result
		case <-time.After(w.pollInterval):
		}
	}
}

type scrapeMatch struct {
	Title            string
	MediaType        string
	Category         string
	TMDBID           *int64
	Confidence       *float64
	Year             *int
	Confident        bool
	CredentialSource string
	CredentialKind   string
}

var downloadYearPattern = regexp.MustCompile(`(?:^|[. _(-])((?:19|20)\d{2})(?:[. _)-]|$)`)

func (w *DownloadWorker) classify(ctx context.Context, task models.DownloadTask, manifest downloadpkg.Manifest) (scrapeMatch, error) {
	rules, err := classification.DecodeStrict([]byte(task.ProfileRulesJSON))
	if err != nil {
		return scrapeMatch{}, appError(CodeProfileValidation, "媒体分类规则快照无效", err)
	}
	_, recognitionRules, err := canonicalRecognitionRules([]byte(firstNonEmpty(task.ProfileRecognitionRulesJSON, "[]")))
	if err != nil {
		return scrapeMatch{}, appError(CodeProfileValidation, "媒体识别规则快照无效", err)
	}
	packCodes, err := parseBuiltinPackCodes(task.ProfileBuiltinRecognitionPacksJSON)
	if err != nil {
		return scrapeMatch{}, appError(CodeProfileValidation, "内置媒体识别词包快照无效", err)
	}
	if len(manifest.Files) == 0 {
		return scrapeMatch{}, appError(CodeTMDBUnavailable, "文件清单中没有可识别的视频文件", nil)
	}
	result := scrapeMatch{CredentialSource: "none"}
	var client mediaRecognitionLookup
	if w.service.metadata != nil {
		metadataClient, source, kind, clientErr := w.service.metadata.clientWithCredentialInfo()
		result.CredentialSource, result.CredentialKind = source, kind
		if clientErr == nil {
			client = metadataClient
		}
	}
	if task.RecognitionOverrideTMDBID != nil && task.RecognitionOverrideMediaType != "" {
		if client == nil {
			return result, appError(mediaRecognitionCredentialMissing, classificationFallbackMessage(mediaRecognitionCredentialMissing), nil)
		}
		language, _ := w.service.downloadRecognitionLocale(task)
		verified, verifyErr := client.GetByID(ctx, task.RecognitionOverrideMediaType, *task.RecognitionOverrideTMDBID, language)
		if verifyErr != nil {
			return result, appError(tmdb.ErrorCode(verifyErr), "TMDB 人工匹配验证失败", nil)
		}
		metadata := classification.Metadata{
			MediaType:           classification.MediaType(verified.MediaType),
			GenreIDs:            append([]int(nil), verified.GenreIDs...),
			OriginalLanguage:    verified.OriginalLanguage,
			ProductionCountries: append([]string(nil), verified.ProductionCountries...),
			OriginCountries:     append([]string(nil), verified.OriginCountries...),
			ReleaseYear:         cloneInt(verified.ReleaseYear),
		}
		classified := classification.Classify(metadata, rules)
		result.Title = verified.Title
		result.MediaType = verified.MediaType
		result.Category = classified.CategoryName
		result.TMDBID = cloneInt64(&verified.ID)
		result.Confidence = cloneFloat64(&verified.Confidence)
		result.Year = cloneInt(verified.ReleaseYear)
		// A persisted override is not a fuzzy match: GetByID has just re-fetched
		// and validated the authoritative identity. Require a complete verified
		// projection instead of applying the automatic-ranking threshold again.
		result.Confident = verified.ID == *task.RecognitionOverrideTMDBID && verified.MediaType == task.RecognitionOverrideMediaType && strings.TrimSpace(result.Title) != "" && strings.TrimSpace(result.Category) != "" && result.Confidence != nil
		if !result.Confident {
			return result, appError(mediaRecognitionLowConfidence, "TMDB 人工匹配验证结果不完整", nil)
		}
		return result, nil
	}
	files := make([]recognitionSourceFile, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		files = append(files, recognitionSourceFile{RelativePath: file.RelativePath, Size: file.Size})
	}
	language, region := w.service.downloadRecognitionLocale(task)
	recognized := recognizeMedia(ctx, client, MediaRecognitionRequest{PackageName: manifest.Name, Files: files, SourceKind: mediarecognition.SourceDownload, MediaTypeHint: task.ScrapeMediaType, YearHint: task.ScrapeYear, BuiltinPackCodes: packCodes, RecognitionRules: recognitionRules, Classification: rules, Language: language, Region: region})
	result.Title, result.MediaType, result.Category = recognized.Title, recognized.MediaType, recognized.CategoryName
	result.TMDBID, result.Confidence, result.Year = recognized.TMDBID, recognized.Confidence, recognized.ReleaseYear
	result.Confident = recognized.Status == mediaRecognitionStatusMatched && recognized.MatchedRuleID != nil && recognized.CategoryName != ""
	if recognized.ErrorCode != "" {
		return result, appError(recognized.ErrorCode, classificationFallbackMessage(recognized.ErrorCode), nil)
	}
	return result, nil
}

func (w *DownloadWorker) persistScrape(task *models.DownloadTask, match scrapeMatch, status string, files int) error {
	updates := map[string]any{"scrape_status": status, "scrape_title": safeLabel(match.Title, 256), "scrape_media_type": safeLabel(match.MediaType, 16), "scrape_category": safeLabel(match.Category, 128), "scrape_tmdb_id": match.TMDBID, "scrape_confidence": match.Confidence, "scrape_year": match.Year, "manifest_file_count": files, "last_error_code": "", "last_error_message": "", "updated_at": time.Now().UTC()}
	if err := w.service.db.Model(task).Updates(updates).Error; err != nil {
		return err
	}
	task.ScrapeStatus = status
	task.ScrapeTitle = safeLabel(match.Title, 256)
	task.ScrapeMediaType = safeLabel(match.MediaType, 16)
	task.ScrapeCategory = safeLabel(match.Category, 128)
	task.ScrapeTMDBID = match.TMDBID
	task.ScrapeConfidence = match.Confidence
	task.ScrapeYear = match.Year
	task.ManifestFileCount = files
	task.LastErrorCode = ""
	task.LastErrorMessage = ""
	return nil
}

func (w *DownloadWorker) routeCategory(ctx context.Context, task *models.DownloadTask, client downloadpkg.MetadataClient, savePath, category, scrapeStatus, errorCode, errorMessage string) error {
	category = strings.Join(strings.Fields(category), " ")
	if category == "" || len([]rune(category)) > 128 || strings.ContainsAny(category, `/\\:\r\n`) {
		return downloadpkg.Error("downloader_category_invalid", false, nil)
	}
	categoryPath := filepath.Join(savePath, category)
	relative, err := filepath.Rel(filepath.Clean(savePath), filepath.Clean(categoryPath))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return downloadpkg.Error("downloader_category_outside_staging", false, err)
	}
	if info, statErr := os.Lstat(categoryPath); statErr == nil {
		if !info.IsDir() {
			return downloadpkg.Error("downloader_category_outside_staging", false, nil)
		}
		resolved, resolveErr := medialibrary.ResolveRoot(savePath, "/"+category)
		if resolveErr != nil || !providerPathsEqual(resolved, categoryPath) {
			return downloadpkg.Error("downloader_category_outside_staging", false, resolveErr)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return downloadpkg.Error("downloader_category_outside_staging", false, statErr)
	}
	categories, err := client.Categories(ctx)
	if err != nil {
		return err
	}
	for _, existing := range categories {
		if strings.EqualFold(existing.Name, category) && (existing.SavePath == "" || !providerPathsEqual(existing.SavePath, categoryPath)) {
			return downloadpkg.Error("downloader_category_outside_staging", false, nil)
		}
	}
	if err := client.EnsureCategory(ctx, category, categoryPath); err != nil {
		return err
	}
	if err := client.SetCategory(ctx, task.ProviderTaskID, category, categoryPath); err != nil {
		return err
	}
	if err := client.Resume(ctx, task.ProviderTaskID); err != nil {
		return err
	}
	task.ScrapeStatus, task.ScrapeCategory, task.StagingCategory, task.Phase = scrapeStatus, category, category, models.DownloadTaskStatusDownloading
	return w.service.db.Model(task).Updates(map[string]any{"phase": task.Phase, "scrape_status": task.ScrapeStatus, "scrape_category": category, "staging_category": category, "last_error_code": safeLabel(errorCode, 96), "last_error_message": safeLabel(errorMessage, 512), "updated_at": time.Now().UTC()}).Error
}

func classificationFallbackCode(err error, match scrapeMatch) string {
	if err != nil {
		code := ErrorCode(err)
		if code != "INTERNAL_ERROR" {
			return code
		}
	}
	if match.TMDBID != nil && !match.Confident {
		return "tmdb_low_confidence"
	}
	return tmdb.ErrorNoMatch
}

func classificationFallbackMessage(code string) string {
	switch code {
	case "tmdb_credential_unavailable", CodeTMDBTokenInvalid:
		return "已自动归入未识别：TMDB 未配置"
	case tmdb.ErrorAuthFailed:
		return "已自动归入未识别：TMDB 凭据认证失败"
	case tmdb.ErrorNetworkUnavailable:
		return "已自动归入未识别：TMDB 网络不可用"
	case tmdb.ErrorNoMatch:
		return "已自动归入未识别：TMDB 无匹配结果"
	case "tmdb_low_confidence":
		return "已自动归入未识别：匹配置信度不足"
	case tmdb.ErrorInvalidRequest:
		return "已自动归入未识别：标题无法识别"
	default:
		return "已自动归入未识别：TMDB 响应不可用"
	}
}

func providerPathsEqual(left, right string) bool {
	leftKind, leftPath, leftOK := normalizeProviderPath(left)
	rightKind, rightPath, rightOK := normalizeProviderPath(right)
	if !leftOK || !rightOK || leftKind != rightKind {
		return false
	}
	if leftKind == "windows" {
		return strings.EqualFold(leftPath, rightPath)
	}
	return leftPath == rightPath
}

func normalizeProviderPath(value string) (string, string, bool) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", "", false
	}
	kind := "unix"
	if len(value) >= 3 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':' && value[2] == '/' {
		kind = "windows"
	} else if strings.HasPrefix(value, "//") {
		kind = "windows"
	} else if !strings.HasPrefix(value, "/") {
		return "", "", false
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", "", false
	}
	return kind, strings.TrimSuffix(cleaned, "/"), true
}

func (w *DownloadWorker) verifyCompleted(ctx context.Context, task *models.DownloadTask, manifest downloadpkg.Manifest) (downloadpkg.Manifest, error) {
	match, err := w.classify(ctx, *task, manifest)
	if err != nil || !match.Confident {
		code := classificationFallbackCode(err, match)
		if persistErr := w.persistScrape(task, match, "completed_unrecognized", len(manifest.Files)); persistErr != nil {
			return downloadpkg.Manifest{}, appError("download_state_persist_failed", "完成后刮削结果保存失败", persistErr)
		}
		serverlog.OperationDownloadClassification.Event(w.service.log.Warn()).Str("task_id", task.ID).Str("reason_code", code).Str("credential_source", match.CredentialSource).Str("credential_kind", match.CredentialKind).Int("manifest_files", len(manifest.Files)).Msg(serverlog.OperationDownloadClassification.Message("下载包未识别，已阻止自动入库"))
		return downloadpkg.Manifest{}, appError(CodeTransferMediaUnrecognized, "下载已完成，但媒体未识别，未自动入库；请修正识别条件后重试", nil)
	}
	selected, err := selectDownloadPackageManifest(manifest, match.MediaType)
	if err != nil {
		if persistErr := w.persistScrape(task, match, "completed_unrecognized", len(manifest.Files)); persistErr != nil {
			return downloadpkg.Manifest{}, appError("download_state_persist_failed", "完成后刮削结果保存失败", persistErr)
		}
		return downloadpkg.Manifest{}, appError(CodeTransferMediaUnrecognized, "下载已完成，但没有找到可信主媒体，未自动入库", nil)
	}
	if err := w.persistScrape(task, match, "completed_verified", len(selected.Files)); err != nil {
		return downloadpkg.Manifest{}, appError("download_state_persist_failed", "完成后刮削结果保存失败", err)
	}
	serverlog.OperationDownloadClassification.Event(w.service.log.Info()).Str("task_id", task.ID).Str("media_type", match.MediaType).Int("manifest_files", len(manifest.Files)).Int("selected_files", len(selected.Files)).Int("excluded_files", len(manifest.Files)-len(selected.Files)).Msg(serverlog.OperationDownloadClassification.Message("下载包识别完成，已生成安全入库清单"))
	return selected, nil
}

func (w *DownloadWorker) Interrupt(ctx context.Context, job ClaimedJob, action string) error {
	var task models.DownloadTask
	if err := w.service.db.First(&task, "job_id = ?", job.Job.ID).Error; err != nil {
		return err
	}
	if task.ProviderType == models.DownloaderTypePluginHTTP && w.service.pluginDownloads != nil {
		return w.service.pluginDownloads.Interrupt(ctx, job, action)
	}
	if task.ProviderTaskID == "" {
		updates := map[string]any{"phase": map[string]string{"pause": models.DownloadTaskStatusPaused, "cancel": models.DownloadTaskStatusCancelled}[action], "updated_at": time.Now().UTC()}
		if action == "cancel" {
			updates["last_error_code"], updates["last_error_message"], updates["scrape_status"] = "", "", ""
		}
		err := w.service.db.Model(&task).Updates(updates).Error
		if err == nil {
			serverlog.OperationDownloadTask.Event(w.service.log.Info()).Str("task_id", task.ID).Str("action", action).Msg(serverlog.OperationDownloadTask.Message("控制操作已完成"))
		}
		return err
	}
	if task.DownloaderID == nil {
		return appError(CodeDownloaderUnavailable, "下载器配置已不存在", nil)
	}
	_, client, err := w.service.downloader.client(*task.DownloaderID)
	if err != nil {
		return err
	}
	switch action {
	case "pause":
		err = client.Pause(ctx, task.ProviderTaskID)
		if err == nil {
			err = w.service.db.Model(&task).Updates(map[string]any{"phase": models.DownloadTaskStatusPaused, "updated_at": time.Now().UTC()}).Error
		}
	case "cancel":
		err = client.Cancel(ctx, task.ProviderTaskID, true)
		if err == nil || providerTaskMissing(err) {
			now := time.Now().UTC()
			err = w.service.db.Model(&task).Updates(map[string]any{"phase": models.DownloadTaskStatusCancelled, "last_error_code": "", "last_error_message": "", "scrape_status": "", "finished_at": now, "updated_at": now}).Error
		}
	default:
		err = appError(CodeInvalidRequest, "未知下载控制操作", nil)
	}
	if err == nil {
		serverlog.OperationDownloadTask.Event(w.service.log.Info()).Str("task_id", task.ID).Str("action", action).Msg(serverlog.OperationDownloadTask.Message("下载器控制操作已完成"))
	} else {
		serverlog.OperationDownloadTask.Event(w.service.log.Warn()).Str("task_id", task.ID).Str("action", action).Str("error_code", ErrorCode(err)).Msg(serverlog.OperationDownloadTask.Message("下载器控制操作失败"))
	}
	return err
}

func (w *DownloadWorker) load(ctx context.Context, job ClaimedJob) (models.DownloadTask, models.Downloader, downloadpkg.Client, downloadpkg.Source, string, error) {
	var payload downloadJobPayload
	if err := json.Unmarshal([]byte(job.Job.PayloadJSON), &payload); err != nil || payload.DownloadTaskID == "" {
		return models.DownloadTask{}, models.Downloader{}, nil, downloadpkg.Source{}, "", appError(CodeInvalidRequest, "下载任务参数无效", err)
	}
	var task models.DownloadTask
	if err := w.service.db.First(&task, "id = ?", payload.DownloadTaskID).Error; err != nil {
		return task, models.Downloader{}, nil, downloadpkg.Source{}, "", err
	}
	if task.DownloaderID == nil {
		return task, models.Downloader{}, nil, downloadpkg.Source{}, "", appError(CodeDownloaderUnavailable, "下载器配置已不存在", nil)
	}
	record, client, err := w.service.downloader.client(*task.DownloaderID)
	if err != nil {
		return task, record, nil, downloadpkg.Source{}, "", err
	}
	if !record.Enabled {
		return task, record, nil, downloadpkg.Source{}, "", appError(CodeDownloaderUnavailable, "下载器已停用", nil)
	}
	savePath, err := w.service.settings.ResolveSnapshot(ctx, record.Type, task.StagingAbsolutePath, task.StagingStorageID, task.StagingRelativePath)
	if err != nil {
		return task, record, nil, downloadpkg.Source{}, "", err
	}
	plaintext, err := w.service.credentials.Decrypt(downloadSourcePurpose(task.ID), task.SourceCiphertext)
	if err != nil {
		return task, record, nil, downloadpkg.Source{}, "", err
	}
	var source downloadSourceEnvelope
	if err := json.Unmarshal([]byte(plaintext), &source); err != nil {
		return task, record, nil, downloadpkg.Source{}, "", err
	}
	return task, record, client, downloadpkg.Source{Kind: source.Kind, URL: source.URL, Torrent: source.Torrent, Filename: source.Filename, ProviderItemID: source.ProviderItemID}, savePath, nil
}

func (w *DownloadWorker) persistTelemetry(task *models.DownloadTask, provider downloadpkg.Task) error {
	now := time.Now().UTC()
	phase := models.DownloadTaskStatusDownloading
	if provider.Completed {
		phase = models.DownloadTaskStatusVerifying
	} else if provider.Failed {
		phase = models.DownloadTaskStatusFailed
	}
	updates := map[string]any{"provider_task_id": task.ProviderTaskID, "provider_output_id": safeLabel(provider.OutputItemID, 128), "provider_status": safeLabel(provider.Status, 64), "phase": phase, "progress": provider.Progress, "bytes_completed": provider.BytesCompleted, "bytes_total": provider.BytesTotal, "download_speed": provider.DownloadSpeed, "upload_speed": provider.UploadSpeed, "eta_seconds": provider.ETASeconds, "last_sampled_at": now, "updated_at": now}
	if err := w.service.db.Model(task).Updates(updates).Error; err != nil {
		return err
	}
	task.Phase, task.ProviderStatus, task.ProviderOutputID, task.LastSampledAt = phase, provider.Status, provider.OutputItemID, &now
	return nil
}

func (w *DownloadWorker) failure(task models.DownloadTask, err error) WorkerResult {
	code, retryable := downloadpkg.ErrorInfo(err)
	var applicationError *AppError
	if errors.As(err, &applicationError) {
		code, retryable = ErrorCode(err), false
	}
	if retryable {
		return w.failureRetryable(task, err)
	}
	message := downloadFailureMessage(code, false)
	_ = w.markFailure(task, code, message, true)
	operation := downloadOperation(task.ProviderType, task.SourceOrigin)
	operation.Event(w.service.log.Error()).Str("task_id", task.ID).Str("error_code", code).Msg(operation.Message("执行失败"))
	return WorkerResult{ErrorCode: code, ErrorMessage: message}
}

func (w *DownloadWorker) failureRetryable(task models.DownloadTask, err error) WorkerResult {
	code, _ := downloadpkg.ErrorInfo(err)
	message := downloadFailureMessage(code, true)
	_ = w.markFailure(task, code, message, false)
	next := time.Now().UTC().Add(10 * time.Second)
	operation := downloadOperation(task.ProviderType, task.SourceOrigin)
	operation.Event(w.service.log.Warn()).Str("task_id", task.ID).Str("error_code", code).Time("retry_at", next).Msg(operation.Message("暂时失败，已安排自动重试"))
	return WorkerResult{RetryAt: &next, ErrorCode: code, ErrorMessage: message}
}

func downloadFailureMessage(code string, retryable bool) string {
	switch code {
	case "downloader_auth_failed":
		return "下载器认证已失效，请更新连接凭据后重试"
	case "downloader_rate_limited":
		return "下载器请求受到限速，任务将延后重试"
	case "downloader_source_invalid", "downloader_source_unsupported":
		return "下载器拒绝了下载链接，请检查链接后重试"
	case "downloader_storage_unavailable":
		return "下载目标目录不存在或已移动，请重新选择目录"
	case "downloader_quota_exhausted":
		return "115 离线下载配额已耗尽，请检查账号权益后重试"
	case "downloader_task_exists":
		return "115 已存在相同离线任务，但未能安全接管；请在 115 删除旧任务记录后重试"
	case "downloader_response_invalid":
		return "下载器返回了无法识别的响应，请重新测试连接"
	case CodeTransferMediaUnrecognized:
		return "下载已完成，但媒体未识别，未自动入库；请修正识别条件后重试"
	case "download_state_persist_failed":
		return "下载完成后的识别结果保存失败，请重试"
	case "download_completion_manifest_invalid":
		return "下载已完成，但保存的文件清单无效，未执行入库"
	case "download_completion_manifest_unavailable":
		return "下载已完成，但暂时无法取得文件清单"
	}
	if retryable {
		return "下载器暂时不可用，任务将自动重试"
	}
	return "下载任务执行失败"
}

func downloadOperation(providerType, sourceOrigin string) serverlog.Operation {
	if sourceOrigin == models.DownloadSourceOriginShare || sourceOrigin == models.DownloadSourceOriginProviderIngest {
		return serverlog.OperationPan115ShareIngest
	}
	if providerType == models.DownloaderTypePan115Offline {
		return serverlog.OperationPan115OfflineDownload
	}
	return serverlog.OperationDownloadTask
}

func (w *DownloadWorker) markFailure(task models.DownloadTask, code, message string, finished bool) error {
	now := time.Now().UTC()
	updates := map[string]any{"last_error_code": safeLabel(code, 96), "last_error_message": message, "updated_at": now}
	if finished {
		updates["phase"], updates["finished_at"] = models.DownloadTaskStatusFailed, now
	}
	return w.service.db.Model(&task).Updates(updates).Error
}
