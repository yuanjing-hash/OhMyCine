package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

type fakeUploadCloudDriver struct {
	*fakeMutationCloudDriver
	uploadCalls int
	uploadErr   error
	uploaded    []cloudpkg.Item
}

func newFakeUploadCloudDriver() *fakeUploadCloudDriver {
	return &fakeUploadCloudDriver{fakeMutationCloudDriver: newFakeMutationCloudDriver()}
}

func (f *fakeUploadCloudDriver) Capabilities() cloudpkg.Capabilities {
	capabilities := f.fakeMutationCloudDriver.Capabilities()
	capabilities.FileUpload = true
	return capabilities
}

func (f *fakeUploadCloudDriver) Upload(_ context.Context, request cloudpkg.UploadRequest) (cloudpkg.Item, error) {
	f.uploadCalls++
	if f.uploadErr != nil {
		return cloudpkg.Item{}, f.uploadErr
	}
	body, err := io.ReadAll(request.Reader)
	if err != nil || int64(len(body)) != request.Size {
		return cloudpkg.Item{}, cloudpkg.Error(cloudpkg.CodeResponseInvalid, false, errors.New("upload fixture size mismatch"))
	}
	f.nextID++
	item := cloudpkg.Item{ID: "uploaded-" + uuid.NewString(), ParentID: request.ParentID, Name: request.Name, Size: request.Size}
	f.items[item.ID] = item
	f.uploaded = append(f.uploaded, item)
	return item, nil
}

type uploadTransferFixture struct {
	queue       *QueueService
	service     *TransferService
	driver      *fakeUploadCloudDriver
	download    models.DownloadTask
	manifest    downloadpkg.Manifest
	library     models.MediaLibrary
	source      string
	targetName  string
	targetDirID string
}

func newUploadTransferFixture(t *testing.T, policy string, conflict bool) uploadTransferFixture {
	t.Helper()
	queue, actor, _ := queueFixture(t)
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	connection := models.Connection{Name: "115", NameNormalized: "115-plugin-upload-" + uuid.NewString(), Provider: cloudpkg.ProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	driver := newFakeUploadCloudDriver()
	for _, item := range []cloudpkg.Item{
		{ID: "0", Name: "root", IsDir: true},
		{ID: "target-storage-root", ParentID: "0", Name: "media", IsDir: true},
		{ID: "library-root", ParentID: "target-storage-root", Name: "library", IsDir: true},
		{ID: "category-dir", ParentID: "library-root", Name: "电影", IsDir: true},
	} {
		driver.items[item.ID] = item
	}
	storage := models.Storage{Name: "115 Media", NameNormalized: "115-plugin-media-" + uuid.NewString(), Type: models.StorageTypePan115, RootPath: "target-storage-root", RootDisplayPath: "/media", RootPathNormalized: "pan115:target-storage-root", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "115 Plugin Movies", NameNormalized: "115-plugin-movies-" + uuid.NewString(), StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/library", ProviderRootID: "library-root", SortOrder: 1, TransferMode: models.MediaLibraryTransferCopy, ConflictPolicy: policy, MovieDirectoryTemplate: "{category}", MovieFilenameTemplate: "{title} ({year})", TVDirectoryTemplate: "{category}/{title} ({year})/Season {season:02}", TVFilenameTemplate: "{title} - S{season:02}E{episode:02}", Enabled: true, Recursive: true, FullScanIntervalHours: 24, IncrementalMinutes: 15, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, MetadataLanguage: "zh-CN", MetadataRegion: "CN", MatchStrategy: "balanced", ProviderRatePerSecond: 1, ProviderConcurrency: 1, MetadataRatePerSecond: 1, MetadataConcurrency: 1, Status: models.MediaLibraryStatusListening, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}

	downloadID := uuid.NewString()
	staging := t.TempDir()
	taskRoot := filepath.Join(staging, pluginDownloadRootName, downloadID)
	if err := os.MkdirAll(taskRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(taskRoot, "Movie.2024.mkv")
	body := []byte("provider video fixture")
	if err := os.WriteFile(source, body, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(providerMetadataEnvelope{
		Version: 1, PluginID: "org.ohmycine.fixture", PluginVersion: "1.0.0", ConnectionID: "plugin-connection",
		Snapshot: contract.ProviderMetadataSnapshot{Version: 1, WorkID: "work", SegmentID: "segment", Kind: "movie", Title: "Movie", PublishedAt: "2024-01-01T00:00:00Z", UniqueIDs: map[string]string{"fixture": "work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	year, confidence := 2024, 1.0
	download := models.DownloadTask{ID: downloadID, OwnerID: actor.User.ID, DownloaderName: "Plugin", ProviderType: models.DownloaderTypePluginHTTP, StagingAbsolutePath: staging, PluginID: "org.ohmycine.fixture", PluginVersion: "1.0.0", PluginConnectionID: "plugin-connection", ProviderMetadataJSON: string(metadata), ProfileID: profile.ID, ProfileRevision: profile.Revision, ProfileRulesJSON: profile.RulesJSON, TargetLibraryID: &library.ID, TargetLibraryName: library.Name, TargetStorageID: &storage.ID, TargetStorageType: models.StorageTypePan115, TargetConnectionID: &connection.ID, TargetProviderRootID: "library-root", TransferMode: models.MediaLibraryTransferCopy, ConflictPolicy: policy, MovieDirectoryTemplate: library.MovieDirectoryTemplate, MovieFilenameTemplate: library.MovieFilenameTemplate, TVDirectoryTemplate: library.TVDirectoryTemplate, TVFilenameTemplate: library.TVFilenameTemplate, DisplayName: "Movie", Phase: models.DownloadTaskStatusCompleted, ScrapeStatus: "completed_verified", ScrapeTitle: "Movie", ScrapeMediaType: "movie", ScrapeCategory: "电影", ScrapeConfidence: &confidence, ScrapeYear: &year, ManifestFileCount: 1, CreatedAt: now, UpdatedAt: now}
	_, err = queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: "Movie", Payload: downloadJobPayload{DownloadTaskID: download.ID}}, func(tx *gorm.DB, job models.Job) error {
		download.JobID = job.ID
		return tx.Create(&download).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: int64(len(body))}}}
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	service.connections = &ConnectionService{db: queue.db, drivers: map[uint]cloudpkg.Driver{connection.ID: driver}}
	if conflict {
		driver.items["existing-video"] = cloudpkg.Item{ID: "existing-video", ParentID: "category-dir", Name: "Movie (2024).mkv", Size: 7}
	}
	return uploadTransferFixture{queue: queue, service: service, driver: driver, download: download, manifest: manifest, library: library, source: source, targetName: "Movie (2024).mkv", targetDirID: "category-dir"}
}

func (f uploadTransferFixture) enqueueAndRun(t *testing.T) WorkerResult {
	t.Helper()
	if err := f.service.EnqueuePackage(f.download, f.manifest, f.manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := f.queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	return NewTransferWorker(f.service).Run(context.Background(), workerRuntime{queue: f.queue, job: *claimed}, *claimed)
}

func TestPluginCloudUploadCompletesAndPersistsReconciledState(t *testing.T) {
	fixture := newUploadTransferFixture(t, models.MediaLibraryConflictOverwrite, false)
	result := fixture.enqueueAndRun(t)
	if result.ErrorCode != "" || result.Wait != nil || result.RetryAt != nil {
		t.Fatalf("result=%+v", result)
	}
	if fixture.driver.uploadCalls != 1 || len(fixture.driver.uploaded) != 1 {
		t.Fatalf("upload calls=%d items=%+v", fixture.driver.uploadCalls, fixture.driver.uploaded)
	}
	uploaded := fixture.driver.uploaded[0]
	if uploaded.ParentID != fixture.targetDirID || uploaded.Name != fixture.targetName || uploaded.Size != fixture.manifest.Files[0].Size {
		t.Fatalf("uploaded=%+v", uploaded)
	}
	var transfer models.TransferTask
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	state, err := decodeCloudTransferState(transfer.CloudStateJSON)
	if err != nil {
		t.Fatal(err)
	}
	item := state.Items[normalizedManifestPath(fixture.manifest.Files[0].RelativePath)]
	if transfer.Phase != models.TransferTaskStatusCompleted || transfer.ProcessedFiles != 1 || item.Status != "completed" || item.CurrentID != uploaded.ID || item.TargetParentID != fixture.targetDirID || item.TargetName != fixture.targetName {
		t.Fatalf("transfer=%+v state=%+v", transfer, state)
	}
	if transfer.CleanupStatus != models.TransferCleanupCompleted {
		t.Fatalf("cleanup status=%q", transfer.CleanupStatus)
	}
	if _, err := os.Stat(filepath.Dir(fixture.source)); !os.IsNotExist(err) {
		t.Fatalf("managed staging root was not cleaned: %v", err)
	}
	var library models.MediaLibrary
	if err := fixture.queue.db.First(&library, fixture.library.ID).Error; err != nil || library.DirtyGeneration != fixture.library.DirtyGeneration+1 {
		t.Fatalf("library=%+v err=%v", library, err)
	}
}

func TestPluginCloudUploadResumesUploadingStateByExactTargetWithoutDuplicate(t *testing.T) {
	fixture := newUploadTransferFixture(t, models.MediaLibraryConflictOverwrite, false)
	if err := fixture.service.EnqueuePackage(fixture.download, fixture.manifest, fixture.manifest); err != nil {
		t.Fatal(err)
	}
	var transfer models.TransferTask
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	key := normalizedManifestPath(fixture.manifest.Files[0].RelativePath)
	state := cloudTransferState{Version: cloudTransferStateVersion, Directories: map[string]string{".": "library-root", "电影": fixture.targetDirID}, Items: map[string]cloudTransferItemState{key: {SourceID: key, TargetParentID: fixture.targetDirID, TargetName: fixture.targetName, Status: "uploading"}}}
	encoded, err := encodeCloudTransferState(state)
	if err != nil {
		t.Fatal(err)
	}
	fixture.driver.items["already-uploaded"] = cloudpkg.Item{ID: "already-uploaded", ParentID: fixture.targetDirID, Name: fixture.targetName, Size: fixture.manifest.Files[0].Size}
	if err := fixture.queue.db.Model(&transfer).Updates(map[string]any{"cloud_state_json": encoded, "phase": models.TransferTaskStatusTransferring}).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(fixture.service).Run(context.Background(), workerRuntime{queue: fixture.queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.RetryAt != nil || result.Wait != nil {
		t.Fatalf("result=%+v", result)
	}
	if fixture.driver.uploadCalls != 0 {
		t.Fatalf("reconciled retry uploaded a duplicate: calls=%d", fixture.driver.uploadCalls)
	}
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	state, err = decodeCloudTransferState(transfer.CloudStateJSON)
	if err != nil || state.Items[key].Status != "completed" || state.Items[key].CurrentID != "already-uploaded" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestPluginCloudUploadConflictPolicies(t *testing.T) {
	tests := []struct {
		name        string
		policy      string
		wantWait    bool
		wantUploads int
		wantName    string
		wantRecycle bool
	}{
		{name: "ask", policy: models.MediaLibraryConflictAsk, wantWait: true, wantName: "Movie (2024).mkv"},
		{name: "skip", policy: models.MediaLibraryConflictSkip, wantName: "Movie (2024).mkv"},
		{name: "rename", policy: models.MediaLibraryConflictRename, wantUploads: 1, wantName: "Movie (2024) (2).mkv"},
		{name: "overwrite", policy: models.MediaLibraryConflictOverwrite, wantUploads: 1, wantName: "Movie (2024).mkv", wantRecycle: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUploadTransferFixture(t, test.policy, true)
			result := fixture.enqueueAndRun(t)
			if test.wantWait {
				if result.Wait == nil || result.Wait.ActionType != "transfer_conflict" || result.ErrorCode != "" {
					t.Fatalf("result=%+v", result)
				}
			} else if result.ErrorCode != "" || result.Wait != nil || result.RetryAt != nil {
				t.Fatalf("result=%+v", result)
			}
			if fixture.driver.uploadCalls != test.wantUploads {
				t.Fatalf("upload calls=%d", fixture.driver.uploadCalls)
			}
			if test.wantUploads == 1 && fixture.driver.uploaded[0].Name != test.wantName {
				t.Fatalf("uploaded=%+v", fixture.driver.uploaded)
			}
			_, conflictExists := fixture.driver.items["existing-video"]
			if test.wantRecycle == conflictExists {
				t.Fatalf("conflict exists=%t recycled=%v", conflictExists, fixture.driver.recycled)
			}
		})
	}
}

func TestPluginCloudUploadRejectsChangedAndNonRegularStagingSources(t *testing.T) {
	t.Run("changed size", func(t *testing.T) {
		fixture := newUploadTransferFixture(t, models.MediaLibraryConflictOverwrite, false)
		if err := os.WriteFile(fixture.source, []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
		result := fixture.enqueueAndRun(t)
		if result.ErrorCode != "cloud_upload_source_changed" || fixture.driver.uploadCalls != 0 {
			t.Fatalf("result=%+v calls=%d", result, fixture.driver.uploadCalls)
		}
	})
	t.Run("directory instead of file", func(t *testing.T) {
		fixture := newUploadTransferFixture(t, models.MediaLibraryConflictOverwrite, false)
		if err := os.Remove(fixture.source); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(fixture.source, 0o700); err != nil {
			t.Fatal(err)
		}
		result := fixture.enqueueAndRun(t)
		if result.ErrorCode != "cloud_upload_source_invalid" || fixture.driver.uploadCalls != 0 {
			t.Fatalf("result=%+v calls=%d", result, fixture.driver.uploadCalls)
		}
	})
	t.Run("same-name file outside exact task root", func(t *testing.T) {
		fixture := newUploadTransferFixture(t, models.MediaLibraryConflictOverwrite, false)
		if err := os.Remove(fixture.source); err != nil {
			t.Fatal(err)
		}
		sibling := filepath.Join(fixture.download.StagingAbsolutePath, filepath.Base(fixture.source))
		if err := os.WriteFile(sibling, []byte("provider video fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		result := fixture.enqueueAndRun(t)
		if result.ErrorCode != "cloud_upload_source_unavailable" || fixture.driver.uploadCalls != 0 {
			t.Fatalf("result=%+v calls=%d", result, fixture.driver.uploadCalls)
		}
		if body, err := os.ReadFile(sibling); err != nil || string(body) != "provider video fixture" {
			t.Fatalf("sibling staging file changed: body=%q err=%v", body, err)
		}
	})
}

var _ cloudpkg.UploadDriver = (*fakeUploadCloudDriver)(nil)
var _ cloudpkg.MutationDriver = (*fakeUploadCloudDriver)(nil)
