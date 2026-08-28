package database

import (
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrationV55AddsMediaCatalogDeletionCheckpoint(t *testing.T) {
	db, err := Open(t.TempDir() + "/migration-v55.db")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 54)
	if db.Migrator().HasTable(&models.MediaCatalogDeletionPreview{}) {
		t.Fatal("v55 checkpoint table existed before upgrade")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&models.MediaCatalogDeletionPreview{}) {
		t.Fatal("v55 media catalog deletion checkpoint table missing")
	}
	for _, column := range []string{"token_hash", "actor_id", "library_id", "work_key", "entry_digest", "snapshot_json", "state_json", "started_at", "consumed_at", "expires_at"} {
		if !db.Migrator().HasColumn(&models.MediaCatalogDeletionPreview{}, column) {
			t.Fatalf("v55 column %s missing", column)
		}
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 55).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v55 migration count=%d err=%v", count, err)
	}
}
