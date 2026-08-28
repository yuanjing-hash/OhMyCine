package services

import (
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

type TransferListFilter struct {
	Scope        string
	Status       string
	LibraryID    *uint
	Category     string
	TransferMode string
	Keyword      string
	Page         int
	PageSize     int
}

type TransferStats struct {
	Processing     int64 `json:"processing"`
	WaitingAction  int64 `json:"waiting_action"`
	Failed         int64 `json:"failed"`
	CompletedToday int64 `json:"completed_today"`
}

type TransferLibraryOption struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type TransferFilterOptions struct {
	Libraries  []TransferLibraryOption `json:"libraries"`
	Categories []string                `json:"categories"`
}

type TransferSummary struct {
	ID               string     `json:"id"`
	OwnerID          uint       `json:"owner_id"`
	DownloadTaskID   string     `json:"download_task_id"`
	JobID            string     `json:"job_id"`
	DisplayName      string     `json:"display_name"`
	DownloaderName   string     `json:"downloader_name"`
	ProviderType     string     `json:"provider_type"`
	ScrapeStatus     string     `json:"scrape_status"`
	ScrapeTitle      string     `json:"scrape_title"`
	ScrapeMediaType  string     `json:"scrape_media_type"`
	ScrapeCategory   string     `json:"scrape_category"`
	ScrapeTMDBID     *int64     `json:"scrape_tmdb_id"`
	ScrapeYear       *int       `json:"scrape_year"`
	ScrapeConfidence *float64   `json:"scrape_confidence"`
	IdentitySource   string     `json:"identity_source"`
	IdentityStatus   string     `json:"identity_status"`
	IdentityLocked   bool       `json:"identity_locked"`
	IdentityRevision uint64     `json:"identity_revision"`
	ProfileID        uint       `json:"profile_id"`
	ProfileRevision  uint64     `json:"profile_revision"`
	LibraryID        uint       `json:"library_id"`
	LibraryName      string     `json:"library_name"`
	RouteKind        string     `json:"route_kind"`
	TransferMode     string     `json:"transfer_mode"`
	ConflictPolicy   string     `json:"conflict_policy"`
	Phase            string     `json:"phase"`
	JobStatus        string     `json:"job_status"`
	RetryAt          *time.Time `json:"retry_at"`
	ProcessedFiles   int        `json:"processed_files"`
	TotalFiles       int        `json:"total_files"`
	LastErrorCode    string     `json:"last_error_code"`
	LastErrorMessage string     `json:"last_error_message"`
	CleanupStatus    string     `json:"cleanup_status"`
	CleanupRemoved   int        `json:"cleanup_removed"`
	CleanupErrorCode string     `json:"cleanup_error_code"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	FinishedAt       *time.Time `json:"finished_at"`
}

type TransferPage struct {
	List          []TransferSummary     `json:"list"`
	Total         int64                 `json:"total"`
	Page          int                   `json:"page"`
	PageSize      int                   `json:"page_size"`
	Stats         TransferStats         `json:"stats"`
	FilterOptions TransferFilterOptions `json:"filter_options"`
}

type TransferDetail struct {
	TransferSummary
	MovieDirectoryTemplate string                  `json:"movie_directory_template"`
	MovieFilenameTemplate  string                  `json:"movie_filename_template"`
	TVDirectoryTemplate    string                  `json:"tv_directory_template"`
	TVFilenameTemplate     string                  `json:"tv_filename_template"`
	PlanSummary            *TransferPlanSummary    `json:"plan_summary"`
	Job                    JobDTO                  `json:"job"`
	Attempts               []models.JobAttempt     `json:"attempts"`
	Timeline               []models.JobStatusEvent `json:"timeline"`
}

type transferProjectionRow struct {
	TransferSummary
	PlanSummaryJSON        string `gorm:"column:plan_summary_json"`
	MovieDirectoryTemplate string `gorm:"column:movie_directory_template"`
	MovieFilenameTemplate  string `gorm:"column:movie_filename_template"`
	TVDirectoryTemplate    string `gorm:"column:tv_directory_template"`
	TVFilenameTemplate     string `gorm:"column:tv_filename_template"`
	TransferErrorCode      string `gorm:"column:transfer_error_code"`
	JobErrorCode           string `gorm:"column:job_error_code"`
	JobErrorMessage        string `gorm:"column:job_error_message"`
}

const transferProjectionColumns = `
	transfer.id, transfer.owner_id, transfer.download_task_id, transfer.job_id,
	download.display_name, download.downloader_name, download.provider_type,
	download.scrape_status, download.scrape_title, download.scrape_media_type,
	download.scrape_category, download.scrape_tmdb_id, download.scrape_year,
	download.scrape_confidence, download.identity_source, download.identity_status,
	download.identity_locked, download.identity_revision, download.profile_id, download.profile_revision,
	transfer.library_id, transfer.library_name, download.transfer_route_kind AS route_kind, download.transfer_mode,
	download.conflict_policy, transfer.phase, jobs.status AS job_status, jobs.next_attempt_at AS retry_at,
	transfer.processed_files, transfer.total_files,
	transfer.last_error_code AS transfer_error_code,
	transfer.cleanup_status, transfer.cleanup_removed, transfer.cleanup_error_code,
	jobs.last_error_code AS job_error_code,
	jobs.last_error_message AS job_error_message,
	transfer.created_at, transfer.updated_at, transfer.finished_at,
	transfer.plan_summary_json, download.movie_directory_template,
	download.movie_filename_template, download.tv_directory_template,
	download.tv_filename_template`

func (s *TransferService) transferReadScope(actor Actor) (*gorm.DB, error) {
	if !actor.Can(authz.PermissionTransfersReadAll) && !actor.Can(authz.PermissionTransfersReadOwn) {
		return nil, appError(CodePermissionDenied, "无权查看媒体整理任务", nil)
	}
	query := s.db.Table("transfer_tasks AS transfer").
		Joins("JOIN download_tasks AS download ON download.id = transfer.download_task_id").
		Joins("JOIN jobs ON jobs.id = transfer.job_id")
	if !actor.Can(authz.PermissionTransfersReadAll) {
		query = query.Where("transfer.owner_id = ?", actor.User.ID)
	}
	return query, nil
}

func (s *TransferService) List(actor Actor, filter TransferListFilter) (TransferPage, error) {
	query, err := s.transferReadScope(actor)
	if err != nil {
		return TransferPage{}, err
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 50
	}
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}
	stats, err := s.transferStats(query.Session(&gorm.Session{}))
	if err != nil {
		return TransferPage{}, err
	}
	filterOptions, err := s.transferFilterOptions(query.Session(&gorm.Session{}))
	if err != nil {
		return TransferPage{}, err
	}
	query = applyTransferFilters(query, filter)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return TransferPage{}, err
	}
	rows := make([]transferProjectionRow, 0)
	if err := query.Select(transferProjectionColumns).
		Order("transfer.created_at DESC, transfer.id DESC").
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Scan(&rows).Error; err != nil {
		return TransferPage{}, err
	}
	list := make([]TransferSummary, 0, len(rows))
	for _, row := range rows {
		finalizeTransferProjection(&row)
		list = append(list, row.TransferSummary)
	}
	return TransferPage{List: list, Total: total, Page: filter.Page, PageSize: filter.PageSize, Stats: stats, FilterOptions: filterOptions}, nil
}

func (s *TransferService) Get(actor Actor, id string) (TransferDetail, error) {
	query, err := s.transferReadScope(actor)
	if err != nil {
		return TransferDetail{}, err
	}
	var row transferProjectionRow
	if err := query.Select(transferProjectionColumns).Where("transfer.id = ?", id).Scan(&row).Error; err != nil {
		return TransferDetail{}, err
	}
	if row.ID == "" {
		return TransferDetail{}, appError(CodeNotFound, "媒体整理任务不存在", gorm.ErrRecordNotFound)
	}
	finalizeTransferProjection(&row)
	plan, err := decodeTransferPlanSummary(row.PlanSummaryJSON)
	if err != nil {
		plan = nil
	}
	job, attempts, timeline, err := s.queue.domainDetail(row.JobID)
	if err != nil {
		return TransferDetail{}, err
	}
	return TransferDetail{
		TransferSummary:        row.TransferSummary,
		MovieDirectoryTemplate: row.MovieDirectoryTemplate,
		MovieFilenameTemplate:  row.MovieFilenameTemplate,
		TVDirectoryTemplate:    row.TVDirectoryTemplate,
		TVFilenameTemplate:     row.TVFilenameTemplate,
		PlanSummary:            plan,
		Job:                    job,
		Attempts:               attempts,
		Timeline:               timeline,
	}, nil
}

func applyTransferFilters(query *gorm.DB, filter TransferListFilter) *gorm.DB {
	switch filter.Scope {
	case "active":
		query = query.Where("jobs.status IN ?", []string{models.JobStatusQueued, models.JobStatusRunning, models.JobStatusRetryWait, models.JobStatusWaitingUserAction, models.JobStatusPaused})
	case "history":
		query = query.Where("jobs.status IN ?", []string{models.JobStatusFailed, models.JobStatusCancelled, models.JobStatusCompleted})
	}
	if filter.LibraryID != nil {
		query = query.Where("transfer.library_id = ?", *filter.LibraryID)
	}
	if filter.Category != "" {
		query = query.Where("download.scrape_category = ?", filter.Category)
	}
	if filter.TransferMode != "" {
		query = query.Where("download.transfer_mode = ?", filter.TransferMode)
	}
	switch filter.Status {
	case "processing":
		query = query.Where("jobs.status IN ?", []string{models.JobStatusQueued, models.JobStatusRunning, models.JobStatusRetryWait})
	case "waiting_action":
		query = query.Where("jobs.status = ?", models.JobStatusWaitingUserAction)
	case "paused":
		query = query.Where("jobs.status = ?", models.JobStatusPaused)
	case "failed":
		query = query.Where("jobs.status = ?", models.JobStatusFailed)
	case "cancelled":
		query = query.Where("jobs.status = ?", models.JobStatusCancelled)
	case "completed":
		query = query.Where("jobs.status = ? AND transfer.phase = ?", models.JobStatusCompleted, models.TransferTaskStatusCompleted)
	}
	if filter.Keyword != "" {
		like := "%" + escapeSQLiteLike(strings.ToLower(filter.Keyword)) + "%"
		query = query.Where("(LOWER(download.display_name) LIKE ? ESCAPE '\\' OR LOWER(download.scrape_title) LIKE ? ESCAPE '\\')", like, like)
	}
	return query
}

func (s *TransferService) transferStats(query *gorm.DB) (TransferStats, error) {
	var stats TransferStats
	counts := []struct {
		target *int64
		where  string
		args   []any
	}{
		{&stats.Processing, "jobs.status IN ?", []any{[]string{models.JobStatusQueued, models.JobStatusRunning, models.JobStatusRetryWait}}},
		{&stats.WaitingAction, "jobs.status = ?", []any{models.JobStatusWaitingUserAction}},
		{&stats.Failed, "jobs.status = ?", []any{models.JobStatusFailed}},
	}
	for _, count := range counts {
		if err := query.Session(&gorm.Session{}).Where(count.where, count.args...).Count(count.target).Error; err != nil {
			return TransferStats{}, err
		}
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UTC()
	if err := query.Session(&gorm.Session{}).
		Where("transfer.phase = ? AND transfer.finished_at >= ?", models.TransferTaskStatusCompleted, start).
		Count(&stats.CompletedToday).Error; err != nil {
		return TransferStats{}, err
	}
	return stats, nil
}

func (s *TransferService) transferFilterOptions(query *gorm.DB) (TransferFilterOptions, error) {
	options := TransferFilterOptions{Libraries: make([]TransferLibraryOption, 0), Categories: make([]string, 0)}
	if err := query.Session(&gorm.Session{}).
		Select("transfer.library_id AS id, MAX(transfer.library_name) AS name").
		Group("transfer.library_id").
		Order("name, id").
		Scan(&options.Libraries).Error; err != nil {
		return TransferFilterOptions{}, err
	}
	if err := query.Session(&gorm.Session{}).
		Where("download.scrape_category <> ''").
		Distinct("download.scrape_category").
		Order("download.scrape_category").
		Pluck("download.scrape_category", &options.Categories).Error; err != nil {
		return TransferFilterOptions{}, err
	}
	return options, nil
}

func finalizeTransferProjection(row *transferProjectionRow) {
	row.LastErrorCode = row.JobErrorCode
	if row.LastErrorCode == "" {
		row.LastErrorCode = row.TransferErrorCode
	}
	row.LastErrorMessage = row.JobErrorMessage
}

func escapeSQLiteLike(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "%", "\\%")
	return strings.ReplaceAll(value, "_", "\\_")
}
