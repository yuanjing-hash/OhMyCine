package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigratePluginInstallationsIsAdditiveAndIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "plugin-installations.db"))
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
	for _, table := range []any{&models.PluginPackage{}, &models.PluginInstallation{}, &models.PluginPermissionGrant{}, &models.PluginRuntimeGeneration{}, &models.PluginInstallPreview{}} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("plugin lifecycle table missing for %T", table)
		}
	}
	for table, indexes := range map[string][]string{
		"plugin_packages":            {"idx_plugin_packages_identity", "idx_plugin_packages_package_sha256"},
		"plugin_installations":       {"idx_plugin_installations_active_package_id", "idx_plugin_installations_status"},
		"plugin_permission_grants":   {"idx_plugin_permission_grants_identity"},
		"plugin_runtime_generations": {"idx_plugin_runtime_generation"},
		"plugin_install_previews":    {"idx_plugin_install_previews_expires_at"},
	} {
		for _, index := range indexes {
			if !db.Migrator().HasIndex(table, index) {
				t.Fatalf("index %s missing on %s", index, table)
			}
		}
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 34).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v34 migration count=%d err=%v", count, err)
	}
	if err := db.Table("schema_migrations").Where("version = ?", 35).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v35 migration count=%d err=%v", count, err)
	}
	var foreignKeys []struct {
		Table    string `gorm:"column:table"`
		From     string `gorm:"column:from"`
		OnDelete string `gorm:"column:on_delete"`
	}
	if err := db.Raw("PRAGMA foreign_key_list(plugin_permission_grants)").Scan(&foreignKeys).Error; err != nil {
		t.Fatal(err)
	}
	foundSetNull := false
	for _, foreignKey := range foreignKeys {
		if foreignKey.Table == "users" && foreignKey.From == "granted_by" && foreignKey.OnDelete == "SET NULL" {
			foundSetNull = true
		}
	}
	if !foundSetNull {
		t.Fatalf("granted_by must use ON DELETE SET NULL: %+v", foreignKeys)
	}
	var columns []struct {
		Name    string `gorm:"column:name"`
		NotNull int    `gorm:"column:notnull"`
	}
	if err := db.Raw("PRAGMA table_info(plugin_permission_grants)").Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if column.Name == "granted_by" && column.NotNull != 0 {
			t.Fatal("granted_by must remain nullable so historical grants do not block user deletion")
		}
	}
}
