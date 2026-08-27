package services

import (
	"context"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/downloader/pan115offline"
)

func TestPan115OfflineDownloaderReusesConnectionStorage(t *testing.T) {
	driver := &fakeCloudDriver{
		nativeOffline:   true,
		createDirectory: true,
		items: map[string]cloud.Item{
			"offline-root": {ID: "offline-root", ParentID: "0", Name: "离线下载", IsDir: true},
			"movies":       {ID: "movies", ParentID: "offline-root", Name: "电影", IsDir: true},
		},
		children: map[string][]cloud.Item{"offline-root": {{ID: "movies", ParentID: "offline-root", Name: "电影", IsDir: true}}},
	}
	db, store, connections, actor := newConnectionTestService(t, driver)
	actor.Permissions[authz.PermissionDownloadersCreate] = struct{}{}
	actor.Permissions[authz.PermissionDownloadersUpdate] = struct{}{}
	actor.Permissions[authz.PermissionDownloadersTest] = struct{}{}
	connection, err := connections.Create(actor, ConnectionInput{Name: "115 account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "115 暂存", NameNormalized: "115 暂存", Type: models.StorageTypePan115, RootPath: "offline-root", RootDisplayPath: "/离线下载", RootPathNormalized: "pan115:test:offline-root", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{"native_offline_download":true}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	registry := downloader.NewRegistry()
	if err := registry.Register(models.DownloaderTypePan115Offline, pan115offline.Capabilities, pan115offline.New); err != nil {
		t.Fatal(err)
	}
	service := NewDownloaderService(db, NewAuditService(db), store, registry)
	service.SetConnectionService(connections)
	if _, err := service.CreateContext(context.Background(), actor, DownloaderInput{Name: "缺少目录", Type: models.DownloaderTypePan115Offline, StorageID: &storage.ID, Enabled: true}, RequestContext{}); ErrorCode(err) != CodeDownloaderStorageRequired {
		t.Fatalf("missing selection code=%q err=%v", ErrorCode(err), err)
	}
	directories := NewProviderDirectoryService(connections, store)
	listing, err := directories.BrowseStorage(context.Background(), actor, storage.ID, "", "")
	if err != nil || len(listing.Items) != 1 {
		t.Fatalf("browse target listing=%+v err=%v", listing, err)
	}
	created, err := service.CreateContext(context.Background(), actor, DownloaderInput{Name: "115 离线", Type: models.DownloaderTypePan115Offline, StorageID: &storage.ID, ProviderDirectoryToken: listing.Items[0].SelectionToken, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if created.StorageID == nil || *created.StorageID != storage.ID || created.StorageName != storage.Name || created.ProviderDirectoryPath != "/电影" || created.UsernameConfigured || created.PasswordConfigured || created.BaseURL != "" || !created.Capabilities.NativeOffline {
		t.Fatalf("unexpected downloader summary: %#v", created)
	}
	var persisted models.Downloader
	if err := db.First(&persisted, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ProviderDirectoryID != "movies" || persisted.ProviderDirectoryPath != "/电影" {
		t.Fatalf("unexpected persisted provider directory: %#v", persisted)
	}
	if _, err := service.Test(context.Background(), actor, created.ID, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	client, err := service.clientFor(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}, Tag: "omc-test"}); err != nil {
		t.Fatal(err)
	}
	if driver.offlineDirectory != "fake-dir-1" {
		t.Fatalf("submitted directory=%q, want omc task directory", driver.offlineDirectory)
	}
	rootToken := listing.CurrentSelectionToken
	updated, err := service.UpdateContext(context.Background(), actor, created.ID, UpdateDownloaderInput{StorageID: &storage.ID, ProviderDirectoryToken: &rootToken}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProviderDirectoryPath != "/" {
		t.Fatalf("updated path=%q, want /", updated.ProviderDirectoryPath)
	}
	if err := db.First(&persisted, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ProviderDirectoryID != "offline-root" {
		t.Fatalf("updated directory id=%q, want offline-root", persisted.ProviderDirectoryID)
	}
}
