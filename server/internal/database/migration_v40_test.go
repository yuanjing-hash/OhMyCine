package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrateDiscoveryCacheIsAdditiveAndIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "discovery.db"))
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
	if !db.Migrator().HasTable(&models.DiscoveryCache{}) || !db.Migrator().HasIndex(&models.DiscoveryCache{}, "idx_discovery_cache_identity") {
		t.Fatal("discovery cache table or identity index missing")
	}
	now := time.Now().UTC()
	first := models.DiscoveryCache{Provider: "tmdb", Section: "trending-movie", Locale: "zh-CN:CN", Page: 1, PayloadJSON: `{}`, FreshUntil: now, StaleUntil: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.ID = 0
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate discovery cache identity accepted")
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 40).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v40 count=%d err=%v", count, err)
	}
}
