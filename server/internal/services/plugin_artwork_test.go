package services

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/packagefs"
)

func TestPluginLibraryArtworkReadsOnlyActiveVerifiedRaster(t *testing.T) {
	service, _, _ := pluginRepositoryFixture(t)
	root := filepath.Join(t.TempDir(), "plugins")
	service.pluginRoot = root
	digest := strings.Repeat("7", 64)
	packagePath := filepath.Join(root, "packages", digest)
	if err := os.MkdirAll(filepath.Join(packagePath, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packagePath, "plugin.wasm"), []byte("wasm"), 0o600); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	if err := os.WriteFile(filepath.Join(packagePath, "assets", "library.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := contract.Manifest{
		SchemaVersion: 1, ID: "org.ohmycine.artwork-test", Name: "Artwork", Description: "fixture", Version: "0.1.0",
		APIVersion: "1", MinServerVersion: "0.1.0", Runtime: "wasm", Entry: "plugin.wasm", LibraryArtwork: "assets/library.png",
		ConfigSchema: json.RawMessage(`{}`), Author: "test", License: "MIT", Source: "https://github.com/example/plugin", PackageSHA256: digest,
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	treeSHA256, err := packagefs.ComputeManagedTreeSHA256(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pluginPackage := models.PluginPackage{PluginID: manifest.ID, Version: manifest.Version, RepositoryOwner: "example", RepositoryRepo: "plugin", RegistryCommit: strings.Repeat("a", 40), RegistryEntryJSON: `{}`, ManifestURL: "https://github.com/example/plugin/manifest.json", PackageURL: "https://github.com/example/plugin/plugin.omcp", PackageSHA256: digest, ExtractedTreeSHA256: treeSHA256, ManifestJSON: string(manifestJSON), PackagePath: packagePath, VerifiedAt: now, CreatedAt: now}
	if err := service.db.Create(&pluginPackage).Error; err != nil {
		t.Fatal(err)
	}
	installation := models.PluginInstallation{PluginID: manifest.ID, ActivePackageID: pluginPackage.ID, Status: models.PluginInstallationEnabled, Revision: 1, RuntimeGeneration: 1, InstalledAt: now, UpdatedAt: now, EnabledAt: &now}
	if err := service.db.Create(&installation).Error; err != nil {
		t.Fatal(err)
	}
	artwork, err := service.OpenLibraryArtwork(digest)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(artwork.File)
	_ = artwork.File.Close()
	if err != nil || string(data) != string(png) || artwork.ContentType != "image/png" {
		t.Fatalf("content=%x type=%q err=%v", data, artwork.ContentType, err)
	}
	if _, err := service.OpenLibraryArtwork(strings.Repeat("6", 64)); ErrorCode(err) != CodeNotFound {
		t.Fatalf("unknown digest code=%q err=%v", ErrorCode(err), err)
	}
	if err := service.db.Model(&models.PluginInstallation{}).Where("plugin_id = ?", manifest.ID).Update("status", models.PluginInstallationDisabled).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenLibraryArtwork(digest); ErrorCode(err) != CodeNotFound {
		t.Fatalf("disabled digest code=%q err=%v", ErrorCode(err), err)
	}
}
