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

func (a *API) CreateSite(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var payload siteWritePayload
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("PT 站点配置无效", err))
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	item, err := a.sites.Create(c.Request.Context(), actor, services.SiteInput{Name: payload.Name, Kind: payload.Kind, BaseURL: payload.BaseURL, Cookie: payload.Cookie, Passkey: payload.Passkey, UserAgent: payload.UserAgent, BrowserEmulation: payload.BrowserEmulation, BrowserServiceURL: payload.BrowserServiceURL, Enabled: enabled, Priority: payload.Priority, TimeoutSeconds: payload.TimeoutSeconds, RateLimitPerMinute: payload.RateLimitPerMinute}, middleware.RequestContextFrom(c))
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
		writeError(c, a.log, invalid("PT 站点配置无效", err))
		return
	}
	item, err := a.sites.Update(c.Request.Context(), actor, id, services.SiteUpdateInput{Name: payload.Name, BaseURL: payload.BaseURL, Cookie: payload.Cookie, Passkey: payload.Passkey, ClearPasskey: payload.ClearPasskey, UserAgent: payload.UserAgent, BrowserEmulation: payload.BrowserEmulation, BrowserServiceURL: payload.BrowserServiceURL, Enabled: payload.Enabled, Priority: payload.Priority, TimeoutSeconds: payload.TimeoutSeconds, RateLimitPerMinute: payload.RateLimitPerMinute, Revision: payload.Revision}, middleware.RequestContextFrom(c))
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
			return input, invalid("PT 搜索页码无效", err)
		}
		input.Page = page
	}
	if value := strings.TrimSpace(c.Query("year")); value != "" {
		year, err := strconv.Atoi(value)
		if err != nil || year < 1880 || year > 2200 {
			return input, invalid("PT 搜索年份无效", err)
		}
		input.Year = &year
	}
	if value := strings.TrimSpace(c.Query("site_id")); value != "" {
		id, err := strconv.ParseUint(value, 10, 32)
		if err != nil || id == 0 {
			return input, invalid("PT 站点筛选无效", err)
		}
		value := uint(id)
		input.SiteID = &value
	}
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

func (a *API) PTSearchStream(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	input, err := parsePTSearch(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		writeError(c, a.log, &services.AppError{Code: services.CodeSiteUnavailable, Message: "当前 HTTP 服务不支持流式 PT 搜索"})
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
		payload, _ := json.Marshal(gin.H{"code": services.ErrorCode(err), "message": "PT 搜索失败"})
		_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload)
		flusher.Flush()
		return
	}
	_, _ = fmt.Fprint(c.Writer, "event: done\ndata: {}\n\n")
	flusher.Flush()
}

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
		writeError(c, a.log, invalid("PT 下载参数无效", err))
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
		writeError(c, a.log, invalid("PT 识别参数无效", err))
		return
	}
	item, err := a.sites.RecognizeResult(c.Request.Context(), actor, payload.ResultToken)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
