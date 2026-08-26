package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) AIRecognitionSettings(c *gin.Context) {
	if a.aiRecognitionSettings == nil {
		writeError(c, a.log, errors.New("AI recognition settings service is unavailable"))
		return
	}
	actor, _ := middleware.ActorFrom(c)
	item, err := a.aiRecognitionSettings.Get(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) UpdateAIRecognitionSettings(c *gin.Context) {
	if a.aiRecognitionSettings == nil {
		writeError(c, a.log, errors.New("AI recognition settings service is unavailable"))
		return
	}
	actor, _ := middleware.ActorFrom(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		Enabled               bool   `json:"enabled"`
		ProviderType          string `json:"provider_type"`
		BaseURL               string `json:"base_url"`
		APIKey                string `json:"api_key"`
		ClearAPIKey           bool   `json:"clear_api_key"`
		Model                 string `json:"model"`
		SendRelativeBasenames bool   `json:"send_relative_basenames"`
		Revision              uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("AI 识别设置无效", err))
		return
	}
	item, err := a.aiRecognitionSettings.Update(actor, services.UpdateAIRecognitionSettingsInput{Enabled: payload.Enabled, ProviderType: payload.ProviderType, BaseURL: payload.BaseURL, APIKey: payload.APIKey, ClearAPIKey: payload.ClearAPIKey, Model: payload.Model, SendRelativeBasenames: payload.SendRelativeBasenames, Revision: payload.Revision}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, item)
}

func (a *API) TestAIRecognitionSettings(c *gin.Context) {
	input, ok := a.bindAIProbe(c)
	if !ok {
		return
	}
	actor, _ := middleware.ActorFrom(c)
	if err := a.aiRecognitionSettings.TestConnection(c.Request.Context(), actor, input, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"status": "online"})
}

func (a *API) AIRecognitionModels(c *gin.Context) {
	input, ok := a.bindAIProbe(c)
	if !ok {
		return
	}
	actor, _ := middleware.ActorFrom(c)
	items, err := a.aiRecognitionSettings.ListModels(c.Request.Context(), actor, input, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) bindAIProbe(c *gin.Context) (services.AIProviderProbeInput, bool) {
	if a.aiRecognitionSettings == nil {
		writeError(c, a.log, errors.New("AI recognition settings service is unavailable"))
		return services.AIProviderProbeInput{}, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var payload struct {
		ProviderType string `json:"provider_type"`
		BaseURL      string `json:"base_url"`
		APIKey       string `json:"api_key"`
		Model        string `json:"model"`
		Revision     uint64 `json:"revision"`
	}
	if err := strictJSON(c, &payload); err != nil {
		writeError(c, a.log, invalid("AI Provider 测试信息无效", err))
		return services.AIProviderProbeInput{}, false
	}
	return services.AIProviderProbeInput{ProviderType: payload.ProviderType, BaseURL: payload.BaseURL, APIKey: payload.APIKey, Model: payload.Model, Revision: payload.Revision}, true
}
