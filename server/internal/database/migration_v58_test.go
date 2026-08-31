package database

import (
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrationV58AddsDisabledPan115RecycleCleanupPolicy(t *testing.T) {
	db, err := Open(t.TempDir() + "/migration-v58.db")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 57)
	now := time.Now().UTC()
	connection := models.Connection{Name: "legacy-115", NameNormalized: "legacy-115", Provider: models.ConnectionProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Omit("RecycleCleanupEnabled", "RecycleCleanupCron", "RecycleCleanupNextRunAt", "RecycleCleanupLastRunAt", "RecycleCleanupLastStatus", "RecycleCleanupLastErrorCode").Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var migrated models.Connection
	if err := db.First(&migrated, connection.ID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.RecycleCleanupEnabled || migrated.RecycleCleanupCron != "0 */7 * * *" || migrated.RecycleCleanupNextRunAt != nil || migrated.RecycleCleanupLastStatus != models.RecycleCleanupStatusIdle || migrated.RecycleCleanupLastErrorCode != "" {
		t.Fatalf("migrated=%+v", migrated)
	}
	var policy models.QueuePolicy
	if err := db.First(&policy, "job_type = ?", "pan115_recycle_cleanup").Error; err != nil {
		t.Fatal(err)
	}
	if policy.ResourceConcurrency != 1 || policy.MaxAttempts != 1 {
		t.Fatalf("policy=%+v", policy)
	}
}
