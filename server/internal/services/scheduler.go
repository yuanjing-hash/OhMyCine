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
	running   map[string]context.CancelFunc
}

func NewScheduler(queue *QueueService, registry *WorkerRegistry, log zerolog.Logger) *Scheduler {
	s := &Scheduler{queue: queue, registry: registry, log: log, tick: time.Second, running: map[string]context.CancelFunc{}}
	queue.SetInterrupt(s.interrupt)
	return s
}
func (s *Scheduler) interrupt(jobID string) {
	s.runningMu.Lock()
	cancel := s.running[jobID]
	s.runningMu.Unlock()
	if cancel != nil {
		cancel()
	}
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
	return nil
}
func (s *Scheduler) Close() {
	if s.cancel != nil {
		s.cancel()
		s.wg.Wait()
		s.cancel = nil
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
			s.log.Error().Err(err).Msg("Queue claim failed")
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
		s.runningMu.Lock()
		s.running[claimed.Job.ID] = cancel
		s.runningMu.Unlock()
		s.wg.Add(1)
		go func(job ClaimedJob, workerCtx context.Context, cancel context.CancelFunc) {
			defer s.wg.Done()
			defer func() { cancel(); s.runningMu.Lock(); delete(s.running, job.Job.ID); s.runningMu.Unlock() }()
			runtime := workerRuntime{queue: s.queue, job: job}
			result := worker.Run(workerCtx, runtime, job)
			if workerCtx.Err() != nil {
				_ = s.queue.AcknowledgeInterrupt(job.Job.ID, job.LeaseToken)
				return
			}
			if result.Wait != nil {
				if err := s.queue.Wait(job.Job.ID, job.LeaseToken, *result.Wait); err != nil && !errors.Is(err, context.Canceled) {
					s.log.Warn().Err(err).Str("job_id", job.Job.ID).Msg("Worker wait result rejected")
				}
				return
			}
			if result.RetryAt != nil {
				_ = s.queue.RetryLater(job.Job.ID, job.LeaseToken, result.ErrorCode, result.ErrorMessage, *result.RetryAt)
				return
			}
			if result.ErrorCode != "" {
				_ = s.queue.Fail(job.Job.ID, job.LeaseToken, result.ErrorCode, result.ErrorMessage)
				return
			}
			_ = s.queue.Complete(job.Job.ID, job.LeaseToken)
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
