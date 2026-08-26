package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrateTransferDeletionV51CreatesPrivatePreviewBoundary(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "transfer-deletion.db"))
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
	if !db.Migrator().HasTable(&models.TransferDeletionPreview{}) {
		t.Fatal("missing v51 transfer deletion preview table")
	}
	for _, column := range []string{"token_hash", "scope", "source_manifest_digest", "managed_manifest_digest", "transfer_job_revision", "download_job_revision", "consumed_at"} {
		if !db.Migrator().HasColumn(&models.TransferDeletionPreview{}, column) {
			t.Fatalf("missing v51 column %s", column)
		}
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 51).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v51 count=%d err=%v", count, err)
	}
}
