package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) PluginRepositories(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.pluginRepositories.List(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) CreatePluginRepository(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var payload struct {
		Name      string `json:"name"`
		GitHubURL string `json:"github_url"`
		Enabled   *bool  `json:"enabled"`
	}
	if err := strictJSON(c, &payload); err != nil || payload.GitHubURL == "" {
		writeError(c, a.log, invalid("插件仓库配置无效", err))
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	item, err := a.pluginRepositories.Create(actor, services.CreatePluginRepositoryInput{Name: payload.Name, GitHubURL: payload.GitHubURL, Enabled: enabled}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, item)
}

func (a *API) UpdatePluginRepository(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		Name     *string `json:"name"`
		Enabled  *bool   `json:"enabled"`
		Revision uint64  `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil || payload.Revision == 0 || (payload.Name == nil && payload.Enabled == nil) {
		writeError(c, a.log, invalid("插件仓库更新无效", err))
		return
	}
	item, err := a.pluginRepositories.Update(actor, id, services.UpdatePluginRepositoryInput{Name: payload.Name, Enabled: payload.Enabled, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) DeletePluginRepository(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var payload struct {
		Revision uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil || payload.Revision == 0 {
		writeError(c, a.log, invalid("插件仓库删除请求无效", err))
		return
	}
	if err := a.pluginRepositories.Delete(actor, id, payload.Revision, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) ReorderPluginRepositories(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var payload struct {
		Order []services.PluginRepositoryOrderInput `json:"order"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("插件仓库顺序无效", err))
		return
	}
	items, err := a.pluginRepositories.Reorder(actor, payload.Order, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) RefreshPluginRepository(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var payload struct{}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("插件仓库刷新请求无效", err))
		return
	}
	item, err := a.pluginRepositories.Refresh(c.Request.Context(), actor, id, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) PluginMarketplace(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.pluginRepositories.Marketplace(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items), "server_version": services.CurrentServerVersion})
}

func (a *API) InstalledPlugins(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.pluginRepositories.Installed(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	runtimeStatus := "unavailable"
	if a.pluginRepositories.RuntimeAvailable() {
		runtimeStatus = "available"
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items), "runtime_status": runtimeStatus})
}

func (a *API) PreviewPluginInstallation(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	pluginID := c.Param("plugin_id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		RepositoryID uint   `json:"repository_id"`
		Version      string `json:"version"`
	}
	if err := strictJSON(c, &payload); err != nil || pluginID == "" || payload.RepositoryID == 0 {
		writeError(c, a.log, invalid("插件安装预览请求无效", err))
		return
	}
	item, err := a.pluginRepositories.PrepareInstall(c.Request.Context(), actor, pluginID, payload.RepositoryID, payload.Version, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) ConfirmPluginInstall(c *gin.Context) {
	a.confirmPluginInstallation(c, "install")
}

func (a *API) ConfirmPluginUpdate(c *gin.Context) {
	a.confirmPluginInstallation(c, "update")
}

func (a *API) confirmPluginInstallation(c *gin.Context, expectedOperation string) {
	actor, _ := middleware.ActorFrom(c)
	pluginID := c.Param("plugin_id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		PreviewID             string `json:"preview_id"`
		PermissionFingerprint string `json:"permission_fingerprint"`
		Revision              uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil || pluginID == "" || payload.PreviewID == "" || payload.PermissionFingerprint == "" {
		writeError(c, a.log, invalid("插件安装确认请求无效", err))
		return
	}
	item, err := a.pluginRepositories.ConfirmInstall(c.Request.Context(), actor, pluginID, expectedOperation, payload.PreviewID, payload.PermissionFingerprint, payload.Revision, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) EnablePlugin(c *gin.Context) {
	a.setPluginEnabled(c, true)
}

func (a *API) DisablePlugin(c *gin.Context) {
	a.setPluginEnabled(c, false)
}

func (a *API) setPluginEnabled(c *gin.Context, enabled bool) {
	actor, _ := middleware.ActorFrom(c)
	pluginID := c.Param("plugin_id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var payload struct {
		Revision uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil || pluginID == "" || payload.Revision == 0 {
		writeError(c, a.log, invalid("插件状态请求无效", err))
		return
	}
	item, err := a.pluginRepositories.SetPluginEnabled(c.Request.Context(), actor, pluginID, enabled, payload.Revision, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) RollbackPlugin(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	pluginID := c.Param("plugin_id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var payload struct {
		Revision uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil || pluginID == "" || payload.Revision == 0 {
		writeError(c, a.log, invalid("插件回滚请求无效", err))
		return
	}
	item, err := a.pluginRepositories.RollbackPlugin(c.Request.Context(), actor, pluginID, payload.Revision, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) UninstallPlugin(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	pluginID := c.Param("plugin_id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var payload struct {
		Revision uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil || pluginID == "" || payload.Revision == 0 {
		writeError(c, a.log, invalid("插件卸载请求无效", err))
		return
	}
	if err := a.pluginRepositories.UninstallPlugin(actor, pluginID, payload.Revision, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) PluginConnections(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	pluginID := c.Param("plugin_id")
	items, err := a.pluginRepositories.ListConnections(actor, pluginID)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) CreatePluginConnection(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	pluginID := c.Param("plugin_id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96<<10)
	var payload struct {
		Name            string          `json:"name"`
		Config          json.RawMessage `json:"config"`
		CredentialScope string          `json:"credential_scope"`
		CredentialMode  string          `json:"credential_mode"`
		Credential      string          `json:"credential"`
		Enabled         *bool           `json:"enabled"`
	}
	if err := strictJSON(c, &payload); err != nil || pluginID == "" {
		writeError(c, a.log, invalid("插件连接创建请求无效", err))
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	item, err := a.pluginRepositories.CreateConnection(actor, pluginID, services.CreatePluginConnectionInput{Name: payload.Name, Config: payload.Config, CredentialScope: payload.CredentialScope, CredentialMode: payload.CredentialMode, Credential: payload.Credential, Enabled: enabled}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, item)
}

func (a *API) UpdatePluginConnection(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	pluginID, connectionID := c.Param("plugin_id"), c.Param("connection_id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96<<10)
	var payload struct {
		Name            *string          `json:"name"`
		Config          *json.RawMessage `json:"config"`
		CredentialScope *string          `json:"credential_scope"`
		CredentialMode  *string          `json:"credential_mode"`
		Credential      *string          `json:"credential"`
		ClearCredential bool             `json:"clear_credential"`
		Enabled         *bool            `json:"enabled"`
		Revision        uint64           `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil || pluginID == "" || connectionID == "" || payload.Revision == 0 {
		writeError(c, a.log, invalid("插件连接更新请求无效", err))
		return
	}
	item, err := a.pluginRepositories.UpdateConnection(actor, pluginID, connectionID, services.UpdatePluginConnectionInput{Name: payload.Name, Config: payload.Config, CredentialScope: payload.CredentialScope, CredentialMode: payload.CredentialMode, Credential: payload.Credential, ClearCredential: payload.ClearCredential, Enabled: payload.Enabled, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) DeletePluginConnection(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	pluginID, connectionID := c.Param("plugin_id"), c.Param("connection_id")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var payload struct {
		Revision uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil || pluginID == "" || connectionID == "" || payload.Revision == 0 {
		writeError(c, a.log, invalid("插件连接删除请求无效", err))
		return
	}
	if err := a.pluginRepositories.DeleteConnection(actor, pluginID, connectionID, payload.Revision, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) StartPluginConnectionAuth(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var payload struct{}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("插件登录请求无效", err))
		return
	}
	item, err := a.pluginRepositories.StartConnectionAuth(c.Request.Context(), actor, c.Param("plugin_id"), c.Param("connection_id"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) PollPluginConnectionAuth(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var payload struct {
		LoginSession string `json:"login_session"`
	}
	if err := strictJSON(c, &payload); err != nil || payload.LoginSession == "" {
		writeError(c, a.log, invalid("插件登录轮询请求无效", err))
		return
	}
	item, err := a.pluginRepositories.PollConnectionAuth(c.Request.Context(), actor, c.Param("plugin_id"), c.Param("connection_id"), payload.LoginSession)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
