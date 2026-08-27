package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FollowEpisodeCoordinate struct {
	Season  int `json:"season"`
	Episode int `json:"episode"`
}

type followCandidate struct {
	SiteID       uint
	SitePriority int
	Item         SiteSearchResult
	Season       int
	Episodes     []int
	Fingerprint  string
	QualityRank  int
}

type FollowSearchWorker struct {
	follows *FollowService
	sites   *SiteService
}

var followEpisodeRangePattern = regexp.MustCompile(`(?i)S(\d{1,3})E(\d{1,4})(?:[-~]E?|E)(\d{1,4})`)
var followCompleteSeasonPattern = regexp.MustCompile(`(?i)(?:complete(?:[ ._-]*(?:season|pack))?|season[ ._-]*pack|全集|全季)`)

func NewFollowSearchWorker(follows *FollowService, sites *SiteService) *FollowSearchWorker {
	return &FollowSearchWorker{follows: follows, sites: sites}
}

func (w *FollowSearchWorker) Run(ctx context.Context, runtime JobRuntime, job ClaimedJob) WorkerResult {
	var payload followJobPayload
	if err := json.Unmarshal([]byte(job.Job.PayloadJSON), &payload); err != nil || payload.RunID == "" || payload.SubscriptionID == "" {
		return WorkerResult{ErrorCode: CodeInvalidRequest, ErrorMessage: "追更任务参数无效"}
	}
	var run models.FollowRun
	if err := w.follows.db.First(&run, "id = ? AND job_id = ?", payload.RunID, job.Job.ID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			// Deleting a subscription cascades its run records but intentionally
			// leaves unrelated queue history intact. A later claim of that stale
			// Job is a safe no-op, not a retryable failure.
			return WorkerResult{}
		}
		return WorkerResult{ErrorCode: CodeFollowNotFound, ErrorMessage: "追更运行记录不存在"}
	}
	start := w.follows.now()
	if err := w.follows.db.Model(&run).Updates(map[string]any{"status": models.FollowRunRunning, "started_at": start, "updated_at": start}).Error; err != nil {
		return followPersistFailure()
	}
	w.follows.publish("follow.running", run.OwnerID, run.JobID, models.FollowRunRunning)
	var subscription models.FollowSubscription
	if err := w.follows.db.First(&subscription, "id = ?", payload.SubscriptionID).Error; err != nil {
		return w.stopRun(run, models.FollowRunStale, "follow_deleted", "订阅已删除")
	}
	if !followRunExecutable(subscription, run.LifecycleRevision) {
		return w.stopRun(run, models.FollowRunCancelled, "follow_paused", "订阅已暂停")
	}
	actor, err := w.follows.authorization.Resolve(run.OwnerID)
	if err != nil {
		return w.block(run, subscription, "follow_owner_unavailable", "订阅用户或权限不可用")
	}
	var snapshot FollowExecutionSnapshot
	if err := json.Unmarshal([]byte(run.ExecutionSnapshotJSON), &snapshot); err != nil {
		return w.block(run, subscription, CodeFollowConfigurationInvalid, "订阅执行快照无效")
	}
	if _, _, err := w.follows.validateSnapshot(actor, subscription.TMDBID, snapshot); err != nil {
		return w.block(run, subscription, CodeFollowConfigurationInvalid, ErrorMessage(err))
	}
	coverage, err := w.follows.coverage.Coverage(ctx, actor, "tv", subscription.TMDBID)
	if err != nil {
		return w.block(run, subscription, ErrorCode(err), ErrorMessage(err))
	}
	current, err := w.currentSubscription(subscription.ID)
	if err != nil || !followRunExecutable(current, run.LifecycleRevision) {
		return w.stopRun(run, models.FollowRunCancelled, "follow_inactive", "订阅已暂停或删除")
	}
	if coverage.TV == nil {
		return w.block(run, subscription, "follow_coverage_unknown", "媒体库覆盖率不可用")
	}
	selectedSeasons := map[int]struct{}{}
	for _, season := range snapshot.Seasons {
		selectedSeasons[season] = struct{}{}
	}
	missing := map[[2]int]struct{}{}
	seenSeasons := map[int]struct{}{}
	target, present, unknown := 0, 0, 0
	for _, season := range coverage.TV.Seasons {
		if _, ok := selectedSeasons[season.SeasonNumber]; !ok {
			continue
		}
		seenSeasons[season.SeasonNumber] = struct{}{}
		for _, episode := range season.Episodes {
			switch episode.Status {
			case "present":
				target++
				present++
				if err := w.upsertImported(subscription.ID, season.SeasonNumber, episode.EpisodeNumber); err != nil {
					return followPersistFailure()
				}
			case "missing":
				target++
				missing[[2]int{season.SeasonNumber, episode.EpisodeNumber}] = struct{}{}
			case "unknown":
				unknown++
			}
		}
		unknown += season.Counts.Unknown - countEpisodeStatus(season.Episodes, "unknown")
	}
	if len(seenSeasons) != len(selectedSeasons) {
		return w.block(run, subscription, "follow_coverage_unknown", "所选季的 TMDB 信息不可用")
	}
	missing, err = w.excludeClaimedDownloads(subscription.ID, missing)
	if err != nil {
		return followPersistFailure()
	}
	missingCoordinates := coordinateList(missing)
	if len(missingCoordinates) > 0 {
		w.follows.publish("follow.missing_found", run.OwnerID, run.JobID, models.FollowRunRunning)
	}
	missingRaw, _ := json.Marshal(missingCoordinates)
	now := w.follows.now()
	if err := w.follows.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.FollowRun{}).Where("id = ?", run.ID).Updates(map[string]any{"missing_snapshot_json": string(missingRaw), "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&models.FollowSubscription{}).
			Where("id = ? AND revision = ? AND lifecycle_revision = ? AND status <> ?", subscription.ID, run.SubscriptionRevision, run.LifecycleRevision, models.FollowStatusPaused).
			Updates(map[string]any{"progress_target": target, "progress_present": present, "progress_missing": target - present, "last_run_id": run.ID, "last_run_at": now, "updated_at": now}).Error
	}); err != nil {
		return followPersistFailure()
	}
	if unknown > 0 {
		return w.block(run, subscription, "follow_coverage_unknown", "所选季存在无法确认播出或缺失状态的剧集")
	}
	if len(missing) == 0 {
		if target > present {
			return w.finish(run, subscription, models.FollowRunSubmitted, models.FollowStatusActive, "", "", snapshot, 0, 0, map[string]int{"active_claims": target - present})
		}
		return w.finish(run, subscription, models.FollowRunCompleted, models.FollowStatusCompleted, "", "", snapshot, 0, 0, map[string]int{})
	}
	progress := float64(20)
	processed, total := int64(1), int64(4)
	_ = runtime.Heartbeat(&progress, &processed, &total, nil, nil)
	_ = runtime.Checkpoint(map[string]any{"stage": "search", "missing": missingCoordinates})
	candidates, queryNames, filterSummary := w.searchCandidates(ctx, actor, subscription, snapshot, missing)
	if ctx.Err() != nil {
		return w.stopRun(run, models.FollowRunCancelled, "follow_cancelled", "追更任务已取消")
	}
	selected := selectFollowCandidates(candidates, missing, snapshot.MaxResourcesPerRun)
	if len(selected) == 0 {
		return w.finish(run, subscription, models.FollowRunNoMatch, models.FollowStatusActive, "", "", snapshot, queryNames, len(candidates), filterSummary)
	}
	progress = 70
	processed = 3
	_ = runtime.Heartbeat(&progress, &processed, &total, nil, nil)
	_ = runtime.Checkpoint(map[string]any{"stage": "submit", "missing": missingCoordinates, "selected": candidateFingerprints(selected)})
	submitted := 0
	for _, candidate := range selected {
		if ctx.Err() != nil {
			return w.stopRun(run, models.FollowRunCancelled, "follow_cancelled", "追更任务已取消")
		}
		current, err := w.currentSubscription(subscription.ID)
		if err != nil || !followRunExecutable(current, run.LifecycleRevision) {
			return w.stopRun(run, models.FollowRunCancelled, "follow_inactive", "订阅已暂停或删除")
		}
		episodes := candidateMissingEpisodes(candidate, missing)
		if len(episodes) == 0 {
			continue
		}
		claimed, err := w.reserveEpisodes(run, subscription.ID, candidate, episodes)
		if err != nil || !claimed {
			continue
		}
		season := candidate.Season
		var singleEpisode *int
		if len(episodes) == 1 {
			value := episodes[0]
			singleEpisode = &value
		}
		libraryID := snapshot.MediaLibraryID
		download, err := w.sites.Download(ctx, actor, SiteDownloadInput{ResultToken: candidate.Item.Token, DownloaderID: snapshot.DownloaderID, MediaLibraryID: &libraryID, Priority: snapshot.DownloadPriority, FollowSubscriptionID: subscription.ID, FollowResourceFingerprint: candidate.Fingerprint, Season: &season, Episode: singleEpisode, BeforeSubmit: func() error {
			current, currentErr := w.currentSubscription(subscription.ID)
			if currentErr != nil || !followRunExecutable(current, run.LifecycleRevision) {
				return appError(CodeConflict, "订阅已暂停或删除", currentErr)
			}
			return nil
		}, BeforePersist: func(tx *gorm.DB) error {
			current, currentErr := w.follows.LockCurrentSubscription(tx, subscription.ID)
			if currentErr != nil || !followRunExecutable(current, run.LifecycleRevision) {
				return appError(CodeConflict, "订阅已暂停或删除", currentErr)
			}
			return nil
		}}, RequestContext{})
		if err != nil {
			w.releaseEpisodes(subscription.ID, run.ID, candidate.Fingerprint)
			current, currentErr := w.currentSubscription(subscription.ID)
			if currentErr != nil || !followRunExecutable(current, run.LifecycleRevision) {
				return w.stopRun(run, models.FollowRunCancelled, "follow_inactive", "订阅已暂停或删除")
			}
			if ctx.Err() != nil {
				return w.stopRun(run, models.FollowRunCancelled, "follow_cancelled", "追更任务已取消")
			}
			if isFollowConfigurationError(ErrorCode(err)) {
				return w.block(run, subscription, CodeFollowConfigurationInvalid, "订阅下载器或目标媒体库配置不可用")
			}
			filterSummary["submit_failed"]++
			continue
		}
		if err := w.attachDownload(subscription.ID, run.ID, candidate.Fingerprint, download.ID); err != nil {
			return followPersistFailure()
		}
		w.follows.publish("follow.download_submitted", run.OwnerID, run.JobID, models.FollowRunSubmitted)
		submitted++
	}
	if submitted == 0 {
		return w.finish(run, subscription, models.FollowRunNoMatch, models.FollowStatusActive, "", "", snapshot, queryNames, len(candidates), filterSummary)
	}
	progress = 100
	processed = 4
	_ = runtime.Heartbeat(&progress, &processed, &total, nil, nil)
	return w.finish(run, subscription, models.FollowRunSubmitted, models.FollowStatusActive, "", "", snapshot, queryNames, len(candidates), filterSummary, submitted)
}

func (w *FollowSearchWorker) searchCandidates(ctx context.Context, actor Actor, subscription models.FollowSubscription, snapshot FollowExecutionSnapshot, missing map[[2]int]struct{}) ([]followCandidate, int, map[string]int) {
	result := []followCandidate{}
	summary := map[string]int{}
	queryNames := 0
	seen := map[string]struct{}{}
	for sitePriority, siteID := range snapshot.SiteIDs {
		for _, season := range snapshot.Seasons {
			hasMissing := false
			for key := range missing {
				if key[0] == season {
					hasMissing = true
					break
				}
			}
			if !hasMissing {
				continue
			}
			grouped, err := w.sites.SearchMediaIdentity(ctx, actor, MediaIdentitySearchInput{MediaType: "tv", TMDBID: subscription.TMDBID, Season: &season, Page: 1, SiteID: &siteID})
			if err != nil {
				summary["site_error"]++
				continue
			}
			if queryNames == 0 {
				queryNames = len(grouped.QueryNames)
			}
			for _, group := range grouped.Groups {
				if group.ErrorCount > 0 {
					summary["site_error"] += group.ErrorCount
				}
				if group.Status != "success" {
					if group.ErrorCount == 0 {
						summary["site_error"]++
					}
					continue
				}
				for _, item := range group.Items {
					candidate, reason, ok := buildFollowCandidate(item, group.SiteID, sitePriority, season, snapshot)
					if !ok {
						summary[reason]++
						continue
					}
					if _, duplicate := seen[candidate.Fingerprint]; duplicate {
						summary["duplicate"]++
						continue
					}
					seen[candidate.Fingerprint] = struct{}{}
					covers := false
					for _, episode := range candidate.Episodes {
						if _, ok := missing[[2]int{season, episode}]; ok {
							covers = true
							break
						}
					}
					if !covers {
						summary["no_missing_coverage"]++
						continue
					}
					result = append(result, candidate)
				}
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return followCandidateLess(result[i], result[j]) })
	return result, queryNames, summary
}

func buildFollowCandidate(item SiteSearchResult, siteID uint, sitePriority, season int, snapshot FollowExecutionSnapshot) (followCandidate, string, bool) {
	parsed, err := mediarecognition.Parse(mediarecognition.InputFacts{PackageName: item.Title, SourceKind: mediarecognition.SourceDownload, MediaTypeHint: mediarecognition.MediaTypeTV, SeasonHint: &season})
	if err != nil || parsed.Season == nil || *parsed.Season != season {
		return followCandidate{}, "episode_unproven", false
	}
	min, max := parsed.Episodes.EpisodeMin, parsed.Episodes.EpisodeMax
	if match := followEpisodeRangePattern.FindStringSubmatch(item.Title); len(match) == 4 {
		matchedSeason, seasonErr := strconv.Atoi(match[1])
		episodeMin, minErr := strconv.Atoi(match[2])
		episodeMax, maxErr := strconv.Atoi(match[3])
		if seasonErr == nil && minErr == nil && maxErr == nil && matchedSeason == season {
			min, max = &episodeMin, &episodeMax
		}
	} else if followCompleteSeasonPattern.MatchString(item.Title) {
		episodeMin, episodeMax := 1, 200
		min, max = &episodeMin, &episodeMax
	}
	if min == nil || max == nil || *min < 1 || *max < *min || *max-*min > 200 {
		return followCandidate{}, "episode_unproven", false
	}
	episodes := make([]int, 0, *max-*min+1)
	for value := *min; value <= *max; value++ {
		episodes = append(episodes, value)
	}
	text := strings.ToLower(item.Title + " " + item.Subtitle + " " + strings.Join(item.Tags, " "))
	spec := item.Specifications
	if !containsEvery(text, snapshot.Filters.IncludeKeywords) {
		return followCandidate{}, "include_keyword", false
	}
	if containsAny(text, snapshot.Filters.ExcludeKeywords) {
		return followCandidate{}, "exclude_keyword", false
	}
	if len(snapshot.Filters.Resolutions) > 0 && !matchesAllowed(spec.Resolution, snapshot.Filters.Resolutions) {
		return followCandidate{}, "resolution", false
	}
	if len(snapshot.Filters.VideoCodecs) > 0 && !matchesAllowed(spec.VideoCodec, snapshot.Filters.VideoCodecs) {
		return followCandidate{}, "video_codec", false
	}
	if len(snapshot.Filters.Qualities) > 0 && !matchesAnyAllowed([]string{item.Quality, spec.Source}, snapshot.Filters.Qualities) {
		return followCandidate{}, "quality", false
	}
	if len(snapshot.Filters.ReleaseGroups) > 0 && !matchesAllowed(spec.ReleaseGroup, snapshot.Filters.ReleaseGroups) {
		return followCandidate{}, "release_group", false
	}
	if matchesAllowed(spec.ReleaseGroup, snapshot.Filters.ExcludeReleaseGroups) {
		return followCandidate{}, "excluded_release_group", false
	}
	if snapshot.Filters.MinSeeders > 0 && (item.Seeders == nil || *item.Seeders < snapshot.Filters.MinSeeders) {
		return followCandidate{}, "seeders", false
	}
	if (snapshot.Filters.MinSizeBytes != nil || snapshot.Filters.MaxSizeBytes != nil) && item.SizeBytes <= 0 {
		return followCandidate{}, "size", false
	}
	if snapshot.Filters.MinSizeBytes != nil && item.SizeBytes < *snapshot.Filters.MinSizeBytes {
		return followCandidate{}, "size", false
	}
	if snapshot.Filters.MaxSizeBytes != nil && item.SizeBytes > *snapshot.Filters.MaxSizeBytes {
		return followCandidate{}, "size", false
	}
	if snapshot.Filters.MaxAgeHours != nil && (item.Published == nil || time.Since(*item.Published) > time.Duration(*snapshot.Filters.MaxAgeHours)*time.Hour) {
		return followCandidate{}, "age", false
	}
	fingerprint := item.ResourceFingerprint
	if fingerprint == "" {
		fingerprint = followFingerprint(siteID, item.Title, item.SizeBytes, item.Published)
	}
	return followCandidate{SiteID: siteID, SitePriority: sitePriority, Item: item, Season: season, Episodes: episodes, Fingerprint: fingerprint, QualityRank: qualityPreferenceRank(item, snapshot)}, "", true
}

func selectFollowCandidates(candidates []followCandidate, missing map[[2]int]struct{}, limit int) []followCandidate {
	remaining := map[[2]int]struct{}{}
	for key := range missing {
		remaining[key] = struct{}{}
	}
	pool := append([]followCandidate(nil), candidates...)
	selected := []followCandidate{}
	for len(remaining) > 0 && len(selected) < limit {
		best := -1
		bestCoverage := 0
		bestExcess := 0
		for index, candidate := range pool {
			coverage := 0
			for _, episode := range candidate.Episodes {
				if _, ok := remaining[[2]int{candidate.Season, episode}]; ok {
					coverage++
				}
			}
			excess := len(candidate.Episodes) - coverage
			if coverage > bestCoverage || (coverage == bestCoverage && coverage > 0 && (best < 0 || excess < bestExcess || excess == bestExcess && followCandidateLess(candidate, pool[best]))) {
				best, bestCoverage, bestExcess = index, coverage, excess
			}
		}
		if best < 0 || bestCoverage == 0 {
			break
		}
		chosen := pool[best]
		selected = append(selected, chosen)
		for _, episode := range chosen.Episodes {
			delete(remaining, [2]int{chosen.Season, episode})
		}
		pool = append(pool[:best], pool[best+1:]...)
	}
	return selected
}

func followCandidateLess(left, right followCandidate) bool {
	if left.SitePriority != right.SitePriority {
		return left.SitePriority < right.SitePriority
	}
	if left.QualityRank != right.QualityRank {
		return left.QualityRank < right.QualityRank
	}
	ls, rs := -1, -1
	if left.Item.Seeders != nil {
		ls = *left.Item.Seeders
	}
	if right.Item.Seeders != nil {
		rs = *right.Item.Seeders
	}
	if ls != rs {
		return ls > rs
	}
	if compared := compareOptionalTime(left.Item.Published, right.Item.Published); compared != 0 {
		return compared > 0
	}
	return left.Fingerprint < right.Fingerprint
}

func (w *FollowSearchWorker) reserveEpisodes(run models.FollowRun, subscriptionID string, candidate followCandidate, episodes []int) (bool, error) {
	now := w.follows.now()
	reserved := false
	err := w.follows.db.Transaction(func(tx *gorm.DB) error {
		record, err := w.follows.LockCurrentSubscription(tx, subscriptionID)
		if err != nil {
			return err
		}
		if !followRunExecutable(record, run.LifecycleRevision) {
			return appError(CodeConflict, "订阅已暂停", nil)
		}
		existing := make(map[int]models.FollowEpisodeClaim, len(episodes))
		for _, episode := range episodes {
			var claim models.FollowEpisodeClaim
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&claim, "subscription_id = ? AND season_number = ? AND episode_number = ?", subscriptionID, candidate.Season, episode).Error
			if err == nil && claim.DownloadTaskID != nil {
				return nil
			}
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}
			if err == nil {
				existing[episode] = claim
			}
		}
		for _, episode := range episodes {
			_, exists := existing[episode]
			claim := models.FollowEpisodeClaim{SubscriptionID: subscriptionID, SeasonNumber: candidate.Season, EpisodeNumber: episode, State: "queued", RunID: &run.ID, ResourceFingerprint: candidate.Fingerprint, UpdatedAt: now}
			if !exists {
				if err := tx.Create(&claim).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Model(&models.FollowEpisodeClaim{}).Where("subscription_id = ? AND season_number = ? AND episode_number = ?", subscriptionID, candidate.Season, episode).Updates(map[string]any{"state": "queued", "run_id": run.ID, "download_task_id": nil, "resource_fingerprint": candidate.Fingerprint, "updated_at": now}).Error; err != nil {
					return err
				}
			}
			reserved = true
		}
		return nil
	})
	return reserved, err
}
func (w *FollowSearchWorker) releaseEpisodes(subscriptionID, runID, fingerprint string) {
	_ = w.follows.db.Model(&models.FollowEpisodeClaim{}).Where("subscription_id = ? AND run_id = ? AND resource_fingerprint = ? AND download_task_id IS NULL", subscriptionID, runID, fingerprint).Updates(map[string]any{"state": "failed", "updated_at": w.follows.now()}).Error
}
func (w *FollowSearchWorker) attachDownload(subscriptionID, runID, fingerprint, downloadID string) error {
	return w.follows.db.Model(&models.FollowEpisodeClaim{}).Where("subscription_id = ? AND run_id = ? AND resource_fingerprint = ?", subscriptionID, runID, fingerprint).Updates(map[string]any{"state": "downloading", "download_task_id": downloadID, "updated_at": w.follows.now()}).Error
}
func (w *FollowSearchWorker) upsertImported(subscriptionID string, season, episode int) error {
	now := w.follows.now()
	return w.follows.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "subscription_id"}, {Name: "season_number"}, {Name: "episode_number"}}, DoUpdates: clause.Assignments(map[string]any{"state": "imported", "updated_at": now})}).Create(&models.FollowEpisodeClaim{SubscriptionID: subscriptionID, SeasonNumber: season, EpisodeNumber: episode, State: "imported", UpdatedAt: now}).Error
}

func (w *FollowSearchWorker) excludeClaimedDownloads(subscriptionID string, missing map[[2]int]struct{}) (map[[2]int]struct{}, error) {
	var claims []models.FollowEpisodeClaim
	if err := w.follows.db.Where("subscription_id = ? AND download_task_id IS NOT NULL", subscriptionID).Find(&claims).Error; err != nil {
		return nil, err
	}
	for _, claim := range claims {
		var task models.DownloadTask
		if err := w.follows.db.First(&task, "id = ?", *claim.DownloadTaskID).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				return nil, err
			}
			continue
		}
		if task.Phase != models.DownloadTaskStatusFailed && task.Phase != models.DownloadTaskStatusCancelled {
			delete(missing, [2]int{claim.SeasonNumber, claim.EpisodeNumber})
		} else {
			if err := w.follows.db.Model(&claim).Updates(map[string]any{"state": "failed", "download_task_id": nil, "updated_at": w.follows.now()}).Error; err != nil {
				return nil, err
			}
		}
	}
	return missing, nil
}

func (w *FollowSearchWorker) currentSubscription(id string) (models.FollowSubscription, error) {
	var record models.FollowSubscription
	err := w.follows.db.First(&record, "id = ?", id).Error
	return record, err
}
func (w *FollowSearchWorker) block(run models.FollowRun, subscription models.FollowSubscription, code, message string) WorkerResult {
	return w.finish(run, subscription, models.FollowRunFailed, models.FollowStatusBlocked, code, message, FollowExecutionSnapshot{}, 0, 0, map[string]int{})
}
func (w *FollowSearchWorker) stopRun(run models.FollowRun, status, code, message string) WorkerResult {
	now := w.follows.now()
	if err := w.follows.db.Model(&run).Updates(map[string]any{"status": status, "error_code": code, "error_message": message, "finished_at": now, "updated_at": now}).Error; err != nil {
		return followPersistFailure()
	}
	w.follows.publish("follow.cancelled", run.OwnerID, run.JobID, status)
	return WorkerResult{}
}
func (w *FollowSearchWorker) finish(run models.FollowRun, subscription models.FollowSubscription, runStatus, subscriptionStatus, code, message string, snapshot FollowExecutionSnapshot, queryNames, candidates int, summary map[string]int, selected ...int) WorkerResult {
	now := w.follows.now()
	next := now.Add(time.Duration(snapshot.Schedule.Minutes) * time.Minute)
	if snapshot.Schedule.Minutes == 0 {
		var stored FollowExecutionSnapshot
		if json.Unmarshal([]byte(run.ExecutionSnapshotJSON), &stored) == nil {
			next = now.Add(time.Duration(stored.Schedule.Minutes) * time.Minute)
		}
	}
	summaryRaw, _ := json.Marshal(summary)
	selectedCount := 0
	if len(selected) > 0 {
		selectedCount = selected[0]
	}
	err := w.follows.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.FollowRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": runStatus, "searched_names_count": queryNames, "candidates": candidates, "selected": selectedCount, "filter_summary_json": string(summaryRaw), "error_code": code, "error_message": message, "finished_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.FollowSubscription{}).Where("id = ?", subscription.ID).Updates(map[string]any{"last_run_id": run.ID, "last_run_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&models.FollowSubscription{}).
			Where("id = ? AND revision = ? AND lifecycle_revision = ? AND status <> ?", subscription.ID, run.SubscriptionRevision, run.LifecycleRevision, models.FollowStatusPaused).
			Updates(map[string]any{"status": subscriptionStatus, "next_run_at": next, "last_error_code": code, "last_error_message": message, "updated_at": now}).Error
	})
	if err != nil {
		return followPersistFailure()
	}
	eventType := map[string]string{models.FollowRunNoMatch: "follow.no_match", models.FollowRunCompleted: "follow.completed", models.FollowRunFailed: "follow.blocked"}[runStatus]
	if eventType != "" {
		w.follows.publish(eventType, run.OwnerID, run.JobID, runStatus)
	}
	return WorkerResult{}
}

func coordinateList(missing map[[2]int]struct{}) []FollowEpisodeCoordinate {
	result := make([]FollowEpisodeCoordinate, 0, len(missing))
	for key := range missing {
		result = append(result, FollowEpisodeCoordinate{Season: key[0], Episode: key[1]})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Season != result[j].Season {
			return result[i].Season < result[j].Season
		}
		return result[i].Episode < result[j].Episode
	})
	return result
}
func countEpisodeStatus(items []MediaCoverageEpisode, status string) int {
	count := 0
	for _, item := range items {
		if item.Status == status {
			count++
		}
	}
	return count
}
func containsEvery(text string, values []string) bool {
	for _, value := range values {
		if !strings.Contains(text, strings.ToLower(value)) {
			return false
		}
	}
	return true
}
func containsAny(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, strings.ToLower(value)) {
			return true
		}
	}
	return false
}
func matchesAllowed(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	for _, item := range allowed {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "" {
			continue
		}
		if value == normalized || strings.Contains(value, normalized) || strings.Contains(normalized, value) {
			return true
		}
	}
	return false
}
func matchesAnyAllowed(values, allowed []string) bool {
	for _, value := range values {
		if matchesAllowed(value, allowed) {
			return true
		}
	}
	return false
}
func qualityPreferenceRank(item SiteSearchResult, snapshot FollowExecutionSnapshot) int {
	preference := func(values []string, actual ...string) int {
		if len(values) == 0 {
			return 0
		}
		for index, value := range values {
			if matchesAnyAllowed(actual, []string{value}) {
				return index
			}
		}
		return len(values)
	}
	resolution := preference(snapshot.Filters.Resolutions, item.Specifications.Resolution)
	codec := preference(snapshot.Filters.VideoCodecs, item.Specifications.VideoCodec)
	quality := preference(snapshot.Filters.Qualities, item.Quality, item.Specifications.Source)
	return resolution*1024 + codec*32 + quality
}
func followFingerprint(siteID uint, title string, size int64, published *time.Time) string {
	stamp := ""
	if published != nil {
		stamp = published.UTC().Format(time.RFC3339Nano)
	}
	sum := sha256.Sum256([]byte(strconv.FormatUint(uint64(siteID), 10) + "\x00" + strings.ToLower(strings.Join(strings.Fields(title), " ")) + "\x00" + strconv.FormatInt(size, 10) + "\x00" + stamp))
	return hex.EncodeToString(sum[:])
}
func candidateMissingEpisodes(candidate followCandidate, missing map[[2]int]struct{}) []int {
	result := []int{}
	for _, episode := range candidate.Episodes {
		if _, ok := missing[[2]int{candidate.Season, episode}]; ok {
			result = append(result, episode)
		}
	}
	return result
}
func candidateFingerprints(candidates []followCandidate) []string {
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		result = append(result, candidate.Fingerprint)
	}
	return result
}

func followRunExecutable(subscription models.FollowSubscription, lifecycleRevision uint64) bool {
	return subscription.Status != models.FollowStatusPaused && subscription.LifecycleRevision == lifecycleRevision
}

func isFollowConfigurationError(code string) bool {
	switch code {
	case CodePermissionDenied, CodeDownloaderUnavailable, CodeDownloaderStorageRequired, CodeDownloaderStorageUnavailable, CodeDownloadStagingRequired, CodeDownloadStagingUnavailable, CodeMediaLibraryStorageUnavailable, CodeMediaLibraryProfileUnavailable, CodeMediaLibraryPathInvalid:
		return true
	default:
		return false
	}
}

func followPersistFailure() WorkerResult {
	return WorkerResult{ErrorCode: "follow_state_persist_failed", ErrorMessage: "追更状态保存失败"}
}
