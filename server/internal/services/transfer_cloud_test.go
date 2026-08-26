package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

type fakeMutationCloudDriver struct {
	items                  map[string]cloudpkg.Item
	nextID                 int
	recycled               []string
	copyCalls              int
	moveCalls              int
	renameCalls            int
	renameErrOnce          bool
	recycleFailID          string
	statFailID             string
	statFailAfterRecycleID string
}

func newFakeMutationCloudDriver() *fakeMutationCloudDriver {
	return &fakeMutationCloudDriver{items: map[string]cloudpkg.Item{}, nextID: 100}
}

func (f *fakeMutationCloudDriver) Provider() string { return cloudpkg.ProviderPan115 }
func (f *fakeMutationCloudDriver) Capabilities() cloudpkg.Capabilities {
	return cloudpkg.Capabilities{NetworkDrive: true, DirectoryList: true, CreateDirectory: true, Move: true, Copy: true, Rename: true, Recycle: true}
}
func (f *fakeMutationCloudDriver) Probe(context.Context) (cloudpkg.Account, error) {
	return cloudpkg.Account{ID: "account"}, nil
}
func (f *fakeMutationCloudDriver) List(_ context.Context, parentID string, request cloudpkg.PageRequest) (cloudpkg.Page, error) {
	items := make([]cloudpkg.Item, 0)
	for _, item := range f.items {
		if item.ParentID == parentID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	start := int(request.Offset)
	if start >= len(items) {
		return cloudpkg.Page{Offset: request.Offset}, nil
	}
	end := start + int(request.Limit)
	if end > len(items) {
		end = len(items)
	}
	return cloudpkg.Page{Items: items[start:end], Offset: request.Offset, HasMore: end < len(items)}, nil
}
func (f *fakeMutationCloudDriver) Stat(_ context.Context, itemID string) (cloudpkg.Item, error) {
	if f.statFailID == itemID {
		return cloudpkg.Item{}, cloudpkg.Error(cloudpkg.CodeUnavailable, true, errors.New("temporary stat failure"))
	}
	if item, ok := f.items[itemID]; ok {
		return item, nil
	}
	return cloudpkg.Item{}, cloudpkg.Error(cloudpkg.CodeNotFound, false, errors.New("not found"))
}
func (f *fakeMutationCloudDriver) DirectURL(context.Context, cloudpkg.DirectURLRequest) (cloudpkg.TemporaryURL, error) {
	return cloudpkg.TemporaryURL{URL: "https://example.invalid", Headers: http.Header{}}, nil
}
func (f *fakeMutationCloudDriver) CreateDirectory(_ context.Context, parentID, name string) (cloudpkg.Item, error) {
	f.nextID++
	item := cloudpkg.Item{ID: "created-" + strconv.Itoa(f.nextID), ParentID: parentID, Name: name, IsDir: true}
	f.items[item.ID] = item
	return item, nil
}
func (f *fakeMutationCloudDriver) Move(_ context.Context, itemID, targetParentID string) error {
	item, ok := f.items[itemID]
	if !ok {
		return cloudpkg.Error(cloudpkg.CodeNotFound, false, nil)
	}
	f.moveCalls++
	item.ParentID = targetParentID
	f.items[itemID] = item
	return nil
}
func (f *fakeMutationCloudDriver) Copy(_ context.Context, itemID, targetParentID string) error {
	item, ok := f.items[itemID]
	if !ok {
		return cloudpkg.Error(cloudpkg.CodeNotFound, false, nil)
	}
	f.copyCalls++
	f.nextID++
	item.ID, item.ParentID = "copied-"+strconv.Itoa(f.nextID), targetParentID
	f.items[item.ID] = item
	return nil
}
func (f *fakeMutationCloudDriver) Rename(_ context.Context, itemID, name string) error {
	item, ok := f.items[itemID]
	if !ok {
		return cloudpkg.Error(cloudpkg.CodeNotFound, false, nil)
	}
	f.renameCalls++
	if f.renameErrOnce {
		f.renameErrOnce = false
		return cloudpkg.Error(cloudpkg.CodeUnavailable, true, errors.New("temporary rename failure"))
	}
	item.Name = name
	f.items[itemID] = item
	return nil
}
func (f *fakeMutationCloudDriver) Recycle(_ context.Context, itemID string) error {
	if f.recycleFailID == itemID {
		if f.statFailAfterRecycleID == itemID {
			f.statFailID = itemID
		}
		return cloudpkg.Error(cloudpkg.CodeUnavailable, true, errors.New("temporary recycle failure"))
	}
	if _, ok := f.items[itemID]; !ok {
		return cloudpkg.Error(cloudpkg.CodeNotFound, false, nil)
	}
	delete(f.items, itemID)
	f.recycled = append(f.recycled, itemID)
	return nil
}

type cloudTransferFixture struct {
	queue      *QueueService
	service    *TransferService
	driver     *fakeMutationCloudDriver
	download   models.DownloadTask
	manifest   downloadpkg.Manifest
	library    models.MediaLibrary
	sourceID   string
	connection uint
}

func newCloudTransferFixture(t *testing.T, mode, policy string, conflict bool) cloudTransferFixture {
	t.Helper()
	queue, actor, _ := queueFixture(t)
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	connection := models.Connection{Name: "115", NameNormalized: "115-cloud-transfer", Provider: cloudpkg.ProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	driver := newFakeMutationCloudDriver()
	for _, item := range []cloudpkg.Item{
		{ID: "0", Name: "root", IsDir: true},
		{ID: "source-root", ParentID: "0", Name: "downloads", IsDir: true},
		{ID: "target-storage-root", ParentID: "0", Name: "media", IsDir: true},
		{ID: "library-root", ParentID: "target-storage-root", Name: "library", IsDir: true},
		{ID: "source-video", ParentID: "source-root", Name: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes, SHA1: "VIDEO-SHA1"},
	} {
		driver.items[item.ID] = item
	}
	sourceStorage := models.Storage{Name: "115 Downloads", NameNormalized: "115-downloads-transfer", Type: models.StorageTypePan115, RootPath: "source-root", RootDisplayPath: "/downloads", RootPathNormalized: "pan115:source-root", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	targetStorage := models.Storage{Name: "115 Media", NameNormalized: "115-media-transfer", Type: models.StorageTypePan115, RootPath: "target-storage-root", RootDisplayPath: "/media", RootPathNormalized: "pan115:target-storage-root", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&sourceStorage).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Create(&targetStorage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "115 Movies", NameNormalized: "115-movies-transfer", StorageID: targetStorage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/library", ProviderRootID: "library-root", SortOrder: 1, TransferMode: mode, ConflictPolicy: policy, MovieDirectoryTemplate: "{category}/{title} ({year})", MovieFilenameTemplate: "{title} ({year})", TVDirectoryTemplate: "{category}/{title} ({year})/Season {season:02}", TVFilenameTemplate: "{title} - S{season:02}E{episode:02}", Enabled: true, Recursive: true, FullScanIntervalHours: 24, IncrementalMinutes: 15, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, MetadataLanguage: "zh-CN", MetadataRegion: "CN", MatchStrategy: "balanced", ProviderRatePerSecond: 1, ProviderConcurrency: 1, MetadataRatePerSecond: 1, MetadataConcurrency: 1, Status: models.MediaLibraryStatusListening, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	year := 2024
	tmdbID, confidence := int64(550), .98
	identityRaw, err := json.Marshal(MediaIdentitySnapshot{Version: 1, Revision: 1, Source: mediaIdentitySourceAutomatic, Status: mediaIdentityStatusVerified, TMDBID: &tmdbID, MediaType: "movie", Title: "Movie", Year: &year, Category: "电影", Confidence: &confidence})
	if err != nil {
		t.Fatal(err)
	}
	download := models.DownloadTask{ID: "cloud-download-" + mode + "-" + policy, OwnerID: actor.User.ID, DownloaderName: "115 Offline", ProviderType: models.DownloaderTypePan115Offline, ProviderOutputID: "source-root", SourceCiphertext: "encrypted", StagingStorageID: &sourceStorage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, ProfileRulesJSON: profile.RulesJSON, TargetLibraryID: &library.ID, TargetLibraryName: library.Name, TargetStorageID: &targetStorage.ID, TargetStorageType: models.StorageTypePan115, TargetConnectionID: &connection.ID, TargetProviderRootID: "library-root", TransferMode: mode, ConflictPolicy: policy, MovieDirectoryTemplate: library.MovieDirectoryTemplate, MovieFilenameTemplate: library.MovieFilenameTemplate, TVDirectoryTemplate: library.TVDirectoryTemplate, TVFilenameTemplate: library.TVFilenameTemplate, DisplayName: "Movie", Phase: models.DownloadTaskStatusCompleted, ScrapeStatus: "completed_verified", ScrapeTitle: "Movie", ScrapeMediaType: "movie", ScrapeCategory: "电影", ScrapeTMDBID: &tmdbID, ScrapeConfidence: &confidence, ScrapeYear: &year, IdentitySource: mediaIdentitySourceAutomatic, IdentityStatus: mediaIdentityStatusVerified, IdentityRevision: 1, IdentitySnapshotJSON: string(identityRaw), ManifestFileCount: 1, CreatedAt: now, UpdatedAt: now}
	_, err = queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: "Movie", Payload: downloadJobPayload{DownloadTaskID: download.ID}}, func(tx *gorm.DB, job models.Job) error {
		download.JobID = job.ID
		return tx.Create(&download).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes, ProviderItemID: "source-video", ProviderParentID: "source-root", SHA1: "VIDEO-SHA1"}}}
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	service.connections = &ConnectionService{db: queue.db, drivers: map[uint]cloudpkg.Driver{connection.ID: driver}}
	if conflict {
		driver.items["category-dir"] = cloudpkg.Item{ID: "category-dir", ParentID: "library-root", Name: "电影", IsDir: true}
		driver.items["movie-dir"] = cloudpkg.Item{ID: "movie-dir", ParentID: "category-dir", Name: "Movie (2024)", IsDir: true}
		driver.items["existing-video"] = cloudpkg.Item{ID: "existing-video", ParentID: "movie-dir", Name: "Movie (2024).mkv", Size: 99, SHA1: "OLD"}
	}
	return cloudTransferFixture{queue: queue, service: service, driver: driver, download: download, manifest: manifest, library: library, sourceID: "source-video", connection: connection.ID}
}

func (f cloudTransferFixture) run(t *testing.T) WorkerResult {
	t.Helper()
	if err := f.service.Enqueue(f.download, f.manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := f.queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	return NewTransferWorker(f.service).Run(context.Background(), workerRuntime{queue: f.queue, job: *claimed}, *claimed)
}

func TestCloudTransferWorkerMovesAndCopiesWithinOneConnection(t *testing.T) {
	for _, mode := range []string{models.MediaLibraryTransferMove, models.MediaLibraryTransferCopy} {
		t.Run(mode, func(t *testing.T) {
			fixture := newCloudTransferFixture(t, mode, models.MediaLibraryConflictOverwrite, false)
			result := fixture.run(t)
			if result.ErrorCode != "" || result.Wait != nil || result.RetryAt != nil {
				t.Fatalf("result=%+v", result)
			}
			var found cloudpkg.Item
			for _, item := range fixture.driver.items {
				if item.Name == "Movie (2024).mkv" && item.ParentID != "source-root" {
					found = item
				}
			}
			if found.ID == "" || found.SHA1 != "VIDEO-SHA1" {
				t.Fatalf("organized item missing: %+v", fixture.driver.items)
			}
			source, sourceExists := fixture.driver.items[fixture.sourceID]
			if mode == models.MediaLibraryTransferMove && (!sourceExists || source.ParentID == "source-root") {
				t.Fatalf("move did not relocate the source: %+v", source)
			}
			if mode == models.MediaLibraryTransferCopy && !sourceExists {
				t.Fatal("copy removed the source")
			}
			var transfer models.TransferTask
			if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error; err != nil {
				t.Fatal(err)
			}
			if transfer.Phase != models.TransferTaskStatusCompleted || transfer.ProcessedFiles != 1 || transfer.CloudStateJSON == "" {
				t.Fatalf("transfer=%+v", transfer)
			}
			if strings.Contains(transfer.PlanSummaryJSON, fixture.sourceID) || strings.Contains(transfer.PlanSummaryJSON, found.ID) {
				t.Fatalf("public summary leaked provider identity: %s", transfer.PlanSummaryJSON)
			}
			var refreshed models.MediaLibrary
			if err := fixture.queue.db.First(&refreshed, fixture.library.ID).Error; err != nil || refreshed.DirtyGeneration != fixture.library.DirtyGeneration+1 {
				t.Fatalf("library=%+v err=%v", refreshed, err)
			}
		})
	}
}

func TestCloudTransferConflictAskDoesNotReplaceExistingItem(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictAsk, true)
	result := fixture.run(t)
	if result.Wait == nil || result.Wait.ActionType != "transfer_conflict" || result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
	if _, ok := fixture.driver.items["existing-video"]; !ok {
		t.Fatal("conflicting target changed before user action")
	}
	if item := fixture.driver.items[fixture.sourceID]; item.ParentID != "source-root" {
		t.Fatalf("source moved before user action: %+v", item)
	}
}

func TestCloudTransferOverwriteRecyclesConflictBeforeMoving(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, true)
	result := fixture.run(t)
	if result.ErrorCode != "" || result.Wait != nil {
		t.Fatalf("result=%+v", result)
	}
	if _, ok := fixture.driver.items["existing-video"]; ok {
		t.Fatal("conflicting item was not recycled")
	}
	if len(fixture.driver.recycled) == 0 || fixture.driver.recycled[0] != "existing-video" {
		t.Fatalf("recycled=%v", fixture.driver.recycled)
	}
	item := fixture.driver.items[fixture.sourceID]
	if item.Name != "Movie (2024).mkv" || item.ParentID != "movie-dir" {
		t.Fatalf("organized source=%+v", item)
	}
}

func TestCloudTransferSkipKeepsConflictAndSource(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictSkip, true)
	result := fixture.run(t)
	if result.ErrorCode != "" || result.Wait != nil || result.RetryAt != nil {
		t.Fatalf("result=%+v", result)
	}
	if _, ok := fixture.driver.items["existing-video"]; !ok {
		t.Fatal("skip removed the conflicting target")
	}
	if source := fixture.driver.items[fixture.sourceID]; source.ParentID != "source-root" {
		t.Fatalf("skip moved the source: %+v", source)
	}
	if len(fixture.driver.recycled) != 0 {
		t.Fatalf("skip recycled provider items: %v", fixture.driver.recycled)
	}
}

func TestCloudTransferRenameKeepsExistingItemAndUsesOneGroupSuffix(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictRename, true)
	fixture.driver.items["source-subtitle"] = cloudpkg.Item{ID: "source-subtitle", ParentID: "source-root", Name: "Movie.2024.zh-CN.default.vtt", Size: 2, SHA1: "SUBTITLE-SHA1"}
	fixture.driver.items["source-subtitle-case"] = cloudpkg.Item{ID: "source-subtitle-case", ParentID: "source-root", Name: "Movie.2024.ZH-CN.default.vtt", Size: 3, SHA1: "SUBTITLE-CASE-SHA1"}
	fixture.manifest.Files = append(fixture.manifest.Files,
		downloadpkg.File{RelativePath: "Movie.2024.zh-CN.default.vtt", Size: 2, ProviderItemID: "source-subtitle", ProviderParentID: "source-root", SHA1: "SUBTITLE-SHA1"},
		downloadpkg.File{RelativePath: "Movie.2024.ZH-CN.default.vtt", Size: 3, ProviderItemID: "source-subtitle-case", ProviderParentID: "source-root", SHA1: "SUBTITLE-CASE-SHA1"},
	)
	result := fixture.run(t)
	if result.ErrorCode != "" || result.Wait != nil {
		t.Fatalf("result=%+v", result)
	}
	if _, ok := fixture.driver.items["existing-video"]; !ok {
		t.Fatal("rename policy changed the existing target")
	}
	item := fixture.driver.items[fixture.sourceID]
	if item.Name != "Movie (2024) (1).mkv" || item.ParentID != "movie-dir" {
		t.Fatalf("renamed source=%+v", item)
	}
	subtitle := fixture.driver.items["source-subtitle"]
	if subtitle.Name != "Movie (2024) (1).zh-CN.default.vtt" || subtitle.ParentID != "movie-dir" {
		t.Fatalf("renamed sidecar=%+v", subtitle)
	}
	caseCollision := fixture.driver.items["source-subtitle-case"]
	if caseCollision.Name != "Movie (2024) (1).ZH-CN.default.2.vtt" || caseCollision.ParentID != "movie-dir" {
		t.Fatalf("renamed colliding sidecar=%+v", caseCollision)
	}
	if len(fixture.driver.recycled) != 0 {
		t.Fatalf("rename policy recycled provider items: %v", fixture.driver.recycled)
	}
}

func TestCloudTransferMoveResumesAfterPlacedItemRenameFailure(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	fixture.driver.renameErrOnce = true
	if err := fixture.service.Enqueue(fixture.download, fixture.manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	worker := NewTransferWorker(fixture.service)
	runtime := workerRuntime{queue: fixture.queue, job: *claimed}
	first := worker.Run(context.Background(), runtime, *claimed)
	if first.RetryAt == nil || first.ErrorCode != cloudpkg.CodeUnavailable {
		t.Fatalf("first result=%+v", first)
	}
	placed := fixture.driver.items[fixture.sourceID]
	if placed.ParentID == "source-root" || placed.Name != "Movie.2024.mkv" {
		t.Fatalf("expected placed but unrenamed item, got %+v", placed)
	}
	var transfer models.TransferTask
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	second := worker.runCloudTransfer(context.Background(), runtime, transfer, fixture.download, fixture.manifest, time.Now())
	if second.ErrorCode != "" || second.RetryAt != nil || second.Wait != nil {
		t.Fatalf("second result=%+v", second)
	}
	completed := fixture.driver.items[fixture.sourceID]
	if completed.Name != "Movie (2024).mkv" || completed.ParentID == "source-root" {
		t.Fatalf("resumed item=%+v", completed)
	}
}

func TestCloudTransferCopyAmbiguityKeepsEveryCandidateAndFailsClosed(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	if err := fixture.service.Enqueue(fixture.download, fixture.manifest); err != nil {
		t.Fatal(err)
	}
	var transfer models.TransferTask
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	tempName := ".ohmycine-import-" + strings.ReplaceAll(transfer.ID, "-", "")
	if len(tempName) > 48 {
		tempName = tempName[:48]
	}
	fixture.driver.items["ambiguous-temp"] = cloudpkg.Item{ID: "ambiguous-temp", ParentID: "library-root", Name: tempName, IsDir: true}
	for _, id := range []string{"ambiguous-copy-a", "ambiguous-copy-b"} {
		fixture.driver.items[id] = cloudpkg.Item{ID: id, ParentID: "ambiguous-temp", Name: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes, SHA1: "VIDEO-SHA1"}
	}
	claimed, err := fixture.queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(fixture.service).Run(context.Background(), workerRuntime{queue: fixture.queue, job: *claimed}, *claimed)
	if result.ErrorCode != cloudpkg.CodeMutationUnknown || result.RetryAt != nil {
		t.Fatalf("result=%+v", result)
	}
	for _, id := range []string{"ambiguous-temp", "ambiguous-copy-a", "ambiguous-copy-b", fixture.sourceID} {
		if _, ok := fixture.driver.items[id]; !ok {
			t.Fatalf("fail-closed cleanup removed %s", id)
		}
	}
}

func TestProviderItemBoundaryAcceptsAccountRootZero(t *testing.T) {
	driver := newFakeMutationCloudDriver()
	driver.items["0"] = cloudpkg.Item{ID: "0", Name: "root", IsDir: true}
	driver.items["nested"] = cloudpkg.Item{ID: "nested", ParentID: "0", Name: "nested", IsDir: true}
	item, err := providerItemWithinRoot(context.Background(), driver, "nested", "0")
	if err != nil || item.ID != "nested" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}
