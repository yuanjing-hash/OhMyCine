package database

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

func TestOpenUsesImmediateTransactionsToPreventBusySnapshot(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "immediate-transaction.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(2)
	if err := db.Exec(`CREATE TABLE transaction_probe (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transaction_probe(id, value) VALUES (1, 0)`).Error; err != nil {
		t.Fatal(err)
	}

	readReady := make(chan struct{})
	continueWrite := make(chan struct{})
	transactionDone := make(chan error, 1)
	go func() {
		transactionDone <- db.Transaction(func(tx *gorm.DB) error {
			var value int
			if err := tx.Raw(`SELECT value FROM transaction_probe WHERE id = 1`).Scan(&value).Error; err != nil {
				return err
			}
			close(readReady)
			<-continueWrite
			return tx.Exec(`UPDATE transaction_probe SET value = value + 1 WHERE id = 1`).Error
		})
	}()
	select {
	case <-readReady:
	case <-time.After(2 * time.Second):
		t.Fatal("transaction did not reach its read phase")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	writer, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writerStarted := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		close(writerStarted)
		_, err := writer.ExecContext(ctx, `UPDATE transaction_probe SET value = value + 1 WHERE id = 1`)
		writerDone <- err
	}()
	<-writerStarted

	// An immediate transaction owns the write reservation before its read.
	// The competing writer must wait instead of invalidating that snapshot.
	select {
	case err := <-writerDone:
		close(continueWrite)
		if err != nil {
			t.Fatalf("competing writer failed before transaction continued: %v", err)
		}
		t.Fatal("competing writer bypassed the immediate transaction")
	case <-time.After(100 * time.Millisecond):
		close(continueWrite)
	}
	if err := <-transactionDone; err != nil {
		t.Fatalf("read-then-write transaction failed: %v", err)
	}
	if err := <-writerDone; err != nil {
		t.Fatalf("competing writer did not resume after commit: %v", err)
	}
	var value int
	if err := db.Raw(`SELECT value FROM transaction_probe WHERE id = 1`).Scan(&value).Error; err != nil || value != 2 {
		t.Fatalf("value=%d err=%v", value, err)
	}
}

func TestMigrateAddsStorageFoundationAndIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable("storages") || !db.Migrator().HasTable("media_classification_profiles") {
		t.Fatal("foundation table missing")
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 2).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("migration count=%d err=%v", count, err)
	}
	if err := db.Table("media_classification_profiles").Where("code = ?", "default-v1").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("default profile count=%d err=%v", count, err)
	}
	var builtin models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&builtin).Error; err != nil {
		t.Fatal(err)
	}
	wantRules, _ := classification.CanonicalJSON(classification.DefaultRules())
	if builtin.RulesJSON != wantRules || builtin.Revision != 1 || !builtin.Protected {
		t.Fatalf("default profile drifted: %+v", builtin)
	}
	if builtin.BuiltinRecognitionPacksJSON != defaultBuiltinRecognitionPacksJSON {
		t.Fatalf("default built-in packs=%q", builtin.BuiltinRecognitionPacksJSON)
	}
	if !db.Migrator().HasTable(&models.MediaLibraryRecognition{}) || !db.Migrator().HasTable(&models.MediaRecognitionCache{}) {
		t.Fatal("v25 recognition tables missing")
	}
	for _, check := range []struct {
		model  any
		column string
	}{
		{&models.MediaClassificationProfile{}, "builtin_recognition_packs_json"},
		{&models.DownloadTask{}, "profile_builtin_recognition_packs_json"},
		{&models.MediaLibraryEntry{}, "recognition_id"},
		{&models.MediaLibraryEntry{}, "tmdb_id"},
		{&models.MediaLibraryEntry{}, "release_year"},
		{&models.MediaLibraryEntry{}, "match_confidence"},
		{&models.MediaLibraryEntry{}, "recognition_error_code"},
		{&models.MediaLibraryScanRun{}, "matched"},
		{&models.MediaLibraryScanRun{}, "unrecognized"},
		{&models.MediaLibraryScanRun{}, "cache_hits"},
		{&models.MediaLibraryScanRun{}, "recognition_failed"},
	} {
		if !db.Migrator().HasColumn(check.model, check.column) {
			t.Fatalf("v25 column %s missing", check.column)
		}
	}
	for table, indexes := range map[string][]string{
		"media_library_recognitions": {"idx_media_library_recognitions_input", "idx_media_library_recognitions_profile", "idx_media_library_recognitions_status", "idx_media_library_recognitions_generation"},
		"media_library_entries":      {"idx_media_library_entries_recognition", "idx_media_library_entries_tmdb"},
		"media_recognition_cache":    {"idx_media_recognition_cache_expires"},
	} {
		for _, index := range indexes {
			if !db.Migrator().HasIndex(table, index) {
				t.Fatalf("v25 index %s missing", index)
			}
		}
	}
	for _, check := range []struct {
		model  any
		column string
	}{
		{&models.MediaLibrary{}, "ingest_enabled"},
		{&models.MediaLibrary{}, "ingest_downloader_id"},
		{&models.MediaLibrary{}, "ingest_owner_id"},
		{&models.MediaLibrary{}, "ingest_provider_root_id"},
		{&models.MediaLibrary{}, "ingest_relative_root"},
		{&models.DownloadTask{}, "staging_provider_directory_id"},
		{&models.DownloadTask{}, "ingest_source_key"},
		{&models.DownloadTask{}, "source_origin"},
	} {
		if !db.Migrator().HasColumn(check.model, check.column) {
			t.Fatalf("v26 column %s missing", check.column)
		}
	}
	for table, index := range map[string]string{
		"media_libraries": "idx_media_libraries_ingest_downloader_id",
		"download_tasks":  "idx_download_tasks_ingest_source_key",
	} {
		if !db.Migrator().HasIndex(table, index) {
			t.Fatalf("v26 index %s missing", index)
		}
	}
	for _, table := range []any{&models.MediaLibrarySourceAsset{}, &models.MediaArtifactRun{}, &models.MediaArtifact{}, &models.ProxySigningKey{}, &models.EmbyProxyGateway{}} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("v27 table %T missing", table)
		}
	}
	for _, check := range []struct {
		model  any
		column string
	}{
		{&models.MediaLibrary{}, "signed_proxy_enabled"},
		{&models.MediaLibrary{}, "metadata_artifacts_enabled"},
		{&models.MediaLibrary{}, "upload_sidecars"},
		{&models.MediaLibrary{}, "artifact_generation"},
		{&models.MediaLibrary{}, "artifact_applied_generation"},
		{&models.MediaLibrary{}, "artifact_status"},
		{&models.MediaLibrary{}, "artifact_error"},
		{&models.MediaLibrary{}, "artifact_updated_at"},
		{&models.MediaLibrary{}, "artifact_cleanup_removed"},
		{&models.MediaLibrary{}, "artifact_cleanup_error"},
		{&models.MediaLibrary{}, "artifact_cleanup_at"},
		{&models.MediaArtifactRun{}, "cleanup_status"},
		{&models.MediaArtifactRun{}, "cleanup_error_code"},
		{&models.MediaArtifactRun{}, "cleanup_at"},
		{&models.Connection{}, "endpoint"},
		{&models.MediaLibrary{}, "strm_asset_extra_extensions"},
	} {
		if !db.Migrator().HasColumn(check.model, check.column) {
			t.Fatalf("v27 column %s missing", check.column)
		}
	}
}

func TestMediaArtifactsMigrationUpgradesV26WithoutGeneratingArtifacts(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "media-artifacts-v26-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 26)
	if err := seedMediaClassificationProfiles(db); err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, storage := range []models.Storage{
		{Name: "Local", NameNormalized: "local", Type: models.StorageTypeLocal, RootPath: `D:\\Media`, RootDisplayPath: `D:\\Media`, RootPathNormalized: `d:\\media`, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now},
		{Name: "Cloud", NameNormalized: "cloud", Type: models.StorageTypePan115, RootPath: "0", RootDisplayPath: "/", RootPathNormalized: "pan115:0", Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&storage).Error; err != nil {
			t.Fatal(err)
		}
	}
	var storages []models.Storage
	if err := db.Order("id").Find(&storages).Error; err != nil {
		t.Fatal(err)
	}
	insertLibrary := func(name string, storageID uint, strm bool) {
		t.Helper()
		if err := db.Exec(`INSERT INTO media_libraries(name,name_normalized,storage_id,profile_id,profile_revision,relative_root,enabled,recursive,full_scan_interval_hours,incremental_minutes,video_extensions_json,ignore_patterns_json,metadata_language,metadata_region,match_strategy,provider_rate_per_second,provider_concurrency,metadata_rate_per_second,metadata_concurrency,strm_enabled,strm_local_root,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, name, strings.ToLower(name), storageID, profile.ID, profile.Revision, "/", false, true, 24, 15, `[".mkv"]`, `[]`, "zh-CN", "CN", "balanced", 100, 2, 5, 1, strm, "", models.MediaLibraryStatusDisabled, now, now).Error; err != nil {
			t.Fatal(err)
		}
	}
	insertLibrary("Local Library", storages[0].ID, false)
	insertLibrary("Cloud Library", storages[1].ID, true)

	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var libraries []models.MediaLibrary
	if err := db.Order("id").Find(&libraries).Error; err != nil {
		t.Fatal(err)
	}
	if len(libraries) != 2 || !libraries[0].MetadataArtifactsEnabled || !libraries[1].MetadataArtifactsEnabled {
		t.Fatalf("v27 artifact defaults=%+v", libraries)
	}
	for _, library := range libraries {
		if library.SignedProxyEnabled || library.UploadSidecars || library.ArtifactGeneration != 0 || library.ArtifactAppliedGeneration != 0 || library.ArtifactStatus != models.MediaArtifactStatusIdle {
			t.Fatalf("migration activated artifact work for library %+v", library)
		}
	}
	for _, model := range []any{&models.MediaLibrarySourceAsset{}, &models.MediaArtifactRun{}, &models.MediaArtifact{}, &models.ProxySigningKey{}, &models.EmbyProxyGateway{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("migration generated %T rows: count=%d err=%v", model, count, err)
		}
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 27).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v27 migration count=%d err=%v", count, err)
	}
	if err := db.Table("schema_migrations").Where("version = ?", 28).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v28 migration count=%d err=%v", count, err)
	}
	if err := db.Table("schema_migrations").Where("version = ?", 29).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v29 migration count=%d err=%v", count, err)
	}
	if !db.Migrator().HasIndex("emby_proxy_gateways", "idx_emby_proxy_gateways_alias_normalized") {
		t.Fatal("v28 normalized Emby gateway alias index missing")
	}
}

func TestArtifactAutoCleanupMigrationBackfillsHistoricalRuns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "artifact-cleanup-v28-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 28)
	if err := seedMediaClassificationProfiles(db); err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	root := t.TempDir()
	storage := models.Storage{Name: "Cleanup local", NameNormalized: "cleanup-local", Type: models.StorageTypeLocal, RootPath: root, RootDisplayPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Cleanup library", NameNormalized: "cleanup-library", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", Enabled: true, Recursive: true, FullScanIntervalHours: 24, IncrementalMinutes: 15, VideoExtensionsJSON: `[".mkv"]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, MetadataLanguage: "zh-CN", MetadataRegion: "CN", MatchStrategy: "balanced", ProviderRatePerSecond: 100, ProviderConcurrency: 2, MetadataRatePerSecond: 5, MetadataConcurrency: 1, Status: models.MediaLibraryStatusListening, CreatedAt: now, UpdatedAt: now}
	if err := db.Omit("ArtifactCleanupRemoved", "ArtifactCleanupError", "ArtifactCleanupAt").Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id, status string
		generation uint64
	}{{"historical-completed", models.MediaArtifactStatusCompleted, 1}, {"historical-superseded", models.MediaArtifactStatusSuperseded, 2}, {"historical-failed", models.MediaArtifactStatusFailed, 3}} {
		if err := db.Exec(`INSERT INTO media_artifact_runs(id,library_id,generation,policy_json,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, item.id, library.ID, item.generation, `{}`, item.status, now, now).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var runs []models.MediaArtifactRun
	if err := db.Order("generation").Find(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 || runs[0].CleanupStatus != models.MediaArtifactCleanupSkipped || runs[1].CleanupStatus != models.MediaArtifactCleanupSkipped || runs[2].CleanupStatus != models.MediaArtifactCleanupPending {
		t.Fatalf("v29 cleanup backfill=%+v", runs)
	}
	for _, column := range []string{"cleanup_status", "cleanup_error_code", "cleanup_at"} {
		if !db.Migrator().HasColumn(&models.MediaArtifactRun{}, column) {
			t.Fatalf("v29 column %s missing", column)
		}
	}
}

func TestPan115ShareIngestMigrationUpgradesV25AndIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "share-ingest-v25-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 25)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 26).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v26 migration count=%d err=%v", count, err)
	}
	var indexSQL string
	if err := db.Raw(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_download_tasks_ingest_source_key'`).Scan(&indexSQL).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexSQL, "WHERE ingest_source_key <> ''") {
		t.Fatalf("v26 ingest index is not partial: %q", indexSQL)
	}
}

func TestSharedRecognitionMigrationUpgradesV24ProfilesAndIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "recognition-v24-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 24)
	if err := seedMediaClassificationProfiles(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rulesJSON, _ := classification.CanonicalJSON(classification.EmptyRules())
	if err := db.Exec(`INSERT INTO media_classification_profiles(name,name_normalized,kind,protected,schema_version,rules_json,recognition_rules_json,movie_directory_template,movie_filename_template,tv_directory_template,tv_filename_template,revision,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "Legacy Profile", "legacy profile", models.MediaClassificationProfileKindCustom, false, 1, rulesJSON, `[]`, defaultMovieDirectoryTemplateForTest, defaultMovieFilenameTemplateForTest, defaultTVDirectoryTemplateForTest, defaultTVFilenameTemplateForTest, 1, now, now).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var profiles []models.MediaClassificationProfile
	if err := db.Order("id").Find(&profiles).Error; err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profiles=%d, want 2", len(profiles))
	}
	for _, profile := range profiles {
		if profile.BuiltinRecognitionPacksJSON != defaultBuiltinRecognitionPacksJSON {
			t.Fatalf("profile %d built-in packs=%q", profile.ID, profile.BuiltinRecognitionPacksJSON)
		}
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 25).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v25 migration count=%d err=%v", count, err)
	}
}

func TestSharedRecognitionRowsCascadeWithMediaLibrary(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "recognition-cascade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Recognition", NameNormalized: "recognition", Type: models.StorageTypeLocal, RootPath: `D:\Recognition`, RootDisplayPath: `D:\Recognition`, RootPathNormalized: `d:\recognition`, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Recognition", NameNormalized: "recognition", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", Enabled: false, Recursive: true, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, Status: models.MediaLibraryStatusDisabled, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	recognition := models.MediaLibraryRecognition{LibraryID: library.ID, SourceKey: "source-key", InputFingerprint: strings.Repeat("a", 64), ProfileID: profile.ID, ProfileRevision: profile.Revision, Status: "unrecognized", LastGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&recognition).Error; err != nil {
		t.Fatal(err)
	}
	entry := models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/movie.mkv", RecognitionID: &recognition.ID, Size: 1, ModifiedAt: now, MediaType: "movie", Title: "Movie", MatchStatus: "unrecognized", LastGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&library).Error; err != nil {
		t.Fatal(err)
	}
	for model, name := range map[any]string{&models.MediaLibraryRecognition{}: "recognition", &models.MediaLibraryEntry{}: "entry"} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", name, count, err)
		}
	}
}

const (
	defaultMovieDirectoryTemplateForTest = "{category}/{title} ({year})"
	defaultMovieFilenameTemplateForTest  = "{title} ({year})"
	defaultTVDirectoryTemplateForTest    = "{category}/{title} ({year})/Season {season:02}"
	defaultTVFilenameTemplateForTest     = "{title} - S{season:02}E{episode:02}"
)

func applyMigrationsThrough(t *testing.T, db *gorm.DB, maximum int) {
	t.Helper()
	if err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range schemaMigrations() {
		if item.Version > maximum {
			break
		}
		apply := func(connection *gorm.DB) error {
			return connection.Transaction(func(tx *gorm.DB) error {
				if err := item.Apply(tx); err != nil {
					return err
				}
				return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", item.Version, time.Now().UTC()).Error
			})
		}
		var err error
		if item.DisableForeignKeys {
			err = db.Connection(func(connection *gorm.DB) error {
				if err := connection.Exec(`PRAGMA foreign_keys = OFF`).Error; err != nil {
					return err
				}
				applyErr := apply(connection)
				enableErr := connection.Exec(`PRAGMA foreign_keys = ON`).Error
				if applyErr != nil {
					return applyErr
				}
				return enableErr
			})
		} else {
			err = apply(db)
		}
		if err != nil {
			t.Fatalf("apply migration %d: %v", item.Version, err)
		}
	}
}

func TestMigrateUpgradesAuthFoundationDatabaseToStorageFoundation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := migrateAuthFoundation(tx); err != nil {
			return err
		}
		return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", 1, time.Now().UTC()).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable("storages") {
		t.Fatal("storages table missing after v1 to v2 upgrade")
	}
	var versions []int
	if err := db.Table("schema_migrations").Order("version").Pluck("version", &versions).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(versions, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}) {
		t.Fatalf("migration versions=%v, want [1..31]", versions)
	}
	if !db.Migrator().HasTable("connections") || !db.Migrator().HasColumn(&models.Storage{}, "root_display_path") {
		t.Fatal("115 connection foundation missing after upgrade")
	}
	if !db.Migrator().HasColumn(&models.MediaClassificationProfile{}, "recognition_rules_json") || !db.Migrator().HasColumn(&models.MediaClassificationProfile{}, "movie_directory_template") || !db.Migrator().HasColumn(&models.DownloadTask{}, "profile_recognition_rules_json") {
		t.Fatal("profile recognition/naming snapshot columns missing after upgrade")
	}
	if !db.Migrator().HasColumn(&models.MediaClassificationProfile{}, "builtin_recognition_packs_json") || !db.Migrator().HasColumn(&models.DownloadTask{}, "profile_builtin_recognition_packs_json") {
		t.Fatal("built-in recognition pack snapshot columns missing after upgrade")
	}
	if !db.Migrator().HasTable(&models.MediaLibraryRecognition{}) || !db.Migrator().HasTable(&models.MediaRecognitionCache{}) || !db.Migrator().HasColumn(&models.MediaLibraryEntry{}, "recognition_id") || !db.Migrator().HasColumn(&models.MediaLibraryScanRun{}, "recognition_failed") {
		t.Fatal("shared media recognition schema missing after upgrade")
	}
	if !db.Migrator().HasTable(&models.MediaArtifactRun{}) || !db.Migrator().HasTable(&models.MediaArtifact{}) || !db.Migrator().HasTable(&models.ProxySigningKey{}) || !db.Migrator().HasColumn(&models.MediaLibrary{}, "artifact_generation") || !db.Migrator().HasColumn(&models.Connection{}, "endpoint") {
		t.Fatal("media artifact and proxy schema missing after upgrade")
	}
	if !db.Migrator().HasTable(&models.Pan115PlaybackLease{}) || !db.Migrator().HasColumn(&models.Connection{}, "recycle_credential_ciphertext") {
		t.Fatal("115 multi-device playback schema missing after upgrade")
	}
	for _, field := range []string{"source_manifest_json", "cleanup_status", "cleanup_removed", "cleanup_error_code"} {
		if !db.Migrator().HasColumn(&models.TransferTask{}, field) {
			t.Fatalf("download staging cleanup column %s missing after upgrade", field)
		}
	}
	if !db.Migrator().HasTable("downloaders") || !db.Migrator().HasTable("download_tasks") || !db.Migrator().HasTable("download_settings") {
		t.Fatal("downloader tables missing after upgrade")
	}
	if !db.Migrator().HasColumn(&models.DownloadTask{}, "staging_storage_id") || !db.Migrator().HasColumn(&models.DownloadTask{}, "staging_relative_path") {
		t.Fatal("download task staging snapshot columns missing after upgrade")
	}
	if !db.Migrator().HasColumn(&models.DownloadSettings{}, "absolute_path") || !db.Migrator().HasColumn(&models.DownloadTask{}, "staging_absolute_path") {
		t.Fatal("global download staging columns missing after upgrade")
	}
	if !db.Migrator().HasTable("metadata_settings") || !db.Migrator().HasColumn(&models.DownloadTask{}, "profile_rules_json") || !db.Migrator().HasColumn(&models.DownloadTask{}, "scrape_category") {
		t.Fatal("download classification schema missing after upgrade")
	}
	if !db.Migrator().HasTable("transfer_tasks") || !db.Migrator().HasColumn(&models.MediaLibrary{}, "sort_order") || !db.Migrator().HasColumn(&models.MediaLibrary{}, "transfer_mode") || !db.Migrator().HasColumn(&models.DownloadTask{}, "target_library_id") || !db.Migrator().HasColumn(&models.DownloadTask{}, "scrape_year") {
		t.Fatal("library import routing schema missing after upgrade")
	}
	if !db.Migrator().HasColumn(&models.TransferTask{}, "plan_summary_json") {
		t.Fatal("transfer organization summary column missing after upgrade")
	}
	if !db.Migrator().HasColumn(&models.MediaLibrary{}, "provider_root_id") {
		t.Fatal("media library provider root column missing after upgrade")
	}
	if !db.Migrator().HasColumn(&models.Downloader{}, "provider_directory_id") || !db.Migrator().HasColumn(&models.Downloader{}, "provider_directory_path") {
		t.Fatal("115 offline downloader directory columns missing after upgrade")
	}
	if !db.Migrator().HasColumn(&models.DownloadTask{}, "target_storage_type") || !db.Migrator().HasColumn(&models.DownloadTask{}, "target_connection_id") || !db.Migrator().HasColumn(&models.DownloadTask{}, "target_provider_root_id") || !db.Migrator().HasColumn(&models.TransferTask{}, "cloud_state_json") {
		t.Fatal("115 cloud import snapshot columns missing after upgrade")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 25).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v25 migration count=%d err=%v", count, err)
	}
	var metadata models.MetadataSettings
	if err := db.First(&metadata, 1).Error; err != nil || metadata.APIBaseURL != "https://api.tmdb.org/3" || metadata.ImageBaseURL != "https://image.tmdb.org/t/p" || metadata.TMDBCredentialKind != "read_access_token" {
		t.Fatalf("metadata routes=%+v err=%v", metadata, err)
	}
}

func TestMigrateLibraryNamingIntoProfilesPreservesLegacyTemplates(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "profile-naming-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var source models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&source).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Legacy", NameNormalized: "legacy", Type: models.StorageTypeLocal, RootPath: `D:\\Legacy`, RootDisplayPath: `D:\\Legacy`, RootPathNormalized: `d:\\legacy`, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	legacyMovieDirectory := "旧电影/{title} ({year})"
	legacyMovieFilename := "{title}.{year}"
	legacyTVDirectory := "旧剧集/{title}/Season {season:02}"
	legacyTVFilename := "{title}.S{season:02}E{episode:02}"
	newLibrary := func(name, root string) models.MediaLibrary {
		return models.MediaLibrary{Name: name, NameNormalized: strings.ToLower(name), StorageID: storage.ID, ProfileID: source.ID, ProfileRevision: source.Revision, RelativeRoot: root, MovieDirectoryTemplate: legacyMovieDirectory, MovieFilenameTemplate: legacyMovieFilename, TVDirectoryTemplate: legacyTVDirectory, TVFilenameTemplate: legacyTVFilename, Enabled: false, Recursive: true, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, CreatedAt: now, UpdatedAt: now}
	}
	first, second := newLibrary("Legacy A", "/a"), newLibrary("Legacy B", "/b")
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateLibraryNamingIntoProfiles(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&first, first.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&second, second.ID).Error; err != nil {
		t.Fatal(err)
	}
	if first.ProfileID == source.ID || first.ProfileID != second.ProfileID || first.ProfileRevision != 1 || !first.ReclassificationDue {
		t.Fatalf("legacy libraries were not rebound to one migrated profile: first=%+v second=%+v", first, second)
	}
	var migrated models.MediaClassificationProfile
	if err := db.First(&migrated, first.ProfileID).Error; err != nil {
		t.Fatal(err)
	}
	if migrated.Protected || migrated.Kind != models.MediaClassificationProfileKindCustom || migrated.RulesJSON != source.RulesJSON || migrated.MovieDirectoryTemplate != legacyMovieDirectory || migrated.MovieFilenameTemplate != legacyMovieFilename || migrated.TVDirectoryTemplate != legacyTVDirectory || migrated.TVFilenameTemplate != legacyTVFilename {
		t.Fatalf("migrated profile=%+v", migrated)
	}
	var profileCount int64
	if err := db.Model(&models.MediaClassificationProfile{}).Count(&profileCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateLibraryNamingIntoProfiles(db); err != nil {
		t.Fatal(err)
	}
	var repeatedCount int64
	if err := db.Model(&models.MediaClassificationProfile{}).Count(&repeatedCount).Error; err != nil || repeatedCount != profileCount {
		t.Fatalf("repeat migration created profiles: before=%d after=%d err=%v", profileCount, repeatedCount, err)
	}
}

func TestMediaLibraryCatalogV21BackfillsOnlyPan115ProviderRoots(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "media-library-catalog-v21.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	statements := []string{
		`CREATE TABLE storages (id INTEGER PRIMARY KEY, type TEXT NOT NULL, root_path TEXT NOT NULL)`,
		`CREATE TABLE media_libraries (id INTEGER PRIMARY KEY, storage_id INTEGER NOT NULL, relative_root TEXT NOT NULL)`,
		`CREATE TABLE media_library_entries (id INTEGER PRIMARY KEY, library_id INTEGER NOT NULL, media_type TEXT NOT NULL, title TEXT NOT NULL)`,
		`INSERT INTO storages(id,type,root_path) VALUES (1,'pan115','provider-media'),(2,'local','D:\\Media')`,
		`INSERT INTO media_libraries(id,storage_id,relative_root) VALUES (1,1,'/'),(2,2,'/')`,
		`INSERT INTO media_library_entries(id,library_id,media_type,title) VALUES (1,1,'tv','示例剧'),(2,1,'tv','示例剧'),(3,1,'movie','示例电影')`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateMediaLibraryCatalogV21(db); err != nil {
		t.Fatal(err)
	}
	var roots []string
	if err := db.Table("media_libraries").Order("id").Pluck("provider_root_id", &roots).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roots, []string{"provider-media", ""}) {
		t.Fatalf("provider roots=%v", roots)
	}
	if !db.Migrator().HasIndex("media_libraries", "idx_media_libraries_provider_root") {
		t.Fatal("provider root index missing")
	}
	var entries []models.MediaLibraryEntry
	if err := db.Order("id").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if entries[0].WorkKey == "" || entries[0].WorkKey != entries[1].WorkKey || entries[0].SeriesTitle != "示例剧" || entries[2].WorkKey == entries[0].WorkKey {
		t.Fatalf("entry catalog backfill=%+v", entries)
	}
	if !db.Migrator().HasIndex("media_library_entries", "idx_media_library_entries_work") || !db.Migrator().HasIndex("media_library_entries", "idx_media_library_entries_search") {
		t.Fatal("media catalog indexes missing")
	}
}

func TestPan115OfflineDownloaderDirectoryMigrationBackfillsStorageRoot(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "pan115-offline-directory-v22.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		`CREATE TABLE storages (id INTEGER PRIMARY KEY, root_path TEXT NOT NULL)`,
		`CREATE TABLE downloaders (id TEXT PRIMARY KEY, type TEXT NOT NULL, storage_id INTEGER)`,
		`INSERT INTO storages(id,root_path) VALUES (1,'provider-root')`,
		`INSERT INTO downloaders(id,type,storage_id) VALUES ('pan','pan115_offline',1),('qbit','qbittorrent',NULL)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migratePan115OfflineDownloaderDirectories(db); err != nil {
		t.Fatal(err)
	}
	var pan models.Downloader
	if err := db.Table("downloaders").Where("id = ?", "pan").Take(&pan).Error; err != nil {
		t.Fatal(err)
	}
	if pan.ProviderDirectoryID != "provider-root" || pan.ProviderDirectoryPath != "/" {
		t.Fatalf("pan directory backfill=%+v", pan)
	}
	var qbit models.Downloader
	if err := db.Table("downloaders").Where("id = ?", "qbit").Take(&qbit).Error; err != nil {
		t.Fatal(err)
	}
	if qbit.ProviderDirectoryID != "" || qbit.ProviderDirectoryPath != "" {
		t.Fatalf("qbit directory must remain empty: %+v", qbit)
	}
}

func TestAutomaticClassificationMigrationRequeuesOnlyLegacyDownloadPrompt(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "automatic-classification-v12.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	prior := []migration{{Version: 1, Apply: migrateAuthFoundation}, {Version: 2, Apply: migrateStorageFoundation}, {Version: 3, Apply: migrateMediaClassificationProfiles}, {Version: 4, Apply: migrateRuntimeLogging}, {Version: 5, Apply: migrateMediaLibraries}, {Version: 6, Apply: migratePersistentQueue}, {Version: 7, Apply: migrateDownloaderManagement}, {Version: 8, Apply: migrateUnifiedDownloadStaging}, {Version: 9, Apply: migrateDownloadClassification}, {Version: 10, Apply: migrateTMDBRoutes}, {Version: 11, Apply: migrateTMDBCredentialKind}, {Version: 12, Apply: migrateGlobalDownloadStaging}}
	for _, item := range prior {
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := item.Apply(tx); err != nil {
				return err
			}
			return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", item.Version, time.Now().UTC()).Error
		}); err != nil {
			t.Fatalf("apply v%d: %v", item.Version, err)
		}
	}
	if err := seedMediaClassificationProfiles(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO users(id, username, username_normalized, display_name, password_hash, status, is_owner, authz_version, created_at, updated_at) VALUES (1, 'owner', 'owner', 'Owner', 'hash', 'active', 1, 1, ?, ?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO jobs(id, owner_id, created_by_kind, job_type, priority, lane_position, revision, status, display_name, provider, resource_key, payload_json, checkpoint_json, last_error_code, last_error_message, created_at, updated_at) VALUES ('legacy-wait', 1, 'user', 'download', 0, 1, 4, 'waiting_user_action', 'Legacy wait', 'qbittorrent', 'downloader:legacy', '{"download_task_id":"legacy-task"}', '{"stage":"classification"}', 'tmdb_unavailable', 'old failure', ?, ?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO downloaders(id, name, name_normalized, type, base_url, capabilities_json, enabled, created_at, updated_at) VALUES ('legacy', 'Legacy', 'legacy', 'qbittorrent', 'http://127.0.0.1:8080', '{}', 1, ?, ?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO download_tasks(id, owner_id, job_id, downloader_id, downloader_name, provider_type, provider_task_id, provider_tag, source_ciphertext, staging_relative_path, staging_absolute_path, profile_id, profile_revision, profile_rules_json, display_name, phase, scrape_status, last_error_code, last_error_message, created_at, updated_at) VALUES ('legacy-task', 1, 'legacy-wait', 'legacy', 'Legacy', 'qbittorrent', 'provider-hash', 'omc-legacy-task', 'encrypted', '/', '', 1, 1, '{}', 'Legacy wait', 'waiting_user_action', 'needs_attention', 'tmdb_unavailable', 'old failure', ?, ?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.JobActionRequest{JobID: "legacy-wait", Version: 1, ActionType: "download_classification", Prompt: "Choose", OptionsJSON: `["continue_uncategorized"]`, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var job models.Job
	if err := db.First(&job, "id = ?", "legacy-wait").Error; err != nil || job.Status != models.JobStatusQueued || job.CheckpointJSON != "{}" || job.LastErrorCode != "" || job.Revision != 5 {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	var task models.DownloadTask
	if err := db.First(&task, "id = ?", "legacy-task").Error; err != nil || task.Phase != models.DownloadTaskStatusClassifying || task.ScrapeStatus != "" || task.LastErrorCode != "" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	var action models.JobActionRequest
	if err := db.First(&action, "job_id = ?", "legacy-wait").Error; err != nil || action.Response != "superseded_automatic" || action.RespondedAt == nil {
		t.Fatalf("action=%+v err=%v", action, err)
	}
}

func TestTMDBCredentialKindMigrationTreatsExistingCiphertextAsReadAccessToken(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "metadata-v10.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	prior := []migration{{Version: 1, Apply: migrateAuthFoundation}, {Version: 2, Apply: migrateStorageFoundation}, {Version: 3, Apply: migrateMediaClassificationProfiles}, {Version: 4, Apply: migrateRuntimeLogging}, {Version: 5, Apply: migrateMediaLibraries}, {Version: 6, Apply: migratePersistentQueue}, {Version: 7, Apply: migrateDownloaderManagement}, {Version: 8, Apply: migrateUnifiedDownloadStaging}, {Version: 9, Apply: migrateDownloadClassification}, {Version: 10, Apply: migrateTMDBRoutes}}
	for _, item := range prior {
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := item.Apply(tx); err != nil {
				return err
			}
			return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", item.Version, time.Now().UTC()).Error
		}); err != nil {
			t.Fatalf("apply v%d: %v", item.Version, err)
		}
	}
	if err := db.Exec(`UPDATE metadata_settings SET tmdb_token_ciphertext = 'legacy-encrypted-token', revision = 9 WHERE id = 1`).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var record models.MetadataSettings
	if err := db.First(&record, 1).Error; err != nil {
		t.Fatal(err)
	}
	if record.TMDBTokenCiphertext != "legacy-encrypted-token" || record.TMDBCredentialKind != "read_access_token" || record.Revision != 9 {
		t.Fatalf("legacy credential changed: %+v", record)
	}
}

func TestUnifiedDownloadStagingMigrationAdoptsAndDetachesLegacyDownloaderStorage(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "download-staging-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, migrate := range []func(*gorm.DB) error{migrateAuthFoundation, migrateStorageFoundation, migrateMediaClassificationProfiles, migrateRuntimeLogging, migrateMediaLibraries, migratePersistentQueue, migrateDownloaderManagement} {
		if err := migrate(db); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Legacy staging", NameNormalized: "legacy staging", Type: models.StorageTypeLocal, RootPath: `D:\Downloads`, RootPathNormalized: `d:\downloads`, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	// This test intentionally exercises the historical v7 schema, before
	// root_display_path was introduced by migration v17.
	if err := db.Omit("RootDisplayPath").Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	downloader := models.Downloader{ID: "legacy-qbit", Name: "Legacy", NameNormalized: "legacy", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://127.0.0.1:8080", StorageID: &storage.ID, CapabilitiesJSON: `{}`, LastHealthStatus: "unknown", CreatedAt: now, UpdatedAt: now}
	if err := db.Omit("ProviderDirectoryID", "ProviderDirectoryPath").Create(&downloader).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateUnifiedDownloadStaging(db); err != nil {
		t.Fatal(err)
	}
	var settings models.DownloadSettings
	if err := db.First(&settings, 1).Error; err != nil || settings.StorageID == nil || *settings.StorageID != storage.ID {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	if err := db.First(&downloader, "id = ?", downloader.ID).Error; err != nil || downloader.StorageID != nil {
		t.Fatalf("downloader=%+v err=%v", downloader, err)
	}
}

func TestTMDBRouteMigrationUpgradesV9WithoutChangingCredential(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "metadata-v9.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	prior := []migration{{Version: 1, Apply: migrateAuthFoundation}, {Version: 2, Apply: migrateStorageFoundation}, {Version: 3, Apply: migrateMediaClassificationProfiles}, {Version: 4, Apply: migrateRuntimeLogging}, {Version: 5, Apply: migrateMediaLibraries}, {Version: 6, Apply: migratePersistentQueue}, {Version: 7, Apply: migrateDownloaderManagement}, {Version: 8, Apply: migrateUnifiedDownloadStaging}, {Version: 9, Apply: migrateDownloadClassification}}
	for _, item := range prior {
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := item.Apply(tx); err != nil {
				return err
			}
			return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", item.Version, time.Now().UTC()).Error
		}); err != nil {
			t.Fatalf("apply v%d: %v", item.Version, err)
		}
	}
	if err := db.Model(&models.MetadataSettings{}).Where("id = ?", 1).Updates(map[string]any{"tmdb_token_ciphertext": "encrypted-envelope", "revision": 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var record models.MetadataSettings
	if err := db.First(&record, 1).Error; err != nil {
		t.Fatal(err)
	}
	if record.TMDBTokenCiphertext != "encrypted-envelope" || record.Revision != 7 || record.APIBaseURL != "https://api.tmdb.org/3" || record.ImageBaseURL != "https://image.tmdb.org/t/p" {
		t.Fatalf("record=%+v", record)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 10).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v10 migration count=%d err=%v", count, err)
	}
}

func TestDownloadClassificationMigrationUpgradesVersion8TaskAndIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "download-classification-v8.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	prior := []migration{{Version: 1, Apply: migrateAuthFoundation}, {Version: 2, Apply: migrateStorageFoundation}, {Version: 3, Apply: migrateMediaClassificationProfiles}, {Version: 4, Apply: migrateRuntimeLogging}, {Version: 5, Apply: migrateMediaLibraries}, {Version: 6, Apply: migratePersistentQueue}, {Version: 7, Apply: migrateDownloaderManagement}, {Version: 8, Apply: migrateUnifiedDownloadStaging}}
	for _, item := range prior {
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := item.Apply(tx); err != nil {
				return err
			}
			return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", item.Version, time.Now().UTC()).Error
		}); err != nil {
			t.Fatalf("apply v%d: %v", item.Version, err)
		}
	}
	if err := seedMediaClassificationProfiles(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO users(id, username, username_normalized, display_name, password_hash, status, is_owner, authz_version, created_at, updated_at) VALUES (1, 'owner', 'owner', 'Owner', 'hash', 'active', 1, 1, ?, ?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO jobs(id, owner_id, created_by_kind, job_type, priority, lane_position, revision, status, display_name, provider, resource_key, payload_json, checkpoint_json, created_at, updated_at) VALUES ('job-v8', 1, 'user', 'download', 0, 1, 1, 'failed', 'Legacy task', 'qbittorrent', 'downloader:legacy', '{"download_task_id":"task-v8"}', '{}', ?, ?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO downloaders(id, name, name_normalized, type, base_url, capabilities_json, enabled, created_at, updated_at) VALUES ('legacy', 'Legacy', 'legacy', 'qbittorrent', 'http://127.0.0.1:8080', '{}', 1, ?, ?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO download_tasks(id, owner_id, job_id, downloader_id, downloader_name, provider_type, provider_task_id, provider_tag, source_ciphertext, staging_relative_path, display_name, phase, created_at, updated_at) VALUES ('task-v8', 1, 'job-v8', 'legacy', 'Legacy', 'qbittorrent', '', 'omc-task-v8', 'encrypted', '/', 'Legacy task', 'failed', ?, ?)`, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("idempotent migrate: %v", err)
	}
	var task models.DownloadTask
	if err := db.First(&task, "id = ?", "task-v8").Error; err != nil {
		t.Fatal(err)
	}
	if task.ProfileID == 0 || task.ProfileRevision != 1 || task.ProfileRulesJSON == "" {
		t.Fatalf("legacy task profile snapshot missing: %+v", task)
	}
	var settings models.MetadataSettings
	if err := db.First(&settings, 1).Error; err != nil || settings.Revision != 1 || settings.TMDBTokenCiphertext != "" {
		t.Fatalf("metadata settings=%+v err=%v", settings, err)
	}
	var migrationCount int64
	if err := db.Table("schema_migrations").Where("version = 9").Count(&migrationCount).Error; err != nil || migrationCount != 1 {
		t.Fatalf("v9 count=%d err=%v", migrationCount, err)
	}
}

func TestOpenConfiguresSQLitePragmasWithoutCGO(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var foreignKeys int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys=%d, want 1", foreignKeys)
	}
	var journalMode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode=%q, want wal", journalMode)
	}
}

func TestOpenPreservesSQLiteURIParametersAndRequiredPragmas(t *testing.T) {
	databasePath := filepath.ToSlash(filepath.Join(t.TempDir(), "uri.db"))
	db, err := Open("file:" + databasePath + "?cache=shared&_pragma=cache_size(321)")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	var foreignKeys int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		t.Fatal(err)
	}
	var journalMode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatal(err)
	}
	var cacheSize int
	if err := db.Raw("PRAGMA cache_size").Scan(&cacheSize).Error; err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 || journalMode != "wal" || cacheSize != 321 {
		t.Fatalf("foreign_keys=%d journal_mode=%q cache_size=%d, want 1, wal and 321", foreignKeys, journalMode, cacheSize)
	}
}

func TestMediaClassificationProfilePermissionSeeds(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "permissions.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	profileCodes := []string{authz.PermissionMediaClassificationProfilesRead, authz.PermissionMediaClassificationProfilesCreate, authz.PermissionMediaClassificationProfilesUpdate, authz.PermissionMediaClassificationProfilesDelete}
	for _, roleCode := range []string{authz.RoleOperator, authz.RoleViewer} {
		var role models.Role
		if err := db.Where("code = ?", roleCode).First(&role).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Model(&models.RolePermission{}).Where("role_id = ? AND permission_code IN ?", role.ID, profileCodes).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		want := int64(4)
		if roleCode == authz.RoleViewer {
			want = 0
		}
		if count != want {
			t.Fatalf("role %s has %d profile permissions, want %d", roleCode, count, want)
		}
	}
}

func TestMediaLibraryMigrationTablesForeignKeysAndPermissionSeeds(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "libraries.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"media_libraries", "media_library_scan_runs", "media_library_entries"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("missing table %s", table)
		}
	}
	var storage models.Storage
	if err := db.Create(&models.Storage{Name: "Library storage", NameNormalized: "library storage", Type: models.StorageTypeLocal, RootPath: `C:\Media`, RootPathNormalized: `c:\media`, Enabled: true, Capabilities: `{}`}).Scan(&storage).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	library := models.MediaLibrary{Name: "Library", NameNormalized: "library", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", Enabled: false, Recursive: true, FullScanIntervalHours: 24, IncrementalMinutes: 15, VideoExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, MetadataLanguage: "zh-CN", MetadataRegion: "CN", MatchStrategy: "balanced", Status: models.MediaLibraryStatusDisabled, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&storage).Error; err == nil {
		t.Fatal("storage deletion unexpectedly bypassed media library foreign key")
	}
	if err := db.Create(&models.MediaLibraryScanRun{LibraryID: library.ID, Kind: "test", Status: "success", StartedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/x.mp4", ProviderID: "x", ModifiedAt: now, MediaType: "movie", Title: "x", MatchStatus: "unmatched", CategoryName: "Other", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&library).Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&models.MediaLibraryScanRun{}, &models.MediaLibraryEntry{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("cascade count=%d err=%v", count, err)
		}
	}
	mediaCodes := []string{authz.PermissionMediaLibrariesRead, authz.PermissionMediaLibrariesCreate, authz.PermissionMediaLibrariesUpdate, authz.PermissionMediaLibrariesDelete, authz.PermissionMediaLibrariesScan}
	for _, roleCode := range []string{authz.RoleOperator, authz.RoleViewer} {
		var role models.Role
		if err := db.Where("code = ?", roleCode).First(&role).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.Model(&models.RolePermission{}).Where("role_id = ? AND permission_code IN ?", role.ID, mediaCodes).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		want := int64(5)
		if roleCode == authz.RoleViewer {
			want = 0
		}
		if count != want {
			t.Fatalf("role %s has %d media library permissions, want %d", roleCode, count, want)
		}
	}
}

func TestPersistentQueueMigrationPoliciesRBACAndForeignKeys(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"jobs", "job_attempts", "job_status_events", "job_action_requests", "queue_policies"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("missing table %s", table)
		}
	}
	var policies int64
	if err := db.Model(&models.QueuePolicy{}).Count(&policies).Error; err != nil || policies != 9 {
		t.Fatalf("policies=%d err=%v", policies, err)
	}
	var operator models.Role
	if err := db.Where("code = ?", authz.RoleOperator).First(&operator).Error; err != nil {
		t.Fatal(err)
	}
	codes := []string{authz.PermissionJobsReadAll, authz.PermissionJobsControlAll, authz.PermissionJobsRespond, authz.PermissionJobsReorder}
	var granted int64
	if err := db.Model(&models.RolePermission{}).Where("role_id = ? AND permission_code IN ?", operator.ID, codes).Count(&granted).Error; err != nil || granted != int64(len(codes)) {
		t.Fatalf("operator queue grants=%d err=%v", granted, err)
	}
	now := time.Now().UTC()
	user := models.User{Username: "queue", UsernameNormalized: "queue", DisplayName: "Queue", PasswordHash: "x", Status: models.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	job := models.Job{ID: "queue-job", OwnerID: &user.ID, CreatedByKind: "user", JobType: "fake", Priority: 1, LanePosition: 1000, Revision: 1, Status: models.JobStatusQueued, DisplayName: "Job", Generation: 1, PayloadJSON: "{}", CheckpointJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	attempt := models.JobAttempt{JobID: job.ID, AttemptNumber: 1, LeaseTokenHash: "hash", Status: models.JobStatusRunning, StartedAt: now}
	if err := db.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&job).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&models.JobAttempt{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("attempt cascade=%d err=%v", count, err)
	}
}

func TestSeedingManagementMigrationDefaults(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "seeding.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"seeding_settings", "seeding_tasks"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("missing table %s", table)
		}
	}
	for _, field := range []string{"seeding_cleanup_enabled", "seeding_minimum_minutes", "seeding_minimum_ratio", "seeding_completion_mode"} {
		if !db.Migrator().HasColumn(&models.DownloadTask{}, field) {
			t.Fatalf("missing download snapshot field %s", field)
		}
	}
	var settings models.SeedingSettings
	if err := db.First(&settings, 1).Error; err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || settings.MinimumSeedMinutes != 1440 || settings.MinimumRatio != 1 || settings.CompletionMode != models.SeedingCompletionAll || settings.Revision != 1 {
		t.Fatalf("settings=%+v", settings)
	}
	var policy models.QueuePolicy
	if err := db.First(&policy, "job_type = ?", "seeding").Error; err != nil {
		t.Fatal(err)
	}
}

func TestTransferOrganizationMigrationAndOperatorPermission(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "transfer-organization.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&models.TransferTask{}, "plan_summary_json") {
		t.Fatal("transfer plan summary column missing")
	}
	var operator models.Role
	if err := db.Where("code = ?", authz.RoleOperator).First(&operator).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&models.RolePermission{}).Where("role_id = ? AND permission_code = ?", operator.ID, authz.PermissionTransfersReadAll).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("operator transfer permission count=%d err=%v", count, err)
	}
}

func TestMigrateV2ToV3PreservesStorageAndCustomProfilesAcrossReseed(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "v2-upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := migrateAuthFoundation(tx); err != nil {
			return err
		}
		if err := migrateStorageFoundation(tx); err != nil {
			return err
		}
		return tx.Exec("INSERT INTO schema_migrations(version, applied_at) VALUES (1, ?), (2, ?)", time.Now().UTC(), time.Now().UTC()).Error
	}); err != nil {
		t.Fatal(err)
	}
	storage := models.Storage{Name: "Existing", NameNormalized: "existing", Type: "local", RootPath: `D:\Media`, RootPathNormalized: `d:\media`, Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	// This row is inserted into the historical v2 schema. New model fields must
	// not make legacy migration fixtures impossible to construct.
	if err := db.Omit("RootDisplayPath").Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}

	rules, _ := json.Marshal(classification.EmptyRules())
	custom := models.MediaClassificationProfile{Name: "Custom", NameNormalized: "custom", Kind: models.MediaClassificationProfileKindCustom, SchemaVersion: 1, RulesJSON: string(rules), Revision: 7, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&custom).Error; err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}

	var storageCount, builtinCount, customCount int64
	if err := db.Model(&models.Storage{}).Where("id = ?", storage.ID).Count(&storageCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MediaClassificationProfile{}).Where("code = ?", "default-v1").Count(&builtinCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MediaClassificationProfile{}).Where("id = ? AND revision = ? AND rules_json = ?", custom.ID, 7, string(rules)).Count(&customCount).Error; err != nil {
		t.Fatal(err)
	}
	if storageCount != 1 || builtinCount != 1 || customCount != 1 {
		t.Fatalf("preservation counts storage=%d builtin=%d custom=%d", storageCount, builtinCount, customCount)
	}
}
