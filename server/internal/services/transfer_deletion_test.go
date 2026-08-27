package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

func prepareOwnedPan115DeletionPackage(t *testing.T, fixture *cloudTransferFixture, transfer *models.TransferTask) {
	t.Helper()
	packageRootID := "owned-package-root"
	fixture.driver.items[packageRootID] = cloudpkg.Item{ID: packageRootID, ParentID: "source-root", Name: "omc-" + fixture.download.ID, IsDir: true}
	item := fixture.driver.items[fixture.sourceID]
	item.ParentID = packageRootID
	fixture.driver.items[fixture.sourceID] = item
	fixture.download.ProviderOutputID = packageRootID
	fixture.manifest.Files[0].ProviderParentID = packageRootID
	raw, err := json.Marshal(fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.queue.db.Model(&models.DownloadTask{}).Where("id = ?", fixture.download.ID).Update("provider_output_id", packageRootID).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.queue.db.Model(&models.TransferTask{}).Where("id = ?", transfer.ID).Update("source_manifest_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	transfer.SourceManifestJSON = string(raw)
}

func attachPan115DeletionDownloader(t *testing.T, fixture *cloudTransferFixture, client *stubDownloadClient) {
	t.Helper()
	now := time.Now().UTC()
	record := models.Downloader{ID: "deletion-pan115-" + fixture.download.ID, OwnerID: fixture.download.OwnerID, Name: "115 Offline", NameNormalized: "deletion-pan115-" + fixture.download.ID, Type: models.DownloaderTypePan115Offline, StorageID: fixture.download.StagingStorageID, ProviderDirectoryID: "source-root", Enabled: true, CapabilitiesJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := fixture.queue.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	registry := downloadpkg.NewRegistry()
	if err := registry.Register(models.DownloaderTypePan115Offline, downloadpkg.Capabilities{Cancel: true, NativeOffline: true}, func(downloadpkg.Config) (downloadpkg.Client, error) { return client, nil }); err != nil {
		t.Fatal(err)
	}
	downloader := NewDownloaderService(fixture.queue.db, fixture.queue.audit, nil, registry)
	downloader.SetConnectionService(fixture.service.connections)
	fixture.service.SetDownloaderService(downloader)
	fixture.download.DownloaderID = &record.ID
	fixture.download.ProviderTaskID = "offline-task-1"
	if err := fixture.queue.db.Model(&models.DownloadTask{}).Where("id = ?", fixture.download.ID).Updates(map[string]any{"downloader_id": record.ID, "provider_task_id": fixture.download.ProviderTaskID}).Error; err != nil {
		t.Fatal(err)
	}
}

func completedTransferForDeletion(t *testing.T) (*QueueService, Actor, *TransferService, models.DownloadTask, models.TransferTask, string, string) {
	t.Helper()
	queue, actor, download, source, destination := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := service.EnqueuePackage(download, manifest, manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	if err := queue.Complete(claimed.Job.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.MediaManagedItem{}).Where("transfer_task_id = ?", transfer.ID).Update("size", info.Size()).Error; err != nil {
		t.Fatal(err)
	}
	// The test fixture uses a tiny file while production manifests contain the
	// authoritative provider size. Keep the immutable source manifest aligned
	// for deletion-boundary validation.
	manifest.Files[0].Size = info.Size()
	raw, _ := json.Marshal(manifest)
	if err := queue.db.Model(&models.TransferTask{}).Where("id = ?", transfer.ID).Update("source_manifest_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	return queue, actor, service, download, transfer, source, destination
}

func TestTransferDeletionRecordOnlyPreservesBothFileSidesAndRejectsReplay(t *testing.T) {
	queue, actor, service, _, transfer, source, destination := completedTransferForDeletion(t)
	preview, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordOnly}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.RequiresFileDelete || preview.SourceItems != 0 || preview.LibraryItems != 0 || len(preview.ConfirmationToken) != 43 {
		t.Fatalf("preview=%+v", preview)
	}
	result, err := service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{})
	if err != nil || !result.Deleted || result.Scope != models.TransferDeletionScopeRecordOnly {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed: %v", err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("library changed: %v", err)
	}
	if _, err := service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeTransferDeletionPreviewExpired {
		t.Fatalf("replay err=%v", err)
	}
	var count int64
	if err := queue.db.Model(&models.DownloadTask{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("download history count=%d err=%v", count, err)
	}
}

func TestTransferDeletionLibraryScopeUsesOnlyManagedOwnership(t *testing.T) {
	queue, actor, service, _, transfer, source, destination := completedTransferForDeletion(t)
	unmanaged := strings.TrimSuffix(destination, ".mkv") + ".user.txt"
	if err := os.WriteFile(unmanaged, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndLibrary}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.LibraryItems != 1 || preview.SourceItems != 0 || !preview.RequiresFileDelete {
		t.Fatalf("preview=%+v", preview)
	}
	result, err := service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{})
	if err != nil || result.LibraryRemoved != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("managed library file remains: %v", err)
	}
	if _, err := os.Stat(unmanaged); err != nil {
		t.Fatalf("unmanaged sibling changed: %v", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source changed: %v", err)
	}
	var library models.MediaLibrary
	if err := queue.db.First(&library, transfer.LibraryID).Error; err != nil || library.DirtyGeneration == 0 || library.ContentRevision == 0 {
		t.Fatalf("library reconcile state=%+v err=%v", library, err)
	}
}

func TestTransferDeletionBothRemovesExactSourceAndManagedLibrary(t *testing.T) {
	queue, actor, service, download, transfer, source, destination := completedTransferForDeletion(t)
	if err := queue.db.Model(&models.Job{}).Where("id = ?", download.JobID).Update("status", models.JobStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordSourceAndLibrary}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.SourceItems != 1 || preview.LibraryItems != 1 {
		t.Fatalf("preview=%+v", preview)
	}
	result, err := service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{})
	if err != nil || result.SourceRemoved != 1 || result.LibraryRemoved != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("library remains: %v", err)
	}
	for label, model := range map[string]any{"download": &models.DownloadTask{}, "transfer": &models.TransferTask{}} {
		var count int64
		if err := queue.db.Model(model).Where("id = ?", map[string]string{"download": download.ID, "transfer": transfer.ID}[label]).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", label, count, err)
		}
	}
}

func TestTransferDeletionSourceScopeRemovesOnlySource(t *testing.T) {
	queue, actor, service, download, transfer, source, destination := completedTransferForDeletion(t)
	if err := queue.db.Model(&models.Job{}).Where("id = ?", download.JobID).Update("status", models.JobStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.SourceItems != 1 || preview.LibraryItems != 0 || preview.SourceStorageType != models.StorageTypeLocal || preview.LibraryStorageType != models.StorageTypeLocal {
		t.Fatalf("preview=%+v", preview)
	}
	result, err := service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{})
	if err != nil || result.SourceRemoved != 1 || result.LibraryRemoved != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("library changed: %v", err)
	}
}

func TestCanonicalLocalDeletionRootRejectsSymlinkOrJunction(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "staging-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := canonicalLocalDeletionRoot(link); err == nil {
		t.Fatal("symlink or junction staging root was accepted")
	}
}

func TestTransferDeletionSourceScopeBlocksActiveDownload(t *testing.T) {
	_, actor, service, _, transfer, _, _ := completedTransferForDeletion(t)
	if _, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{}); ErrorCode(err) != CodeQueueStateConflict {
		t.Fatalf("active download err=%v", err)
	}
}

func TestTransferDeletionRejectsBoundaryChangeAfterPreview(t *testing.T) {
	queue, actor, service, download, transfer, _, _ := completedTransferForDeletion(t)
	preview, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndLibrary}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", download.ID).Update("identity_revision", download.IdentityRevision+1).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeTransferDeletionBoundaryChanged {
		t.Fatalf("boundary err=%v", err)
	}
	if _, err := os.Stat(previewPathForTransfer(t, queue, transfer.ID)); err != nil {
		t.Fatalf("boundary change deleted file: %v", err)
	}
}

func TestTransferDeletionPan115LibraryScopeRecyclesStableManagedItem(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
	if result := fixture.run(t); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	var managed models.MediaManagedItem
	if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil || fixture.queue.db.Where("transfer_task_id = ?", transfer.ID).First(&managed).Error != nil {
		t.Fatal("missing 115 managed ownership")
	}
	if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", transfer.JobID).Update("status", models.JobStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
	preview, err := fixture.service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndLibrary}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.LibraryItems != 1 {
		t.Fatalf("preview=%+v", preview)
	}
	result, err := fixture.service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{})
	if err != nil || result.LibraryRemoved != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, exists := fixture.driver.items[managed.ProviderItemID]; exists {
		t.Fatalf("115 managed item %s remains", managed.ProviderItemID)
	}
	if _, exists := fixture.driver.items[fixture.sourceID]; !exists {
		t.Fatal("115 source item was deleted by library-only scope")
	}
}

func TestTransferDeletionPan115PartialFailureRetainsRemainingOwnership(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
	if result := fixture.run(t); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	var first models.MediaManagedItem
	if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil || fixture.queue.db.Where("transfer_task_id = ?", transfer.ID).First(&first).Error != nil {
		t.Fatal("missing first managed item")
	}
	if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", transfer.JobID).Update("status", models.JobStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	secondProvider := "managed-second"
	fixture.driver.items[secondProvider] = cloudpkg.Item{ID: secondProvider, ParentID: "library-root", Name: "Second.ass", Size: 20}
	second := models.MediaManagedItem{OpaqueID: "managed-second-opaque", LibraryID: transfer.LibraryID, TransferTaskID: transfer.ID, DownloadTaskID: transfer.DownloadTaskID, IdentityRevision: first.IdentityRevision, Kind: models.MediaManagedItemKindSidecar, RelativePath: "Second.ass", ProviderItemID: secondProvider, ProviderParentID: "library-root", Size: 20, Managed: true, Active: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := fixture.queue.db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	fixture.driver.recycleFailID = secondProvider
	actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
	preview, err := fixture.service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndLibrary}, RequestContext{})
	if err != nil || preview.LibraryItems != 2 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if _, err := fixture.service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeTransferDeletionPartial {
		t.Fatalf("partial err=%v", err)
	}
	var active, inactive int64
	if err := fixture.queue.db.Model(&models.MediaManagedItem{}).Where("transfer_task_id = ? AND active = ?", transfer.ID, true).Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.queue.db.Model(&models.MediaManagedItem{}).Where("transfer_task_id = ? AND active = ?", transfer.ID, false).Count(&inactive).Error; err != nil {
		t.Fatal(err)
	}
	if active != 1 || inactive != 1 {
		t.Fatalf("partial ownership active=%d inactive=%d", active, inactive)
	}
	var transferCount int64
	if err := fixture.queue.db.Model(&models.TransferTask{}).Where("id = ?", transfer.ID).Count(&transferCount).Error; err != nil || transferCount != 1 {
		t.Fatalf("transfer count=%d err=%v", transferCount, err)
	}
}

func TestTransferDeletionPan115SourceRecycleFailureWithUnavailableStatIsNotSuccess(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
	if result := fixture.run(t); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil {
		t.Fatal("missing transfer")
	}
	for _, jobID := range []string{transfer.JobID, fixture.download.JobID} {
		if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", jobID).Update("status", models.JobStatusCompleted).Error; err != nil {
			t.Fatal(err)
		}
	}
	actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
	preview, err := fixture.service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.driver.recycleFailID = fixture.download.ProviderOutputID
	fixture.driver.statFailAfterRecycleID = fixture.download.ProviderOutputID
	_, err = fixture.service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{})
	if ErrorCode(err) != CodeTransferDeletionPartial {
		t.Fatalf("confirm err=%v", err)
	}
	if _, exists := fixture.driver.items[fixture.sourceID]; !exists {
		t.Fatal("source disappeared after failed recycle")
	}
	var count int64
	if err := fixture.queue.db.Model(&models.TransferTask{}).Where("id = ?", transfer.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("transfer count=%d err=%v", count, err)
	}
}

func TestTransferDeletionRecordOnlyLargePan115ManifestUsesNoProviderCalls(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
	if result := fixture.run(t); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil {
		t.Fatal("missing transfer")
	}
	if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", transfer.JobID).Update("status", models.JobStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	files := make([]downloadpkg.File, 10_000)
	for index := range files {
		files[index] = downloadpkg.File{RelativePath: fmt.Sprintf("Season/file-%05d.mkv", index), Size: 1, ProviderItemID: fmt.Sprintf("item-%05d", index), ProviderParentID: "parent"}
	}
	raw, _ := json.Marshal(downloadpkg.Manifest{Name: "large", Complete: true, Files: files})
	if err := fixture.queue.db.Model(&models.TransferTask{}).Where("id = ?", transfer.ID).Update("source_manifest_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	client := &stubDownloadClient{}
	attachPan115DeletionDownloader(t, &fixture, client)
	fixture.driver.listCalls, fixture.driver.statCalls = 0, 0
	actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
	preview, err := fixture.service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordOnly}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	if fixture.driver.listCalls != 0 || fixture.driver.statCalls != 0 || client.cancelled {
		t.Fatalf("record-only called provider: list=%d stat=%d cancel=%v", fixture.driver.listCalls, fixture.driver.statCalls, client.cancelled)
	}
}

func TestTransferDeletionPan115MissingRootConvergesAndCancelsOfflineTask(t *testing.T) {
	for _, test := range []struct {
		name      string
		cancelErr error
	}{
		{name: "existing offline task"},
		{name: "offline task already missing", cancelErr: downloadpkg.Error("downloader_task_not_found", false, nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
			if result := fixture.run(t); result.ErrorCode != "" {
				t.Fatalf("transfer=%+v", result)
			}
			var transfer models.TransferTask
			if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil {
				t.Fatal("missing transfer")
			}
			for _, jobID := range []string{transfer.JobID, fixture.download.JobID} {
				if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", jobID).Update("status", models.JobStatusCompleted).Error; err != nil {
					t.Fatal(err)
				}
			}
			prepareOwnedPan115DeletionPackage(t, &fixture, &transfer)
			client := &stubDownloadClient{cancelErr: test.cancelErr}
			attachPan115DeletionDownloader(t, &fixture, client)
			delete(fixture.driver.items, fixture.download.ProviderOutputID)
			fixture.driver.recycled = nil
			actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
			preview, err := fixture.service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{})
			if err != nil || preview.SourceMissing != 1 {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			result, err := fixture.service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{})
			if err != nil || result.SourceRemoved != 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !client.cancelled || client.deleteData || len(fixture.driver.recycled) != 0 {
				t.Fatalf("cleanup cancel=%v delete_data=%v recycled=%v", client.cancelled, client.deleteData, fixture.driver.recycled)
			}
		})
	}
}

func TestTransferDeletionPan115RecyclesOnlyOwnedPackageRoot(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
	if result := fixture.run(t); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil {
		t.Fatal("missing transfer")
	}
	for _, jobID := range []string{transfer.JobID, fixture.download.JobID} {
		if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", jobID).Update("status", models.JobStatusCompleted).Error; err != nil {
			t.Fatal(err)
		}
	}
	prepareOwnedPan115DeletionPackage(t, &fixture, &transfer)
	client := &stubDownloadClient{}
	attachPan115DeletionDownloader(t, &fixture, client)
	fixture.driver.recycled = nil
	actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
	preview, err := fixture.service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{})
	if err != nil || preview.SourceDetached != 0 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	result, err := fixture.service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{})
	if err != nil || result.SourceRemoved != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !client.cancelled || client.deleteData || len(fixture.driver.recycled) != 1 || fixture.driver.recycled[0] != fixture.download.ProviderOutputID {
		t.Fatalf("cleanup cancel=%v delete_data=%v recycled=%v", client.cancelled, client.deleteData, fixture.driver.recycled)
	}
}

func TestTransferDeletionPan115PartialMovePreservesDetachedItems(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
	if result := fixture.run(t); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil {
		t.Fatal("missing transfer")
	}
	for _, jobID := range []string{transfer.JobID, fixture.download.JobID} {
		if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", jobID).Update("status", models.JobStatusCompleted).Error; err != nil {
			t.Fatal(err)
		}
	}
	prepareOwnedPan115DeletionPackage(t, &fixture, &transfer)
	packageRootID := fixture.download.ProviderOutputID
	fixture.driver.items["remaining-source"] = cloudpkg.Item{ID: "remaining-source", ParentID: packageRootID, Name: "remaining.mkv", Size: 2, SHA1: "REMAINING"}
	detached := fixture.driver.items[fixture.sourceID]
	detached.ParentID = "library-root"
	fixture.driver.items[fixture.sourceID] = detached
	fixture.manifest.Files = append(fixture.manifest.Files, downloadpkg.File{RelativePath: "remaining.mkv", Size: 2, ProviderItemID: "remaining-source", ProviderParentID: packageRootID, SHA1: "REMAINING"})
	raw, _ := json.Marshal(fixture.manifest)
	if err := fixture.queue.db.Model(&models.TransferTask{}).Where("id = ?", transfer.ID).Update("source_manifest_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	client := &stubDownloadClient{}
	attachPan115DeletionDownloader(t, &fixture, client)
	actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
	preview, err := fixture.service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{})
	if err != nil || preview.SourceDetached != 1 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	result, err := fixture.service.ConfirmDeletion(context.Background(), actor, transfer.ID, preview.ConfirmationToken, RequestContext{})
	if err != nil || result.SourceRemoved != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, exists := fixture.driver.items[fixture.sourceID]; !exists {
		t.Fatal("detached library-side item was recycled")
	}
	if _, exists := fixture.driver.items["remaining-source"]; exists {
		t.Fatal("remaining source item was not removed with its package root")
	}
}

func TestTransferDeletionPan115PreviewScalesWithParentDirectories(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
	if result := fixture.run(t); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil {
		t.Fatal("missing transfer")
	}
	for _, jobID := range []string{transfer.JobID, fixture.download.JobID} {
		if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", jobID).Update("status", models.JobStatusCompleted).Error; err != nil {
			t.Fatal(err)
		}
	}
	prepareOwnedPan115DeletionPackage(t, &fixture, &transfer)
	files := make([]downloadpkg.File, 0, 38)
	for parentIndex := 0; parentIndex < 5; parentIndex++ {
		parentID := fmt.Sprintf("source-parent-%d", parentIndex)
		fixture.driver.items[parentID] = cloudpkg.Item{ID: parentID, ParentID: fixture.download.ProviderOutputID, Name: fmt.Sprintf("Season %02d", parentIndex+1), IsDir: true}
		for fileIndex := parentIndex; fileIndex < 38; fileIndex += 5 {
			itemID := fmt.Sprintf("source-item-%02d", fileIndex)
			fixture.driver.items[itemID] = cloudpkg.Item{ID: itemID, ParentID: parentID, Name: fmt.Sprintf("episode-%02d.mkv", fileIndex), Size: int64(fileIndex + 1)}
			files = append(files, downloadpkg.File{RelativePath: fmt.Sprintf("Season %02d/episode-%02d.mkv", parentIndex+1, fileIndex), Size: int64(fileIndex + 1), ProviderItemID: itemID, ProviderParentID: parentID})
		}
	}
	raw, _ := json.Marshal(downloadpkg.Manifest{Name: "38-files", Complete: true, Files: files})
	if err := fixture.queue.db.Model(&models.TransferTask{}).Where("id = ?", transfer.ID).Update("source_manifest_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	fixture.driver.listCalls, fixture.driver.statCalls = 0, 0
	actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
	preview, err := fixture.service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{})
	if err != nil || preview.SourceItems != 38 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if fixture.driver.statCalls > 8 || fixture.driver.listCalls > 6 {
		t.Fatalf("preview did per-item provider work: stat=%d list=%d", fixture.driver.statCalls, fixture.driver.listCalls)
	}
}

func TestTransferDeletionUsesLiveLeaseInsteadOfStaleTransferPhase(t *testing.T) {
	queue, actor, service, _, transfer, _, _ := completedTransferForDeletion(t)
	future := time.Now().UTC().Add(time.Minute)
	if err := queue.db.Model(&models.Job{}).Where("id = ?", transfer.JobID).Updates(map[string]any{"lease_token_hash": "active", "lease_expires_at": future}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordOnly}, RequestContext{}); ErrorCode(err) != CodeQueueStateConflict {
		t.Fatalf("active lease err=%v", err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", transfer.JobID).Updates(map[string]any{"lease_token_hash": "", "lease_expires_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.TransferTask{}).Where("id = ?", transfer.ID).Update("phase", models.TransferTaskStatusTransferring).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordOnly}, RequestContext{}); err != nil {
		t.Fatalf("stale transfer phase blocked deletion: %v", err)
	}
}

func TestTransferDeletionBlocksTerminalDependentJobsWithActiveLeases(t *testing.T) {
	t.Run("reorganization", func(t *testing.T) {
		queue, actor, service, _, transfer, _, _ := completedTransferForDeletion(t)
		now, future := time.Now().UTC(), time.Now().UTC().Add(time.Minute)
		ownerID := actor.User.ID
		job := models.Job{ID: "reorganization-closing-job", OwnerID: &ownerID, JobType: JobTypeMediaReorganization, Revision: 1, Status: models.JobStatusCancelled, DisplayName: "closing reorganization", LeaseTokenHash: "active", LeaseExpiresAt: &future, CreatedAt: now, UpdatedAt: now}
		task := models.MediaReorganizationTask{ID: "reorganization-closing", OwnerID: ownerID, JobID: job.ID, LibraryID: transfer.LibraryID, TransferTaskID: transfer.ID, SourceIdentityRevision: 1, TargetIdentityRevision: 2, TargetIdentityJSON: `{}`, ManagedManifestDigest: strings.Repeat("d", 64), RuleRevision: 1, ConflictPolicy: models.MediaLibraryConflictRename, PlanJSON: `{}`, StateJSON: `{}`, Phase: models.MediaReorganizationPhaseCompleted, CreatedAt: now, UpdatedAt: now, FinishedAt: &now}
		if err := queue.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&job).Error; err != nil {
				return err
			}
			return tx.Create(&task).Error
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordOnly}, RequestContext{}); ErrorCode(err) != CodeQueueStateConflict {
			t.Fatalf("active reorganization lease err=%v", err)
		}
	})

	t.Run("seeding", func(t *testing.T) {
		queue, actor, service, download, transfer, _, _ := completedTransferForDeletion(t)
		now, future := time.Now().UTC(), time.Now().UTC().Add(time.Minute)
		ownerID := actor.User.ID
		if err := queue.db.Model(&models.Job{}).Where("id = ?", download.JobID).Update("status", models.JobStatusCancelled).Error; err != nil {
			t.Fatal(err)
		}
		job := models.Job{ID: "seeding-closing-job", OwnerID: &ownerID, JobType: "seeding", Revision: 1, Status: models.JobStatusCancelled, DisplayName: "closing seeding", LeaseTokenHash: "active", LeaseExpiresAt: &future, CreatedAt: now, UpdatedAt: now}
		task := models.SeedingTask{ID: "seeding-closing", OwnerID: ownerID, JobID: job.ID, DownloadTaskID: download.ID, DownloaderName: download.DownloaderName, ProviderType: download.ProviderType, ProviderTaskID: "provider", TransferMode: models.MediaLibraryTransferCopy, Phase: models.SeedingTaskStatusCompleted, CreatedAt: now, UpdatedAt: now, FinishedAt: &now}
		if err := queue.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&job).Error; err != nil {
				return err
			}
			return tx.Create(&task).Error
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{}); ErrorCode(err) != CodeQueueStateConflict {
			t.Fatalf("active seeding lease err=%v", err)
		}
	})
}

func TestTransferDeletionPan115ProviderFailuresDoNotConvergeAsMissing(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "authentication", err: cloudpkg.Error(cloudpkg.CodeAuthExpired, false, context.Canceled)},
		{name: "rate limited", err: cloudpkg.Error(cloudpkg.CodeRateLimited, true, context.Canceled)},
		{name: "timeout", err: cloudpkg.Error(cloudpkg.CodeUnavailable, true, context.DeadlineExceeded)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
			if result := fixture.run(t); result.ErrorCode != "" {
				t.Fatalf("transfer=%+v", result)
			}
			var transfer models.TransferTask
			if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil {
				t.Fatal("missing transfer")
			}
			for _, jobID := range []string{transfer.JobID, fixture.download.JobID} {
				if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", jobID).Update("status", models.JobStatusCompleted).Error; err != nil {
					t.Fatal(err)
				}
			}
			prepareOwnedPan115DeletionPackage(t, &fixture, &transfer)
			fixture.driver.statErrors[fixture.download.ProviderOutputID] = test.err
			actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
			if _, err := fixture.service.PreviewDeletion(context.Background(), actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{}); ErrorCode(err) != CodeTransferDeletionBoundaryChanged {
				t.Fatalf("provider failure converged: %v", err)
			}
		})
	}
}

func TestTransferDeletionHonorsCallerDeadline(t *testing.T) {
	fixture := newCloudTransferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, false)
	if result := fixture.run(t); result.ErrorCode != "" {
		t.Fatalf("transfer=%+v", result)
	}
	var transfer models.TransferTask
	if fixture.queue.db.Where("download_task_id = ?", fixture.download.ID).First(&transfer).Error != nil {
		t.Fatal("missing transfer")
	}
	for _, jobID := range []string{transfer.JobID, fixture.download.JobID} {
		if err := fixture.queue.db.Model(&models.Job{}).Where("id = ?", jobID).Update("status", models.JobStatusCompleted).Error; err != nil {
			t.Fatal(err)
		}
	}
	prepareOwnedPan115DeletionPackage(t, &fixture, &transfer)
	fixture.driver.statBlockID = fixture.download.ProviderOutputID
	actor := Actor{User: models.User{ID: fixture.download.OwnerID}, Permissions: map[string]struct{}{authz.PermissionJobsControlAll: {}}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := fixture.service.PreviewDeletion(ctx, actor, transfer.ID, TransferDeletionPreviewInput{Scope: models.TransferDeletionScopeRecordAndSource}, RequestContext{})
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("deadline err=%v elapsed=%s", err, time.Since(started))
	}
}

func previewPathForTransfer(t *testing.T, queue *QueueService, transferID string) string {
	t.Helper()
	var transfer models.TransferTask
	var library models.MediaLibrary
	var storage models.Storage
	var item models.MediaManagedItem
	if queue.db.First(&transfer, "id = ?", transferID).Error != nil || queue.db.First(&library, transfer.LibraryID).Error != nil || queue.db.First(&storage, library.StorageID).Error != nil || queue.db.Where("transfer_task_id = ?", transferID).First(&item).Error != nil {
		t.Fatal("missing transfer deletion fixture")
	}
	return storage.RootPath + string(os.PathSeparator) + filepathFromSlash(item.RelativePath)
}

func filepathFromSlash(value string) string {
	return strings.ReplaceAll(value, "/", string(os.PathSeparator))
}
