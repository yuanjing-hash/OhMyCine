package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

const (
	defaultMediaPageSize = 50
	maxMediaQueryLength  = 200
)

type MediaPageQuery struct {
	Page        int
	PageSize    int
	Query       string
	MediaType   string
	MatchStatus string
	Category    string
}

type MediaLibraryEntryPage struct {
	List     []models.MediaLibraryEntry `json:"list"`
	Total    int64                      `json:"total"`
	Page     int                        `json:"page"`
	PageSize int                        `json:"page_size"`
}

type MediaCatalogItem struct {
	ID                   string                    `json:"id"`
	Title                string                    `json:"title"`
	Kind                 string                    `json:"kind"`
	FileCount            int64                     `json:"file_count"`
	SeasonCount          int64                     `json:"season_count"`
	EpisodeCount         int64                     `json:"episode_count"`
	Size                 int64                     `json:"size"`
	ModifiedAt           time.Time                 `json:"modified_at"`
	CategoryName         string                    `json:"category_name"`
	MatchStatus          string                    `json:"match_status"`
	TMDBID               *int64                    `json:"tmdb_id,omitempty"`
	ReleaseYear          *int                      `json:"release_year,omitempty"`
	Confidence           *float64                  `json:"confidence,omitempty"`
	RecognitionErrorCode string                    `json:"recognition_error_code,omitempty"`
	OriginalTitle        string                    `json:"original_title,omitempty"`
	Overview             string                    `json:"overview,omitempty"`
	PosterURL            string                    `json:"poster_url,omitempty"`
	BackdropURL          string                    `json:"backdrop_url,omitempty"`
	RecognitionToken     string                    `json:"recognition_token,omitempty"`
	RecognitionRevision  int64                     `json:"recognition_revision,omitempty"`
	ManualOverride       bool                      `json:"manual_override"`
	LibraryWorks         []MediaCatalogLibraryWork `json:"library_works"`
}

// MediaCatalogLibraryWork is the only bridge from an aggregate poster to a
// concrete library work. The opaque token never grants access by itself; every
// action resolves it again under the selected library and current actor.
type MediaCatalogLibraryWork struct {
	LibraryID   uint   `json:"library_id"`
	LibraryName string `json:"library_name"`
	WorkID      string `json:"work_id"`
	FileCount   int64  `json:"file_count"`
}

type MediaCatalogPage struct {
	List       []MediaCatalogItem `json:"list"`
	Total      int64              `json:"total"`
	Page       int                `json:"page"`
	PageSize   int                `json:"page_size"`
	Categories []string           `json:"categories"`
}

type MediaCatalogEpisode struct {
	ID           uint      `json:"id"`
	Title        string    `json:"title"`
	Season       *int      `json:"season"`
	Episode      *int      `json:"episode"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size"`
	ModifiedAt   time.Time `json:"modified_at"`
}

type MediaCatalogSeason struct {
	Number   int                   `json:"number"`
	Episodes []MediaCatalogEpisode `json:"episodes"`
}

type MediaCatalogDetail struct {
	Work                   MediaCatalogItem              `json:"work"`
	Seasons                []MediaCatalogSeason          `json:"seasons"`
	Files                  []MediaCatalogEpisode         `json:"files"`
	ReorganizableTransfers []MediaCatalogManagedTransfer `json:"reorganizable_transfers"`
}

// MediaCatalogManagedTransfer is a safe navigation projection into the
// managed-only correction workflow. It intentionally exposes no provider ID,
// physical root, manifest or credential-bearing source data.
type MediaCatalogManagedTransfer struct {
	TransferTaskID   string `json:"transfer_task_id"`
	DownloadTaskID   string `json:"download_task_id"`
	IdentityRevision uint64 `json:"identity_revision"`
	FileCount        int64  `json:"file_count"`
}

type mediaCatalogRow struct {
	LibraryID            uint
	WorkKey              string
	Title                string
	Kind                 string
	FileCount            int64
	SeasonCount          int64
	EpisodeCount         int64
	Size                 int64
	ModifiedText         string
	CategoryName         string
	MatchStatus          string
	TMDBID               *int64
	ReleaseYear          *int
	Confidence           *float64
	RecognitionErrorCode string
	RecognitionID        *uint
}

func (s *MediaLibraryService) EntryPage(actor Actor, libraryID uint, query MediaPageQuery) (MediaLibraryEntryPage, error) {
	query, err := normalizeMediaPageQuery(query)
	if err != nil {
		return MediaLibraryEntryPage{}, err
	}
	if err := s.ensureMediaLibraryReadable(actor, libraryID); err != nil {
		return MediaLibraryEntryPage{}, err
	}
	db := applyEntryFilters(s.db.Model(&models.MediaLibraryEntry{}).Where("library_id = ?", libraryID), query)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return MediaLibraryEntryPage{}, err
	}
	items := make([]models.MediaLibraryEntry, 0)
	if err := db.Order("relative_path").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&items).Error; err != nil {
		return MediaLibraryEntryPage{}, err
	}
	return MediaLibraryEntryPage{List: items, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

func (s *MediaLibraryService) Catalog(actor Actor, libraryID uint, query MediaPageQuery) (MediaCatalogPage, error) {
	query, err := normalizeMediaPageQuery(query)
	if err != nil {
		return MediaCatalogPage{}, err
	}
	if err := s.ensureMediaLibraryReadable(actor, libraryID); err != nil {
		return MediaCatalogPage{}, err
	}
	categories, err := catalogCategories(s.db.Model(&models.MediaLibraryEntry{}).Where("library_id = ?", libraryID))
	if err != nil {
		return MediaCatalogPage{}, err
	}
	grouped := applyCatalogFilters(s.db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND work_key <> ''", libraryID), query).Select("work_key").Group("work_key")
	var total int64
	if err := s.db.Table("(?) AS media_catalog", grouped).Count(&total).Error; err != nil {
		return MediaCatalogPage{}, err
	}
	rows := make([]mediaCatalogRow, 0)
	rowsQuery := applyCatalogFilters(s.db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND work_key <> ''", libraryID), query)
	if err := selectCatalogRows(rowsQuery).
		Offset((query.Page - 1) * query.PageSize).
		Limit(query.PageSize).
		Scan(&rows).Error; err != nil {
		return MediaCatalogPage{}, err
	}
	items, err := s.catalogItems(rows)
	if err != nil {
		return MediaCatalogPage{}, err
	}
	var library models.MediaLibrary
	if err := s.db.Select("id,name").First(&library, libraryID).Error; err != nil {
		return MediaCatalogPage{}, err
	}
	for index := range items {
		items[index].LibraryWorks = []MediaCatalogLibraryWork{{LibraryID: library.ID, LibraryName: library.Name, WorkID: items[index].ID, FileCount: items[index].FileCount}}
	}
	return MediaCatalogPage{List: items, Total: total, Page: query.Page, PageSize: query.PageSize, Categories: categories}, nil
}

// AggregateCatalog performs grouping before paging. It deliberately does not
// concatenate per-library pages: matched works merge only by trustworthy TMDB
// identity, while unmatched works remain library-scoped.
func (s *MediaLibraryService) AggregateCatalog(actor Actor, query MediaPageQuery) (MediaCatalogPage, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return MediaCatalogPage{}, appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	query, err := normalizeMediaPageQuery(query)
	if err != nil {
		return MediaCatalogPage{}, err
	}
	base := s.db.Model(&models.MediaLibraryEntry{}).
		Joins("JOIN media_libraries ON media_libraries.id = media_library_entries.library_id").
		Joins("JOIN storages ON storages.id = media_libraries.storage_id").
		Where("media_library_entries.work_key <> '' AND media_libraries.enabled = ? AND storages.enabled = ?", true, true)
	categories, err := catalogCategories(base)
	if err != nil {
		return MediaCatalogPage{}, err
	}
	rows := make([]mediaCatalogRow, 0)
	if err := selectCatalogRows(applyCatalogFilters(base, query)).Scan(&rows).Error; err != nil {
		return MediaCatalogPage{}, err
	}
	items, err := s.catalogItems(rows)
	if err != nil {
		return MediaCatalogPage{}, err
	}
	libraryNames := map[uint]string{}
	var libraries []models.MediaLibrary
	if err := s.db.Select("id,name").Where("enabled = ?", true).Find(&libraries).Error; err != nil {
		return MediaCatalogPage{}, err
	}
	for _, library := range libraries {
		libraryNames[library.ID] = library.Name
	}
	merged := make(map[string]*MediaCatalogItem, len(items))
	order := make([]string, 0, len(items))
	for index, item := range items {
		row := rows[index]
		identity := fmt.Sprintf("library:%d:%s", row.LibraryID, row.WorkKey)
		if item.MatchStatus == mediaRecognitionStatusMatched && item.TMDBID != nil {
			identity = item.Kind + ":tmdb:" + strconv.FormatInt(*item.TMDBID, 10)
		}
		work := MediaCatalogLibraryWork{LibraryID: row.LibraryID, LibraryName: libraryNames[row.LibraryID], WorkID: item.ID, FileCount: item.FileCount}
		if current, ok := merged[identity]; ok {
			current.FileCount += item.FileCount
			current.EpisodeCount += item.EpisodeCount
			current.Size += item.Size
			if item.SeasonCount > current.SeasonCount {
				current.SeasonCount = item.SeasonCount
			}
			if item.ModifiedAt.After(current.ModifiedAt) {
				current.ModifiedAt = item.ModifiedAt
			}
			current.LibraryWorks = append(current.LibraryWorks, work)
			continue
		}
		copyItem := item
		copyItem.ID = base64.RawURLEncoding.EncodeToString([]byte(identity))
		copyItem.LibraryWorks = []MediaCatalogLibraryWork{work}
		merged[identity] = &copyItem
		order = append(order, identity)
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := merged[order[i]], merged[order[j]]
		if left.Title == right.Title {
			return order[i] < order[j]
		}
		return strings.ToLower(left.Title) < strings.ToLower(right.Title)
	})
	total := len(order)
	start := (query.Page - 1) * query.PageSize
	if start > total {
		start = total
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	page := make([]MediaCatalogItem, 0, end-start)
	for _, key := range order[start:end] {
		page = append(page, *merged[key])
	}
	return MediaCatalogPage{List: page, Total: int64(total), Page: query.Page, PageSize: query.PageSize, Categories: categories}, nil
}

func catalogCategories(db *gorm.DB) ([]string, error) {
	items := make([]string, 0)
	if err := db.Where("media_library_entries.category_name <> ''").Distinct().Order("media_library_entries.category_name COLLATE NOCASE").Pluck("media_library_entries.category_name", &items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *MediaLibraryService) CatalogDetail(actor Actor, libraryID uint, token string) (MediaCatalogDetail, error) {
	if err := s.ensureMediaLibraryReadable(actor, libraryID); err != nil {
		return MediaCatalogDetail{}, err
	}
	workKey, err := decodeCatalogToken(token)
	if err != nil {
		return MediaCatalogDetail{}, err
	}
	filtered := s.db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND work_key = ?", libraryID, workKey)
	var row mediaCatalogRow
	if err := selectCatalogRows(filtered).Scan(&row).Error; err != nil {
		return MediaCatalogDetail{}, err
	}
	if row.WorkKey == "" {
		return MediaCatalogDetail{}, appError(CodeNotFound, "媒体作品不存在", gorm.ErrRecordNotFound)
	}
	var entries []models.MediaLibraryEntry
	if err := s.db.Where("library_id = ? AND work_key = ?", libraryID, workKey).Order("COALESCE(season, 0), COALESCE(episode, 0), relative_path").Find(&entries).Error; err != nil {
		return MediaCatalogDetail{}, err
	}
	items, err := s.catalogItems([]mediaCatalogRow{row})
	if err != nil {
		return MediaCatalogDetail{}, err
	}
	var library models.MediaLibrary
	if err := s.db.Select("id,name").First(&library, libraryID).Error; err != nil {
		return MediaCatalogDetail{}, err
	}
	items[0].LibraryWorks = []MediaCatalogLibraryWork{{LibraryID: library.ID, LibraryName: library.Name, WorkID: items[0].ID, FileCount: items[0].FileCount}}
	detail := MediaCatalogDetail{Work: items[0], Seasons: make([]MediaCatalogSeason, 0), Files: make([]MediaCatalogEpisode, 0), ReorganizableTransfers: make([]MediaCatalogManagedTransfer, 0)}
	detail.ReorganizableTransfers = s.catalogManagedTransfers(actor, libraryID, entries)
	if row.Kind != "series" {
		for _, entry := range entries {
			detail.Files = append(detail.Files, catalogEpisode(entry))
		}
		return detail, nil
	}
	episodes := make([]MediaCatalogEpisode, 0, len(entries))
	for _, entry := range entries {
		episodes = append(episodes, catalogEpisode(entry))
	}
	sort.SliceStable(episodes, func(i, j int) bool {
		leftSeason, rightSeason := pointerIntValue(episodes[i].Season), pointerIntValue(episodes[j].Season)
		if leftSeason != rightSeason {
			return leftSeason < rightSeason
		}
		leftEpisode, rightEpisode := pointerIntValue(episodes[i].Episode), pointerIntValue(episodes[j].Episode)
		if leftEpisode != rightEpisode {
			return leftEpisode < rightEpisode
		}
		return episodes[i].RelativePath < episodes[j].RelativePath
	})
	seasonIndexes := make(map[int]int)
	for _, episode := range episodes {
		number := 0
		if episode.Season != nil {
			number = *episode.Season
		}
		index, exists := seasonIndexes[number]
		if !exists {
			index = len(detail.Seasons)
			seasonIndexes[number] = index
			detail.Seasons = append(detail.Seasons, MediaCatalogSeason{Number: number, Episodes: make([]MediaCatalogEpisode, 0)})
		}
		detail.Seasons[index].Episodes = append(detail.Seasons[index].Episodes, episode)
	}
	return detail, nil
}

func (s *MediaLibraryService) catalogManagedTransfers(actor Actor, libraryID uint, entries []models.MediaLibraryEntry) []MediaCatalogManagedTransfer {
	if !actor.Can(authz.PermissionJobsControlAll) && !actor.Can(authz.PermissionJobsControlOwn) {
		return []MediaCatalogManagedTransfer{}
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.RelativePath != "" {
			paths = append(paths, entry.RelativePath)
		}
	}
	if len(paths) == 0 {
		return []MediaCatalogManagedTransfer{}
	}
	query := s.db.Table("media_managed_items AS managed").
		Select("managed.transfer_task_id, managed.download_task_id, MAX(managed.identity_revision) AS identity_revision, COUNT(*) AS file_count").
		Joins("JOIN transfer_tasks AS transfer ON transfer.id = managed.transfer_task_id").
		Where("managed.library_id = ? AND managed.managed = ? AND managed.active = ? AND managed.relative_path IN ? AND transfer.phase = ?", libraryID, true, true, paths, models.TransferTaskStatusCompleted)
	if !actor.Can(authz.PermissionJobsControlAll) {
		query = query.Where("transfer.owner_id = ?", actor.User.ID)
	}
	items := make([]MediaCatalogManagedTransfer, 0)
	if err := query.Group("managed.transfer_task_id, managed.download_task_id").Order("managed.transfer_task_id").Scan(&items).Error; err != nil {
		return []MediaCatalogManagedTransfer{}
	}
	return items
}

func normalizeMediaPageQuery(query MediaPageQuery) (MediaPageQuery, error) {
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = defaultMediaPageSize
	}
	if query.Page < 1 || (query.PageSize != 20 && query.PageSize != 50 && query.PageSize != 100) {
		return MediaPageQuery{}, appError(CodeInvalidRequest, "分页参数无效", nil)
	}
	query.Query = strings.TrimSpace(query.Query)
	if len([]rune(query.Query)) > maxMediaQueryLength {
		return MediaPageQuery{}, appError(CodeInvalidRequest, "搜索内容过长", nil)
	}
	query.MediaType = strings.TrimSpace(strings.ToLower(query.MediaType))
	if query.MediaType != "" && query.MediaType != "movie" && query.MediaType != "series" {
		return MediaPageQuery{}, appError(CodeInvalidRequest, "媒体类型筛选无效", nil)
	}
	query.MatchStatus = strings.ToLower(strings.TrimSpace(query.MatchStatus))
	if query.MatchStatus != "" && query.MatchStatus != mediaRecognitionStatusMatched && query.MatchStatus != mediaRecognitionStatusUnrecognized {
		return MediaPageQuery{}, appError(CodeInvalidRequest, "匹配状态筛选无效", nil)
	}
	query.Category = strings.TrimSpace(query.Category)
	if len([]rune(query.Category)) > 128 || strings.ContainsAny(query.Category, "\x00\r\n") {
		return MediaPageQuery{}, appError(CodeInvalidRequest, "媒体分类筛选无效", nil)
	}
	return query, nil
}

func applyEntryFilters(db *gorm.DB, query MediaPageQuery) *gorm.DB {
	if query.Query != "" {
		like := "%" + escapeLike(query.Query) + "%"
		db = db.Where("(title LIKE ? ESCAPE '\\' OR relative_path LIKE ? ESCAPE '\\')", like, like)
	}
	switch query.MediaType {
	case "movie":
		db = db.Where("media_type = ?", "movie")
	case "series":
		db = db.Where("media_type = ?", "tv")
	}
	if query.MatchStatus != "" {
		db = db.Where("match_status = ?", query.MatchStatus)
	}
	if query.Category != "" {
		db = db.Where("category_name = ?", query.Category)
	}
	return db
}

func applyCatalogFilters(db *gorm.DB, query MediaPageQuery) *gorm.DB {
	if query.Query != "" {
		like := "%" + escapeLike(query.Query) + "%"
		db = db.Where("(title LIKE ? ESCAPE '\\' OR series_title LIKE ? ESCAPE '\\')", like, like)
	}
	switch query.MediaType {
	case "movie":
		db = db.Where("media_type = ?", "movie")
	case "series":
		db = db.Where("media_type = ?", "tv")
	}
	if query.MatchStatus != "" {
		db = db.Where("match_status = ?", query.MatchStatus)
	}
	if query.Category != "" {
		db = db.Where("category_name = ?", query.Category)
	}
	return db
}

func selectCatalogRows(db *gorm.DB) *gorm.DB {
	return db.Select(`media_library_entries.library_id AS library_id, work_key,
		CASE WHEN MAX(CASE WHEN media_type = 'tv' THEN 1 ELSE 0 END) = 1 THEN 'series' ELSE 'movie' END AS kind,
		MAX(CASE WHEN series_title <> '' THEN series_title ELSE title END) AS title,
		COUNT(*) AS file_count,
		COUNT(DISTINCT CASE WHEN season IS NOT NULL THEN season END) AS season_count,
		SUM(CASE WHEN episode IS NOT NULL THEN 1 ELSE 0 END) AS episode_count,
		COALESCE(SUM(size), 0) AS size,
		MAX(modified_at) AS modified_text,
		MIN(category_name) AS category_name,
		MAX(tmdb_id) AS tmdb_id,
		MAX(release_year) AS release_year,
		MAX(match_confidence) AS confidence,
		MIN(recognition_error_code) AS recognition_error_code,
		MAX(recognition_id) AS recognition_id,
		CASE WHEN SUM(CASE WHEN match_status = 'matched' THEN 1 ELSE 0 END) = COUNT(*) THEN 'matched' ELSE 'unrecognized' END AS match_status`).
		Group("media_library_entries.library_id, work_key").
		Order("title COLLATE NOCASE, work_key")
}

func catalogItem(row mediaCatalogRow) MediaCatalogItem {
	return MediaCatalogItem{ID: encodeCatalogToken(row.WorkKey), Title: row.Title, Kind: row.Kind, FileCount: row.FileCount, SeasonCount: row.SeasonCount, EpisodeCount: row.EpisodeCount, Size: row.Size, ModifiedAt: parseCatalogTime(row.ModifiedText), CategoryName: row.CategoryName, MatchStatus: row.MatchStatus, TMDBID: row.TMDBID, ReleaseYear: row.ReleaseYear, Confidence: row.Confidence, RecognitionErrorCode: row.RecognitionErrorCode, LibraryWorks: []MediaCatalogLibraryWork{}}
}

func (s *MediaLibraryService) catalogItems(rows []mediaCatalogRow) ([]MediaCatalogItem, error) {
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		if row.RecognitionID != nil {
			ids = append(ids, *row.RecognitionID)
		}
	}
	recognitions := make(map[uint]models.MediaLibraryRecognition, len(ids))
	if len(ids) > 0 {
		var records []models.MediaLibraryRecognition
		if err := s.db.Where("id IN ?", ids).Find(&records).Error; err != nil {
			return nil, err
		}
		for _, record := range records {
			recognitions[record.ID] = record
		}
	}
	items := make([]MediaCatalogItem, 0, len(rows))
	for _, row := range rows {
		item := catalogItem(row)
		if row.RecognitionID != nil {
			if recognition, ok := recognitions[*row.RecognitionID]; ok {
				item.RecognitionToken, item.RecognitionRevision, item.ManualOverride = encodeRecognitionToken(recognition.ID), recognition.UpdatedAt.UnixNano(), recognition.ManualOverride
				if _, snapshot, err := decodeRecognitionMetadata(recognition.MetadataJSON); err == nil {
					item.OriginalTitle, item.Overview = snapshot.OriginalTitle, snapshot.Overview
					item.PosterURL = s.catalogImageURL(snapshot.PosterPath, "w500")
					item.BackdropURL = s.catalogImageURL(snapshot.BackdropPath, "w1280")
				}
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *MediaLibraryService) catalogImageURL(identity, size string) string {
	identity = safeTMDBImagePath(identity)
	if identity == "" || s.metadata == nil {
		return ""
	}
	client, err := s.metadata.Client()
	if err != nil {
		return ""
	}
	upstream, err := client.ImageURL(identity, size)
	if err != nil {
		return ""
	}
	return proxyDiscoveryImage("tmdb", upstream)
}

// catalogRecognitionTokens resolves a work token server-side and refuses an
// empty or drifting association. Callers never accept recognition IDs from the
// browser.
func (s *MediaLibraryService) catalogRecognitionTokens(actor Actor, libraryID uint, workToken string) ([]string, error) {
	if err := s.ensureMediaLibraryReadable(actor, libraryID); err != nil {
		return nil, err
	}
	workKey, err := decodeCatalogToken(workToken)
	if err != nil {
		return nil, err
	}
	var ids []uint
	if err := s.db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND work_key = ? AND recognition_id IS NOT NULL", libraryID, workKey).Distinct().Order("recognition_id").Pluck("recognition_id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, appError(CodeConflict, "当前作品没有可维护的识别记录", nil)
	}
	tokens := make([]string, 0, len(ids))
	for _, id := range ids {
		tokens = append(tokens, encodeRecognitionToken(id))
	}
	return tokens, nil
}

func (s *MediaLibraryService) CatalogRecognitionCandidates(ctx context.Context, actor Actor, libraryID uint, workToken, title, mediaType string, year *int) ([]tmdb.Candidate, error) {
	tokens, err := s.catalogRecognitionTokens(actor, libraryID, workToken)
	if err != nil {
		return nil, err
	}
	return s.RecognitionCandidates(ctx, actor, libraryID, tokens[0], title, mediaType, year)
}

func (s *MediaLibraryService) RetryCatalogRecognition(ctx context.Context, actor Actor, libraryID uint, workToken string, request RequestContext) ([]MediaRecognitionSummary, error) {
	tokens, err := s.catalogRecognitionTokens(actor, libraryID, workToken)
	if err != nil {
		return nil, err
	}
	result := make([]MediaRecognitionSummary, 0, len(tokens))
	for _, token := range tokens {
		item, itemErr := s.RetryRecognition(ctx, actor, libraryID, token, request)
		if itemErr != nil {
			return nil, itemErr
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *MediaLibraryService) OverrideCatalogRecognition(ctx context.Context, actor Actor, libraryID uint, workToken string, input MediaRecognitionOverrideInput, request RequestContext) ([]MediaRecognitionSummary, error) {
	tokens, err := s.catalogRecognitionTokens(actor, libraryID, workToken)
	if err != nil {
		return nil, err
	}
	result := make([]MediaRecognitionSummary, 0, len(tokens))
	for _, token := range tokens {
		item, itemErr := s.OverrideRecognition(ctx, actor, libraryID, token, input, request)
		if itemErr != nil {
			return nil, itemErr
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *MediaLibraryService) ClearCatalogRecognitionOverride(ctx context.Context, actor Actor, libraryID uint, workToken string, request RequestContext) ([]MediaRecognitionSummary, error) {
	tokens, err := s.catalogRecognitionTokens(actor, libraryID, workToken)
	if err != nil {
		return nil, err
	}
	result := make([]MediaRecognitionSummary, 0, len(tokens))
	for _, token := range tokens {
		var record models.MediaLibraryRecognition
		id, _ := decodeRecognitionToken(token)
		if s.db.First(&record, id).Error == nil && !record.ManualOverride {
			continue
		}
		item, itemErr := s.ClearRecognitionOverride(ctx, actor, libraryID, token, request)
		if itemErr != nil {
			return nil, itemErr
		}
		result = append(result, item)
	}
	return result, nil
}

func parseCatalogTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func catalogEpisode(entry models.MediaLibraryEntry) MediaCatalogEpisode {
	season, episode := resolvedCatalogEpisodeFacts(entry)
	return MediaCatalogEpisode{ID: entry.ID, Title: entry.Title, Season: season, Episode: episode, RelativePath: entry.RelativePath, Size: entry.Size, ModifiedAt: entry.ModifiedAt}
}

// resolvedCatalogEpisodeFacts repairs legacy projections at the read boundary
// without trusting arbitrary client input. A provider-relative filename is a
// stronger per-file fact than a work-level recognition hint persisted by an
// older scanner.
func resolvedCatalogEpisodeFacts(entry models.MediaLibraryEntry) (*int, *int) {
	season, episode := cloneInt(entry.Season), cloneInt(entry.Episode)
	parsed := medialibrary.ParseMedia(path.Base(strings.ReplaceAll(entry.RelativePath, "\\", "/")), entry.RelativePath)
	if parsed.Season != nil {
		season = cloneInt(parsed.Season)
	}
	if parsed.Episode != nil {
		episode = cloneInt(parsed.Episode)
	}
	return season, episode
}

func pointerIntValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func encodeCatalogToken(workKey string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(workKey))
}

func decodeCatalogToken(token string) (string, error) {
	if token == "" || len(token) > 256 {
		return "", appError(CodeInvalidRequest, "媒体作品标识无效", nil)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", appError(CodeInvalidRequest, "媒体作品标识无效", err)
	}
	workKey := string(decoded)
	if len(workKey) > 80 || (!strings.HasPrefix(workKey, "series:") && !strings.HasPrefix(workKey, "movie:") && !strings.HasPrefix(workKey, "file:")) {
		return "", appError(CodeInvalidRequest, "媒体作品标识无效", nil)
	}
	return workKey, nil
}

func escapeLike(value string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(value)
}

func (s *MediaLibraryService) ensureMediaLibraryReadable(actor Actor, libraryID uint) error {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	var count int64
	if err := s.db.Model(&models.MediaLibrary{}).Where("id = ?", libraryID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return mediaLibraryNotFound(gorm.ErrRecordNotFound)
	}
	return nil
}
