package services

import (
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestStorageConstraintErrorMapsUniqueColumnsToStableCodes(t *testing.T) {
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := models.Storage{
		Name: "Media", NameNormalized: "media", Type: models.StorageTypeLocal,
		RootPath: `C:\Media`, RootPathNormalized: `c:\media`, Enabled: true,
		Capabilities: `{}`, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&base).Error; err != nil {
		t.Fatal(err)
	}

	duplicateName := base
	duplicateName.ID = 0
	duplicateName.RootPath = `D:\Media`
	duplicateName.RootPathNormalized = `d:\media`
	err = db.Create(&duplicateName).Error
	if code := ErrorCode(storageConstraintError(err)); code != CodeStorageNameConflict {
		t.Fatalf("duplicate name code=%q error=%v", code, err)
	}

	duplicateRoot := base
	duplicateRoot.ID = 0
	duplicateRoot.Name = "Other"
	duplicateRoot.NameNormalized = "other"
	err = db.Create(&duplicateRoot).Error
	if code := ErrorCode(storageConstraintError(err)); code != CodeStoragePathConflict {
		t.Fatalf("duplicate root code=%q error=%v", code, err)
	}
}
