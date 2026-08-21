package database

import (
	"path/filepath"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrateEmbyWebEnhancementsEnablesExistingGatewaysByDefault(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "v31.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		`CREATE TABLE emby_proxy_gateways (id INTEGER PRIMARY KEY, connection_id INTEGER NOT NULL, public_id TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 0, policy_revision INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`INSERT INTO emby_proxy_gateways(id, connection_id, public_id, enabled, policy_revision, created_at, updated_at) VALUES (1, 1, 'home', 1, 4, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateEmbyWebEnhancements(db); err != nil {
		t.Fatal(err)
	}
	var gateway models.EmbyProxyGateway
	if err := db.Table("emby_proxy_gateways").First(&gateway, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !gateway.ExternalPlayerEnabled || !gateway.FanartEnabled || gateway.PolicyRevision != 4 {
		t.Fatalf("migrated gateway=%+v", gateway)
	}
}
