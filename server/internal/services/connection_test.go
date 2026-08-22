package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud/pan115"
	"gorm.io/gorm"
)

const testPan115Cookie = "UID=100_A1; CID=cid-value; SEID=seid-value; KID=kid-value"

type fakeCloudDriver struct {
	account          cloud.Account
	err              error
	items            map[string]cloud.Item
	children         map[string][]cloud.Item
	nativeOffline    bool
	signedProxy      bool
	smallFileUpload  bool
	offlineTask      cloud.OfflineTask
	offlineDirectory string
	probeCalls       int
	statCalls        int
	directURLCalls   atomic.Int32
	directURL        string
	directHeaders    http.Header
	echoDirectUA     bool
	createDirectory  bool
	copyItems        bool
	recycleItems     bool
	nextItem         int
	recycled         []string
	purged           []string
	directFileIDs    []string
}

func (f *fakeCloudDriver) Provider() string { return cloud.ProviderPan115 }
func (f *fakeCloudDriver) Capabilities() cloud.Capabilities {
	return cloud.Capabilities{NetworkDrive: true, DirectoryList: true, TemporaryDirectURL: true, SignedProxy: f.signedProxy, SmallFileUpload: f.smallFileUpload, NativeOfflineDownload: f.nativeOffline, CreateDirectory: f.createDirectory, Copy: f.copyItems, Recycle: f.recycleItems}
}
func (f *fakeCloudDriver) Probe(context.Context) (cloud.Account, error) {
	f.probeCalls++
	return f.account, f.err
}
func (f *fakeCloudDriver) List(_ context.Context, parentID string, request cloud.PageRequest) (cloud.Page, error) {
	items := f.children[parentID]
	start := int(request.Offset)
	if start >= len(items) {
		return cloud.Page{Offset: request.Offset}, nil
	}
	end := start + int(request.Limit)
	if end > len(items) {
		end = len(items)
	}
	return cloud.Page{Items: items[start:end], Offset: request.Offset, HasMore: end < len(items)}, nil
}
func (f *fakeCloudDriver) Stat(_ context.Context, id string) (cloud.Item, error) {
	f.statCalls++
	if id == "0" {
		return cloud.Item{ID: "0", Name: "115 网盘", IsDir: true}, nil
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return cloud.Item{}, cloud.Error(cloud.CodeNotFound, false, nil)
}
func (f *fakeCloudDriver) DirectURL(_ context.Context, request cloud.DirectURLRequest) (cloud.TemporaryURL, error) {
	f.directURLCalls.Add(1)
	f.directFileIDs = append(f.directFileIDs, request.FileID)
	value := f.directURL
	if value == "" {
		value = "https://example.invalid/file"
	}
	headers := f.directHeaders.Clone()
	if f.echoDirectUA && strings.TrimSpace(request.UserAgent) != "" {
		if headers == nil {
			headers = make(http.Header)
		}
		headers.Set("User-Agent", strings.TrimSpace(request.UserAgent))
	}
	return cloud.TemporaryURL{URL: value, Headers: headers, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (f *fakeCloudDriver) CreateDirectory(_ context.Context, parentID, name string) (cloud.Item, error) {
	if !f.createDirectory {
		return cloud.Item{}, cloud.Error(cloud.CodeUnavailable, false, nil)
	}
	f.nextItem++
	item := cloud.Item{ID: "fake-dir-" + strconv.Itoa(f.nextItem), ParentID: parentID, Name: name, IsDir: true}
	if f.items == nil {
		f.items = map[string]cloud.Item{}
	}
	if f.children == nil {
		f.children = map[string][]cloud.Item{}
	}
	f.items[item.ID] = item
	f.children[parentID] = append(f.children[parentID], item)
	return item, nil
}
func (f *fakeCloudDriver) Move(context.Context, string, string) error {
	return cloud.Error(cloud.CodeUnavailable, false, nil)
}
func (f *fakeCloudDriver) Copy(_ context.Context, itemID, parentID string) error {
	if !f.copyItems {
		return cloud.Error(cloud.CodeUnavailable, false, nil)
	}
	source, ok := f.items[itemID]
	if !ok {
		return cloud.Error(cloud.CodeNotFound, false, nil)
	}
	f.nextItem++
	copyItem := source
	copyItem.ID, copyItem.ParentID, copyItem.PickCode = "fake-copy-"+strconv.Itoa(f.nextItem), parentID, "copy-pickcode"
	f.items[copyItem.ID] = copyItem
	f.children[parentID] = append(f.children[parentID], copyItem)
	return nil
}
func (f *fakeCloudDriver) Rename(context.Context, string, string) error {
	return cloud.Error(cloud.CodeUnavailable, false, nil)
}
func (f *fakeCloudDriver) Recycle(_ context.Context, itemID string) error {
	if !f.recycleItems {
		return cloud.Error(cloud.CodeUnavailable, false, nil)
	}
	item, ok := f.items[itemID]
	if !ok {
		return cloud.Error(cloud.CodeNotFound, false, nil)
	}
	delete(f.items, itemID)
	delete(f.children, itemID)
	children := f.children[item.ParentID]
	for index := range children {
		if children[index].ID == itemID {
			f.children[item.ParentID] = append(children[:index], children[index+1:]...)
			break
		}
	}
	f.recycled = append(f.recycled, itemID)
	return nil
}
func (f *fakeCloudDriver) PurgeRecycle(_ context.Context, itemID string) error {
	if strings.TrimSpace(itemID) == "" {
		return cloud.Error(cloud.CodeResponseInvalid, false, nil)
	}
	f.purged = append(f.purged, itemID)
	return nil
}
func (f *fakeCloudDriver) SubmitOffline(_ context.Context, _ string, directoryID string) (cloud.OfflineTask, error) {
	f.offlineDirectory = directoryID
	return f.offlineTask, f.err
}
func (f *fakeCloudDriver) GetOffline(context.Context, string) (cloud.OfflineTask, error) {
	return f.offlineTask, f.err
}
func (f *fakeCloudDriver) CancelOffline(context.Context, string, bool) error { return f.err }

func newConnectionTestService(t *testing.T, driver *fakeCloudDriver) (*gorm.DB, *credential.Store, *ConnectionService, Actor) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "connections.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	store, err := credential.Open(filepath.Join(t.TempDir(), "credential.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	registry := cloud.NewRegistry()
	if err := registry.Register(cloud.ProviderPan115, func(config cloud.Config) (cloud.Driver, error) {
		if _, err := pan115.ParseCookie(config.Cookie); err != nil {
			return nil, cloud.Error(cloud.CodeCookieInvalid, false, err)
		}
		return driver, nil
	}); err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "connection-test", UsernameNormalized: "connection-test", DisplayName: "Connection Test", PasswordHash: "test", Status: models.UserStatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	permissions := map[string]struct{}{
		authz.PermissionConnectionsRead: {}, authz.PermissionConnectionsCreate: {}, authz.PermissionConnectionsUpdate: {},
		authz.PermissionConnectionsDelete: {}, authz.PermissionConnectionsTest: {},
		authz.PermissionStoragesRead: {}, authz.PermissionStoragesBrowse: {}, authz.PermissionStoragesCreate: {}, authz.PermissionStoragesUpdate: {}, authz.PermissionStoragesTest: {},
	}
	return db, store, NewConnectionService(db, NewAuditService(db), store, registry, zerolog.Nop()), Actor{User: user, Permissions: permissions}
}

func TestConnectionCredentialIsEncryptedRedactedAndPreserved(t *testing.T) {
	db, store, service, actor := newConnectionTestService(t, &fakeCloudDriver{})
	created, err := service.Create(actor, ConnectionInput{Name: "我的 115", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, RecyclePassword: "recycle-safe-code", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !created.CredentialConfigured {
		t.Fatal("credential should be reported as configured")
	}
	if !created.RecyclePasswordConfigured {
		t.Fatal("recycle password should be reported as configured")
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Cookie") || strings.Contains(string(encoded), "cookie") || strings.Contains(string(encoded), "SEID") || strings.Contains(string(encoded), "recycle-safe-code") {
		t.Fatalf("connection DTO exposed credential material: %s", encoded)
	}
	var record models.Connection
	if err := db.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.CredentialCiphertext == "" || strings.Contains(record.CredentialCiphertext, "SEID") || record.CredentialCiphertext == testPan115Cookie {
		t.Fatal("credential was not stored as opaque ciphertext")
	}
	if record.RecycleCredentialCiphertext == "" || strings.Contains(record.RecycleCredentialCiphertext, "recycle-safe-code") {
		t.Fatal("recycle password was not stored as opaque ciphertext")
	}
	before := record.CredentialCiphertext
	recycleBefore := record.RecycleCredentialCiphertext
	empty := "   "
	updated, err := service.Update(actor, created.ID, UpdateConnectionInput{Cookie: &empty, Revision: created.Revision}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.CredentialCiphertext != before || record.RecycleCredentialCiphertext != recycleBefore || updated.Revision != created.Revision+1 {
		t.Fatal("blank credential update did not preserve the existing envelope")
	}
	plaintext, err := store.Decrypt(connectionPurpose(created.ID), record.CredentialCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "UID=100_A1; CID=cid-value; SEID=seid-value; KID=kid-value" {
		t.Fatalf("unexpected normalized credential: %q", plaintext)
	}
	recyclePlaintext, err := store.Decrypt(connectionRecyclePurpose(created.ID), record.RecycleCredentialCiphertext)
	if err != nil || recyclePlaintext != "recycle-safe-code" {
		t.Fatalf("unexpected recycle credential: %q err=%v", recyclePlaintext, err)
	}
	var audits []models.AuditLog
	if err := db.Where("target_type = ?", "connection").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	for _, audit := range audits {
		if strings.Contains(audit.Metadata, "SEID") || strings.Contains(audit.Metadata, "cookie") || strings.Contains(audit.Metadata, "cipher") {
			t.Fatalf("audit exposed credential material: %s", audit.Metadata)
		}
	}
}

func TestEmbyManagementSummaryUsesEncryptedCredentialAndReturnsSafeAggregates(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "summary-key" {
			http.Error(w, "missing credential", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/System/Info":
			_, _ = w.Write([]byte(`{"Id":"emby-id","ServerName":"家庭 Emby","Version":"4.9.0"}`))
		case "/Library/VirtualFolders":
			_, _ = w.Write([]byte(`[{"Name":"private-a"},{"Name":"private-b"}]`))
		case "/Items/Counts":
			_, _ = w.Write([]byte(`{"MovieCount":20,"SeriesCount":5,"EpisodeCount":120}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	db, _, service, actor := newConnectionTestService(t, &fakeCloudDriver{})
	created, err := service.Create(actor, ConnectionInput{Name: "播放器", Provider: models.ConnectionProviderEmby, Endpoint: upstream.URL, APIKey: "summary-key", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := service.EmbyManagementSummary(context.Background(), actor, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Status != "ready" || summary.Version != "4.9.0" || summary.LibraryCount == nil || *summary.LibraryCount != 2 || summary.MovieCount == nil || *summary.MovieCount != 20 || summary.SeriesCount == nil || *summary.SeriesCount != 5 || summary.EpisodeCount == nil || *summary.EpisodeCount != 120 {
		t.Fatalf("unexpected Emby management summary: %+v", summary)
	}
	encoded, _ := json.Marshal(summary)
	if strings.Contains(string(encoded), "summary-key") || strings.Contains(string(encoded), "private-a") || strings.Contains(string(encoded), upstream.URL) {
		t.Fatalf("management DTO exposed privileged details: %s", encoded)
	}
	var record models.Connection
	if err := db.First(&record, created.ID).Error; err != nil || record.CredentialCiphertext == "summary-key" || strings.Contains(record.CredentialCiphertext, "summary-key") {
		t.Fatal("Emby management credential was not kept encrypted")
	}
}

func TestConnectionListFiltersProvidersBeforeBuildingPublicDTOs(t *testing.T) {
	_, _, service, actor := newConnectionTestService(t, &fakeCloudDriver{})
	if _, err := service.Create(actor, ConnectionInput{Name: "Cloud", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(actor, ConnectionInput{Name: "Player", Provider: models.ConnectionProviderEmby, Endpoint: "http://127.0.0.1:8096", APIKey: "summary-key", Enabled: true}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	cloudItems, err := service.List(actor, cloud.ProviderPan115)
	if err != nil {
		t.Fatal(err)
	}
	if len(cloudItems) != 1 || cloudItems[0].Provider != cloud.ProviderPan115 {
		t.Fatalf("cloud filter returned %+v", cloudItems)
	}
	playerItems, err := service.List(actor, models.ConnectionProviderEmby)
	if err != nil {
		t.Fatal(err)
	}
	if len(playerItems) != 1 || playerItems[0].Provider != models.ConnectionProviderEmby {
		t.Fatalf("player filter returned %+v", playerItems)
	}
	if _, err := service.List(actor, "future-provider"); ErrorCode(err) != CodeConnectionProviderUnsupported {
		t.Fatalf("unsupported filter code=%q err=%v", ErrorCode(err), err)
	}
}

func TestConnectionValidationConflictsAndReferences(t *testing.T) {
	db, _, service, actor := newConnectionTestService(t, &fakeCloudDriver{})
	if _, err := service.Create(actor, ConnectionInput{Name: "Invalid", Provider: cloud.ProviderPan115, Cookie: "UID=x", Enabled: true}, RequestContext{}); ErrorCode(err) != CodePan115CookieInvalid {
		t.Fatalf("invalid cookie code=%q err=%v", ErrorCode(err), err)
	}
	created, err := service.Create(actor, ConnectionInput{Name: "Cloud", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(actor, ConnectionInput{Name: " cloud ", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{}); ErrorCode(err) != CodeConnectionNameConflict {
		t.Fatalf("duplicate name code=%q err=%v", ErrorCode(err), err)
	}
	name := "Changed"
	if _, err := service.Update(actor, created.ID, UpdateConnectionInput{Name: &name, Revision: created.Revision + 99}, RequestContext{}); ErrorCode(err) != CodeConflict {
		t.Fatalf("revision conflict code=%q err=%v", ErrorCode(err), err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Reference", NameNormalized: "reference", Type: models.StorageTypeLocal, RootPath: t.TempDir(), RootDisplayPath: "Reference", RootPathNormalized: strings.ToLower(t.TempDir()), ConnectionID: &created.ID, Enabled: true, Capabilities: "{}", CreatedAt: now, UpdatedAt: now}
	storage.RootPathNormalized = strings.ToLower(storage.RootPath)
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(actor, created.ID, RequestContext{}); ErrorCode(err) != CodeConnectionInUse {
		t.Fatalf("in-use delete code=%q err=%v", ErrorCode(err), err)
	}
}

func TestConnectionProbePersistsOnlySafeSummary(t *testing.T) {
	used, total := uint64(12), uint64(34)
	driver := &fakeCloudDriver{account: cloud.Account{ID: "account-1", Name: "测试账号", VIP: true, UsedBytes: &used, TotalBytes: &total}}
	db, _, service, actor := newConnectionTestService(t, driver)
	created, err := service.Create(actor, ConnectionInput{Name: "Cloud", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Test(context.Background(), actor, created.ID, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Health.Status != "online" || result.Account.ID != "account-1" || result.Account.Name != "测试账号" || !result.Account.VIP {
		t.Fatalf("unexpected probe summary: %+v", result)
	}
	var record models.Connection
	if err := db.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastHealthStatus != "online" || record.QuotaUsedBytes == nil || *record.QuotaUsedBytes != used {
		t.Fatalf("safe health fields were not persisted: %+v", record)
	}
}

func TestPlayerEmbyInstancesUseMediaPermissionAndNormalizedSystemID(t *testing.T) {
	db, _, service, actor := newConnectionTestService(t, &fakeCloudDriver{})
	now := time.Now().UTC()
	valid := models.Connection{
		Name: "家庭 Emby", NameNormalized: "家庭 emby", Provider: models.ConnectionProviderEmby,
		Endpoint: "https://private.example.test", CredentialCiphertext: "encrypted-secret", Enabled: true,
		AccountID: "  SYSTEM-ID  ", LastHealthStatus: "online", Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	missing := models.Connection{
		Name: "待检测 Emby", NameNormalized: "待检测 emby", Provider: models.ConnectionProviderEmby,
		Endpoint: "https://other.example.test", CredentialCiphertext: "other-secret", Enabled: true,
		AccountID: "   ", LastHealthStatus: "unknown", Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&missing).Error; err != nil {
		t.Fatal(err)
	}
	mediaActor := Actor{User: actor.User, Permissions: map[string]struct{}{authz.PermissionMediaLibrariesRead: {}}}
	instances, err := service.PlayerEmbyInstances(mediaActor)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Name != valid.Name || instances[0].HealthStatus != "online" || instances[0].InstanceFingerprint != EmbyInstanceFingerprint("system-id") {
		t.Fatalf("unexpected safe Emby projection: %+v", instances)
	}
	connectionOnlyActor := Actor{User: actor.User, Permissions: map[string]struct{}{authz.PermissionConnectionsRead: {}}}
	instances, err = service.PlayerEmbyInstances(connectionOnlyActor)
	if err != nil || len(instances) != 0 {
		t.Fatalf("connection-only actor received Player Emby projection: instances=%+v err=%v", instances, err)
	}
}
