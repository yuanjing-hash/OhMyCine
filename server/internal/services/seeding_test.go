package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

func seedingFixture(t *testing.T, mode string, cleanup bool) (*SeedingService, *QueueService, Actor, *stubDownloadClient, models.DownloadTask) {
	t.Helper()
	_, downloaders, queue, actor, client := downloadFixture(t)
	actor.Permissions[authz.PermissionJobsControlOwn] = struct{}{}
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "qBit seeding", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://127.0.0.1:8080", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	download := models.DownloadTask{ID: "download-" + mode, OwnerID: actor.User.ID, DownloaderID: &provider.ID, DownloaderName: provider.Name, ProviderType: provider.Type, ProviderTaskID: "provider-hash", SourceCiphertext: "encrypted", ProfileRulesJSON: "{}", TransferMode: mode, SeedingCleanupEnabled: cleanup, SeedingMinimumMinutes: 60, SeedingMinimumRatio: 1, SeedingCompletionMode: models.SeedingCompletionAll, DisplayName: "Example", Phase: models.DownloadTaskStatusCompleted, CreatedAt: now, UpdatedAt: now}
	_, err = queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: "original", Payload: map[string]any{"download_task_id": download.ID}}, func(tx *gorm.DB, job models.Job) error { download.JobID = job.ID; return tx.Create(&download).Error })
	if err != nil {
		t.Fatal(err)
	}
	manifest := downloadpkg.Manifest{Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	storage := models.Storage{Name: "Seeding target " + mode, NameNormalized: "seeding-target-" + mode, Type: models.StorageTypeLocal, RootPath: t.TempDir(), RootPathNormalized: "seeding-root-" + mode, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Seeding library " + mode, NameNormalized: "seeding-library-" + mode, StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", Enabled: false, VideoExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, Status: models.MediaLibraryStatusDisabled, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	transfer := models.TransferTask{ID: "transfer-" + mode, OwnerID: actor.User.ID, JobID: download.JobID, DownloadTaskID: download.ID, LibraryID: library.ID, LibraryName: library.Name, ManifestJSON: string(rawManifest), SourceManifestJSON: string(rawManifest), Phase: models.TransferTaskStatusCompleted, CleanupStatus: models.TransferCleanupDeferred, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	return NewSeedingService(queue.db, queue.audit, queue, downloaders, zerolog.Nop()), queue, actor, client, download
}

func TestSeedingWorkerUsesModeSpecificCleanup(t *testing.T) {
	for _, test := range []struct {
		mode       string
		deleteData bool
	}{{models.MediaLibraryTransferCopy, true}, {models.MediaLibraryTransferSymlink, false}} {
		t.Run(test.mode, func(t *testing.T) {
			service, queue, _, client, download := seedingFixture(t, test.mode, true)
			cleanupCalls := 0
			service.SetStagingCleanup(func(_ context.Context, downloadTaskID string, deleteData bool) error {
				cleanupCalls++
				if downloadTaskID != download.ID || deleteData != test.deleteData {
					t.Fatalf("staging cleanup download=%s deleteData=%v", downloadTaskID, deleteData)
				}
				return nil
			})
			ratio, seeded, uploaded := 1.5, int64(7200), int64(1024)
			client.seedTask = &downloadpkg.Task{ID: "provider-hash", Status: "uploading", Ratio: &ratio, SeededSeconds: &seeded, UploadedBytes: &uploaded, Seeding: true}
			if err := service.Enqueue(download); err != nil {
				t.Fatal(err)
			}
			claimed, err := queue.Claim([]string{"seeding"})
			if err != nil || claimed == nil {
				t.Fatalf("claim=%v err=%v", claimed, err)
			}
			result := NewSeedingWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
			if result.ErrorCode != "" || result.RetryAt != nil {
				t.Fatalf("result=%+v", result)
			}
			if !client.cancelled || client.deleteData != test.deleteData {
				t.Fatalf("cancelled=%v deleteData=%v", client.cancelled, client.deleteData)
			}
			if cleanupCalls != 1 {
				t.Fatalf("staging cleanup calls=%d", cleanupCalls)
			}
			var task models.SeedingTask
			if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
				t.Fatal(err)
			}
			if task.Phase != models.SeedingTaskStatusCompleted || task.Ratio == nil || *task.Ratio != ratio {
				t.Fatalf("task=%+v", task)
			}
		})
	}
}

func TestSeedingCopyAvoidsWholePackageDeletionWhenProtectedFilesRemain(t *testing.T) {
	service, queue, _, client, download := seedingFixture(t, models.MediaLibraryTransferCopy, true)
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	selected := downloadpkg.Manifest{Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	source := selected
	source.Files = append(append([]downloadpkg.File{}, selected.Files...),
		downloadpkg.File{RelativePath: "Possible.Real.Movie.mp4", Size: minimumAutomaticTransferVideoBytes},
		downloadpkg.File{RelativePath: "unmatched.zh-CN.srt", Size: 128},
		downloadpkg.File{RelativePath: "tracker.txt", Size: 32},
	)
	selectedJSON, _ := json.Marshal(selected)
	sourceJSON, _ := json.Marshal(source)
	if err := queue.db.Model(&transfer).Updates(map[string]any{"manifest_json": string(selectedJSON), "source_manifest_json": string(sourceJSON)}).Error; err != nil {
		t.Fatal(err)
	}
	cleanupCalls := 0
	service.SetStagingCleanup(func(_ context.Context, downloadTaskID string, deleteData bool) error {
		cleanupCalls++
		if downloadTaskID != download.ID || deleteData {
			t.Fatalf("unsafe staging cleanup download=%s deleteData=%v", downloadTaskID, deleteData)
		}
		return nil
	})
	ratio, seeded := 2.0, int64(7200)
	client.seedTask = &downloadpkg.Task{ID: "provider-hash", Ratio: &ratio, SeededSeconds: &seeded, Seeding: true}
	if err := service.Enqueue(download); err != nil {
		t.Fatal(err)
	}
	var task models.SeedingTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.DeleteData {
		t.Fatal("copy seeding task allowed whole-package deletion with protected leftovers")
	}
	// Simulate a task persisted by an older Server version. The destructive
	// boundary must still downgrade it before contacting qBittorrent.
	if err := queue.db.Model(&task).Update("delete_data", true).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"seeding"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	result := NewSeedingWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.RetryAt != nil {
		t.Fatalf("result=%+v", result)
	}
	if !client.cancelled || client.deleteData || cleanupCalls != 1 {
		t.Fatalf("cancelled=%v deleteData=%v cleanupCalls=%d", client.cancelled, client.deleteData, cleanupCalls)
	}
}

func TestSeedingDisabledRetainsProviderAndMoveRemovesOnlyTask(t *testing.T) {
	service, queue, actor, client, download := seedingFixture(t, models.MediaLibraryTransferCopy, false)
	ratio, seeded := .5, int64(300)
	client.seedTask = &downloadpkg.Task{ID: "provider-hash", Ratio: &ratio, SeededSeconds: &seeded, Seeding: true}
	if err := service.Enqueue(download); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"seeding"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	result := NewSeedingWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.RetryAt == nil || client.cancelled {
		t.Fatalf("result=%+v cancelled=%v", result, client.cancelled)
	}
	var retained models.SeedingTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&retained).Error; err != nil {
		t.Fatal(err)
	}
	if retained.Phase != models.SeedingTaskStatusRetained {
		t.Fatalf("phase=%s", retained.Phase)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", download.JobID).Updates(map[string]any{"status": models.JobStatusCompleted, "finished_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.downloaders.Delete(actor, *download.DownloaderID, RequestContext{}); ErrorCode(err) != CodeDownloaderInUse {
		t.Fatalf("delete active seeding downloader err=%v", err)
	}

	moveService, moveQueue, _, moveClient, moved := seedingFixture(t, models.MediaLibraryTransferMove, true)
	if err := moveService.AfterTransfer(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	if !moveClient.cancelled || moveClient.deleteData {
		t.Fatalf("move cleanup cancelled=%v deleteData=%v", moveClient.cancelled, moveClient.deleteData)
	}
	var count int64
	if err := moveQueue.db.Model(&models.SeedingTask{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	missingDownloader := "removed-downloader"
	moved.DownloaderID = &missingDownloader
	if err := moveService.AfterTransfer(context.Background(), moved); err != nil {
		t.Fatalf("completed move was blocked by removed downloader config: %v", err)
	}
}

func TestSeedingProviderTaskNotFoundCompletesIdempotently(t *testing.T) {
	service, queue, _, client, download := seedingFixture(t, models.MediaLibraryTransferCopy, true)
	client.getErr = downloadpkg.Error("downloader_task_not_found", false, errors.New("gone"))
	if err := service.Enqueue(download); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"seeding"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	result := NewSeedingWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
	var task models.SeedingTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if task.Phase != models.SeedingTaskStatusCompleted {
		t.Fatalf("phase=%s", task.Phase)
	}
}

func TestQueuedSeedingCancellationReconcilesProviderBeforeAcknowledgement(t *testing.T) {
	service, queue, actor, client, download := seedingFixture(t, models.MediaLibraryTransferCopy, true)
	cleanupCalls := 0
	service.SetStagingCleanup(func(_ context.Context, downloadTaskID string, deleteData bool) error {
		cleanupCalls++
		if downloadTaskID != download.ID || !deleteData {
			t.Fatalf("staging cleanup download=%s deleteData=%v", downloadTaskID, deleteData)
		}
		return nil
	})
	if err := service.Enqueue(download); err != nil {
		t.Fatal(err)
	}
	var task models.SeedingTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	controlled, err := queue.Control(actor, task.JobID, "cancel", RequestContext{})
	if err != nil || controlled.Status != models.JobStatusQueued || controlled.InterruptPending != models.JobStatusCancelled {
		t.Fatalf("controlled=%+v err=%v", controlled, err)
	}
	claimed, err := queue.Claim([]string{"seeding"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	worker := NewSeedingWorker(service)
	if err := worker.Interrupt(context.Background(), *claimed, "cancel"); err != nil {
		t.Fatal(err)
	}
	if !client.cancelled || !client.deleteData {
		t.Fatalf("provider cleanup cancelled=%v deleteData=%v", client.cancelled, client.deleteData)
	}
	if cleanupCalls != 1 {
		t.Fatalf("staging cleanup calls=%d", cleanupCalls)
	}
	if err := queue.AcknowledgeInterrupt(claimed.Job.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	var job models.Job
	if err := queue.db.First(&job, "id = ?", task.JobID).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != models.JobStatusCancelled || task.Phase != models.SeedingTaskStatusCompleted {
		t.Fatalf("job=%s task=%s", job.Status, task.Phase)
	}
}
