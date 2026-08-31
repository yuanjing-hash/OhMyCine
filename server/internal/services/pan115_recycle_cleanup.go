package services

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"gorm.io/gorm"
)

const JobTypePan115RecycleCleanup = "pan115_recycle_cleanup"

type pan115RecycleCleanupJobPayload struct {
	ConnectionID uint   `json:"connection_id"`
	Revision     uint64 `json:"revision"`
}

// Pan115RecycleCleanupService scans persisted connection policy and enqueues
// one coalesced system job per due account. SQLite remains authoritative.
type Pan115RecycleCleanupService struct {
	db          *gorm.DB
	queue       *QueueService
	audit       *AuditService
	connections *ConnectionService
	log         zerolog.Logger
	now         func() time.Time
	tick        time.Duration
	pollMu      sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func NewPan115RecycleCleanupService(db *gorm.DB, queue *QueueService, audit *AuditService, connections *ConnectionService, log zerolog.Logger) *Pan115RecycleCleanupService {
	return &Pan115RecycleCleanupService{db: db, queue: queue, audit: audit, connections: connections, log: log, now: time.Now, tick: 30 * time.Second}
}

func (s *Pan115RecycleCleanupService) Start(parent context.Context) error {
	if s.cancel != nil {
		return errors.New("pan115 recycle cleanup scheduler already started")
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	if err := s.Poll(ctx); err != nil {
		cancel()
		s.cancel = nil
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Poll(ctx); err != nil {
					s.log.Warn().Str("error_code", "pan115_recycle_cleanup_scan_failed").Msg("115 回收站清理调度扫描失败")
				}
			}
		}
	}()
	return nil
}

func (s *Pan115RecycleCleanupService) Close() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
	s.cancel = nil
}

func (s *Pan115RecycleCleanupService) Poll(ctx context.Context) error {
	// Poll can be called by Start and tests/manual recovery at the same time.
	// Serialize the scan so a due account cannot create two jobs before either
	// caller observes the other's insert.
	s.pollMu.Lock()
	defer s.pollMu.Unlock()

	now := s.now().UTC()
	var records []models.Connection
	if err := s.db.WithContext(ctx).Where("provider = ? AND enabled = ? AND recycle_cleanup_enabled = ? AND recycle_cleanup_next_run_at IS NOT NULL AND recycle_cleanup_next_run_at <= ?", models.ConnectionProviderPan115, true, true, now).Order("recycle_cleanup_next_run_at,id").Find(&records).Error; err != nil {
		return err
	}
	for _, record := range records {
		resourceKey := "connection:" + strconv.FormatUint(uint64(record.ID), 10)
		var active int64
		if err := s.db.WithContext(ctx).Model(&models.Job{}).
			Where("job_type = ? AND resource_key = ? AND coalescing_key = ? AND status IN ?", JobTypePan115RecycleCleanup, resourceKey, "scheduled", activeJobStatuses()).
			Count(&active).Error; err != nil {
			return err
		}
		// Queue coalescing intentionally advances generation/revision. Scheduled
		// polling must not use that behavior because it can revoke a running job.
		if active > 0 {
			continue
		}
		_, err := s.queue.Enqueue(EnqueueJobInput{
			System: true, JobType: JobTypePan115RecycleCleanup, Priority: 10,
			DisplayName: "清空 115 回收站", ResourceKey: resourceKey,
			CoalescingKey: "scheduled", Payload: pan115RecycleCleanupJobPayload{ConnectionID: record.ID, Revision: record.Revision},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func NewPan115RecycleCleanupWorker(service *Pan115RecycleCleanupService) Worker {
	return WorkerFunc(service.run)
}

func (s *Pan115RecycleCleanupService) run(ctx context.Context, _ JobRuntime, claimed ClaimedJob) WorkerResult {
	var payload pan115RecycleCleanupJobPayload
	if err := json.Unmarshal([]byte(claimed.Job.PayloadJSON), &payload); err != nil || payload.ConnectionID == 0 || payload.Revision == 0 {
		return WorkerResult{ErrorCode: CodeInvalidRequest, ErrorMessage: "回收站清理任务参数无效"}
	}
	var record models.Connection
	if err := s.db.WithContext(ctx).First(&record, payload.ConnectionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return WorkerResult{}
		}
		return WorkerResult{ErrorCode: "pan115_recycle_cleanup_state_failed", ErrorMessage: "无法读取回收站清理策略"}
	}
	// Any configuration mutation revokes this frozen job. It safely completes
	// without touching the provider; a future due scan uses the new revision.
	if record.Provider != models.ConnectionProviderPan115 || !record.Enabled || !record.RecycleCleanupEnabled || record.Revision != payload.Revision || record.RecycleCredentialCiphertext == "" {
		return WorkerResult{}
	}
	if _, err := nextRecycleCleanup(record.RecycleCleanupCron, s.now()); err != nil {
		return s.finish(record, false, CodeInvalidRequest)
	}
	latest, driver, err := s.connections.driver(record.ID)
	if err != nil || latest.Revision != payload.Revision || !latest.RecycleCleanupEnabled {
		code := ErrorCode(err)
		if code == "" {
			code = "pan115_recycle_cleanup_unavailable"
		}
		return s.finish(record, false, code)
	}
	cleaner, ok := driver.(cloudpkg.RecycleBinCleaner)
	if !ok {
		return s.finish(record, false, "pan115_recycle_cleanup_unsupported")
	}
	if err := cleaner.ClearRecycleBin(ctx); err != nil {
		code, _ := cloudpkg.ErrorInfo(err)
		if code == "" {
			code = "pan115_recycle_cleanup_failed"
		}
		return s.finish(record, false, code)
	}
	return s.finish(record, true, "")
}

func (s *Pan115RecycleCleanupService) finish(record models.Connection, success bool, errorCode string) WorkerResult {
	now := s.now().UTC()
	next, err := nextRecycleCleanup(record.RecycleCleanupCron, now)
	if err != nil {
		next = now.Add(24 * time.Hour)
		if errorCode == "" {
			errorCode = CodeInvalidRequest
		}
		success = false
	}
	status, outcome := models.RecycleCleanupStatusFailed, "failure"
	if success {
		status, outcome = models.RecycleCleanupStatusSucceeded, "success"
	}
	transactionErr := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.Connection{}).Where("id = ? AND revision = ?", record.ID, record.Revision).Updates(map[string]any{
			"recycle_cleanup_last_run_at": now, "recycle_cleanup_last_status": status,
			"recycle_cleanup_last_error_code": safeLabel(errorCode, 96), "recycle_cleanup_next_run_at": next,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		return s.audit.Record(tx, nil, "connection.recycle_cleanup.run", "connection", uintID(record.ID), outcome, map[string]any{"revision": record.Revision, "error_code": safeLabel(errorCode, 96), "trigger": "schedule"}, RequestContext{})
	})
	if transactionErr != nil {
		return WorkerResult{ErrorCode: "pan115_recycle_cleanup_state_failed", ErrorMessage: "无法保存回收站清理状态"}
	}
	// Provider failure is a completed schedule occurrence, not an immediate
	// retry. The next Cron occurrence remains the only retry boundary.
	return WorkerResult{}
}
