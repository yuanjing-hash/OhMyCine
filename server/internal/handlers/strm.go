package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func strmLibraryID(c *gin.Context) (uint, error) {
	value, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || value == 0 {
		return 0, &services.AppError{Code: services.CodeInvalidRequest, Message: "媒体库 ID 无效", Cause: err}
	}
	return uint(value), nil
}

func queryPage(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	return page, size
}

func (a *API) STRMLibraries(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.strm.Libraries(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) STRMRuns(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	libraryID, _ := strconv.ParseUint(c.Query("library_id"), 10, 32)
	page, size := queryPage(c)
	data, err := a.strm.Runs(actor, uint(libraryID), page, size)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) STRMArtifacts(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	libraryID, _ := strconv.ParseUint(c.Query("library_id"), 10, 32)
	page, size := queryPage(c)
	data, err := a.strm.Artifacts(actor, uint(libraryID), page, size)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) ReconcileSTRM(c *gin.Context) {
	id, err := strmLibraryID(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	var input struct {
		Mode string `json:"mode"`
	}
	if bindErr := c.ShouldBindJSON(&input); bindErr != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "STRM 刷新参数无效", Cause: bindErr})
		return
	}
	actor, _ := middleware.ActorFrom(c)
	job, err := a.strm.RequestReconcile(actor, id, input.Mode)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusAccepted, job)
}
func (a *API) RetrySTRMRun(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	if err := a.strm.RetryRun(actor, strings.TrimSpace(c.Param("run"))); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusAccepted, gin.H{"queued": true})
}
func (a *API) PreviewSTRMCleanup(c *gin.Context) {
	id, err := strmLibraryID(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	actor, _ := middleware.ActorFrom(c)
	data, err := a.strm.PreviewCleanup(actor, id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) ExecuteSTRMCleanup(c *gin.Context) {
	id, err := strmLibraryID(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	var input struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	if bindErr := c.ShouldBindJSON(&input); bindErr != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "清理确认参数无效", Cause: bindErr})
		return
	}
	actor, _ := middleware.ActorFrom(c)
	count, err := a.strm.ExecuteCleanup(actor, id, input.ConfirmationToken, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"removed": count})
}
