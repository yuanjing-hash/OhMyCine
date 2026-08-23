package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigratePluginOnlineMediaContractsIsAdditiveBackfillsAndCascades(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "plugin-online.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 36)
	now := time.Now().UTC()
	pkg := models.PluginPackage{PluginID: "org.example.online", Version: "0.1.0", RepositoryOwner: "example", RepositoryRepo: "plugins", RegistryCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RegistryEntryJSON: `{}`, ManifestURL: "https://github.com/example/plugins/releases/download/v1/manifest.json", PackageURL: "https://github.com/example/plugins/releases/download/v1/plugin.omcp", PackageSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ExtractedTreeSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ManifestJSON: `{}`, PackagePath: "managed", VerifiedAt: now, CreatedAt: now}
	if err := db.Create(&pkg).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PluginInstallation{PluginID: pkg.PluginID, ActivePackageID: pkg.ID, Status: models.PluginInstallationEnabled, Revision: 1, InstalledAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	connection := models.PluginConnection{ID: "11111111-1111-4111-8111-111111111111", PluginID: pkg.PluginID, Name: "legacy online", ConfigJSON: `{}`, CredentialMode: models.PluginCredentialModeNone, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Exec(`INSERT INTO plugin_connections(id, plugin_id, name, config_json, credential_scope, credential_mode, credential_ciphertext, enabled, revision, created_at, updated_at) VALUES (?, ?, ?, ?, '', ?, '', 1, 1, ?, ?)`, connection.ID, connection.PluginID, connection.Name, connection.ConfigJSON, connection.CredentialMode, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&models.PluginOnlineLibrary{}, &models.PluginFeedCache{}, &models.PluginActionReceipt{}} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing v37 table for %T", model)
		}
	}
	var library models.PluginOnlineLibrary
	if err := db.First(&library, "id = ?", connection.ID).Error; err != nil || library.ConnectionID != connection.ID || library.ExternalKey != "default" || library.Name != connection.Name {
		t.Fatalf("backfilled library=%+v err=%v", library, err)
	}
	cache := models.PluginFeedCache{LibraryID: library.ID, RouteKey: "recommended", CursorKey: "cursor", RefreshSession: connection.ID, ResponseJSON: `[]`, ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
	receipt := models.PluginActionReceipt{LibraryID: library.ID, Action: "favorite.add", IdempotencyHash: "hash", ResponseJSON: `{"accepted":true}`, CreatedAt: now}
	if err := db.Create(&cache).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&receipt).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&connection).Error; err != nil {
		t.Fatal(err)
	}
	for name, model := range map[string]any{"libraries": &models.PluginOnlineLibrary{}, "caches": &models.PluginFeedCache{}, "receipts": &models.PluginActionReceipt{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s survived connection delete: count=%d err=%v", name, count, err)
		}
	}
}
