package services

import "errors"

const (
	CodeInvalidRequest                 = "INVALID_REQUEST"
	CodeNotAuthenticated               = "NOT_AUTHENTICATED"
	CodePermissionDenied               = "PERMISSION_DENIED"
	CodeNotFound                       = "NOT_FOUND"
	CodeConflict                       = "CONFLICT"
	CodeSetupComplete                  = "SETUP_ALREADY_COMPLETE"
	CodeInvalidCredentials             = "INVALID_CREDENTIALS"
	CodeLoginRateLimited               = "LOGIN_RATE_LIMITED"
	CodeOwnerProtected                 = "OWNER_PROTECTED"
	CodeLastAdminRequired              = "LAST_ADMIN_REQUIRED"
	CodeSelfModification               = "SELF_MODIFICATION_FORBIDDEN"
	CodePrivilegeEscalation            = "PRIVILEGE_ESCALATION"
	CodeProtectedRole                  = "PROTECTED_ROLE"
	CodeRoleInUse                      = "ROLE_IN_USE"
	CodeRecoveryRequired               = "OWNER_RECOVERY_REQUIRED"
	CodeStorageTypeUnsupported         = "storage_type_unsupported"
	CodeStorageNameRequired            = "storage_name_required"
	CodeStorageNameConflict            = "storage_name_conflict"
	CodeStoragePathConflict            = "storage_path_conflict"
	CodeProfileValidation              = "media_classification_profile_validation"
	CodeProfileNameRequired            = "media_classification_profile_name_required"
	CodeProfileNameConflict            = "media_classification_profile_name_conflict"
	CodeProfileProtected               = "media_classification_profile_protected"
	CodeProfileRevisionConflict        = "media_classification_profile_revision_conflict"
	CodeProfileInUse                   = "media_classification_profile_in_use"
	CodeRuntimeLogFilterInvalid        = "runtime_log_filter_invalid"
	CodeRuntimeLogPolicyInvalid        = "runtime_log_policy_invalid"
	CodeRuntimeLogUnavailable          = "runtime_log_unavailable"
	CodeMediaLibraryNameRequired       = "media_library_name_required"
	CodeMediaLibraryNameConflict       = "media_library_name_conflict"
	CodeMediaLibraryPathInvalid        = "media_library_path_invalid"
	CodeMediaLibraryOverlap            = "media_library_overlap"
	CodeMediaLibraryStorageUnavailable = "media_library_storage_unavailable"
	CodeMediaLibraryProfileUnavailable = "media_library_profile_unavailable"
	CodeMediaLibraryScanFailed         = "media_library_scan_failed"
	CodeMediaLibraryBusy               = "media_library_busy"
	CodeDownloaderTypeUnsupported      = "downloader_type_unsupported"
	CodeDownloaderNameRequired         = "downloader_name_required"
	CodeDownloaderNameConflict         = "downloader_name_conflict"
	CodeDownloaderStorageRequired      = "downloader_storage_required"
	CodeDownloaderStorageUnavailable   = "downloader_storage_unavailable"
	CodeDownloaderInUse                = "downloader_in_use"
	CodeDownloaderUnavailable          = "downloader_unavailable"
	CodeDownloadStagingRequired        = "download_staging_required"
	CodeDownloadStagingUnavailable     = "download_staging_unavailable"
	CodeDownloadSourceInvalid          = "download_source_invalid"
	CodeDownloadTorrentInvalid         = "download_torrent_invalid"
	CodeTransferMediaUnrecognized      = "transfer_media_unrecognized"
	CodeTMDBTokenInvalid               = "tmdb_token_invalid"
	CodeTMDBUnavailable                = "tmdb_unavailable"
	CodeConnectionProviderUnsupported  = "connection_provider_unsupported"
	CodeConnectionNameRequired         = "connection_name_required"
	CodeConnectionNameConflict         = "connection_name_conflict"
	CodeConnectionInUse                = "connection_in_use"
	CodeConnectionUnavailable          = "connection_unavailable"
	CodePan115CookieInvalid            = "pan115_cookie_invalid"
	CodeEmbyEndpointInvalid            = "emby_endpoint_invalid"
	CodeEmbyAPIKeyInvalid              = "emby_api_key_invalid"
	CodeEmbyUnavailable                = "emby_unavailable"
	CodeEmbyGatewayUnavailable         = "emby_gateway_unavailable"
	CodeEmbyGatewayAliasInvalid        = "emby_gateway_alias_invalid"
	CodeEmbyGatewayAliasReserved       = "emby_gateway_alias_reserved"
	CodeEmbyGatewayAliasConflict       = "emby_gateway_alias_conflict"
	CodeEmbySummaryPartial             = "emby_summary_partial"
	CodeEmbyPlaybackTicketInvalid      = "emby_playback_ticket_invalid"
	CodeProxySignatureInvalid          = "proxy_signature_invalid"
	CodeProxySignatureExpired          = "proxy_signature_expired"
	CodeProxyTargetUnavailable         = "proxy_target_unavailable"
	CodeProxyUpstreamUnavailable       = "proxy_upstream_unavailable"
	CodeProxyHeadersUnsupported        = "proxy_headers_unsupported"
	CodeProxyUnavailable               = "proxy_unavailable"
	CodeProxyDeviceLimit               = "proxy_device_limit"
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
