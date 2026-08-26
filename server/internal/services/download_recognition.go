package services

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DownloadRecognitionOverrideInput struct {
	TMDBID    int64
	MediaType string
	Season    *int
	Episode   *int
}

func (s *DownloadService) RecognitionCandidates(ctx context.Context, actor Actor, id, title, mediaType string, year *int) ([]tmdb.Candidate, error) {
	task, _, err := s.downloadRecognitionRecoveryContext(actor, id, false)
	if err != nil {
		return nil, err
	}
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		title = firstNonEmpty(task.ScrapeTitle, task.DisplayName)
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "" && mediaType != "movie" && mediaType != "tv" {
		return nil, appError(CodeInvalidRequest, "媒体类型无效", nil)
	}
	if s.metadata == nil {
		return nil, appError(CodeTMDBUnavailable, "TMDB 未配置", nil)
	}
	client, _, _, err := s.metadata.clientWithCredentialInfo()
	if err != nil {
		return nil, err
	}
	language, region := s.downloadRecognitionLocale(task)
	types := []string{mediaType}
	if mediaType == "" {
		types = []string{"movie", "tv"}
	}
	items := make([]tmdb.Candidate, 0, 10)
	seen := make(map[string]struct{})
	var firstFailure error
	for _, kind := range types {
		candidates, searchErr := client.SearchCandidates(ctx, kind, title, year, language, region, 10)
		if searchErr != nil {
			if tmdb.ErrorCode(searchErr) != tmdb.ErrorNoMatch && firstFailure == nil {
				firstFailure = searchErr
			}
			continue
		}
		for _, candidate := range candidates {
			key := candidate.MediaType + ":" + strconv.FormatInt(candidate.ID, 10)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, candidate)
		}
	}
	if len(items) == 0 {
		if firstFailure != nil {
			return nil, appError(tmdb.ErrorCode(firstFailure), classificationFallbackMessage(tmdb.ErrorCode(firstFailure)), nil)
		}
		return nil, appError(tmdb.ErrorNoMatch, "没有找到匹配的 TMDB 项目，请调整关键词后重试", nil)
	}
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].Confidence != items[right].Confidence {
			return items[left].Confidence > items[right].Confidence
		}
		if items[left].ReleaseYear != nil && items[right].ReleaseYear != nil && *items[left].ReleaseYear != *items[right].ReleaseYear {
			return *items[left].ReleaseYear > *items[right].ReleaseYear
		}
		if items[left].MediaType != items[right].MediaType {
			return items[left].MediaType < items[right].MediaType
		}
		return items[left].ID < items[right].ID
	})
	if len(items) > 10 {
		items = items[:10]
	}
	return items, nil
}

func (s *DownloadService) OverrideRecognition(ctx context.Context, actor Actor, id string, input DownloadRecognitionOverrideInput, request RequestContext) (DownloadTaskSummary, error) {
	task, _, err := s.downloadRecognitionRecoveryContext(actor, id, true)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	if input.TMDBID <= 0 || (input.MediaType != "movie" && input.MediaType != "tv") {
		return DownloadTaskSummary{}, appError(CodeInvalidRequest, "TMDB 匹配选择无效", nil)
	}
	season, episode, err := validateDownloadRecognitionEpisodeOverride(input.MediaType, input.Season, input.Episode)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	if err := validateCompletedManifestEpisodeOverride(task.CompletedManifestJSON, episode); err != nil {
		return DownloadTaskSummary{}, err
	}
	if s.metadata == nil {
		return DownloadTaskSummary{}, appError(CodeTMDBUnavailable, "TMDB 未配置", nil)
	}
	client, _, _, err := s.metadata.clientWithCredentialInfo()
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	language, _ := s.downloadRecognitionLocale(task)
	verified, err := client.GetByID(ctx, input.MediaType, input.TMDBID, language)
	if err != nil {
		return DownloadTaskSummary{}, appError(tmdb.ErrorCode(err), "TMDB 项目验证失败", nil)
	}
	if verified.ID != input.TMDBID || verified.MediaType != input.MediaType {
		return DownloadTaskSummary{}, appError(CodeInvalidRequest, "TMDB 项目身份不一致", nil)
	}
	manifest, _, manifestErr := completedDownloadManifest(task.CompletedManifestJSON)
	if manifestErr != nil {
		return DownloadTaskSummary{}, manifestErr
	}
	identityRevision := task.IdentityRevision + 1
	identityMatch := scrapeMatch{Title: verified.Title, MediaType: verified.MediaType, Category: task.ScrapeCategory, TMDBID: &verified.ID, Confidence: &verified.Confidence, Year: verified.ReleaseYear, Season: season, Episode: episode, IdentityStatus: mediaIdentityStatusVerified}
	_, identityJSON, identityErr := buildDownloadIdentitySnapshot(task, identityMatch, manifest, mediaIdentitySourceManual, mediaIdentityStatusVerified, true, identityRevision)
	if identityErr != nil {
		return DownloadTaskSummary{}, identityErr
	}
	now := time.Now().UTC()
	var queuedJob models.Job
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var lockedTask models.DownloadTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedTask, "id = ?", task.ID).Error; err != nil {
			return downloadTaskNotFound(err)
		}
		var lockedJob models.Job
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedJob, "id = ?", lockedTask.JobID).Error; err != nil {
			return queueNotFound(err)
		}
		if lockedJob.Status != models.JobStatusFailed || lockedTask.ScrapeStatus != "completed_unrecognized" {
			return appError(CodeQueueStateConflict, "该任务已不处于识别失败状态", nil)
		}
		if err := tx.Model(&lockedTask).Updates(map[string]any{
			"recognition_override_tmdb_id":    verified.ID,
			"recognition_override_media_type": verified.MediaType,
			"recognition_override_season":     season,
			"recognition_override_episode":    episode,
			"identity_source":                 mediaIdentitySourceManual,
			"identity_status":                 mediaIdentityStatusVerified,
			"identity_locked":                 true,
			"identity_revision":               identityRevision,
			"identity_snapshot_json":          identityJSON,
			"phase":                           models.DownloadTaskStatusVerifying,
			"last_error_code":                 "",
			"last_error_message":              "",
			"finished_at":                     nil,
			"updated_at":                      now,
		}).Error; err != nil {
			return err
		}
		from := lockedJob.Status
		if err := tx.Model(&lockedJob).Updates(map[string]any{
			"status":             models.JobStatusQueued,
			"revision":           lockedJob.Revision + 1,
			"next_attempt_at":    nil,
			"finished_at":        nil,
			"last_error_code":    "",
			"last_error_message": "",
			"updated_at":         now,
		}).Error; err != nil {
			return err
		}
		lockedJob.Status = models.JobStatusQueued
		lockedJob.Revision++
		lockedJob.NextAttemptAt = nil
		lockedJob.FinishedAt = nil
		lockedJob.LastErrorCode = ""
		lockedJob.LastErrorMessage = ""
		lockedJob.UpdatedAt = now
		if err := recordJobEvent(tx, lockedJob.ID, "control.retry", from, lockedJob.Status, &actor.User.ID, "", now); err != nil {
			return err
		}
		auditMetadata := map[string]any{"media_type": verified.MediaType, "tmdb_id": verified.ID}
		if season != nil {
			auditMetadata["season"] = *season
		}
		if episode != nil {
			auditMetadata["episode"] = *episode
		}
		if err := s.audit.Record(tx, &actor.User.ID, "download.recognition_override", "download_task", lockedTask.ID, "success", auditMetadata, request); err != nil {
			return err
		}
		if err := s.audit.Record(tx, &actor.User.ID, "jobs.retry", "job", lockedJob.ID, "success", map[string]any{"from": from, "to": lockedJob.Status}, request); err != nil {
			return err
		}
		queuedJob = lockedJob
		return nil
	})
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	s.queue.wake()
	s.queue.publish(queuedJob, "job.status_changed")
	task.RecognitionOverrideTMDBID = cloneInt64(&verified.ID)
	task.RecognitionOverrideMediaType = verified.MediaType
	task.RecognitionOverrideSeason = cloneInt(season)
	task.RecognitionOverrideEpisode = cloneInt(episode)
	task.IdentitySource, task.IdentityStatus, task.IdentityLocked = mediaIdentitySourceManual, mediaIdentityStatusVerified, true
	task.IdentityRevision, task.IdentitySnapshotJSON = identityRevision, identityJSON
	task.Phase = models.DownloadTaskStatusVerifying
	task.LastErrorCode, task.LastErrorMessage, task.FinishedAt = "", "", nil
	return downloadTaskSummary(task, models.JobStatusQueued), nil
}

func validateDownloadRecognitionEpisodeOverride(mediaType string, season, episode *int) (*int, *int, error) {
	if mediaType == "movie" {
		if season != nil || episode != nil {
			return nil, nil, appError(CodeInvalidRequest, "电影不能指定季数或集数", nil)
		}
		return nil, nil, nil
	}
	if season != nil && (*season < 0 || *season > 200) {
		return nil, nil, appError(CodeInvalidRequest, "季数必须在 0 到 200 之间", nil)
	}
	if episode != nil && (*episode < 1 || *episode > 100000) {
		return nil, nil, appError(CodeInvalidRequest, "集数必须在 1 到 100000 之间", nil)
	}
	normalizedSeason := cloneInt(season)
	if episode != nil && normalizedSeason == nil {
		defaultSeason := 1
		normalizedSeason = &defaultSeason
	}
	return normalizedSeason, cloneInt(episode), nil
}

func completedManifestVideoCount(manifest downloadpkg.Manifest) int {
	count := 0
	for _, file := range manifest.Files {
		if isVideoFile(file.RelativePath) {
			count++
		}
	}
	return count
}

func validateCompletedManifestEpisodeOverride(raw string, episode *int) error {
	if episode == nil {
		return nil
	}
	manifest, exists, err := completedDownloadManifest(raw)
	if err != nil {
		return err
	}
	if !exists {
		return appError(CodeInvalidRequest, "旧任务尚无完成文件清单，请先重新识别一次后再指定集数", nil)
	}
	if completedManifestVideoCount(manifest) != 1 {
		return appError(CodeInvalidRequest, "仅单集下载可以人工指定集数", nil)
	}
	return nil
}

func (s *DownloadService) downloadRecognitionRecoveryContext(actor Actor, id string, control bool) (models.DownloadTask, models.Job, error) {
	var task models.DownloadTask
	if err := s.db.First(&task, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return task, models.Job{}, downloadTaskNotFound(err)
	}
	if control {
		if !actor.Can(authz.PermissionJobsControlAll) && (task.OwnerID != actor.User.ID || !actor.Can(authz.PermissionJobsControlOwn)) {
			return task, models.Job{}, appError(CodePermissionDenied, "无权修正该下载任务", nil)
		}
	} else if !actor.Can(authz.PermissionDownloadsReadAll) && (task.OwnerID != actor.User.ID || !actor.Can(authz.PermissionDownloadsReadOwn)) {
		return task, models.Job{}, appError(CodePermissionDenied, "无权查看该下载任务", nil)
	}
	var job models.Job
	if err := s.db.First(&job, "id = ?", task.JobID).Error; err != nil {
		return task, job, queueNotFound(err)
	}
	_, snapshotExists, snapshotErr := completedDownloadManifest(task.CompletedManifestJSON)
	if snapshotErr != nil {
		return task, job, snapshotErr
	}
	if job.Status != models.JobStatusFailed || task.ScrapeStatus != "completed_unrecognized" || (!snapshotExists && task.ProviderTaskID == "") || task.TargetLibraryID == nil {
		return task, job, appError(CodeQueueStateConflict, "该任务不需要人工识别恢复", nil)
	}
	return task, job, nil
}

func (s *DownloadService) downloadRecognitionLocale(task models.DownloadTask) (string, string) {
	language, region := "zh-CN", "CN"
	if task.TargetLibraryID == nil {
		return language, region
	}
	var library models.MediaLibrary
	if err := s.db.Select("metadata_language", "metadata_region").First(&library, *task.TargetLibraryID).Error; err == nil {
		if strings.TrimSpace(library.MetadataLanguage) != "" {
			language = library.MetadataLanguage
		}
		if strings.TrimSpace(library.MetadataRegion) != "" {
			region = library.MetadataRegion
		}
	}
	return language, region
}
