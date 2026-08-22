package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigratePluginRepositoriesIsAdditiveAndIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "plugin-repositories.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	created := models.PluginRepository{
		Name: "Official", GitHubURL: "https://github.com/ohmycine/plugins", GitHubOwner: "ohmycine", GitHubRepo: "plugins",
		Enabled: true, Priority: 1000, Revision: 1, CachedRegistryJSON: "", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := db.Create(&created).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&models.PluginRepository{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("repository count=%d err=%v", count, err)
	}
	if err := db.Table("schema_migrations").Where("version = ?", 33).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v33 migration count=%d err=%v", count, err)
	}
	for _, column := range []string{"github_url", "github_owner", "github_repo", "enabled", "priority", "revision", "last_commit_sha", "last_refreshed_at", "last_error_code", "cached_registry_json"} {
		if !db.Migrator().HasColumn(&models.PluginRepository{}, column) {
			t.Fatalf("plugin repository column %s missing", column)
		}
	}
}
