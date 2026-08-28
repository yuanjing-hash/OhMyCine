package database

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrationV56AddsRouteSnapshotsAndFreezesOneDefaultIngestLibrary(t *testing.T) {
	db, err := Open(t.TempDir() + "/migration-v56.db")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 55)
	if err := seedMediaClassificationProfiles(db); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	owner := models.User{Username: "v56-owner", UsernameNormalized: "v56-owner", DisplayName: "v56 owner", PasswordHash: "x", Status: models.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	connection := models.Connection{Name: "115", NameNormalized: "v56-115-" + uuid.NewString(), Provider: models.ConnectionProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	storage := models.Storage{Name: "115", NameNormalized: "v56-storage-" + uuid.NewString(), Type: models.StorageTypePan115, RootPath: "0", RootPathNormalized: "v56:0:" + uuid.NewString(), ConnectionID: &connection.ID, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	libraryA := legacyV56Library("A", storage.ID, profile.ID, 20, true, now)
	libraryB := legacyV56Library("B", storage.ID, profile.ID, 10, false, now)
	if err := db.Omit("DefaultIngestConnectionID").Create(&libraryA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Omit("DefaultIngestConnectionID").Create(&libraryB).Error; err != nil {
		t.Fatal(err)
	}
	downloader := models.Downloader{ID: uuid.NewString(), OwnerID: owner.ID, Name: "115", NameNormalized: "v56-downloader-" + uuid.NewString(), Type: models.DownloaderTypePan115Offline, StorageID: &storage.ID, ProviderDirectoryID: "downloads", AutoListenLifeEvents: true, Enabled: true, CapabilitiesJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&downloader).Error; err != nil {
		t.Fatal(err)
	}
	connectionB := models.Connection{Name: "115 B", NameNormalized: "v56-115-b-" + uuid.NewString(), Provider: models.ConnectionProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&connectionB).Error; err != nil {
		t.Fatal(err)
	}
	storageB := models.Storage{Name: "115 B", NameNormalized: "v56-storage-b-" + uuid.NewString(), Type: models.StorageTypePan115, RootPath: "0", RootPathNormalized: "v56:b:0:" + uuid.NewString(), ConnectionID: &connectionB.ID, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storageB).Error; err != nil {
		t.Fatal(err)
	}
	libraryBConnection := legacyV56Library("B connection", storageB.ID, profile.ID, 30, false, now)
	if err := db.Omit("DefaultIngestConnectionID").Create(&libraryBConnection).Error; err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	legacyJobs := []models.Job{
		{ID: "v56-same-job", OwnerID: &ownerID, JobType: "download", Status: models.JobStatusQueued, DisplayName: "same", Provider: models.DownloaderTypePan115Offline, Revision: 1, Generation: 1, PayloadJSON: `{}`, CheckpointJSON: `{}`, CreatedAt: now, UpdatedAt: now},
		{ID: "v56-cross-job", OwnerID: &ownerID, JobType: "download", Status: models.JobStatusQueued, DisplayName: "cross", Provider: models.DownloaderTypePan115Offline, Revision: 1, Generation: 1, PayloadJSON: `{}`, CheckpointJSON: `{}`, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&legacyJobs).Error; err != nil {
		t.Fatal(err)
	}
	legacyTasks := []models.DownloadTask{
		{ID: "v56-same", OwnerID: ownerID, JobID: legacyJobs[0].ID, DownloaderID: &downloader.ID, DownloaderName: downloader.Name, ProviderType: models.DownloaderTypePan115Offline, SourceCiphertext: "encrypted", StagingStorageID: &storage.ID, TargetLibraryID: &libraryA.ID, TargetStorageID: &storage.ID, TargetStorageType: models.StorageTypePan115, TargetConnectionID: &connection.ID, DisplayName: "same", Phase: models.DownloadTaskStatusQueued, CreatedAt: now, UpdatedAt: now},
		{ID: "v56-cross", OwnerID: ownerID, JobID: legacyJobs[1].ID, DownloaderID: &downloader.ID, DownloaderName: downloader.Name, ProviderType: models.DownloaderTypePan115Offline, SourceCiphertext: "encrypted", StagingStorageID: &storage.ID, TargetLibraryID: &libraryBConnection.ID, TargetStorageID: &storageB.ID, TargetStorageType: models.StorageTypePan115, TargetConnectionID: &connectionB.ID, DisplayName: "cross", Phase: models.DownloadTaskStatusQueued, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Omit("SourceDataSourceJSON", "TargetDataSourceJSON", "TransferRouteKind", "TransferRouteVersion").Create(&legacyTasks).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source_data_source_json", "target_data_source_json", "transfer_route_kind", "transfer_route_version"} {
		if !db.Migrator().HasColumn(&models.DownloadTask{}, field) {
			t.Fatalf("download task v56 column %s missing", field)
		}
	}
	for _, field := range []string{"source_data_source_json", "target_data_source_json", "route_kind", "route_version"} {
		if !db.Migrator().HasColumn(&models.TransferTask{}, field) {
			t.Fatalf("transfer task v56 column %s missing", field)
		}
	}
	if !db.Migrator().HasColumn(&models.MediaLibrary{}, "default_ingest_connection_id") {
		t.Fatal("default ingest connection column missing")
	}
	var reloadedA, reloadedB models.MediaLibrary
	if err := db.First(&reloadedA, libraryA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&reloadedB, libraryB.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedA.DefaultIngestConnectionID == nil || *reloadedA.DefaultIngestConnectionID != connection.ID || reloadedB.DefaultIngestConnectionID != nil {
		t.Fatalf("legacy explicit default was not preserved: A=%v B=%v", reloadedA.DefaultIngestConnectionID, reloadedB.DefaultIngestConnectionID)
	}
	var sameTask, crossTask models.DownloadTask
	if err := db.First(&sameTask, "id = ?", "v56-same").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&crossTask, "id = ?", "v56-cross").Error; err != nil {
		t.Fatal(err)
	}
	connectionIdentity := `"connection_identity":"` + strconv.FormatUint(uint64(connection.ID), 10) + `"`
	sourceScope := `"storage_scope":"` + strconv.FormatUint(uint64(storage.ID), 10) + `"`
	if sameTask.TransferRouteKind != models.TransferRouteSameSourceProvider || sameTask.TransferRouteVersion != models.TransferRouteVersionCurrent || !strings.Contains(sameTask.SourceDataSourceJSON, connectionIdentity) || !strings.Contains(sameTask.SourceDataSourceJSON, sourceScope) || !strings.Contains(sameTask.TargetDataSourceJSON, sourceScope) {
		t.Fatalf("unambiguous same-source task was not frozen: %+v", sameTask)
	}
	if crossTask.TransferRouteKind != "" || crossTask.TransferRouteVersion != 0 {
		t.Fatalf("legacy cross-source task was unexpectedly rerouted: %+v", crossTask)
	}
	if err := db.Model(&reloadedB).Update("default_ingest_connection_id", connection.ID).Error; err == nil {
		t.Fatal("connection accepted two default ingest libraries")
	}
	var indexSQL string
	if err := db.Raw(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_media_libraries_default_ingest_connection'`).Scan(&indexSQL).Error; err != nil || !strings.Contains(strings.ToUpper(indexSQL), "WHERE DEFAULT_INGEST_CONNECTION_ID IS NOT NULL") {
		t.Fatalf("default partial unique index=%q err=%v", indexSQL, err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 56).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v56 migration count=%d err=%v", count, err)
	}
}

func legacyV56Library(name string, storageID, profileID uint, sortOrder int, ingest bool, now time.Time) models.MediaLibrary {
	return models.MediaLibrary{
		Name: name, NameNormalized: "v56-library-" + strings.ToLower(name) + "-" + uuid.NewString(), StorageID: storageID,
		ProfileID: profileID, ProfileRevision: 1, RelativeRoot: "/" + name, ProviderRootID: strings.ToLower(name), SortOrder: sortOrder,
		TransferMode: models.MediaLibraryTransferMove, ConflictPolicy: models.MediaLibraryConflictAsk, Enabled: true, Recursive: true,
		VideoExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, IngestEnabled: ingest, Status: models.MediaLibraryStatusDisabled, CreatedAt: now, UpdatedAt: now,
	}
}
