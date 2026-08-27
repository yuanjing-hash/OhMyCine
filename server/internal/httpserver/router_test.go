package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/config"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/handlers"
	"github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud/pan115"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	sitepkg "github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"gorm.io/gorm"
)

type testClient struct {
	router      http.Handler
	cookie      *http.Cookie
	csrf        string
	queue       *services.QueueService
	db          *gorm.DB
	connections *services.ConnectionService
	signedProxy *services.SignedProxyService
	embyGateway *services.EmbyGatewayService
	changes     *services.MediaChangeService
	sites       *services.SiteService
	lastHeader  http.Header
}
type testEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func TestBuiltInLibraryArtworkIsPublicInertRaster(t *testing.T) {
	client := newTestClient(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/assets/library-covers/library-local.png", nil)
	response := httptest.NewRecorder()
	client.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || response.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(response.Header().Get("Cache-Control"), "public") {
		t.Fatalf("status=%d type=%q cache=%q nosniff=%q", response.Code, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"), response.Header().Get("X-Content-Type-Options"))
	}
	if body := response.Body.Bytes(); len(body) < 8 || !bytes.Equal(body[:8], []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}) {
		t.Fatalf("invalid embedded PNG: %x", body)
	}
	missing := httptest.NewRecorder()
	client.router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/v1/assets/library-covers/../../secret", nil))
	if missing.Code == http.StatusOK {
		t.Fatal("unsafe artwork path was served")
	}
}

type routerCloudDriver struct{}

type routerSiteAdapter struct{}

func (routerSiteAdapter) Kind() string { return "pttime" }
func (routerSiteAdapter) Test(context.Context, sitepkg.Config) (sitepkg.Health, error) {
	return sitepkg.Health{Status: "online", Username: "router-user"}, nil
}
func (routerSiteAdapter) Search(_ context.Context, _ sitepkg.Config, query sitepkg.Query) (sitepkg.Page, error) {
	seeders := 8
	return sitepkg.Page{Page: query.Page, Items: []sitepkg.Result{{TorrentID: "88", Title: "Seven.Samurai.1954.1080p", Seeders: &seeders}}}, nil
}
func (routerSiteAdapter) Download(context.Context, sitepkg.Config, string) ([]byte, string, error) {
	return []byte("d4:infod4:name4:testee"), "fixture.torrent", nil
}

func (routerCloudDriver) Provider() string { return cloudpkg.ProviderPan115 }
func (routerCloudDriver) Capabilities() cloudpkg.Capabilities {
	return cloudpkg.Capabilities{NetworkDrive: true, DirectoryList: true, TemporaryDirectURL: true, SignedProxy: true}
}
func (routerCloudDriver) Probe(context.Context) (cloudpkg.Account, error) {
	return cloudpkg.Account{ID: "safe-account", Name: "测试账号"}, nil
}
func (routerCloudDriver) List(_ context.Context, parentID string, _ cloudpkg.PageRequest) (cloudpkg.Page, error) {
	if parentID == "0" {
		return cloudpkg.Page{Items: []cloudpkg.Item{{ID: "root-1", ParentID: "0", Name: "媒体", IsDir: true}}}, nil
	}
	return cloudpkg.Page{}, nil
}
func (routerCloudDriver) Stat(_ context.Context, id string) (cloudpkg.Item, error) {
	if id == "0" {
		return cloudpkg.Item{ID: "0", Name: "115 网盘", IsDir: true}, nil
	}
	if id == "root-1" {
		return cloudpkg.Item{ID: id, ParentID: "0", Name: "媒体", IsDir: true}, nil
	}
	if id == "video-1" {
		return cloudpkg.Item{ID: id, ParentID: "root-1", Name: "Movie.mkv", PickCode: "private-pickcode", Size: 100}, nil
	}
	return cloudpkg.Item{}, cloudpkg.Error(cloudpkg.CodeNotFound, false, nil)
}
func (routerCloudDriver) DirectURL(context.Context, cloudpkg.DirectURLRequest) (cloudpkg.TemporaryURL, error) {
	return cloudpkg.TemporaryURL{URL: "https://cdn.example.test/video", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func newTestClient(t *testing.T) *testClient {
	t.Helper()
	testRoot := t.TempDir()
	cfg := config.Config{Host: "127.0.0.1", Port: 3000, DatabasePath: filepath.Join(testRoot, "server.db"), LogDirectory: filepath.Join(testRoot, "logs"), CredentialKeyFile: filepath.Join(testRoot, "credentials.key"), Environment: "test", PublicOrigin: "http://localhost:3000", SessionIdleTTL: 2 * time.Hour, SessionMaxTTL: 7 * 24 * time.Hour}
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	log := NewLogger("test")
	audit := services.NewAuditService(db)
	authorization := services.NewAuthorizationService(db)
	auth, err := services.NewAuthService(db, cfg, authorization, audit)
	if err != nil {
		t.Fatal(err)
	}
	admin := services.NewAdminService(db, authorization, auth, audit)
	storages := services.NewStorageService(db, audit)
	directories, err := services.NewDirectoryBrowserService(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	profiles := services.NewMediaClassificationProfileService(db, audit, nil)
	api := handlers.NewAPI(cfg, auth, admin, audit, storages, directories, profiles, log)
	libraries := services.NewMediaLibraryService(db, audit, log)
	profiles.SetReferences(libraries)
	profiles.SetRevisionNotifier(libraries)
	storages.SetReferenceChecker(libraries)
	api.SetMediaLibraryService(libraries)
	t.Cleanup(libraries.Close)
	logManager, err := logging.NewManager(cfg.LogDirectory, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logManager.Close() })
	runtimeLogs, err := services.NewRuntimeLogService(db, logManager, audit)
	if err != nil {
		t.Fatal(err)
	}
	api.SetRuntimeLogService(runtimeLogs)
	queue := services.NewQueueService(db, audit)
	events := services.NewQueueEventHub()
	queue.SetEventHub(events)
	api.SetQueueService(queue)
	api.SetQueueEventHub(events)
	credentialStore, err := credential.Open(cfg.CredentialKeyFile, "")
	if err != nil {
		t.Fatal(err)
	}
	cloudRegistry := cloudpkg.NewRegistry()
	if err := cloudRegistry.Register(cloudpkg.ProviderPan115, func(config cloudpkg.Config) (cloudpkg.Driver, error) {
		if _, err := pan115.ParseCookie(config.Cookie); err != nil {
			return nil, cloudpkg.Error(cloudpkg.CodeCookieInvalid, false, err)
		}
		return routerCloudDriver{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	connections := services.NewConnectionService(db, audit, credentialStore, cloudRegistry, log)
	api.SetCredentialRevealService(services.NewCredentialRevealService(db, audit, credentialStore))
	changes := services.NewMediaChangeService(db)
	refresh := services.NewMediaServerRefreshService(db, queue, audit, connections)
	changes.SetReadyHandler(refresh.EnqueueLibrary)
	libraries.SetMediaChangeService(changes)
	api.SetMediaChangeService(changes)
	api.SetMediaServerRefreshService(refresh)
	signedProxy, err := services.NewSignedProxyService(db, credentialStore, connections, cfg.PublicOrigin, log)
	if err != nil {
		t.Fatal(err)
	}
	embyGateway, err := services.NewEmbyGatewayService(db, audit, signedProxy, cfg.PublicOrigin, log)
	if err != nil {
		t.Fatal(err)
	}
	api.SetSignedProxyService(signedProxy)
	api.SetEmbyGatewayService(embyGateway)
	libraries.SetConnectionService(connections)
	api.SetConnectionService(connections)
	storages.SetConnectionService(connections)
	providerDirectories := services.NewProviderDirectoryService(connections, credentialStore)
	api.SetProviderDirectoryService(providerDirectories)
	directories.SetProviderDirectoryService(providerDirectories)
	providerRegistry := downloadpkg.NewRegistry()
	fakeClient := downloadpkg.NewFakeClient()
	if err := providerRegistry.Register(models.DownloaderTypeFake, downloadpkg.FakeCapabilities, func(downloadpkg.Config) (downloadpkg.Client, error) { return fakeClient, nil }); err != nil {
		t.Fatal(err)
	}
	downloaders := services.NewDownloaderService(db, audit, credentialStore, providerRegistry)
	downloadSettings := services.NewDownloadSettingsService(db, audit)
	seedingSettings := services.NewSeedingSettingsService(db, audit)
	metadataSettings := services.NewMetadataSettingsService(db, audit, credentialStore)
	aiRecognitionSettings := services.NewAIRecognitionSettingsService(db, audit, credentialStore)
	discovery := services.NewDiscoveryService(db, metadataSettings, log)
	api.SetDiscoveryService(discovery)
	coverage := services.NewMediaCoverageService(db, metadataSettings)
	api.SetMediaCoverageService(coverage)
	api.SetFollowService(services.NewFollowService(db, audit, queue, coverage, authorization))
	libraries.SetMetadataSettingsService(metadataSettings)
	storages.AddReferenceChecker(downloadSettings)
	downloads := services.NewDownloadService(db, audit, credentialStore, downloaders, downloadSettings, queue, log)
	downloads.SetMetadataSettings(metadataSettings)
	downloads.SetSeedingSettings(seedingSettings)
	sites := services.NewSiteServiceWithAdapters(db, audit, credentialStore, downloads, []sitepkg.Adapter{routerSiteAdapter{}}, log)
	sites.SetMetadataSettings(metadataSettings)
	api.SetSiteService(sites)
	transfers := services.NewTransferService(db, audit, queue, log)
	seeding := services.NewSeedingService(db, audit, queue, downloaders, log)
	transfers.SetSeedingService(seeding)
	downloads.SetTransferService(transfers)
	api.SetDownloaderService(downloaders)
	api.SetDownloadService(downloads)
	api.SetTransferService(transfers)
	api.SetDownloadSettingsService(downloadSettings)
	api.SetMetadataSettingsService(metadataSettings)
	api.SetAIRecognitionSettingsService(aiRecognitionSettings)
	api.SetSeedingSettingsService(seedingSettings)
	api.SetSeedingService(seeding)
	api.SetPluginRepositoryService(services.NewPluginRepositoryService(db, audit, nil, log))
	return &testClient{router: New(cfg, api, auth, log), queue: queue, db: db, connections: connections, signedProxy: signedProxy, embyGateway: embyGateway, changes: changes, sites: sites}
}

func TestFollowRoutesRequireAuthenticationPermissionsAndNoStore(t *testing.T) {
	owner := newTestClient(t)
	status, _ := owner.request(t, http.MethodGet, "/api/v1/follows", nil, false)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous follows status=%d", status)
	}
	owner.setup(t)
	status, envelope := owner.request(t, http.MethodGet, "/api/v1/follows", nil, false)
	if status != http.StatusOK || owner.lastHeader.Get("Cache-Control") != "no-store" || bytes.Contains(bytes.ToLower(envelope.Data), []byte("source_ciphertext")) {
		t.Fatalf("owner follows status=%d cache=%q data=%s", status, owner.lastHeader.Get("Cache-Control"), envelope.Data)
	}

	var viewer models.Role
	if err := owner.db.First(&viewer, "code = ?", authz.RoleViewer).Error; err != nil {
		t.Fatal(err)
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "follow-viewer", "password": "follow-viewer-strong-password", "role_ids": []uint{viewer.ID}}, true)
	if status != http.StatusCreated {
		t.Fatalf("create follow viewer status=%d", status)
	}
	viewerClient := newTestClientWithRouter(owner.router)
	viewerClient.login(t, "follow-viewer", "follow-viewer-strong-password")
	status, _ = viewerClient.request(t, http.MethodGet, "/api/v1/follows", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer follows status=%d", status)
	}
}

func TestCredentialRevealRouteIsNoStoreAuthorizedAndDoesNotChangeNormalDTOs(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	secret := "UID=100_A1; CID=cid-value; SEID=reveal-route-secret; KID=kid-value"
	status, created := owner.request(t, http.MethodPost, "/api/v1/connections", map[string]any{
		"name": "Reveal Route 115", "provider": "pan115", "cookie": secret, "enabled": true,
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create status=%d message=%s", status, created.Message)
	}
	var connection struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(created.Data, &connection); err != nil {
		t.Fatal(err)
	}
	status, list := owner.request(t, http.MethodGet, "/api/v1/connections?provider=pan115", nil, false)
	if status != http.StatusOK || bytes.Contains(list.Data, []byte(secret)) {
		t.Fatalf("normal DTO leaked credential status=%d data=%s", status, list.Data)
	}

	status, revealed := owner.request(t, http.MethodPost, "/api/v1/credentials/reveal", map[string]any{"resource_type": "connection", "resource_id": uintString(connection.ID), "field": "credential"}, true)
	if status != http.StatusOK || owner.lastHeader.Get("Cache-Control") != "no-store" || !bytes.Contains(revealed.Data, []byte(secret)) {
		t.Fatalf("reveal status=%d cache=%q data=%s", status, owner.lastHeader.Get("Cache-Control"), revealed.Data)
	}

	rolesStatus, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if rolesStatus != http.StatusOK {
		t.Fatal(rolesStatus)
	}
	var roles struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	_ = json.Unmarshal(rolesEnvelope.Data, &roles)
	var viewerID uint
	for _, role := range roles.List {
		if role.Code == authz.RoleViewer {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "reveal-viewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "reveal-viewer", "viewer-strong-password")
	status, denied := viewer.request(t, http.MethodPost, "/api/v1/credentials/reveal", map[string]any{"resource_type": "connection", "resource_id": uintString(connection.ID), "field": "credential"}, true)
	if status != http.StatusForbidden || bytes.Contains(denied.Data, []byte(secret)) || viewer.lastHeader.Get("Cache-Control") != "no-store" {
		t.Fatalf("denied status=%d cache=%q data=%s", status, viewer.lastHeader.Get("Cache-Control"), denied.Data)
	}
}

func createRouterSignedArtifact(t *testing.T, client *testClient, actor services.Actor) string {
	t.Helper()
	connection, err := client.connections.Create(actor, services.ConnectionInput{Name: "Proxy route account", Provider: cloudpkg.ProviderPan115, Cookie: "UID=100_A1; CID=cid-value; SEID=seid-value; KID=kid-value", Enabled: true}, services.RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Proxy route cloud", NameNormalized: "proxy-route-cloud", Type: models.StorageTypePan115, RootPath: "root-1", RootDisplayPath: "/媒体", RootPathNormalized: "pan115:proxy-route", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{"temporary_direct_url":true,"signed_proxy":true}`, CreatedAt: now, UpdatedAt: now}
	if err := client.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := client.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Proxy route library", NameNormalized: "proxy-route-library", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", ProviderRootID: "root-1", Enabled: true, Recursive: true, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, STRMEnabled: true, SignedProxyEnabled: true, STRMLocalRoot: t.TempDir(), Status: models.MediaLibraryStatusListening, ArtifactStatus: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := client.db.Select("*").Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: "22222222-2222-4222-8222-222222222222", LibraryID: library.ID, Generation: 1, PolicyJSON: `{}`, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := client.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	opaque := "artifact_BBBBBBBBBBBBBBBBBBBBBB"
	entry := models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/Movie.mkv", ProviderID: "video-1", Size: 100, ModifiedAt: now, MediaType: "movie", Title: "Movie", WorkKey: "movie:file:proxy-route", MatchStatus: "unrecognized", CategoryName: "未分类", LastGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := client.db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	artifact := models.MediaArtifact{OpaqueID: opaque, RunID: run.ID, LibraryID: library.ID, SourceIdentity: "entry:" + uintString(entry.ID), ProviderItemID: "video-1", Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/Movie.strm", Managed: true, Active: true, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := client.db.Select("*").Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	signed, err := client.signedProxy.SignArtifact(opaque, library.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestSignedSTRMProxyUsesSignedPublicOriginAndSupportsGETHeadOnly(t *testing.T) {
	client := newTestClient(t)
	user := models.User{Username: "proxy-route", UsernameNormalized: "proxy-route", DisplayName: "Proxy Route", PasswordHash: "unused", Status: models.UserStatusActive}
	if err := client.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	actor := services.Actor{User: user, Permissions: map[string]struct{}{
		authz.PermissionConnectionsCreate: {}, authz.PermissionConnectionsRead: {}, authz.PermissionConnectionsTest: {},
	}}
	signed := createRouterSignedArtifact(t, client, actor)
	parsed, _ := url.Parse(signed)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequest(method, parsed.RequestURI(), nil)
		request.Host = "attacker.example"
		request.Header.Set("User-Agent", "OhMyCine-Test")
		response := httptest.NewRecorder()
		client.router.ServeHTTP(response, request)
		if response.Code != http.StatusFound || response.Header().Get("Location") != "https://cdn.example.test/video" || strings.Contains(response.Header().Get("Location"), "attacker.example") {
			t.Fatalf("%s status=%d location=%q body=%q", method, response.Code, response.Header().Get("Location"), response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, parsed.RequestURI(), nil)
	response := httptest.NewRecorder()
	client.router.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST status=%d allow=%q", response.Code, response.Header().Get("Allow"))
	}
}

func TestEmbyGatewayRoutePreservesUpstreamHeadersAndBypassesAdminBrowserGuard(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-Proto") != "" {
			http.Error(w, "untrusted forwarded headers reached upstream", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/web/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'")
			_, _ = io.WriteString(w, `<!doctype html><html><head><script src="boot.js"></script></head><body></body></html>`)
			return
		}
		if r.URL.Path != "/api/plain" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=120")
		w.Header().Set("Content-Security-Policy", "default-src https://emby.example")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Permissions-Policy", "fullscreen=(self)")
		_, _ = io.WriteString(w, r.Method)
	}))
	defer upstream.Close()
	client := newTestClient(t)
	user := models.User{Username: "emby-route", UsernameNormalized: "emby-route", DisplayName: "Emby Route", PasswordHash: "unused", Status: models.UserStatusActive}
	if err := client.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	actor := services.Actor{User: user, Permissions: map[string]struct{}{authz.PermissionConnectionsCreate: {}, authz.PermissionConnectionsRead: {}, authz.PermissionConnectionsUpdate: {}}}
	connection, err := client.connections.Create(actor, services.ConnectionInput{Name: "Route Emby", Provider: models.ConnectionProviderEmby, Endpoint: upstream.URL, APIKey: "server-admin-key", Enabled: true}, services.RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.db.Model(&models.Connection{}).Where("id = ?", connection.ID).Update("last_health_status", "online").Error; err != nil {
		t.Fatal(err)
	}
	gateway, err := client.embyGateway.Configure(actor, connection.ID, true, 1, services.RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	downstream := httptest.NewServer(client.router)
	defer downstream.Close()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		request, err := http.NewRequest(method, downstream.URL+"/emby/"+gateway.PublicID+"/api/plain", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "attacker.example"
		request.Header.Set("Forwarded", "host=attacker.example;proto=https")
		request.Header.Set("X-Forwarded-For", "203.0.113.5")
		request.Header.Set("X-Forwarded-Host", "attacker.example")
		request.Header.Set("X-Forwarded-Proto", "https")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || string(body) != method || response.Header.Get("Cache-Control") != "public, max-age=120" || response.Header.Get("Content-Security-Policy") != "default-src https://emby.example" || response.Header.Get("X-Frame-Options") != "SAMEORIGIN" || response.Header.Get("Permissions-Policy") != "fullscreen=(self)" {
			t.Fatalf("%s status=%d headers=%v body=%q", method, response.StatusCode, response.Header, body)
		}
	}
	indexResponse, err := http.Get(downstream.URL + "/emby/" + gateway.PublicID + "/web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	indexBody, _ := io.ReadAll(indexResponse.Body)
	_ = indexResponse.Body.Close()
	compatibilityAssetURL := "/emby/" + gateway.PublicID + "/web/ohmycine-directplay.js"
	if indexResponse.StatusCode != http.StatusOK || !strings.Contains(string(indexBody), `src="`+compatibilityAssetURL+`"`) || !strings.Contains(indexResponse.Header.Get("Cache-Control"), "no-store") || indexResponse.Header.Get("Content-Security-Policy") != "default-src 'self'; script-src 'self'" {
		t.Fatalf("index status=%d headers=%v body=%q", indexResponse.StatusCode, indexResponse.Header, indexBody)
	}
	assetResponse, err := http.Get(downstream.URL + compatibilityAssetURL)
	if err != nil {
		t.Fatal(err)
	}
	assetBody, _ := io.ReadAll(assetResponse.Body)
	_ = assetResponse.Body.Close()
	if assetResponse.StatusCode != http.StatusOK || !strings.Contains(string(assetBody), "Object.defineProperty") || !strings.Contains(string(assetBody), "MutationObserver") || assetResponse.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("compatibility asset status=%d headers=%v body=%q", assetResponse.StatusCode, assetResponse.Header, assetBody)
	}
}

func TestEmbyGatewayPlaybackUsesEmbyAPIRelativeStreamPathThroughGinRouter(t *testing.T) {
	client := newTestClient(t)
	user := models.User{Username: "emby-playback-route", UsernameNormalized: "emby-playback-route", DisplayName: "Emby Playback Route", PasswordHash: "unused", Status: models.UserStatusActive}
	if err := client.db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	actor := services.Actor{User: user, Permissions: map[string]struct{}{
		authz.PermissionConnectionsCreate: {},
		authz.PermissionConnectionsRead:   {},
		authz.PermissionConnectionsUpdate: {},
	}}
	signedURL := createRouterSignedArtifact(t, client, actor)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/Items/7/PlaybackInfo" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"MediaSources": []any{
			map[string]any{"Id": "mediasource_7", "Type": "Video", "Path": signedURL, "SupportsTranscoding": true, "TranscodingUrl": "/emby/videos/7/master.m3u8"},
		}})
	}))
	defer upstream.Close()

	createGateway := func(name string) services.EmbyGatewaySummary {
		t.Helper()
		connection, err := client.connections.Create(actor, services.ConnectionInput{Name: name, Provider: models.ConnectionProviderEmby, Endpoint: upstream.URL + "/emby", APIKey: "server-admin-key", Enabled: true}, services.RequestContext{})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.db.Model(&models.Connection{}).Where("id = ?", connection.ID).Update("last_health_status", "online").Error; err != nil {
			t.Fatal(err)
		}
		gateway, err := client.embyGateway.Configure(actor, connection.ID, true, 1, services.RequestContext{})
		if err != nil {
			t.Fatal(err)
		}
		return gateway
	}
	gateway := createGateway("Emby playback route")
	downstream := httptest.NewServer(client.router)
	defer downstream.Close()
	httpClient := &http.Client{
		Transport: downstream.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	doRequest := func(method, path string, body io.Reader) (*http.Response, []byte) {
		t.Helper()
		request, err := http.NewRequest(method, downstream.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("User-Agent", "Emby Web/4.9.5.0")
		response, err := httpClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		responseBody, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return response, responseBody
	}

	// Emby Web addresses PlaybackInfo beneath the gateway server base plus its
	// own /emby application path.
	gatewayBase := "/emby/" + gateway.PublicID
	playbackResponse, playbackBody := doRequest(http.MethodPost, gatewayBase+"/emby/Items/7/PlaybackInfo", strings.NewReader(`{"UserId":"user-1"}`))
	if playbackResponse.StatusCode != http.StatusOK || playbackResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("PlaybackInfo status=%d cache=%q body=%q", playbackResponse.StatusCode, playbackResponse.Header.Get("Cache-Control"), playbackBody)
	}
	var playback struct {
		MediaSources []struct {
			ID              string `json:"Id"`
			Path            string `json:"Path"`
			DirectStreamURL string `json:"DirectStreamUrl"`
		} `json:"MediaSources"`
	}
	if err := json.Unmarshal(playbackBody, &playback); err != nil || len(playback.MediaSources) != 1 {
		t.Fatalf("decode PlaybackInfo err=%v body=%q", err, playbackBody)
	}
	streamValue := playback.MediaSources[0].DirectStreamURL
	streamURL, err := url.Parse(streamValue)
	if err != nil || streamURL.Path != "/videos/7/stream" || playback.MediaSources[0].Path != streamValue || streamURL.Query().Get("omc_ticket") == "" || strings.Contains(streamValue, gatewayBase) {
		t.Fatalf("invalid API-relative stream URL %q path=%q err=%v", streamValue, playback.MediaSources[0].Path, err)
	}

	// This is the exact construction used by Emby Web: gateway server base,
	// Emby's /emby API base, then the API-relative DirectStreamUrl.
	browserStreamPath := gatewayBase + "/emby" + streamURL.RequestURI()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response, responseBody := doRequest(method, browserStreamPath, nil)
		if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "https://cdn.example.test/video" || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s stream status=%d location=%q cache=%q body=%q", method, response.StatusCode, response.Header.Get("Location"), response.Header.Get("Cache-Control"), responseBody)
		}
	}

	invalidQuery := streamURL.Query()
	ticket := invalidQuery.Get("omc_ticket")
	replacement := "A"
	if strings.HasSuffix(ticket, replacement) {
		replacement = "B"
	}
	invalidQuery.Set("omc_ticket", ticket[:len(ticket)-1]+replacement)
	invalidResponse, _ := doRequest(http.MethodGet, gatewayBase+"/emby"+streamURL.Path+"?"+invalidQuery.Encode(), nil)
	if invalidResponse.StatusCode != http.StatusForbidden || invalidResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid ticket status=%d cache=%q", invalidResponse.StatusCode, invalidResponse.Header.Get("Cache-Control"))
	}

	wrongSourceQuery := streamURL.Query()
	wrongSourceQuery.Set("MediaSourceId", "mediasource_other")
	wrongSourceResponse, _ := doRequest(http.MethodGet, gatewayBase+"/emby"+streamURL.Path+"?"+wrongSourceQuery.Encode(), nil)
	if wrongSourceResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-source ticket status=%d", wrongSourceResponse.StatusCode)
	}

	secondGateway := createGateway("Emby playback route second")
	crossGatewayResponse, _ := doRequest(http.MethodGet, "/emby/"+secondGateway.PublicID+"/emby"+streamURL.RequestURI(), nil)
	if crossGatewayResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-gateway ticket status=%d", crossGatewayResponse.StatusCode)
	}
}

func (c *testClient) request(t *testing.T, method, path string, body any, csrf bool) (int, testEnvelope) {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Origin", "http://localhost:3000")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if csrf {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	response := httptest.NewRecorder()
	c.router.ServeHTTP(response, req)
	c.lastHeader = response.Header().Clone()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "omc_session" {
			c.cookie = cookie
		}
	}
	var envelope testEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %d %s: %v", response.Code, response.Body.String(), err)
	}
	return response.Code, envelope
}

func (c *testClient) playerRequest(t *testing.T, method, path, token string, body any) (int, testEnvelope, http.Header) {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	c.router.ServeHTTP(response, request)
	var envelope testEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Player response %d %s: %v", response.Code, response.Body.String(), err)
	}
	return response.Code, envelope, response.Header().Clone()
}

func TestPlayerDeviceAuthenticationIsRevocableAndIsolatedFromBrowserAdmin(t *testing.T) {
	client := newTestClient(t)
	client.setup(t)
	var owner models.User
	if err := client.db.Where("username_normalized = ?", "owner").First(&owner).Error; err != nil {
		t.Fatal(err)
	}
	createRouterSignedArtifact(t, client, services.Actor{User: owner, Permissions: map[string]struct{}{
		authz.PermissionConnectionsCreate: {}, authz.PermissionConnectionsRead: {},
	}})
	loginBody := map[string]any{"username": "owner", "password": "strong-owner-password", "device_id": "windows-device-0001", "device_name": "卧室 Windows Player"}
	status, envelope, headers := client.playerRequest(t, http.MethodPost, "/api/v1/player/auth/login", "", loginBody)
	if status != http.StatusOK || headers.Get("Cache-Control") != "no-store" || len(headers.Values("Set-Cookie")) != 0 {
		t.Fatalf("Player login status=%d cache=%q cookies=%v message=%s", status, headers.Get("Cache-Control"), headers.Values("Set-Cookie"), envelope.Message)
	}
	var login struct {
		AccessToken string `json:"access_token"`
		Device      struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	if err := json.Unmarshal(envelope.Data, &login); err != nil || !strings.HasPrefix(login.AccessToken, "omc_player_") || login.Device.ID == "" {
		t.Fatalf("invalid Player login response err=%v data=%s", err, envelope.Data)
	}
	status, bootstrapEnvelope, _ := client.playerRequest(t, http.MethodGet, "/api/v1/player/bootstrap", login.AccessToken, nil)
	if status != http.StatusOK {
		t.Fatalf("Player bootstrap status=%d", status)
	}
	var bootstrap struct {
		MediaLibraryCount int `json:"media_library_count"`
	}
	if err := json.Unmarshal(bootstrapEnvelope.Data, &bootstrap); err != nil || bootstrap.MediaLibraryCount != 1 {
		t.Fatalf("Player bootstrap count invalid err=%v data=%s", err, bootstrapEnvelope.Data)
	}
	status, librariesEnvelope, _ := client.playerRequest(t, http.MethodGet, "/api/v1/player/media-libraries", login.AccessToken, nil)
	if status != http.StatusOK || bytes.Contains(librariesEnvelope.Data, []byte(`"root_path"`)) || bytes.Contains(librariesEnvelope.Data, []byte(`"relative_root"`)) {
		t.Fatalf("Player libraries status=%d data=%s", status, librariesEnvelope.Data)
	}
	var library models.MediaLibrary
	if err := client.db.Where("name_normalized = ?", "proxy-route-library").First(&library).Error; err != nil {
		t.Fatal(err)
	}
	var readyChange models.MediaLibraryChange
	if err := client.db.Transaction(func(tx *gorm.DB) error {
		var err error
		readyChange, err = client.changes.RecordTx(tx, library.ID, library.DirtyGeneration, models.MediaLibraryChangeCatalog, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	client.changes.NotifyCommitted(library.ID, readyChange.Revision)
	status, changesEnvelope, changeHeaders := client.playerRequest(t, http.MethodGet, "/api/v1/player/media-changes?cursor=0&wait_seconds=0", login.AccessToken, nil)
	if status != http.StatusOK || changeHeaders.Get("Cache-Control") != "no-store" || !bytes.Contains(changesEnvelope.Data, []byte(`"library_id":`+uintString(library.ID))) || bytes.Contains(changesEnvelope.Data, []byte("relative_root")) {
		t.Fatalf("Player changes status=%d data=%s", status, changesEnvelope.Data)
	}
	var changePage struct {
		Changes []struct {
			ContentRevision uint64    `json:"content_revision"`
			ChangedAt       time.Time `json:"changed_at"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(changesEnvelope.Data, &changePage); err != nil || len(changePage.Changes) != 1 || changePage.Changes[0].ContentRevision != readyChange.Revision || changePage.Changes[0].ChangedAt.IsZero() {
		t.Fatalf("Player change DTO invalid err=%v data=%s", err, changesEnvelope.Data)
	}
	status, _, _ = client.playerRequest(t, http.MethodGet, "/api/v1/player/media-changes?cursor=18446744073709551616&wait_seconds=0", login.AccessToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("overflow Player change cursor status=%d", status)
	}
	status, _, _ = client.playerRequest(t, http.MethodGet, "/api/v1/player/media-changes?cursor="+strings.Repeat("1", 128)+"&wait_seconds=0", login.AccessToken, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("oversized Player change cursor status=%d", status)
	}
	status, catalogEnvelope, _ := client.playerRequest(t, http.MethodGet, "/api/v1/player/media-libraries/"+uintString(library.ID)+"/catalog?page=1&page_size=20", login.AccessToken, nil)
	if status != http.StatusOK || bytes.Contains(catalogEnvelope.Data, []byte("/Movie.mkv")) || bytes.Contains(catalogEnvelope.Data, []byte("video-1")) {
		t.Fatalf("Player catalog status=%d data=%s", status, catalogEnvelope.Data)
	}
	var catalog struct {
		List []struct {
			ID string `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(catalogEnvelope.Data, &catalog); err != nil || len(catalog.List) != 1 || catalog.List[0].ID == "" {
		t.Fatalf("Player catalog invalid err=%v data=%s", err, catalogEnvelope.Data)
	}
	status, detailEnvelope, _ := client.playerRequest(t, http.MethodGet, "/api/v1/player/media-libraries/"+uintString(library.ID)+"/catalog/"+url.PathEscape(catalog.List[0].ID), login.AccessToken, nil)
	if status != http.StatusOK || bytes.Contains(detailEnvelope.Data, []byte("/Movie.mkv")) || bytes.Contains(detailEnvelope.Data, []byte("video-1")) {
		t.Fatalf("Player detail status=%d data=%s", status, detailEnvelope.Data)
	}
	var playableDetail struct {
		Versions []struct {
			Playable      bool   `json:"playable"`
			DeliveryKind  string `json:"delivery_kind"`
			ExactIdentity string `json:"exact_identity"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(detailEnvelope.Data, &playableDetail); err != nil || len(playableDetail.Versions) != 1 || !playableDetail.Versions[0].Playable || playableDetail.Versions[0].DeliveryKind != "server_redirect" || !strings.HasPrefix(playableDetail.Versions[0].ExactIdentity, "ohmycine:artifact:") {
		t.Fatalf("Player playable detail invalid err=%v data=%s", err, detailEnvelope.Data)
	}
	var entry models.MediaLibraryEntry
	if err := client.db.Where("library_id = ?", library.ID).First(&entry).Error; err != nil {
		t.Fatal(err)
	}
	localRoot := t.TempDir()
	localBody := []byte("0123456789abcdef")
	if err := os.WriteFile(filepath.Join(localRoot, "Movie.mkv"), localBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.db.Model(&models.Storage{}).Where("id = ?", library.StorageID).Updates(map[string]any{"type": models.StorageTypeLocal, "root_path": localRoot, "root_path_normalized": strings.ToLower(localRoot)}).Error; err != nil {
		t.Fatal(err)
	}
	status, localDetailEnvelope, _ := client.playerRequest(t, http.MethodGet, "/api/v1/player/media-libraries/"+uintString(library.ID)+"/catalog/"+url.PathEscape(catalog.List[0].ID), login.AccessToken, nil)
	var localDetail struct {
		Versions []struct {
			Playable      bool   `json:"playable"`
			DeliveryKind  string `json:"delivery_kind"`
			ExactIdentity string `json:"exact_identity"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(localDetailEnvelope.Data, &localDetail); status != http.StatusOK || err != nil || len(localDetail.Versions) != 1 || !localDetail.Versions[0].Playable || localDetail.Versions[0].DeliveryKind != "server_stream" || !strings.HasPrefix(localDetail.Versions[0].ExactIdentity, "server:entry:") {
		t.Fatalf("local Player detail was not playable: status=%d err=%v data=%s", status, err, localDetailEnvelope.Data)
	}
	for _, test := range []struct {
		method        string
		rangeHeader   string
		wantStatus    int
		wantBody      string
		contentRange  string
		contentLength string
	}{
		{method: http.MethodGet, wantStatus: http.StatusOK, wantBody: string(localBody), contentLength: "16"},
		{method: http.MethodHead, wantStatus: http.StatusOK, contentLength: "16"},
		{method: http.MethodGet, rangeHeader: "bytes=4-7", wantStatus: http.StatusPartialContent, wantBody: "4567", contentRange: "bytes 4-7/16", contentLength: "4"},
		{method: http.MethodGet, rangeHeader: "bytes=99-100", wantStatus: http.StatusRequestedRangeNotSatisfiable, wantBody: "invalid range: failed to overlap\n", contentRange: "bytes */16"},
	} {
		localRequest := httptest.NewRequest(test.method, "/api/v1/player/media-entries/"+uintString(entry.ID)+"/stream", nil)
		localRequest.Header.Set("Authorization", "Bearer "+login.AccessToken)
		if test.rangeHeader != "" {
			localRequest.Header.Set("Range", test.rangeHeader)
		}
		localResponse := httptest.NewRecorder()
		client.router.ServeHTTP(localResponse, localRequest)
		if localResponse.Code != test.wantStatus || localResponse.Body.String() != test.wantBody || localResponse.Header().Get("Content-Range") != test.contentRange || localResponse.Header().Get("Cache-Control") != "no-store" || localResponse.Header().Get("Content-Length") != test.contentLength {
			t.Fatalf("local stream method=%s range=%q status=%d body=%q content-range=%q", test.method, test.rangeHeader, localResponse.Code, localResponse.Body.String(), localResponse.Header().Get("Content-Range"))
		}
		if (test.wantStatus == http.StatusOK || test.wantStatus == http.StatusPartialContent) && (localResponse.Header().Get("Content-Type") == "" || localResponse.Header().Get("Accept-Ranges") != "bytes") {
			t.Fatalf("local stream headers content-type=%q accept-ranges=%q", localResponse.Header().Get("Content-Type"), localResponse.Header().Get("Accept-Ranges"))
		}
	}
	if err := client.db.Model(&models.Storage{}).Where("id = ?", library.StorageID).Update("type", models.StorageTypePan115).Error; err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		streamRequest := httptest.NewRequest(method, "/api/v1/player/media-entries/"+uintString(entry.ID)+"/stream", nil)
		streamRequest.Header.Set("Authorization", "Bearer "+login.AccessToken)
		streamRequest.Header.Set("User-Agent", "OhMyCine Player Test")
		streamResponse := httptest.NewRecorder()
		client.router.ServeHTTP(streamResponse, streamRequest)
		if streamResponse.Code != http.StatusFound || streamResponse.Header().Get("Location") != "https://cdn.example.test/video" || streamResponse.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s Player stream status=%d location=%q", method, streamResponse.Code, streamResponse.Header().Get("Location"))
		}
	}
	if err := client.db.Model(&models.MediaArtifact{}).Where("source_identity = ?", "entry:"+uintString(entry.ID)).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}
	unavailableRequest := httptest.NewRequest(http.MethodGet, "/api/v1/player/media-entries/"+uintString(entry.ID)+"/stream", nil)
	unavailableRequest.Header.Set("Authorization", "Bearer "+login.AccessToken)
	unavailableResponse := httptest.NewRecorder()
	client.router.ServeHTTP(unavailableResponse, unavailableRequest)
	if unavailableResponse.Code != http.StatusNotFound || unavailableResponse.Header().Get("Location") != "" {
		t.Fatalf("unavailable Player stream status=%d location=%q body=%s", unavailableResponse.Code, unavailableResponse.Header().Get("Location"), unavailableResponse.Body.String())
	}

	management := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	management.Header.Set("Authorization", "Bearer "+login.AccessToken)
	managementResponse := httptest.NewRecorder()
	client.router.ServeHTTP(managementResponse, management)
	if managementResponse.Code != http.StatusUnauthorized {
		t.Fatalf("Player bearer entered browser management API: status=%d", managementResponse.Code)
	}

	status, secondEnvelope, _ := client.playerRequest(t, http.MethodPost, "/api/v1/player/auth/login", "", loginBody)
	if status != http.StatusOK {
		t.Fatalf("second Player login status=%d message=%s", status, secondEnvelope.Message)
	}
	var second struct {
		AccessToken string `json:"access_token"`
		Device      struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	if err := json.Unmarshal(secondEnvelope.Data, &second); err != nil || second.AccessToken == "" || second.AccessToken == login.AccessToken || second.Device.ID == "" {
		t.Fatalf("second Player token invalid err=%v", err)
	}
	status, _, _ = client.playerRequest(t, http.MethodGet, "/api/v1/player/bootstrap", login.AccessToken, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("superseded Player token status=%d", status)
	}
	status, devicesEnvelope := client.request(t, http.MethodGet, "/api/v1/player-devices", nil, false)
	if status != http.StatusOK || client.lastHeader.Get("Cache-Control") != "no-store" || bytes.Contains(devicesEnvelope.Data, []byte("token")) || bytes.Contains(devicesEnvelope.Data, []byte("device_id")) || bytes.Contains(devicesEnvelope.Data, []byte("user_agent")) {
		t.Fatalf("browser Player devices status=%d cache=%q data=%s", status, client.lastHeader.Get("Cache-Control"), devicesEnvelope.Data)
	}
	var deviceList struct {
		List []struct {
			ID string `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(devicesEnvelope.Data, &deviceList); err != nil || len(deviceList.List) != 1 || deviceList.List[0].ID != second.Device.ID {
		t.Fatalf("browser Player devices invalid err=%v data=%s", err, devicesEnvelope.Data)
	}
	status, _ = client.request(t, http.MethodDelete, "/api/v1/player-devices/"+url.PathEscape(second.Device.ID), map[string]any{}, false)
	if status != http.StatusForbidden {
		t.Fatalf("Player device revoke without csrf status=%d", status)
	}
	status, _, _ = client.playerRequest(t, http.MethodGet, "/api/v1/player-devices", second.AccessToken, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("Player bearer entered device management API: status=%d", status)
	}
	status, _ = client.request(t, http.MethodDelete, "/api/v1/player-devices/"+url.PathEscape(second.Device.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("browser Player device revoke status=%d", status)
	}
	status, _, _ = client.playerRequest(t, http.MethodGet, "/api/v1/player/bootstrap", second.AccessToken, nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked Player token status=%d", status)
	}
}

func TestPlayerMediaChangesRevalidatesDeviceAfterWake(t *testing.T) {
	client := newTestClient(t)
	client.setup(t)
	status, loginEnvelope, _ := client.playerRequest(t, http.MethodPost, "/api/v1/player/auth/login", "", map[string]any{
		"username": "owner", "password": "strong-owner-password",
		"device_id": "wake-revalidation-device", "device_name": "Wake Revalidation Player",
	})
	var login struct {
		AccessToken string `json:"access_token"`
		Device      struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	if err := json.Unmarshal(loginEnvelope.Data, &login); status != http.StatusOK || err != nil || login.AccessToken == "" || login.Device.ID == "" {
		t.Fatalf("login status=%d err=%v data=%s", status, err, loginEnvelope.Data)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/player/media-changes?cursor=0&wait_seconds=12", nil)
	request.Header.Set("Authorization", "Bearer "+login.AccessToken)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		client.router.ServeHTTP(response, request)
		close(done)
	}()

	// Give the request time to finish its initial empty query and enter the
	// broadcast wait. The explicit wake below keeps the test bounded.
	time.Sleep(50 * time.Millisecond)
	now := time.Now().UTC()
	if err := client.db.Model(&models.DeviceToken{}).Where("id = ?", login.Device.ID).Update("revoked_at", now).Error; err != nil {
		t.Fatal(err)
	}
	client.changes.NotifyCommitted(1, 1)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked long poll did not stop after wake")
	}
	if response.Code != http.StatusUnauthorized || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), login.AccessToken) {
		t.Fatalf("status=%d cache=%q body=%q", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
}

func TestPlayerMediaChangesRevalidatesDisabledUserAfterWake(t *testing.T) {
	client := newTestClient(t)
	client.setup(t)
	status, loginEnvelope, _ := client.playerRequest(t, http.MethodPost, "/api/v1/player/auth/login", "", map[string]any{
		"username": "owner", "password": "strong-owner-password",
		"device_id": "wake-disabled-user-device", "device_name": "Disabled User Player",
	})
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginEnvelope.Data, &login); status != http.StatusOK || err != nil || login.AccessToken == "" {
		t.Fatalf("login status=%d err=%v data=%s", status, err, loginEnvelope.Data)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/player/media-changes?cursor=0&wait_seconds=12", nil)
	request.Header.Set("Authorization", "Bearer "+login.AccessToken)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		client.router.ServeHTTP(response, request)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := client.db.Model(&models.User{}).Where("username_normalized = ?", "owner").Update("status", models.UserStatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	client.changes.NotifyCommitted(1, 1)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("disabled-user long poll did not stop after wake")
	}
	if response.Code != http.StatusUnauthorized || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), login.AccessToken) {
		t.Fatalf("status=%d cache=%q body=%q", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
}

func TestPlayerMediaChangesFiltersLibrariesDisabledBeforeResponse(t *testing.T) {
	client := newTestClient(t)
	client.setup(t)
	var owner models.User
	if err := client.db.Where("username_normalized = ?", "owner").First(&owner).Error; err != nil {
		t.Fatal(err)
	}
	createRouterSignedArtifact(t, client, services.Actor{User: owner, Permissions: map[string]struct{}{
		authz.PermissionConnectionsCreate: {}, authz.PermissionConnectionsRead: {},
	}})
	status, loginEnvelope, _ := client.playerRequest(t, http.MethodPost, "/api/v1/player/auth/login", "", map[string]any{
		"username": "owner", "password": "strong-owner-password",
		"device_id": "visibility-filter-device", "device_name": "Visibility Filter Player",
	})
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginEnvelope.Data, &login); status != http.StatusOK || err != nil {
		t.Fatalf("login status=%d err=%v", status, err)
	}
	var library models.MediaLibrary
	if err := client.db.Where("name_normalized = ?", "proxy-route-library").First(&library).Error; err != nil {
		t.Fatal(err)
	}
	var change models.MediaLibraryChange
	if err := client.db.Transaction(func(tx *gorm.DB) error {
		var err error
		change, err = client.changes.RecordTx(tx, library.ID, library.DirtyGeneration, models.MediaLibraryChangeCatalog, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := client.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}

	status, envelope, _ := client.playerRequest(t, http.MethodGet, "/api/v1/player/media-changes?cursor=0&wait_seconds=0", login.AccessToken, nil)
	var page struct {
		Cursor  string            `json:"cursor"`
		Changes []json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(envelope.Data, &page); status != http.StatusOK || err != nil || len(page.Changes) != 0 || page.Cursor != strconv.FormatUint(change.Sequence, 10) {
		t.Fatalf("status=%d page=%+v err=%v data=%s", status, page, err, envelope.Data)
	}
	if err := client.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := client.db.Model(&models.Storage{}).Where("id = ?", library.StorageID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	status, envelope, _ = client.playerRequest(t, http.MethodGet, "/api/v1/player/media-changes?cursor=0&wait_seconds=0", login.AccessToken, nil)
	if err := json.Unmarshal(envelope.Data, &page); status != http.StatusOK || err != nil || len(page.Changes) != 0 || page.Cursor != strconv.FormatUint(change.Sequence, 10) {
		t.Fatalf("disabled storage status=%d page=%+v err=%v data=%s", status, page, err, envelope.Data)
	}
}

func (c *testClient) setup(t *testing.T) map[string]any {
	status, envelope := c.request(t, http.MethodPost, "/api/v1/setup/owner", map[string]any{"username": "owner", "display_name": "Owner", "password": "strong-owner-password"}, false)
	if status != http.StatusCreated {
		t.Fatalf("setup status=%d body=%s", status, envelope.Message)
	}
	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	c.csrf, _ = data["csrf_token"].(string)
	if c.csrf == "" || c.cookie == nil {
		t.Fatal("setup did not issue csrf and session cookie")
	}
	return data
}

func TestPTSiteAndDiscoveryRoutesAreProtectedRedactedAndStreamSafe(t *testing.T) {
	client := newTestClient(t)
	unauthenticated := httptest.NewRecorder()
	client.router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/sites", nil))
	if unauthenticated.Code != http.StatusUnauthorized || unauthenticated.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated sites status=%d cache=%q", unauthenticated.Code, unauthenticated.Header().Get("Cache-Control"))
	}

	client.setup(t)
	status, envelope := client.request(t, http.MethodGet, "/api/v1/sites/catalog", nil, false)
	if status != http.StatusOK || !bytes.Contains(envelope.Data, []byte(`"key":"pttime"`)) {
		t.Fatalf("site catalog status=%d data=%s", status, envelope.Data)
	}
	status, _ = client.request(t, http.MethodPost, "/api/v1/sites/resolve", map[string]any{"site_type": "bt", "base_url": "https://nyaa.si"}, false)
	if status != http.StatusForbidden {
		t.Fatalf("BT site resolve without csrf status=%d", status)
	}
	status, envelope = client.request(t, http.MethodPost, "/api/v1/sites/resolve", map[string]any{"site_type": "bt", "base_url": "https://nyaa.si.evil.test"}, true)
	if status != http.StatusBadRequest || !bytes.Contains(envelope.Data, []byte(services.CodeSiteBTHostUnsupported)) {
		t.Fatalf("BT lookalike host resolve status=%d data=%s", status, envelope.Data)
	}
	payload := map[string]any{"name": "PTTime", "kind": "pttime", "base_url": "https://pt.example.test", "cookie": "uid=1; token=router-secret", "passkey": "router-passkey", "enabled": true, "priority": 100, "timeout_seconds": 12, "rate_limit_per_minute": 120}
	status, _ = client.request(t, http.MethodPost, "/api/v1/sites", payload, false)
	if status != http.StatusForbidden {
		t.Fatalf("site create without csrf status=%d", status)
	}
	status, envelope = client.request(t, http.MethodPost, "/api/v1/sites", payload, true)
	if status != http.StatusCreated || bytes.Contains(envelope.Data, []byte("router-secret")) || bytes.Contains(envelope.Data, []byte("router-passkey")) {
		t.Fatalf("site create status=%d data=%s", status, envelope.Data)
	}
	var created services.SiteSummary
	if err := json.Unmarshal(envelope.Data, &created); err != nil || created.ID == 0 || created.Health.Status != "online" {
		t.Fatalf("created site err=%v item=%+v", err, created)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/sites", nil, false)
	if status != http.StatusOK || client.lastHeader.Get("Cache-Control") != "no-store" || bytes.Contains(envelope.Data, []byte("router-secret")) || bytes.Contains(envelope.Data, []byte("router-passkey")) || bytes.Contains(envelope.Data, []byte("credential_ciphertext")) {
		t.Fatalf("site list status=%d cache=%q data=%s", status, client.lastHeader.Get("Cache-Control"), envelope.Data)
	}

	status, envelope = client.request(t, http.MethodGet, "/api/v1/discovery/pt-search?keyword=Seven%20Samurai&media_type=movie&year=1954&page=1", nil, false)
	if status != http.StatusOK || bytes.Contains(envelope.Data, []byte("torrent_id")) || bytes.Contains(envelope.Data, []byte("router-secret")) || !bytes.Contains(envelope.Data, []byte(`"token"`)) {
		t.Fatalf("PT search status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/discovery/torrent-search?keyword=Seven%20Samurai&media_type=movie&year=1954&page=1", nil, false)
	if status != http.StatusOK || bytes.Contains(envelope.Data, []byte("torrent_id")) || bytes.Contains(envelope.Data, []byte("router-secret")) || !bytes.Contains(envelope.Data, []byte(`"token"`)) {
		t.Fatalf("torrent search alias status=%d data=%s", status, envelope.Data)
	}
	var searchResult struct {
		Groups []services.SiteSearchGroup `json:"groups"`
	}
	if err := json.Unmarshal(envelope.Data, &searchResult); err != nil || len(searchResult.Groups) != 1 || len(searchResult.Groups[0].Items) != 1 {
		t.Fatalf("PT search envelope err=%v result=%+v", err, searchResult)
	}
	client.sites.SetMetadataSettings(nil)
	status, envelope = client.request(t, http.MethodPost, "/api/v1/discovery/pt-results/recognize", map[string]any{"result_token": searchResult.Groups[0].Items[0].Token}, true)
	if status != http.StatusOK || !bytes.Contains(envelope.Data, []byte(`"engine_version":"`+mediarecognition.EngineVersion+`"`)) || !bytes.Contains(envelope.Data, []byte(`"status":"unrecognized"`)) || !bytes.Contains(envelope.Data, []byte("tmdb_credential_unavailable")) {
		t.Fatalf("metadata-free recognition status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodPost, "/api/v1/discovery/pt-results/recognize", map[string]any{"result_token": "invalid"}, true)
	if status != http.StatusGone || !bytes.Contains(envelope.Data, []byte(services.CodeSiteResultExpired)) {
		t.Fatalf("invalid recognition token status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodPost, "/api/v1/discovery/torrent-results/tmdb-candidates", map[string]any{"result_token": "invalid", "title": "Seven Samurai", "media_type": "movie"}, true)
	if status != http.StatusGone || client.lastHeader.Get("Cache-Control") != "no-store" || !bytes.Contains(envelope.Data, []byte(services.CodeSiteResultExpired)) {
		t.Fatalf("invalid manual candidate token status=%d cache=%q data=%s", status, client.lastHeader.Get("Cache-Control"), envelope.Data)
	}
	status, envelope = client.request(t, http.MethodPut, "/api/v1/discovery/torrent-results/recognition-override", map[string]any{"result_token": "invalid", "tmdb_id": 346, "media_type": "movie"}, true)
	if status != http.StatusGone || client.lastHeader.Get("Cache-Control") != "no-store" || !bytes.Contains(envelope.Data, []byte(services.CodeSiteResultExpired)) {
		t.Fatalf("invalid manual override token status=%d cache=%q data=%s", status, client.lastHeader.Get("Cache-Control"), envelope.Data)
	}

	streamRequest := httptest.NewRequest(http.MethodGet, "/api/v1/discovery/pt-search/stream?keyword=Seven%20Samurai&page=1", nil)
	streamRequest.Header.Set("Origin", "http://localhost:3000")
	streamRequest.AddCookie(client.cookie)
	streamResponse := httptest.NewRecorder()
	client.router.ServeHTTP(streamResponse, streamRequest)
	streamBody := streamResponse.Body.String()
	if streamResponse.Code != http.StatusOK || !strings.HasPrefix(streamResponse.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(streamBody, "event: site") || !strings.Contains(streamBody, "event: done") || strings.Contains(streamBody, "router-secret") || strings.Contains(streamBody, "torrent_id") {
		t.Fatalf("SSE status=%d type=%q body=%q", streamResponse.Code, streamResponse.Header().Get("Content-Type"), streamBody)
	}

	status, envelope = client.request(t, http.MethodPost, "/api/v1/discovery/downloads", map[string]any{"result_token": "invalid", "downloader_id": "none", "media_library_id": 0}, true)
	if status != http.StatusGone || !bytes.Contains(envelope.Data, []byte(services.CodeSiteResultExpired)) {
		t.Fatalf("invalid discovery token status=%d data=%s", status, envelope.Data)
	}
	status, _ = client.request(t, http.MethodDelete, "/api/v1/sites/"+uintString(created.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("delete site status=%d", status)
	}
}

func TestMediaIdentityDiscoveryRoutesValidateInputAndEnforceCoveragePermissions(t *testing.T) {
	owner := newTestClient(t)
	unauthenticated := httptest.NewRecorder()
	owner.router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/discovery/media-search?query=Seven%20Samurai", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated media search status=%d", unauthenticated.Code)
	}

	owner.setup(t)
	for _, path := range []string{
		"/api/v1/discovery/media-search",
		"/api/v1/discovery/media-search?query=Seven%20Samurai&media_type=person",
		"/api/v1/discovery/media-search?query=Seven%20Samurai&page=invalid",
		"/api/v1/discovery/media/tv/invalid/coverage",
		"/api/v1/discovery/media/person/100/torrent-search",
		"/api/v1/discovery/media/tv/100/torrent-search?page=invalid",
		"/api/v1/discovery/media/movie/100/torrent-search?season=1",
		"/api/v1/discovery/media/tv/100/torrent-search/stream?site_id=invalid",
	} {
		status, envelope := owner.request(t, http.MethodGet, path, nil, false)
		if status != http.StatusBadRequest || !bytes.Contains(envelope.Data, []byte(services.CodeInvalidRequest)) || owner.lastHeader.Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid identity route %q status=%d cache=%q data=%s", path, status, owner.lastHeader.Get("Cache-Control"), envelope.Data)
		}
	}

	for _, path := range []string{
		"/api/v1/discovery/media-search?query=Seven%20Samurai&media_type=movie&page=1",
		"/api/v1/discovery/media/movie/346/coverage",
		"/api/v1/discovery/media/movie/346/torrent-search",
		"/api/v1/discovery/media/movie/346/torrent-search/stream",
	} {
		status, envelope := owner.request(t, http.MethodGet, path, nil, false)
		if status != http.StatusServiceUnavailable || owner.lastHeader.Get("Cache-Control") != "no-store" {
			t.Fatalf("metadata-unavailable identity route %q status=%d cache=%q data=%s", path, status, owner.lastHeader.Get("Cache-Control"), envelope.Data)
		}
		body := string(envelope.Data)
		for _, forbidden := range []string{"torrent_id", "magnet:", "passkey", "cookie", "relative_path", "provider_id", "root_path"} {
			if strings.Contains(strings.ToLower(body), forbidden) {
				t.Fatalf("identity route %q leaked %q: %s", path, forbidden, body)
			}
		}
	}

	status, roleEnvelope := owner.request(t, http.MethodPost, "/api/v1/roles", map[string]any{"code": "discovery_only", "name": "Discovery only", "permissions": []string{authz.PermissionDiscoveryRead}}, true)
	if status != http.StatusCreated {
		t.Fatalf("create discovery role status=%d data=%s", status, roleEnvelope.Data)
	}
	var role struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(roleEnvelope.Data, &role); err != nil || role.ID == 0 {
		t.Fatalf("discovery role=%+v err=%v", role, err)
	}
	status, userEnvelope := owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "discovery-user", "display_name": "Discovery User", "password": "discovery-user-strong-password", "role_ids": []uint{role.ID}}, true)
	if status != http.StatusCreated {
		t.Fatalf("create discovery user status=%d data=%s", status, userEnvelope.Data)
	}
	discoveryOnly := newTestClientWithRouter(owner.router)
	discoveryOnly.login(t, "discovery-user", "discovery-user-strong-password")
	status, _ = discoveryOnly.request(t, http.MethodGet, "/api/v1/discovery/media/movie/346/coverage", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("coverage without media_libraries.read status=%d", status)
	}
	status, _ = discoveryOnly.request(t, http.MethodGet, "/api/v1/discovery/media-search?query=Seven%20Samurai", nil, false)
	if status == http.StatusForbidden {
		t.Fatalf("discovery-only media search was incorrectly denied")
	}
	status, _ = discoveryOnly.request(t, http.MethodGet, "/api/v1/discovery/media/movie/346/torrent-search", nil, false)
	if status == http.StatusForbidden {
		t.Fatalf("discovery-only identity torrent search was incorrectly denied")
	}
}

func TestPluginRepositoryAPIsRequireAuthUseNoStoreAndRejectRawURLs(t *testing.T) {
	client := newTestClient(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/plugin-repositories", nil)
	response := httptest.NewRecorder()
	client.router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("denied repository response status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}

	client.setup(t)
	status, envelope := client.request(t, http.MethodPost, "/api/v1/plugin-repositories", map[string]any{"github_url": "https://raw.githubusercontent.com/owner/plugins/main/index.json"}, true)
	if status != http.StatusBadRequest || !bytes.Contains(envelope.Data, []byte(services.CodePluginRepositoryURLInvalid)) {
		t.Fatalf("unsafe URL status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodPost, "/api/v1/plugin-repositories", map[string]any{"github_url": "https://github.com/Owner/Plugins", "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create repository status=%d message=%s", status, envelope.Message)
	}
	var created services.PluginRepositorySummary
	if err := json.Unmarshal(envelope.Data, &created); err != nil {
		t.Fatal(err)
	}
	if created.GitHubURL != "https://github.com/owner/plugins" || created.Revision != 1 {
		t.Fatalf("created=%+v", created)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/plugin-repositories", nil, false)
	if status != http.StatusOK || client.lastHeader.Get("Cache-Control") != "no-store" || bytes.Contains(envelope.Data, []byte("cached_registry_json")) || bytes.Contains(envelope.Data, []byte("github_owner")) {
		t.Fatalf("list status=%d cache=%q data=%s", status, client.lastHeader.Get("Cache-Control"), envelope.Data)
	}
	status, envelope = client.request(t, http.MethodPatch, "/api/v1/plugin-repositories/"+uintString(created.ID), map[string]any{"enabled": false, "revision": created.Revision + 1}, true)
	if status != http.StatusConflict || !bytes.Contains(envelope.Data, []byte(services.CodePluginRepositoryRevision)) {
		t.Fatalf("stale update status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/plugins/installed", nil, false)
	if status != http.StatusOK || !bytes.Contains(envelope.Data, []byte(`"runtime_status":"unavailable"`)) {
		t.Fatalf("installed status=%d data=%s", status, envelope.Data)
	}
}

func TestConnectionAPIStoresButNeverReturnsPan115Cookie(t *testing.T) {
	client := newTestClient(t)
	denied := httptest.NewRecorder()
	client.router.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v1/connections", nil))
	if denied.Code != http.StatusUnauthorized || denied.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("denied connection response status=%d cache=%q", denied.Code, denied.Header().Get("Cache-Control"))
	}
	client.setup(t)
	cookie := "UID=router_A1; CID=router-cid; SEID=router-secret; KID=router-kid"
	status, envelope := client.request(t, http.MethodPost, "/api/v1/connections", map[string]any{"name": "115 主账号", "provider": "pan115", "cookie": cookie, "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create connection status=%d message=%s", status, envelope.Message)
	}
	if strings.Contains(string(envelope.Data), "router-secret") || strings.Contains(strings.ToLower(string(envelope.Data)), "cookie") {
		t.Fatalf("create response exposed cookie: %s", envelope.Data)
	}
	var created services.ConnectionSummary
	if err := json.Unmarshal(envelope.Data, &created); err != nil {
		t.Fatal(err)
	}
	status, envelope = client.request(t, http.MethodPatch, "/api/v1/connections/"+uintString(created.ID), map[string]any{"name": "115 主账号", "revision": created.Revision}, true)
	if status != http.StatusOK || strings.Contains(string(envelope.Data), "router-secret") {
		t.Fatalf("update connection status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/connections", nil, false)
	if status != http.StatusOK || client.lastHeader.Get("Cache-Control") != "no-store" || strings.Contains(string(envelope.Data), "router-secret") || strings.Contains(strings.ToLower(string(envelope.Data)), "cipher") {
		t.Fatalf("list response status=%d cache=%q data=%s", status, client.lastHeader.Get("Cache-Control"), envelope.Data)
	}
	status, _ = client.request(t, http.MethodDelete, "/api/v1/connections/"+uintString(created.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("delete connection status=%d", status)
	}
}

func TestPan115DirectoryPickerCreatesStableCloudStorage(t *testing.T) {
	client := newTestClient(t)
	client.setup(t)
	status, envelope := client.request(t, http.MethodPost, "/api/v1/connections", map[string]any{"name": "115 account", "provider": "pan115", "cookie": "UID=test_A1; CID=test-cid; SEID=test-secret", "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create connection status=%d message=%s", status, envelope.Message)
	}
	var connection services.ConnectionSummary
	if err := json.Unmarshal(envelope.Data, &connection); err != nil {
		t.Fatal(err)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/connections/"+uintString(connection.ID)+"/directories", nil, false)
	if status != http.StatusOK || client.lastHeader.Get("Cache-Control") != "no-store" {
		t.Fatalf("browse status=%d cache=%q", status, client.lastHeader.Get("Cache-Control"))
	}
	var listing services.DirectoryListing
	if err := json.Unmarshal(envelope.Data, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 || listing.Items[0].SelectionToken == "" {
		t.Fatalf("listing=%+v", listing)
	}
	status, envelope = client.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "115 media", "type": "pan115", "connection_id": connection.ID, "provider_picker_token": listing.Items[0].SelectionToken, "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create storage status=%d message=%s data=%s", status, envelope.Message, envelope.Data)
	}
	var storage services.StorageSummary
	if err := json.Unmarshal(envelope.Data, &storage); err != nil {
		t.Fatal(err)
	}
	if storage.Type != models.StorageTypePan115 || storage.RootPath != "root-1" || storage.RootDisplayPath != "/媒体" || storage.ConnectionID == nil || *storage.ConnectionID != connection.ID {
		t.Fatalf("storage=%+v", storage)
	}
	if strings.Contains(string(envelope.Data), "test-secret") || strings.Contains(strings.ToLower(string(envelope.Data)), "cookie") {
		t.Fatalf("storage response exposed credential: %s", envelope.Data)
	}
}

func TestDownloaderAndDownloadAPIRedactSensitiveSources(t *testing.T) {
	client := newTestClient(t)
	deniedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/downloaders", nil)
	deniedResponse := httptest.NewRecorder()
	client.router.ServeHTTP(deniedResponse, deniedRequest)
	if deniedResponse.Code != http.StatusUnauthorized || deniedResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("denied downloader response status=%d cache=%q", deniedResponse.Code, deniedResponse.Header().Get("Cache-Control"))
	}
	client.setup(t)
	status, envelope := client.request(t, http.MethodPost, "/api/v1/downloaders", map[string]any{"name": "Fake downloader", "type": "fake", "enabled": true, "username": "hidden-user", "password": "hidden-password"}, true)
	if status != http.StatusCreated {
		t.Fatalf("create downloader status=%d body=%s", status, envelope.Data)
	}
	if strings.Contains(string(envelope.Data), "hidden-user") || strings.Contains(string(envelope.Data), "hidden-password") {
		t.Fatal("downloader response leaked credentials")
	}
	var downloader struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(envelope.Data, &downloader); err != nil || downloader.ID == "" {
		t.Fatalf("downloader=%+v err=%v", downloader, err)
	}
	source := "https://tracker.example.test/download?id=1&passkey=secret-passkey"
	status, envelope = client.request(t, http.MethodPost, "/api/v1/downloads", map[string]any{"downloader_id": downloader.ID, "display_name": "API download", "source_kind": "url", "source_url": source}, true)
	if status != http.StatusCreated {
		t.Fatalf("create download status=%d body=%s", status, envelope.Data)
	}
	if strings.Contains(string(envelope.Data), "tracker.example") || strings.Contains(string(envelope.Data), "secret-passkey") {
		t.Fatal("download response leaked source")
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/downloads", nil, false)
	if status != http.StatusOK || strings.Contains(string(envelope.Data), "secret-passkey") {
		t.Fatalf("list status=%d body=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodPost, "/api/v1/downloads", map[string]any{"downloader_id": downloader.ID, "source_kind": "url", "source_url": "file:///private/media"}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid source status=%d body=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodPut, "/api/v1/downloads/missing/import-target", map[string]any{"media_library_id": 1}, true)
	if status != http.StatusNotFound || !bytes.Contains(envelope.Data, []byte(services.CodeNotFound)) {
		t.Fatalf("retarget route status=%d body=%s", status, envelope.Data)
	}
}

func TestTerminalDownloadCanBeDeletedAndActiveDownloadIsRetained(t *testing.T) {
	client := newTestClient(t)
	client.setup(t)
	status, envelope := client.request(t, http.MethodPost, "/api/v1/downloaders", map[string]any{"name": "Delete fake downloader", "type": "fake", "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create downloader status=%d body=%s", status, envelope.Data)
	}
	var provider struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(envelope.Data, &provider); err != nil || provider.ID == "" {
		t.Fatalf("provider=%+v err=%v", provider, err)
	}

	create := func(name string) services.DownloadTaskSummary {
		t.Helper()
		status, envelope := client.request(t, http.MethodPost, "/api/v1/downloads", map[string]any{"downloader_id": provider.ID, "display_name": name, "source_kind": "url", "source_url": "magnet:?xt=urn:btih:" + strings.ToLower(strings.ReplaceAll(name, " ", "-"))}, true)
		if status != http.StatusCreated {
			t.Fatalf("create download %q status=%d body=%s", name, status, envelope.Data)
		}
		var task services.DownloadTaskSummary
		if err := json.Unmarshal(envelope.Data, &task); err != nil || task.ID == "" || task.JobID == "" {
			t.Fatalf("task=%+v err=%v", task, err)
		}
		return task
	}

	failed := create("Failed delete")
	claimed, err := client.queue.Claim([]string{"download"})
	if err != nil || claimed == nil || claimed.Job.ID != failed.JobID {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := client.queue.Fail(failed.JobID, claimed.LeaseToken, "download_failed", "下载任务执行失败"); err != nil {
		t.Fatal(err)
	}
	status, envelope = client.request(t, http.MethodDelete, "/api/v1/downloads/"+failed.ID, map[string]any{}, true)
	if status != http.StatusOK || strings.Contains(string(envelope.Data), "magnet") {
		t.Fatalf("delete status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/downloads", nil, false)
	if status != http.StatusOK || strings.Contains(string(envelope.Data), failed.ID) || strings.Contains(string(envelope.Data), failed.JobID) {
		t.Fatalf("deleted task remained status=%d data=%s", status, envelope.Data)
	}

	active := create("Active retained")
	claimed, err = client.queue.Claim([]string{"download"})
	if err != nil || claimed == nil || claimed.Job.ID != active.JobID {
		t.Fatalf("active claim=%+v err=%v", claimed, err)
	}
	status, envelope = client.request(t, http.MethodDelete, "/api/v1/downloads/"+active.ID, map[string]any{}, true)
	if status != http.StatusConflict {
		t.Fatalf("active delete status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/downloads", nil, false)
	if status != http.StatusOK || !strings.Contains(string(envelope.Data), active.ID) {
		t.Fatalf("active task was removed status=%d data=%s", status, envelope.Data)
	}
}

func TestWaitingProviderDownloadCancelPersistsIntentThroughHTTP(t *testing.T) {
	client := newTestClient(t)
	setup := client.setup(t)
	user, ok := setup["user"].(map[string]any)
	if !ok {
		t.Fatalf("setup user=%#v", setup["user"])
	}
	ownerID := uint(user["id"].(float64))
	job, err := client.queue.Enqueue(services.EnqueueJobInput{OwnerID: ownerID, JobType: "download", Provider: models.DownloaderTypeQBittorrent, DisplayName: "HTTP pending cancel", Payload: map[string]any{"download_task_id": "http-task"}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := client.queue.Claim([]string{"download"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := client.queue.Wait(job.ID, claimed.LeaseToken, services.WaitForAction{ActionType: "import_conflict", Prompt: "Choose", Options: []string{"overwrite", "skip"}, Checkpoint: map[string]any{"stage": "import"}}); err != nil {
		t.Fatal(err)
	}
	status, envelope := client.request(t, http.MethodPost, "/api/v1/jobs/"+job.ID+"/cancel", map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("cancel status=%d data=%s", status, envelope.Data)
	}
	var controlled services.JobDTO
	if err := json.Unmarshal(envelope.Data, &controlled); err != nil {
		t.Fatal(err)
	}
	if controlled.Status != models.JobStatusQueued || controlled.InterruptPending != models.JobStatusCancelled || !controlled.CancellationRequested || controlled.Action != nil {
		t.Fatalf("controlled=%+v", controlled)
	}
}

func TestUnifiedDownloadSettingsUseGlobalDirectoryTokenAndShowConfiguredPath(t *testing.T) {
	client := newTestClient(t)
	deniedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/settings/downloads", nil)
	deniedResponse := httptest.NewRecorder()
	client.router.ServeHTTP(deniedResponse, deniedRequest)
	if deniedResponse.Code != http.StatusUnauthorized || deniedResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("denied settings status=%d cache=%q", deniedResponse.Code, deniedResponse.Header().Get("Cache-Control"))
	}
	client.setup(t)
	root := t.TempDir()
	status, envelope := client.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "Unified staging", "type": "local", "root_path": root, "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("storage status=%d message=%s", status, envelope.Message)
	}
	var storage struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(envelope.Data, &storage); err != nil || storage.ID == 0 {
		t.Fatalf("storage=%+v err=%v", storage, err)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/storages/"+uintString(storage.ID)+"/directory", nil, false)
	if status != http.StatusOK {
		t.Fatalf("directory status=%d message=%s", status, envelope.Message)
	}
	var directory struct {
		SelectionToken string `json:"current_selection_token"`
	}
	if err := json.Unmarshal(envelope.Data, &directory); err != nil || directory.SelectionToken == "" {
		t.Fatalf("directory=%+v err=%v", directory, err)
	}
	status, envelope = client.request(t, http.MethodPatch, "/api/v1/settings/downloads", map[string]any{"directory_token": directory.SelectionToken, "revision": 1}, true)
	if status != http.StatusOK {
		t.Fatalf("settings status=%d data=%s", status, envelope.Data)
	}
	var settings struct {
		Configured   bool   `json:"configured"`
		AbsolutePath string `json:"absolute_path"`
	}
	if err := json.Unmarshal(envelope.Data, &settings); err != nil || !settings.Configured || settings.AbsolutePath != root {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/settings/downloads", nil, false)
	if status != http.StatusOK {
		t.Fatalf("get settings status=%d data=%s", status, envelope.Data)
	}
	if err := json.Unmarshal(envelope.Data, &settings); err != nil || settings.AbsolutePath != root {
		t.Fatalf("get settings=%+v err=%v", settings, err)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/settings/downloads/directory", nil, false)
	if status != http.StatusOK || client.lastHeader.Get("Cache-Control") != "no-store" {
		t.Fatalf("configured directory status=%d data=%s", status, envelope.Data)
	}
	var current struct {
		Location string `json:"location"`
	}
	if err := json.Unmarshal(envelope.Data, &current); err != nil || current.Location != root {
		t.Fatalf("configured directory=%+v err=%v", current, err)
	}
	status, envelope = client.request(t, http.MethodPatch, "/api/v1/settings/downloads", map[string]any{"absolute_path": root, "revision": 2}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("client-authored staging path status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/settings/downloads", nil, false)
	if status != http.StatusOK || json.Unmarshal(envelope.Data, &settings) != nil || settings.AbsolutePath != root {
		t.Fatalf("rejected absolute path changed settings status=%d settings=%+v", status, settings)
	}
}

func TestMetadataSettingsNeverEchoTMDBToken(t *testing.T) {
	client := newTestClient(t)
	denied := httptest.NewRecorder()
	client.router.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v1/settings/metadata", nil))
	if denied.Code != http.StatusUnauthorized || denied.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated metadata status=%d cache=%q", denied.Code, denied.Header().Get("Cache-Control"))
	}
	client.setup(t)
	const token = "eyJhbGciOiJIUzI1NiJ9.router-secret-token"
	status, envelope := client.request(t, http.MethodPatch, "/api/v1/settings/metadata", map[string]any{"tmdb_token": token, "credential_kind": "automatic", "clear_tmdb": false, "revision": 1}, true)
	if status != http.StatusBadRequest || strings.Contains(string(envelope.Data), token) || strings.Contains(string(envelope.Data), "router-secret-token") || strings.Contains(envelope.Message, token) {
		t.Fatalf("metadata patch status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/settings/metadata", nil, false)
	var settings struct {
		Configured       bool   `json:"tmdb_configured"`
		CustomConfigured bool   `json:"custom_configured"`
		CredentialSource string `json:"credential_source"`
		CredentialKind   string `json:"credential_kind"`
		APIBaseURL       string `json:"api_base_url"`
		Revision         uint64 `json:"revision"`
	}
	if err := json.Unmarshal(envelope.Data, &settings); status != http.StatusOK || err != nil || settings.Configured || settings.CustomConfigured || settings.CredentialSource != "none" || settings.CredentialKind != "" || settings.Revision != 1 {
		t.Fatalf("metadata settings status=%d settings=%+v err=%v", status, settings, err)
	}
	status, oversized := client.request(t, http.MethodPost, "/api/v1/settings/metadata/test-api", map[string]any{"base_url": "https://" + strings.Repeat("a", 20<<10) + ".example.test/3", "revision": 1}, true)
	if status != http.StatusBadRequest || strings.Contains(string(oversized.Data), token) {
		t.Fatalf("oversized metadata route status=%d data=%s", status, oversized.Data)
	}
	status, routeFailure := client.request(t, http.MethodPost, "/api/v1/settings/metadata/test-api", map[string]any{"base_url": "http://unsafe.example/3", "revision": 1}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("unsafe route status=%d data=%s", status, routeFailure.Data)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/settings/metadata", nil, false)
	var afterFailure struct {
		APIBaseURL string `json:"api_base_url"`
		Revision   uint64 `json:"revision"`
	}
	if err := json.Unmarshal(envelope.Data, &afterFailure); status != http.StatusOK || err != nil || afterFailure.APIBaseURL != settings.APIBaseURL || afterFailure.Revision != 1 {
		t.Fatalf("failed route changed metadata status=%d settings=%+v err=%v", status, afterFailure, err)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/settings/metadata", nil, false)
	if status != http.StatusOK || strings.Contains(string(envelope.Data), token) {
		t.Fatalf("metadata get status=%d data=%s", status, envelope.Data)
	}
}

func TestAIRecognitionSettingsAreNoStoreOptInAndNeverEchoAPIKey(t *testing.T) {
	client := newTestClient(t)
	denied := httptest.NewRecorder()
	client.router.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v1/settings/ai-recognition", nil))
	if denied.Code != http.StatusUnauthorized || denied.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated AI settings status=%d cache=%q", denied.Code, denied.Header().Get("Cache-Control"))
	}
	client.setup(t)
	status, envelope := client.request(t, http.MethodGet, "/api/v1/settings/ai-recognition", nil, false)
	var initial struct {
		Enabled    bool   `json:"enabled"`
		Configured bool   `json:"api_key_configured"`
		Provider   string `json:"provider_type"`
		Revision   uint64 `json:"revision"`
	}
	if err := json.Unmarshal(envelope.Data, &initial); status != http.StatusOK || err != nil || initial.Enabled || initial.Configured || initial.Provider != "openai_compatible" || initial.Revision != 1 {
		t.Fatalf("initial status=%d value=%+v err=%v", status, initial, err)
	}
	const secret = "router-ai-secret"
	status, envelope = client.request(t, http.MethodPatch, "/api/v1/settings/ai-recognition", map[string]any{"enabled": false, "provider_type": "openai_compatible", "base_url": "https://api.example.com", "api_key": secret, "model": "fixture-model", "send_relative_basenames": false, "revision": 1}, true)
	if status != http.StatusOK || strings.Contains(string(envelope.Data), secret) {
		t.Fatalf("AI patch status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodGet, "/api/v1/settings/ai-recognition", nil, false)
	if status != http.StatusOK || strings.Contains(string(envelope.Data), secret) || !bytes.Contains(envelope.Data, []byte(`"api_key_configured":true`)) || !bytes.Contains(envelope.Data, []byte(`"enabled":false`)) {
		t.Fatalf("AI get status=%d data=%s", status, envelope.Data)
	}
	status, envelope = client.request(t, http.MethodPost, "/api/v1/credentials/reveal", map[string]any{"resource_type": "ai_recognition", "resource_id": "1", "field": "api_key"}, true)
	if status != http.StatusOK || !bytes.Contains(envelope.Data, []byte(secret)) || client.lastHeader.Get("Cache-Control") != "no-store" {
		t.Fatalf("AI reveal status=%d cache=%q data=%s", status, client.lastHeader.Get("Cache-Control"), envelope.Data)
	}
}

func TestSeedingSettingsDefaultSafeAndRevisioned(t *testing.T) {
	client := newTestClient(t)
	denied := httptest.NewRecorder()
	client.router.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v1/settings/seeding", nil))
	if denied.Code != http.StatusUnauthorized || denied.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d cache=%q", denied.Code, denied.Header().Get("Cache-Control"))
	}
	client.setup(t)
	status, envelope := client.request(t, http.MethodGet, "/api/v1/settings/seeding", nil, false)
	var settings struct {
		Enabled  bool    `json:"enabled"`
		Minutes  int     `json:"minimum_seed_minutes"`
		Ratio    float64 `json:"minimum_ratio"`
		Mode     string  `json:"completion_mode"`
		Revision uint64  `json:"revision"`
	}
	if status != http.StatusOK || json.Unmarshal(envelope.Data, &settings) != nil || settings.Enabled || settings.Minutes != 1440 || settings.Ratio != 1 || settings.Mode != "all" || settings.Revision != 1 {
		t.Fatalf("status=%d settings=%+v", status, settings)
	}
	status, _ = client.request(t, http.MethodPatch, "/api/v1/settings/seeding", map[string]any{"enabled": true, "minimum_seed_minutes": 0, "minimum_ratio": 0, "completion_mode": "all", "revision": 1}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("empty thresholds status=%d", status)
	}
	status, envelope = client.request(t, http.MethodPatch, "/api/v1/settings/seeding", map[string]any{"enabled": true, "minimum_seed_minutes": 720, "minimum_ratio": 1.5, "completion_mode": "any", "revision": 1}, true)
	if status != http.StatusOK || json.Unmarshal(envelope.Data, &settings) != nil || !settings.Enabled || settings.Revision != 2 || settings.Mode != "any" {
		t.Fatalf("status=%d settings=%+v", status, settings)
	}
	status, _ = client.request(t, http.MethodPatch, "/api/v1/settings/seeding", map[string]any{"enabled": false, "minimum_seed_minutes": 0, "minimum_ratio": 0, "completion_mode": "all", "revision": 1}, true)
	if status != http.StatusConflict {
		t.Fatalf("stale revision status=%d", status)
	}
}

func (c *testClient) login(t *testing.T, username, password string) {
	status, envelope := c.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": username, "password": password}, false)
	if status != http.StatusOK {
		t.Fatalf("login status=%d message=%s", status, envelope.Message)
	}
	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	c.csrf, _ = data["csrf_token"].(string)
}

func TestSetupSessionAndViewerPermissionBoundary(t *testing.T) {
	client := newTestClient(t)
	setup := client.setup(t)
	status, transfers := client.request(t, http.MethodGet, "/api/v1/transfers?page=1&page_size=20", nil, false)
	if status != http.StatusOK || !bytes.Contains(transfers.Data, []byte(`"stats"`)) || client.lastHeader.Get("Cache-Control") != "no-store" {
		t.Fatalf("owner transfers status=%d cache=%q data=%s", status, client.lastHeader.Get("Cache-Control"), transfers.Data)
	}
	status, _ = client.request(t, http.MethodDelete, "/api/v1/transfers/missing", map[string]any{}, false)
	if status != http.StatusForbidden {
		t.Fatalf("transfer delete without csrf status=%d", status)
	}
	status, _ = client.request(t, http.MethodDelete, "/api/v1/transfers/missing", map[string]any{}, true)
	if status != http.StatusNotFound {
		t.Fatalf("missing transfer delete status=%d", status)
	}
	for _, path := range []string{
		"/api/v1/transfers?status=unknown",
		"/api/v1/transfers?scope=unknown",
		"/api/v1/transfers?transfer_mode=hardlink",
		"/api/v1/transfers?page=0",
		"/api/v1/transfers?page_size=201",
		"/api/v1/transfers?library_id=invalid",
	} {
		status, _ = client.request(t, http.MethodGet, path, nil, false)
		if status != http.StatusBadRequest || client.lastHeader.Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid transfer filter %q status=%d cache=%q", path, status, client.lastHeader.Get("Cache-Control"))
		}
	}
	for _, path := range []string{"/api/v1/downloads?scope=unknown", "/api/v1/downloads?limit=0", "/api/v1/downloads?limit=invalid"} {
		status, _ = client.request(t, http.MethodGet, path, nil, false)
		if status != http.StatusBadRequest {
			t.Fatalf("invalid download filter %q status=%d", path, status)
		}
	}
	status, _ = client.request(t, http.MethodPost, "/api/v1/setup/owner", map[string]any{"username": "other", "password": "another-strong-password"}, false)
	if status != http.StatusConflict {
		t.Fatalf("second setup status=%d", status)
	}

	status, rolesEnvelope := client.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var rolesData struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	if err := json.Unmarshal(rolesEnvelope.Data, &rolesData); err != nil {
		t.Fatal(err)
	}
	var viewerID uint
	for _, role := range rolesData.List {
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	if viewerID == 0 {
		t.Fatal("viewer role missing")
	}
	status, _ = client.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "viewer", "display_name": "Viewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatalf("create viewer status=%d", status)
	}

	viewer := newTestClientWithRouter(client.router)
	viewer.login(t, "viewer", "viewer-strong-password")
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/dashboard", nil, false)
	if status != http.StatusOK {
		t.Fatalf("viewer dashboard status=%d", status)
	}
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/users", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer users status=%d", status)
	}
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/transfers", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer transfers status=%d", status)
	}
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/player-devices", nil, false)
	if status != http.StatusOK {
		t.Fatalf("viewer Player devices status=%d", status)
	}
	status, _ = viewer.request(t, http.MethodDelete, "/api/v1/player-devices/missing", map[string]any{}, true)
	if status != http.StatusForbidden {
		t.Fatalf("viewer Player device revoke status=%d", status)
	}
	status, _ = viewer.request(t, http.MethodDelete, "/api/v1/transfers/missing", map[string]any{}, true)
	if status != http.StatusForbidden {
		t.Fatalf("viewer transfer delete status=%d", status)
	}
	status, metadataDenied := viewer.request(t, http.MethodGet, "/api/v1/settings/metadata", nil, false)
	if status != http.StatusForbidden || metadataDenied.Message == "" {
		t.Fatalf("viewer metadata status=%d response=%+v", status, metadataDenied)
	}
	status, _ = viewer.request(t, http.MethodPost, "/api/v1/settings/metadata/test-api", map[string]any{"base_url": "https://api.example.test/3", "revision": 1}, true)
	if status != http.StatusForbidden {
		t.Fatalf("viewer metadata mutation status=%d", status)
	}
	status, _ = viewer.request(t, http.MethodPost, "/api/v1/settings/metadata/test-token", map[string]any{"tmdb_token": "must-not-be-tested", "revision": 1}, true)
	if status != http.StatusForbidden {
		t.Fatalf("viewer metadata token test status=%d", status)
	}

	user := setup["user"].(map[string]any)
	ownerID := uint(user["id"].(float64))
	status, _ = client.request(t, http.MethodPost, "/api/v1/users/"+uintString(ownerID)+"/disable", map[string]any{}, true)
	if status != http.StatusForbidden {
		t.Fatalf("owner self-disable status=%d", status)
	}
	status, _ = client.request(t, http.MethodPost, "/api/v1/auth/logout", map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("logout status=%d", status)
	}
	status, _ = client.request(t, http.MethodGet, "/api/v1/auth/me", nil, false)
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked session status=%d", status)
	}
}

func TestDelegatedRoleCannotEscalatePrivileges(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	status, roleEnvelope := owner.request(t, http.MethodPost, "/api/v1/roles", map[string]any{"code": "role_builder", "name": "Role Builder", "permissions": []string{"roles.read", "roles.create"}}, true)
	if status != http.StatusCreated {
		t.Fatalf("create role status=%d message=%s", status, roleEnvelope.Message)
	}
	var role struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(roleEnvelope.Data, &role); err != nil {
		t.Fatal(err)
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "delegate", "display_name": "Delegate", "password": "delegate-strong-password", "role_ids": []uint{role.ID}}, true)
	if status != http.StatusCreated {
		t.Fatalf("create delegate status=%d", status)
	}
	delegate := newTestClientWithRouter(owner.router)
	delegate.login(t, "delegate", "delegate-strong-password")
	status, _ = delegate.request(t, http.MethodPost, "/api/v1/roles", map[string]any{"code": "escalated", "name": "Escalated", "permissions": []string{"system.admin"}}, true)
	if status != http.StatusForbidden {
		t.Fatalf("privilege escalation status=%d", status)
	}
}

func TestStorageCRUDRBACAndDeleteLeavesFilesUntouched(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	root := t.TempDir()
	marker := filepath.Join(root, "nested", "movie.mp4")
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, envelope := owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": " Local  Media ", "type": "local", "root_path": root, "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create storage status=%d message=%s", status, envelope.Message)
	}
	var created struct {
		ID       uint   `json:"id"`
		RootPath string `json:"root_path"`
		Probe    struct {
			Readable  bool    `json:"readable"`
			FreeBytes *uint64 `json:"free_bytes"`
		} `json:"probe"`
	}
	if err := json.Unmarshal(envelope.Data, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.RootPath != filepath.Clean(root) || !created.Probe.Readable || created.Probe.FreeBytes == nil {
		t.Fatalf("unexpected storage: %+v", created)
	}

	status, envelope = owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "local media", "type": "local", "root_path": t.TempDir(), "enabled": true}, true)
	if status != http.StatusConflict {
		t.Fatalf("duplicate name status=%d code=%s", status, envelope.Data)
	}
	status, envelope = owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "Relative", "type": "local", "root_path": "relative", "enabled": true}, true)
	if status != http.StatusBadRequest || !bytes.Contains(envelope.Data, []byte("storage_path_not_absolute")) {
		t.Fatalf("relative path status=%d data=%s", status, envelope.Data)
	}

	status, _ = owner.request(t, http.MethodPost, "/api/v1/storages/"+uintString(created.ID)+"/test", map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("test storage status=%d", status)
	}
	status, _ = owner.request(t, http.MethodDelete, "/api/v1/storages/"+uintString(created.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("delete storage status=%d", status)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "media" {
		t.Fatalf("media changed after config deletion: %q %v", content, err)
	}
	status, auditEnvelope := owner.request(t, http.MethodGet, "/api/v1/audit?limit=20", nil, false)
	if status != http.StatusOK {
		t.Fatalf("audit status=%d", status)
	}
	if bytes.Contains(auditEnvelope.Data, []byte(root)) || bytes.Contains(auditEnvelope.Data, []byte("movie.mp4")) {
		t.Fatalf("audit response leaked local path or child filename: %s", auditEnvelope.Data)
	}

	status, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var rolesData struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	if err := json.Unmarshal(rolesEnvelope.Data, &rolesData); err != nil {
		t.Fatal(err)
	}
	var viewerID uint
	for _, role := range rolesData.List {
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "storageviewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "storageviewer", "viewer-strong-password")
	deniedRequests := []struct {
		method string
		path   string
		body   any
		csrf   bool
	}{
		{http.MethodGet, "/api/v1/storages", nil, false},
		{http.MethodPost, "/api/v1/storages", map[string]any{"name": "Denied", "root_path": root}, true},
		{http.MethodPatch, "/api/v1/storages/1", map[string]any{"enabled": false}, true},
		{http.MethodDelete, "/api/v1/storages/1", map[string]any{}, true},
		{http.MethodPost, "/api/v1/storages/1/test", map[string]any{}, true},
	}
	for _, request := range deniedRequests {
		status, envelope = viewer.request(t, request.method, request.path, request.body, request.csrf)
		if status != http.StatusForbidden || !bytes.Contains(envelope.Data, []byte(services.CodePermissionDenied)) {
			t.Fatalf("viewer %s %s status=%d data=%s", request.method, request.path, status, envelope.Data)
		}
	}
}

func TestMediaLibraryAPICRUDRBACAndAutomaticInitialization(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "API.Movie.mp4"), []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, storageEnvelope := owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "Library source", "type": "local", "root_path": root, "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create storage status=%d message=%s", status, storageEnvelope.Message)
	}
	var storage struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(storageEnvelope.Data, &storage); err != nil {
		t.Fatal(err)
	}
	status, profilesEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var profiles struct {
		List []struct {
			ID uint `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(profilesEnvelope.Data, &profiles); err != nil || len(profiles.List) == 0 {
		t.Fatalf("profiles=%+v err=%v", profiles, err)
	}
	status, directoryEnvelope := owner.request(t, http.MethodGet, "/api/v1/storages/"+uintString(storage.ID)+"/directory", nil, false)
	if status != http.StatusOK {
		t.Fatalf("storage directory status=%d", status)
	}
	var directory struct {
		CurrentSelectionToken string `json:"current_selection_token"`
	}
	if err := json.Unmarshal(directoryEnvelope.Data, &directory); err != nil || directory.CurrentSelectionToken == "" {
		t.Fatalf("directory=%+v err=%v", directory, err)
	}
	payload := map[string]any{"name": "API library", "storage_id": storage.ID, "profile_id": profiles.List[0].ID, "relative_root_token": directory.CurrentSelectionToken, "enabled": true, "recursive": true}
	status, rejected := owner.request(t, http.MethodPost, "/api/v1/media-libraries", map[string]any{"name": "Raw path", "storage_id": storage.ID, "profile_id": profiles.List[0].ID, "relative_root": "/", "enabled": false}, true)
	if status != http.StatusBadRequest || !bytes.Contains(rejected.Data, []byte(services.CodeInvalidRequest)) {
		t.Fatalf("raw relative root status=%d data=%s", status, rejected.Data)
	}
	status, libraryEnvelope := owner.request(t, http.MethodPost, "/api/v1/media-libraries", payload, true)
	if status != http.StatusCreated {
		t.Fatalf("create library status=%d message=%s", status, libraryEnvelope.Message)
	}
	var library struct {
		ID           uint   `json:"id"`
		RelativeRoot string `json:"relative_root"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(libraryEnvelope.Data, &library); err != nil {
		t.Fatal(err)
	}
	if library.RelativeRoot != "/" || strings.Contains(string(libraryEnvelope.Data), root) {
		t.Fatalf("unsafe library response: %s", libraryEnvelope.Data)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		status, libraryEnvelope = owner.request(t, http.MethodGet, "/api/v1/media-libraries/"+uintString(library.ID), nil, false)
		if status != http.StatusOK {
			t.Fatal(status)
		}
		if err := json.Unmarshal(libraryEnvelope.Data, &library); err != nil {
			t.Fatal(err)
		}
		if library.Status == "listening" {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if library.Status != "listening" {
		t.Fatalf("library status=%q", library.Status)
	}
	secondRoot := t.TempDir()
	status, secondStorageEnvelope := owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "Second library source", "type": "local", "root_path": secondRoot, "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create second storage status=%d", status)
	}
	var secondStorage struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(secondStorageEnvelope.Data, &secondStorage); err != nil {
		t.Fatal(err)
	}
	status, secondDirectoryEnvelope := owner.request(t, http.MethodGet, "/api/v1/storages/"+uintString(secondStorage.ID)+"/directory", nil, false)
	if status != http.StatusOK {
		t.Fatalf("second directory status=%d", status)
	}
	if err := json.Unmarshal(secondDirectoryEnvelope.Data, &directory); err != nil || directory.CurrentSelectionToken == "" {
		t.Fatalf("second directory=%+v err=%v", directory, err)
	}
	status, secondLibraryEnvelope := owner.request(t, http.MethodPost, "/api/v1/media-libraries", map[string]any{"name": "Second API library", "storage_id": secondStorage.ID, "profile_id": profiles.List[0].ID, "relative_root_token": directory.CurrentSelectionToken, "enabled": false}, true)
	if status != http.StatusCreated {
		t.Fatalf("create second library status=%d message=%s", status, secondLibraryEnvelope.Message)
	}
	var secondLibrary struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(secondLibraryEnvelope.Data, &secondLibrary); err != nil {
		t.Fatal(err)
	}
	status, orderEnvelope := owner.request(t, http.MethodPut, "/api/v1/media-libraries/order", map[string]any{"ids": []uint{secondLibrary.ID, library.ID}}, true)
	if status != http.StatusOK {
		t.Fatalf("reorder status=%d message=%s", status, orderEnvelope.Message)
	}
	var ordered struct {
		List []struct {
			ID        uint `json:"id"`
			SortOrder int  `json:"sort_order"`
		} `json:"list"`
	}
	if err := json.Unmarshal(orderEnvelope.Data, &ordered); err != nil || len(ordered.List) != 2 || ordered.List[0].ID != secondLibrary.ID || ordered.List[0].SortOrder != 1 {
		t.Fatalf("ordered=%+v err=%v", ordered, err)
	}
	status, entriesEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-libraries/"+uintString(library.ID)+"/entries", nil, false)
	if status != http.StatusOK || !strings.Contains(string(entriesEnvelope.Data), "/API.Movie.mp4") || strings.Contains(string(entriesEnvelope.Data), root) {
		t.Fatalf("entries status=%d data=%s", status, entriesEnvelope.Data)
	}
	status, recognitionEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-libraries/"+uintString(library.ID)+"/recognitions?status=unrecognized&page=1&page_size=20", nil, false)
	if status != http.StatusOK || owner.lastHeader.Get("Cache-Control") != "no-store" || strings.Contains(string(recognitionEnvelope.Data), root) || strings.Contains(string(recognitionEnvelope.Data), "provider_id") {
		t.Fatalf("recognitions status=%d cache=%q data=%s", status, owner.lastHeader.Get("Cache-Control"), recognitionEnvelope.Data)
	}
	var recognitionPage struct {
		List []struct {
			Token string `json:"token"`
		} `json:"list"`
	}
	if err := json.Unmarshal(recognitionEnvelope.Data, &recognitionPage); err != nil || len(recognitionPage.List) != 1 || recognitionPage.List[0].Token == "" {
		t.Fatalf("recognitions=%+v err=%v", recognitionPage, err)
	}
	status, retryEnvelope := owner.request(t, http.MethodPost, "/api/v1/media-libraries/"+uintString(library.ID)+"/recognitions/"+recognitionPage.List[0].Token+"/retry", map[string]any{}, true)
	if status != http.StatusOK || !bytes.Contains(retryEnvelope.Data, []byte(`"status":"unrecognized"`)) {
		t.Fatalf("recognition retry status=%d data=%s", status, retryEnvelope.Data)
	}
	status, catalogEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-libraries/"+uintString(library.ID)+"/catalog?page=1&page_size=20&media_type=movie", nil, false)
	if status != http.StatusOK || !strings.Contains(string(catalogEnvelope.Data), "API Movie") || strings.Contains(string(catalogEnvelope.Data), root) {
		t.Fatalf("catalog status=%d data=%s", status, catalogEnvelope.Data)
	}
	var catalog struct {
		List []struct {
			ID string `json:"id"`
		} `json:"list"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(catalogEnvelope.Data, &catalog); err != nil || catalog.Total != 1 || len(catalog.List) != 1 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	status, catalogDetailEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-libraries/"+uintString(library.ID)+"/catalog/"+catalog.List[0].ID, nil, false)
	if status != http.StatusOK || !strings.Contains(string(catalogDetailEnvelope.Data), "/API.Movie.mp4") || strings.Contains(string(catalogDetailEnvelope.Data), root) {
		t.Fatalf("catalog detail status=%d data=%s", status, catalogDetailEnvelope.Data)
	}
	status, invalidPageEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-libraries/"+uintString(library.ID)+"/entries?page=nope&page_size=50", nil, false)
	if status != http.StatusBadRequest || !bytes.Contains(invalidPageEnvelope.Data, []byte(services.CodeInvalidRequest)) {
		t.Fatalf("invalid page status=%d data=%s", status, invalidPageEnvelope.Data)
	}
	status, _ = owner.request(t, http.MethodDelete, "/api/v1/storages/"+uintString(storage.ID), map[string]any{}, true)
	if status != http.StatusConflict {
		t.Fatalf("referenced storage delete status=%d", status)
	}
	status, _ = owner.request(t, http.MethodDelete, "/api/v1/media-libraries/"+uintString(secondLibrary.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("delete second media library status=%d", status)
	}
	status, _ = owner.request(t, http.MethodDelete, "/api/v1/media-libraries/"+uintString(library.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("delete media library status=%d", status)
	}
}

func TestDirectoryPickerRBACNoStoreAndStorageRoundTrip(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/filesystem/roots", nil)
	req.AddCookie(owner.cookie)
	response := httptest.NewRecorder()
	owner.router.ServeHTTP(response, req)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("roots status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}
	var roots testEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &roots); err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Items []struct {
			SelectionToken string `json:"selection_token"`
		} `json:"items"`
	}
	if err := json.Unmarshal(roots.Data, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) == 0 {
		t.Skip("Server process has no selectable filesystem roots")
	}
	status, envelope := owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "Picker root", "type": "local", "picker_token": listing.Items[0].SelectionToken}, true)
	if status != http.StatusCreated {
		t.Fatalf("picker create status=%d message=%s data=%s", status, envelope.Message, envelope.Data)
	}

	// Viewer has neither middleware nor service authorization for filesystem disclosure.
	status, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var rolesData struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	if err := json.Unmarshal(rolesEnvelope.Data, &rolesData); err != nil {
		t.Fatal(err)
	}
	var viewerID uint
	for _, role := range rolesData.List {
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "picker-viewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "picker-viewer", "viewer-strong-password")
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/filesystem/roots", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer roots status=%d", status)
	}
	viewerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/filesystem/roots", nil)
	viewerRequest.AddCookie(viewer.cookie)
	viewerResponse := httptest.NewRecorder()
	viewer.router.ServeHTTP(viewerResponse, viewerRequest)
	if viewerResponse.Code != http.StatusForbidden || viewerResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("viewer roots cache status=%d cache=%q", viewerResponse.Code, viewerResponse.Header().Get("Cache-Control"))
	}
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/filesystem/directories?token=invalid", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer directories status=%d", status)
	}
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/storages/1/directory", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer storage directory status=%d", status)
	}
}

func TestMediaClassificationProfileLifecycleAndPermissionBoundary(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	status, listEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusOK {
		t.Fatalf("list status=%d", status)
	}
	var listData struct {
		List []struct {
			ID   uint    `json:"id"`
			Code *string `json:"code"`
		} `json:"list"`
	}
	if err := json.Unmarshal(listEnvelope.Data, &listData); err != nil || len(listData.List) != 1 {
		t.Fatalf("list=%s err=%v", listEnvelope.Data, err)
	}
	builtinID := listData.List[0].ID
	status, _ = owner.request(t, http.MethodPatch, "/api/v1/media-classification-profiles/"+uintString(builtinID), map[string]any{"revision": 1, "name": "x", "rules": map[string]any{}}, true)
	if status != http.StatusForbidden {
		t.Fatalf("protected update status=%d", status)
	}
	status, createEnvelope := owner.request(t, http.MethodPost, "/api/v1/media-classification-profiles", map[string]any{"name": "API Custom"}, true)
	if status != http.StatusCreated {
		t.Fatalf("create status=%d message=%s", status, createEnvelope.Message)
	}
	var created struct {
		ID                      uint            `json:"id"`
		Revision                uint64          `json:"revision"`
		Rules                   json.RawMessage `json:"rules"`
		BuiltinRecognitionPacks []string        `json:"builtin_recognition_packs"`
	}
	if err := json.Unmarshal(createEnvelope.Data, &created); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(created.BuiltinRecognitionPacks, []string{"tv-v1", "anime-v1"}) {
		t.Fatalf("created built-in packs=%v", created.BuiltinRecognitionPacks)
	}
	status, _ = owner.request(t, http.MethodPatch, "/api/v1/media-classification-profiles/"+uintString(created.ID), map[string]any{"revision": created.Revision, "name": "API Custom", "rules": json.RawMessage(created.Rules), "builtin_recognition_packs": []string{"unknown-v1"}}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid built-in packs status=%d", status)
	}
	status, updateEnvelope := owner.request(t, http.MethodPatch, "/api/v1/media-classification-profiles/"+uintString(created.ID), map[string]any{"revision": created.Revision, "name": "API Custom", "rules": json.RawMessage(created.Rules), "builtin_recognition_packs": []string{}}, true)
	if status != http.StatusOK {
		t.Fatalf("update status=%d", status)
	}
	var updated struct {
		BuiltinRecognitionPacks []string `json:"builtin_recognition_packs"`
	}
	if err := json.Unmarshal(updateEnvelope.Data, &updated); err != nil || updated.BuiltinRecognitionPacks == nil || len(updated.BuiltinRecognitionPacks) != 0 {
		t.Fatalf("updated built-in packs=%v err=%v", updated.BuiltinRecognitionPacks, err)
	}
	status, stale := owner.request(t, http.MethodPatch, "/api/v1/media-classification-profiles/"+uintString(created.ID), map[string]any{"revision": created.Revision, "name": "API Custom", "rules": json.RawMessage(created.Rules)}, true)
	if status != http.StatusConflict || !bytes.Contains(stale.Data, []byte(services.CodeProfileRevisionConflict)) {
		t.Fatalf("stale status=%d data=%s", status, stale.Data)
	}
	status, copyEnvelope := owner.request(t, http.MethodPost, "/api/v1/media-classification-profiles/"+uintString(builtinID)+"/copy", map[string]any{}, true)
	if status != http.StatusCreated {
		t.Fatalf("copy status=%d", status)
	}
	if bytes.Contains(copyEnvelope.Data, []byte("default-movie-animation")) {
		t.Fatal("copy retained built-in category ids")
	}
	status, auditEnvelope := owner.request(t, http.MethodGet, "/api/v1/audit?limit=50", nil, false)
	if status != http.StatusOK || bytes.Contains(auditEnvelope.Data, []byte("rules_json")) || bytes.Contains(auditEnvelope.Data, []byte("动画电影")) {
		t.Fatalf("unsafe audit=%s", auditEnvelope.Data)
	}

	status, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var roles struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	_ = json.Unmarshal(rolesEnvelope.Data, &roles)
	var operatorID, viewerID uint
	for _, role := range roles.List {
		if role.Code == "operator" {
			operatorID = role.ID
		}
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "profileoperator", "password": "operator-strong-password", "role_ids": []uint{operatorID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	operator := newTestClientWithRouter(owner.router)
	operator.login(t, "profileoperator", "operator-strong-password")
	status, operatorCreate := operator.request(t, http.MethodPost, "/api/v1/media-classification-profiles", map[string]any{"name": "Operator Profile"}, true)
	if status != http.StatusCreated {
		t.Fatalf("operator create=%d message=%s", status, operatorCreate.Message)
	}
	var operatorProfile struct {
		ID       uint            `json:"id"`
		Revision uint64          `json:"revision"`
		Rules    json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(operatorCreate.Data, &operatorProfile); err != nil {
		t.Fatal(err)
	}
	status, _ = operator.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusOK {
		t.Fatalf("operator list=%d", status)
	}
	status, _ = operator.request(t, http.MethodPost, "/api/v1/media-classification-profiles/"+uintString(operatorProfile.ID)+"/copy", map[string]any{"name": "Operator Copy"}, true)
	if status != http.StatusCreated {
		t.Fatalf("operator copy=%d", status)
	}
	status, _ = operator.request(t, http.MethodPatch, "/api/v1/media-classification-profiles/"+uintString(operatorProfile.ID), map[string]any{"revision": operatorProfile.Revision, "name": "Operator Updated", "rules": operatorProfile.Rules}, true)
	if status != http.StatusOK {
		t.Fatalf("operator update=%d", status)
	}
	status, _ = operator.request(t, http.MethodDelete, "/api/v1/media-classification-profiles/"+uintString(operatorProfile.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("operator delete=%d", status)
	}

	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "profileviewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "profileviewer", "viewer-strong-password")
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer list=%d", status)
	}
	status, _ = viewer.request(t, http.MethodPost, "/api/v1/media-classification-profiles", map[string]any{"name": "Denied"}, true)
	if status != http.StatusForbidden {
		t.Fatalf("viewer create=%d", status)
	}
}

func TestMediaClassificationProfileRequestsRejectUnknownFields(t *testing.T) {
	client := newTestClient(t)
	client.setup(t)
	status, _ := client.request(t, http.MethodPost, "/api/v1/media-classification-profiles", map[string]any{"name": "Unknown", "mystery": true}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("create status=%d", status)
	}
	status, listEnvelope := client.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var listData struct {
		List []struct {
			ID uint `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(listEnvelope.Data, &listData); err != nil {
		t.Fatal(err)
	}
	status, _ = client.request(t, http.MethodPost, "/api/v1/media-classification-profiles/"+uintString(listData.List[0].ID)+"/copy", map[string]any{"mystery": true}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("copy status=%d", status)
	}
}

func TestTransferDeletionRequestsRejectUnknownFieldsAndDisableCaching(t *testing.T) {
	client := newTestClient(t)
	client.setup(t)
	transferID := "00000000-0000-0000-0000-000000000001"
	for _, test := range []struct {
		path string
		body map[string]any
	}{
		{path: "/api/v1/transfers/" + transferID + "/deletion-preview", body: map[string]any{"scope": models.TransferDeletionScopeRecordOnly, "mystery": true}},
		{path: "/api/v1/transfers/" + transferID + "/deletion-confirm", body: map[string]any{"token": strings.Repeat("a", 43), "mystery": true}},
	} {
		status, _ := client.request(t, http.MethodPost, test.path, test.body, true)
		if status != http.StatusBadRequest || client.lastHeader.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s status=%d cache=%q", test.path, status, client.lastHeader.Get("Cache-Control"))
		}
	}
}

func TestRuntimeLogsRBACNoStoreQuerySettingsAndExport(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	for _, path := range []string{"/api/v1/runtime-logs", "/api/v1/runtime-logs/facets", "/api/v1/runtime-logs/settings"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(owner.cookie)
		response := httptest.NewRecorder()
		owner.router.ServeHTTP(response, req)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s status=%d cache=%q", path, response.Code, response.Header().Get("Cache-Control"))
		}
	}
	status, settingsEnvelope := owner.request(t, http.MethodGet, "/api/v1/runtime-logs/settings", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var settings map[string]any
	if json.Unmarshal(settingsEnvelope.Data, &settings) != nil {
		t.Fatal("decode settings")
	}
	settings["max_file_mib"] = 21
	status, _ = owner.request(t, http.MethodPatch, "/api/v1/runtime-logs/settings", settings, true)
	if status != http.StatusOK {
		t.Fatalf("update settings status=%d", status)
	}
	status, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var roles struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	_ = json.Unmarshal(rolesEnvelope.Data, &roles)
	var operatorID, viewerID uint
	for _, role := range roles.List {
		if role.Code == "operator" {
			operatorID = role.ID
		}
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "logoperator", "password": "operator-strong-password", "role_ids": []uint{operatorID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	operator := newTestClientWithRouter(owner.router)
	operator.login(t, "logoperator", "operator-strong-password")
	status, _ = operator.request(t, http.MethodGet, "/api/v1/runtime-logs", nil, false)
	if status != http.StatusOK {
		t.Fatalf("operator read=%d", status)
	}
	status, _ = operator.request(t, http.MethodPost, "/api/v1/runtime-logs/export", map[string]any{}, true)
	if status != http.StatusForbidden {
		t.Fatalf("operator export=%d", status)
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "logviewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "logviewer", "viewer-strong-password")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-logs", nil)
	req.AddCookie(viewer.cookie)
	response := httptest.NewRecorder()
	viewer.router.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("viewer status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}
}

func TestQueueAPIReadControlReorderStrictJSONAndViewerBoundary(t *testing.T) {
	owner := newTestClient(t)
	setup := owner.setup(t)
	user := setup["user"].(map[string]any)
	ownerID := uint(user["id"].(float64))
	one, err := owner.queue.Enqueue(services.EnqueueJobInput{OwnerID: ownerID, JobType: "fake", Priority: 10, DisplayName: "First", Payload: map[string]any{"step": 1}})
	if err != nil {
		t.Fatal(err)
	}
	two, err := owner.queue.Enqueue(services.EnqueueJobInput{OwnerID: ownerID, JobType: "fake", Priority: 10, DisplayName: "Second", Payload: map[string]any{"step": 2}})
	if err != nil {
		t.Fatal(err)
	}
	status, envelope := owner.request(t, http.MethodGet, "/api/v1/jobs?status=queued&job_type=fake&priority=10", nil, false)
	if status != http.StatusOK || !bytes.Contains(envelope.Data, []byte(one.ID)) {
		t.Fatalf("list=%d %s", status, envelope.Data)
	}
	status, _ = owner.request(t, http.MethodPut, "/api/v1/job-lanes/fake/10/order", map[string]any{"jobs": []map[string]any{{"id": two.ID, "revision": two.Revision}, {"id": one.ID, "revision": one.Revision}}}, true)
	if status != http.StatusOK {
		t.Fatalf("reorder=%d", status)
	}
	status, _ = owner.request(t, http.MethodPut, "/api/v1/job-lanes/fake/10/order", map[string]any{"jobs": []map[string]any{}, "unknown": true}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("strict json=%d", status)
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/jobs/"+one.ID+"/cancel", map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("cancel=%d", status)
	}
	status, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var roles struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	_ = json.Unmarshal(rolesEnvelope.Data, &roles)
	var viewerID uint
	for _, role := range roles.List {
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "queueviewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "queueviewer", "viewer-strong-password")
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/jobs", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer=%d", status)
	}
}

func newTestClientWithRouter(router http.Handler) *testClient { return &testClient{router: router} }
func uintString(value uint) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	result := ""
	for value > 0 {
		result = string(digits[value%10]) + result
		value /= 10
	}
	return result
}
