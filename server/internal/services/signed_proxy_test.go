package services

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

func TestProxyHeadersCompatibleOnlyAcceptsTheBoundClientUserAgent(t *testing.T) {
	const userAgent = "Emby Web/4.9.5.0"
	for name, item := range map[string]struct {
		headers http.Header
		want    bool
	}{
		"none":               {headers: nil, want: true},
		"matching ua":        {headers: http.Header{"User-Agent": {userAgent}}, want: true},
		"different ua":       {headers: http.Header{"User-Agent": {"Other Player"}}},
		"multiple ua values": {headers: http.Header{"User-Agent": {userAgent, "Other Player"}}},
		"cookie":             {headers: http.Header{"User-Agent": {userAgent}, "Cookie": {"private"}}},
		"referer":            {headers: http.Header{"Referer": {"https://115.com/"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := proxyHeadersCompatible(item.headers, userAgent); got != item.want {
				t.Fatalf("compatible=%t, want %t", got, item.want)
			}
		})
	}
}

func TestSignedProxyUsesPublicOriginValidatesSignatureAndIsolatesUserAgentCache(t *testing.T) {
	driver := &fakeCloudDriver{signedProxy: true, echoDirectUA: true, items: map[string]cloud.Item{
		"video-file": {ID: "video-file", ParentID: "library-root", Name: "Movie.mkv", PickCode: "private-pickcode", Size: 100},
	}}
	db, store, connections, actor := newConnectionTestService(t, driver)
	connection, err := connections.Create(actor, ConnectionInput{Name: "Proxy account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Proxy cloud", NameNormalized: "proxy-cloud", Type: models.StorageTypePan115, RootPath: "storage-root", RootDisplayPath: "/媒体", RootPathNormalized: "pan115:proxy", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{"temporary_direct_url":true,"signed_proxy":true}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Proxy library", NameNormalized: "proxy-library", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", ProviderRootID: "library-root", Enabled: true, Recursive: true, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, STRMEnabled: true, SignedProxyEnabled: true, MetadataArtifactsEnabled: true, STRMLocalRoot: t.TempDir(), Status: models.MediaLibraryStatusListening, ArtifactStatus: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := db.Select("*").Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: "11111111-1111-4111-8111-111111111111", LibraryID: library.ID, Generation: 1, PolicyJSON: `{}`, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	opaque := "artifact_AAAAAAAAAAAAAAAAAAAAAA"
	artifact := models.MediaArtifact{OpaqueID: opaque, RunID: run.ID, LibraryID: library.ID, ProviderItemID: "video-file", Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/Movie.strm", Managed: true, Active: true, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := db.Select("*").Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewSignedProxyService(db, store, connections, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	signed, err := service.SignArtifact(opaque, library.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "media.example.test" || strings.Contains(signed, "video-file") || strings.Contains(signed, "pickcode") || strings.Contains(signed, storage.RootPath) {
		t.Fatalf("unsafe signed URL %q", signed)
	}
	expiry, _ := strconv.ParseInt(parsed.Query().Get("exp"), 10, 64)
	first, err := service.Resolve(context.Background(), opaque, parsed.Query().Get("kid"), expiry, parsed.Query().Get("sig"), "Player-A")
	if err != nil || first.URL != "https://example.invalid/file" {
		t.Fatalf("first redirect=%+v err=%v", first, err)
	}
	if _, err := service.Resolve(context.Background(), opaque, parsed.Query().Get("kid"), expiry, parsed.Query().Get("sig"), "Player-A"); err != nil {
		t.Fatal(err)
	}
	if calls := driver.directURLCalls.Load(); calls != 1 {
		t.Fatalf("same-UA cache direct calls=%d, want 1", calls)
	}
	if _, err := service.Resolve(context.Background(), opaque, parsed.Query().Get("kid"), expiry, parsed.Query().Get("sig"), "Player-B"); err != nil {
		t.Fatal(err)
	}
	if calls := driver.directURLCalls.Load(); calls != 2 {
		t.Fatalf("cross-UA cache direct calls=%d, want 2", calls)
	}
	if _, err := service.Resolve(context.Background(), opaque, parsed.Query().Get("kid"), expiry, parsed.Query().Get("sig")+"x", "Player-A"); ErrorCode(err) != CodeProxySignatureInvalid {
		t.Fatalf("tamper code=%q err=%v", ErrorCode(err), err)
	}
	service.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, err := service.Resolve(context.Background(), opaque, parsed.Query().Get("kid"), expiry, parsed.Query().Get("sig"), "Player-A"); ErrorCode(err) != CodeProxySignatureExpired {
		t.Fatalf("expiry code=%q err=%v", ErrorCode(err), err)
	}
	if strings.Contains(filepath.ToSlash(signed), filepath.ToSlash(library.STRMLocalRoot)) {
		t.Fatalf("projection root leaked in signed URL %q", signed)
	}
}

func TestSignedProxyRejectsInvalidPublicOriginAndOversizedTTL(t *testing.T) {
	db, store, connections, _ := newConnectionTestService(t, &fakeCloudDriver{})
	if _, err := NewSignedProxyService(db, store, connections, "https://user:pass@example.test/path", zerolog.Nop()); err == nil {
		t.Fatal("credentialed/path public origin was accepted")
	}
	for _, origin := range []string{"http://0.0.0.0:3000", "http://0.0.0.0.:3000", "http://[::]:3000"} {
		if _, err := NewSignedProxyService(db, store, connections, origin, zerolog.Nop()); err == nil {
			t.Fatalf("wildcard public origin was accepted: %s", origin)
		}
	}
	service, err := NewSignedProxyService(db, store, connections, "http://127.0.0.1:3000", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SignArtifact("artifact_AAAAAAAAAAAAAAAAAAAAAA", 1, proxyMaximumTTL+time.Second); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("oversized TTL code=%q err=%v", ErrorCode(err), err)
	}
}

func TestSignedProxyPan115UsesBoundedSecondaryCopyAndExactCleanup(t *testing.T) {
	driver := &fakeCloudDriver{signedProxy: true, createDirectory: true, copyItems: true, recycleItems: true, items: map[string]cloud.Item{
		"video-file": {ID: "video-file", ParentID: "library-root", Name: "Movie.mkv", PickCode: "private-pickcode", SHA1: "abc", Size: 100},
	}}
	db, store, connections, actor := newConnectionTestService(t, driver)
	connection, err := connections.Create(actor, ConnectionInput{Name: "Multi-device account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, RecyclePassword: "safe-code", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Multi-device cloud", NameNormalized: "multi-device-cloud", Type: models.StorageTypePan115, RootPath: "storage-root", RootDisplayPath: "/媒体", RootPathNormalized: "pan115:multi-device", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{"temporary_direct_url":true,"signed_proxy":true}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Multi-device library", NameNormalized: "multi-device-library", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", ProviderRootID: "library-root", Enabled: true, Recursive: true, VideoExtensionsJSON: `[".mkv"]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, STRMEnabled: true, SignedProxyEnabled: true, MetadataArtifactsEnabled: true, STRMLocalRoot: t.TempDir(), Status: models.MediaLibraryStatusListening, ArtifactStatus: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := db.Select("*").Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: "22222222-2222-4222-8222-222222222222", LibraryID: library.ID, Generation: 1, PolicyJSON: `{}`, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	opaque := "artifact_BBBBBBBBBBBBBBBBBBBBBB"
	artifact := models.MediaArtifact{OpaqueID: opaque, RunID: run.ID, LibraryID: library.ID, ProviderItemID: "video-file", Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/Movie.strm", Managed: true, Active: true, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := db.Select("*").Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewSignedProxyService(db, store, connections, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	service.playback.now = func() time.Time { return now }
	signed, err := service.SignArtifact(opaque, library.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signed)
	expiry, _ := strconv.ParseInt(parsed.Query().Get("exp"), 10, 64)
	resolve := func(remote string) error {
		_, resolveErr := service.ResolveForClient(context.Background(), opaque, parsed.Query().Get("kid"), expiry, parsed.Query().Get("sig"), "Emby", remote)
		return resolveErr
	}
	if err := resolve("10.0.0.1:1000"); err != nil {
		t.Fatal(err)
	}
	if err := resolve("10.0.0.2:2000"); err != nil {
		t.Fatal(err)
	}
	if len(driver.directFileIDs) != 2 || driver.directFileIDs[0] != "video-file" || driver.directFileIDs[1] == "video-file" {
		t.Fatalf("direct URL file routing=%v", driver.directFileIDs)
	}
	if err := resolve("10.0.0.3:3000"); ErrorCode(err) != CodeProxyDeviceLimit {
		t.Fatalf("third device code=%q err=%v", ErrorCode(err), err)
	}
	var lease models.Pan115PlaybackLease
	if err := db.Where("role = ?", models.Pan115PlaybackRoleSecondary).First(&lease).Error; err != nil {
		t.Fatal(err)
	}
	if lease.CopyDirectoryID == "" || strings.Contains(lease.ClientFingerprint, "10.0.0.2") || len(lease.ClientFingerprint) != sha256.Size*2 {
		t.Fatalf("unsafe or incomplete lease=%+v", lease)
	}
	now = now.Add(10 * time.Second)
	service.playback.sweep(context.Background())
	if len(driver.recycled) != 1 || len(driver.purged) != 1 || driver.recycled[0] != driver.purged[0] {
		t.Fatalf("recycled=%v purged=%v", driver.recycled, driver.purged)
	}
	if err := db.First(&lease, "id = ?", lease.ID).Error; err != nil || lease.CopyDirectoryID != "" || lease.Status != models.Pan115PlaybackLeaseCompleted {
		t.Fatalf("cleaned lease=%+v err=%v", lease, err)
	}
}
