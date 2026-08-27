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
	AutoListenLifeEvents   *bool   `json:"auto_listen_life_events"`
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
	autoListen := payload.AutoListenLifeEvents != nil && *payload.AutoListenLifeEvents
	item, err := a.downloaders.CreateContext(c.Request.Context(), actor, services.DownloaderInput{Name: payload.Name, Type: payload.Type, BaseURL: payload.BaseURL, Username: username, Password: password, Enabled: enabled, StorageID: payload.StorageID, ProviderDirectoryToken: token, AutoListenLifeEvents: autoListen}, middleware.RequestContextFrom(c))
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
	item, err := a.downloaders.UpdateContext(c.Request.Context(), actor, id, services.UpdateDownloaderInput{Name: name, BaseURL: baseURL, Username: payload.Username, Password: payload.Password, ClearUsername: payload.ClearUsername, ClearPassword: payload.ClearPassword, Enabled: payload.Enabled, StorageID: payload.StorageID, ProviderDirectoryToken: payload.ProviderDirectoryToken, AutoListenLifeEvents: payload.AutoListenLifeEvents}, middleware.RequestContextFrom(c))
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
	deleteData := false
	if raw := strings.TrimSpace(c.Query("delete_data")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(c, a.log, invalid("删除数据选项无效", err))
			return
		}
		deleteData = value
	}
	if err := a.downloads.Delete(c.Request.Context(), actor, id, deleteData, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) CancelDownloadPipeline(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	if err := a.downloads.CancelPipeline(c.Request.Context(), actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"cancelled": true, "provider_data_retained": true})
}

func (a *API) DownloadRecognitionCandidates(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	var year *int
	if raw := strings.TrimSpace(c.Query("year")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1888 || value > 2200 {
			writeError(c, a.log, invalid("年份无效", err))
			return
		}
		year = &value
	}
	items, err := a.downloads.RecognitionCandidates(c.Request.Context(), actor, id, c.Query("title"), c.Query("media_type"), year)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) OverrideDownloadRecognition(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	input, err := decodeDownloadRecognitionOverride(c)
	if err != nil {
		writeError(c, a.log, invalid("TMDB 匹配选择无效", err))
		return
	}
	item, err := a.downloads.OverrideRecognition(c.Request.Context(), actor, id, input, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) RetargetDownloadImport(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	var payload struct {
		MediaLibraryID uint `json:"media_library_id"`
	}
	if err := strictJSON(c, &payload); err != nil || payload.MediaLibraryID == 0 {
		writeError(c, a.log, invalid("目标媒体库无效", err))
		return
	}
	item, err := a.downloads.RetargetCompletedImport(c.Request.Context(), actor, id, payload.MediaLibraryID, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

type downloadRecognitionOverridePayload struct {
	TMDBID    int64  `json:"tmdb_id"`
	MediaType string `json:"media_type"`
	Season    *int   `json:"season"`
	Episode   *int   `json:"episode"`
}

func decodeDownloadRecognitionOverride(c *gin.Context) (services.DownloadRecognitionOverrideInput, error) {
	var payload downloadRecognitionOverridePayload
	if err := strictJSON(c, &payload); err != nil {
		return services.DownloadRecognitionOverrideInput{}, err
	}
	return services.DownloadRecognitionOverrideInput{TMDBID: payload.TMDBID, MediaType: payload.MediaType, Season: payload.Season, Episode: payload.Episode}, nil
}

func stringID(c *gin.Context) (string, bool) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" || len(id) > 64 || strings.ContainsAny(id, "/\\\x00") {
		writeError(c, zerolog.Nop(), invalid("资源 ID 无效", nil))
		return "", false
	}
	return id, true
}
