package services

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

type fakeQueueClock struct{ now time.Time }

func (c *fakeQueueClock) Now() time.Time { return c.now }

func queueFixture(t *testing.T) (*QueueService, Actor, *fakeQueueClock) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	user := models.User{Username: "queue-owner", UsernameNormalized: "queue-owner", DisplayName: "Queue Owner", PasswordHash: "x", Status: models.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	permissions := map[string]struct{}{}
	for _, code := range []string{authz.PermissionJobsReadAll, authz.PermissionJobsControlAll, authz.PermissionJobsRespond, authz.PermissionJobsReorder, authz.PermissionQueuePoliciesManage} {
		permissions[code] = struct{}{}
	}
	clock := &fakeQueueClock{now: now}
	service := NewQueueService(db, NewAuditService(db))
	service.SetClock(clock)
	return service, Actor{User: user, Permissions: permissions}, clock
}

func enqueueFake(t *testing.T, service *QueueService, actor Actor, name, resource string) JobDTO {
	t.Helper()
	job, err := service.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "fake", Priority: 10, DisplayName: name, ResourceKey: resource, Payload: map[string]any{"step": 1}})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestQueueLaneOrderingClaimCapacityAndLeaseGuard(t *testing.T) {
	service, actor, _ := queueFixture(t)
	first := enqueueFake(t, service, actor, "First", "provider-a")
	second := enqueueFake(t, service, actor, "Second", "provider-b")
	third := enqueueFake(t, service, actor, "Third", "provider-c")
	if _, err := service.Lane(actor, "fake", 10); err != nil {
		t.Fatal(err)
	}
	ordered := []LaneRevision{{ID: third.ID, Revision: third.Revision}, {ID: first.ID, Revision: first.Revision}, {ID: second.ID, Revision: second.Revision}}
	lane, err := service.Reorder(actor, "fake", 10, ordered, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if lane[0].ID != third.ID || *lane[0].LaneRank != 1 {
		t.Fatalf("lane=%+v", lane)
	}
	claimed, err := service.Claim([]string{"fake"})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.Job.ID != third.ID {
		t.Fatalf("claimed=%+v", claimed)
	}
	if _, err := service.Reorder(actor, "fake", 10, ordered, RequestContext{}); ErrorCode(err) != CodeQueueOrderConflict {
		t.Fatalf("reorder conflict=%v", err)
	}
	if err := service.Complete(claimed.Job.ID, "stale-token"); ErrorCode(err) != CodeQueueLeaseInvalid {
		t.Fatalf("stale lease=%v", err)
	}
	if err := service.Complete(claimed.Job.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadQueueAllowsUnkeyedQBittorrentJobsWhileSerializingProviderKey(t *testing.T) {
	service, actor, _ := queueFixture(t)
	enqueue := func(name, resource string) JobDTO {
		job, err := service.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: name, ResourceKey: resource, Payload: map[string]any{"step": 1}})
		if err != nil {
			t.Fatal(err)
		}
		return job
	}
	qbitA := enqueue("qbit-a", "")
	qbitB := enqueue("qbit-b", "")
	panA := enqueue("pan-a", "downloader:pan")
	panB := enqueue("pan-b", "downloader:pan")
	claimed := map[string]struct{}{}
	for index := 0; index < 3; index++ {
		job, err := service.Claim([]string{"download"})
		if err != nil || job == nil {
			t.Fatalf("claim %d=%+v err=%v", index, job, err)
		}
		claimed[job.Job.ID] = struct{}{}
	}
	for _, id := range []string{qbitA.ID, qbitB.ID} {
		if _, ok := claimed[id]; !ok {
			t.Fatalf("qBittorrent job %s remained serialized: claimed=%v", id, claimed)
		}
	}
	panClaims := 0
	for _, id := range []string{panA.ID, panB.ID} {
		if _, ok := claimed[id]; ok {
			panClaims++
		}
	}
	if panClaims != 1 {
		t.Fatalf("same 115 provider claims=%d, want 1", panClaims)
	}
	if next, err := service.Claim([]string{"download"}); err != nil || next != nil {
		t.Fatalf("second 115 task bypassed resource limit: next=%+v err=%v", next, err)
	}
}

func TestQueueActionRetryRecoveryCoalescingAndPrivateState(t *testing.T) {
	service, actor, clock := queueFixture(t)
	if _, err := service.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "fake", DisplayName: "Unsafe", Payload: map[string]any{"nested": map[string]any{"api_token": "secret"}}}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("unsafe payload=%v", err)
	}
	for _, value := range []string{`C:\Media\movie.mkv`, `\\nas\media\movie.mkv`, `/srv/media/movie.mkv`, `https://cdn.example.test/movie?token=secret`} {
		if _, err := service.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "fake", DisplayName: "Unsafe value", Payload: map[string]any{"source": value}}); ErrorCode(err) != CodeInvalidRequest {
			t.Fatalf("unsafe value accepted %q: %v", value, err)
		}
	}
	job, err := service.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "fake", Priority: 1, DisplayName: "Coalesced", ResourceKey: "r", CoalescingKey: "same", Payload: map[string]any{"step": 1}})
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "fake", Priority: 1, DisplayName: "Coalesced", ResourceKey: "r", CoalescingKey: "same", Payload: map[string]any{"step": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != job.ID {
		t.Fatal("coalescing created duplicate")
	}
	claimed, err := service.Claim([]string{"fake"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SaveCheckpoint(claimed.Job.ID, claimed.LeaseToken, map[string]any{"page": 2}); err != nil {
		t.Fatal(err)
	}
	if err := service.Wait(claimed.Job.ID, claimed.LeaseToken, WaitForAction{ActionType: "conflict", Prompt: "Choose", Options: []string{"replace", "skip"}, Checkpoint: map[string]any{"page": 2}}); err != nil {
		t.Fatal(err)
	}
	detail, err := service.Get(actor, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != models.JobStatusWaitingUserAction || detail.Action == nil {
		t.Fatalf("detail=%+v", detail)
	}
	if _, err := service.Respond(actor, job.ID, detail.Action.Version, "skip", RequestContext{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Respond(actor, job.ID, detail.Action.Version, "skip", RequestContext{}); ErrorCode(err) != CodeQueueActionStale {
		t.Fatalf("stale action=%v", err)
	}
	claimed, err = service.Claim([]string{"fake"})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(11 * time.Second)
	if err := service.RecoverExpiredLeases(); err != nil {
		t.Fatal(err)
	}
	if err := service.Complete(claimed.Job.ID, claimed.LeaseToken); ErrorCode(err) != CodeQueueLeaseInvalid {
		t.Fatalf("expired lease=%v", err)
	}
	if err := service.PromoteDueRetries(); err != nil {
		t.Fatal(err)
	}
	timeline, err := service.Timeline(actor, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) < 5 {
		t.Fatalf("timeline=%+v", timeline)
	}
}

func TestQueueActionResponseIsCheckpointedAndSupersededByNextWait(t *testing.T) {
	service, actor, _ := queueFixture(t)
	job, err := service.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "fake", DisplayName: "Action checkpoint", Payload: map[string]any{"step": 1}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim([]string{"fake"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := service.Wait(job.ID, claimed.LeaseToken, WaitForAction{ActionType: "import_conflict", Prompt: "Choose", Options: []string{"overwrite", "skip"}, Checkpoint: map[string]any{"stage": "import"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Respond(actor, job.ID, 1, "overwrite", RequestContext{}); err != nil {
		t.Fatal(err)
	}
	claimed, err = service.Claim([]string{"fake"})
	if err != nil || claimed == nil {
		t.Fatalf("reclaim=%+v err=%v", claimed, err)
	}
	if got := checkpointActionResponse(claimed.Job.CheckpointJSON, "import_conflict"); got != "overwrite" {
		t.Fatalf("checkpoint action=%q raw=%s", got, claimed.Job.CheckpointJSON)
	}
	if err := service.Wait(job.ID, claimed.LeaseToken, WaitForAction{ActionType: "import_conflict", Prompt: "Choose again", Options: []string{"overwrite", "skip"}, Checkpoint: map[string]any{"stage": "import"}}); err != nil {
		t.Fatal(err)
	}
	var waiting models.Job
	if err := service.db.First(&waiting, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got := checkpointActionResponse(waiting.CheckpointJSON, "import_conflict"); got != "" {
		t.Fatalf("old action response survived new wait: %q raw=%s", got, waiting.CheckpointJSON)
	}
}

func checkpointActionResponse(raw, actionType string) string {
	var checkpoint struct {
		ActionResponse *struct {
			ActionType string `json:"action_type"`
			Value      string `json:"value"`
		} `json:"action_response"`
	}
	if json.Unmarshal([]byte(raw), &checkpoint) != nil || checkpoint.ActionResponse == nil || checkpoint.ActionResponse.ActionType != actionType {
		return ""
	}
	return checkpoint.ActionResponse.Value
}

func TestProviderControlIntentSurvivesClaimLeaseRecovery(t *testing.T) {
	service, actor, clock := queueFixture(t)
	job, err := service.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", Provider: models.DownloaderTypeQBittorrent, DisplayName: "Restart-safe cancel", Payload: map[string]any{"download_task_id": "task-id"}})
	if err != nil {
		t.Fatal(err)
	}
	controlled, err := service.Control(actor, job.ID, "cancel", RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if controlled.Status != models.JobStatusQueued || controlled.InterruptPending != models.JobStatusCancelled {
		t.Fatalf("controlled=%+v", controlled)
	}
	if _, err := service.Control(actor, job.ID, "cancel", RequestContext{}); ErrorCode(err) != CodeQueueStateConflict {
		t.Fatalf("duplicate pending control error=%v", err)
	}
	claimed, err := service.Claim([]string{"download"})
	if err != nil || claimed == nil || claimed.Job.InterruptStatus != models.JobStatusCancelled {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	clock.now = clock.now.Add(time.Minute)
	if err := service.RecoverExpiredLeases(); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.Get(actor, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != models.JobStatusQueued || recovered.InterruptPending != models.JobStatusCancelled || !recovered.CancellationRequested {
		t.Fatalf("recovered=%+v", recovered)
	}
	reclaimed, err := service.Claim([]string{"download"})
	if err != nil || reclaimed == nil || reclaimed.Job.InterruptStatus != models.JobStatusCancelled {
		t.Fatalf("reclaimed=%+v err=%v", reclaimed, err)
	}
}

func TestRejectedProviderCancelRestoresPreviouslyPausedJob(t *testing.T) {
	service, actor, _ := queueFixture(t)
	job, err := service.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", Provider: models.DownloaderTypeQBittorrent, DisplayName: "Paused provider cancel", Payload: map[string]any{"download_task_id": "task-id"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.Job{}).Where("id = ?", job.ID).Update("status", models.JobStatusPaused).Error; err != nil {
		t.Fatal(err)
	}
	controlled, err := service.Control(actor, job.ID, "cancel", RequestContext{})
	if err != nil || controlled.Status != models.JobStatusQueued || providerControlOrigin(controlledCheckpoint(t, service, job.ID)) != models.JobStatusPaused {
		t.Fatalf("controlled=%+v err=%v", controlled, err)
	}
	claimed, err := service.Claim([]string{"download"})
	if err != nil || claimed == nil {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	if err := service.RejectInterrupt(job.ID, claimed.LeaseToken, "cancel", "downloader_control_failed", "provider rejected cancel"); err != nil {
		t.Fatal(err)
	}
	restored, err := service.Get(actor, job.ID)
	if err != nil || restored.Status != models.JobStatusPaused || restored.InterruptPending != "" || restored.CancellationRequested {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
}

func controlledCheckpoint(t *testing.T, service *QueueService, id string) string {
	t.Helper()
	var job models.Job
	if err := service.db.First(&job, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return job.CheckpointJSON
}

func TestSchedulerKeepsLeaseAliveWhileWorkerBlocksPastNominalLease(t *testing.T) {
	service, actor, _ := queueFixture(t)
	service.SetClock(realClock{})
	var policy models.QueuePolicy
	if err := service.db.First(&policy, "job_type = ?", "fake").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdatePolicy(actor, "fake", policy.Revision, 1, 1, 3, 5, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	job := enqueueFake(t, service, actor, "Slow provider mutation", "provider-a")
	var runs atomic.Int32
	registry := NewWorkerRegistry()
	if err := registry.Register("fake", WorkerFunc(func(ctx context.Context, _ JobRuntime, _ ClaimedJob) WorkerResult {
		runs.Add(1)
		select {
		case <-ctx.Done():
			return WorkerResult{ErrorCode: "unexpected_cancel", ErrorMessage: "worker was cancelled"}
		case <-time.After(6 * time.Second):
			return WorkerResult{}
		}
	})); err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(service, registry, zerolog.Nop())
	scheduler.tick = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	if err := scheduler.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { cancel(); scheduler.Close() }()
	service.wake()
	deadline := time.Now().Add(9 * time.Second)
	var detail JobDTO
	var err error
	for time.Now().Before(deadline) {
		detail, err = service.Get(actor, job.ID)
		if err == nil && (detail.Status == models.JobStatusCompleted || detail.Status == models.JobStatusFailed) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || detail.Status != models.JobStatusCompleted || detail.AttemptCount != 1 || runs.Load() != 1 {
		t.Fatalf("detail=%+v runs=%d err=%v", detail, runs.Load(), err)
	}
	var expired int64
	if err := service.db.Model(&models.JobStatusEvent{}).Where("job_id = ? AND event_type = ?", job.ID, "lease.expired").Count(&expired).Error; err != nil {
		t.Fatal(err)
	}
	if expired != 0 {
		t.Fatalf("lease expired %d times", expired)
	}
}

func TestSchedulerCancelsWorkerWhenLeaseRenewalLosesOwnership(t *testing.T) {
	service, actor, _ := queueFixture(t)
	service.SetClock(realClock{})
	var policy models.QueuePolicy
	if err := service.db.First(&policy, "job_type = ?", "fake").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdatePolicy(actor, "fake", policy.Revision, 1, 1, 3, 5, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	job := enqueueFake(t, service, actor, "Lost lease", "provider-a")
	claimedCh := make(chan ClaimedJob, 1)
	cancelledCh := make(chan struct{})
	registry := NewWorkerRegistry()
	if err := registry.Register("fake", WorkerFunc(func(ctx context.Context, _ JobRuntime, claimed ClaimedJob) WorkerResult {
		claimedCh <- claimed
		<-ctx.Done()
		close(cancelledCh)
		return WorkerResult{}
	})); err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(service, registry, zerolog.Nop())
	scheduler.tick = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	if err := scheduler.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { cancel(); scheduler.Close() }()
	service.wake()
	var claimed ClaimedJob
	select {
	case claimed = <-claimedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("worker was not claimed")
	}
	if err := service.Complete(job.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelledCh:
	case <-time.After(3 * time.Second):
		t.Fatal("worker kept running after definitive lease loss")
	}
}

func TestWorkerRegistryValidationAndDeterminism(t *testing.T) {
	registry := NewWorkerRegistry()
	if err := registry.Register(" Fake ", WorkerFunc(func(context.Context, JobRuntime, ClaimedJob) WorkerResult { return WorkerResult{} })); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("fake", WorkerFunc(func(context.Context, JobRuntime, ClaimedJob) WorkerResult { return WorkerResult{} })); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := registry.Register("", nil); err == nil {
		t.Fatal("nil accepted")
	}
}

func TestSchedulerWithEmptyRegistryLeavesJobsQueued(t *testing.T) {
	service, actor, _ := queueFixture(t)
	job := enqueueFake(t, service, actor, "No worker yet", "provider-a")
	scheduler := NewScheduler(service, NewWorkerRegistry(), zerolog.Nop())

	scheduler.dispatch(context.Background())

	detail, err := service.Get(actor, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != models.JobStatusQueued || detail.AttemptCount != 0 {
		t.Fatalf("empty registry claimed job: %+v", detail)
	}
}

func TestRunningInterruptKeepsCapacityUntilWorkerAcknowledges(t *testing.T) {
	service, actor, _ := queueFixture(t)
	var policy models.QueuePolicy
	if err := service.db.First(&policy, "job_type = ?", "fake").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdatePolicy(actor, "fake", policy.Revision, 1, 1, 3, 10, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	first := enqueueFake(t, service, actor, "Blocking", "provider-a")
	_ = enqueueFake(t, service, actor, "Next", "provider-b")
	claimed, err := service.Claim([]string{"fake"})
	if err != nil || claimed == nil || claimed.Job.ID != first.ID {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	interrupted := make(chan string, 1)
	service.SetInterrupt(func(id, action string) {
		if action != "pause" {
			t.Errorf("action=%q", action)
		}
		interrupted <- id
	})
	detail, err := service.Control(actor, first.ID, "pause", RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != models.JobStatusRunning || detail.InterruptPending != models.JobStatusPaused || !detail.CancellationRequested {
		t.Fatalf("pending detail=%+v", detail)
	}
	if id := <-interrupted; id != first.ID {
		t.Fatal(id)
	}
	secondClaim, err := service.Claim([]string{"fake"})
	if err != nil {
		t.Fatal(err)
	}
	if secondClaim != nil {
		t.Fatalf("resource/type capacity was freed before ack: %+v", secondClaim)
	}
	if err := service.AcknowledgeInterrupt(first.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	detail, _ = service.Get(actor, first.ID)
	if detail.Status != models.JobStatusPaused || detail.InterruptPending != "" {
		t.Fatalf("ack detail=%+v", detail)
	}
}

func TestRejectInterruptKeepsRunningAndClearsPendingIntent(t *testing.T) {
	service, actor, _ := queueFixture(t)
	job := enqueueFake(t, service, actor, "Provider control", "provider-a")
	claimed, err := service.Claim([]string{"fake"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	service.SetInterrupt(func(string, string) {})
	if _, err := service.Control(actor, job.ID, "pause", RequestContext{}); err != nil {
		t.Fatal(err)
	}
	if err := service.RejectInterrupt(job.ID, claimed.LeaseToken, "pause", "downloader_control_failed", "下载器未能暂停任务"); err != nil {
		t.Fatal(err)
	}
	detail, err := service.Get(actor, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != models.JobStatusRunning || detail.InterruptPending != "" || detail.CancellationRequested || detail.LastErrorCode != "downloader_control_failed" {
		t.Fatalf("detail=%+v", detail)
	}
}

func TestRejectInterruptRejectsStaleLeaseWithoutClearingPendingIntent(t *testing.T) {
	service, actor, _ := queueFixture(t)
	job := enqueueFake(t, service, actor, "Provider control", "provider-a")
	claimed, err := service.Claim([]string{"fake"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	service.SetInterrupt(func(string, string) {})
	if _, err := service.Control(actor, job.ID, "pause", RequestContext{}); err != nil {
		t.Fatal(err)
	}
	if err := service.RejectInterrupt(job.ID, "stale-token", "pause", "downloader_control_failed", "下载器未能暂停任务"); ErrorCode(err) != CodeQueueLeaseInvalid {
		t.Fatalf("stale lease=%v", err)
	}
	detail, err := service.Get(actor, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != models.JobStatusRunning || detail.InterruptPending != models.JobStatusPaused || !detail.CancellationRequested || detail.LastErrorCode != "" {
		t.Fatalf("stale rejection changed pending intent: %+v", detail)
	}
	if err := service.RejectInterrupt(job.ID, claimed.LeaseToken, "pause", "downloader_control_failed", "下载器未能暂停任务"); err != nil {
		t.Fatal(err)
	}
}

func TestQueueEventHubScopesOwnersAndThrottlesProgress(t *testing.T) {
	hub := NewQueueEventHub()
	owner := Actor{User: models.User{ID: 1}, Permissions: map[string]struct{}{authz.PermissionJobsReadOwn: {}}}
	other := Actor{User: models.User{ID: 2}, Permissions: map[string]struct{}{authz.PermissionJobsReadOwn: {}}}
	ownerEvents, stopOwner := hub.Subscribe(owner)
	defer stopOwner()
	otherEvents, stopOther := hub.Subscribe(other)
	defer stopOther()
	ownerID := uint(1)
	now := time.Now().UTC()
	hub.Publish(JobEvent{Type: "job.progress", JobID: "one", OwnerID: &ownerID, At: now})
	hub.Publish(JobEvent{Type: "job.progress", JobID: "one", OwnerID: &ownerID, At: now.Add(100 * time.Millisecond)})
	select {
	case <-ownerEvents:
	default:
		t.Fatal("owner did not receive event")
	}
	select {
	case event := <-ownerEvents:
		t.Fatalf("progress was not throttled: %+v", event)
	default:
	}
	select {
	case event := <-otherEvents:
		t.Fatalf("other owner received event: %+v", event)
	default:
	}
}

func TestQueueEventHubAllowsTransferReadersOnlyTransferEvents(t *testing.T) {
	hub := NewQueueEventHub()
	owner := Actor{User: models.User{ID: 1}, Permissions: map[string]struct{}{authz.PermissionTransfersReadOwn: {}}}
	all := Actor{User: models.User{ID: 2}, Permissions: map[string]struct{}{authz.PermissionTransfersReadAll: {}}}
	ownerEvents, stopOwner := hub.Subscribe(owner)
	defer stopOwner()
	allEvents, stopAll := hub.Subscribe(all)
	defer stopAll()
	ownerID := uint(1)
	hub.Publish(JobEvent{Type: "job.updated", JobID: "transfer-one", JobType: "transfer", OwnerID: &ownerID, At: time.Now().UTC()})
	hub.Publish(JobEvent{Type: "job.updated", JobID: "download-one", JobType: "download", OwnerID: &ownerID, At: time.Now().UTC()})
	select {
	case event := <-ownerEvents:
		if event.JobID != "transfer-one" {
			t.Fatalf("owner received wrong event: %+v", event)
		}
	default:
		t.Fatal("transfer owner did not receive transfer event")
	}
	select {
	case event := <-ownerEvents:
		t.Fatalf("transfer owner received unrelated job event: %+v", event)
	default:
	}
	select {
	case event := <-allEvents:
		if event.JobID != "transfer-one" {
			t.Fatalf("transfer reader received wrong event: %+v", event)
		}
	default:
		t.Fatal("all-transfer reader did not receive transfer event")
	}
	select {
	case event := <-allEvents:
		t.Fatalf("all-transfer reader received unrelated job event: %+v", event)
	default:
	}
}

func TestQueueResourceFairnessBeyondSixtyFourBlockedJobs(t *testing.T) {
	service, actor, _ := queueFixture(t)
	var policy models.QueuePolicy
	_ = service.db.First(&policy, "job_type = ?", "fake").Error
	_, _ = service.UpdatePolicy(actor, "fake", policy.Revision, 2, 1, 3, 10, RequestContext{})
	blocked := enqueueFake(t, service, actor, "Active A", "provider-a")
	claim, err := service.Claim([]string{"fake"})
	if err != nil || claim.Job.ID != blocked.ID {
		t.Fatal(err)
	}
	for i := 0; i < 65; i++ {
		enqueueFake(t, service, actor, fmt.Sprintf("Blocked %d", i), "provider-a")
	}
	runnable := enqueueFake(t, service, actor, "Runnable B", "provider-b")
	next, err := service.Claim([]string{"fake"})
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.Job.ID != runnable.ID {
		t.Fatalf("provider B starved: %+v", next)
	}
}
