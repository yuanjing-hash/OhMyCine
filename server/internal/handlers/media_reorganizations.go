package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) PreviewMediaReorganization(c *gin.Context) {
	if a.reorganizations == nil {
		writeError(c, a.log, errors.New("media reorganization service is unavailable"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var input services.MediaReorganizationPreviewInput
	if err := strictJSON(c, &input); err != nil {
		writeError(c, a.log, invalid("重新整理预览参数无效", err))
		return
	}
	actor, _ := middleware.ActorFrom(c)
	result, err := a.reorganizations.Preview(c.Request.Context(), actor, input, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}

func (a *API) ConfirmMediaReorganization(c *gin.Context) {
	if a.reorganizations == nil {
		writeError(c, a.log, errors.New("media reorganization service is unavailable"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
	var input struct {
		ConfirmationToken string `json:"confirmation_token"`
	}
	if err := strictJSON(c, &input); err != nil || strings.TrimSpace(input.ConfirmationToken) == "" {
		writeError(c, a.log, invalid("重新整理确认参数无效", err))
		return
	}
	actor, _ := middleware.ActorFrom(c)
	result, err := a.reorganizations.Confirm(actor, input.ConfirmationToken, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusAccepted, result)
}

func (a *API) MediaReorganization(c *gin.Context) {
	if a.reorganizations == nil {
		writeError(c, a.log, errors.New("media reorganization service is unavailable"))
		return
	}
	id, ok := stringID(c)
	if !ok {
		return
	}
	actor, _ := middleware.ActorFrom(c)
	result, err := a.reorganizations.Get(actor, id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}
