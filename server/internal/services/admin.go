package services

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type AdminService struct {
	db    *gorm.DB
	authz *AuthorizationService
	auth  *AuthService
	audit *AuditService
}

func NewAdminService(db *gorm.DB, authorization *AuthorizationService, auth *AuthService, audit *AuditService) *AdminService {
	return &AdminService{db: db, authz: authorization, auth: auth, audit: audit}
}

type RoleSummary struct {
	ID          uint     `json:"id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	Protected   bool     `json:"protected"`
	Active      bool     `json:"active"`
	Permissions []string `json:"permissions"`
	UserCount   int64    `json:"user_count"`
}

type UserSummary struct {
	ID          uint          `json:"id"`
	Username    string        `json:"username"`
	DisplayName string        `json:"display_name"`
	Status      string        `json:"status"`
	IsOwner     bool          `json:"is_owner"`
	Roles       []RoleSummary `json:"roles"`
	LastLoginAt any           `json:"last_login_at"`
	CreatedAt   any           `json:"created_at"`
}

func (s *AdminService) ListUsers() ([]UserSummary, error) {
	var users []models.User
	if err := s.db.Order("id").Find(&users).Error; err != nil {
		return nil, err
	}
	result := make([]UserSummary, 0, len(users))
	for _, user := range users {
		roles, err := s.rolesForUser(s.db, user.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, UserSummary{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Status: user.Status, IsOwner: user.IsOwner, Roles: roles, LastLoginAt: user.LastLoginAt, CreatedAt: user.CreatedAt})
	}
	return result, nil
}

type CreateUserInput struct {
	Username    string
	DisplayName string
	Password    string
	RoleIDs     []uint
}

func (s *AdminService) CreateUser(actor Actor, input CreateUserInput, request RequestContext) (UserSummary, error) {
	if err := validateUsername(input.Username); err != nil {
		return UserSummary{}, err
	}
	hash, err := HashPassword(input.Password)
	if err != nil {
		return UserSummary{}, err
	}
	if len(input.RoleIDs) == 0 {
		var viewer models.Role
		if err := s.db.Where("code = ?", authz.RoleViewer).First(&viewer).Error; err != nil {
			return UserSummary{}, err
		}
		input.RoleIDs = []uint{viewer.ID}
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(input.Username)
	}
	var user models.User
	err = s.db.Transaction(func(tx *gorm.DB) error {
		currentActor, err := s.authz.resolveWithDB(tx, actor.User.ID)
		if err != nil {
			return err
		}
		if err := s.validateRoleAssignment(tx, currentActor, input.RoleIDs); err != nil {
			return err
		}
		user = models.User{Username: strings.TrimSpace(input.Username), UsernameNormalized: NormalizeUsername(input.Username), DisplayName: displayName, PasswordHash: hash, Status: models.UserStatusActive, AuthzVersion: 1}
		if err := tx.Create(&user).Error; err != nil {
			return appError(CodeConflict, "用户名已存在", err)
		}
		for _, roleID := range uniqueUint(input.RoleIDs) {
			if err := tx.Create(&models.UserRole{UserID: user.ID, RoleID: roleID, AssignedBy: &actor.User.ID}).Error; err != nil {
				return err
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "users.create", "user", uintID(user.ID), "success", map[string]any{"username": user.Username, "role_ids": input.RoleIDs}, request)
	})
	if err != nil {
		return UserSummary{}, err
	}
	return s.getUserSummary(user.ID)
}

type UpdateUserInput struct {
	DisplayName *string
}

func (s *AdminService) UpdateUser(actor Actor, userID uint, input UpdateUserInput, request RequestContext) (UserSummary, error) {
	var user models.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return UserSummary{}, notFound(err, "用户不存在")
	}
	updates := map[string]any{}
	if input.DisplayName != nil {
		name := strings.TrimSpace(*input.DisplayName)
		if name == "" || len(name) > 128 {
			return UserSummary{}, appError(CodeInvalidRequest, "显示名称不能为空且不能超过 128 个字符", nil)
		}
		updates["display_name"] = name
	}
	if len(updates) == 0 {
		return s.getUserSummary(userID)
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user).Updates(updates).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "users.update", "user", uintID(user.ID), "success", map[string]any{"fields": []string{"display_name"}}, request)
	}); err != nil {
		return UserSummary{}, err
	}
	return s.getUserSummary(userID)
}

func (s *AdminService) SetUserEnabled(actor Actor, userID uint, enabled bool, request RequestContext) error {
	if actor.User.ID == userID {
		return appError(CodeSelfModification, "不能停用或启用当前登录账户", nil)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.First(&user, userID).Error; err != nil {
			return notFound(err, "用户不存在")
		}
		if user.IsOwner && !enabled {
			return appError(CodeOwnerProtected, "实例 owner 不能被停用", nil)
		}
		status := models.UserStatusDisabled
		if enabled {
			status = models.UserStatusActive
		}
		if err := tx.Model(&user).Updates(map[string]any{"status": status, "authz_version": gorm.Expr("authz_version + 1")}).Error; err != nil {
			return err
		}
		if !enabled {
			if err := s.auth.RevokeUserSessions(tx, user.ID); err != nil {
				return err
			}
			if err := s.ensureAdminRemains(tx); err != nil {
				return err
			}
		}
		action := "users.enable"
		if !enabled {
			action = "users.disable"
		}
		return s.audit.Record(tx, &actor.User.ID, action, "user", uintID(user.ID), "success", map[string]any{}, request)
	})
}

func (s *AdminService) DeleteUser(actor Actor, userID uint, request RequestContext) error {
	if actor.User.ID == userID {
		return appError(CodeSelfModification, "不能删除当前登录账户", nil)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.First(&user, userID).Error; err != nil {
			return notFound(err, "用户不存在")
		}
		if user.IsOwner {
			return appError(CodeOwnerProtected, "实例 owner 不能被删除", nil)
		}
		if err := tx.Delete(&user).Error; err != nil {
			return err
		}
		if err := s.ensureAdminRemains(tx); err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "users.delete", "user", uintID(user.ID), "success", map[string]any{"username": user.Username}, request)
	})
}

func (s *AdminService) ReplaceUserRoles(actor Actor, userID uint, roleIDs []uint, request RequestContext) error {
	if actor.User.ID == userID {
		return appError(CodeSelfModification, "不能修改当前登录账户的角色", nil)
	}
	roleIDs = uniqueUint(roleIDs)
	if len(roleIDs) == 0 {
		return appError(CodeInvalidRequest, "用户至少需要一个角色", nil)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		currentActor, err := s.authz.resolveWithDB(tx, actor.User.ID)
		if err != nil {
			return err
		}
		if err := s.validateRoleAssignment(tx, currentActor, roleIDs); err != nil {
			return err
		}
		var user models.User
		if err := tx.First(&user, userID).Error; err != nil {
			return notFound(err, "用户不存在")
		}
		if user.IsOwner {
			var count int64
			if err := tx.Model(&models.Role{}).Where("id IN ? AND code = ? AND active = ?", roleIDs, authz.RoleAdministrator, true).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return appError(CodeOwnerProtected, "实例 owner 必须保留管理员角色", nil)
			}
		}
		if err := tx.Where("user_id = ?", userID).Delete(&models.UserRole{}).Error; err != nil {
			return err
		}
		for _, roleID := range roleIDs {
			if err := tx.Create(&models.UserRole{UserID: userID, RoleID: roleID, AssignedBy: &actor.User.ID}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&user).Update("authz_version", gorm.Expr("authz_version + 1")).Error; err != nil {
			return err
		}
		if err := s.ensureAdminRemains(tx); err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "roles.assign", "user", uintID(userID), "success", map[string]any{"role_ids": roleIDs}, request)
	})
}

func (s *AdminService) ResetPassword(actor Actor, userID uint, password, currentPassword string, request RequestContext) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var currentActor models.User
		if err := tx.First(&currentActor, actor.User.ID).Error; err != nil {
			return appError(CodeNotAuthenticated, "登录会话无效", err)
		}
		if bcrypt.CompareHashAndPassword([]byte(currentActor.PasswordHash), []byte(currentPassword)) != nil {
			return appError(CodeInvalidCredentials, "当前操作者密码错误", nil)
		}
		var user models.User
		if err := tx.First(&user, userID).Error; err != nil {
			return notFound(err, "用户不存在")
		}
		if user.IsOwner && actor.User.ID != user.ID {
			return appError(CodeOwnerProtected, "只有 owner 本人可以修改 owner 密码", nil)
		}
		if err := tx.Model(&user).Updates(map[string]any{"password_hash": hash, "authz_version": gorm.Expr("authz_version + 1")}).Error; err != nil {
			return err
		}
		if err := s.auth.RevokeUserSessions(tx, user.ID); err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "users.password_reset", "user", uintID(user.ID), "success", map[string]any{}, request)
	})
}

func (s *AdminService) ListRoles() ([]RoleSummary, error) {
	var roles []models.Role
	if err := s.db.Order("kind DESC, id").Find(&roles).Error; err != nil {
		return nil, err
	}
	result := make([]RoleSummary, 0, len(roles))
	for _, role := range roles {
		summary, err := s.roleSummary(s.db, role)
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	return result, nil
}

type CreateRoleInput struct {
	Code            string
	Name            string
	Description     string
	PermissionCodes []string
}

func (s *AdminService) CreateRole(actor Actor, input CreateRoleInput, request RequestContext) (RoleSummary, error) {
	code := strings.ToLower(strings.TrimSpace(input.Code))
	if !validRoleCode(code) {
		return RoleSummary{}, appError(CodeInvalidRequest, "角色 code 应为 3 到 64 个小写字母、数字、点、短横线或下划线", nil)
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 128 {
		return RoleSummary{}, appError(CodeInvalidRequest, "角色名称不能为空且不能超过 128 个字符", nil)
	}
	description := strings.TrimSpace(input.Description)
	if len(description) > 512 {
		return RoleSummary{}, appError(CodeInvalidRequest, "角色说明不能超过 512 个字符", nil)
	}
	var role models.Role
	err := s.db.Transaction(func(tx *gorm.DB) error {
		currentActor, err := s.authz.resolveWithDB(tx, actor.User.ID)
		if err != nil {
			return err
		}
		codes, err := s.validatePermissionGrant(currentActor, input.PermissionCodes)
		if err != nil {
			return err
		}
		role = models.Role{Code: code, Name: name, Description: description, Kind: models.RoleKindCustom, Active: true}
		if err := tx.Create(&role).Error; err != nil {
			return appError(CodeConflict, "角色 code 已存在", err)
		}
		if err := replaceRolePermissions(tx, role.ID, codes); err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "roles.create", "role", uintID(role.ID), "success", map[string]any{"code": code, "permissions": codes}, request)
	})
	if err != nil {
		return RoleSummary{}, err
	}
	return s.roleSummary(s.db, role)
}

type UpdateRoleInput struct {
	Name        *string
	Description *string
	Active      *bool
}

func (s *AdminService) UpdateRole(actor Actor, roleID uint, input UpdateRoleInput, request RequestContext) (RoleSummary, error) {
	var role models.Role
	if err := s.db.First(&role, roleID).Error; err != nil {
		return RoleSummary{}, notFound(err, "角色不存在")
	}
	if role.Protected {
		return RoleSummary{}, appError(CodeProtectedRole, "系统角色不能编辑", nil)
	}
	updates := map[string]any{}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || len(name) > 128 {
			return RoleSummary{}, appError(CodeInvalidRequest, "角色名称不能为空且不能超过 128 个字符", nil)
		}
		updates["name"] = name
	}
	if input.Description != nil {
		description := strings.TrimSpace(*input.Description)
		if len(description) > 512 {
			return RoleSummary{}, appError(CodeInvalidRequest, "角色说明不能超过 512 个字符", nil)
		}
		updates["description"] = description
	}
	if input.Active != nil {
		updates["active"] = *input.Active
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		before, err := s.authz.resolveWithDB(tx, actor.User.ID)
		if err != nil {
			return err
		}
		if err := tx.Model(&role).Updates(updates).Error; err != nil {
			return err
		}
		if err := s.ensureActorNotDowngraded(tx, before); err != nil {
			return err
		}
		if err := s.ensureAdminRemains(tx); err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "roles.update", "role", uintID(role.ID), "success", map[string]any{"fields": mapKeys(updates)}, request)
	}); err != nil {
		return RoleSummary{}, err
	}
	if err := s.db.First(&role, roleID).Error; err != nil {
		return RoleSummary{}, err
	}
	return s.roleSummary(s.db, role)
}

func (s *AdminService) ReplaceRolePermissions(actor Actor, roleID uint, permissionCodes []string, request RequestContext) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		currentActor, err := s.authz.resolveWithDB(tx, actor.User.ID)
		if err != nil {
			return err
		}
		codes, err := s.validatePermissionGrant(currentActor, permissionCodes)
		if err != nil {
			return err
		}
		var role models.Role
		if err := tx.First(&role, roleID).Error; err != nil {
			return notFound(err, "角色不存在")
		}
		if role.Protected {
			return appError(CodeProtectedRole, "系统角色权限由版本迁移维护", nil)
		}
		before, err := s.authz.resolveWithDB(tx, actor.User.ID)
		if err != nil {
			return err
		}
		if err := replaceRolePermissions(tx, role.ID, codes); err != nil {
			return err
		}
		if err := s.ensureActorNotDowngraded(tx, before); err != nil {
			return err
		}
		if err := s.ensureAdminRemains(tx); err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "roles.permissions_update", "role", uintID(role.ID), "success", map[string]any{"permissions": codes}, request)
	})
}

func (s *AdminService) DeleteRole(actor Actor, roleID uint, request RequestContext) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var role models.Role
		if err := tx.First(&role, roleID).Error; err != nil {
			return notFound(err, "角色不存在")
		}
		if role.Protected {
			return appError(CodeProtectedRole, "系统角色不能删除", nil)
		}
		var count int64
		if err := tx.Model(&models.UserRole{}).Where("role_id = ?", role.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return appError(CodeRoleInUse, "角色仍分配给用户，不能删除", nil)
		}
		if err := tx.Delete(&role).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "roles.delete", "role", uintID(role.ID), "success", map[string]any{"code": role.Code}, request)
	})
}

func (s *AdminService) ListPermissions() ([]models.Permission, error) {
	var permissions []models.Permission
	if err := s.db.Order("module, code").Find(&permissions).Error; err != nil {
		return nil, err
	}
	return permissions, nil
}

type DashboardSummary struct {
	Initialized      bool  `json:"initialized"`
	RecoveryRequired bool  `json:"recovery_required"`
	Users            int64 `json:"users"`
	ActiveUsers      int64 `json:"active_users"`
	Roles            int64 `json:"roles"`
	AuditEvents      int64 `json:"audit_events"`
}

func (s *AdminService) Dashboard() (DashboardSummary, error) {
	var summary DashboardSummary
	if err := s.db.Model(&models.User{}).Count(&summary.Users).Error; err != nil {
		return summary, err
	}
	if err := s.db.Model(&models.User{}).Where("status = ?", models.UserStatusActive).Count(&summary.ActiveUsers).Error; err != nil {
		return summary, err
	}
	if err := s.db.Model(&models.Role{}).Count(&summary.Roles).Error; err != nil {
		return summary, err
	}
	if err := s.db.Model(&models.AuditLog{}).Count(&summary.AuditEvents).Error; err != nil {
		return summary, err
	}
	var owners int64
	if err := s.db.Model(&models.User{}).Where("is_owner = ?", true).Count(&owners).Error; err != nil {
		return summary, err
	}
	summary.Initialized = summary.Users > 0
	summary.RecoveryRequired = summary.Initialized && owners == 0
	return summary, nil
}

func (s *AdminService) validateRoleAssignment(db *gorm.DB, actor Actor, roleIDs []uint) error {
	var roles []models.Role
	if err := db.Where("id IN ? AND active = ?", uniqueUint(roleIDs), true).Find(&roles).Error; err != nil {
		return err
	}
	if len(roles) != len(uniqueUint(roleIDs)) {
		return appError(CodeInvalidRequest, "角色不存在或已停用", nil)
	}
	codes, err := s.permissionsForRoles(db, roles)
	if err != nil {
		return err
	}
	if !actor.IsSystemAdmin() && !subset(codes, actor.Permissions) {
		return appError(CodePrivilegeEscalation, "不能授予操作者自己没有的权限", nil)
	}
	return nil
}

func (s *AdminService) validatePermissionGrant(actor Actor, codes []string) ([]string, error) {
	codes = uniqueStrings(codes)
	for _, code := range codes {
		if !authz.Contains(code) {
			return nil, appError(CodeInvalidRequest, fmt.Sprintf("未知权限码：%s", code), nil)
		}
	}
	if !actor.IsSystemAdmin() && !subset(codes, actor.Permissions) {
		return nil, appError(CodePrivilegeEscalation, "不能授予操作者自己没有的权限", nil)
	}
	return codes, nil
}

func (s *AdminService) permissionsForRoles(db *gorm.DB, roles []models.Role) ([]string, error) {
	for _, role := range roles {
		if role.Code == authz.RoleAdministrator {
			return authz.Codes()
		}
	}
	ids := make([]uint, 0, len(roles))
	for _, role := range roles {
		ids = append(ids, role.ID)
	}
	var codes []string
	if len(ids) > 0 {
		if err := db.Model(&models.RolePermission{}).Where("role_id IN ?", ids).Distinct().Pluck("permission_code", &codes).Error; err != nil {
			return nil, err
		}
	}
	sort.Strings(codes)
	return codes, nil
}

func (s *AdminService) ensureActorNotDowngraded(tx *gorm.DB, before Actor) error {
	after, err := s.authz.resolveWithDB(tx, before.User.ID)
	if err != nil {
		return err
	}
	for code := range before.Permissions {
		if !after.Can(code) {
			return appError(CodeSelfModification, "不能通过角色变更降低当前登录账户权限", nil)
		}
	}
	return nil
}

func (s *AdminService) ensureAdminRemains(tx *gorm.DB) error {
	var count int64
	err := tx.Raw(`SELECT COUNT(DISTINCT users.id)
		FROM users
		JOIN user_roles ON user_roles.user_id = users.id
		JOIN roles ON roles.id = user_roles.role_id AND roles.active = 1
		LEFT JOIN role_permissions ON role_permissions.role_id = roles.id
		WHERE users.status = ? AND (roles.code = ? OR role_permissions.permission_code = ?)`, models.UserStatusActive, authz.RoleAdministrator, authz.PermissionSystemAdmin).Scan(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return appError(CodeLastAdminRequired, "系统至少需要一个有效管理员", nil)
	}
	return nil
}

func (s *AdminService) rolesForUser(db *gorm.DB, userID uint) ([]RoleSummary, error) {
	var roles []models.Role
	if err := db.Table("roles").Joins("JOIN user_roles ON user_roles.role_id = roles.id").Where("user_roles.user_id = ?", userID).Order("roles.id").Find(&roles).Error; err != nil {
		return nil, err
	}
	result := make([]RoleSummary, 0, len(roles))
	for _, role := range roles {
		summary, err := s.roleSummary(db, role)
		if err != nil {
			return nil, err
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s *AdminService) roleSummary(db *gorm.DB, role models.Role) (RoleSummary, error) {
	permissions := []string{}
	if role.Code == authz.RoleAdministrator {
		var err error
		permissions, err = authz.Codes()
		if err != nil {
			return RoleSummary{}, err
		}
	} else if err := db.Model(&models.RolePermission{}).Where("role_id = ?", role.ID).Order("permission_code").Pluck("permission_code", &permissions).Error; err != nil {
		return RoleSummary{}, err
	}
	var count int64
	if err := db.Model(&models.UserRole{}).Where("role_id = ?", role.ID).Count(&count).Error; err != nil {
		return RoleSummary{}, err
	}
	return RoleSummary{ID: role.ID, Code: role.Code, Name: role.Name, Description: role.Description, Kind: role.Kind, Protected: role.Protected, Active: role.Active, Permissions: permissions, UserCount: count}, nil
}

func (s *AdminService) getUserSummary(userID uint) (UserSummary, error) {
	var user models.User
	if err := s.db.First(&user, userID).Error; err != nil {
		return UserSummary{}, notFound(err, "用户不存在")
	}
	roles, err := s.rolesForUser(s.db, user.ID)
	if err != nil {
		return UserSummary{}, err
	}
	return UserSummary{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Status: user.Status, IsOwner: user.IsOwner, Roles: roles, LastLoginAt: user.LastLoginAt, CreatedAt: user.CreatedAt}, nil
}

func replaceRolePermissions(tx *gorm.DB, roleID uint, codes []string) error {
	if err := tx.Where("role_id = ?", roleID).Delete(&models.RolePermission{}).Error; err != nil {
		return err
	}
	for _, code := range codes {
		if err := tx.Create(&models.RolePermission{RoleID: roleID, PermissionCode: code}).Error; err != nil {
			return err
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func uniqueUint(values []uint) []uint {
	set := map[uint]struct{}{}
	for _, value := range values {
		if value != 0 {
			set[value] = struct{}{}
		}
	}
	result := make([]uint, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func validRoleCode(value string) bool {
	if len(value) < 3 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func notFound(err error, message string) error {
	if err == gorm.ErrRecordNotFound {
		return appError(CodeNotFound, message, err)
	}
	return err
}
