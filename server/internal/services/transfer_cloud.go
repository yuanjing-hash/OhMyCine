package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	pathpkg "path"
	"sort"
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
	cloudBatchIntentVersion   = 1
	maxCloudStateBytes        = 1024 * 1024
	maxCloudDirectoryEntries  = 10000
)

type cloudTransferState struct {
	Version         int                               `json:"version"`
	Directories     map[string]string                 `json:"directories"`
	TempDirectoryID string                            `json:"temp_directory_id,omitempty"`
	Items           map[string]cloudTransferItemState `json:"items"`
	ManagedRoot     string                            `json:"managed_root,omitempty"`
	Materialized    map[string]materializedItemState  `json:"materialized,omitempty"`
	BatchIntent     *cloudTransferBatchIntent         `json:"batch_intent,omitempty"`
}

type cloudDirectoryAttemptCache struct {
	basePath string
	resolver cloudpkg.DirectoryPathResolver
	listings map[string][]cloudpkg.Item
}

type cloudDirectoryAttemptContextKey struct{}

func withCloudDirectoryAttempt(ctx context.Context, driver cloudpkg.Driver, basePath string) context.Context {
	cache := &cloudDirectoryAttemptCache{basePath: basePath, listings: map[string][]cloudpkg.Item{}}
	cache.resolver, _ = driver.(cloudpkg.DirectoryPathResolver)
	return context.WithValue(ctx, cloudDirectoryAttemptContextKey{}, cache)
}

func cloudDirectoryAttemptFromContext(ctx context.Context) *cloudDirectoryAttemptCache {
	cache, _ := ctx.Value(cloudDirectoryAttemptContextKey{}).(*cloudDirectoryAttemptCache)
	return cache
}

type materializedItemState struct {
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
	SHA1         string `json:"sha1,omitempty"`
	Status       string `json:"status"`
}

type cloudTransferItemState struct {
	SourceID       string `json:"source_id"`
	CurrentID      string `json:"current_id,omitempty"`
	TargetParentID string `json:"target_parent_id,omitempty"`
	TargetName     string `json:"target_name,omitempty"`
	Status         string `json:"status"`
}

// cloudTransferBatchIntent is private durable state. Provider identities and
// names intentionally remain in CloudStateJSON and never enter public task
// summaries, logs, audit details, or API DTOs.
type cloudTransferBatchIntent struct {
	Version        int                      `json:"version"`
	Operation      string                   `json:"operation"`
	TargetParentID string                   `json:"target_parent_id"`
	Items          []cloudTransferBatchItem `json:"items"`
}

type cloudTransferBatchItem struct {
	SourceID       string `json:"source_id"`
	SourceParentID string `json:"source_parent_id"`
	SourceName     string `json:"source_name"`
	TargetParentID string `json:"target_parent_id"`
	TargetName     string `json:"target_name"`
	Size           int64  `json:"size"`
	SHA1           string `json:"sha1,omitempty"`
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
	timings := cloudpkg.NewOperationTimingCollector()
	ctx = cloudpkg.WithOperationTimingCollector(ctx, timings)
	ctx = cloudpkg.WithReadClass(ctx, cloudpkg.ReadClassPipeline)
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
	batchMutations, _ := driver.(cloudpkg.BatchMutationDriver)

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
	targetBasePath, _ := joinProviderPath(targetStorage.RootDisplayPath, download.TargetRelativeRoot)
	targetRoot, err := resolveCloudTargetRoot(ctx, driver, targetBasePath, download.TargetProviderRootID, targetStorage.RootPath)
	if err != nil || !targetRoot.IsDir {
		return w.cloudFailure(task, cloudTransferError("cloud_transfer_boundary_invalid", false, err))
	}
	ctx = withCloudDirectoryAttempt(ctx, driver, targetBasePath)

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
	if err := w.persistCloudState(&task, state, models.TransferTaskStatusCheckingDirectories, completedCloudItems(state), &encodedSummary); err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}
	serverlog.OperationPan115CloudTransfer.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Int("files", len(targets)).Msg(serverlog.OperationPan115CloudTransfer.Message("已完成命名规划，开始检查目标目录"))

	for _, target := range targets {
		if err := validateCloudTargetItem(target); err != nil {
			return w.cloudFailure(task, cloudTransferError("cloud_transfer_manifest_invalid", false, err))
		}
		if download.TransferMode == models.MediaLibraryTransferCopy && strings.TrimSpace(target.File.SHA1) == "" {
			return w.cloudFailure(task, cloudTransferError("cloud_transfer_manifest_invalid", false, errors.New("copy source has no stable content identity")))
		}
	}
	// Build the directory DAG before any move/copy. A season or title directory
	// shared by many files is reconciled exactly once per attempt instead of
	// paying a provider Stat/List round trip for every episode.
	validatedDirectories := map[string]struct{}{".": {}}
	for _, directory := range uniqueCloudTargetDirectories(targets) {
		if _, err := w.ensureCloudDirectory(ctx, mutations, &task, &state, directory, validatedDirectories); err != nil {
			return w.cloudFailure(task, err)
		}
	}

	policy := download.ConflictPolicy
	if response := transferActionResponse(taskJobCheckpoint(w.service.db, task.JobID)); response != "" {
		policy = response
	}
	if err := w.persistCloudState(&task, state, models.TransferTaskStatusCheckingConflicts, completedCloudItems(state), nil); err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
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
	if batchMutations == nil && state.BatchIntent != nil {
		if err := downgradeCloudBatchIntent(&state, targets, download.TransferMode); err != nil {
			return w.cloudFailure(task, cloudTransferError("cloud_transfer_state_invalid", false, err))
		}
	}
	if batchMutations != nil && policy == models.MediaLibraryConflictOverwrite && len(conflicts) > 0 {
		if err := recycleCloudConflictBatches(ctx, batchMutations, driver, download.TargetProviderRootID, conflicts); err != nil {
			return w.cloudFailure(task, err)
		}
		// Every exact conflict has been reconciled as absent. Clearing this private
		// attempt-local projection prevents the singleton compatibility path from
		// issuing a second recycle request.
		conflicts = map[string]cloudpkg.Item{}
	}

	if err := w.persistCloudState(&task, state, models.TransferTaskStatusMoving, completedCloudItems(state), nil); err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}
	if batchMutations != nil && download.TransferMode == models.MediaLibraryTransferCopy {
		if err := w.prestageCloudCopyBatches(ctx, batchMutations, driver, &task, &state, download, sourceStorage, targets, conflicts, applied, policy); err != nil {
			return w.cloudFailure(task, err)
		}
	}
	if batchMutations != nil && download.TransferMode == models.MediaLibraryTransferMove {
		if err := w.executeCloudMoveBatches(ctx, batchMutations, driver, &task, &state, download, sourceStorage, targets, conflicts, applied, policy); err != nil {
			return w.cloudFailure(task, err)
		}
		for index := range targets {
			if index >= len(summary.Items) {
				break
			}
			summary.Items[index].RelativePath = targets[index].Relative
			if state.Items[targets[index].File.ProviderItemID].Status == "skipped" {
				summary.Items[index].Result = "skipped"
			} else {
				summary.Items[index].Result = "completed"
			}
		}
		encodedSummary, err = encodeTransferPlanSummary(summary)
		if err != nil {
			return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
		}
		if err := w.persistCloudState(&task, state, models.TransferTaskStatusMoving, completedCloudItems(state), &encodedSummary); err != nil {
			return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
		}
		processed64, total64 := int64(completedCloudItems(state)), int64(len(targets))
		progress := float64(processed64) * 100 / float64(total64)
		if err := runtime.Heartbeat(&progress, &processed64, &total64, nil, nil); err != nil {
			return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "入库任务租约已失效"}
		}
	} else {
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
					invalidateCloudDirectoryListings(ctx, conflict.ParentID)
				default:
					return w.cloudFailure(task, cloudTransferError("transfer_conflict_failed", false, nil))
				}
			}
			if state.Items[key].Status == "" || state.Items[key].Status == "copied" {
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
					err = w.executeCloudMove(ctx, mutations, driver, &task, &state, source, targetParentID, targetName)
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
			if err := w.persistCloudState(&task, state, models.TransferTaskStatusMoving, processed, &encodedSummary); err != nil {
				return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
			}
			processed64, total64 := int64(processed), int64(len(targets))
			progress := float64(processed64) * 100 / float64(total64)
			if err := runtime.Heartbeat(&progress, &processed64, &total64, nil, nil); err != nil {
				return WorkerResult{ErrorCode: CodeQueueLeaseInvalid, ErrorMessage: "入库任务租约已失效"}
			}
		}
	}

	if download.TransferMode == models.MediaLibraryTransferCopy && state.TempDirectoryID != "" {
		tempID := state.TempDirectoryID
		if _, err := providerItemWithinRoot(ctx, driver, tempID, download.TargetProviderRootID); err != nil {
			if code, _ := cloudpkg.ErrorInfo(err); code != cloudpkg.CodeNotFound {
				return w.cloudFailure(task, cloudTransferError("cloud_transfer_boundary_invalid", false, err))
			}
		} else {
			items, err := listCloudTargetDirectory(ctx, driver, tempID)
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
	if err := w.persistCloudState(&task, state, models.TransferTaskStatusReconciling, len(targets), nil); err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
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
		return w.service.audit.Record(tx, &task.OwnerID, "transfer.complete", "transfer_task", task.ID, "success", map[string]any{"download_task_id": task.DownloadTaskID, "media_library_id": task.LibraryID, "mode": download.TransferMode, "files": len(targets), "provider": cloudpkg.ProviderPan115}, RequestContext{})
	})
	if errors.Is(err, context.Canceled) {
		return WorkerResult{}
	}
	if err != nil {
		return w.cloudFailure(task, cloudTransferError("transfer_state_persist_failed", true, err))
	}
	task.Phase = models.TransferTaskStatusCompleted
	result := w.finishCompletedTransfer(ctx, task)
	timing := timings.Snapshot()
	serverlog.OperationPan115CloudTransfer.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Str("transfer_mode", download.TransferMode).Int("files", len(targets)).
		Int64("duration_ms", time.Since(started).Milliseconds()).Int("provider_wait_calls", timing.ProviderWaitCalls).Int64("provider_wait_ms", timing.ProviderWait.Milliseconds()).
		Int("provider_call_calls", timing.ProviderCallCalls).Int64("provider_call_ms", timing.ProviderCall.Milliseconds()).Int("target_list_calls", timing.TargetListCalls).
		Int64("target_list_ms", timing.TargetList.Milliseconds()).Int("batch_mutation_calls", timing.BatchMutationCalls).Int64("batch_mutation_ms", timing.BatchMutation.Milliseconds()).
		Int("db_checkpoint_calls", timing.DBCheckpointCalls).Int64("db_checkpoint_ms", timing.DBCheckpoint.Milliseconds()).Msg(serverlog.OperationPan115CloudTransfer.Message("完成"))
	return result
}

func uniqueCloudTargetDirectories(targets []transferTargetItem) []string {
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		directory := pathpkg.Clean(pathpkg.Dir(target.Relative))
		if directory == "." || directory == "" {
			continue
		}
		seen[directory] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for directory := range seen {
		result = append(result, directory)
	}
	sort.Slice(result, func(i, j int) bool {
		leftDepth := strings.Count(result[i], "/")
		rightDepth := strings.Count(result[j], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return result[i] < result[j]
	})
	return result
}

func decodeCloudTransferState(raw string) (cloudTransferState, error) {
	state := cloudTransferState{Version: cloudTransferStateVersion, Directories: map[string]string{}, Items: map[string]cloudTransferItemState{}, Materialized: map[string]materializedItemState{}}
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
	if state.Materialized == nil {
		state.Materialized = map[string]materializedItemState{}
	}
	if err := validateCloudBatchIntent(state.BatchIntent); err != nil {
		return state, err
	}
	return state, nil
}

func validateCloudBatchIntent(intent *cloudTransferBatchIntent) error {
	if intent == nil {
		return nil
	}
	if intent.Version != cloudBatchIntentVersion || (intent.Operation != "move" && intent.Operation != "copy") || strings.TrimSpace(intent.TargetParentID) == "" || len(intent.Items) == 0 || len(intent.Items) > cloudpkg.MaxBatchMutationItems {
		return errors.New("cloud transfer batch intent is invalid")
	}
	seen := make(map[string]struct{}, len(intent.Items))
	for _, item := range intent.Items {
		if strings.TrimSpace(item.SourceID) == "" || item.SourceID != strings.TrimSpace(item.SourceID) ||
			strings.TrimSpace(item.SourceParentID) == "" || item.SourceParentID != strings.TrimSpace(item.SourceParentID) ||
			strings.TrimSpace(item.TargetParentID) == "" || item.TargetParentID != strings.TrimSpace(item.TargetParentID) ||
			(intent.Operation == "move" && item.TargetParentID != intent.TargetParentID) || item.SourceName == "" || item.TargetName == "" || item.Size < 0 ||
			pathpkg.Base(item.SourceName) != item.SourceName || pathpkg.Base(item.TargetName) != item.TargetName ||
			strings.ContainsAny(item.SourceID+item.SourceParentID+item.SourceName+item.TargetParentID+item.TargetName+item.SHA1, "\x00\r\n\\") || strings.TrimSpace(item.SHA1) != item.SHA1 {
			return errors.New("cloud transfer batch item is invalid")
		}
		if _, duplicate := seen[item.SourceID]; duplicate {
			return errors.New("cloud transfer batch item is duplicated")
		}
		seen[item.SourceID] = struct{}{}
	}
	return nil
}

func validateCloudBatchIntentTargets(intent *cloudTransferBatchIntent, state cloudTransferState, targets []transferTargetItem, operation string) error {
	if err := validateCloudBatchIntent(intent); err != nil {
		return err
	}
	if intent == nil || intent.Operation != operation {
		return errors.New("cloud transfer batch operation does not match transfer mode")
	}
	expected := make(map[string]cloudTransferBatchItem, len(targets))
	for _, target := range targets {
		item := cloudTransferBatchItem{
			SourceID:       target.File.ProviderItemID,
			SourceParentID: target.File.ProviderParentID,
			SourceName:     pathpkg.Base(strings.ReplaceAll(target.File.RelativePath, "\\", "/")),
			TargetParentID: state.Directories[pathpkg.Dir(target.Relative)],
			TargetName:     pathpkg.Base(target.Relative),
			Size:           target.File.Size,
			SHA1:           target.File.SHA1,
		}
		expected[item.SourceID] = item
	}
	for _, item := range intent.Items {
		want, ok := expected[item.SourceID]
		if !ok || item.SourceParentID != want.SourceParentID || item.SourceName != want.SourceName ||
			item.TargetParentID != want.TargetParentID || item.TargetName != want.TargetName || item.Size != want.Size ||
			!strings.EqualFold(strings.TrimSpace(item.SHA1), strings.TrimSpace(want.SHA1)) {
			return errors.New("cloud transfer batch intent does not match the current manifest")
		}
	}
	if operation == "copy" && intent.TargetParentID != state.TempDirectoryID {
		return errors.New("cloud copy batch intent does not match the staging directory")
	}
	return nil
}

func downgradeCloudBatchIntent(state *cloudTransferState, targets []transferTargetItem, operation string) error {
	if state == nil || state.BatchIntent == nil {
		return nil
	}
	if err := validateCloudBatchIntentTargets(state.BatchIntent, *state, targets, operation); err != nil {
		return err
	}
	// The singleton move/copy reconciler already converges stable moved IDs and
	// task-private copy candidates. Clearing only the in-memory optional intent
	// lets a capability rollback make progress; the next ordinary checkpoint is
	// the durable downgrade. A crash before then still retains the old intent.
	state.BatchIntent = nil
	return nil
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

func (w *TransferWorker) persistCloudStateTimed(ctx context.Context, task *models.TransferTask, state cloudTransferState, phase string, processed int, summary *string) error {
	started := time.Now()
	defer func() { cloudpkg.RecordDBCheckpoint(ctx, time.Since(started)) }()
	return w.persistCloudState(task, state, phase, processed, summary)
}

func (w *TransferWorker) ensureCloudDirectory(ctx context.Context, mutations cloudpkg.MutationDriver, task *models.TransferTask, state *cloudTransferState, relative string, validated map[string]struct{}) (string, error) {
	relative = pathpkg.Clean(relative)
	if relative == "." || relative == "" {
		return state.Directories["."], nil
	}
	if _, ok := validated[relative]; ok && state.Directories[relative] != "" {
		return state.Directories[relative], nil
	}
	if cache := cloudDirectoryAttemptFromContext(ctx); cache != nil && cache.resolver != nil && cache.basePath != "" {
		providerPath, pathErr := joinProviderPath(cache.basePath, "/"+relative)
		if pathErr == nil {
			item, resolveErr := cache.resolver.ResolveDirectory(ctx, providerPath)
			if resolveErr == nil && item.IsDir {
				if saved := state.Directories[relative]; saved != "" && saved != item.ID {
					return "", cloudTransferError("cloud_transfer_boundary_invalid", false, errors.New("cloud directory identity changed"))
				}
				state.Directories[relative] = item.ID
				validated[relative] = struct{}{}
				if err := w.persistCloudState(task, *state, models.TransferTaskStatusCheckingDirectories, task.ProcessedFiles, nil); err != nil {
					return "", cloudTransferError("transfer_state_persist_failed", true, err)
				}
				return item.ID, nil
			}
			if resolveErr != nil {
				code, _ := cloudpkg.ErrorInfo(resolveErr)
				if code != cloudpkg.CodeNotFound {
					return "", resolveErr
				}
			}
		}
	}
	if id := state.Directories[relative]; id != "" {
		item, err := providerItemWithinRoot(ctx, mutations, id, state.Directories["."])
		if err == nil && item.IsDir {
			validated[relative] = struct{}{}
			return id, nil
		}
		delete(state.Directories, relative)
	}
	parentPath := pathpkg.Dir(relative)
	parentID, err := w.ensureCloudDirectory(ctx, mutations, task, state, parentPath, validated)
	if err != nil {
		return "", err
	}
	name := pathpkg.Base(relative)
	items, err := listCloudTargetDirectoryCached(ctx, mutations, parentID)
	if err != nil {
		return "", err
	}
	matches := namedCloudItems(items, name)
	if len(matches) > 1 || (len(matches) == 1 && !matches[0].IsDir) {
		return "", cloudTransferError(cloudpkg.CodeConflict, false, errors.New("cloud directory name is ambiguous"))
	}
	var directory cloudpkg.Item
	createdDirectory := false
	if len(matches) == 1 {
		directory = matches[0]
	} else {
		if err := w.persistCloudState(task, *state, models.TransferTaskStatusCreatingDirectories, task.ProcessedFiles, nil); err != nil {
			return "", cloudTransferError("transfer_state_persist_failed", true, err)
		}
		directory, err = mutations.CreateDirectory(ctx, parentID, name)
		if err != nil {
			items, listErr := listCloudTargetDirectory(ctx, mutations, parentID)
			matches = namedCloudItems(items, name)
			if listErr != nil || len(matches) != 1 || !matches[0].IsDir {
				return "", err
			}
			directory = matches[0]
		} else {
			createdDirectory = true
		}
	}
	if cache := cloudDirectoryAttemptFromContext(ctx); cache != nil {
		parentItems := cache.listings[parentID]
		if len(namedCloudItems(parentItems, directory.Name)) == 0 {
			cache.listings[parentID] = append(parentItems, directory)
		}
		if createdDirectory {
			cache.listings[directory.ID] = []cloudpkg.Item{}
		}
	}
	state.Directories[relative] = directory.ID
	validated[relative] = struct{}{}
	phase := models.TransferTaskStatusCheckingDirectories
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
			items, err = listCloudTargetDirectoryCached(ctx, driver, parentID)
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
					items, err = listCloudTargetDirectory(ctx, driver, parentID)
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

func recycleCloudConflictBatches(ctx context.Context, mutations cloudpkg.BatchMutationDriver, driver cloudpkg.Driver, targetRootID string, conflicts map[string]cloudpkg.Item) error {
	byParent := make(map[string]map[string]cloudpkg.Item)
	for _, conflict := range conflicts {
		if conflict.ID == "" {
			continue
		}
		if conflict.IsDir || strings.TrimSpace(conflict.ParentID) == "" {
			return cloudTransferError("cloud_transfer_target_type_conflict", false, nil)
		}
		items := byParent[conflict.ParentID]
		if items == nil {
			items = make(map[string]cloudpkg.Item)
			byParent[conflict.ParentID] = items
		}
		if previous, duplicate := items[conflict.ID]; duplicate && (previous.Name != conflict.Name || previous.Size != conflict.Size || !strings.EqualFold(previous.SHA1, conflict.SHA1)) {
			return cloudTransferError(cloudpkg.CodeResponseInvalid, false, errors.New("cloud conflict identity is inconsistent"))
		}
		items[conflict.ID] = conflict
	}
	parentIDs := make([]string, 0, len(byParent))
	for parentID := range byParent {
		parentIDs = append(parentIDs, parentID)
	}
	sort.Strings(parentIDs)

	proof := newProviderBoundaryProof(driver)
	validated := make(map[string][]string, len(parentIDs))
	for _, parentID := range parentIDs {
		parent, err := proof.within(ctx, parentID, targetRootID)
		if err != nil || !parent.IsDir {
			return cloudTransferError("cloud_transfer_boundary_invalid", false, err)
		}
		listed, err := listCloudTargetDirectory(ctx, driver, parentID)
		if err != nil {
			return err
		}
		current := make(map[string]cloudpkg.Item, len(listed))
		for _, item := range listed {
			if _, duplicate := current[item.ID]; duplicate {
				return cloudTransferError(cloudpkg.CodeResponseInvalid, false, errors.New("cloud conflict listing contains duplicate identity"))
			}
			current[item.ID] = item
		}
		ids := make([]string, 0, len(byParent[parentID]))
		for itemID, expected := range byParent[parentID] {
			item, exists := current[itemID]
			if !exists {
				continue
			}
			if item.IsDir || item.ParentID != parentID || item.Name != expected.Name || item.Size != expected.Size ||
				(expected.SHA1 != "" && !strings.EqualFold(item.SHA1, expected.SHA1)) {
				return cloudTransferError(cloudpkg.CodeConflict, false, errors.New("cloud conflict identity changed"))
			}
			ids = append(ids, itemID)
		}
		sort.Strings(ids)
		validated[parentID] = ids
	}

	for _, parentID := range parentIDs {
		ids := validated[parentID]
		for start := 0; start < len(ids); start += cloudpkg.MaxBatchMutationItems {
			end := start + cloudpkg.MaxBatchMutationItems
			if end > len(ids) {
				end = len(ids)
			}
			chunk := ids[start:end]
			callErr := executeCloudBatchMutation(ctx, func() error { return mutations.RecycleMany(ctx, chunk) })
			invalidateCloudDirectoryListings(ctx, parentID)
			after, reconcileErr := listCloudTargetDirectory(ctx, driver, parentID)
			if reconcileErr != nil {
				if callErr != nil {
					return callErr
				}
				return reconcileErr
			}
			remaining := make(map[string]struct{}, len(after))
			for _, item := range after {
				if _, duplicate := remaining[item.ID]; duplicate {
					return cloudTransferError(cloudpkg.CodeResponseInvalid, false, errors.New("cloud conflict reconciliation contains duplicate identity"))
				}
				remaining[item.ID] = struct{}{}
			}
			for _, itemID := range chunk {
				if _, exists := remaining[itemID]; exists {
					if callErr != nil {
						return callErr
					}
					return cloudTransferError(cloudpkg.CodeMutationUnknown, true, errors.New("cloud conflict batch result is incomplete"))
				}
			}
		}
	}
	return nil
}

type providerBoundaryProof struct {
	driver cloudpkg.Driver
	items  map[string]cloudpkg.Item
}

func newProviderBoundaryProof(driver cloudpkg.Driver) *providerBoundaryProof {
	return &providerBoundaryProof{driver: driver, items: map[string]cloudpkg.Item{}}
}

func (proof *providerBoundaryProof) stat(ctx context.Context, itemID string) (cloudpkg.Item, error) {
	if item, ok := proof.items[itemID]; ok {
		return item, nil
	}
	item, err := proof.driver.Stat(ctx, itemID)
	if err != nil {
		return cloudpkg.Item{}, err
	}
	if strings.TrimSpace(item.ID) != itemID {
		return cloudpkg.Item{}, errors.New("provider returned a mismatched item identity")
	}
	proof.items[itemID] = item
	return item, nil
}

func (proof *providerBoundaryProof) within(ctx context.Context, itemID, rootID string) (cloudpkg.Item, error) {
	itemID, rootID = strings.TrimSpace(itemID), strings.TrimSpace(rootID)
	if itemID == "" || rootID == "" {
		return cloudpkg.Item{}, errors.New("provider item boundary is incomplete")
	}
	current := itemID
	visited := make(map[string]struct{}, maxCloudBoundaryDepth)
	var initial cloudpkg.Item
	for depth := 0; depth < maxCloudBoundaryDepth; depth++ {
		item, err := proof.stat(ctx, current)
		if err != nil {
			return cloudpkg.Item{}, err
		}
		if depth == 0 {
			initial = item
		}
		if current == rootID {
			return initial, nil
		}
		if _, exists := visited[current]; exists {
			return cloudpkg.Item{}, errors.New("provider item parent cycle")
		}
		visited[current] = struct{}{}
		current = strings.TrimSpace(item.ParentID)
		if current == "" || (current == "0" && rootID != "0") {
			return cloudpkg.Item{}, errors.New("provider item is outside the configured root")
		}
	}
	return cloudpkg.Item{}, errors.New("provider item parent depth exceeded")
}

func (w *TransferWorker) prestageCloudCopyBatches(ctx context.Context, mutations cloudpkg.BatchMutationDriver, driver cloudpkg.Driver, task *models.TransferTask, state *cloudTransferState, download models.DownloadTask, sourceStorage models.Storage, targets []transferTargetItem, conflicts map[string]cloudpkg.Item, applied map[string]bool, policy string) error {
	if state.BatchIntent != nil {
		if err := validateCloudBatchIntentTargets(state.BatchIntent, *state, targets, "copy"); err != nil {
			return cloudTransferError("cloud_transfer_state_invalid", false, err)
		}
		remaining, err := w.reconcileCloudCopyIntent(ctx, driver, state)
		if err != nil {
			return err
		}
		if len(remaining) > 0 {
			state.BatchIntent.Items = remaining
			if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
				return cloudTransferError("transfer_state_persist_failed", true, err)
			}
		} else {
			state.BatchIntent = nil
			if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
				return cloudTransferError("transfer_state_persist_failed", true, err)
			}
		}
	}
	if state.TempDirectoryID == "" {
		name := ".ohmycine-import-" + strings.ReplaceAll(task.ID, "-", "")
		if len(name) > 48 {
			name = name[:48]
		}
		items, err := listCloudTargetDirectory(ctx, driver, state.Directories["."])
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
			created, createErr := mutations.CreateDirectory(ctx, state.Directories["."], name)
			if createErr != nil {
				items, listErr := listCloudTargetDirectory(ctx, driver, state.Directories["."])
				matches = namedCloudItems(items, name)
				if listErr != nil || len(matches) != 1 || !matches[0].IsDir {
					return createErr
				}
				created = matches[0]
			}
			state.TempDirectoryID = created.ID
		}
		if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
		}
	}
	if _, err := providerItemWithinRoot(ctx, driver, state.TempDirectoryID, state.Directories["."]); err != nil {
		return cloudTransferError("cloud_transfer_boundary_invalid", false, err)
	}

	pending := make([]cloudTransferBatchItem, 0, len(targets))
	for _, target := range targets {
		key := target.File.ProviderItemID
		if saved := state.Items[key]; saved.CurrentID != "" || saved.Status == "completed" || saved.Status == "skipped" || applied[key] {
			continue
		}
		if conflict := conflicts[key]; conflict.ID != "" && policy == models.MediaLibraryConflictSkip {
			continue
		}
		pending = append(pending, cloudTransferBatchItem{
			SourceID: key, SourceParentID: target.File.ProviderParentID,
			SourceName:     pathpkg.Base(strings.ReplaceAll(target.File.RelativePath, "\\", "/")),
			TargetParentID: state.Directories[pathpkg.Dir(target.Relative)], TargetName: pathpkg.Base(target.Relative),
			Size: target.File.Size, SHA1: target.File.SHA1,
		})
	}
	if len(pending) < 2 {
		return nil
	}
	// Copy result IDs are provider-created, so reconciliation uses the stable
	// name+size+SHA1 tuple. Ambiguous tuples retain the singleton fail-closed
	// path instead of entering a batch that cannot be safely attributed.
	counts := make(map[string]int, len(pending))
	nameCounts := make(map[string]int, len(pending))
	for _, item := range pending {
		counts[cloudCopyBatchKey(item)]++
		nameCounts[strings.ToLower(item.SourceName)]++
	}
	batchable := pending[:0]
	for _, item := range pending {
		// Every copy in one provider request lands in the same task-private
		// directory under its original name. Even different content with the same
		// case-folded name can collide there, so keep those items on the existing
		// singleton fail-closed path.
		if counts[cloudCopyBatchKey(item)] == 1 && nameCounts[strings.ToLower(item.SourceName)] == 1 {
			batchable = append(batchable, item)
		}
	}
	if len(batchable) < 2 {
		return nil
	}
	proof := newProviderBoundaryProof(driver)
	packageRootID := strings.TrimSpace(download.ProviderOutputID)
	packageRoot, err := proof.within(ctx, packageRootID, sourceStorage.RootPath)
	if err != nil || !packageRoot.IsDir {
		return cloudTransferError("cloud_transfer_source_changed", false, err)
	}
	if err := preflightCloudCopySources(ctx, driver, proof, packageRootID, batchable); err != nil {
		return err
	}
	for start := 0; start < len(batchable); start += cloudpkg.MaxBatchMutationItems {
		end := start + cloudpkg.MaxBatchMutationItems
		if end > len(batchable) {
			end = len(batchable)
		}
		chunk := append([]cloudTransferBatchItem(nil), batchable[start:end]...)
		state.BatchIntent = &cloudTransferBatchIntent{Version: cloudBatchIntentVersion, Operation: "copy", TargetParentID: state.TempDirectoryID, Items: chunk}
		if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
		}
		ids := make([]string, 0, len(chunk))
		for _, item := range chunk {
			ids = append(ids, item.SourceID)
		}
		callErr := executeCloudBatchMutation(ctx, func() error { return mutations.CopyMany(ctx, ids, state.TempDirectoryID) })
		invalidateCloudDirectoryListings(ctx, state.TempDirectoryID)
		remaining, reconcileErr := w.reconcileCloudCopyIntent(ctx, driver, state)
		if reconcileErr != nil {
			return reconcileErr
		}
		if len(remaining) > 0 {
			state.BatchIntent.Items = remaining
			if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
				return cloudTransferError("transfer_state_persist_failed", true, err)
			}
			if callErr != nil {
				return callErr
			}
			return cloudTransferError(cloudpkg.CodeMutationUnknown, true, errors.New("cloud copy batch result is incomplete"))
		}
		state.BatchIntent = nil
		if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
		}
		_ = callErr
	}
	return nil
}

func cloudCopyBatchKey(item cloudTransferBatchItem) string {
	return item.SourceName + "\x00" + strconv.FormatInt(item.Size, 10) + "\x00" + strings.ToLower(strings.TrimSpace(item.SHA1))
}

func preflightCloudCopySources(ctx context.Context, driver cloudpkg.Driver, proof *providerBoundaryProof, packageRootID string, pending []cloudTransferBatchItem) error {
	byParent := make(map[string][]cloudTransferBatchItem)
	for _, item := range pending {
		if strings.TrimSpace(item.SourceParentID) == "" {
			return cloudTransferError("cloud_transfer_manifest_invalid", false, errors.New("cloud source parent identity is missing"))
		}
		byParent[item.SourceParentID] = append(byParent[item.SourceParentID], item)
	}
	for parentID, expectedItems := range byParent {
		parent, err := proof.within(ctx, parentID, packageRootID)
		if err != nil || !parent.IsDir {
			return cloudTransferError("cloud_transfer_source_changed", false, err)
		}
		items, err := listCloudDirectory(ctx, driver, parentID)
		if err != nil {
			return err
		}
		listed := make(map[string]cloudpkg.Item, len(items))
		for _, item := range items {
			if _, duplicate := listed[item.ID]; duplicate {
				return cloudTransferError(cloudpkg.CodeResponseInvalid, false, errors.New("cloud source listing contains duplicate identity"))
			}
			listed[item.ID] = item
		}
		for _, expected := range expectedItems {
			item, exists := listed[expected.SourceID]
			if !exists || item.ParentID != parentID || item.Name != expected.SourceName || item.IsDir || item.Size != expected.Size || (expected.SHA1 != "" && !strings.EqualFold(item.SHA1, expected.SHA1)) {
				return cloudTransferError("cloud_transfer_source_changed", false, errors.New("cloud source identity changed"))
			}
		}
	}
	return nil
}

func (w *TransferWorker) reconcileCloudCopyIntent(ctx context.Context, driver cloudpkg.Driver, state *cloudTransferState) ([]cloudTransferBatchItem, error) {
	intent := state.BatchIntent
	if err := validateCloudBatchIntent(intent); err != nil || intent == nil || intent.Operation != "copy" {
		return nil, cloudTransferError("cloud_transfer_state_invalid", false, err)
	}
	items, err := listCloudTargetDirectory(ctx, driver, intent.TargetParentID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" || item.ParentID != intent.TargetParentID {
			return nil, cloudTransferError(cloudpkg.CodeResponseInvalid, false, errors.New("cloud copy staging listing is invalid"))
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return nil, cloudTransferError(cloudpkg.CodeResponseInvalid, false, errors.New("cloud copy staging listing contains duplicate identity"))
		}
		seen[item.ID] = struct{}{}
	}
	remaining := make([]cloudTransferBatchItem, 0, len(intent.Items))
	claimed := make(map[string]struct{}, len(intent.Items))
	for _, expected := range intent.Items {
		matches := make([]cloudpkg.Item, 0, 1)
		for _, item := range items {
			if _, used := claimed[item.ID]; used || item.IsDir || item.Name != expected.SourceName || item.Size != expected.Size || (expected.SHA1 != "" && !strings.EqualFold(item.SHA1, expected.SHA1)) {
				continue
			}
			matches = append(matches, item)
		}
		if len(matches) == 0 {
			remaining = append(remaining, expected)
			continue
		}
		if len(matches) != 1 {
			return nil, cloudTransferError(cloudpkg.CodeMutationUnknown, false, errors.New("copied item identity is ambiguous"))
		}
		copyItem := matches[0]
		claimed[copyItem.ID] = struct{}{}
		state.Items[expected.SourceID] = cloudTransferItemState{SourceID: expected.SourceID, CurrentID: copyItem.ID, TargetParentID: expected.TargetParentID, TargetName: expected.TargetName, Status: "copied"}
	}
	return remaining, nil
}

func (w *TransferWorker) executeCloudMoveBatches(ctx context.Context, mutations cloudpkg.BatchMutationDriver, driver cloudpkg.Driver, task *models.TransferTask, state *cloudTransferState, download models.DownloadTask, sourceStorage models.Storage, targets []transferTargetItem, conflicts map[string]cloudpkg.Item, applied map[string]bool, policy string) error {
	if state.BatchIntent != nil {
		if err := validateCloudBatchIntentTargets(state.BatchIntent, *state, targets, "move"); err != nil {
			return cloudTransferError("cloud_transfer_state_invalid", false, err)
		}
	}
	for _, target := range targets {
		key := target.File.ProviderItemID
		if saved := state.Items[key]; saved.Status == "completed" || saved.Status == "skipped" {
			continue
		}
		if applied[key] {
			state.Items[key] = cloudTransferItemState{SourceID: key, CurrentID: key, TargetParentID: state.Directories[pathpkg.Dir(target.Relative)], TargetName: pathpkg.Base(target.Relative), Status: "completed"}
			continue
		}
		conflict := conflicts[key]
		if conflict.ID == "" {
			continue
		}
		switch policy {
		case models.MediaLibraryConflictSkip:
			state.Items[key] = cloudTransferItemState{SourceID: key, TargetParentID: conflict.ParentID, TargetName: conflict.Name, Status: "skipped"}
		case models.MediaLibraryConflictOverwrite:
			if conflict.IsDir {
				return cloudTransferError("cloud_transfer_target_type_conflict", false, nil)
			}
			if _, err := providerItemWithinRoot(ctx, driver, conflict.ID, download.TargetProviderRootID); err != nil {
				return cloudTransferError("cloud_transfer_boundary_invalid", false, err)
			}
			if err := mutations.Recycle(ctx, conflict.ID); err != nil {
				if _, statErr := driver.Stat(ctx, conflict.ID); statErr == nil {
					return err
				} else if code, _ := cloudpkg.ErrorInfo(statErr); code != cloudpkg.CodeNotFound {
					return err
				}
			}
			invalidateCloudDirectoryListings(ctx, conflict.ParentID)
		default:
			return cloudTransferError("transfer_conflict_failed", false, nil)
		}
	}

	if state.BatchIntent != nil {
		remaining, err := w.reconcileCloudMoveIntent(ctx, mutations, driver, task, state)
		if err != nil {
			return err
		}
		if len(remaining) > 0 {
			state.BatchIntent.Items = remaining
			if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
				return cloudTransferError("transfer_state_persist_failed", true, err)
			}
		} else {
			state.BatchIntent = nil
			if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
				return cloudTransferError("transfer_state_persist_failed", true, err)
			}
		}
	}

	pending := make([]cloudTransferBatchItem, 0, len(targets))
	for _, target := range targets {
		key := target.File.ProviderItemID
		if saved := state.Items[key]; saved.Status == "completed" || saved.Status == "skipped" {
			continue
		}
		targetParentID := state.Directories[pathpkg.Dir(target.Relative)]
		pending = append(pending, cloudTransferBatchItem{
			SourceID: key, SourceParentID: target.File.ProviderParentID,
			SourceName:     pathpkg.Base(strings.ReplaceAll(target.File.RelativePath, "\\", "/")),
			TargetParentID: targetParentID, TargetName: pathpkg.Base(target.Relative), Size: target.File.Size, SHA1: target.File.SHA1,
		})
	}
	if len(pending) == 0 {
		return nil
	}

	proof := newProviderBoundaryProof(driver)
	packageRootID := strings.TrimSpace(download.ProviderOutputID)
	packageRoot, err := proof.within(ctx, packageRootID, sourceStorage.RootPath)
	if err != nil || !packageRoot.IsDir {
		return cloudTransferError("cloud_transfer_source_changed", false, err)
	}
	sources, err := preflightCloudMoveSources(ctx, driver, proof, packageRootID, pending)
	if err != nil {
		return err
	}

	groups := make(map[string][]cloudTransferBatchItem)
	for _, item := range pending {
		targetParentID := item.TargetParentID
		if targetParentID == "" {
			return cloudTransferError("cloud_transfer_state_invalid", false, nil)
		}
		if sources[item.SourceID].ParentID == targetParentID {
			intent := &cloudTransferBatchIntent{Version: cloudBatchIntentVersion, Operation: "move", TargetParentID: targetParentID, Items: []cloudTransferBatchItem{item}}
			state.BatchIntent = intent
			if _, err := w.reconcileCloudMoveIntent(ctx, mutations, driver, task, state); err != nil {
				return err
			}
			state.BatchIntent = nil
			continue
		}
		groups[targetParentID] = append(groups[targetParentID], item)
	}
	parentIDs := make([]string, 0, len(groups))
	for parentID := range groups {
		parentIDs = append(parentIDs, parentID)
	}
	sort.Strings(parentIDs)
	for _, parentID := range parentIDs {
		items := groups[parentID]
		for start := 0; start < len(items); start += cloudpkg.MaxBatchMutationItems {
			end := start + cloudpkg.MaxBatchMutationItems
			if end > len(items) {
				end = len(items)
			}
			chunk := append([]cloudTransferBatchItem(nil), items[start:end]...)
			state.BatchIntent = &cloudTransferBatchIntent{Version: cloudBatchIntentVersion, Operation: "move", TargetParentID: parentID, Items: chunk}
			if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
				return cloudTransferError("transfer_state_persist_failed", true, err)
			}
			ids := make([]string, 0, len(chunk))
			for _, item := range chunk {
				ids = append(ids, item.SourceID)
			}
			callErr := executeCloudBatchMutation(ctx, func() error { return mutations.MoveMany(ctx, ids, parentID) })
			invalidateCloudDirectoryListings(ctx, parentID)
			remaining, reconcileErr := w.reconcileCloudMoveIntent(ctx, mutations, driver, task, state)
			if reconcileErr != nil {
				return reconcileErr
			}
			if len(remaining) > 0 {
				state.BatchIntent.Items = remaining
				if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
					return cloudTransferError("transfer_state_persist_failed", true, err)
				}
				if callErr != nil {
					return callErr
				}
				return cloudTransferError(cloudpkg.CodeMutationUnknown, true, errors.New("cloud move batch result is incomplete"))
			}
			state.BatchIntent = nil
			if err := w.persistCloudStateTimed(ctx, task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
				return cloudTransferError("transfer_state_persist_failed", true, err)
			}
			// An error after every identity is observed at the exact destination is
			// an ambiguous provider acknowledgement, not a failed transfer.
			_ = callErr
		}
	}
	return nil
}

func preflightCloudMoveSources(ctx context.Context, driver cloudpkg.Driver, proof *providerBoundaryProof, packageRootID string, pending []cloudTransferBatchItem) (map[string]cloudpkg.Item, error) {
	byParent := make(map[string][]cloudTransferBatchItem)
	for _, item := range pending {
		if strings.TrimSpace(item.SourceParentID) == "" {
			return nil, cloudTransferError("cloud_transfer_manifest_invalid", false, errors.New("cloud source parent identity is missing"))
		}
		byParent[item.SourceParentID] = append(byParent[item.SourceParentID], item)
	}
	parents := make([]string, 0, len(byParent))
	for parentID := range byParent {
		parents = append(parents, parentID)
	}
	sort.Strings(parents)
	result := make(map[string]cloudpkg.Item, len(pending))
	for _, parentID := range parents {
		parent, err := proof.within(ctx, parentID, packageRootID)
		if err != nil || !parent.IsDir {
			return nil, cloudTransferError("cloud_transfer_source_changed", false, err)
		}
		items, err := listCloudDirectory(ctx, driver, parentID)
		if err != nil {
			return nil, err
		}
		listed := make(map[string]cloudpkg.Item, len(items))
		for _, item := range items {
			if _, duplicate := listed[item.ID]; duplicate {
				return nil, cloudTransferError(cloudpkg.CodeResponseInvalid, false, errors.New("cloud source listing contains duplicate identity"))
			}
			listed[item.ID] = item
		}
		for _, expected := range byParent[parentID] {
			item, ok := listed[expected.SourceID]
			if !ok {
				// Legacy checkpoints may have moved the stable identity before the
				// batch-intent format existed. Accept only an exact target parent.
				current, statErr := driver.Stat(ctx, expected.SourceID)
				if statErr != nil {
					return nil, cloudTransferError("cloud_transfer_source_changed", false, statErr)
				}
				if current.ParentID != expected.TargetParentID {
					return nil, cloudTransferError("cloud_transfer_source_changed", false, errors.New("cloud source left the proven package boundary"))
				}
				item = current
			}
			if item.ID != expected.SourceID || item.IsDir || item.Size != expected.Size ||
				(expected.SHA1 != "" && !strings.EqualFold(item.SHA1, expected.SHA1)) ||
				(item.ParentID == parentID && item.Name != expected.SourceName) {
				return nil, cloudTransferError("cloud_transfer_source_changed", false, errors.New("cloud source identity changed"))
			}
			result[expected.SourceID] = item
		}
	}
	return result, nil
}

func listCloudTargetDirectory(ctx context.Context, driver cloudpkg.Driver, parentID string) ([]cloudpkg.Item, error) {
	started := time.Now()
	defer func() { cloudpkg.RecordTargetList(ctx, time.Since(started)) }()
	return listCloudDirectory(ctx, driver, parentID)
}

func listCloudTargetDirectoryCached(ctx context.Context, driver cloudpkg.Driver, parentID string) ([]cloudpkg.Item, error) {
	cache := cloudDirectoryAttemptFromContext(ctx)
	if cache != nil {
		if items, ok := cache.listings[parentID]; ok {
			return items, nil
		}
	}
	items, err := listCloudTargetDirectory(ctx, driver, parentID)
	if err == nil && cache != nil {
		cache.listings[parentID] = items
	}
	return items, err
}

func invalidateCloudDirectoryListings(ctx context.Context, parentIDs ...string) {
	cache := cloudDirectoryAttemptFromContext(ctx)
	if cache == nil {
		return
	}
	for _, parentID := range parentIDs {
		if parentID = strings.TrimSpace(parentID); parentID != "" {
			delete(cache.listings, parentID)
		}
	}
}

func resolveCloudTargetRoot(ctx context.Context, driver cloudpkg.Driver, providerPath, expectedID, storageRootID string) (cloudpkg.Item, error) {
	if resolver, ok := driver.(cloudpkg.DirectoryPathResolver); ok && strings.TrimSpace(providerPath) != "" {
		item, err := resolver.ResolveDirectory(ctx, providerPath)
		if err != nil {
			return cloudpkg.Item{}, err
		}
		if !item.IsDir || strings.TrimSpace(item.ID) != strings.TrimSpace(expectedID) {
			return cloudpkg.Item{}, errors.New("cloud target root identity changed")
		}
		return item, nil
	}
	return providerItemWithinRoot(ctx, driver, expectedID, storageRootID)
}

func executeCloudBatchMutation(ctx context.Context, call func() error) error {
	started := time.Now()
	defer func() { cloudpkg.RecordBatchMutation(ctx, time.Since(started)) }()
	return call()
}

func (w *TransferWorker) reconcileCloudMoveIntent(ctx context.Context, mutations cloudpkg.MutationDriver, driver cloudpkg.Driver, task *models.TransferTask, state *cloudTransferState) ([]cloudTransferBatchItem, error) {
	intent := state.BatchIntent
	if err := validateCloudBatchIntent(intent); err != nil || intent == nil || intent.Operation != "move" {
		return nil, cloudTransferError("cloud_transfer_state_invalid", false, err)
	}
	items, err := listCloudTargetDirectory(ctx, driver, intent.TargetParentID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]cloudpkg.Item, len(items))
	for _, item := range items {
		if _, duplicate := byID[item.ID]; duplicate {
			return nil, cloudTransferError(cloudpkg.CodeResponseInvalid, false, errors.New("cloud target listing contains duplicate identity"))
		}
		byID[item.ID] = item
	}
	remaining := make([]cloudTransferBatchItem, 0, len(intent.Items))
	for _, expected := range intent.Items {
		item, ok := byID[expected.SourceID]
		if !ok {
			remaining = append(remaining, expected)
			continue
		}
		if item.IsDir || item.ParentID != intent.TargetParentID || item.Size != expected.Size || (expected.SHA1 != "" && !strings.EqualFold(item.SHA1, expected.SHA1)) {
			return nil, cloudTransferError("cloud_transfer_source_changed", false, errors.New("cloud move result identity changed"))
		}
		if item.Name != expected.TargetName {
			if err := mutations.Rename(ctx, item.ID, expected.TargetName); err != nil {
				verified, statErr := driver.Stat(ctx, item.ID)
				if statErr != nil || verified.ID != expected.SourceID || verified.IsDir || verified.ParentID != intent.TargetParentID || verified.Name != expected.TargetName || verified.Size != expected.Size ||
					(expected.SHA1 != "" && !strings.EqualFold(verified.SHA1, expected.SHA1)) {
					return nil, err
				}
			}
			invalidateCloudDirectoryListings(ctx, intent.TargetParentID)
		}
		state.Items[expected.SourceID] = cloudTransferItemState{SourceID: expected.SourceID, CurrentID: expected.SourceID, TargetParentID: intent.TargetParentID, TargetName: expected.TargetName, Status: "completed"}
	}
	return remaining, nil
}

func (w *TransferWorker) executeCloudMove(ctx context.Context, mutations cloudpkg.MutationDriver, driver cloudpkg.Driver, task *models.TransferTask, state *cloudTransferState, source cloudpkg.Item, targetParentID, targetName string) error {
	if source.ParentID != targetParentID {
		if err := mutations.Move(ctx, source.ID, targetParentID); err != nil {
			current, statErr := driver.Stat(ctx, source.ID)
			if statErr != nil || current.ParentID != targetParentID {
				return err
			}
		}
		invalidateCloudDirectoryListings(ctx, source.ParentID, targetParentID)
	}
	current, err := driver.Stat(ctx, source.ID)
	if err != nil {
		return err
	}
	if current.Name != targetName {
		if err := w.persistCloudState(task, *state, models.TransferTaskStatusRenaming, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
		}
		if err := mutations.Rename(ctx, source.ID, targetName); err != nil {
			verified, statErr := driver.Stat(ctx, source.ID)
			if statErr != nil || verified.Name != targetName {
				return err
			}
		}
		invalidateCloudDirectoryListings(ctx, targetParentID)
		if err := w.persistCloudState(task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
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
		items, err := listCloudTargetDirectory(ctx, driver, state.Directories["."])
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
				items, listErr := listCloudTargetDirectory(ctx, driver, state.Directories["."])
				matches = namedCloudItems(items, name)
				if listErr != nil || len(matches) != 1 || !matches[0].IsDir {
					return err
				}
				created = matches[0]
			}
			state.TempDirectoryID = created.ID
		}
		if err := w.persistCloudState(task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
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
			invalidateCloudDirectoryListings(ctx, state.TempDirectoryID)
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
		if err := w.persistCloudState(task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
		}
	}
	current, err := driver.Stat(ctx, copyID)
	if err != nil {
		return err
	}
	if current.Name != targetName {
		if err := w.persistCloudState(task, *state, models.TransferTaskStatusRenaming, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
		}
		if err := mutations.Rename(ctx, copyID, targetName); err != nil {
			verified, statErr := driver.Stat(ctx, copyID)
			if statErr != nil || verified.Name != targetName {
				return err
			}
		}
		invalidateCloudDirectoryListings(ctx, current.ParentID)
		if err := w.persistCloudState(task, *state, models.TransferTaskStatusMoving, completedCloudItems(*state), nil); err != nil {
			return cloudTransferError("transfer_state_persist_failed", true, err)
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
		invalidateCloudDirectoryListings(ctx, current.ParentID, targetParentID)
	}
	itemState.Status = "completed"
	state.Items[source.ID] = itemState
	return nil
}

func findCloudCopyCandidate(ctx context.Context, driver cloudpkg.Driver, parentID string, source cloudpkg.Item) (cloudpkg.Item, int, error) {
	items, err := listCloudTargetDirectory(ctx, driver, parentID)
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
	if activeErr := ensureDownloadPipelineActive(w.service.db, task.DownloadTaskID); errors.Is(activeErr, context.Canceled) {
		return WorkerResult{}
	}
	code, retryable := cloudpkg.ErrorInfo(err)
	var failure *cloudTransferFailure
	if errors.As(err, &failure) {
		code, retryable = failure.code, failure.retryable
	}
	code = safeLabel(code, 96)
	message := "115 云端整理失败"
	if retryable {
		phase := task.Phase
		if code == cloudpkg.CodeRateLimited {
			phase = models.TransferTaskStatusRiskBackoff
			message = "115 触发接口风控，云端整理将自动重试"
		} else {
			message = "115 暂时不可用，云端整理将自动重试"
		}
		_ = w.service.db.Model(&task).Updates(map[string]any{"phase": phase, "last_error_code": code, "updated_at": time.Now().UTC()}).Error
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
