package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrationV54AddsMediaArtifactContentLease(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "artifact-lease.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 53)
	if db.Migrator().HasColumn(&models.MediaArtifact{}, "content_expires_at") || db.Migrator().HasColumn(&models.MediaArtifact{}, "content_format_version") {
		t.Fatal("v54 lease columns existed before upgrade")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&models.MediaArtifact{}, "content_expires_at") || !db.Migrator().HasColumn(&models.MediaArtifact{}, "content_format_version") {
		t.Fatal("v54 lease columns missing after upgrade")
	}
	var columns []struct {
		Name       string
		NotNull    int     `gorm:"column:notnull"`
		DefaultSQL *string `gorm:"column:dflt_value"`
	}
	if err := db.Raw("PRAGMA table_info(media_artifacts)").Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	foundFormat := false
	for _, column := range columns {
		if column.Name == "content_format_version" {
			foundFormat = column.NotNull == 1 && column.DefaultSQL != nil && *column.DefaultSQL == "''"
		}
	}
	if !foundFormat {
		t.Fatalf("content_format_version default is not additive-safe: %+v", columns)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 54).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v54 migration count=%d err=%v", count, err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("repeated migration failed: %v", err)
	}
}
