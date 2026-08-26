package services

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// captureLocalManagedItems persists ownership only for files whose completed
// destination was durably recorded by the transfer worker. A truncated public
// plan deliberately results in a conservative partial ownership manifest.
func captureLocalManagedItems(tx *gorm.DB, task models.TransferTask, download models.DownloadTask, summary TransferPlanSummary) error {
	for _, item := range summary.Items {
		if item.Result != "completed" {
			continue
		}
		if err := upsertManagedItem(tx, task, download, item.RelativePath, item.Kind, item.Size, "", ""); err != nil {
			return err
		}
	}
	return nil
}

func captureCloudManagedItems(tx *gorm.DB, task models.TransferTask, download models.DownloadTask, targets []transferTargetItem, state cloudTransferState) error {
	for _, target := range targets {
		key := normalizedManifestPath(target.File.RelativePath)
		if download.ProviderType == models.DownloaderTypePan115Offline {
			key = strings.TrimSpace(target.File.ProviderItemID)
		}
		item := state.Items[key]
		if item.Status != "completed" || strings.TrimSpace(item.CurrentID) == "" {
			continue
		}
		kind := models.MediaManagedItemKindSidecar
		if isVideoFile(target.Relative) {
			kind = models.MediaManagedItemKindVideo
		}
		if err := upsertManagedItem(tx, task, download, target.Relative, kind, target.File.Size, item.CurrentID, item.TargetParentID); err != nil {
			return err
		}
	}
	return nil
}

func upsertManagedItem(tx *gorm.DB, task models.TransferTask, download models.DownloadTask, relative, kind string, size int64, providerItemID, providerParentID string) error {
	relative, err := sanitizeTransferRelativePath(relative)
	if err != nil || (kind != models.MediaManagedItemKindVideo && kind != models.MediaManagedItemKindSidecar) || size < 0 {
		return errors.New("invalid managed media item")
	}
	now := time.Now().UTC()
	var existing models.MediaManagedItem
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("library_id = ? AND relative_path = ?", task.LibraryID, relative).First(&existing).Error
	if err == nil {
		if !existing.Managed {
			return errors.New("managed media ownership conflict")
		}
		return tx.Model(&existing).Updates(map[string]any{
			"transfer_task_id": task.ID, "download_task_id": task.DownloadTaskID,
			"identity_revision": download.IdentityRevision, "kind": kind,
			"provider_item_id": providerItemID, "provider_parent_id": providerParentID,
			"size": size, "active": true, "updated_at": now,
		}).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	row := models.MediaManagedItem{
		OpaqueID: uuid.NewString(), LibraryID: task.LibraryID, TransferTaskID: task.ID,
		DownloadTaskID: task.DownloadTaskID, IdentityRevision: download.IdentityRevision,
		Kind: kind, RelativePath: relative, ProviderItemID: providerItemID,
		ProviderParentID: providerParentID, Size: size, Managed: true, Active: true,
		CreatedAt: now, UpdatedAt: now,
	}
	return tx.Create(&row).Error
}
