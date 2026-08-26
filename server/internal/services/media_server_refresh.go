package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	mediaserverpkg "github.com/yuanjing-hash/ohmycine/server/pkg/mediaserver"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const JobTypeMediaServerRefresh = "media_server_refresh"

type MediaServerRefreshTargetInput struct {
	LibraryID           uint
	ConnectionID        uint
	UpstreamLibraryID   string
	UpstreamLibraryName string
	Enabled             bool
	Revision            uint64
}

type MediaServerRefreshTargetTestResult struct {
	UpstreamLibraryName string    `json:"upstream_library_name"`
	ContentType         string    `json:"content_type"`
	CheckedAt           time.Time `json:"checked_at"`
}

type mediaServerRefreshJobPayload struct {
	TargetID uint `json:"target_id"`
}

type MediaServerRefreshService struct {
	db          *gorm.DB
	queue       *QueueService
	audit       *AuditService
	connections *ConnectionService
}

func NewMediaServerRefreshService(db *gorm.DB, queue *QueueService, audit *AuditService, connections *ConnectionService) *MediaServerRefreshService {
	return &MediaServerRefreshService{db: db, queue: queue, audit: audit, connections: connections}
}

func (s *MediaServerRefreshService) List(actor Actor) ([]models.MediaServerRefreshTarget, error) {
	if !actor.Can(authz.PermissionConnectionsRead) || !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看媒体服务器刷新目标", nil)
	}
	var targets []models.MediaServerRefreshTarget
	err := s.db.Order("library_id,connection_id,id").Find(&targets).Error
	return targets, err
}

func (s *MediaServerRefreshService) ListUpstreamLibraries(ctx context.Context, actor Actor, connectionID uint) ([]mediaserverpkg.Library, error) {
	if !actor.Can(authz.PermissionConnectionsRead) {
		return nil, appError(CodePermissionDenied, "无权查看媒体服务器媒体库", nil)
	}
	client, _, err := s.connections.mediaServerClient(connectionID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	items, err := client.ListLibraries(ctx)
	if err != nil {
		return nil, appError(mediaserverpkg.ErrorCode(err), "无法读取媒体服务器媒体库", err)
	}
	return items, nil
}

func (s *MediaServerRefreshService) TestTarget(ctx context.Context, actor Actor, id uint, request RequestContext) (MediaServerRefreshTargetTestResult, error) {
	if !actor.Can(authz.PermissionConnectionsTest) || !actor.Can(authz.PermissionMediaLibrariesRead) {
		return MediaServerRefreshTargetTestResult{}, appError(CodePermissionDenied, "无权测试媒体服务器刷新目标", nil)
	}
	var target models.MediaServerRefreshTarget
	if err := s.db.First(&target, id).Error; err != nil {
		return MediaServerRefreshTargetTestResult{}, mediaServerRefreshTargetNotFound(err)
	}
	checkedAt := time.Now().UTC()
	client, _, probeErr := s.connections.mediaServerClient(target.ConnectionID)
	var matched mediaserverpkg.Library
	if probeErr == nil {
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		var items []mediaserverpkg.Library
		items, probeErr = client.ListLibraries(callCtx)
		cancel()
		if probeErr == nil {
			for _, item := range items {
				if item.ID == target.UpstreamLibraryID {
					matched = item
					break
				}
			}
			if matched.ID == "" {
				probeErr = mediaserverpkg.NewError(mediaserverpkg.ErrorLibraryMissing, nil)
			}
		}
	}
	code, outcome := "", "success"
	if probeErr != nil {
		code, outcome = mediaServerRefreshErrorCode(probeErr), "failure"
	}
	if err := s.audit.Record(s.db, &actor.User.ID, "media_server_refresh_target.test", "media_server_refresh_target", uintID(id), outcome, map[string]any{"library_id": target.LibraryID, "connection_id": target.ConnectionID, "error_code": code}, request); err != nil {
		return MediaServerRefreshTargetTestResult{}, err
	}
	if probeErr != nil {
		return MediaServerRefreshTargetTestResult{}, appError(code, "媒体服务器刷新目标测试失败", probeErr)
	}
	return MediaServerRefreshTargetTestResult{UpstreamLibraryName: safeLabel(matched.Name, 256), ContentType: safeLabel(matched.ContentType, 64), CheckedAt: checkedAt}, nil
}

func (s *MediaServerRefreshService) Create(ctx context.Context, actor Actor, input MediaServerRefreshTargetInput, request RequestContext) (models.MediaServerRefreshTarget, error) {
	if !actor.Can(authz.PermissionConnectionsUpdate) || !actor.Can(authz.PermissionMediaLibrariesUpdate) {
		return models.MediaServerRefreshTarget{}, appError(CodePermissionDenied, "无权创建媒体服务器刷新目标", nil)
	}
	upstreamID := strings.TrimSpace(input.UpstreamLibraryID)
	if input.LibraryID == 0 || input.ConnectionID == 0 || upstreamID == "" || len(upstreamID) > 256 {
		return models.MediaServerRefreshTarget{}, appError(CodeInvalidRequest, "媒体服务器刷新目标无效", nil)
	}
	var library models.MediaLibrary
	if err := s.db.First(&library, input.LibraryID).Error; err != nil {
		return models.MediaServerRefreshTarget{}, mediaLibraryNotFound(err)
	}
	items, err := s.ListUpstreamLibraries(ctx, actor, input.ConnectionID)
	if err != nil {
		return models.MediaServerRefreshTarget{}, err
	}
	name := ""
	for _, item := range items {
		if item.ID == upstreamID {
			name = item.Name
			break
		}
	}
	if name == "" {
		return models.MediaServerRefreshTarget{}, appError(mediaserverpkg.ErrorLibraryMissing, "所选上游媒体库不存在", nil)
	}
	now := time.Now().UTC()
	target := models.MediaServerRefreshTarget{LibraryID: library.ID, ConnectionID: input.ConnectionID, UpstreamLibraryID: upstreamID, UpstreamLibraryName: safeLabel(name, 256), Enabled: input.Enabled, SuccessfulRevision: 0, LastStatus: "idle", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&models.MediaLibrary{}, library.ID).Error; err != nil {
			return mediaLibraryNotFound(err)
		}
		readyRevision, err := latestReadyMediaRevision(tx, library.ID)
		if err != nil {
			return err
		}
		target.DesiredRevision = readyRevision
		if err := tx.Create(&target).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_server_refresh_target.create", "media_server_refresh_target", uintID(target.ID), "success", map[string]any{"library_id": target.LibraryID, "connection_id": target.ConnectionID, "enabled": target.Enabled}, request)
	}); err != nil {
		return models.MediaServerRefreshTarget{}, appError(CodeConflict, "媒体服务器刷新目标已存在或已变化", err)
	}
	if target.Enabled && target.DesiredRevision > 0 {
		_ = s.EnqueueTarget(target.ID)
	}
	return target, nil
}

func (s *MediaServerRefreshService) Update(actor Actor, id uint, input MediaServerRefreshTargetInput, request RequestContext) (models.MediaServerRefreshTarget, error) {
	if !actor.Can(authz.PermissionConnectionsUpdate) || !actor.Can(authz.PermissionMediaLibrariesUpdate) {
		return models.MediaServerRefreshTarget{}, appError(CodePermissionDenied, "无权修改媒体服务器刷新目标", nil)
	}
	var target models.MediaServerRefreshTarget
	if err := s.db.First(&target, id).Error; err != nil {
		return target, mediaServerRefreshTargetNotFound(err)
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"enabled": input.Enabled, "revision": input.Revision + 1, "updated_at": time.Now().UTC()}
		if input.Enabled {
			readyRevision, err := latestReadyMediaRevision(tx, target.LibraryID)
			if err != nil {
				return err
			}
			if readyRevision > target.DesiredRevision {
				updates["desired_revision"] = readyRevision
			}
		}
		result := tx.Model(&models.MediaServerRefreshTarget{}).Where("id = ? AND revision = ?", id, input.Revision).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConflict, "媒体服务器刷新目标已变化，请刷新", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "media_server_refresh_target.update", "media_server_refresh_target", uintID(id), "success", map[string]any{"enabled": input.Enabled}, request)
	})
	if err != nil {
		return target, err
	}
	if err := s.db.First(&target, id).Error; err != nil {
		return target, err
	}
	if target.Enabled && (target.DesiredRevision > target.SuccessfulRevision || target.ManualGeneration > target.SuccessfulManualGeneration) {
		_ = s.EnqueueTarget(target.ID)
	}
	return target, nil
}

func (s *MediaServerRefreshService) Delete(actor Actor, id uint, request RequestContext) error {
	if !actor.Can(authz.PermissionConnectionsUpdate) || !actor.Can(authz.PermissionMediaLibrariesUpdate) {
		return appError(CodePermissionDenied, "无权删除媒体服务器刷新目标", nil)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&models.MediaServerRefreshTarget{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeNotFound, "媒体服务器刷新目标不存在", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "media_server_refresh_target.delete", "media_server_refresh_target", uintID(id), "success", nil, request)
	})
}

func (s *MediaServerRefreshService) ManualRefresh(actor Actor, id uint, request RequestContext) (JobDTO, error) {
	if !actor.Can(authz.PermissionMediaServersRefresh) {
		return JobDTO{}, appError(CodePermissionDenied, "无权刷新媒体服务器", nil)
	}
	var target models.MediaServerRefreshTarget
	if err := s.db.First(&target, id).Error; err != nil {
		return JobDTO{}, mediaServerRefreshTargetNotFound(err)
	}
	if !target.Enabled {
		return JobDTO{}, appError(CodeConnectionUnavailable, "媒体服务器刷新目标已停用", nil)
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.MediaServerRefreshTarget{}).Where("id = ? AND enabled = ?", id, true).Updates(map[string]any{"manual_generation": gorm.Expr("manual_generation + 1"), "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConnectionUnavailable, "媒体服务器刷新目标已停用", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "media_server_refresh.request", "media_server_refresh_target", uintID(id), "success", nil, request)
	}); err != nil {
		return JobDTO{}, err
	}
	if err := s.db.First(&target, id).Error; err != nil {
		return JobDTO{}, err
	}
	return s.enqueue(target)
}

func (s *MediaServerRefreshService) Retry(actor Actor, id uint, request RequestContext) (JobDTO, error) {
	if !actor.Can(authz.PermissionMediaServersRefresh) {
		return JobDTO{}, appError(CodePermissionDenied, "无权重试媒体服务器刷新", nil)
	}
	var target models.MediaServerRefreshTarget
	if err := s.db.First(&target, id).Error; err != nil {
		return JobDTO{}, mediaServerRefreshTargetNotFound(err)
	}
	if !target.Enabled {
		return JobDTO{}, appError(CodeConnectionUnavailable, "媒体服务器刷新目标已停用", nil)
	}
	if target.LastStatus != models.JobStatusFailed || (target.DesiredRevision <= target.SuccessfulRevision && target.ManualGeneration <= target.SuccessfulManualGeneration) {
		return JobDTO{}, appError(CodeConflict, "当前没有可重试的媒体服务器刷新", nil)
	}
	if err := s.audit.Record(s.db, &actor.User.ID, "media_server_refresh.retry", "media_server_refresh_target", uintID(id), "success", nil, request); err != nil {
		return JobDTO{}, err
	}
	return s.enqueue(target)
}

func (s *MediaServerRefreshService) EnqueueLibrary(libraryID uint, _ uint64) {
	var targets []models.MediaServerRefreshTarget
	if err := s.db.Where("library_id = ? AND enabled = ? AND last_status <> ? AND desired_revision > successful_revision", libraryID, true, models.JobStatusFailed).Find(&targets).Error; err != nil {
		return
	}
	for _, target := range targets {
		_, _ = s.enqueue(target)
	}
}

func (s *MediaServerRefreshService) RecoverPending() error {
	var targets []models.MediaServerRefreshTarget
	if err := s.db.Where("enabled = ? AND last_status <> ? AND (desired_revision > successful_revision OR manual_generation > successful_manual_generation)", true, models.JobStatusFailed).Find(&targets).Error; err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := s.enqueue(target); err != nil {
			return err
		}
	}
	return nil
}

func (s *MediaServerRefreshService) EnqueueTarget(id uint) error {
	var target models.MediaServerRefreshTarget
	if err := s.db.First(&target, id).Error; err != nil {
		return mediaServerRefreshTargetNotFound(err)
	}
	_, err := s.enqueue(target)
	return err
}

func (s *MediaServerRefreshService) enqueue(target models.MediaServerRefreshTarget) (JobDTO, error) {
	job, err := s.queue.Enqueue(EnqueueJobInput{System: true, JobType: JobTypeMediaServerRefresh, Priority: 10, DisplayName: "刷新媒体服务器媒体库", Provider: "media_server", ResourceKey: fmt.Sprintf("media-server-target:%d", target.ID), CoalescingKey: "latest", Payload: mediaServerRefreshJobPayload{TargetID: target.ID}})
	if err != nil {
		return JobDTO{}, err
	}
	result := s.db.Model(&models.MediaServerRefreshTarget{}).Where("id = ?", target.ID).Updates(map[string]any{"last_job_id": job.ID, "last_status": models.JobStatusQueued, "last_error_code": "", "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return JobDTO{}, result.Error
	}
	if result.RowsAffected != 1 {
		return JobDTO{}, appError(CodeNotFound, "媒体服务器刷新目标不存在", nil)
	}
	return job, nil
}

type MediaServerRefreshWorker struct{ service *MediaServerRefreshService }

func NewMediaServerRefreshWorker(service *MediaServerRefreshService) *MediaServerRefreshWorker {
	return &MediaServerRefreshWorker{service: service}
}

func (w *MediaServerRefreshWorker) Run(ctx context.Context, _ JobRuntime, job ClaimedJob) WorkerResult {
	var payload mediaServerRefreshJobPayload
	if err := json.Unmarshal([]byte(job.Job.PayloadJSON), &payload); err != nil || payload.TargetID == 0 {
		return WorkerResult{ErrorCode: "media_server_refresh_payload_invalid", ErrorMessage: "媒体服务器刷新任务参数无效"}
	}
	var target models.MediaServerRefreshTarget
	if err := w.service.db.First(&target, payload.TargetID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		// Deleting a target revokes its queued work. A stale system Job is a
		// successful no-op and must not leave a misleading operator failure.
		return WorkerResult{}
	} else if err != nil {
		return WorkerResult{ErrorCode: "media_server_refresh_target_missing", ErrorMessage: "媒体服务器刷新目标不存在"}
	}
	if !target.Enabled || (target.DesiredRevision <= target.SuccessfulRevision && target.ManualGeneration <= target.SuccessfulManualGeneration) {
		return WorkerResult{}
	}
	started, desired, manualGeneration := time.Now().UTC(), target.DesiredRevision, target.ManualGeneration
	run := models.MediaServerRefreshRun{ID: uuid.NewString(), TargetID: target.ID, JobID: job.Job.ID, DesiredRevision: desired, Status: models.JobStatusRunning, StartedAt: started}
	if err := w.service.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		return tx.Model(&models.MediaServerRefreshTarget{}).Where("id = ?", target.ID).Updates(map[string]any{"last_status": models.JobStatusRunning, "last_error_code": "", "last_attempt_at": started, "updated_at": started}).Error
	}); err != nil {
		return WorkerResult{ErrorCode: "media_server_refresh_state_failed", ErrorMessage: "媒体服务器刷新状态保存失败"}
	}
	client, _, err := w.service.connections.mediaServerClient(target.ConnectionID)
	if err == nil {
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err = client.RefreshLibrary(callCtx, target.UpstreamLibraryID)
		cancel()
	}
	finished := time.Now().UTC()
	if err != nil {
		code := mediaServerRefreshErrorCode(err)
		retryable := code == mediaserverpkg.ErrorUnavailable || code == mediaserverpkg.ErrorRateLimited
		if retryable {
			var policy models.QueuePolicy
			if policyErr := w.service.db.First(&policy, "job_type = ?", JobTypeMediaServerRefresh).Error; policyErr != nil || job.Job.AttemptCount >= policy.MaxAttempts {
				retryable = false
			}
		}
		lastStatus := models.JobStatusFailed
		if retryable {
			lastStatus = models.JobStatusRetryWait
		}
		_ = w.service.db.Transaction(func(tx *gorm.DB) error {
			if updateErr := tx.Model(&models.MediaServerRefreshRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": lastStatus, "error_code": code, "finished_at": finished}).Error; updateErr != nil {
				return updateErr
			}
			return tx.Model(&models.MediaServerRefreshTarget{}).Where("id = ?", target.ID).Updates(map[string]any{"last_status": lastStatus, "last_error_code": code, "updated_at": finished}).Error
		})
		if retryable {
			next := finished.Add(mediaServerRefreshRetryDelay(job.Job.AttemptCount))
			return WorkerResult{RetryAt: &next, ErrorCode: code, ErrorMessage: "媒体服务器暂时不可用，将自动重试"}
		}
		return WorkerResult{ErrorCode: code, ErrorMessage: "媒体服务器刷新失败，请检查连接与目标配置"}
	}
	err = w.service.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MediaServerRefreshRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": models.JobStatusCompleted, "finished_at": finished}).Error; err != nil {
			return err
		}
		var current models.MediaServerRefreshTarget
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, target.ID).Error; err != nil {
			return err
		}
		updates := map[string]any{"successful_revision": desired, "successful_manual_generation": manualGeneration, "last_status": models.JobStatusCompleted, "last_error_code": "", "last_successful_at": finished, "updated_at": finished}
		if current.SuccessfulRevision > desired {
			delete(updates, "successful_revision")
		}
		if current.SuccessfulManualGeneration > manualGeneration {
			delete(updates, "successful_manual_generation")
		}
		return tx.Model(&current).Updates(updates).Error
	})
	if err != nil {
		return WorkerResult{ErrorCode: "media_server_refresh_state_failed", ErrorMessage: "媒体服务器刷新结果保存失败"}
	}
	return WorkerResult{}
}

func mediaServerRefreshTargetNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "媒体服务器刷新目标不存在", err)
	}
	return err
}

func latestReadyMediaRevision(tx *gorm.DB, libraryID uint) (uint64, error) {
	var revision uint64
	err := tx.Model(&models.MediaLibraryChange{}).
		Where("library_id = ? AND state = ?", libraryID, models.MediaLibraryChangeReady).
		Select("COALESCE(MAX(revision), 0)").Scan(&revision).Error
	return revision, err
}

func mediaServerRefreshErrorCode(err error) string {
	var providerError *mediaserverpkg.Error
	if errors.As(err, &providerError) {
		return providerError.Code
	}
	code := ErrorCode(err)
	if code == "INTERNAL_ERROR" {
		return "media_server_configuration_invalid"
	}
	return code
}

func mediaServerRefreshRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Minute << min(attempt-1, 4)
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}
