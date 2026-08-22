package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

type PlayerMediaLibrary struct {
	ID             uint       `json:"id"`
	Name           string     `json:"name"`
	StorageType    string     `json:"storage_type"`
	SortOrder      int        `json:"sort_order"`
	Status         string     `json:"status"`
	EntryCount     int64      `json:"entry_count"`
	DirectStream   bool       `json:"direct_stream"`
	STRMEnabled    bool       `json:"strm_enabled"`
	LastSuccessful *time.Time `json:"last_successful_scan_at,omitempty"`
}

type PlayerMediaIdentity struct {
	Scheme    string `json:"scheme"`
	MediaType string `json:"media_type"`
	Value     string `json:"value"`
}

type PlayerMediaItem struct {
	ID             string              `json:"id"`
	LibraryID      uint                `json:"library_id"`
	Title          string              `json:"title"`
	OriginalTitle  string              `json:"original_title,omitempty"`
	Kind           string              `json:"kind"`
	ReleaseYear    *int                `json:"release_year,omitempty"`
	Overview       string              `json:"overview,omitempty"`
	Tagline        string              `json:"tagline,omitempty"`
	Rating         float64             `json:"rating,omitempty"`
	RuntimeMinutes int                 `json:"runtime_minutes,omitempty"`
	Genres         []string            `json:"genres,omitempty"`
	Directors      []string            `json:"directors,omitempty"`
	Writers        []string            `json:"writers,omitempty"`
	Cast           []string            `json:"cast,omitempty"`
	TMDBID         int64               `json:"tmdb_id,omitempty"`
	IMDbID         string              `json:"imdb_id,omitempty"`
	PosterPath     string              `json:"poster_path,omitempty"`
	BackdropPath   string              `json:"backdrop_path,omitempty"`
	StillPaths     []string            `json:"still_paths,omitempty"`
	WorkIdentity   PlayerMediaIdentity `json:"work_identity"`
	FileCount      int64               `json:"file_count"`
	SeasonCount    int64               `json:"season_count"`
	EpisodeCount   int64               `json:"episode_count"`
	ModifiedAt     time.Time           `json:"modified_at"`
	CategoryName   string              `json:"category_name"`
	MatchStatus    string              `json:"match_status"`
}

type PlayerMediaItemPage struct {
	List     []PlayerMediaItem `json:"list"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

type PlayerMediaVersion struct {
	ID            uint      `json:"id"`
	Title         string    `json:"title"`
	Season        *int      `json:"season,omitempty"`
	Episode       *int      `json:"episode,omitempty"`
	Size          int64     `json:"size"`
	ModifiedAt    time.Time `json:"modified_at"`
	Playable      bool      `json:"playable"`
	StreamPath    string    `json:"stream_path,omitempty"`
	ExactIdentity string    `json:"exact_identity"`
}

type PlayerMediaDetail struct {
	Item     PlayerMediaItem      `json:"item"`
	Versions []PlayerMediaVersion `json:"versions"`
}

type PlayerEmbyInstance struct {
	ConnectionID        uint   `json:"connection_id"`
	Name                string `json:"name"`
	HealthStatus        string `json:"health_status"`
	InstanceFingerprint string `json:"instance_fingerprint"`
}

const (
	playerStreamKindLocal    = "local_file"
	playerStreamKindRedirect = "redirect"
)

// PlayerStreamResolution is an internal request-scoped result. LocalPath is
// deliberately absent: an absolute filesystem path must never cross the API
// boundary. The handler owns and closes File after ServeContent returns.
type PlayerStreamResolution struct {
	Kind        string
	File        *os.File
	Name        string
	ModifiedAt  time.Time
	RedirectURL string
}

func PlayerStreamUnavailableError() error {
	return appError(CodeProxyTargetUnavailable, "播放目标不可用", nil)
}

func (s *MediaLibraryService) PlayerLibraries(actor Actor) ([]PlayerMediaLibrary, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	type row struct {
		ID                   uint
		Name                 string
		StorageType          string
		SortOrder            int
		Status               string
		STRMEnabled          bool
		SignedProxyEnabled   bool
		LastSuccessfulScanAt *time.Time
		EntryCount           int64
	}
	var rows []row
	err := s.db.Table("media_libraries").
		Select("media_libraries.id, media_libraries.name, storages.type AS storage_type, media_libraries.sort_order, media_libraries.status, media_libraries.strm_enabled, media_libraries.signed_proxy_enabled, media_libraries.last_successful_scan_at, COUNT(media_library_entries.id) AS entry_count").
		Joins("JOIN storages ON storages.id = media_libraries.storage_id").
		Joins("LEFT JOIN media_library_entries ON media_library_entries.library_id = media_libraries.id").
		Where("media_libraries.enabled = ? AND storages.enabled = ?", true, true).
		Group("media_libraries.id").Order("media_libraries.sort_order, media_libraries.id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]PlayerMediaLibrary, 0, len(rows))
	for _, row := range rows {
		directStream := row.StorageType == models.StorageTypeLocal || row.StorageType == models.StorageTypePan115 && row.STRMEnabled && row.SignedProxyEnabled
		result = append(result, PlayerMediaLibrary{ID: row.ID, Name: row.Name, StorageType: row.StorageType, SortOrder: row.SortOrder, Status: row.Status, EntryCount: row.EntryCount, DirectStream: directStream, STRMEnabled: row.STRMEnabled, LastSuccessful: row.LastSuccessfulScanAt})
	}
	return result, nil
}

func (s *MediaLibraryService) PlayerCatalog(actor Actor, libraryID uint, query MediaPageQuery) (PlayerMediaItemPage, error) {
	if err := s.ensurePlayerMediaLibraryReadable(actor, libraryID); err != nil {
		return PlayerMediaItemPage{}, err
	}
	page, err := s.Catalog(actor, libraryID, query)
	if err != nil {
		return PlayerMediaItemPage{}, err
	}
	items := make([]PlayerMediaItem, 0, len(page.List))
	for _, item := range page.List {
		projected, err := s.playerMediaItem(libraryID, item)
		if err != nil {
			return PlayerMediaItemPage{}, err
		}
		items = append(items, projected)
	}
	return PlayerMediaItemPage{List: items, Total: int(page.Total), Page: page.Page, PageSize: page.PageSize}, nil
}

func (s *MediaLibraryService) PlayerCatalogDetail(actor Actor, libraryID uint, token string) (PlayerMediaDetail, error) {
	if err := s.ensurePlayerMediaLibraryReadable(actor, libraryID); err != nil {
		return PlayerMediaDetail{}, err
	}
	detail, err := s.CatalogDetail(actor, libraryID, token)
	if err != nil {
		return PlayerMediaDetail{}, err
	}
	item, err := s.playerMediaItem(libraryID, detail.Work)
	if err != nil {
		return PlayerMediaDetail{}, err
	}
	workKey, err := decodeCatalogToken(token)
	if err != nil {
		return PlayerMediaDetail{}, err
	}
	streamMode, err := s.playerDirectStreamMode(libraryID)
	if err != nil {
		return PlayerMediaDetail{}, err
	}
	var entries []models.MediaLibraryEntry
	if err := s.db.Where("library_id = ? AND work_key = ?", libraryID, workKey).Order("COALESCE(season, 0), COALESCE(episode, 0), relative_path").Find(&entries).Error; err != nil {
		return PlayerMediaDetail{}, err
	}
	versions := make([]PlayerMediaVersion, 0, len(entries))
	for _, entry := range entries {
		playable := false
		exactIdentity := "server:entry:" + strconv.FormatUint(uint64(entry.ID), 10)
		switch streamMode {
		case models.StorageTypeLocal:
			file, _, openErr := openLocalPlayerEntry(s.db, entry)
			if openErr == nil {
				playable = true
				_ = file.Close()
			}
		case models.StorageTypePan115:
			var artifact models.MediaArtifact
			artifactErr := s.db.Where("library_id = ? AND source_identity = ? AND kind = ? AND target_kind = ? AND managed = ? AND active = ? AND status = ?", entry.LibraryID, fmt.Sprintf("entry:%d", entry.ID), models.MediaArtifactKindSTRM, models.MediaArtifactTargetLocalProjection, true, true, models.MediaArtifactStatusCompleted).
				Order("updated_at DESC").First(&artifact).Error
			if artifactErr != nil && artifactErr != gorm.ErrRecordNotFound {
				return PlayerMediaDetail{}, artifactErr
			}
			if artifactErr == nil {
				playable = true
				exactIdentity = "ohmycine:artifact:" + artifact.OpaqueID
			}
		}
		streamPath := ""
		if playable {
			streamPath = "/api/v1/player/media-entries/" + strconv.FormatUint(uint64(entry.ID), 10) + "/stream"
		}
		versions = append(versions, PlayerMediaVersion{ID: entry.ID, Title: entry.Title, Season: entry.Season, Episode: entry.Episode, Size: entry.Size, ModifiedAt: entry.ModifiedAt, Playable: playable, StreamPath: streamPath, ExactIdentity: exactIdentity})
	}
	return PlayerMediaDetail{Item: item, Versions: versions}, nil
}

func (s *MediaLibraryService) playerDirectStreamMode(libraryID uint) (string, error) {
	type row struct {
		StorageType        string
		LibraryEnabled     bool
		StorageEnabled     bool
		STRMEnabled        bool
		SignedProxyEnabled bool
	}
	var item row
	err := s.db.Table("media_libraries").
		Select("storages.type AS storage_type, media_libraries.enabled AS library_enabled, storages.enabled AS storage_enabled, media_libraries.strm_enabled, media_libraries.signed_proxy_enabled").
		Joins("JOIN storages ON storages.id = media_libraries.storage_id").
		Where("media_libraries.id = ?", libraryID).Take(&item).Error
	if err != nil {
		return "", err
	}
	if !item.LibraryEnabled || !item.StorageEnabled {
		return "", nil
	}
	if item.StorageType == models.StorageTypeLocal {
		return models.StorageTypeLocal, nil
	}
	if item.StorageType == models.StorageTypePan115 && item.STRMEnabled && item.SignedProxyEnabled {
		return models.StorageTypePan115, nil
	}
	return "", nil
}

func (s *MediaLibraryService) PlayerSearch(actor Actor, query MediaPageQuery) (PlayerMediaItemPage, error) {
	query, err := normalizeMediaPageQuery(query)
	if err != nil {
		return PlayerMediaItemPage{}, err
	}
	libraries, err := s.PlayerLibraries(actor)
	if err != nil {
		return PlayerMediaItemPage{}, err
	}
	all := make([]PlayerMediaItem, 0)
	for _, library := range libraries {
		for pageNumber := 1; ; pageNumber++ {
			page, err := s.PlayerCatalog(actor, library.ID, MediaPageQuery{Page: pageNumber, PageSize: 100, Query: query.Query, MediaType: query.MediaType, MatchStatus: query.MatchStatus})
			if err != nil {
				return PlayerMediaItemPage{}, err
			}
			all = append(all, page.List...)
			if len(page.List) == 0 || pageNumber*page.PageSize >= page.Total {
				break
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Title == all[j].Title {
			return all[i].ID < all[j].ID
		}
		return strings.ToLower(all[i].Title) < strings.ToLower(all[j].Title)
	})
	total := len(all)
	start := total
	pageIndex := query.Page - 1
	if pageIndex <= total/query.PageSize {
		candidate := pageIndex * query.PageSize
		if candidate < total {
			start = candidate
		}
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	return PlayerMediaItemPage{List: all[start:end], Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

func (s *MediaLibraryService) ensurePlayerMediaLibraryReadable(actor Actor, libraryID uint) error {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	var count int64
	err := s.db.Table("media_libraries").
		Joins("JOIN storages ON storages.id = media_libraries.storage_id").
		Where("media_libraries.id = ? AND media_libraries.enabled = ? AND storages.enabled = ?", libraryID, true, true).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return mediaLibraryNotFound(gorm.ErrRecordNotFound)
	}
	return nil
}

func (s *MediaLibraryService) playerMediaItem(libraryID uint, item MediaCatalogItem) (PlayerMediaItem, error) {
	workKey, err := decodeCatalogToken(item.ID)
	if err != nil {
		return PlayerMediaItem{}, err
	}
	var recognition models.MediaLibraryRecognition
	err = s.db.Table("media_library_recognitions").
		Joins("JOIN media_library_entries ON media_library_entries.recognition_id = media_library_recognitions.id").
		Where("media_library_entries.library_id = ? AND media_library_entries.work_key = ?", libraryID, workKey).
		Order("media_library_recognitions.updated_at DESC").First(&recognition).Error
	var snapshot tmdb.Snapshot
	if err == nil && recognition.MetadataJSON != "" && recognition.MetadataJSON != "{}" {
		_, snapshot, err = decodeRecognitionMetadata(recognition.MetadataJSON)
		if err != nil {
			return PlayerMediaItem{}, err
		}
	} else if err != nil && err != gorm.ErrRecordNotFound {
		return PlayerMediaItem{}, err
	}
	identity := PlayerMediaIdentity{Scheme: "server", MediaType: item.Kind, Value: fmt.Sprintf("library:%d:work:%s", libraryID, item.ID)}
	if item.TMDBID != nil {
		identity = PlayerMediaIdentity{Scheme: "tmdb", MediaType: item.Kind, Value: strconv.FormatInt(*item.TMDBID, 10)}
	}
	return PlayerMediaItem{
		ID: item.ID, LibraryID: libraryID, Title: item.Title, OriginalTitle: snapshot.OriginalTitle,
		Kind: item.Kind, ReleaseYear: item.ReleaseYear, Overview: snapshot.Overview, Tagline: snapshot.Tagline,
		Rating: snapshot.VoteAverage, RuntimeMinutes: snapshot.RuntimeMinutes, Genres: genreNames(snapshot.Genres),
		Directors: personNames(snapshot.Directors), Writers: personNames(snapshot.Writers), Cast: personNames(snapshot.Cast),
		TMDBID: snapshot.TMDBID, IMDbID: snapshot.IMDbID, PosterPath: safeTMDBImagePath(snapshot.PosterPath), BackdropPath: safeTMDBImagePath(snapshot.BackdropPath),
		StillPaths: snapshotStillPaths(snapshot), WorkIdentity: identity, FileCount: item.FileCount,
		SeasonCount: item.SeasonCount, EpisodeCount: item.EpisodeCount, ModifiedAt: item.ModifiedAt,
		CategoryName: item.CategoryName, MatchStatus: item.MatchStatus,
	}, nil
}

func genreNames(genres []tmdb.Genre) []string {
	result := make([]string, 0, len(genres))
	seen := make(map[string]struct{}, len(genres))
	for _, genre := range genres {
		name := strings.TrimSpace(genre.Name)
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
		if len(result) == 100 {
			break
		}
	}
	return result
}

func personNames(people []tmdb.Person) []string {
	result := make([]string, 0, len(people))
	seen := make(map[string]struct{}, len(people))
	for _, person := range people {
		name := strings.TrimSpace(person.Name)
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
		if len(result) == 100 {
			break
		}
	}
	return result
}

func snapshotStillPaths(snapshot tmdb.Snapshot) []string {
	paths := append([]string{snapshot.BackdropPath}, snapshot.BackdropPaths...)
	result := make([]string, 0, 8)
	seen := make(map[string]struct{}, 8)
	for _, path := range paths {
		path = safeTMDBImagePath(path)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
		if len(result) == 8 {
			break
		}
	}
	return result
}

func safeTMDBImagePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#\\\r\n") || strings.Contains(value, "..") {
		return ""
	}
	return value
}

func (s *ConnectionService) PlayerEmbyInstances(actor Actor) ([]PlayerEmbyInstance, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return []PlayerEmbyInstance{}, nil
	}
	var records []models.Connection
	if err := s.db.Where("provider = ? AND enabled = ? AND account_id <> ''", models.ConnectionProviderEmby, true).Order("name_normalized, id").Find(&records).Error; err != nil {
		return nil, err
	}
	result := make([]PlayerEmbyInstance, 0, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.AccountID) == "" {
			continue
		}
		result = append(result, PlayerEmbyInstance{ConnectionID: record.ID, Name: record.Name, HealthStatus: record.LastHealthStatus, InstanceFingerprint: EmbyInstanceFingerprint(record.AccountID)})
	}
	return result, nil
}

func EmbyInstanceFingerprint(systemID string) string {
	value := "ohmycine:emby-instance:v1\x00" + strings.ToLower(strings.TrimSpace(systemID))
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *SignedProxyService) ResolvePlayerEntry(ctx context.Context, actor Actor, entryID uint, userAgent, remoteAddr string) (PlayerStreamResolution, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return PlayerStreamResolution{}, appError(CodePermissionDenied, "无权播放该媒体", nil)
	}
	var entry models.MediaLibraryEntry
	if err := s.db.First(&entry, entryID).Error; err != nil {
		return PlayerStreamResolution{}, appError(CodeNotFound, "媒体文件不存在", err)
	}
	var library models.MediaLibrary
	if err := s.db.First(&library, entry.LibraryID).Error; err != nil || !library.Enabled {
		return PlayerStreamResolution{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	var storage models.Storage
	if err := s.db.First(&storage, library.StorageID).Error; err != nil || !storage.Enabled {
		return PlayerStreamResolution{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	if storage.Type == models.StorageTypeLocal {
		file, info, err := openLocalPlayerEntry(s.db, entry)
		if err != nil {
			return PlayerStreamResolution{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
		}
		return PlayerStreamResolution{Kind: playerStreamKindLocal, File: file, Name: filepath.Base(filepath.FromSlash(entry.RelativePath)), ModifiedAt: info.ModTime()}, nil
	}
	if storage.Type != models.StorageTypePan115 || !library.STRMEnabled || !library.SignedProxyEnabled {
		return PlayerStreamResolution{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", nil)
	}
	var artifact models.MediaArtifact
	if err := s.db.Where("library_id = ? AND source_identity = ? AND kind = ? AND target_kind = ? AND managed = ? AND active = ? AND status = ?", entry.LibraryID, fmt.Sprintf("entry:%d", entry.ID), models.MediaArtifactKindSTRM, models.MediaArtifactTargetLocalProjection, true, true, models.MediaArtifactStatusCompleted).
		Order("updated_at DESC").First(&artifact).Error; err != nil {
		return PlayerStreamResolution{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	redirect, err := s.ResolveArtifactForClient(ctx, artifact.OpaqueID, userAgent, remoteAddr)
	if err != nil {
		return PlayerStreamResolution{}, err
	}
	return PlayerStreamResolution{Kind: playerStreamKindRedirect, RedirectURL: redirect.URL}, nil
}

func openLocalPlayerEntry(db *gorm.DB, entry models.MediaLibraryEntry) (*os.File, os.FileInfo, error) {
	var library models.MediaLibrary
	if err := db.First(&library, entry.LibraryID).Error; err != nil || !library.Enabled {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	var storage models.Storage
	if err := db.First(&storage, library.StorageID).Error; err != nil || !storage.Enabled || storage.Type != models.StorageTypeLocal {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	libraryRoot, err := medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)
	if err != nil {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	relative := strings.TrimSpace(strings.ReplaceAll(entry.RelativePath, "\\", "/"))
	if relative == "" || strings.Contains(relative, "\x00") || strings.HasPrefix(relative, "//") {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", nil)
	}
	relative = strings.TrimLeft(relative, "/")
	cleanRelative := filepath.Clean(filepath.FromSlash(relative))
	if cleanRelative == "." || cleanRelative == ".." || filepath.IsAbs(cleanRelative) || filepath.VolumeName(cleanRelative) != "" || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", nil)
	}
	target, err := storagefs.Constrain(libraryRoot, filepath.Join(libraryRoot, cleanRelative))
	if err != nil {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	current := libraryRoot
	rootInfo, err := os.Lstat(current)
	if err != nil || !rootInfo.IsDir() || storagefs.IsReparsePoint(current, rootInfo) {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	for _, part := range strings.Split(cleanRelative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil || storagefs.IsReparsePoint(current, info) {
			return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", statErr)
		}
		if current != target && !info.IsDir() {
			return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", nil)
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(libraryRoot)
	if err != nil {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	if _, err := storagefs.Constrain(resolvedRoot, resolved); err != nil || storagefs.NormalizeForComparison(resolved) != storagefs.NormalizeForComparison(filepath.Join(resolvedRoot, cleanRelative)) {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	return file, info, nil
}
