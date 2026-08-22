package services

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	pluginrepository "github.com/yuanjing-hash/ohmycine/server/internal/plugins/repository"
)

type fakePluginAssets struct {
	manifest []byte
	archive  []byte
}

func (assets *fakePluginAssets) FetchManifest(context.Context, contract.GitHubRepository, string) ([]byte, error) {
	return append([]byte(nil), assets.manifest...), nil
}

func (assets *fakePluginAssets) FetchPackage(context.Context, contract.GitHubRepository, string) ([]byte, error) {
	return append([]byte(nil), assets.archive...), nil
}

type fakePluginRuntime struct {
	starts    int
	stops     int
	startFail bool
	stopFail  bool
}

func (*fakePluginRuntime) Validate(context.Context, string) error { return nil }
func (runtime *fakePluginRuntime) Start(context.Context, string, string, uint64) error {
	if runtime.startFail {
		return errors.New("start failed")
	}
	runtime.starts++
	return nil
}
func (*fakePluginRuntime) Invoke(context.Context, string, string, []byte) ([]byte, error) {
	return nil, errors.New("not implemented by lifecycle fake")
}
func (runtime *fakePluginRuntime) Stop(string) error {
	if runtime.stopFail {
		return errors.New("stop failed")
	}
	runtime.stops++
	return nil
}
func (*fakePluginRuntime) Close(context.Context) error { return nil }

func TestPluginInstallationLifecycleAndPermissionConfirmation(t *testing.T) {
	service, actor, fetcher := pluginRepositoryFixture(t)
	runtime := &fakePluginRuntime{}
	service.runtime = runtime
	service.pluginRoot = filepath.Join(t.TempDir(), "plugins")
	assets := &fakePluginAssets{}
	service.assets = assets

	repository, err := service.Create(actor, CreatePluginRepositoryInput{Name: "官方", GitHubURL: "https://github.com/ohmycine/plugins", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	source := contract.GitHubRepository{Owner: "ohmycine", Name: "plugins"}
	setPluginRelease(t, service, fetcher, repository.ID, source, "0.1.0", []contract.Permission{{Kind: contract.PermissionPrivateStorage, MaxBytes: int64Pointer(4096)}}, assets, "a")
	preview, err := service.PrepareInstall(context.Background(), actor, "org.ohmycine.fixture", repository.ID, "", RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Operation != "install" || len(preview.PermissionDiff.Added) != 1 || preview.InstallationRevision != 0 {
		t.Fatalf("preview=%+v", preview)
	}
	if _, err := service.ConfirmInstall(context.Background(), actor, preview.PluginID, "update", preview.ID, preview.PermissionFingerprint, preview.InstallationRevision, RequestContext{}); ErrorCode(err) != CodePluginRevisionConflict {
		t.Fatalf("cross-route confirmation error=%v code=%s", err, ErrorCode(err))
	}
	if _, err := service.ConfirmInstall(context.Background(), actor, preview.PluginID, preview.Operation, preview.ID, strings.Repeat("0", 64), preview.InstallationRevision, RequestContext{}); ErrorCode(err) != CodePluginPermissionChanged {
		t.Fatalf("wrong fingerprint error=%v code=%s", err, ErrorCode(err))
	}
	installed, err := service.ConfirmInstall(context.Background(), actor, preview.PluginID, preview.Operation, preview.ID, preview.PermissionFingerprint, preview.InstallationRevision, RequestContext{})
	if err != nil || installed.Status != models.PluginInstallationDisabled || installed.Version != "0.1.0" {
		t.Fatalf("installed=%+v err=%v", installed, err)
	}
	installed, err = service.SetPluginEnabled(context.Background(), actor, installed.ID, true, installed.Revision, RequestContext{})
	if err != nil || installed.Status != models.PluginInstallationEnabled || runtime.starts != 1 {
		t.Fatalf("enabled=%+v runtime=%+v err=%v", installed, runtime, err)
	}

	setPluginRelease(t, service, fetcher, repository.ID, source, "0.2.0", []contract.Permission{{Kind: contract.PermissionPrivateStorage, MaxBytes: int64Pointer(8192)}, {Kind: contract.PermissionDownloadPlan}}, assets, "b")
	preview, err = service.PrepareInstall(context.Background(), actor, installed.ID, repository.ID, "0.2.0", RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Operation != "update" || len(preview.PermissionDiff.Added) != 2 || len(preview.PermissionDiff.Removed) != 1 || preview.InstallationRevision != installed.Revision {
		t.Fatalf("update preview=%+v", preview)
	}
	installed, err = service.ConfirmInstall(context.Background(), actor, installed.ID, preview.Operation, preview.ID, preview.PermissionFingerprint, preview.InstallationRevision, RequestContext{})
	if err != nil || installed.Version != "0.2.0" || installed.PreviousVersion != "0.1.0" || runtime.starts != 2 {
		t.Fatalf("updated=%+v runtime=%+v err=%v", installed, runtime, err)
	}
	installed, err = service.RollbackPlugin(context.Background(), actor, installed.ID, installed.Revision, RequestContext{})
	if err != nil || installed.Version != "0.1.0" || installed.PreviousVersion != "0.2.0" || runtime.starts != 3 {
		t.Fatalf("rolled back=%+v runtime=%+v err=%v", installed, runtime, err)
	}
	if err := service.UninstallPlugin(actor, installed.ID, installed.Revision, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	var packageCount, installationCount int64
	_ = service.db.Model(&models.PluginPackage{}).Count(&packageCount).Error
	_ = service.db.Model(&models.PluginInstallation{}).Count(&installationCount).Error
	if packageCount != 0 || installationCount != 0 || runtime.stops == 0 {
		t.Fatalf("packages=%d installations=%d runtime=%+v", packageCount, installationCount, runtime)
	}
	entries, err := os.ReadDir(filepath.Join(service.pluginRoot, "packages"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("package directory entries=%v err=%v", entries, err)
	}
}

func TestPluginConfirmationRejectsPackageContentTampering(t *testing.T) {
	service, actor, fetcher := pluginRepositoryFixture(t)
	service.runtime = &fakePluginRuntime{}
	service.pluginRoot = filepath.Join(t.TempDir(), "plugins")
	assets := &fakePluginAssets{}
	service.assets = assets
	repository, _ := service.Create(actor, CreatePluginRepositoryInput{GitHubURL: "https://github.com/ohmycine/plugins", Enabled: true}, RequestContext{})
	source := contract.GitHubRepository{Owner: "ohmycine", Name: "plugins"}
	setPluginRelease(t, service, fetcher, repository.ID, source, "0.1.0", nil, assets, "a")
	preview, err := service.PrepareInstall(context.Background(), actor, "org.ohmycine.fixture", repository.ID, "", RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var previewRecord models.PluginInstallPreview
	if err := service.db.First(&previewRecord, "id = ?", preview.ID).Error; err != nil {
		t.Fatal(err)
	}
	var pluginPackage models.PluginPackage
	if err := service.db.First(&pluginPackage, previewRecord.PluginPackageID).Error; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginPackage.PackagePath, "plugin.wasm"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmInstall(context.Background(), actor, preview.PluginID, preview.Operation, preview.ID, preview.PermissionFingerprint, preview.InstallationRevision, RequestContext{}); ErrorCode(err) != CodePluginPackageInvalid {
		t.Fatalf("tampered confirmation error=%v code=%s", err, ErrorCode(err))
	}
}

func TestPluginUpdateStartFailurePreservesOldVersion(t *testing.T) {
	service, actor, fetcher := pluginRepositoryFixture(t)
	runtime := &fakePluginRuntime{}
	service.runtime = runtime
	service.pluginRoot = filepath.Join(t.TempDir(), "plugins")
	assets := &fakePluginAssets{}
	service.assets = assets
	repository, _ := service.Create(actor, CreatePluginRepositoryInput{GitHubURL: "https://github.com/ohmycine/plugins", Enabled: true}, RequestContext{})
	source := contract.GitHubRepository{Owner: "ohmycine", Name: "plugins"}
	setPluginRelease(t, service, fetcher, repository.ID, source, "0.1.0", nil, assets, "a")
	preview, _ := service.PrepareInstall(context.Background(), actor, "org.ohmycine.fixture", repository.ID, "", RequestContext{})
	installed, _ := service.ConfirmInstall(context.Background(), actor, preview.PluginID, preview.Operation, preview.ID, preview.PermissionFingerprint, 0, RequestContext{})
	installed, _ = service.SetPluginEnabled(context.Background(), actor, installed.ID, true, installed.Revision, RequestContext{})
	setPluginRelease(t, service, fetcher, repository.ID, source, "0.2.0", nil, assets, "b")
	preview, _ = service.PrepareInstall(context.Background(), actor, installed.ID, repository.ID, "0.2.0", RequestContext{})
	runtime.startFail = true
	if _, err := service.ConfirmInstall(context.Background(), actor, installed.ID, preview.Operation, preview.ID, preview.PermissionFingerprint, preview.InstallationRevision, RequestContext{}); ErrorCode(err) != CodePluginRuntimeStartFailed {
		t.Fatalf("error=%v code=%s", err, ErrorCode(err))
	}
	current, err := service.installedByID(installed.ID)
	if err != nil || current.Version != "0.1.0" || current.Status != models.PluginInstallationEnabled {
		t.Fatalf("current=%+v err=%v", current, err)
	}
}

func TestPluginStopFailureMarksInstallationFailed(t *testing.T) {
	service, actor, fetcher := pluginRepositoryFixture(t)
	runtime := &fakePluginRuntime{}
	service.runtime = runtime
	service.pluginRoot = filepath.Join(t.TempDir(), "plugins")
	assets := &fakePluginAssets{}
	service.assets = assets
	repository, _ := service.Create(actor, CreatePluginRepositoryInput{GitHubURL: "https://github.com/ohmycine/plugins", Enabled: true}, RequestContext{})
	source := contract.GitHubRepository{Owner: "ohmycine", Name: "plugins"}
	setPluginRelease(t, service, fetcher, repository.ID, source, "0.1.0", nil, assets, "a")
	preview, _ := service.PrepareInstall(context.Background(), actor, "org.ohmycine.fixture", repository.ID, "", RequestContext{})
	installed, _ := service.ConfirmInstall(context.Background(), actor, preview.PluginID, preview.Operation, preview.ID, preview.PermissionFingerprint, 0, RequestContext{})
	installed, _ = service.SetPluginEnabled(context.Background(), actor, installed.ID, true, installed.Revision, RequestContext{})
	runtime.stopFail = true
	if _, err := service.SetPluginEnabled(context.Background(), actor, installed.ID, false, installed.Revision, RequestContext{}); ErrorCode(err) != CodePluginRuntimeStopFailed {
		t.Fatalf("error=%v code=%s", err, ErrorCode(err))
	}
	current, err := service.installedByID(installed.ID)
	if err != nil || current.Status != models.PluginInstallationFailed || current.Revision != installed.Revision+1 || current.LastRuntimeErrorCode == "" {
		t.Fatalf("current=%+v err=%v", current, err)
	}
}

func setPluginRelease(t *testing.T, service *PluginRepositoryService, fetcher *fakePluginRegistryFetcher, repositoryID uint, source contract.GitHubRepository, version string, permissions []contract.Permission, assets *fakePluginAssets, marker string) {
	t.Helper()
	assets.archive = pluginArchive(t, []byte("wasm-"+marker))
	digest := sha256.Sum256(assets.archive)
	digestHex := hex.EncodeToString(digest[:])
	manifest := contract.Manifest{SchemaVersion: 1, ID: "org.ohmycine.fixture", Name: "Fixture", Description: "Fixture plugin", Version: version, APIVersion: contract.APIVersion, MinServerVersion: "0.1.0", Runtime: "wasm", Entry: "plugin.wasm", Capabilities: []contract.Capability{contract.CapabilitySiteFeed}, Permissions: permissions, ConfigSchema: json.RawMessage(`{"type":"object"}`), Author: "OhMyCine", License: "MIT", Source: source.CanonicalURL(), PackageSHA256: digestHex}
	assets.manifest, _ = json.Marshal(manifest)
	base := source.CanonicalURL() + "/releases/download/v" + version + "/"
	registry := contract.Registry{SchemaVersion: 1, Repository: contract.RepositoryInfo{ID: "org.ohmycine.repository", Name: "Repository", Homepage: source.CanonicalURL(), UpdatedAt: timeNow()}, Plugins: []contract.RegistryEntry{{ID: manifest.ID, Name: manifest.Name, Description: manifest.Description, Version: version, Channel: "stable", Categories: []string{"online-media"}, ManifestURL: base + "plugin.json", PackageURL: base + "plugin.omcp", PackageSHA256: digestHex, MinServerVersion: "0.1.0"}}}
	fetcher.snapshots[strings.ToLower(source.Owner+"/"+source.Name)] = pluginrepository.Snapshot{CommitSHA: strings.Repeat(marker, 40), Registry: registry}
	var repository models.PluginRepository
	if err := service.db.First(&repository, repositoryID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), actorWithPluginPermissions(t, service), repositoryID, RequestContext{}); err != nil {
		t.Fatal(err)
	}
}

func actorWithPluginPermissions(t *testing.T, service *PluginRepositoryService) Actor {
	t.Helper()
	var user models.User
	if err := service.db.First(&user).Error; err != nil {
		t.Fatal(err)
	}
	return Actor{User: user, Permissions: map[string]struct{}{"plugins.read": {}, "plugins.install": {}}}
}

func pluginArchive(t *testing.T, wasm []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	file, err := writer.Create("plugin.wasm")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write(wasm)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func int64Pointer(value int64) *int64 { return &value }

func timeNow() time.Time { return time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC) }
