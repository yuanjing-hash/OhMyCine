package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) FollowDefaults(c *gin.Context) {
	tmdbID, err := strconv.ParseInt(c.Query("tmdb_id"), 10, 64)
	if err != nil || c.Query("media_type") != "tv" {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "订阅媒体身份无效", Cause: err})
		return
	}
	actor, _ := middleware.ActorFrom(c)
	data, err := a.follows.Defaults(c.Request.Context(), actor, tmdbID)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) Follows(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))
	data, err := a.follows.List(actor, page, size, c.Query("status"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) Follow(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.follows.Get(actor, c.Param("id"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) CreateFollow(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var input services.CreateFollowInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "订阅配置无效", Cause: err})
		return
	}
	data, err := a.follows.Create(c.Request.Context(), actor, input, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, data)
}
func (a *API) UpdateFollow(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var input services.UpdateFollowInput
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "订阅配置无效", Cause: err})
		return
	}
	data, err := a.follows.Update(c.Request.Context(), actor, c.Param("id"), input, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) SetFollowPaused(paused bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, _ := middleware.ActorFrom(c)
		data, err := a.follows.SetPaused(actor, c.Param("id"), paused, middleware.RequestContextFrom(c))
		if err != nil {
			writeError(c, a.log, err)
			return
		}
		success(c, http.StatusOK, data)
	}
}
func (a *API) DeleteFollow(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	if err := a.follows.Delete(actor, c.Param("id"), middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}
func (a *API) SearchFollow(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	jobID, err := a.follows.Enqueue(c.Request.Context(), actor, c.Param("id"), "manual", middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusAccepted, gin.H{"job_id": jobID})
}
func (a *API) FollowRuns(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.follows.Runs(actor, c.Param("id"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}
