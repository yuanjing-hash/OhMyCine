package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
	"gorm.io/gorm"
)

const maxPluginConnectionConfigBytes = 64 * 1024

var sensitiveConfigKey = regexp.MustCompile(`(?i)(password|secret|token|cookie|authorization|api[_-]?key|passkey|credential)`)

type PluginConnectionSummary struct {
	ID                   string          `json:"id"`
	PluginID             string          `json:"plugin_id"`
	Name                 string          `json:"name"`
	Config               json.RawMessage `json:"config"`
	CredentialScope      string          `json:"credential_scope"`
	CredentialMode       string          `json:"credential_mode"`
	CredentialConfigured bool            `json:"credential_configured"`
	Enabled              bool            `json:"enabled"`
	Revision             uint64          `json:"revision"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type CreatePluginConnectionInput struct {
	Name            string
	Config          json.RawMessage
	CredentialScope string
	CredentialMode  string
	Credential      string
	Enabled         bool
}

type UpdatePluginConnectionInput struct {
	Name            *string
	Config          *json.RawMessage
	CredentialScope *string
	CredentialMode  *string
	Credential      *string
	ClearCredential bool
	Enabled         *bool
	Revision        uint64
}

func (s *PluginRepositoryService) ListConnections(actor Actor, pluginID string) ([]PluginConnectionSummary, error) {
	if !actor.Can(authz.PermissionPluginsRead) {
		return nil, appError(CodePermissionDenied, "无权查看插件连接", nil)
	}
	var records []models.PluginConnection
	if err := s.db.Where("plugin_id = ?", pluginID).Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	result := make([]PluginConnectionSummary, 0, len(records))
	for _, record := range records {
		result = append(result, pluginConnectionSummary(record))
	}
	return result, nil
}

func (s *PluginRepositoryService) CreateConnection(actor Actor, pluginID string, input CreatePluginConnectionInput, request RequestContext) (PluginConnectionSummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return PluginConnectionSummary{}, appError(CodePermissionDenied, "无权创建插件连接", nil)
	}
	if s.credentials == nil {
		return PluginConnectionSummary{}, appError(CodePluginRuntimeUnavailable, "插件凭据服务不可用", nil)
	}
	manifest, err := s.installedManifest(pluginID)
	if err != nil {
		return PluginConnectionSummary{}, err
	}
	name, config, scope, mode, err := normalizePluginConnectionInput(manifest, input.Name, input.Config, input.CredentialScope, input.CredentialMode, input.Credential != "")
	if err != nil {
		return PluginConnectionSummary{}, err
	}
	now := time.Now().UTC()
	record := models.PluginConnection{ID: uuid.NewString(), PluginID: pluginID, Name: name, ConfigJSON: string(config), CredentialScope: scope, CredentialMode: mode, Enabled: input.Enabled, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if input.Credential != "" {
		ciphertext, err := s.credentials.Encrypt(hostapi.CredentialPurpose(pluginID, record.ID, scope), strings.TrimSpace(input.Credential))
		if err != nil {
			return PluginConnectionSummary{}, err
		}
		record.CredentialCiphertext = ciphertext
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin_connection.create", "plugin_connection", record.ID, "success", map[string]any{"plugin_id": pluginID, "credential_scope": scope, "credential_mode": mode, "credential_configured": record.CredentialCiphertext != ""}, request)
	}); err != nil {
		return PluginConnectionSummary{}, err
	}
	return pluginConnectionSummary(record), nil
}

func (s *PluginRepositoryService) UpdateConnection(actor Actor, pluginID, connectionID string, input UpdatePluginConnectionInput, request RequestContext) (PluginConnectionSummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return PluginConnectionSummary{}, appError(CodePermissionDenied, "无权修改插件连接", nil)
	}
	if s.credentials == nil || input.Revision == 0 || input.Revision >= math.MaxInt64 || (input.Credential != nil && input.ClearCredential) {
		return PluginConnectionSummary{}, appError(CodeInvalidRequest, "插件连接修改请求无效", nil)
	}
	manifest, err := s.installedManifest(pluginID)
	if err != nil {
		return PluginConnectionSummary{}, err
	}
	var current models.PluginConnection
	if err := s.db.First(&current, "id = ? AND plugin_id = ?", connectionID, pluginID).Error; err != nil {
		return PluginConnectionSummary{}, pluginConnectionNotFound(err)
	}
	name := current.Name
	if input.Name != nil {
		name = *input.Name
	}
	config := json.RawMessage(current.ConfigJSON)
	if input.Config != nil {
		config = *input.Config
	}
	scope, mode := current.CredentialScope, current.CredentialMode
	if input.CredentialScope != nil {
		scope = *input.CredentialScope
	}
	if input.CredentialMode != nil {
		mode = *input.CredentialMode
	}
	credentialWillExist := current.CredentialCiphertext != ""
	if input.Credential != nil {
		credentialWillExist = strings.TrimSpace(*input.Credential) != ""
	}
	if input.ClearCredential {
		credentialWillExist = false
	}
	name, config, scope, mode, err = normalizePluginConnectionInput(manifest, name, config, scope, mode, credentialWillExist)
	if err != nil {
		return PluginConnectionSummary{}, err
	}
	updates := map[string]any{"name": name, "config_json": string(config), "credential_scope": scope, "credential_mode": mode, "revision": input.Revision + 1, "updated_at": time.Now().UTC()}
	if input.Enabled != nil {
		updates["enabled"] = *input.Enabled
	}
	if input.ClearCredential || (input.Credential != nil && strings.TrimSpace(*input.Credential) == "") {
		updates["credential_ciphertext"] = ""
		updates["credential_scope"] = ""
		updates["credential_mode"] = models.PluginCredentialModeNone
	} else if input.Credential != nil {
		ciphertext, err := s.credentials.Encrypt(hostapi.CredentialPurpose(pluginID, connectionID, scope), strings.TrimSpace(*input.Credential))
		if err != nil {
			return PluginConnectionSummary{}, err
		}
		updates["credential_ciphertext"] = ciphertext
	} else if scope != current.CredentialScope && current.CredentialCiphertext != "" {
		return PluginConnectionSummary{}, appError(CodeConflict, "修改凭据范围时必须重新填写凭据", nil)
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.PluginConnection{}).Where("id = ? AND plugin_id = ? AND revision = ?", connectionID, pluginID, input.Revision).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodePluginRevisionConflict, "插件连接已变化，请刷新后重试", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin_connection.update", "plugin_connection", connectionID, "success", map[string]any{"plugin_id": pluginID, "credential_scope": updates["credential_scope"], "credential_mode": updates["credential_mode"], "credential_changed": input.Credential != nil || input.ClearCredential}, request)
	}); err != nil {
		return PluginConnectionSummary{}, err
	}
	if err := s.db.First(&current, "id = ?", connectionID).Error; err != nil {
		return PluginConnectionSummary{}, err
	}
	return pluginConnectionSummary(current), nil
}

func (s *PluginRepositoryService) DeleteConnection(actor Actor, pluginID, connectionID string, revision uint64, request RequestContext) error {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return appError(CodePermissionDenied, "无权删除插件连接", nil)
	}
	if revision == 0 {
		return appError(CodeInvalidRequest, "插件连接 revision 无效", nil)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND plugin_id = ? AND revision = ?", connectionID, pluginID, revision).Delete(&models.PluginConnection{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodePluginRevisionConflict, "插件连接已变化，请刷新后重试", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin_connection.delete", "plugin_connection", connectionID, "success", map[string]any{"plugin_id": pluginID}, request)
	})
}

// InvokePlugin is the generic site-operation boundary used by online-library
// services. It never interprets provider-specific IDs or response fields.
func (s *PluginRepositoryService) InvokePlugin(ctx context.Context, connectionID, operation string, request any) ([]byte, error) {
	if s.runtime == nil {
		return nil, appError(CodePluginRuntimeUnavailable, "插件运行时不可用", nil)
	}
	var connection models.PluginConnection
	if err := s.db.First(&connection, "id = ? AND enabled = ?", connectionID, true).Error; err != nil {
		return nil, pluginConnectionNotFound(err)
	}
	manifest, err := s.installedManifest(connection.PluginID)
	if err != nil {
		return nil, err
	}
	if !manifestHasCapability(manifest, contract.Capability(operation)) {
		return nil, appError(CodePermissionDenied, "插件未声明此能力", nil)
	}
	payload, err := json.Marshal(request)
	if err != nil || len(payload) > maxPluginConnectionConfigBytes {
		return nil, appError(CodeInvalidRequest, "插件调用请求无效", err)
	}
	response, err := s.runtime.Invoke(ctx, connection.PluginID, operation, payload)
	if err != nil {
		return nil, appError(CodePluginRuntimeUnavailable, "插件调用失败", err)
	}
	return response, nil
}

func (s *PluginRepositoryService) installedManifest(pluginID string) (contract.Manifest, error) {
	installation, _, manifest, err := s.loadInstalled(pluginID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return contract.Manifest{}, appError(CodeNotFound, "插件未安装", err)
		}
		return contract.Manifest{}, err
	}
	if installation.Status == models.PluginInstallationFailed {
		return contract.Manifest{}, appError(CodePluginRuntimeUnavailable, "插件当前不可用", nil)
	}
	return manifest, nil
}

func normalizePluginConnectionInput(manifest contract.Manifest, name string, config json.RawMessage, scope, mode string, hasCredential bool) (string, json.RawMessage, string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return "", nil, "", "", appError(CodeInvalidRequest, "插件连接名称无效", nil)
	}
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	if len(config) > maxPluginConnectionConfigBytes || !json.Valid(config) || !safePluginConfig(config) {
		return "", nil, "", "", appError(CodeInvalidRequest, "插件连接配置无效或包含敏感字段", nil)
	}
	scope, mode = strings.TrimSpace(scope), strings.TrimSpace(mode)
	if !hasCredential {
		return name, append(json.RawMessage(nil), config...), "", models.PluginCredentialModeNone, nil
	}
	if scope == "" || !manifestHasCredentialScope(manifest, scope) {
		return "", nil, "", "", appError(CodePermissionDenied, "插件未声明此凭据范围", nil)
	}
	if mode != models.PluginCredentialModeCookie && mode != models.PluginCredentialModeBearer {
		return "", nil, "", "", appError(CodeInvalidRequest, "插件凭据模式无效", nil)
	}
	return name, append(json.RawMessage(nil), config...), scope, mode, nil
}

func safePluginConfig(config json.RawMessage) bool {
	decoder := json.NewDecoder(strings.NewReader(string(config)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	root, ok := value.(map[string]any)
	return ok && safePluginConfigValue(root, 0)
}

func safePluginConfigValue(value any, depth int) bool {
	if depth > 8 {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) > 128 {
			return false
		}
		for key, child := range typed {
			if len(key) > 128 || sensitiveConfigKey.MatchString(key) || !safePluginConfigValue(child, depth+1) {
				return false
			}
		}
	case []any:
		if len(typed) > 256 {
			return false
		}
		for _, child := range typed {
			if !safePluginConfigValue(child, depth+1) {
				return false
			}
		}
	case string:
		return len(typed) <= 4096
	case json.Number, bool, nil:
		return true
	default:
		return false
	}
	return true
}

func manifestHasCredentialScope(manifest contract.Manifest, scope string) bool {
	for _, permission := range manifest.Permissions {
		if permission.Kind == contract.PermissionCredentialUse {
			for _, allowed := range permission.Scopes {
				if allowed == scope {
					return true
				}
			}
		}
	}
	return false
}

func manifestHasCapability(manifest contract.Manifest, capability contract.Capability) bool {
	for _, current := range manifest.Capabilities {
		if current == capability {
			return true
		}
	}
	return false
}

func pluginConnectionSummary(record models.PluginConnection) PluginConnectionSummary {
	return PluginConnectionSummary{ID: record.ID, PluginID: record.PluginID, Name: record.Name, Config: json.RawMessage(record.ConfigJSON), CredentialScope: record.CredentialScope, CredentialMode: record.CredentialMode, CredentialConfigured: record.CredentialCiphertext != "", Enabled: record.Enabled, Revision: record.Revision, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}

func pluginConnectionNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "插件连接不存在", err)
	}
	return err
}
