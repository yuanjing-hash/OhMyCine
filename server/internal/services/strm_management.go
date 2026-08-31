package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	"gorm.io/gorm"
)

const JobTypeSTRMReconcile = "strm_reconcile"

const cleanupClaimOperation = "artifact_cleanup"

type STRMLibraryOverview struct {
	ID                        uint                     `json:"id"`
	Name                      string                   `json:"name"`
	StorageID                 uint                     `json:"storage_id"`
	ArtifactGeneration        uint64                   `json:"artifact_generation"`
	ArtifactAppliedGeneration uint64                   `json:"artifact_applied_generation"`
	ArtifactStatus            string                   `json:"artifact_status"`
	ArtifactError             string                   `json:"artifact_error"`
	ArtifactUpdatedAt         *time.Time               `json:"artifact_updated_at"`
	ArtifactCleanupRemoved    int                      `json:"artifact_cleanup_removed"`
	ArtifactCleanupError      string                   `json:"artifact_cleanup_error"`
	ArtifactCleanupAt         *time.Time               `json:"artifact_cleanup_at"`
	LatestRun                 *models.MediaArtifactRun `json:"latest_run"`
}

type STRMRunPage struct {
	List     []models.MediaArtifactRun `json:"list"`
	Total    int64                     `json:"total"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
}
type STRMArtifactPage struct {
	List     []models.MediaArtifact `json:"list"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
}
type STRMCleanupPreview struct {
	Count             int            `json:"count"`
	KindCounts        map[string]int `json:"kind_counts"`
	Paths             []string       `json:"paths"`
	Generation        uint64         `json:"generation"`
	ConfirmationToken string         `json:"confirmation_token"`
	ExpiresAt         time.Time      `json:"expires_at"`
}

type strmReconcilePayload struct {
	LibraryID uint   `json:"library_id"`
	Mode      string `json:"mode"`
}
type strmCleanupClaim struct {
	Operation         string `json:"operation"`
	ActorID           uint   `json:"actor_id"`
	LibraryID         uint   `json:"library_id"`
	Generation        uint64 `json:"generation"`
	AppliedGeneration uint64 `json:"applied_generation"`
	RootIdentityHash  string `json:"root_identity_hash"`
	Snapshot          string `json:"snapshot"`
	ExpiresAt         int64  `json:"expires_at"`
}

type STRMManagementService struct {
	db         *gorm.DB
	audit      *AuditService
	queue      *QueueService
	libraries  *MediaLibraryService
	artifacts  *MediaArtifactService
	cleanupKey []byte
	log        zerolog.Logger
	removeFile func(string) error
	removeDir  func(string) error
}

func NewSTRMManagementService(db *gorm.DB, audit *AuditService, queue *QueueService, libraries *MediaLibraryService, artifacts *MediaArtifactService, loggers ...zerolog.Logger) *STRMManagementService {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("secure STRM cleanup token key unavailable")
	}
	log := zerolog.Nop()
	if len(loggers) > 0 {
		log = loggers[0]
	}
	return &STRMManagementService{db: db, audit: audit, queue: queue, libraries: libraries, artifacts: artifacts, cleanupKey: key, log: log, removeFile: os.Remove, removeDir: os.Remove}
}

func (s *STRMManagementService) Libraries(actor Actor) ([]STRMLibraryOverview, error) {
	if !actor.Can(authz.PermissionSTRMRunsRead) {
		return nil, appError(CodePermissionDenied, "无权查看 STRM 管理", nil)
	}
	var libraries []models.MediaLibrary
	if err := s.db.Where("strm_enabled = ?", true).Order("sort_order, id").Find(&libraries).Error; err != nil {
		return nil, err
	}
	result := make([]STRMLibraryOverview, 0, len(libraries))
	for _, library := range libraries {
		item := STRMLibraryOverview{ID: library.ID, Name: library.Name, StorageID: library.StorageID, ArtifactGeneration: library.ArtifactGeneration, ArtifactAppliedGeneration: library.ArtifactAppliedGeneration, ArtifactStatus: library.ArtifactStatus, ArtifactError: library.ArtifactError, ArtifactUpdatedAt: library.ArtifactUpdatedAt, ArtifactCleanupRemoved: library.ArtifactCleanupRemoved, ArtifactCleanupError: library.ArtifactCleanupError, ArtifactCleanupAt: library.ArtifactCleanupAt}
		var run models.MediaArtifactRun
		if err := s.db.Where("library_id = ?", library.ID).Order("generation DESC, created_at DESC").First(&run).Error; err == nil {
			item.LatestRun = &run
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func pageBounds(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize
}

func (s *STRMManagementService) Runs(actor Actor, libraryID uint, page, pageSize int) (STRMRunPage, error) {
	if !actor.Can(authz.PermissionSTRMRunsRead) {
		return STRMRunPage{}, appError(CodePermissionDenied, "无权查看 STRM 历史", nil)
	}
	page, pageSize = pageBounds(page, pageSize)
	query := s.db.Model(&models.MediaArtifactRun{})
	if libraryID != 0 {
		query = query.Where("library_id = ?", libraryID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return STRMRunPage{}, err
	}
	list := make([]models.MediaArtifactRun, 0)
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return STRMRunPage{}, err
	}
	return STRMRunPage{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *STRMManagementService) Artifacts(actor Actor, libraryID uint, page, pageSize int) (STRMArtifactPage, error) {
	if !actor.Can(authz.PermissionSTRMRunsRead) {
		return STRMArtifactPage{}, appError(CodePermissionDenied, "无权查看 STRM 产物", nil)
	}
	page, pageSize = pageBounds(page, pageSize)
	query := s.db.Model(&models.MediaArtifact{}).Where("managed = ?", true)
	if libraryID != 0 {
		query = query.Where("library_id = ?", libraryID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return STRMArtifactPage{}, err
	}
	list := make([]models.MediaArtifact, 0)
	if err := query.Order("updated_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return STRMArtifactPage{}, err
	}
	return STRMArtifactPage{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *STRMManagementService) RequestReconcile(actor Actor, libraryID uint, mode string) (JobDTO, error) {
	if !actor.Can(authz.PermissionSTRMRunsCreate) {
		return JobDTO{}, appError(CodePermissionDenied, "无权启动 STRM 刷新", nil)
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "incremental" && mode != "full" {
		return JobDTO{}, appError(CodeInvalidRequest, "STRM 刷新模式无效", nil)
	}
	var library models.MediaLibrary
	if err := s.db.First(&library, libraryID).Error; err != nil {
		return JobDTO{}, mediaLibraryNotFound(err)
	}
	if !library.Enabled || !library.STRMEnabled {
		return JobDTO{}, appError(CodeConflict, "媒体库未启用 STRM", nil)
	}
	label := "增量"
	if mode == "full" {
		label = "全量"
	}
	// Repeated clicks for the same library and mode represent the same desired
	// reconciliation. Keep full and incremental requests distinct so a queued
	// incremental refresh can never silently swallow a later full rebuild.
	return s.queue.Enqueue(EnqueueJobInput{System: true, JobType: JobTypeSTRMReconcile, DisplayName: fmt.Sprintf("STRM %s刷新 · %s", label, library.Name), Provider: "media_library", ResourceKey: "library:" + strconv.FormatUint(uint64(libraryID), 10), CoalescingKey: "manual_" + mode, Payload: strmReconcilePayload{LibraryID: libraryID, Mode: mode}})
}

func (s *STRMManagementService) RetryRun(actor Actor, runID string) error {
	if !actor.Can(authz.PermissionSTRMRunsCreate) {
		return appError(CodePermissionDenied, "无权重试 STRM 任务", nil)
	}
	var run models.MediaArtifactRun
	if err := s.db.First(&run, "id = ?", runID).Error; err != nil {
		return appError(CodeNotFound, "STRM 运行记录不存在", err)
	}
	if run.Status != models.MediaArtifactStatusFailed {
		return appError(CodeConflict, "只有失败的 STRM 任务可以重试", nil)
	}
	if s.artifacts == nil {
		return appError(CodeConflict, "STRM 产物服务不可用", nil)
	}
	var library models.MediaLibrary
	if err := s.db.First(&library, run.LibraryID).Error; err != nil {
		return mediaLibraryNotFound(err)
	}
	if !library.Enabled || !library.STRMEnabled || library.ArtifactGeneration != run.Generation || library.ArtifactAppliedGeneration >= run.Generation {
		return appError(CodeConflict, "该 STRM 运行记录已不是当前可重试 generation", nil)
	}
	now := time.Now().UTC()
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.MediaArtifactRun{}).Where("id = ? AND status = ?", run.ID, models.MediaArtifactStatusFailed).Updates(map[string]any{"status": models.MediaArtifactStatusQueued, "retry_count": gorm.Expr("retry_count + 1"), "error_code": "", "cleanup_status": models.MediaArtifactCleanupPending, "cleanup_error_code": "", "cleanup_at": nil, "finished_at": nil, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConflict, "STRM 任务状态已变化", nil)
		}
		result = tx.Model(&models.MediaLibrary{}).Where("id = ? AND enabled = ? AND strm_enabled = ? AND artifact_generation = ? AND artifact_applied_generation < ?", run.LibraryID, true, true, run.Generation, run.Generation).Updates(map[string]any{"artifact_status": models.MediaArtifactStatusQueued, "artifact_error": "", "artifact_updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConflict, "STRM generation 已变化", nil)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := s.artifacts.ScheduleGeneration(run.LibraryID, run.Generation); err != nil {
		s.restoreRetryFailure(run, "artifact_retry_schedule_failed")
		return err
	}
	// ScheduleGeneration deliberately becomes a no-op when the current media
	// library policy no longer produces artifacts. A retry must not report
	// success and leave a queued run without an executable Job in that case.
	var current models.MediaArtifactRun
	if err := s.db.First(&current, "id = ?", run.ID).Error; err != nil {
		s.restoreRetryFailure(run, "artifact_retry_schedule_failed")
		return err
	}
	if current.Status == models.MediaArtifactStatusRunning || current.Status == models.MediaArtifactStatusCompleted {
		return nil
	}
	if current.Status == models.MediaArtifactStatusQueued && current.JobID != nil {
		var active int64
		if err := s.db.Model(&models.Job{}).Where("id = ? AND status IN ?", *current.JobID, activeJobStatuses()).Count(&active).Error; err != nil {
			s.restoreRetryFailure(run, "artifact_retry_schedule_failed")
			return err
		}
		if active == 1 {
			return nil
		}
	}
	s.restoreRetryFailure(run, "artifact_retry_not_applicable")
	return appError(CodeConflict, "当前媒体库策略不再生成该 STRM 产物", nil)
}

func (s *STRMManagementService) restoreRetryFailure(run models.MediaArtifactRun, code string) {
	now := time.Now().UTC()
	_ = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.MediaArtifactRun{}).Where("id = ? AND status = ?", run.ID, models.MediaArtifactStatusQueued).Updates(map[string]any{"status": models.MediaArtifactStatusFailed, "error_code": code, "cleanup_status": models.MediaArtifactCleanupSkipped, "cleanup_at": now, "finished_at": now, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		return tx.Model(&models.MediaLibrary{}).Where("id = ? AND artifact_generation = ? AND artifact_status = ?", run.LibraryID, run.Generation, models.MediaArtifactStatusQueued).Updates(map[string]any{"artifact_status": models.MediaArtifactStatusFailed, "artifact_error": code, "artifact_updated_at": now}).Error
	})
}

type artifactCleanupPlan struct {
	Library      models.MediaLibrary
	Run          *models.MediaArtifactRun
	Policy       mediaArtifactPolicy
	Root         string
	RootIdentity string
	Targets      map[uint]artifactCleanupTarget
	Artifacts    []models.MediaArtifact
	Snapshot     string
	Automatic    bool
}

type artifactCleanupTarget struct {
	Root         string
	RootIdentity string
}

type artifactCleanupGuard struct {
	LibraryID         uint
	Generation        uint64
	AppliedGeneration uint64
	RunID             string
	RootIdentity      string
	Snapshot          string
	Automatic         bool
}

type artifactCleanupFailure struct{ code string }

func (e *artifactCleanupFailure) Error() string { return e.code }

type artifactCleanupSkip struct{ reason string }

func (e *artifactCleanupSkip) Error() string { return e.reason }

func cleanupFailure(code string) error { return &artifactCleanupFailure{code: code} }

func cleanupStableCode(err error) string {
	var failure *artifactCleanupFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return "artifact_cleanup_failed"
}

func cleanupManifestSnapshot(artifacts []models.MediaArtifact) string {
	hash := sha256.New()
	for _, item := range artifacts {
		_, _ = fmt.Fprintf(hash, "%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t\x00%t\x00%s\n", item.ID, item.RunID, item.RelativePath, item.ContentFingerprint, item.Kind, item.TargetKind, item.Managed, item.Active, item.Status)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *STRMManagementService) buildCleanupPlan(libraryID uint, runID string, automatic bool) (artifactCleanupPlan, error) {
	var plan artifactCleanupPlan
	if err := s.db.First(&plan.Library, libraryID).Error; err != nil {
		return plan, mediaLibraryNotFound(err)
	}
	if automatic && (!plan.Library.Enabled || !plan.Library.STRMEnabled) {
		return plan, &artifactCleanupSkip{reason: "artifact_cleanup_policy_disabled"}
	}
	if !automatic && strings.TrimSpace(plan.Library.STRMLocalRoot) == "" {
		return plan, appError(CodeConflict, "媒体库没有可清理的 STRM 投影目录", nil)
	}
	root, identity, err := canonicalProjectionRoot(plan.Library.STRMLocalRoot)
	if err != nil {
		return plan, cleanupFailure("artifact_cleanup_root_unavailable")
	}
	plan.Root, plan.RootIdentity, plan.Automatic = root, identity, automatic
	if automatic {
		var run models.MediaArtifactRun
		if err := s.db.First(&run, "id = ? AND library_id = ?", runID, libraryID).Error; err != nil {
			return plan, cleanupFailure("artifact_cleanup_run_unavailable")
		}
		if err := json.Unmarshal([]byte(run.PolicyJSON), &plan.Policy); err != nil {
			return plan, cleanupFailure("artifact_cleanup_policy_invalid")
		}
		if run.Status != models.MediaArtifactStatusCompleted || run.Generation != plan.Policy.Generation || plan.Policy.LibraryID != libraryID {
			return plan, &artifactCleanupSkip{reason: "artifact_cleanup_run_incomplete"}
		}
		if !plan.Policy.CleanupEligible || plan.Policy.ScanRunID == 0 || plan.Policy.ScanPartial || !automaticCleanupScanKind(plan.Policy.ScanKind) {
			return plan, &artifactCleanupSkip{reason: "artifact_cleanup_scan_ineligible"}
		}
		if plan.Policy.TargetKind != models.MediaArtifactTargetLocalProjection || !plan.Policy.STRMEnabled {
			return plan, &artifactCleanupSkip{reason: "artifact_cleanup_policy_disabled"}
		}
		if plan.Policy.ProjectionRootIdentity == "" || plan.Policy.ProjectionRootIdentity != identity {
			return plan, cleanupFailure("artifact_cleanup_root_changed")
		}
		if plan.Library.ArtifactGeneration != run.Generation || plan.Library.ArtifactAppliedGeneration != run.Generation {
			return plan, &artifactCleanupSkip{reason: "artifact_cleanup_generation_changed"}
		}
		var scan models.MediaLibraryScanRun
		if err := s.db.First(&scan, "id = ? AND library_id = ? AND generation = ?", plan.Policy.ScanRunID, libraryID, run.Generation).Error; err != nil || scan.Status != "success" || scan.Partial || scan.Kind != plan.Policy.ScanKind {
			return plan, &artifactCleanupSkip{reason: "artifact_cleanup_scan_ineligible"}
		}
		plan.Run = &run
	}
	if err := s.db.Where("library_id = ? AND target_kind = ? AND managed = ? AND active = ?", libraryID, models.MediaArtifactTargetLocalProjection, true, false).Order("id").Find(&plan.Artifacts).Error; err != nil {
		return plan, err
	}
	targets, boundaryIdentity, err := s.resolveCleanupCandidateRoots(plan.Artifacts, root, identity, automatic)
	if err != nil {
		return plan, err
	}
	plan.Targets = targets
	plan.RootIdentity = boundaryIdentity
	plan.Snapshot = cleanupManifestSnapshot(plan.Artifacts)
	return plan, nil
}

func (s *STRMManagementService) resolveCleanupCandidateRoots(artifacts []models.MediaArtifact, currentRoot, currentRootIdentity string, automatic bool) (map[uint]artifactCleanupTarget, string, error) {
	policies := make(map[string]mediaArtifactPolicy)
	targets := make(map[uint]artifactCleanupTarget, len(artifacts))
	for _, artifact := range artifacts {
		policy, ok := policies[artifact.RunID]
		if !ok {
			var run models.MediaArtifactRun
			if err := s.db.First(&run, "id = ?", artifact.RunID).Error; err != nil || json.Unmarshal([]byte(run.PolicyJSON), &policy) != nil {
				return nil, "", cleanupFailure("artifact_cleanup_ownership_invalid")
			}
			policies[artifact.RunID] = policy
		}
		if policy.LibraryID != artifact.LibraryID || policy.TargetKind != models.MediaArtifactTargetLocalProjection {
			return nil, "", cleanupFailure("artifact_cleanup_ownership_invalid")
		}
		if !cleanupArtifactPathAllowed(artifact, policy) {
			return nil, "", cleanupFailure("artifact_cleanup_path_kind_invalid")
		}
		ownerRoot, ownerIdentity, err := canonicalProjectionRoot(policy.ProjectionRoot)
		if err != nil {
			return nil, "", cleanupFailure("artifact_cleanup_root_changed")
		}
		if policy.ProjectionRootIdentity != "" && policy.ProjectionRootIdentity != ownerIdentity {
			return nil, "", cleanupFailure("artifact_cleanup_root_changed")
		}
		if automatic {
			if ownerIdentity != currentRootIdentity {
				return nil, "", cleanupFailure("artifact_cleanup_root_changed")
			}
			ownerRoot, ownerIdentity = currentRoot, currentRootIdentity
		}
		targets[artifact.ID] = artifactCleanupTarget{Root: ownerRoot, RootIdentity: ownerIdentity}
	}
	return targets, cleanupRootBoundarySnapshot(currentRootIdentity, artifacts, targets), nil
}

func cleanupRootBoundarySnapshot(currentRootIdentity string, artifacts []models.MediaArtifact, targets map[uint]artifactCleanupTarget) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "current\x00%s\n", currentRootIdentity)
	for _, artifact := range artifacts {
		target := targets[artifact.ID]
		_, _ = fmt.Fprintf(hash, "%d\x00%s\n", artifact.ID, target.RootIdentity)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func cleanupArtifactPathAllowed(artifact models.MediaArtifact, policy mediaArtifactPolicy) bool {
	extension := strings.ToLower(filepath.Ext(filepath.FromSlash(artifact.RelativePath)))
	switch artifact.Kind {
	case models.MediaArtifactKindSTRM:
		return extension == ".strm"
	case models.MediaArtifactKindNFO:
		return extension == ".nfo"
	case models.MediaArtifactKindPoster, models.MediaArtifactKindFanart, models.MediaArtifactKindThumb, models.MediaArtifactKindImage:
		return extension == ".jpg"
	case models.MediaArtifactKindSubtitle:
		return extension == ".srt" || extension == ".ssa" || extension == ".ass"
	case models.MediaArtifactKindSourceAsset:
		for _, allowed := range policy.AssetExtensions {
			if extension == "."+strings.ToLower(strings.TrimPrefix(allowed, ".")) {
				return true
			}
		}
	}
	return false
}

func (s *STRMManagementService) PreviewCleanup(actor Actor, libraryID uint) (STRMCleanupPreview, error) {
	if !actor.Can(authz.PermissionSTRMCleanup) {
		return STRMCleanupPreview{}, appError(CodePermissionDenied, "无权清理 STRM 产物", nil)
	}
	plan, err := s.buildCleanupPlan(libraryID, "", false)
	if err != nil {
		return STRMCleanupPreview{}, err
	}
	expires := time.Now().UTC().Add(5 * time.Minute)
	claim := strmCleanupClaim{Operation: cleanupClaimOperation, ActorID: actor.User.ID, LibraryID: libraryID, Generation: plan.Library.ArtifactGeneration, AppliedGeneration: plan.Library.ArtifactAppliedGeneration, RootIdentityHash: cleanupRootIdentityHash(plan.RootIdentity), Snapshot: plan.Snapshot, ExpiresAt: expires.Unix()}
	token, err := s.signCleanupClaim(claim)
	if err != nil {
		return STRMCleanupPreview{}, err
	}
	kinds := map[string]int{}
	paths := make([]string, 0, min(50, len(plan.Artifacts)))
	for _, item := range plan.Artifacts {
		kinds[item.Kind]++
		if len(paths) < 50 {
			paths = append(paths, item.RelativePath)
		}
	}
	return STRMCleanupPreview{Count: len(plan.Artifacts), KindCounts: kinds, Paths: paths, Generation: plan.Library.ArtifactAppliedGeneration, ConfirmationToken: token, ExpiresAt: expires}, nil
}

func (s *STRMManagementService) ExecuteCleanup(actor Actor, libraryID uint, token string, request RequestContext) (int, error) {
	if !actor.Can(authz.PermissionSTRMCleanup) {
		return 0, appError(CodePermissionDenied, "无权清理 STRM 产物", nil)
	}
	claim, err := s.verifyCleanupClaim(token)
	if err != nil || claim.Operation != cleanupClaimOperation || claim.ActorID != actor.User.ID || claim.LibraryID != libraryID {
		return 0, appError(CodeInvalidRequest, "清理确认已失效，请重新预览", err)
	}
	unlock := s.lockCleanupBoundary(libraryID)
	defer unlock()
	plan, err := s.buildCleanupPlan(libraryID, "", false)
	if err != nil {
		return 0, err
	}
	if claim.ExpiresAt < time.Now().UTC().Unix() || claim.Generation != plan.Library.ArtifactGeneration || claim.AppliedGeneration != plan.Library.ArtifactAppliedGeneration || claim.RootIdentityHash != cleanupRootIdentityHash(plan.RootIdentity) || !hmac.Equal([]byte(claim.Snapshot), []byte(plan.Snapshot)) {
		return 0, appError(CodeConflict, "产物清单已变化，请重新预览", nil)
	}
	started := time.Now()
	serverlog.OperationArtifactCleanup.Event(s.log.Info()).Uint("library_id", libraryID).Uint64("generation", plan.Library.ArtifactAppliedGeneration).Msg(serverlog.OperationArtifactCleanup.Message("人工清理开始"))
	removed, removedDirectories, executeErr := s.executeCleanupPlan(context.Background(), plan)
	if executeErr != nil {
		code := cleanupStableCode(executeErr)
		_ = s.audit.Record(nil, &actor.User.ID, "strm.cleanup", "media_library", strconv.FormatUint(uint64(libraryID), 10), "failed", map[string]any{"count": removed, "directory_count": removedDirectories, "error_code": code}, request)
		serverlog.OperationArtifactCleanup.Event(s.log.Error()).Uint("library_id", libraryID).Uint64("generation", plan.Library.ArtifactAppliedGeneration).Int("removed", removed).Int("removed_directories", removedDirectories).Str("error_code", code).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationArtifactCleanup.Message("人工清理失败"))
		return removed, appError(CodeConflict, "STRM 产物清理安全检查失败", nil)
	}
	if removed > 0 {
		if err := s.db.Transaction(func(tx *gorm.DB) error {
			return s.audit.Record(tx, &actor.User.ID, "strm.cleanup", "media_library", strconv.FormatUint(uint64(libraryID), 10), "success", map[string]any{"count": removed, "directory_count": removedDirectories}, request)
		}); err != nil {
			return 0, err
		}
	}
	serverlog.OperationArtifactCleanup.Event(s.log.Info()).Uint("library_id", libraryID).Uint64("generation", plan.Library.ArtifactAppliedGeneration).Int("removed", removed).Int("removed_directories", removedDirectories).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationArtifactCleanup.Message("人工清理完成"))
	return removed, nil
}

func (s *STRMManagementService) lockCleanupBoundary(libraryID uint) func() {
	if s.libraries == nil {
		return func() {}
	}
	lock := s.libraries.scanLock(libraryID)
	lock.Lock()
	return lock.Unlock
}

func (s *STRMManagementService) executeCleanupPlan(ctx context.Context, plan artifactCleanupPlan) (int, int, error) {
	guard := artifactCleanupGuard{LibraryID: plan.Library.ID, Generation: plan.Library.ArtifactGeneration, AppliedGeneration: plan.Library.ArtifactAppliedGeneration, RootIdentity: plan.RootIdentity, Snapshot: plan.Snapshot, Automatic: plan.Automatic}
	if plan.Run != nil {
		guard.RunID = plan.Run.ID
		guard.Generation = plan.Run.Generation
		guard.AppliedGeneration = plan.Run.Generation
	}
	removed := 0
	removedDirectories := 0
	for _, artifact := range plan.Artifacts {
		if err := ctx.Err(); err != nil {
			return removed, removedDirectories, cleanupFailure("artifact_cleanup_context_canceled")
		}
		fresh, err := s.buildCleanupPlan(guard.LibraryID, guard.RunID, guard.Automatic)
		if err != nil {
			return removed, removedDirectories, err
		}
		if fresh.Library.ArtifactGeneration != guard.Generation || fresh.Library.ArtifactAppliedGeneration != guard.AppliedGeneration || fresh.RootIdentity != guard.RootIdentity || !hmac.Equal([]byte(fresh.Snapshot), []byte(guard.Snapshot)) {
			return removed, removedDirectories, cleanupFailure("artifact_cleanup_boundary_changed")
		}
		current, ok := cleanupArtifactByID(fresh.Artifacts, artifact.ID)
		if !ok || current.RunID != artifact.RunID || current.RelativePath != artifact.RelativePath || current.ContentFingerprint != artifact.ContentFingerprint || !current.Managed || current.Active || current.TargetKind != models.MediaArtifactTargetLocalProjection {
			return removed, removedDirectories, cleanupFailure("artifact_cleanup_ownership_changed")
		}
		originalStatus := current.Status
		if err := s.claimCleanupArtifact(fresh, current); err != nil {
			return removed, removedDirectories, err
		}
		expectedArtifacts := append([]models.MediaArtifact(nil), fresh.Artifacts...)
		for index := range expectedArtifacts {
			if expectedArtifacts[index].ID == current.ID {
				expectedArtifacts[index].Status = models.MediaArtifactStatusCleanup
				break
			}
		}
		expectedSnapshot := cleanupManifestSnapshot(expectedArtifacts)
		fresh, err = s.buildCleanupPlan(guard.LibraryID, guard.RunID, guard.Automatic)
		if err != nil || fresh.Library.ArtifactGeneration != guard.Generation || fresh.Library.ArtifactAppliedGeneration != guard.AppliedGeneration || fresh.RootIdentity != guard.RootIdentity || !hmac.Equal([]byte(fresh.Snapshot), []byte(expectedSnapshot)) {
			s.restoreCleanupArtifact(current, originalStatus)
			if err != nil {
				return removed, removedDirectories, err
			}
			return removed, removedDirectories, cleanupFailure("artifact_cleanup_boundary_changed")
		}
		guard.Snapshot = fresh.Snapshot
		current.Status = models.MediaArtifactStatusCleanup
		cleanupTarget, ok := fresh.Targets[current.ID]
		if !ok {
			s.restoreCleanupArtifact(current, originalStatus)
			return removed, removedDirectories, cleanupFailure("artifact_cleanup_ownership_changed")
		}
		target, exists, err := safeCleanupTarget(cleanupTarget.Root, cleanupTarget.RootIdentity, current.RelativePath)
		if err != nil {
			s.restoreCleanupArtifact(current, originalStatus)
			return removed, removedDirectories, err
		}
		if exists {
			if _, stillExists, err := safeCleanupTarget(cleanupTarget.Root, cleanupTarget.RootIdentity, current.RelativePath); err != nil || !stillExists {
				s.restoreCleanupArtifact(current, originalStatus)
				if err != nil {
					return removed, removedDirectories, err
				}
				return removed, removedDirectories, cleanupFailure("artifact_cleanup_target_changed")
			}
			if err := s.removeFile(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.restoreCleanupArtifact(current, originalStatus)
				return removed, removedDirectories, cleanupFailure("artifact_cleanup_delete_failed")
			}
		}
		pruned, err := s.removeEmptyCleanupAncestors(ctx, cleanupTarget.Root, cleanupTarget.RootIdentity, target)
		removedDirectories += pruned
		if err != nil {
			s.restoreCleanupArtifact(current, originalStatus)
			return removed, removedDirectories, err
		}
		if err := s.deleteClaimedCleanupArtifact(plan, current); err != nil {
			return removed, removedDirectories, err
		}
		removed++
		next, err := s.buildCleanupPlan(guard.LibraryID, guard.RunID, guard.Automatic)
		if err != nil {
			return removed, removedDirectories, err
		}
		guard.RootIdentity = next.RootIdentity
		guard.Snapshot = next.Snapshot
	}
	return removed, removedDirectories, nil
}

func (s *STRMManagementService) claimCleanupArtifact(plan artifactCleanupPlan, artifact models.MediaArtifact) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var library models.MediaLibrary
		if err := tx.First(&library, plan.Library.ID).Error; err != nil {
			return err
		}
		if library.ArtifactGeneration != plan.Library.ArtifactGeneration || library.ArtifactAppliedGeneration != plan.Library.ArtifactAppliedGeneration {
			return cleanupFailure("artifact_cleanup_generation_changed")
		}
		if plan.Run != nil && (plan.Run.ID == "" || plan.Run.Generation != library.ArtifactAppliedGeneration) {
			return cleanupFailure("artifact_cleanup_run_changed")
		}
		result := tx.Model(&models.MediaArtifact{}).Where("id = ? AND library_id = ? AND run_id = ? AND target_kind = ? AND kind = ? AND managed = ? AND active = ? AND relative_path = ? AND content_fingerprint = ? AND status = ?", artifact.ID, artifact.LibraryID, artifact.RunID, models.MediaArtifactTargetLocalProjection, artifact.Kind, true, false, artifact.RelativePath, artifact.ContentFingerprint, artifact.Status).Update("status", models.MediaArtifactStatusCleanup)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return cleanupFailure("artifact_cleanup_ownership_changed")
		}
		return nil
	})
}

func (s *STRMManagementService) restoreCleanupArtifact(artifact models.MediaArtifact, status string) {
	_ = s.db.Model(&models.MediaArtifact{}).Where("id = ? AND run_id = ? AND managed = ? AND active = ? AND status = ?", artifact.ID, artifact.RunID, true, false, models.MediaArtifactStatusCleanup).Update("status", status).Error
}

func (s *STRMManagementService) deleteClaimedCleanupArtifact(plan artifactCleanupPlan, artifact models.MediaArtifact) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var boundaryCount int64
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ? AND artifact_generation = ? AND artifact_applied_generation = ?", plan.Library.ID, plan.Library.ArtifactGeneration, plan.Library.ArtifactAppliedGeneration).Count(&boundaryCount).Error; err != nil {
			return err
		}
		if boundaryCount != 1 {
			return cleanupFailure("artifact_cleanup_generation_changed")
		}
		result := tx.Where("id = ? AND library_id = ? AND run_id = ? AND target_kind = ? AND kind = ? AND managed = ? AND active = ? AND relative_path = ? AND content_fingerprint = ? AND status = ?", artifact.ID, artifact.LibraryID, artifact.RunID, models.MediaArtifactTargetLocalProjection, artifact.Kind, true, false, artifact.RelativePath, artifact.ContentFingerprint, models.MediaArtifactStatusCleanup).Delete(&models.MediaArtifact{})
		if result.Error != nil {
			return cleanupFailure("artifact_cleanup_manifest_delete_failed")
		}
		if result.RowsAffected != 1 {
			return cleanupFailure("artifact_cleanup_ownership_changed")
		}
		if plan.Run != nil {
			result = tx.Model(&models.MediaArtifactRun{}).Where("id = ? AND library_id = ? AND generation = ? AND status = ? AND cleanup_status = ?", plan.Run.ID, plan.Run.LibraryID, plan.Run.Generation, models.MediaArtifactStatusCompleted, models.MediaArtifactCleanupRunning).Update("removed_count", gorm.Expr("removed_count + 1"))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return cleanupFailure("artifact_cleanup_run_changed")
			}
		}
		return nil
	})
}

func cleanupArtifactByID(artifacts []models.MediaArtifact, id uint) (models.MediaArtifact, bool) {
	for _, artifact := range artifacts {
		if artifact.ID == id {
			return artifact, true
		}
	}
	return models.MediaArtifact{}, false
}

func safeCleanupTarget(root, rootIdentity, relativePath string) (string, bool, error) {
	cleanRelative := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(strings.TrimSpace(relativePath), "/")))
	if cleanRelative == "." || cleanRelative == ".." || filepath.IsAbs(cleanRelative) || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return "", false, cleanupFailure("artifact_cleanup_path_invalid")
	}
	target, err := storagefs.Constrain(root, filepath.Join(root, cleanRelative))
	if err != nil {
		return "", false, cleanupFailure("artifact_cleanup_path_outside_root")
	}
	_, currentIdentity, err := canonicalProjectionRoot(root)
	if err != nil || currentIdentity != rootIdentity {
		return "", false, cleanupFailure("artifact_cleanup_root_changed")
	}
	parent := filepath.Dir(target)
	relativeParent, err := filepath.Rel(root, parent)
	if err != nil || filepath.IsAbs(relativeParent) || relativeParent == ".." || strings.HasPrefix(relativeParent, ".."+string(filepath.Separator)) {
		return "", false, cleanupFailure("artifact_cleanup_path_outside_root")
	}
	current := root
	if relativeParent != "." {
		for _, part := range strings.Split(relativeParent, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, statErr := os.Lstat(current)
			if errors.Is(statErr, os.ErrNotExist) {
				return target, false, nil
			}
			if statErr != nil || !info.IsDir() || storagefs.IsReparsePoint(current, info) {
				return "", false, cleanupFailure("artifact_cleanup_reparse_boundary")
			}
		}
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return target, false, nil
		}
		return "", false, cleanupFailure("artifact_cleanup_boundary_unreadable")
	}
	if _, err := storagefs.Constrain(rootIdentity, storagefs.NormalizeForComparison(resolvedParent)); err != nil {
		return "", false, cleanupFailure("artifact_cleanup_reparse_boundary")
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return target, false, nil
	}
	if err != nil {
		return "", false, cleanupFailure("artifact_cleanup_target_unreadable")
	}
	if info.IsDir() || storagefs.IsReparsePoint(target, info) {
		return "", false, cleanupFailure("artifact_cleanup_target_invalid")
	}
	return target, true, nil
}

// removeEmptyCleanupAncestors converges only the lexical ancestor chain owned by
// one claimed artifact. It intentionally uses non-recursive directory removal:
// a user file, an active artifact, or any other entry stops convergence without
// being touched. The projection root itself is never passed to removeDir.
func (s *STRMManagementService) removeEmptyCleanupAncestors(ctx context.Context, root, rootIdentity, target string) (int, error) {
	root = filepath.Clean(root)
	current := filepath.Dir(target)
	if _, err := storagefs.Constrain(root, current); err != nil {
		return 0, cleanupFailure("artifact_cleanup_path_outside_root")
	}
	removed := 0
	for storagefs.NormalizeForComparison(current) != storagefs.NormalizeForComparison(root) {
		if err := ctx.Err(); err != nil {
			return removed, cleanupFailure("artifact_cleanup_context_canceled")
		}
		parent := filepath.Dir(current)
		directory, exists, err := safeCleanupDirectory(root, rootIdentity, current)
		if err != nil {
			return removed, err
		}
		if !exists {
			current = parent
			continue
		}
		empty, exists, err := cleanupDirectoryEmpty(directory)
		if err != nil {
			return removed, cleanupFailure("artifact_cleanup_directory_unreadable")
		}
		if !exists {
			current = parent
			continue
		}
		if !empty {
			return removed, nil
		}
		// Revalidate after reading and immediately before the destructive call so
		// a directory replaced by a symlink/junction is rejected.
		directory, exists, err = safeCleanupDirectory(root, rootIdentity, current)
		if err != nil {
			return removed, err
		}
		if !exists {
			current = parent
			continue
		}
		if err := s.removeDir(directory); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				current = parent
				continue
			}
			// A concurrent creator wins over cleanup. Confirming a new entry makes
			// this an ordinary convergence stop rather than a failed deletion.
			empty, exists, inspectErr := cleanupDirectoryEmpty(directory)
			if inspectErr != nil {
				return removed, cleanupFailure("artifact_cleanup_directory_unreadable")
			}
			if !exists {
				current = parent
				continue
			}
			if !empty {
				return removed, nil
			}
			return removed, cleanupFailure("artifact_cleanup_directory_delete_failed")
		}
		removed++
		current = parent
	}
	return removed, nil
}

func safeCleanupDirectory(root, rootIdentity, candidate string) (string, bool, error) {
	directory, err := storagefs.Constrain(root, candidate)
	if err != nil {
		return "", false, cleanupFailure("artifact_cleanup_path_outside_root")
	}
	_, currentIdentity, err := canonicalProjectionRoot(root)
	if err != nil || currentIdentity != rootIdentity {
		return "", false, cleanupFailure("artifact_cleanup_root_changed")
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil || filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false, cleanupFailure("artifact_cleanup_path_outside_root")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return directory, false, nil
		}
		if statErr != nil {
			return "", false, cleanupFailure("artifact_cleanup_directory_unreadable")
		}
		if !info.IsDir() || storagefs.IsReparsePoint(current, info) {
			return "", false, cleanupFailure("artifact_cleanup_reparse_boundary")
		}
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return directory, false, nil
		}
		return "", false, cleanupFailure("artifact_cleanup_directory_unreadable")
	}
	if _, err := storagefs.Constrain(rootIdentity, storagefs.NormalizeForComparison(resolved)); err != nil {
		return "", false, cleanupFailure("artifact_cleanup_reparse_boundary")
	}
	return directory, true, nil
}

func cleanupDirectoryEmpty(path string) (empty bool, exists bool, err error) {
	directory, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, true, err
	}
	_, readErr := directory.Readdirnames(1)
	closeErr := directory.Close()
	if readErr == nil {
		return false, true, closeErr
	}
	if !errors.Is(readErr, io.EOF) {
		return false, true, readErr
	}
	if closeErr != nil {
		return false, true, closeErr
	}
	return true, true, nil
}

func (s *STRMManagementService) AutoCleanup(ctx context.Context, runID string) ArtifactCleanupResult {
	var run models.MediaArtifactRun
	if err := s.db.First(&run, "id = ?", runID).Error; err != nil {
		return ArtifactCleanupResult{ErrorCode: "artifact_cleanup_run_unavailable"}
	}
	unlock := s.lockCleanupBoundary(run.LibraryID)
	defer unlock()
	if err := s.db.First(&run, "id = ?", runID).Error; err != nil {
		return ArtifactCleanupResult{ErrorCode: "artifact_cleanup_run_unavailable"}
	}
	if run.Status == models.MediaArtifactStatusCompleted {
		switch run.CleanupStatus {
		case models.MediaArtifactCleanupCompleted:
			return ArtifactCleanupResult{}
		case models.MediaArtifactCleanupSkipped:
			return ArtifactCleanupResult{Skipped: true}
		}
		now := time.Now().UTC()
		result := s.db.Model(&models.MediaArtifactRun{}).Where("id = ? AND status = ? AND cleanup_status IN ?", run.ID, models.MediaArtifactStatusCompleted, []string{"", models.MediaArtifactCleanupPending, models.MediaArtifactCleanupRunning, models.MediaArtifactCleanupFailed}).Updates(map[string]any{"cleanup_status": models.MediaArtifactCleanupRunning, "cleanup_error_code": "", "cleanup_at": nil, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return ArtifactCleanupResult{ErrorCode: "artifact_cleanup_state_failed"}
		}
		run.CleanupStatus = models.MediaArtifactCleanupRunning
	}
	started := time.Now()
	serverlog.OperationArtifactCleanup.Event(s.log.Info()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Msg(serverlog.OperationArtifactCleanup.Message("自动清理开始"))
	plan, err := s.buildCleanupPlan(run.LibraryID, run.ID, true)
	if err != nil {
		var skip *artifactCleanupSkip
		if errors.As(err, &skip) {
			if persistErr := s.persistAutoCleanup(run, 0, 0, "", "skipped", skip.reason); persistErr != nil {
				code := "artifact_cleanup_state_failed"
				serverlog.OperationArtifactCleanup.Event(s.log.Error()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Str("error_code", code).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationArtifactCleanup.Message("自动清理失败"))
				return ArtifactCleanupResult{ErrorCode: code}
			}
			serverlog.OperationArtifactCleanup.Event(s.log.Info()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Str("status", "skipped").Str("reason_code", skip.reason).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationArtifactCleanup.Message("自动清理跳过"))
			return ArtifactCleanupResult{Skipped: true}
		}
		code := cleanupStableCode(err)
		_ = s.persistAutoCleanup(run, 0, 0, code, "failed", code)
		serverlog.OperationArtifactCleanup.Event(s.log.Error()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Str("error_code", code).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationArtifactCleanup.Message("自动清理失败"))
		return ArtifactCleanupResult{ErrorCode: code}
	}
	removed, removedDirectories, executeErr := s.executeCleanupPlan(ctx, plan)
	code := ""
	outcome := "success"
	if executeErr != nil {
		code = cleanupStableCode(executeErr)
		outcome = "failed"
	}
	if err := s.persistAutoCleanup(run, removed, removedDirectories, code, outcome, code); err != nil {
		code = "artifact_cleanup_state_failed"
	}
	if code != "" {
		serverlog.OperationArtifactCleanup.Event(s.log.Error()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Int("removed", removed).Int("removed_directories", removedDirectories).Str("error_code", code).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationArtifactCleanup.Message("自动清理失败"))
		return ArtifactCleanupResult{Removed: removed, ErrorCode: code}
	}
	serverlog.OperationArtifactCleanup.Event(s.log.Info()).Uint("library_id", run.LibraryID).Str("task_id", run.ID).Uint64("generation", run.Generation).Int("removed", removed).Int("removed_directories", removedDirectories).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationArtifactCleanup.Message("自动清理完成"))
	return ArtifactCleanupResult{Removed: removed}
}

func (s *STRMManagementService) persistAutoCleanup(run models.MediaArtifactRun, removed, removedDirectories int, errorCode, outcome, auditCode string) error {
	now := time.Now().UTC()
	return s.db.Transaction(func(tx *gorm.DB) error {
		cleanupStatus := models.MediaArtifactCleanupCompleted
		switch outcome {
		case "failed":
			cleanupStatus = models.MediaArtifactCleanupFailed
		case "skipped":
			cleanupStatus = models.MediaArtifactCleanupSkipped
		}
		if err := tx.Model(&models.MediaArtifactRun{}).Where("id = ?", run.ID).Updates(map[string]any{"cleanup_status": cleanupStatus, "cleanup_error_code": errorCode, "cleanup_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		var current models.MediaArtifactRun
		if err := tx.Select("removed_count").First(&current, "id = ?", run.ID).Error; err != nil {
			return err
		}
		if outcome != "skipped" {
			result := tx.Model(&models.MediaLibrary{}).Where("id = ? AND artifact_generation = ? AND artifact_applied_generation = ?", run.LibraryID, run.Generation, run.Generation).Updates(map[string]any{"artifact_cleanup_removed": current.RemovedCount, "artifact_cleanup_error": errorCode, "artifact_cleanup_at": now})
			if result.Error != nil {
				return result.Error
			}
		}
		metadata := map[string]any{"run_id": run.ID, "generation": run.Generation, "count": removed, "directory_count": removedDirectories, "total_count": current.RemovedCount}
		if auditCode != "" {
			if outcome == "skipped" {
				metadata["reason_code"] = auditCode
			} else {
				metadata["error_code"] = auditCode
			}
		}
		return s.audit.Record(tx, nil, "strm.cleanup.auto", "media_library", strconv.FormatUint(uint64(run.LibraryID), 10), outcome, metadata, RequestContext{})
	})
}

func (s *STRMManagementService) signCleanupClaim(claim strmCleanupClaim) (string, error) {
	if claim.Operation != cleanupClaimOperation {
		return "", errors.New("invalid cleanup operation")
	}
	body, err := json.Marshal(claim)
	if err != nil {
		return "", err
	}
	if len(body) > 1024 {
		return "", errors.New("cleanup claim is too large")
	}
	mac := hmac.New(sha256.New, s.cleanupKey)
	_, _ = mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func cleanupRootIdentityHash(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func (s *STRMManagementService) verifyCleanupClaim(token string) (strmCleanupClaim, error) {
	var claim strmCleanupClaim
	if len(token) == 0 || len(token) > 2048 || strings.Count(token, ".") != 1 {
		return claim, errors.New("invalid token")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(parts[0]) == 0 || len(parts[0]) > 1368 || len(parts[1]) != 43 {
		return claim, errors.New("invalid token")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claim, err
	}
	if len(body) == 0 || len(body) > 1024 {
		return claim, errors.New("invalid token size")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claim, err
	}
	if len(signature) != sha256.Size {
		return claim, errors.New("invalid token size")
	}
	if base64.RawURLEncoding.EncodeToString(body) != parts[0] || base64.RawURLEncoding.EncodeToString(signature) != parts[1] {
		return claim, errors.New("non-canonical token")
	}
	mac := hmac.New(sha256.New, s.cleanupKey)
	_, _ = mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return claim, errors.New("invalid signature")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claim); err != nil {
		return claim, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return claim, errors.New("invalid token body")
	}
	if claim.Operation != cleanupClaimOperation || claim.ActorID == 0 || claim.LibraryID == 0 || claim.Generation == 0 || claim.AppliedGeneration == 0 || len(claim.RootIdentityHash) != sha256.Size*2 || len(claim.Snapshot) != sha256.Size*2 || claim.ExpiresAt <= 0 {
		return claim, errors.New("invalid token claim")
	}
	return claim, nil
}

type STRMReconcileWorker struct{ service *STRMManagementService }

func NewSTRMReconcileWorker(service *STRMManagementService) *STRMReconcileWorker {
	return &STRMReconcileWorker{service: service}
}
func (w *STRMReconcileWorker) Run(ctx context.Context, _ JobRuntime, job ClaimedJob) WorkerResult {
	if w == nil || w.service == nil {
		return WorkerResult{ErrorCode: "strm_service_unavailable", ErrorMessage: "STRM 服务不可用"}
	}
	var payload strmReconcilePayload
	if err := json.Unmarshal([]byte(job.Job.PayloadJSON), &payload); err != nil {
		return WorkerResult{ErrorCode: "strm_payload_invalid", ErrorMessage: "STRM 任务参数无效"}
	}
	if _, err := w.service.libraries.ReconcileSTRM(ctx, payload.LibraryID, payload.Mode); err != nil {
		return WorkerResult{ErrorCode: ErrorCode(err), ErrorMessage: "STRM 媒体库刷新失败"}
	}
	return WorkerResult{}
}
