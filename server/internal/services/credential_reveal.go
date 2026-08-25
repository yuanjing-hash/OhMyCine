package services

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
	"gorm.io/gorm"
)

var credentialRevealIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

const (
	CredentialResourceConnection       = "connection"
	CredentialResourceDownloader       = "downloader"
	CredentialResourceSite             = "site"
	CredentialResourceCookieCloud      = "cookiecloud"
	CredentialResourceMetadata         = "metadata"
	CredentialResourcePluginConnection = "plugin_connection"
)

// CredentialRevealInput is deliberately generic only at the HTTP boundary.
// reveal() below owns the complete resource/field allowlist so no caller can
// supply a database column or encryption purpose.
type CredentialRevealInput struct {
	ResourceType string
	ResourceID   string
	Field        string
}

type CredentialRevealResult struct {
	Value string `json:"value"`
}

type CredentialRevealService struct {
	db          *gorm.DB
	audit       *AuditService
	credentials *credential.Store
}

func NewCredentialRevealService(db *gorm.DB, audit *AuditService, credentials *credential.Store) *CredentialRevealService {
	return &CredentialRevealService{db: db, audit: audit, credentials: credentials}
}

func (s *CredentialRevealService) Reveal(actor Actor, input CredentialRevealInput, request RequestContext) (CredentialRevealResult, error) {
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.Field = strings.TrimSpace(input.Field)
	if err := validateCredentialRevealInput(input); err != nil {
		s.auditInvalidReveal(actor, input, request)
		return CredentialRevealResult{}, err
	}
	if !actor.Can(authz.PermissionConnectionsSecretsExport) {
		s.auditReveal(actor, input, "failure", CodePermissionDenied, request)
		return CredentialRevealResult{}, appError(CodePermissionDenied, "无权查看已保存凭据", nil)
	}
	value, err := s.reveal(input)
	if err != nil {
		s.auditReveal(actor, input, "failure", ErrorCode(err), request)
		return CredentialRevealResult{}, err
	}
	if value == "" {
		err = appError(CodeNotFound, "该凭据尚未配置", nil)
		s.auditReveal(actor, input, "failure", ErrorCode(err), request)
		return CredentialRevealResult{}, err
	}
	if err := s.audit.Record(s.db, &actor.User.ID, "credential.reveal", "external_credential", input.ResourceType+":"+input.ResourceID, "success", map[string]any{
		"resource_type": input.ResourceType,
		"field":         input.Field,
	}, request); err != nil {
		return CredentialRevealResult{}, err
	}
	return CredentialRevealResult{Value: value}, nil
}

func (s *CredentialRevealService) auditInvalidReveal(actor Actor, input CredentialRevealInput, request RequestContext) {
	metadata := map[string]any{"error_code": CodeInvalidRequest}
	if len(input.ResourceType) <= 64 && credentialRevealIdentifier.MatchString(input.ResourceType) {
		metadata["resource_type"] = input.ResourceType
	}
	if len(input.Field) <= 64 && credentialRevealIdentifier.MatchString(input.Field) {
		metadata["field"] = input.Field
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "credential.reveal", "external_credential", "invalid", "failure", metadata, request)
}

func (s *CredentialRevealService) reveal(input CredentialRevealInput) (string, error) {
	switch input.ResourceType {
	case CredentialResourceConnection:
		id, err := revealUintID(input.ResourceID)
		if err != nil {
			return "", err
		}
		var record models.Connection
		if err := s.db.First(&record, id).Error; err != nil {
			return "", revealNotFound(err)
		}
		switch input.Field {
		case "credential":
			return s.decrypt(connectionPurpose(record.ID, record.Provider), record.CredentialCiphertext)
		case "recycle_password":
			if record.Provider != models.ConnectionProviderPan115 {
				return "", revealInvalid()
			}
			return s.decrypt(connectionRecyclePurpose(record.ID), record.RecycleCredentialCiphertext)
		default:
			return "", revealInvalid()
		}
	case CredentialResourceDownloader:
		if input.ResourceID == "" || len(input.ResourceID) > 64 {
			return "", revealInvalid()
		}
		var record models.Downloader
		if err := s.db.First(&record, "id = ?", input.ResourceID).Error; err != nil {
			return "", revealNotFound(err)
		}
		switch input.Field {
		case "username":
			return s.decrypt(downloaderPurpose(record.ID, "username"), record.UsernameCiphertext)
		case "password":
			return s.decrypt(downloaderPurpose(record.ID, "password"), record.PasswordCiphertext)
		default:
			return "", revealInvalid()
		}
	case CredentialResourceSite:
		id, err := revealUintID(input.ResourceID)
		if err != nil {
			return "", err
		}
		var record models.Site
		if err := s.db.First(&record, id).Error; err != nil {
			return "", revealNotFound(err)
		}
		raw, err := s.decrypt(siteCredentialPurpose(record.ID, record.Kind), record.CredentialCiphertext)
		if err != nil {
			return "", err
		}
		var envelope siteCredentialEnvelope
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
			return "", appError(CodeCredentialRevealUnavailable, "凭据无法读取", err)
		}
		switch input.Field {
		case "cookie":
			return envelope.Cookie, nil
		case "passkey":
			return envelope.Passkey, nil
		case "api_key":
			return envelope.APIKey, nil
		default:
			return "", revealInvalid()
		}
	case CredentialResourceCookieCloud:
		if input.ResourceID != "1" {
			return "", revealInvalid()
		}
		var record models.CookieCloudSettings
		if err := s.db.First(&record, 1).Error; err != nil {
			return "", revealNotFound(err)
		}
		raw, err := s.decrypt(cookieCloudCredentialPurpose, record.CredentialCiphertext)
		if err != nil {
			return "", err
		}
		var envelope cookieCloudCredential
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
			return "", appError(CodeCredentialRevealUnavailable, "凭据无法读取", err)
		}
		switch input.Field {
		case "uuid":
			return envelope.UUID, nil
		case "password":
			return envelope.Password, nil
		case "auth_header":
			return envelope.AuthHeader, nil
		default:
			return "", revealInvalid()
		}
	case CredentialResourceMetadata:
		if input.ResourceID != "1" || input.Field != "tmdb_credential" {
			return "", revealInvalid()
		}
		var record models.MetadataSettings
		if err := s.db.First(&record, 1).Error; err != nil {
			return "", revealNotFound(err)
		}
		// Only a user-supplied encrypted value is revealable. Deployment and
		// built-in credentials never enter this branch and cannot be requested.
		return s.decrypt(tmdbTokenPurpose, record.TMDBTokenCiphertext)
	case CredentialResourcePluginConnection:
		if input.ResourceID == "" || len(input.ResourceID) > 64 || input.Field != "credential" {
			return "", revealInvalid()
		}
		var record models.PluginConnection
		if err := s.db.First(&record, "id = ?", input.ResourceID).Error; err != nil {
			return "", revealNotFound(err)
		}
		if record.CredentialMode != models.PluginCredentialModeCookie && record.CredentialMode != models.PluginCredentialModeBearer {
			return "", revealInvalid()
		}
		return s.decrypt(hostapi.CredentialPurpose(record.PluginID, record.ID, record.CredentialScope), record.CredentialCiphertext)
	default:
		return "", revealInvalid()
	}
}

func (s *CredentialRevealService) decrypt(purpose, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", appError(CodeNotFound, "该凭据尚未配置", nil)
	}
	value, err := s.credentials.Decrypt(purpose, ciphertext)
	if err != nil {
		return "", appError(CodeCredentialRevealUnavailable, "凭据无法读取", err)
	}
	return value, nil
}

func (s *CredentialRevealService) auditReveal(actor Actor, input CredentialRevealInput, outcome, code string, request RequestContext) {
	metadata := map[string]any{"resource_type": input.ResourceType, "field": input.Field}
	if code != "" {
		metadata["error_code"] = code
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "credential.reveal", "external_credential", input.ResourceType+":"+input.ResourceID, outcome, metadata, request)
}

func revealUintID(raw string) (uint, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 || uint64(uint(value)) != value {
		return 0, revealInvalid()
	}
	return uint(value), nil
}

func revealInvalid() error {
	return appError(CodeInvalidRequest, "不支持查看该凭据", nil)
}

func validateCredentialRevealInput(input CredentialRevealInput) error {
	if input.ResourceType == "" || len(input.ResourceType) > 64 || !credentialRevealIdentifier.MatchString(input.ResourceType) ||
		input.ResourceID == "" || len(input.ResourceID) > 128 || !credentialRevealIdentifier.MatchString(input.ResourceID) ||
		input.Field == "" || len(input.Field) > 64 || !credentialRevealIdentifier.MatchString(input.Field) {
		return revealInvalid()
	}
	return nil
}

func revealNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "凭据所属配置不存在", err)
	}
	return err
}
