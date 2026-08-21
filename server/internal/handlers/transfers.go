package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) Transfers(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	filter := services.TransferListFilter{
		Scope:        strings.TrimSpace(c.DefaultQuery("scope", "active")),
		Status:       strings.TrimSpace(c.Query("status")),
		Category:     strings.TrimSpace(c.Query("category")),
		TransferMode: strings.TrimSpace(c.Query("transfer_mode")),
		Keyword:      strings.TrimSpace(c.Query("keyword")),
	}
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
	validStatus := map[string]bool{"": true, "processing": true, "waiting_action": true, "paused": true, "failed": true, "completed": true, "cancelled": true}
	if !validStatus[filter.Status] {
		writeError(c, a.log, invalid("status 无效", nil))
		return
	}
	validScope := map[string]bool{"active": true, "history": true, "all": true}
	if !validScope[filter.Scope] {
		writeError(c, a.log, invalid("scope 无效", nil))
		return
	}
	validMode := map[string]bool{"": true, "move": true, "copy": true, "symlink": true}
	if !validMode[filter.TransferMode] {
		writeError(c, a.log, invalid("transfer_mode 无效", nil))
		return
	}
	if len([]rune(filter.Category)) > 128 || len([]rune(filter.Keyword)) > 256 || strings.ContainsAny(filter.Category+filter.Keyword, "\x00\r\n") {
		writeError(c, a.log, invalid("筛选条件无效", nil))
		return
	}
	if value := strings.TrimSpace(c.Query("library_id")); value != "" {
		id, parseErr := strconv.ParseUint(value, 10, 64)
		if parseErr != nil || id == 0 {
			writeError(c, a.log, invalid("library_id 无效", parseErr))
			return
		}
		libraryID := uint(id)
		filter.LibraryID = &libraryID
	}
	data, err := a.transfers.List(actor, filter)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) Transfer(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	data, err := a.transfers.Get(actor, id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) DeleteTransfer(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := stringID(c)
	if !ok {
		return
	}
	if err := a.transfers.Delete(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}
