package services

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

func TestDefaultIngestLibrarySwitchIsConnectionUniqueAndRequiredByListener(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionMediaLibrariesRead] = struct{}{}
	actor.Permissions[authz.PermissionMediaLibrariesUpdate] = struct{}{}
	now := time.Now().UTC()
	connection := createRouteConnection(t, queue, "default", now)
	driver := newFakeMutationCloudDriver()
	for _, item := range []cloudpkg.Item{{ID: "0", IsDir: true}, {ID: "storage", ParentID: "0", IsDir: true}, {ID: "library-a", ParentID: "storage", IsDir: true}, {ID: "library-b", ParentID: "storage", IsDir: true}} {
		driver.items[item.ID] = item
	}
	connections := &ConnectionService{db: queue.db, drivers: map[uint]cloudpkg.Driver{connection.ID: driver}}
	storage := createRouteStorage(t, queue, "default", connection.ID, "storage", now)
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	libraryA := createRouteLibrary(t, queue, "default-a", storage.ID, profile.ID, "library-a", now)
	libraryB := createRouteLibrary(t, queue, "default-b", storage.ID, profile.ID, "library-b", now)
	libraries := NewMediaLibraryService(queue.db, queue.audit, zerolog.Nop())
	libraries.SetConnectionService(connections)

	if err := requireDefaultIngestForStorage(context.Background(), queue.db, &storage.ID); ErrorCode(err) != CodeMediaLibraryStorageUnavailable {
		t.Fatalf("listener without default error=%v", err)
	}
	selected, err := libraries.SetDefaultIngestLibrary(context.Background(), actor, libraryA.ID, RequestContext{})
	if err != nil || selected.LibraryID != libraryA.ID || selected.ConnectionID != connection.ID {
		t.Fatalf("set default=%+v err=%v", selected, err)
	}
	if err := requireDefaultIngestForStorage(context.Background(), queue.db, &storage.ID); err != nil {
		t.Fatalf("listener rejected with default: %v", err)
	}
	selected, err = libraries.SetDefaultIngestLibrary(context.Background(), actor, libraryB.ID, RequestContext{})
	if err != nil || selected.LibraryID != libraryB.ID {
		t.Fatalf("switch default=%+v err=%v", selected, err)
	}
	var reloadedA, reloadedB models.MediaLibrary
	_ = queue.db.First(&reloadedA, libraryA.ID).Error
	_ = queue.db.First(&reloadedB, libraryB.ID).Error
	if reloadedA.DefaultIngestConnectionID != nil || reloadedB.DefaultIngestConnectionID == nil || *reloadedB.DefaultIngestConnectionID != connection.ID {
		t.Fatalf("switched defaults A=%v B=%v", reloadedA.DefaultIngestConnectionID, reloadedB.DefaultIngestConnectionID)
	}
	downloader := models.Downloader{ID: uuid.NewString(), OwnerID: actor.User.ID, Name: "listen", NameNormalized: "listen-" + uuid.NewString(), Type: models.DownloaderTypePan115Offline, StorageID: &storage.ID, ProviderDirectoryID: "downloads", AutoListenLifeEvents: true, Enabled: true, CapabilitiesJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&downloader).Error; err != nil {
		t.Fatal(err)
	}
	if err := libraries.ClearDefaultIngestLibrary(context.Background(), actor, connection.ID, RequestContext{}); ErrorCode(err) != CodeConflict {
		t.Fatalf("cleared default while listener active: %v", err)
	}
	if err := queue.db.Model(&downloader).Update("auto_listen_life_events", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := libraries.ClearDefaultIngestLibrary(context.Background(), actor, connection.ID, RequestContext{}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultIngestLibrarySurvivesSafeUpdatesAndGuardsDestructiveChanges(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionMediaLibrariesRead] = struct{}{}
	actor.Permissions[authz.PermissionMediaLibrariesUpdate] = struct{}{}
	actor.Permissions[authz.PermissionMediaLibrariesDelete] = struct{}{}
	now := time.Now().UTC()
	connectionA := createRouteConnection(t, queue, "guard-a", now)
	connectionB := createRouteConnection(t, queue, "guard-b", now)
	driverA := newFakeMutationCloudDriver()
	for _, item := range []cloudpkg.Item{{ID: "0", IsDir: true}, {ID: "storage-a", ParentID: "0", IsDir: true}, {ID: "library-a", ParentID: "storage-a", IsDir: true}, {ID: "library-a-renamed", ParentID: "storage-a", IsDir: true}, {ID: "downloads", ParentID: "storage-a", IsDir: true}} {
		driverA.items[item.ID] = item
	}
	driverB := newFakeMutationCloudDriver()
	for _, item := range []cloudpkg.Item{{ID: "0", IsDir: true}, {ID: "storage-b", ParentID: "0", IsDir: true}, {ID: "library-b", ParentID: "storage-b", IsDir: true}} {
		driverB.items[item.ID] = item
	}
	connections := &ConnectionService{db: queue.db, drivers: map[uint]cloudpkg.Driver{connectionA.ID: driverA, connectionB.ID: driverB}}
	storageA := createRouteStorage(t, queue, "guard-a", connectionA.ID, "storage-a", now)
	storageB := createRouteStorage(t, queue, "guard-b", connectionB.ID, "storage-b", now)
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	library := createRouteLibrary(t, queue, "guard", storageA.ID, profile.ID, "library-a", now)
	service := NewMediaLibraryService(queue.db, queue.audit, zerolog.Nop())
	service.SetConnectionService(connections)
	t.Cleanup(service.Close)
	if _, err := service.SetDefaultIngestLibrary(context.Background(), actor, library.ID, RequestContext{}); err != nil {
		t.Fatal(err)
	}

	input := defaultIngestUpdateInput(library, storageA.ID, "library-a", true)
	input.Name = "guard renamed"
	updated, err := service.Update(context.Background(), actor, library.ID, input, RequestContext{})
	if err != nil || updated.DefaultIngestConnectionID == nil || *updated.DefaultIngestConnectionID != connectionA.ID {
		t.Fatalf("unrelated update lost default: default=%v err=%v", updated.DefaultIngestConnectionID, err)
	}
	input = defaultIngestUpdateInput(updated.MediaLibrary, storageA.ID, "library-a-renamed", true)
	updated, err = service.Update(context.Background(), actor, library.ID, input, RequestContext{})
	if err != nil || updated.DefaultIngestConnectionID == nil || *updated.DefaultIngestConnectionID != connectionA.ID {
		t.Fatalf("same-connection root update lost default: default=%v err=%v", updated.DefaultIngestConnectionID, err)
	}

	listener := models.Downloader{ID: uuid.NewString(), OwnerID: actor.User.ID, Name: "guard-listener", NameNormalized: "guard-listener-" + uuid.NewString(), Type: models.DownloaderTypePan115Offline, StorageID: &storageA.ID, ProviderDirectoryID: "downloads", AutoListenLifeEvents: true, Enabled: true, CapabilitiesJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&listener).Error; err != nil {
		t.Fatal(err)
	}
	disable := defaultIngestUpdateInput(updated.MediaLibrary, storageA.ID, "library-a-renamed", false)
	if _, err := service.Update(context.Background(), actor, library.ID, disable, RequestContext{}); ErrorCode(err) != CodeConflict {
		t.Fatalf("disabled default while listener active: %v", err)
	}
	if err := service.Delete(actor, library.ID, RequestContext{}); ErrorCode(err) != CodeConflict {
		t.Fatalf("deleted default while listener active: %v", err)
	}
	var preserved models.MediaLibrary
	if err := queue.db.First(&preserved, library.ID).Error; err != nil || !preserved.Enabled || preserved.DefaultIngestConnectionID == nil {
		t.Fatalf("blocked change mutated default library: %+v err=%v", preserved, err)
	}

	if err := queue.db.Model(&listener).Update("auto_listen_life_events", false).Error; err != nil {
		t.Fatal(err)
	}
	crossConnection := defaultIngestUpdateInput(preserved, storageB.ID, "library-b", true)
	updated, err = service.Update(context.Background(), actor, library.ID, crossConnection, RequestContext{})
	if err != nil || updated.DefaultIngestConnectionID != nil || updated.StorageID != storageB.ID {
		t.Fatalf("different-connection update did not clear default: %+v err=%v", updated.MediaLibrary, err)
	}
}

func defaultIngestUpdateInput(library models.MediaLibrary, storageID uint, providerRoot string, enabled bool) MediaLibraryInput {
	return MediaLibraryInput{
		Name: library.Name, StorageID: storageID, ProfileID: library.ProfileID, RelativeRoot: library.RelativeRoot,
		ProviderRootID: providerRoot, Enabled: enabled, Recursive: library.Recursive, TransferMode: library.TransferMode,
		ConflictPolicy: library.ConflictPolicy,
	}
}
