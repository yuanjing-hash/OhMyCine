package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/config"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type API struct {
	config config.Config
	auth   *services.AuthService
	admin  *services.AdminService
	audit  *services.AuditService
	log    zerolog.Logger
}

func NewAPI(cfg config.Config, auth *services.AuthService, admin *services.AdminService, audit *services.AuditService, log zerolog.Logger) *API {
	return &API{config: cfg, auth: auth, admin: admin, audit: audit, log: log}
}

func (a *API) CookieName() string {
	if a.config.CookieSecure {
		return "__Host-omc_session"
	}
	return "omc_session"
}

func (a *API) Health(c *gin.Context) { success(c, http.StatusOK, gin.H{"status": "ok"}) }

func (a *API) SetupStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	required, recovery, err := a.auth.SetupRequired()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"setup_required": required, "recovery_required": recovery})
}

func (a *API) SetupOwner(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input struct {
		Username    string `json:"username" binding:"required"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "请填写有效的 owner 信息", Cause: err})
		return
	}
	actor, token, err := a.auth.SetupOwner(services.SetupInput{Username: input.Username, DisplayName: input.DisplayName, Password: input.Password}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	a.setSessionCookie(c, token)
	success(c, http.StatusCreated, gin.H{"user": services.CurrentUserFromActor(actor), "csrf_token": a.auth.CSRFToken(token)})
}

func (a *API) Login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, &services.AppError{Code: services.CodeInvalidRequest, Message: "请输入用户名和密码", Cause: err})
		return
	}
	actor, token, err := a.auth.Login(input.Username, input.Password, c.GetHeader("User-Agent"), middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	a.setSessionCookie(c, token)
	success(c, http.StatusOK, gin.H{"user": services.CurrentUserFromActor(actor), "csrf_token": a.auth.CSRFToken(token)})
}

func (a *API) Logout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	if err := a.auth.Logout(middleware.SessionTokenFrom(c), actor, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	a.clearSessionCookie(c)
	success(c, http.StatusOK, gin.H{"logged_out": true})
}

func (a *API) Me(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	success(c, http.StatusOK, services.CurrentUserFromActor(actor))
}
func (a *API) CSRF(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	success(c, http.StatusOK, gin.H{"csrf_token": a.auth.CSRFToken(middleware.SessionTokenFrom(c))})
}

func (a *API) Dashboard(c *gin.Context) {
	data, err := a.admin.Dashboard()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) Permissions(c *gin.Context) {
	data, err := a.admin.ListPermissions()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}
func (a *API) Users(c *gin.Context) {
	data, err := a.admin.ListUsers()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}

func (a *API) CreateUser(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var input struct {
		Username    string `json:"username" binding:"required"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password" binding:"required"`
		RoleIDs     []uint `json:"role_ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("用户信息不完整", err))
		return
	}
	data, err := a.admin.CreateUser(actor, services.CreateUserInput{Username: input.Username, DisplayName: input.DisplayName, Password: input.Password, RoleIDs: input.RoleIDs}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, data)
}

func (a *API) UpdateUser(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		DisplayName *string `json:"display_name"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("用户信息无效", err))
		return
	}
	data, err := a.admin.UpdateUser(actor, id, services.UpdateUserInput{DisplayName: input.DisplayName}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) SetUserEnabled(c *gin.Context, enabled bool) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.admin.SetUserEnabled(actor, id, enabled, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"enabled": enabled})
}
func (a *API) DeleteUser(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.admin.DeleteUser(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}

func (a *API) AssignRoles(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		RoleIDs []uint `json:"role_ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("请选择角色", err))
		return
	}
	if err := a.admin.ReplaceUserRoles(actor, id, input.RoleIDs, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"updated": true})
}

func (a *API) ResetPassword(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		Password        string `json:"password" binding:"required"`
		CurrentPassword string `json:"current_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("密码信息无效", err))
		return
	}
	if err := a.admin.ResetPassword(actor, id, input.Password, input.CurrentPassword, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"reset": true})
}

func (a *API) Roles(c *gin.Context) {
	data, err := a.admin.ListRoles()
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}

func (a *API) CreateRole(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	var input struct {
		Code        string   `json:"code" binding:"required"`
		Name        string   `json:"name" binding:"required"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("角色信息不完整", err))
		return
	}
	data, err := a.admin.CreateRole(actor, services.CreateRoleInput{Code: input.Code, Name: input.Name, Description: input.Description, PermissionCodes: input.Permissions}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusCreated, data)
}

func (a *API) UpdateRole(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Active      *bool   `json:"active"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("角色信息无效", err))
		return
	}
	data, err := a.admin.UpdateRole(actor, id, services.UpdateRoleInput{Name: input.Name, Description: input.Description, Active: input.Active}, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, data)
}

func (a *API) SetRolePermissions(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	var input struct {
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("权限列表无效", err))
		return
	}
	if err := a.admin.ReplaceRolePermissions(actor, id, input.Permissions, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"updated": true})
}

func (a *API) DeleteRole(c *gin.Context) {
	actor, _ := middleware.ActorFrom(c)
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := a.admin.DeleteRole(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"deleted": true})
}
func (a *API) Audit(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	data, err := a.audit.List(limit)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": data, "total": len(data)})
}

func (a *API) setSessionCookie(c *gin.Context, token string) {
	http.SetCookie(c.Writer, &http.Cookie{Name: a.CookieName(), Value: token, Path: "/", MaxAge: int(a.config.SessionMaxTTL.Seconds()), HttpOnly: true, Secure: a.config.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func (a *API) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: a.CookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.config.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func pathID(c *gin.Context) (uint, bool) {
	value, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || value == 0 {
		writeError(c, zerolog.Nop(), invalid("资源 ID 无效", err))
		return 0, false
	}
	return uint(value), true
}
func invalid(message string, err error) error {
	return &services.AppError{Code: services.CodeInvalidRequest, Message: message, Cause: err}
}
