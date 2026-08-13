package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) Jobs(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	filter := services.JobListFilter{Status: strings.TrimSpace(c.Query("status")), JobType: strings.TrimSpace(c.Query("job_type")), Provider: strings.TrimSpace(c.Query("provider"))}
	var err error
	filter.Page, err = strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || filter.Page < 1 {
		writeError(c, a.log, invalid("page 无效", err))
		return
	}
	filter.PageSize, err = strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if err != nil || filter.PageSize < 1 || filter.PageSize > 200 {
		writeError(c, a.log, invalid("page_size 无效", err))
		return
	}
	if filter.Status != "" {
		valid := map[string]bool{"queued": true, "running": true, "waiting_user_action": true, "retry_wait": true, "paused": true, "completed": true, "failed": true, "cancelled": true}
		if !valid[filter.Status] {
			writeError(c, a.log, invalid("status 无效", nil))
			return
		}
	}
	if len(filter.JobType) > 32 || len(filter.Provider) > 64 {
		writeError(c, a.log, invalid("筛选条件无效", nil))
		return
	}
	if value := c.Query("priority"); value != "" {
		priority, err := strconv.Atoi(value)
		if err != nil || priority < -100 || priority > 100 {
			writeError(c, a.log, invalid("priority 无效", err))
			return
		}
		filter.Priority = &priority
	}
	if value := c.Query("owner_id"); value != "" {
		id, err := strconv.ParseUint(value, 10, 64)
		if err != nil || id == 0 {
			writeError(c, a.log, invalid("owner_id 无效", err))
			return
		}
		owner := uint(id)
		filter.OwnerID = &owner
	}
	if raw := c.Query("created_from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(c, a.log, invalid("created_from 无效", err))
			return
		}
		filter.CreatedFrom = &parsed
	}
	if raw := c.Query("created_to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(c, a.log, invalid("created_to 无效", err))
			return
		}
		filter.CreatedTo = &parsed
	}
	data, err := a.queue.List(actor, filter)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) Job(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.queue.Get(actor, c.Param("id"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) JobAttempts(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.queue.Attempts(actor, c.Param("id"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}
func (a *API) JobTimeline(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.queue.Timeline(actor, c.Param("id"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}
func (a *API) JobControl(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, _ := middleware.ActorFrom(c)
		data, err := a.queue.Control(actor, c.Param("id"), action, middleware.RequestContextFrom(c))
		if err != nil {
			writeError(c, a.log, err)
			return
		}
		success(c, http.StatusOK, data)
	}
}
func (a *API) RespondJobAction(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	version, err := strconv.ParseUint(c.Param("version"), 10, 64)
	if err != nil || version == 0 {
		writeError(c, a.log, invalid("操作版本无效", err))
		return
	}
	var input struct {
		Response string `json:"response" binding:"required"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("响应信息无效", err))
		return
	}
	data, err := a.queue.Respond(actor, c.Param("id"), version, input.Response, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) ReorderJobLane(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	priority, err := strconv.Atoi(c.Param("priority"))
	if err != nil {
		writeError(c, a.log, invalid("priority 无效", err))
		return
	}
	var input struct {
		Jobs []services.LaneRevision `json:"jobs" binding:"required"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("队列顺序无效", err))
		return
	}
	data, err := a.queue.Reorder(actor, c.Param("jobType"), priority, input.Jobs, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}
func (a *API) QueuePolicies(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.queue.Policies(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}
func (a *API) UpdateQueuePolicy(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var input struct {
		Revision            uint64 `json:"revision" binding:"required"`
		Concurrency         int    `json:"concurrency" binding:"required"`
		ResourceConcurrency int    `json:"resource_concurrency"`
		MaxAttempts         int    `json:"max_attempts" binding:"required"`
		LeaseSeconds        int    `json:"lease_seconds" binding:"required"`
	}
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("队列策略无效", err))
		return
	}
	data, err := a.queue.UpdatePolicy(actor, c.Param("jobType"), input.Revision, input.Concurrency, input.ResourceConcurrency, input.MaxAttempts, input.LeaseSeconds, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
