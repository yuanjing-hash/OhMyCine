package services

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RetargetCompletedImport changes only the destination snapshot of a failed
// Transfer. The completed download and its immutable provider-relative
// manifests are reused; no downloader command is issued.
func (s *DownloadService) RetargetCompletedImport(ctx context.Context, actor Actor, downloadID string, libraryID uint, request RequestContext) (DownloadTaskSummary, error) {
	downloadID = strings.TrimSpace(downloadID)
	if downloadID == "" || libraryID == 0 {
		return DownloadTaskSummary{}, appError(CodeInvalidRequest, "请选择新的目标媒体库", nil)
	}
	var task models.DownloadTask
	if err := s.db.First(&task, "id = ?", downloadID).Error; err != nil {
		return DownloadTaskSummary{}, downloadTaskNotFound(err)
	}
	if !actor.Can(authz.PermissionJobsControlAll) && (task.OwnerID != actor.User.ID || !actor.Can(authz.PermissionJobsControlOwn)) {
		return DownloadTaskSummary{}, appError(CodePermissionDenied, "无权修改该任务的入库目标", nil)
	}
	if task.DownloaderID == nil {
		return DownloadTaskSummary{}, appError(CodeMediaLibraryStorageUnavailable, "该任务的下载器快照不支持修改目标", nil)
	}
	var downloader models.Downloader
	if err := s.db.First(&downloader, "id = ?", *task.DownloaderID).Error; err != nil {
		return DownloadTaskSummary{}, appError(CodeDownloaderUnavailable, "原下载器配置不可用", err)
	}
	sourceKind := ""
	switch task.SourceOrigin {
	case models.DownloadSourceOriginShare:
		sourceKind = downloadpkg.SourcePan115Share
	case models.DownloadSourceOriginProviderIngest:
		sourceKind = downloadpkg.SourceProviderItem
	}
	target, profile, err := s.resolveDownloadTarget(ctx, downloader, libraryID, sourceKind)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	rules, err := classification.DecodeStrict([]byte(profile.RulesJSON))
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeProfileValidation, "新媒体库分类规则无效", err)
	}
	canonicalRules, err := classification.CanonicalJSON(rules)
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeProfileValidation, "新媒体库分类规则无效", err)
	}
	organization, err := storedProfileOrganizationConfig(profile)
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeProfileValidation, "新媒体库识别与命名配置无效", err)
	}
	var transfer models.TransferTask
	if err := s.db.First(&transfer, "download_task_id = ?", task.ID).Error; err != nil {
		return DownloadTaskSummary{}, appError(CodeNotFound, "媒体整理任务不存在", err)
	}
	var selectedManifest downloadpkg.Manifest
	if err := json.Unmarshal([]byte(transfer.ManifestJSON), &selectedManifest); err != nil || len(selectedManifest.Files) == 0 {
		return DownloadTaskSummary{}, appError(CodeInvalidRequest, "已完成文件清单不可用，不能安全修改目标", err)
	}

	// Preserve the already verified work identity while reclassifying it with
	// the newly selected Profile. This avoids a second fuzzy title decision.
	preview := task
	preview.ProfileID = profile.ID
	preview.ProfileRevision = profile.Revision
	preview.ProfileRulesJSON = canonicalRules
	preview.ProfileBuiltinRecognitionPacksJSON = organization.BuiltinRecognitionPacksJSON
	preview.ProfileRecognitionRulesJSON = organization.RecognitionRulesJSON
	match, err := NewDownloadWorker(s).classify(ctx, preview, selectedManifest)
	if err != nil || !match.Confident {
		return DownloadTaskSummary{}, appError(CodeTransferMediaUnrecognized, "新媒体库规则无法确认当前媒体身份，未修改目标", err)
	}
	identitySource := firstNonEmpty(match.IdentitySource, firstNonEmpty(task.IdentitySource, mediaIdentitySourceAutomatic))
	identityStatus := firstNonEmpty(match.IdentityStatus, firstNonEmpty(task.IdentityStatus, mediaIdentityStatusProvisional))
	identityLocked := task.IdentityLocked || identitySource == mediaIdentitySourceManual
	identityRevision := task.IdentityRevision + 1
	if identityRevision == 0 {
		identityRevision = 1
	}
	_, identityJSON, err := buildDownloadIdentitySnapshot(preview, match, selectedManifest, identitySource, identityStatus, identityLocked, identityRevision)
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeTransferMediaUnrecognized, "当前媒体身份快照无法更新，未修改目标", err)
	}

	now := time.Now().UTC()
	var queuedJob models.Job
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var lockedTask models.DownloadTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedTask, "id = ?", task.ID).Error; err != nil {
			return downloadTaskNotFound(err)
		}
		var lockedTransfer models.TransferTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedTransfer, "download_task_id = ?", lockedTask.ID).Error; err != nil {
			return appError(CodeNotFound, "媒体整理任务不存在", err)
		}
		var lockedJob models.Job
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedJob, "id = ?", lockedTransfer.JobID).Error; err != nil {
			return queueNotFound(err)
		}
		if lockedJob.Status != models.JobStatusFailed || lockedTransfer.Phase != models.TransferTaskStatusFailed {
			return appError(CodeQueueStateConflict, "仅入库失败的任务可以修改目标", nil)
		}
		if lockedTransfer.ProcessedFiles != 0 || lockedTransfer.CleanupRemoved != 0 || strings.TrimSpace(lockedTransfer.PlanSummaryJSON) != "" || strings.TrimSpace(lockedTransfer.CloudStateJSON) != "" || lockedTransfer.CleanupStatus != models.TransferCleanupPending {
			return appError(CodeQueueStateConflict, "任务已产生入库规划、云端检查点或部分写入，不能静默切换目标", nil)
		}
		if err := tx.Model(&lockedTask).Updates(map[string]any{
			"profile_id": profile.ID, "profile_revision": profile.Revision, "profile_rules_json": canonicalRules,
			"profile_builtin_recognition_packs_json": organization.BuiltinRecognitionPacksJSON, "profile_recognition_rules_json": organization.RecognitionRulesJSON,
			"target_library_id": target.LibraryID, "target_library_name": target.LibraryName, "target_storage_id": target.StorageID,
			"target_storage_type": target.StorageType, "target_connection_id": target.ConnectionID, "target_provider_root_id": target.ProviderRootID,
			"target_storage_root": target.StorageRoot, "target_relative_root": target.RelativeRoot, "transfer_mode": target.TransferMode,
			"conflict_policy": target.ConflictPolicy, "movie_directory_template": organization.MovieDirectoryTemplate,
			"movie_filename_template": organization.MovieFilenameTemplate, "tv_directory_template": organization.TVDirectoryTemplate,
			"tv_filename_template": organization.TVFilenameTemplate, "scrape_category": safeLabel(match.Category, 128),
			"identity_source": identitySource, "identity_status": identityStatus, "identity_locked": identityLocked,
			"identity_revision": identityRevision, "identity_snapshot_json": identityJSON, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&lockedTransfer).Updates(map[string]any{
			"library_id": target.LibraryID, "library_name": target.LibraryName, "phase": models.TransferTaskStatusQueued,
			"processed_files": 0, "plan_summary_json": "", "cloud_state_json": "", "last_error_code": "",
			"cleanup_status": models.TransferCleanupPending, "cleanup_removed": 0, "cleanup_error_code": "", "finished_at": nil, "updated_at": now,
		}).Error; err != nil {
			return err
		}
		from := lockedJob.Status
		if err := tx.Model(&lockedJob).Updates(map[string]any{
			"status": models.JobStatusQueued, "revision": lockedJob.Revision + 1, "next_attempt_at": nil, "finished_at": nil,
			"last_error_code": "", "last_error_message": "", "updated_at": now,
		}).Error; err != nil {
			return err
		}
		lockedJob.Status = models.JobStatusQueued
		lockedJob.Revision++
		lockedJob.NextAttemptAt = nil
		lockedJob.FinishedAt = nil
		lockedJob.LastErrorCode, lockedJob.LastErrorMessage = "", ""
		lockedJob.UpdatedAt = now
		if err := recordJobEvent(tx, lockedJob.ID, "transfer.retarget", from, lockedJob.Status, &actor.User.ID, "", now); err != nil {
			return err
		}
		if err := s.audit.Record(tx, &actor.User.ID, "transfer.retarget", "transfer_task", lockedTransfer.ID, "success", map[string]any{"from_library_id": lockedTransfer.LibraryID, "to_library_id": target.LibraryID}, request); err != nil {
			return err
		}
		queuedJob = lockedJob
		return nil
	})
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	s.queue.wake()
	s.queue.publish(queuedJob, "job.status_changed")
	task.ProfileID, task.ProfileRevision, task.ProfileRulesJSON = profile.ID, profile.Revision, canonicalRules
	task.TargetLibraryID, task.TargetLibraryName = &target.LibraryID, target.LibraryName
	task.TargetStorageID, task.TargetStorageType = &target.StorageID, target.StorageType
	task.TargetConnectionID, task.TargetProviderRootID = target.ConnectionID, target.ProviderRootID
	task.TargetStorageRoot, task.TargetRelativeRoot = target.StorageRoot, target.RelativeRoot
	task.TransferMode, task.ConflictPolicy, task.ScrapeCategory = target.TransferMode, target.ConflictPolicy, safeLabel(match.Category, 128)
	task.IdentitySource, task.IdentityStatus, task.IdentityLocked = identitySource, identityStatus, identityLocked
	task.IdentityRevision, task.IdentitySnapshotJSON = identityRevision, identityJSON
	return downloadTaskSummary(task, models.JobStatusQueued), nil
}
