package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type mediaLibraryPayload struct {
	Name                     string   `json:"name"`
	StorageID                uint     `json:"storage_id"`
	ProfileID                uint     `json:"profile_id"`
	RelativeRoot             string   `json:"relative_root"`
	RelativeRootToken        string   `json:"relative_root_token"`
	Enabled                  *bool    `json:"enabled"`
	Recursive                *bool    `json:"recursive"`
	FullScanIntervalHours    int      `json:"full_scan_interval_hours"`
	IncrementalMinutes       int      `json:"incremental_minutes"`
	VideoExtensions          []string `json:"video_extensions"`
	STRMAssetExtraExtensions []string `json:"strm_asset_extra_extensions"`
	IgnorePatterns           []string `json:"ignore_patterns"`
	MetadataLanguage         string   `json:"metadata_language"`
	MetadataRegion           string   `json:"metadata_region"`
	MatchStrategy            string   `json:"match_strategy"`
	ProviderRatePerSecond    int      `json:"provider_rate_per_second"`
	ProviderConcurrency      int      `json:"provider_concurrency"`
	MetadataRatePerSecond    int      `json:"metadata_rate_per_second"`
	MetadataConcurrency      int      `json:"metadata_concurrency"`
	STRMEnabled              bool     `json:"strm_enabled"`
	STRMLocalRootToken       string   `json:"strm_local_root_token"`
	MetadataArtifactsEnabled *bool    `json:"metadata_artifacts_enabled"`
	UploadSidecars           bool     `json:"upload_sidecars"`
	TransferMode             string   `json:"transfer_mode"`
	ConflictPolicy           string   `json:"conflict_policy"`
	MovieDirectoryTemplate   string   `json:"movie_directory_template"`
	MovieFilenameTemplate    string   `json:"movie_filename_template"`
	TVDirectoryTemplate      string   `json:"tv_directory_template"`
	TVFilenameTemplate       string   `json:"tv_filename_template"`
	IngestEnabled            bool     `json:"ingest_enabled"`
	IngestDownloaderID       string   `json:"ingest_downloader_id"`
	IngestRelativeRoot       string   `json:"ingest_relative_root"`
	IngestRelativeRootToken  string   `json:"ingest_relative_root_token"`
}

func (a *API) ReorderMediaLibraries(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var payload struct {
		IDs []uint `json:"ids"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("媒体库顺序无效", err))
		return
	}
	items, err := a.libraries.Reorder(actor, payload.IDs, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) MediaLibraries(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.libraries.List(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}
func (a *API) MediaLibrary(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := a.libraries.Get(actor, id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
func (a *API) CreateMediaLibrary(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var payload mediaLibraryPayload
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("媒体库配置无效", err))
		return
	}
	input, err := a.mediaLibraryInput(c, actor, 0, payload)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	item, err := a.libraries.Create(c.Request.Context(), actor, input, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, item)
}
func (a *API) UpdateMediaLibrary(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload mediaLibraryPayload
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("媒体库配置无效", err))
		return
	}
	input, err := a.mediaLibraryInput(c, actor, id, payload)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	item, err := a.libraries.Update(c.Request.Context(), actor, id, input, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
func (a *API) DeleteMediaLibrary(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.libraries.Delete(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}
func (a *API) ScanMediaLibrary(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	run, err := a.libraries.ScanNow(c.Request.Context(), actor, id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, run)
}
func (a *API) RetryMediaLibrary(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.libraries.Retry(actor, id); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusAccepted, gin.H{"retrying": true})
}
func (a *API) MediaLibraryEntries(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	query, err := mediaPageQuery(c, true)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	page, err := a.libraries.EntryPage(actor, id, query)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, page)
}
func (a *API) MediaLibraryRecognitions(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	query, err := mediaPageQuery(c, false)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	page, err := a.libraries.Recognitions(actor, id, query, c.Query("status"), c.Query("manual_only") == "true")
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, page)
}
func (a *API) RetryMediaLibraryRecognition(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := a.libraries.RetryRecognition(c.Request.Context(), actor, id, c.Param("token"), middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
func (a *API) MediaLibraryRecognitionCandidates(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var year *int
	if raw := c.Query("year"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1888 || value > 2200 {
			writeError(c, a.log, invalid("年份无效", err))
			return
		}
		year = &value
	}
	items, err := a.libraries.RecognitionCandidates(c.Request.Context(), actor, id, c.Param("token"), c.Query("title"), c.Query("media_type"), year)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}
func (a *API) OverrideMediaLibraryRecognition(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload struct {
		TMDBID    int64  `json:"tmdb_id"`
		MediaType string `json:"media_type"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("TMDB 匹配选择无效", err))
		return
	}
	item, err := a.libraries.OverrideRecognition(c.Request.Context(), actor, id, c.Param("token"), services.MediaRecognitionOverrideInput{TMDBID: payload.TMDBID, MediaType: payload.MediaType}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
func (a *API) ClearMediaLibraryRecognitionOverride(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := a.libraries.ClearRecognitionOverride(c.Request.Context(), actor, id, c.Param("token"), middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
func (a *API) MediaLibraryCatalog(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	query, err := mediaPageQuery(c, false)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	page, err := a.libraries.Catalog(actor, id, query)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, page)
}
func (a *API) MediaLibraryCatalogDetail(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	detail, err := a.libraries.CatalogDetail(actor, id, c.Param("work"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, detail)
}
func (a *API) MediaLibraryRuns(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	items, err := a.libraries.Runs(actor, id, limit)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func mediaPageQuery(c *gin.Context, legacyLimit bool) (services.MediaPageQuery, error) {
	query := services.MediaPageQuery{Query: c.Query("query"), MediaType: c.Query("media_type"), MatchStatus: c.Query("match_status"), Category: c.Query("category")}
	var err error
	if raw, exists := c.GetQuery("page"); exists {
		query.Page, err = strconv.Atoi(raw)
		if err != nil {
			return services.MediaPageQuery{}, invalid("分页参数无效", err)
		}
	}
	if raw, exists := c.GetQuery("page_size"); exists {
		query.PageSize, err = strconv.Atoi(raw)
		if err != nil {
			return services.MediaPageQuery{}, invalid("分页参数无效", err)
		}
	} else if legacyLimit {
		if raw, exists := c.GetQuery("limit"); exists {
			limit, parseErr := strconv.Atoi(raw)
			if parseErr != nil || limit < 1 {
				return services.MediaPageQuery{}, invalid("分页参数无效", parseErr)
			}
			switch {
			case limit <= 20:
				query.PageSize = 20
			case limit <= 50:
				query.PageSize = 50
			default:
				query.PageSize = 100
			}
		}
	}
	return query, nil
}

func (a *API) mediaLibraryInput(c *gin.Context, actor services.Actor, libraryID uint, p mediaLibraryPayload) (services.MediaLibraryInput, error) {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	recursive := true
	if p.Recursive != nil {
		recursive = *p.Recursive
	}
	storage, err := a.storage.Get(actor, p.StorageID)
	if err != nil {
		return services.MediaLibraryInput{}, err
	}
	relative := p.RelativeRoot
	providerRootID := ""
	if storage.Type == "pan115" {
		if p.RelativeRootToken != "" {
			selection, err := a.providerDirectory.ResolveStorageSelection(c.Request.Context(), actor, p.StorageID, p.RelativeRootToken)
			if err != nil {
				return services.MediaLibraryInput{}, err
			}
			relative = selection.RelativeRoot
			providerRootID = selection.ProviderID
		} else {
			if libraryID == 0 {
				return services.MediaLibraryInput{}, invalid("必须通过 115 目录选择器选择媒体库来源目录", nil)
			}
			existing, err := a.libraries.Get(actor, libraryID)
			if err != nil {
				return services.MediaLibraryInput{}, err
			}
			if p.StorageID != existing.StorageID || (p.RelativeRoot != "" && p.RelativeRoot != existing.RelativeRoot) {
				return services.MediaLibraryInput{}, invalid("更改 Storage 或来源目录必须重新使用 115 目录选择器", nil)
			}
			relative = existing.RelativeRoot
			providerRootID = existing.ProviderRootID
		}
	} else if p.RelativeRootToken != "" {
		resolved, err := a.directory.ResolveStorageRelativeSelection(c.Request.Context(), actor, p.StorageID, p.RelativeRootToken)
		if err != nil {
			return services.MediaLibraryInput{}, err
		}
		relative = resolved
	}
	if storage.Type != "pan115" && p.RelativeRootToken == "" {
		if libraryID == 0 {
			return services.MediaLibraryInput{}, invalid("必须通过 Server 目录选择器选择媒体库来源目录", nil)
		}
		existing, err := a.libraries.Get(actor, libraryID)
		if err != nil {
			return services.MediaLibraryInput{}, err
		}
		if p.StorageID != existing.StorageID || (p.RelativeRoot != "" && p.RelativeRoot != existing.RelativeRoot) {
			return services.MediaLibraryInput{}, invalid("更改 Storage 或来源目录必须重新使用 Server 目录选择器", nil)
		}
		relative = existing.RelativeRoot
	}
	strmLocalRoot := ""
	if p.STRMEnabled {
		if p.STRMLocalRootToken != "" {
			resolved, err := a.directory.ResolveSelection(c.Request.Context(), actor, p.STRMLocalRootToken)
			if err != nil {
				return services.MediaLibraryInput{}, err
			}
			strmLocalRoot = resolved
		} else {
			if libraryID == 0 {
				return services.MediaLibraryInput{}, invalid("启用 STRM 必须通过 Server 目录选择器选择本地投影目录", nil)
			}
			existing, err := a.libraries.Get(actor, libraryID)
			if err != nil {
				return services.MediaLibraryInput{}, err
			}
			if !existing.STRMEnabled || existing.STRMLocalPath == "" {
				return services.MediaLibraryInput{}, invalid("启用 STRM 必须重新选择本地投影目录", nil)
			}
			strmLocalRoot = existing.STRMLocalPath
		}
	} else if p.STRMLocalRootToken != "" {
		return services.MediaLibraryInput{}, invalid("未启用 STRM 时不能选择本地投影目录", nil)
	}
	// MediaLibrary-level intake is a legacy read/worker contract. New writes
	// configure manual 115 adoption on the Downloader instead. Preserve an
	// existing snapshot verbatim so editing an unrelated library setting cannot
	// silently disable an in-flight legacy route; ignore legacy intake fields
	// supplied by new create/update payloads.
	legacyIngestEnabled, legacyIngestDownloaderID := false, ""
	legacyIngestProviderRootID, legacyIngestRelativeRoot := "", ""
	if libraryID != 0 {
		existing, err := a.libraries.Get(actor, libraryID)
		if err != nil {
			return services.MediaLibraryInput{}, err
		}
		legacyIngestEnabled = existing.IngestEnabled
		if existing.IngestDownloaderID != nil {
			legacyIngestDownloaderID = *existing.IngestDownloaderID
		}
		legacyIngestProviderRootID = existing.IngestProviderRootID
		legacyIngestRelativeRoot = existing.IngestRelativeRoot
	}
	return services.MediaLibraryInput{Name: p.Name, StorageID: p.StorageID, ProfileID: p.ProfileID, RelativeRoot: relative, ProviderRootID: providerRootID, Enabled: enabled, Recursive: recursive, FullScanIntervalHours: p.FullScanIntervalHours, IncrementalMinutes: p.IncrementalMinutes, VideoExtensions: p.VideoExtensions, STRMAssetExtraExtensions: p.STRMAssetExtraExtensions, IgnorePatterns: p.IgnorePatterns, MetadataLanguage: p.MetadataLanguage, MetadataRegion: p.MetadataRegion, MatchStrategy: p.MatchStrategy, ProviderRatePerSecond: p.ProviderRatePerSecond, ProviderConcurrency: p.ProviderConcurrency, MetadataRatePerSecond: p.MetadataRatePerSecond, MetadataConcurrency: p.MetadataConcurrency, STRMEnabled: p.STRMEnabled, STRMLocalRoot: strmLocalRoot, MetadataArtifactsEnabled: p.MetadataArtifactsEnabled, UploadSidecars: p.UploadSidecars, TransferMode: p.TransferMode, ConflictPolicy: p.ConflictPolicy, MovieDirectoryTemplate: p.MovieDirectoryTemplate, MovieFilenameTemplate: p.MovieFilenameTemplate, TVDirectoryTemplate: p.TVDirectoryTemplate, TVFilenameTemplate: p.TVFilenameTemplate, IngestEnabled: legacyIngestEnabled, IngestDownloaderID: legacyIngestDownloaderID, IngestProviderRootID: legacyIngestProviderRootID, IngestRelativeRoot: legacyIngestRelativeRoot}, nil
}
