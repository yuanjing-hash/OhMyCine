package services

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

func TestPan115RecycleCleanupRequiresPasswordCronAndConfirmation(t *testing.T) {
	_, _, service, actor := newConnectionTestService(t, &fakeCloudDriver{})
	base := ConnectionInput{Name: "cleanup", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true, RecycleCleanupEnabled: true, RecycleCleanupCron: defaultRecycleCleanupCron}
	if _, err := service.Create(actor, base, RequestContext{}); err == nil {
		t.Fatal("enabled cleanup without password and confirmation was accepted")
	}
	base.RecyclePassword, base.RecycleCleanupConfirmed = "safe-code", true
	base.RecycleCleanupCron = "bad cron"
	if _, err := service.Create(actor, base, RequestContext{}); err == nil {
		t.Fatal("invalid five-field cron was accepted")
	}
	base.RecycleCleanupCron = defaultRecycleCleanupCron
	created, err := service.Create(actor, base, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !created.RecycleCleanupEnabled || created.RecycleCleanupNextRunAt == nil || !created.RecyclePasswordConfigured {
		t.Fatalf("summary=%+v", created)
	}
	remove := true
	if _, err := service.Update(actor, created.ID, UpdateConnectionInput{RemoveRecyclePassword: &remove, Revision: created.Revision}, RequestContext{}); err == nil {
		t.Fatal("password removal while cleanup enabled was accepted")
	}
}

func TestPan115RecycleCleanupPollCoalescesAndWorkerClearsWithoutSecrets(t *testing.T) {
	driver := &fakeCloudDriver{}
	db, _, connections, actor := newConnectionTestService(t, driver)
	created, err := connections.Create(actor, ConnectionInput{Name: "scheduled-cleanup", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, RecyclePassword: "safe-code", Enabled: true, RecycleCleanupEnabled: true, RecycleCleanupCron: "*/5 * * * *", RecycleCleanupConfirmed: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Add(-time.Minute)
	if err := db.Model(&models.Connection{}).Where("id = ?", created.ID).Update("recycle_cleanup_next_run_at", due).Error; err != nil {
		t.Fatal(err)
	}
	queue := NewQueueService(db, NewAuditService(db))
	service := NewPan115RecycleCleanupService(db, queue, NewAuditService(db), connections, zerolog.Nop())
	if err := service.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	var jobs []models.Job
	if err := db.Where("job_type = ?", JobTypePan115RecycleCleanup).Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs=%d", len(jobs))
	}
	if jobs[0].Revision != 1 || jobs[0].Generation != 1 {
		t.Fatalf("duplicate poll mutated active job: revision=%d generation=%d", jobs[0].Revision, jobs[0].Generation)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(jobs[0].PayloadJSON), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 2 || raw["connection_id"] == nil || raw["revision"] == nil {
		t.Fatalf("payload=%v", raw)
	}
	claimed, err := queue.Claim([]string{JobTypePan115RecycleCleanup})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if err := service.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	var running models.Job
	if err := db.First(&running, "id = ?", claimed.Job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if running.Status != models.JobStatusRunning || running.Revision != claimed.Job.Revision || running.Generation != claimed.Job.Generation || running.LeaseTokenHash != claimed.Job.LeaseTokenHash {
		t.Fatalf("poll mutated running job: before=%+v after=%+v", claimed.Job, running)
	}
	result := service.run(context.Background(), nil, *claimed)
	if result.ErrorCode != "" || driver.clearRecycleCalls != 1 {
		t.Fatalf("result=%+v calls=%d", result, driver.clearRecycleCalls)
	}
	var updated models.Connection
	if err := db.First(&updated, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.RecycleCleanupLastStatus != models.RecycleCleanupStatusSucceeded || updated.RecycleCleanupLastRunAt == nil || updated.RecycleCleanupNextRunAt == nil {
		t.Fatalf("updated=%+v", updated)
	}
}

func TestPan115RecycleCleanupWorkerRejectsStaleRevision(t *testing.T) {
	driver := &fakeCloudDriver{}
	db, _, connections, actor := newConnectionTestService(t, driver)
	created, err := connections.Create(actor, ConnectionInput{Name: "stale-cleanup", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, RecyclePassword: "safe-code", Enabled: true, RecycleCleanupEnabled: true, RecycleCleanupCron: defaultRecycleCleanupCron, RecycleCleanupConfirmed: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	queue := NewQueueService(db, NewAuditService(db))
	service := NewPan115RecycleCleanupService(db, queue, NewAuditService(db), connections, zerolog.Nop())
	job, err := queue.Enqueue(EnqueueJobInput{System: true, JobType: JobTypePan115RecycleCleanup, DisplayName: "stale", ResourceKey: "connection:test", Payload: pan115RecycleCleanupJobPayload{ConnectionID: created.ID, Revision: created.Revision}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Connection{}).Where("id = ?", created.ID).Update("revision", created.Revision+1).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{JobTypePan115RecycleCleanup})
	if err != nil || claimed == nil || claimed.Job.ID != job.ID {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	result := service.run(context.Background(), nil, *claimed)
	if result.ErrorCode != "" || driver.clearRecycleCalls != 0 {
		t.Fatalf("result=%+v calls=%d", result, driver.clearRecycleCalls)
	}
}

func TestPan115RecycleCleanupConcurrentPollDoesNotMutateActiveJob(t *testing.T) {
	db, _, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	created, err := connections.Create(actor, ConnectionInput{Name: "concurrent-cleanup", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, RecyclePassword: "safe-code", Enabled: true, RecycleCleanupEnabled: true, RecycleCleanupCron: defaultRecycleCleanupCron, RecycleCleanupConfirmed: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Connection{}).Where("id = ?", created.ID).Update("recycle_cleanup_next_run_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	service := NewPan115RecycleCleanupService(db, NewQueueService(db, NewAuditService(db)), NewAuditService(db), connections, zerolog.Nop())
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- service.Poll(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var jobs []models.Job
	if err := db.Where("job_type = ?", JobTypePan115RecycleCleanup).Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Revision != 1 || jobs[0].Generation != 1 {
		t.Fatalf("jobs=%+v", jobs)
	}
}
