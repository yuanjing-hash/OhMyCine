package services

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

const (
	crossSourceRootName         = ".omc-cross-source"
	crossSourceMinimumFreeBytes = uint64(512 * 1024 * 1024)
)

func (w *TransferWorker) materializePan115Source(ctx context.Context, runtime JobRuntime, task models.TransferTask, download models.DownloadTask, manifest downloadpkg.Manifest) (models.TransferTask, string, error) {
	if w.service.connections == nil || download.StagingStorageID == nil || strings.TrimSpace(download.ProviderOutputID) == "" {
		return task, "", cloudTransferError("cross_source_snapshot_invalid", false, nil)
	}
	if download.TransferMode == models.MediaLibraryTransferSymlink && download.TargetStorageType == models.StorageTypeLocal {
		return task, "", cloudTransferError("cross_source_transfer_mode_invalid", false, errors.New("a transient materialization root cannot back library symlinks"))
	}
	var sourceStorage models.Storage
	if err := w.service.db.First(&sourceStorage, *download.StagingStorageID).Error; err != nil || sourceStorage.Type != models.StorageTypePan115 || sourceStorage.ConnectionID == nil {
		return task, "", cloudTransferError("cross_source_source_missing", false, err)
	}
	_, driver, err := w.service.connections.driver(*sourceStorage.ConnectionID)
	if err != nil {
		return task, "", err
	}
	reader, ok := driver.(cloudpkg.ReadDriver)
	if !ok || !driver.Capabilities().TemporaryDirectURL {
		return task, "", cloudTransferError("cross_source_read_capability_missing", false, nil)
	}
	packageRoot, err := providerItemWithinRoot(ctx, driver, strings.TrimSpace(download.ProviderOutputID), sourceStorage.RootPath)
	if err != nil || !packageRoot.IsDir {
		return task, "", cloudTransferError("cross_source_boundary_invalid", false, err)
	}
	state, err := decodeCloudTransferState(task.CloudStateJSON)
	if err != nil {
		return task, "", cloudTransferError("cloud_transfer_state_invalid", false, err)
	}
	managedRoot, err := w.crossSourceManagedRoot(ctx, task, download, &state)
	if err != nil {
		return task, "", err
	}
	if err := w.persistCloudState(&task, state, models.TransferTaskStatusPlanning, completedMaterializedItems(state), nil); err != nil {
		return task, "", cloudTransferError("transfer_state_persist_failed", true, err)
	}

	remaining, err := materializationRemainingBytes(managedRoot, manifest)
	if err != nil {
		return task, "", cloudTransferError("cross_source_staging_changed", false, err)
	}
	if err := requireMaterializationSpace(managedRoot, remaining, manifestTotalBytes(manifest)); err != nil {
		return task, "", err
	}

	for _, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return task, "", err
		}
		key := normalizedManifestPath(file.RelativePath)
		if err := validateMaterializationManifestFile(file, key); err != nil {
			return task, "", cloudTransferError("cross_source_manifest_invalid", false, err)
		}
		current, boundaryErr := providerItemWithinRoot(ctx, driver, file.ProviderItemID, packageRoot.ID)
		if boundaryErr != nil || current.IsDir || current.ParentID != file.ProviderParentID || current.Size != file.Size || !strings.EqualFold(strings.TrimSpace(current.SHA1), strings.TrimSpace(file.SHA1)) {
			return task, "", cloudTransferError("cross_source_source_changed", false, boundaryErr)
		}
		destination, pathErr := materializedPath(managedRoot, file.RelativePath)
		if pathErr != nil {
			return task, "", cloudTransferError("cross_source_manifest_invalid", false, pathErr)
		}
		if saved, exists := state.Materialized[key]; exists {
			if saved.RelativePath != key || saved.Size != file.Size || !strings.EqualFold(saved.SHA1, file.SHA1) {
				return task, "", cloudTransferError("cloud_transfer_state_invalid", false, nil)
			}
			if saved.Status == "completed" {
				if err := verifyMaterializedFile(destination, file); err == nil {
					continue
				}
				return task, "", cloudTransferError("cross_source_staging_changed", false, err)
			}
		}
		state.Materialized[key] = materializedItemState{RelativePath: key, Size: file.Size, SHA1: file.SHA1, Status: "materializing"}
		if err := w.persistCloudState(&task, state, models.TransferTaskStatusTransferring, completedMaterializedItems(state), nil); err != nil {
			return task, "", cloudTransferError("transfer_state_persist_failed", true, err)
		}
		if err := materializeProviderFile(ctx, runtime, reader, file, managedRoot, destination); err != nil {
			return task, "", err
		}
		state.Materialized[key] = materializedItemState{RelativePath: key, Size: file.Size, SHA1: file.SHA1, Status: "completed"}
		if err := w.persistCloudState(&task, state, models.TransferTaskStatusTransferring, completedMaterializedItems(state), nil); err != nil {
			return task, "", cloudTransferError("transfer_state_persist_failed", true, err)
		}
		processed, total := int64(completedMaterializedItems(state)), int64(len(manifest.Files))
		progress := float64(processed) * 100 / float64(total)
		if err := runtime.Heartbeat(&progress, &processed, &total, nil, nil); err != nil {
			return task, "", cloudTransferError(CodeQueueLeaseInvalid, true, err)
		}
	}
	return task, managedRoot, nil
}

func (w *TransferWorker) crossSourceManagedRoot(ctx context.Context, task models.TransferTask, download models.DownloadTask, state *cloudTransferState) (string, error) {
	if state == nil {
		return "", cloudTransferError("cloud_transfer_state_invalid", false, nil)
	}
	if strings.TrimSpace(state.ManagedRoot) != "" {
		relative := filepath.Clean(filepath.FromSlash(state.ManagedRoot))
		if filepath.IsAbs(relative) || filepath.Base(relative) != task.ID || filepath.Base(filepath.Dir(relative)) != crossSourceRootName || filepath.Dir(filepath.Dir(relative)) != "." {
			return "", cloudTransferError("cross_source_staging_boundary_invalid", false, nil)
		}
		staging := strings.TrimSpace(download.StagingAbsolutePath)
		if staging == "" {
			return "", cloudTransferError(CodeDownloadStagingRequired, false, nil)
		}
		staging, err := validateGlobalStaging(ctx, staging)
		root := filepath.Join(staging, relative)
		if err != nil || ensureWithin(staging, root) != nil || ensureSafeDirectoryPath(staging, root, false) != nil {
			return "", cloudTransferError("cross_source_staging_boundary_invalid", false, err)
		}
		return root, nil
	}
	if _, err := uuid.Parse(task.ID); err != nil {
		return "", cloudTransferError("cross_source_staging_identity_invalid", false, err)
	}
	staging := strings.TrimSpace(download.StagingAbsolutePath)
	if staging == "" {
		var settings models.DownloadSettings
		if err := w.service.db.First(&settings, 1).Error; err != nil {
			return "", cloudTransferError(CodeDownloadStagingRequired, false, err)
		}
		resolved, err := (&DownloadSettingsService{db: w.service.db}).resolveRecord(ctx, settings)
		if err != nil {
			return "", cloudTransferError(CodeDownloadStagingRequired, false, err)
		}
		staging = resolved
	}
	staging, err := validateGlobalStaging(ctx, staging)
	if err != nil {
		return "", cloudTransferError(CodeDownloadStagingUnavailable, false, err)
	}
	managedRoot := filepath.Join(staging, crossSourceRootName, task.ID)
	if ensureWithin(staging, managedRoot) != nil || ensureSafeDirectoryPath(staging, managedRoot, true) != nil {
		return "", cloudTransferError("cross_source_staging_boundary_invalid", false, nil)
	}
	if strings.TrimSpace(download.StagingAbsolutePath) == "" {
		result := w.service.db.Model(&models.DownloadTask{}).
			Where("id = ? AND staging_absolute_path = ''", download.ID).
			Update("staging_absolute_path", staging)
		if result.Error != nil {
			return "", cloudTransferError("transfer_state_persist_failed", true, result.Error)
		}
		if result.RowsAffected == 0 {
			var current models.DownloadTask
			if err := w.service.db.Select("staging_absolute_path").First(&current, "id = ?", download.ID).Error; err != nil || !providerPathsEqual(current.StagingAbsolutePath, staging) {
				return "", cloudTransferError("cross_source_staging_snapshot_conflict", false, err)
			}
		}
	}
	state.ManagedRoot = filepath.ToSlash(filepath.Join(crossSourceRootName, task.ID))
	return managedRoot, nil
}

func validateMaterializationManifestFile(file downloadpkg.File, key string) error {
	sha := strings.TrimSpace(file.SHA1)
	if key == "" || key != strings.ReplaceAll(strings.TrimSpace(file.RelativePath), "\\", "/") || file.ProviderItemID == "" || file.ProviderParentID == "" || file.Size < 0 || len(sha) != sha1.Size*2 {
		return errors.New("cross-source manifest identity is incomplete")
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return errors.New("cross-source manifest SHA1 is invalid")
	}
	return nil
}

func materializedPath(root, relative string) (string, error) {
	relative = filepath.Clean(filepath.FromSlash(relative))
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || strings.Contains(relative, ":") {
		return "", errors.New("materialized path is unsafe")
	}
	target := filepath.Join(root, relative)
	if err := ensureWithin(root, target); err != nil {
		return "", err
	}
	return target, nil
}

func materializationRemainingBytes(root string, manifest downloadpkg.Manifest) (uint64, error) {
	var remaining uint64
	for _, file := range manifest.Files {
		target, err := materializedPath(root, file.RelativePath)
		if err != nil || file.Size < 0 {
			return 0, errors.New("materialization size is invalid")
		}
		present := int64(0)
		for _, candidate := range []string{target, target + ".partial"} {
			info, statErr := os.Lstat(candidate)
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			if statErr != nil || !info.Mode().IsRegular() || medialibrary.IsUnsafeDirectory(candidate, fs.FileInfoToDirEntry(info)) || info.Size() > file.Size {
				return 0, errors.New("materialization checkpoint is unsafe")
			}
			if info.Size() > present {
				present = info.Size()
			}
		}
		remaining += uint64(file.Size - present)
	}
	return remaining, nil
}

func manifestTotalBytes(manifest downloadpkg.Manifest) uint64 {
	var total uint64
	for _, file := range manifest.Files {
		if file.Size > 0 && uint64(file.Size) <= ^uint64(0)-total {
			total += uint64(file.Size)
		}
	}
	return total
}

func requireMaterializationSpace(root string, remaining, total uint64) error {
	probe := (storagefs.LocalDriver{}).ProbeRoot(root)
	if !probe.Available || probe.FreeBytes == nil {
		return cloudTransferError("cross_source_space_unknown", false, nil)
	}
	margin := total / 20
	if margin < crossSourceMinimumFreeBytes {
		margin = crossSourceMinimumFreeBytes
	}
	if remaining > ^uint64(0)-margin || *probe.FreeBytes < remaining+margin {
		return cloudTransferError("cross_source_space_insufficient", false, fmt.Errorf("required=%d available=%d", remaining+margin, *probe.FreeBytes))
	}
	return nil
}

func materializeProviderFile(ctx context.Context, runtime JobRuntime, reader cloudpkg.ReadDriver, file downloadpkg.File, root, destination string) error {
	if err := ensureSafeDirectoryPath(root, filepath.Dir(destination), true); err != nil {
		return cloudTransferError("cross_source_staging_boundary_invalid", false, err)
	}
	if err := verifyMaterializedFile(destination, file); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return cloudTransferError("cross_source_staging_changed", false, err)
	}
	partial := destination + ".partial"
	offset := int64(0)
	if info, err := os.Lstat(partial); err == nil {
		if !info.Mode().IsRegular() || medialibrary.IsUnsafeDirectory(partial, fs.FileInfoToDirEntry(info)) || info.Size() > file.Size {
			return cloudTransferError("cross_source_staging_changed", false, err)
		}
		offset = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return cloudTransferError("cross_source_staging_unavailable", true, err)
	}
	stream, err := reader.OpenRead(ctx, cloudpkg.ReadRequest{FileID: file.ProviderItemID, Offset: offset})
	if err != nil {
		return err
	}
	if offset > 0 && !stream.OffsetAccepted {
		_ = stream.Body.Close()
		offset = 0
		stream, err = reader.OpenRead(ctx, cloudpkg.ReadRequest{FileID: file.ProviderItemID})
		if err != nil {
			return err
		}
	}
	defer func() { _ = stream.Body.Close() }()
	if stream.TotalSize != nil && *stream.TotalSize != file.Size {
		return cloudTransferError("cross_source_source_changed", false, nil)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	output, err := os.OpenFile(partial, flags, 0o600)
	if err != nil {
		return cloudTransferError("cross_source_staging_unavailable", true, err)
	}
	if offset > 0 {
		if _, err := output.Seek(offset, io.SeekStart); err != nil {
			_ = output.Close()
			return cloudTransferError("cross_source_staging_unavailable", true, err)
		}
	}
	remaining := file.Size - offset
	written, copyErr := copyMaterializedStream(ctx, runtime, output, stream.Body, remaining)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil {
		if errors.Is(copyErr, errMaterializedStreamTooLarge) {
			_ = os.Remove(partial)
			return cloudTransferError("cross_source_source_changed", false, copyErr)
		}
		return copyErr
	}
	if syncErr != nil || closeErr != nil || offset+written != file.Size {
		return cloudTransferError("cross_source_download_incomplete", true, firstNonNil(syncErr, closeErr))
	}
	if err := verifyMaterializedFile(partial, file); err != nil {
		_ = os.Remove(partial)
		return cloudTransferError("cross_source_checksum_mismatch", false, err)
	}
	if err := os.Rename(partial, destination); err != nil {
		return cloudTransferError("cross_source_staging_finalize_failed", true, err)
	}
	return nil
}

var errMaterializedStreamTooLarge = errors.New("materialized stream exceeded the frozen manifest size")

func copyMaterializedStream(ctx context.Context, runtime JobRuntime, destination io.Writer, source io.Reader, expected int64) (int64, error) {
	if expected < 0 {
		return 0, errMaterializedStreamTooLarge
	}
	buffer := make([]byte, 1024*1024)
	var total int64
	lastHeartbeat := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		// Read at most the frozen remaining size plus one proof byte. Without
		// this bound a malformed or compromised CDN response could keep filling
		// the Server staging volume before the post-download size check runs.
		readLimit := len(buffer)
		remaining := expected - total
		if remaining < int64(readLimit) {
			readLimit = int(remaining) + 1
		}
		read, readErr := source.Read(buffer[:readLimit])
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			total += int64(written)
			if writeErr != nil {
				return total, cloudTransferError("cross_source_staging_write_failed", true, writeErr)
			}
			if written != read {
				return total, cloudTransferError("cross_source_staging_write_failed", true, io.ErrShortWrite)
			}
			if total > expected {
				return total, errMaterializedStreamTooLarge
			}
		}
		if time.Since(lastHeartbeat) >= 10*time.Second {
			if err := runtime.Heartbeat(nil, nil, nil, nil, nil); err != nil {
				return total, cloudTransferError(CodeQueueLeaseInvalid, true, err)
			}
			lastHeartbeat = time.Now()
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, cloudTransferError("cross_source_read_failed", true, readErr)
		}
	}
}

func verifyMaterializedFile(path string, expected downloadpkg.File) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || medialibrary.IsUnsafeDirectory(path, fs.FileInfoToDirEntry(info)) || info.Size() != expected.Size {
		return errors.New("materialized file identity changed")
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha1.New()
	_, copyErr := io.Copy(hash, input)
	closeErr := input.Close()
	if copyErr != nil || closeErr != nil {
		return firstNonNil(copyErr, closeErr)
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), strings.TrimSpace(expected.SHA1)) {
		return errors.New("materialized file SHA1 changed")
	}
	return nil
}

func completedMaterializedItems(state cloudTransferState) int {
	completed := 0
	for _, item := range state.Materialized {
		if item.Status == "completed" {
			completed++
		}
	}
	return completed
}

func firstNonNil(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func cleanupCrossSourceManagedRoot(task models.TransferTask, download models.DownloadTask) error {
	state, err := decodeCloudTransferState(task.CloudStateJSON)
	if err != nil || strings.TrimSpace(state.ManagedRoot) == "" {
		return err
	}
	relative := filepath.Clean(filepath.FromSlash(state.ManagedRoot))
	if filepath.IsAbs(relative) || filepath.Base(relative) != task.ID || filepath.Base(filepath.Dir(relative)) != crossSourceRootName || filepath.Dir(filepath.Dir(relative)) != "." {
		return errors.New("cross-source cleanup identity is invalid")
	}
	if _, err := uuid.Parse(task.ID); err != nil {
		return errors.New("cross-source cleanup task identity is invalid")
	}
	staging, err := validateGlobalStaging(context.Background(), download.StagingAbsolutePath)
	if err != nil {
		return err
	}
	root := filepath.Join(staging, relative)
	if err := ensureWithin(staging, root); err != nil {
		return err
	}
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	paths := make([]string, 0, len(state.Materialized)*2+1)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ensureWithin(root, path) != nil || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("cross-source cleanup encountered an unsafe path")
		}
		info, err := entry.Info()
		if err != nil || medialibrary.IsUnsafeDirectory(path, fs.FileInfoToDirEntry(info)) {
			return errors.New("cross-source cleanup encountered an unsafe entry")
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(paths) - 1; index >= 0; index-- {
		if err := os.Remove(paths[index]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	_ = os.Remove(filepath.Dir(root)) // only succeeds when no other task remains
	return nil
}

// Interrupt removes only task-owned incomplete bytes. Completed materialized
// files and provider/library data remain untouched, matching pipeline-cancel's
// conservative keep-data contract.
func (w *TransferWorker) Interrupt(ctx context.Context, job ClaimedJob, action string) error {
	if action != "cancel" {
		return nil
	}
	var payload transferJobPayload
	if err := json.Unmarshal([]byte(job.Job.PayloadJSON), &payload); err != nil || payload.TransferTaskID == "" {
		return errors.New("transfer interrupt payload is invalid")
	}
	var task models.TransferTask
	if err := w.service.db.First(&task, "id = ?", payload.TransferTaskID).Error; err != nil {
		return err
	}
	var download models.DownloadTask
	if err := w.service.db.First(&download, "id = ?", task.DownloadTaskID).Error; err != nil {
		return err
	}
	return cleanupCrossSourcePartials(ctx, task, download)
}

func cleanupCrossSourcePartials(ctx context.Context, task models.TransferTask, download models.DownloadTask) error {
	state, err := decodeCloudTransferState(task.CloudStateJSON)
	if err != nil || strings.TrimSpace(state.ManagedRoot) == "" {
		return err
	}
	relative := filepath.Clean(filepath.FromSlash(state.ManagedRoot))
	if filepath.IsAbs(relative) || filepath.Base(relative) != task.ID || filepath.Base(filepath.Dir(relative)) != crossSourceRootName || filepath.Dir(filepath.Dir(relative)) != "." {
		return errors.New("cross-source partial cleanup identity is invalid")
	}
	staging, err := validateGlobalStaging(ctx, download.StagingAbsolutePath)
	if err != nil {
		return err
	}
	root := filepath.Join(staging, relative)
	if err := ensureWithin(staging, root); err != nil {
		return err
	}
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	partialFiles := make([]string, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ensureWithin(root, path) != nil || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("cross-source partial cleanup encountered an unsafe path")
		}
		info, err := entry.Info()
		if err != nil || medialibrary.IsUnsafeDirectory(path, fs.FileInfoToDirEntry(info)) {
			return errors.New("cross-source partial cleanup encountered an unsafe entry")
		}
		if info.Mode().IsRegular() && strings.HasSuffix(entry.Name(), ".partial") {
			partialFiles = append(partialFiles, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, path := range partialFiles {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		pruneEmptyStagingDirectories(root, filepath.Dir(path))
	}
	return nil
}
