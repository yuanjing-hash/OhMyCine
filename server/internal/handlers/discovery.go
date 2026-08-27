package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) DiscoveryRecommendations(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "推荐页码无效", Cause: err})
		return
	}
	refresh := strings.EqualFold(c.DefaultQuery("refresh", "false"), "true")
	result, err := a.discovery.Overview(c.Request.Context(), actor, c.Query("provider"), page, refresh)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}

func (a *API) DiscoveryMediaSearch(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "影视搜索页码无效", Cause: err})
		return
	}
	result, err := a.discovery.MediaSearch(c.Request.Context(), actor, c.Query("query"), c.DefaultQuery("media_type", "all"), page)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}

func (a *API) DiscoveryDetail(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	result, err := a.discovery.Detail(c.Request.Context(), actor, c.Param("provider"), c.Param("mediaType"), c.Param("providerID"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}

func (a *API) DiscoveryMediaCoverage(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("tmdbID")), 10, 64)
	if err != nil || id <= 0 {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "媒体覆盖率身份无效", Cause: err})
		return
	}
	result, err := a.mediaCoverage.Coverage(c.Request.Context(), actor, c.Param("mediaType"), id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}

func (a *API) DiscoveryImage(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	body, contentType, err := a.discovery.Image(c.Request.Context(), actor, c.Param("provider"), c.Param("token"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	c.Header("Cache-Control", "private, max-age=86400")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, contentType, body)
}

func (a *API) RefreshDiscoveryRecommendations(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		Provider string `json:"provider"`
		Section  string `json:"section"`
		Page     int    `json:"page"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "刷新参数无效", Cause: err})
		return
	}
	if payload.Page == 0 {
		payload.Page = 1
	}
	result, err := a.discovery.Section(c.Request.Context(), actor, payload.Provider, payload.Section, payload.Page, "zh-CN", "CN", true)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}
