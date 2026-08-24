package database

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMigratePTSiteCatalogPreservesExistingSitesAndAllowsCatalogKinds(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "site-catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migratePTSites(db); err != nil {
		t.Fatal(err)
	}
	if err := migrateCookieCloudAndSiteRendering(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO sites(name,name_normalized,kind,base_url,credential_ciphertext,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, "site-pttime", "site-pttime", "pttime", "https://pttime.example.test", "ciphertext", now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := migratePTSiteCatalog(db); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"hdsky", "nexusphp"} {
		name := "site-" + kind
		if err := db.Exec(`INSERT INTO sites(name,name_normalized,kind,base_url,credential_ciphertext,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, name, name, kind, "https://"+kind+".example.test", "ciphertext", now, now).Error; err != nil {
			t.Fatalf("insert kind %s: %v", kind, err)
		}
	}
	var count int64
	if err := db.Table("sites").Count(&count).Error; err != nil || count != 3 {
		t.Fatalf("site count=%d err=%v", count, err)
	}
	var preserved string
	if err := db.Table("sites").Select("credential_ciphertext").Where("kind = ?", "pttime").Scan(&preserved).Error; err != nil || preserved != "ciphertext" {
		t.Fatalf("preserved credential=%q err=%v", preserved, err)
	}
}
