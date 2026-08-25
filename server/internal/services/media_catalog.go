package services

import (
	"encoding/base64"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
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
	ID                   string    `json:"id"`
	Title                string    `json:"title"`
	Kind                 string    `json:"kind"`
	FileCount            int64     `json:"file_count"`
	SeasonCount          int64     `json:"season_count"`
	EpisodeCount         int64     `json:"episode_count"`
	Size                 int64     `json:"size"`
	ModifiedAt           time.Time `json:"modified_at"`
	CategoryName         string    `json:"category_name"`
	MatchStatus          string    `json:"match_status"`
	TMDBID               *int64    `json:"tmdb_id,omitempty"`
	ReleaseYear          *int      `json:"release_year,omitempty"`
	Confidence           *float64  `json:"confidence,omitempty"`
	RecognitionErrorCode string    `json:"recognition_error_code,omitempty"`
}

type MediaCatalogPage struct {
	List     []MediaCatalogItem `json:"list"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
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
	Work    MediaCatalogItem      `json:"work"`
	Seasons []MediaCatalogSeason  `json:"seasons"`
	Files   []MediaCatalogEpisode `json:"files"`
}

type mediaCatalogRow struct {
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
	items := make([]MediaCatalogItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, catalogItem(row))
	}
	return MediaCatalogPage{List: items, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
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
	detail := MediaCatalogDetail{Work: catalogItem(row), Seasons: make([]MediaCatalogSeason, 0), Files: make([]MediaCatalogEpisode, 0)}
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
	return db.Select(`work_key,
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
		CASE WHEN SUM(CASE WHEN match_status = 'matched' THEN 1 ELSE 0 END) = COUNT(*) THEN 'matched' ELSE 'unrecognized' END AS match_status`).
		Group("work_key").
		Order("title COLLATE NOCASE, work_key")
}

func catalogItem(row mediaCatalogRow) MediaCatalogItem {
	return MediaCatalogItem{ID: encodeCatalogToken(row.WorkKey), Title: row.Title, Kind: row.Kind, FileCount: row.FileCount, SeasonCount: row.SeasonCount, EpisodeCount: row.EpisodeCount, Size: row.Size, ModifiedAt: parseCatalogTime(row.ModifiedText), CategoryName: row.CategoryName, MatchStatus: row.MatchStatus, TMDBID: row.TMDBID, ReleaseYear: row.ReleaseYear, Confidence: row.Confidence, RecognitionErrorCode: row.RecognitionErrorCode}
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
