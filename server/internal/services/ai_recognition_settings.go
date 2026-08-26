package services

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/aiprovider"
	"gorm.io/gorm"
)

const aiRecognitionAPIKeyPurpose = "settings:ai-recognition:api-key:v1"

type AIRecognitionSettingsService struct {
	db              *gorm.DB
	audit           *AuditService
	credentials     *credential.Store
	providerFactory func(aiprovider.Config) (aiprovider.Provider, error)
}

type AIRecognitionSettingsSummary struct {
	Enabled               bool      `json:"enabled"`
	ProviderType          string    `json:"provider_type"`
	BaseURL               string    `json:"base_url"`
	APIKeyConfigured      bool      `json:"api_key_configured"`
	Model                 string    `json:"model"`
	SendRelativeBasenames bool      `json:"send_relative_basenames"`
	Revision              uint64    `json:"revision"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type UpdateAIRecognitionSettingsInput struct {
	Enabled               bool
	ProviderType          string
	BaseURL               string
	APIKey                string
	ClearAPIKey           bool
	Model                 string
	SendRelativeBasenames bool
	Revision              uint64
}

// AIProviderProbeInput describes an explicit administrator action. Empty
// fields reuse the saved value; the candidate API key is transient and never
// persisted by TestConnection or ListModels.
type AIProviderProbeInput struct {
	ProviderType string
	BaseURL      string
	APIKey       string
	Model        string
	Revision     uint64
}

func NewAIRecognitionSettingsService(db *gorm.DB, audit *AuditService, credentials *credential.Store) *AIRecognitionSettingsService {
	return &AIRecognitionSettingsService{db: db, audit: audit, credentials: credentials, providerFactory: aiprovider.New}
}

func (s *AIRecognitionSettingsService) Get(actor Actor) (AIRecognitionSettingsSummary, error) {
	if !actor.Can(authz.PermissionSettingsRead) {
		return AIRecognitionSettingsSummary{}, appError(CodePermissionDenied, "无权查看 AI 识别设置", nil)
	}
	record, err := s.record()
	if err != nil {
		return AIRecognitionSettingsSummary{}, err
	}
	return summarizeAISettings(record), nil
}

func (s *AIRecognitionSettingsService) Update(actor Actor, input UpdateAIRecognitionSettingsInput, request RequestContext) (AIRecognitionSettingsSummary, error) {
	if !actor.Can(authz.PermissionSettingsUpdate) {
		return AIRecognitionSettingsSummary{}, appError(CodePermissionDenied, "无权编辑 AI 识别设置", nil)
	}
	if input.Revision == 0 || input.Revision == math.MaxUint64 {
		return AIRecognitionSettingsSummary{}, appError(CodeConflict, "AI 识别设置版本无效，请刷新", nil)
	}
	record, err := s.record()
	if err != nil {
		return AIRecognitionSettingsSummary{}, err
	}
	if record.Revision != input.Revision {
		return AIRecognitionSettingsSummary{}, appError(CodeConflict, "AI 识别设置已变化，请刷新", nil)
	}
	providerType, baseURL, model, err := normalizeAISettings(input.ProviderType, input.BaseURL, input.Model)
	if err != nil {
		return AIRecognitionSettingsSummary{}, err
	}
	apiKey := strings.TrimSpace(input.APIKey)
	if input.ClearAPIKey && apiKey != "" {
		return AIRecognitionSettingsSummary{}, appError(CodeInvalidRequest, "不能同时保存并清除 AI API Key", nil)
	}
	if len(apiKey) > 4096 || strings.ContainsAny(apiKey, "\r\n") {
		return AIRecognitionSettingsSummary{}, appError(CodeAIConfigurationInvalid, "AI API Key 格式无效", nil)
	}
	providerChanged := providerType != record.ProviderType
	if providerChanged && record.APIKeyCiphertext != "" && apiKey == "" && !input.ClearAPIKey {
		return AIRecognitionSettingsSummary{}, appError(CodeAIConfigurationInvalid, "切换 AI Provider 时请重新填写 API Key", nil)
	}
	configured := record.APIKeyCiphertext != ""
	updates := map[string]any{
		"enabled": input.Enabled, "provider_type": providerType, "base_url": baseURL, "model": model,
		"send_relative_basenames": input.SendRelativeBasenames, "revision": input.Revision + 1, "updated_at": time.Now().UTC(),
	}
	if input.ClearAPIKey {
		updates["api_key_ciphertext"] = ""
		configured = false
	} else if apiKey != "" {
		ciphertext, encryptErr := s.credentials.Encrypt(aiRecognitionAPIKeyPurpose, apiKey)
		if encryptErr != nil {
			return AIRecognitionSettingsSummary{}, encryptErr
		}
		updates["api_key_ciphertext"] = ciphertext
		configured = true
	}
	if input.Enabled && (!configured || model == "") {
		return AIRecognitionSettingsSummary{}, appError(CodeAIConfigurationInvalid, "开启 AI 识别前请配置 API Key 和模型", nil)
	}
	result := s.db.Model(&models.AIRecognitionSettings{}).Where("id = ? AND revision = ?", 1, input.Revision).Updates(updates)
	if result.Error != nil {
		return AIRecognitionSettingsSummary{}, result.Error
	}
	if result.RowsAffected != 1 {
		return AIRecognitionSettingsSummary{}, appError(CodeConflict, "AI 识别设置已变化，请刷新", nil)
	}
	var updated models.AIRecognitionSettings
	if err := s.db.First(&updated, 1).Error; err != nil {
		return AIRecognitionSettingsSummary{}, err
	}
	summary := summarizeAISettings(updated)
	_ = s.audit.Record(s.db, &actor.User.ID, "ai_recognition_settings.update", "ai_recognition_settings", "1", "success", map[string]any{
		"enabled": summary.Enabled, "provider_type": summary.ProviderType, "api_key_configured": summary.APIKeyConfigured,
		"send_relative_basenames": summary.SendRelativeBasenames,
	}, request)
	return summary, nil
}

func (s *AIRecognitionSettingsService) TestConnection(ctx context.Context, actor Actor, input AIProviderProbeInput, request RequestContext) error {
	if !actor.Can(authz.PermissionSettingsUpdate) {
		return appError(CodePermissionDenied, "无权测试 AI 识别设置", nil)
	}
	provider, config, err := s.probeProvider(input)
	if err != nil {
		s.auditProbe(actor, "test", input.ProviderType, "failure", ErrorCode(err), request)
		return err
	}
	if err := provider.Test(ctx); err != nil {
		mapped := mapAIProviderError(err)
		s.auditProbe(actor, "test", config.ProviderType, "failure", ErrorCode(mapped), request)
		return mapped
	}
	s.auditProbe(actor, "test", config.ProviderType, "success", "", request)
	return nil
}

func (s *AIRecognitionSettingsService) ListModels(ctx context.Context, actor Actor, input AIProviderProbeInput, request RequestContext) ([]aiprovider.Model, error) {
	if !actor.Can(authz.PermissionSettingsUpdate) {
		return nil, appError(CodePermissionDenied, "无权读取 AI 模型列表", nil)
	}
	provider, config, err := s.probeProvider(input)
	if err != nil {
		s.auditProbe(actor, "models", input.ProviderType, "failure", ErrorCode(err), request)
		return nil, err
	}
	models, err := provider.ListModels(ctx)
	if err != nil {
		mapped := mapAIProviderError(err)
		s.auditProbe(actor, "models", config.ProviderType, "failure", ErrorCode(mapped), request)
		return nil, mapped
	}
	s.auditProbe(actor, "models", config.ProviderType, "success", "", request)
	return models, nil
}

// RuntimeConfig is the single runtime gate. Disabled settings return before
// decrypting the API key, so callers cannot accidentally create a provider or
// issue an AI request while the feature is off.
func (s *AIRecognitionSettingsService) RuntimeConfig() (aiprovider.Config, bool, error) {
	record, err := s.record()
	if err != nil {
		return aiprovider.Config{}, false, err
	}
	if !record.Enabled {
		return aiprovider.Config{}, false, nil
	}
	config, err := s.configFromRecord(record)
	if err != nil {
		return aiprovider.Config{}, false, err
	}
	return config, true, nil
}

// RuntimeRelativeBasenamesEnabled exposes only the opt-in payload policy. It
// never decrypts the API key and is consulted only after recognition reaches
// an AI-eligible branch.
func (s *AIRecognitionSettingsService) RuntimeRelativeBasenamesEnabled() bool {
	record, err := s.record()
	return err == nil && record.Enabled && record.SendRelativeBasenames
}

func (s *AIRecognitionSettingsService) GenerateCandidateArbitration(ctx context.Context, payload aiprovider.CandidateArbitrationPayload) (aiprovider.CandidateArbitrationResult, error) {
	var empty aiprovider.CandidateArbitrationResult
	if err := aiprovider.ValidateArbitrationPayload(payload); err != nil {
		return empty, mapAIProviderError(err)
	}
	provider, enabled, err := s.runtimeProvider()
	if err != nil || !enabled {
		if !enabled && err == nil {
			err = aiprovider.ErrDisabled
		}
		return empty, err
	}
	raw, err := provider.GenerateStructured(ctx, aiprovider.StructuredRequest{SystemPrompt: aiprovider.CandidateArbitrationSystemPrompt, Payload: payload, SchemaName: "media_candidate_arbitration", Schema: aiprovider.CandidateArbitrationSchema()})
	if err != nil {
		return empty, mapAIProviderError(err)
	}
	result, err := aiprovider.DecodeCandidateArbitration(raw, payload)
	if err != nil {
		return empty, mapAIProviderError(err)
	}
	return result, nil
}

func (s *AIRecognitionSettingsService) GenerateTitleRewrite(ctx context.Context, payload aiprovider.TitleRewritePayload) (aiprovider.TitleRewriteResult, error) {
	var empty aiprovider.TitleRewriteResult
	if err := aiprovider.ValidateRewritePayload(payload); err != nil {
		return empty, mapAIProviderError(err)
	}
	provider, enabled, err := s.runtimeProvider()
	if err != nil || !enabled {
		if !enabled && err == nil {
			err = aiprovider.ErrDisabled
		}
		return empty, err
	}
	raw, err := provider.GenerateStructured(ctx, aiprovider.StructuredRequest{SystemPrompt: aiprovider.TitleRewriteSystemPrompt, Payload: payload, SchemaName: "media_title_rewrite", Schema: aiprovider.TitleRewriteSchema()})
	if err != nil {
		return empty, mapAIProviderError(err)
	}
	result, err := aiprovider.DecodeTitleRewrite(raw)
	if err != nil {
		return empty, mapAIProviderError(err)
	}
	return result, nil
}

func (s *AIRecognitionSettingsService) runtimeProvider() (aiprovider.Provider, bool, error) {
	config, enabled, err := s.RuntimeConfig()
	if err != nil || !enabled {
		return nil, enabled, err
	}
	provider, err := s.providerFactory(config)
	if err != nil {
		return nil, true, mapAIProviderError(err)
	}
	return provider, true, nil
}

func (s *AIRecognitionSettingsService) probeProvider(input AIProviderProbeInput) (aiprovider.Provider, aiprovider.Config, error) {
	record, err := s.record()
	if err != nil {
		return nil, aiprovider.Config{}, err
	}
	if input.Revision == 0 || input.Revision != record.Revision {
		return nil, aiprovider.Config{}, appError(CodeConflict, "AI 识别设置已变化，请刷新", nil)
	}
	config, err := s.configFromRecord(record)
	if err != nil && strings.TrimSpace(input.APIKey) == "" {
		return nil, aiprovider.Config{}, err
	}
	if value := strings.TrimSpace(input.ProviderType); value != "" {
		if value != record.ProviderType && strings.TrimSpace(input.APIKey) == "" {
			return nil, aiprovider.Config{}, appError(CodeAIConfigurationInvalid, "测试另一种 Provider 时请填写对应 API Key", nil)
		}
		config.ProviderType = value
	}
	if value := strings.TrimSpace(input.BaseURL); value != "" || config.ProviderType == aiprovider.ProviderGoogleAIStudio {
		config.BaseURL = value
	}
	if value := strings.TrimSpace(input.APIKey); value != "" {
		config.APIKey = value
	}
	if value := strings.TrimSpace(input.Model); value != "" {
		config.Model = value
	}
	provider, err := s.providerFactory(config)
	if err != nil {
		return nil, config, mapAIProviderError(err)
	}
	return provider, config, nil
}

func (s *AIRecognitionSettingsService) configFromRecord(record models.AIRecognitionSettings) (aiprovider.Config, error) {
	if record.APIKeyCiphertext == "" {
		return aiprovider.Config{}, appError(CodeAIConfigurationInvalid, "尚未配置 AI API Key", nil)
	}
	apiKey, err := s.credentials.Decrypt(aiRecognitionAPIKeyPurpose, record.APIKeyCiphertext)
	if err != nil {
		return aiprovider.Config{}, err
	}
	return aiprovider.Config{ProviderType: record.ProviderType, BaseURL: record.BaseURL, APIKey: apiKey, Model: record.Model}, nil
}

func (s *AIRecognitionSettingsService) record() (models.AIRecognitionSettings, error) {
	var record models.AIRecognitionSettings
	return record, s.db.First(&record, 1).Error
}

func normalizeAISettings(providerType, baseURL, model string) (string, string, string, error) {
	providerType = strings.TrimSpace(providerType)
	baseURL = strings.TrimSpace(baseURL)
	model = strings.TrimSpace(model)
	if len(model) > 256 || strings.ContainsAny(model, "\r\n?#") {
		return "", "", "", appError(CodeAIConfigurationInvalid, "AI 模型名称格式无效", nil)
	}
	if providerType == aiprovider.ProviderGoogleAIStudio {
		baseURL = aiprovider.GoogleAIStudioBaseURL
	}
	if _, err := aiprovider.New(aiprovider.Config{ProviderType: providerType, BaseURL: baseURL, APIKey: "configuration-validation", Model: model}); err != nil {
		return "", "", "", mapAIProviderError(err)
	}
	return providerType, baseURL, model, nil
}

func summarizeAISettings(record models.AIRecognitionSettings) AIRecognitionSettingsSummary {
	baseURL := record.BaseURL
	if record.ProviderType == aiprovider.ProviderGoogleAIStudio {
		baseURL = aiprovider.GoogleAIStudioBaseURL
	}
	return AIRecognitionSettingsSummary{Enabled: record.Enabled, ProviderType: record.ProviderType, BaseURL: baseURL, APIKeyConfigured: record.APIKeyCiphertext != "", Model: record.Model, SendRelativeBasenames: record.SendRelativeBasenames, Revision: record.Revision, UpdatedAt: record.UpdatedAt}
}

func mapAIProviderError(err error) error {
	if err == nil || err == aiprovider.ErrDisabled {
		return err
	}
	switch aiprovider.ErrorCode(err) {
	case aiprovider.ErrorInvalidConfig:
		return appError(CodeAIConfigurationInvalid, "AI Provider 配置无效", err)
	case aiprovider.ErrorAuthentication:
		return appError(CodeAIAuthentication, "AI Provider 认证失败，请检查 API Key", err)
	case aiprovider.ErrorRateLimited:
		return appError(CodeAIRateLimited, "AI Provider 请求受限，请稍后重试", err)
	case aiprovider.ErrorResponseInvalid, aiprovider.ErrorResponseTooLarge, aiprovider.ErrorSchemaUnsupported:
		return appError(CodeAIResponseInvalid, "AI Provider 返回了无效的结构化结果", err)
	default:
		return appError(CodeAIUnavailable, "AI Provider 暂时不可用", err)
	}
}

func (s *AIRecognitionSettingsService) auditProbe(actor Actor, action, providerType, outcome, code string, request RequestContext) {
	metadata := map[string]any{"provider_type": strings.TrimSpace(providerType)}
	if code != "" {
		metadata["error_code"] = code
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "ai_recognition_settings."+action, "ai_recognition_settings", "1", outcome, metadata, request)
}
