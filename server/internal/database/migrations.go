package database

import (
	"fmt"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

type migration struct {
	Version int
	Apply   func(*gorm.DB) error
}

// Migrate applies explicit, monotonically versioned schema migrations and safe seeds.
func Migrate(db *gorm.DB) error {
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL
	)`).Error; err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	migrations := []migration{{Version: 1, Apply: migrateAuthFoundation}}
	for _, item := range migrations {
		var count int64
		if err := db.Table("schema_migrations").Where("version = ?", item.Version).Count(&count).Error; err != nil {
			return fmt.Errorf("read migration %d: %w", item.Version, err)
		}
		if count > 0 {
			continue
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := item.Apply(tx); err != nil {
				return err
			}
			return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", item.Version, time.Now().UTC()).Error
		}); err != nil {
			return fmt.Errorf("apply migration %d: %w", item.Version, err)
		}
	}
	return seedAuthorization(db)
}

func migrateAuthFoundation(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL,
			username_normalized TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('active','disabled')),
			is_owner INTEGER NOT NULL DEFAULT 0,
			authz_version INTEGER NOT NULL DEFAULT 1,
			last_login_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE UNIQUE INDEX idx_users_single_owner ON users(is_owner) WHERE is_owner = 1`,
		`CREATE INDEX idx_users_status ON users(status)`,
		`CREATE TABLE roles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('system','custom')),
			protected INTEGER NOT NULL DEFAULT 0,
			active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE permissions (
			code TEXT PRIMARY KEY,
			module TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			risk TEXT NOT NULL CHECK(risk IN ('normal','sensitive','destructive')),
			deprecated_at DATETIME
		)`,
		`CREATE INDEX idx_permissions_module ON permissions(module)`,
		`CREATE TABLE user_roles (
			user_id INTEGER NOT NULL,
			role_id INTEGER NOT NULL,
			assigned_by INTEGER,
			created_at DATETIME NOT NULL,
			PRIMARY KEY(user_id, role_id),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY(role_id) REFERENCES roles(id) ON DELETE RESTRICT,
			FOREIGN KEY(assigned_by) REFERENCES users(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE role_permissions (
			role_id INTEGER NOT NULL,
			permission_code TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			PRIMARY KEY(role_id, permission_code),
			FOREIGN KEY(role_id) REFERENCES roles(id) ON DELETE CASCADE,
			FOREIGN KEY(permission_code) REFERENCES permissions(code) ON DELETE RESTRICT
		)`,
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL UNIQUE,
			user_id INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			last_seen_at DATETIME NOT NULL,
			idle_expires_at DATETIME NOT NULL,
			absolute_expires_at DATETIME NOT NULL,
			revoked_at DATETIME,
			user_agent_hash TEXT,
			ip_hint TEXT,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_sessions_user_id ON sessions(user_id)`,
		`CREATE INDEX idx_sessions_expiry ON sessions(idle_expires_at, absolute_expires_at)`,
		`CREATE TABLE audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor_id INTEGER,
			action TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT NOT NULL,
			outcome TEXT NOT NULL,
			metadata TEXT NOT NULL,
			request_id TEXT,
			ip_hint TEXT,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(actor_id) REFERENCES users(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX idx_audit_logs_created_at ON audit_logs(created_at)`,
		`CREATE INDEX idx_audit_logs_action ON audit_logs(action)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func seedAuthorization(db *gorm.DB) error {
	permissions, err := authz.Catalog()
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, permission := range permissions {
			model := models.Permission{Code: permission.Code, Module: permission.Module, Name: permission.Name, Description: permission.Description, Risk: permission.Risk}
			if err := tx.Where("code = ?", model.Code).Assign(model).FirstOrCreate(&model).Error; err != nil {
				return err
			}
		}
		roles := []models.Role{
			{Code: authz.RoleAdministrator, Name: "管理员", Description: "拥有 Server 全部能力的受保护系统角色。", Kind: models.RoleKindSystem, Protected: true, Active: true},
			{Code: authz.RoleOperator, Name: "运维人员", Description: "管理连接、存储目标、STRM 和媒体服务器刷新。", Kind: models.RoleKindSystem, Protected: true, Active: true},
			{Code: authz.RoleViewer, Name: "只读用户", Description: "只查看状态和已授权的媒体信息。", Kind: models.RoleKindSystem, Protected: true, Active: true},
		}
		for i := range roles {
			role := roles[i]
			if err := tx.Where("code = ?", role.Code).Assign(role).FirstOrCreate(&role).Error; err != nil {
				return err
			}
			roles[i] = role
		}
		operatorCodes := []string{"dashboard.read", "connections.read", "connections.create", "connections.update", "connections.test", "destinations.read", "destinations.create", "destinations.update", "strm.runs.read", "strm.runs.create", "strm.runs.cancel", "media_servers.refresh", "settings.read"}
		viewerCodes := []string{"dashboard.read", "connections.read", "destinations.read", "strm.runs.read"}
		for roleIndex, codes := range [][]string{nil, operatorCodes, viewerCodes} {
			if roleIndex == 0 {
				continue
			}
			role := roles[roleIndex]
			if err := tx.Where("role_id = ?", role.ID).Delete(&models.RolePermission{}).Error; err != nil {
				return err
			}
			for _, code := range codes {
				if err := tx.Create(&models.RolePermission{RoleID: role.ID, PermissionCode: code, CreatedAt: time.Now().UTC()}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
