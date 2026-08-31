package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

func configurePan115CleanupPackage(t *testing.T, fixture *cloudTransferFixture, withListener bool) {
	t.Helper()
	stagingRootID := "cleanup-storage-root"
	packageParentID := stagingRootID
	fixture.driver.items[stagingRootID] = cloudpkg.Item{ID: stagingRootID, ParentID: "0", Name: "staging", IsDir: true}
	listenerRootID := ""
	if withListener {
		listenerRootID = "cleanup-listener-root"
		packageParentID = listenerRootID
		fixture.driver.items[listenerRootID] = cloudpkg.Item{ID: listenerRootID, ParentID: stagingRootID, Name: "listener", IsDir: true}
	}
	packageRoot := fixture.driver.items[fixture.download.ProviderOutputID]
	packageRoot.ParentID = packageParentID
	fixture.driver.items[packageRoot.ID] = packageRoot
	fixture.download.StagingProviderDirectoryID = listenerRootID
	if err := fixture.queue.db.Model(&models.Storage{}).Where("id = ?", *fixture.download.StagingStorageID).Updates(map[string]any{"root_path": stagingRootID, "root_path_normalized": "pan115:" + stagingRootID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.queue.db.Model(&models.DownloadTask{}).Where("id = ?", fixture.download.ID).Update("staging_provider_directory_id", listenerRootID).Error; err != nil {
		t.Fatal(err)
	}
}

func markPan115TransferCompleted(t *testing.T, fixture cloudTransferFixture, task *models.TransferTask) {
	t.Helper()
	task.Phase = models.TransferTaskStatusCompleted
	if err := fixture.queue.db.Model(&models.TransferTask{}).Where("id = ?", task.ID).Update("phase", models.TransferTaskStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
}

type cleanupReconcileFailureDriver struct {
	*fakeMutationCloudDriver
	failAfterRecycleID string
}

func (d *cleanupReconcileFailureDriver) Recycle(ctx context.Context, itemID string) error {
	err := d.fakeMutationCloudDriver.Recycle(ctx, itemID)
	if err == nil && itemID == d.failAfterRecycleID {
		d.statFailID = itemID
	}
	return err
}

func enqueueCompletedPan115CleanupTask(t *testing.T, fixture cloudTransferFixture) models.TransferTask {
	t.Helper()
	if err := fixture.service.EnqueuePackage(fixture.download, fixture.manifest, fixture.manifest); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	markPan115TransferCompleted(t, fixture, &task)
	return task
}

func movePan115SelectedMediaOutOfPackage(t *testing.T, fixture cloudTransferFixture) {
	t.Helper()
	item := fixture.driver.items[fixture.sourceID]
	item.ParentID = "library-root"
	fixture.driver.items[item.ID] = item
}

func TestTransferCleanupDeletesOnlyClearlyNonMediaLocalManifestFiles(t *testing.T) {
	queue, _, download, selectedPath, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	advertisementPath := filepath.Join(download.StagingAbsolutePath, download.ScrapeCategory, "广告", "promo.txt")
	if err := os.MkdirAll(filepath.Dir(advertisementPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(advertisementPath, []byte("spam"), 0o600); err != nil {
		t.Fatal(err)
	}
	const mainVideoSize = int64(57) * (1 << 30) / 2 // 28.5 GiB provider manifest entry.
	selected := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: mainVideoSize}}}
	source := selected
	source.Files = append(append([]downloadpkg.File{}, selected.Files...), downloadpkg.File{RelativePath: "广告/promo.txt", Size: 4})
	if err := service.EnqueuePackage(download, selected, source); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	removed, err := service.cleanupTransferStaging(context.Background(), task, download)
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(advertisementPath); !os.IsNotExist(err) {
		t.Fatalf("advertisement was not removed: %v", err)
	}
	if _, err := os.Stat(selectedPath); err != nil {
		t.Fatalf("selected video changed: %v", err)
	}
	if err := queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupCompleted || task.CleanupRemoved != 1 {
		t.Fatalf("cleanup task=%+v err=%v", task, err)
	}
}

func TestTransferCleanupRetainsUnselectedVideosAndUnmatchedSubtitles(t *testing.T) {
	queue, _, download, selectedPath, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	protected := []string{
		"extras/possible-main.mp4",
		"extras/unmatched.srt",
		"extras/unmatched.vtt",
		"extras/unmatched.sub",
		"extras/unmatched.idx",
		"extras/unmatched.sup",
	}
	for _, relative := range protected {
		path := filepath.Join(download.StagingAbsolutePath, download.ScrapeCategory, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	junkRelative := "extras/tracker-url.txt"
	junkPath := filepath.Join(download.StagingAbsolutePath, download.ScrapeCategory, filepath.FromSlash(junkRelative))
	if err := os.WriteFile(junkPath, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected := downloadpkg.Manifest{Complete: true, Files: []downloadpkg.File{{RelativePath: filepath.Base(selectedPath), Size: minimumAutomaticTransferVideoBytes}}}
	source := selected
	source.Files = append([]downloadpkg.File{}, selected.Files...)
	for _, relative := range protected {
		source.Files = append(source.Files, downloadpkg.File{RelativePath: relative, Size: 4})
	}
	source.Files = append(source.Files, downloadpkg.File{RelativePath: junkRelative, Size: 4})
	if err := service.EnqueuePackage(download, selected, source); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	plan, err := buildTransferCleanupPlan(task)
	if err != nil || len(plan.Removable) != 1 || plan.ProtectedCount != len(protected) {
		t.Fatalf("cleanup plan=%+v err=%v", plan, err)
	}
	removed, err := service.cleanupTransferStaging(context.Background(), task, download)
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(junkPath); !os.IsNotExist(err) {
		t.Fatalf("non-media junk was not removed: %v", err)
	}
	for _, relative := range protected {
		path := filepath.Join(download.StagingAbsolutePath, download.ScrapeCategory, filepath.FromSlash(relative))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected file %q changed: %v", relative, err)
		}
	}
}

func TestTransferCleanupRefusesIncompleteOrNonSubsetManifest(t *testing.T) {
	selected := downloadpkg.Manifest{Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	source := downloadpkg.Manifest{Complete: true, Files: []downloadpkg.File{{RelativePath: "Different.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	selectedJSON, _ := json.Marshal(selected)
	sourceJSON, _ := json.Marshal(source)
	if extras, err := transferCleanupDifference(models.TransferTask{ManifestJSON: string(selectedJSON), SourceManifestJSON: string(sourceJSON)}); err == nil || extras != nil {
		t.Fatalf("non-subset cleanup accepted: extras=%+v err=%v", extras, err)
	}
	source.Complete = false
	sourceJSON, _ = json.Marshal(source)
	if extras, err := transferCleanupDifference(models.TransferTask{ManifestJSON: string(selectedJSON), SourceManifestJSON: string(sourceJSON)}); err == nil || extras != nil {
		t.Fatalf("partial source cleanup accepted: extras=%+v err=%v", extras, err)
	}
}

func TestTransferCleanupUsesOriginalStagingCategoryAfterRecognitionRecovery(t *testing.T) {
	_, _, download, _, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	download.StagingCategory = "未识别"
	download.ScrapeCategory = "华语电影"
	extra := downloadpkg.File{RelativePath: "广告/promo.txt", Size: 5}
	extraPath := filepath.Join(download.StagingAbsolutePath, download.StagingCategory, filepath.FromSlash(extra.RelativePath))
	if err := os.MkdirAll(filepath.Dir(extraPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extraPath, []byte("promo"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed, err := cleanupLocalStaging(download, []downloadpkg.File{extra})
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(extraPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging extra still exists: %v", err)
	}
}

func TestTransferCleanupRefusesUnsafeOrChangedManifestIdentity(t *testing.T) {
	for _, relative := range []string{"../escape.txt", "/absolute.txt", `C:\\absolute.txt`, `\\\\server\\share\\file.txt`, "folder/../file.txt", "folder//file.txt", " file.txt"} {
		if key := transferCleanupFileKey(downloadpkg.File{RelativePath: relative, Size: 1}); key != "" {
			t.Fatalf("unsafe cleanup identity %q produced key %q", relative, key)
		}
	}
	selected := downloadpkg.Manifest{Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	source := selected
	source.Files = append([]downloadpkg.File(nil), selected.Files...)
	source.Files[0].Size++
	selectedJSON, _ := json.Marshal(selected)
	sourceJSON, _ := json.Marshal(source)
	if _, err := transferCleanupDifference(models.TransferTask{ManifestJSON: string(selectedJSON), SourceManifestJSON: string(sourceJSON)}); err == nil {
		t.Fatal("cleanup accepted selected/source identity with different size")
	}
}

func TestTransferCleanupRetriesChangedLocalFileAndAccumulatesCount(t *testing.T) {
	queue, _, download, selectedPath, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	first := filepath.Join(download.StagingAbsolutePath, download.ScrapeCategory, "extras", "first.txt")
	second := filepath.Join(download.StagingAbsolutePath, download.ScrapeCategory, "extras", "second.txt")
	if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected := downloadpkg.Manifest{Complete: true, Files: []downloadpkg.File{{RelativePath: filepath.Base(selectedPath), Size: minimumAutomaticTransferVideoBytes}}}
	source := selected
	source.Files = append(append([]downloadpkg.File{}, selected.Files...), downloadpkg.File{RelativePath: "extras/first.txt", Size: 3}, downloadpkg.File{RelativePath: "extras/second.txt", Size: 3})
	if err := service.EnqueuePackage(download, selected, source); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if removed, err := service.cleanupTransferStaging(context.Background(), task, download); err == nil || removed != 1 {
		t.Fatalf("first cleanup removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("first extra was not removed: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("changed extra should be retained: %v", err)
	}
	if err := os.WriteFile(second, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if removed, err := service.cleanupTransferStaging(context.Background(), task, download); err != nil || removed != 1 {
		t.Fatalf("retry cleanup removed=%d err=%v", removed, err)
	}
	if err := queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupCompleted || task.CleanupRemoved != 2 {
		t.Fatalf("cleanup retry task=%+v err=%v", task, err)
	}
}

func TestTransferCleanupRecyclesOnlyExactPan115Extras(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	configurePan115CleanupPackage(t, &fixture, true)
	fixture.driver.items["advertisement"] = cloudItem("advertisement", "source-root", "promo.txt", 4, "AD-SHA1")
	selected := fixture.manifest
	source := selected
	source.Files = append(append([]downloadpkg.File{}, selected.Files...), downloadpkg.File{RelativePath: "promo.txt", Size: 4, ProviderItemID: "advertisement", ProviderParentID: "source-root", SHA1: "AD-SHA1"})
	if err := fixture.service.EnqueuePackage(fixture.download, selected, source); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	markPan115TransferCompleted(t, fixture, &task)
	removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download)
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if len(fixture.driver.recycled) != 1 || fixture.driver.recycled[0] != "advertisement" {
		t.Fatalf("recycled=%v", fixture.driver.recycled)
	}
	if fixture.driver.recycleBatchCalls != 1 {
		t.Fatalf("recycle batch calls=%d want=1", fixture.driver.recycleBatchCalls)
	}
	if _, exists := fixture.driver.items[fixture.sourceID]; !exists {
		t.Fatal("selected provider media was recycled")
	}
}

func TestTransferCleanupPan115BatchesByProvenSourceParent(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	configurePan115CleanupPackage(t, &fixture, true)
	extras := make([]downloadpkg.File, 0, 10)
	for parentIndex := 0; parentIndex < 4; parentIndex++ {
		parentID := "cleanup-parent-" + strconv.Itoa(parentIndex)
		fixture.driver.items[parentID] = cloudpkg.Item{ID: parentID, ParentID: "source-root", Name: "extras", IsDir: true}
	}
	for index := 0; index < 10; index++ {
		parentID := "cleanup-parent-" + strconv.Itoa(index%4)
		itemID := "cleanup-item-" + strconv.Itoa(index)
		fixture.driver.items[itemID] = cloudItem(itemID, parentID, "extra.txt", int64(10+index), "CLEAN-"+strconv.Itoa(index))
		extras = append(extras, downloadpkg.File{RelativePath: "extras/extra-" + strconv.Itoa(index) + ".txt", ProviderItemID: itemID, ProviderParentID: parentID, Size: int64(10 + index), SHA1: "CLEAN-" + strconv.Itoa(index)})
	}
	removed, err := fixture.service.cleanupCloudStaging(context.Background(), fixture.download, extras)
	if err != nil || removed != len(extras) {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if fixture.driver.recycleBatchCalls != 4 {
		t.Fatalf("recycle batch calls=%d want=4", fixture.driver.recycleBatchCalls)
	}
	// One Storage root, one listener root, one package root and four unique
	// source parents; never one Stat per item.
	if fixture.driver.statCalls > 7 {
		t.Fatalf("cleanup boundary proof regressed to per-item stats: %d", fixture.driver.statCalls)
	}
}

func TestTransferCleanupPan115RetainsProtectedManifestItems(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	configurePan115CleanupPackage(t, &fixture, true)
	fixture.driver.items["alternate-video"] = cloudItem("alternate-video", "source-root", "alternate.mkv", 4, "VIDEO-SHA1")
	fixture.driver.items["unmatched-subtitle"] = cloudItem("unmatched-subtitle", "source-root", "subtitle.vtt", 4, "SUB-SHA1")
	fixture.driver.items["junk"] = cloudItem("junk", "source-root", "tracker.txt", 4, "JUNK-SHA1")
	selected := fixture.manifest
	source := selected
	source.Files = append(append([]downloadpkg.File{}, selected.Files...),
		downloadpkg.File{RelativePath: "alternate.mkv", Size: 4, ProviderItemID: "alternate-video", ProviderParentID: "source-root", SHA1: "VIDEO-SHA1"},
		downloadpkg.File{RelativePath: "subtitle.vtt", Size: 4, ProviderItemID: "unmatched-subtitle", ProviderParentID: "source-root", SHA1: "SUB-SHA1"},
		downloadpkg.File{RelativePath: "tracker.txt", Size: 4, ProviderItemID: "junk", ProviderParentID: "source-root", SHA1: "JUNK-SHA1"},
	)
	if err := fixture.service.EnqueuePackage(fixture.download, selected, source); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	markPan115TransferCompleted(t, fixture, &task)
	removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download)
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if len(fixture.driver.recycled) != 1 || fixture.driver.recycled[0] != "junk" {
		t.Fatalf("recycled=%v", fixture.driver.recycled)
	}
	for _, id := range []string{"alternate-video", "unmatched-subtitle"} {
		if _, exists := fixture.driver.items[id]; !exists {
			t.Fatalf("protected 115 item %q was recycled", id)
		}
	}
}

func TestTransferCleanupPan115RequiresExactPackageRoot(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	configurePan115CleanupPackage(t, &fixture, true)
	fixture.driver.items["other-package"] = cloudItem("other-package", "0", "other", 0, "")
	fixture.driver.items["outside-junk"] = cloudItem("outside-junk", "other-package", "tracker.txt", 4, "JUNK-SHA1")
	selected := fixture.manifest
	source := selected
	source.Files = append(append([]downloadpkg.File{}, selected.Files...), downloadpkg.File{RelativePath: "tracker.txt", Size: 4, ProviderItemID: "outside-junk", ProviderParentID: "other-package", SHA1: "JUNK-SHA1"})
	if err := fixture.service.EnqueuePackage(fixture.download, selected, source); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	markPan115TransferCompleted(t, fixture, &task)
	if removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download); err == nil || removed != 0 {
		t.Fatalf("outside-package cleanup removed=%d err=%v", removed, err)
	}
	if len(fixture.driver.recycled) != 0 {
		t.Fatalf("outside-package item was recycled: %v", fixture.driver.recycled)
	}
	fixture.download.ProviderOutputID = ""
	if removed, err := fixture.service.cleanupCloudStaging(context.Background(), fixture.download, []downloadpkg.File{{RelativePath: "tracker.txt", Size: 4, ProviderItemID: "outside-junk"}}); err == nil || removed != 0 {
		t.Fatalf("missing package root cleanup removed=%d err=%v", removed, err)
	}
}

func TestTransferCleanupPan115RecyclesEmptyAdoptedPackageWithNoExtras(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	configurePan115CleanupPackage(t, &fixture, true)
	movePan115SelectedMediaOutOfPackage(t, fixture)
	task := enqueueCompletedPan115CleanupTask(t, fixture)

	removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download)
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if len(fixture.driver.recycled) != 1 || fixture.driver.recycled[0] != fixture.download.ProviderOutputID {
		t.Fatalf("recycled=%v", fixture.driver.recycled)
	}
	for _, protectedID := range []string{"cleanup-storage-root", "cleanup-listener-root"} {
		if _, exists := fixture.driver.items[protectedID]; !exists {
			t.Fatalf("protected root %q was recycled", protectedID)
		}
	}
	if fixture.driver.listCalls != 1 || fixture.driver.statCalls < 5 {
		t.Fatalf("empty-package proof/list/reconcile calls stat=%d list=%d", fixture.driver.statCalls, fixture.driver.listCalls)
	}
	if err := fixture.queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupCompleted || task.CleanupRemoved != 1 || task.CleanupErrorCode != "" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
}

func TestTransferCleanupPan115KeepsNonEmptyAndConvergesMissingPackage(t *testing.T) {
	t.Run("non-empty", func(t *testing.T) {
		fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
		configurePan115CleanupPackage(t, &fixture, true)
		task := enqueueCompletedPan115CleanupTask(t, fixture)
		removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download)
		if err != nil || removed != 0 {
			t.Fatalf("removed=%d err=%v", removed, err)
		}
		if len(fixture.driver.recycled) != 0 {
			t.Fatalf("non-empty package recycled=%v", fixture.driver.recycled)
		}
		if _, exists := fixture.driver.items[fixture.download.ProviderOutputID]; !exists {
			t.Fatal("non-empty package root disappeared")
		}
	})

	t.Run("already-missing", func(t *testing.T) {
		fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
		configurePan115CleanupPackage(t, &fixture, true)
		delete(fixture.driver.items, fixture.sourceID)
		delete(fixture.driver.items, fixture.download.ProviderOutputID)
		task := enqueueCompletedPan115CleanupTask(t, fixture)
		removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download)
		if err != nil || removed != 0 {
			t.Fatalf("removed=%d err=%v", removed, err)
		}
		if len(fixture.driver.recycled) != 0 {
			t.Fatalf("missing package recycled again=%v", fixture.driver.recycled)
		}
		if err := fixture.queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupCompleted || task.CleanupRemoved != 0 {
			t.Fatalf("task=%+v err=%v", task, err)
		}
	})
}

func TestTransferCleanupPan115RejectsProtectedAndOutsidePackageRoots(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *cloudTransferFixture)
	}{
		{name: "storage root", setup: func(t *testing.T, fixture *cloudTransferFixture) {
			movePan115SelectedMediaOutOfPackage(t, *fixture)
			if err := fixture.queue.db.Model(&models.Storage{}).Where("id = ?", *fixture.download.StagingStorageID).Updates(map[string]any{"root_path": fixture.download.ProviderOutputID, "root_path_normalized": "pan115:" + fixture.download.ProviderOutputID}).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{name: "listener root", setup: func(t *testing.T, fixture *cloudTransferFixture) {
			configurePan115CleanupPackage(t, fixture, true)
			movePan115SelectedMediaOutOfPackage(t, *fixture)
			fixture.download.StagingProviderDirectoryID = fixture.download.ProviderOutputID
		}},
		{name: "outside listener", setup: func(t *testing.T, fixture *cloudTransferFixture) {
			configurePan115CleanupPackage(t, fixture, true)
			movePan115SelectedMediaOutOfPackage(t, *fixture)
			root := fixture.driver.items[fixture.download.ProviderOutputID]
			root.ParentID = "0"
			fixture.driver.items[root.ID] = root
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
			test.setup(t, &fixture)
			if err := fixture.queue.db.Model(&models.DownloadTask{}).Where("id = ?", fixture.download.ID).Update("staging_provider_directory_id", fixture.download.StagingProviderDirectoryID).Error; err != nil {
				t.Fatal(err)
			}
			task := enqueueCompletedPan115CleanupTask(t, fixture)
			removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download)
			if err == nil || removed != 0 || transferStagingCleanupCode(err) != "download_staging_package_boundary_invalid" {
				t.Fatalf("removed=%d code=%s err=%v", removed, transferStagingCleanupCode(err), err)
			}
			if len(fixture.driver.recycled) != 0 {
				t.Fatalf("unsafe root recycled=%v", fixture.driver.recycled)
			}
			if err := fixture.queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupFailed || task.CleanupErrorCode != "download_staging_package_boundary_invalid" || task.CleanupRemoved != 0 {
				t.Fatalf("task=%+v err=%v", task, err)
			}
		})
	}
}

func TestTransferCleanupPan115DoesNotRecyclePackageBeforeTransferCompletion(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	configurePan115CleanupPackage(t, &fixture, true)
	movePan115SelectedMediaOutOfPackage(t, fixture)
	if err := fixture.service.EnqueuePackage(fixture.download, fixture.manifest, fixture.manifest); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download)
	if err == nil || removed != 0 || transferStagingCleanupCode(err) != "download_staging_package_not_completed" {
		t.Fatalf("removed=%d code=%s err=%v", removed, transferStagingCleanupCode(err), err)
	}
	if len(fixture.driver.recycled) != 0 {
		t.Fatalf("incomplete transfer recycled=%v", fixture.driver.recycled)
	}
}

func TestTransferCleanupPan115RetriesRecycleAndReconcileFailures(t *testing.T) {
	t.Run("recycle failure", func(t *testing.T) {
		fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
		configurePan115CleanupPackage(t, &fixture, true)
		movePan115SelectedMediaOutOfPackage(t, fixture)
		task := enqueueCompletedPan115CleanupTask(t, fixture)
		fixture.driver.recycleFailID = fixture.download.ProviderOutputID

		if removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download); err == nil || removed != 0 || transferStagingCleanupCode(err) != "download_staging_package_recycle_failed" {
			t.Fatalf("first removed=%d code=%s err=%v", removed, transferStagingCleanupCode(err), err)
		}
		fixture.driver.recycleFailID = ""
		if removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download); err != nil || removed != 1 {
			t.Fatalf("retry removed=%d err=%v", removed, err)
		}
		if len(fixture.driver.recycled) != 1 {
			t.Fatalf("recycled=%v", fixture.driver.recycled)
		}
		if err := fixture.queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupCompleted || task.CleanupRemoved != 1 {
			t.Fatalf("task=%+v err=%v", task, err)
		}
	})

	t.Run("reconcile failure after recycle", func(t *testing.T) {
		fixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
		configurePan115CleanupPackage(t, &fixture, true)
		movePan115SelectedMediaOutOfPackage(t, fixture)
		task := enqueueCompletedPan115CleanupTask(t, fixture)
		wrapped := &cleanupReconcileFailureDriver{fakeMutationCloudDriver: fixture.driver, failAfterRecycleID: fixture.download.ProviderOutputID}
		fixture.service.connections.drivers[fixture.connection] = wrapped

		if removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download); err == nil || removed != 0 || transferStagingCleanupCode(err) != "download_staging_package_reconcile_failed" {
			t.Fatalf("first removed=%d code=%s err=%v", removed, transferStagingCleanupCode(err), err)
		}
		fixture.driver.statFailID = ""
		if removed, err := fixture.service.cleanupTransferStaging(context.Background(), task, fixture.download); err != nil || removed != 0 {
			t.Fatalf("retry removed=%d err=%v", removed, err)
		}
		if len(fixture.driver.recycled) != 1 {
			t.Fatalf("package recycle repeated=%v", fixture.driver.recycled)
		}
		if err := fixture.queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupCompleted || task.CleanupRemoved != 0 {
			t.Fatalf("task=%+v err=%v", task, err)
		}
	})
}

func TestTransferCleanupDefersQBittorrentCopyUntilSeedingCleanup(t *testing.T) {
	queue, actor, download, selectedPath, _ := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	extraPath := filepath.Join(download.StagingAbsolutePath, download.ScrapeCategory, "promo.txt")
	if err := os.WriteFile(extraPath, []byte("spam"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected := downloadpkg.Manifest{Complete: true, Files: []downloadpkg.File{{RelativePath: filepath.Base(selectedPath), Size: minimumAutomaticTransferVideoBytes}}}
	source := selected
	source.Files = append(append([]downloadpkg.File{}, selected.Files...), downloadpkg.File{RelativePath: "promo.txt", Size: 4})
	if err := service.EnqueuePackage(download, selected, source); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&task).Update("phase", models.TransferTaskStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	subscription := models.FollowSubscription{ID: "follow-transfer-terminal-sync", OwnerID: actor.User.ID, MediaType: "tv", TMDBID: 100, Title: "Transfer sync", Status: models.FollowStatusActive, Revision: 1, LifecycleRevision: 1, ExecutionSnapshotJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&subscription).Error; err != nil {
		t.Fatal(err)
	}
	claim := models.FollowEpisodeClaim{SubscriptionID: subscription.ID, SeasonNumber: 1, EpisodeNumber: 1, State: "downloading", DownloadTaskID: &download.ID, ResourceFingerprint: "transfer-sync", UpdatedAt: now}
	if err := queue.db.Create(&claim).Error; err != nil {
		t.Fatal(err)
	}
	task.Phase = models.TransferTaskStatusCompleted
	if result := NewTransferWorker(service).finishCompletedTransfer(context.Background(), task); result.ErrorCode != "" || result.RetryAt != nil {
		t.Fatalf("finish result=%+v", result)
	}
	if err := queue.db.First(&claim, "subscription_id = ? AND season_number = ? AND episode_number = ?", subscription.ID, 1, 1).Error; err != nil || claim.State != "imported" {
		t.Fatalf("completed follow claim=%+v err=%v", claim, err)
	}
	if err := queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupDeferred {
		t.Fatalf("deferred task=%+v err=%v", task, err)
	}
	for _, path := range []string{selectedPath, extraPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("seeding source changed before provider cleanup %q: %v", path, err)
		}
	}
	if err := service.CleanupAfterSeeding(context.Background(), download.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupCompleted || task.CleanupRemoved != 1 {
		t.Fatalf("completed task=%+v err=%v", task, err)
	}
}

func TestFinishCompletedTransferDoesNotAdvanceFollowOrCleanupAfterPipelineCancel(t *testing.T) {
	queue, actor, download, _, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := queue.db.First(&task, "download_task_id = ?", download.ID).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	subscription := models.FollowSubscription{ID: "follow-transfer-cancelled", OwnerID: actor.User.ID, MediaType: "tv", TMDBID: 101, Title: "Cancelled transfer", Status: models.FollowStatusActive, Revision: 1, LifecycleRevision: 1, ExecutionSnapshotJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&subscription).Error; err != nil {
		t.Fatal(err)
	}
	claim := models.FollowEpisodeClaim{SubscriptionID: subscription.ID, SeasonNumber: 1, EpisodeNumber: 1, State: "downloading", DownloadTaskID: &download.ID, ResourceFingerprint: "cancelled-transfer", UpdatedAt: now}
	if err := queue.db.Create(&claim).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", download.ID).Update("phase", models.DownloadTaskStatusCancelled).Error; err != nil {
		t.Fatal(err)
	}
	task.Phase = models.TransferTaskStatusCompleted
	if result := NewTransferWorker(service).finishCompletedTransfer(context.Background(), task); result.ErrorCode != "" || result.RetryAt != nil {
		t.Fatalf("cancelled finish result=%+v", result)
	}
	if err := queue.db.First(&claim, "subscription_id = ? AND season_number = ? AND episode_number = ?", subscription.ID, 1, 1).Error; err != nil || claim.State != "downloading" {
		t.Fatalf("cancelled follow claim=%+v err=%v", claim, err)
	}
	if err := queue.db.First(&task, "id = ?", task.ID).Error; err != nil || task.CleanupStatus != models.TransferCleanupPending {
		t.Fatalf("cancelled cleanup task=%+v err=%v", task, err)
	}
}

func cloudItem(id, parentID, name string, size int64, sha1 string) cloudpkg.Item {
	return cloudpkg.Item{ID: id, ParentID: parentID, Name: name, Size: size, SHA1: sha1}
}
