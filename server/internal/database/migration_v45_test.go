package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrateCompletedDownloadManifestIsPrivateAdditiveAndIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "completed-download-manifest.db"))
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
	if !db.Migrator().HasColumn(&models.DownloadTask{}, "completed_manifest_json") || !db.Migrator().HasColumn(&models.DownloadTask{}, "staging_category") {
		t.Fatal("completed download recovery columns missing")
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 45).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v45 count=%d err=%v", count, err)
	}
}
