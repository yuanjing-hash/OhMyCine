package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) DownloadSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	settings, err := a.downloadSettings.Get(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, settings)
}

func (a *API) DownloadSettingsDirectory(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	settings, err := a.downloadSettings.Get(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	if settings.AbsolutePath == "" {
		data, rootsErr := a.directory.Roots(c.Request.Context(), actor, middleware.RequestContextFrom(c))
		if rootsErr != nil {
			writeError(c, a.log, rootsErr)
			return
		}
		success(c, http.StatusOK, data)
		return
	}
	data, err := a.directory.OpenPath(c.Request.Context(), actor, settings.AbsolutePath, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) UpdateDownloadSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var payload struct {
		DirectoryToken string `json:"directory_token"`
		Revision       uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil || payload.DirectoryToken == "" || payload.Revision == 0 {
		writeError(c, a.log, invalid("下载暂存设置无效", err))
		return
	}
	absolute, err := a.directory.ResolveSelection(c.Request.Context(), actor, payload.DirectoryToken)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	settings, err := a.downloadSettings.Update(c.Request.Context(), actor, services.UpdateDownloadSettingsInput{AbsolutePath: absolute, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, settings)
}
