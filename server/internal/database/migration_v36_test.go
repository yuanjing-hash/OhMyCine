package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigratePluginHostCapabilitiesIsAdditiveAndIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "plugin-host.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&models.PluginConnection{}) || !db.Migrator().HasTable(&models.PluginPrivateKV{}) {
		t.Fatal("plugin host capability tables are missing")
	}
	if !db.Migrator().HasIndex("plugin_private_kv", "idx_plugin_private_kv_identity") {
		t.Fatal("plugin private KV identity index is missing")
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 36).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v36 migration count=%d err=%v", count, err)
	}
}
