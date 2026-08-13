package database

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

func TestMigrateAddsStorageFoundationAndIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "migration.db"))
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
	if !db.Migrator().HasTable("storages") || !db.Migrator().HasTable("media_classification_profiles") {
		t.Fatal("foundation table missing")
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 2).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("migration count=%d err=%v", count, err)
	}
	if err := db.Table("media_classification_profiles").Where("code = ?", "default-v1").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("default profile count=%d err=%v", count, err)
	}
	var builtin models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&builtin).Error; err != nil {
		t.Fatal(err)
	}
	wantRules, _ := classification.CanonicalJSON(classification.DefaultRules())
	if builtin.RulesJSON != wantRules || builtin.Revision != 1 || !builtin.Protected {
		t.Fatalf("default profile drifted: %+v", builtin)
	}
}

func TestMigrateUpgradesAuthFoundationDatabaseToStorageFoundation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := migrateAuthFoundation(tx); err != nil {
			return err
		}
		return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 1, time.Now().UTC()).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable("storages") {
		t.Fatal("storages table missing after v1 to v2 upgrade")
	}
	var versions []int
	if err := db.Table("schema_migrations").Order("version").Pluck("version", &versions).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(versions, []int{1, 2, 3, 4}) {
		t.Fatalf("migration versions=%v, want [1 2 3 4]", versions)
	}
}

func TestOpenConfiguresSQLitePragmasWithoutCGO(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var foreignKeys int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d, want 1", foreignKeys)
	}
	var journalMode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode=%q, want wal", journalMode)
	}
}

func TestMediaClassificationProfilePermissionSeeds(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "permissions.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	profileCodes := []string{authz.PermissionMediaClassificationProfilesRead, authz.PermissionMediaClassificationProfilesCreate, authz.PermissionMediaClassificationProfilesUpdate, authz.PermissionMediaClassificationProfilesDelete}
	for _, roleCode := range []string{authz.RoleOperator, authz.RoleViewer} {
		var role models.Role
		if err := db.Where("code = ?", roleCode).First(&role).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Model(&models.RolePermission{}).Where("role_id = ? AND permission_code IN ?", role.ID, profileCodes).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		want := int64(4)
		if roleCode == authz.RoleViewer {
			want = 0
		}
		if count != want {
			t.Fatalf("role %s has %d profile permissions, want %d", roleCode, count, want)
		}
	}
}

func TestMigrateV2ToV3PreservesStorageAndCustomProfilesAcrossReseed(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "v2-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := migrateAuthFoundation(tx); err != nil {
			return err
		}
		if err := migrateStorageFoundation(tx); err != nil {
			return err
		}
		return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (1, ?), (2, ?)", time.Now().UTC(), time.Now().UTC()).Error
	}); err != nil {
		t.Fatal(err)
	}
	storage := models.Storage{Name: "Existing", NameNormalized: "existing", Type: "local", RootPath: `D:\Media`, RootPathNormalized: `d:\media`, Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}

	rules, _ := json.Marshal(classification.EmptyRules())
	custom := models.MediaClassificationProfile{Name: "Custom", NameNormalized: "custom", Kind: models.MediaClassificationProfileKindCustom, SchemaVersion: 1, RulesJSON: string(rules), Revision: 7, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&custom).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}

	var storageCount, builtinCount, customCount int64
	if err := db.Model(&models.Storage{}).Where("id = ?", storage.ID).Count(&storageCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MediaClassificationProfile{}).Where("code = ?", "default-v1").Count(&builtinCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MediaClassificationProfile{}).Where("id = ? AND revision = ? AND rules_json = ?", custom.ID, 7, string(rules)).Count(&customCount).Error; err != nil {
		t.Fatal(err)
	}
	if storageCount != 1 || builtinCount != 1 || customCount != 1 {
		t.Fatalf("preservation counts storage=%d builtin=%d custom=%d", storageCount, builtinCount, customCount)
	}
}
