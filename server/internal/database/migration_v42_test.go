package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigratePTSitesIsAdditiveAndIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "sites.db"))
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
	if !db.Migrator().HasTable(&models.Site{}) || !db.Migrator().HasIndex(&models.Site{}, "idx_sites_name_normalized") {
		t.Fatal("sites table or unique index missing")
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 42).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v42 count=%d err=%v", count, err)
	}
}
