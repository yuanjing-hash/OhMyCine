package services

import (
	"context"
	"fmt"
	"path/filepath"
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
	service.SetInterrupt(func(id string) { interrupted <- id })
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
