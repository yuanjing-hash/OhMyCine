package services

import (
	"context"
	"errors"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

func (w *TransferWorker) runCloudUpload(ctx context.Context, runtime JobRuntime, task models.TransferTask, download models.DownloadTask, manifest downloadpkg.Manifest, started time.Time) WorkerResult {
	if w.service.connections == nil || download.TargetConnectionID == nil || download.TargetStorageID == nil || strings.TrimSpace(download.TargetProviderRootID) == "" || download.ProviderType != models.DownloaderTypePluginHTTP {
		return w.cloudFailure(task, cloudTransferError("cloud_upload_snapshot_invalid", false, nil))
	}
	if download.TransferMode != models.MediaLibraryTransferMove && download.TransferMode != models.MediaLibraryTransferCopy {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_mode_invalid", false, nil))
	}
	connection, driver, err := w.service.connections.driver(*download.TargetConnectionID)
	if err != nil {
		return w.cloudFailure(task, err)
	}
	uploader, uploadOK := driver.(cloudpkg.UploadDriver)
	mutations, mutationOK := driver.(cloudpkg.MutationDriver)
	capabilities := driver.Capabilities()
	if !uploadOK || !mutationOK || connection.Provider != cloudpkg.ProviderPan115 || !capabilities.FileUpload || !capabilities.CreateDirectory || !capabilities.Recycle {
		return w.cloudFailure(task, cloudTransferError("cloud_upload_capability_missing", false, nil))
	}
	var targetStorage models.Storage
	if err := w.service.db.First(&targetStorage, *download.TargetStorageID).Error; err != nil || targetStorage.Type != models.StorageTypePan115 || targetStorage.ConnectionID == nil || *targetStorage.ConnectionID != *download.TargetConnectionID {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_boundary_invalid", false, err))
	}
	targetRoot, err := providerItemWithinRoot(ctx, driver, download.TargetProviderRootID, targetStorage.RootPath)
	if err != nil || !targetRoot.IsDir {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_boundary_invalid", false, err))
	}
	targets, err := buildTransferTargets(download, manifest)
	if err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_plan_invalid", false, err))
	}
	state, err := decodeCloudTransferState(task.CloudStateJSON)
	if err != nil {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_state_invalid", false, err))
	}
	state.Directories["."] = targetRoot.ID
	if err := w.persistCloudState(&task, state, models.TransferTaskStatusPlanning, completedCloudItems(state), nil); err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}
	summaryPlan := make([]transferPlanItem, 0, len(targets))
	for _, target := range targets {
		summaryPlan = append(summaryPlan, transferPlanItem{Relative: target.Relative, Size: target.File.Size, Group: target.Group})
	}
	validatedDirectories := map[string]struct{}{".": {}}
	for _, directory := range uniqueCloudTargetDirectories(targets) {
		if _, err := w.ensureCloudDirectory(ctx, mutations, &task, &state, directory, validatedDirectories); err != nil {
			return w.cloudFailure(task, err)
		}
	}
	summary, err := newTransferPlanSummary(summaryPlan)
	if err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_plan_invalid", false, err))
	}
	encodedSummary, err := encodeTransferPlanSummary(summary)
	if err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_plan_invalid", false, err))
	}
	if err := w.service.db.Model(&task).Updates(map[string]any{"plan_summary_json": encodedSummary, "total_files": len(targets), "updated_at": time.Now().UTC()}).Error; err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}
	task.PlanSummaryJSON, task.TotalFiles = encodedSummary, len(targets)

	policy := download.ConflictPolicy
	if response := transferActionResponse(taskJobCheckpoint(w.service.db, task.JobID)); response != "" {
		policy = response
	}
	conflicts, err := w.cloudUploadConflicts(ctx, driver, targets, state)
	if err != nil {
		return w.cloudFailure(task, err)
	}
	if len(conflicts) > 0 && policy == models.MediaLibraryConflictAsk {
		return WorkerResult{Wait: &WaitForAction{ActionType: "transfer_conflict", Prompt: "目标媒体库存在同名文件，请选择处理方式", Options: []string{models.MediaLibraryConflictOverwrite, models.MediaLibraryConflictSkip, models.MediaLibraryConflictRename}, Preview: map[string]string{"媒体库": task.LibraryName, "冲突文件": strconv.Itoa(len(conflicts))}, Checkpoint: map[string]any{"conflict_count": len(conflicts)}}}
	}
	if err := w.persistCloudState(&task, state, models.TransferTaskStatusTransferring, completedCloudItems(state), nil); err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}

	for index, target := range targets {
		if ctx.Err() != nil {
			return WorkerResult{}
		}
		key := normalizedManifestPath(target.File.RelativePath)
		if state.Items[key].Status == "completed" || state.Items[key].Status == "skipped" {
			continue
		}
		parentID := state.Directories[pathpkg.Dir(target.Relative)]
		name := pathpkg.Base(target.Relative)
		if itemState := state.Items[key]; itemState.Status == "uploading" {
			if itemState.TargetParentID != parentID || strings.TrimSpace(itemState.TargetName) == "" {
				return w.cloudFailure(task, cloudTransferError("cloud_transfer_state_invalid", false, nil))
			}
			name = itemState.TargetName
			reconciled, found, reconcileErr := exactCloudUploadTarget(ctx, driver, parentID, name, target.File.Size)
			if reconcileErr != nil {
				return w.cloudFailure(task, reconcileErr)
			}
			if found {
				state.Items[key] = cloudTransferItemState{SourceID: key, CurrentID: reconciled.ID, TargetParentID: parentID, TargetName: name, Status: "completed"}
			}
		}
		if conflict := conflicts[key]; conflict.ID != "" {
			switch policy {
			case models.MediaLibraryConflictSkip:
				state.Items[key] = cloudTransferItemState{SourceID: key, CurrentID: conflict.ID, TargetParentID: parentID, TargetName: name, Status: "skipped"}
			case models.MediaLibraryConflictOverwrite:
				if conflict.IsDir {
					return w.cloudFailure(task, cloudTransferError("cloud_transfer_target_type_conflict", false, nil))
				}
				if _, err := providerItemWithinRoot(ctx, driver, conflict.ID, download.TargetProviderRootID); err != nil {
					return w.cloudFailure(task, cloudTransferError("cloud_transfer_boundary_invalid", false, err))
				}
				if err := mutations.Recycle(ctx, conflict.ID); err != nil {
					return w.cloudFailure(task, err)
				}
			case models.MediaLibraryConflictRename:
				name, err = availableCloudUploadName(ctx, driver, parentID, name)
				if err != nil {
					return w.cloudFailure(task, err)
				}
			default:
				return w.cloudFailure(task, cloudTransferError("transfer_conflict_failed", false, nil))
			}
		}
		if state.Items[key].Status == "" {
			state.Items[key] = cloudTransferItemState{SourceID: key, TargetParentID: parentID, TargetName: name, Status: "uploading"}
			if persistErr := w.persistCloudState(&task, state, models.TransferTaskStatusTransferring, completedCloudItems(state), nil); persistErr != nil {
				return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, persistErr))
			}
		}
		if state.Items[key].Status == "uploading" {
			sourceRoot := filepath.Join(download.StagingAbsolutePath, pluginDownloadRootName, download.ID)
			source, err := resolveManifestSource(sourceRoot, sourceRoot, target.File.RelativePath)
			if err != nil {
				return w.cloudFailure(task, cloudTransferError("cloud_upload_source_invalid", false, err))
			}
			file, err := os.Open(source)
			if err != nil {
				return w.cloudFailure(task, cloudTransferError("cloud_upload_source_unavailable", true, err))
			}
			info, statErr := file.Stat()
			if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != target.File.Size {
				_ = file.Close()
				return w.cloudFailure(task, cloudTransferError("cloud_upload_source_changed", false, statErr))
			}
			uploaded, uploadErr := uploadCloudFileWithHeartbeat(ctx, runtime, uploader, cloudpkg.UploadRequest{ParentID: parentID, Name: name, Size: target.File.Size, Reader: file})
			closeErr := file.Close()
			if uploadErr != nil {
				return w.cloudFailure(task, uploadErr)
			}
			if closeErr != nil || uploaded.ID == "" || uploaded.ParentID != parentID || uploaded.Name != name || uploaded.IsDir || uploaded.Size != target.File.Size {
				return w.cloudFailure(task, cloudTransferError("cloud_upload_reconcile_failed", true, closeErr))
			}
			state.Items[key] = cloudTransferItemState{SourceID: key, CurrentID: uploaded.ID, TargetParentID: parentID, TargetName: name, Status: "completed"}
		}
		processed := completedCloudItems(state)
		if index < len(summary.Items) {
			summary.Items[index].RelativePath = pathpkg.Join(pathpkg.Dir(target.Relative), state.Items[key].TargetName)
			if state.Items[key].Status == "skipped" {
				summary.Items[index].Result = "skipped"
			} else {
				summary.Items[index].Result = "completed"
			}
		}
		encodedSummary, err = encodeTransferPlanSummary(summary)
		if err != nil {
			return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
		}
		if persistErr := w.persistCloudState(&task, state, models.TransferTaskStatusTransferring, processed, &encodedSummary); persistErr != nil {
			return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, persistErr))
		}
		processed64, total64 := int64(processed), int64(len(targets))
		progress := float64(processed64) * 100 / float64(total64)
		if err := runtime.Heartbeat(&progress, &processed64, &total64, nil, nil); err != nil {
			return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "入库任务租约已失效"}
		}
	}

	now := time.Now().UTC()
	err = w.service.db.Transaction(func(tx *gorm.DB) error {
		if err := ensureDownloadPipelineActive(tx, task.DownloadTaskID); err != nil {
			return err
		}
		if err := captureCloudManagedItems(tx, task, download, targets, state); err != nil {
			return err
		}
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", task.LibraryID).UpdateColumn("dirty_generation", gorm.Expr("dirty_generation + 1")).Error; err != nil {
			return err
		}
		if err := tx.Model(&task).Updates(map[string]any{"phase": models.TransferTaskStatusCompleted, "processed_files": len(targets), "last_error_code": "", "finished_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return w.service.audit.Record(tx, &task.OwnerID, "transfer.complete", "transfer_task", task.ID, "success", map[string]any{"download_task_id": task.DownloadTaskID, "media_library_id": task.LibraryID, "mode": "managed_upload", "files": len(targets), "provider": cloudpkg.ProviderPan115}, RequestContext{})
	})
	if errors.Is(err, context.Canceled) {
		return WorkerResult{}
	}
	if err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}
	serverlog.OperationPan115CloudTransfer.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Int("files", len(targets)).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationPan115CloudTransfer.Message("受管上传完成"))
	task.Phase = models.TransferTaskStatusCompleted
	return w.finishCompletedTransfer(ctx, task)
}

func (w *TransferWorker) cloudUploadConflicts(ctx context.Context, driver cloudpkg.Driver, targets []transferTargetItem, state cloudTransferState) (map[string]cloudpkg.Item, error) {
	result := make(map[string]cloudpkg.Item)
	cache := make(map[string][]cloudpkg.Item)
	for _, target := range targets {
		key := normalizedManifestPath(target.File.RelativePath)
		if state.Items[key].Status == "completed" || state.Items[key].Status == "skipped" || state.Items[key].Status == "uploading" {
			continue
		}
		parentID := state.Directories[pathpkg.Dir(target.Relative)]
		items, ok := cache[parentID]
		if !ok {
			var err error
			items, err = listCloudDirectory(ctx, driver, parentID)
			if err != nil {
				return nil, err
			}
			cache[parentID] = items
		}
		matches := namedCloudItems(items, pathpkg.Base(target.Relative))
		if len(matches) > 1 {
			return nil, cloudTransferError(cloudpkg.CodeConflict, false, errors.New("cloud upload target is ambiguous"))
		}
		if len(matches) == 1 {
			result[key] = matches[0]
		}
	}
	return result, nil
}

func exactCloudUploadTarget(ctx context.Context, driver cloudpkg.Driver, parentID, name string, size int64) (cloudpkg.Item, bool, error) {
	items, err := listCloudDirectory(ctx, driver, parentID)
	if err != nil {
		return cloudpkg.Item{}, false, err
	}
	matches := namedCloudItems(items, name)
	if len(matches) > 1 {
		return cloudpkg.Item{}, false, cloudTransferError(cloudpkg.CodeConflict, false, errors.New("cloud upload target is ambiguous"))
	}
	if len(matches) == 0 {
		return cloudpkg.Item{}, false, nil
	}
	if matches[0].IsDir || matches[0].Size != size {
		return cloudpkg.Item{}, false, cloudTransferError(cloudpkg.CodeConflict, false, errors.New("cloud upload target changed"))
	}
	return matches[0], true, nil
}

func uploadCloudFileWithHeartbeat(ctx context.Context, runtime JobRuntime, uploader cloudpkg.UploadDriver, request cloudpkg.UploadRequest) (cloudpkg.Item, error) {
	type result struct {
		item cloudpkg.Item
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		item, err := uploader.Upload(ctx, request)
		completed <- result{item: item, err: err}
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var heartbeatErr error
	for {
		select {
		case result := <-completed:
			if heartbeatErr != nil && result.err == nil {
				return cloudpkg.Item{}, cloudTransferError(CodeQueueLeaseInvalid, true, heartbeatErr)
			}
			return result.item, result.err
		case <-ticker.C:
			if heartbeatErr == nil {
				heartbeatErr = runtime.Heartbeat(nil, nil, nil, nil, nil)
			}
		}
	}
}

func availableCloudUploadName(ctx context.Context, driver cloudpkg.Driver, parentID, name string) (string, error) {
	items, err := listCloudDirectory(ctx, driver, parentID)
	if err != nil {
		return "", err
	}
	existing := make(map[string]struct{}, len(items))
	for _, item := range items {
		existing[strings.ToLower(item.Name)] = struct{}{}
	}
	extension := pathpkg.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for suffix := 2; suffix <= 999; suffix++ {
		candidate := stem + " (" + strconv.Itoa(suffix) + ")" + extension
		if _, exists := existing[strings.ToLower(candidate)]; !exists {
			return candidate, nil
		}
	}
	return "", cloudTransferError(cloudpkg.CodeConflict, false, errors.New("no cloud upload target name is available"))
}
