package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/releaseversion"
	"gorm.io/gorm"
)

type TransferService struct {
	db                      *gorm.DB
	audit                   *AuditService
	queue                   *QueueService
	log                     zerolog.Logger
	seeding                 *SeedingService
	connections             *ConnectionService
	verifyCompletedManifest func(context.Context, *models.DownloadTask, downloadpkg.Manifest) (downloadpkg.Manifest, error)
}

type transferJobPayload struct {
	TransferTaskID string `json:"transfer_task_id"`
}

func NewTransferService(db *gorm.DB, audit *AuditService, queue *QueueService, log zerolog.Logger) *TransferService {
	return &TransferService{db: db, audit: audit, queue: queue, log: log}
}

func (s *TransferService) SetSeedingService(seeding *SeedingService) { s.seeding = seeding }
func (s *TransferService) SetConnectionService(connections *ConnectionService) {
	s.connections = connections
}

func (s *TransferService) SetCompletedManifestVerifier(verifier func(context.Context, *models.DownloadTask, downloadpkg.Manifest) (downloadpkg.Manifest, error)) {
	s.verifyCompletedManifest = verifier
}

// Delete removes a terminal media-organization record and its queue history.
// Download/provider records and all source or library files remain untouched.
func (s *TransferService) Delete(actor Actor, id string, request RequestContext) error {
	id = strings.TrimSpace(id)
	var deletedJob models.Job
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var task models.TransferTask
		if err := tx.First(&task, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return appError(CodeNotFound, "媒体整理任务不存在", err)
			}
			return err
		}
		var job models.Job
		if err := tx.First(&job, "id = ?", task.JobID).Error; err != nil {
			return err
		}
		if !actor.Can(authz.PermissionJobsControlAll) && (task.OwnerID != actor.User.ID || !actor.Can(authz.PermissionJobsControlOwn)) {
			return appError(CodePermissionDenied, "无权删除该媒体整理记录", nil)
		}
		if !isDeletableTransferJobStatus(job.Status) {
			return appError(CodeQueueStateConflict, "仅失败、已取消或已完成的媒体整理记录可以删除", nil)
		}
		if err := s.audit.Record(tx, &actor.User.ID, "transfer.delete", "transfer_task", task.ID, "success", map[string]any{
			"job_status": job.Status,
			"phase":      task.Phase,
			"library_id": task.LibraryID,
		}, request); err != nil {
			return err
		}
		if err := tx.Delete(&task).Error; err != nil {
			return err
		}
		if err := tx.Delete(&job).Error; err != nil {
			return err
		}
		deletedJob = job
		return nil
	})
	if err != nil {
		return err
	}
	s.queue.publish(deletedJob, "job.deleted")
	return nil
}

func isDeletableTransferJobStatus(status string) bool {
	return status == models.JobStatusFailed || status == models.JobStatusCancelled || status == models.JobStatusCompleted
}

func (s *TransferService) Enqueue(download models.DownloadTask, manifest downloadpkg.Manifest) error {
	return s.EnqueuePackage(download, manifest, manifest)
}

func (s *TransferService) EnqueuePackage(download models.DownloadTask, manifest, sourceManifest downloadpkg.Manifest) error {
	if download.TargetLibraryID == nil || download.TargetStorageID == nil {
		return nil
	}
	if err := validateAutomaticTransferSnapshot(download, manifest); err != nil {
		return appError(CodeTransferMediaUnrecognized, "媒体识别结果不可信，未创建自动入库任务", err)
	}
	var existing models.TransferTask
	if err := s.db.Where("download_task_id = ?", download.ID).First(&existing).Error; err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	raw, err := json.Marshal(manifest)
	if err != nil || len(raw) > 1024*1024 {
		return appError(CodeInvalidRequest, "下载文件清单过大，无法创建入库任务", err)
	}
	sourceRaw, err := json.Marshal(sourceManifest)
	if err != nil || len(sourceRaw) > 1024*1024 || !sourceManifest.Complete {
		return appError(CodeInvalidRequest, "完整下载文件清单无效，无法创建入库任务", err)
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	record := models.TransferTask{ID: id, OwnerID: download.OwnerID, DownloadTaskID: download.ID, LibraryID: *download.TargetLibraryID, LibraryName: download.TargetLibraryName, ManifestJSON: string(raw), SourceManifestJSON: string(sourceRaw), Phase: models.TransferTaskStatusQueued, CleanupStatus: models.TransferCleanupPending, TotalFiles: len(manifest.Files), CreatedAt: now, UpdatedAt: now}
	provider := models.StorageTypeLocal
	resourceKey := "library:" + strconv.FormatUint(uint64(*download.TargetLibraryID), 10)
	if download.TargetStorageType == models.StorageTypePan115 && download.TargetConnectionID != nil {
		provider = cloudpkg.ProviderPan115
		resourceKey = "connection:" + strconv.FormatUint(uint64(*download.TargetConnectionID), 10)
	}
	_, err = s.queue.EnqueueWith(EnqueueJobInput{OwnerID: download.OwnerID, JobType: "transfer", DisplayName: "入库：" + download.DisplayName, Provider: provider, ResourceKey: resourceKey, Payload: transferJobPayload{TransferTaskID: id}}, func(tx *gorm.DB, job models.Job) error {
		record.JobID = job.ID
		return tx.Create(&record).Error
	})
	if err != nil {
		var raced models.TransferTask
		if lookup := s.db.Where("download_task_id = ?", download.ID).First(&raced).Error; lookup == nil {
			return nil
		}
	}
	if err == nil {
		serverlog.OperationMediaTransfer.Event(s.log.Info()).Str("task_id", record.ID).Str("download_task_id", download.ID).Uint("library_id", record.LibraryID).Int("files", len(manifest.Files)).Msg(serverlog.OperationMediaTransfer.Message("已进入整理队列"))
	}
	return err
}

type TransferWorker struct {
	service *TransferService
}

func NewTransferWorker(service *TransferService) *TransferWorker {
	return &TransferWorker{service: service}
}

type transferPlanItem struct {
	Source      string
	Destination string
	Relative    string
	Size        int64
	Group       string
}

const (
	maxTransferPlanSummaryItems = 100
	maxTransferPlanSummaryBytes = 48 * 1024
)

type TransferPlanSummaryItem struct {
	RelativePath string `json:"relative_path"`
	Kind         string `json:"kind"`
	Size         int64  `json:"size"`
	Result       string `json:"result"`
}

type TransferPlanSummary struct {
	Items      []TransferPlanSummaryItem `json:"items"`
	TotalFiles int                       `json:"total_files"`
	Truncated  bool                      `json:"truncated"`
}

func (w *TransferWorker) Run(ctx context.Context, runtime JobRuntime, job ClaimedJob) WorkerResult {
	started := time.Now()
	var payload transferJobPayload
	if err := json.Unmarshal([]byte(job.Job.PayloadJSON), &payload); err != nil || payload.TransferTaskID == "" {
		return WorkerResult{ErrorCode: "transfer_payload_invalid", ErrorMessage: "入库任务参数无效"}
	}
	var task models.TransferTask
	if err := w.service.db.First(&task, "id = ?", payload.TransferTaskID).Error; err != nil {
		return WorkerResult{ErrorCode: "transfer_task_missing", ErrorMessage: "入库任务不存在"}
	}
	serverlog.OperationMediaTransfer.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Int("total_files", task.TotalFiles).Msg(serverlog.OperationMediaTransfer.Message("开始规划并入库"))
	if task.Phase == models.TransferTaskStatusCompleted {
		return w.finishCompletedTransfer(ctx, task)
	}
	var download models.DownloadTask
	if err := w.service.db.First(&download, "id = ?", task.DownloadTaskID).Error; err != nil {
		return w.fail(task, "transfer_download_missing", "原下载任务不存在")
	}
	var manifest downloadpkg.Manifest
	if err := json.Unmarshal([]byte(task.ManifestJSON), &manifest); err != nil || len(manifest.Files) == 0 {
		return w.fail(task, "transfer_manifest_invalid", "下载文件清单无效")
	}
	if err := validateAutomaticTransferSnapshot(download, manifest); err != nil {
		if w.service.verifyCompletedManifest == nil {
			return w.fail(task, CodeTransferMediaUnrecognized, "媒体未识别，未自动入库")
		}
		// A legacy/failed transfer may still carry a public plan projection and
		// provider checkpoint produced from an older, unfiltered manifest. Clear
		// those caches before re-verification so a failed retry cannot continue to
		// display or resume advertisement files that are no longer trustworthy.
		if persistErr := w.service.db.Model(&task).Updates(map[string]any{
			"phase":             models.TransferTaskStatusPlanning,
			"processed_files":   0,
			"total_files":       0,
			"plan_summary_json": "",
			"cloud_state_json":  "",
			"updated_at":        time.Now().UTC(),
		}).Error; persistErr != nil {
			return w.fail(task, "transfer_state_persist_failed", "旧入库计划清理失败")
		}
		task.Phase = models.TransferTaskStatusPlanning
		task.ProcessedFiles = 0
		task.TotalFiles = 0
		task.PlanSummaryJSON = ""
		task.CloudStateJSON = ""
		verifiedManifest, verifyErr := w.service.verifyCompletedManifest(ctx, &download, manifest)
		if verifyErr != nil {
			return w.fail(task, CodeTransferMediaUnrecognized, "媒体未识别，未自动入库")
		}
		raw, marshalErr := json.Marshal(verifiedManifest)
		if marshalErr != nil || len(raw) > 1024*1024 {
			return w.fail(task, "transfer_manifest_invalid", "下载文件清单无效")
		}
		if persistErr := w.service.db.Model(&task).Updates(map[string]any{"manifest_json": string(raw), "total_files": len(verifiedManifest.Files), "updated_at": time.Now().UTC()}).Error; persistErr != nil {
			return w.fail(task, "transfer_state_persist_failed", "入库清单保存失败")
		}
		manifest = verifiedManifest
		task.ManifestJSON = string(raw)
		task.TotalFiles = len(verifiedManifest.Files)
	}
	if download.TargetStorageType == models.StorageTypePan115 {
		if download.ProviderType == models.DownloaderTypePluginHTTP {
			return w.runCloudUpload(ctx, runtime, task, download, manifest, started)
		}
		return w.runCloudTransfer(ctx, runtime, task, download, manifest, started)
	}
	_ = w.service.db.Model(&task).Updates(map[string]any{"phase": models.TransferTaskStatusPlanning, "updated_at": time.Now().UTC()}).Error
	plan, targetRoot, err := buildTransferPlan(download, manifest)
	if err != nil {
		return w.fail(task, "transfer_plan_invalid", "无法生成安全的入库计划")
	}
	summary, err := newTransferPlanSummary(plan)
	if err != nil {
		return w.fail(task, "transfer_plan_invalid", "无法生成安全的入库计划")
	}
	encodedSummary, err := encodeTransferPlanSummary(summary)
	if err != nil {
		return w.fail(task, "transfer_plan_invalid", "无法生成安全的入库计划")
	}
	if err := w.service.db.Model(&task).Updates(map[string]any{"plan_summary_json": encodedSummary, "total_files": len(plan), "updated_at": time.Now().UTC()}).Error; err != nil {
		return w.fail(task, "transfer_state_persist_failed", "入库计划保存失败")
	}
	task.PlanSummaryJSON = encodedSummary
	task.TotalFiles = len(plan)
	policy := download.ConflictPolicy
	if response := transferActionResponse(job.Job.CheckpointJSON); response != "" {
		policy = response
	}
	conflicts := 0
	for _, item := range plan {
		if _, err := os.Lstat(item.Destination); err == nil {
			if transferAlreadyApplied(download.TransferMode, item) {
				continue
			}
			conflicts++
		} else if !errors.Is(err, os.ErrNotExist) {
			return w.fail(task, "transfer_target_unavailable", "入库目标不可用")
		}
	}
	if conflicts > 0 && policy == models.MediaLibraryConflictAsk {
		return WorkerResult{Wait: &WaitForAction{ActionType: "transfer_conflict", Prompt: "目标媒体库存在同名文件，请选择处理方式", Options: []string{models.MediaLibraryConflictOverwrite, models.MediaLibraryConflictSkip, models.MediaLibraryConflictRename}, Preview: map[string]string{"媒体库": task.LibraryName, "冲突文件": strconv.Itoa(conflicts)}, Checkpoint: map[string]any{"conflict_count": conflicts}}}
	}
	_ = w.service.db.Model(&task).Updates(map[string]any{"phase": models.TransferTaskStatusTransferring, "updated_at": time.Now().UTC()}).Error
	renameGroups := map[string]string{}
	if policy == models.MediaLibraryConflictRename {
		for _, item := range plan {
			if _, ok := renameGroups[item.Group]; ok {
				continue
			}
			hasConflict := false
			for _, grouped := range plan {
				if grouped.Group != item.Group {
					continue
				}
				if _, err := os.Lstat(grouped.Destination); err == nil && !transferAlreadyApplied(download.TransferMode, grouped) {
					hasConflict = true
					break
				}
			}
			if hasConflict {
				base, err := availableTransferGroupBase(plan, item.Group)
				if err != nil {
					return w.fail(task, "transfer_conflict_failed", "无法处理目标文件冲突")
				}
				renameGroups[item.Group] = base
			}
		}
	}
	total := int64(len(plan))
	for index, item := range plan {
		if err := ctx.Err(); err != nil {
			return WorkerResult{}
		}
		if base, ok := renameGroups[item.Group]; ok {
			item.Destination = base + strings.TrimPrefix(item.Destination, item.Group)
		}
		destination, skip, err := resolveTransferConflict(item, policy, download.TransferMode)
		if err != nil {
			return w.fail(task, "transfer_conflict_failed", "无法处理目标文件冲突")
		}
		if !skip {
			if err := executeTransfer(download.TransferMode, item.Source, destination, targetRoot); err != nil {
				return w.fail(task, "transfer_write_failed", "文件入库失败")
			}
		}
		if index < len(summary.Items) {
			relative, relativeErr := filepath.Rel(targetRoot, destination)
			if relativeErr != nil {
				return w.fail(task, "transfer_plan_invalid", "入库结果路径无效")
			}
			safeRelative, relativeErr := sanitizeTransferRelativePath(filepath.ToSlash(relative))
			if relativeErr != nil {
				return w.fail(task, "transfer_plan_invalid", "入库结果路径无效")
			}
			summary.Items[index].RelativePath = safeRelative
			if skip {
				summary.Items[index].Result = "skipped"
			} else {
				summary.Items[index].Result = "completed"
			}
		}
		encodedSummary, err = encodeTransferPlanSummary(summary)
		if err != nil {
			return w.fail(task, "transfer_state_persist_failed", "入库结果保存失败")
		}
		processed := int64(index + 1)
		progress := float64(processed) * 100 / float64(total)
		if err := runtime.Heartbeat(&progress, &processed, &total, nil, nil); err != nil {
			return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "入库任务租约已失效"}
		}
		if err := w.service.db.Model(&task).Updates(map[string]any{"processed_files": processed, "plan_summary_json": encodedSummary, "updated_at": time.Now().UTC()}).Error; err != nil {
			return w.fail(task, "transfer_state_persist_failed", "入库结果保存失败")
		}
	}
	now := time.Now().UTC()
	err = w.service.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", task.LibraryID).UpdateColumn("dirty_generation", gorm.Expr("dirty_generation + 1")).Error; err != nil {
			return err
		}
		if err := tx.Model(&task).Updates(map[string]any{"phase": models.TransferTaskStatusCompleted, "processed_files": len(plan), "finished_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return w.service.audit.Record(tx, &task.OwnerID, "transfer.complete", "transfer_task", task.ID, "success", map[string]any{"download_task_id": task.DownloadTaskID, "media_library_id": task.LibraryID, "mode": download.TransferMode, "files": len(plan)}, RequestContext{})
	})
	if err != nil {
		return w.fail(task, "transfer_state_persist_failed", "入库结果保存失败")
	}
	serverlog.OperationMediaTransfer.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Str("transfer_mode", download.TransferMode).Int("files", len(plan)).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationMediaTransfer.Message("完成"))
	task.Phase = models.TransferTaskStatusCompleted
	return w.finishCompletedTransfer(ctx, task)
}

func newTransferPlanSummary(plan []transferPlanItem) (TransferPlanSummary, error) {
	summary := TransferPlanSummary{Items: make([]TransferPlanSummaryItem, 0, min(len(plan), maxTransferPlanSummaryItems)), TotalFiles: len(plan), Truncated: len(plan) > maxTransferPlanSummaryItems}
	for _, item := range plan {
		if len(summary.Items) >= maxTransferPlanSummaryItems {
			break
		}
		relative, err := sanitizeTransferRelativePath(item.Relative)
		if err != nil {
			return TransferPlanSummary{}, err
		}
		kind := "sidecar"
		if isVideoFile(relative) {
			kind = "video"
		}
		candidate := append(summary.Items, TransferPlanSummaryItem{RelativePath: relative, Kind: kind, Size: item.Size, Result: "planned"})
		test := summary
		test.Items = candidate
		encoded, err := json.Marshal(test)
		if err != nil {
			return TransferPlanSummary{}, err
		}
		if len(encoded) > maxTransferPlanSummaryBytes {
			summary.Truncated = true
			break
		}
		summary.Items = candidate
	}
	return summary, nil
}

func encodeTransferPlanSummary(summary TransferPlanSummary) (string, error) {
	if summary.TotalFiles < 0 || len(summary.Items) > maxTransferPlanSummaryItems || summary.TotalFiles < len(summary.Items) {
		return "", errors.New("invalid transfer plan summary")
	}
	for index := range summary.Items {
		relative, err := sanitizeTransferRelativePath(summary.Items[index].RelativePath)
		if err != nil {
			return "", err
		}
		summary.Items[index].RelativePath = relative
		if summary.Items[index].Kind != "video" && summary.Items[index].Kind != "sidecar" {
			return "", errors.New("invalid transfer plan item kind")
		}
		if summary.Items[index].Result != "planned" && summary.Items[index].Result != "completed" && summary.Items[index].Result != "skipped" {
			return "", errors.New("invalid transfer plan item result")
		}
		if summary.Items[index].Size < 0 {
			return "", errors.New("invalid transfer plan item size")
		}
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxTransferPlanSummaryBytes {
		return "", errors.New("transfer plan summary is too large")
	}
	return string(encoded), nil
}

func decodeTransferPlanSummary(raw string) (*TransferPlanSummary, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if len(raw) > maxTransferPlanSummaryBytes {
		return nil, errors.New("transfer plan summary is too large")
	}
	var summary TransferPlanSummary
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&summary); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("transfer plan summary must contain exactly one JSON value")
		}
		return nil, err
	}
	if _, err := encodeTransferPlanSummary(summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func sanitizeTransferRelativePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	cleaned := pathpkg.Clean(value)
	if value == "" || cleaned == "." || cleaned == ".." || pathpkg.IsAbs(cleaned) || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, ":") || len([]rune(cleaned)) > 1024 {
		return "", errors.New("unsafe transfer relative path")
	}
	for _, r := range cleaned {
		if r < 32 || r == 127 {
			return "", errors.New("unsafe transfer relative path")
		}
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "." || segment == ".." || len([]rune(segment)) > 255 {
			return "", errors.New("unsafe transfer relative path")
		}
	}
	return cleaned, nil
}

func (w *TransferWorker) fail(task models.TransferTask, code, message string) WorkerResult {
	_ = w.service.db.Model(&task).Updates(map[string]any{"phase": models.TransferTaskStatusFailed, "last_error_code": code, "updated_at": time.Now().UTC()}).Error
	serverlog.OperationMediaTransfer.Event(w.service.log.Error()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Str("error_code", code).Msg(serverlog.OperationMediaTransfer.Message("失败"))
	return WorkerResult{ErrorCode: code, ErrorMessage: message}
}

func transferActionResponse(raw string) string {
	var checkpoint struct {
		ActionResponse struct {
			ActionType string `json:"action_type"`
			Value      string `json:"value"`
		} `json:"action_response"`
	}
	if json.Unmarshal([]byte(raw), &checkpoint) == nil && checkpoint.ActionResponse.ActionType == "transfer_conflict" {
		return checkpoint.ActionResponse.Value
	}
	return ""
}

func buildTransferPlan(download models.DownloadTask, manifest downloadpkg.Manifest) ([]transferPlanItem, string, error) {
	if download.TargetLibraryID == nil || download.TargetStorageID == nil || download.TargetStorageRoot == "" || download.StagingAbsolutePath == "" {
		return nil, "", errors.New("target snapshot is incomplete")
	}
	targetRoot, err := medialibrary.ResolveRoot(download.TargetStorageRoot, download.TargetRelativeRoot)
	if err != nil {
		return nil, "", err
	}
	sourceRoot := filepath.Join(download.StagingAbsolutePath, firstNonEmpty(download.StagingCategory, download.ScrapeCategory))
	if download.ProviderType == models.DownloaderTypePluginHTTP {
		sourceRoot = filepath.Join(download.StagingAbsolutePath, pluginDownloadRootName, download.ID)
	}
	if err := ensureWithin(download.StagingAbsolutePath, sourceRoot); err != nil {
		return nil, "", err
	}
	targets, err := buildTransferTargets(download, manifest)
	if err != nil {
		return nil, "", err
	}
	plan := make([]transferPlanItem, 0, len(targets))
	for _, target := range targets {
		fallbackRoot := download.StagingAbsolutePath
		if download.ProviderType == models.DownloaderTypePluginHTTP {
			// Plugin manifests are owned by one exact task root. They must never
			// fall back to another file in the shared download staging directory.
			fallbackRoot = sourceRoot
		}
		source, err := resolveManifestSource(sourceRoot, fallbackRoot, target.File.RelativePath)
		if err != nil {
			return nil, "", err
		}
		destination := filepath.Join(targetRoot, filepath.FromSlash(target.Relative))
		if err := ensureWithin(targetRoot, destination); err != nil {
			return nil, "", err
		}
		group := filepath.Join(targetRoot, filepath.FromSlash(target.Group))
		plan = append(plan, transferPlanItem{Source: source, Destination: destination, Relative: target.Relative, Size: target.File.Size, Group: group})
	}
	return plan, targetRoot, nil
}

type transferTargetItem struct {
	File     downloadpkg.File
	Relative string
	Group    string
}

func buildTransferTargets(download models.DownloadTask, manifest downloadpkg.Manifest) ([]transferTargetItem, error) {
	if err := validateAutomaticTransferSnapshot(download, manifest); err != nil {
		return nil, err
	}
	videoCount := 0
	for _, file := range manifest.Files {
		if isVideoFile(file.RelativePath) {
			videoCount++
		}
	}
	if videoCount == 0 {
		return nil, errors.New("manifest contains no video")
	}
	if download.ScrapeMediaType == "movie" && videoCount != 1 {
		return nil, errors.New("movie transfer manifest must contain exactly one primary video")
	}
	plan := make([]transferTargetItem, 0, len(manifest.Files))
	targetByStem := map[string]transferTargetItem{}
	usedTargets := map[string]struct{}{}
	for _, file := range manifest.Files {
		if !isVideoFile(file.RelativePath) {
			continue
		}
		normalizedSource := strings.ReplaceAll(file.RelativePath, "\\", "/")
		_, _, season, episode := medialibrary.ParseFilename(pathpkg.Base(normalizedSource), "/"+normalizedSource)
		if download.ScrapeMediaType == "tv" && episode == nil {
			return nil, errors.New("tv transfer item has no trustworthy episode number")
		}
		values := transferTemplateValues{Category: download.ScrapeCategory, Title: download.ScrapeTitle, Year: download.ScrapeYear, Version: releaseversion.Parse(normalizedSource), Season: season, Episode: episode}
		dirTemplate, fileTemplate := download.MovieDirectoryTemplate, download.MovieFilenameTemplate
		if download.ScrapeMediaType == "tv" {
			dirTemplate, fileTemplate = download.TVDirectoryTemplate, download.TVFilenameTemplate
		}
		relativeDir, err := renderImportTemplate(dirTemplate, values, true)
		if err != nil {
			return nil, err
		}
		base, err := renderImportTemplate(fileTemplate, values, false)
		if err != nil {
			return nil, err
		}
		if download.ScrapeMediaType == "movie" && values.Version != "" && !strings.Contains(fileTemplate, "{version}") && !strings.Contains(strings.ToLower(base), strings.ToLower(values.Version)) {
			base = appendMovieReleaseVersion(base, values.Version)
		}
		ext := strings.ToLower(pathpkg.Ext(normalizedSource))
		relative := filepath.ToSlash(filepath.Join(relativeDir, base+ext))
		if _, err := sanitizeTransferRelativePath(relative); err != nil {
			return nil, err
		}
		group := strings.TrimSuffix(relative, ext)
		item := transferTargetItem{File: file, Relative: relative, Group: group}
		plan = append(plan, item)
		targetByStem[transferSourceStemKey(normalizedSource)] = item
		usedTargets[strings.ToLower(relative)] = struct{}{}
	}
	for _, file := range manifest.Files {
		normalizedSource := strings.ReplaceAll(file.RelativePath, "\\", "/")
		ext := strings.ToLower(pathpkg.Ext(normalizedSource))
		if !isAutomaticTransferSidecarFile(normalizedSource) {
			continue
		}
		video, suffix, ok := transferTargetForSidecar(normalizedSource, targetByStem)
		if !ok {
			continue
		}
		relative := strings.TrimSuffix(video.Relative, pathpkg.Ext(video.Relative)) + suffix + ext
		relative, err := uniqueTransferTargetRelative(relative, usedTargets)
		if err != nil {
			return nil, err
		}
		if _, err := sanitizeTransferRelativePath(relative); err != nil {
			return nil, errors.New("sidecar target escapes root")
		}
		plan = append(plan, transferTargetItem{File: file, Relative: relative, Group: video.Group})
		usedTargets[strings.ToLower(relative)] = struct{}{}
	}
	return plan, nil
}

func validateAutomaticTransferSnapshot(download models.DownloadTask, manifest downloadpkg.Manifest) error {
	providerVerified := false
	if download.ProviderType == models.DownloaderTypePluginHTTP && download.ProviderMetadataJSON != "" {
		if envelope, err := decodeProviderMetadataEnvelope(download.ProviderMetadataJSON); err == nil && envelope.PluginID == download.PluginID && envelope.PluginVersion == download.PluginVersion && envelope.ConnectionID == download.PluginConnectionID && contract.ValidateProviderMetadataSnapshot(envelope.Snapshot, envelope.Snapshot.WorkID, envelope.Snapshot.SegmentID) == nil {
			providerVerified = true
		}
	}
	if download.ScrapeStatus != "completed_verified" || (!providerVerified && download.ScrapeTMDBID == nil) || download.ScrapeConfidence == nil || *download.ScrapeConfidence < .80 {
		return errors.New("transfer requires a verified metadata snapshot")
	}
	if strings.TrimSpace(download.ScrapeTitle) == "" || strings.TrimSpace(download.ScrapeCategory) == "" || (download.ScrapeMediaType != "movie" && download.ScrapeMediaType != "tv") {
		return errors.New("transfer metadata snapshot is incomplete")
	}
	if !manifest.Complete || len(manifest.Files) == 0 {
		return errors.New("transfer manifest is incomplete")
	}
	selected, err := selectDownloadPackageManifest(manifest, download.ScrapeMediaType)
	if providerVerified {
		selected, err = selectProviderDownloadPackageManifest(manifest, download.ScrapeMediaType)
	}
	if err != nil || !sameAutomaticTransferFiles(manifest.Files, selected.Files) {
		return errors.New("transfer manifest was not package-selected")
	}
	videoCount := 0
	for _, file := range manifest.Files {
		if !isVideoFile(file.RelativePath) {
			continue
		}
		videoCount++
		if download.ScrapeMediaType == "tv" {
			normalized := strings.ReplaceAll(file.RelativePath, "\\", "/")
			_, _, _, episode := medialibrary.ParseFilename(pathpkg.Base(normalized), "/"+normalized)
			if episode == nil {
				return errors.New("tv transfer item has no trustworthy episode number")
			}
		}
	}
	if videoCount == 0 || (download.ScrapeMediaType == "movie" && videoCount != 1) {
		return errors.New("transfer manifest was not package-selected")
	}
	return nil
}

func sameAutomaticTransferFiles(left, right []downloadpkg.File) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if normalizedManifestPath(left[index].RelativePath) != normalizedManifestPath(right[index].RelativePath) ||
			left[index].Size != right[index].Size ||
			left[index].ProviderItemID != right[index].ProviderItemID ||
			left[index].ProviderParentID != right[index].ProviderParentID ||
			left[index].SHA1 != right[index].SHA1 {
			return false
		}
	}
	return true
}

func transferSourceStemKey(relativePath string) string {
	normalized := strings.ReplaceAll(relativePath, "\\", "/")
	stem := strings.TrimSuffix(pathpkg.Base(normalized), pathpkg.Ext(normalized))
	return strings.ToLower(pathpkg.Dir(normalized) + "/" + stem)
}

func transferTargetForSidecar(relativePath string, targets map[string]transferTargetItem) (transferTargetItem, string, bool) {
	normalized := strings.ReplaceAll(relativePath, "\\", "/")
	directory := strings.ToLower(pathpkg.Dir(normalized))
	stem := strings.TrimSuffix(pathpkg.Base(normalized), pathpkg.Ext(normalized))
	matchingDirectory := make([]transferTargetItem, 0, 2)
	var matched transferTargetItem
	matchedSuffix := ""
	matchedStemLength := -1
	for key, target := range targets {
		if pathpkg.Dir(key) != directory {
			continue
		}
		matchingDirectory = append(matchingDirectory, target)
		videoPath := strings.ReplaceAll(target.File.RelativePath, "\\", "/")
		videoStem := strings.TrimSuffix(pathpkg.Base(videoPath), pathpkg.Ext(videoPath))
		if suffix, ok := transferSidecarSuffix(stem, videoStem); ok && len(videoStem) > matchedStemLength {
			matched, matchedSuffix, matchedStemLength = target, suffix, len(videoStem)
		}
	}
	if matchedStemLength >= 0 {
		return matched, matchedSuffix, true
	}
	if strings.EqualFold(pathpkg.Ext(normalized), ".jpg") && len(matchingDirectory) == 1 {
		switch strings.ToLower(stem) {
		case "poster", "folder", "cover", "fanart", "backdrop":
			return matchingDirectory[0], "", true
		}
	}
	return transferTargetItem{}, "", false
}

func transferSidecarSuffix(sidecarStem, videoStem string) (string, bool) {
	if len(sidecarStem) < len(videoStem) || !strings.EqualFold(sidecarStem[:len(videoStem)], videoStem) {
		return "", false
	}
	suffix := sidecarStem[len(videoStem):]
	if suffix == "" {
		return "", true
	}
	if suffix[0] != '.' && suffix[0] != '-' && suffix[0] != ' ' {
		return "", false
	}
	return suffix, true
}

func uniqueTransferTargetRelative(relative string, used map[string]struct{}) (string, error) {
	if _, exists := used[strings.ToLower(relative)]; !exists {
		return relative, nil
	}
	extension := pathpkg.Ext(relative)
	stem := strings.TrimSuffix(relative, extension)
	for suffix := 2; suffix <= 999; suffix++ {
		candidate := stem + "." + strconv.Itoa(suffix) + extension
		if _, exists := used[strings.ToLower(candidate)]; !exists {
			return candidate, nil
		}
	}
	return "", errors.New("no unique sidecar target name is available")
}

type transferTemplateValues struct {
	Category string
	Title    string
	Year     *int
	Version  string
	Season   *int
	Episode  *int
}

func renderImportTemplate(template string, values transferTemplateValues, directory bool) (string, error) {
	year := "未知年份"
	if values.Year != nil {
		year = strconv.Itoa(*values.Year)
	}
	number := func(value *int, padded bool) string {
		if value == nil {
			if padded {
				return "00"
			}
			return "0"
		}
		if padded {
			return fmt.Sprintf("%02d", *value)
		}
		return strconv.Itoa(*value)
	}
	replacements := map[string]string{"{category}": cleanImportSegment(values.Category), "{title}": cleanImportSegment(values.Title), "{year}": year, "{version}": cleanImportSegment(values.Version), "{season:02}": number(values.Season, true), "{episode:02}": number(values.Episode, true), "{season}": number(values.Season, false), "{episode}": number(values.Episode, false)}
	rendered := strings.TrimSpace(template)
	for key, value := range replacements {
		rendered = strings.ReplaceAll(rendered, key, value)
	}
	if values.Version == "" && strings.Contains(template, "{version}") {
		rendered = strings.TrimSpace(strings.TrimRight(rendered, " ._-"))
	}
	if directory {
		parts := strings.Split(strings.ReplaceAll(rendered, "\\", "/"), "/")
		for index := range parts {
			parts[index] = cleanImportSegment(parts[index])
			if parts[index] == "" {
				return "", errors.New("empty directory segment")
			}
		}
		rendered = filepath.Join(parts...)
	} else {
		rendered = cleanImportSegment(rendered)
	}
	if rendered == "" || filepath.IsAbs(rendered) || rendered == ".." || strings.HasPrefix(rendered, ".."+string(filepath.Separator)) {
		return "", errors.New("rendered template is unsafe")
	}
	return rendered, nil
}

func cleanImportSegment(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '-'
		}
		return r
	}, value)
	value = strings.Trim(value, " .")
	if len([]rune(value)) > 120 {
		value = string([]rune(value)[:120])
	}
	return value
}

func appendMovieReleaseVersion(base, version string) string {
	base, version = cleanImportSegment(base), cleanImportSegment(version)
	if base == "" || version == "" {
		return base
	}
	const separator = " - "
	baseRunes, suffixRunes := []rune(base), []rune(separator+version)
	if available := 120 - len(suffixRunes); available > 0 && len(baseRunes) > available {
		baseRunes = baseRunes[:available]
		base = strings.Trim(string(baseRunes), " .")
	}
	return base + separator + version
}

func resolveManifestSource(categoryRoot, stagingRoot, relative string) (string, error) {
	relative = filepath.Clean(strings.ReplaceAll(relative, "/", string(filepath.Separator)))
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || strings.Contains(relative, ":") {
		return "", errors.New("manifest path is unsafe")
	}
	var safeFallback string
	unsafeCandidate := false
	for _, root := range []string{categoryRoot, stagingRoot} {
		candidate := filepath.Join(root, relative)
		if ensureWithin(stagingRoot, candidate) != nil {
			unsafeCandidate = true
			continue
		}
		if err := ensureSafeDirectoryPath(stagingRoot, filepath.Dir(candidate), false); err != nil {
			if errors.Is(err, errUnsafeTransferPath) {
				unsafeCandidate = true
			}
			continue
		}
		if safeFallback == "" {
			safeFallback = candidate
		}
		info, err := os.Lstat(candidate)
		if err == nil {
			if info.Mode().IsRegular() && !medialibrary.IsUnsafeDirectory(candidate, fs.FileInfoToDirEntry(info)) {
				return candidate, nil
			}
			unsafeCandidate = true
		}
	}
	if unsafeCandidate {
		return "", errors.New("manifest source path is unsafe")
	}
	// A move task may restart after the source was moved but before the durable
	// progress update committed. Keep the safely constrained expected path so
	// transferAlreadyApplied can recognize the completed destination.
	if safeFallback != "" {
		return safeFallback, nil
	}
	return "", errors.New("manifest source is missing")
}

var errUnsafeTransferPath = errors.New("transfer directory path is unsafe")

func availableTransferGroupBase(plan []transferPlanItem, group string) (string, error) {
	for index := 1; index <= 9999; index++ {
		candidateBase := fmt.Sprintf("%s (%d)", group, index)
		available := true
		for _, item := range plan {
			if item.Group != group {
				continue
			}
			candidate := candidateBase + strings.TrimPrefix(item.Destination, item.Group)
			if _, err := os.Lstat(candidate); err == nil || !errors.Is(err, os.ErrNotExist) {
				available = false
				break
			}
		}
		if available {
			return candidateBase, nil
		}
	}
	return "", errors.New("no free target group name")
}

func ensureWithin(root, target string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("path escapes root")
	}
	return nil
}

func ensureSafeDirectoryPath(root, directory string, create bool) error {
	root = filepath.Clean(root)
	directory = filepath.Clean(directory)
	if err := ensureWithin(root, directory); err != nil {
		return err
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return err
	}
	current := root
	parts := []string{}
	if relative != "." {
		parts = strings.Split(relative, string(filepath.Separator))
	}
	for _, part := range append([]string{""}, parts...) {
		if part != "" {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || medialibrary.IsUnsafeDirectory(current, fs.FileInfoToDirEntry(info)) {
			return errUnsafeTransferPath
		}
	}
	return nil
}

func transferAlreadyApplied(mode string, item transferPlanItem) bool {
	info, err := os.Lstat(item.Destination)
	if err != nil {
		return false
	}
	if mode == models.MediaLibraryTransferSymlink && info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(item.Destination)
		return err == nil && filepath.Clean(target) == filepath.Clean(item.Source)
	}
	if !info.Mode().IsRegular() {
		return false
	}
	if item.Size > 0 && info.Size() != item.Size {
		return false
	}
	if mode == models.MediaLibraryTransferMove {
		_, sourceErr := os.Lstat(item.Source)
		return errors.Is(sourceErr, os.ErrNotExist)
	}
	return true
}

func resolveTransferConflict(item transferPlanItem, policy, mode string) (string, bool, error) {
	if _, err := os.Lstat(item.Destination); errors.Is(err, os.ErrNotExist) {
		return item.Destination, false, nil
	} else if err != nil {
		return "", false, err
	}
	if transferAlreadyApplied(mode, item) {
		return item.Destination, true, nil
	}
	switch policy {
	case models.MediaLibraryConflictSkip:
		return item.Destination, true, nil
	case models.MediaLibraryConflictOverwrite:
		info, err := os.Lstat(item.Destination)
		if err != nil || info.IsDir() {
			return "", false, errors.New("target is not replaceable")
		}
		if err := os.Remove(item.Destination); err != nil {
			return "", false, err
		}
		return item.Destination, false, nil
	case models.MediaLibraryConflictRename:
		ext := filepath.Ext(item.Destination)
		base := strings.TrimSuffix(item.Destination, ext)
		for index := 1; index <= 9999; index++ {
			candidate := fmt.Sprintf("%s (%d)%s", base, index, ext)
			if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
				return candidate, false, nil
			}
		}
		return "", false, errors.New("no free target name")
	default:
		return "", false, errors.New("unresolved transfer conflict")
	}
}

func executeTransfer(mode, source, destination, targetRoot string) error {
	if ensureWithin(targetRoot, destination) != nil {
		return errors.New("target escapes library root")
	}
	if err := ensureSafeDirectoryPath(targetRoot, filepath.Dir(destination), true); err != nil {
		return err
	}
	switch mode {
	case models.MediaLibraryTransferMove:
		if err := os.Rename(source, destination); err == nil {
			return nil
		}
		if err := copyFileAtomic(source, destination); err != nil {
			return err
		}
		return os.Remove(source)
	case models.MediaLibraryTransferCopy:
		return copyFileAtomic(source, destination)
	case models.MediaLibraryTransferSymlink:
		return os.Symlink(source, destination)
	default:
		return errors.New("unsupported transfer mode")
	}
}

func copyFileAtomic(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	temp := destination + ".omc-" + uuid.NewString() + ".partial"
	output, err := os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(temp)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, destination); err != nil {
		return err
	}
	ok = true
	return nil
}
