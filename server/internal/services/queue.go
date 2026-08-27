package services

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	CodeQueueOrderConflict     = "queue_order_conflict"
	CodeQueueStateConflict     = "queue_state_conflict"
	CodeQueueLeaseInvalid      = "queue_lease_invalid"
	CodeQueueActionStale       = "queue_action_stale"
	CodeQueueActionInvalid     = "queue_action_invalid"
	CodeQueuePolicyConflict    = "queue_policy_conflict"
	CodeQueueWorkerUnavailable = "queue_worker_unavailable"
)

type Clock interface{ Now() time.Time }
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type QueueService struct {
	db                    *gorm.DB
	audit                 *AuditService
	clock                 Clock
	notify                chan struct{}
	interrupt             func(string, string)
	interruptAcknowledged func(string, string) error
	retryAccepted         func(*gorm.DB, models.Job, time.Time) error
	events                *QueueEventHub
}

func (s *QueueService) SetInterrupt(fn func(string, string)) { s.interrupt = fn }
func (s *QueueService) SetInterruptAcknowledged(fn func(string, string) error) {
	s.interruptAcknowledged = fn
}
func (s *QueueService) SetRetryAccepted(fn func(*gorm.DB, models.Job, time.Time) error) {
	s.retryAccepted = fn
}
func (s *QueueService) SetEventHub(hub *QueueEventHub) { s.events = hub }
func (s *QueueService) publish(job models.Job, eventType string) {
	if s.events != nil {
		s.events.Publish(JobEvent{Type: eventType, JobID: job.ID, JobType: job.JobType, OwnerID: job.OwnerID, Status: job.Status, At: s.clock.Now()})
	}
}

// interruptLocally stops in-process worker execution after a domain service
// has already persisted a cancellation. It never invokes a provider control
// method; provider tasks and files remain untouched.
func (s *QueueService) interruptLocally(jobIDs []string) {
	for _, id := range jobIDs {
		if s.interrupt != nil {
			s.interrupt(id, "cancel_pipeline")
		}
	}
	s.wake()
}

func NewQueueService(db *gorm.DB, audit *AuditService) *QueueService {
	return &QueueService{db: db, audit: audit, clock: realClock{}, notify: make(chan struct{}, 1)}
}

func (s *QueueService) SetClock(clock Clock) {
	if clock != nil {
		s.clock = clock
	}
}
func (s *QueueService) Wakeups() <-chan struct{} { return s.notify }
func (s *QueueService) wake() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

type EnqueueJobInput struct {
	OwnerID       uint
	System        bool
	JobType       string
	Priority      int
	DisplayName   string
	Provider      string
	ResourceKey   string
	CoalescingKey string
	Payload       any
}

func (s *QueueService) Enqueue(input EnqueueJobInput) (JobDTO, error) {
	return s.EnqueueWith(input, nil)
}

// EnqueueWith atomically creates a Job and a domain record. The callback must
// perform database work only; wakeups/events are emitted after commit.
func (s *QueueService) EnqueueWith(input EnqueueJobInput, after func(*gorm.DB, models.Job) error) (JobDTO, error) {
	input.JobType = strings.ToLower(strings.TrimSpace(input.JobType))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if (!input.System && input.OwnerID == 0) || (input.System && input.OwnerID != 0) || input.JobType == "" || input.DisplayName == "" || len(input.DisplayName) > 256 {
		return JobDTO{}, appError(CodeInvalidRequest, "任务信息无效", nil)
	}
	payload, err := json.Marshal(input.Payload)
	if err != nil || len(payload) > 64*1024 {
		return JobDTO{}, appError(CodeInvalidRequest, "任务参数无效", err)
	}
	if string(payload) == "null" {
		payload = []byte("{}")
	}
	if err := validatePrivateState(payload); err != nil {
		return JobDTO{}, err
	}
	now := s.clock.Now()
	var job models.Job
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if input.CoalescingKey != "" {
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("job_type = ? AND resource_key = ? AND coalescing_key = ? AND status IN ?", input.JobType, input.ResourceKey, input.CoalescingKey, activeJobStatuses()).First(&job).Error
			if err == nil {
				job.Generation++
				job.Revision++
				job.UpdatedAt = now
				return tx.Model(&job).Updates(map[string]any{"generation": job.Generation, "revision": job.Revision, "updated_at": now}).Error
			}
			if err != gorm.ErrRecordNotFound {
				return err
			}
		}
		var maxPosition int64
		if err := tx.Model(&models.Job{}).Where("job_type = ? AND priority = ?", input.JobType, input.Priority).Select("COALESCE(MAX(lane_position), 0)").Scan(&maxPosition).Error; err != nil {
			return err
		}
		kind := "user"
		var owner *uint
		if input.System {
			kind = "system"
		} else {
			value := input.OwnerID
			owner = &value
		}
		job = models.Job{ID: uuid.NewString(), OwnerID: owner, CreatedByKind: kind, JobType: input.JobType, Priority: input.Priority, LanePosition: maxPosition + 1000, Revision: 1, Status: models.JobStatusQueued, DisplayName: input.DisplayName, Provider: safeLabel(input.Provider, 64), ResourceKey: safeLabel(input.ResourceKey, 256), CoalescingKey: safeLabel(input.CoalescingKey, 256), Generation: 1, PayloadJSON: string(payload), CheckpointJSON: "{}", CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		if err := recordJobEvent(tx, job.ID, "created", "", models.JobStatusQueued, owner, "", now); err != nil {
			return err
		}
		if after != nil {
			return after(tx, job)
		}
		return nil
	})
	if err != nil {
		return JobDTO{}, err
	}
	s.wake()
	s.publish(job, "job.created")
	return s.toDTO(job, nil), nil
}

func activeJobStatuses() []string {
	return []string{models.JobStatusQueued, models.JobStatusRunning, models.JobStatusWaitingUserAction, models.JobStatusRetryWait, models.JobStatusPaused}
}
func safeLabel(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		value = value[:max]
	}
	return value
}

func validatePublicText(value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return appError(CodeInvalidRequest, "公开任务文本包含控制字符", nil)
	}
	return validatePrivateState([]byte(`{"value":` + strconv.Quote(value) + `}`))
}

func recordJobEvent(tx *gorm.DB, jobID, eventType, fromStatus, toStatus string, actorID *uint, safeCode string, now time.Time) error {
	return tx.Create(&models.JobStatusEvent{JobID: jobID, EventType: eventType, FromStatus: fromStatus, ToStatus: toStatus, ActorID: actorID, SafeCode: safeLabel(safeCode, 96), CreatedAt: now}).Error
}

var forbiddenPrivateKeys = []string{"authorization", "cookie", "password", "passwd", "secret", "token", "api_key", "apikey", "passkey", "credential", "absolute_path", "local_path", "signed_url"}
var windowsAbsolutePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func validatePrivateState(raw []byte) error {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return appError(CodeInvalidRequest, "任务私有状态不是有效 JSON", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return appError(CodeInvalidRequest, "任务私有状态必须是对象", nil)
	}
	var walk func(any) error
	walk = func(current any) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
				for _, forbidden := range forbiddenPrivateKeys {
					if normalized == forbidden || strings.HasSuffix(normalized, "_"+forbidden) {
						return appError(CodeInvalidRequest, "任务私有状态包含禁止字段", nil)
					}
				}
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := walk(child); err != nil {
					return err
				}
			}
		case string:
			if filepath.IsAbs(typed) || strings.HasPrefix(typed, "/") || windowsAbsolutePath.MatchString(typed) || strings.HasPrefix(typed, `\\`) || strings.HasPrefix(typed, "//") {
				return appError(CodeInvalidRequest, "任务私有状态包含绝对路径", nil)
			}
			if parsed, err := url.Parse(typed); err == nil && parsed.IsAbs() {
				for key := range parsed.Query() {
					normalized := strings.ToLower(key)
					if strings.Contains(normalized, "token") || strings.Contains(normalized, "sign") || strings.Contains(normalized, "key") || strings.Contains(normalized, "auth") {
						return appError(CodeInvalidRequest, "任务私有状态包含签名 URL", nil)
					}
				}
			}
		}
		return nil
	}
	return walk(value)
}

type ActionRequestDTO struct {
	Version    uint64            `json:"version"`
	ActionType string            `json:"action_type"`
	Prompt     string            `json:"prompt"`
	Options    []string          `json:"options"`
	Preview    map[string]string `json:"preview"`
	ExpiresAt  *time.Time        `json:"expires_at"`
}

type JobDTO struct {
	ID                    string            `json:"id"`
	OwnerID               *uint             `json:"owner_id"`
	CreatedByKind         string            `json:"created_by_kind"`
	JobType               string            `json:"job_type"`
	Priority              int               `json:"priority"`
	LanePosition          int64             `json:"lane_position"`
	LaneRank              *int              `json:"lane_rank"`
	Revision              uint64            `json:"revision"`
	Status                string            `json:"status"`
	DisplayName           string            `json:"display_name"`
	Provider              string            `json:"provider"`
	ResourceKey           string            `json:"resource_key"`
	Progress              *float64          `json:"progress"`
	ProcessedItems        *int64            `json:"processed_items"`
	TotalItems            *int64            `json:"total_items"`
	Speed                 *float64          `json:"speed"`
	ETASeconds            *int64            `json:"eta_seconds"`
	LastErrorCode         string            `json:"last_error_code"`
	LastErrorMessage      string            `json:"last_error_message"`
	NextAttemptAt         *time.Time        `json:"next_attempt_at"`
	CancellationRequested bool              `json:"cancellation_requested"`
	InterruptPending      string            `json:"interrupt_pending"`
	AttemptCount          int               `json:"attempt_count"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	StartedAt             *time.Time        `json:"started_at"`
	FinishedAt            *time.Time        `json:"finished_at"`
	Action                *ActionRequestDTO `json:"action_request"`
}

func (s *QueueService) toDTO(job models.Job, action *models.JobActionRequest) JobDTO {
	dto := JobDTO{ID: job.ID, OwnerID: job.OwnerID, CreatedByKind: job.CreatedByKind, JobType: job.JobType, Priority: job.Priority, LanePosition: job.LanePosition, Revision: job.Revision, Status: job.Status, DisplayName: job.DisplayName, Provider: job.Provider, ResourceKey: job.ResourceKey, Progress: job.Progress, ProcessedItems: job.ProcessedItems, TotalItems: job.TotalItems, Speed: job.Speed, ETASeconds: job.ETASeconds, LastErrorCode: job.LastErrorCode, LastErrorMessage: job.LastErrorMessage, NextAttemptAt: job.NextAttemptAt, CancellationRequested: job.CancellationAsked, InterruptPending: job.InterruptStatus, AttemptCount: job.AttemptCount, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt, StartedAt: job.StartedAt, FinishedAt: job.FinishedAt}
	if action != nil && action.Response == "" {
		var options []string
		_ = json.Unmarshal([]byte(action.OptionsJSON), &options)
		var preview map[string]string
		_ = json.Unmarshal([]byte(action.PreviewJSON), &preview)
		dto.Action = &ActionRequestDTO{Version: action.Version, ActionType: action.ActionType, Prompt: action.Prompt, Options: options, Preview: preview, ExpiresAt: action.ExpiresAt}
	}
	return dto
}

type JobListFilter struct {
	Status      string
	JobType     string
	Provider    string
	Priority    *int
	OwnerID     *uint
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	Page        int
	PageSize    int
}
type JobPage struct {
	List     []JobDTO `json:"list"`
	Total    int64    `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
}

func (s *QueueService) canRead(actor Actor, ownerID *uint) bool {
	return actor.Can(authz.PermissionJobsReadAll) || (ownerID != nil && actor.Can(authz.PermissionJobsReadOwn) && actor.User.ID == *ownerID)
}
func (s *QueueService) canControl(actor Actor, ownerID *uint) bool {
	return actor.Can(authz.PermissionJobsControlAll) || (ownerID != nil && actor.Can(authz.PermissionJobsControlOwn) && actor.User.ID == *ownerID)
}

func (s *QueueService) List(actor Actor, filter JobListFilter) (JobPage, error) {
	if !actor.Can(authz.PermissionJobsReadAll) && !actor.Can(authz.PermissionJobsReadOwn) {
		return JobPage{}, appError(CodePermissionDenied, "没有查看任务的权限", nil)
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 50
	}
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}
	query := s.db.Model(&models.Job{})
	if !actor.Can(authz.PermissionJobsReadAll) {
		query = query.Where("owner_id = ?", actor.User.ID)
	}
	if filter.OwnerID != nil {
		query = query.Where("owner_id = ?", *filter.OwnerID)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.JobType != "" {
		query = query.Where("job_type = ?", filter.JobType)
	}
	if filter.Provider != "" {
		query = query.Where("provider = ?", filter.Provider)
	}
	if filter.Priority != nil {
		query = query.Where("priority = ?", *filter.Priority)
	}
	if filter.CreatedFrom != nil {
		query = query.Where("created_at >= ?", *filter.CreatedFrom)
	}
	if filter.CreatedTo != nil {
		query = query.Where("created_at <= ?", *filter.CreatedTo)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return JobPage{}, err
	}
	var jobs []models.Job
	if err := query.Order("created_at DESC, id DESC").Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&jobs).Error; err != nil {
		return JobPage{}, err
	}
	list := make([]JobDTO, 0, len(jobs))
	for _, job := range jobs {
		list = append(list, s.toDTO(job, nil))
	}
	return JobPage{List: list, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *QueueService) Get(actor Actor, id string) (JobDTO, error) {
	var job models.Job
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return JobDTO{}, queueNotFound(err)
	}
	if !s.canRead(actor, job.OwnerID) {
		return JobDTO{}, appError(CodePermissionDenied, "没有查看任务的权限", nil)
	}
	var action models.JobActionRequest
	var actionPtr *models.JobActionRequest
	if err := s.db.Where("job_id = ?", id).Order("version DESC").First(&action).Error; err == nil {
		actionPtr = &action
	} else if err != gorm.ErrRecordNotFound {
		return JobDTO{}, err
	}
	return s.toDTO(job, actionPtr), nil
}

func (s *QueueService) Attempts(actor Actor, id string) ([]models.JobAttempt, error) {
	var job models.Job
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return nil, queueNotFound(err)
	}
	if !s.canRead(actor, job.OwnerID) {
		return nil, appError(CodePermissionDenied, "没有查看任务的权限", nil)
	}
	var attempts []models.JobAttempt
	if err := s.db.Where("job_id = ?", id).Order("attempt_number DESC").Find(&attempts).Error; err != nil {
		return nil, err
	}
	return attempts, nil
}

func (s *QueueService) Timeline(actor Actor, id string) ([]models.JobStatusEvent, error) {
	var job models.Job
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return nil, queueNotFound(err)
	}
	if !s.canRead(actor, job.OwnerID) {
		return nil, appError(CodePermissionDenied, "没有查看任务的权限", nil)
	}
	var events []models.JobStatusEvent
	if err := s.db.Where("job_id = ?", id).Order("id").Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

// domainDetail returns the same allowlisted queue facts used by the task
// center after the calling domain service has already authorized its resource.
// It is intentionally package-private so HTTP handlers cannot bypass queue or
// domain authorization.
func (s *QueueService) domainDetail(id string) (JobDTO, []models.JobAttempt, []models.JobStatusEvent, error) {
	var job models.Job
	if err := s.db.First(&job, "id = ?", id).Error; err != nil {
		return JobDTO{}, nil, nil, queueNotFound(err)
	}
	var action models.JobActionRequest
	var actionPtr *models.JobActionRequest
	if err := s.db.Where("job_id = ?", id).Order("version DESC").First(&action).Error; err == nil {
		actionPtr = &action
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return JobDTO{}, nil, nil, err
	}
	attempts := make([]models.JobAttempt, 0)
	if err := s.db.Where("job_id = ?", id).Order("attempt_number DESC").Find(&attempts).Error; err != nil {
		return JobDTO{}, nil, nil, err
	}
	timeline := make([]models.JobStatusEvent, 0)
	if err := s.db.Where("job_id = ?", id).Order("id").Find(&timeline).Error; err != nil {
		return JobDTO{}, nil, nil, err
	}
	return s.toDTO(job, actionPtr), attempts, timeline, nil
}

type LaneRevision struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}

func (s *QueueService) Reorder(actor Actor, jobType string, priority int, ordered []LaneRevision, request RequestContext) ([]JobDTO, error) {
	if !actor.Can(authz.PermissionJobsReorder) {
		return nil, appError(CodePermissionDenied, "没有调整任务顺序的权限", nil)
	}
	if len(ordered) == 0 {
		return nil, appError(CodeInvalidRequest, "任务顺序不能为空", nil)
	}
	seen := map[string]struct{}{}
	for _, item := range ordered {
		if item.ID == "" || item.Revision == 0 {
			return nil, appError(CodeInvalidRequest, "任务顺序版本无效", nil)
		}
		if _, ok := seen[item.ID]; ok {
			return nil, appError(CodeInvalidRequest, "任务顺序包含重复项", nil)
		}
		seen[item.ID] = struct{}{}
	}
	now := s.clock.Now()
	var jobs []models.Job
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var laneCount int64
		if err := tx.Model(&models.Job{}).Where("job_type = ? AND priority = ? AND status = ?", jobType, priority, models.JobStatusQueued).Count(&laneCount).Error; err != nil {
			return err
		}
		if laneCount != int64(len(ordered)) {
			return appError(CodeQueueOrderConflict, "队列已变化，请刷新后重试", nil)
		}
		ids := make([]string, len(ordered))
		for i := range ordered {
			ids[i] = ordered[i].ID
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) != len(ordered) {
			return appError(CodeQueueOrderConflict, "队列已变化，请刷新后重试", nil)
		}
		byID := map[string]models.Job{}
		for _, job := range jobs {
			byID[job.ID] = job
		}
		for i, item := range ordered {
			job := byID[item.ID]
			if job.Status != models.JobStatusQueued || job.JobType != jobType || job.Priority != priority || job.Revision != item.Revision {
				return appError(CodeQueueOrderConflict, "队列已变化，请刷新后重试", nil)
			}
			result := tx.Model(&models.Job{}).Where("id = ? AND revision = ? AND status = ?", job.ID, job.Revision, models.JobStatusQueued).Updates(map[string]any{"lane_position": int64(i+1) * 1000, "revision": job.Revision + 1, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return appError(CodeQueueOrderConflict, "队列已变化，请刷新后重试", nil)
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "jobs.reorder", "job_lane", fmt.Sprintf("%s:%d", jobType, priority), "success", map[string]any{"count": len(ordered)}, request)
	})
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		s.publish(job, "job.order_changed")
	}
	return s.Lane(actor, jobType, priority)
}

func (s *QueueService) Lane(actor Actor, jobType string, priority int) ([]JobDTO, error) {
	if !actor.Can(authz.PermissionJobsReadAll) && !actor.Can(authz.PermissionJobsReadOwn) {
		return nil, appError(CodePermissionDenied, "没有查看任务的权限", nil)
	}
	q := s.db.Where("job_type = ? AND priority = ? AND status = ?", jobType, priority, models.JobStatusQueued)
	if !actor.Can(authz.PermissionJobsReadAll) {
		q = q.Where("owner_id = ?", actor.User.ID)
	}
	var jobs []models.Job
	if err := q.Order("lane_position, created_at, id").Find(&jobs).Error; err != nil {
		return nil, err
	}
	list := make([]JobDTO, 0, len(jobs))
	for i, job := range jobs {
		dto := s.toDTO(job, nil)
		rank := i + 1
		dto.LaneRank = &rank
		list = append(list, dto)
	}
	return list, nil
}

func (s *QueueService) Control(actor Actor, id, action string, request RequestContext) (JobDTO, error) {
	now := s.clock.Now()
	var job models.Job
	interruptAfterCommit := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "id = ?", id).Error; err != nil {
			return queueNotFound(err)
		}
		if !s.canControl(actor, job.OwnerID) {
			return appError(CodePermissionDenied, "没有控制任务的权限", nil)
		}
		from := job.Status
		if job.InterruptStatus != "" {
			return appError(CodeQueueStateConflict, "任务控制正在与下载器确认，请稍候", nil)
		}
		updates := map[string]any{"revision": job.Revision + 1, "updated_at": now}
		providerControl := (job.JobType == "download" || job.JobType == "seeding") && job.Provider != "" && job.Provider != models.DownloaderTypeFake
		switch action {
		case "pause":
			if from != models.JobStatusQueued && from != models.JobStatusRunning && from != models.JobStatusRetryWait {
				return appError(CodeQueueStateConflict, "当前任务不能暂停", nil)
			}
			if from == models.JobStatusRunning {
				interruptAfterCommit = true
				updates["status"] = models.JobStatusRunning
				updates["interrupt_status"] = models.JobStatusPaused
				updates["cancellation_asked"] = true
			} else if providerControl {
				updates["status"] = models.JobStatusQueued
				updates["interrupt_status"] = models.JobStatusPaused
				updates["cancellation_asked"] = true
				updates["next_attempt_at"] = nil
			} else {
				updates["status"] = models.JobStatusPaused
				releaseLease(updates)
			}
		case "resume":
			if from != models.JobStatusPaused {
				return appError(CodeQueueStateConflict, "当前任务不能恢复", nil)
			}
			updates["status"] = models.JobStatusQueued
			updates["next_attempt_at"] = nil
		case "cancel":
			if from == models.JobStatusCompleted || from == models.JobStatusCancelled {
				return appError(CodeQueueStateConflict, "当前任务不能取消", nil)
			}
			if from == models.JobStatusRunning {
				interruptAfterCommit = true
				updates["status"] = models.JobStatusRunning
				updates["interrupt_status"] = models.JobStatusCancelled
				updates["cancellation_asked"] = true
			} else if providerControl {
				updates["status"] = models.JobStatusQueued
				updates["interrupt_status"] = models.JobStatusCancelled
				updates["cancellation_asked"] = true
				updates["next_attempt_at"] = nil
				updates["finished_at"] = nil
			} else {
				updates["status"] = models.JobStatusCancelled
				updates["finished_at"] = now
				updates["cancellation_asked"] = true
				releaseLease(updates)
			}
		case "retry":
			if from != models.JobStatusFailed {
				return appError(CodeQueueStateConflict, "仅失败任务可以重试", nil)
			}
			updates["status"] = models.JobStatusQueued
			updates["next_attempt_at"] = nil
			updates["finished_at"] = nil
			updates["last_error_code"] = ""
			updates["last_error_message"] = ""
		default:
			return appError(CodeInvalidRequest, "未知任务操作", nil)
		}
		if err := tx.Model(&job).Updates(updates).Error; err != nil {
			return err
		}
		if action == "retry" && s.retryAccepted != nil {
			if err := s.retryAccepted(tx, job, now); err != nil {
				return err
			}
		}
		if providerControl && from != models.JobStatusRunning && (action == "pause" || action == "cancel") {
			checkpoint, checkpointErr := setProviderControlOrigin(job.CheckpointJSON, from)
			if checkpointErr != nil {
				return checkpointErr
			}
			if err := tx.Model(&job).Update("checkpoint_json", checkpoint).Error; err != nil {
				return err
			}
			job.CheckpointJSON = checkpoint
			if err := tx.Model(&models.JobActionRequest{}).Where("job_id = ? AND response = ''", job.ID).Updates(map[string]any{"response": "closed_by_control", "responded_by": actor.User.ID, "responded_at": now}).Error; err != nil {
				return err
			}
		}
		job.Revision++
		job.Status = updates["status"].(string)
		if action == "retry" {
			// Keep the in-memory post-commit snapshot consistent with the row that
			// Control just persisted; no later consumer should observe the old
			// terminal fields beside the new queued status.
			job.NextAttemptAt = nil
			job.FinishedAt = nil
			job.LastErrorCode = ""
			job.LastErrorMessage = ""
		}
		if pending, ok := updates["interrupt_status"].(string); ok {
			job.InterruptStatus = pending
		}
		if asked, ok := updates["cancellation_asked"].(bool); ok {
			job.CancellationAsked = asked
		}
		job.UpdatedAt = now
		if err := recordJobEvent(tx, job.ID, "control."+action, from, job.Status, &actor.User.ID, "", now); err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "jobs."+action, "job", job.ID, "success", map[string]any{"from": from, "to": job.Status}, request)
	})
	if err != nil {
		return JobDTO{}, err
	}
	if interruptAfterCommit && s.interrupt != nil {
		s.interrupt(job.ID, action)
	}
	s.wake()
	s.publish(job, "job.status_changed")
	return s.Get(actor, id)
}

// RejectInterrupt clears a provider control intent when the external action
// failed. The worker keeps its lease and continues reconciliation.
func (s *QueueService) RejectInterrupt(id, token, action, code, message string) error {
	if err := validatePublicText(message); err != nil {
		message = "下载器未能执行任务控制操作"
	}
	expected := models.JobStatusPaused
	if action == "cancel" {
		expected = models.JobStatusCancelled
	}
	now := s.clock.Now()
	var job models.Job
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var err error
		job, err = s.verifyLease(tx, id, token)
		if err != nil {
			return err
		}
		if job.Status != models.JobStatusRunning || job.InterruptStatus != expected {
			return nil
		}
		origin := providerControlOrigin(job.CheckpointJSON)
		updates := map[string]any{"interrupt_status": "", "cancellation_asked": false, "revision": job.Revision + 1, "last_error_code": safeLabel(code, 96), "last_error_message": safeLabel(message, 512), "checkpoint_json": clearProviderControlOrigin(job.CheckpointJSON), "updated_at": now}
		restored := origin == models.JobStatusPaused || origin == models.JobStatusFailed
		if restored {
			updates["status"] = origin
			if origin == models.JobStatusFailed {
				updates["finished_at"] = now
			}
			releaseLease(updates)
		}
		if err := tx.Model(&job).Updates(updates).Error; err != nil {
			return err
		}
		job.InterruptStatus, job.CancellationAsked, job.LastErrorCode, job.LastErrorMessage = "", false, safeLabel(code, 96), safeLabel(message, 512)
		if restored {
			job.Status = origin
			if err := closeAttempt(tx, job, origin, safeLabel(code, 96), safeLabel(message, 512), now); err != nil {
				return err
			}
		}
		job.Revision++
		return recordJobEvent(tx, id, "control."+action+".rejected", models.JobStatusRunning, job.Status, nil, safeLabel(code, 96), now)
	})
	if err == nil {
		s.publish(job, "job.status_changed")
	}
	return err
}

func (s *QueueService) AcknowledgeInterrupt(id, token string) error {
	now := s.clock.Now()
	action := ""
	err := s.db.Transaction(func(tx *gorm.DB) error {
		job, err := s.verifyLease(tx, id, token)
		if err != nil {
			return err
		}
		if job.InterruptStatus != models.JobStatusPaused && job.InterruptStatus != models.JobStatusCancelled {
			return nil
		}
		if job.InterruptStatus == models.JobStatusCancelled {
			action = "cancel"
		} else {
			action = "pause"
		}
		return s.acknowledgeInterruptTx(tx, job, now)
	})
	if err == nil && action != "" && s.interruptAcknowledged != nil {
		if hookErr := s.interruptAcknowledged(id, action); hookErr != nil {
			return hookErr
		}
	}
	if err == nil {
		var job models.Job
		if s.db.First(&job, "id = ?", id).Error == nil {
			s.publish(job, "job.status_changed")
		}
	}
	return err
}

func releaseLease(updates map[string]any) {
	updates["lease_token_hash"] = ""
	updates["lease_expires_at"] = nil
	updates["heartbeat_at"] = nil
}

func setProviderControlOrigin(raw, origin string) (string, error) {
	var checkpoint map[string]any
	if err := json.Unmarshal([]byte(raw), &checkpoint); err != nil {
		return "", fmt.Errorf("decode provider control checkpoint: %w", err)
	}
	if checkpoint == nil {
		checkpoint = map[string]any{}
	}
	checkpoint["provider_control"] = map[string]any{"origin_status": origin}
	encoded, err := json.Marshal(checkpoint)
	if err != nil || len(encoded) > 64*1024 {
		return "", appError(CodeInvalidRequest, "任务检查点无效", err)
	}
	if err := validatePrivateState(encoded); err != nil {
		return "", err
	}
	return string(encoded), nil
}

func providerControlOrigin(raw string) string {
	var checkpoint struct {
		ProviderControl struct {
			OriginStatus string `json:"origin_status"`
		} `json:"provider_control"`
	}
	if json.Unmarshal([]byte(raw), &checkpoint) != nil {
		return ""
	}
	return checkpoint.ProviderControl.OriginStatus
}

func clearProviderControlOrigin(raw string) string {
	var checkpoint map[string]any
	if json.Unmarshal([]byte(raw), &checkpoint) != nil || checkpoint == nil {
		return "{}"
	}
	delete(checkpoint, "provider_control")
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func (s *QueueService) Respond(actor Actor, id string, version uint64, response string, request RequestContext) (JobDTO, error) {
	if !actor.Can(authz.PermissionJobsRespond) {
		return JobDTO{}, appError(CodePermissionDenied, "没有响应任务操作的权限", nil)
	}
	now := s.clock.Now()
	var job models.Job
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "id = ?", id).Error; err != nil {
			return queueNotFound(err)
		}
		if !s.canRead(actor, job.OwnerID) {
			return appError(CodePermissionDenied, "没有响应任务操作的权限", nil)
		}
		if job.Status != models.JobStatusWaitingUserAction {
			return appError(CodeQueueActionStale, "等待操作已变化，请刷新", nil)
		}
		var action models.JobActionRequest
		if err := tx.Where("job_id = ? AND version = ?", id, version).First(&action).Error; err != nil {
			return appError(CodeQueueActionStale, "等待操作已变化，请刷新", err)
		}
		if action.Response != "" {
			return appError(CodeQueueActionStale, "该操作已经响应", nil)
		}
		var options []string
		_ = json.Unmarshal([]byte(action.OptionsJSON), &options)
		valid := false
		for _, option := range options {
			if option == response {
				valid = true
				break
			}
		}
		if !valid {
			return appError(CodeQueueActionInvalid, "响应选项无效", nil)
		}
		if action.ExpiresAt != nil && action.ExpiresAt.Before(now) {
			return appError(CodeQueueActionStale, "等待操作已过期", nil)
		}
		var checkpoint map[string]any
		if err := json.Unmarshal([]byte(job.CheckpointJSON), &checkpoint); err != nil {
			return fmt.Errorf("decode job checkpoint for action response: %w", err)
		}
		if checkpoint == nil {
			checkpoint = map[string]any{}
		}
		checkpoint["action_response"] = map[string]any{"version": version, "action_type": action.ActionType, "value": response}
		checkpointJSON, err := json.Marshal(checkpoint)
		if err != nil || len(checkpointJSON) > 64*1024 {
			return appError(CodeInvalidRequest, "任务检查点无效", err)
		}
		if err := validatePrivateState(checkpointJSON); err != nil {
			return err
		}
		if err := tx.Model(&action).Updates(map[string]any{"response": response, "responded_by": actor.User.ID, "responded_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&job).Updates(map[string]any{"status": models.JobStatusQueued, "revision": job.Revision + 1, "checkpoint_json": string(checkpointJSON), "updated_at": now}).Error; err != nil {
			return err
		}
		if err := recordJobEvent(tx, id, "action.responded", models.JobStatusWaitingUserAction, models.JobStatusQueued, &actor.User.ID, "", now); err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "jobs.respond", "job", id, "success", map[string]any{"version": version, "option": response}, request)
	})
	if err != nil {
		return JobDTO{}, err
	}
	s.wake()
	var changed models.Job
	if s.db.First(&changed, "id = ?", id).Error == nil {
		s.publish(changed, "job.status_changed")
	}
	return s.Get(actor, id)
}

func (s *QueueService) Policies(actor Actor) ([]models.QueuePolicy, error) {
	if !actor.Can(authz.PermissionJobsReadAll) && !actor.Can(authz.PermissionJobsReadOwn) {
		return nil, appError(CodePermissionDenied, "没有查看队列策略的权限", nil)
	}
	var policies []models.QueuePolicy
	err := s.db.Order("job_type").Find(&policies).Error
	return policies, err
}
func (s *QueueService) UpdatePolicy(actor Actor, jobType string, revision uint64, concurrency, resourceConcurrency, maxAttempts, leaseSeconds int, request RequestContext) (models.QueuePolicy, error) {
	if !actor.Can(authz.PermissionQueuePoliciesManage) {
		return models.QueuePolicy{}, appError(CodePermissionDenied, "没有管理队列策略的权限", nil)
	}
	if concurrency < 1 || resourceConcurrency < 0 || maxAttempts < 1 || leaseSeconds < 5 {
		return models.QueuePolicy{}, appError(CodeInvalidRequest, "队列策略无效", nil)
	}
	now := s.clock.Now()
	result := s.db.Model(&models.QueuePolicy{}).Where("job_type = ? AND revision = ?", jobType, revision).Updates(map[string]any{"concurrency": concurrency, "resource_concurrency": resourceConcurrency, "max_attempts": maxAttempts, "lease_seconds": leaseSeconds, "revision": revision + 1, "updated_at": now})
	if result.Error != nil {
		return models.QueuePolicy{}, result.Error
	}
	if result.RowsAffected != 1 {
		return models.QueuePolicy{}, appError(CodeQueuePolicyConflict, "队列策略已变化，请刷新", nil)
	}
	if err := s.audit.Record(s.db, &actor.User.ID, "queue_policies.update", "queue_policy", jobType, "success", map[string]any{"concurrency": concurrency, "resource_concurrency": resourceConcurrency}, request); err != nil {
		return models.QueuePolicy{}, err
	}
	var policy models.QueuePolicy
	err := s.db.First(&policy, "job_type = ?", jobType).Error
	s.wake()
	if s.events != nil {
		s.events.Publish(JobEvent{Type: "queue.policy_changed", At: now})
	}
	return policy, err
}

type ClaimedJob struct {
	Job        models.Job
	Attempt    models.JobAttempt
	LeaseToken string
}

func (s *QueueService) Claim(jobTypes []string) (*ClaimedJob, error) {
	now := s.clock.Now()
	var claimed *ClaimedJob
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var policies []models.QueuePolicy
		q := tx.Order("job_type")
		if len(jobTypes) > 0 {
			q = q.Where("job_type IN ?", jobTypes)
		}
		if err := q.Find(&policies).Error; err != nil {
			return err
		}
		for _, policy := range policies {
			var active int64
			if err := tx.Model(&models.Job{}).Where("job_type = ? AND status = ? AND lease_expires_at > ?", policy.JobType, models.JobStatusRunning, now).Count(&active).Error; err != nil {
				return err
			}
			if active >= int64(policy.Concurrency) {
				continue
			}
			var candidates []models.Job
			if err := tx.Where("job_type = ? AND status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", policy.JobType, models.JobStatusQueued, now).Order("priority DESC, lane_position, created_at, id").Find(&candidates).Error; err != nil {
				return err
			}
			for _, candidate := range candidates {
				if policy.ResourceConcurrency > 0 && candidate.ResourceKey != "" {
					var resourceActive int64
					if err := tx.Model(&models.Job{}).Where("resource_key = ? AND status = ? AND lease_expires_at > ?", candidate.ResourceKey, models.JobStatusRunning, now).Count(&resourceActive).Error; err != nil {
						return err
					}
					if resourceActive >= int64(policy.ResourceConcurrency) {
						continue
					}
				}
				token, hash, err := newLeaseToken()
				if err != nil {
					return err
				}
				expires := now.Add(time.Duration(policy.LeaseSeconds) * time.Second)
				result := tx.Model(&models.Job{}).Where("id = ? AND status = ? AND revision = ?", candidate.ID, models.JobStatusQueued, candidate.Revision).Updates(map[string]any{"status": models.JobStatusRunning, "revision": candidate.Revision + 1, "lease_token_hash": hash, "lease_expires_at": expires, "heartbeat_at": now, "started_at": gorm.Expr("COALESCE(started_at, ?)", now), "started_generation": candidate.Generation, "attempt_count": candidate.AttemptCount + 1, "updated_at": now})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					continue
				}
				attempt := models.JobAttempt{JobID: candidate.ID, AttemptNumber: candidate.AttemptCount + 1, LeaseTokenHash: hash, Status: models.JobStatusRunning, StartedAt: now}
				if err := tx.Create(&attempt).Error; err != nil {
					return err
				}
				if err := recordJobEvent(tx, candidate.ID, "claimed", models.JobStatusQueued, models.JobStatusRunning, nil, "", now); err != nil {
					return err
				}
				candidate.Status = models.JobStatusRunning
				candidate.Revision++
				candidate.LeaseTokenHash = hash
				candidate.LeaseExpiresAt = &expires
				candidate.HeartbeatAt = &now
				candidate.AttemptCount++
				candidate.StartedGeneration = candidate.Generation
				claimed = &ClaimedJob{Job: candidate, Attempt: attempt, LeaseToken: token}
				return nil
			}
		}
		return nil
	})
	if err == nil && claimed != nil {
		s.publish(claimed.Job, "job.status_changed")
	}
	return claimed, err
}

func newLeaseToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}
func leaseHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *QueueService) verifyLease(tx *gorm.DB, id, token string) (models.Job, error) {
	var job models.Job
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "id = ?", id).Error; err != nil {
		return job, queueNotFound(err)
	}
	now := s.clock.Now()
	if job.Status != models.JobStatusRunning || job.LeaseTokenHash == "" || job.LeaseTokenHash != leaseHash(token) || job.LeaseExpiresAt == nil || !job.LeaseExpiresAt.After(now) {
		return job, appError(CodeQueueLeaseInvalid, "任务租约已失效", nil)
	}
	return job, nil
}

func (s *QueueService) Heartbeat(id, token string, progress *float64, processed, total *int64, speed *float64, eta *int64) error {
	_, err := s.heartbeat(id, token, progress, processed, total, speed, eta, true)
	return err
}

// renewLease extends scheduler ownership without publishing a synthetic
// progress event. Worker heartbeats retain their progress/event semantics.
func (s *QueueService) renewLease(id, token string) (time.Duration, error) {
	return s.heartbeat(id, token, nil, nil, nil, nil, nil, false)
}

func (s *QueueService) heartbeat(id, token string, progress *float64, processed, total *int64, speed *float64, eta *int64, publishProgress bool) (time.Duration, error) {
	var published models.Job
	var leaseDuration time.Duration
	err := s.db.Transaction(func(tx *gorm.DB) error {
		job, err := s.verifyLease(tx, id, token)
		if err != nil {
			return err
		}
		var policy models.QueuePolicy
		if err := tx.First(&policy, "job_type = ?", job.JobType).Error; err != nil {
			return err
		}
		now := s.clock.Now()
		leaseDuration = time.Duration(policy.LeaseSeconds) * time.Second
		expires := now.Add(leaseDuration)
		updates := map[string]any{"lease_expires_at": expires, "heartbeat_at": now, "updated_at": now}
		if progress != nil {
			if *progress < 0 || *progress > 100 {
				return appError(CodeInvalidRequest, "任务进度无效", nil)
			}
			updates["progress"] = *progress
		}
		if processed != nil {
			updates["processed_items"] = *processed
		}
		if total != nil {
			updates["total_items"] = *total
		}
		if speed != nil {
			updates["speed"] = *speed
		}
		if eta != nil {
			updates["eta_seconds"] = *eta
		}
		if err := tx.Model(&job).Updates(updates).Error; err != nil {
			return err
		}
		published = job
		return nil
	})
	if err == nil && publishProgress {
		s.publish(published, "job.progress")
	}
	return leaseDuration, err
}

func (s *QueueService) SaveCheckpoint(id, token string, target any) error {
	raw, err := json.Marshal(target)
	if err != nil || len(raw) > 64*1024 {
		return appError(CodeInvalidRequest, "任务检查点无效", err)
	}
	if string(raw) == "null" {
		raw = []byte("{}")
	}
	if err := validatePrivateState(raw); err != nil {
		return err
	}
	now := s.clock.Now()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		job, err := s.verifyLease(tx, id, token)
		if err != nil {
			return err
		}
		return tx.Model(&job).Updates(map[string]any{"checkpoint_json": string(raw), "updated_at": now}).Error
	})
	return err
}

type WaitForAction struct {
	ActionType string
	Prompt     string
	Options    []string
	Preview    map[string]string
	Checkpoint any
	ExpiresAt  *time.Time
}

func (s *QueueService) Wait(id, token string, input WaitForAction) error {
	if input.ActionType == "" || input.Prompt == "" || len(input.Options) == 0 {
		return appError(CodeInvalidRequest, "等待操作信息无效", nil)
	}
	if err := validatePublicText(input.Prompt); err != nil {
		return err
	}
	for key, value := range input.Preview {
		if err := validatePublicText(key); err != nil {
			return err
		}
		if err := validatePublicText(value); err != nil {
			return err
		}
	}
	options, _ := json.Marshal(input.Options)
	preview, _ := json.Marshal(input.Preview)
	checkpoint, err := json.Marshal(input.Checkpoint)
	if err != nil || len(checkpoint) > 64*1024 {
		return appError(CodeInvalidRequest, "任务检查点无效", err)
	}
	if string(checkpoint) == "null" {
		checkpoint = []byte("{}")
	}
	if err := validatePrivateState(checkpoint); err != nil {
		return err
	}
	now := s.clock.Now()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		job, err := s.verifyLease(tx, id, token)
		if err != nil {
			return err
		}
		var version uint64
		if err := tx.Model(&models.JobActionRequest{}).Where("job_id = ?", id).Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
			return err
		}
		action := models.JobActionRequest{JobID: id, Version: version + 1, ActionType: safeLabel(input.ActionType, 64), Prompt: safeLabel(input.Prompt, 512), OptionsJSON: string(options), PreviewJSON: string(preview), ExpiresAt: input.ExpiresAt, CreatedAt: now}
		if err := tx.Create(&action).Error; err != nil {
			return err
		}
		updates := map[string]any{"status": models.JobStatusWaitingUserAction, "revision": job.Revision + 1, "checkpoint_json": string(checkpoint), "updated_at": now}
		releaseLease(updates)
		if err := tx.Model(&job).Updates(updates).Error; err != nil {
			return err
		}
		if err := recordJobEvent(tx, id, "action.required", models.JobStatusRunning, models.JobStatusWaitingUserAction, nil, "", now); err != nil {
			return err
		}
		return closeAttempt(tx, job, models.JobStatusWaitingUserAction, "", "", now)
	})
	if err == nil {
		var job models.Job
		if s.db.First(&job, "id = ?", id).Error == nil {
			s.publish(job, "job.action_required")
		}
	}
	return err
}

func (s *QueueService) Complete(id, token string) error {
	return s.finishLease(id, token, models.JobStatusCompleted, "", "", nil)
}
func (s *QueueService) Fail(id, token, code, message string) error {
	if err := validatePublicText(message); err != nil {
		message = "任务执行失败，详细信息已隐藏"
	}
	return s.finishLease(id, token, models.JobStatusFailed, safeLabel(code, 96), safeLabel(message, 512), nil)
}
func (s *QueueService) RetryLater(id, token, code, message string, next time.Time) error {
	if err := validatePublicText(message); err != nil {
		message = "任务执行失败，详细信息已隐藏"
	}
	return s.finishLease(id, token, models.JobStatusRetryWait, safeLabel(code, 96), safeLabel(message, 512), &next)
}
func (s *QueueService) finishLease(id, token, status, code, message string, next *time.Time) error {
	now := s.clock.Now()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		job, err := s.verifyLease(tx, id, token)
		if err != nil {
			return err
		}
		if job.InterruptStatus != "" {
			return s.acknowledgeInterruptTx(tx, job, now)
		}
		finalStatus := status
		finished := any(now)
		if status == models.JobStatusRetryWait {
			finished = nil
		}
		if status == models.JobStatusCompleted && job.Generation > job.StartedGeneration {
			finalStatus = models.JobStatusQueued
			finished = nil
		}
		updates := map[string]any{"status": finalStatus, "revision": job.Revision + 1, "last_error_code": code, "last_error_message": message, "next_attempt_at": next, "finished_at": finished, "updated_at": now}
		releaseLease(updates)
		if err := tx.Model(&job).Updates(updates).Error; err != nil {
			return err
		}
		if err := recordJobEvent(tx, id, "worker.finished", models.JobStatusRunning, finalStatus, nil, code, now); err != nil {
			return err
		}
		return closeAttempt(tx, job, finalStatus, code, message, now)
	})
	if err == nil {
		s.wake()
		var job models.Job
		if s.db.First(&job, "id = ?", id).Error == nil {
			s.publish(job, "job.status_changed")
		}
	}
	return err
}

func (s *QueueService) acknowledgeInterruptTx(tx *gorm.DB, job models.Job, now time.Time) error {
	updates := map[string]any{"status": job.InterruptStatus, "interrupt_status": "", "cancellation_asked": false, "revision": job.Revision + 1, "checkpoint_json": clearProviderControlOrigin(job.CheckpointJSON), "last_error_code": "", "last_error_message": "", "updated_at": now}
	if job.InterruptStatus == models.JobStatusCancelled {
		updates["finished_at"] = now
	}
	releaseLease(updates)
	if err := tx.Model(&job).Updates(updates).Error; err != nil {
		return err
	}
	if err := recordJobEvent(tx, job.ID, "worker.interrupted", models.JobStatusRunning, job.InterruptStatus, nil, "", now); err != nil {
		return err
	}
	return closeAttempt(tx, job, job.InterruptStatus, "", "", now)
}
func closeAttempt(tx *gorm.DB, job models.Job, status, code, message string, now time.Time) error {
	return tx.Model(&models.JobAttempt{}).Where("job_id = ? AND attempt_number = ? AND finished_at IS NULL", job.ID, job.AttemptCount).Updates(map[string]any{"status": status, "safe_error_code": code, "safe_error_message": message, "finished_at": now}).Error
}

func (s *QueueService) PromoteDueRetries() error {
	now := s.clock.Now()
	var due []models.Job
	if err := s.db.Where("status = ? AND next_attempt_at <= ?", models.JobStatusRetryWait, now).Find(&due).Error; err != nil {
		return err
	}
	result := s.db.Model(&models.Job{}).Where("status = ? AND next_attempt_at <= ?", models.JobStatusRetryWait, now).Updates(map[string]any{"status": models.JobStatusQueued, "next_attempt_at": nil, "revision": gorm.Expr("revision + 1"), "updated_at": now})
	if result.Error == nil && result.RowsAffected > 0 {
		s.wake()
		for _, job := range due {
			job.Status = models.JobStatusQueued
			s.publish(job, "job.status_changed")
		}
	}
	return result.Error
}
func (s *QueueService) RecoverExpiredLeases() error {
	now := s.clock.Now()
	var recovered []models.Job
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var jobs []models.Job
		if err := tx.Where("status = ? AND lease_expires_at <= ?", models.JobStatusRunning, now).Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			var policy models.QueuePolicy
			if err := tx.First(&policy, "job_type = ?", job.JobType).Error; err != nil {
				return err
			}
			status := models.JobStatusRetryWait
			next := any(now)
			code := "worker_lease_expired"
			message := "Worker connection was lost; the job will resume from its checkpoint."
			pendingInterrupt := job.InterruptStatus == models.JobStatusPaused || job.InterruptStatus == models.JobStatusCancelled
			if pendingInterrupt {
				status = models.JobStatusQueued
				next = nil
				code = "worker_interrupt_recovered"
				message = "Worker control was interrupted and will be reconciled with the provider."
			} else if job.AttemptCount >= policy.MaxAttempts {
				status = models.JobStatusFailed
				next = nil
			}
			updates := map[string]any{"status": status, "revision": job.Revision + 1, "next_attempt_at": next, "last_error_code": code, "last_error_message": message, "updated_at": now}
			if !pendingInterrupt {
				updates["interrupt_status"] = ""
				updates["cancellation_asked"] = false
			}
			if status == models.JobStatusCancelled {
				updates["finished_at"] = now
			}
			releaseLease(updates)
			if err := tx.Model(&job).Updates(updates).Error; err != nil {
				return err
			}
			if err := recordJobEvent(tx, job.ID, "lease.expired", models.JobStatusRunning, status, nil, code, now); err != nil {
				return err
			}
			if err := closeAttempt(tx, job, status, code, message, now); err != nil {
				return err
			}
			job.Status = status
			recovered = append(recovered, job)
		}
		return nil
	})
	if err == nil {
		for _, job := range recovered {
			s.publish(job, "job.status_changed")
		}
	}
	return err
}

func queueNotFound(err error) error {
	if err == gorm.ErrRecordNotFound {
		return appError(CodeNotFound, "任务不存在", err)
	}
	return err
}
