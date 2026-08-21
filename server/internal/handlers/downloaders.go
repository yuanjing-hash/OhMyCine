package handlers

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type downloaderPayload struct {
	Name                   string  `json:"name"`
	Type                   string  `json:"type"`
	BaseURL                string  `json:"base_url"`
	Username               *string `json:"username"`
	Password               *string `json:"password"`
	ClearUsername          bool    `json:"clear_username"`
	ClearPassword          bool    `json:"clear_password"`
	StorageID              *uint   `json:"storage_id"`
	ProviderDirectoryToken *string `json:"provider_directory_token"`
	Enabled                *bool   `json:"enabled"`
}

func (a *API) Downloaders(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	items, err := a.downloaders.List(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) CreateDownloader(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var payload downloaderPayload
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("下载器配置无效", err))
		return
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	username, password := "", ""
	if payload.Username != nil {
		username = *payload.Username
	}
	if payload.Password != nil {
		password = *payload.Password
	}
	token := ""
	if payload.ProviderDirectoryToken != nil {
		token = *payload.ProviderDirectoryToken
	}
	item, err := a.downloaders.CreateContext(c.Request.Context(), actor, services.DownloaderInput{Name: payload.Name, Type: payload.Type, BaseURL: payload.BaseURL, Username: username, Password: password, Enabled: enabled, StorageID: payload.StorageID, ProviderDirectoryToken: token}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, item)
}

func (a *API) UpdateDownloader(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	id, ok := stringID(c)
	if !ok {
		return
	}
	var payload downloaderPayload
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("下载器配置无效", err))
		return
	}
	var name, baseURL *string
	if payload.Name != "" {
		name = &payload.Name
	}
	if payload.BaseURL != "" {
		baseURL = &payload.BaseURL
	}
	item, err := a.downloaders.UpdateContext(c.Request.Context(), actor, id, services.UpdateDownloaderInput{Name: name, BaseURL: baseURL, Username: payload.Username, Password: payload.Password, ClearUsername: payload.ClearUsername, ClearPassword: payload.ClearPassword, Enabled: payload.Enabled, StorageID: payload.StorageID, ProviderDirectoryToken: payload.ProviderDirectoryToken}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) TestDownloader(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	item, err := a.downloaders.Test(c.Request.Context(), actor, id, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) DeleteDownloader(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	if err := a.downloaders.Delete(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) Downloads(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 200 {
		writeError(c, a.log, invalid("limit 无效", err))
		return
	}
	scope := strings.TrimSpace(c.DefaultQuery("scope", services.DownloadListScopeActive))
	items, total, err := a.downloads.ListScoped(actor, scope, limit)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": total})
}

func (a *API) CreateDownload(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 6<<20)
	var payload struct {
		DownloaderID   string `json:"downloader_id"`
		MediaLibraryID *uint  `json:"media_library_id"`
		ProfileID      uint   `json:"profile_id"`
		DisplayName    string `json:"display_name"`
		Priority       int    `json:"priority"`
		SourceKind     string `json:"source_kind"`
		SourceURL      string `json:"source_url"`
		TorrentBase64  string `json:"torrent_base64"`
		TorrentName    string `json:"torrent_filename"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("下载任务信息无效", err))
		return
	}
	var torrent []byte
	if payload.TorrentBase64 != "" {
		var err error
		torrent, err = base64.StdEncoding.DecodeString(payload.TorrentBase64)
		if err != nil {
			torrent, err = base64.RawStdEncoding.DecodeString(payload.TorrentBase64)
		}
		if err != nil {
			writeError(c, a.log, invalid("种子文件编码无效", nil))
			return
		}
	}
	item, err := a.downloads.Submit(c.Request.Context(), actor, services.SubmitDownloadInput{DownloaderID: payload.DownloaderID, MediaLibraryID: payload.MediaLibraryID, ProfileID: payload.ProfileID, DisplayName: payload.DisplayName, Priority: payload.Priority, Source: services.DownloadSourceInput{Kind: payload.SourceKind, URL: payload.SourceURL, Torrent: torrent, Filename: payload.TorrentName}}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, item)
}

func (a *API) DeleteDownload(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	if err := a.downloads.Delete(c.Request.Context(), actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func stringID(c *gin.Context) (string, bool) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" || len(id) > 64 || strings.ContainsAny(id, "/\\\x00") {
		writeError(c, zerolog.Nop(), invalid("资源 ID 无效", nil))
		return "", false
	}
	return id, true
}
