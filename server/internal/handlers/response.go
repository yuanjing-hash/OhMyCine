package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
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
	case services.CodeInvalidRequest, services.CodeStorageNameRequired, "storage_path_not_absolute", "storage_path_not_found", "storage_path_not_directory", "storage_path_reparse_point", "storage_unreadable", services.CodeStorageTypeUnsupported:
		status = http.StatusBadRequest
	case services.CodeDirectoryTokenInvalid, services.CodeDirectoryTokenExpired, services.CodeDirectoryNotFound, services.CodeDirectoryUnreadable, services.CodeDirectoryUnavailable:
		status = http.StatusBadRequest
	case services.CodeNotAuthenticated, services.CodeInvalidCredentials:
		status = http.StatusUnauthorized
	case services.CodePermissionDenied, services.CodePrivilegeEscalation, services.CodeOwnerProtected, services.CodeLastAdminRequired, services.CodeSelfModification, services.CodeProtectedRole:
		status = http.StatusForbidden
	case services.CodeNotFound:
		status = http.StatusNotFound
	case services.CodeConflict, services.CodeSetupComplete, services.CodeRoleInUse, services.CodeRecoveryRequired, services.CodeStorageNameConflict, services.CodeStoragePathConflict:
		status = http.StatusConflict
	case services.CodeLoginRateLimited:
		status = http.StatusTooManyRequests
	case services.CodeDirectoryRateLimited:
		status = http.StatusTooManyRequests
	case services.CodeDirectoryBusy:
		status = http.StatusServiceUnavailable
	}
	if status == http.StatusInternalServerError {
		log.Error().Err(err).Msg("Request failed")
	}
	appCode := status*100 + 1
	var appErr *services.AppError
	if errors.As(err, &appErr) && appErr.Code == services.CodeInvalidRequest {
		appCode = 40001
	}
	c.JSON(status, response{Code: appCode, Message: services.ErrorMessage(err), Data: gin.H{"error_code": code}})
}
