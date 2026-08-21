package handlers

import (
	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
	"net/http"
	"strings"
)

func (a *API) MetadataSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	item, err := a.metadataSettings.Get(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
func (a *API) UpdateMetadataSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		TMDBToken      string `json:"tmdb_token"`
		CredentialKind string `json:"credential_kind"`
		ClearTMDB      bool   `json:"clear_tmdb"`
		Revision       uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("元数据设置无效", err))
		return
	}
	input := services.UpdateMetadataSettingsInput{TMDBToken: payload.TMDBToken, CredentialKind: payload.CredentialKind, ClearTMDB: payload.ClearTMDB, Revision: payload.Revision}
	var item services.MetadataSettingsSummary
	var err error
	if strings.TrimSpace(payload.TMDBToken) != "" && !payload.ClearTMDB {
		// Keep the legacy PATCH shape compatible while enforcing probe-before-persist.
		item, err = a.metadataSettings.TestAndSetToken(c.Request.Context(), actor, input, middleware.RequestContextFrom(c))
	} else {
		item, err = a.metadataSettings.Update(actor, input, middleware.RequestContextFrom(c))
	}
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
func (a *API) TestMetadataSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	if err := a.metadataSettings.Test(c.Request.Context(), actor); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"status": "online"})
}

func (a *API) TestAndSetMetadataToken(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		TMDBToken      string `json:"tmdb_token"`
		CredentialKind string `json:"credential_kind"`
		Revision       uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("TMDB Token 测试信息无效", err))
		return
	}
	item, err := a.metadataSettings.TestAndSetToken(c.Request.Context(), actor, services.UpdateMetadataSettingsInput{TMDBToken: payload.TMDBToken, CredentialKind: payload.CredentialKind, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) TestAndSetMetadataRoute(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, _ := middleware.ActorFrom(c)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var payload struct {
			BaseURL  string `json:"base_url"`
			Revision uint64 `json:"revision"`
		}
		if err := strictJSON(c, &payload); err != nil {
			writeError(c, a.log, invalid("TMDB 路由设置无效", err))
			return
		}
		input := services.UpdateTMDBRouteInput{BaseURL: payload.BaseURL, Revision: payload.Revision}
		var item services.MetadataSettingsSummary
		var err error
		if kind == "api" {
			item, err = a.metadataSettings.TestAndSetAPI(c.Request.Context(), actor, input, middleware.RequestContextFrom(c))
		} else {
			item, err = a.metadataSettings.TestAndSetImage(c.Request.Context(), actor, input, middleware.RequestContextFrom(c))
		}
		if err != nil {
			writeError(c, a.log, err)
			return
		}
		success(c, http.StatusOK, item)
	}
}
