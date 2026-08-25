package database

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/packagefs"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	"gorm.io/gorm"
)

type migration struct {
	Version            int
	Apply              func(*gorm.DB) error
	DisableForeignKeys bool
}

// Migrate applies explicit, monotonically versioned schema migrations and safe seeds.
func Migrate(db *gorm.DB) error {
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL
	)`).Error; err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	for _, item := range schemaMigrations() {
		var count int64
		if err := db.Table("schema_migrations").Where("version = ?", item.Version).Count(&count).Error; err != nil {
			return fmt.Errorf("read migration %d: %w", item.Version, err)
		}
		if count > 0 {
			continue
		}
		apply := func(connection *gorm.DB) error {
			return connection.Transaction(func(tx *gorm.DB) error {
				if err := item.Apply(tx); err != nil {
					return err
				}
				return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", item.Version, time.Now().UTC()).Error
			})
		}
		var err error
		if item.DisableForeignKeys {
			err = db.Connection(func(connection *gorm.DB) error {
				if err := connection.Exec(`PRAGMA foreign_keys = OFF`).Error; err != nil {
					return err
				}
				applyErr := apply(connection)
				enableErr := connection.Exec(`PRAGMA foreign_keys = ON`).Error
				if applyErr != nil {
					return applyErr
				}
				if enableErr != nil {
					return enableErr
				}
				var violations int64
				if err := connection.Raw(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&violations).Error; err != nil {
					return err
				}
				if violations != 0 {
					return fmt.Errorf("foreign key check found %d violations", violations)
				}
				return nil
			})
		} else {
			err = apply(db)
		}
		if err != nil {
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

func schemaMigrations() []migration {
	return []migration{{Version: 1, Apply: migrateAuthFoundation}, {Version: 2, Apply: migrateStorageFoundation}, {Version: 3, Apply: migrateMediaClassificationProfiles}, {Version: 4, Apply: migrateRuntimeLogging}, {Version: 5, Apply: migrateMediaLibraries}, {Version: 6, Apply: migratePersistentQueue}, {Version: 7, Apply: migrateDownloaderManagement}, {Version: 8, Apply: migrateUnifiedDownloadStaging}, {Version: 9, Apply: migrateDownloadClassification}, {Version: 10, Apply: migrateTMDBRoutes}, {Version: 11, Apply: migrateTMDBCredentialKind}, {Version: 12, Apply: migrateGlobalDownloadStaging}, {Version: 13, Apply: migrateAutomaticDownloadClassification}, {Version: 14, Apply: migrateLibraryImportRouting}, {Version: 15, Apply: migrateSeedingManagement}, {Version: 16, Apply: migrateTransferOrganizationCenter}, {Version: 17, Apply: migratePan115Connections}, {Version: 18, Apply: migratePan115StorageRoots, DisableForeignKeys: true}, {Version: 19, Apply: migrateProviderEventInbox}, {Version: 20, Apply: migratePan115OfflineDownloader, DisableForeignKeys: true}, {Version: 21, Apply: migrateMediaLibraryCatalogV21}, {Version: 22, Apply: migratePan115OfflineDownloaderDirectories}, {Version: 23, Apply: migratePan115CloudImport}, {Version: 24, Apply: migrateProfileRecognitionAndNaming}, {Version: 25, Apply: migrateSharedMediaRecognition}, {Version: 26, Apply: migratePan115ShareIngest}, {Version: 27, Apply: migrateMediaArtifactsAndProxy}, {Version: 28, Apply: migrateSTRMAssetExtensionsAndGatewayAlias}, {Version: 29, Apply: migrateArtifactAutoCleanup}, {Version: 30, Apply: migratePan115MultiDevicePlayback}, {Version: 31, Apply: migrateEmbyWebEnhancements}, {Version: 32, Apply: migratePlayerDeviceTokens}, {Version: 33, Apply: migratePluginRepositories}, {Version: 34, Apply: migratePluginInstallations}, {Version: 35, Apply: migratePluginPackageIntegrity, DisableForeignKeys: true}, {Version: 36, Apply: migratePluginHostCapabilities}, {Version: 37, Apply: migratePluginOnlineMediaContracts}, {Version: 38, Apply: migratePluginManagedImports}, {Version: 39, Apply: migrateMediaRefreshNotify}, {Version: 40, Apply: migrateDiscoveryCache}, {Version: 41, Apply: migrateDownloadRecognitionOverride}, {Version: 42, Apply: migratePTSites}, {Version: 43, Apply: migrateCookieCloudAndSiteRendering}, {Version: 44, Apply: migratePTSiteCatalog, DisableForeignKeys: true}, {Version: 45, Apply: migrateCompletedDownloadManifest}, {Version: 46, Apply: migrateDownloaderQueueDelegation}}
}

func migrateDownloaderQueueDelegation(db *gorm.DB) error {
	// Preserve administrator-tuned policies. Only the untouched v6 seed is
	// lifted so qBittorrent can enforce its own active-download limits.
	return db.Exec(`UPDATE queue_policies SET concurrency = 64, revision = 2, updated_at = ? WHERE job_type = 'download' AND concurrency = 2 AND resource_concurrency = 1 AND max_attempts = 5 AND lease_seconds = 30 AND revision = 1`, time.Now().UTC()).Error
}

func migrateCompletedDownloadManifest(db *gorm.DB) error {
	for _, statement := range []string{
		`ALTER TABLE download_tasks ADD COLUMN completed_manifest_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE download_tasks ADD COLUMN staging_category TEXT NOT NULL DEFAULT ''`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePTSiteCatalog(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE sites_v44 (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, name_normalized TEXT NOT NULL, kind TEXT NOT NULL CHECK(length(kind) BETWEEN 1 AND 32), base_url TEXT NOT NULL, credential_ciphertext TEXT NOT NULL, user_agent TEXT NOT NULL DEFAULT '', browser_emulation INTEGER NOT NULL DEFAULT 0, browser_service_url TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, priority INTEGER NOT NULL DEFAULT 100, timeout_seconds INTEGER NOT NULL DEFAULT 12, rate_limit_per_minute INTEGER NOT NULL DEFAULT 12, last_health_status TEXT NOT NULL DEFAULT 'unknown', last_health_error_code TEXT NOT NULL DEFAULT '', last_health_username TEXT NOT NULL DEFAULT '', last_health_checked_at DATETIME, revision INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`INSERT INTO sites_v44(id,name,name_normalized,kind,base_url,credential_ciphertext,user_agent,browser_emulation,browser_service_url,enabled,priority,timeout_seconds,rate_limit_per_minute,last_health_status,last_health_error_code,last_health_username,last_health_checked_at,revision,created_at,updated_at) SELECT id,name,name_normalized,kind,base_url,credential_ciphertext,user_agent,browser_emulation,browser_service_url,enabled,priority,timeout_seconds,rate_limit_per_minute,last_health_status,last_health_error_code,last_health_username,last_health_checked_at,revision,created_at,updated_at FROM sites`,
		`DROP TABLE sites`,
		`ALTER TABLE sites_v44 RENAME TO sites`,
		`CREATE UNIQUE INDEX idx_sites_name_normalized ON sites(name_normalized)`,
		`CREATE INDEX idx_sites_kind ON sites(kind)`,
		`CREATE INDEX idx_sites_enabled ON sites(enabled)`,
		`CREATE INDEX idx_sites_priority ON sites(priority)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateCookieCloudAndSiteRendering(db *gorm.DB) error {
	now := time.Now().UTC()
	statements := []string{
		`ALTER TABLE sites ADD COLUMN browser_emulation INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sites ADD COLUMN browser_service_url TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE cookie_cloud_settings (id INTEGER PRIMARY KEY CHECK(id = 1), mode TEXT NOT NULL DEFAULT 'disabled' CHECK(mode IN ('disabled','remote','local')), base_url TEXT NOT NULL DEFAULT '', credential_ciphertext TEXT NOT NULL DEFAULT '', auto_sync_minutes INTEGER NOT NULL DEFAULT 0, last_sync_status TEXT NOT NULL DEFAULT 'never', last_sync_error_code TEXT NOT NULL DEFAULT '', last_sync_at DATETIME, revision INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE cookie_cloud_payloads (id INTEGER PRIMARY KEY CHECK(id = 1), uuid_hash TEXT NOT NULL DEFAULT '', encrypted_payload TEXT NOT NULL DEFAULT '', crypto_type TEXT NOT NULL DEFAULT 'legacy', updated_at DATETIME NOT NULL)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	if err := db.Exec(`INSERT INTO cookie_cloud_settings(id,mode,base_url,credential_ciphertext,auto_sync_minutes,last_sync_status,last_sync_error_code,revision,created_at,updated_at) VALUES (1,'disabled','','',0,'never','',1,?,?)`, now, now).Error; err != nil {
		return err
	}
	return db.Exec(`INSERT INTO cookie_cloud_payloads(id,uuid_hash,encrypted_payload,crypto_type,updated_at) VALUES (1,'','','legacy',?)`, now).Error
}

func migratePTSites(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE sites (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, name_normalized TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('pttime')), base_url TEXT NOT NULL, credential_ciphertext TEXT NOT NULL, user_agent TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, priority INTEGER NOT NULL DEFAULT 100, timeout_seconds INTEGER NOT NULL DEFAULT 12, rate_limit_per_minute INTEGER NOT NULL DEFAULT 12, last_health_status TEXT NOT NULL DEFAULT 'unknown', last_health_error_code TEXT NOT NULL DEFAULT '', last_health_username TEXT NOT NULL DEFAULT '', last_health_checked_at DATETIME, revision INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE UNIQUE INDEX idx_sites_name_normalized ON sites(name_normalized)`,
		`CREATE INDEX idx_sites_kind ON sites(kind)`,
		`CREATE INDEX idx_sites_enabled ON sites(enabled)`,
		`CREATE INDEX idx_sites_priority ON sites(priority)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateDownloadRecognitionOverride(db *gorm.DB) error {
	for _, statement := range []string{
		`ALTER TABLE download_tasks ADD COLUMN recognition_override_tmdb_id INTEGER`,
		`ALTER TABLE download_tasks ADD COLUMN recognition_override_media_type TEXT NOT NULL DEFAULT ''`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateDiscoveryCache(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE discovery_caches (id INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL, section TEXT NOT NULL, locale TEXT NOT NULL, page INTEGER NOT NULL, payload_json TEXT NOT NULL, fresh_until DATETIME NOT NULL, stale_until DATETIME NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE UNIQUE INDEX idx_discovery_cache_identity ON discovery_caches(provider, section, locale, page)`,
		`CREATE INDEX idx_discovery_caches_fresh_until ON discovery_caches(fresh_until)`,
		`CREATE INDEX idx_discovery_caches_stale_until ON discovery_caches(stale_until)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateMediaRefreshNotify(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE media_libraries ADD COLUMN content_revision INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE media_library_changes (sequence INTEGER PRIMARY KEY AUTOINCREMENT, library_id INTEGER NOT NULL, revision INTEGER NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('catalog','metadata','removal')), state TEXT NOT NULL CHECK(state IN ('pending','ready')), generation INTEGER NOT NULL DEFAULT 0, ready_at DATETIME, created_at DATETIME NOT NULL, FOREIGN KEY(library_id) REFERENCES media_libraries(id) ON DELETE CASCADE, UNIQUE(library_id, revision))`,
		`CREATE INDEX idx_media_library_changes_ready ON media_library_changes(state, sequence)`,
		`CREATE INDEX idx_media_library_changes_created_at ON media_library_changes(created_at)`,
		`CREATE TABLE media_server_refresh_targets (id INTEGER PRIMARY KEY AUTOINCREMENT, library_id INTEGER NOT NULL, connection_id INTEGER NOT NULL, upstream_library_id TEXT NOT NULL, upstream_library_name TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, desired_revision INTEGER NOT NULL DEFAULT 0, successful_revision INTEGER NOT NULL DEFAULT 0, manual_generation INTEGER NOT NULL DEFAULT 0, successful_manual_generation INTEGER NOT NULL DEFAULT 0, last_job_id TEXT, last_status TEXT NOT NULL DEFAULT 'idle', last_error_code TEXT NOT NULL DEFAULT '', last_attempt_at DATETIME, last_successful_at DATETIME, revision INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, FOREIGN KEY(library_id) REFERENCES media_libraries(id) ON DELETE CASCADE, FOREIGN KEY(connection_id) REFERENCES connections(id) ON DELETE RESTRICT, FOREIGN KEY(last_job_id) REFERENCES jobs(id) ON DELETE SET NULL, UNIQUE(library_id, connection_id, upstream_library_id))`,
		`CREATE INDEX idx_media_server_refresh_targets_library ON media_server_refresh_targets(library_id)`,
		`CREATE INDEX idx_media_server_refresh_targets_connection ON media_server_refresh_targets(connection_id)`,
		`CREATE INDEX idx_media_server_refresh_targets_desired ON media_server_refresh_targets(desired_revision)`,
		`CREATE TABLE media_server_refresh_runs (id TEXT PRIMARY KEY, target_id INTEGER NOT NULL, job_id TEXT NOT NULL, desired_revision INTEGER NOT NULL, status TEXT NOT NULL, error_code TEXT NOT NULL DEFAULT '', started_at DATETIME NOT NULL, finished_at DATETIME, FOREIGN KEY(target_id) REFERENCES media_server_refresh_targets(id) ON DELETE CASCADE, FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE)`,
		`CREATE INDEX idx_media_server_refresh_runs_target ON media_server_refresh_runs(target_id, started_at DESC)`,
		`CREATE INDEX idx_media_server_refresh_runs_job ON media_server_refresh_runs(job_id)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePluginManagedImports(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE download_tasks ADD COLUMN plugin_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN plugin_version TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN plugin_connection_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN provider_metadata_json TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX idx_download_tasks_plugin_id ON download_tasks(plugin_id)`,
		`CREATE INDEX idx_download_tasks_plugin_connection_id ON download_tasks(plugin_connection_id)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePluginOnlineMediaContracts(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE plugin_connections ADD COLUMN last_health_status TEXT NOT NULL DEFAULT 'unknown'`,
		`ALTER TABLE plugin_connections ADD COLUMN last_health_error_code TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE plugin_connections ADD COLUMN last_health_checked_at DATETIME`,
		`CREATE INDEX idx_plugin_connections_last_health_status ON plugin_connections(last_health_status)`,
		`CREATE TABLE plugin_online_libraries (
			id TEXT PRIMARY KEY, plugin_id TEXT NOT NULL, connection_id TEXT NOT NULL,
			external_key TEXT NOT NULL, name TEXT NOT NULL, home_contributions_json TEXT NOT NULL DEFAULT '[]',
			enabled INTEGER NOT NULL DEFAULT 1, sort_order INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			FOREIGN KEY(plugin_id) REFERENCES plugin_installations(plugin_id) ON DELETE CASCADE,
			FOREIGN KEY(connection_id) REFERENCES plugin_connections(id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX idx_plugin_online_library_identity ON plugin_online_libraries(connection_id, external_key)`,
		`CREATE INDEX idx_plugin_online_libraries_plugin_id ON plugin_online_libraries(plugin_id)`,
		`CREATE INDEX idx_plugin_online_libraries_enabled ON plugin_online_libraries(enabled)`,
		`CREATE INDEX idx_plugin_online_libraries_sort_order ON plugin_online_libraries(sort_order)`,
		`INSERT INTO plugin_online_libraries(id, plugin_id, connection_id, external_key, name, enabled, sort_order, revision, created_at, updated_at)
		 SELECT id, plugin_id, id, 'default', name, enabled, 0, 1, created_at, updated_at FROM plugin_connections`,
		`CREATE TABLE plugin_feed_caches (
			id INTEGER PRIMARY KEY AUTOINCREMENT, library_id TEXT NOT NULL, route_key TEXT NOT NULL,
			cursor_key TEXT NOT NULL, refresh_session TEXT NOT NULL, response_json TEXT NOT NULL,
			expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			FOREIGN KEY(library_id) REFERENCES plugin_online_libraries(id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX idx_plugin_feed_cache_identity ON plugin_feed_caches(library_id, route_key, cursor_key, refresh_session)`,
		`CREATE INDEX idx_plugin_feed_caches_expires_at ON plugin_feed_caches(expires_at)`,
		`CREATE TABLE plugin_action_receipts (
			id INTEGER PRIMARY KEY AUTOINCREMENT, library_id TEXT NOT NULL, action TEXT NOT NULL,
			idempotency_hash TEXT NOT NULL, response_json TEXT NOT NULL, created_at DATETIME NOT NULL,
			FOREIGN KEY(library_id) REFERENCES plugin_online_libraries(id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX idx_plugin_action_receipt_identity ON plugin_action_receipts(library_id, action, idempotency_hash)`,
		`CREATE INDEX idx_plugin_action_receipts_created_at ON plugin_action_receipts(created_at)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePluginHostCapabilities(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE plugin_connections (
			id TEXT PRIMARY KEY, plugin_id TEXT NOT NULL, name TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}', credential_scope TEXT NOT NULL DEFAULT '',
			credential_mode TEXT NOT NULL DEFAULT 'none', credential_ciphertext TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1, revision INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			FOREIGN KEY(plugin_id) REFERENCES plugin_installations(plugin_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_plugin_connections_plugin_id ON plugin_connections(plugin_id)`,
		`CREATE INDEX idx_plugin_connections_enabled ON plugin_connections(enabled)`,
		`CREATE TABLE plugin_private_kv (
			id INTEGER PRIMARY KEY AUTOINCREMENT, plugin_id TEXT NOT NULL, connection_id TEXT NOT NULL,
			key TEXT NOT NULL, value_ciphertext TEXT NOT NULL, plaintext_bytes INTEGER NOT NULL,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			FOREIGN KEY(plugin_id) REFERENCES plugin_installations(plugin_id) ON DELETE CASCADE,
			FOREIGN KEY(connection_id) REFERENCES plugin_connections(id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX idx_plugin_private_kv_identity ON plugin_private_kv(plugin_id, connection_id, key)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePluginInstallations(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE plugin_packages (
			id INTEGER PRIMARY KEY AUTOINCREMENT, plugin_id TEXT NOT NULL, version TEXT NOT NULL,
			repository_id INTEGER, repository_owner TEXT NOT NULL, repository_repo TEXT NOT NULL,
			registry_commit TEXT NOT NULL, package_sha256 TEXT NOT NULL, manifest_json TEXT NOT NULL,
			package_path TEXT NOT NULL, verified_at DATETIME NOT NULL, created_at DATETIME NOT NULL,
			FOREIGN KEY(repository_id) REFERENCES plugin_repositories(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX idx_plugin_packages_identity ON plugin_packages(plugin_id, version, repository_owner, repository_repo)`,
		`CREATE UNIQUE INDEX idx_plugin_packages_package_sha256 ON plugin_packages(package_sha256)`,
		`CREATE TABLE plugin_installations (
			plugin_id TEXT PRIMARY KEY, active_package_id INTEGER NOT NULL, previous_package_id INTEGER,
			status TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1, runtime_generation INTEGER NOT NULL DEFAULT 0,
			last_runtime_error_code TEXT NOT NULL DEFAULT '', installed_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL, enabled_at DATETIME,
			FOREIGN KEY(active_package_id) REFERENCES plugin_packages(id) ON DELETE RESTRICT,
			FOREIGN KEY(previous_package_id) REFERENCES plugin_packages(id) ON DELETE RESTRICT
		)`,
		`CREATE INDEX idx_plugin_installations_active_package_id ON plugin_installations(active_package_id)`,
		`CREATE INDEX idx_plugin_installations_previous_package_id ON plugin_installations(previous_package_id)`,
		`CREATE INDEX idx_plugin_installations_status ON plugin_installations(status)`,
		`CREATE TABLE plugin_permission_grants (
			id INTEGER PRIMARY KEY AUTOINCREMENT, plugin_id TEXT NOT NULL, plugin_package_id INTEGER NOT NULL,
			permission_key TEXT NOT NULL, permission_json TEXT NOT NULL, granted_by INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(plugin_package_id) REFERENCES plugin_packages(id) ON DELETE CASCADE,
			FOREIGN KEY(granted_by) REFERENCES users(id) ON DELETE RESTRICT
		)`,
		`CREATE UNIQUE INDEX idx_plugin_permission_grants_identity ON plugin_permission_grants(plugin_id, plugin_package_id, permission_key)`,
		`CREATE INDEX idx_plugin_permission_grants_granted_by ON plugin_permission_grants(granted_by)`,
		`CREATE TABLE plugin_runtime_generations (
			id INTEGER PRIMARY KEY AUTOINCREMENT, plugin_id TEXT NOT NULL, plugin_package_id INTEGER NOT NULL,
			generation INTEGER NOT NULL, status TEXT NOT NULL, safe_error_code TEXT NOT NULL DEFAULT '',
			started_at DATETIME NOT NULL, stopped_at DATETIME,
			FOREIGN KEY(plugin_package_id) REFERENCES plugin_packages(id) ON DELETE RESTRICT
		)`,
		`CREATE UNIQUE INDEX idx_plugin_runtime_generation ON plugin_runtime_generations(plugin_id, generation)`,
		`CREATE INDEX idx_plugin_runtime_generations_package_id ON plugin_runtime_generations(plugin_package_id)`,
		`CREATE INDEX idx_plugin_runtime_generations_status ON plugin_runtime_generations(status)`,
		`CREATE TABLE plugin_install_previews (
			id TEXT PRIMARY KEY, plugin_id TEXT NOT NULL, plugin_package_id INTEGER NOT NULL,
			operation TEXT NOT NULL, permission_fingerprint TEXT NOT NULL, installation_revision INTEGER NOT NULL,
			created_by INTEGER NOT NULL, expires_at DATETIME NOT NULL, consumed_at DATETIME, created_at DATETIME NOT NULL,
			FOREIGN KEY(plugin_package_id) REFERENCES plugin_packages(id) ON DELETE CASCADE,
			FOREIGN KEY(created_by) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_plugin_install_previews_plugin_id ON plugin_install_previews(plugin_id)`,
		`CREATE INDEX idx_plugin_install_previews_package_id ON plugin_install_previews(plugin_package_id)`,
		`CREATE INDEX idx_plugin_install_previews_created_by ON plugin_install_previews(created_by)`,
		`CREATE INDEX idx_plugin_install_previews_expires_at ON plugin_install_previews(expires_at)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePluginPackageIntegrity(db *gorm.DB) error {
	for _, statement := range []string{
		`ALTER TABLE plugin_packages ADD COLUMN registry_entry_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE plugin_packages ADD COLUMN manifest_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE plugin_packages ADD COLUMN package_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE plugin_packages ADD COLUMN extracted_tree_sha256 TEXT NOT NULL DEFAULT ''`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	var packages []models.PluginPackage
	if err := db.Find(&packages).Error; err != nil {
		return err
	}
	for _, pluginPackage := range packages {
		treeSHA256, err := packagefs.ComputeManagedTreeSHA256(pluginPackage.PackagePath)
		if err != nil {
			return fmt.Errorf("enroll plugin package %d integrity: %w", pluginPackage.ID, err)
		}
		if err := db.Model(&models.PluginPackage{}).Where("id = ?", pluginPackage.ID).Update("extracted_tree_sha256", treeSHA256).Error; err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE TABLE plugin_permission_grants_v35 (
			id INTEGER PRIMARY KEY AUTOINCREMENT, plugin_id TEXT NOT NULL, plugin_package_id INTEGER NOT NULL,
			permission_key TEXT NOT NULL, permission_json TEXT NOT NULL, granted_by INTEGER,
			created_at DATETIME NOT NULL,
			FOREIGN KEY(plugin_package_id) REFERENCES plugin_packages(id) ON DELETE CASCADE,
			FOREIGN KEY(granted_by) REFERENCES users(id) ON DELETE SET NULL
		)`,
		`INSERT INTO plugin_permission_grants_v35(id, plugin_id, plugin_package_id, permission_key, permission_json, granted_by, created_at)
		 SELECT id, plugin_id, plugin_package_id, permission_key, permission_json, granted_by, created_at FROM plugin_permission_grants`,
		`DROP TABLE plugin_permission_grants`,
		`ALTER TABLE plugin_permission_grants_v35 RENAME TO plugin_permission_grants`,
		`CREATE UNIQUE INDEX idx_plugin_permission_grants_identity ON plugin_permission_grants(plugin_id, plugin_package_id, permission_key)`,
		`CREATE INDEX idx_plugin_permission_grants_granted_by ON plugin_permission_grants(granted_by)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePluginRepositories(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE plugin_repositories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			github_url TEXT NOT NULL,
			github_owner TEXT NOT NULL,
			github_repo TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			priority INTEGER NOT NULL,
			revision INTEGER NOT NULL DEFAULT 1,
			last_commit_sha TEXT NOT NULL DEFAULT '',
			last_refreshed_at DATETIME,
			last_error_code TEXT NOT NULL DEFAULT '',
			cached_registry_json TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE UNIQUE INDEX idx_plugin_repositories_github_url ON plugin_repositories(github_url)`,
		`CREATE INDEX idx_plugin_repositories_enabled ON plugin_repositories(enabled)`,
		`CREATE INDEX idx_plugin_repositories_priority ON plugin_repositories(priority)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePlayerDeviceTokens(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE device_tokens (
			id TEXT PRIMARY KEY, token_hash TEXT NOT NULL, user_id INTEGER NOT NULL,
			device_id_hash TEXT NOT NULL, device_name TEXT NOT NULL, client_kind TEXT NOT NULL,
			created_at DATETIME NOT NULL, last_seen_at DATETIME NOT NULL,
			idle_expires_at DATETIME NOT NULL, absolute_expires_at DATETIME NOT NULL,
			revoked_at DATETIME,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX idx_device_tokens_token_hash ON device_tokens(token_hash)`,
		`CREATE INDEX idx_device_tokens_user_id ON device_tokens(user_id)`,
		`CREATE INDEX idx_device_tokens_device_id_hash ON device_tokens(device_id_hash)`,
		`CREATE INDEX idx_device_tokens_client_kind ON device_tokens(client_kind)`,
		`CREATE INDEX idx_device_tokens_idle_expires_at ON device_tokens(idle_expires_at)`,
		`CREATE INDEX idx_device_tokens_absolute_expires_at ON device_tokens(absolute_expires_at)`,
		`CREATE INDEX idx_device_tokens_revoked_at ON device_tokens(revoked_at)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateEmbyWebEnhancements(db *gorm.DB) error {
	for _, statement := range []string{
		`ALTER TABLE emby_proxy_gateways ADD COLUMN external_player_enabled INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE emby_proxy_gateways ADD COLUMN fanart_enabled INTEGER NOT NULL DEFAULT 1`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePan115MultiDevicePlayback(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE connections ADD COLUMN recycle_credential_ciphertext TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE pan115_playback_leases (
			id TEXT PRIMARY KEY, connection_id INTEGER NOT NULL, artifact_opaque_id TEXT NOT NULL,
			client_fingerprint TEXT NOT NULL, role TEXT NOT NULL, source_provider_item_id TEXT NOT NULL,
			copy_directory_id TEXT NOT NULL DEFAULT '', copy_item_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL, lease_expires_at DATETIME NOT NULL, cleanup_after DATETIME,
			retry_count INTEGER NOT NULL DEFAULT 0, next_retry_at DATETIME,
			last_error_code TEXT NOT NULL DEFAULT '', cleaned_at DATETIME,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			UNIQUE(artifact_opaque_id, client_fingerprint),
			FOREIGN KEY(connection_id) REFERENCES connections(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_pan115_playback_leases_connection ON pan115_playback_leases(connection_id)`,
		`CREATE INDEX idx_pan115_playback_leases_status ON pan115_playback_leases(status)`,
		`CREATE INDEX idx_pan115_playback_leases_expiry ON pan115_playback_leases(lease_expires_at)`,
		`CREATE INDEX idx_pan115_playback_leases_cleanup ON pan115_playback_leases(cleanup_after, next_retry_at)`,
		`ALTER TABLE transfer_tasks ADD COLUMN source_manifest_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE transfer_tasks ADD COLUMN cleanup_status TEXT NOT NULL DEFAULT 'pending'`,
		`ALTER TABLE transfer_tasks ADD COLUMN cleanup_removed INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE transfer_tasks ADD COLUMN cleanup_error_code TEXT NOT NULL DEFAULT ''`,
		`UPDATE transfer_tasks SET source_manifest_json = manifest_json, cleanup_status = 'skipped'`,
		`CREATE INDEX idx_transfer_tasks_cleanup_status ON transfer_tasks(cleanup_status)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateArtifactAutoCleanup(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE media_libraries ADD COLUMN artifact_cleanup_removed INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_libraries ADD COLUMN artifact_cleanup_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE media_libraries ADD COLUMN artifact_cleanup_at DATETIME`,
		`ALTER TABLE media_artifact_runs ADD COLUMN cleanup_status TEXT NOT NULL DEFAULT 'pending'`,
		`ALTER TABLE media_artifact_runs ADD COLUMN cleanup_error_code TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE media_artifact_runs ADD COLUMN cleanup_at DATETIME`,
		`UPDATE media_artifact_runs SET cleanup_status = CASE WHEN status IN ('completed', 'superseded') THEN 'skipped' ELSE 'pending' END`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateSTRMAssetExtensionsAndGatewayAlias(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE media_libraries ADD COLUMN strm_asset_extra_extensions TEXT NOT NULL DEFAULT '[]'`,
		`UPDATE media_libraries SET video_extensions_json = '[".mp4",".mkv",".ts",".iso",".rmvb",".avi",".mov",".mpeg",".mpg",".wmv",".3gp",".asf",".m4v",".flv",".m2ts",".tp",".f4v"]'`,
		`CREATE UNIQUE INDEX idx_emby_proxy_gateways_alias_normalized ON emby_proxy_gateways(lower(public_id))`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

// migrateMediaArtifactsAndProxy adds only durable policy and ownership facts.
// It does not enumerate media, generate files, upload sidecars or create a
// signing key while Server startup is inside the migration transaction.
func migrateMediaArtifactsAndProxy(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE media_libraries ADD COLUMN signed_proxy_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_libraries ADD COLUMN metadata_artifacts_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_libraries ADD COLUMN upload_sidecars INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_libraries ADD COLUMN artifact_generation INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_libraries ADD COLUMN artifact_applied_generation INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_libraries ADD COLUMN artifact_status TEXT NOT NULL DEFAULT 'idle'`,
		`ALTER TABLE media_libraries ADD COLUMN artifact_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE media_libraries ADD COLUMN artifact_updated_at DATETIME`,
		`UPDATE media_libraries SET metadata_artifacts_enabled = CASE WHEN EXISTS (SELECT 1 FROM storages WHERE storages.id = media_libraries.storage_id AND storages.type = 'local') THEN 1 WHEN strm_enabled = 1 THEN 1 ELSE 0 END`,
		`ALTER TABLE connections ADD COLUMN endpoint TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE media_library_source_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT, library_id INTEGER NOT NULL, generation INTEGER NOT NULL,
			provider_id TEXT NOT NULL DEFAULT '', parent_provider_id TEXT NOT NULL DEFAULT '',
			relative_path TEXT NOT NULL, name TEXT NOT NULL, extension TEXT NOT NULL, size INTEGER NOT NULL,
			modified_at DATETIME NOT NULL, hash_hint TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			UNIQUE(library_id, relative_path),
			FOREIGN KEY(library_id) REFERENCES media_libraries(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_media_library_source_assets_library ON media_library_source_assets(library_id)`,
		`CREATE INDEX idx_media_library_source_assets_generation ON media_library_source_assets(generation)`,
		`CREATE INDEX idx_media_library_source_assets_extension ON media_library_source_assets(extension)`,
		`CREATE INDEX idx_media_library_source_assets_active ON media_library_source_assets(active)`,
		`CREATE TABLE media_artifact_runs (
			id TEXT PRIMARY KEY, library_id INTEGER NOT NULL, generation INTEGER NOT NULL, job_id TEXT,
			policy_json TEXT NOT NULL, status TEXT NOT NULL, expected_count INTEGER NOT NULL DEFAULT 0,
			written_count INTEGER NOT NULL DEFAULT 0, updated_count INTEGER NOT NULL DEFAULT 0,
			removed_count INTEGER NOT NULL DEFAULT 0, skipped_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0, retry_count INTEGER NOT NULL DEFAULT 0,
			error_code TEXT NOT NULL DEFAULT '', started_at DATETIME, finished_at DATETIME,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			UNIQUE(library_id, generation), UNIQUE(job_id),
			FOREIGN KEY(library_id) REFERENCES media_libraries(id) ON DELETE CASCADE,
			FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX idx_media_artifact_runs_library ON media_artifact_runs(library_id)`,
		`CREATE INDEX idx_media_artifact_runs_status ON media_artifact_runs(status)`,
		`CREATE TABLE media_artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT, opaque_id TEXT NOT NULL UNIQUE, run_id TEXT NOT NULL,
			library_id INTEGER NOT NULL, source_identity TEXT NOT NULL DEFAULT '',
			provider_item_id TEXT NOT NULL DEFAULT '', provider_parent_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL, target_kind TEXT NOT NULL, relative_path TEXT NOT NULL,
			content_fingerprint TEXT NOT NULL DEFAULT '', target_provider_id TEXT NOT NULL DEFAULT '',
			managed INTEGER NOT NULL, active INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL,
			error_code TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			UNIQUE(library_id, target_kind, relative_path),
			FOREIGN KEY(run_id) REFERENCES media_artifact_runs(id) ON DELETE CASCADE,
			FOREIGN KEY(library_id) REFERENCES media_libraries(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_media_artifacts_run ON media_artifacts(run_id)`,
		`CREATE INDEX idx_media_artifacts_library ON media_artifacts(library_id)`,
		`CREATE INDEX idx_media_artifacts_source ON media_artifacts(source_identity)`,
		`CREATE INDEX idx_media_artifacts_kind ON media_artifacts(kind)`,
		`CREATE INDEX idx_media_artifacts_active ON media_artifacts(active)`,
		`CREATE INDEX idx_media_artifacts_status ON media_artifacts(status)`,
		`CREATE TABLE proxy_signing_keys (
			id TEXT PRIMARY KEY, secret_ciphertext TEXT NOT NULL, status TEXT NOT NULL,
			created_at DATETIME NOT NULL, deactivated_at DATETIME
		)`,
		`CREATE INDEX idx_proxy_signing_keys_status ON proxy_signing_keys(status)`,
		`CREATE UNIQUE INDEX idx_proxy_signing_keys_active ON proxy_signing_keys(status) WHERE status = 'active'`,
		`CREATE TABLE emby_proxy_gateways (
			id INTEGER PRIMARY KEY AUTOINCREMENT, connection_id INTEGER NOT NULL UNIQUE,
			public_id TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 0,
			policy_revision INTEGER NOT NULL DEFAULT 1, last_health_status TEXT NOT NULL DEFAULT 'unknown',
			last_health_error_code TEXT NOT NULL DEFAULT '', last_health_checked_at DATETIME,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			FOREIGN KEY(connection_id) REFERENCES connections(id) ON DELETE CASCADE
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

// migratePan115ShareIngest adds the media-library-owned intake boundary and
// immutable provider staging facts used by share/adopted download tasks. It is
// additive and never inspects or mutates provider data during startup.
func migratePan115ShareIngest(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE media_libraries ADD COLUMN ingest_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_libraries ADD COLUMN ingest_downloader_id TEXT`,
		`ALTER TABLE media_libraries ADD COLUMN ingest_owner_id INTEGER`,
		`ALTER TABLE media_libraries ADD COLUMN ingest_provider_root_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE media_libraries ADD COLUMN ingest_relative_root TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN staging_provider_directory_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN ingest_source_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN source_origin TEXT NOT NULL DEFAULT 'user'`,
		`CREATE INDEX idx_media_libraries_ingest_downloader_id ON media_libraries(ingest_downloader_id)`,
		`CREATE INDEX idx_media_libraries_ingest_owner_id ON media_libraries(ingest_owner_id)`,
		`CREATE UNIQUE INDEX idx_download_tasks_ingest_source_key ON download_tasks(ingest_source_key) WHERE ingest_source_key <> ''`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

const defaultBuiltinRecognitionPacksJSON = `["tv-v1","anime-v1"]`

// migrateSharedMediaRecognition adds the provider-neutral recognition facts and
// immutable built-in word-pack selections. It intentionally preserves every
// v24 media entry as a pending fact for a later recognition pass.
func migrateSharedMediaRecognition(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE media_classification_profiles ADD COLUMN builtin_recognition_packs_json TEXT NOT NULL DEFAULT '["tv-v1","anime-v1"]'`,
		`ALTER TABLE download_tasks ADD COLUMN profile_builtin_recognition_packs_json TEXT NOT NULL DEFAULT '["tv-v1","anime-v1"]'`,
		`CREATE TABLE media_library_recognitions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			library_id INTEGER NOT NULL,
			source_key TEXT NOT NULL,
			input_fingerprint TEXT NOT NULL,
			profile_id INTEGER NOT NULL,
			profile_revision INTEGER NOT NULL,
			status TEXT NOT NULL,
			error_code TEXT NOT NULL DEFAULT '',
			media_type TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			release_year INTEGER,
			tmdb_id INTEGER,
			confidence REAL,
			category_name TEXT NOT NULL DEFAULT '',
			matched_rule_id TEXT,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			manual_override INTEGER NOT NULL DEFAULT 0,
			last_generation INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(library_id, source_key),
			FOREIGN KEY(library_id) REFERENCES media_libraries(id) ON DELETE CASCADE,
			FOREIGN KEY(profile_id) REFERENCES media_classification_profiles(id) ON DELETE RESTRICT
		)`,
		`CREATE INDEX idx_media_library_recognitions_input ON media_library_recognitions(input_fingerprint)`,
		`CREATE INDEX idx_media_library_recognitions_profile ON media_library_recognitions(profile_id)`,
		`CREATE INDEX idx_media_library_recognitions_status ON media_library_recognitions(library_id, status)`,
		`CREATE INDEX idx_media_library_recognitions_media_type ON media_library_recognitions(media_type)`,
		`CREATE INDEX idx_media_library_recognitions_tmdb ON media_library_recognitions(tmdb_id)`,
		`CREATE INDEX idx_media_library_recognitions_generation ON media_library_recognitions(library_id, last_generation)`,
		`CREATE TABLE media_recognition_cache (
			lookup_key TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			error_code TEXT NOT NULL DEFAULT '',
			result_json TEXT NOT NULL DEFAULT '{}',
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE INDEX idx_media_recognition_cache_expires ON media_recognition_cache(expires_at)`,
		`ALTER TABLE media_library_entries ADD COLUMN recognition_id INTEGER REFERENCES media_library_recognitions(id) ON DELETE SET NULL`,
		`ALTER TABLE media_library_entries ADD COLUMN tmdb_id INTEGER`,
		`ALTER TABLE media_library_entries ADD COLUMN release_year INTEGER`,
		`ALTER TABLE media_library_entries ADD COLUMN match_confidence REAL`,
		`ALTER TABLE media_library_entries ADD COLUMN recognition_error_code TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX idx_media_library_entries_recognition ON media_library_entries(recognition_id)`,
		`CREATE INDEX idx_media_library_entries_tmdb ON media_library_entries(library_id, tmdb_id)`,
		`ALTER TABLE media_library_scan_runs ADD COLUMN matched INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_library_scan_runs ADD COLUMN unrecognized INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_library_scan_runs ADD COLUMN cache_hits INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_library_scan_runs ADD COLUMN recognition_failed INTEGER NOT NULL DEFAULT 0`,
		`UPDATE media_classification_profiles SET builtin_recognition_packs_json = '["tv-v1","anime-v1"]' WHERE builtin_recognition_packs_json IS NULL OR trim(builtin_recognition_packs_json) = ''`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateProfileRecognitionAndNaming(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE media_classification_profiles ADD COLUMN recognition_rules_json TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE media_classification_profiles ADD COLUMN movie_directory_template TEXT NOT NULL DEFAULT '{category}/{title} ({year})'`,
		`ALTER TABLE media_classification_profiles ADD COLUMN movie_filename_template TEXT NOT NULL DEFAULT '{title} ({year})'`,
		`ALTER TABLE media_classification_profiles ADD COLUMN tv_directory_template TEXT NOT NULL DEFAULT '{category}/{title} ({year})/Season {season:02}'`,
		`ALTER TABLE media_classification_profiles ADD COLUMN tv_filename_template TEXT NOT NULL DEFAULT '{title} - S{season:02}E{episode:02}'`,
		`ALTER TABLE download_tasks ADD COLUMN profile_recognition_rules_json TEXT NOT NULL DEFAULT '[]'`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return migrateLibraryNamingIntoProfiles(db)
}

type legacyLibraryNaming struct {
	ID                     uint
	ProfileID              uint
	MovieDirectoryTemplate string
	MovieFilenameTemplate  string
	TVDirectoryTemplate    string
	TVFilenameTemplate     string
}

type legacyProfileNamingKey struct {
	ProfileID              uint
	MovieDirectoryTemplate string
	MovieFilenameTemplate  string
	TVDirectoryTemplate    string
	TVFilenameTemplate     string
}

// migrateLibraryNamingIntoProfiles preserves the v14-v23 contract where each
// MediaLibrary owned its naming templates. A Profile can be shared by libraries
// with different templates, so every distinct legacy combination receives a
// private Profile copy and all matching libraries are rebound atomically.
func migrateLibraryNamingIntoProfiles(db *gorm.DB) error {
	var libraries []legacyLibraryNaming
	if err := db.Table("media_libraries").Order("id").Find(&libraries).Error; err != nil {
		return err
	}
	if len(libraries) == 0 {
		return nil
	}
	var profiles []models.MediaClassificationProfile
	if err := db.Find(&profiles).Error; err != nil {
		return err
	}
	profilesByID := make(map[uint]models.MediaClassificationProfile, len(profiles))
	for _, profile := range profiles {
		profilesByID[profile.ID] = profile
	}
	migrated := map[legacyProfileNamingKey]uint{}
	for _, library := range libraries {
		profile, exists := profilesByID[library.ProfileID]
		if !exists {
			return fmt.Errorf("media library %d references missing profile %d", library.ID, library.ProfileID)
		}
		key := legacyProfileNamingKey{ProfileID: profile.ID, MovieDirectoryTemplate: library.MovieDirectoryTemplate, MovieFilenameTemplate: library.MovieFilenameTemplate, TVDirectoryTemplate: library.TVDirectoryTemplate, TVFilenameTemplate: library.TVFilenameTemplate}
		if profileNamingMatchesLegacy(profile, key) {
			continue
		}
		profileID, exists := migrated[key]
		if !exists {
			name, normalized, err := uniqueMigratedProfileName(db, profile.Name, library.ID)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			copy := models.MediaClassificationProfile{Name: name, NameNormalized: normalized, Kind: models.MediaClassificationProfileKindCustom, Protected: false, SchemaVersion: profile.SchemaVersion, RulesJSON: profile.RulesJSON, RecognitionRulesJSON: profile.RecognitionRulesJSON, MovieDirectoryTemplate: key.MovieDirectoryTemplate, MovieFilenameTemplate: key.MovieFilenameTemplate, TVDirectoryTemplate: key.TVDirectoryTemplate, TVFilenameTemplate: key.TVFilenameTemplate, Revision: 1, CreatedAt: now, UpdatedAt: now}
			// This code runs before v25. Keep the historical INSERT pinned to the
			// v24 columns so future model fields cannot make a v23 upgrade fail.
			if err := db.Exec(`INSERT INTO media_classification_profiles(name,name_normalized,kind,protected,schema_version,rules_json,recognition_rules_json,movie_directory_template,movie_filename_template,tv_directory_template,tv_filename_template,revision,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, copy.Name, copy.NameNormalized, copy.Kind, copy.Protected, copy.SchemaVersion, copy.RulesJSON, copy.RecognitionRulesJSON, copy.MovieDirectoryTemplate, copy.MovieFilenameTemplate, copy.TVDirectoryTemplate, copy.TVFilenameTemplate, copy.Revision, copy.CreatedAt, copy.UpdatedAt).Error; err != nil {
				return err
			}
			if err := db.Select("id").Where("name_normalized = ?", normalized).First(&copy).Error; err != nil {
				return err
			}
			profileID = copy.ID
			migrated[key] = profileID
		}
		if err := db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Updates(map[string]any{"profile_id": profileID, "profile_revision": 1, "reclassification_due": true}).Error; err != nil {
			return err
		}
	}
	return nil
}

func profileNamingMatchesLegacy(profile models.MediaClassificationProfile, naming legacyProfileNamingKey) bool {
	return profile.MovieDirectoryTemplate == naming.MovieDirectoryTemplate &&
		profile.MovieFilenameTemplate == naming.MovieFilenameTemplate &&
		profile.TVDirectoryTemplate == naming.TVDirectoryTemplate &&
		profile.TVFilenameTemplate == naming.TVFilenameTemplate
}

func uniqueMigratedProfileName(db *gorm.DB, source string, libraryID uint) (string, string, error) {
	base := truncateRunes(strings.TrimSpace(source), 96)
	if base == "" {
		base = "媒体规则"
	}
	base = fmt.Sprintf("%s（旧命名-%d）", base, libraryID)
	for suffix := 1; suffix <= 1000; suffix++ {
		name := base
		if suffix > 1 {
			name = fmt.Sprintf("%s-%d", base, suffix)
		}
		name = truncateRunes(name, 128)
		normalized := strings.ToLower(strings.TrimSpace(name))
		var count int64
		if err := db.Model(&models.MediaClassificationProfile{}).Where("name_normalized = ?", normalized).Count(&count).Error; err != nil {
			return "", "", err
		}
		if count == 0 {
			return name, normalized, nil
		}
	}
	return "", "", errors.New("unable to allocate migrated profile name")
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

func migratePan115CloudImport(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE download_tasks ADD COLUMN target_storage_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN target_connection_id INTEGER`,
		`ALTER TABLE download_tasks ADD COLUMN target_provider_root_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE transfer_tasks ADD COLUMN cloud_state_json TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX idx_download_tasks_target_connection ON download_tasks(target_connection_id)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePan115OfflineDownloaderDirectories(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE downloaders ADD COLUMN provider_directory_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE downloaders ADD COLUMN provider_directory_path TEXT NOT NULL DEFAULT ''`,
		`UPDATE downloaders SET provider_directory_id = COALESCE((SELECT storages.root_path FROM storages WHERE storages.id = downloaders.storage_id), ''), provider_directory_path = '/' WHERE type = 'pan115_offline' AND provider_directory_id = ''`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateMediaLibraryCatalogV21(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE media_libraries ADD COLUMN provider_root_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE media_library_entries ADD COLUMN work_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE media_library_entries ADD COLUMN series_title TEXT NOT NULL DEFAULT ''`,
		`UPDATE media_libraries SET provider_root_id = COALESCE((SELECT storages.root_path FROM storages WHERE storages.id = media_libraries.storage_id AND storages.type = 'pan115'), '') WHERE provider_root_id = ''`,
		`UPDATE media_library_entries SET series_title = title WHERE media_type = 'tv' AND series_title = ''`,
		`UPDATE media_library_entries SET work_key = CASE WHEN media_type = 'tv' THEN 'series:legacy:' || substr(hex(lower(trim(title))), 1, 48) ELSE 'file:legacy:' || id END WHERE work_key = ''`,
		`CREATE INDEX idx_media_libraries_provider_root ON media_libraries(storage_id, provider_root_id)`,
		`CREATE INDEX idx_media_library_entries_work ON media_library_entries(library_id, work_key)`,
		`CREATE INDEX idx_media_library_entries_search ON media_library_entries(library_id, media_type, title)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePan115OfflineDownloader(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE downloaders_v20 (id TEXT PRIMARY KEY, name TEXT NOT NULL, name_normalized TEXT NOT NULL UNIQUE, type TEXT NOT NULL CHECK(type IN ('fake','qbittorrent','pan115_offline')), base_url TEXT NOT NULL DEFAULT '', username_ciphertext TEXT NOT NULL DEFAULT '', password_ciphertext TEXT NOT NULL DEFAULT '', storage_id INTEGER, enabled INTEGER NOT NULL DEFAULT 1, capabilities_json TEXT NOT NULL, last_health_status TEXT NOT NULL DEFAULT 'unknown', last_health_version TEXT NOT NULL DEFAULT '', last_health_error_code TEXT NOT NULL DEFAULT '', last_health_checked_at DATETIME, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, FOREIGN KEY(storage_id) REFERENCES storages(id) ON DELETE RESTRICT)`,
		`INSERT INTO downloaders_v20 SELECT id,name,name_normalized,type,base_url,username_ciphertext,password_ciphertext,storage_id,enabled,capabilities_json,last_health_status,last_health_version,last_health_error_code,last_health_checked_at,created_at,updated_at FROM downloaders`,
		`DROP TABLE downloaders`,
		`ALTER TABLE downloaders_v20 RENAME TO downloaders`,
		`CREATE INDEX idx_downloaders_type ON downloaders(type)`,
		`CREATE INDEX idx_downloaders_storage ON downloaders(storage_id)`,
		`ALTER TABLE download_tasks ADD COLUMN provider_output_id TEXT NOT NULL DEFAULT ''`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateProviderEventInbox(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE provider_events (id INTEGER PRIMARY KEY AUTOINCREMENT, connection_id INTEGER NOT NULL, stream TEXT NOT NULL, provider_event_id TEXT NOT NULL, event_time DATETIME NOT NULL, kind TEXT NOT NULL, item_id TEXT NOT NULL, parent_id TEXT NOT NULL DEFAULT '', previous_parent_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', payload_json TEXT NOT NULL, processed_at DATETIME, created_at DATETIME NOT NULL, FOREIGN KEY(connection_id) REFERENCES connections(id) ON DELETE CASCADE, UNIQUE(connection_id, stream, provider_event_id))`,
		`CREATE INDEX idx_provider_events_connection_id ON provider_events(connection_id)`,
		`CREATE INDEX idx_provider_events_event_time ON provider_events(event_time)`,
		`CREATE INDEX idx_provider_events_item_id ON provider_events(item_id)`,
		`CREATE INDEX idx_provider_events_processed_at ON provider_events(processed_at)`,
		`CREATE TABLE provider_cursors (connection_id INTEGER NOT NULL, stream TEXT NOT NULL, cursor_time DATETIME NOT NULL, cursor_id TEXT NOT NULL DEFAULT '', updated_at DATETIME NOT NULL, PRIMARY KEY(connection_id, stream), FOREIGN KEY(connection_id) REFERENCES connections(id) ON DELETE CASCADE)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePan115Connections(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE connections (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			name_normalized TEXT NOT NULL UNIQUE,
			provider TEXT NOT NULL,
			credential_ciphertext TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			account_id TEXT NOT NULL DEFAULT '',
			account_name TEXT NOT NULL DEFAULT '',
			account_vip INTEGER NOT NULL DEFAULT 0,
			quota_used_bytes INTEGER,
			quota_total_bytes INTEGER,
			last_health_status TEXT NOT NULL DEFAULT 'unknown',
			last_health_error_code TEXT NOT NULL DEFAULT '',
			last_health_checked_at DATETIME,
			revision INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE INDEX idx_connections_provider ON connections(provider)`,
		`ALTER TABLE storages ADD COLUMN root_display_path TEXT NOT NULL DEFAULT ''`,
		`UPDATE storages SET root_display_path = root_path WHERE root_display_path = ''`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePan115StorageRoots(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE storages_v18 (
			id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, name_normalized TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL CHECK(type IN ('local','pan115')), root_path TEXT NOT NULL,
			root_display_path TEXT NOT NULL DEFAULT '', root_path_normalized TEXT NOT NULL UNIQUE,
			connection_id INTEGER, enabled INTEGER NOT NULL DEFAULT 1, capabilities TEXT NOT NULL,
			last_probe_exists INTEGER NOT NULL DEFAULT 0, last_probe_readable INTEGER NOT NULL DEFAULT 0,
			last_probe_available INTEGER NOT NULL DEFAULT 0, last_probe_free_bytes INTEGER,
			last_probe_total_bytes INTEGER, last_probe_error_code TEXT NOT NULL DEFAULT '',
			last_probe_checked_at DATETIME, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			FOREIGN KEY(connection_id) REFERENCES connections(id) ON DELETE RESTRICT
		)`,
		`INSERT INTO storages_v18 SELECT id,name,name_normalized,type,root_path,root_display_path,root_path_normalized,connection_id,enabled,capabilities,last_probe_exists,last_probe_readable,last_probe_available,last_probe_free_bytes,last_probe_total_bytes,last_probe_error_code,last_probe_checked_at,created_at,updated_at FROM storages`,
		`DROP TABLE storages`,
		`ALTER TABLE storages_v18 RENAME TO storages`,
		`CREATE INDEX idx_storages_connection_id ON storages(connection_id)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func migrateTransferOrganizationCenter(db *gorm.DB) error {
	return db.Exec(`ALTER TABLE transfer_tasks ADD COLUMN plan_summary_json TEXT NOT NULL DEFAULT ''`).Error
}

func migrateSeedingManagement(db *gorm.DB) error {
	now := time.Now().UTC()
	statements := []string{
		`ALTER TABLE download_tasks ADD COLUMN seeding_cleanup_enabled INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE download_tasks ADD COLUMN seeding_minimum_minutes INTEGER NOT NULL DEFAULT 1440`,
		`ALTER TABLE download_tasks ADD COLUMN seeding_minimum_ratio REAL NOT NULL DEFAULT 1`,
		`ALTER TABLE download_tasks ADD COLUMN seeding_completion_mode TEXT NOT NULL DEFAULT 'all'`,
		`CREATE TABLE seeding_settings (id INTEGER PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 0, minimum_seed_minutes INTEGER NOT NULL DEFAULT 1440, minimum_ratio REAL NOT NULL DEFAULT 1, completion_mode TEXT NOT NULL DEFAULT 'all', revision INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE seeding_tasks (id TEXT PRIMARY KEY, owner_id INTEGER NOT NULL, job_id TEXT NOT NULL UNIQUE, download_task_id TEXT NOT NULL UNIQUE, downloader_id TEXT, downloader_name TEXT NOT NULL, provider_type TEXT NOT NULL, provider_task_id TEXT NOT NULL, transfer_mode TEXT NOT NULL, delete_data INTEGER NOT NULL, cleanup_enabled INTEGER NOT NULL, minimum_seed_minutes INTEGER NOT NULL, minimum_ratio REAL NOT NULL, completion_mode TEXT NOT NULL, phase TEXT NOT NULL, ratio REAL, seeded_seconds INTEGER, uploaded_bytes INTEGER, last_sampled_at DATETIME, last_error_code TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, finished_at DATETIME, FOREIGN KEY(owner_id) REFERENCES users(id) ON DELETE RESTRICT, FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE, FOREIGN KEY(download_task_id) REFERENCES download_tasks(id) ON DELETE CASCADE, FOREIGN KEY(downloader_id) REFERENCES downloaders(id) ON DELETE SET NULL)`,
		`CREATE INDEX idx_seeding_tasks_owner ON seeding_tasks(owner_id, created_at DESC)`,
		`CREATE INDEX idx_seeding_tasks_phase ON seeding_tasks(phase, updated_at)`,
		`UPDATE downloaders SET capabilities_json = '{"pause":true,"resume":true,"cancel":true,"delete_data":true,"download_speed":true,"upload_speed":true,"eta":true,"seeding":true,"native_offline":false,"output_constraint":"local_staging"}' WHERE type = 'qbittorrent'`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return db.Create(&models.SeedingSettings{ID: 1, Enabled: false, MinimumSeedMinutes: 1440, MinimumRatio: 1, CompletionMode: models.SeedingCompletionAll, Revision: 1, CreatedAt: now, UpdatedAt: now}).Error
}

func migrateLibraryImportRouting(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE media_libraries ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE media_libraries ADD COLUMN transfer_mode TEXT NOT NULL DEFAULT 'move'`,
		`ALTER TABLE media_libraries ADD COLUMN conflict_policy TEXT NOT NULL DEFAULT 'ask'`,
		`ALTER TABLE media_libraries ADD COLUMN movie_directory_template TEXT NOT NULL DEFAULT '{category}/{title} ({year})'`,
		`ALTER TABLE media_libraries ADD COLUMN movie_filename_template TEXT NOT NULL DEFAULT '{title} ({year})'`,
		`ALTER TABLE media_libraries ADD COLUMN tv_directory_template TEXT NOT NULL DEFAULT '{category}/{title} ({year})/Season {season:02}'`,
		`ALTER TABLE media_libraries ADD COLUMN tv_filename_template TEXT NOT NULL DEFAULT '{title} - S{season:02}E{episode:02}'`,
		`CREATE INDEX idx_media_libraries_sort_order ON media_libraries(sort_order, id)`,
		`ALTER TABLE download_tasks ADD COLUMN target_library_id INTEGER`,
		`ALTER TABLE download_tasks ADD COLUMN target_library_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN target_storage_id INTEGER`,
		`ALTER TABLE download_tasks ADD COLUMN target_storage_root TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN target_relative_root TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN transfer_mode TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN conflict_policy TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN movie_directory_template TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN movie_filename_template TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN tv_directory_template TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN tv_filename_template TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN scrape_year INTEGER`,
		`CREATE INDEX idx_download_tasks_target_library ON download_tasks(target_library_id)`,
		`CREATE TABLE transfer_tasks (
			id TEXT PRIMARY KEY,
			owner_id INTEGER NOT NULL,
			job_id TEXT NOT NULL UNIQUE,
			download_task_id TEXT NOT NULL UNIQUE,
			library_id INTEGER NOT NULL,
			library_name TEXT NOT NULL,
			manifest_json TEXT NOT NULL,
			phase TEXT NOT NULL,
			processed_files INTEGER NOT NULL DEFAULT 0,
			total_files INTEGER NOT NULL DEFAULT 0,
			last_error_code TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			finished_at DATETIME,
			FOREIGN KEY(owner_id) REFERENCES users(id) ON DELETE RESTRICT,
			FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE,
			FOREIGN KEY(download_task_id) REFERENCES download_tasks(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_transfer_tasks_library ON transfer_tasks(library_id, created_at DESC)`,
		`CREATE INDEX idx_transfer_tasks_phase ON transfer_tasks(phase, updated_at)`,
		`UPDATE media_libraries SET sort_order = id WHERE sort_order = 0`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

// migrateAutomaticDownloadClassification releases only the legacy download
// classification prompts created before preclassification became fully
// automatic. Other queue action types still retain their normal user-action
// semantics. The provider task remains paused until the download worker safely
// assigns either a matched category or the managed "未识别" fallback.
func migrateAutomaticDownloadClassification(db *gorm.DB) error {
	now := time.Now().UTC()
	legacyJobs := db.Table("jobs").
		Select("jobs.id").
		Joins("JOIN job_action_requests ON job_action_requests.job_id = jobs.id").
		Where("jobs.job_type = ? AND jobs.status = ? AND job_action_requests.action_type = ?", "download", models.JobStatusWaitingUserAction, "download_classification")
	if err := db.Model(&models.DownloadTask{}).
		Where("job_id IN (?)", legacyJobs).
		Updates(map[string]any{
			"phase":              models.DownloadTaskStatusClassifying,
			"scrape_status":      "",
			"last_error_code":    "",
			"last_error_message": "",
			"finished_at":        nil,
			"updated_at":         now,
		}).Error; err != nil {
		return err
	}
	if err := db.Model(&models.JobActionRequest{}).
		Where("action_type = ? AND response = '' AND job_id IN (?)", "download_classification", legacyJobs).
		Updates(map[string]any{"response": "superseded_automatic", "responded_at": now}).Error; err != nil {
		return err
	}
	return db.Model(&models.Job{}).
		Where("job_type = ? AND status = ? AND id IN (?)", "download", models.JobStatusWaitingUserAction, legacyJobs).
		Updates(map[string]any{
			"status":             models.JobStatusQueued,
			"revision":           gorm.Expr("revision + 1"),
			"checkpoint_json":    "{}",
			"next_attempt_at":    nil,
			"lease_token_hash":   "",
			"lease_expires_at":   nil,
			"heartbeat_at":       nil,
			"cancellation_asked": false,
			"interrupt_status":   "",
			"last_error_code":    "",
			"last_error_message": "",
			"finished_at":        nil,
			"updated_at":         now,
		}).Error
}

func migrateTMDBCredentialKind(db *gorm.DB) error {
	// Every pre-v11 encrypted value was a v4 Read Access Token. The default
	// preserves that authentication mode without decrypting or rewriting it.
	return db.Exec(`ALTER TABLE metadata_settings ADD COLUMN tmdb_credential_kind TEXT NOT NULL DEFAULT 'read_access_token'`).Error
}

// migrateGlobalDownloadStaging detaches future staging selections from the
// Storage registry while retaining the v8 fields as a lossless compatibility
// fallback. Backfill is purely lexical so a temporarily unavailable legacy
// disk/share cannot prevent Server startup; services revalidate the complete
// path before save and every execution.
func migrateGlobalDownloadStaging(db *gorm.DB) error {
	if err := db.Exec(`ALTER TABLE download_settings ADD COLUMN absolute_path TEXT NOT NULL DEFAULT ''`).Error; err != nil {
		return err
	}
	if err := db.Exec(`ALTER TABLE download_tasks ADD COLUMN staging_absolute_path TEXT NOT NULL DEFAULT ''`).Error; err != nil {
		return err
	}
	type legacySetting struct {
		StorageID    *uint
		RelativePath string
		RootPath     string
	}
	var setting legacySetting
	if err := db.Table("download_settings").Select("download_settings.storage_id, download_settings.relative_path, storages.root_path").Joins("LEFT JOIN storages ON storages.id = download_settings.storage_id").Where("download_settings.id = ?", 1).Scan(&setting).Error; err != nil {
		return err
	}
	if absolute := legacyStagingAbsolute(setting.RootPath, setting.RelativePath); absolute != "" {
		if err := db.Table("download_settings").Where("id = ?", 1).Update("absolute_path", absolute).Error; err != nil {
			return err
		}
	}
	type legacyTask struct {
		ID           string
		RelativePath string
		RootPath     string
	}
	var tasks []legacyTask
	if err := db.Table("download_tasks").Select("download_tasks.id, download_tasks.staging_relative_path AS relative_path, storages.root_path").Joins("LEFT JOIN storages ON storages.id = download_tasks.staging_storage_id").Scan(&tasks).Error; err != nil {
		return err
	}
	for _, task := range tasks {
		absolute := legacyStagingAbsolute(task.RootPath, task.RelativePath)
		if absolute == "" {
			continue
		}
		if err := db.Table("download_tasks").Where("id = ?", task.ID).Update("staging_absolute_path", absolute).Error; err != nil {
			return err
		}
	}
	return nil
}

func legacyStagingAbsolute(root, relative string) string {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || !filepath.IsAbs(root) {
		return ""
	}
	relative = strings.Trim(strings.ReplaceAll(strings.TrimSpace(relative), "\\", "/"), "/")
	if relative == "" {
		return root
	}
	if relative == ".." || strings.HasPrefix(relative, "../") || strings.Contains(relative, "/../") || strings.HasSuffix(relative, "/..") {
		return ""
	}
	candidate, err := storagefs.Constrain(root, filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return ""
	}
	return candidate
}

func migrateTMDBRoutes(db *gorm.DB) error {
	if err := db.Exec(`ALTER TABLE metadata_settings ADD COLUMN api_base_url TEXT NOT NULL DEFAULT 'https://api.tmdb.org/3'`).Error; err != nil {
		return err
	}
	return db.Exec(`ALTER TABLE metadata_settings ADD COLUMN image_base_url TEXT NOT NULL DEFAULT 'https://image.tmdb.org/t/p'`).Error
}

func migrateDownloadClassification(db *gorm.DB) error {
	now := time.Now().UTC()
	statements := []string{
		`CREATE TABLE metadata_settings (id INTEGER PRIMARY KEY CHECK(id = 1), tmdb_token_ciphertext TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 1, updated_at DATETIME NOT NULL)`,
		`INSERT INTO metadata_settings(id, tmdb_token_ciphertext, revision, updated_at) VALUES (1, '', 1, ?)`,
		`ALTER TABLE download_tasks ADD COLUMN profile_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE download_tasks ADD COLUMN profile_revision INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE download_tasks ADD COLUMN profile_rules_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN scrape_status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN scrape_title TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN scrape_media_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN scrape_category TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE download_tasks ADD COLUMN scrape_tmdb_id INTEGER`,
		`ALTER TABLE download_tasks ADD COLUMN scrape_confidence REAL`,
		`ALTER TABLE download_tasks ADD COLUMN manifest_file_count INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX idx_download_tasks_profile ON download_tasks(profile_id)`,
	}
	for index, statement := range statements {
		if index == 1 {
			if err := db.Exec(statement, now).Error; err != nil {
				return err
			}
			continue
		}
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return db.Exec(`UPDATE download_tasks SET profile_id = COALESCE((SELECT id FROM media_classification_profiles WHERE code = 'default-v1'), 0), profile_revision = 1, profile_rules_json = COALESCE((SELECT rules_json FROM media_classification_profiles WHERE code = 'default-v1'), '') WHERE profile_id = 0`).Error
}

func migrateUnifiedDownloadStaging(db *gorm.DB) error {
	now := time.Now().UTC()
	statements := []string{
		`CREATE TABLE download_settings (id INTEGER PRIMARY KEY CHECK(id = 1), storage_id INTEGER, relative_path TEXT NOT NULL DEFAULT '/', revision INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, FOREIGN KEY(storage_id) REFERENCES storages(id) ON DELETE RESTRICT)`,
		`ALTER TABLE download_tasks ADD COLUMN staging_storage_id INTEGER`,
		`ALTER TABLE download_tasks ADD COLUMN staging_relative_path TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX idx_download_tasks_staging_storage ON download_tasks(staging_storage_id)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	if err := db.Exec(`INSERT INTO download_settings(id, storage_id, relative_path, revision, created_at, updated_at)
		VALUES (1, (SELECT d.storage_id FROM downloaders d JOIN storages s ON s.id = d.storage_id WHERE d.storage_id IS NOT NULL AND s.enabled = 1 AND s.type = 'local' ORDER BY d.created_at, d.id LIMIT 1), '/', 1, ?, ?)`, now, now).Error; err != nil {
		return err
	}
	if err := db.Exec(`UPDATE download_tasks SET staging_storage_id = (SELECT d.storage_id FROM downloaders d WHERE d.id = download_tasks.downloader_id), staging_relative_path = '/' WHERE staging_storage_id IS NULL`).Error; err != nil {
		return err
	}
	return db.Exec(`UPDATE downloaders SET storage_id = NULL`).Error
}

func migrateDownloaderManagement(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE downloaders (id TEXT PRIMARY KEY, name TEXT NOT NULL, name_normalized TEXT NOT NULL UNIQUE, type TEXT NOT NULL CHECK(type IN ('fake','qbittorrent')), base_url TEXT NOT NULL DEFAULT '', username_ciphertext TEXT NOT NULL DEFAULT '', password_ciphertext TEXT NOT NULL DEFAULT '', storage_id INTEGER, enabled INTEGER NOT NULL DEFAULT 1, capabilities_json TEXT NOT NULL, last_health_status TEXT NOT NULL DEFAULT 'unknown', last_health_version TEXT NOT NULL DEFAULT '', last_health_error_code TEXT NOT NULL DEFAULT '', last_health_checked_at DATETIME, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, FOREIGN KEY(storage_id) REFERENCES storages(id) ON DELETE RESTRICT)`,
		`CREATE INDEX idx_downloaders_type ON downloaders(type)`,
		`CREATE INDEX idx_downloaders_storage ON downloaders(storage_id)`,
		`CREATE TABLE download_tasks (id TEXT PRIMARY KEY, owner_id INTEGER NOT NULL, job_id TEXT NOT NULL UNIQUE, downloader_id TEXT, downloader_name TEXT NOT NULL, provider_type TEXT NOT NULL, provider_task_id TEXT NOT NULL DEFAULT '', provider_tag TEXT NOT NULL DEFAULT '', source_ciphertext TEXT NOT NULL, display_name TEXT NOT NULL, provider_status TEXT NOT NULL DEFAULT '', phase TEXT NOT NULL, progress REAL, bytes_completed INTEGER, bytes_total INTEGER, download_speed INTEGER, upload_speed INTEGER, eta_seconds INTEGER, last_sampled_at DATETIME, last_error_code TEXT NOT NULL DEFAULT '', last_error_message TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, finished_at DATETIME, FOREIGN KEY(owner_id) REFERENCES users(id) ON DELETE RESTRICT, FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE, FOREIGN KEY(downloader_id) REFERENCES downloaders(id) ON DELETE SET NULL)`,
		`CREATE INDEX idx_download_tasks_owner ON download_tasks(owner_id, created_at DESC)`,
		`CREATE INDEX idx_download_tasks_downloader ON download_tasks(downloader_id, created_at DESC)`,
		`CREATE INDEX idx_download_tasks_provider_task ON download_tasks(provider_type, provider_task_id)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
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
		{JobType: "download", Concurrency: 64, ResourceConcurrency: 1, MaxAttempts: 5, LeaseSeconds: 30},
		{JobType: "transfer", Concurrency: 2, ResourceConcurrency: 1, MaxAttempts: 3, LeaseSeconds: 30},
		{JobType: "seeding", Concurrency: 4, ResourceConcurrency: 1, MaxAttempts: 5, LeaseSeconds: 30},
		{JobType: "upload", Concurrency: 2, ResourceConcurrency: 1, MaxAttempts: 3, LeaseSeconds: 30},
		{JobType: "scrape", Concurrency: 4, ResourceConcurrency: 2, MaxAttempts: 4, LeaseSeconds: 30},
		{JobType: "media_artifact", Concurrency: 4, ResourceConcurrency: 1, MaxAttempts: 4, LeaseSeconds: 30},
		{JobType: "strm_reconcile", Concurrency: 4, ResourceConcurrency: 1, MaxAttempts: 3, LeaseSeconds: 30},
		{JobType: "media_server_refresh", Concurrency: 4, ResourceConcurrency: 1, MaxAttempts: 5, LeaseSeconds: 30},
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
		hasBuiltinPacks := db.Migrator().HasColumn(&models.MediaClassificationProfile{}, "builtin_recognition_packs_json")
		builtinPacksMatch := !hasBuiltinPacks || existing.BuiltinRecognitionPacksJSON == defaultBuiltinRecognitionPacksJSON
		if existing.Name == "默认分类规则" && existing.NameNormalized == "默认分类规则" && existing.RulesJSON == rulesJSON && existing.Revision == 1 && builtinPacksMatch {
			return nil
		}
		// This stable code is application-owned. Refresh only the known built-in
		// row so a release can keep its immutable contract exact; custom rows are
		// never selected by display name and are never touched here.
		updates := map[string]any{
			"name": "默认分类规则", "name_normalized": "默认分类规则",
			"rules_json": rulesJSON, "revision": 1, "updated_at": now,
		}
		if hasBuiltinPacks {
			updates["builtin_recognition_packs_json"] = defaultBuiltinRecognitionPacksJSON
		}
		return db.Model(&existing).Updates(updates).Error
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	// Keep this seed compatible with focused migration tests that intentionally
	// stop before later additive Profile columns. New columns have database
	// defaults and therefore do not need to appear in the INSERT column list.
	if err := db.Exec(`INSERT INTO media_classification_profiles(code,name,name_normalized,kind,protected,schema_version,rules_json,revision,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, code, "默认分类规则", "默认分类规则", models.MediaClassificationProfileKindSystem, true, 1, rulesJSON, 1, now, now).Error; err != nil {
		return fmt.Errorf("seed default classification profile: %w", err)
	}
	return nil
}

func migrateStorageFoundation(db *gorm.DB) error {
	return db.Exec(`CREATE TABLE storages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		name_normalized TEXT NOT NULL UNIQUE,
		type TEXT NOT NULL CHECK(type IN ('local','pan115')),
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
		operatorCodes := []string{"dashboard.read", "logs.read", "media_libraries.read", "media_libraries.create", "media_libraries.update", "media_libraries.delete", "media_libraries.scan", "connections.read", "connections.create", "connections.update", "connections.test", "downloaders.read", "downloaders.create", "downloaders.update", "downloaders.delete", "downloaders.test", "downloads.read_all", "downloads.create", "downloads.manage_all", "transfers.read_all", "storages.read", "storages.browse", "storages.create", "storages.update", "storages.delete", "storages.test", "media_classification_profiles.read", "media_classification_profiles.create", "media_classification_profiles.update", "media_classification_profiles.delete", "destinations.read", "destinations.create", "destinations.update", "strm.runs.read", "strm.runs.create", "strm.runs.cancel", "media_servers.refresh", "settings.read", "jobs.read_all", "jobs.control_all", "jobs.respond", "jobs.reorder"}
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
