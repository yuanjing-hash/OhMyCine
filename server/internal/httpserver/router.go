package httpserver

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/config"
	"github.com/yuanjing-hash/ohmycine/server/internal/handlers"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
	"github.com/yuanjing-hash/ohmycine/server/webui"
)

func New(cfg config.Config, api *handlers.API, auth *services.AuthService, log zerolog.Logger) *gin.Engine {
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	router.Use(gin.Recovery(), middleware.RequestID(), middleware.SecurityHeaders(), middleware.Logger(log), middleware.BrowserMutationProtection(cfg.AllowedOrigins()))
	v1 := router.Group("/api/v1")
	v1.GET("/health", api.Health)
	v1.GET("/setup/status", api.SetupStatus)
	v1.POST("/setup/owner", api.SetupOwner)
	v1.POST("/auth/login", api.Login)

	protected := v1.Group("")
	protected.Use(middleware.Auth(auth, api.CookieName()), middleware.CSRF(auth))
	protected.POST("/auth/logout", api.Logout)
	protected.GET("/auth/me", api.Me)
	protected.GET("/auth/csrf", api.CSRF)
	protected.GET("/dashboard", middleware.RequirePermission(authz.PermissionDashboardRead), api.Dashboard)
	protected.GET("/permissions", middleware.RequirePermission(authz.PermissionRolesRead), api.Permissions)
	protected.GET("/users", middleware.RequirePermission(authz.PermissionUsersRead), api.Users)
	protected.POST("/users", middleware.RequirePermission(authz.PermissionUsersCreate), api.CreateUser)
	protected.PATCH("/users/:id", middleware.RequirePermission(authz.PermissionUsersUpdate), api.UpdateUser)
	protected.POST("/users/:id/disable", middleware.RequirePermission(authz.PermissionUsersDisable), func(c *gin.Context) { api.SetUserEnabled(c, false) })
	protected.POST("/users/:id/enable", middleware.RequirePermission(authz.PermissionUsersUpdate), func(c *gin.Context) { api.SetUserEnabled(c, true) })
	protected.POST("/users/:id/reset-password", middleware.RequirePermission(authz.PermissionUsersUpdate), api.ResetPassword)
	protected.PUT("/users/:id/roles", middleware.RequirePermission(authz.PermissionRolesAssign), api.AssignRoles)
	protected.DELETE("/users/:id", middleware.RequirePermission(authz.PermissionUsersDelete), api.DeleteUser)
	protected.GET("/roles", middleware.RequirePermission(authz.PermissionRolesRead), api.Roles)
	protected.POST("/roles", middleware.RequirePermission(authz.PermissionRolesCreate), api.CreateRole)
	protected.PATCH("/roles/:id", middleware.RequirePermission(authz.PermissionRolesUpdate), api.UpdateRole)
	protected.PUT("/roles/:id/permissions", middleware.RequirePermission(authz.PermissionRolesUpdate), api.SetRolePermissions)
	protected.DELETE("/roles/:id", middleware.RequirePermission(authz.PermissionRolesDelete), api.DeleteRole)
	protected.GET("/audit", middleware.RequirePermission(authz.PermissionAuditRead), api.Audit)
	protected.GET("/storages", middleware.RequirePermission(authz.PermissionStoragesRead), api.Storages)
	protected.GET("/filesystem/roots", middleware.NoStore(), middleware.RequirePermission(authz.PermissionStoragesBrowse), api.DirectoryRoots)
	protected.GET("/filesystem/directories", middleware.NoStore(), middleware.RequirePermission(authz.PermissionStoragesBrowse), api.Directories)
	protected.GET("/storages/:id/directory", middleware.NoStore(), middleware.RequirePermission(authz.PermissionStoragesBrowse), api.StorageDirectory)
	protected.POST("/storages", middleware.RequirePermission(authz.PermissionStoragesCreate), api.CreateStorage)
	protected.PATCH("/storages/:id", middleware.RequirePermission(authz.PermissionStoragesUpdate), api.UpdateStorage)
	protected.DELETE("/storages/:id", middleware.RequirePermission(authz.PermissionStoragesDelete), api.DeleteStorage)
	protected.POST("/storages/:id/test", middleware.RequirePermission(authz.PermissionStoragesTest), api.TestStorage)
	protected.GET("/media-classification-profiles", middleware.RequirePermission(authz.PermissionMediaClassificationProfilesRead), api.MediaClassificationProfiles)
	protected.GET("/media-classification-profiles/:id", middleware.RequirePermission(authz.PermissionMediaClassificationProfilesRead), api.MediaClassificationProfile)
	protected.POST("/media-classification-profiles", middleware.RequirePermission(authz.PermissionMediaClassificationProfilesCreate), api.CreateMediaClassificationProfile)
	protected.POST("/media-classification-profiles/:id/copy", middleware.RequirePermission(authz.PermissionMediaClassificationProfilesCreate), api.CopyMediaClassificationProfile)
	protected.PATCH("/media-classification-profiles/:id", middleware.RequirePermission(authz.PermissionMediaClassificationProfilesUpdate), api.UpdateMediaClassificationProfile)
	protected.DELETE("/media-classification-profiles/:id", middleware.RequirePermission(authz.PermissionMediaClassificationProfilesDelete), api.DeleteMediaClassificationProfile)

	if assets, ok := webui.Assets(); ok {
		webui.Register(router, assets)
	} else {
		router.NoRoute(func(c *gin.Context) {
			c.JSON(http.StatusNotFound, gin.H{"code": 40401, "message": "not found", "data": nil})
		})
	}
	return router
}

func NewLogger(environment string) zerolog.Logger {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	writer := zerolog.ConsoleWriter{Out: os.Stdout}
	if environment == "production" {
		return zerolog.New(os.Stdout).With().Timestamp().Logger()
	}
	return zerolog.New(writer).With().Timestamp().Logger()
}
