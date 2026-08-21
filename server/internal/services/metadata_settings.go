package services

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

// Keep the original AAD so pre-v11 encrypted Read Access Tokens remain
// decryptable; the adjacent database kind now determines authentication.
const tmdbTokenPurpose = "settings:metadata:tmdb-read-token"

type MetadataSettingsService struct {
	db            *gorm.DB
	audit         *AuditService
	credentials   *credential.Store
	deployment    tmdb.Credential
	apiTester     func(context.Context, tmdb.Credential, string, string) error
	imageTester   func(context.Context, string) error
	clientFactory func(tmdb.Credential, string, string) (*tmdb.Client, error)
}
type MetadataSettingsSummary struct {
	TMDBConfigured   bool      `json:"tmdb_configured"`
	CustomConfigured bool      `json:"custom_configured"`
	CredentialSource string    `json:"credential_source"`
	CredentialKind   string    `json:"credential_kind"`
	APIBaseURL       string    `json:"api_base_url"`
	ImageBaseURL     string    `json:"image_base_url"`
	Revision         uint64    `json:"revision"`
	UpdatedAt        time.Time `json:"updated_at"`
}
type UpdateMetadataSettingsInput struct {
	TMDBToken      string
	CredentialKind string
	ClearTMDB      bool
	Revision       uint64
}
type UpdateTMDBRouteInput struct {
	BaseURL  string
	Revision uint64
}

func NewMetadataSettingsService(db *gorm.DB, audit *AuditService, credentials *credential.Store, deploymentCredential ...tmdb.Credential) *MetadataSettingsService {
	configuredDeployment := tmdb.Credential{}
	if len(deploymentCredential) > 0 {
		configuredDeployment = deploymentCredential[0]
		configuredDeployment.Value = strings.TrimSpace(configuredDeployment.Value)
	}
	service := &MetadataSettingsService{db: db, audit: audit, credentials: credentials, deployment: configuredDeployment}
	service.apiTester = func(ctx context.Context, credential tmdb.Credential, apiBase, imageBase string) error {
		client, err := tmdb.NewWithCredentialRoutes(credential, apiBase, imageBase)
		if err != nil {
			return err
		}
		return client.TestAPI(ctx)
	}
	service.imageTester = tmdb.TestImageBase
	service.clientFactory = tmdb.NewWithCredentialRoutes
	return service
}

func (s *MetadataSettingsService) Get(actor Actor) (MetadataSettingsSummary, error) {
	if !actor.Can(authz.PermissionSettingsRead) {
		return MetadataSettingsSummary{}, appError(CodePermissionDenied, "无权查看元数据设置", nil)
	}
	record, err := s.record()
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	return s.summary(record), nil
}

func (s *MetadataSettingsService) Update(actor Actor, input UpdateMetadataSettingsInput, request RequestContext) (MetadataSettingsSummary, error) {
	if !actor.Can(authz.PermissionSettingsUpdate) {
		return MetadataSettingsSummary{}, appError(CodePermissionDenied, "无权编辑元数据设置", nil)
	}
	if err := validateMetadataRevision(input.Revision); err != nil {
		return MetadataSettingsSummary{}, err
	}
	token := strings.TrimSpace(input.TMDBToken)
	if input.ClearTMDB && token != "" {
		return MetadataSettingsSummary{}, appError(CodeInvalidRequest, "不能同时保存并清除 TMDB Token", nil)
	}
	if token != "" {
		return MetadataSettingsSummary{}, appError(CodeInvalidRequest, "TMDB 候选凭据必须先测试成功再保存", nil)
	}
	updates := map[string]any{"revision": input.Revision + 1, "updated_at": time.Now().UTC()}
	if input.ClearTMDB {
		updates["tmdb_token_ciphertext"] = ""
		updates["tmdb_credential_kind"] = string(tmdb.CredentialKindReadAccessToken)
	}
	updated, err := s.updateCAS(input.Revision, updates)
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	summary := s.summary(updated)
	_ = s.audit.Record(s.db, &actor.User.ID, "metadata_settings.update", "metadata_settings", "1", "success", map[string]any{"credential_source": summary.CredentialSource, "credential_kind": summary.CredentialKind, "custom_configured": summary.CustomConfigured}, request)
	return summary, nil
}

func (s *MetadataSettingsService) Test(ctx context.Context, actor Actor) error {
	if !actor.Can(authz.PermissionSettingsUpdate) {
		return appError(CodePermissionDenied, "无权测试元数据设置", nil)
	}
	record, err := s.record()
	if err != nil {
		return err
	}
	credential, _, err := s.effectiveCredential(record)
	if err != nil {
		return err
	}
	if err := s.apiTester(ctx, credential, record.APIBaseURL, record.ImageBaseURL); err != nil {
		return appError(CodeTMDBUnavailable, "TMDB API 测试失败，请检查凭据与网络", nil)
	}
	return nil
}

func (s *MetadataSettingsService) TestAndSetToken(ctx context.Context, actor Actor, input UpdateMetadataSettingsInput, request RequestContext) (MetadataSettingsSummary, error) {
	if !actor.Can(authz.PermissionSettingsUpdate) {
		return MetadataSettingsSummary{}, appError(CodePermissionDenied, "无权编辑元数据设置", nil)
	}
	if err := validateMetadataRevision(input.Revision); err != nil {
		return MetadataSettingsSummary{}, err
	}
	token := strings.TrimSpace(input.TMDBToken)
	if token == "" || input.ClearTMDB {
		return MetadataSettingsSummary{}, appError(CodeInvalidRequest, "请提供需要测试的 TMDB 凭据", nil)
	}
	candidate, err := candidateCredential(input.CredentialKind, token)
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	record, err := s.record()
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	if record.Revision != input.Revision {
		return MetadataSettingsSummary{}, appError(CodeConflict, "元数据设置已变化，请刷新", nil)
	}
	if err := s.apiTester(ctx, candidate, record.APIBaseURL, record.ImageBaseURL); err != nil {
		return MetadataSettingsSummary{}, appError(CodeTMDBUnavailable, "TMDB 凭据测试失败，原凭据已保留", nil)
	}
	ciphertext, err := s.credentials.Encrypt(tmdbTokenPurpose, candidate.Value)
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	updated, err := s.updateCAS(input.Revision, map[string]any{"tmdb_token_ciphertext": ciphertext, "tmdb_credential_kind": string(candidate.Kind), "revision": input.Revision + 1, "updated_at": time.Now().UTC()})
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	summary := s.summary(updated)
	_ = s.audit.Record(s.db, &actor.User.ID, "metadata_settings.token_test", "metadata_settings", "1", "success", map[string]any{"credential_source": summary.CredentialSource, "credential_kind": summary.CredentialKind}, request)
	return summary, nil
}

func (s *MetadataSettingsService) TestAndSetAPI(ctx context.Context, actor Actor, input UpdateTMDBRouteInput, request RequestContext) (MetadataSettingsSummary, error) {
	if !actor.Can(authz.PermissionSettingsUpdate) {
		return MetadataSettingsSummary{}, appError(CodePermissionDenied, "无权编辑元数据设置", nil)
	}
	if err := validateMetadataRevision(input.Revision); err != nil {
		return MetadataSettingsSummary{}, err
	}
	base, err := tmdb.ValidateBaseURL(input.BaseURL)
	if err != nil {
		return MetadataSettingsSummary{}, appError(CodeInvalidRequest, "TMDB API 地址必须是安全的 HTTPS 前缀", nil)
	}
	record, err := s.record()
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	if record.Revision != input.Revision {
		return MetadataSettingsSummary{}, appError(CodeConflict, "元数据设置已变化，请刷新", nil)
	}
	credential, _, err := s.effectiveCredential(record)
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	if err := s.apiTester(ctx, credential, base, record.ImageBaseURL); err != nil {
		return MetadataSettingsSummary{}, appError(CodeTMDBUnavailable, "TMDB API 地址测试失败，原设置已保留", nil)
	}
	updated, err := s.updateCAS(input.Revision, map[string]any{"api_base_url": base, "revision": input.Revision + 1, "updated_at": time.Now().UTC()})
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "metadata_settings.api_route", "metadata_settings", "1", "success", map[string]any{"default": base == tmdb.DefaultAPIBaseURL}, request)
	return s.summary(updated), nil
}

func (s *MetadataSettingsService) TestAndSetImage(ctx context.Context, actor Actor, input UpdateTMDBRouteInput, request RequestContext) (MetadataSettingsSummary, error) {
	if !actor.Can(authz.PermissionSettingsUpdate) {
		return MetadataSettingsSummary{}, appError(CodePermissionDenied, "无权编辑元数据设置", nil)
	}
	if err := validateMetadataRevision(input.Revision); err != nil {
		return MetadataSettingsSummary{}, err
	}
	base, err := tmdb.ValidateBaseURL(input.BaseURL)
	if err != nil {
		return MetadataSettingsSummary{}, appError(CodeInvalidRequest, "TMDB 图片地址必须是安全的 HTTPS 前缀", nil)
	}
	record, err := s.record()
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	if record.Revision != input.Revision {
		return MetadataSettingsSummary{}, appError(CodeConflict, "元数据设置已变化，请刷新", nil)
	}
	if err := s.imageTester(ctx, base); err != nil {
		return MetadataSettingsSummary{}, appError(CodeTMDBUnavailable, "TMDB 图片地址测试失败，原设置已保留", nil)
	}
	updated, err := s.updateCAS(input.Revision, map[string]any{"image_base_url": base, "revision": input.Revision + 1, "updated_at": time.Now().UTC()})
	if err != nil {
		return MetadataSettingsSummary{}, err
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "metadata_settings.image_route", "metadata_settings", "1", "success", map[string]any{"default": base == tmdb.DefaultImageBaseURL}, request)
	return s.summary(updated), nil
}

func (s *MetadataSettingsService) Client() (*tmdb.Client, error) {
	client, _, _, err := s.clientWithCredentialInfo()
	return client, err
}

// clientWithCredentialInfo returns only allowlisted credential metadata for
// runtime diagnostics. The credential value never leaves this service.
func (s *MetadataSettingsService) clientWithCredentialInfo() (*tmdb.Client, string, string, error) {
	record, err := s.record()
	if err != nil {
		return nil, "none", "", err
	}
	credential, source, err := s.effectiveCredential(record)
	if err != nil {
		return nil, source, "", err
	}
	client, err := s.clientFactory(credential, record.APIBaseURL, record.ImageBaseURL)
	if err != nil {
		return nil, source, string(credential.Kind), appError(CodeTMDBUnavailable, "TMDB 路由配置无效", nil)
	}
	return client, source, string(credential.Kind), nil
}

func (s *MetadataSettingsService) record() (models.MetadataSettings, error) {
	var record models.MetadataSettings
	err := s.db.First(&record, 1).Error
	if record.APIBaseURL == "" {
		record.APIBaseURL = tmdb.DefaultAPIBaseURL
	}
	if record.ImageBaseURL == "" {
		record.ImageBaseURL = tmdb.DefaultImageBaseURL
	}
	return record, err
}
func (s *MetadataSettingsService) effectiveCredential(record models.MetadataSettings) (tmdb.Credential, string, error) {
	if record.TMDBTokenCiphertext != "" {
		token, err := s.credentials.Decrypt(tmdbTokenPurpose, record.TMDBTokenCiphertext)
		if err != nil {
			return tmdb.Credential{}, "none", err
		}
		kind := tmdb.CredentialKind(record.TMDBCredentialKind)
		if kind == "" { // Defensive compatibility for records read before v11 migration.
			kind = tmdb.CredentialKindReadAccessToken
		}
		credential, err := tmdb.ValidateCredential(tmdb.Credential{Kind: kind, Value: token})
		if err != nil {
			return tmdb.Credential{}, "none", err
		}
		return credential, "custom", nil
	}
	if s.deployment.Value != "" {
		credential, err := tmdb.ValidateCredential(s.deployment)
		if err != nil {
			return tmdb.Credential{}, "none", err
		}
		return credential, "deployment", nil
	}
	builtinToken := strings.TrimSpace(tmdb.BuiltinReadAccessToken)
	builtinAPIKey := strings.TrimSpace(tmdb.BuiltinAPIKey)
	if builtinToken != "" && builtinAPIKey != "" {
		return tmdb.Credential{}, "none", appError(CodeTMDBTokenInvalid, "Server 内置 TMDB 凭据配置冲突", nil)
	}
	if token := builtinToken; token != "" {
		return tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: token}, "builtin", nil
	}
	if apiKey := builtinAPIKey; apiKey != "" {
		return tmdb.Credential{Kind: tmdb.CredentialKindAPIKey, Value: apiKey}, "builtin", nil
	}
	return tmdb.Credential{}, "none", appError(CodeTMDBTokenInvalid, "当前 Server 没有可用的 TMDB 凭据", nil)
}
func (s *MetadataSettingsService) summary(record models.MetadataSettings) MetadataSettingsSummary {
	credential, source, err := s.effectiveCredential(record)
	if err != nil {
		source = "none"
	}
	return MetadataSettingsSummary{TMDBConfigured: source != "none", CustomConfigured: record.TMDBTokenCiphertext != "", CredentialSource: source, CredentialKind: string(credential.Kind), APIBaseURL: record.APIBaseURL, ImageBaseURL: record.ImageBaseURL, Revision: record.Revision, UpdatedAt: record.UpdatedAt}
}

func candidateCredential(kindRaw, value string) (tmdb.Credential, error) {
	kind := tmdb.CredentialKind(strings.TrimSpace(kindRaw))
	if kind == "" {
		// Older Server clients only sent tmdb_token. Preserve that contract.
		kind = tmdb.CredentialKindReadAccessToken
	}
	credential, err := tmdb.ValidateCredential(tmdb.Credential{Kind: kind, Value: value})
	if err != nil {
		return tmdb.Credential{}, appError(CodeTMDBTokenInvalid, "TMDB 凭据类型或内容无效", nil)
	}
	return credential, nil
}
func (s *MetadataSettingsService) updateCAS(revision uint64, updates map[string]any) (models.MetadataSettings, error) {
	result := s.db.Model(&models.MetadataSettings{}).Where("id = ? AND revision = ?", 1, revision).Updates(updates)
	if result.Error != nil {
		return models.MetadataSettings{}, result.Error
	}
	if result.RowsAffected != 1 {
		return models.MetadataSettings{}, appError(CodeConflict, "元数据设置已变化，请刷新", nil)
	}
	return s.record()
}
func validateMetadataRevision(revision uint64) error {
	if revision == 0 || revision >= math.MaxInt64 {
		return appError(CodeConflict, "元数据设置版本无效，请刷新", nil)
	}
	return nil
}
