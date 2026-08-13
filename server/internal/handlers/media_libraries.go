package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type mediaLibraryPayload struct {
	Name                  string   `json:"name"`
	StorageID             uint     `json:"storage_id"`
	ProfileID             uint     `json:"profile_id"`
	RelativeRoot          string   `json:"relative_root"`
	RelativeRootToken     string   `json:"relative_root_token"`
	Enabled               *bool    `json:"enabled"`
	Recursive             *bool    `json:"recursive"`
	FullScanIntervalHours int      `json:"full_scan_interval_hours"`
	IncrementalMinutes    int      `json:"incremental_minutes"`
	VideoExtensions       []string `json:"video_extensions"`
	IgnorePatterns        []string `json:"ignore_patterns"`
	MetadataLanguage      string   `json:"metadata_language"`
	MetadataRegion        string   `json:"metadata_region"`
	MatchStrategy         string   `json:"match_strategy"`
	ProviderRatePerSecond int      `json:"provider_rate_per_second"`
	ProviderConcurrency   int      `json:"provider_concurrency"`
	MetadataRatePerSecond int      `json:"metadata_rate_per_second"`
	MetadataConcurrency   int      `json:"metadata_concurrency"`
	STRMEnabled           bool     `json:"strm_enabled"`
	STRMLocalRootToken    string   `json:"strm_local_root_token"`
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
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	items, err := a.libraries.Entries(actor, id, limit)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
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

func (a *API) mediaLibraryInput(c *gin.Context, actor services.Actor, libraryID uint, p mediaLibraryPayload) (services.MediaLibraryInput, error) {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	recursive := true
	if p.Recursive != nil {
		recursive = *p.Recursive
	}
	relative := p.RelativeRoot
	if p.RelativeRootToken != "" {
		resolved, err := a.directory.ResolveStorageRelativeSelection(c.Request.Context(), actor, p.StorageID, p.RelativeRootToken)
		if err != nil {
			return services.MediaLibraryInput{}, err
		}
		relative = resolved
	}
	if p.RelativeRootToken == "" {
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
	if p.STRMLocalRootToken != "" {
		return services.MediaLibraryInput{}, invalid("当前本地 Storage 不支持 STRM 投影", nil)
	}
	return services.MediaLibraryInput{Name: p.Name, StorageID: p.StorageID, ProfileID: p.ProfileID, RelativeRoot: relative, Enabled: enabled, Recursive: recursive, FullScanIntervalHours: p.FullScanIntervalHours, IncrementalMinutes: p.IncrementalMinutes, VideoExtensions: p.VideoExtensions, IgnorePatterns: p.IgnorePatterns, MetadataLanguage: p.MetadataLanguage, MetadataRegion: p.MetadataRegion, MatchStrategy: p.MatchStrategy, ProviderRatePerSecond: p.ProviderRatePerSecond, ProviderConcurrency: p.ProviderConcurrency, MetadataRatePerSecond: p.MetadataRatePerSecond, MetadataConcurrency: p.MetadataConcurrency, STRMEnabled: p.STRMEnabled}, nil
}
