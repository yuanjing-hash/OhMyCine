package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	transferDeletionPreviewTTL = 5 * time.Minute
	transferDeletionTimeout    = 45 * time.Second
)

func boundedTransferDeletionContext(parent context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= transferDeletionTimeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, transferDeletionTimeout)
}

func jobHasActiveLease(job models.Job, now time.Time) bool {
	return job.LeaseTokenHash != "" && job.LeaseExpiresAt != nil && job.LeaseExpiresAt.After(now)
}

type TransferDeletionPreviewInput struct {
	Scope string `json:"scope"`
}

type TransferDeletionConfirmInput struct {
	Token string `json:"token"`
}

type TransferDeletionPreviewResult struct {
	Scope              string    `json:"scope"`
	SourceItems        int       `json:"source_items"`
	SourceBytes        int64     `json:"source_bytes"`
	LibraryItems       int       `json:"library_items"`
	LibraryBytes       int64     `json:"library_bytes"`
	ProviderType       string    `json:"provider_type"`
	SourceStorageType  string    `json:"source_storage_type"`
	LibraryStorageType string    `json:"library_storage_type"`
	SourceMissing      int       `json:"source_missing"`
	SourceDetached     int       `json:"source_detached"`
	LibraryMissing     int       `json:"library_missing"`
	Blocked            bool      `json:"blocked"`
	Blockers           []string  `json:"blockers"`
	RequiresFileDelete bool      `json:"requires_file_delete"`
	Warnings           []string  `json:"warnings"`
	ConfirmationToken  string    `json:"confirmation_token"`
	ExpiresAt          time.Time `json:"expires_at"`
}

type TransferDeletionResult struct {
	Deleted        bool   `json:"deleted"`
	Scope          string `json:"scope"`
	SourceRemoved  int    `json:"source_removed"`
	LibraryRemoved int    `json:"library_removed"`
}

type transferDeletionBoundary struct {
	transfer       models.TransferTask
	download       models.DownloadTask
	library        models.MediaLibrary
	storage        models.Storage
	transferJob    models.Job
	downloadJob    models.Job
	seeding        *models.SeedingTask
	seedingJob     *models.Job
	managed        []models.MediaManagedItem
	sourceManifest downloadpkg.Manifest
	sourceMissing  int
	sourceDetached int
	sourcePresent  int
	sourceRootGone bool
	libraryMissing int
}

type transferDeletionState struct {
	Version          int           `json:"version"`
	SourceCompleted  bool          `json:"source_completed"`
	LibraryCompleted map[uint]bool `json:"library_completed"`
	SourceRemoved    int           `json:"source_removed"`
	LibraryRemoved   int           `json:"library_removed"`
}

func validTransferDeletionScope(scope string) bool {
	switch scope {
	case models.TransferDeletionScopeRecordOnly, models.TransferDeletionScopeRecordAndSource,
		models.TransferDeletionScopeRecordAndLibrary, models.TransferDeletionScopeRecordSourceAndLibrary:
		return true
	default:
		return false
	}
}

func deletionIncludesSource(scope string) bool {
	return scope == models.TransferDeletionScopeRecordAndSource || scope == models.TransferDeletionScopeRecordSourceAndLibrary
}

func deletionIncludesLibrary(scope string) bool {
	return scope == models.TransferDeletionScopeRecordAndLibrary || scope == models.TransferDeletionScopeRecordSourceAndLibrary
}

func (s *TransferService) PreviewDeletion(ctx context.Context, actor Actor, transferID string, input TransferDeletionPreviewInput, request RequestContext) (TransferDeletionPreviewResult, error) {
	ctx, cancel := boundedTransferDeletionContext(ctx)
	defer cancel()
	scope := strings.TrimSpace(input.Scope)
	if !validTransferDeletionScope(scope) {
		return TransferDeletionPreviewResult{}, appError(CodeTransferDeletionScopeInvalid, "删除范围无效", nil)
	}
	boundary, err := s.loadTransferDeletionBoundary(ctx, actor, strings.TrimSpace(transferID), scope)
	if err != nil {
		return TransferDeletionPreviewResult{}, err
	}
	sourceDigest := sourceDeletionDigest(boundary.download, boundary.sourceManifest)
	managedDigest := managedManifestDigest(boundary.managed)
	token, tokenHash, err := newOpaqueConfirmationToken()
	if err != nil {
		return TransferDeletionPreviewResult{}, err
	}
	now := time.Now().UTC()
	state, _ := json.Marshal(transferDeletionState{Version: 1, LibraryCompleted: map[uint]bool{}})
	preview := models.TransferDeletionPreview{
		ID: uuid.NewString(), TokenHash: tokenHash, ActorID: actor.User.ID,
		TransferTaskID: boundary.transfer.ID, DownloadTaskID: boundary.download.ID,
		LibraryID: boundary.library.ID, Scope: scope, IdentityRevision: boundary.download.IdentityRevision,
		SourceManifestDigest: sourceDigest, ManagedManifestDigest: managedDigest,
		TransferJobRevision: boundary.transferJob.Revision, DownloadJobRevision: boundary.downloadJob.Revision,
		StateJSON: string(state), ExpiresAt: now.Add(transferDeletionPreviewTTL), CreatedAt: now, UpdatedAt: now,
	}
	if boundary.seedingJob != nil {
		preview.SeedingJobRevision = boundary.seedingJob.Revision
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&preview).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "transfer.deletion_preview", "transfer_task", boundary.transfer.ID, "success", map[string]any{"scope": scope, "source_items": len(boundary.sourceManifest.Files), "library_items": len(boundary.managed)}, request)
	}); err != nil {
		return TransferDeletionPreviewResult{}, err
	}
	sourceStorageType := models.StorageTypeLocal
	if boundary.download.ProviderType == models.DownloaderTypePan115Offline {
		sourceStorageType = models.StorageTypePan115
	}
	result := TransferDeletionPreviewResult{Scope: scope, ProviderType: boundary.download.ProviderType, SourceStorageType: sourceStorageType, LibraryStorageType: boundary.storage.Type, SourceMissing: boundary.sourceMissing, SourceDetached: boundary.sourceDetached, LibraryMissing: boundary.libraryMissing, Blocked: false, Blockers: []string{}, RequiresFileDelete: scope != models.TransferDeletionScopeRecordOnly, Warnings: deletionWarnings(scope), ConfirmationToken: token, ExpiresAt: preview.ExpiresAt}
	if boundary.sourceDetached > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d 项已离开来源包或不再存在，将保留当前位置且不会按文件 ID 越界删除", boundary.sourceDetached))
	}
	if deletionIncludesSource(scope) {
		result.SourceItems, result.SourceBytes = len(boundary.sourceManifest.Files), manifestBytes(boundary.sourceManifest.Files)
	}
	if deletionIncludesLibrary(scope) {
		result.LibraryItems, result.LibraryBytes = len(boundary.managed), managedBytes(boundary.managed)
	}
	return result, nil
}

func (s *TransferService) ConfirmDeletion(ctx context.Context, actor Actor, transferID, rawToken string, request RequestContext) (TransferDeletionResult, error) {
	ctx, cancel := boundedTransferDeletionContext(ctx)
	defer cancel()
	rawToken = strings.TrimSpace(rawToken)
	if len(rawToken) != 43 {
		return TransferDeletionResult{}, appError(CodeTransferDeletionPreviewExpired, "删除确认已失效，请重新预览", nil)
	}
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])
	var preview models.TransferDeletionPreview
	now := time.Now().UTC()
	if err := s.db.First(&preview, "token_hash = ?", tokenHash).Error; err != nil {
		return TransferDeletionResult{}, appError(CodeTransferDeletionPreviewExpired, "删除确认已失效，请重新预览", nil)
	}
	if preview.ActorID != actor.User.ID || preview.TransferTaskID != strings.TrimSpace(transferID) || preview.ConsumedAt != nil || !preview.ExpiresAt.After(now) {
		return TransferDeletionResult{}, appError(CodeTransferDeletionPreviewExpired, "删除确认已失效，请重新预览", nil)
	}
	// Provider and filesystem checks deliberately happen outside the SQLite
	// writer transaction. The short consume transaction below rechecks all
	// persisted revisions/digests, and every destructive primitive checks the
	// physical boundary again immediately before mutation.
	boundary, err := s.loadTransferDeletionBoundary(ctx, actor, preview.TransferTaskID, preview.Scope)
	if err != nil {
		return TransferDeletionResult{}, err
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&preview, "token_hash = ?", tokenHash).Error; err != nil {
			return appError(CodeTransferDeletionPreviewExpired, "删除确认已失效，请重新预览", nil)
		}
		consumeNow := time.Now().UTC()
		if preview.ActorID != actor.User.ID || preview.TransferTaskID != strings.TrimSpace(transferID) || preview.ConsumedAt != nil || !preview.ExpiresAt.After(consumeNow) {
			return appError(CodeTransferDeletionPreviewExpired, "删除确认已失效，请重新预览", nil)
		}
		var currentDownload models.DownloadTask
		var currentTransferJob, currentDownloadJob models.Job
		var currentManaged []models.MediaManagedItem
		if tx.First(&currentDownload, "id = ?", preview.DownloadTaskID).Error != nil || tx.First(&currentTransferJob, "id = ?", boundary.transfer.JobID).Error != nil || tx.First(&currentDownloadJob, "id = ?", currentDownload.JobID).Error != nil {
			return appError(CodeTransferDeletionBoundaryChanged, "任务边界已变化，请重新预览", nil)
		}
		if deletionIncludesLibrary(preview.Scope) {
			if err := tx.Where("transfer_task_id = ? AND library_id = ? AND managed = ? AND active = ?", preview.TransferTaskID, preview.LibraryID, true, true).Order("id").Find(&currentManaged).Error; err != nil {
				return err
			}
		}
		var currentSource downloadpkg.Manifest
		if deletionIncludesSource(preview.Scope) && decodeCompleteSourceManifest(boundary.transfer, currentDownload, &currentSource) != nil {
			return appError(CodeTransferDeletionBoundaryChanged, "来源清单已变化，请重新预览", nil)
		}
		if !deletionIncludesSource(preview.Scope) {
			currentSource = downloadpkg.Manifest{}
		}
		if currentDownload.IdentityRevision != preview.IdentityRevision || currentTransferJob.Revision != preview.TransferJobRevision || currentDownloadJob.Revision != preview.DownloadJobRevision || sourceDeletionDigest(currentDownload, currentSource) != preview.SourceManifestDigest || managedManifestDigest(currentManaged) != preview.ManagedManifestDigest {
			return appError(CodeTransferDeletionBoundaryChanged, "任务或文件清单已变化，请重新预览", nil)
		}
		if boundary.seedingJob != nil && boundary.seedingJob.Revision != preview.SeedingJobRevision {
			return appError(CodeTransferDeletionBoundaryChanged, "做种状态已变化，请重新预览", nil)
		}
		preview.ConsumedAt, preview.UpdatedAt = &consumeNow, consumeNow
		return tx.Model(&preview).Updates(map[string]any{"consumed_at": consumeNow, "updated_at": consumeNow}).Error
	}); err != nil {
		return TransferDeletionResult{}, err
	}

	state := transferDeletionState{Version: 1, LibraryCompleted: map[uint]bool{}}
	_ = json.Unmarshal([]byte(preview.StateJSON), &state)
	if state.LibraryCompleted == nil {
		state.LibraryCompleted = map[uint]bool{}
	}
	if deletionIncludesSource(preview.Scope) && !state.SourceCompleted {
		removed, deleteErr := s.deleteTransferSource(ctx, boundary)
		state.SourceRemoved += removed
		if deleteErr != nil {
			if persistErr := s.persistDeletionState(preview.ID, state, CodeTransferDeletionPartial); persistErr != nil {
				deleteErr = errors.Join(deleteErr, fmt.Errorf("persist source deletion state: %w", persistErr))
			}
			s.persistDeletionFailure(preview, CodeTransferDeletionPartial, request)
			return TransferDeletionResult{}, appError(CodeTransferDeletionPartial, "源文件删除未完整完成，记录已保留；请修复后重新预览", deleteErr)
		}
		state.SourceCompleted = true
		if err := s.persistDeletionState(preview.ID, state, ""); err != nil {
			return TransferDeletionResult{}, err
		}
	}
	if deletionIncludesLibrary(preview.Scope) {
		removed, deleteErr := s.deleteTransferLibrary(ctx, boundary, &state)
		state.LibraryRemoved += removed
		if deleteErr != nil {
			if persistErr := s.persistDeletionState(preview.ID, state, CodeTransferDeletionPartial); persistErr != nil {
				deleteErr = errors.Join(deleteErr, fmt.Errorf("persist library deletion state: %w", persistErr))
			}
			s.persistDeletionFailure(preview, CodeTransferDeletionPartial, request)
			return TransferDeletionResult{}, appError(CodeTransferDeletionPartial, "媒体库文件删除未完整完成，记录和未完成清单已保留；请重新预览", deleteErr)
		}
	}
	if err := s.finalizeTransferDeletion(actor, boundary, preview.Scope, state, request); err != nil {
		if persistErr := s.persistDeletionState(preview.ID, state, CodeTransferDeletionPartial); persistErr != nil {
			err = errors.Join(err, fmt.Errorf("persist final deletion state: %w", persistErr))
		}
		return TransferDeletionResult{}, err
	}
	return TransferDeletionResult{Deleted: true, Scope: preview.Scope, SourceRemoved: state.SourceRemoved, LibraryRemoved: state.LibraryRemoved}, nil
}

func (s *TransferService) loadTransferDeletionBoundary(ctx context.Context, actor Actor, transferID, scope string) (transferDeletionBoundary, error) {
	return s.loadTransferDeletionBoundaryWithDB(ctx, s.db, actor, transferID, scope)
}

func (s *TransferService) loadTransferDeletionBoundaryWithDB(ctx context.Context, db *gorm.DB, actor Actor, transferID, scope string) (transferDeletionBoundary, error) {
	var b transferDeletionBoundary
	if err := db.First(&b.transfer, "id = ?", transferID).Error; err != nil {
		return b, appError(CodeNotFound, "媒体整理任务不存在", nil)
	}
	if !actor.Can(authz.PermissionJobsControlAll) && (b.transfer.OwnerID != actor.User.ID || !actor.Can(authz.PermissionJobsControlOwn)) {
		return b, appError(CodePermissionDenied, "无权删除该媒体整理记录", nil)
	}
	if db.First(&b.transferJob, "id = ?", b.transfer.JobID).Error != nil || !isDeletableTransferJobStatus(b.transferJob.Status) {
		return b, appError(CodeQueueStateConflict, "媒体整理任务仍在执行，不能删除", nil)
	}
	if jobHasActiveLease(b.transferJob, time.Now().UTC()) {
		return b, appError(CodeQueueStateConflict, "媒体整理 worker 仍在收口，请稍后重试", nil)
	}
	if db.First(&b.download, "id = ?", b.transfer.DownloadTaskID).Error != nil || db.First(&b.downloadJob, "id = ?", b.download.JobID).Error != nil || db.First(&b.library, b.transfer.LibraryID).Error != nil || db.First(&b.storage, b.library.StorageID).Error != nil {
		return b, appError(CodeTransferDeletionUnavailable, "删除边界不完整", nil)
	}
	var reorgs []models.MediaReorganizationTask
	if err := db.Where("transfer_task_id = ?", b.transfer.ID).Find(&reorgs).Error; err != nil {
		return b, err
	}
	for _, reorg := range reorgs {
		var job models.Job
		if db.First(&job, "id = ?", reorg.JobID).Error != nil || !isTerminalPipelineJobStatus(job.Status) {
			return b, appError(CodeQueueStateConflict, "重新整理任务仍在执行，不能删除", nil)
		}
		if jobHasActiveLease(job, time.Now().UTC()) {
			return b, appError(CodeQueueStateConflict, "重新整理 worker 仍在收口，请稍后重试", nil)
		}
	}
	if deletionIncludesSource(scope) {
		if !isTerminalPipelineJobStatus(b.downloadJob.Status) {
			return b, appError(CodeQueueStateConflict, "下载任务仍在执行，不能删除源文件", nil)
		}
		if jobHasActiveLease(b.downloadJob, time.Now().UTC()) {
			return b, appError(CodeQueueStateConflict, "下载 worker 仍在收口，请稍后重试", nil)
		}
		var seeding models.SeedingTask
		if err := db.Where("download_task_id = ?", b.download.ID).First(&seeding).Error; err == nil {
			var job models.Job
			if db.First(&job, "id = ?", seeding.JobID).Error != nil || !isTerminalPipelineJobStatus(job.Status) {
				return b, appError(CodeQueueStateConflict, "下载仍在做种，请先停止做种再删除源文件", nil)
			}
			if jobHasActiveLease(job, time.Now().UTC()) {
				return b, appError(CodeQueueStateConflict, "做种 worker 仍在收口，请稍后重试", nil)
			}
			b.seeding, b.seedingJob = &seeding, &job
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return b, err
		}
		if err := decodeCompleteSourceManifest(b.transfer, b.download, &b.sourceManifest); err != nil {
			return b, appError(CodeTransferDeletionUnavailable, "没有完整来源清单，不能安全删除源文件", nil)
		}
		if err := s.validateSourceDeletionBoundary(ctx, &b); err != nil {
			return b, err
		}
	}
	if deletionIncludesLibrary(scope) {
		if err := db.Where("transfer_task_id = ? AND library_id = ? AND managed = ? AND active = ?", b.transfer.ID, b.library.ID, true, true).Order("id").Limit(maxReorganizationItems + 1).Find(&b.managed).Error; err != nil {
			return b, err
		}
		if len(b.managed) == 0 || len(b.managed) > maxReorganizationItems {
			return b, appError(CodeTransferDeletionUnavailable, "没有完整媒体库托管清单，不能安全删除", nil)
		}
		if err := s.validateLibraryDeletionBoundary(ctx, &b); err != nil {
			return b, err
		}
	}
	return b, nil
}

func decodeCompleteSourceManifest(transfer models.TransferTask, download models.DownloadTask, manifest *downloadpkg.Manifest) error {
	raw := transfer.SourceManifestJSON
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		raw = download.CompletedManifestJSON
	}
	if json.Unmarshal([]byte(raw), manifest) != nil || !manifest.Complete || len(manifest.Files) == 0 || len(manifest.Files) > 10000 {
		return errors.New("invalid source manifest")
	}
	seen := map[string]struct{}{}
	for _, file := range manifest.Files {
		key := transferCleanupFileKey(file)
		if key == "" {
			return errors.New("unsafe source item")
		}
		if _, ok := seen[key]; ok {
			return errors.New("duplicate source item")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func sourceDeletionDigest(download models.DownloadTask, manifest downloadpkg.Manifest) string {
	keys := make([]string, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		keys = append(keys, transferCleanupFileKey(file))
	}
	sort.Strings(keys)
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\n", download.ProviderType, download.ProviderTaskID, download.ProviderOutputID, download.StagingProviderDirectoryID, download.StagingAbsolutePath)
	for _, key := range keys {
		_, _ = fmt.Fprintln(h, key)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *TransferService) validateSourceDeletionBoundary(ctx context.Context, b *transferDeletionBoundary) error {
	if b.download.ProviderType == models.DownloaderTypePan115Offline {
		_, _, present, detached, rootGone, err := s.pan115DeletionBoundary(ctx, *b, b.sourceManifest.Files)
		b.sourcePresent, b.sourceDetached, b.sourceRootGone = present, detached, rootGone
		if rootGone {
			b.sourceMissing = len(b.sourceManifest.Files)
		}
		return err
	}
	if strings.TrimSpace(b.download.StagingAbsolutePath) == "" {
		return appError(CodeTransferDeletionUnavailable, "来源暂存根不可用", nil)
	}
	root, err := canonicalLocalDeletionRoot(b.download.StagingAbsolutePath)
	if err != nil {
		return appError(CodeTransferDeletionBoundaryChanged, "来源暂存根无效", nil)
	}
	categoryRoot := filepath.Join(root, firstNonEmpty(b.download.StagingCategory, b.download.ScrapeCategory))
	if ensureWithin(root, categoryRoot) != nil {
		return appError(CodeTransferDeletionBoundaryChanged, "来源暂存目录越界", nil)
	}
	for _, file := range b.sourceManifest.Files {
		target, err := resolveManifestSource(categoryRoot, root, file.RelativePath)
		if err != nil {
			return appError(CodeTransferDeletionBoundaryChanged, "来源清单路径越界", nil)
		}
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			b.sourceMissing++
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != file.Size || ensureSafeDirectoryPath(root, filepath.Dir(target), false) != nil {
			return appError(CodeTransferDeletionBoundaryChanged, "来源文件已变化", nil)
		}
	}
	return nil
}

func (s *TransferService) validateLibraryDeletionBoundary(ctx context.Context, b *transferDeletionBoundary) error {
	if b.storage.Type == models.StorageTypeLocal {
		root, err := medialibrary.ResolveRoot(b.storage.RootPath, b.library.RelativeRoot)
		if err != nil {
			return appError(CodeTransferDeletionBoundaryChanged, "媒体库目录无效", nil)
		}
		root, err = (storagefs.LocalDriver{}).CanonicalizeRoot(root)
		if err != nil {
			return appError(CodeTransferDeletionBoundaryChanged, "媒体库目录不可用", nil)
		}
		for _, item := range b.managed {
			relative, err := sanitizeTransferRelativePath(item.RelativePath)
			if err != nil {
				return appError(CodeTransferDeletionBoundaryChanged, "托管文件路径无效", nil)
			}
			target := filepath.Join(root, filepath.FromSlash(relative))
			info, err := os.Lstat(target)
			if errors.Is(err, os.ErrNotExist) {
				b.libraryMissing++
				continue
			}
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != item.Size || ensureWithin(root, target) != nil || ensureSafeDirectoryPath(root, filepath.Dir(target), false) != nil || medialibrary.IsUnsafeDirectory(target, fs.FileInfoToDirEntry(info)) {
				return appError(CodeTransferDeletionBoundaryChanged, "媒体库托管文件已变化", nil)
			}
		}
		return nil
	}
	if b.storage.Type != models.StorageTypePan115 || b.storage.ConnectionID == nil || b.library.ProviderRootID == "" || s.connections == nil {
		return appError(CodeTransferDeletionUnavailable, "当前媒体库存储不支持安全删除", nil)
	}
	_, driver, err := s.connections.driver(*b.storage.ConnectionID)
	if err != nil {
		return appError(CodeTransferDeletionUnavailable, "115 连接不可用", nil)
	}
	root, err := providerItemWithinRoot(ctx, driver, b.library.ProviderRootID, b.storage.RootPath)
	if err != nil || !root.IsDir {
		return appError(CodeTransferDeletionBoundaryChanged, "115 媒体库边界已变化", nil)
	}
	for _, item := range b.managed {
		current, err := providerItemWithinRoot(ctx, driver, item.ProviderItemID, root.ID)
		if err != nil {
			if code, _ := cloudpkg.ErrorInfo(err); code == cloudpkg.CodeNotFound {
				b.libraryMissing++
				continue
			}
			return appError(CodeTransferDeletionBoundaryChanged, "115 托管文件不可验证", nil)
		}
		if current.IsDir || current.Size != item.Size || (item.ProviderParentID != "" && current.ParentID != item.ProviderParentID) {
			return appError(CodeTransferDeletionBoundaryChanged, "115 托管文件已变化", nil)
		}
	}
	return nil
}

func (s *TransferService) pan115DeletionBoundary(ctx context.Context, b transferDeletionBoundary, files []downloadpkg.File) (cloudpkg.Driver, cloudpkg.Item, int, int, bool, error) {
	if s.connections == nil || b.download.StagingStorageID == nil {
		return nil, cloudpkg.Item{}, 0, 0, false, appError(CodeTransferDeletionUnavailable, "115 来源存储不可用", nil)
	}
	var storage models.Storage
	if s.db.First(&storage, *b.download.StagingStorageID).Error != nil || storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil {
		return nil, cloudpkg.Item{}, 0, 0, false, appError(CodeTransferDeletionUnavailable, "115 来源存储不可用", nil)
	}
	_, driver, err := s.connections.driver(*storage.ConnectionID)
	if err != nil {
		return nil, cloudpkg.Item{}, 0, 0, false, appError(CodeTransferDeletionUnavailable, "115 连接不可用", nil)
	}
	rootID := strings.TrimSpace(b.download.ProviderOutputID)
	if rootID == "" {
		return nil, cloudpkg.Item{}, 0, 0, false, appError(CodeTransferDeletionUnavailable, "旧任务缺少独立 115 包目录，不能安全删除来源", nil)
	}
	root, err := providerItemWithinRoot(ctx, driver, rootID, storage.RootPath)
	if err != nil {
		if code, _ := cloudpkg.ErrorInfo(err); code == cloudpkg.CodeNotFound {
			return driver, cloudpkg.Item{ID: rootID}, 0, 0, true, nil
		}
		return nil, cloudpkg.Item{}, 0, 0, false, appError(CodeTransferDeletionBoundaryChanged, "115 来源边界不可验证", err)
	}
	if !root.IsDir {
		return nil, cloudpkg.Item{}, 0, 0, false, appError(CodeTransferDeletionBoundaryChanged, "115 来源包目录身份已变化", nil)
	}
	groups := make(map[string][]downloadpkg.File)
	for _, file := range files {
		if strings.TrimSpace(file.ProviderItemID) == "" || strings.TrimSpace(file.ProviderParentID) == "" {
			return nil, cloudpkg.Item{}, 0, 0, false, appError(CodeTransferDeletionBoundaryChanged, "115 来源身份缺失", nil)
		}
		groups[file.ProviderParentID] = append(groups[file.ProviderParentID], file)
	}
	cache := map[string]cloudpkg.Item{root.ID: root}
	present, detached := 0, 0
	for parentID, group := range groups {
		if parentID != root.ID {
			parent, err := providerDirectoryWithinRootCached(ctx, driver, parentID, root, cache)
			if err != nil {
				if code, _ := cloudpkg.ErrorInfo(err); code == cloudpkg.CodeNotFound {
					detached += len(group)
					continue
				}
				return nil, cloudpkg.Item{}, present, detached, false, appError(CodeTransferDeletionBoundaryChanged, "115 来源父目录不可验证", err)
			}
			if !parent.IsDir {
				return nil, cloudpkg.Item{}, present, detached, false, appError(CodeTransferDeletionBoundaryChanged, "115 来源父目录身份已变化", nil)
			}
		}
		items, err := listCloudDirectory(ctx, driver, parentID)
		if err != nil {
			if code, _ := cloudpkg.ErrorInfo(err); code == cloudpkg.CodeNotFound {
				detached += len(group)
				continue
			}
			return nil, cloudpkg.Item{}, present, detached, false, appError(CodeTransferDeletionBoundaryChanged, "115 来源目录不可读取", err)
		}
		byID := make(map[string]cloudpkg.Item, len(items))
		for _, item := range items {
			byID[item.ID] = item
		}
		for _, file := range group {
			item, ok := byID[file.ProviderItemID]
			if !ok {
				detached++
				continue
			}
			if item.IsDir || item.ParentID != parentID || item.Size != file.Size || (file.SHA1 != "" && item.SHA1 != "" && !strings.EqualFold(file.SHA1, item.SHA1)) {
				return nil, cloudpkg.Item{}, present, detached, false, appError(CodeTransferDeletionBoundaryChanged, "115 来源文件已变化", nil)
			}
			present++
		}
	}
	return driver, root, present, detached, false, nil
}

func providerDirectoryWithinRootCached(ctx context.Context, driver cloudpkg.Driver, directoryID string, root cloudpkg.Item, cache map[string]cloudpkg.Item) (cloudpkg.Item, error) {
	directoryID = strings.TrimSpace(directoryID)
	root.ID = strings.TrimSpace(root.ID)
	if directoryID == "" || root.ID == "" || !root.IsDir {
		return cloudpkg.Item{}, errors.New("provider directory boundary is incomplete")
	}
	currentID := directoryID
	visited := make(map[string]struct{}, maxCloudBoundaryDepth)
	var initial cloudpkg.Item
	for depth := 0; depth < maxCloudBoundaryDepth; depth++ {
		item, ok := cache[currentID]
		if !ok {
			var err error
			item, err = driver.Stat(ctx, currentID)
			if err != nil {
				return cloudpkg.Item{}, err
			}
			cache[currentID] = item
		}
		if strings.TrimSpace(item.ID) != currentID || !item.IsDir {
			return cloudpkg.Item{}, errors.New("provider returned an invalid directory identity")
		}
		if depth == 0 {
			initial = item
		}
		if currentID == root.ID {
			return initial, nil
		}
		if _, exists := visited[currentID]; exists {
			return cloudpkg.Item{}, errors.New("provider directory parent cycle")
		}
		visited[currentID] = struct{}{}
		currentID = strings.TrimSpace(item.ParentID)
		if currentID == "" || (currentID == "0" && root.ID != "0") {
			return cloudpkg.Item{}, errors.New("provider directory is outside the source package root")
		}
	}
	return cloudpkg.Item{}, errors.New("provider directory parent depth exceeded")
}

func (s *TransferService) deleteTransferSource(ctx context.Context, b transferDeletionBoundary) (int, error) {
	if b.download.ProviderType == models.DownloaderTypePan115Offline {
		driver, root, present, _, rootGone, err := s.pan115DeletionBoundary(ctx, b, b.sourceManifest.Files)
		if err != nil {
			return 0, err
		}
		if b.download.ProviderTaskID != "" {
			if b.download.DownloaderID == nil || s.downloader == nil {
				return 0, appError(CodeTransferDeletionUnavailable, "115 下载器不可用，未移除离线任务", nil)
			}
			_, client, clientErr := s.downloader.client(*b.download.DownloaderID)
			if clientErr != nil {
				return 0, appError(CodeTransferDeletionUnavailable, "115 下载器不可用，未移除离线任务", clientErr)
			}
			if cancelErr := client.Cancel(ctx, b.download.ProviderTaskID, false); cancelErr != nil && !providerTaskMissing(cancelErr) {
				return 0, cancelErr
			}
		}
		if rootGone {
			_ = s.db.Model(&models.DownloadTask{}).Where("id = ?", b.download.ID).Updates(map[string]any{"provider_task_id": "", "provider_status": "deleted", "updated_at": time.Now().UTC()}).Error
			return 0, nil
		}
		mutations, ok := driver.(cloudpkg.MutationDriver)
		if !ok || !mutations.Capabilities().Recycle {
			return 0, appError(CodeTransferDeletionUnavailable, "115 回收能力不可用", nil)
		}
		if err := mutations.Recycle(ctx, root.ID); err != nil {
			_, statErr := driver.Stat(ctx, root.ID)
			if code, _ := cloudpkg.ErrorInfo(statErr); code != cloudpkg.CodeNotFound {
				if statErr != nil {
					return 0, fmt.Errorf("115 package recycle failed and state is unavailable: %w", errors.Join(err, statErr))
				}
				return 0, err
			}
		}
		_ = s.db.Model(&models.DownloadTask{}).Where("id = ?", b.download.ID).Updates(map[string]any{"provider_task_id": "", "provider_status": "deleted", "updated_at": time.Now().UTC()}).Error
		return present, nil
	}
	if b.download.ProviderTaskID != "" && b.download.DownloaderID != nil && s.downloader != nil {
		_, client, err := s.downloader.client(*b.download.DownloaderID)
		if err != nil {
			return 0, appError(CodeTransferDeletionUnavailable, "原下载器不可用，未删除源文件", nil)
		}
		if err := client.Cancel(ctx, b.download.ProviderTaskID, true); err == nil {
			_ = s.db.Model(&models.DownloadTask{}).Where("id = ?", b.download.ID).Updates(map[string]any{"provider_task_id": "", "provider_status": "deleted", "updated_at": time.Now().UTC()}).Error
			return len(b.sourceManifest.Files), nil
		} else if !providerTaskMissing(err) {
			return 0, err
		}
	}
	return deleteLocalSourceManifest(b.download, b.sourceManifest.Files)
}

func deleteLocalSourceManifest(download models.DownloadTask, files []downloadpkg.File) (int, error) {
	root, err := canonicalLocalDeletionRoot(download.StagingAbsolutePath)
	if err != nil {
		return 0, err
	}
	categoryRoot := filepath.Join(root, firstNonEmpty(download.StagingCategory, download.ScrapeCategory))
	removed := 0
	for _, file := range files {
		target, err := resolveManifestSource(categoryRoot, root, file.RelativePath)
		if err != nil {
			return removed, err
		}
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != file.Size || ensureSafeDirectoryPath(root, filepath.Dir(target), false) != nil {
			return removed, errors.New("source file changed")
		}
		if err := os.Remove(target); err != nil {
			return removed, err
		}
		removed++
		pruneEmptyStagingDirectories(root, filepath.Dir(target))
	}
	return removed, nil
}

func (s *TransferService) deleteTransferLibrary(ctx context.Context, b transferDeletionBoundary, state *transferDeletionState) (int, error) {
	removed := 0
	if b.storage.Type == models.StorageTypeLocal {
		root, err := medialibrary.ResolveRoot(b.storage.RootPath, b.library.RelativeRoot)
		if err != nil {
			return removed, err
		}
		root, err = (storagefs.LocalDriver{}).CanonicalizeRoot(root)
		if err != nil {
			return removed, err
		}
		for _, item := range b.managed {
			if state.LibraryCompleted[item.ID] {
				continue
			}
			target := filepath.Join(root, filepath.FromSlash(item.RelativePath))
			info, err := os.Lstat(target)
			if !errors.Is(err, os.ErrNotExist) {
				if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != item.Size || ensureWithin(root, target) != nil || ensureSafeDirectoryPath(root, filepath.Dir(target), false) != nil {
					return removed, errors.New("managed library file changed")
				}
				if err := os.Remove(target); err != nil {
					return removed, err
				}
				removed++
				pruneEmptyStagingDirectories(root, filepath.Dir(target))
			}
			state.LibraryCompleted[item.ID] = true
			if err := s.markManagedDeletionProgress(item); err != nil {
				return removed, err
			}
		}
		return removed, nil
	}
	_, driver, err := s.connections.driver(*b.storage.ConnectionID)
	if err != nil {
		return removed, err
	}
	mutations, ok := driver.(cloudpkg.MutationDriver)
	if !ok || !mutations.Capabilities().Recycle {
		return removed, errors.New("cloud recycle unavailable")
	}
	root, err := providerItemWithinRoot(ctx, driver, b.library.ProviderRootID, b.storage.RootPath)
	if err != nil {
		return removed, err
	}
	for _, item := range b.managed {
		if state.LibraryCompleted[item.ID] {
			continue
		}
		current, err := providerItemWithinRoot(ctx, driver, item.ProviderItemID, root.ID)
		if err == nil {
			if current.IsDir || current.Size != item.Size || (item.ProviderParentID != "" && current.ParentID != item.ProviderParentID) {
				return removed, errors.New("managed cloud item changed")
			}
			if err := mutations.Recycle(ctx, current.ID); err != nil {
				return removed, err
			}
			removed++
		} else if code, _ := cloudpkg.ErrorInfo(err); code != cloudpkg.CodeNotFound {
			return removed, err
		}
		state.LibraryCompleted[item.ID] = true
		if err := s.markManagedDeletionProgress(item); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

func (s *TransferService) markManagedDeletionProgress(item models.MediaManagedItem) error {
	now := time.Now().UTC()
	result := s.db.Model(&models.MediaManagedItem{}).
		Where("id = ? AND transfer_task_id = ? AND library_id = ? AND relative_path = ? AND provider_item_id = ? AND provider_parent_id = ? AND size = ? AND managed = ? AND active = ?", item.ID, item.TransferTaskID, item.LibraryID, item.RelativePath, item.ProviderItemID, item.ProviderParentID, item.Size, true, true).
		Updates(map[string]any{"active": false, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return appError(CodeTransferDeletionBoundaryChanged, "媒体库托管清单已变化", nil)
	}
	return nil
}

func canonicalLocalDeletionRoot(raw string) (string, error) {
	root := filepath.Clean(strings.TrimSpace(raw))
	if root == "." || !filepath.IsAbs(root) {
		return "", errors.New("download staging root is invalid")
	}
	return (storagefs.LocalDriver{}).CanonicalizeRoot(root)
}

func (s *TransferService) persistDeletionState(id string, state transferDeletionState, code string) error {
	raw, _ := json.Marshal(state)
	return s.db.Model(&models.TransferDeletionPreview{}).Where("id = ?", id).Updates(map[string]any{"state_json": string(raw), "last_error_code": code, "updated_at": time.Now().UTC()}).Error
}

func (s *TransferService) persistDeletionFailure(preview models.TransferDeletionPreview, code string, request RequestContext) {
	_ = s.db.Transaction(func(tx *gorm.DB) error {
		_ = tx.Model(&models.TransferDeletionPreview{}).Where("id = ?", preview.ID).Updates(map[string]any{"last_error_code": code, "updated_at": time.Now().UTC()}).Error
		return s.audit.Record(tx, &preview.ActorID, "transfer.deletion_confirm", "transfer_task", preview.TransferTaskID, "failure", map[string]any{"scope": preview.Scope, "error_code": code}, request)
	})
}

func (s *TransferService) finalizeTransferDeletion(actor Actor, b transferDeletionBoundary, scope string, state transferDeletionState, request RequestContext) error {
	deletedJobs := []models.Job{}
	var changeRevision uint64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if deletionIncludesLibrary(scope) {
			paths := make([]string, 0, len(b.managed))
			for _, item := range b.managed {
				paths = append(paths, item.RelativePath)
			}
			if len(paths) > 0 {
				if err := tx.Where("library_id = ? AND relative_path IN ?", b.library.ID, paths).Delete(&models.MediaLibraryEntry{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", b.library.ID).Update("dirty_generation", gorm.Expr("dirty_generation + 1")).Error; err != nil {
				return err
			}
			if s.mediaChanges != nil {
				var current models.MediaLibrary
				if err := tx.First(&current, b.library.ID).Error; err != nil {
					return err
				}
				change, err := s.mediaChanges.RecordTx(tx, b.library.ID, current.DirtyGeneration, models.MediaLibraryChangeRemoval, true)
				if err != nil {
					return err
				}
				changeRevision = change.Revision
			} else if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", b.library.ID).Update("content_revision", gorm.Expr("content_revision + 1")).Error; err != nil {
				return err
			}
		}
		if err := s.audit.Record(tx, &actor.User.ID, "transfer.deletion_confirm", "transfer_task", b.transfer.ID, "success", map[string]any{"scope": scope, "source_removed": state.SourceRemoved, "library_removed": state.LibraryRemoved, "library_id": b.library.ID}, request); err != nil {
			return err
		}
		reorgJobs, err := cleanupTransferHistoryDependencies(tx, b.transfer.ID)
		if err != nil {
			return err
		}
		deletedJobs = append(deletedJobs, reorgJobs...)
		if err := tx.Delete(&b.transfer).Error; err != nil {
			return err
		}
		if err := tx.Delete(&b.transferJob).Error; err != nil {
			return err
		}
		deletedJobs = append(deletedJobs, b.transferJob)
		if deletionIncludesSource(scope) {
			if b.seeding != nil {
				if err := tx.Delete(b.seeding).Error; err != nil {
					return err
				}
				if err := tx.Delete(b.seedingJob).Error; err != nil {
					return err
				}
				deletedJobs = append(deletedJobs, *b.seedingJob)
			}
			if err := tx.Delete(&b.download).Error; err != nil {
				return err
			}
			if err := tx.Delete(&b.downloadJob).Error; err != nil {
				return err
			}
			deletedJobs = append(deletedJobs, b.downloadJob)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.queue != nil {
		for _, job := range deletedJobs {
			s.queue.publish(job, "job.deleted")
		}
	}
	if changeRevision > 0 && s.mediaChanges != nil {
		s.mediaChanges.NotifyCommitted(b.library.ID, changeRevision)
	}
	return nil
}

func manifestBytes(files []downloadpkg.File) int64 {
	var total int64
	for _, item := range files {
		if item.Size > 0 {
			total += item.Size
		}
	}
	return total
}
func managedBytes(items []models.MediaManagedItem) int64 {
	var total int64
	for _, item := range items {
		if item.Size > 0 {
			total += item.Size
		}
	}
	return total
}

func deletionWarnings(scope string) []string {
	switch scope {
	case models.TransferDeletionScopeRecordOnly:
		return []string{"只清理 OhMyCine 的媒体整理记录，不会删除任何文件。"}
	case models.TransferDeletionScopeRecordAndSource:
		return []string{"会删除下载器任务及其来源数据；媒体库文件保留。", "该操作不可通过 OhMyCine 撤销。"}
	case models.TransferDeletionScopeRecordAndLibrary:
		return []string{"只删除当前 Transfer 明确托管的媒体库文件；来源与做种数据保留。", "未登记的同目录文件不会被删除。"}
	default:
		return []string{"会同时删除来源数据和当前 Transfer 明确托管的媒体库文件。", "该操作不可通过 OhMyCine 撤销。"}
	}
}
