package services

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

type retargetImportFixture struct {
	downloads  *DownloadService
	queue      *QueueService
	actor      Actor
	provider   *stubDownloadClient
	task       models.DownloadTask
	transfer   models.TransferTask
	oldLibrary models.MediaLibrary
	newLibrary models.MediaLibrary
}

func newRetargetImportFixture(t *testing.T) retargetImportFixture {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/movie/346" {
			http.NotFound(writer, request)
			return
		}
		_, _ = io.WriteString(writer, `{"id":346,"title":"七武士","original_title":"Seven Samurai","original_language":"ja","release_date":"1954-04-26","genres":[{"id":18,"name":"剧情"}],"production_countries":[{"iso_3166_1":"JP"}]}`)
	}))
	t.Cleanup(upstream.Close)

	downloads, downloaders, queue, actor, provider := downloadFixture(t)
	metadata := NewMetadataSettingsService(queue.db, queue.audit, downloads.credentials, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", upstream.URL, upstream.Client())
	}
	downloads.SetMetadataSettings(metadata)

	createStorage := func(name, root string) models.Storage {
		storage := models.Storage{Name: name, NameNormalized: strings.ToLower(name), Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`}
		if err := queue.db.Create(&storage).Error; err != nil {
			t.Fatal(err)
		}
		return storage
	}
	staging := createStorage("Retarget staging", t.TempDir())
	oldStorage := createStorage("Retarget old", t.TempDir())
	newStorage := createStorage("Retarget new", t.TempDir())
	configureDownloadStaging(t, queue, staging.ID)

	providerConfig, err := downloaders.Create(actor, DownloaderInput{Name: "Retarget qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	createLibrary := func(name string, storageID uint, transferMode string) models.MediaLibrary {
		library := models.MediaLibrary{
			Name: name, NameNormalized: strings.ToLower(name), StorageID: storageID, ProfileID: profile.ID, ProfileRevision: profile.Revision,
			RelativeRoot: "/", TransferMode: transferMode, ConflictPolicy: models.MediaLibraryConflictRename,
			Enabled: true, Recursive: true, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if err := queue.db.Create(&library).Error; err != nil {
			t.Fatal(err)
		}
		return library
	}
	oldLibrary := createLibrary("Retarget old library", oldStorage.ID, models.MediaLibraryTransferMove)
	newLibrary := createLibrary("Retarget new library", newStorage.ID, models.MediaLibraryTransferCopy)
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{
		DownloaderID: providerConfig.ID, MediaLibraryID: &oldLibrary.ID, DisplayName: "Seven Samurai",
		Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:retarget"},
	}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	confidence, tmdbID, year := 1.0, int64(346), 1954
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Updates(map[string]any{
		"phase": models.DownloadTaskStatusCompleted, "scrape_status": "completed_verified", "scrape_title": "七武士",
		"scrape_media_type": "movie", "scrape_category": "外语电影", "scrape_tmdb_id": tmdbID,
		"scrape_confidence": confidence, "scrape_year": year,
	}).Error; err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := queue.db.First(&task, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	manifest := downloadpkg.Manifest{Name: "Seven.Samurai.1954", Complete: true, Files: []downloadpkg.File{{RelativePath: "Seven.Samurai.1954.mkv", Size: 2 * 1024 * 1024 * 1024}}}
	transferService := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	if err := transferService.Enqueue(task, manifest); err != nil {
		t.Fatal(err)
	}
	var transfer models.TransferTask
	if err := queue.db.First(&transfer, "download_task_id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", task.JobID).Updates(map[string]any{"status": models.JobStatusCompleted, "finished_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", transfer.JobID).Updates(map[string]any{"status": models.JobStatusFailed, "last_error_code": CodeTransferRouteUnsupported, "finished_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&transfer).Updates(map[string]any{"phase": models.TransferTaskStatusFailed, "last_error_code": CodeTransferRouteUnsupported, "finished_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.First(&transfer, "id = ?", transfer.ID).Error; err != nil {
		t.Fatal(err)
	}
	return retargetImportFixture{downloads: downloads, queue: queue, actor: actor, provider: provider, task: task, transfer: transfer, oldLibrary: oldLibrary, newLibrary: newLibrary}
}

func TestRetargetCompletedImportQueuesOnlyTransferAndNeverTouchesDownloader(t *testing.T) {
	fixture := newRetargetImportFixture(t)
	result, err := fixture.downloads.RetargetCompletedImport(context.Background(), fixture.actor, fixture.task.ID, fixture.newLibrary.ID, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if result.JobStatus != models.JobStatusQueued || result.TargetLibraryID == nil || *result.TargetLibraryID != fixture.newLibrary.ID || result.TransferMode != models.MediaLibraryTransferCopy {
		t.Fatalf("result=%+v", result)
	}
	fixture.provider.mu.Lock()
	gets, submits, paused, resumed, cancelled := fixture.provider.gets, fixture.provider.submits, fixture.provider.paused, fixture.provider.resumed, fixture.provider.cancelled
	fixture.provider.mu.Unlock()
	if gets != 0 || submits != 0 || paused || resumed || cancelled {
		t.Fatalf("retarget touched downloader: gets=%d submits=%d paused=%v resumed=%v cancelled=%v", gets, submits, paused, resumed, cancelled)
	}
	var task models.DownloadTask
	if err := fixture.queue.db.First(&task, "id = ?", fixture.task.ID).Error; err != nil {
		t.Fatal(err)
	}
	var transfer models.TransferTask
	if err := fixture.queue.db.First(&transfer, "id = ?", fixture.transfer.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.TargetLibraryID == nil || *task.TargetLibraryID != fixture.newLibrary.ID || transfer.LibraryID != fixture.newLibrary.ID || transfer.Phase != models.TransferTaskStatusQueued {
		t.Fatalf("task=%+v transfer=%+v", task, transfer)
	}
	var queued []models.Job
	if err := fixture.queue.db.Where("status = ?", models.JobStatusQueued).Find(&queued).Error; err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].ID != transfer.JobID || queued[0].JobType != "transfer" {
		t.Fatalf("queued jobs=%+v", queued)
	}
}

func TestRetargetCompletedImportRejectsWithoutMutatingTarget(t *testing.T) {
	tests := []struct {
		name     string
		wantCode string
		mutate   func(t *testing.T, fixture *retargetImportFixture) Actor
	}{
		{
			name: "permission", wantCode: CodePermissionDenied,
			mutate: func(_ *testing.T, fixture *retargetImportFixture) Actor {
				return Actor{User: models.User{ID: fixture.actor.User.ID + 1}, Permissions: map[string]struct{}{authz.PermissionJobsControlOwn: {}}}
			},
		},
		{
			name: "state", wantCode: CodeQueueStateConflict,
			mutate: func(t *testing.T, fixture *retargetImportFixture) Actor {
				if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", fixture.transfer.JobID).Update("status", models.JobStatusRunning).Error; err != nil {
					t.Fatal(err)
				}
				return fixture.actor
			},
		},
		{
			name: "partial write", wantCode: CodeQueueStateConflict,
			mutate: func(t *testing.T, fixture *retargetImportFixture) Actor {
				if err := fixture.queue.db.Model(&models.TransferTask{}).Where("id = ?", fixture.transfer.ID).Update("processed_files", 1).Error; err != nil {
					t.Fatal(err)
				}
				return fixture.actor
			},
		},
		{
			name: "unsupported target", wantCode: CodeMediaLibraryStorageUnavailable,
			mutate: func(t *testing.T, fixture *retargetImportFixture) Actor {
				if err := fixture.queue.db.Model(&models.Downloader{}).Where("id = ?", *fixture.task.DownloaderID).Update("type", models.DownloaderTypePan115Offline).Error; err != nil {
					t.Fatal(err)
				}
				return fixture.actor
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRetargetImportFixture(t)
			actor := test.mutate(t, &fixture)
			_, err := fixture.downloads.RetargetCompletedImport(context.Background(), actor, fixture.task.ID, fixture.newLibrary.ID, RequestContext{})
			if ErrorCode(err) != test.wantCode {
				t.Fatalf("err=%v code=%s want=%s", err, ErrorCode(err), test.wantCode)
			}
			var task models.DownloadTask
			if dbErr := fixture.queue.db.First(&task, "id = ?", fixture.task.ID).Error; dbErr != nil {
				t.Fatal(dbErr)
			}
			var transfer models.TransferTask
			if dbErr := fixture.queue.db.First(&transfer, "id = ?", fixture.transfer.ID).Error; dbErr != nil {
				t.Fatal(dbErr)
			}
			if task.TargetLibraryID == nil || *task.TargetLibraryID != fixture.oldLibrary.ID || transfer.LibraryID != fixture.oldLibrary.ID || transfer.Phase != models.TransferTaskStatusFailed {
				t.Fatalf("rejection mutated target: task=%+v transfer=%+v", task, transfer)
			}
		})
	}
}
