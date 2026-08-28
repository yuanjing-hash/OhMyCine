package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const mediaCatalogDeletionPreviewTTL = 5 * time.Minute

type MediaCatalogDeletionPreviewResult struct {
	LibraryID         uint      `json:"library_id"`
	LibraryName       string    `json:"library_name"`
	StorageType       string    `json:"storage_type"`
	Title             string    `json:"title"`
	FileCount         int       `json:"file_count"`
	TotalBytes        int64     `json:"total_bytes"`
	RelativePaths     []string  `json:"relative_paths"`
	STRMImpactCount   int64     `json:"strm_impact_count"`
	MissingCount      int       `json:"missing_count"`
	Warnings          []string  `json:"warnings"`
	ConfirmationToken string    `json:"confirmation_token"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type MediaCatalogDeletionResult struct {
	Deleted      bool `json:"deleted"`
	RemovedFiles int  `json:"removed_files"`
	MissingFiles int  `json:"missing_files"`
}

type mediaCatalogDeletionSnapshot struct {
	Version        int                        `json:"version"`
	BoundaryDigest string                     `json:"boundary_digest"`
	LibraryRootID  string                     `json:"library_root_id,omitempty"`
	Items          []mediaCatalogDeletionItem `json:"items"`
}

type mediaCatalogDeletionItem struct {
	EntryID          uint      `json:"entry_id"`
	RelativePath     string    `json:"relative_path"`
	ProviderItemID   string    `json:"provider_item_id,omitempty"`
	ProviderParentID string    `json:"provider_parent_id,omitempty"`
	ProviderName     string    `json:"provider_name,omitempty"`
	ProviderSHA1     string    `json:"provider_sha1,omitempty"`
	Size             int64     `json:"size"`
	ModifiedAt       time.Time `json:"modified_at"`
}

type mediaCatalogDeletionState struct {
	Version   int           `json:"version"`
	Completed map[uint]bool `json:"completed"`
	Removed   int           `json:"removed"`
	Missing   int           `json:"missing"`
}

func (s *MediaLibraryService) PreviewCatalogDeletion(ctx context.Context, actor Actor, libraryID uint, workToken string, request RequestContext) (MediaCatalogDeletionPreviewResult, error) {
	ctx, cancel := boundedTransferDeletionContext(ctx)
	defer cancel()
	if !actor.Can(authz.PermissionMediaLibrariesMediaDelete) {
		return MediaCatalogDeletionPreviewResult{}, appError(CodePermissionDenied, "无权删除媒体库作品源文件", nil)
	}
	library, storage, workKey, entries, err := s.catalogDeletionBoundary(libraryID, workToken)
	if err != nil {
		return MediaCatalogDeletionPreviewResult{}, err
	}
	snapshot := mediaCatalogDeletionSnapshot{Version: 2, BoundaryDigest: catalogDeletionBoundaryDigest(library, storage), Items: make([]mediaCatalogDeletionItem, 0, len(entries))}
	missing := 0
	switch storage.Type {
	case models.StorageTypeLocal:
		root, rootErr := catalogLocalDeletionRoot(storage, library)
		if rootErr != nil {
			return MediaCatalogDeletionPreviewResult{}, rootErr
		}
		for _, entry := range entries {
			item := deletionItemFromEntry(entry)
			target, resolveErr := catalogLocalDeletionTarget(root, entry.RelativePath)
			if resolveErr != nil {
				return MediaCatalogDeletionPreviewResult{}, appError(CodeMediaCatalogDeletionChanged, "媒体文件路径越界", nil)
			}
			info, statErr := os.Lstat(target)
			if errors.Is(statErr, os.ErrNotExist) {
				missing++
				snapshot.Items = append(snapshot.Items, item)
				continue
			}
			if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != entry.Size || ensureSafeDirectoryPath(root, filepath.Dir(target), false) != nil {
				return MediaCatalogDeletionPreviewResult{}, appError(CodeMediaCatalogDeletionChanged, "媒体文件已变化，请先重新扫描", statErr)
			}
			snapshot.Items = append(snapshot.Items, item)
		}
	case models.StorageTypePan115:
		if storage.ConnectionID == nil || s.connections == nil {
			return MediaCatalogDeletionPreviewResult{}, appError(CodeMediaCatalogDeletionUnavailable, "115 连接不可用", nil)
		}
		_, driver, driverErr := s.connections.driver(*storage.ConnectionID)
		if driverErr != nil {
			return MediaCatalogDeletionPreviewResult{}, appError(CodeMediaCatalogDeletionUnavailable, "115 连接不可用", driverErr)
		}
		root, rootErr := providerItemWithinRoot(ctx, driver, library.ProviderRootID, storage.RootPath)
		if rootErr != nil || !root.IsDir {
			return MediaCatalogDeletionPreviewResult{}, appError(CodeMediaCatalogDeletionChanged, "115 媒体库边界已变化", rootErr)
		}
		snapshot.LibraryRootID = root.ID
		for _, entry := range entries {
			item := deletionItemFromEntry(entry)
			current, statErr := providerItemWithinRoot(ctx, driver, entry.ProviderID, root.ID)
			if code, _ := cloudpkg.ErrorInfo(statErr); code == cloudpkg.CodeNotFound {
				missing++
				snapshot.Items = append(snapshot.Items, item)
				continue
			}
			if statErr != nil || current.IsDir || current.Size != entry.Size {
				return MediaCatalogDeletionPreviewResult{}, appError(CodeMediaCatalogDeletionChanged, "115 媒体文件已变化，请先重新扫描", statErr)
			}
			item.ProviderParentID, item.ProviderName, item.ProviderSHA1 = current.ParentID, current.Name, strings.TrimSpace(current.SHA1)
			snapshot.Items = append(snapshot.Items, item)
		}
	default:
		return MediaCatalogDeletionPreviewResult{}, appError(CodeMediaCatalogDeletionUnavailable, "当前媒体库存储不支持安全删除", nil)
	}
	token, tokenHash, err := newOpaqueConfirmationToken()
	if err != nil {
		return MediaCatalogDeletionPreviewResult{}, err
	}
	now := time.Now().UTC()
	snapshotRaw, _ := json.Marshal(snapshot)
	stateRaw, _ := json.Marshal(mediaCatalogDeletionState{Version: 1, Completed: map[uint]bool{}})
	preview := models.MediaCatalogDeletionPreview{ID: uuid.NewString(), TokenHash: tokenHash, ActorID: actor.User.ID, LibraryID: library.ID, WorkKey: workKey, EntryDigest: catalogDeletionDigest(entries), StorageType: storage.Type, SnapshotJSON: string(snapshotRaw), StateJSON: string(stateRaw), ExpiresAt: now.Add(mediaCatalogDeletionPreviewTTL), CreatedAt: now, UpdatedAt: now}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&preview).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_catalog.deletion_preview", "media_library", fmt.Sprint(library.ID), "success", map[string]any{"work_hash": catalogWorkHash(workKey), "files": len(entries), "storage_type": storage.Type}, request)
	}); err != nil {
		return MediaCatalogDeletionPreviewResult{}, err
	}
	paths := make([]string, 0, min(len(entries), 20))
	var total int64
	for _, entry := range entries {
		if len(paths) < 20 {
			paths = append(paths, strings.TrimPrefix(filepath.ToSlash(entry.RelativePath), "/"))
		}
		total += max(entry.Size, 0)
	}
	warnings := []string{"只会删除本次预览列出的当前媒体库文件；不会跨媒体库删除同名作品。"}
	if storage.Type == models.StorageTypePan115 {
		warnings = append(warnings, "115 文件会进入回收站，本操作不会自动清空回收站。")
	}
	return MediaCatalogDeletionPreviewResult{LibraryID: library.ID, LibraryName: library.Name, StorageType: storage.Type, Title: catalogDeletionTitle(entries), FileCount: len(entries), TotalBytes: total, RelativePaths: paths, STRMImpactCount: s.catalogSTRMImpact(library.ID, entries), MissingCount: missing, Warnings: warnings, ConfirmationToken: token, ExpiresAt: preview.ExpiresAt}, nil
}

func (s *MediaLibraryService) ConfirmCatalogDeletion(ctx context.Context, actor Actor, libraryID uint, workToken, token string, request RequestContext) (MediaCatalogDeletionResult, error) {
	ctx, cancel := boundedTransferDeletionContext(ctx)
	defer cancel()
	if !actor.Can(authz.PermissionMediaLibrariesMediaDelete) {
		return MediaCatalogDeletionResult{}, appError(CodePermissionDenied, "无权删除媒体库作品源文件", nil)
	}
	workKey, err := decodeCatalogToken(workToken)
	if err != nil {
		return MediaCatalogDeletionResult{}, err
	}
	token = strings.TrimSpace(token)
	if len(token) != 43 {
		return MediaCatalogDeletionResult{}, appError(CodeMediaCatalogDeletionExpired, "删除确认已失效，请重新预览", nil)
	}
	hash := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(hash[:])
	var preview models.MediaCatalogDeletionPreview
	if err := s.db.First(&preview, "token_hash = ?", tokenHash).Error; err != nil {
		return MediaCatalogDeletionResult{}, appError(CodeMediaCatalogDeletionExpired, "删除确认已失效，请重新预览", nil)
	}
	now := time.Now().UTC()
	if preview.ActorID != actor.User.ID || preview.LibraryID != libraryID || preview.WorkKey != workKey || preview.ConsumedAt != nil || !preview.ExpiresAt.After(now) {
		return MediaCatalogDeletionResult{}, appError(CodeMediaCatalogDeletionExpired, "删除确认已失效，请重新预览", nil)
	}
	var snapshot mediaCatalogDeletionSnapshot
	var state mediaCatalogDeletionState
	if json.Unmarshal([]byte(preview.SnapshotJSON), &snapshot) != nil || snapshot.Version != 2 || json.Unmarshal([]byte(preview.StateJSON), &state) != nil || state.Version != 1 {
		return MediaCatalogDeletionResult{}, appError(CodeMediaCatalogDeletionChanged, "删除预览损坏，请重新预览", nil)
	}
	if state.Completed == nil {
		state.Completed = map[uint]bool{}
	}
	library, storage, _, entries, err := s.catalogDeletionBoundary(libraryID, workToken)
	if err != nil {
		return MediaCatalogDeletionResult{}, err
	}
	if storage.Type != preview.StorageType || snapshot.BoundaryDigest != catalogDeletionBoundaryDigest(library, storage) || catalogDeletionDigest(entries) != preview.EntryDigest || !snapshotMatchesEntries(snapshot.Items, entries) {
		return MediaCatalogDeletionResult{}, appError(CodeMediaCatalogDeletionChanged, "媒体作品或文件清单已变化，请重新预览", nil)
	}
	// Claim this execution with a short transaction. A stale request cannot run
	// beside another confirm; a failed request clears the claim after its last
	// durable checkpoint.
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var locked models.MediaCatalogDeletionPreview
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", preview.ID).Error; err != nil {
			return err
		}
		claimNow := time.Now().UTC()
		if locked.ActorID != actor.User.ID || locked.LibraryID != libraryID || locked.WorkKey != workKey || locked.ConsumedAt != nil || !locked.ExpiresAt.After(claimNow) {
			return appError(CodeMediaCatalogDeletionExpired, "删除确认已失效，请重新预览", nil)
		}
		if locked.StartedAt != nil && locked.StartedAt.After(time.Now().UTC().Add(-transferDeletionTimeout)) {
			return appError(CodeConflict, "删除任务正在执行", nil)
		}
		return tx.Model(&locked).Updates(map[string]any{"started_at": now, "updated_at": now}).Error
	}); err != nil {
		return MediaCatalogDeletionResult{}, err
	}
	fail := func(cause error) (MediaCatalogDeletionResult, error) {
		raw, _ := json.Marshal(state)
		_ = s.db.Model(&models.MediaCatalogDeletionPreview{}).Where("id = ?", preview.ID).Updates(map[string]any{"state_json": string(raw), "started_at": nil, "last_error_code": CodeMediaCatalogDeletionPartial, "updated_at": time.Now().UTC()}).Error
		_ = s.audit.Record(s.db, &actor.User.ID, "media_catalog.deletion_confirm", "media_library", fmt.Sprint(libraryID), "failure", map[string]any{"work_hash": catalogWorkHash(workKey), "error_code": CodeMediaCatalogDeletionPartial}, request)
		return MediaCatalogDeletionResult{}, appError(CodeMediaCatalogDeletionPartial, "部分文件未能移除；已保存进度，请重新预览后继续", cause)
	}
	if storage.Type == models.StorageTypeLocal {
		root, rootErr := catalogLocalDeletionRoot(storage, library)
		if rootErr != nil {
			return fail(rootErr)
		}
		for _, item := range snapshot.Items {
			if state.Completed[item.EntryID] {
				continue
			}
			target, resolveErr := catalogLocalDeletionTarget(root, item.RelativePath)
			if resolveErr != nil {
				return fail(resolveErr)
			}
			info, statErr := os.Lstat(target)
			if errors.Is(statErr, os.ErrNotExist) {
				state.Missing++
			} else if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != item.Size || ensureSafeDirectoryPath(root, filepath.Dir(target), false) != nil {
				return fail(errors.New("local media file changed"))
			} else if removeErr := os.Remove(target); removeErr != nil {
				return fail(removeErr)
			} else {
				state.Removed++
				pruneEmptyStagingDirectories(root, filepath.Dir(target))
			}
			state.Completed[item.EntryID] = true
			if persistErr := s.persistCatalogDeletionState(preview.ID, state); persistErr != nil {
				return fail(persistErr)
			}
		}
	} else {
		if storage.ConnectionID == nil || s.connections == nil {
			return fail(errors.New("pan115 connection unavailable"))
		}
		_, driver, driverErr := s.connections.driver(*storage.ConnectionID)
		if driverErr != nil {
			return fail(driverErr)
		}
		mutations, ok := driver.(cloudpkg.MutationDriver)
		if !ok || !mutations.Capabilities().Recycle {
			return fail(errors.New("pan115 recycle unavailable"))
		}
		root, rootErr := providerItemWithinRoot(ctx, driver, library.ProviderRootID, storage.RootPath)
		if rootErr != nil || root.ID != snapshot.LibraryRootID {
			return fail(errors.New("pan115 library root changed"))
		}
		for _, item := range snapshot.Items {
			if state.Completed[item.EntryID] {
				continue
			}
			current, statErr := providerItemWithinRoot(ctx, driver, item.ProviderItemID, root.ID)
			if code, _ := cloudpkg.ErrorInfo(statErr); code == cloudpkg.CodeNotFound {
				state.Missing++
			} else if statErr != nil || current.IsDir || current.Size != item.Size || current.ParentID != item.ProviderParentID || current.Name != item.ProviderName || (item.ProviderSHA1 != "" && !strings.EqualFold(strings.TrimSpace(current.SHA1), item.ProviderSHA1)) {
				return fail(errors.New("pan115 media file changed"))
			} else if recycleErr := mutations.Recycle(ctx, current.ID); recycleErr != nil {
				return fail(recycleErr)
			} else {
				state.Removed++
			}
			state.Completed[item.EntryID] = true
			if persistErr := s.persistCatalogDeletionState(preview.ID, state); persistErr != nil {
				return fail(persistErr)
			}
		}
	}
	requiresArtifacts := mediaLibraryRequiresArtifacts(storage.Type, library, s.artifacts != nil)
	var changeRevision, generation uint64
	changeReady := false
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var current []models.MediaLibraryEntry
		if err := tx.Where("library_id = ? AND work_key = ?", libraryID, workKey).Order("id").Find(&current).Error; err != nil {
			return err
		}
		if catalogDeletionDigest(current) != preview.EntryDigest {
			return appError(CodeMediaCatalogDeletionChanged, "媒体作品或文件清单已变化，请重新预览", nil)
		}
		ids, paths, recognitionIDs := make([]uint, 0, len(current)), make([]string, 0, len(current)), make([]uint, 0)
		for _, entry := range current {
			ids = append(ids, entry.ID)
			paths = append(paths, entry.RelativePath)
			if entry.RecognitionID != nil {
				recognitionIDs = append(recognitionIDs, *entry.RecognitionID)
			}
		}
		if len(ids) > 0 {
			if err := tx.Where("library_id = ? AND id IN ?", libraryID, ids).Delete(&models.MediaLibraryEntry{}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.MediaManagedItem{}).Where("library_id = ? AND relative_path IN ? AND active = ?", libraryID, paths, true).Updates(map[string]any{"active": false, "updated_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		for _, recognitionID := range recognitionIDs {
			var count int64
			if err := tx.Model(&models.MediaLibraryEntry{}).Where("recognition_id = ?", recognitionID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Delete(&models.MediaLibraryRecognition{}, recognitionID).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", libraryID).Updates(map[string]any{"dirty_generation": gorm.Expr("dirty_generation + 1"), "artifact_generation": gorm.Expr("artifact_generation + 1")}).Error; err != nil {
			return err
		}
		var updated models.MediaLibrary
		if err := tx.First(&updated, libraryID).Error; err != nil {
			return err
		}
		generation = updated.ArtifactGeneration
		if requiresArtifacts {
			// A deletion generation is a complete projection of all remaining
			// facts. Carry unchanged metadata and source assets forward so the
			// artifact worker can remove only artifacts whose source disappeared.
			now := time.Now().UTC()
			if err := tx.Model(&models.MediaLibraryRecognition{}).Where("library_id = ?", libraryID).Updates(map[string]any{"last_generation": generation, "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.MediaLibrarySourceAsset{}).Where("library_id = ? AND active = ?", libraryID, true).Updates(map[string]any{"generation": generation, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if s.changes != nil {
			change, changeErr := s.changes.RecordTx(tx, libraryID, updated.DirtyGeneration, models.MediaLibraryChangeRemoval, !requiresArtifacts)
			if changeErr != nil {
				return changeErr
			}
			changeRevision = change.Revision
			changeReady = change.State == models.MediaLibraryChangeReady
		} else if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", libraryID).Update("content_revision", gorm.Expr("content_revision + 1")).Error; err != nil {
			return err
		}
		finished := time.Now().UTC()
		if err := tx.Model(&models.MediaCatalogDeletionPreview{}).Where("id = ?", preview.ID).Updates(map[string]any{"consumed_at": finished, "started_at": nil, "last_error_code": "", "updated_at": finished}).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_catalog.deletion_confirm", "media_library", fmt.Sprint(libraryID), "success", map[string]any{"work_hash": catalogWorkHash(workKey), "files": len(ids), "removed": state.Removed, "missing": state.Missing, "storage_type": storage.Type}, request)
	}); err != nil {
		return fail(err)
	}
	if changeReady && changeRevision > 0 && s.changes != nil {
		s.changes.NotifyCommitted(libraryID, changeRevision)
	}
	if requiresArtifacts && generation > 0 {
		_ = s.artifacts.ScheduleGeneration(libraryID, generation)
	}
	s.wakeCatalogDeletionReconcile(libraryID)
	return MediaCatalogDeletionResult{Deleted: true, RemovedFiles: state.Removed, MissingFiles: state.Missing}, nil
}

func (s *MediaLibraryService) catalogDeletionBoundary(libraryID uint, workToken string) (models.MediaLibrary, models.Storage, string, []models.MediaLibraryEntry, error) {
	workKey, err := decodeCatalogToken(workToken)
	if err != nil {
		return models.MediaLibrary{}, models.Storage{}, "", nil, err
	}
	var library models.MediaLibrary
	if err := s.db.First(&library, libraryID).Error; err != nil {
		return library, models.Storage{}, "", nil, mediaLibraryNotFound(err)
	}
	var storage models.Storage
	if err := s.db.First(&storage, library.StorageID).Error; err != nil {
		return library, storage, "", nil, err
	}
	var entries []models.MediaLibraryEntry
	if err := s.db.Where("library_id = ? AND work_key = ?", libraryID, workKey).Order("id").Limit(maxReorganizationItems + 1).Find(&entries).Error; err != nil {
		return library, storage, "", nil, err
	}
	if len(entries) == 0 {
		return library, storage, "", nil, appError(CodeNotFound, "媒体作品不存在", nil)
	}
	if len(entries) > maxReorganizationItems {
		return library, storage, "", nil, appError(CodeMediaCatalogDeletionUnavailable, "作品文件过多，不能在单次安全删除中处理", nil)
	}
	return library, storage, workKey, entries, nil
}

func deletionItemFromEntry(entry models.MediaLibraryEntry) mediaCatalogDeletionItem {
	return mediaCatalogDeletionItem{EntryID: entry.ID, RelativePath: filepath.ToSlash(entry.RelativePath), ProviderItemID: entry.ProviderID, Size: entry.Size, ModifiedAt: entry.ModifiedAt.UTC()}
}
func catalogDeletionTitle(entries []models.MediaLibraryEntry) string {
	if len(entries) == 0 {
		return ""
	}
	if strings.TrimSpace(entries[0].SeriesTitle) != "" {
		return entries[0].SeriesTitle
	}
	return entries[0].Title
}
func catalogWorkHash(workKey string) string {
	sum := sha256.Sum256([]byte(workKey))
	return hex.EncodeToString(sum[:8])
}
func catalogDeletionDigest(entries []models.MediaLibraryEntry) string {
	copyEntries := append([]models.MediaLibraryEntry(nil), entries...)
	sort.Slice(copyEntries, func(i, j int) bool { return copyEntries[i].ID < copyEntries[j].ID })
	h := sha256.New()
	for _, entry := range copyEntries {
		recognition := uint(0)
		if entry.RecognitionID != nil {
			recognition = *entry.RecognitionID
		}
		_, _ = fmt.Fprintf(h, "%d\x00%s\x00%s\x00%d\x00%d\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\n",
			entry.ID, filepath.ToSlash(entry.RelativePath), entry.ProviderID, entry.Size, entry.ModifiedAt.UTC().UnixNano(), recognition,
			entry.WorkKey, entry.MediaType, entry.Title, optionalInt64String(entry.TMDBID), entry.MatchStatus, entry.LastGeneration, entry.UpdatedAt.UTC().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))
}

func catalogDeletionBoundaryDigest(library models.MediaLibrary, storage models.Storage) string {
	connectionID := uint(0)
	if storage.ConnectionID != nil {
		connectionID = *storage.ConnectionID
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%d\x00%s\x00%s\x00%d\x00%s\x00%s",
		library.ID, library.StorageID, filepath.ToSlash(library.RelativeRoot), library.ProviderRootID,
		storage.ID, storage.Type, strings.TrimSpace(storage.RootPath)+"\x00"+fmt.Sprint(connectionID))))
	return hex.EncodeToString(sum[:])
}

func optionalInt64String(value *int64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(*value)
}
func snapshotMatchesEntries(items []mediaCatalogDeletionItem, entries []models.MediaLibraryEntry) bool {
	if len(items) != len(entries) {
		return false
	}
	byID := make(map[uint]models.MediaLibraryEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for _, item := range items {
		entry, ok := byID[item.EntryID]
		if !ok || filepath.ToSlash(entry.RelativePath) != item.RelativePath || entry.ProviderID != item.ProviderItemID || entry.Size != item.Size || entry.ModifiedAt.UTC().UnixNano() != item.ModifiedAt.UTC().UnixNano() {
			return false
		}
	}
	return true
}
func catalogLocalDeletionRoot(storage models.Storage, library models.MediaLibrary) (string, error) {
	root, err := medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)
	if err != nil {
		return "", err
	}
	return (storagefs.LocalDriver{}).CanonicalizeRoot(root)
}
func catalogLocalDeletionTarget(root, relative string) (string, error) {
	relative = filepath.FromSlash(strings.TrimPrefix(filepath.ToSlash(relative), "/"))
	target := filepath.Join(root, relative)
	if ensureWithin(root, target) != nil {
		return "", errors.New("path outside library root")
	}
	return target, nil
}
func (s *MediaLibraryService) persistCatalogDeletionState(id string, state mediaCatalogDeletionState) error {
	raw, _ := json.Marshal(state)
	return s.db.Model(&models.MediaCatalogDeletionPreview{}).Where("id = ?", id).Updates(map[string]any{"state_json": string(raw), "updated_at": time.Now().UTC()}).Error
}
func (s *MediaLibraryService) catalogSTRMImpact(libraryID uint, entries []models.MediaLibraryEntry) int64 {
	if len(entries) == 0 {
		return 0
	}
	sourceIdentities := make([]string, 0, len(entries))
	for _, entry := range entries {
		sourceIdentities = append(sourceIdentities, fmt.Sprintf("entry:%d", entry.ID))
	}
	var count int64
	if len(sourceIdentities) > 0 {
		_ = s.db.Model(&models.MediaArtifact{}).Where("library_id = ? AND source_identity IN ? AND kind = ? AND active = ?", libraryID, sourceIdentities, models.MediaArtifactKindSTRM, true).Count(&count).Error
	}
	return count
}
func (s *MediaLibraryService) wakeCatalogDeletionReconcile(libraryID uint) {
	s.mu.Lock()
	handle, ok := s.supervisors[libraryID]
	s.mu.Unlock()
	if !ok {
		return
	}
	select {
	case handle.wake <- struct{}{}:
	default:
	}
}
