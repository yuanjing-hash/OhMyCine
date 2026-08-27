package services

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	JobTypeFollowSearch            = "follow-search"
	CodeFollowNotFound             = "follow_not_found"
	CodeFollowConflict             = "follow_season_conflict"
	CodeFollowRevisionConflict     = "follow_revision_conflict"
	CodeFollowConfigurationInvalid = "follow_configuration_invalid"
)

type FollowSchedule struct {
	Kind    string `json:"kind"`
	Minutes int    `json:"minutes"`
}

type FollowFilters struct {
	Resolutions          []string `json:"resolutions"`
	VideoCodecs          []string `json:"video_codecs"`
	Qualities            []string `json:"qualities"`
	IncludeKeywords      []string `json:"include_keywords"`
	ExcludeKeywords      []string `json:"exclude_keywords"`
	ReleaseGroups        []string `json:"release_groups"`
	ExcludeReleaseGroups []string `json:"exclude_release_groups"`
	MinSeeders           int      `json:"min_seeders"`
	MaxAgeHours          *int     `json:"max_age_hours"`
	MinSizeBytes         *int64   `json:"min_size_bytes"`
	MaxSizeBytes         *int64   `json:"max_size_bytes"`
}

type FollowExecutionSnapshot struct {
	Version            int            `json:"version"`
	Seasons            []int          `json:"seasons"`
	SiteIDs            []uint         `json:"site_ids"`
	DownloaderID       string         `json:"downloader_id"`
	MediaLibraryID     uint           `json:"media_library_id"`
	Schedule           FollowSchedule `json:"schedule"`
	Filters            FollowFilters  `json:"filters"`
	MaxResourcesPerRun int            `json:"max_resources_per_run"`
	DownloadPriority   int            `json:"download_priority"`
}

type FollowOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type FollowSiteOption struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}
type FollowLibraryOption struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type FollowDefaults struct {
	Snapshot          FollowExecutionSnapshot `json:"snapshot"`
	Sites             []FollowSiteOption      `json:"sites"`
	Downloaders       []FollowOption          `json:"downloaders"`
	MediaLibraries    []FollowLibraryOption   `json:"media_libraries"`
	SubscribedSeasons []int                   `json:"subscribed_seasons"`
	Coverage          MediaCoverage           `json:"coverage"`
}

type CreateFollowInput struct {
	TMDBID    int64                   `json:"tmdb_id"`
	Title     string                  `json:"title"`
	Year      *int                    `json:"year,omitempty"`
	PosterRef string                  `json:"poster_ref,omitempty"`
	Snapshot  FollowExecutionSnapshot `json:"snapshot"`
}

type UpdateFollowInput struct {
	Revision uint64                  `json:"revision"`
	Snapshot FollowExecutionSnapshot `json:"snapshot"`
}

type FollowSummary struct {
	ID               string                  `json:"id"`
	OwnerID          uint                    `json:"owner_id"`
	MediaType        string                  `json:"media_type"`
	TMDBID           int64                   `json:"tmdb_id"`
	Title            string                  `json:"title"`
	Year             *int                    `json:"year,omitempty"`
	PosterRef        string                  `json:"poster_ref,omitempty"`
	Status           string                  `json:"status"`
	Revision         uint64                  `json:"revision"`
	Snapshot         FollowExecutionSnapshot `json:"snapshot"`
	ProgressTarget   int                     `json:"progress_target"`
	ProgressPresent  int                     `json:"progress_present"`
	ProgressMissing  int                     `json:"progress_missing"`
	LastRunID        *string                 `json:"last_run_id,omitempty"`
	LastRunAt        *time.Time              `json:"last_run_at,omitempty"`
	NextRunAt        *time.Time              `json:"next_run_at,omitempty"`
	LastErrorCode    string                  `json:"last_error_code"`
	LastErrorMessage string                  `json:"last_error_message"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

type FollowPage struct {
	List     []FollowSummary `json:"list"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}
type FollowRunSummary struct {
	ID                   string         `json:"id"`
	JobID                string         `json:"job_id"`
	Trigger              string         `json:"trigger"`
	Status               string         `json:"status"`
	SubscriptionRevision uint64         `json:"subscription_revision"`
	SearchedNamesCount   int            `json:"searched_names_count"`
	Candidates           int            `json:"candidates"`
	Selected             int            `json:"selected"`
	FilterSummary        map[string]int `json:"filter_summary"`
	ErrorCode            string         `json:"error_code"`
	ErrorMessage         string         `json:"error_message"`
	StartedAt            *time.Time     `json:"started_at,omitempty"`
	FinishedAt           *time.Time     `json:"finished_at,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
}

type followJobPayload struct {
	RunID                string `json:"run_id"`
	SubscriptionID       string `json:"subscription_id"`
	SubscriptionRevision uint64 `json:"subscription_revision"`
	Trigger              string `json:"trigger"`
}

type FollowService struct {
	db            *gorm.DB
	audit         *AuditService
	queue         *QueueService
	coverage      *MediaCoverageService
	authorization *AuthorizationService
	now           func() time.Time
	cancel        context.CancelFunc
	wg            sync.WaitGroup
}

func NewFollowService(db *gorm.DB, audit *AuditService, queue *QueueService, coverage *MediaCoverageService, authorization *AuthorizationService) *FollowService {
	return &FollowService{db: db, audit: audit, queue: queue, coverage: coverage, authorization: authorization, now: func() time.Time { return time.Now().UTC() }}
}

func (s *FollowService) Start(parent context.Context) error {
	if s.cancel != nil {
		return errors.New("follow scheduler already started")
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		_ = s.EnqueueDue(ctx, 100)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.EnqueueDue(ctx, 100)
			}
		}
	}()
	return nil
}
func (s *FollowService) Close() {
	if s.cancel != nil {
		s.cancel()
		s.wg.Wait()
		s.cancel = nil
	}
}

func (s *FollowService) publish(eventType string, ownerID uint, jobID, status string) {
	if s.queue != nil && s.queue.events != nil {
		owner := ownerID
		s.queue.events.Publish(JobEvent{Type: eventType, JobID: jobID, JobType: JobTypeFollowSearch, OwnerID: &owner, Status: status, At: s.now()})
	}
}

func (s *FollowService) Defaults(ctx context.Context, actor Actor, tmdbID int64) (FollowDefaults, error) {
	canConfigure := actor.Can(authz.PermissionFollowsCreate) || actor.Can(authz.PermissionFollowsUpdateOwn) || actor.Can(authz.PermissionFollowsUpdateAll)
	if !canConfigure || !actor.Can(authz.PermissionDownloadsCreate) {
		return FollowDefaults{}, appError(CodePermissionDenied, "无权创建订阅", nil)
	}
	coverage, err := s.coverage.Coverage(ctx, actor, "tv", tmdbID)
	if err != nil {
		return FollowDefaults{}, err
	}
	var sites []models.Site
	if err := s.db.Where("enabled = ?", true).Order("priority,id").Find(&sites).Error; err != nil {
		return FollowDefaults{}, err
	}
	var downloaders []models.Downloader
	if err := s.db.Where("enabled = ?", true).Order("created_at,id").Find(&downloaders).Error; err != nil {
		return FollowDefaults{}, err
	}
	var libraries []models.MediaLibrary
	if err := s.db.Where("enabled = ?", true).Order("sort_order,id").Find(&libraries).Error; err != nil {
		return FollowDefaults{}, err
	}
	result := FollowDefaults{Coverage: coverage, Sites: []FollowSiteOption{}, Downloaders: []FollowOption{}, MediaLibraries: []FollowLibraryOption{}, SubscribedSeasons: []int{}}
	if err := s.db.Model(&models.FollowSubscriptionSeason{}).Where("owner_id = ? AND tmdb_id = ?", actor.User.ID, tmdbID).Order("season_number").Pluck("season_number", &result.SubscribedSeasons).Error; err != nil {
		return FollowDefaults{}, err
	}
	for _, item := range sites {
		result.Sites = append(result.Sites, FollowSiteOption{ID: item.ID, Name: item.Name})
		result.Snapshot.SiteIDs = append(result.Snapshot.SiteIDs, item.ID)
	}
	for _, item := range downloaders {
		result.Downloaders = append(result.Downloaders, FollowOption{ID: item.ID, Name: item.Name})
	}
	for _, item := range libraries {
		result.MediaLibraries = append(result.MediaLibraries, FollowLibraryOption{ID: item.ID, Name: item.Name})
	}
	result.Snapshot.Version = 1
	result.Snapshot.Schedule = FollowSchedule{Kind: "interval", Minutes: 360}
	result.Snapshot.MaxResourcesPerRun = 3
	result.Snapshot.Filters = FollowFilters{Resolutions: []string{}, VideoCodecs: []string{}, Qualities: []string{}, IncludeKeywords: []string{}, ExcludeKeywords: []string{}, ReleaseGroups: []string{}, ExcludeReleaseGroups: []string{}, MinSeeders: 1}
	if len(downloaders) > 0 {
		result.Snapshot.DownloaderID = downloaders[0].ID
	}
	if len(libraries) > 0 {
		result.Snapshot.MediaLibraryID = libraries[0].ID
	}
	return result, nil
}

func (s *FollowService) Create(ctx context.Context, actor Actor, input CreateFollowInput, request RequestContext) (FollowSummary, error) {
	if !actor.Can(authz.PermissionFollowsCreate) || !actor.Can(authz.PermissionDownloadsCreate) {
		return FollowSummary{}, appError(CodePermissionDenied, "无权创建订阅", nil)
	}
	if input.TMDBID <= 0 {
		return FollowSummary{}, appError(CodeInvalidRequest, "订阅媒体身份无效", nil)
	}
	coverage, err := s.coverage.Coverage(ctx, actor, "tv", input.TMDBID)
	if err != nil {
		return FollowSummary{}, err
	}
	snapshot, raw, err := s.validateSnapshot(actor, input.TMDBID, input.Snapshot)
	if err != nil {
		return FollowSummary{}, err
	}
	if err := validateFollowSeasons(coverage, snapshot.Seasons); err != nil {
		return FollowSummary{}, err
	}
	title := strings.Join(strings.Fields(input.Title), " ")
	if title == "" {
		title = coverage.Title
	}
	if len([]rune(title)) > 256 {
		return FollowSummary{}, appError(CodeInvalidRequest, "订阅标题过长", nil)
	}
	if err := validatePublicText(title); err != nil {
		return FollowSummary{}, appError(CodeInvalidRequest, "订阅标题包含不安全内容", err)
	}
	poster := strings.TrimSpace(input.PosterRef)
	if poster != "" && (!strings.HasPrefix(poster, "/api/v1/discovery/images/") || len(poster) > 1024) {
		return FollowSummary{}, appError(CodeInvalidRequest, "订阅海报引用无效", nil)
	}
	now := s.now()
	next := now
	record := models.FollowSubscription{ID: uuid.NewString(), OwnerID: actor.User.ID, MediaType: "tv", TMDBID: input.TMDBID, Title: title, Year: input.Year, PosterRef: poster, Status: models.FollowStatusActive, Revision: 1, ExecutionSnapshotJSON: string(raw), NextRunAt: &next, CreatedAt: now, UpdatedAt: now}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return followConstraintError(err)
		}
		for _, season := range snapshot.Seasons {
			if err := tx.Create(&models.FollowSubscriptionSeason{SubscriptionID: record.ID, OwnerID: record.OwnerID, TMDBID: record.TMDBID, SeasonNumber: season, Special: season == 0}).Error; err != nil {
				return followConstraintError(err)
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "follow.create", "follow", record.ID, "success", map[string]any{"revision": 1, "season_count": len(snapshot.Seasons)}, request)
	})
	if err != nil {
		return FollowSummary{}, err
	}
	_, _ = s.enqueueRecord(ctx, record, "created")
	return s.getSummary(record.ID)
}

func (s *FollowService) List(actor Actor, page, pageSize int, status string) (FollowPage, error) {
	if !actor.Can(authz.PermissionFollowsReadOwn) && !actor.Can(authz.PermissionFollowsReadAll) {
		return FollowPage{}, appError(CodePermissionDenied, "无权查看订阅", nil)
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 30
	}
	if pageSize > 100 {
		pageSize = 100
	}
	query := s.db.Model(&models.FollowSubscription{})
	if !actor.Can(authz.PermissionFollowsReadAll) {
		query = query.Where("owner_id = ?", actor.User.ID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return FollowPage{}, err
	}
	var rows []models.FollowSubscription
	if err := query.Order("updated_at DESC,id").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error; err != nil {
		return FollowPage{}, err
	}
	list := make([]FollowSummary, 0, len(rows))
	for _, row := range rows {
		item, err := followSummary(row)
		if err != nil {
			return FollowPage{}, err
		}
		list = append(list, item)
	}
	return FollowPage{List: list, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *FollowService) Get(actor Actor, id string) (FollowSummary, error) {
	record, err := s.load(actor, id, "read")
	if err != nil {
		return FollowSummary{}, err
	}
	return followSummary(record)
}

func (s *FollowService) Update(ctx context.Context, actor Actor, id string, input UpdateFollowInput, request RequestContext) (FollowSummary, error) {
	record, err := s.load(actor, id, "update")
	if err != nil {
		return FollowSummary{}, err
	}
	snapshot, raw, err := s.validateSnapshot(actor, record.TMDBID, input.Snapshot)
	if err != nil {
		return FollowSummary{}, err
	}
	coverage, err := s.coverage.Coverage(ctx, actor, "tv", record.TMDBID)
	if err != nil {
		return FollowSummary{}, err
	}
	if err := validateFollowSeasons(coverage, snapshot.Seasons); err != nil {
		return FollowSummary{}, err
	}
	now := s.now()
	next := now.Add(time.Duration(snapshot.Schedule.Minutes) * time.Minute)
	updates := map[string]any{
		"execution_snapshot_json": string(raw),
		"revision":                input.Revision + 1,
		"next_run_at":             next,
		"last_error_code":         "",
		"last_error_message":      "",
		"updated_at":              now,
	}
	if record.Status == models.FollowStatusBlocked {
		updates["status"] = models.FollowStatusActive
		updates["next_run_at"] = now
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.FollowSubscription{}).Where("id = ? AND revision = ?", id, input.Revision).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeFollowRevisionConflict, "订阅已被修改，请刷新后重试", nil)
		}
		if err := tx.Where("subscription_id = ?", id).Delete(&models.FollowSubscriptionSeason{}).Error; err != nil {
			return err
		}
		for _, season := range snapshot.Seasons {
			if err := tx.Create(&models.FollowSubscriptionSeason{SubscriptionID: id, OwnerID: record.OwnerID, TMDBID: record.TMDBID, SeasonNumber: season, Special: season == 0}).Error; err != nil {
				return followConstraintError(err)
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "follow.update", "follow", id, "success", map[string]any{"revision": input.Revision + 1, "season_count": len(snapshot.Seasons)}, request)
	})
	if err != nil {
		return FollowSummary{}, err
	}
	return s.getSummary(id)
}

func (s *FollowService) SetPaused(actor Actor, id string, paused bool, request RequestContext) (FollowSummary, error) {
	record, err := s.load(actor, id, "update")
	if err != nil {
		return FollowSummary{}, err
	}
	status := models.FollowStatusActive
	action := "follow.resume"
	if paused {
		status = models.FollowStatusPaused
		action = "follow.pause"
	}
	now := s.now()
	updates := map[string]any{"status": status, "updated_at": now, "last_error_code": "", "last_error_message": "", "lifecycle_revision": gorm.Expr("lifecycle_revision + 1")}
	if !paused {
		updates["next_run_at"] = now
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.FollowSubscription{}).Where("id = ? AND status <> ?", id, status).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		return s.audit.Record(tx, &actor.User.ID, action, "follow", id, "success", map[string]any{"revision": record.Revision, "changed": result.RowsAffected == 1}, request)
	}); err != nil {
		return FollowSummary{}, err
	}
	return s.getSummary(id)
}

func (s *FollowService) Delete(actor Actor, id string, request RequestContext) error {
	record, err := s.load(actor, id, "delete")
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&models.FollowSubscription{}, "id = ?", id).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "follow.delete", "follow", id, "success", map[string]any{"owner_id": record.OwnerID, "revision": record.Revision}, request)
	})
}

func (s *FollowService) Runs(actor Actor, id string) ([]FollowRunSummary, error) {
	if _, err := s.load(actor, id, "read"); err != nil {
		return nil, err
	}
	var rows []models.FollowRun
	if err := s.db.Where("subscription_id = ?", id).Order("created_at DESC").Limit(100).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]FollowRunSummary, 0, len(rows))
	for _, row := range rows {
		summary := map[string]int{}
		_ = json.Unmarshal([]byte(row.FilterSummaryJSON), &summary)
		result = append(result, FollowRunSummary{ID: row.ID, JobID: row.JobID, Trigger: row.Trigger, Status: row.Status, SubscriptionRevision: row.SubscriptionRevision, SearchedNamesCount: row.SearchedNamesCount, Candidates: row.Candidates, Selected: row.Selected, FilterSummary: summary, ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, CreatedAt: row.CreatedAt})
	}
	return result, nil
}

func (s *FollowService) Enqueue(ctx context.Context, actor Actor, id, trigger string, request RequestContext) (string, error) {
	record, err := s.load(actor, id, "execute")
	if err != nil {
		return "", err
	}
	if record.Status == models.FollowStatusPaused {
		return "", appError(CodeConflict, "已暂停的订阅不能立即搜索", nil)
	}
	jobID, err := s.enqueueRecord(ctx, record, trigger)
	if err != nil {
		return "", err
	}
	if trigger == "manual" {
		if err := s.audit.Record(s.db, &actor.User.ID, "follow.search", "follow", record.ID, "success", map[string]any{"revision": record.Revision, "job_id": jobID}, request); err != nil {
			return "", err
		}
	}
	return jobID, nil
}

func (s *FollowService) EnqueueDue(ctx context.Context, limit int) error {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	now := s.now()
	var rows []models.FollowSubscription
	if err := s.db.WithContext(ctx).Where("status IN ? AND next_run_at IS NOT NULL AND next_run_at <= ?", []string{models.FollowStatusActive, models.FollowStatusCompleted}, now).Order("next_run_at,id").Limit(limit).Find(&rows).Error; err != nil {
		return err
	}
	for _, record := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, _ = s.enqueueRecord(ctx, record, "scheduled")
	}
	return nil
}

func (s *FollowService) enqueueRecord(_ context.Context, record models.FollowSubscription, trigger string) (string, error) {
	if trigger != "manual" && trigger != "created" {
		trigger = "scheduled"
	}
	resource := "follow:" + record.ID
	var existing models.Job
	if err := s.db.Where("job_type = ? AND resource_key = ? AND coalescing_key = ? AND status IN ?", JobTypeFollowSearch, resource, "search", activeJobStatuses()).First(&existing).Error; err == nil {
		return existing.ID, nil
	} else if err != gorm.ErrRecordNotFound {
		return "", err
	}
	runID := uuid.NewString()
	payload := followJobPayload{RunID: runID, SubscriptionID: record.ID, SubscriptionRevision: record.Revision, Trigger: trigger}
	now := s.now()
	priority := 0
	var snapshot FollowExecutionSnapshot
	if json.Unmarshal([]byte(record.ExecutionSnapshotJSON), &snapshot) == nil {
		priority = snapshot.DownloadPriority / 20
		if priority > 5 {
			priority = 5
		}
		if priority < -5 {
			priority = -5
		}
	}
	job, err := s.queue.EnqueueWith(EnqueueJobInput{OwnerID: record.OwnerID, JobType: JobTypeFollowSearch, Priority: priority, DisplayName: "自动追更 · " + record.Title, Provider: "follow", ResourceKey: resource, CoalescingKey: "search", Payload: payload}, func(tx *gorm.DB, job models.Job) error {
		return tx.Create(&models.FollowRun{ID: runID, SubscriptionID: record.ID, OwnerID: record.OwnerID, SubscriptionRevision: record.Revision, LifecycleRevision: record.LifecycleRevision, ExecutionSnapshotJSON: record.ExecutionSnapshotJSON, JobID: job.ID, Trigger: trigger, Status: models.FollowRunQueued, MissingSnapshotJSON: "[]", FilterSummaryJSON: "{}", CreatedAt: now, UpdatedAt: now}).Error
	})
	if err != nil {
		return "", err
	}
	var count int64
	_ = s.db.Model(&models.FollowRun{}).Where("id = ?", runID).Count(&count).Error
	if count == 0 {
		return job.ID, nil
	}
	return job.ID, nil
}

func (s *FollowService) load(actor Actor, id, action string) (models.FollowSubscription, error) {
	var record models.FollowSubscription
	if err := s.db.First(&record, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return record, appError(CodeFollowNotFound, "订阅不存在", err)
		}
		return record, err
	}
	own := record.OwnerID == actor.User.ID
	allowed := false
	switch action {
	case "read":
		allowed = actor.Can(authz.PermissionFollowsReadAll) || (own && actor.Can(authz.PermissionFollowsReadOwn))
	case "update":
		allowed = actor.Can(authz.PermissionFollowsUpdateAll) || (own && actor.Can(authz.PermissionFollowsUpdateOwn))
	case "delete":
		allowed = actor.Can(authz.PermissionFollowsDeleteAll) || (own && actor.Can(authz.PermissionFollowsDeleteOwn))
	case "execute":
		allowed = actor.Can(authz.PermissionFollowsExecuteAll) || (own && actor.Can(authz.PermissionFollowsExecuteOwn))
	}
	if !allowed {
		return record, appError(CodePermissionDenied, "无权操作该订阅", nil)
	}
	return record, nil
}

func (s *FollowService) validateSnapshot(actor Actor, tmdbID int64, input FollowExecutionSnapshot) (FollowExecutionSnapshot, []byte, error) {
	if !actor.Can(authz.PermissionDownloadsCreate) || !actor.Can(authz.PermissionMediaLibrariesRead) || !actor.Can(authz.PermissionDiscoveryRead) {
		return input, nil, appError(CodePermissionDenied, "订阅执行权限不足", nil)
	}
	input.Version = 1
	input.DownloaderID = strings.TrimSpace(input.DownloaderID)
	if input.Schedule.Kind == "" {
		input.Schedule.Kind = "interval"
	}
	if input.Schedule.Kind != "interval" || input.Schedule.Minutes < 30 || input.Schedule.Minutes > 10080 || input.DownloaderID == "" || input.MediaLibraryID == 0 || input.MaxResourcesPerRun < 1 || input.MaxResourcesPerRun > 10 || input.DownloadPriority < -100 || input.DownloadPriority > 100 {
		return input, nil, appError(CodeFollowConfigurationInvalid, "订阅执行策略无效", nil)
	}
	if len(input.Seasons) == 0 || len(input.Seasons) > 20 || !intsInRange(input.Seasons, 0, 200) {
		return input, nil, appError(CodeFollowConfigurationInvalid, "订阅季选择无效", nil)
	}
	input.Seasons = uniqueSortedInts(input.Seasons, 0, 200, 20)
	if len(input.SiteIDs) == 0 || len(input.SiteIDs) > 20 || !positiveUints(input.SiteIDs) {
		return input, nil, appError(CodeFollowConfigurationInvalid, "订阅站点选择无效", nil)
	}
	input.SiteIDs = uniqueSortedUintsByOrder(input.SiteIDs, 20)
	var downloader models.Downloader
	if err := s.db.Where("id = ? AND enabled = ?", input.DownloaderID, true).First(&downloader).Error; err != nil {
		return input, nil, appError(CodeFollowConfigurationInvalid, "订阅下载器不存在或已停用", err)
	}
	var library models.MediaLibrary
	if err := s.db.Where("id = ? AND enabled = ?", input.MediaLibraryID, true).First(&library).Error; err != nil {
		return input, nil, appError(CodeFollowConfigurationInvalid, "订阅目标媒体库不存在或已停用", err)
	}
	var count int64
	if err := s.db.Model(&models.Site{}).Where("id IN ? AND enabled = ?", input.SiteIDs, true).Count(&count).Error; err != nil || count != int64(len(input.SiteIDs)) {
		return input, nil, appError(CodeFollowConfigurationInvalid, "订阅站点不存在或已停用", err)
	}
	var err error
	for _, values := range []*[]string{&input.Filters.Resolutions, &input.Filters.VideoCodecs, &input.Filters.Qualities, &input.Filters.IncludeKeywords, &input.Filters.ExcludeKeywords, &input.Filters.ReleaseGroups, &input.Filters.ExcludeReleaseGroups} {
		*values, err = normalizeRuleStrings(*values)
		if err != nil {
			return input, nil, err
		}
	}
	if input.Filters.MinSeeders < 0 || input.Filters.MinSeeders > 100000 || !validOptionalInt(input.Filters.MaxAgeHours, 1, 87600) || !validOptionalInt64(input.Filters.MinSizeBytes, 0, 1<<50) || !validOptionalInt64(input.Filters.MaxSizeBytes, 1, 1<<50) || (input.Filters.MinSizeBytes != nil && input.Filters.MaxSizeBytes != nil && *input.Filters.MinSizeBytes > *input.Filters.MaxSizeBytes) {
		return input, nil, appError(CodeFollowConfigurationInvalid, "订阅过滤规则无效", nil)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return input, nil, err
	}
	if len(raw) > 32*1024 {
		return input, nil, appError(CodeFollowConfigurationInvalid, "订阅执行策略过大", nil)
	}
	if err := validatePrivateState(raw); err != nil {
		return input, nil, appError(CodeFollowConfigurationInvalid, "订阅执行策略包含不安全内容", nil)
	}
	_ = tmdbID
	return input, raw, nil
}

func validateFollowSeasons(coverage MediaCoverage, seasons []int) error {
	if coverage.TV == nil {
		return appError(CodeFollowConfigurationInvalid, "订阅季信息不可用", nil)
	}
	available := make(map[int]struct{}, len(coverage.TV.Seasons))
	for _, season := range coverage.TV.Seasons {
		available[season.SeasonNumber] = struct{}{}
	}
	for _, season := range seasons {
		if _, ok := available[season]; !ok {
			return appError(CodeFollowConfigurationInvalid, "订阅包含不存在的季", nil)
		}
	}
	return nil
}

func (s *FollowService) getSummary(id string) (FollowSummary, error) {
	var record models.FollowSubscription
	if err := s.db.First(&record, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return FollowSummary{}, appError(CodeFollowNotFound, "订阅不存在", err)
		}
		return FollowSummary{}, err
	}
	return followSummary(record)
}

func followSummary(record models.FollowSubscription) (FollowSummary, error) {
	var snapshot FollowExecutionSnapshot
	if err := json.Unmarshal([]byte(record.ExecutionSnapshotJSON), &snapshot); err != nil {
		return FollowSummary{}, err
	}
	return FollowSummary{ID: record.ID, OwnerID: record.OwnerID, MediaType: record.MediaType, TMDBID: record.TMDBID, Title: record.Title, Year: record.Year, PosterRef: record.PosterRef, Status: record.Status, Revision: record.Revision, Snapshot: snapshot, ProgressTarget: record.ProgressTarget, ProgressPresent: record.ProgressPresent, ProgressMissing: record.ProgressMissing, LastRunID: record.LastRunID, LastRunAt: record.LastRunAt, NextRunAt: record.NextRunAt, LastErrorCode: record.LastErrorCode, LastErrorMessage: record.LastErrorMessage, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, nil
}
func followConstraintError(err error) error {
	if err != nil && (strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "constraint")) {
		return appError(CodeFollowConflict, "所选剧集季已被订阅", err)
	}
	return err
}
func uniqueSortedInts(values []int, min, max, limit int) []int {
	seen := map[int]struct{}{}
	result := []int{}
	for _, v := range values {
		if v < min || v > max {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	sort.Ints(result)
	if len(result) > limit {
		return result[:limit]
	}
	return result
}
func intsInRange(values []int, min, max int) bool {
	for _, value := range values {
		if value < min || value > max {
			return false
		}
	}
	return true
}
func positiveUints(values []uint) bool {
	for _, value := range values {
		if value == 0 {
			return false
		}
	}
	return true
}
func uniqueSortedUintsByOrder(values []uint, limit int) []uint {
	seen := map[uint]struct{}{}
	result := []uint{}
	for _, v := range values {
		if v == 0 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
		if len(result) == limit {
			break
		}
	}
	return result
}
func normalizeRuleStrings(values []string) ([]string, error) {
	if len(values) > 16 {
		return nil, appError(CodeFollowConfigurationInvalid, "订阅规则数量过多", nil)
	}
	seen := map[string]struct{}{}
	result := []string{}
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" {
			continue
		}
		if len([]rune(value)) > 64 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, appError(CodeFollowConfigurationInvalid, "订阅规则文本无效", nil)
		}
		lower := strings.ToLower(value)
		if strings.HasPrefix(lower, "magnet:") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.Contains(lower, "cookie=") || strings.Contains(lower, "passkey=") || strings.Contains(lower, "password=") || strings.Contains(lower, "authorization:") || strings.Contains(lower, "api_key=") {
			return nil, appError(CodeFollowConfigurationInvalid, "订阅规则不能包含地址或凭据", nil)
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}
func validOptionalInt(value *int, min, max int) bool {
	return value == nil || *value >= min && *value <= max
}
func validOptionalInt64(value *int64, min, max int64) bool {
	return value == nil || *value >= min && *value <= max
}

// LockCurrentSubscription is used by the worker immediately before each
// download handoff. It deliberately ignores revision changes (the run snapshot
// is immutable) while pause/delete remains an immediate stop signal.
func (s *FollowService) LockCurrentSubscription(tx *gorm.DB, id string) (models.FollowSubscription, error) {
	var record models.FollowSubscription
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", id).Error
	return record, err
}
