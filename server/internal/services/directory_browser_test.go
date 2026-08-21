package services

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/directory"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

type fakeDirectoryAdapter struct{ root string }

func (f fakeDirectoryAdapter) Platform() string { return "fake" }
func (f fakeDirectoryAdapter) Version() string  { return "fake-v1" }
func (f fakeDirectoryAdapter) Roots(context.Context) ([]directory.Root, error) {
	return []directory.Root{{Path: f.root, Name: "Server root", Kind: "mount", Selectable: true, Enterable: true}}, nil
}
func (f fakeDirectoryAdapter) Directories(_ context.Context, path string, _ int) ([]directory.Entry, bool, error) {
	return []directory.Entry{{Path: filepath.Join(path, "Media"), Name: "Media", Selectable: true, Enterable: true}, {Path: filepath.Join(path, "Link"), Name: "Link", Reason: "link_not_allowed"}}, false, nil
}
func (f fakeDirectoryAdapter) Validate(context.Context, string) error { return nil }

type blockingDirectoryAdapter struct {
	started chan struct{}
	once    sync.Once
}

func (f *blockingDirectoryAdapter) Platform() string { return "fake" }
func (f *blockingDirectoryAdapter) Version() string  { return "blocking-v1" }
func (f *blockingDirectoryAdapter) Roots(ctx context.Context) ([]directory.Root, error) {
	f.once.Do(func() { close(f.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}
func (f *blockingDirectoryAdapter) Directories(ctx context.Context, _ string, _ int) ([]directory.Entry, bool, error) {
	f.once.Do(func() { close(f.started) })
	<-ctx.Done()
	return nil, false, ctx.Err()
}
func (f *blockingDirectoryAdapter) Validate(context.Context, string) error { return nil }

func TestDirectoryBrowserTokensArePurposeBoundTamperProofAndExpire(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	root := t.TempDir()
	service, err := NewDirectoryBrowserService(db, fakeDirectoryAdapter{root: root})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: models.User{ID: 1}, Permissions: map[string]struct{}{authz.PermissionStoragesBrowse: {}}}
	listing, err := service.Roots(context.Background(), actor, RequestContext{IPHint: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 || listing.Items[0].Token == "" || listing.Items[0].SelectionToken == "" {
		t.Fatalf("unexpected roots: %+v", listing)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(listing.Items[0].SelectionToken)
	if err != nil {
		t.Fatalf("selection token is not opaque base64: %v", err)
	}
	if strings.Contains(string(decoded), root) {
		t.Fatal("opaque selection token exposed its absolute path payload")
	}
	if _, err := service.ResolveSelection(context.Background(), actor, listing.Items[0].Token); ErrorCode(err) != CodeDirectoryTokenInvalid {
		t.Fatalf("browse token selected: %v", err)
	}
	replacement := byte('A')
	if listing.Items[0].SelectionToken[0] == replacement {
		replacement = 'B'
	}
	tampered := string(replacement) + listing.Items[0].SelectionToken[1:]
	if _, err := service.ResolveSelection(context.Background(), actor, tampered); ErrorCode(err) != CodeDirectoryTokenInvalid {
		t.Fatalf("tampered token: %v", err)
	}
	service.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	if _, err := service.ResolveSelection(context.Background(), actor, listing.Items[0].SelectionToken); ErrorCode(err) != CodeDirectoryTokenExpired {
		t.Fatalf("expired token: %v", err)
	}
}

func TestDirectoryBrowserReturnsOnServiceTimeout(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	adapter := &blockingDirectoryAdapter{started: make(chan struct{})}
	service, err := NewDirectoryBrowserService(db, adapter)
	if err != nil {
		t.Fatal(err)
	}
	service.timeout = 20 * time.Millisecond
	actor := Actor{User: models.User{ID: 1}, Permissions: map[string]struct{}{authz.PermissionStoragesBrowse: {}}}
	started := time.Now()
	_, err = service.Roots(context.Background(), actor, RequestContext{IPHint: "127.0.0.1"})
	if ErrorCode(err) != CodeDirectoryUnavailable {
		t.Fatalf("timeout code=%q error=%v", ErrorCode(err), err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("service timeout did not bound response time: %v", elapsed)
	}
}

func TestDirectoryBrowserSelectionRejectsStalePath(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	root := t.TempDir()
	service, err := NewDirectoryBrowserService(db, directory.NativeAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: models.User{ID: 1}, Permissions: map[string]struct{}{authz.PermissionStoragesBrowse: {}}}
	token, err := service.sign(root, tokenPurposeSelect)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveSelection(context.Background(), actor, token); ErrorCode(err) != CodeDirectoryNotFound {
		t.Fatalf("stale selection code=%q error=%v", ErrorCode(err), err)
	}
}

func TestDirectoryBrowserResolvesOnlyStorageRelativeSelections(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "relative.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	root := t.TempDir()
	inside := filepath.Join(root, "Media", "Movies")
	outside := t.TempDir()
	for _, path := range []string{inside, outside} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	storage := models.Storage{Name: "Relative", NameNormalized: "relative", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewDirectoryBrowserService(db, directory.NativeAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: models.User{ID: 1}, Permissions: map[string]struct{}{authz.PermissionStoragesBrowse: {}}}
	insideToken, _ := service.sign(inside, tokenPurposeSelect)
	relative, err := service.ResolveStorageRelativeSelection(context.Background(), actor, storage.ID, insideToken)
	if err != nil || relative != "/Media/Movies" {
		t.Fatalf("relative=%q err=%v", relative, err)
	}
	rootToken, _ := service.sign(root, tokenPurposeSelect)
	if relative, err := service.ResolveStorageRelativeSelection(context.Background(), actor, storage.ID, rootToken); err != nil || relative != "/" {
		t.Fatalf("root relative=%q err=%v", relative, err)
	}
	outsideToken, _ := service.sign(outside, tokenPurposeSelect)
	if _, err := service.ResolveStorageRelativeSelection(context.Background(), actor, storage.ID, outsideToken); ErrorCode(err) != CodeMediaLibraryPathInvalid {
		t.Fatalf("outside code=%q err=%v", ErrorCode(err), err)
	}
}

func TestDirectoryBrowserStorageNavigationCannotLeaveLocalRoot(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "storage-navigation.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	root, outside := t.TempDir(), t.TempDir()
	storage := models.Storage{Name: "Navigation", NameNormalized: "navigation", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	service, err := NewDirectoryBrowserService(db, fakeDirectoryAdapter{root: root})
	if err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: models.User{ID: 1}, Permissions: map[string]struct{}{authz.PermissionStoragesBrowse: {}}}
	listing, err := service.StorageToken(context.Background(), actor, storage.ID, "", "", RequestContext{})
	if err != nil || listing.Location != root || len(listing.Items) == 0 {
		t.Fatalf("listing=%+v err=%v", listing, err)
	}
	if _, err := service.StorageToken(context.Background(), actor, storage.ID, listing.Items[0].Token, "", RequestContext{}); err != nil {
		t.Fatalf("inside navigation failed: %v", err)
	}
	outsideToken, _ := service.sign(outside, tokenPurposeBrowse)
	if _, err := service.StorageToken(context.Background(), actor, storage.ID, outsideToken, "", RequestContext{}); ErrorCode(err) != CodeMediaLibraryPathInvalid {
		t.Fatalf("outside navigation code=%q err=%v", ErrorCode(err), err)
	}
}

func TestDirectoryBrowserRepeatsServiceAuthorization(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	service, err := NewDirectoryBrowserService(db, fakeDirectoryAdapter{root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Roots(context.Background(), Actor{User: models.User{ID: 2}}, RequestContext{}); ErrorCode(err) != CodePermissionDenied {
		t.Fatalf("unauthorized roots: %v", err)
	}
}

func TestDirectoryBrowserRateLimitKeysAreActorAndIPScopedAndBounded(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	service, err := NewDirectoryBrowserService(db, fakeDirectoryAdapter{root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 120; index++ {
		release, acquireErr := service.acquire(1, "127.0.0.1")
		if acquireErr != nil {
			t.Fatalf("actor/ip request %d: %v", index, acquireErr)
		}
		release()
	}
	if _, err := service.acquire(1, "127.0.0.1"); ErrorCode(err) != CodeDirectoryRateLimited {
		t.Fatalf("same actor/ip rate code=%q error=%v", ErrorCode(err), err)
	}
	release, err := service.acquire(1, "127.0.0.2")
	if err != nil {
		t.Fatalf("different IP shared rate window: %v", err)
	}
	release()
	release, err = service.acquire(2, "127.0.0.1")
	if err != nil {
		t.Fatalf("different actor shared rate window: %v", err)
	}
	release()

	service.mu.Lock()
	service.limits = make(map[string]*browseWindow, maxDirectoryRateKeys)
	for index := 0; index < maxDirectoryRateKeys; index++ {
		service.limits[uintID(uint(index+10))+"\x00ip"] = &browseWindow{started: service.now()}
	}
	service.mu.Unlock()
	if _, err := service.acquire(999999, "new-ip"); ErrorCode(err) != CodeDirectoryRateLimited {
		t.Fatalf("bounded rate map code=%q error=%v", ErrorCode(err), err)
	}
}
