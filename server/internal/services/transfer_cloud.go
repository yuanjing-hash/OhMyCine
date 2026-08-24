package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	pathpkg "path"
	"strconv"
	"strings"
	"time"

	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

const (
	cloudTransferStateVersion = 1
	maxCloudStateBytes        = 1024 * 1024
	maxCloudDirectoryEntries  = 10000
)

type cloudTransferState struct {
	Version         int                               `json:"version"`
	Directories     map[string]string                 `json:"directories"`
	TempDirectoryID string                            `json:"temp_directory_id,omitempty"`
	Items           map[string]cloudTransferItemState `json:"items"`
}

type cloudTransferItemState struct {
	SourceID       string `json:"source_id"`
	CurrentID      string `json:"current_id,omitempty"`
	TargetParentID string `json:"target_parent_id,omitempty"`
	TargetName     string `json:"target_name,omitempty"`
	Status         string `json:"status"`
}

type cloudTransferFailure struct {
	code      string
	retryable bool
	cause     error
}

func (e *cloudTransferFailure) Error() string { return e.code }
func (e *cloudTransferFailure) Unwrap() error { return e.cause }

func cloudTransferError(code string, retryable bool, cause error) error {
	return &cloudTransferFailure{code: code, retryable: retryable, cause: cause}
}

func (w *TransferWorker) runCloudTransfer(ctx context.Context, runtime JobRuntime, task models.TransferTask, download models.DownloadTask, manifest downloadpkg.Manifest, started time.Time) WorkerResult {
	if w.service.connections == nil || download.TargetConnectionID == nil || download.TargetStorageID == nil || download.StagingStorageID == nil || strings.TrimSpace(download.TargetProviderRootID) == "" {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_snapshot_invalid", false, nil))
	}
	if download.ProviderType != models.DownloaderTypePan115Offline || (download.TransferMode != models.MediaLibraryTransferMove && download.TransferMode != models.MediaLibraryTransferCopy) {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_mode_invalid", false, nil))
	}
	connection, driver, err := w.service.connections.driver(*download.TargetConnectionID)
	if err != nil {
		return w.cloudFailure(task, err)
	}
	mutations, ok := driver.(cloudpkg.MutationDriver)
	if !ok || connection.Provider != cloudpkg.ProviderPan115 {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_capability_missing", false, nil))
	}
	capabilities := mutations.Capabilities()
	if !capabilities.CreateDirectory || !capabilities.Rename || !capabilities.Recycle || (download.TransferMode == models.MediaLibraryTransferMove && !capabilities.Move) || (download.TransferMode == models.MediaLibraryTransferCopy && !capabilities.Copy) {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_capability_missing", false, nil))
	}

	var sourceStorage, targetStorage models.Storage
	if err := w.service.db.First(&sourceStorage, *download.StagingStorageID).Error; err != nil {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_source_missing", false, err))
	}
	if err := w.service.db.First(&targetStorage, *download.TargetStorageID).Error; err != nil {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_target_missing", false, err))
	}
	if sourceStorage.Type != models.StorageTypePan115 || targetStorage.Type != models.StorageTypePan115 || sourceStorage.ConnectionID == nil || targetStorage.ConnectionID == nil || *sourceStorage.ConnectionID != *download.TargetConnectionID || *targetStorage.ConnectionID != *download.TargetConnectionID {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_boundary_invalid", false, nil))
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
	if err := w.persistCloudState(&task, state, models.TransferTaskStatusPlanning, task.ProcessedFiles, nil); err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}

	summaryPlan := make([]transferPlanItem, 0, len(targets))
	for _, target := range targets {
		summaryPlan = append(summaryPlan, transferPlanItem{Relative: target.Relative, Size: target.File.Size, Group: target.Group})
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
	if err := w.persistCloudState(&task, state, models.TransferTaskStatusTransferring, completedCloudItems(state), &encodedSummary); err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}
	serverlog.OperationPan115CloudTransfer.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Int("files", len(targets)).Msg(serverlog.OperationPan115CloudTransfer.Message("已完成命名规划，开始按 115 风控节奏准备目录并逐文件入库"))

	for _, target := range targets {
		if err := validateCloudTargetItem(target); err != nil {
			return w.cloudFailure(task, cloudTransferError("cloud_transfer_manifest_invalid", false, err))
		}
		if download.TransferMode == models.MediaLibraryTransferCopy && strings.TrimSpace(target.File.SHA1) == "" {
			return w.cloudFailure(task, cloudTransferError("cloud_transfer_manifest_invalid", false, errors.New("copy source has no stable content identity")))
		}
		if _, err := w.ensureCloudDirectory(ctx, mutations, &task, &state, pathpkg.Dir(target.Relative)); err != nil {
			return w.cloudFailure(task, err)
		}
	}

	policy := download.ConflictPolicy
	if response := transferActionResponse(taskJobCheckpoint(w.service.db, task.JobID)); response != "" {
		policy = response
	}
	conflicts, applied, err := w.cloudConflicts(ctx, driver, targets, state, download.TransferMode)
	if err != nil {
		return w.cloudFailure(task, err)
	}
	if len(conflicts) > 0 && policy == models.MediaLibraryConflictAsk {
		serverlog.OperationPan115CloudTransfer.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Int("conflicts", len(conflicts)).Msg(serverlog.OperationPan115CloudTransfer.Message("等待处理云端同名文件"))
		return WorkerResult{Wait: &WaitForAction{ActionType: "transfer_conflict", Prompt: "目标媒体库存在同名文件，请选择处理方式", Options: []string{models.MediaLibraryConflictOverwrite, models.MediaLibraryConflictSkip, models.MediaLibraryConflictRename}, Preview: map[string]string{"媒体库": task.LibraryName, "冲突文件": strconv.Itoa(len(conflicts))}, Checkpoint: map[string]any{"conflict_count": len(conflicts)}}}
	}
	if policy == models.MediaLibraryConflictRename && len(conflicts) > 0 {
		if err := w.renameCloudConflictGroups(ctx, driver, targets, state, conflicts); err != nil {
			return w.cloudFailure(task, err)
		}
		conflicts, applied, err = w.cloudConflicts(ctx, driver, targets, state, download.TransferMode)
		if err != nil {
			return w.cloudFailure(task, err)
		}
	}

	if err := w.persistCloudState(&task, state, models.TransferTaskStatusTransferring, completedCloudItems(state), nil); err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}
	for index := range targets {
		if ctx.Err() != nil {
			return WorkerResult{}
		}
		target := &targets[index]
		key := target.File.ProviderItemID
		itemState := state.Items[key]
		if itemState.Status == "completed" || itemState.Status == "skipped" {
			continue
		}
		if applied[key] {
			itemState = cloudTransferItemState{SourceID: key, CurrentID: key, TargetParentID: state.Directories[pathpkg.Dir(target.Relative)], TargetName: pathpkg.Base(target.Relative), Status: "completed"}
			if download.TransferMode == models.MediaLibraryTransferCopy {
				itemState.CurrentID = ""
			}
			state.Items[key] = itemState
		} else if conflict := conflicts[key]; conflict.ID != "" {
			switch policy {
			case models.MediaLibraryConflictSkip:
				state.Items[key] = cloudTransferItemState{SourceID: key, TargetParentID: conflict.ParentID, TargetName: conflict.Name, Status: "skipped"}
			case models.MediaLibraryConflictOverwrite:
				if conflict.IsDir {
					return w.cloudFailure(task, cloudTransferError("cloud_transfer_target_type_conflict", false, nil))
				}
				if _, err := providerItemWithinRoot(ctx, driver, conflict.ID, download.TargetProviderRootID); err != nil {
					return w.cloudFailure(task, cloudTransferError("cloud_transfer_boundary_invalid", false, err))
				}
				if err := mutations.Recycle(ctx, conflict.ID); err != nil {
					if _, statErr := driver.Stat(ctx, conflict.ID); statErr == nil {
						return w.cloudFailure(task, err)
					} else if code, _ := cloudpkg.ErrorInfo(statErr); code != cloudpkg.CodeNotFound {
						return w.cloudFailure(task, err)
					}
				}
			default:
				return w.cloudFailure(task, cloudTransferError("transfer_conflict_failed", false, nil))
			}
		}
		if state.Items[key].Status == "" {
			targetParentID := state.Directories[pathpkg.Dir(target.Relative)]
			targetName := pathpkg.Base(target.Relative)
			source, err := driver.Stat(ctx, key)
			if err != nil || source.IsDir || !cloudManifestMatches(source, target.File) {
				return w.cloudFailure(task, cloudTransferError("cloud_transfer_source_changed", false, err))
			}
			originalName := pathpkg.Base(strings.ReplaceAll(target.File.RelativePath, "\\", "/"))
			if download.TransferMode == models.MediaLibraryTransferMove && source.ParentID == targetParentID && (source.Name == originalName || source.Name == targetName) {
				// A previous attempt may have placed the stable item before rename or
				// before its checkpoint commit. The already-validated target parent is
				// the only destination outside the source root accepted here.
			} else {
				bounded, boundaryErr := providerItemWithinRoot(ctx, driver, key, sourceStorage.RootPath)
				if boundaryErr != nil || bounded.Name != originalName || (target.File.ProviderParentID != "" && bounded.ParentID != target.File.ProviderParentID) {
					return w.cloudFailure(task, cloudTransferError("cloud_transfer_source_changed", false, boundaryErr))
				}
				source = bounded
			}
			if download.TransferMode == models.MediaLibraryTransferMove {
				err = w.executeCloudMove(ctx, mutations, driver, source, targetParentID, targetName)
				if err == nil {
					state.Items[key] = cloudTransferItemState{SourceID: key, CurrentID: key, TargetParentID: targetParentID, TargetName: targetName, Status: "completed"}
				}
			} else {
				err = w.executeCloudCopy(ctx, mutations, driver, &task, &state, source, targetParentID, targetName)
			}
			if err != nil {
				return w.cloudFailure(task, err)
			}
		}
		processed := completedCloudItems(state)
		if index < len(summary.Items) {
			summary.Items[index].RelativePath = target.Relative
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
		if err := w.persistCloudState(&task, state, models.TransferTaskStatusTransferring, processed, &encodedSummary); err != nil {
			return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
		}
		processed64, total64 := int64(processed), int64(len(targets))
		progress := float64(processed64) * 100 / float64(total64)
		if err := runtime.Heartbeat(&progress, &processed64, &total64, nil, nil); err != nil {
			return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "入库任务租约已失效"}
		}
	}

	if download.TransferMode == models.MediaLibraryTransferCopy && state.TempDirectoryID != "" {
		tempID := state.TempDirectoryID
		if _, err := providerItemWithinRoot(ctx, driver, tempID, download.TargetProviderRootID); err != nil {
			if code, _ := cloudpkg.ErrorInfo(err); code != cloudpkg.CodeNotFound {
				return w.cloudFailure(task, cloudTransferError("cloud_transfer_boundary_invalid", false, err))
			}
		} else {
			items, err := listCloudDirectory(ctx, driver, tempID)
			if err != nil {
				return w.cloudFailure(task, err)
			}
			if len(items) != 0 {
				return w.cloudFailure(task, cloudTransferError(cloudpkg.CodeMutationUnknown, false, errors.New("copy staging directory is not empty")))
			}
			if err := mutations.Recycle(ctx, tempID); err != nil {
				if _, statErr := driver.Stat(ctx, tempID); statErr == nil {
					return w.cloudFailure(task, err)
				} else if code, _ := cloudpkg.ErrorInfo(statErr); code != cloudpkg.CodeNotFound {
					return w.cloudFailure(task, err)
				}
			}
		}
		state.TempDirectoryID = ""
		if err := w.persistCloudState(&task, state, models.TransferTaskStatusReconciling, len(targets), nil); err != nil {
			return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
		}
	}

	now := time.Now().UTC()
	err = w.service.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", task.LibraryID).UpdateColumn("dirty_generation", gorm.Expr("dirty_generation + 1")).Error; err != nil {
			return err
		}
		if err := tx.Model(&task).Updates(map[string]any{"phase": models.TransferTaskStatusCompleted, "processed_files": len(targets), "last_error_code": "", "finished_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return w.service.audit.Record(tx, &task.OwnerID, "transfer.complete", "transfer_task", task.ID, "success", map[string]any{"download_task_id": task.DownloadTaskID, "media_library_id": task.LibraryID, "mode": download.TransferMode, "files": len(targets), "provider": cloudpkg.ProviderPan115}, RequestContext{})
	})
	if err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}
	serverlog.OperationPan115CloudTransfer.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Str("transfer_mode", download.TransferMode).Int("files", len(targets)).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationPan115CloudTransfer.Message("完成"))
	task.Phase = models.TransferTaskStatusCompleted
	return w.finishCompletedTransfer(ctx, task)
}

func decodeCloudTransferState(raw string) (cloudTransferState, error) {
	state := cloudTransferState{Version: cloudTransferStateVersion, Directories: map[string]string{}, Items: map[string]cloudTransferItemState{}}
	if strings.TrimSpace(raw) == "" {
		return state, nil
	}
	if len(raw) > maxCloudStateBytes {
		return state, errors.New("cloud transfer state is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return state, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return state, errors.New("cloud transfer state has trailing data")
		}
		return state, err
	}
	if state.Version != cloudTransferStateVersion || state.Directories == nil || state.Items == nil {
		return state, errors.New("cloud transfer state version is invalid")
	}
	return state, nil
}

func encodeCloudTransferState(state cloudTransferState) (string, error) {
	encoded, err := json.Marshal(state)
	if err != nil || len(encoded) > maxCloudStateBytes {
		return "", errors.New("cloud transfer state is too large")
	}
	return string(encoded), nil
}

func (w *TransferWorker) persistCloudState(task *models.TransferTask, state cloudTransferState, phase string, processed int, summary *string) error {
	encoded, err := encodeCloudTransferState(state)
	if err != nil {
		return err
	}
	updates := map[string]any{"cloud_state_json": encoded, "phase": phase, "processed_files": processed, "updated_at": time.Now().UTC()}
	if summary != nil {
		updates["plan_summary_json"] = *summary
	}
	if err := w.service.db.Model(task).Updates(updates).Error; err != nil {
		return err
	}
	task.CloudStateJSON, task.Phase, task.ProcessedFiles = encoded, phase, processed
	return nil
}

func (w *TransferWorker) ensureCloudDirectory(ctx context.Context, mutations cloudpkg.MutationDriver, task *models.TransferTask, state *cloudTransferState, relative string) (string, error) {
	relative = pathpkg.Clean(relative)
	if relative == "." || relative == "" {
		return state.Directories["."], nil
	}
	if id := state.Directories[relative]; id != "" {
		item, err := providerItemWithinRoot(ctx, mutations, id, state.Directories["."])
		if err == nil && item.IsDir {
			return id, nil
		}
		delete(state.Directories, relative)
	}
	parentPath := pathpkg.Dir(relative)
	parentID, err := w.ensureCloudDirectory(ctx, mutations, task, state, parentPath)
	if err != nil {
		return "", err
	}
	name := pathpkg.Base(relative)
	items, err := listCloudDirectory(ctx, mutations, parentID)
	if err != nil {
		return "", err
	}
	matches := namedCloudItems(items, name)
	if len(matches) > 1 || (len(matches) == 1 && !matches[0].IsDir) {
		return "", cloudTransferError(cloudpkg.CodeConflict, false, errors.New("cloud directory name is ambiguous"))
	}
	var directory cloudpkg.Item
	if len(matches) == 1 {
		directory = matches[0]
	} else {
		directory, err = mutations.CreateDirectory(ctx, parentID, name)
		if err != nil {
			items, listErr := listCloudDirectory(ctx, mutations, parentID)
			matches = namedCloudItems(items, name)
			if listErr != nil || len(matches) != 1 || !matches[0].IsDir {
				return "", err
			}
			directory = matches[0]
		}
	}
	state.Directories[relative] = directory.ID
	phase := task.Phase
	if phase == "" {
		phase = models.TransferTaskStatusPlanning
	}
	if err := w.persistCloudState(task, *state, phase, task.ProcessedFiles, nil); err != nil {
		return "", cloudTransferError("transfer_state_persist_failed", true, err)
	}
	return directory.ID, nil
}

func listCloudDirectory(ctx context.Context, driver cloudpkg.Driver, parentID string) ([]cloudpkg.Item, error) {
	const pageSize int64 = 200
	items := make([]cloudpkg.Item, 0, pageSize)
	for offset := int64(0); ; offset += pageSize {
		page, err := driver.List(ctx, parentID, cloudpkg.PageRequest{Offset: offset, Limit: pageSize})
		if err != nil {
			return nil, err
		}
		if len(page.Items) == 0 && page.HasMore {
			return nil, cloudTransferError(cloudpkg.CodeResponseInvalid, true, errors.New("cloud directory page made no progress"))
		}
		items = append(items, page.Items...)
		if len(items) > maxCloudDirectoryEntries {
			return nil, cloudTransferError(cloudpkg.CodeResponseInvalid, false, errors.New("cloud directory is too large"))
		}
		if !page.HasMore {
			return items, nil
		}
	}
}

func namedCloudItems(items []cloudpkg.Item, name string) []cloudpkg.Item {
	result := make([]cloudpkg.Item, 0, 1)
	for _, item := range items {
		if item.Name == name {
			result = append(result, item)
		}
	}
	return result
}

func (w *TransferWorker) cloudConflicts(ctx context.Context, driver cloudpkg.Driver, targets []transferTargetItem, state cloudTransferState, mode string) (map[string]cloudpkg.Item, map[string]bool, error) {
	conflicts := map[string]cloudpkg.Item{}
	applied := map[string]bool{}
	listings := map[string][]cloudpkg.Item{}
	for _, target := range targets {
		key := target.File.ProviderItemID
		if key == "" {
			return nil, nil, cloudTransferError("cloud_transfer_manifest_invalid", false, nil)
		}
		if saved := state.Items[key]; saved.Status == "completed" || saved.Status == "skipped" {
			applied[key] = true
			continue
		}
		parentID := state.Directories[pathpkg.Dir(target.Relative)]
		if parentID == "" {
			return nil, nil, cloudTransferError("cloud_transfer_state_invalid", false, nil)
		}
		items, ok := listings[parentID]
		if !ok {
			var err error
			items, err = listCloudDirectory(ctx, driver, parentID)
			if err != nil {
				return nil, nil, err
			}
			listings[parentID] = items
		}
		matches := namedCloudItems(items, pathpkg.Base(target.Relative))
		if len(matches) > 1 {
			return nil, nil, cloudTransferError(cloudpkg.CodeMutationUnknown, false, errors.New("target name is ambiguous"))
		}
		if len(matches) == 0 {
			continue
		}
		match := matches[0]
		if match.IsDir {
			conflicts[key] = match
			continue
		}
		if mode == models.MediaLibraryTransferMove && match.ID == key {
			applied[key] = true
			continue
		}
		if mode == models.MediaLibraryTransferCopy && cloudManifestMatches(match, target.File) {
			applied[key] = true
			continue
		}
		conflicts[key] = match
	}
	return conflicts, applied, nil
}

func (w *TransferWorker) renameCloudConflictGroups(ctx context.Context, driver cloudpkg.Driver, targets []transferTargetItem, state cloudTransferState, conflicts map[string]cloudpkg.Item) error {
	groups := map[string]struct{}{}
	for _, target := range targets {
		if conflicts[target.File.ProviderItemID].ID != "" {
			groups[target.Group] = struct{}{}
		}
	}
	for group := range groups {
		for suffix := 1; suffix <= 999; suffix++ {
			available := true
			candidateGroup := group + " (" + strconv.Itoa(suffix) + ")"
			listings := map[string][]cloudpkg.Item{}
			for _, target := range targets {
				if target.Group != group {
					continue
				}
				parentID := state.Directories[pathpkg.Dir(target.Relative)]
				if parentID == "" {
					return cloudTransferError("cloud_transfer_state_invalid", false, nil)
				}
				items, ok := listings[parentID]
				if !ok {
					var err error
					items, err = listCloudDirectory(ctx, driver, parentID)
					if err != nil {
						return err
					}
					listings[parentID] = items
				}
				name := pathpkg.Base(candidateGroup) + strings.TrimPrefix(target.Relative, target.Group)
				if len(namedCloudItems(items, name)) != 0 {
					available = false
					break
				}
			}
			if available {
				for index := range targets {
					if targets[index].Group != group {
						continue
					}
					suffix := strings.TrimPrefix(targets[index].Relative, targets[index].Group)
					targets[index].Group = candidateGroup
					targets[index].Relative = candidateGroup + suffix
				}
				break
			}
			if suffix == 999 {
				return cloudTransferError(cloudpkg.CodeConflict, false, errors.New("no available cloud target name"))
			}
		}
	}
	return nil
}

func cloudManifestMatches(item cloudpkg.Item, file downloadpkg.File) bool {
	if item.IsDir || item.Size != file.Size {
		return false
	}
	if strings.TrimSpace(file.SHA1) != "" && !strings.EqualFold(strings.TrimSpace(item.SHA1), strings.TrimSpace(file.SHA1)) {
		return false
	}
	return true
}

func (w *TransferWorker) executeCloudMove(ctx context.Context, mutations cloudpkg.MutationDriver, driver cloudpkg.Driver, source cloudpkg.Item, targetParentID, targetName string) error {
	if source.ParentID != targetParentID {
		if err := mutations.Move(ctx, source.ID, targetParentID); err != nil {
			current, statErr := driver.Stat(ctx, source.ID)
			if statErr != nil || current.ParentID != targetParentID {
				return err
			}
		}
	}
	current, err := driver.Stat(ctx, source.ID)
	if err != nil {
		return err
	}
	if current.Name != targetName {
		if err := mutations.Rename(ctx, source.ID, targetName); err != nil {
			verified, statErr := driver.Stat(ctx, source.ID)
			if statErr != nil || verified.Name != targetName {
				return err
			}
		}
	}
	return nil
}

func (w *TransferWorker) executeCloudCopy(ctx context.Context, mutations cloudpkg.MutationDriver, driver cloudpkg.Driver, task *models.TransferTask, state *cloudTransferState, source cloudpkg.Item, targetParentID, targetName string) error {
	if state.TempDirectoryID == "" {
		name := ".ohmycine-import-" + strings.ReplaceAll(task.ID, "-", "")
		if len(name) > 48 {
			name = name[:48]
		}
		items, err := listCloudDirectory(ctx, driver, state.Directories["."])
		if err != nil {
			return err
		}
		matches := namedCloudItems(items, name)
		if len(matches) > 1 || (len(matches) == 1 && !matches[0].IsDir) {
			return cloudTransferError(cloudpkg.CodeMutationUnknown, false, errors.New("copy staging directory is ambiguous"))
		}
		if len(matches) == 1 {
			state.TempDirectoryID = matches[0].ID
		} else {
			created, err := mutations.CreateDirectory(ctx, state.Directories["."], name)
			if err != nil {
				items, listErr := listCloudDirectory(ctx, driver, state.Directories["."])
				matches = namedCloudItems(items, name)
				if listErr != nil || len(matches) != 1 || !matches[0].IsDir {
					return err
				}
				created = matches[0]
			}
			state.TempDirectoryID = created.ID
		}
		if err := w.persistCloudState(task, *state, models.TransferTaskStatusTransferring, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
		}
	}
	if _, err := providerItemWithinRoot(ctx, driver, state.TempDirectoryID, state.Directories["."]); err != nil {
		return cloudTransferError("cloud_transfer_boundary_invalid", false, err)
	}
	itemState := state.Items[source.ID]
	copyID := itemState.CurrentID
	if copyID != "" {
		current, err := providerItemWithinRoot(ctx, driver, copyID, state.Directories["."])
		if err != nil {
			copyID = ""
		} else if current.ParentID == targetParentID && current.Name == targetName && cloudManifestMatches(current, downloadpkg.File{Size: source.Size, SHA1: source.SHA1}) {
			itemState.Status = "completed"
			state.Items[source.ID] = itemState
			return nil
		}
	}
	if copyID == "" {
		candidate, count, err := findCloudCopyCandidate(ctx, driver, state.TempDirectoryID, source)
		if err != nil {
			return err
		}
		if count > 1 {
			return cloudTransferError(cloudpkg.CodeMutationUnknown, false, errors.New("copied item identity is ambiguous"))
		}
		if count == 0 {
			if err := mutations.Copy(ctx, source.ID, state.TempDirectoryID); err != nil {
				candidate, count, _ = findCloudCopyCandidate(ctx, driver, state.TempDirectoryID, source)
				if count != 1 {
					return err
				}
			}
			if candidate.ID == "" {
				candidate, count, err = findCloudCopyCandidate(ctx, driver, state.TempDirectoryID, source)
				if err != nil {
					return err
				}
				if count != 1 {
					return cloudTransferError(cloudpkg.CodeMutationUnknown, count == 0, errors.New("copy result identity is unknown"))
				}
			}
		}
		copyID = candidate.ID
		itemState = cloudTransferItemState{SourceID: source.ID, CurrentID: copyID, TargetParentID: targetParentID, TargetName: targetName, Status: "copied"}
		state.Items[source.ID] = itemState
		if err := w.persistCloudState(task, *state, models.TransferTaskStatusTransferring, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
		}
	}
	current, err := driver.Stat(ctx, copyID)
	if err != nil {
		return err
	}
	if current.Name != targetName {
		if err := mutations.Rename(ctx, copyID, targetName); err != nil {
			verified, statErr := driver.Stat(ctx, copyID)
			if statErr != nil || verified.Name != targetName {
				return err
			}
		}
	}
	current, err = driver.Stat(ctx, copyID)
	if err != nil {
		return err
	}
	if current.ParentID != targetParentID {
		if err := mutations.Move(ctx, copyID, targetParentID); err != nil {
			verified, statErr := driver.Stat(ctx, copyID)
			if statErr != nil || verified.ParentID != targetParentID {
				return err
			}
		}
	}
	itemState.Status = "completed"
	state.Items[source.ID] = itemState
	return nil
}

func findCloudCopyCandidate(ctx context.Context, driver cloudpkg.Driver, parentID string, source cloudpkg.Item) (cloudpkg.Item, int, error) {
	items, err := listCloudDirectory(ctx, driver, parentID)
	if err != nil {
		return cloudpkg.Item{}, 0, err
	}
	var candidate cloudpkg.Item
	count := 0
	for _, item := range items {
		if item.Name == source.Name && cloudManifestMatches(item, downloadpkg.File{Size: source.Size, SHA1: source.SHA1}) {
			candidate, count = item, count+1
		}
	}
	return candidate, count, nil
}

func completedCloudItems(state cloudTransferState) int {
	count := 0
	for _, item := range state.Items {
		if item.Status == "completed" || item.Status == "skipped" {
			count++
		}
	}
	return count
}

func taskJobCheckpoint(db *gorm.DB, jobID string) string {
	var job models.Job
	if db.Select("checkpoint_json").First(&job, "id = ?", jobID).Error != nil {
		return ""
	}
	return job.CheckpointJSON
}

func (w *TransferWorker) cloudFailure(task models.TransferTask, err error) WorkerResult {
	code, retryable := cloudpkg.ErrorInfo(err)
	var failure *cloudTransferFailure
	if errors.As(err, &failure) {
		code, retryable = failure.code, failure.retryable
	}
	code = safeLabel(code, 96)
	message := "115 云端整理失败"
	if retryable {
		message = "115 暂时不可用，云端整理将自动重试"
		_ = w.service.db.Model(&task).Updates(map[string]any{"last_error_code": code, "updated_at": time.Now().UTC()}).Error
		next := time.Now().UTC().Add(15 * time.Second)
		serverlog.OperationPan115CloudTransfer.Event(w.service.log.Warn()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Str("error_code", code).Time("retry_at", next).Msg(serverlog.OperationPan115CloudTransfer.Message("暂时失败，已安排自动重试"))
		return WorkerResult{RetryAt: &next, ErrorCode: code, ErrorMessage: message}
	}
	_ = w.service.db.Model(&task).Updates(map[string]any{"phase": models.TransferTaskStatusFailed, "last_error_code": code, "updated_at": time.Now().UTC()}).Error
	serverlog.OperationPan115CloudTransfer.Event(w.service.log.Error()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Str("error_code", code).Msg(serverlog.OperationPan115CloudTransfer.Message("失败"))
	return WorkerResult{ErrorCode: code, ErrorMessage: message}
}

func validateCloudTargetItem(target transferTargetItem) error {
	if strings.TrimSpace(target.File.ProviderItemID) == "" || strings.TrimSpace(target.Relative) == "" {
		return fmt.Errorf("cloud target identity is incomplete")
	}
	return nil
}
