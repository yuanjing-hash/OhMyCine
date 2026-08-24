package handlers

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) CookieCloudRoot(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, "OhMyCine CookieCloud API root: /cookiecloud")
}

func (a *API) CookieCloudUpdate(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	reader := io.Reader(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<20))
	if strings.Contains(strings.ToLower(c.GetHeader("Content-Encoding")), "gzip") {
		compressed, err := gzip.NewReader(reader)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"action": "error"})
			return
		}
		defer compressed.Close()
		reader = compressed
	}
	var payload struct {
		UUID       string `json:"uuid"`
		Encrypted  string `json:"encrypted"`
		CryptoType string `json:"crypto_type"`
	}
	decoder := json.NewDecoder(reader)
	if decoder.Decode(&payload) != nil || strings.TrimSpace(payload.UUID) == "" || strings.TrimSpace(payload.Encrypted) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"action": "error"})
		return
	}
	if err := a.cookieCloud.Receive(payload.UUID, payload.Encrypted, payload.CryptoType, c.GetHeader("X-CookieCloud-Auth")); err != nil {
		writeError(c, a.log, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"action": "done"})
}

func (a *API) CookieCloudSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	item, err := a.cookieCloud.Get(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) UpdateCookieCloudSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var payload struct {
		Mode            string `json:"mode"`
		BaseURL         string `json:"base_url"`
		UUID            string `json:"uuid"`
		Password        string `json:"password"`
		AuthHeader      string `json:"auth_header"`
		AutoSyncMinutes int    `json:"auto_sync_minutes"`
		Revision        uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("CookieCloud 设置无效", err))
		return
	}
	item, err := a.cookieCloud.Update(c.Request.Context(), actor, services.CookieCloudSettingsInput{Mode: payload.Mode, BaseURL: payload.BaseURL, UUID: payload.UUID, Password: payload.Password, AuthHeader: payload.AuthHeader, AutoSyncMinutes: payload.AutoSyncMinutes, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) SyncCookieCloud(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	item, err := a.cookieCloud.Sync(c.Request.Context(), actor, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}
