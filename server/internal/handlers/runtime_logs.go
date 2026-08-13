package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) RuntimeLogs(c *gin.Context) {
	filter, err := runtimeLogFilter(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	actor, _ := middleware.ActorFrom(c)
	data, err := a.runtimeLogs.Query(c.Request.Context(), actor, filter)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) RuntimeLogFacets(c *gin.Context) {
	filter, err := runtimeLogFilter(c)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	filter.Limit = logging.MaxQueryLimit
	filter.Cursor = ""
	actor, _ := middleware.ActorFrom(c)
	data, err := a.runtimeLogs.Facets(c.Request.Context(), actor, filter)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) RuntimeLogSettings(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	data, err := a.runtimeLogs.Settings(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) UpdateRuntimeLogSettings(c *gin.Context) {
	var input struct {
		logging.Policy
		Revision uint64 `json:"revision"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("运行日志设置格式无效", err))
		return
	}
	actor, _ := middleware.ActorFrom(c)
	data, err := a.runtimeLogs.UpdateSettings(actor, input.Policy, input.Revision, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) ExportRuntimeLogs(c *gin.Context) {
	var input struct {
		From         string   `json:"from"`
		To           string   `json:"to"`
		Levels       []string `json:"levels"`
		Modules      []string `json:"modules"`
		Components   []string `json:"components"`
		PluginIDs    []string `json:"plugin_ids"`
		Keyword      string   `json:"keyword"`
		RequestID    string   `json:"request_id"`
		TaskID       string   `json:"task_id"`
		LibraryID    string   `json:"library_id"`
		ConnectionID string   `json:"connection_id"`
		StorageID    string   `json:"storage_id"`
		DownloaderID string   `json:"downloader_id"`
		ScanRunID    string   `json:"scan_run_id"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("运行日志导出条件无效", err))
		return
	}
	filter, err := filterFromValues(input.From, input.To, input.Levels, input.Modules, input.Components, input.PluginIDs, input.Keyword, input.RequestID, input.TaskID, input.LibraryID, input.ConnectionID, input.StorageID, input.DownloaderID, input.ScanRunID, logging.MaxQueryLimit, "")
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	actor, _ := middleware.ActorFrom(c)
	payload, count, err := a.runtimeLogs.Export(c.Request.Context(), actor, filter, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", `attachment; filename="ohmycine-runtime-logs.jsonl.gz"`)
	c.Header("X-Log-Entry-Count", strconv.Itoa(count))
	c.Data(http.StatusOK, "application/gzip", payload)
}

func runtimeLogFilter(c *gin.Context) (logging.Filter, error) {
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return logging.Filter{}, runtimeFilterError(err)
		}
		limit = value
	}
	return filterFromValues(c.Query("from"), c.Query("to"), splitValues(c.QueryArray("level")), splitValues(c.QueryArray("module")), splitValues(c.QueryArray("component")), splitValues(c.QueryArray("plugin_id")), c.Query("keyword"), c.Query("request_id"), c.Query("task_id"), c.Query("library_id"), c.Query("connection_id"), c.Query("storage_id"), c.Query("downloader_id"), c.Query("scan_run_id"), limit, c.Query("cursor"))
}

func filterFromValues(fromRaw, toRaw string, levels, modules, components, plugins []string, keyword, requestID, taskID, libraryID, connectionID, storageID, downloaderID, scanRunID string, limit int, cursor string) (logging.Filter, error) {
	var from, to time.Time
	var err error
	if fromRaw != "" {
		from, err = time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			return logging.Filter{}, runtimeFilterError(err)
		}
	}
	if toRaw != "" {
		to, err = time.Parse(time.RFC3339, toRaw)
		if err != nil {
			return logging.Filter{}, runtimeFilterError(err)
		}
	}
	f := logging.Filter{From: from, To: to, Levels: levels, Modules: modules, Components: components, PluginIDs: plugins, Keyword: strings.TrimSpace(keyword), RequestID: strings.TrimSpace(requestID), TaskID: strings.TrimSpace(taskID), LibraryID: strings.TrimSpace(libraryID), ConnectionID: strings.TrimSpace(connectionID), StorageID: strings.TrimSpace(storageID), DownloaderID: strings.TrimSpace(downloaderID), ScanRunID: strings.TrimSpace(scanRunID), Limit: limit, Cursor: cursor}
	if err := f.Normalize(time.Now().UTC()); err != nil {
		return logging.Filter{}, runtimeFilterError(err)
	}
	return f, nil
}
func splitValues(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}
func runtimeFilterError(err error) error {
	return &services.AppError{Code: services.CodeRuntimeLogFilterInvalid, Message: "运行日志筛选条件无效", Cause: err}
}
