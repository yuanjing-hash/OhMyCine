package services

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

type WorkerResult struct {
	RetryAt      *time.Time
	Wait         *WaitForAction
	ErrorCode    string
	ErrorMessage string
}
type JobRuntime interface {
	Heartbeat(progress *float64, processed, total *int64, speed *float64, eta *int64) error
	Checkpoint(target any) error
}
type Worker interface {
	Run(context.Context, JobRuntime, ClaimedJob) WorkerResult
}
type InterruptibleWorker interface {
	Interrupt(context.Context, ClaimedJob, string) error
}
type WorkerFunc func(context.Context, JobRuntime, ClaimedJob) WorkerResult

func (f WorkerFunc) Run(ctx context.Context, runtime JobRuntime, job ClaimedJob) WorkerResult {
	return f(ctx, runtime, job)
}

type WorkerRegistry struct {
	mu      sync.RWMutex
	workers map[string]Worker
}

func NewWorkerRegistry() *WorkerRegistry { return &WorkerRegistry{workers: map[string]Worker{}} }
func (r *WorkerRegistry) Register(jobType string, worker Worker) error {
	jobType = strings.ToLower(strings.TrimSpace(jobType))
	if jobType == "" || worker == nil {
		return fmt.Errorf("worker type and implementation are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.workers[jobType]; exists {
		return fmt.Errorf("worker %q is already registered", jobType)
	}
	r.workers[jobType] = worker
	return nil
}
func (r *WorkerRegistry) Get(jobType string) (Worker, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	worker, ok := r.workers[jobType]
	return worker, ok
}
func (r *WorkerRegistry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]string, 0, len(r.workers))
	for jobType := range r.workers {
		result = append(result, jobType)
	}
	sort.Strings(result)
	return result
}

type Scheduler struct {
	queue     *QueueService
	registry  *WorkerRegistry
	log       zerolog.Logger
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	tick      time.Duration
	runningMu sync.Mutex
	running   map[string]runningWork
}

type runningWork struct {
	cancel context.CancelFunc
	worker Worker
	job    ClaimedJob
}

const (
	defaultJobLeaseDuration = 30 * time.Second
	minLeaseKeepalive       = time.Second
	maxLeaseKeepalive       = 10 * time.Second
)

type leaseKeepalive struct {
	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
	errMu    sync.Mutex
	err      error
}

func (k *leaseKeepalive) setError(err error) {
	k.errMu.Lock()
	k.err = err
	k.errMu.Unlock()
}

func (k *leaseKeepalive) Stop() error {
	k.stopOnce.Do(func() { close(k.stop) })
	<-k.done
	k.errMu.Lock()
	defer k.errMu.Unlock()
	return k.err
}

func NewScheduler(queue *QueueService, registry *WorkerRegistry, log zerolog.Logger) *Scheduler {
	s := &Scheduler{queue: queue, registry: registry, log: log, tick: time.Second, running: map[string]runningWork{}}
	queue.SetInterrupt(s.interrupt)
	return s
}
func (s *Scheduler) interrupt(jobID, action string) {
	s.runningMu.Lock()
	running, ok := s.running[jobID]
	s.runningMu.Unlock()
	if !ok {
		return
	}
	interruptible, ok := running.worker.(InterruptibleWorker)
	if !ok {
		running.cancel()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := interruptible.Interrupt(ctx, running.job, action); err != nil {
			serverlog.OperationTaskQueue.Event(s.log.Warn()).Str("job_id", jobID).Str("action", action).Str("error_code", "provider_task_control_failed").Msg(serverlog.OperationTaskQueue.Message("下载器控制操作失败"))
			_ = s.queue.RejectInterrupt(jobID, running.job.LeaseToken, action, "downloader_control_failed", "下载器未能执行任务控制操作")
			return
		}
		running.cancel()
	}()
}
func (s *Scheduler) Start(parent context.Context) error {
	if s.cancel != nil {
		return errors.New("scheduler already started")
	}
	if err := s.queue.RecoverExpiredLeases(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.wg.Add(1)
	go s.loop(ctx)
	serverlog.OperationTaskQueue.Event(s.log.Info()).Strs("worker_types", s.registry.Types()).Msg(serverlog.OperationTaskQueue.Message("调度器已启动"))
	return nil
}
func (s *Scheduler) Close() {
	if s.cancel != nil {
		s.cancel()
		s.wg.Wait()
		s.cancel = nil
		serverlog.OperationTaskQueue.Event(s.log.Info()).Msg(serverlog.OperationTaskQueue.Message("调度器已停止"))
	}
}
func (s *Scheduler) loop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.queue.Wakeups():
			s.dispatch(ctx)
		case <-ticker.C:
			_ = s.queue.PromoteDueRetries()
			_ = s.queue.RecoverExpiredLeases()
			s.dispatch(ctx)
		}
	}
}

func (s *Scheduler) startLeaseKeepalive(ctx context.Context, cancel context.CancelFunc, job ClaimedJob) *leaseKeepalive {
	keepalive := &leaseKeepalive{stop: make(chan struct{}), done: make(chan struct{})}
	leaseDuration := claimedLeaseDuration(job)
	go func() {
		defer close(keepalive.done)
		timer := time.NewTimer(leaseKeepaliveInterval(leaseDuration))
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-keepalive.stop:
				return
			case <-timer.C:
				renewedDuration, err := s.queue.renewLease(job.Job.ID, job.LeaseToken)
				if err != nil {
					keepalive.setError(err)
					serverlog.OperationTaskQueue.Event(s.log.Warn()).Str("job_id", job.Job.ID).Str("job_type", job.Job.JobType).Str("error_code", ErrorCode(err)).Msg(serverlog.OperationTaskQueue.Message("任务租约保活失败，正在停止 Worker"))
					cancel()
					return
				}
				leaseDuration = renewedDuration
				timer.Reset(leaseKeepaliveInterval(leaseDuration))
			}
		}
	}()
	return keepalive
}

func claimedLeaseDuration(job ClaimedJob) time.Duration {
	if job.Job.HeartbeatAt != nil && job.Job.LeaseExpiresAt != nil {
		if duration := job.Job.LeaseExpiresAt.Sub(*job.Job.HeartbeatAt); duration > 0 {
			return duration
		}
	}
	return defaultJobLeaseDuration
}

func leaseKeepaliveInterval(leaseDuration time.Duration) time.Duration {
	if leaseDuration <= 0 {
		leaseDuration = defaultJobLeaseDuration
	}
	interval := leaseDuration / 3
	if interval < minLeaseKeepalive {
		interval = minLeaseKeepalive
	}
	if interval > maxLeaseKeepalive {
		interval = maxLeaseKeepalive
	}
	if half := leaseDuration / 2; half > 0 && interval > half {
		interval = half
	}
	return interval
}

func (s *Scheduler) dispatch(ctx context.Context) {
	jobTypes := s.registry.Types()
	// An empty registry means this process cannot execute queue work. Passing an
	// empty filter to QueueService.Claim intentionally means "all policies" for
	// maintenance/tests, so the scheduler must fail closed here instead of
	// claiming jobs that have no executable worker.
	if len(jobTypes) == 0 {
		return
	}
	for {
		claimed, err := s.queue.Claim(jobTypes)
		if err != nil {
			serverlog.OperationTaskQueue.Event(s.log.Error()).Str("error_code", "queue_claim_failed").Msg(serverlog.OperationTaskQueue.Message("领取任务失败"))
			return
		}
		if claimed == nil {
			return
		}
		worker, ok := s.registry.Get(claimed.Job.JobType)
		if !ok {
			_ = s.queue.Fail(claimed.Job.ID, claimed.LeaseToken, CodeQueueWorkerUnavailable, "No worker is registered for this job type.")
			continue
		}
		workerCtx, cancel := context.WithCancel(ctx)
		serverlog.OperationTaskQueue.Event(s.log.Info()).Str("job_id", claimed.Job.ID).Str("job_type", claimed.Job.JobType).Int("attempt", claimed.Job.AttemptCount).Msg(serverlog.OperationTaskQueue.Message("任务开始执行"))
		s.runningMu.Lock()
		s.running[claimed.Job.ID] = runningWork{cancel: cancel, worker: worker, job: *claimed}
		s.runningMu.Unlock()
		s.wg.Add(1)
		go func(job ClaimedJob, workerCtx context.Context, cancel context.CancelFunc) {
			defer s.wg.Done()
			defer func() { cancel(); s.runningMu.Lock(); delete(s.running, job.Job.ID); s.runningMu.Unlock() }()
			runtime := workerRuntime{queue: s.queue, job: job}
			keepalive := s.startLeaseKeepalive(workerCtx, cancel, job)
			defer keepalive.Stop()
			if job.Job.InterruptStatus == models.JobStatusPaused || job.Job.InterruptStatus == models.JobStatusCancelled {
				action := "pause"
				if job.Job.InterruptStatus == models.JobStatusCancelled {
					action = "cancel"
				}
				origin := providerControlOrigin(job.Job.CheckpointJSON)
				leaseReleasesOnReject := origin == models.JobStatusPaused || origin == models.JobStatusFailed
				interruptible, ok := worker.(InterruptibleWorker)
				if ok {
					if err := interruptible.Interrupt(workerCtx, job, action); err == nil {
						if keepalive.Stop() != nil {
							return
						}
						if ackErr := s.queue.AcknowledgeInterrupt(job.Job.ID, job.LeaseToken); ackErr != nil {
							serverlog.OperationTaskQueue.Event(s.log.Warn()).Str("job_id", job.Job.ID).Str("action", action).Str("error_code", "queue_interrupt_ack_failed").Msg(serverlog.OperationTaskQueue.Message("控制结果保存失败"))
						}
						return
					} else if workerCtx.Err() != nil {
						return
					}
					if leaseReleasesOnReject && keepalive.Stop() != nil {
						return
					}
					if rejectErr := s.queue.RejectInterrupt(job.Job.ID, job.LeaseToken, action, "downloader_control_failed", "下载器未能执行任务控制操作"); rejectErr != nil {
						serverlog.OperationTaskQueue.Event(s.log.Warn()).Str("job_id", job.Job.ID).Str("error_code", "queue_interrupt_rejection_persist_failed").Msg(serverlog.OperationTaskQueue.Message("控制失败状态保存失败"))
						return
					}
					if leaseReleasesOnReject {
						return
					}
				} else {
					if leaseReleasesOnReject && keepalive.Stop() != nil {
						return
					}
					if err := s.queue.RejectInterrupt(job.Job.ID, job.LeaseToken, action, CodeQueueWorkerUnavailable, "任务 Worker 不支持外部控制"); err != nil {
						return
					}
					if leaseReleasesOnReject {
						return
					}
				}
			}
			result := worker.Run(workerCtx, runtime, job)
			if keepalive.Stop() != nil {
				return
			}
			if workerCtx.Err() != nil {
				if ackErr := s.queue.AcknowledgeInterrupt(job.Job.ID, job.LeaseToken); ackErr != nil {
					serverlog.OperationTaskQueue.Event(s.log.Warn()).Str("job_id", job.Job.ID).Str("error_code", "queue_interrupt_cleanup_failed").Msg(serverlog.OperationTaskQueue.Message("中断清理状态保存失败"))
				}
				return
			}
			if result.Wait != nil {
				serverlog.OperationTaskQueue.Event(s.log.Info()).Str("job_id", job.Job.ID).Str("job_type", job.Job.JobType).Msg(serverlog.OperationTaskQueue.Message("任务等待用户处理"))
				if err := s.queue.Wait(job.Job.ID, job.LeaseToken, *result.Wait); err != nil && !errors.Is(err, context.Canceled) {
					serverlog.OperationTaskQueue.Event(s.log.Warn()).Str("job_id", job.Job.ID).Str("error_code", "queue_wait_persist_failed").Msg(serverlog.OperationTaskQueue.Message("等待状态保存失败"))
				}
				return
			}
			if result.RetryAt != nil {
				if err := s.queue.RetryLater(job.Job.ID, job.LeaseToken, result.ErrorCode, result.ErrorMessage, *result.RetryAt); err != nil {
					serverlog.OperationTaskQueue.Event(s.log.Error()).Str("job_id", job.Job.ID).Str("job_type", job.Job.JobType).Str("error_code", "queue_retry_persist_failed").Msg(serverlog.OperationTaskQueue.Message("重试状态保存失败"))
				} else if result.ErrorCode != "" {
					serverlog.OperationTaskQueue.Event(s.log.Warn()).Str("job_id", job.Job.ID).Str("job_type", job.Job.JobType).Str("error_code", result.ErrorCode).Time("retry_at", *result.RetryAt).Msg(serverlog.OperationTaskQueue.Message("任务已安排重试"))
				} else {
					serverlog.OperationTaskQueue.Event(s.log.Debug()).Str("job_id", job.Job.ID).Str("job_type", job.Job.JobType).Time("next_run_at", *result.RetryAt).Msg(serverlog.OperationTaskQueue.Message("任务已安排下次运行"))
				}
				return
			}
			if result.ErrorCode != "" {
				if err := s.queue.Fail(job.Job.ID, job.LeaseToken, result.ErrorCode, result.ErrorMessage); err != nil {
					serverlog.OperationTaskQueue.Event(s.log.Error()).Str("job_id", job.Job.ID).Str("job_type", job.Job.JobType).Str("error_code", "queue_failure_persist_failed").Msg(serverlog.OperationTaskQueue.Message("失败状态保存失败"))
				} else {
					serverlog.OperationTaskQueue.Event(s.log.Error()).Str("job_id", job.Job.ID).Str("job_type", job.Job.JobType).Str("error_code", result.ErrorCode).Msg(serverlog.OperationTaskQueue.Message("任务执行失败"))
				}
				return
			}
			if err := s.queue.Complete(job.Job.ID, job.LeaseToken); err != nil {
				serverlog.OperationTaskQueue.Event(s.log.Error()).Str("job_id", job.Job.ID).Str("job_type", job.Job.JobType).Str("error_code", "queue_completion_persist_failed").Msg(serverlog.OperationTaskQueue.Message("完成状态保存失败"))
			} else {
				serverlog.OperationTaskQueue.Event(s.log.Info()).Str("job_id", job.Job.ID).Str("job_type", job.Job.JobType).Msg(serverlog.OperationTaskQueue.Message("任务执行完成"))
			}
		}(*claimed, workerCtx, cancel)
	}
}

type workerRuntime struct {
	queue *QueueService
	job   ClaimedJob
}

func (r workerRuntime) Heartbeat(progress *float64, processed, total *int64, speed *float64, eta *int64) error {
	return r.queue.Heartbeat(r.job.Job.ID, r.job.LeaseToken, progress, processed, total, speed, eta)
}
func (r workerRuntime) Checkpoint(target any) error {
	return r.queue.SaveCheckpoint(r.job.Job.ID, r.job.LeaseToken, target)
}

// RegisterFakeWorkers provides deterministic typed workers for queue acceptance
// tests and local integration without pretending real adapters are implemented.
func RegisterFakeWorkers(registry *WorkerRegistry) {
	_ = registry.Register("fake", WorkerFunc(func(ctx context.Context, runtime JobRuntime, job ClaimedJob) WorkerResult {
		progress := 100.0
		processed, total := int64(1), int64(1)
		_ = runtime.Heartbeat(&progress, &processed, &total, nil, nil)
		return WorkerResult{}
	}))
}
