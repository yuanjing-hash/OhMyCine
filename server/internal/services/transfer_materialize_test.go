package services

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

type fakeReadCloudDriver struct {
	*fakeMutationCloudDriver
	content []byte
	offsets []int64
}

type materializeTestRuntime struct{}

func (materializeTestRuntime) Heartbeat(*float64, *int64, *int64, *float64, *int64) error { return nil }
func (materializeTestRuntime) Checkpoint(any) error                                       { return nil }

func (f *fakeReadCloudDriver) Capabilities() cloudpkg.Capabilities {
	value := f.fakeMutationCloudDriver.Capabilities()
	value.TemporaryDirectURL = true
	return value
}

func (f *fakeReadCloudDriver) OpenRead(_ context.Context, request cloudpkg.ReadRequest) (cloudpkg.ReadResult, error) {
	f.offsets = append(f.offsets, request.Offset)
	if request.Offset < 0 || request.Offset > int64(len(f.content)) {
		return cloudpkg.ReadResult{}, errors.New("invalid offset")
	}
	size := int64(len(f.content))
	return cloudpkg.ReadResult{Body: io.NopCloser(bytes.NewReader(f.content[request.Offset:])), OffsetAccepted: true, TotalSize: &size}, nil
}

func TestPan115ToLocalMaterializationResumesPartialAndCleansOwnedRoot(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	connection := models.Connection{Name: "115 source", NameNormalized: "115-materialize-" + uuid.NewString(), Provider: cloudpkg.ProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	driver := &fakeReadCloudDriver{fakeMutationCloudDriver: newFakeMutationCloudDriver(), content: bytes.Repeat([]byte{'v'}, int(minimumAutomaticTransferVideoBytes))}
	sum := sha1.Sum(driver.content)
	sha := hex.EncodeToString(sum[:])
	for _, item := range []cloudpkg.Item{
		{ID: "0", Name: "root", IsDir: true},
		{ID: "source-storage-root", ParentID: "0", Name: "downloads", IsDir: true},
		{ID: "package-root", ParentID: "source-storage-root", Name: "omc-task", IsDir: true},
		{ID: "source-video", ParentID: "package-root", Name: "Movie.2024.mkv", Size: int64(len(driver.content)), SHA1: sha},
	} {
		driver.items[item.ID] = item
	}
	sourceStorage := models.Storage{Name: "115 source", NameNormalized: "115-source-" + uuid.NewString(), Type: models.StorageTypePan115, RootPath: "source-storage-root", RootPathNormalized: "pan115:source-storage-root", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&sourceStorage).Error; err != nil {
		t.Fatal(err)
	}
	targetRoot := t.TempDir()
	targetStorage := models.Storage{Name: "Local target", NameNormalized: "local-target-" + uuid.NewString(), Type: models.StorageTypeLocal, RootPath: targetRoot, RootPathNormalized: targetRoot, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&targetStorage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Movies", NameNormalized: "materialized-movies-" + uuid.NewString(), StorageID: targetStorage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", TransferMode: models.MediaLibraryTransferCopy, ConflictPolicy: models.MediaLibraryConflictOverwrite, MovieDirectoryTemplate: "{category}/{title} ({year})", MovieFilenameTemplate: "{title} ({year})", TVDirectoryTemplate: "{category}/{title} ({year})/Season {season:02}", TVFilenameTemplate: "{title} - S{season:02}E{episode:02}", Enabled: true, Recursive: true, FullScanIntervalHours: 24, IncrementalMinutes: 15, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, MetadataLanguage: "zh-CN", MetadataRegion: "CN", MatchStrategy: "balanced", CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	year, tmdbID, confidence := 2024, int64(550), .99
	identityRaw, _ := json.Marshal(MediaIdentitySnapshot{Version: 1, Revision: 1, Source: mediaIdentitySourceAutomatic, Status: mediaIdentityStatusVerified, TMDBID: &tmdbID, MediaType: "movie", Title: "Movie", Year: &year, Category: "电影", Confidence: &confidence})
	sourceIdentity, _ := marshalDataSourceIdentity(models.DataSourceIdentity{Kind: models.DataSourceKindProvider, ProviderType: models.StorageTypePan115, ConnectionIdentity: strconv.FormatUint(uint64(connection.ID), 10), StorageScope: strconv.FormatUint(uint64(sourceStorage.ID), 10)})
	targetIdentity, _ := marshalDataSourceIdentity(localDataSourceIdentity())
	download := models.DownloadTask{ID: uuid.NewString(), OwnerID: actor.User.ID, DownloaderName: "115", ProviderType: models.DownloaderTypePan115Offline, ProviderOutputID: "package-root", SourceCiphertext: "encrypted", StagingStorageID: &sourceStorage.ID, StagingAbsolutePath: staging, ProfileID: profile.ID, ProfileRevision: profile.Revision, ProfileRulesJSON: profile.RulesJSON, TargetLibraryID: &library.ID, TargetLibraryName: library.Name, TargetStorageID: &targetStorage.ID, TargetStorageType: models.StorageTypeLocal, TargetStorageRoot: targetRoot, TargetRelativeRoot: "/", SourceDataSourceJSON: sourceIdentity, TargetDataSourceJSON: targetIdentity, TransferRouteKind: models.TransferRouteCrossSource, TransferRouteVersion: models.TransferRouteVersionCurrent, TransferMode: models.MediaLibraryTransferCopy, ConflictPolicy: models.MediaLibraryConflictOverwrite, MovieDirectoryTemplate: library.MovieDirectoryTemplate, MovieFilenameTemplate: library.MovieFilenameTemplate, TVDirectoryTemplate: library.TVDirectoryTemplate, TVFilenameTemplate: library.TVFilenameTemplate, DisplayName: "Movie", Phase: models.DownloadTaskStatusCompleted, ScrapeStatus: "completed_verified", ScrapeTitle: "Movie", ScrapeMediaType: "movie", ScrapeCategory: "电影", ScrapeTMDBID: &tmdbID, ScrapeConfidence: &confidence, ScrapeYear: &year, IdentitySource: mediaIdentitySourceAutomatic, IdentityStatus: mediaIdentityStatusVerified, IdentityRevision: 1, IdentitySnapshotJSON: string(identityRaw), ManifestFileCount: 1, CreatedAt: now, UpdatedAt: now}
	_, err := queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: "Movie", Payload: downloadJobPayload{DownloadTaskID: download.ID}}, func(tx *gorm.DB, job models.Job) error {
		download.JobID = job.ID
		return tx.Create(&download).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: int64(len(driver.content)), ProviderItemID: "source-video", ProviderParentID: "package-root", SHA1: sha}}}
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	service.connections = &ConnectionService{db: queue.db, drivers: map[uint]cloudpkg.Driver{connection.ID: driver}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	var transferJob models.Job
	if err := queue.db.First(&transferJob, "id = ?", transfer.JobID).Error; err != nil || transferJob.ResourceKey != "staging:cross-source" {
		t.Fatalf("cross-source resource key=%q err=%v", transferJob.ResourceKey, err)
	}
	managed := filepath.Join(staging, crossSourceRootName, transfer.ID)
	if err := os.MkdirAll(managed, 0o700); err != nil {
		t.Fatal(err)
	}
	partialSize := len(driver.content) / 2
	if err := os.WriteFile(filepath.Join(managed, "Movie.2024.mkv.partial"), driver.content[:partialSize], 0o600); err != nil {
		t.Fatal(err)
	}
	key := normalizedManifestPath(manifest.Files[0].RelativePath)
	state := cloudTransferState{Version: cloudTransferStateVersion, Directories: map[string]string{}, Items: map[string]cloudTransferItemState{}, ManagedRoot: filepath.ToSlash(filepath.Join(crossSourceRootName, transfer.ID)), Materialized: map[string]materializedItemState{key: {RelativePath: key, Size: manifest.Files[0].Size, SHA1: sha, Status: "materializing"}}}
	encoded, _ := encodeCloudTransferState(state)
	if err := queue.db.Model(&transfer).Update("cloud_state_json", encoded).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.Wait != nil || result.RetryAt != nil {
		t.Fatalf("result=%+v", result)
	}
	if len(driver.offsets) != 1 || driver.offsets[0] != int64(partialSize) {
		t.Fatalf("read offsets=%v", driver.offsets)
	}
	destination := filepath.Join(targetRoot, "电影", "Movie (2024)", "Movie (2024).mkv")
	if info, err := os.Stat(destination); err != nil || info.Size() != int64(len(driver.content)) {
		t.Fatalf("destination info=%+v err=%v", info, err)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed root was not cleaned: %v", err)
	}
}

func TestCrossSourceCancelCleansOnlyOwnedPartialFiles(t *testing.T) {
	staging := t.TempDir()
	task := models.TransferTask{ID: uuid.NewString()}
	state := cloudTransferState{Version: cloudTransferStateVersion, Directories: map[string]string{}, Items: map[string]cloudTransferItemState{}, ManagedRoot: filepath.ToSlash(filepath.Join(crossSourceRootName, task.ID)), Materialized: map[string]materializedItemState{}}
	task.CloudStateJSON, _ = encodeCloudTransferState(state)
	download := models.DownloadTask{StagingAbsolutePath: staging}
	root := filepath.Join(staging, crossSourceRootName, task.ID, "nested")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(root, "video.mkv.partial")
	completed := filepath.Join(root, "completed.mkv")
	if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(completed, []byte("completed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanupCrossSourcePartials(context.Background(), task, download); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("partial retained: %v", err)
	}
	if body, err := os.ReadFile(completed); err != nil || string(body) != "completed" {
		t.Fatalf("completed file changed: body=%q err=%v", body, err)
	}
}

func TestCopyMaterializedStreamStopsAtFrozenManifestSize(t *testing.T) {
	var destination bytes.Buffer
	payload := bytes.Repeat([]byte{'x'}, 4*1024*1024)
	written, err := copyMaterializedStream(context.Background(), materializeTestRuntime{}, &destination, bytes.NewReader(payload), 1024)
	if !errors.Is(err, errMaterializedStreamTooLarge) {
		t.Fatalf("oversized stream error=%v", err)
	}
	if written != 1025 || destination.Len() != 1025 {
		t.Fatalf("oversized stream wrote=%d buffered=%d", written, destination.Len())
	}
}

var _ cloudpkg.ReadDriver = (*fakeReadCloudDriver)(nil)
