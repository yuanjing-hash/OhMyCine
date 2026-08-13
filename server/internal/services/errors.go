package services

import "errors"

const (
	CodeInvalidRequest          = "INVALID_REQUEST"
	CodeNotAuthenticated        = "NOT_AUTHENTICATED"
	CodePermissionDenied        = "PERMISSION_DENIED"
	CodeNotFound                = "NOT_FOUND"
	CodeConflict                = "CONFLICT"
	CodeSetupComplete           = "SETUP_ALREADY_COMPLETE"
	CodeInvalidCredentials      = "INVALID_CREDENTIALS"
	CodeLoginRateLimited        = "LOGIN_RATE_LIMITED"
	CodeOwnerProtected          = "OWNER_PROTECTED"
	CodeLastAdminRequired       = "LAST_ADMIN_REQUIRED"
	CodeSelfModification        = "SELF_MODIFICATION_FORBIDDEN"
	CodePrivilegeEscalation     = "PRIVILEGE_ESCALATION"
	CodeProtectedRole           = "PROTECTED_ROLE"
	CodeRoleInUse               = "ROLE_IN_USE"
	CodeRecoveryRequired        = "OWNER_RECOVERY_REQUIRED"
	CodeStorageTypeUnsupported  = "storage_type_unsupported"
	CodeStorageNameRequired     = "storage_name_required"
	CodeStorageNameConflict     = "storage_name_conflict"
	CodeStoragePathConflict     = "storage_path_conflict"
	CodeProfileValidation       = "media_classification_profile_validation"
	CodeProfileNameRequired     = "media_classification_profile_name_required"
	CodeProfileNameConflict     = "media_classification_profile_name_conflict"
	CodeProfileProtected        = "media_classification_profile_protected"
	CodeProfileRevisionConflict = "media_classification_profile_revision_conflict"
	CodeProfileInUse            = "media_classification_profile_in_use"
)

// AppError is a stable, client-safe domain error.
type AppError struct {
	Code    string
	Message string
	Cause   error
}

func (e *AppError) Error() string { return e.Message }
func (e *AppError) Unwrap() error { return e.Cause }

func appError(code, message string, cause error) error {
	return &AppError{Code: code, Message: message, Cause: cause}
}

// ErrorCode returns the stable code for a domain error.
func ErrorCode(err error) string {
	var target *AppError
	if errors.As(err, &target) {
		return target.Code
	}
	return "INTERNAL_ERROR"
}

// ErrorMessage returns the safe message for a domain error.
func ErrorMessage(err error) string {
	var target *AppError
	if errors.As(err, &target) {
		return target.Message
	}
	return "服务器内部错误"
}
