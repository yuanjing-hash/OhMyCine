package services

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

type pan115CatalogDeletionFixture struct {
	service *MediaLibraryService
	queue   *QueueService
	driver  *fakeMutationCloudDriver
	library models.MediaLibrary
	actor   Actor
	workKey string
	work    string
	entries []models.MediaLibraryEntry
}

func newPan115CatalogDeletionFixture(t *testing.T, fileCount int) pan115CatalogDeletionFixture {
	t.Helper()
	cloudFixture := newCloudTransferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	service := NewMediaLibraryService(cloudFixture.queue.db, cloudFixture.queue.audit, zerolog.Nop())
	service.SetConnectionService(cloudFixture.service.connections)
	var user models.User
	if err := service.db.First(&user, cloudFixture.download.OwnerID).Error; err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: user, Permissions: map[string]struct{}{authz.PermissionMediaLibrariesMediaDelete: {}}}
	now := time.Now().UTC().Truncate(time.Second)
	workKey := "movie:pan115-delete"
	entries := make([]models.MediaLibraryEntry, 0, fileCount)
	for index := 0; index < fileCount; index++ {
		id := "catalog-delete-" + string(rune('a'+index))
		name := "Delete-" + string(rune('A'+index)) + ".mkv"
		size := int64(100 + index)
		cloudFixture.driver.items[id] = cloudpkg.Item{ID: id, ParentID: cloudFixture.library.ProviderRootID, Name: name, Size: size, SHA1: "SHA-" + id, ModifiedAt: now}
		entries = append(entries, models.MediaLibraryEntry{
			LibraryID: cloudFixture.library.ID, RelativePath: "/" + name, ProviderID: id, Size: size, ModifiedAt: now,
			MediaType: "movie", Title: "Delete", WorkKey: workKey, MatchStatus: "unrecognized", CategoryName: "电影",
			CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := service.db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}
	return pan115CatalogDeletionFixture{service: service, queue: cloudFixture.queue, driver: cloudFixture.driver, library: cloudFixture.library, actor: actor, workKey: workKey, work: encodeCatalogToken(workKey), entries: entries}
}

func (f pan115CatalogDeletionFixture) preview(t *testing.T) MediaCatalogDeletionPreviewResult {
	t.Helper()
	preview, err := f.service.PreviewCatalogDeletion(context.Background(), f.actor, f.library.ID, f.work, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func TestPan115CatalogDeletionRejectsUnprovenLibraryRootAncestry(t *testing.T) {
	fixture := newPan115CatalogDeletionFixture(t, 1)
	root := fixture.driver.items[fixture.library.ProviderRootID]
	root.ParentID = "source-root"
	fixture.driver.items[root.ID] = root
	if _, err := fixture.service.PreviewCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, RequestContext{}); ErrorCode(err) != CodeMediaCatalogDeletionChanged {
		t.Fatalf("root ancestry error=%v code=%q", err, ErrorCode(err))
	}
	if len(fixture.driver.recycled) != 0 {
		t.Fatalf("unexpected recycle=%v", fixture.driver.recycled)
	}
}

func TestPan115CatalogDeletionRejectsProviderIdentityDrift(t *testing.T) {
	mutations := map[string]func(cloudpkg.Item) cloudpkg.Item{
		"id":     func(item cloudpkg.Item) cloudpkg.Item { item.ID = "replacement-id"; return item },
		"parent": func(item cloudpkg.Item) cloudpkg.Item { item.ParentID = "target-storage-root"; return item },
		"name":   func(item cloudpkg.Item) cloudpkg.Item { item.Name = "Replacement.mkv"; return item },
		"size":   func(item cloudpkg.Item) cloudpkg.Item { item.Size++; return item },
		"sha1":   func(item cloudpkg.Item) cloudpkg.Item { item.SHA1 = "REPLACEMENT-SHA1"; return item },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			fixture := newPan115CatalogDeletionFixture(t, 1)
			preview := fixture.preview(t)
			id := fixture.entries[0].ProviderID
			fixture.driver.items[id] = mutate(fixture.driver.items[id])
			if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeMediaCatalogDeletionPartial {
				t.Fatalf("drift confirm error=%v code=%q", err, ErrorCode(err))
			}
			if len(fixture.driver.recycled) != 0 {
				t.Fatalf("drift recycled=%v", fixture.driver.recycled)
			}
		})
	}
}

func TestPan115CatalogDeletionUsesExactRecycleAndConvergesMissing(t *testing.T) {
	t.Run("recycle", func(t *testing.T) {
		fixture := newPan115CatalogDeletionFixture(t, 1)
		preview := fixture.preview(t)
		result, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Deleted || result.RemovedFiles != 1 || result.MissingFiles != 0 || !slices.Equal(fixture.driver.recycled, []string{fixture.entries[0].ProviderID}) {
			t.Fatalf("result=%+v recycled=%v", result, fixture.driver.recycled)
		}
	})

	t.Run("missing", func(t *testing.T) {
		fixture := newPan115CatalogDeletionFixture(t, 1)
		delete(fixture.driver.items, fixture.entries[0].ProviderID)
		preview := fixture.preview(t)
		if preview.MissingCount != 1 {
			t.Fatalf("preview=%+v", preview)
		}
		result, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Deleted || result.RemovedFiles != 0 || result.MissingFiles != 1 || len(fixture.driver.recycled) != 0 {
			t.Fatalf("result=%+v recycled=%v", result, fixture.driver.recycled)
		}
		var count int64
		if err := fixture.service.db.Model(&models.MediaLibraryEntry{}).Where("id = ?", fixture.entries[0].ID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("entry count=%d err=%v", count, err)
		}
	})
}

func TestPan115CatalogDeletionPersistsPartialCheckpointAndResumesIdempotently(t *testing.T) {
	fixture := newPan115CatalogDeletionFixture(t, 2)
	preview := fixture.preview(t)
	fixture.driver.recycleFailID = fixture.entries[1].ProviderID
	if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeMediaCatalogDeletionPartial {
		t.Fatalf("partial error=%v code=%q", err, ErrorCode(err))
	}
	if !slices.Equal(fixture.driver.recycled, []string{fixture.entries[0].ProviderID}) {
		t.Fatalf("first attempt recycled=%v", fixture.driver.recycled)
	}
	var claim models.MediaCatalogDeletionPreview
	if err := fixture.service.db.Where("token_hash <> ''").Order("created_at DESC").First(&claim).Error; err != nil {
		t.Fatal(err)
	}
	var state mediaCatalogDeletionState
	if err := jsonUnmarshalForTest(claim.StateJSON, &state); err != nil {
		t.Fatal(err)
	}
	if !state.Completed[fixture.entries[0].ID] || state.Completed[fixture.entries[1].ID] || state.Removed != 1 || claim.StartedAt != nil {
		t.Fatalf("checkpoint=%+v claim=%+v", state, claim)
	}
	fixture.driver.recycleFailID = ""
	result, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedFiles != 2 || !slices.Equal(fixture.driver.recycled, []string{fixture.entries[0].ProviderID, fixture.entries[1].ProviderID}) {
		t.Fatalf("resume result=%+v recycled=%v", result, fixture.driver.recycled)
	}
}

func TestPan115CatalogDeletionTokenAndBoundaryClaims(t *testing.T) {
	t.Run("actor mismatch", func(t *testing.T) {
		fixture := newPan115CatalogDeletionFixture(t, 1)
		preview := fixture.preview(t)
		other := fixture.actor
		other.User.ID++
		if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), other, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeMediaCatalogDeletionExpired {
			t.Fatalf("actor mismatch error=%v", err)
		}
	})

	t.Run("library mismatch", func(t *testing.T) {
		fixture := newPan115CatalogDeletionFixture(t, 1)
		preview := fixture.preview(t)
		if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID+999, fixture.work, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeMediaCatalogDeletionExpired {
			t.Fatalf("library mismatch error=%v", err)
		}
		if len(fixture.driver.recycled) != 0 {
			t.Fatalf("cross-library recycle=%v", fixture.driver.recycled)
		}
	})

	t.Run("expiry", func(t *testing.T) {
		fixture := newPan115CatalogDeletionFixture(t, 1)
		preview := fixture.preview(t)
		if err := fixture.service.db.Model(&models.MediaCatalogDeletionPreview{}).Where("library_id = ?", fixture.library.ID).Update("expires_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeMediaCatalogDeletionExpired {
			t.Fatalf("expiry error=%v", err)
		}
	})

	t.Run("entry digest drift", func(t *testing.T) {
		fixture := newPan115CatalogDeletionFixture(t, 1)
		preview := fixture.preview(t)
		if err := fixture.service.db.Model(&models.MediaLibraryEntry{}).Where("id = ?", fixture.entries[0].ID).Update("size", fixture.entries[0].Size+1).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeMediaCatalogDeletionChanged {
			t.Fatalf("digest drift error=%v", err)
		}
		if len(fixture.driver.recycled) != 0 {
			t.Fatalf("digest drift recycled=%v", fixture.driver.recycled)
		}
	})

	t.Run("library boundary drift", func(t *testing.T) {
		fixture := newPan115CatalogDeletionFixture(t, 1)
		preview := fixture.preview(t)
		if err := fixture.service.db.Model(&models.MediaLibrary{}).Where("id = ?", fixture.library.ID).Update("provider_root_id", "source-root").Error; err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeMediaCatalogDeletionChanged {
			t.Fatalf("boundary drift error=%v", err)
		}
		if len(fixture.driver.recycled) != 0 {
			t.Fatalf("boundary drift recycled=%v", fixture.driver.recycled)
		}
	})

	t.Run("replay", func(t *testing.T) {
		fixture := newPan115CatalogDeletionFixture(t, 1)
		preview := fixture.preview(t)
		if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{}); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeMediaCatalogDeletionExpired {
			t.Fatalf("replay error=%v", err)
		}
	})
}

func TestPan115CatalogDeletionQueuesArtifactsBeforePublishingRefresh(t *testing.T) {
	fixture := newPan115CatalogDeletionFixture(t, 1)
	projection := t.TempDir()
	if err := fixture.service.db.Model(&models.MediaLibrary{}).Where("id = ?", fixture.library.ID).Updates(map[string]any{
		"strm_enabled": true, "signed_proxy_enabled": true, "strm_local_root": projection, "metadata_artifacts_enabled": true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.db.First(&fixture.library, fixture.library.ID).Error; err != nil {
		t.Fatal(err)
	}
	store, err := credential.Open(filepath.Join(t.TempDir(), "credential.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewSignedProxyService(fixture.service.db, store, fixture.service.connections, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	changes := NewMediaChangeService(fixture.service.db)
	artifacts := NewMediaArtifactService(fixture.service.db, fixture.queue, proxy, zerolog.Nop())
	artifacts.SetConnectionService(fixture.service.connections)
	artifacts.SetMediaChangeService(changes)
	fixture.service.SetMediaChangeService(changes)
	fixture.service.SetArtifactService(artifacts)
	now := time.Now().UTC()
	target := models.MediaServerRefreshTarget{LibraryID: fixture.library.ID, ConnectionID: *fixture.serviceStorageConnectionID(t, fixture.library.StorageID), UpstreamLibraryID: "upstream", UpstreamLibraryName: "电影", Enabled: true, LastStatus: "idle", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := fixture.service.db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	otherRecognition := models.MediaLibraryRecognition{LibraryID: fixture.library.ID, SourceKey: "other", InputFingerprint: "other", ProfileID: fixture.library.ProfileID, ProfileRevision: fixture.library.ProfileRevision, Status: mediaRecognitionStatusMatched, MediaType: "movie", Title: "Other", MetadataJSON: `{"version":1,"classification":{}}`, LastGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := fixture.service.db.Create(&otherRecognition).Error; err != nil {
		t.Fatal(err)
	}
	sourceAsset := models.MediaLibrarySourceAsset{LibraryID: fixture.library.ID, Generation: 1, ProviderID: "other-subtitle", RelativePath: "/Other.srt", Name: "Other.srt", Extension: ".srt", Size: 1, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := fixture.service.db.Create(&sourceAsset).Error; err != nil {
		t.Fatal(err)
	}
	wake := make(chan struct{}, 1)
	done := make(chan struct{})
	close(done)
	fixture.service.supervisors[fixture.library.ID] = supervisorHandle{cancel: func() {}, done: done, wake: wake}
	preview := fixture.preview(t)
	if _, err := fixture.service.ConfirmCatalogDeletion(context.Background(), fixture.actor, fixture.library.ID, fixture.work, preview.ConfirmationToken, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("library reconcile was not woken")
	}
	var library models.MediaLibrary
	if err := fixture.service.db.First(&library, fixture.library.ID).Error; err != nil {
		t.Fatal(err)
	}
	var change models.MediaLibraryChange
	if err := fixture.service.db.Where("library_id = ?", fixture.library.ID).Order("sequence DESC").First(&change).Error; err != nil {
		t.Fatal(err)
	}
	if change.Kind != models.MediaLibraryChangeRemoval || change.State != models.MediaLibraryChangePending || change.Generation != library.ArtifactGeneration || target.DesiredRevision != 0 {
		t.Fatalf("library=%+v change=%+v target=%+v", library, change, target)
	}
	var run models.MediaArtifactRun
	if err := fixture.service.db.Where("library_id = ? AND generation = ?", fixture.library.ID, library.ArtifactGeneration).First(&run).Error; err != nil || run.Status != models.MediaArtifactStatusQueued || run.JobID == nil {
		t.Fatalf("artifact run=%+v err=%v", run, err)
	}
	if err := fixture.service.db.First(&otherRecognition, otherRecognition.ID).Error; err != nil || otherRecognition.LastGeneration != library.ArtifactGeneration {
		t.Fatalf("recognition=%+v err=%v", otherRecognition, err)
	}
	if err := fixture.service.db.First(&sourceAsset, sourceAsset.ID).Error; err != nil || sourceAsset.Generation != library.ArtifactGeneration {
		t.Fatalf("source asset=%+v err=%v", sourceAsset, err)
	}
	// The carry-forward assertions above exercise the deletion transaction.
	// Remove these synthetic facts before running the real artifact worker;
	// unlike a scanned source asset they deliberately have no provider bytes.
	if err := fixture.service.db.Delete(&sourceAsset).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.db.Delete(&otherRecognition).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.queue.Claim([]string{JobTypeMediaArtifact})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewMediaArtifactWorker(artifacts).Run(context.Background(), &providerWakeRuntime{}, *claimed)
	if result.ErrorCode != "" || result.RetryAt != nil {
		t.Fatalf("artifact result=%+v", result)
	}
	if err := fixture.service.db.First(&change, change.Sequence).Error; err != nil || change.State != models.MediaLibraryChangeReady {
		t.Fatalf("published change=%+v err=%v", change, err)
	}
	if err := fixture.service.db.First(&target, target.ID).Error; err != nil || target.DesiredRevision != change.Revision {
		t.Fatalf("refresh target=%+v change=%+v err=%v", target, change, err)
	}
}

func (f pan115CatalogDeletionFixture) serviceStorageConnectionID(t *testing.T, storageID uint) *uint {
	t.Helper()
	var storage models.Storage
	if err := f.service.db.First(&storage, storageID).Error; err != nil {
		t.Fatal(err)
	}
	if storage.ConnectionID == nil {
		t.Fatal("storage connection is missing")
	}
	return storage.ConnectionID
}

func jsonUnmarshalForTest(raw string, value any) error {
	return json.Unmarshal([]byte(raw), value)
}
