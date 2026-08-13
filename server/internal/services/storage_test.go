package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestStorageUpdateRevalidatesAndProbesUnchangedRoot(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	audit := NewAuditService(db)
	service := NewStorageService(db, audit)
	root := t.TempDir()
	user := models.User{Username: "storage-test", UsernameNormalized: "storage-test", DisplayName: "Storage Test", PasswordHash: "test", Status: models.UserStatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: user, Permissions: map[string]struct{}{
		authz.PermissionStoragesCreate: {}, authz.PermissionStoragesUpdate: {},
	}}
	created, err := service.Create(actor, StorageInput{Name: "Media", Type: "local", RootPath: root, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	newName := "Renamed"
	_, err = service.Update(actor, created.ID, UpdateStorageInput{Name: &newName}, RequestContext{})
	if ErrorCode(err) != "storage_path_not_found" {
		t.Fatalf("stale unchanged root code=%q error=%v", ErrorCode(err), err)
	}
}

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
