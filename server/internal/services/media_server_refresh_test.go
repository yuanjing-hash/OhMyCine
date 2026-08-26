package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

type mediaServerRefreshFixture struct {
	service     *MediaServerRefreshService
	connections *ConnectionService
	queue       *QueueService
	actor       Actor
	library     models.MediaLibrary
}

func newMediaServerRefreshFixture(t *testing.T) mediaServerRefreshFixture {
	t.Helper()
	db, _, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	for _, permission := range []string{
		authz.PermissionMediaLibrariesRead,
		authz.PermissionMediaLibrariesUpdate,
		authz.PermissionMediaServersRefresh,
	} {
		actor.Permissions[permission] = struct{}{}
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{
		Name: "Refresh Local", NameNormalized: "refresh-local", Type: models.StorageTypeLocal,
		RootPath: t.TempDir(), RootDisplayPath: "Refresh Local", RootPathNormalized: "refresh-local-root",
		Enabled: true, Capabilities: "{}", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{
		Name: "Refresh Library", NameNormalized: "refresh-library", StorageID: storage.ID,
		ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", Enabled: true,
		Recursive: true, VideoExtensionsJSON: `[".mkv"]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`,
		Status: models.MediaLibraryStatusListening, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	audit := NewAuditService(db)
	queue := NewQueueService(db, audit)
	return mediaServerRefreshFixture{
		service:     NewMediaServerRefreshService(db, queue, audit, connections),
		connections: connections,
		queue:       queue,
		actor:       actor,
		library:     library,
	}
}

func (f mediaServerRefreshFixture) createConnection(t *testing.T, endpoint string) uint {
	t.Helper()
	created, err := f.connections.Create(f.actor, ConnectionInput{
		Name:     "Media Server " + time.Now().UTC().Format("150405.000000000"),
		Provider: models.ConnectionProviderJellyfin, Endpoint: endpoint, APIKey: "private-management-key", Enabled: true,
	}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func (f mediaServerRefreshFixture) createTarget(t *testing.T, connectionID uint, desired uint64) models.MediaServerRefreshTarget {
	t.Helper()
	now := time.Now().UTC()
	target := models.MediaServerRefreshTarget{
		LibraryID: f.library.ID, ConnectionID: connectionID, UpstreamLibraryID: "stable-library-id-" + time.Now().UTC().Format("150405.000000000"),
		UpstreamLibraryName: "电影", Enabled: true, DesiredRevision: desired,
		LastStatus: "idle", Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := f.service.db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	return target
}

func runRefreshJob(t *testing.T, fixture mediaServerRefreshFixture) (*ClaimedJob, WorkerResult) {
	t.Helper()
	claimed, err := fixture.queue.Claim([]string{JobTypeMediaServerRefresh})
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("media-server refresh job was not claimable")
	}
	return claimed, NewMediaServerRefreshWorker(fixture.service).Run(context.Background(), nil, *claimed)
}

func TestManualRefreshAtContentRevisionZeroStillCallsUpstreamOnce(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "private-management-key" || r.Method != http.MethodPost {
			t.Fatalf("unexpected refresh request method=%s token=%q", r.Method, r.Header.Get("X-Emby-Token"))
		}
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	fixture := newMediaServerRefreshFixture(t)
	target := fixture.createTarget(t, fixture.createConnection(t, upstream.URL), 0)

	job, err := fixture.service.ManualRefresh(fixture.actor, target.ID, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(job.DisplayName, "private-management-key") {
		t.Fatal("job DTO leaked a credential")
	}
	claimed, result := runRefreshJob(t, fixture)
	if result.ErrorCode != "" || result.RetryAt != nil {
		t.Fatalf("worker result=%+v", result)
	}
	if err := fixture.queue.Complete(claimed.Job.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls=%d", calls.Load())
	}
	if err := fixture.service.db.First(&target, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if target.SuccessfulRevision != 0 || target.SuccessfulManualGeneration != 1 {
		t.Fatalf("target=%+v", target)
	}
	var persisted models.Job
	if err := fixture.service.db.First(&persisted, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.PayloadJSON != `{"target_id":`+uintID(target.ID)+`}` {
		t.Fatalf("unsafe or unexpected payload=%q", persisted.PayloadJSON)
	}
}

func TestRefreshWorkerClassifiesAuthenticationAndRateLimit(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		wantCode  string
		wantRetry bool
	}{
		{name: "authentication", status: http.StatusUnauthorized, wantCode: "media_server_unauthorized"},
		{name: "rate_limit", status: http.StatusTooManyRequests, wantCode: "media_server_rate_limited", wantRetry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "private upstream response", test.status)
			}))
			defer upstream.Close()
			fixture := newMediaServerRefreshFixture(t)
			target := fixture.createTarget(t, fixture.createConnection(t, upstream.URL), 1)
			if err := fixture.service.EnqueueTarget(target.ID); err != nil {
				t.Fatal(err)
			}
			_, result := runRefreshJob(t, fixture)
			if result.ErrorCode != test.wantCode || (result.RetryAt != nil) != test.wantRetry {
				t.Fatalf("result=%+v", result)
			}
			if strings.Contains(result.ErrorMessage, "private upstream response") {
				t.Fatal("worker result leaked upstream response")
			}
			if err := fixture.service.db.First(&target, target.ID).Error; err != nil {
				t.Fatal(err)
			}
			wantStatus := models.JobStatusFailed
			if test.wantRetry {
				wantStatus = models.JobStatusRetryWait
			}
			if target.LastStatus != wantStatus {
				t.Fatalf("target status=%q want=%q", target.LastStatus, wantStatus)
			}
			var run models.MediaServerRefreshRun
			if err := fixture.service.db.Where("target_id = ?", target.ID).Order("started_at DESC").First(&run).Error; err != nil {
				t.Fatal(err)
			}
			if run.Status != wantStatus {
				t.Fatalf("run status=%q want=%q", run.Status, wantStatus)
			}
		})
	}
}

func TestRefreshWorkerStopsTransientRetriesAtQueuePolicyLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private upstream response", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	fixture := newMediaServerRefreshFixture(t)
	if err := fixture.service.db.Model(&models.QueuePolicy{}).Where("job_type = ?", JobTypeMediaServerRefresh).Update("max_attempts", 1).Error; err != nil {
		t.Fatal(err)
	}
	target := fixture.createTarget(t, fixture.createConnection(t, upstream.URL), 1)
	if err := fixture.service.EnqueueTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	_, result := runRefreshJob(t, fixture)
	if result.ErrorCode != "media_server_unavailable" || result.RetryAt != nil {
		t.Fatalf("final transient result=%+v", result)
	}
	if err := fixture.service.db.First(&target, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if target.LastStatus != models.JobStatusFailed || target.LastErrorCode != "media_server_unavailable" {
		t.Fatalf("terminal target=%+v", target)
	}
	var run models.MediaServerRefreshRun
	if err := fixture.service.db.Where("target_id = ?", target.ID).Order("started_at DESC").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != models.JobStatusFailed {
		t.Fatalf("terminal run=%+v", run)
	}
}

func TestMediaServerRefreshRetryDelayIsBounded(t *testing.T) {
	for _, test := range []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: time.Minute},
		{attempt: 1, want: time.Minute},
		{attempt: 2, want: 2 * time.Minute},
		{attempt: 4, want: 8 * time.Minute},
		{attempt: 5, want: 15 * time.Minute},
		{attempt: 20, want: 15 * time.Minute},
	} {
		if got := mediaServerRefreshRetryDelay(test.attempt); got != test.want {
			t.Fatalf("attempt=%d delay=%s want=%s", test.attempt, got, test.want)
		}
	}
}

func TestRefreshFailureDoesNotBlockAnotherTarget(t *testing.T) {
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private failure", http.StatusUnauthorized)
	}))
	defer failed.Close()
	var successfulCalls atomic.Int32
	successful := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		successfulCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer successful.Close()
	fixture := newMediaServerRefreshFixture(t)
	first := fixture.createTarget(t, fixture.createConnection(t, failed.URL), 1)
	second := fixture.createTarget(t, fixture.createConnection(t, successful.URL), 1)
	if err := fixture.service.EnqueueTarget(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.EnqueueTarget(second.ID); err != nil {
		t.Fatal(err)
	}
	claimed, failedResult := runRefreshJob(t, fixture)
	if failedResult.ErrorCode != "media_server_unauthorized" {
		t.Fatalf("failed result=%+v", failedResult)
	}
	if err := fixture.queue.Fail(claimed.Job.ID, claimed.LeaseToken, failedResult.ErrorCode, failedResult.ErrorMessage); err != nil {
		t.Fatal(err)
	}
	claimed, successfulResult := runRefreshJob(t, fixture)
	if successfulResult.ErrorCode != "" {
		t.Fatalf("successful result=%+v", successfulResult)
	}
	if err := fixture.queue.Complete(claimed.Job.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if successfulCalls.Load() != 1 {
		t.Fatalf("successful target calls=%d", successfulCalls.Load())
	}
}

func TestRefreshGenerationAdvanceDuringWorkerRequeuesLatestRevision(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	fixture := newMediaServerRefreshFixture(t)
	target := fixture.createTarget(t, fixture.createConnection(t, upstream.URL), 1)
	if err := fixture.service.EnqueueTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.queue.Claim([]string{JobTypeMediaServerRefresh})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	resultChannel := make(chan WorkerResult, 1)
	go func() {
		resultChannel <- NewMediaServerRefreshWorker(fixture.service).Run(context.Background(), nil, *claimed)
	}()
	<-started
	if err := fixture.service.db.Model(&models.MediaServerRefreshTarget{}).Where("id = ?", target.ID).Update("desired_revision", 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.EnqueueTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	if result := <-resultChannel; result.ErrorCode != "" {
		t.Fatalf("first result=%+v", result)
	}
	if err := fixture.queue.Complete(claimed.Job.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	var job models.Job
	if err := fixture.service.db.First(&job, "id = ?", claimed.Job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != models.JobStatusQueued || job.Generation <= job.StartedGeneration {
		t.Fatalf("job=%+v", job)
	}
	if err := fixture.service.db.First(&target, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if target.SuccessfulRevision != 1 || target.DesiredRevision != 2 {
		t.Fatalf("target after first run=%+v", target)
	}
	secondClaim, secondResult := runRefreshJob(t, fixture)
	if secondResult.ErrorCode != "" {
		t.Fatalf("second result=%+v", secondResult)
	}
	if err := fixture.queue.Complete(secondClaim.Job.ID, secondClaim.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.db.First(&target, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || target.SuccessfulRevision != 2 {
		t.Fatalf("calls=%d target=%+v", calls.Load(), target)
	}
}

func TestRecoverPendingEnqueuesRevisionAndManualWork(t *testing.T) {
	fixture := newMediaServerRefreshFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer upstream.Close()
	connectionID := fixture.createConnection(t, upstream.URL)
	revisionTarget := fixture.createTarget(t, connectionID, 3)
	manualTarget := fixture.createTarget(t, connectionID, 0)
	if err := fixture.service.db.Model(&models.MediaServerRefreshTarget{}).Where("id = ?", manualTarget.ID).Update("manual_generation", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.RecoverPending(); err != nil {
		t.Fatal(err)
	}
	var jobs []models.Job
	if err := fixture.service.db.Where("job_type = ?", JobTypeMediaServerRefresh).Order("id").Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs=%+v revision_target=%d manual_target=%d", jobs, revisionTarget.ID, manualTarget.ID)
	}
}

func TestCreateUsesLatestReadyRevisionAndRedactsUpstreamIdentity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Library/VirtualFolders" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"ItemId":"stable-library-id","Name":"电影","CollectionType":"movies"}]`))
	}))
	defer upstream.Close()
	fixture := newMediaServerRefreshFixture(t)
	connectionID := fixture.createConnection(t, upstream.URL)
	now := time.Now().UTC()
	if err := fixture.service.db.Model(&models.MediaLibrary{}).Where("id = ?", fixture.library.ID).Update("content_revision", 7).Error; err != nil {
		t.Fatal(err)
	}
	changes := []models.MediaLibraryChange{
		{LibraryID: fixture.library.ID, Revision: 3, Kind: models.MediaLibraryChangeCatalog, State: models.MediaLibraryChangeReady, Generation: 3, ReadyAt: &now, CreatedAt: now},
		{LibraryID: fixture.library.ID, Revision: 7, Kind: models.MediaLibraryChangeCatalog, State: models.MediaLibraryChangePending, Generation: 7, CreatedAt: now},
	}
	if err := fixture.service.db.Create(&changes).Error; err != nil {
		t.Fatal(err)
	}
	target, err := fixture.service.Create(context.Background(), fixture.actor, MediaServerRefreshTargetInput{
		LibraryID: fixture.library.ID, ConnectionID: connectionID, UpstreamLibraryID: "stable-library-id", Enabled: false,
	}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if target.DesiredRevision != 3 {
		t.Fatalf("desired revision=%d", target.DesiredRevision)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "stable-library-id") || strings.Contains(string(encoded), "upstream_library_id") {
		t.Fatalf("target DTO leaked upstream identity: %s", encoded)
	}
}

func TestEnablingTargetCatchesUpReadyChangesCommittedWhileDisabled(t *testing.T) {
	fixture := newMediaServerRefreshFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer upstream.Close()
	target := fixture.createTarget(t, fixture.createConnection(t, upstream.URL), 0)
	if err := fixture.service.db.Model(&target).Updates(map[string]any{"enabled": false, "desired_revision": 0}).Error; err != nil {
		t.Fatal(err)
	}
	changes := NewMediaChangeService(fixture.service.db)
	if err := fixture.service.db.Transaction(func(tx *gorm.DB) error {
		_, err := changes.RecordTx(tx, fixture.library.ID, 1, models.MediaLibraryChangeCatalog, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.db.First(&target, target.ID).Error; err != nil || target.DesiredRevision != 0 {
		t.Fatalf("disabled target advanced desired revision: %+v err=%v", target, err)
	}
	updated, err := fixture.service.Update(fixture.actor, target.ID, MediaServerRefreshTargetInput{Enabled: true, Revision: target.Revision}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DesiredRevision != 1 || !updated.Enabled {
		t.Fatalf("updated target=%+v", updated)
	}
	var jobs int64
	if err := fixture.service.db.Model(&models.Job{}).Where("job_type = ?", JobTypeMediaServerRefresh).Count(&jobs).Error; err != nil || jobs != 1 {
		t.Fatalf("catch-up jobs=%d err=%v", jobs, err)
	}
}

func TestTargetTestVerifiesExactUpstreamLibraryWithoutLeakingIdentity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Library/VirtualFolders" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"ItemId":"private-library-id","Name":"家庭电影","CollectionType":"movies"}]`))
	}))
	defer upstream.Close()
	fixture := newMediaServerRefreshFixture(t)
	now := time.Now().UTC()
	target := models.MediaServerRefreshTarget{LibraryID: fixture.library.ID, ConnectionID: fixture.createConnection(t, upstream.URL), UpstreamLibraryID: "private-library-id", UpstreamLibraryName: "家庭电影", Enabled: false, LastStatus: "idle", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := fixture.service.db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	result, err := fixture.service.TestTarget(context.Background(), fixture.actor, target.ID, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if result.UpstreamLibraryName != "家庭电影" || result.ContentType != "movies" || result.CheckedAt.IsZero() {
		t.Fatalf("test result=%+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-library-id") {
		t.Fatalf("test result leaked upstream identity: %s", encoded)
	}
}

func TestFailedTargetRetryReusesOutstandingRevisionWithoutManualGeneration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private upstream error", http.StatusUnauthorized)
	}))
	defer upstream.Close()
	fixture := newMediaServerRefreshFixture(t)
	target := fixture.createTarget(t, fixture.createConnection(t, upstream.URL), 2)
	if err := fixture.service.EnqueueTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	claimed, result := runRefreshJob(t, fixture)
	if result.ErrorCode != "media_server_unauthorized" {
		t.Fatalf("worker result=%+v", result)
	}
	if err := fixture.queue.Fail(claimed.Job.ID, claimed.LeaseToken, result.ErrorCode, result.ErrorMessage); err != nil {
		t.Fatal(err)
	}
	fixture.service.EnqueueLibrary(target.LibraryID, target.DesiredRevision+1)
	if err := fixture.service.RecoverPending(); err != nil {
		t.Fatal(err)
	}
	var beforeRetry int64
	if err := fixture.service.db.Model(&models.Job{}).Where("job_type = ?", JobTypeMediaServerRefresh).Count(&beforeRetry).Error; err != nil || beforeRetry != 1 {
		t.Fatalf("terminal target was automatically requeued: jobs=%d err=%v", beforeRetry, err)
	}
	job, err := fixture.service.Retry(fixture.actor, target.ID, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != models.JobStatusQueued {
		t.Fatalf("retry job=%+v", job)
	}
	if err := fixture.service.db.First(&target, target.ID).Error; err != nil {
		t.Fatal(err)
	}
	if target.ManualGeneration != 0 || target.DesiredRevision != 2 || target.LastStatus != models.JobStatusQueued || target.LastErrorCode != "" {
		t.Fatalf("retried target=%+v", target)
	}
}

func TestConnectionDeleteReportsRefreshTargetReference(t *testing.T) {
	fixture := newMediaServerRefreshFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer upstream.Close()
	connectionID := fixture.createConnection(t, upstream.URL)
	fixture.createTarget(t, connectionID, 0)
	if err := fixture.connections.Delete(fixture.actor, connectionID, RequestContext{}); ErrorCode(err) != CodeConnectionInUse {
		t.Fatalf("refresh target reference delete code=%q err=%v", ErrorCode(err), err)
	}
}

func TestDeletedTargetTurnsQueuedRefreshIntoSafeNoop(t *testing.T) {
	fixture := newMediaServerRefreshFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("deleted target must not contact upstream")
	}))
	defer upstream.Close()
	target := fixture.createTarget(t, fixture.createConnection(t, upstream.URL), 1)
	if err := fixture.service.EnqueueTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.queue.Claim([]string{JobTypeMediaServerRefresh})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := fixture.service.db.Delete(&target).Error; err != nil {
		t.Fatal(err)
	}
	result := NewMediaServerRefreshWorker(fixture.service).Run(context.Background(), nil, *claimed)
	if result.ErrorCode != "" || result.RetryAt != nil {
		t.Fatalf("deleted target result=%+v", result)
	}
}
