package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type siteWritePayload struct {
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	BaseURL            string `json:"base_url"`
	Cookie             string `json:"cookie"`
	Passkey            string `json:"passkey"`
	APIKey             string `json:"api_key"`
	UserAgent          string `json:"user_agent"`
	BrowserEmulation   bool   `json:"browser_emulation"`
	BrowserServiceURL  string `json:"browser_service_url"`
	Enabled            *bool  `json:"enabled"`
	Priority           int    `json:"priority"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	RateLimitPerMinute int    `json:"rate_limit_per_minute"`
}

func (a *API) Sites(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.sites.List(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) SiteCatalog(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.sites.Catalog(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) DiscoverySearchOptions(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.sites.SearchOptions(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) ResolveBTSite(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var payload struct {
		SiteType string `json:"site_type"`
		BaseURL  string `json:"base_url"`
	}
	if err := strictJSON(c, &payload); err != nil || strings.ToLower(strings.TrimSpace(payload.SiteType)) != "bt" {
		writeError(c, a.log, invalid("BT 站点地址无效", err))
		return
	}
	item, err := a.sites.ResolveBT(actor, payload.BaseURL)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) CreateSite(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var payload siteWritePayload
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("站点配置无效", err))
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	item, err := a.sites.Create(c.Request.Context(), actor, services.SiteInput{Name: payload.Name, Kind: payload.Kind, BaseURL: payload.BaseURL, Cookie: payload.Cookie, Passkey: payload.Passkey, APIKey: payload.APIKey, UserAgent: payload.UserAgent, BrowserEmulation: payload.BrowserEmulation, BrowserServiceURL: payload.BrowserServiceURL, Enabled: enabled, Priority: payload.Priority, TimeoutSeconds: payload.TimeoutSeconds, RateLimitPerMinute: payload.RateLimitPerMinute}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, item)
}

func (a *API) UpdateSite(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var payload struct {
		Name               *string `json:"name"`
		BaseURL            *string `json:"base_url"`
		Cookie             *string `json:"cookie"`
		Passkey            *string `json:"passkey"`
		ClearPasskey       bool    `json:"clear_passkey"`
		APIKey             *string `json:"api_key"`
		ClearAPIKey        bool    `json:"clear_api_key"`
		UserAgent          *string `json:"user_agent"`
		BrowserEmulation   *bool   `json:"browser_emulation"`
		BrowserServiceURL  *string `json:"browser_service_url"`
		Enabled            *bool   `json:"enabled"`
		Priority           *int    `json:"priority"`
		TimeoutSeconds     *int    `json:"timeout_seconds"`
		RateLimitPerMinute *int    `json:"rate_limit_per_minute"`
		Revision           uint64  `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("站点配置无效", err))
		return
	}
	item, err := a.sites.Update(c.Request.Context(), actor, id, services.SiteUpdateInput{Name: payload.Name, BaseURL: payload.BaseURL, Cookie: payload.Cookie, Passkey: payload.Passkey, ClearPasskey: payload.ClearPasskey, APIKey: payload.APIKey, ClearAPIKey: payload.ClearAPIKey, UserAgent: payload.UserAgent, BrowserEmulation: payload.BrowserEmulation, BrowserServiceURL: payload.BrowserServiceURL, Enabled: payload.Enabled, Priority: payload.Priority, TimeoutSeconds: payload.TimeoutSeconds, RateLimitPerMinute: payload.RateLimitPerMinute, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) TestSite(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := a.sites.Test(c.Request.Context(), actor, id, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) DeleteSite(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.sites.Delete(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func parseSearchSiteScope(c *gin.Context) (*uint, []uint, error) {
	rawSiteID, hasSiteID := c.GetQuery("site_id")
	rawSiteIDs, hasSiteIDs := c.Request.URL.Query()["site_ids"]
	if hasSiteID && hasSiteIDs {
		return nil, nil, invalid("单站筛选和多站筛选不能同时使用", nil)
	}
	if hasSiteID {
		id, err := strconv.ParseUint(strings.TrimSpace(rawSiteID), 10, 32)
		if err != nil || id == 0 {
			return nil, nil, invalid("站点筛选无效", err)
		}
		parsed := uint(id)
		return &parsed, nil, nil
	}
	if !hasSiteIDs {
		return nil, nil, nil
	}
	values := make([]string, 0, len(rawSiteIDs))
	for _, raw := range rawSiteIDs {
		for _, value := range strings.Split(raw, ",") {
			values = append(values, strings.TrimSpace(value))
		}
	}
	if len(values) == 0 || len(values) > 64 {
		return nil, nil, invalid("请选择 1 到 64 个搜索站点", nil)
	}
	result := make([]uint, 0, len(values))
	seen := make(map[uint]struct{}, len(values))
	for _, value := range values {
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil || id == 0 {
			return nil, nil, invalid("站点筛选无效", err)
		}
		parsed := uint(id)
		if _, exists := seen[parsed]; exists {
			continue
		}
		seen[parsed] = struct{}{}
		result = append(result, parsed)
	}
	if len(result) == 0 {
		return nil, nil, invalid("请至少选择一个搜索站点", nil)
	}
	return nil, result, nil
}

func parsePTSearch(c *gin.Context) (services.SiteSearchInput, error) {
	input := services.SiteSearchInput{Keyword: strings.TrimSpace(c.Query("keyword")), MediaType: strings.TrimSpace(c.Query("media_type")), SearchBy: strings.TrimSpace(c.Query("search_by")), Page: 1}
	if value := strings.TrimSpace(c.Query("tmdb_id")); value != "" {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return input, invalid("TMDB 搜索身份无效", err)
		}
		input.TMDBID = &id
	}
	if value := strings.TrimSpace(c.Query("page")); value != "" {
		page, err := strconv.Atoi(value)
		if err != nil {
			return input, invalid("种子资源搜索页码无效", err)
		}
		input.Page = page
	}
	if value := strings.TrimSpace(c.Query("year")); value != "" {
		year, err := strconv.Atoi(value)
		if err != nil || year < 1880 || year > 2200 {
			return input, invalid("种子资源搜索年份无效", err)
		}
		input.Year = &year
	}
	siteID, siteIDs, err := parseSearchSiteScope(c)
	if err != nil {
		return input, err
	}
	input.SiteID, input.SiteIDs = siteID, siteIDs
	return input, nil
}

func (a *API) PTSearch(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	input, err := parsePTSearch(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	items, err := a.sites.Search(c.Request.Context(), actor, input)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"groups": items})
}

func (a *API) TorrentSearch(c *gin.Context) { a.PTSearch(c) }

func parseMediaIdentitySearch(c *gin.Context) (services.MediaIdentitySearchInput, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("tmdbID")), 10, 64)
	input := services.MediaIdentitySearchInput{MediaType: strings.ToLower(strings.TrimSpace(c.Param("mediaType"))), TMDBID: id, Page: 1}
	if err != nil || id <= 0 || (input.MediaType != "movie" && input.MediaType != "tv") {
		return input, invalid("TMDB 搜索身份无效", err)
	}
	if value := strings.TrimSpace(c.Query("page")); value != "" {
		page, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			return input, invalid("种子资源搜索页码无效", parseErr)
		}
		input.Page = page
	}
	if value := strings.TrimSpace(c.Query("season")); value != "" {
		season, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			return input, invalid("资源搜索季数无效", parseErr)
		}
		input.Season = &season
	}
	if input.SiteID, input.SiteIDs, err = parseSearchSiteScope(c); err != nil {
		return input, err
	}
	return input, nil
}

func (a *API) MediaIdentitySearch(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	input, err := parseMediaIdentitySearch(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	result, err := a.sites.SearchMediaIdentity(c.Request.Context(), actor, input)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}

func (a *API) MediaIdentitySearchStream(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	input, err := parseMediaIdentitySearch(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		writeError(c, a.log, &services.AppError{Code: services.CodeSiteUnavailable, Message: "当前 HTTP 服务不支持流式种子资源搜索"})
		return
	}
	started := false
	err = a.sites.SearchMediaIdentityEach(c.Request.Context(), actor, input, func(result services.MediaIdentitySearchResult) {
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		c.Header("Cache-Control", "no-store")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
		started = true
		meta, _ := json.Marshal(gin.H{"media_type": result.MediaType, "tmdb_id": result.TMDBID, "title": result.Title, "year": result.Year, "query_names": result.QueryNames})
		_, _ = fmt.Fprintf(c.Writer, "event: media\ndata: %s\n\n", meta)
		flusher.Flush()
	}, func(group services.SiteSearchGroup) {
		if c.Request.Context().Err() != nil {
			return
		}
		payload, marshalErr := json.Marshal(group)
		if marshalErr != nil {
			return
		}
		_, _ = fmt.Fprintf(c.Writer, "event: site\ndata: %s\n\n", payload)
		flusher.Flush()
	})
	if err != nil {
		if !started {
			writeError(c, a.log, err)
		}
		return
	}
	_, _ = fmt.Fprint(c.Writer, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

func (a *API) PTSearchStream(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	input, err := parsePTSearch(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		writeError(c, a.log, &services.AppError{Code: services.CodeSiteUnavailable, Message: "当前 HTTP 服务不支持流式种子资源搜索"})
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher.Flush()
	var writeMu sync.Mutex
	err = a.sites.SearchEach(c.Request.Context(), actor, input, func(group services.SiteSearchGroup) {
		if c.Request.Context().Err() != nil {
			return
		}
		payload, marshalErr := json.Marshal(group)
		if marshalErr != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		if c.Request.Context().Err() != nil {
			return
		}
		_, _ = fmt.Fprintf(c.Writer, "event: site\ndata: %s\n\n", payload)
		flusher.Flush()
	})
	if c.Request.Context().Err() != nil {
		return
	}
	if err != nil {
		payload, _ := json.Marshal(gin.H{"code": services.ErrorCode(err), "message": "种子资源搜索失败"})
		_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload)
		flusher.Flush()
		return
	}
	_, _ = fmt.Fprint(c.Writer, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

func (a *API) TorrentSearchStream(c *gin.Context) { a.PTSearchStream(c) }

func (a *API) CreateDiscoveryDownload(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		ResultToken    string `json:"result_token"`
		DownloaderID   string `json:"downloader_id"`
		MediaLibraryID *uint  `json:"media_library_id"`
		ProfileID      uint   `json:"profile_id"`
		Priority       int    `json:"priority"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("种子资源下载参数无效", err))
		return
	}
	item, err := a.sites.Download(c.Request.Context(), actor, services.SiteDownloadInput{ResultToken: payload.ResultToken, DownloaderID: payload.DownloaderID, MediaLibraryID: payload.MediaLibraryID, ProfileID: payload.ProfileID, Priority: payload.Priority}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, item)
}

func (a *API) RecognizePTResult(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var payload struct {
		ResultToken string `json:"result_token"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("种子资源识别参数无效", err))
		return
	}
	item, err := a.sites.RecognizeResult(c.Request.Context(), actor, payload.ResultToken)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) RecognizeTorrentResult(c *gin.Context) { a.RecognizePTResult(c) }

func (a *API) PTResultRecognitionCandidates(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var payload struct {
		ResultToken string `json:"result_token"`
		Title       string `json:"title"`
		MediaType   string `json:"media_type"`
		Year        *int   `json:"year"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("TMDB 候选搜索参数无效", err))
		return
	}
	items, err := a.sites.RecognitionCandidates(c.Request.Context(), actor, payload.ResultToken, payload.Title, payload.MediaType, payload.Year)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) OverridePTResultRecognition(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var payload struct {
		ResultToken string `json:"result_token"`
		TMDBID      int64  `json:"tmdb_id"`
		MediaType   string `json:"media_type"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("种子资源人工识别参数无效", err))
		return
	}
	item, err := a.sites.OverrideResultRecognition(c.Request.Context(), actor, services.SiteManualRecognitionInput{ResultToken: payload.ResultToken, TMDBID: payload.TMDBID, MediaType: payload.MediaType})
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
