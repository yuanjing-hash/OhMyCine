package services

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

func mediaLibraryTestService(t *testing.T) (*MediaLibraryService, *gorm.DB, Actor, models.Storage, models.MediaClassificationProfile) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "libraries.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	user := models.User{Username: "library-test", UsernameNormalized: "library-test", DisplayName: "Library Test", PasswordHash: "unused", Status: models.UserStatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: user, Permissions: map[string]struct{}{
		authz.PermissionMediaLibrariesRead: {}, authz.PermissionMediaLibrariesCreate: {},
		authz.PermissionMediaLibrariesUpdate: {}, authz.PermissionMediaLibrariesDelete: {},
		authz.PermissionMediaLibrariesScan: {},
	}}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	storage := models.Storage{Name: "Test storage", NameNormalized: "test storage", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	service := NewMediaLibraryService(db, NewAuditService(db), zerolog.Nop())
	t.Cleanup(service.Close)
	return service, db, actor, storage, profile
}

func testLibraryInput(name string, storage models.Storage, profile models.MediaClassificationProfile, enabled bool) MediaLibraryInput {
	return MediaLibraryInput{Name: name, StorageID: storage.ID, ProfileID: profile.ID, RelativeRoot: "/", Enabled: enabled, Recursive: true}
}

func waitForLibrary(t *testing.T, db *gorm.DB, id uint, condition func(models.MediaLibrary) bool) models.MediaLibrary {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var library models.MediaLibrary
		if err := db.First(&library, id).Error; err == nil && condition(library) {
			return library
		}
		time.Sleep(40 * time.Millisecond)
	}
	var library models.MediaLibrary
	_ = db.First(&library, id).Error
	t.Fatalf("library %d did not reach expected state; last=%+v", id, library)
	return models.MediaLibrary{}
}

func TestMediaLibraryAutomaticallyBuildsBaselineThenListens(t *testing.T) {
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	files := []string{"Movie.mp4", filepath.Join("Movies", "Nested.mkv"), filepath.Join("Series", "Show.S01E01.mp4"), filepath.Join("Series", "Show.S01E02.mp4")}
	for _, relative := range files {
		path := filepath.Join(storage.RootPath, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	created, err := service.Create(context.Background(), actor, testLibraryInput("Local library", storage, profile, true), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	library := waitForLibrary(t, db, created.ID, func(item models.MediaLibrary) bool { return item.Status == models.MediaLibraryStatusListening })
	if library.BaselineGeneration < 2 || library.DirtyGeneration < 2 {
		t.Fatalf("baseline/catch-up generations were not committed: %+v", library)
	}
	entries, err := service.Entries(actor, created.ID, 20)
	if err != nil || len(entries) != len(files) {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	var kinds []string
	if err := db.Model(&models.MediaLibraryScanRun{}).Where("library_id = ? AND status = ?", created.ID, "success").Order("id").Pluck("kind", &kinds).Error; err != nil {
		t.Fatal(err)
	}
	if len(kinds) < 2 || kinds[0] != "initial" || kinds[1] != "catch_up" {
		t.Fatalf("scan kinds=%v, want initial then catch_up", kinds)
	}
	payload, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), storage.RootPath) {
		t.Fatalf("physical storage root leaked in media library response: %s", payload)
	}
}

func TestDisabledLibraryWaitsUntilEnabledAndWatcherReconcilesChanges(t *testing.T) {
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	input := testLibraryInput("Deferred library", storage, profile, false)
	created, err := service.Create(context.Background(), actor, input, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	var runCount int64
	if err := db.Model(&models.MediaLibraryScanRun{}).Where("library_id = ?", created.ID).Count(&runCount).Error; err != nil || runCount != 0 {
		t.Fatalf("disabled library run count=%d err=%v", runCount, err)
	}
	input.Enabled = true
	if _, err := service.Update(context.Background(), actor, created.ID, input, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	waitForLibrary(t, db, created.ID, func(item models.MediaLibrary) bool { return item.Status == models.MediaLibraryStatusListening })

	mediaPath := filepath.Join(storage.RootPath, "Arrived.S02E03.mp4")
	if err := os.WriteFile(mediaPath, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForLibrary(t, db, created.ID, func(models.MediaLibrary) bool {
		var count int64
		return db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND relative_path = ?", created.ID, "/Arrived.S02E03.mp4").Count(&count).Error == nil && count == 1
	})
	if err := os.Remove(mediaPath); err != nil {
		t.Fatal(err)
	}
	waitForLibrary(t, db, created.ID, func(models.MediaLibrary) bool {
		var count int64
		return db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND relative_path = ?", created.ID, "/Arrived.S02E03.mp4").Count(&count).Error == nil && count == 0
	})
}

func TestMediaLibraryRejectsOverlappingRootsAndLocalSTRM(t *testing.T) {
	service, _, actor, storage, profile := mediaLibraryTestService(t)
	if err := os.MkdirAll(filepath.Join(storage.RootPath, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	first := testLibraryInput("First", storage, profile, false)
	first.RelativeRoot = "/nested"
	if _, err := service.Create(context.Background(), actor, first, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	second := testLibraryInput("Second", storage, profile, false)
	if _, err := service.Create(context.Background(), actor, second, RequestContext{}); ErrorCode(err) != CodeMediaLibraryOverlap {
		t.Fatalf("overlap code=%q err=%v", ErrorCode(err), err)
	}
	strm := testLibraryInput("STRM", storage, profile, false)
	strm.StorageID = storage.ID
	strm.STRMEnabled = true
	if _, err := service.Create(context.Background(), actor, strm, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("local STRM code=%q err=%v", ErrorCode(err), err)
	}
}

func TestMediaLibraryReferenceAndProfileRevisionContracts(t *testing.T) {
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	created, err := service.Create(context.Background(), actor, testLibraryInput("Referenced", storage, profile, false), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if refs, err := service.References(profile.ID); err != nil || len(refs) != 1 || refs[0] != created.Name {
		t.Fatalf("profile refs=%v err=%v", refs, err)
	}
	if refs, err := service.StorageReferences(storage.ID); err != nil || len(refs) != 1 || refs[0] != created.Name {
		t.Fatalf("storage refs=%v err=%v", refs, err)
	}
	if err := service.ProfileRevisionChanged(profile.ID, profile.Revision+1); err != nil {
		t.Fatal(err)
	}
	var library models.MediaLibrary
	if err := db.First(&library, created.ID).Error; err != nil || !library.ReclassificationDue {
		t.Fatalf("library=%+v err=%v", library, err)
	}
}

func TestLiveMediaLibraryRootIsReadOnly(t *testing.T) {
	root := os.Getenv("OMC_LIVE_LIBRARY_ROOT")
	if root == "" {
		t.Skip("set OMC_LIVE_LIBRARY_ROOT for the opt-in local acceptance test")
	}
	before := snapshotTree(t, root)
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	storage.RootPath = root
	storage.RootPathNormalized = strings.ToLower(filepath.Clean(root))
	if err := db.Save(&storage).Error; err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), actor, testLibraryInput("Live acceptance", storage, profile, true), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	waitForLibrary(t, db, created.ID, func(item models.MediaLibrary) bool { return item.Status == models.MediaLibraryStatusListening })
	entries, err := service.Entries(actor, created.ID, 500)
	if err != nil {
		t.Fatal(err)
	}
	mp4Count := 0
	for _, entry := range entries {
		if strings.EqualFold(filepath.Ext(entry.RelativePath), ".mp4") {
			mp4Count++
		}
		if filepath.IsAbs(entry.RelativePath) || strings.Contains(entry.RelativePath, root) {
			t.Fatalf("entry exposes a physical absolute path")
		}
	}
	if mp4Count != 4 {
		t.Fatalf("discovered %d MP4 entries, want 4", mp4Count)
	}
	after := snapshotTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("source tree item count changed: before=%d after=%d", len(before), len(after))
	}
	for relative, want := range before {
		if got, ok := after[relative]; !ok || got != want {
			t.Fatalf("source item changed during scan: %q", relative)
		}
	}
}

type treeItemSnapshot struct {
	Size    int64
	Mode    os.FileMode
	ModTime int64
}

func snapshotTree(t *testing.T, root string) map[string]treeItemSnapshot {
	t.Helper()
	items := map[string]treeItemSnapshot{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		items[relative] = treeItemSnapshot{Size: info.Size(), Mode: info.Mode(), ModTime: info.ModTime().UnixNano()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return items
}
