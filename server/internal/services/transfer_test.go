package services

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

func TestDownloadListExposesFailedTransferJobForStageRetry(t *testing.T) {
	queue, actor, download, _, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	actor.Permissions[authz.PermissionDownloadsReadAll] = struct{}{}
	actor.Permissions[authz.PermissionDownloadsManageAll] = struct{}{}
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop()).Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&transfer).Update("phase", models.TransferTaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", transfer.JobID).Update("status", models.JobStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	listed, err := (&DownloadService{db: queue.db}).List(actor, 10)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	if listed[0].TransferPhase != models.TransferTaskStatusFailed || listed[0].TransferTaskID != transfer.ID || listed[0].TransferJobID != transfer.JobID || listed[0].TransferJobStatus != models.JobStatusFailed {
		t.Fatalf("transfer retry summary=%+v", listed[0])
	}
}

func TestTransferOrganizationListDetailAndOwnership(t *testing.T) {
	queue, actor, download, source, destination := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	actor.Permissions[authz.PermissionTransfersReadAll] = struct{}{}
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.Wait != nil {
		t.Fatalf("result=%+v", result)
	}
	if err := queue.Complete(claimed.Job.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	page, err := service.List(actor, TransferListFilter{Status: "completed", Category: "华语电影", TransferMode: models.MediaLibraryTransferCopy, Keyword: "Movie", Page: 1, PageSize: 20})
	if err != nil || page.Total != 1 || len(page.List) != 1 || page.Stats.CompletedToday != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if len(page.FilterOptions.Libraries) != 1 || page.FilterOptions.Libraries[0].ID != page.List[0].LibraryID || len(page.FilterOptions.Categories) != 1 || page.FilterOptions.Categories[0] != "华语电影" {
		t.Fatalf("filter options=%+v", page.FilterOptions)
	}
	detail, err := service.Get(actor, page.List[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.PlanSummary == nil || len(detail.PlanSummary.Items) != 1 || detail.PlanSummary.Items[0].RelativePath != "华语电影/Movie (2024)/Movie (2024).mkv" || detail.PlanSummary.Items[0].Result != "completed" {
		t.Fatalf("plan summary=%+v", detail.PlanSummary)
	}
	if detail.Job.Status != models.JobStatusCompleted || len(detail.Timeline) == 0 || detail.MovieDirectoryTemplate == "" {
		t.Fatalf("detail=%+v", detail)
	}
	publicJSON, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, absolute := range []string{download.StagingAbsolutePath, filepath.Dir(filepath.Dir(destination)), source} {
		if strings.Contains(string(publicJSON), absolute) {
			t.Fatalf("detail leaked absolute path %q: %s", absolute, publicJSON)
		}
	}
	for _, privateField := range []string{"manifest_json", "provider_task_id", "staging_absolute_path", "target_storage_root", "payload_json", "checkpoint_json"} {
		if strings.Contains(string(publicJSON), privateField) {
			t.Fatalf("detail leaked private field %q: %s", privateField, publicJSON)
		}
	}
	unsafeSummary := `{"items":[{"relative_path":"C:/private/movie.mkv","kind":"video","size":1,"result":"planned"}],"total_files":1,"truncated":false}`
	if err := queue.db.Model(&models.TransferTask{}).Where("id = ?", detail.ID).Update("plan_summary_json", unsafeSummary).Error; err != nil {
		t.Fatal(err)
	}
	unsafeDetail, err := service.Get(actor, detail.ID)
	if err != nil || unsafeDetail.PlanSummary != nil {
		t.Fatalf("unsafe stored plan was exposed: plan=%+v err=%v", unsafeDetail.PlanSummary, err)
	}
	other := Actor{User: models.User{ID: actor.User.ID + 99}, Permissions: map[string]struct{}{authz.PermissionTransfersReadOwn: {}}}
	otherPage, err := service.List(other, TransferListFilter{Keyword: "Movie", Page: 1, PageSize: 20})
	if err != nil || otherPage.Total != 0 || len(otherPage.List) != 0 || len(otherPage.FilterOptions.Libraries) != 0 || len(otherPage.FilterOptions.Categories) != 0 || otherPage.Stats != (TransferStats{}) {
		t.Fatalf("other page=%+v err=%v", otherPage, err)
	}
	if _, err := service.Get(other, page.List[0].ID); ErrorCode(err) != CodeNotFound {
		t.Fatalf("other detail error=%v", err)
	}
}

func TestTransferOrganizationRetryAndCancellationUseJobStatus(t *testing.T) {
	queue, actor, download, _, _ := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	actor.Permissions[authz.PermissionTransfersReadAll] = struct{}{}
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&transfer).Update("phase", models.TransferTaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}

	processing, err := service.List(actor, TransferListFilter{Status: "processing", Page: 1, PageSize: 20})
	if err != nil || processing.Total != 1 || processing.Stats.Processing != 1 || processing.Stats.Failed != 0 {
		t.Fatalf("processing page=%+v err=%v", processing, err)
	}
	failed, err := service.List(actor, TransferListFilter{Status: "failed", Page: 1, PageSize: 20})
	if err != nil || failed.Total != 0 {
		t.Fatalf("failed page=%+v err=%v", failed, err)
	}

	if err := queue.db.Model(&models.Job{}).Where("id = ?", transfer.JobID).Update("status", models.JobStatusCancelled).Error; err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.List(actor, TransferListFilter{Status: "cancelled", Page: 1, PageSize: 20})
	if err != nil || cancelled.Total != 1 || cancelled.Stats.Processing != 0 || cancelled.Stats.Failed != 0 {
		t.Fatalf("cancelled page=%+v err=%v", cancelled, err)
	}
}

func TestDownloadAndTransferLifecycleScopesMoveSettledWorkIntoHistory(t *testing.T) {
	queue, actor, download, source, destination := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, true)
	actor.Permissions[authz.PermissionDownloadsReadAll] = struct{}{}
	actor.Permissions[authz.PermissionDownloadsManageAll] = struct{}{}
	actor.Permissions[authz.PermissionTransfersReadAll] = struct{}{}
	transfers := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	downloads := &DownloadService{db: queue.db, audit: queue.audit, queue: queue}
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := transfers.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", download.JobID).Update("status", models.JobStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}

	active, activeTotal, err := downloads.ListScoped(actor, DownloadListScopeActive, 20)
	if err != nil || activeTotal != 1 || len(active) != 1 || active[0].LifecycleScope != DownloadListScopeActive {
		t.Fatalf("active=%+v total=%d err=%v", active, activeTotal, err)
	}
	transferActive, err := transfers.List(actor, TransferListFilter{Scope: "active", Page: 1, PageSize: 20})
	if err != nil || transferActive.Total != 1 {
		t.Fatalf("transfer active=%+v err=%v", transferActive, err)
	}

	if err := queue.db.Model(&transfer).Updates(map[string]any{"phase": models.TransferTaskStatusCompleted, "finished_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", transfer.JobID).Update("status", models.JobStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	active, activeTotal, err = downloads.ListScoped(actor, DownloadListScopeActive, 20)
	if err != nil || activeTotal != 0 || len(active) != 0 {
		t.Fatalf("settled active=%+v total=%d err=%v", active, activeTotal, err)
	}
	history, historyTotal, err := downloads.ListScoped(actor, DownloadListScopeHistory, 20)
	if err != nil || historyTotal != 1 || len(history) != 1 || history[0].LifecycleScope != DownloadListScopeHistory {
		t.Fatalf("history=%+v total=%d err=%v", history, historyTotal, err)
	}
	transferHistory, err := transfers.List(actor, TransferListFilter{Scope: "history", Page: 1, PageSize: 20})
	if err != nil || transferHistory.Total != 1 {
		t.Fatalf("transfer history=%+v err=%v", transferHistory, err)
	}
	seeding := models.SeedingTask{ID: "seeding-history", OwnerID: actor.User.ID, DownloadTaskID: download.ID, DownloaderName: download.DownloaderName, ProviderType: download.ProviderType, ProviderTaskID: "provider-history", TransferMode: models.MediaLibraryTransferCopy, DeleteData: true, Phase: models.SeedingTaskStatusQueued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	_, err = queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "seeding", DisplayName: "做种：Movie", Payload: seedingJobPayload{SeedingTaskID: seeding.ID}}, func(tx *gorm.DB, job models.Job) error {
		seeding.JobID = job.ID
		return tx.Create(&seeding).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	active, activeTotal, err = downloads.ListScoped(actor, DownloadListScopeActive, 20)
	if err != nil || activeTotal != 1 || len(active) != 1 || active[0].SeedingTaskID != seeding.ID {
		t.Fatalf("seeding active=%+v total=%d err=%v", active, activeTotal, err)
	}
	if err := queue.db.Model(&seeding).Updates(map[string]any{"phase": models.SeedingTaskStatusCompleted, "finished_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", seeding.JobID).Update("status", models.JobStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	history, historyTotal, err = downloads.ListScoped(actor, DownloadListScopeHistory, 20)
	if err != nil || historyTotal != 1 || len(history) != 1 || history[0].SeedingJobStatus != models.JobStatusCompleted {
		t.Fatalf("settled seeding history=%+v total=%d err=%v", history, historyTotal, err)
	}

	if err := downloads.Delete(context.Background(), actor, download.ID, RequestContext{RequestID: "history-delete"}); err != nil {
		t.Fatal(err)
	}
	for label, model := range map[string]any{"download": &models.DownloadTask{}, "transfer": &models.TransferTask{}, "seeding": &models.SeedingTask{}, "job": &models.Job{}} {
		var count int64
		query := queue.db.Model(model)
		if label == "download" {
			query = query.Where("id = ?", download.ID)
		} else if label == "transfer" {
			query = query.Where("id = ?", transfer.ID)
		} else if label == "seeding" {
			query = query.Where("id = ?", seeding.ID)
		} else {
			query = query.Where("id IN ?", []string{download.JobID, transfer.JobID, seeding.JobID})
		}
		if err := query.Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", label, count, err)
		}
	}
	for _, path := range []string{source, destination} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("history deletion changed file %q: %v", path, err)
		}
	}
	var audit models.AuditLog
	if err := queue.db.Where("action = ? AND target_id = ?", "download.history_delete", download.ID).First(&audit).Error; err != nil || !strings.Contains(audit.Metadata, `"cleanup":"history_only"`) || strings.Contains(audit.Metadata, source) {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
}

func TestTransferDeleteRemovesOnlyTerminalOrganizationRecords(t *testing.T) {
	for _, status := range []string{models.JobStatusFailed, models.JobStatusCancelled, models.JobStatusCompleted} {
		t.Run(status, func(t *testing.T) {
			queue, actor, download, source, destination := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, true)
			service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
			manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
			if err := service.Enqueue(download, manifest); err != nil {
				t.Fatal(err)
			}
			var task models.TransferTask
			if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
				t.Fatal(err)
			}
			if err := queue.db.Model(&models.Job{}).Where("id = ?", task.JobID).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := queue.db.Create(&models.JobAttempt{JobID: task.JobID, AttemptNumber: 1, LeaseTokenHash: "hash", Status: status, StartedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if err := queue.db.Create(&models.JobStatusEvent{JobID: task.JobID, EventType: "test", ToStatus: status, CreatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if err := queue.db.Create(&models.JobActionRequest{JobID: task.JobID, Version: 1, ActionType: "transfer_conflict", Prompt: "test", OptionsJSON: `[]`, PreviewJSON: `{}`, CreatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}

			if err := service.Delete(actor, task.ID, RequestContext{RequestID: "delete-transfer"}); err != nil {
				t.Fatal(err)
			}
			for label, model := range map[string]any{
				"transfer": &models.TransferTask{}, "job": &models.Job{}, "attempt": &models.JobAttempt{},
				"event": &models.JobStatusEvent{}, "action": &models.JobActionRequest{},
			} {
				query := queue.db
				if label == "transfer" {
					query = query.Where("id = ?", task.ID)
				} else if label == "job" {
					query = query.Where("id = ?", task.JobID)
				} else {
					query = query.Where("job_id = ?", task.JobID)
				}
				var count int64
				if err := query.Model(model).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("%s count=%d err=%v", label, count, err)
				}
			}
			var downloadCount int64
			if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", download.ID).Count(&downloadCount).Error; err != nil || downloadCount != 1 {
				t.Fatalf("download count=%d err=%v", downloadCount, err)
			}
			for _, path := range []string{source, destination} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("media path changed %q: %v", path, err)
				}
			}
			var audit models.AuditLog
			if err := queue.db.Where("action = ? AND target_id = ?", "transfer.delete", task.ID).First(&audit).Error; err != nil {
				t.Fatal(err)
			}
			if strings.Contains(audit.Metadata, source) || strings.Contains(audit.Metadata, destination) || !strings.Contains(audit.Metadata, `"job_status"`) {
				t.Fatalf("unsafe or incomplete audit metadata: %s", audit.Metadata)
			}
		})
	}
}

func TestTransferDeleteRejectsActiveStatesAndUnauthorizedOwner(t *testing.T) {
	for _, status := range []string{models.JobStatusQueued, models.JobStatusRunning, models.JobStatusWaitingUserAction, models.JobStatusRetryWait, models.JobStatusPaused} {
		t.Run(status, func(t *testing.T) {
			queue, actor, download, _, _ := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
			service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
			manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
			if err := service.Enqueue(download, manifest); err != nil {
				t.Fatal(err)
			}
			var task models.TransferTask
			if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
				t.Fatal(err)
			}
			if err := queue.db.Model(&models.Job{}).Where("id = ?", task.JobID).Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			if err := service.Delete(actor, task.ID, RequestContext{}); ErrorCode(err) != CodeQueueStateConflict {
				t.Fatalf("status=%s error=%v", status, err)
			}
			var count int64
			if err := queue.db.Model(&models.TransferTask{}).Where("id = ?", task.ID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("task count=%d err=%v", count, err)
			}
		})
	}

	queue, actor, download, _, _ := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", task.JobID).Update("status", models.JobStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	unauthorized := Actor{User: models.User{ID: actor.User.ID + 1}, Permissions: map[string]struct{}{authz.PermissionJobsControlOwn: {}}}
	if err := service.Delete(unauthorized, task.ID, RequestContext{}); ErrorCode(err) != CodePermissionDenied {
		t.Fatalf("unauthorized error=%v", err)
	}
	owner := actor
	delete(owner.Permissions, authz.PermissionJobsControlAll)
	owner.Permissions[authz.PermissionJobsControlOwn] = struct{}{}
	if err := service.Delete(owner, task.ID, RequestContext{}); err != nil {
		t.Fatalf("owner delete error=%v", err)
	}
}

func TestTransferPlanSummaryBoundsAndRejectsUnsafePaths(t *testing.T) {
	plan := make([]transferPlanItem, 0, maxTransferPlanSummaryItems+5)
	for index := 0; index < maxTransferPlanSummaryItems+5; index++ {
		plan = append(plan, transferPlanItem{Relative: filepath.ToSlash(filepath.Join("电影", "Movie "+strconv.Itoa(index)+".mkv")), Size: int64(index)})
	}
	summary, err := newTransferPlanSummary(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Items) != maxTransferPlanSummaryItems || !summary.Truncated || summary.TotalFiles != len(plan) {
		t.Fatalf("summary=%+v", summary)
	}
	encoded, err := encodeTransferPlanSummary(summary)
	if err != nil || len(encoded) > maxTransferPlanSummaryBytes {
		t.Fatalf("encoded bytes=%d err=%v", len(encoded), err)
	}
	for _, unsafe := range []string{"../escape.mkv", `C:\\Media\\movie.mkv`, "/srv/media/movie.mkv", "folder/line\nbreak.mkv"} {
		if _, err := sanitizeTransferRelativePath(unsafe); err == nil {
			t.Fatalf("unsafe path accepted: %q", unsafe)
		}
	}
	if decoded, err := decodeTransferPlanSummary(encoded + `{}`); err == nil || decoded != nil {
		t.Fatalf("trailing JSON was accepted: decoded=%+v err=%v", decoded, err)
	}
}

func transferFixture(t *testing.T, mode, policy string, existingTarget bool) (*QueueService, Actor, models.DownloadTask, string, string) {
	t.Helper()
	queue, actor, _ := queueFixture(t)
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	target := t.TempDir()
	storage := models.Storage{Name: "Target", NameNormalized: strings.ToLower("Target" + target), Type: models.StorageTypeLocal, RootPath: target, RootPathNormalized: strings.ToLower(target), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Movies", NameNormalized: strings.ToLower("Movies" + target), StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", SortOrder: 1, TransferMode: mode, ConflictPolicy: policy, MovieDirectoryTemplate: "{category}/{title} ({year})", MovieFilenameTemplate: "{title} ({year})", TVDirectoryTemplate: "{category}/{title} ({year})/Season {season:02}", TVFilenameTemplate: "{title} - S{season:02}E{episode:02}", Enabled: false, Recursive: true, FullScanIntervalHours: 24, IncrementalMinutes: 15, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, MetadataLanguage: "zh-CN", MetadataRegion: "CN", MatchStrategy: "balanced", ProviderRatePerSecond: 100, ProviderConcurrency: 2, MetadataRatePerSecond: 5, MetadataConcurrency: 1, Status: models.MediaLibraryStatusDisabled, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	category := "华语电影"
	relativeSource := "Movie.2024.mkv"
	source := filepath.Join(staging, category, relativeSource)
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("video-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	year := 2024
	tmdbID, confidence := int64(550), .98
	task := models.DownloadTask{ID: "download-" + mode + "-" + policy, OwnerID: actor.User.ID, DownloaderName: "qBit", ProviderType: models.DownloaderTypeQBittorrent, SourceCiphertext: "encrypted", StagingAbsolutePath: staging, ProfileID: profile.ID, ProfileRevision: profile.Revision, ProfileRulesJSON: profile.RulesJSON, TargetLibraryID: &library.ID, TargetLibraryName: library.Name, TargetStorageID: &storage.ID, TargetStorageRoot: target, TargetRelativeRoot: "/", TransferMode: mode, ConflictPolicy: policy, MovieDirectoryTemplate: library.MovieDirectoryTemplate, MovieFilenameTemplate: library.MovieFilenameTemplate, TVDirectoryTemplate: library.TVDirectoryTemplate, TVFilenameTemplate: library.TVFilenameTemplate, DisplayName: "Movie", Phase: models.DownloadTaskStatusCompleted, ScrapeStatus: "completed_verified", ScrapeTitle: "Movie", ScrapeMediaType: "movie", ScrapeCategory: category, ScrapeTMDBID: &tmdbID, ScrapeConfidence: &confidence, ScrapeYear: &year, ManifestFileCount: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	_, err := queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: "Movie", Payload: downloadJobPayload{DownloadTaskID: task.ID}}, func(tx *gorm.DB, job models.Job) error {
		task.JobID = job.ID
		return tx.Create(&task).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(target, category, "Movie (2024)", "Movie (2024).mkv")
	if existingTarget {
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return queue, actor, task, source, destination
}

func TestTransferWorkerCopiesAndMovesIntoMediaLibrary(t *testing.T) {
	for _, mode := range []string{models.MediaLibraryTransferCopy, models.MediaLibraryTransferMove} {
		t.Run(mode, func(t *testing.T) {
			queue, _, download, source, destination := transferFixture(t, mode, models.MediaLibraryConflictOverwrite, false)
			service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
			manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
			if err := service.Enqueue(download, manifest); err != nil {
				t.Fatal(err)
			}
			claimed, err := queue.Claim([]string{"transfer"})
			if err != nil || claimed == nil {
				t.Fatalf("claim=%+v err=%v", claimed, err)
			}
			result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
			if result.ErrorCode != "" || result.Wait != nil {
				t.Fatalf("result=%+v", result)
			}
			if payload, err := os.ReadFile(destination); err != nil || string(payload) != "video-data" {
				t.Fatalf("destination payload=%q err=%v", payload, err)
			}
			_, sourceErr := os.Stat(source)
			if mode == models.MediaLibraryTransferMove && !os.IsNotExist(sourceErr) {
				t.Fatalf("move source still exists: %v", sourceErr)
			}
			if mode == models.MediaLibraryTransferCopy && sourceErr != nil {
				t.Fatalf("copy source missing: %v", sourceErr)
			}
		})
	}
}

func TestTransferWorkerAsksWithoutHoldingAResolvedPolicy(t *testing.T) {
	queue, _, download, _, destination := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictAsk, true)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.Wait == nil || result.Wait.ActionType != "transfer_conflict" {
		t.Fatalf("result=%+v", result)
	}
	payload, err := os.ReadFile(destination)
	if err != nil || string(payload) != "existing" {
		t.Fatalf("existing target changed before confirmation: %q err=%v", payload, err)
	}
}

func TestTransferWorkerRenamesVideoAndSidecarAsOneGroup(t *testing.T) {
	queue, _, download, source, destination := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename, true)
	sidecarSource := strings.TrimSuffix(source, filepath.Ext(source)) + ".zh-CN.forced.ass"
	if err := os.WriteFile(sidecarSource, []byte("subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{
		{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes},
		{RelativePath: "Movie.2024.zh-CN.forced.ass", Size: int64(len("subtitle"))},
	}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.Wait != nil {
		t.Fatalf("result=%+v", result)
	}
	renamedBase := strings.TrimSuffix(destination, filepath.Ext(destination)) + " (1)"
	for path, want := range map[string]string{renamedBase + ".mkv": "video-data", renamedBase + ".zh-CN.forced.ass": "subtitle"} {
		payload, err := os.ReadFile(path)
		if err != nil || string(payload) != want {
			t.Fatalf("renamed companion %s payload=%q err=%v", path, payload, err)
		}
	}
}

func TestBuildTransferTargetsPreservesSubtitleSuffixesFormatsAndUniqueness(t *testing.T) {
	_, _, download, _, _ := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{
		{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes},
		{RelativePath: "Movie.2024.zh-CN.forced.ass", Size: 1},
		{RelativePath: "Movie.2024.en.default.vtt", Size: 1},
		{RelativePath: "Movie.2024.HI.sup", Size: 1},
		{RelativePath: "Movie.2024.zh.sub", Size: 1},
		{RelativePath: "Movie.2024.zh.idx", Size: 1},
		{RelativePath: "Movie.2024.ZH.ass", Size: 1},
		{RelativePath: "Movie.2024.zh.ass", Size: 1},
	}}
	targets, err := buildTransferTargets(download, manifest)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		key := strings.ToLower(target.Relative)
		if _, duplicate := got[key]; duplicate {
			t.Fatalf("duplicate target %q in %+v", target.Relative, targets)
		}
		got[key] = struct{}{}
	}
	for _, suffix := range []string{".zh-cn.forced.ass", ".en.default.vtt", ".hi.sup", ".zh.sub", ".zh.idx", ".zh.ass", ".zh.2.ass"} {
		found := false
		for relative := range got {
			if strings.HasSuffix(relative, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing suffix %q in targets %+v", suffix, targets)
		}
	}
}

func TestBuildTransferTargetsPreservesMovieReleaseVersionAcrossTemplates(t *testing.T) {
	_, _, download, _, _ := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	download.ScrapeTitle = "七武士"
	year := 1954
	download.ScrapeYear = &year
	manifest := downloadpkg.Manifest{Name: "Seven Samurai", Complete: true, Files: []downloadpkg.File{{RelativePath: "Seven.Samurai.1954.CC.2160p.UHD.BluRay.REMUX.HDR10.DoVi.x265.DTS-HD.MA.2.0-SONYHD.mkv", Size: minimumAutomaticTransferVideoBytes}}}

	targets, err := buildTransferTargets(download, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || !strings.HasSuffix(targets[0].Relative, "七武士 (1954) - 2160p UHD BluRay REMUX HDR10 Dolby Vision.mkv") {
		t.Fatalf("legacy template target=%+v", targets)
	}

	download.MovieFilenameTemplate = "{title} ({year}) [{version}]"
	targets, err = buildTransferTargets(download, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || !strings.HasSuffix(targets[0].Relative, "七武士 (1954) [2160p UHD BluRay REMUX HDR10 Dolby Vision].mkv") {
		t.Fatalf("version placeholder target=%+v", targets)
	}
	if strings.Count(targets[0].Relative, "2160p") != 1 {
		t.Fatalf("version suffix duplicated: %s", targets[0].Relative)
	}
	if err := validateImportTemplate(download.MovieFilenameTemplate, false); err != nil {
		t.Fatalf("version placeholder rejected: %v", err)
	}
}

func TestRenderImportTemplateRemovesEmptyOptionalVersionSuffix(t *testing.T) {
	year := 2024
	result, err := renderImportTemplate("{title} ({year}) - {version}", transferTemplateValues{Title: "Movie", Year: &year}, false)
	if err != nil {
		t.Fatal(err)
	}
	if result != "Movie (2024)" {
		t.Fatalf("result=%q", result)
	}
}

func TestTransferWorkerAppliesAutomaticConflictPolicies(t *testing.T) {
	for _, test := range []struct {
		policy string
		want   string
	}{{models.MediaLibraryConflictOverwrite, "video-data"}, {models.MediaLibraryConflictSkip, "existing"}} {
		t.Run(test.policy, func(t *testing.T) {
			queue, _, download, _, destination := transferFixture(t, models.MediaLibraryTransferCopy, test.policy, true)
			service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
			manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
			if err := service.Enqueue(download, manifest); err != nil {
				t.Fatal(err)
			}
			claimed, err := queue.Claim([]string{"transfer"})
			if err != nil || claimed == nil {
				t.Fatalf("claim=%+v err=%v", claimed, err)
			}
			result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
			if result.ErrorCode != "" || result.Wait != nil {
				t.Fatalf("result=%+v", result)
			}
			payload, err := os.ReadFile(destination)
			if err != nil || string(payload) != test.want {
				t.Fatalf("destination payload=%q want=%q err=%v", payload, test.want, err)
			}
		})
	}
}

func TestTransferWorkerCreatesSymlinkWhenPlatformAllowsIt(t *testing.T) {
	probeRoot := t.TempDir()
	probeSource := filepath.Join(probeRoot, "source")
	probeLink := filepath.Join(probeRoot, "link")
	if err := os.WriteFile(probeSource, []byte("probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(probeSource, probeLink); err != nil {
		t.Skipf("platform does not permit test symlinks: %v", err)
	}
	queue, _, download, source, destination := transferFixture(t, models.MediaLibraryTransferSymlink, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.Wait != nil {
		t.Fatalf("result=%+v", result)
	}
	info, err := os.Lstat(destination)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("destination is not a symlink: mode=%v err=%v", info, err)
	}
	linked, err := os.Readlink(destination)
	if err != nil || filepath.Clean(linked) != filepath.Clean(source) {
		t.Fatalf("link=%q source=%q err=%v", linked, source, err)
	}
}

func TestTransferRejectsSymlinkedSourceAncestor(t *testing.T) {
	queue, _, download, source, _ := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	categoryRoot := filepath.Dir(source)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(categoryRoot); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, filepath.Base(source)), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, categoryRoot); err != nil {
		t.Skipf("platform does not permit test symlinks: %v", err)
	}
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if _, _, err := buildTransferPlan(download, manifest); err == nil {
		t.Fatal("symlinked source ancestor was accepted")
	}
	var count int64
	if err := queue.db.Model(&models.TransferTask{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("unexpected transfer rows=%d err=%v", count, err)
	}
}

func TestTransferPlanFindsQBittorrentFileLeftInStagingRoot(t *testing.T) {
	_, _, download, categorySource, destination := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	rootSource := filepath.Join(download.StagingAbsolutePath, filepath.Base(categorySource))
	if err := os.Rename(categorySource, rootSource); err != nil {
		t.Fatal(err)
	}
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: filepath.Base(rootSource), Size: minimumAutomaticTransferVideoBytes}}}
	plan, _, err := buildTransferPlan(download, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 {
		t.Fatalf("plan length=%d want=1", len(plan))
	}
	if filepath.Clean(plan[0].Source) != filepath.Clean(rootSource) {
		t.Fatalf("source=%q want=%q", plan[0].Source, rootSource)
	}
	if filepath.Clean(plan[0].Destination) != filepath.Clean(destination) {
		t.Fatalf("destination=%q want=%q", plan[0].Destination, destination)
	}
}

func TestResolveManifestSourcePrefersClassifiedPath(t *testing.T) {
	staging := t.TempDir()
	categoryRoot := filepath.Join(staging, "TV")
	if err := os.MkdirAll(categoryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(categoryRoot, "Episode.mkv"), filepath.Join(staging, "Episode.mkv")} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := resolveManifestSource(categoryRoot, staging, "Episode.mkv")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(categoryRoot, "Episode.mkv")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("source=%q want classified source %q", got, want)
	}
}

func TestTransferRejectsSymlinkedTargetAncestor(t *testing.T) {
	queue, _, download, _, destination := transferFixture(t, models.MediaLibraryTransferCopy, models.MediaLibraryConflictOverwrite, false)
	external := t.TempDir()
	categoryTarget := filepath.Dir(filepath.Dir(destination))
	if err := os.Symlink(external, categoryTarget); err != nil {
		t.Skipf("platform does not permit test symlinks: %v", err)
	}
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie.2024", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	if err := service.Enqueue(download, manifest); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "transfer_write_failed" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(filepath.Join(external, filepath.Base(filepath.Dir(destination)), filepath.Base(destination))); !os.IsNotExist(err) {
		t.Fatalf("unsafe target was written outside the library: %v", err)
	}
}

func TestTransferEpisodeFactsUsesValidatedManualSingleEpisodeOverride(t *testing.T) {
	season, episode := 2, 9
	download := models.DownloadTask{
		RecognitionOverrideMediaType: "tv",
		RecognitionOverrideSeason:    &season,
		RecognitionOverrideEpisode:   &episode,
	}
	actualSeason, actualEpisode := transferEpisodeFacts(download, "Ultraman Omega.mkv", 1)
	if actualSeason == nil || *actualSeason != 2 || actualEpisode == nil || *actualEpisode != 9 {
		t.Fatalf("season=%v episode=%v", actualSeason, actualEpisode)
	}
	_, multipleEpisode := transferEpisodeFacts(download, "Ultraman Omega.mkv", 2)
	if multipleEpisode != nil {
		t.Fatalf("manual single episode override leaked into multi-video package: %v", *multipleEpisode)
	}
}

func TestTransferEpisodeFactsUsesPersistedScrapeFactsWithoutReusingSingleEpisode(t *testing.T) {
	season, episode := 1, 9
	download := models.DownloadTask{
		ScrapeMediaType: "tv",
		ScrapeSeason:    &season,
		ScrapeEpisode:   &episode,
	}
	actualSeason, actualEpisode := transferEpisodeFacts(download, "Ultraman Omega.mkv", 1)
	if actualSeason == nil || *actualSeason != 1 || actualEpisode == nil || *actualEpisode != 9 {
		t.Fatalf("season=%v episode=%v", actualSeason, actualEpisode)
	}
	_, multipleEpisode := transferEpisodeFacts(download, "Ultraman Omega.mkv", 2)
	if multipleEpisode != nil {
		t.Fatalf("persisted single episode leaked into multi-video package: %v", *multipleEpisode)
	}
}
