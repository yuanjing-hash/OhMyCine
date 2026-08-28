package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

func TestTransferCompletionCreatesManagedOwnershipManifest(t *testing.T) {
	queue, _, download, _, destination := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
	var item models.MediaManagedItem
	if err := queue.db.First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if !item.Managed || !item.Active || item.Kind != models.MediaManagedItemKindVideo || filepath.ToSlash(item.RelativePath) != "华语电影/Movie (2024)/Movie (2024).mkv" || item.ProviderItemID != "" {
		t.Fatalf("unsafe ownership manifest: %+v", item)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatal(err)
	}
}

func TestReorganizationUsesOriginalProviderIdentityForFlattenedMultiSeasonItems(t *testing.T) {
	taskSeason := 2
	tmdbID := int64(75129)
	download := models.DownloadTask{ScrapeMediaType: "tv", ScrapeTitle: "屌丝男士", ScrapeCategory: "国产剧", ScrapeTMDBID: &tmdbID, ScrapeSeason: &taskSeason, IdentityRevision: 1}
	manifest := downloadpkg.Manifest{Name: "屌丝男士 第1-4季", Complete: true}
	identity := MediaIdentitySnapshot{Version: 1, Revision: 1, Source: mediaIdentitySourceAutomatic, Status: mediaIdentityStatusVerified, TMDBID: &tmdbID, MediaType: "tv", Title: download.ScrapeTitle, Category: download.ScrapeCategory}
	managed := make([]models.MediaManagedItem, 0, 4)
	for season := 1; season <= 4; season++ {
		episode := 1
		relative := fmt.Sprintf("屌丝男士第%d季/屌丝男士第%d季.第1集.Diors.Man.S%02d.E01.mp4", season, season, season)
		providerID := fmt.Sprintf("provider-%d", season)
		size := minimumAutomaticTransferVideoBytes + int64(season)
		manifest.Files = append(manifest.Files, downloadpkg.File{RelativePath: relative, Size: size, ProviderItemID: providerID, SHA1: fmt.Sprintf("SHA1-%d", season)})
		seasonValue := season
		identity.Episodes = append(identity.Episodes, mediarecognition.FileEpisodeFact{RelativePath: relative, Season: &seasonValue, Episode: &episode, Evidence: "structured"})
		managed = append(managed, models.MediaManagedItem{ID: uint(season), LibraryID: 1, TransferTaskID: "transfer", Kind: models.MediaManagedItemKindVideo, RelativePath: fmt.Sprintf("国产剧/屌丝男士/Season 02/屌丝男士 - S02E%02d.mp4", season), ProviderItemID: providerID, Size: size, Managed: true, Active: true})
	}
	identityRaw, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	download.IdentitySnapshotJSON = string(identityRaw)
	transfer := models.TransferTask{ID: "transfer", ManifestJSON: string(manifestRaw)}
	library := models.MediaLibrary{ID: 1, ProfileRevision: 1, TVDirectoryTemplate: "{category}/{title}/Season {season:02}", TVFilenameTemplate: "{title} - S{season:02}E{episode:02}"}
	storage := models.Storage{Type: models.StorageTypePan115}
	plan, _, err := buildReorganizationPlan(library, storage, transfer, download, identity, identity, managed, models.MediaLibraryConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 4 {
		t.Fatalf("items=%d want=4", len(plan.Items))
	}
	for index, item := range plan.Items {
		season := index + 1
		want := fmt.Sprintf("电视剧/国产剧/屌丝男士/Season %02d/屌丝男士 - S%02dE01.mp4", season, season)
		if item.NewRelativePath != want || item.SourceSHA1 != fmt.Sprintf("SHA1-%d", season) {
			t.Fatalf("item[%d]=%+v want_path=%q", index, item, want)
		}
	}
}

func TestMediaReorganizationLocalWorkerIsIdempotentAndUpdatesIdentity(t *testing.T) {
	queue, actor, download, _, oldPath := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	year := 2024
	oldID := int64(550)
	identity := MediaIdentitySnapshot{Version: 1, Revision: 1, Source: mediaIdentitySourceAutomatic, Status: mediaIdentityStatusVerified, TMDBID: &oldID, MediaType: "movie", Title: "Movie", Year: &year, Category: "华语电影"}
	identityRaw, _ := json.Marshal(identity)
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", download.ID).Updates(map[string]any{"identity_revision": 1, "identity_snapshot_json": string(identityRaw), "identity_source": mediaIdentitySourceAutomatic, "identity_status": mediaIdentityStatusVerified}).Error; err != nil {
		t.Fatal(err)
	}
	download.IdentityRevision, download.IdentitySnapshotJSON = 1, string(identityRaw)
	transferService := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := transferService.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	claimed, _ := queue.Claim([]string{"transfer"})
	if result := NewTransferWorker(transferService).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.MediaManagedItem{}).Where("transfer_task_id = ?", transfer.ID).Update("size", info.Size()).Error; err != nil {
		t.Fatal(err)
	}
	var library models.MediaLibrary
	var storage models.Storage
	if queue.db.First(&library, transfer.LibraryID).Error != nil || queue.db.First(&storage, library.StorageID).Error != nil {
		t.Fatal("missing library boundary")
	}
	newYear := 2025
	newID := int64(999)
	target := identity
	target.Revision, target.Source, target.Locked, target.TMDBID, target.Title, target.Year = 2, mediaIdentitySourceManual, true, &newID, "Correct Movie", &newYear
	targetRaw, _ := json.Marshal(target)
	var items []models.MediaManagedItem
	if err := queue.db.Where("transfer_task_id = ?", transfer.ID).Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	plan, _, err := buildReorganizationPlan(library, storage, transfer, download, identity, target, items, models.MediaLibraryConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	planRaw, _ := json.Marshal(plan)
	stateRaw, _ := json.Marshal(reorganizationState{Version: 1, Completed: map[uint]bool{}})
	taskID := "reorganization-local"
	var task models.MediaReorganizationTask
	_, err = queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: JobTypeMediaReorganization, DisplayName: "correct", Payload: mediaReorganizationJobPayload{ReorganizationTaskID: taskID}}, func(tx *gorm.DB, job models.Job) error {
		task = models.MediaReorganizationTask{ID: taskID, OwnerID: actor.User.ID, JobID: job.ID, LibraryID: library.ID, TransferTaskID: transfer.ID, SourceIdentityRevision: 1, TargetIdentityRevision: 2, TargetIdentityJSON: string(targetRaw), ManagedManifestDigest: managedManifestDigest(items), RuleRevision: library.ProfileRevision, ConflictPolicy: models.MediaLibraryConflictRename, PlanJSON: string(planRaw), StateJSON: string(stateRaw), Phase: models.MediaReorganizationPhaseQueued, TotalItems: len(plan.Items), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		return tx.Create(&task).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	reorgClaim, err := queue.Claim([]string{JobTypeMediaReorganization})
	if err != nil || reorgClaim == nil {
		t.Fatalf("claim=%+v err=%v", reorgClaim, err)
	}
	worker := NewMediaReorganizationWorker(NewMediaReorganizationService(queue.db, queue.audit, queue, nil, nil, zerolog.Nop()))
	if result := worker.Run(context.Background(), workerRuntime{queue: queue, job: *reorgClaim}, *reorgClaim); result.ErrorCode != "" {
		t.Fatalf("reorganization=%+v", result)
	}
	newPath := filepath.Join(storage.RootPath, filepath.FromSlash(plan.Items[0].NewRelativePath))
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new target missing: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old target remains: %v", err)
	}
	var updated models.DownloadTask
	if err := queue.db.First(&updated, "id = ?", download.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.IdentityRevision != 2 || !updated.IdentityLocked || updated.ScrapeTitle != "Correct Movie" || updated.ScrapeTMDBID == nil || *updated.ScrapeTMDBID != newID {
		t.Fatalf("identity not committed: %+v", updated)
	}
	// Worker replay must be a no-op and cannot duplicate or move the file again.
	if result := worker.Run(context.Background(), workerRuntime{queue: queue, job: *reorgClaim}, *reorgClaim); result.ErrorCode != "" {
		t.Fatalf("replay=%+v", result)
	}
}

func TestMediaReorganizationPreviewReclassifiesWithCurrentLibraryProfile(t *testing.T) {
	queue, actor, download, _, _ := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	year, oldID := 2024, int64(550)
	identity := MediaIdentitySnapshot{Version: 1, Revision: 1, Source: mediaIdentitySourceAutomatic, Status: mediaIdentityStatusVerified, TMDBID: &oldID, MediaType: "movie", Title: "Movie", Year: &year, Category: "华语电影"}
	identityRaw, _ := json.Marshal(identity)
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", download.ID).Updates(map[string]any{"identity_revision": 1, "identity_snapshot_json": string(identityRaw), "identity_source": mediaIdentitySourceAutomatic, "identity_status": mediaIdentityStatusVerified}).Error; err != nil {
		t.Fatal(err)
	}
	download.IdentityRevision, download.IdentitySnapshotJSON = 1, string(identityRaw)
	transfers := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := transfers.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if result := NewTransferWorker(transfers).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/movie/999" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"id":999,"title":"Correct Foreign Movie","original_title":"Correct Foreign Movie","original_language":"ja","release_date":"2025-01-02","genres":[{"id":18,"name":"Drama"}],"production_countries":[{"iso_3166_1":"JP"}]}`))
	}))
	defer upstream.Close()
	metadata := NewMetadataSettingsService(queue.db, queue.audit, nil, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", upstream.URL, upstream.Client())
	}
	service := NewMediaReorganizationService(queue.db, queue.audit, queue, metadata, nil, zerolog.Nop())
	preview, err := service.Preview(context.Background(), actor, MediaReorganizationPreviewInput{TransferTaskID: transfer.ID, TMDBID: 999, MediaType: "movie", ConflictPolicy: models.MediaLibraryConflictRename}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || !strings.HasPrefix(filepath.ToSlash(preview.Items[0].NewRelativePath), "电影/外语电影/") {
		t.Fatalf("preview did not use current profile classification: %+v", preview)
	}
	var persisted models.MediaReorganizationPreview
	if err := queue.db.First(&persisted, "transfer_task_id = ?", transfer.ID).Error; err != nil {
		t.Fatal(err)
	}
	var target MediaIdentitySnapshot
	if err := json.Unmarshal([]byte(persisted.TargetIdentityJSON), &target); err != nil || target.Category != "外语电影" || target.Revision != 2 || !target.Locked {
		t.Fatalf("target identity category was not persisted: target=%+v err=%v", target, err)
	}
}

func TestReorganizationPlanNeverIncludesUnmanagedFiles(t *testing.T) {
	queue, _, download, _, _ := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	var library models.MediaLibrary
	var storage models.Storage
	if queue.db.First(&library, *download.TargetLibraryID).Error != nil || queue.db.First(&storage, library.StorageID).Error != nil {
		t.Fatal("missing fixture")
	}
	transfer := models.TransferTask{ID: "owned-transfer", LibraryID: library.ID}
	year, id := 2024, int64(1)
	source := MediaIdentitySnapshot{Version: 1, Revision: 1, MediaType: "movie", Title: "Old", Year: &year, Category: "电影"}
	target := source
	target.Title, target.TMDBID = "New", &id
	managed := []models.MediaManagedItem{{ID: 1, LibraryID: library.ID, TransferTaskID: transfer.ID, Kind: models.MediaManagedItemKindVideo, RelativePath: "old/old.mkv", Size: 10, Managed: true, Active: true}}
	plan, _, err := buildReorganizationPlan(library, storage, transfer, models.DownloadTask{}, source, target, managed, models.MediaLibraryConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || strings.Contains(plan.Items[0].OldRelativePath, "unmanaged") {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestReorganizationCloudTargetIsRecheckedAfterPreview(t *testing.T) {
	driver := newFakeMutationCloudDriver()
	driver.items["target"] = cloudpkg.Item{ID: "target", ParentID: "parent", Name: "Movie.mkv", Size: 10}
	if err := ensureReorganizationCloudTargetAvailable(context.Background(), driver, "parent", "Movie.mkv", "managed"); ErrorCode(err) != CodeReorganizationConflict {
		t.Fatalf("late conflict error=%v", err)
	}
	if err := ensureReorganizationCloudTargetAvailable(context.Background(), driver, "parent", "Movie.mkv", "target"); err != nil {
		t.Fatalf("idempotent current item rejected: %v", err)
	}
}

func TestCloudReorganizationSourceSHA1FailsClosedWhenProviderOmitsHash(t *testing.T) {
	item := reorganizationPlanItem{Size: 10, SourceSHA1: "EXPECTED"}
	if !cloudReorganizationSourceChanged(item, cloudpkg.Item{Size: 10}) {
		t.Fatal("missing provider SHA1 must invalidate a plan that captured a source SHA1")
	}
	if cloudReorganizationSourceChanged(item, cloudpkg.Item{Size: 10, SHA1: "expected"}) {
		t.Fatal("matching SHA1 should remain valid regardless of case")
	}
}

func TestMediaReorganizationPan115UsesStableManagedItemAndParentBoundary(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictRename, false)
	year, oldID := 2024, int64(550)
	identity := MediaIdentitySnapshot{Version: 1, Revision: 1, Source: mediaIdentitySourceAutomatic, Status: mediaIdentityStatusVerified, TMDBID: &oldID, MediaType: "movie", Title: "Movie", Year: &year, Category: "电影"}
	identityRaw, _ := json.Marshal(identity)
	if err := fixture.queue.db.Model(&models.DownloadTask{}).Where("id = ?", fixture.download.ID).Updates(map[string]any{"identity_revision": 1, "identity_snapshot_json": string(identityRaw), "identity_source": mediaIdentitySourceAutomatic, "identity_status": mediaIdentityStatusVerified}).Error; err != nil {
		t.Fatal(err)
	}
	fixture.download.IdentityRevision, fixture.download.IdentitySnapshotJSON = 1, string(identityRaw)
	if result := fixture.run(t); result.ErrorCode != "" {
		t.Fatalf("cloud transfer=%+v", result)
	}
	var transfer models.TransferTask
	var storage models.Storage
	var items []models.MediaManagedItem
	if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil || fixture.queue.db.First(&storage, fixture.library.StorageID).Error != nil || fixture.queue.db.Where("transfer_task_id = ?", transfer.ID).Find(&items).Error != nil {
		t.Fatal("missing cloud ownership boundary")
	}
	if len(items) != 1 || items[0].ProviderItemID == "" || items[0].ProviderParentID == "" {
		t.Fatalf("cloud ownership=%+v", items)
	}
	newID := int64(777)
	target := identity
	target.Revision, target.Source, target.Locked, target.TMDBID, target.Title = 2, mediaIdentitySourceManual, true, &newID, "Correct Cloud Movie"
	targetRaw, _ := json.Marshal(target)
	plan, _, err := buildReorganizationPlan(fixture.library, storage, transfer, fixture.download, identity, target, items, models.MediaLibraryConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	planRaw, _ := json.Marshal(plan)
	stateRaw, _ := json.Marshal(reorganizationState{Version: 1, Completed: map[uint]bool{}})
	taskID := "reorganization-pan115"
	_, err = fixture.queue.EnqueueWith(EnqueueJobInput{OwnerID: fixture.download.OwnerID, JobType: JobTypeMediaReorganization, DisplayName: "cloud correct", Payload: mediaReorganizationJobPayload{ReorganizationTaskID: taskID}}, func(tx *gorm.DB, job models.Job) error {
		return tx.Create(&models.MediaReorganizationTask{ID: taskID, OwnerID: fixture.download.OwnerID, JobID: job.ID, LibraryID: fixture.library.ID, TransferTaskID: transfer.ID, SourceIdentityRevision: 1, TargetIdentityRevision: 2, TargetIdentityJSON: string(targetRaw), ManagedManifestDigest: managedManifestDigest(items), RuleRevision: fixture.library.ProfileRevision, ConflictPolicy: models.MediaLibraryConflictRename, PlanJSON: string(planRaw), StateJSON: string(stateRaw), Phase: models.MediaReorganizationPhaseQueued, TotalItems: len(plan.Items), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.queue.Claim([]string{JobTypeMediaReorganization})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	service := NewMediaReorganizationService(fixture.queue.db, fixture.queue.audit, fixture.queue, nil, fixture.service.connections, zerolog.Nop())
	result := NewMediaReorganizationWorker(service).Run(context.Background(), workerRuntime{queue: fixture.queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" {
		t.Fatalf("cloud reorganization=%+v", result)
	}
	item := fixture.driver.items[items[0].ProviderItemID]
	if item.Name != "Correct Cloud Movie (2024).mkv" || item.ParentID == items[0].ProviderParentID || fixture.driver.moveCalls == 0 || fixture.driver.renameCalls == 0 {
		t.Fatalf("cloud item not reorganized safely: item=%+v moves=%d renames=%d", item, fixture.driver.moveCalls, fixture.driver.renameCalls)
	}
}
