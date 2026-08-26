package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrateDownloadMediaIdentityIsPrivateAdditiveAndIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "download-media-identity.db"))
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
	for _, column := range []string{"identity_source", "identity_status", "identity_locked", "identity_revision", "identity_snapshot_json"} {
		if !db.Migrator().HasColumn(&models.DownloadTask{}, column) {
			t.Fatalf("download media identity column %q missing", column)
		}
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 48).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v48 count=%d err=%v", count, err)
	}
}
