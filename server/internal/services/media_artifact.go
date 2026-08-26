package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/nfo"
	"gorm.io/gorm"
)

const JobTypeMediaArtifact = "media_artifact"

type mediaArtifactPolicy struct {
	LibraryID              uint     `json:"library_id"`
	Generation             uint64   `json:"generation"`
	StorageID              uint     `json:"storage_id"`
	StorageType            string   `json:"storage_type"`
	ConnectionID           uint     `json:"connection_id,omitempty"`
	ProjectionRoot         string   `json:"projection_root"`
	ProjectionRootIdentity string   `json:"projection_root_identity,omitempty"`
	TargetKind             string   `json:"target_kind"`
	STRMEnabled            bool     `json:"strm_enabled"`
	Metadata               bool     `json:"metadata_artifacts_enabled"`
	AssetExtensions        []string `json:"asset_extensions,omitempty"`
	ScanRunID              uint     `json:"scan_run_id,omitempty"`
	ScanKind               string   `json:"scan_kind,omitempty"`
	ScanPartial            bool     `json:"scan_partial,omitempty"`
	CleanupEligible        bool     `json:"cleanup_eligible,omitempty"`
}

type mediaArtifactJobPayload struct {
	ArtifactRunID string `json:"artifact_run_id"`
}

type ArtifactCleanupResult struct {
	Removed   int
	ErrorCode string
	Skipped   bool
}

type MediaArtifactCleanup interface {
	AutoCleanup(context.Context, string) ArtifactCleanupResult
}

type MediaArtifactService struct {
	db          *gorm.DB
	queue       *QueueService
	signedProxy *SignedProxyService
	metadata    *MetadataSettingsService
	connections *ConnectionService
	cleanup     MediaArtifactCleanup
	changes     *MediaChangeService
	log         zerolog.Logger
}

func NewMediaArtifactService(db *gorm.DB, queue *QueueService, signedProxy *SignedProxyService, log zerolog.Logger) *MediaArtifactService {
	return &MediaArtifactService{db: db, queue: queue, signedProxy: signedProxy, log: log}
}

func (s *MediaArtifactService) SetMetadataSettingsService(metadata *MetadataSettingsService) {
	s.metadata = metadata
}

func (s *MediaArtifactService) SetConnectionService(connections *ConnectionService) {
	s.connections = connections
}

func (s *MediaArtifactService) SetCleanupService(cleanup MediaArtifactCleanup) {
	s.cleanup = cleanup
}
func (s *MediaArtifactService) SetMediaChangeService(changes *MediaChangeService) {
	s.changes = changes
}

// ScheduleGeneration persists the immutable policy snapshot first and then
// enqueues only the run ID. Active per-library Jobs coalesce; their worker
// always advances to the newest queued generation before writing a file.
func (s *MediaArtifactService) ScheduleGeneration(libraryID uint, generation uint64) error {
	if s == nil || s.queue == nil || libraryID == 0 || generation == 0 {
		return appError(CodeInvalidRequest, "媒体产物任务参数无效", nil)
	}
	var library models.MediaLibrary
	if err := s.db.First(&library, libraryID).Error; err != nil {
		return mediaLibraryNotFound(err)
	}
	var storage models.Storage
	if err := s.db.First(&storage, library.StorageID).Error; err != nil {
		return err
	}
	var extraAssetExtensions []string
	_ = json.Unmarshal([]byte(library.STRMAssetExtraExtensionsJSON), &extraAssetExtensions)
	policy := mediaArtifactPolicy{LibraryID: library.ID, Generation: generation, StorageID: library.StorageID, StorageType: storage.Type, Metadata: library.MetadataArtifactsEnabled, AssetExtensions: effectiveSourceAssetExtensions(extraAssetExtensions)}
	switch storage.Type {
	case models.StorageTypeLocal:
		if !library.MetadataArtifactsEnabled {
			return nil
		}
		root, resolveErr := medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)
		if resolveErr != nil {
			return appError(CodeInvalidRequest, "本地媒体库产物目录无效", nil)
		}
		canonical, canonicalErr := (storagefs.LocalDriver{}).CanonicalizeRoot(root)
		if canonicalErr != nil {
			return appError(CodeInvalidRequest, "本地媒体库产物目录不可用", nil)
		}
		policy.ProjectionRoot, policy.TargetKind = canonical, models.MediaArtifactTargetLocalAdjacent
	default:
		if !library.STRMEnabled || !library.SignedProxyEnabled || s.signedProxy == nil {
			return nil
		}
		if storage.ConnectionID == nil || *storage.ConnectionID == 0 {
			return appError(CodeConnectionUnavailable, "媒体库连接不可用", nil)
		}
		policy.ConnectionID = *storage.ConnectionID
		policy.ProjectionRoot, policy.TargetKind, policy.STRMEnabled = library.STRMLocalRoot, models.MediaArtifactTargetLocalProjection, true
	}
	canonicalRoot, rootIdentity, err := canonicalProjectionRoot(policy.ProjectionRoot)
	if err != nil {
		return appError(CodeInvalidRequest, "媒体产物目录不可用", nil)
	}
	policy.ProjectionRoot, policy.ProjectionRootIdentity = canonicalRoot, rootIdentity
	var scanRun models.MediaLibraryScanRun
	if err := s.db.Where("library_id = ? AND generation = ?", library.ID, generation).Order("id DESC").First(&scanRun).Error; err == nil {
		policy.ScanRunID = scanRun.ID
		policy.ScanKind = scanRun.Kind
		policy.ScanPartial = scanRun.Partial
		policy.CleanupEligible = policy.TargetKind == models.MediaArtifactTargetLocalProjection && scanRun.Status == "success" && !scanRun.Partial && automaticCleanupScanKind(scanRun.Kind)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	run := models.MediaArtifactRun{ID: uuid.NewString(), LibraryID: library.ID, Generation: generation, PolicyJSON: string(policyJSON), Status: models.MediaArtifactStatusQueued, CleanupStatus: models.MediaArtifactCleanupPending, CreatedAt: now, UpdatedAt: now}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var existing models.MediaArtifactRun
		if err := tx.Where("library_id = ? AND generation = ?", library.ID, generation).First(&existing).Error; err == nil {
			run = existing
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		return tx.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Updates(map[string]any{"artifact_generation": generation, "artifact_status": models.MediaArtifactStatusQueued, "artifact_error": "", "artifact_updated_at": now}).Error
	}); err != nil {
		return err
	}
	if run.Status == models.MediaArtifactStatusCompleted || run.Status == models.MediaArtifactStatusRunning {
		return nil
	}
	job, err := s.queue.Enqueue(EnqueueJobInput{System: true, JobType: JobTypeMediaArtifact, Priority: 0, DisplayName: fmt.Sprintf("媒体产物 · 媒体库 %d", library.ID), Provider: "media_library", ResourceKey: fmt.Sprintf("library:%d", library.ID), CoalescingKey: "latest_generation", Payload: mediaArtifactJobPayload{ArtifactRunID: run.ID}})
	if err != nil {
		_ = s.failRun(run.ID, "artifact_enqueue_failed")
		return err
	}
	// One coalesced Job may represent several generations, while job_id is a
	// unique relation. Keep it attached to the newest run instead of silently
	// ignoring the uniqueness failure and leaving history on an obsolete run.
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MediaArtifactRun{}).Where("job_id = ? AND id <> ?", job.ID, run.ID).Update("job_id", nil).Error; err != nil {
			return err
		}
		return tx.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Update("job_id", job.ID).Error
	}); err != nil {
		return err
	}
	return nil
}

func (s *MediaArtifactService) failRun(runID, code string) error {
	now := time.Now().UTC()
	return s.db.Transaction(func(tx *gorm.DB) error {
		var run models.MediaArtifactRun
		if err := tx.First(&run, "id = ?", runID).Error; err != nil {
			return err
		}
		if err := tx.Model(&run).Updates(map[string]any{"status": models.MediaArtifactStatusFailed, "error_code": code, "cleanup_status": models.MediaArtifactCleanupSkipped, "cleanup_at": now, "finished_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&models.MediaLibrary{}).Where("id = ? AND artifact_generation = ?", run.LibraryID, run.Generation).Updates(map[string]any{"artifact_status": models.MediaArtifactStatusFailed, "artifact_error": code, "artifact_updated_at": now}).Error
	})
}

type MediaArtifactWorker struct{ service *MediaArtifactService }

func NewMediaArtifactWorker(service *MediaArtifactService) *MediaArtifactWorker {
	return &MediaArtifactWorker{service: service}
}

func (w *MediaArtifactWorker) Run(ctx context.Context, runtime JobRuntime, job ClaimedJob) WorkerResult {
	if w == nil || w.service == nil {
		return WorkerResult{ErrorCode: "artifact_service_unavailable", ErrorMessage: "媒体产物服务不可用"}
	}
	var payload mediaArtifactJobPayload
	if err := json.Unmarshal([]byte(job.Job.PayloadJSON), &payload); err != nil || strings.TrimSpace(payload.ArtifactRunID) == "" {
		return WorkerResult{ErrorCode: "artifact_payload_invalid", ErrorMessage: "媒体产物任务参数无效"}
	}
	run, policy, err := w.service.latestRun(payload.ArtifactRunID)
	if err != nil {
		return WorkerResult{ErrorCode: "artifact_run_missing", ErrorMessage: "媒体产物记录不存在"}
	}
	if run.Status == models.MediaArtifactStatusCompleted {
		if w.service.cleanup != nil && run.CleanupStatus != models.MediaArtifactCleanupCompleted && run.CleanupStatus != models.MediaArtifactCleanupSkipped {
			cleanup := w.service.cleanup.AutoCleanup(ctx, run.ID)
			if cleanup.ErrorCode != "" {
				return WorkerResult{ErrorCode: cleanup.ErrorCode, ErrorMessage: "媒体产物清理失败，媒体变更尚未发布"}
			}
		}
		return w.service.publishGenerationReady(run.LibraryID, run.Generation)
	}
	if run.Status == models.MediaArtifactStatusSuperseded {
		return WorkerResult{}
	}
	if superseded, checkErr := w.service.artifactPolicySuperseded(policy); checkErr != nil {
		return WorkerResult{ErrorCode: "artifact_state_unavailable", ErrorMessage: "媒体产物状态不可用"}
	} else if superseded {
		now := time.Now().UTC()
		_ = w.service.db.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": models.MediaArtifactStatusSuperseded, "cleanup_status": models.MediaArtifactCleanupSkipped, "cleanup_at": now, "finished_at": now, "updated_at": now}).Error
		return WorkerResult{}
	}
	started := time.Now()
	now := time.Now().UTC()
	if err := w.service.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": models.MediaArtifactStatusRunning, "started_at": now, "finished_at": nil, "error_code": "", "updated_at": now}).Error; err != nil {
			return err
		}
		result := tx.Model(&models.MediaLibrary{}).Where("id = ? AND artifact_generation = ?", run.LibraryID, run.Generation).Updates(map[string]any{"artifact_status": models.MediaArtifactStatusRunning, "artifact_error": "", "artifact_updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("artifact generation changed before start")
		}
		return nil
	}); err != nil {
		return WorkerResult{ErrorCode: "artifact_state_persist_failed", ErrorMessage: "媒体产物状态保存失败"}
	}
	serverlog.OperationMediaArtifact.Event(w.service.log.Info()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Msg(serverlog.OperationMediaArtifact.Message("开始"))
	if policy.STRMEnabled {
		serverlog.OperationSTRMGeneration.Event(w.service.log.Info()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Msg(serverlog.OperationSTRMGeneration.Message("开始"))
	}
	result := w.service.generateArtifacts(ctx, runtime, run, policy)
	_ = w.service.db.First(&run, "id = ?", run.ID).Error
	if run.Status == models.MediaArtifactStatusSuperseded {
		serverlog.OperationMediaArtifact.Event(w.service.log.Info()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationMediaArtifact.Message("已由更新 generation 接管"))
		return WorkerResult{}
	}
	if result.ErrorCode != "" {
		serverlog.OperationMediaArtifact.Event(w.service.log.Error()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Str("error_code", result.ErrorCode).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationMediaArtifact.Message("失败"))
		if policy.STRMEnabled {
			serverlog.OperationSTRMGeneration.Event(w.service.log.Error()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Str("error_code", result.ErrorCode).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationSTRMGeneration.Message("失败"))
		}
		return result
	}
	serverlog.OperationMediaArtifact.Event(w.service.log.Info()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Int("written", run.WrittenCount).Int("updated", run.UpdatedCount).Int("skipped", run.SkippedCount).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationMediaArtifact.Message("完成"))
	if policy.STRMEnabled {
		serverlog.OperationSTRMGeneration.Event(w.service.log.Info()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Int("written", run.WrittenCount).Int("updated", run.UpdatedCount).Int("skipped", run.SkippedCount).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationSTRMGeneration.Message("完成"))
	}
	return WorkerResult{}
}

func (s *MediaArtifactService) latestRun(initialID string) (models.MediaArtifactRun, mediaArtifactPolicy, error) {
	var initial models.MediaArtifactRun
	if err := s.db.First(&initial, "id = ?", initialID).Error; err != nil {
		return models.MediaArtifactRun{}, mediaArtifactPolicy{}, err
	}
	var latest models.MediaArtifactRun
	err := s.db.Where("library_id = ? AND status IN ?", initial.LibraryID, []string{models.MediaArtifactStatusQueued, models.MediaArtifactStatusFailed, models.MediaArtifactStatusRunning}).Order("generation DESC").First(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		latest = initial
	} else if err != nil {
		return models.MediaArtifactRun{}, mediaArtifactPolicy{}, err
	}
	if latest.Generation > initial.Generation {
		now := time.Now().UTC()
		_ = s.db.Model(&models.MediaArtifactRun{}).Where("library_id = ? AND generation < ? AND status IN ?", initial.LibraryID, latest.Generation, []string{models.MediaArtifactStatusQueued, models.MediaArtifactStatusFailed}).Updates(map[string]any{"status": models.MediaArtifactStatusSuperseded, "cleanup_status": models.MediaArtifactCleanupSkipped, "cleanup_at": now, "finished_at": now, "updated_at": now}).Error
	}
	var policy mediaArtifactPolicy
	if err := json.Unmarshal([]byte(latest.PolicyJSON), &policy); err != nil || policy.LibraryID != latest.LibraryID || policy.Generation != latest.Generation || (!policy.STRMEnabled && !policy.Metadata) || (policy.TargetKind != models.MediaArtifactTargetLocalAdjacent && policy.TargetKind != models.MediaArtifactTargetLocalProjection) {
		return models.MediaArtifactRun{}, mediaArtifactPolicy{}, errors.New("artifact policy is invalid")
	}
	return latest, policy, nil
}

func (s *MediaArtifactService) generateArtifacts(ctx context.Context, runtime JobRuntime, run models.MediaArtifactRun, policy mediaArtifactPolicy) WorkerResult {
	root, err := (storagefs.LocalDriver{}).CanonicalizeRoot(policy.ProjectionRoot)
	if err != nil {
		_ = s.failRun(run.ID, "artifact_projection_unavailable")
		return WorkerResult{ErrorCode: "artifact_projection_unavailable", ErrorMessage: "媒体产物目录不可用"}
	}
	var entries []models.MediaLibraryEntry
	if err := s.db.Where("library_id = ?", run.LibraryID).Order("relative_path").Find(&entries).Error; err != nil {
		_ = s.failRun(run.ID, "artifact_source_unavailable")
		return WorkerResult{ErrorCode: "artifact_source_unavailable", ErrorMessage: "媒体源清单不可用"}
	}
	entriesByRecognition := make(map[uint][]models.MediaLibraryEntry)
	for _, entry := range entries {
		if entry.RecognitionID != nil {
			entriesByRecognition[*entry.RecognitionID] = append(entriesByRecognition[*entry.RecognitionID], entry)
		}
	}
	var recognitions []models.MediaLibraryRecognition
	if policy.Metadata {
		if err := s.db.Where("library_id = ? AND last_generation = ? AND status = ?", run.LibraryID, run.Generation, mediaRecognitionStatusMatched).Order("id").Find(&recognitions).Error; err != nil {
			_ = s.failRun(run.ID, "artifact_metadata_unavailable")
			return WorkerResult{ErrorCode: "artifact_metadata_unavailable", ErrorMessage: "媒体元数据快照不可用"}
		}
	}
	var sourceAssets []models.MediaLibrarySourceAsset
	if policy.STRMEnabled && policy.StorageType != models.StorageTypeLocal {
		if err := s.db.Where("library_id = ? AND generation = ? AND active = ?", run.LibraryID, run.Generation, true).Order("relative_path").Find(&sourceAssets).Error; err != nil {
			_ = s.failRun(run.ID, "artifact_source_assets_unavailable")
			return WorkerResult{ErrorCode: "artifact_source_assets_unavailable", ErrorMessage: "源伴随文件清单不可用"}
		}
	}
	videoCount := 0
	if policy.STRMEnabled {
		videoCount = len(entries)
	}
	run.ExpectedCount = videoCount + len(sourceAssets) + len(recognitions)
	for index, entry := range entries {
		if !policy.STRMEnabled {
			break
		}
		if err := ctx.Err(); err != nil {
			_ = s.failRun(run.ID, "artifact_context_canceled")
			return WorkerResult{ErrorCode: "artifact_context_canceled", ErrorMessage: "媒体产物任务已取消"}
		}
		if superseded, checkErr := s.artifactPolicySuperseded(policy); checkErr != nil || superseded {
			now := time.Now().UTC()
			_ = s.db.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": models.MediaArtifactStatusSuperseded, "cleanup_status": models.MediaArtifactCleanupSkipped, "cleanup_at": now, "finished_at": now, "updated_at": now}).Error
			return WorkerResult{}
		}
		outcome, writeErr := s.writeSTRM(ctx, root, run, entry)
		switch outcome {
		case "written":
			run.WrittenCount++
		case "updated":
			run.UpdatedCount++
		case "skipped":
			run.SkippedCount++
		}
		if writeErr != nil {
			run.FailedCount++
		}
		processed, total := int64(index+1), int64(run.ExpectedCount)
		progress := float64(processed) / float64(max(1, run.ExpectedCount))
		if err := runtime.Heartbeat(&progress, &processed, &total, nil, nil); err != nil {
			_ = s.failRun(run.ID, CodeQueueLeaseInvalid)
			return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "媒体产物任务租约已失效"}
		}
	}
	for index, asset := range sourceAssets {
		if err := ctx.Err(); err != nil {
			_ = s.failRun(run.ID, "artifact_context_canceled")
			return WorkerResult{ErrorCode: "artifact_context_canceled", ErrorMessage: "媒体产物任务已取消"}
		}
		if superseded, checkErr := s.artifactPolicySuperseded(policy); checkErr != nil || superseded {
			now := time.Now().UTC()
			_ = s.db.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": models.MediaArtifactStatusSuperseded, "cleanup_status": models.MediaArtifactCleanupSkipped, "cleanup_at": now, "finished_at": now, "updated_at": now}).Error
			return WorkerResult{}
		}
		outcome, writeErr := s.writeSourceAsset(ctx, root, run, policy, asset)
		switch outcome {
		case "written":
			run.WrittenCount++
		case "updated":
			run.UpdatedCount++
		case "skipped":
			run.SkippedCount++
		}
		if writeErr != nil {
			run.FailedCount++
		}
		processed, total := int64(videoCount+index+1), int64(run.ExpectedCount)
		progress := float64(processed) / float64(max(1, run.ExpectedCount))
		if err := runtime.Heartbeat(&progress, &processed, &total, nil, nil); err != nil {
			_ = s.failRun(run.ID, CodeQueueLeaseInvalid)
			return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "媒体产物任务租约已失效"}
		}
	}
	for index, recognition := range recognitions {
		if err := ctx.Err(); err != nil {
			_ = s.failRun(run.ID, "artifact_context_canceled")
			return WorkerResult{ErrorCode: "artifact_context_canceled", ErrorMessage: "媒体产物任务已取消"}
		}
		if superseded, checkErr := s.artifactPolicySuperseded(policy); checkErr != nil || superseded {
			now := time.Now().UTC()
			_ = s.db.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": models.MediaArtifactStatusSuperseded, "cleanup_status": models.MediaArtifactCleanupSkipped, "cleanup_at": now, "finished_at": now, "updated_at": now}).Error
			return WorkerResult{}
		}
		outcome, writeErr := s.writeNFO(ctx, root, run, policy.TargetKind, recognition, entriesByRecognition[recognition.ID])
		switch outcome {
		case "written":
			run.WrittenCount++
		case "updated":
			run.UpdatedCount++
		case "skipped":
			run.SkippedCount++
		}
		if writeErr != nil {
			run.FailedCount++
		}
		processed, total := int64(videoCount+len(sourceAssets)+index+1), int64(run.ExpectedCount)
		progress := float64(processed) / float64(max(1, run.ExpectedCount))
		if err := runtime.Heartbeat(&progress, &processed, &total, nil, nil); err != nil {
			_ = s.failRun(run.ID, CodeQueueLeaseInvalid)
			return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "媒体产物任务租约已失效"}
		}
	}
	finished := time.Now().UTC()
	status, code := models.MediaArtifactStatusCompleted, ""
	if run.FailedCount > 0 {
		status, code = models.MediaArtifactStatusFailed, "artifact_write_failed"
	}
	superseded := false
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if status == models.MediaArtifactStatusCompleted {
			var current models.MediaLibrary
			if err := tx.First(&current, run.LibraryID).Error; err != nil {
				return err
			}
			if current.ArtifactGeneration != run.Generation || !artifactPolicyMatchesLibrary(policy, current) {
				superseded = true
				return tx.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": models.MediaArtifactStatusSuperseded, "expected_count": run.ExpectedCount, "written_count": run.WrittenCount, "updated_count": run.UpdatedCount, "skipped_count": run.SkippedCount, "failed_count": run.FailedCount, "error_code": "", "cleanup_status": models.MediaArtifactCleanupSkipped, "cleanup_at": finished, "finished_at": finished, "updated_at": finished}).Error
			}
			if err := tx.Model(&models.MediaArtifact{}).Where("library_id = ? AND target_kind = ? AND run_id <> ? AND active = ?", run.LibraryID, policy.TargetKind, run.ID, true).Updates(map[string]any{"active": false, "updated_at": finished}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": status, "expected_count": run.ExpectedCount, "written_count": run.WrittenCount, "updated_count": run.UpdatedCount, "skipped_count": run.SkippedCount, "failed_count": run.FailedCount, "error_code": code, "cleanup_status": models.MediaArtifactCleanupPending, "cleanup_error_code": "", "cleanup_at": nil, "finished_at": finished, "updated_at": finished}).Error; err != nil {
				return err
			}
			result := tx.Model(&models.MediaLibrary{}).Where("id = ? AND artifact_generation = ?", run.LibraryID, run.Generation).Updates(map[string]any{"artifact_status": status, "artifact_error": code, "artifact_updated_at": finished, "artifact_applied_generation": run.Generation})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("artifact generation changed during completion")
			}
			return nil
		}
		if err := tx.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": status, "expected_count": run.ExpectedCount, "written_count": run.WrittenCount, "updated_count": run.UpdatedCount, "skipped_count": run.SkippedCount, "failed_count": run.FailedCount, "error_code": code, "cleanup_status": models.MediaArtifactCleanupSkipped, "cleanup_at": finished, "finished_at": finished, "updated_at": finished}).Error; err != nil {
			return err
		}
		return tx.Model(&models.MediaLibrary{}).Where("id = ? AND artifact_generation = ?", run.LibraryID, run.Generation).Updates(map[string]any{"artifact_status": status, "artifact_error": code, "artifact_updated_at": finished}).Error
	}); err != nil {
		return WorkerResult{ErrorCode: "artifact_state_persist_failed", ErrorMessage: "媒体产物结果保存失败"}
	}
	if superseded {
		return WorkerResult{}
	}
	if run.FailedCount > 0 {
		next := time.Now().UTC().Add(time.Minute)
		return WorkerResult{RetryAt: &next, ErrorCode: code, ErrorMessage: "部分媒体产物生成失败，将自动重试"}
	}
	if s.cleanup != nil {
		cleanup := s.cleanup.AutoCleanup(ctx, run.ID)
		if cleanup.ErrorCode != "" {
			return WorkerResult{ErrorCode: cleanup.ErrorCode, ErrorMessage: "媒体产物清理失败，媒体变更尚未发布"}
		}
	}
	if policy.ScanPartial {
		// A partial provider enumeration cannot prove that the completed artifact
		// projection contains the authoritative library state. It must neither
		// publish nor supersede an older pending content change.
		return WorkerResult{}
	}
	return s.publishGenerationReady(run.LibraryID, run.Generation)
}

func mediaLibraryRequiresArtifacts(storageType string, library models.MediaLibrary, available bool) bool {
	if !available {
		return false
	}
	if storageType == models.StorageTypeLocal {
		return library.MetadataArtifactsEnabled
	}
	return library.STRMEnabled && library.SignedProxyEnabled
}

func (s *MediaArtifactService) publishGenerationReady(libraryID uint, generation uint64) WorkerResult {
	if s.changes == nil {
		return WorkerResult{}
	}
	var readied []models.MediaLibraryChange
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		readied, err = s.changes.MarkGenerationReadyTx(tx, libraryID, generation)
		return err
	}); err != nil {
		return WorkerResult{ErrorCode: "media_change_ready_failed", ErrorMessage: "媒体变更发布失败"}
	}
	if len(readied) > 0 {
		s.changes.NotifyCommitted(libraryID, readied[len(readied)-1].Revision)
	}
	return WorkerResult{}
}

func artifactPolicyMatchesLibrary(policy mediaArtifactPolicy, library models.MediaLibrary) bool {
	if library.StorageID != policy.StorageID || library.ArtifactGeneration != policy.Generation {
		return false
	}
	switch policy.TargetKind {
	case models.MediaArtifactTargetLocalProjection:
		if !library.Enabled || !library.STRMEnabled || !library.SignedProxyEnabled {
			return false
		}
		if policy.ProjectionRootIdentity == "" {
			return storagefs.NormalizeForComparison(library.STRMLocalRoot) == storagefs.NormalizeForComparison(policy.ProjectionRoot)
		}
		_, identity, err := canonicalProjectionRoot(library.STRMLocalRoot)
		return err == nil && identity == policy.ProjectionRootIdentity
	case models.MediaArtifactTargetLocalAdjacent:
		return library.Enabled && library.MetadataArtifactsEnabled
	default:
		return false
	}
}

func (s *MediaArtifactService) artifactPolicySuperseded(policy mediaArtifactPolicy) (bool, error) {
	var current models.MediaLibrary
	if err := s.db.First(&current, policy.LibraryID).Error; err != nil {
		return false, err
	}
	return !artifactPolicyMatchesLibrary(policy, current), nil
}

func automaticCleanupScanKind(kind string) bool {
	switch kind {
	case "initial", "catch_up", "manual", "event", "incremental", "full", "strm_incremental_manual", "strm_full_manual":
		return true
	default:
		return false
	}
}

func canonicalProjectionRoot(root string) (string, string, error) {
	canonical, err := (storagefs.LocalDriver{}).CanonicalizeRoot(root)
	if err != nil {
		return "", "", err
	}
	resolved, err := filepath.EvalSymlinks(canonical)
	if err != nil {
		return "", "", err
	}
	resolved, err = (storagefs.LocalDriver{}).CanonicalizeRoot(resolved)
	if err != nil {
		return "", "", err
	}
	return canonical, storagefs.NormalizeForComparison(resolved), nil
}

func (s *MediaArtifactService) writeSourceAsset(ctx context.Context, root string, run models.MediaArtifactRun, policy mediaArtifactPolicy, asset models.MediaLibrarySourceAsset) (string, error) {
	extension := strings.ToLower(strings.TrimSpace(asset.Extension))
	bareExtension := strings.TrimPrefix(extension, ".")
	if len(policy.AssetExtensions) == 0 {
		policy.AssetExtensions = effectiveSourceAssetExtensions(nil)
	}
	allowed := false
	for _, candidate := range policy.AssetExtensions {
		if candidate == bareExtension {
			allowed = true
			break
		}
	}
	if !allowed {
		return "skipped", nil
	}
	limit := int64(10 << 20)
	kind := models.MediaArtifactKindSourceAsset
	switch extension {
	case ".srt", ".ssa", ".ass":
		kind = models.MediaArtifactKindSubtitle
	case ".jpg":
		limit, kind = 20<<20, models.MediaArtifactKindImage
	}
	if asset.Size <= 0 || asset.Size > limit || strings.TrimSpace(asset.ProviderID) == "" || policy.ConnectionID == 0 || s.connections == nil {
		return "", errors.New("source asset facts are invalid")
	}
	_, driver, err := s.connections.driver(policy.ConnectionID)
	if err != nil {
		return "", err
	}
	item, err := driver.Stat(ctx, asset.ProviderID)
	if err != nil {
		return "", err
	}
	if item.ID != asset.ProviderID || item.IsDir || item.Size <= 0 || item.Size > limit || (asset.ParentProviderID != "" && item.ParentID != asset.ParentProviderID) {
		return "", errors.New("source asset identity changed")
	}
	const userAgent = "OhMyCine-Artifact/1.0"
	temporary, err := driver.DirectURL(ctx, cloudpkg.DirectURLRequest{FileID: item.ID, PickCode: item.PickCode, UserAgent: userAgent})
	if err != nil {
		return "", err
	}
	if len(temporary.Headers) != 0 {
		return "", errors.New("source asset requires unsupported upstream headers")
	}
	parsed, err := url.Parse(temporary.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", errors.New("source asset URL is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", errors.New("source asset request is invalid")
	}
	request.Header.Set("Accept", "*/*")
	request.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("source asset download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > limit {
		return "", errors.New("source asset response is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(body) == 0 || int64(len(body)) > limit {
		return "", errors.New("source asset body is invalid")
	}
	if extension == ".jpg" {
		contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
		if contentType != "image/jpeg" || len(body) < 3 || body[0] != 0xff || body[1] != 0xd8 || body[2] != 0xff {
			return "", errors.New("source asset JPEG is invalid")
		}
	} else if kind == models.MediaArtifactKindSubtitle && bytes.IndexByte(body, 0) >= 0 {
		return "", errors.New("source asset contains binary data")
	}
	relative := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimPrefix(asset.RelativePath, "/"))))
	if relative == "." || relative == ".." || strings.HasPrefix(relative, "../") || !strings.EqualFold(filepath.Ext(relative), extension) {
		return "", errors.New("source asset path is invalid")
	}
	return s.writeLocalArtifact(root, run, localArtifactSpec{SourceIdentity: fmt.Sprintf("asset:%d", asset.ID), ProviderItemID: asset.ProviderID, Kind: kind, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/" + relative, Content: func(models.MediaArtifact) ([]byte, error) { return body, nil }})
}

func (s *MediaArtifactService) writeNFO(ctx context.Context, root string, run models.MediaArtifactRun, targetKind string, recognition models.MediaLibraryRecognition, entries []models.MediaLibraryEntry) (string, error) {
	if len(entries) == 0 {
		return "skipped", nil
	}
	_, snapshot, err := decodeRecognitionMetadata(recognition.MetadataJSON)
	if err != nil {
		return "", err
	}
	content, err := nfo.Render(snapshot)
	if errors.Is(err, nfo.ErrSnapshotIncomplete) {
		return "skipped", nil
	}
	if err != nil {
		return "", err
	}
	relative, err := nfoRelativePath(snapshot.MediaType, entries)
	if err != nil {
		return "", err
	}
	outcome, err := s.writeLocalArtifact(root, run, localArtifactSpec{SourceIdentity: fmt.Sprintf("recognition:%d", recognition.ID), Kind: models.MediaArtifactKindNFO, TargetKind: targetKind, RelativePath: relative, Content: func(models.MediaArtifact) ([]byte, error) { return content, nil }})
	if err != nil {
		return outcome, err
	}
	images := nfo.Images(snapshot)
	if len(images) == 0 {
		return outcome, nil
	}
	if s.metadata == nil {
		return outcome, errors.New("metadata image client is unavailable")
	}
	client, _, _, err := s.metadata.clientWithCredentialInfo()
	if err != nil {
		return outcome, err
	}
	combined := outcome
	for _, image := range images {
		imageRelative, imageKind, err := nfoImageRelativePath(relative, image)
		if err != nil {
			return combined, err
		}
		limit := int64(10 << 20)
		size := "w780"
		if image.Kind == "fanart" {
			limit, size = 15<<20, "w1280"
		}
		body, err := client.DownloadJPEG(ctx, image.TMDBPath, size, limit)
		if err != nil {
			return combined, err
		}
		season := ""
		if image.SeasonNumber != nil {
			season = fmt.Sprintf(":%d", *image.SeasonNumber)
		}
		imageOutcome, err := s.writeLocalArtifact(root, run, localArtifactSpec{SourceIdentity: fmt.Sprintf("recognition:%d:%s%s", recognition.ID, image.Kind, season), Kind: imageKind, TargetKind: targetKind, RelativePath: imageRelative, Content: func(models.MediaArtifact) ([]byte, error) { return body, nil }})
		combined = combineArtifactOutcome(combined, imageOutcome)
		if err != nil {
			return combined, err
		}
	}
	return combined, nil
}

func nfoImageRelativePath(nfoRelative string, image nfo.ImageIdentity) (string, string, error) {
	directory := filepath.ToSlash(filepath.Dir(nfoRelative))
	base := strings.TrimSuffix(filepath.Base(nfoRelative), filepath.Ext(nfoRelative))
	switch image.Kind {
	case "poster":
		if base == "tvshow" {
			return filepath.ToSlash(filepath.Join(directory, "poster.jpg")), models.MediaArtifactKindPoster, nil
		}
		return filepath.ToSlash(filepath.Join(directory, base+"-poster.jpg")), models.MediaArtifactKindPoster, nil
	case "fanart":
		if base == "tvshow" {
			return filepath.ToSlash(filepath.Join(directory, "fanart.jpg")), models.MediaArtifactKindFanart, nil
		}
		return filepath.ToSlash(filepath.Join(directory, base+"-fanart.jpg")), models.MediaArtifactKindFanart, nil
	case "season_poster":
		if image.SeasonNumber == nil || *image.SeasonNumber < 0 || *image.SeasonNumber > 10000 {
			return "", "", errors.New("season image identity is invalid")
		}
		return filepath.ToSlash(filepath.Join(directory, fmt.Sprintf("season%02d-poster.jpg", *image.SeasonNumber))), models.MediaArtifactKindPoster, nil
	default:
		return "", "", errors.New("metadata image kind is invalid")
	}
}

func combineArtifactOutcome(left, right string) string {
	if left == "written" || right == "written" {
		return "written"
	}
	if left == "updated" || right == "updated" {
		return "updated"
	}
	if left == "skipped" || right == "skipped" {
		return "skipped"
	}
	return ""
}

func nfoRelativePath(mediaType string, entries []models.MediaLibraryEntry) (string, error) {
	if len(entries) == 0 {
		return "", errors.New("NFO source entries are missing")
	}
	first := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimPrefix(entries[0].RelativePath, "/"))))
	if first == "." || first == ".." || strings.HasPrefix(first, "../") {
		return "", errors.New("NFO source path is invalid")
	}
	if mediaType == "movie" {
		extension := filepath.Ext(first)
		if extension == "" {
			return "", errors.New("movie source extension is missing")
		}
		return "/" + strings.TrimSuffix(first, extension) + ".nfo", nil
	}
	if mediaType != "tv" {
		return "", errors.New("NFO media type is invalid")
	}
	directory := filepath.ToSlash(filepath.Dir(first))
	base := strings.ToLower(filepath.Base(directory))
	if strings.HasPrefix(base, "season ") || strings.HasPrefix(base, "season.") || strings.HasPrefix(base, "season_") || strings.HasPrefix(base, "season-") {
		directory = filepath.ToSlash(filepath.Dir(directory))
	}
	if directory == "." {
		directory = ""
	}
	return "/" + strings.Trim(directory+"/tvshow.nfo", "/"), nil
}

func (s *MediaArtifactService) writeSTRM(_ context.Context, root string, run models.MediaArtifactRun, entry models.MediaLibraryEntry) (string, error) {
	relative, err := strmRelativePath(entry.RelativePath)
	if err != nil {
		return "", err
	}
	return s.writeLocalArtifact(root, run, localArtifactSpec{SourceIdentity: fmt.Sprintf("entry:%d", entry.ID), ProviderItemID: entry.ProviderID, Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: relative, Content: func(artifact models.MediaArtifact) ([]byte, error) {
		signed, err := s.signedProxy.SignArtifact(artifact.OpaqueID, run.LibraryID, proxyDefaultTTL)
		if err != nil {
			return nil, err
		}
		return []byte(signed + "\n"), nil
	}})
}

type localArtifactSpec struct {
	SourceIdentity string
	ProviderItemID string
	Kind           string
	TargetKind     string
	RelativePath   string
	Content        func(models.MediaArtifact) ([]byte, error)
}

func (s *MediaArtifactService) writeLocalArtifact(root string, run models.MediaArtifactRun, spec localArtifactSpec) (string, error) {
	relative := spec.RelativePath
	var artifact models.MediaArtifact
	findErr := s.db.Where("library_id = ? AND target_kind = ? AND relative_path = ?", run.LibraryID, spec.TargetKind, relative).First(&artifact).Error
	exists := findErr == nil
	if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return "", findErr
	}
	target, err := storagefs.Constrain(root, filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(relative, "/"))))
	if err != nil {
		return "", err
	}
	if !exists {
		if _, err := os.Lstat(target); err == nil {
			return "skipped", nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		opaque, err := NewArtifactOpaqueID()
		if err != nil {
			return "", err
		}
		artifact = models.MediaArtifact{OpaqueID: opaque, RunID: run.ID, LibraryID: run.LibraryID, SourceIdentity: spec.SourceIdentity, ProviderItemID: spec.ProviderItemID, Kind: spec.Kind, TargetKind: spec.TargetKind, RelativePath: relative, Managed: true, Active: true, Status: models.MediaArtifactStatusQueued, CreatedAt: time.Now().UTC()}
	} else if !artifact.Managed {
		return "skipped", nil
	} else if artifact.Status == models.MediaArtifactStatusCleanup && !artifact.Active {
		return "", errors.New("managed artifact cleanup is in progress")
	}
	artifact.RunID, artifact.SourceIdentity, artifact.ProviderItemID, artifact.Kind, artifact.Active = run.ID, spec.SourceIdentity, spec.ProviderItemID, spec.Kind, true
	content, err := spec.Content(artifact)
	if err != nil {
		return "", err
	}
	fingerprint := sha256.Sum256(content)
	fingerprintHex := hex.EncodeToString(fingerprint[:])
	if exists && artifact.ContentFingerprint == fingerprintHex {
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			artifact.Status, artifact.ErrorCode, artifact.UpdatedAt = models.MediaArtifactStatusCompleted, "", time.Now().UTC()
			if err := s.db.Save(&artifact).Error; err != nil {
				return "", err
			}
			return "skipped", nil
		}
	}
	if err := atomicWriteArtifact(root, target, content); err != nil {
		artifact.Status, artifact.ErrorCode, artifact.UpdatedAt = models.MediaArtifactStatusFailed, "artifact_write_failed", time.Now().UTC()
		_ = s.db.Save(&artifact).Error
		return "", err
	}
	artifact.ContentFingerprint, artifact.Status, artifact.ErrorCode, artifact.UpdatedAt = fingerprintHex, models.MediaArtifactStatusCompleted, "", time.Now().UTC()
	if err := s.db.Save(&artifact).Error; err != nil {
		return "", err
	}
	if exists {
		return "updated", nil
	}
	return "written", nil
}

func strmRelativePath(source string) (string, error) {
	source = filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimPrefix(strings.TrimSpace(source), "/"))))
	if source == "." || source == ".." || strings.HasPrefix(source, "../") {
		return "", errors.New("STRM source path is invalid")
	}
	extension := strings.ToLower(filepath.Ext(source))
	if extension == ".iso" {
		return "/" + source + ".strm", nil
	}
	if extension == "" {
		return "", errors.New("STRM source extension is missing")
	}
	return "/" + strings.TrimSuffix(source, filepath.Ext(source)) + ".strm", nil
}

func atomicWriteArtifact(root, target string, content []byte) error {
	parent := filepath.Dir(target)
	if err := ensureSafeProjectionDirectory(root, parent); err != nil {
		return err
	}
	if _, err := (storagefs.LocalDriver{}).CanonicalizeRoot(parent); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".omc-artifact-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := bytes.NewReader(content).WriteTo(temporary); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if _, err := (storagefs.LocalDriver{}).CanonicalizeRoot(parent); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed artifact target became a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return replaceArtifactFile(temporaryName, target)
}

func ensureSafeProjectionDirectory(root, parent string) error {
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("artifact target escapes projection root")
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if _, err := (storagefs.LocalDriver{}).CanonicalizeRoot(current); err != nil {
			return err
		}
	}
	return nil
}
