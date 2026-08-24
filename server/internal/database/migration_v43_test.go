package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrateCookieCloudAndSiteRenderingIsAdditiveAndIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "cookiecloud.db"))
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
	if !db.Migrator().HasColumn(&models.Site{}, "BrowserEmulation") || !db.Migrator().HasColumn(&models.Site{}, "BrowserServiceURL") {
		t.Fatal("site browser emulation columns missing")
	}
	if !db.Migrator().HasTable(&models.CookieCloudSettings{}) || !db.Migrator().HasTable(&models.CookieCloudPayload{}) {
		t.Fatal("CookieCloud singleton tables missing")
	}
	if !db.Migrator().HasColumn(&models.CookieCloudPayload{}, "CryptoType") {
		t.Fatal("CookieCloud crypto type column missing")
	}
	var settings models.CookieCloudSettings
	if err := db.First(&settings, 1).Error; err != nil || settings.Mode != "disabled" || settings.Revision != 1 {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	var payload models.CookieCloudPayload
	if err := db.First(&payload, 1).Error; err != nil {
		t.Fatalf("payload singleton missing: %v", err)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 43).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v43 count=%d err=%v", count, err)
	}
}
