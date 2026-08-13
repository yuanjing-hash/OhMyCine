package database

import (
	"fmt"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
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
	migrations := []migration{{Version: 1, Apply: migrateAuthFoundation}, {Version: 2, Apply: migrateStorageFoundation}, {Version: 3, Apply: migrateMediaClassificationProfiles}, {Version: 4, Apply: migrateRuntimeLogging}, {Version: 5, Apply: migrateMediaLibraries}, {Version: 6, Apply: migratePersistentQueue}}
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
	if err := seedAuthorization(db); err != nil {
		return err
	}
	if err := seedMediaClassificationProfiles(db); err != nil {
		return err
	}
	return seedQueuePolicies(db)
}

func migratePersistentQueue(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE jobs (id TEXT PRIMARY KEY, owner_id INTEGER, created_by_kind TEXT NOT NULL DEFAULT 'user' CHECK(created_by_kind IN ('user','system')), job_type TEXT NOT NULL, priority INTEGER NOT NULL, lane_position INTEGER NOT NULL, revision INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL CHECK(status IN ('queued','running','waiting_user_action','retry_wait','paused','completed','failed','cancelled')), display_name TEXT NOT NULL, provider TEXT NOT NULL DEFAULT '', resource_key TEXT NOT NULL DEFAULT '', coalescing_key TEXT NOT NULL DEFAULT '', generation INTEGER NOT NULL DEFAULT 1, started_generation INTEGER NOT NULL DEFAULT 0, payload_json TEXT NOT NULL DEFAULT '{}', checkpoint_json TEXT NOT NULL DEFAULT '{}', progress REAL, processed_items INTEGER, total_items INTEGER, speed REAL, eta_seconds INTEGER, last_error_code TEXT NOT NULL DEFAULT '', last_error_message TEXT NOT NULL DEFAULT '', next_attempt_at DATETIME, lease_token_hash TEXT NOT NULL DEFAULT '', lease_expires_at DATETIME, heartbeat_at DATETIME, cancellation_asked INTEGER NOT NULL DEFAULT 0, interrupt_status TEXT NOT NULL DEFAULT '', attempt_count INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, started_at DATETIME, finished_at DATETIME, CHECK((created_by_kind = 'user' AND owner_id IS NOT NULL) OR (created_by_kind = 'system' AND owner_id IS NULL)), FOREIGN KEY(owner_id) REFERENCES users(id) ON DELETE RESTRICT)`,
		`CREATE INDEX idx_jobs_lane ON jobs(job_type, priority, lane_position, created_at, id)`,
		`CREATE INDEX idx_jobs_claim ON jobs(job_type, priority, status, next_attempt_at, lane_position)`,
		`CREATE INDEX idx_jobs_owner_status ON jobs(owner_id, status)`,
		`CREATE INDEX idx_jobs_resource ON jobs(resource_key, status)`,
		`CREATE UNIQUE INDEX idx_jobs_active_coalescing ON jobs(job_type, resource_key, coalescing_key) WHERE coalescing_key <> '' AND status IN ('queued','running','waiting_user_action','retry_wait','paused')`,
		`CREATE TABLE job_attempts (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, attempt_number INTEGER NOT NULL, lease_token_hash TEXT NOT NULL, status TEXT NOT NULL, safe_error_code TEXT NOT NULL DEFAULT '', safe_error_message TEXT NOT NULL DEFAULT '', started_at DATETIME NOT NULL, finished_at DATETIME, FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE, UNIQUE(job_id, attempt_number))`,
		`CREATE INDEX idx_job_attempts_job ON job_attempts(job_id, attempt_number DESC)`,
		`CREATE TABLE job_status_events (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, event_type TEXT NOT NULL, from_status TEXT NOT NULL DEFAULT '', to_status TEXT NOT NULL DEFAULT '', actor_id INTEGER, safe_code TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL, FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE, FOREIGN KEY(actor_id) REFERENCES users(id) ON DELETE SET NULL)`,
		`CREATE INDEX idx_job_status_events_job ON job_status_events(job_id, id)`,
		`CREATE TABLE job_action_requests (id INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL, version INTEGER NOT NULL, action_type TEXT NOT NULL, prompt TEXT NOT NULL, options_json TEXT NOT NULL, preview_json TEXT NOT NULL DEFAULT '{}', expires_at DATETIME, response TEXT NOT NULL DEFAULT '', responded_by INTEGER, responded_at DATETIME, created_at DATETIME NOT NULL, FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE, FOREIGN KEY(responded_by) REFERENCES users(id) ON DELETE SET NULL, UNIQUE(job_id, version))`,
		`CREATE INDEX idx_job_actions_job ON job_action_requests(job_id, version DESC)`,
		`CREATE TABLE queue_policies (job_type TEXT PRIMARY KEY, concurrency INTEGER NOT NULL CHECK(concurrency > 0), resource_concurrency INTEGER NOT NULL DEFAULT 0 CHECK(resource_concurrency >= 0), max_attempts INTEGER NOT NULL DEFAULT 3 CHECK(max_attempts > 0), lease_seconds INTEGER NOT NULL DEFAULT 30 CHECK(lease_seconds >= 5), revision INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func seedQueuePolicies(db *gorm.DB) error {
	now := time.Now().UTC()
	defaults := []models.QueuePolicy{
		{JobType: "download", Concurrency: 2, ResourceConcurrency: 1, MaxAttempts: 5, LeaseSeconds: 30},
		{JobType: "transfer", Concurrency: 2, ResourceConcurrency: 1, MaxAttempts: 3, LeaseSeconds: 30},
		{JobType: "upload", Concurrency: 2, ResourceConcurrency: 1, MaxAttempts: 3, LeaseSeconds: 30},
		{JobType: "scrape", Concurrency: 4, ResourceConcurrency: 2, MaxAttempts: 4, LeaseSeconds: 30},
		{JobType: "refresh", Concurrency: 2, ResourceConcurrency: 1, MaxAttempts: 3, LeaseSeconds: 30},
		{JobType: "fake", Concurrency: 2, ResourceConcurrency: 1, MaxAttempts: 3, LeaseSeconds: 10},
	}
	for _, policy := range defaults {
		policy.Revision, policy.CreatedAt, policy.UpdatedAt = 1, now, now
		if err := db.Where("job_type = ?", policy.JobType).FirstOrCreate(&policy).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateMediaLibraries(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE media_libraries (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, name_normalized TEXT NOT NULL UNIQUE, storage_id INTEGER NOT NULL, profile_id INTEGER NOT NULL, profile_revision INTEGER NOT NULL, relative_root TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, recursive INTEGER NOT NULL DEFAULT 1, full_scan_interval_hours INTEGER NOT NULL DEFAULT 24, incremental_minutes INTEGER NOT NULL DEFAULT 15, video_extensions_json TEXT NOT NULL, ignore_patterns_json TEXT NOT NULL, metadata_language TEXT NOT NULL DEFAULT 'zh-CN', metadata_region TEXT NOT NULL DEFAULT 'CN', match_strategy TEXT NOT NULL DEFAULT 'balanced', provider_rate_per_second INTEGER NOT NULL DEFAULT 100, provider_concurrency INTEGER NOT NULL DEFAULT 2, metadata_rate_per_second INTEGER NOT NULL DEFAULT 5, metadata_concurrency INTEGER NOT NULL DEFAULT 1, strm_enabled INTEGER NOT NULL DEFAULT 0, strm_local_root TEXT NOT NULL DEFAULT '', status TEXT NOT NULL, status_error_code TEXT NOT NULL DEFAULT '', next_retry_at DATETIME, last_scan_at DATETIME, last_successful_scan_at DATETIME, baseline_generation INTEGER NOT NULL DEFAULT 0, dirty_generation INTEGER NOT NULL DEFAULT 0, reclassification_due INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, FOREIGN KEY(storage_id) REFERENCES storages(id) ON DELETE RESTRICT, FOREIGN KEY(profile_id) REFERENCES media_classification_profiles(id) ON DELETE RESTRICT)`,
		`CREATE INDEX idx_media_libraries_storage_id ON media_libraries(storage_id)`, `CREATE INDEX idx_media_libraries_profile_id ON media_libraries(profile_id)`, `CREATE INDEX idx_media_libraries_status ON media_libraries(status)`,
		`CREATE TABLE media_library_scan_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, library_id INTEGER NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL, generation INTEGER NOT NULL, discovered INTEGER NOT NULL DEFAULT 0, added INTEGER NOT NULL DEFAULT 0, updated INTEGER NOT NULL DEFAULT 0, removed INTEGER NOT NULL DEFAULT 0, error_code TEXT NOT NULL DEFAULT '', partial INTEGER NOT NULL DEFAULT 0, started_at DATETIME NOT NULL, finished_at DATETIME, FOREIGN KEY(library_id) REFERENCES media_libraries(id) ON DELETE CASCADE)`,
		`CREATE INDEX idx_media_library_scan_runs_library ON media_library_scan_runs(library_id, id DESC)`,
		`CREATE TABLE media_library_entries (id INTEGER PRIMARY KEY AUTOINCREMENT, library_id INTEGER NOT NULL, relative_path TEXT NOT NULL, provider_id TEXT NOT NULL, size INTEGER NOT NULL, modified_at DATETIME NOT NULL, media_type TEXT NOT NULL, title TEXT NOT NULL, season INTEGER, episode INTEGER, match_status TEXT NOT NULL, category_name TEXT NOT NULL, matched_rule_id TEXT, last_generation INTEGER NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, UNIQUE(library_id, relative_path), FOREIGN KEY(library_id) REFERENCES media_libraries(id) ON DELETE CASCADE)`,
		`CREATE INDEX idx_media_library_entries_library_generation ON media_library_entries(library_id, last_generation)`, `CREATE INDEX idx_media_library_entries_type ON media_library_entries(library_id, media_type)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateRuntimeLogging(db *gorm.DB) error {
	now := time.Now().UTC()
	if err := db.Exec(`CREATE TABLE runtime_log_policies (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		level TEXT NOT NULL CHECK(level IN ('debug','info','warn','error')),
		max_file_mi_b INTEGER NOT NULL,
		max_backups INTEGER NOT NULL,
		retention_days INTEGER NOT NULL,
		max_total_mi_b INTEGER NOT NULL,
		revision INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`).Error; err != nil {
		return err
	}
	return db.Exec(`INSERT INTO runtime_log_policies(id, level, max_file_mi_b, max_backups, retention_days, max_total_mi_b, revision, created_at, updated_at)
		VALUES (1, 'info', 20, 10, 30, 500, 1, ?, ?)`, now, now).Error
}

func migrateMediaClassificationProfiles(db *gorm.DB) error {
	return db.Exec(`CREATE TABLE media_classification_profiles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT UNIQUE,
		name TEXT NOT NULL,
		name_normalized TEXT NOT NULL UNIQUE,
		kind TEXT NOT NULL CHECK(kind IN ('system','custom')),
		protected INTEGER NOT NULL DEFAULT 0,
		schema_version INTEGER NOT NULL CHECK(schema_version = 1),
		rules_json TEXT NOT NULL,
		revision INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`).Error
}

func seedMediaClassificationProfiles(db *gorm.DB) error {
	rulesJSON, err := classification.CanonicalJSON(classification.DefaultRules())
	if err != nil {
		return fmt.Errorf("validate default classification profile: %w", err)
	}
	code := "default-v1"
	now := time.Now().UTC()
	var existing models.MediaClassificationProfile
	err = db.Where("code = ?", code).First(&existing).Error
	if err == nil {
		if existing.Kind != models.MediaClassificationProfileKindSystem || !existing.Protected || existing.SchemaVersion != classification.SchemaVersion {
			return fmt.Errorf("default classification profile metadata is invalid")
		}
		if existing.Name == "默认分类规则" && existing.NameNormalized == "默认分类规则" && existing.RulesJSON == rulesJSON && existing.Revision == 1 {
			return nil
		}
		// This stable code is application-owned. Refresh only the known built-in
		// row so a release can keep its immutable contract exact; custom rows are
		// never selected by display name and are never touched here.
		return db.Model(&existing).Updates(map[string]any{
			"name": "默认分类规则", "name_normalized": "默认分类规则",
			"rules_json": rulesJSON, "revision": 1, "updated_at": now,
		}).Error
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	record := models.MediaClassificationProfile{Code: &code, Name: "默认分类规则", NameNormalized: "默认分类规则", Kind: models.MediaClassificationProfileKindSystem, Protected: true, SchemaVersion: 1, RulesJSON: rulesJSON, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&record).Error; err != nil {
		return fmt.Errorf("seed default classification profile: %w", err)
	}
	return nil
}

func migrateStorageFoundation(db *gorm.DB) error {
	return db.Exec(`CREATE TABLE storages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		name_normalized TEXT NOT NULL UNIQUE,
		type TEXT NOT NULL CHECK(type IN ('local')),
		root_path TEXT NOT NULL,
		root_path_normalized TEXT NOT NULL UNIQUE,
		connection_id INTEGER,
		enabled INTEGER NOT NULL DEFAULT 1,
		capabilities TEXT NOT NULL,
		last_probe_exists INTEGER NOT NULL DEFAULT 0,
		last_probe_readable INTEGER NOT NULL DEFAULT 0,
		last_probe_available INTEGER NOT NULL DEFAULT 0,
		last_probe_free_bytes INTEGER,
		last_probe_total_bytes INTEGER,
		last_probe_error_code TEXT NOT NULL DEFAULT '',
		last_probe_checked_at DATETIME,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`).Error
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
		operatorCodes := []string{"dashboard.read", "logs.read", "media_libraries.read", "media_libraries.create", "media_libraries.update", "media_libraries.delete", "media_libraries.scan", "connections.read", "connections.create", "connections.update", "connections.test", "storages.read", "storages.browse", "storages.create", "storages.update", "storages.delete", "storages.test", "media_classification_profiles.read", "media_classification_profiles.create", "media_classification_profiles.update", "media_classification_profiles.delete", "destinations.read", "destinations.create", "destinations.update", "strm.runs.read", "strm.runs.create", "strm.runs.cancel", "media_servers.refresh", "settings.read", "jobs.read_all", "jobs.control_all", "jobs.respond", "jobs.reorder"}
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
