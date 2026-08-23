package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type pluginAssetGateway interface {
	OpenAsset(context.Context, string, string, string) (*hostapi.AssetStream, error)
}

func (a *API) PlayerOnlineLibraries(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.pluginRepositories.OnlineLibraries(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) PlayerOnlineNavigation(c *gin.Context) {
	a.playerOnlineInvoke(c, func(actor services.Actor) (json.RawMessage, error) {
		return a.pluginRepositories.OnlineNavigation(c.Request.Context(), actor, c.Param("id"))
	})
}

func (a *API) PlayerOnlineFeed(c *gin.Context) {
	a.playerOnlineInvoke(c, func(actor services.Actor) (json.RawMessage, error) {
		return a.pluginRepositories.OnlineFeed(c.Request.Context(), actor, c.Param("id"), c.Param("routeKey"), c.Query("cursor"), c.Query("refresh_session"))
	})
}

func (a *API) PlayerOnlineFeedRefresh(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var input struct{}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("在线媒体栏目刷新请求无效", err))
		return
	}
	a.playerOnlineInvoke(c, func(actor services.Actor) (json.RawMessage, error) {
		return a.pluginRepositories.RefreshOnlineFeed(c.Request.Context(), actor, c.Param("id"), c.Param("routeKey"))
	})
}

func (a *API) PlayerHomeContributions(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.pluginRepositories.HomeContributions(c.Request.Context(), actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) PlayerOnlineSearch(c *gin.Context) {
	a.playerOnlineInvoke(c, func(actor services.Actor) (json.RawMessage, error) {
		return a.pluginRepositories.OnlineSearch(c.Request.Context(), actor, c.Param("id"), c.Query("q"), c.Query("cursor"))
	})
}

func (a *API) PlayerOnlineDetail(c *gin.Context) {
	a.playerOnlineInvoke(c, func(actor services.Actor) (json.RawMessage, error) {
		return a.pluginRepositories.OnlineDetail(c.Request.Context(), actor, c.Param("id"), c.Param("itemId"))
	})
}

func (a *API) PlayerOnlinePlayback(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var input struct {
		SegmentID string `json:"segmentId"`
		VersionID string `json:"versionId"`
		VariantID string `json:"variantId"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("在线播放请求无效", err))
		return
	}
	a.playerOnlineInvoke(c, func(actor services.Actor) (json.RawMessage, error) {
		return a.pluginRepositories.OnlinePlayback(c.Request.Context(), actor, c.Param("id"), c.Param("itemId"), input.SegmentID, input.VersionID, input.VariantID)
	})
}

func (a *API) PlayerOnlineAction(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var input struct {
		SegmentID      string `json:"segmentId"`
		VersionID      string `json:"versionId"`
		Value          *bool  `json:"value"`
		IdempotencyKey string `json:"idempotencyKey"`
		Confirmed      bool   `json:"confirmed"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("在线媒体操作请求无效", err))
		return
	}
	a.playerOnlineInvoke(c, func(actor services.Actor) (json.RawMessage, error) {
		return a.pluginRepositories.InvokeSiteAction(c.Request.Context(), actor, c.Param("id"), c.Param("itemId"), c.Param("action"), services.PluginSiteActionInput{
			SegmentID: input.SegmentID, VersionID: input.VersionID, Value: input.Value,
			IdempotencyKey: input.IdempotencyKey, Confirmed: input.Confirmed,
		})
	})
}

func (a *API) PlayerOnlineDownload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var input struct {
		SegmentID      string `json:"segmentId"`
		VersionID      string `json:"versionId"`
		VariantID      string `json:"variantId"`
		MediaLibraryID uint   `json:"mediaLibraryId"`
		Priority       int    `json:"priority"`
		DisplayName    string `json:"displayName"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("在线媒体下载请求无效", err))
		return
	}
	actor, _ := middleware.ActorFrom(c)
	item, err := a.downloads.SubmitPluginDownload(c.Request.Context(), actor, services.SubmitPluginDownloadInput{
		ConnectionID: c.Param("id"), ItemID: c.Param("itemId"), SegmentID: input.SegmentID,
		VersionID: input.VersionID, VariantID: input.VariantID, MediaLibraryID: input.MediaLibraryID,
		Priority: input.Priority, DisplayName: input.DisplayName,
	}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusAccepted, item)
}

func (a *API) PlayerOnlineProgress(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var input struct {
		SegmentID      string   `json:"segmentId"`
		VersionID      string   `json:"versionId"`
		Event          string   `json:"event"`
		Position       float64  `json:"positionSeconds"`
		Duration       *float64 `json:"durationSeconds"`
		IdempotencyKey string   `json:"idempotencyKey"`
		OccurredAt     string   `json:"occurredAt"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("在线播放进度请求无效", err))
		return
	}
	a.playerOnlineInvoke(c, func(actor services.Actor) (json.RawMessage, error) {
		return a.pluginRepositories.SyncOnlineProgress(c.Request.Context(), actor, c.Param("id"), c.Param("itemId"), input.SegmentID, input.VersionID, input.Event, input.Position, input.Duration, input.IdempotencyKey, input.OccurredAt)
	})
}

func (a *API) PlayerOnlineHistory(c *gin.Context) {
	pageSize := 24
	if raw := c.Query("page_size"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(c, a.log, invalid("在线媒体历史分页大小无效", err))
			return
		}
		pageSize = parsed
	}
	actor, _ := middleware.ActorFrom(c)
	page, err := a.pluginRepositories.OnlineHistory(c.Request.Context(), actor, c.Query("library_id"), c.Query("cursor"), pageSize)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, page)
}

func (a *API) PlayerOnlineAsset(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		writeError(c, a.log, &services.AppError{Code: services.CodePermissionDenied, Message: "无权读取在线媒体资源"})
		return
	}
	if a.pluginAssets == nil {
		writeError(c, a.log, &services.AppError{Code: services.CodePluginRuntimeUnavailable, Message: "在线媒体资源服务不可用"})
		return
	}
	stream, err := a.pluginAssets.OpenAsset(c.Request.Context(), c.Param("opaque"), c.Request.Method, c.GetHeader("Range"))
	if err != nil {
		code := hostapi.ErrorCode(err)
		appCode, message := services.CodePluginOnlineLibraryUnavailable, "在线媒体资源暂时不可用"
		switch code {
		case "plugin_asset_reference_invalid", "plugin_asset_expired":
			appCode, message = services.CodePluginAssetExpired, "在线媒体资源已过期，请重新开始播放"
		case "plugin_asset_range_invalid":
			appCode, message = services.CodeInvalidRequest, "在线媒体资源 Range 请求无效"
		}
		writeError(c, a.log, &services.AppError{Code: appCode, Message: message, Cause: err})
		return
	}
	defer stream.Body.Close()
	for name, values := range stream.Header {
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	c.Header("Cache-Control", "no-store")
	c.Status(stream.StatusCode)
	if c.Request.Method == http.MethodHead || stream.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return
	}
	_, _ = io.Copy(c.Writer, stream.Body)
}

func (a *API) playerOnlineInvoke(c *gin.Context, invoke func(services.Actor) (json.RawMessage, error)) {
	actor, _ := middleware.ActorFrom(c)
	data, err := invoke(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
