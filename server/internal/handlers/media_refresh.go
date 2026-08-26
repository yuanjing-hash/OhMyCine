package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) PlayerMediaChanges(c *gin.Context) {
	actor := mustActor(c)
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		writeError(c, a.log, &services.AppError{Code: services.CodePermissionDenied, Message: "无权查看媒体库变更"})
		return
	}
	cursor, err := strconv.ParseUint(c.DefaultQuery("cursor", "0"), 10, 64)
	if err != nil {
		writeError(c, a.log, invalid("媒体变更游标无效", err))
		return
	}
	waitSeconds, err := strconv.Atoi(c.DefaultQuery("wait_seconds", "0"))
	if err != nil || waitSeconds < 0 || waitSeconds > 12 {
		writeError(c, a.log, invalid("媒体变更等待时间无效", err))
		return
	}
	page, err := a.mediaChanges.ReadyAfter(cursor, 256)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	if len(page.Changes) == 0 && !page.ResyncRequired && waitSeconds > 0 {
		timer := time.NewTimer(time.Duration(waitSeconds) * time.Second)
		defer timer.Stop()
		select {
		case <-c.Request.Context().Done():
			return
		case <-a.mediaChanges.Wakeups():
		case <-timer.C:
		}
		page, err = a.mediaChanges.ReadyAfter(cursor, 256)
		if err != nil {
			writeError(c, a.log, err)
			return
		}
	}
	next := page.LatestSequence
	if len(page.Changes) > 0 {
		next = page.Changes[len(page.Changes)-1].Sequence
	}
	items := make([]gin.H, 0, len(page.Changes))
	for _, change := range page.Changes {
		items = append(items, gin.H{"library_id": change.LibraryID, "revision": change.Revision, "kind": change.Kind})
	}
	success(c, http.StatusOK, gin.H{"cursor": strconv.FormatUint(next, 10), "resync_required": page.ResyncRequired, "changes": items})
}

func (a *API) MediaServerRefreshTargets(c *gin.Context) {
	items, err := a.mediaServerRefresh.List(mustActor(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) MediaServerUpstreamLibraries(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(c, a.log, invalid("连接标识无效", err))
		return
	}
	items, err := a.mediaServerRefresh.ListUpstreamLibraries(c.Request.Context(), mustActor(c), uint(id))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) CreateMediaServerRefreshTarget(c *gin.Context) {
	var payload struct {
		LibraryID         uint   `json:"library_id" binding:"required"`
		ConnectionID      uint   `json:"connection_id" binding:"required"`
		UpstreamLibraryID string `json:"upstream_library_id" binding:"required"`
		Enabled           bool   `json:"enabled"`
	}
	if err := strictJSON(c, &payload); err != nil || payload.LibraryID == 0 || payload.ConnectionID == 0 || payload.UpstreamLibraryID == "" {
		writeError(c, a.log, invalid("媒体服务器刷新目标无效", err))
		return
	}
	item, err := a.mediaServerRefresh.Create(c.Request.Context(), mustActor(c), services.MediaServerRefreshTargetInput{LibraryID: payload.LibraryID, ConnectionID: payload.ConnectionID, UpstreamLibraryID: payload.UpstreamLibraryID, Enabled: payload.Enabled}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, item)
}

func (a *API) UpdateMediaServerRefreshTarget(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(c, a.log, invalid("刷新目标标识无效", err))
		return
	}
	var payload struct {
		Enabled  bool   `json:"enabled"`
		Revision uint64 `json:"revision" binding:"required"`
	}
	if err := strictJSON(c, &payload); err != nil || payload.Revision == 0 {
		writeError(c, a.log, invalid("媒体服务器刷新目标无效", err))
		return
	}
	item, err := a.mediaServerRefresh.Update(mustActor(c), uint(id), services.MediaServerRefreshTargetInput{Enabled: payload.Enabled, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) DeleteMediaServerRefreshTarget(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(c, a.log, invalid("刷新目标标识无效", err))
		return
	}
	if err := a.mediaServerRefresh.Delete(mustActor(c), uint(id), middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) RefreshMediaServerTarget(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(c, a.log, invalid("刷新目标标识无效", err))
		return
	}
	job, err := a.mediaServerRefresh.ManualRefresh(mustActor(c), uint(id), middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusAccepted, job)
}

func (a *API) TestMediaServerRefreshTarget(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(c, a.log, invalid("刷新目标标识无效", err))
		return
	}
	result, err := a.mediaServerRefresh.TestTarget(c.Request.Context(), mustActor(c), uint(id), middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}

func (a *API) RetryMediaServerRefreshTarget(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		writeError(c, a.log, invalid("刷新目标标识无效", err))
		return
	}
	job, err := a.mediaServerRefresh.Retry(mustActor(c), uint(id), middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusAccepted, job)
}
