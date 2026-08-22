package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
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
	ID           string              `json:"id"`
	LibraryID    uint                `json:"library_id"`
	Title        string              `json:"title"`
	Kind         string              `json:"kind"`
	ReleaseYear  *int                `json:"release_year,omitempty"`
	Overview     string              `json:"overview,omitempty"`
	PosterPath   string              `json:"poster_path,omitempty"`
	BackdropPath string              `json:"backdrop_path,omitempty"`
	WorkIdentity PlayerMediaIdentity `json:"work_identity"`
	FileCount    int64               `json:"file_count"`
	SeasonCount  int64               `json:"season_count"`
	EpisodeCount int64               `json:"episode_count"`
	ModifiedAt   time.Time           `json:"modified_at"`
	CategoryName string              `json:"category_name"`
	MatchStatus  string              `json:"match_status"`
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
		result = append(result, PlayerMediaLibrary{ID: row.ID, Name: row.Name, StorageType: row.StorageType, SortOrder: row.SortOrder, Status: row.Status, EntryCount: row.EntryCount, DirectStream: row.StorageType == models.StorageTypePan115 && row.STRMEnabled && row.SignedProxyEnabled, STRMEnabled: row.STRMEnabled, LastSuccessful: row.LastSuccessfulScanAt})
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
	directStreamEligible, err := s.playerDirectStreamEligible(libraryID)
	if err != nil {
		return PlayerMediaDetail{}, err
	}
	var entries []models.MediaLibraryEntry
	if err := s.db.Where("library_id = ? AND work_key = ?", libraryID, workKey).Order("COALESCE(season, 0), COALESCE(episode, 0), relative_path").Find(&entries).Error; err != nil {
		return PlayerMediaDetail{}, err
	}
	versions := make([]PlayerMediaVersion, 0, len(entries))
	for _, entry := range entries {
		var artifact models.MediaArtifact
		err := gorm.ErrRecordNotFound
		if directStreamEligible {
			err = s.db.Where("library_id = ? AND source_identity = ? AND kind = ? AND target_kind = ? AND managed = ? AND active = ? AND status = ?", entry.LibraryID, fmt.Sprintf("entry:%d", entry.ID), models.MediaArtifactKindSTRM, models.MediaArtifactTargetLocalProjection, true, true, models.MediaArtifactStatusCompleted).
				Order("updated_at DESC").First(&artifact).Error
		}
		playable := err == nil
		if err != nil && err != gorm.ErrRecordNotFound {
			return PlayerMediaDetail{}, err
		}
		exactIdentity := "server:entry:" + strconv.FormatUint(uint64(entry.ID), 10)
		streamPath := ""
		if playable {
			exactIdentity = "ohmycine:artifact:" + artifact.OpaqueID
			streamPath = "/api/v1/player/media-entries/" + strconv.FormatUint(uint64(entry.ID), 10) + "/stream"
		}
		versions = append(versions, PlayerMediaVersion{ID: entry.ID, Title: entry.Title, Season: entry.Season, Episode: entry.Episode, Size: entry.Size, ModifiedAt: entry.ModifiedAt, Playable: playable, StreamPath: streamPath, ExactIdentity: exactIdentity})
	}
	return PlayerMediaDetail{Item: item, Versions: versions}, nil
}

func (s *MediaLibraryService) playerDirectStreamEligible(libraryID uint) (bool, error) {
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
		return false, err
	}
	return item.LibraryEnabled && item.StorageEnabled && item.StorageType == models.StorageTypePan115 && item.STRMEnabled && item.SignedProxyEnabled, nil
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
	return PlayerMediaItem{ID: item.ID, LibraryID: libraryID, Title: item.Title, Kind: item.Kind, ReleaseYear: item.ReleaseYear, Overview: snapshot.Overview, PosterPath: snapshot.PosterPath, BackdropPath: snapshot.BackdropPath, WorkIdentity: identity, FileCount: item.FileCount, SeasonCount: item.SeasonCount, EpisodeCount: item.EpisodeCount, ModifiedAt: item.ModifiedAt, CategoryName: item.CategoryName, MatchStatus: item.MatchStatus}, nil
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

func (s *SignedProxyService) ResolvePlayerEntry(ctx context.Context, actor Actor, entryID uint, userAgent, remoteAddr string) (ProxyRedirect, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return ProxyRedirect{}, appError(CodePermissionDenied, "无权播放该媒体", nil)
	}
	var entry models.MediaLibraryEntry
	if err := s.db.First(&entry, entryID).Error; err != nil {
		return ProxyRedirect{}, appError(CodeNotFound, "媒体文件不存在", err)
	}
	var library models.MediaLibrary
	if err := s.db.First(&library, entry.LibraryID).Error; err != nil || !library.Enabled {
		return ProxyRedirect{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	var artifact models.MediaArtifact
	if err := s.db.Where("library_id = ? AND source_identity = ? AND kind = ? AND target_kind = ? AND managed = ? AND active = ? AND status = ?", entry.LibraryID, fmt.Sprintf("entry:%d", entry.ID), models.MediaArtifactKindSTRM, models.MediaArtifactTargetLocalProjection, true, true, models.MediaArtifactStatusCompleted).
		Order("updated_at DESC").First(&artifact).Error; err != nil {
		return ProxyRedirect{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	return s.ResolveArtifactForClient(ctx, artifact.OpaqueID, userAgent, remoteAddr)
}
