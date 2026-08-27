package database

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrationV52CreatesAutomaticTVFollowFoundation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "follow.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"follow_subscriptions", "follow_subscription_seasons", "follow_runs", "follow_episode_claims"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("missing table %s", table)
		}
	}
	if !db.Migrator().HasColumn(&models.DownloadTask{}, "follow_subscription_id") || !db.Migrator().HasColumn(&models.DownloadTask{}, "follow_resource_fingerprint") {
		t.Fatal("download follow idempotency columns missing")
	}
	var policy models.QueuePolicy
	if err := db.First(&policy, "job_type = ?", "follow-search").Error; err != nil {
		t.Fatal(err)
	}
	if policy.Concurrency != 4 || policy.ResourceConcurrency != 1 || policy.LeaseSeconds != 60 {
		t.Fatalf("unexpected follow policy %+v", policy)
	}
	now := time.Now().UTC()
	user := models.User{Username: "follow", UsernameNormalized: "follow", DisplayName: "Follow", PasswordHash: "x", Status: models.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	first := models.FollowSubscription{ID: "first", OwnerID: user.ID, MediaType: "tv", TMDBID: 100, Title: "Series", Status: models.FollowStatusActive, Revision: 1, ExecutionSnapshotJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.FollowSubscriptionSeason{SubscriptionID: first.ID, OwnerID: user.ID, TMDBID: 100, SeasonNumber: 1}).Error; err != nil {
		t.Fatal(err)
	}
	second := models.FollowSubscription{ID: "second", OwnerID: user.ID, MediaType: "tv", TMDBID: 100, Title: "Series", Status: models.FollowStatusPaused, Revision: 1, ExecutionSnapshotJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.FollowSubscriptionSeason{SubscriptionID: second.ID, OwnerID: user.ID, TMDBID: 100, SeasonNumber: 1}).Error; err == nil {
		t.Fatal("duplicate owner/media/season should fail")
	}
	bad := first
	bad.ID = "bad-media"
	bad.MediaType = "movie"
	if err := db.Create(&bad).Error; err == nil {
		t.Fatal("non-TV follow should fail its check constraint")
	}
	if err := db.Exec(`INSERT INTO follow_episode_claims(subscription_id,season_number,episode_number,state,updated_at) VALUES(?,?,?,?,?)`, first.ID, 201, 1, "missing", now).Error; err == nil {
		t.Fatal("out-of-range claim season should fail its check constraint")
	}
	ownerID := user.ID
	job := models.Job{ID: "follow-download-job", OwnerID: &ownerID, CreatedByKind: "user", JobType: "download", Revision: 1, Status: models.JobStatusQueued, DisplayName: "Follow download", Provider: "fake", ResourceKey: "fake:follow", Generation: 1, PayloadJSON: `{}`, CheckpointJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	download := models.DownloadTask{ID: "follow-download", OwnerID: user.ID, JobID: job.ID, DownloaderName: "Fake", ProviderType: models.DownloaderTypeFake, SourceCiphertext: "encrypted", FollowSubscriptionID: first.ID, FollowResourceFingerprint: strings.Repeat("a", 64), DisplayName: "Series S01E01", Phase: models.DownloadTaskStatusQueued, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&download).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&first).Error; err != nil {
		t.Fatal(err)
	}
	var seasons int64
	if err := db.Model(&models.FollowSubscriptionSeason{}).Where("subscription_id = ?", first.ID).Count(&seasons).Error; err != nil || seasons != 0 {
		t.Fatalf("cascade seasons=%d err=%v", seasons, err)
	}
	var preserved models.DownloadTask
	if err := db.First(&preserved, "id = ?", download.ID).Error; err != nil || preserved.FollowSubscriptionID != first.ID {
		t.Fatalf("follow deletion removed or rewrote submitted download: %+v err=%v", preserved, err)
	}
	var foreignKeyViolations []struct{ Table string }
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&foreignKeyViolations).Error; err != nil || len(foreignKeyViolations) != 0 {
		t.Fatalf("foreign key violations=%v err=%v", foreignKeyViolations, err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("idempotent migrate: %v", err)
	}
}

func TestMigrationV52UpgradesPreviousHead(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "follow-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 51)
	if db.Migrator().HasTable("follow_subscriptions") || db.Migrator().HasColumn(&models.DownloadTask{}, "follow_subscription_id") {
		t.Fatal("v52 schema existed before upgrade")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 52).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v52 migration count=%d err=%v", count, err)
	}
	if !db.Migrator().HasTable("follow_subscriptions") || !db.Migrator().HasColumn(&models.DownloadTask{}, "follow_subscription_id") {
		t.Fatal("v52 schema missing after previous-head upgrade")
	}
}
