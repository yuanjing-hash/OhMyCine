package services

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

func TestMediaChangeReadyCommitAdvancesRevisionAndTargets(t *testing.T) {
	management, _, _, library, _ := strmManagementFixture(t)
	db := management.db
	now := time.Now().UTC()
	connection := models.Connection{Name: "Refresh Emby", NameNormalized: "refresh-emby", Provider: models.ConnectionProviderEmby, Endpoint: "https://emby.example.test", CredentialCiphertext: "encrypted", Enabled: true, LastHealthStatus: "unknown", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	target := models.MediaServerRefreshTarget{LibraryID: library.ID, ConnectionID: connection.ID, UpstreamLibraryID: "library-id", UpstreamLibraryName: "电影", Enabled: true, LastStatus: "idle", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	changes := NewMediaChangeService(db)
	var change models.MediaLibraryChange
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		change, err = changes.RecordTx(tx, library.ID, 3, models.MediaLibraryChangeCatalog, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if change.State != models.MediaLibraryChangeReady || change.Revision != 1 {
		t.Fatalf("change=%+v", change)
	}
	if err := db.First(&library, library.ID).Error; err != nil || library.ContentRevision != 1 {
		t.Fatalf("library revision=%d err=%v", library.ContentRevision, err)
	}
	if err := db.First(&target, target.ID).Error; err != nil || target.DesiredRevision != 1 {
		t.Fatalf("target desired=%d err=%v", target.DesiredRevision, err)
	}
}

func TestManualMetadataChangeWaitsForRegeneratedArtifacts(t *testing.T) {
	libraries, db, actor, storage, profile := mediaLibraryTestService(t)
	metadataArtifacts := true
	created, err := libraries.Create(context.Background(), actor, MediaLibraryInput{
		Name: "Metadata barrier", StorageID: storage.ID, ProfileID: profile.ID, RelativeRoot: "/",
		Recursive: true, MetadataArtifactsEnabled: &metadataArtifacts,
	}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	recognition := models.MediaLibraryRecognition{
		LibraryID: created.ID, SourceKey: "metadata-barrier", InputFingerprint: "metadata-barrier-fingerprint",
		ProfileID: profile.ID, ProfileRevision: profile.Revision, Status: mediaRecognitionStatusMatched,
		MediaType: "movie", Title: "Before", MetadataJSON: `{"version":1,"classification":{}}`,
		LastGeneration: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&recognition).Error; err != nil {
		t.Fatal(err)
	}
	entry := models.MediaLibraryEntry{
		LibraryID: created.ID, RelativePath: "/Before.mkv", RecognitionID: &recognition.ID,
		Size: 1, ModifiedAt: now, MediaType: "movie", Title: "Before", WorkKey: "movie:before",
		MatchStatus: mediaRecognitionStatusMatched, LastGeneration: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	queue := NewQueueService(db, NewAuditService(db))
	changes := NewMediaChangeService(db)
	artifacts := NewMediaArtifactService(db, queue, nil, zerolog.Nop())
	artifacts.SetMediaChangeService(changes)
	libraries.SetArtifactService(artifacts)
	libraries.SetMediaChangeService(changes)
	tmdbID := int64(346)
	confidence := 1.0
	if err := libraries.persistRecognitionResult(recognition, profile, MediaRecognitionResult{
		Status: mediaRecognitionStatusMatched, MediaType: "movie", Title: "After", TMDBID: &tmdbID, Confidence: &confidence,
	}, true); err != nil {
		t.Fatal(err)
	}

	var change models.MediaLibraryChange
	if err := db.Where("library_id = ?", created.ID).First(&change).Error; err != nil {
		t.Fatal(err)
	}
	if change.State != models.MediaLibraryChangePending || change.Generation != 2 {
		t.Fatalf("change=%+v", change)
	}
	page, err := changes.ReadyAfter(0, 10)
	if err != nil || len(page.Changes) != 0 {
		t.Fatalf("ready page=%+v err=%v", page, err)
	}
	var run models.MediaArtifactRun
	if err := db.Where("library_id = ? AND generation = ?", created.ID, change.Generation).First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != models.MediaArtifactStatusQueued {
		t.Fatalf("artifact run=%+v", run)
	}
	if err := db.First(&recognition, recognition.ID).Error; err != nil {
		t.Fatal(err)
	}
	if recognition.LastGeneration != change.Generation || recognition.Title != "After" {
		t.Fatalf("recognition=%+v change=%+v", recognition, change)
	}
}

func TestMediaChangePendingWaitsForMatchingArtifactGeneration(t *testing.T) {
	management, _, _, library, _ := strmManagementFixture(t)
	db := management.db
	changes := NewMediaChangeService(db)
	var pending models.MediaLibraryChange
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		pending, err = changes.RecordTx(tx, library.ID, 7, models.MediaLibraryChangeCatalog, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if pending.State != models.MediaLibraryChangePending {
		t.Fatalf("pending=%+v", pending)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		ready, err := changes.MarkGenerationReadyTx(tx, library.ID, 6)
		if err != nil || len(ready) != 0 {
			t.Fatalf("wrong generation ready=%v err=%v", ready, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		ready, err := changes.MarkGenerationReadyTx(tx, library.ID, 7)
		if err != nil || len(ready) != 1 || ready[0].State != models.MediaLibraryChangeReady {
			t.Fatalf("ready=%v err=%v", ready, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := changes.ReadyAfter(0, 10)
	if err != nil || len(page.Changes) != 1 || page.Changes[0].Sequence != pending.Sequence {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestNewerCompleteArtifactGenerationCarriesLatestOlderPendingChange(t *testing.T) {
	management, _, _, library, _ := strmManagementFixture(t)
	db := management.db
	changes := NewMediaChangeService(db)
	var first, latest models.MediaLibraryChange
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		first, err = changes.RecordTx(tx, library.ID, 4, models.MediaLibraryChangeCatalog, false)
		if err != nil {
			return err
		}
		latest, err = changes.RecordTx(tx, library.ID, 5, models.MediaLibraryChangeMetadata, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		ready, err := changes.MarkGenerationReadyTx(tx, library.ID, 6)
		if err != nil {
			return err
		}
		if len(ready) != 1 || ready[0].Sequence != latest.Sequence || ready[0].State != models.MediaLibraryChangeReady {
			t.Fatalf("carried ready=%+v latest=%+v", ready, latest)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var obsoleteCount int64
	if err := db.Model(&models.MediaLibraryChange{}).Where("sequence = ?", first.Sequence).Count(&obsoleteCount).Error; err != nil || obsoleteCount != 0 {
		t.Fatalf("obsolete pending count=%d err=%v", obsoleteCount, err)
	}
}

func TestMatchingArtifactGenerationSupersedesOlderPendingChange(t *testing.T) {
	management, _, _, library, _ := strmManagementFixture(t)
	db := management.db
	changes := NewMediaChangeService(db)
	var old, current models.MediaLibraryChange
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		old, err = changes.RecordTx(tx, library.ID, 4, models.MediaLibraryChangeCatalog, false)
		if err != nil {
			return err
		}
		current, err = changes.RecordTx(tx, library.ID, 5, models.MediaLibraryChangeRemoval, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		ready, err := changes.MarkGenerationReadyTx(tx, library.ID, 5)
		if err != nil {
			return err
		}
		if len(ready) != 1 || ready[0].Sequence != current.Sequence {
			t.Fatalf("ready=%+v current=%+v", ready, current)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var obsoleteCount int64
	if err := db.Model(&models.MediaLibraryChange{}).Where("sequence = ?", old.Sequence).Count(&obsoleteCount).Error; err != nil || obsoleteCount != 0 {
		t.Fatalf("obsolete pending count=%d err=%v", obsoleteCount, err)
	}
}

func TestMediaChangePruningRetainsLatestReadyRevisionPerLibrary(t *testing.T) {
	management, _, _, firstLibrary, _ := strmManagementFixture(t)
	db := management.db
	secondLibrary := firstLibrary
	secondLibrary.ID = 0
	secondLibrary.Name = "Quiet refresh library"
	secondLibrary.NameNormalized = "quiet refresh library"
	secondLibrary.ContentRevision = 0
	secondLibrary.CreatedAt = time.Now().UTC()
	secondLibrary.UpdatedAt = secondLibrary.CreatedAt
	if err := db.Create(&secondLibrary).Error; err != nil {
		t.Fatal(err)
	}
	changes := NewMediaChangeService(db)
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := changes.RecordTx(tx, firstLibrary.ID, 1, models.MediaLibraryChangeCatalog, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for generation := uint64(1); generation <= 4; generation++ {
		if err := db.Transaction(func(tx *gorm.DB) error {
			_, err := changes.RecordTx(tx, secondLibrary.ID, generation, models.MediaLibraryChangeCatalog, true)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	changes.retained = 2
	changes.prune()
	readyRevision, err := latestReadyMediaRevision(db, firstLibrary.ID)
	if err != nil || readyRevision != 1 {
		t.Fatalf("quiet library ready revision=%d err=%v", readyRevision, err)
	}
	var retained int64
	if err := db.Model(&models.MediaLibraryChange{}).Where("library_id = ? AND state = ?", firstLibrary.ID, models.MediaLibraryChangeReady).Count(&retained).Error; err != nil || retained != 1 {
		t.Fatalf("quiet library retained changes=%d err=%v", retained, err)
	}
}
