package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrateDownloadRecognitionEpisodeOverrideIsPrivateAdditiveAndIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "download-recognition-episode.db"))
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
	if !db.Migrator().HasColumn(&models.DownloadTask{}, "scrape_season") || !db.Migrator().HasColumn(&models.DownloadTask{}, "scrape_episode") || !db.Migrator().HasColumn(&models.DownloadTask{}, "recognition_override_season") || !db.Migrator().HasColumn(&models.DownloadTask{}, "recognition_override_episode") {
		t.Fatal("download recognition season/episode override columns missing")
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 47).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v47 count=%d err=%v", count, err)
	}
}
