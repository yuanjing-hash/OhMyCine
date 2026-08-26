package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrateMediaReorganizationV50CreatesPrivateOwnershipBoundary(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "reorganization.db"))
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
	for _, model := range []any{&models.MediaManagedItem{}, &models.MediaReorganizationPreview{}, &models.MediaReorganizationTask{}} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing v50 table for %T", model)
		}
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 50).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v50 count=%d err=%v", count, err)
	}
	var policy models.QueuePolicy
	if err := db.First(&policy, "job_type = ?", "media_reorganization").Error; err != nil {
		t.Fatal(err)
	}
	if policy.Concurrency != 2 || policy.ResourceConcurrency != 1 || policy.MaxAttempts != 5 {
		t.Fatalf("unexpected reorganization queue policy: %+v", policy)
	}
}
