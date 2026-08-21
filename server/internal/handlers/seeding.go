package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) SeedingSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	item, err := a.seedingSettings.Get(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) UpdateSeedingSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var payload struct {
		Enabled            bool    `json:"enabled"`
		MinimumSeedMinutes int     `json:"minimum_seed_minutes"`
		MinimumRatio       float64 `json:"minimum_ratio"`
		CompletionMode     string  `json:"completion_mode"`
		Revision           uint64  `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("做种设置无效", err))
		return
	}
	item, err := a.seedingSettings.Update(actor, services.UpdateSeedingSettingsInput{Enabled: payload.Enabled, MinimumSeedMinutes: payload.MinimumSeedMinutes, MinimumRatio: payload.MinimumRatio, CompletionMode: payload.CompletionMode, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) SeedingTasks(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	items, err := a.seeding.List(actor, limit)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) StopSeedingTask(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	item, err := a.seeding.Stop(c.Request.Context(), actor, id, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
