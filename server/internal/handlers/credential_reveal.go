package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) RevealCredential(c *gin.Context) {
	if a.credentialReveal == nil {
		writeError(c, a.log, errors.New("credential reveal service is unavailable"))
		return
	}
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var payload struct {
		ResourceType string `json:"resource_type"`
		ResourceID   string `json:"resource_id"`
		Field        string `json:"field"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("凭据查看请求无效", err))
		return
	}
	result, err := a.credentialReveal.Reveal(actor, services.CredentialRevealInput{
		ResourceType: payload.ResourceType,
		ResourceID:   payload.ResourceID,
		Field:        payload.Field,
	}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, result)
}
