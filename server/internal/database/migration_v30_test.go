package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigratePan115MultiDevicePlaybackMarksLegacyTransfersSkipped(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "v30.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		`CREATE TABLE connections (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE transfer_tasks (id TEXT PRIMARY KEY, manifest_json TEXT NOT NULL)`,
		`INSERT INTO transfer_tasks(id, manifest_json) VALUES ('legacy', '{"Complete":true,"Files":[{"relative_path":"Movie.mkv","size":1}]}')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migratePan115MultiDevicePlayback(db); err != nil {
		t.Fatal(err)
	}
	var task models.TransferTask
	if err := db.Table("transfer_tasks").Select("id, manifest_json, source_manifest_json, cleanup_status, cleanup_removed, cleanup_error_code").First(&task, "id = ?", "legacy").Error; err != nil {
		t.Fatal(err)
	}
	if task.SourceManifestJSON != task.ManifestJSON || task.CleanupStatus != models.TransferCleanupSkipped || task.CleanupRemoved != 0 || task.CleanupErrorCode != "" {
		t.Fatalf("legacy transfer cleanup defaults=%+v", task)
	}
}
