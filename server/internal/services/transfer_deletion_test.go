package services

import (
	"context"
	"encoding/json"
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
)

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
	fixture.driver.recycleFailID = fixture.sourceID
	fixture.driver.statFailAfterRecycleID = fixture.sourceID
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
