package services

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

func (w *TransferWorker) finishCompletedTransfer(ctx context.Context, task models.TransferTask) WorkerResult {
	var download models.DownloadTask
	if err := w.service.db.First(&download, "id = ?", task.DownloadTaskID).Error; err != nil {
		return WorkerResult{ErrorCode: "transfer_download_missing", ErrorMessage: "原下载任务不存在"}
	}
	if download.TargetStorageType != models.StorageTypePan115 && w.service.seeding != nil {
		if err := w.service.seeding.AfterTransfer(ctx, download); err != nil {
			next := time.Now().UTC().Add(time.Minute)
			return WorkerResult{RetryAt: &next, ErrorCode: "post_transfer_provider_failed", ErrorMessage: "下载器收尾失败，将自动重试"}
		}
	}
	if download.ProviderType == models.DownloaderTypePluginHTTP {
		if download.TransferMode == models.MediaLibraryTransferSymlink {
			_ = w.service.db.Model(&models.TransferTask{}).Where("id = ?", task.ID).Updates(map[string]any{"cleanup_status": models.TransferCleanupSkipped, "cleanup_error_code": "", "updated_at": time.Now().UTC()}).Error
			return WorkerResult{}
		}
		removed, err := cleanupPluginDownloadOutput(download)
		if err != nil {
			next := time.Now().UTC().Add(time.Minute)
			return WorkerResult{RetryAt: &next, ErrorCode: "download_staging_cleanup_failed", ErrorMessage: "入库已完成，站点下载暂存清理将自动重试"}
		}
		_ = w.service.db.Model(&models.TransferTask{}).Where("id = ?", task.ID).Updates(map[string]any{"cleanup_status": models.TransferCleanupCompleted, "cleanup_removed": removed, "cleanup_error_code": "", "updated_at": time.Now().UTC()}).Error
		serverlog.OperationDownloadStagingCleanup.Event(w.service.log.Info()).Str("task_id", task.ID).Int("removed", removed).Str("source_cleanup", "plugin_managed_output").Msg(serverlog.OperationDownloadStagingCleanup.Message("已清理站点下载暂存产物"))
		return WorkerResult{}
	}
	if download.ProviderType == models.DownloaderTypeQBittorrent && (download.TransferMode == models.MediaLibraryTransferCopy || download.TransferMode == models.MediaLibraryTransferSymlink) {
		if task.CleanupStatus != models.TransferCleanupCompleted {
			_ = w.service.db.Model(&models.TransferTask{}).Where("id = ?", task.ID).Updates(map[string]any{"cleanup_status": models.TransferCleanupDeferred, "cleanup_error_code": "", "updated_at": time.Now().UTC()}).Error
		}
		return WorkerResult{}
	}
	if task.CleanupStatus == models.TransferCleanupCompleted || task.CleanupStatus == models.TransferCleanupSkipped {
		return WorkerResult{}
	}
	if _, err := w.service.cleanupTransferStaging(ctx, task, download); err != nil {
		next := time.Now().UTC().Add(time.Minute)
		return WorkerResult{RetryAt: &next, ErrorCode: "download_staging_cleanup_failed", ErrorMessage: "入库已完成，下载暂存清理将自动重试"}
	}
	return WorkerResult{}
}

// CleanupAfterSeeding is called only after qBittorrent has acknowledged its
// seeding cleanup. deleteData means the provider already removed the entire
// source package; symlink mode keeps selected media and removes only the
// unselected, manifest-owned files.
func (s *TransferService) CleanupAfterSeeding(ctx context.Context, downloadTaskID string, deleteData bool) error {
	var task models.TransferTask
	if err := s.db.First(&task, "download_task_id = ?", downloadTaskID).Error; err != nil {
		return err
	}
	if task.CleanupStatus == models.TransferCleanupCompleted || task.CleanupStatus == models.TransferCleanupSkipped {
		return nil
	}
	var download models.DownloadTask
	if err := s.db.First(&download, "id = ?", downloadTaskID).Error; err != nil {
		return err
	}
	if deleteData {
		extras, err := transferCleanupDifference(task)
		if err != nil {
			return s.persistTransferCleanupFailure(task.ID, "download_staging_manifest_invalid", err)
		}
		now := time.Now().UTC()
		if err := s.db.Model(&models.TransferTask{}).Where("id = ?", task.ID).Updates(map[string]any{"cleanup_status": models.TransferCleanupCompleted, "cleanup_removed": len(extras), "cleanup_error_code": "", "updated_at": now}).Error; err != nil {
			return err
		}
		serverlog.OperationDownloadStagingCleanup.Event(s.log.Info()).Str("task_id", task.ID).Int("removed", len(extras)).Str("source_cleanup", "provider_delete_data").Msg(serverlog.OperationDownloadStagingCleanup.Message("做种结束后已由下载器清理暂存数据"))
		return nil
	}
	_, err := s.cleanupTransferStaging(ctx, task, download)
	return err
}

func (s *TransferService) cleanupTransferStaging(ctx context.Context, task models.TransferTask, download models.DownloadTask) (int, error) {
	extras, err := transferCleanupDifference(task)
	if err != nil {
		return 0, s.persistTransferCleanupFailure(task.ID, "download_staging_manifest_invalid", err)
	}
	if len(extras) == 0 {
		if err := s.persistTransferCleanupSuccess(task.ID, 0); err != nil {
			return 0, err
		}
		return 0, nil
	}
	_ = s.db.Model(&models.TransferTask{}).Where("id = ?", task.ID).Updates(map[string]any{"cleanup_status": models.TransferCleanupRunning, "cleanup_error_code": "", "updated_at": time.Now().UTC()}).Error
	removed := 0
	if download.TargetStorageType == models.StorageTypePan115 {
		removed, err = s.cleanupCloudStaging(ctx, download, extras)
	} else {
		removed, err = cleanupLocalStaging(download, extras)
	}
	if err != nil {
		return removed, s.persistTransferCleanupFailureWithCount(task.ID, "download_staging_cleanup_failed", removed, err)
	}
	if err := s.persistTransferCleanupSuccess(task.ID, removed); err != nil {
		return removed, err
	}
	serverlog.OperationDownloadStagingCleanup.Event(s.log.Info()).Str("task_id", task.ID).Int("removed", removed).Msg(serverlog.OperationDownloadStagingCleanup.Message("已清理未选中的暂存文件"))
	return removed, nil
}

func transferCleanupDifference(task models.TransferTask) ([]downloadpkg.File, error) {
	plan, err := buildTransferCleanupPlan(task)
	if err != nil {
		return nil, err
	}
	return plan.Removable, nil
}

type transferCleanupPlan struct {
	Removable      []downloadpkg.File
	ProtectedCount int
}

func buildTransferCleanupPlan(task models.TransferTask) (transferCleanupPlan, error) {
	var source, selected downloadpkg.Manifest
	if json.Unmarshal([]byte(task.SourceManifestJSON), &source) != nil || json.Unmarshal([]byte(task.ManifestJSON), &selected) != nil || !source.Complete || !selected.Complete || len(selected.Files) == 0 {
		return transferCleanupPlan{}, errors.New("transfer cleanup manifest is incomplete")
	}
	selectedKeys := make(map[string]struct{}, len(selected.Files))
	for _, file := range selected.Files {
		key := transferCleanupFileKey(file)
		if key == "" {
			return transferCleanupPlan{}, errors.New("transfer cleanup selected file identity is invalid")
		}
		if _, duplicate := selectedKeys[key]; duplicate {
			return transferCleanupPlan{}, errors.New("transfer cleanup selected file identity is duplicated")
		}
		selectedKeys[key] = struct{}{}
	}
	plan := transferCleanupPlan{Removable: make([]downloadpkg.File, 0, len(source.Files))}
	seen := map[string]struct{}{}
	for _, file := range source.Files {
		key := transferCleanupFileKey(file)
		if key == "" {
			return transferCleanupPlan{}, errors.New("transfer cleanup file identity is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return transferCleanupPlan{}, errors.New("transfer cleanup file identity is duplicated")
		}
		seen[key] = struct{}{}
		if _, keep := selectedKeys[key]; !keep {
			// A package selector can be confidently wrong even after metadata
			// verification. Never make that mistake irreversible by deleting an
			// unselected video or an unmatched subtitle. Only clearly non-media
			// manifest items are eligible for automatic staging cleanup.
			if isVideoFile(file.RelativePath) || isAutomaticTransferSubtitleFile(file.RelativePath) || isAutomaticTransferDanmakuFile(file.RelativePath) {
				plan.ProtectedCount++
				continue
			}
			plan.Removable = append(plan.Removable, file)
		}
	}
	for key := range selectedKeys {
		if _, exists := seen[key]; !exists {
			return transferCleanupPlan{}, errors.New("transfer cleanup selection is not a source subset")
		}
	}
	return plan, nil
}

func transferCleanupFileKey(file downloadpkg.File) string {
	raw := strings.ReplaceAll(file.RelativePath, "\\", "/")
	path := normalizedManifestPath(raw)
	providerItemID := strings.TrimSpace(file.ProviderItemID)
	providerParentID := strings.TrimSpace(file.ProviderParentID)
	sha1 := strings.TrimSpace(file.SHA1)
	if raw == "" || raw != strings.TrimSpace(raw) || strings.HasPrefix(raw, "/") || path == "." || path == ".." ||
		strings.HasPrefix(path, "../") || path != raw || strings.Contains(path, ":") ||
		strings.ContainsAny(path, "\x00\r\n") || providerItemID != file.ProviderItemID ||
		providerParentID != file.ProviderParentID || sha1 != file.SHA1 ||
		strings.ContainsAny(providerItemID+providerParentID+sha1, "\x00\r\n") {
		return ""
	}
	return path + "\x00" + providerItemID + "\x00" + providerParentID + "\x00" + strconv.FormatInt(file.Size, 10) + "\x00" + strings.ToLower(sha1)
}

func cleanupLocalStaging(download models.DownloadTask, extras []downloadpkg.File) (int, error) {
	stagingRoot := filepath.Clean(download.StagingAbsolutePath)
	if stagingRoot == "." || !filepath.IsAbs(stagingRoot) {
		return 0, errors.New("download staging root is invalid")
	}
	categoryRoot := filepath.Join(stagingRoot, download.ScrapeCategory)
	if err := ensureWithin(stagingRoot, categoryRoot); err != nil {
		return 0, err
	}
	removed := 0
	for _, file := range extras {
		target, err := resolveManifestSource(categoryRoot, stagingRoot, file.RelativePath)
		if err != nil {
			return removed, err
		}
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || medialibrary.IsUnsafeDirectory(target, fs.FileInfoToDirEntry(info)) || (file.Size > 0 && info.Size() != file.Size) {
			return removed, errors.New("download staging file changed")
		}
		if err := os.Remove(target); err != nil {
			return removed, err
		}
		removed++
		pruneEmptyStagingDirectories(stagingRoot, filepath.Dir(target))
	}
	return removed, nil
}

func pruneEmptyStagingDirectories(root, directory string) {
	root, directory = filepath.Clean(root), filepath.Clean(directory)
	for directory != root && ensureWithin(root, directory) == nil {
		if err := os.Remove(directory); err != nil {
			return
		}
		directory = filepath.Dir(directory)
	}
}

func (s *TransferService) cleanupCloudStaging(ctx context.Context, download models.DownloadTask, extras []downloadpkg.File) (int, error) {
	packageRootID := strings.TrimSpace(download.ProviderOutputID)
	if s.connections == nil || download.StagingStorageID == nil || packageRootID == "" {
		return 0, errors.New("cloud staging connection is unavailable")
	}
	var storage models.Storage
	if err := s.db.First(&storage, *download.StagingStorageID).Error; err != nil || storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil {
		return 0, errors.New("cloud staging storage is unavailable")
	}
	_, driver, err := s.connections.driver(*storage.ConnectionID)
	if err != nil {
		return 0, err
	}
	packageRoot, err := providerItemWithinRoot(ctx, driver, packageRootID, storage.RootPath)
	if err != nil || !packageRoot.IsDir {
		return 0, errors.New("cloud staging package boundary is invalid")
	}
	mutations, ok := driver.(cloudpkg.MutationDriver)
	if !ok || !mutations.Capabilities().Recycle {
		return 0, errors.New("cloud staging recycle capability is unavailable")
	}
	removed := 0
	for _, file := range extras {
		if strings.TrimSpace(file.ProviderItemID) == "" {
			return removed, errors.New("cloud staging item identity is missing")
		}
		item, err := providerItemWithinRoot(ctx, driver, file.ProviderItemID, packageRootID)
		if err != nil {
			if code, _ := cloudpkg.ErrorInfo(err); code == cloudpkg.CodeNotFound {
				continue
			}
			return removed, err
		}
		if item.IsDir || item.Size != file.Size || (file.SHA1 != "" && !strings.EqualFold(item.SHA1, file.SHA1)) || (file.ProviderParentID != "" && item.ParentID != file.ProviderParentID) {
			return removed, errors.New("cloud staging item changed")
		}
		if err := mutations.Recycle(ctx, item.ID); err != nil {
			if _, statErr := driver.Stat(ctx, item.ID); statErr == nil {
				return removed, err
			} else if code, _ := cloudpkg.ErrorInfo(statErr); code != cloudpkg.CodeNotFound {
				return removed, err
			}
		}
		removed++
	}
	return removed, nil
}

func (s *TransferService) persistTransferCleanupSuccess(taskID string, removed int) error {
	return s.db.Model(&models.TransferTask{}).Where("id = ?", taskID).Updates(map[string]any{"cleanup_status": models.TransferCleanupCompleted, "cleanup_removed": gorm.Expr("cleanup_removed + ?", removed), "cleanup_error_code": "", "updated_at": time.Now().UTC()}).Error
}

func (s *TransferService) persistTransferCleanupFailure(taskID, code string, cause error) error {
	return s.persistTransferCleanupFailureWithCount(taskID, code, 0, cause)
}

func (s *TransferService) persistTransferCleanupFailureWithCount(taskID, code string, removed int, cause error) error {
	if err := s.db.Model(&models.TransferTask{}).Where("id = ?", taskID).Updates(map[string]any{"cleanup_status": models.TransferCleanupFailed, "cleanup_removed": gorm.Expr("cleanup_removed + ?", removed), "cleanup_error_code": safeLabel(code, 96), "updated_at": time.Now().UTC()}).Error; err != nil {
		return err
	}
	serverlog.OperationDownloadStagingCleanup.Event(s.log.Warn()).Str("task_id", taskID).Str("error_code", safeLabel(code, 96)).Msg(serverlog.OperationDownloadStagingCleanup.Message("暂存清理失败，将保留文件并重试"))
	return cause
}
