package database

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrateMediaRefreshNotifyIsAdditiveIdempotentAndConstrained(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "media-refresh-v38-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 38)
	if db.Migrator().HasTable(&models.MediaLibraryChange{}) {
		t.Fatal("v39 table unexpectedly existed before migration")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&models.MediaLibrary{}, "content_revision") || !db.Migrator().HasTable(&models.MediaLibraryChange{}) || !db.Migrator().HasTable(&models.MediaServerRefreshTarget{}) || !db.Migrator().HasTable(&models.MediaServerRefreshRun{}) {
		t.Fatal("media refresh schema is incomplete")
	}
	for table, indexes := range map[string][]string{
		"media_library_changes":        {"idx_media_library_changes_ready", "idx_media_library_changes_created_at"},
		"media_server_refresh_targets": {"idx_media_server_refresh_targets_library", "idx_media_server_refresh_targets_connection", "idx_media_server_refresh_targets_desired"},
		"media_server_refresh_runs":    {"idx_media_server_refresh_runs_target", "idx_media_server_refresh_runs_job"},
	} {
		for _, index := range indexes {
			if !db.Migrator().HasIndex(table, index) {
				t.Fatalf("missing %s.%s", table, index)
			}
		}
	}
	var migrationCount int64
	if err := db.Table("schema_migrations").Where("version = ?", 39).Count(&migrationCount).Error; err != nil || migrationCount != 1 {
		t.Fatalf("v39 migration count=%d err=%v", migrationCount, err)
	}
	var policy models.QueuePolicy
	if err := db.First(&policy, "job_type = ?", "media_server_refresh").Error; err != nil {
		t.Fatal(err)
	}
	if policy.ResourceConcurrency != 1 || policy.MaxAttempts != 5 {
		t.Fatalf("refresh queue policy=%+v", policy)
	}

	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Refresh migration storage", NameNormalized: "refresh migration storage", Type: models.StorageTypeLocal, RootPath: t.TempDir(), RootDisplayPath: "Refresh migration storage", RootPathNormalized: "refresh-migration-storage", Enabled: true, Capabilities: "{}", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Refresh migration library", NameNormalized: "refresh migration library", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", Enabled: true, Recursive: true, VideoExtensionsJSON: `[]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, Status: models.MediaLibraryStatusListening, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	connection := models.Connection{Name: "Refresh migration Emby", NameNormalized: "refresh migration emby", Provider: models.ConnectionProviderEmby, Endpoint: "https://emby.example.test", CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO media_server_refresh_targets(library_id,connection_id,upstream_library_id,upstream_library_name,created_at,updated_at) VALUES(?,?,?,?,?,?)`, library.ID, connection.ID, "private-upstream-id", "电影", now, now).Error; err != nil {
		t.Fatal(err)
	}
	var target models.MediaServerRefreshTarget
	if err := db.First(&target, "library_id = ?", library.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !target.Enabled || target.DesiredRevision != 0 || target.SuccessfulRevision != 0 || target.Revision != 1 {
		t.Fatalf("target defaults=%+v", target)
	}
	if err := db.Create(&models.MediaServerRefreshTarget{LibraryID: library.ID, ConnectionID: connection.ID, UpstreamLibraryID: "private-upstream-id", UpstreamLibraryName: "duplicate", Enabled: true, LastStatus: "idle", Revision: 1, CreatedAt: now, UpdatedAt: now}).Error; err == nil {
		t.Fatal("duplicate refresh target identity was accepted")
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-upstream-id") || strings.Contains(string(encoded), "upstream_library_id") {
		t.Fatalf("target serialization leaked upstream identity: %s", encoded)
	}
	if err := db.Delete(&connection).Error; err == nil {
		t.Fatal("connection deletion bypassed refresh target RESTRICT")
	}
	change := models.MediaLibraryChange{LibraryID: library.ID, Revision: 1, Kind: models.MediaLibraryChangeCatalog, State: models.MediaLibraryChangeReady, Generation: 1, ReadyAt: &now, CreatedAt: now}
	if err := db.Create(&change).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&library).Error; err != nil {
		t.Fatal(err)
	}
	for model, label := range map[any]string{&models.MediaLibraryChange{}: "change", &models.MediaServerRefreshTarget{}: "target"} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("cascaded %s count=%d err=%v", label, count, err)
		}
	}
}
