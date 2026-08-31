package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type connectionPayload struct {
	Name                    string  `json:"name"`
	Provider                string  `json:"provider"`
	Cookie                  *string `json:"cookie"`
	RecyclePassword         *string `json:"recycle_password"`
	RemoveRecyclePassword   *bool   `json:"remove_recycle_password"`
	RecycleCleanupEnabled   *bool   `json:"recycle_cleanup_enabled"`
	RecycleCleanupCron      *string `json:"recycle_cleanup_cron"`
	RecycleCleanupConfirmed bool    `json:"recycle_cleanup_confirmed"`
	Endpoint                *string `json:"endpoint"`
	APIKey                  *string `json:"api_key"`
	Enabled                 *bool   `json:"enabled"`
	Revision                uint64  `json:"revision"`
}

func (a *API) Connections(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.connections.List(actor, c.Query("provider"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) ConnectionDirectories(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	listing, err := a.providerDirectory.Browse(c.Request.Context(), actor, id, c.Query("token"), c.Query("page_token"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, listing)
}

func (a *API) CreateConnection(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var payload connectionPayload
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("连接配置无效", err))
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	cookie := ""
	if payload.Cookie != nil {
		cookie = *payload.Cookie
	}
	endpoint, apiKey := "", ""
	if payload.Endpoint != nil {
		endpoint = *payload.Endpoint
	}
	if payload.APIKey != nil {
		apiKey = *payload.APIKey
	}
	recyclePassword := ""
	if payload.RecyclePassword != nil {
		recyclePassword = *payload.RecyclePassword
	}
	cleanupEnabled := false
	if payload.RecycleCleanupEnabled != nil {
		cleanupEnabled = *payload.RecycleCleanupEnabled
	}
	cleanupCron := ""
	if payload.RecycleCleanupCron != nil {
		cleanupCron = *payload.RecycleCleanupCron
	}
	item, err := a.connections.Create(actor, services.ConnectionInput{Name: payload.Name, Provider: payload.Provider, Cookie: cookie, RecyclePassword: recyclePassword, Endpoint: endpoint, APIKey: apiKey, Enabled: enabled, RecycleCleanupEnabled: cleanupEnabled, RecycleCleanupCron: cleanupCron, RecycleCleanupConfirmed: payload.RecycleCleanupConfirmed}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, item)
}

func (a *API) UpdateConnection(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var payload connectionPayload
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("连接配置无效", err))
		return
	}
	var name *string
	if payload.Name != "" {
		name = &payload.Name
	}
	item, err := a.connections.Update(actor, id, services.UpdateConnectionInput{Name: name, Cookie: payload.Cookie, RecyclePassword: payload.RecyclePassword, RemoveRecyclePassword: payload.RemoveRecyclePassword, RecycleCleanupEnabled: payload.RecycleCleanupEnabled, RecycleCleanupCron: payload.RecycleCleanupCron, RecycleCleanupConfirmed: payload.RecycleCleanupConfirmed, Endpoint: payload.Endpoint, APIKey: payload.APIKey, Enabled: payload.Enabled, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) EmbyGatewaySettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := a.embyGateway.Get(actor, id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) EmbyManagementSummary(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := a.connections.EmbyManagementSummary(c.Request.Context(), actor, id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) UpdateEmbyGatewaySettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		Enabled               bool    `json:"enabled"`
		Alias                 *string `json:"alias"`
		ExternalPlayerEnabled *bool   `json:"external_player_enabled"`
		FanartEnabled         *bool   `json:"fanart_enabled"`
		Revision              uint64  `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("Emby 网关配置无效", err))
		return
	}
	item, err := a.embyGateway.ConfigureSettings(actor, id, services.EmbyGatewaySettingsInput{Enabled: payload.Enabled, Alias: payload.Alias, ExternalPlayerEnabled: payload.ExternalPlayerEnabled, FanartEnabled: payload.FanartEnabled, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) EmbyGateway(c *gin.Context) {
	if a.embyGateway == nil {
		c.Status(http.StatusNotFound)
		return
	}
	// The global headers protect OhMyCine's own Web UI. The gateway must
	// transparently preserve the fixed upstream's policy instead.
	for _, name := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Permissions-Policy"} {
		c.Writer.Header().Del(name)
	}
	c.Abort()
	a.embyGateway.ServeHTTP(c.Writer, c.Request, c.Param("gateway"), c.Param("path"))
}

func (a *API) TestConnection(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := a.connections.Test(c.Request.Context(), actor, id, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) DeleteConnection(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.connections.Delete(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}
