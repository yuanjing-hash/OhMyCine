package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func success(c *gin.Context, status int, data any) {
	c.JSON(status, response{Code: 0, Message: "success", Data: data})
}

func writeError(c *gin.Context, log zerolog.Logger, err error) {
	status := http.StatusInternalServerError
	code := services.ErrorCode(err)
	switch code {
	case services.CodeInvalidRequest, services.CodeQueueActionInvalid, services.CodeConnectionProviderUnsupported, services.CodeConnectionNameRequired, services.CodePan115CookieInvalid, services.CodeEmbyEndpointInvalid, services.CodeEmbyAPIKeyInvalid, services.CodeStorageNameRequired, services.CodeProfileValidation, services.CodeProfileNameRequired, services.CodeRuntimeLogFilterInvalid, services.CodeRuntimeLogPolicyInvalid, services.CodeMediaLibraryNameRequired, services.CodeMediaLibraryPathInvalid, services.CodeMediaLibraryStorageUnavailable, services.CodeMediaLibraryProfileUnavailable, "storage_path_not_absolute", "storage_path_not_found", "storage_path_not_directory", "storage_path_reparse_point", "storage_unreadable", services.CodeStorageTypeUnsupported, services.CodeDownloaderTypeUnsupported, services.CodeDownloaderNameRequired, services.CodeDownloaderStorageRequired, services.CodeDownloaderStorageUnavailable, services.CodeDownloadStagingRequired, services.CodeDownloadStagingUnavailable, services.CodeDownloadSourceInvalid, services.CodeDownloadTorrentInvalid, services.CodeTMDBTokenInvalid:
		status = http.StatusBadRequest
	case services.CodeDirectoryTokenInvalid, services.CodeDirectoryTokenExpired, services.CodeDirectoryNotFound, services.CodeDirectoryUnreadable, services.CodeDirectoryUnavailable:
		status = http.StatusBadRequest
	case services.CodeNotAuthenticated, services.CodeInvalidCredentials:
		status = http.StatusUnauthorized
	case services.CodePermissionDenied, services.CodePrivilegeEscalation, services.CodeOwnerProtected, services.CodeLastAdminRequired, services.CodeSelfModification, services.CodeProtectedRole, services.CodeProfileProtected:
		status = http.StatusForbidden
	case services.CodeNotFound:
		status = http.StatusNotFound
	case services.CodeConflict, services.CodeSetupComplete, services.CodeRoleInUse, services.CodeRecoveryRequired, services.CodeConnectionNameConflict, services.CodeConnectionInUse, services.CodeStorageNameConflict, services.CodeStoragePathConflict, services.CodeProfileNameConflict, services.CodeProfileRevisionConflict, services.CodeProfileInUse, services.CodeMediaLibraryNameConflict, services.CodeMediaLibraryOverlap, services.CodeMediaLibraryBusy, services.CodeDownloaderNameConflict, services.CodeDownloaderInUse:
		status = http.StatusConflict
	case services.CodeQueueOrderConflict, services.CodeQueueStateConflict, services.CodeQueueLeaseInvalid, services.CodeQueueActionStale, services.CodeQueuePolicyConflict:
		status = http.StatusConflict
	case services.CodeLoginRateLimited:
		status = http.StatusTooManyRequests
	case services.CodeDirectoryRateLimited:
		status = http.StatusTooManyRequests
	case services.CodeDirectoryBusy:
		status = http.StatusServiceUnavailable
	case services.CodeRuntimeLogUnavailable, services.CodeMediaLibraryScanFailed, services.CodeQueueWorkerUnavailable, services.CodeConnectionUnavailable, services.CodeEmbyUnavailable, services.CodeEmbyGatewayUnavailable, services.CodeDownloaderUnavailable, services.CodeTMDBUnavailable:
		status = http.StatusServiceUnavailable
	}
	if status == http.StatusInternalServerError {
		operation := serverlog.OperationForHTTPRoute(c.FullPath())
		operation.Event(log.Error()).Str("request_id", middleware.RequestIDFrom(c)).Str("error_code", code).Msg(operation.Message("请求处理失败"))
	}
	appCode := status*100 + 1
	var appErr *services.AppError
	if errors.As(err, &appErr) && appErr.Code == services.CodeInvalidRequest {
		appCode = 40001
	}
	c.JSON(status, response{Code: appCode, Message: services.ErrorMessage(err), Data: gin.H{"error_code": code}})
}
