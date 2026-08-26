package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
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

func TestNormalizeSourceAssetExtraExtensions(t *testing.T) {
	valid, err := normalizeSourceAssetExtraExtensions([]string{"png", "xml", "png"})
	if err != nil || !reflect.DeepEqual(valid, []string{"png", "xml"}) {
		t.Fatalf("valid=%v err=%v", valid, err)
	}
	invalid := [][]string{{".png"}, {"PNG"}, {"sub title"}, {"../xml"}, {"srt"}, {"mkv"}, {"toolongvalue"}}
	for _, values := range invalid {
		if _, err := normalizeSourceAssetExtraExtensions(values); err == nil {
			t.Fatalf("expected rejection for %q", values)
		}
	}
	tooMany := make([]string, maxSourceAssetExtraExtensions+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("x%d", index)
	}
	if _, err := normalizeSourceAssetExtraExtensions(tooMany); err == nil {
		t.Fatal("expected extension count limit")
	}
}

func TestMediaRecognitionProjectionChangedTracksUserVisibleMetadata(t *testing.T) {
	year, tmdbID, confidence, rule := 2024, int64(42), 0.91, "rule-1"
	record := models.MediaLibraryRecognition{
		Status: "matched", MediaType: "movie", Title: "Example", ReleaseYear: &year,
		TMDBID: &tmdbID, Confidence: &confidence, CategoryName: "Movies", MatchedRuleID: &rule,
		MetadataJSON: `{"title":"Example"}`,
	}
	result := MediaRecognitionResult{
		Status: "matched", MediaType: "movie", Title: "Example", ReleaseYear: &year,
		TMDBID: &tmdbID, Confidence: &confidence, CategoryName: "Movies", MatchedRuleID: &rule,
	}
	if mediaRecognitionProjectionChanged(record, result, record.MetadataJSON, false) {
		t.Fatal("identical recognition projection must remain unchanged")
	}
	if !mediaRecognitionProjectionChanged(record, result, `{"title":"Example","overview":"updated"}`, false) {
		t.Fatal("metadata-only changes must be observable")
	}
}

func TestMediaLibraryEntryProjectionChangedIgnoresPhysicalFacts(t *testing.T) {
	season, episode, tmdbID, year, confidence, rule := 1, 2, int64(42), 2024, 0.91, "rule-1"
	before := models.MediaLibraryEntry{
		MediaType: "series", Title: "Episode", SeriesTitle: "Example", Season: &season, Episode: &episode,
		WorkKey: "tmdb:tv:42", MatchStatus: "matched", TMDBID: &tmdbID, ReleaseYear: &year,
		MatchConfidence: &confidence, CategoryName: "TV", MatchedRuleID: &rule,
	}
	after := before
	after.Size++
	after.ModifiedAt = time.Now().UTC()
	if mediaLibraryEntryProjectionChanged(before, after) {
		t.Fatal("physical file facts are counted separately from catalog projection")
	}
	after.Title = "Updated episode title"
	if !mediaLibraryEntryProjectionChanged(before, after) {
		t.Fatal("entry metadata changes must be observable")
	}
}

func TestMediaLibraryScanUsesSharedTMDBRecognitionAndPersistentCache(t *testing.T) {
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	if err := os.WriteFile(filepath.Join(storage.RootPath, "Seven.Samurai.1954.1080p.BluRay.mkv"), []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/search/movie":
			_, _ = response.Write([]byte(`{"results":[{"id":346,"title":"七武士","original_title":"Seven Samurai","original_language":"ja","genre_ids":[18],"release_date":"1954-04-26"}]}`))
		case "/movie/346":
			_, _ = response.Write([]byte(`{"id":346,"title":"七武士","original_language":"ja","release_date":"1954-04-26","genres":[{"id":18}],"production_countries":[{"iso_3166_1":"JP"}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer upstream.Close()
	client, err := tmdb.NewForTest("test-token", upstream.URL, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	metadata := NewMetadataSettingsService(db, NewAuditService(db), nil, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "deployment-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) { return client, nil }
	service.SetMetadataSettingsService(metadata)

	created, err := service.Create(context.Background(), actor, testLibraryInput("Recognized library", storage, profile, false), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ScanNow(context.Background(), actor, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Matched != 1 || first.Unrecognized != 0 || first.CacheHits != 0 {
		t.Fatalf("first run=%+v", first)
	}
	var entry models.MediaLibraryEntry
	if err := db.Where("library_id = ?", created.ID).First(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if entry.MatchStatus != "matched" || entry.TMDBID == nil || *entry.TMDBID != 346 || entry.Title != "七武士" || !strings.HasPrefix(entry.WorkKey, "movie:tmdb:") {
		t.Fatalf("entry=%+v", entry)
	}
	var recognition models.MediaLibraryRecognition
	if err := db.First(&recognition, entry.RecognitionID).Error; err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := decodeRecognitionMetadata(recognition.MetadataJSON)
	if err != nil || snapshot.TMDBID != 346 || snapshot.Title != "七武士" || snapshot.OriginalTitle != "Seven Samurai" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	requestCount := requests
	second, err := service.ScanNow(context.Background(), actor, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.CacheHits != 1 || requests != requestCount {
		t.Fatalf("second=%+v requests=%d want=%d", second, requests, requestCount)
	}
	page, err := service.Recognitions(actor, created.ID, MediaPageQuery{Page: 1, PageSize: 20}, "matched")
	if err != nil || page.Total != 1 || page.List[0].TMDBID == nil || *page.List[0].TMDBID != 346 || strings.Contains(page.List[0].SourceSummary, storage.RootPath) {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	year := 1954
	candidates, err := service.RecognitionCandidates(context.Background(), actor, created.ID, page.List[0].Token, "Seven Samurai", "movie", &year)
	if err != nil || len(candidates) != 1 || candidates[0].ID != 346 {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	overridden, err := service.OverrideRecognition(context.Background(), actor, created.ID, page.List[0].Token, MediaRecognitionOverrideInput{TMDBID: 346, MediaType: "movie"}, RequestContext{})
	if err != nil || !overridden.ManualOverride || overridden.Status != "matched" {
		t.Fatalf("overridden=%+v err=%v", overridden, err)
	}
	cleared, err := service.ClearRecognitionOverride(context.Background(), actor, created.ID, page.List[0].Token, RequestContext{})
	if err != nil || cleared.ManualOverride || cleared.Status != "matched" {
		t.Fatalf("cleared=%+v err=%v", cleared, err)
	}
}

func TestMediaLibraryRecognitionHonorsConfiguredConcurrencyAndRateGate(t *testing.T) {
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	for _, name := range []string{"Alpha.2020.mkv", "Beta.2020.mkv", "Gamma.2020.mkv"} {
		if err := os.WriteFile(filepath.Join(storage.RootPath, name), []byte("media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var active, maximum atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/search/movie" {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				previous := maximum.Load()
				if current <= previous || maximum.CompareAndSwap(previous, current) {
					break
				}
			}
			time.Sleep(80 * time.Millisecond)
			title := request.URL.Query().Get("query")
			_, _ = response.Write([]byte(`{"results":[{"id":100,"title":` + strconv.Quote(title) + `,"original_title":` + strconv.Quote(title) + `,"original_language":"en","genre_ids":[18],"release_date":"2020-01-01"}]}`))
			return
		}
		if request.URL.Path == "/movie/100" {
			_, _ = response.Write([]byte(`{"id":100,"title":"Concurrent title","original_title":"Concurrent title","original_language":"en","release_date":"2020-01-01","genres":[{"id":18}],"production_countries":[{"iso_3166_1":"US"}]}`))
			return
		}
		http.NotFound(response, request)
	}))
	defer upstream.Close()
	client, err := tmdb.NewForTest("test-token", upstream.URL, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	metadata := NewMetadataSettingsService(db, NewAuditService(db), nil, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "deployment-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) { return client, nil }
	service.SetMetadataSettingsService(metadata)

	input := testLibraryInput("Concurrent recognition", storage, profile, false)
	input.MetadataConcurrency = 3
	input.MetadataRatePerSecond = 100
	created, err := service.Create(context.Background(), actor, input, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.ScanNow(context.Background(), actor, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Matched != 3 || maximum.Load() < 2 {
		t.Fatalf("run=%+v maximum concurrent TMDB searches=%d, want at least 2", run, maximum.Load())
	}
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

func TestMediaLibrarySourceChangeClearsOldCatalogAndBuildsNewBaseline(t *testing.T) {
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	oldRoot := filepath.Join(storage.RootPath, "old")
	newRoot := filepath.Join(storage.RootPath, "new")
	if err := os.MkdirAll(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldRoot, "Old.Movie.mp4"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldRoot, "Old.Movie.srt"), []byte("subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "New.Movie.mp4"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	input := testLibraryInput("Changing source", storage, profile, true)
	input.RelativeRoot = "/old"
	created, err := service.Create(context.Background(), actor, input, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	waitForLibrary(t, db, created.ID, func(item models.MediaLibrary) bool {
		var count, assetCount int64
		return item.Status == models.MediaLibraryStatusListening &&
			db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND relative_path = ?", created.ID, "/Old.Movie.mp4").Count(&count).Error == nil && count == 1 &&
			db.Model(&models.MediaLibrarySourceAsset{}).Where("library_id = ? AND relative_path = ?", created.ID, "/Old.Movie.srt").Count(&assetCount).Error == nil && assetCount == 1
	})
	cleanupAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := db.Model(&models.MediaLibrary{}).Where("id = ?", created.ID).Updates(map[string]any{"artifact_cleanup_removed": 3, "artifact_cleanup_error": "cleanup_test", "artifact_cleanup_at": cleanupAt}).Error; err != nil {
		t.Fatal(err)
	}

	input.RelativeRoot = "/new"
	input.Enabled = false
	updated, err := service.Update(context.Background(), actor, created.ID, input, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.BaselineGeneration != 0 || updated.DirtyGeneration != 0 || updated.LastScanAt != nil || updated.LastSuccessfulScanAt != nil {
		t.Fatalf("source change retained old scan state: %+v", updated.MediaLibrary)
	}
	if updated.ArtifactCleanupRemoved != 3 || updated.ArtifactCleanupError != "cleanup_test" || updated.ArtifactCleanupAt == nil {
		t.Fatalf("source change lost cleanup observability: %+v", updated.MediaLibrary)
	}
	var entryCount, runCount, recognitionCount, assetCount int64
	if err := db.Model(&models.MediaLibraryEntry{}).Where("library_id = ?", created.ID).Count(&entryCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MediaLibraryScanRun{}).Where("library_id = ?", created.ID).Count(&runCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MediaLibraryRecognition{}).Where("library_id = ?", created.ID).Count(&recognitionCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MediaLibrarySourceAsset{}).Where("library_id = ?", created.ID).Count(&assetCount).Error; err != nil {
		t.Fatal(err)
	}
	if entryCount != 0 || runCount != 0 || recognitionCount != 0 || assetCount != 0 {
		t.Fatalf("source change retained source-bound records: entries=%d runs=%d recognitions=%d assets=%d", entryCount, runCount, recognitionCount, assetCount)
	}

	input.Enabled = true
	if _, err := service.Update(context.Background(), actor, created.ID, input, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	waitForLibrary(t, db, created.ID, func(item models.MediaLibrary) bool {
		var newCount, oldCount int64
		newErr := db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND relative_path = ?", created.ID, "/New.Movie.mp4").Count(&newCount).Error
		oldErr := db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND relative_path = ?", created.ID, "/Old.Movie.mp4").Count(&oldCount).Error
		return item.Status == models.MediaLibraryStatusListening && newErr == nil && oldErr == nil && newCount == 1 && oldCount == 0
	})

	var kinds []string
	if err := db.Model(&models.MediaLibraryScanRun{}).Where("library_id = ? AND status = ?", created.ID, "success").Order("id").Pluck("kind", &kinds).Error; err != nil {
		t.Fatal(err)
	}
	if len(kinds) < 2 || kinds[0] != "initial" || kinds[1] != "catch_up" {
		t.Fatalf("new source scan kinds=%v, want initial then catch_up", kinds)
	}
}

func TestMediaLibrarySourceIdentityIncludesProviderRoot(t *testing.T) {
	existing := models.MediaLibrary{StorageID: 7, RelativeRoot: "/Movies", ProviderRootID: "provider-old"}
	tests := []struct {
		name        string
		replacement models.MediaLibrary
		changed     bool
	}{
		{name: "same source", replacement: existing, changed: false},
		{name: "storage changed", replacement: models.MediaLibrary{StorageID: 8, RelativeRoot: "/Movies", ProviderRootID: "provider-old"}, changed: true},
		{name: "relative root changed", replacement: models.MediaLibrary{StorageID: 7, RelativeRoot: "/TV", ProviderRootID: "provider-old"}, changed: true},
		{name: "provider root changed", replacement: models.MediaLibrary{StorageID: 7, RelativeRoot: "/Movies", ProviderRootID: "provider-new"}, changed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := mediaLibrarySourceChanged(existing, test.replacement); actual != test.changed {
				t.Fatalf("mediaLibrarySourceChanged()=%v, want %v", actual, test.changed)
			}
		})
	}
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

func TestPan115MediaLibraryArtifactPolicyRequiresCapabilitiesAndProjectionRoot(t *testing.T) {
	driver := &fakeCloudDriver{
		signedProxy: true,
		items: map[string]cloud.Item{
			"cloud-root": {ID: "cloud-root", ParentID: "0", Name: "媒体", IsDir: true},
		},
	}
	db, _, connections, actor := newConnectionTestService(t, driver)
	actor.Permissions[authz.PermissionMediaLibrariesRead] = struct{}{}
	actor.Permissions[authz.PermissionMediaLibrariesCreate] = struct{}{}
	connection, err := connections.Create(actor, ConnectionInput{Name: "115 artifact account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	storageService := NewStorageService(db, NewAuditService(db))
	storageService.SetConnectionService(connections)
	storage, err := storageService.CreateContext(context.Background(), actor, StorageInput{Name: "115 artifact root", Type: models.StorageTypePan115, RootPath: "cloud-root", RootDisplayPath: "/媒体", ConnectionID: &connection.ID, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	service := NewMediaLibraryService(db, NewAuditService(db), zerolog.Nop())
	service.SetConnectionService(connections)
	t.Cleanup(service.Close)
	projection := t.TempDir()
	metadata := true
	input := MediaLibraryInput{Name: "115 STRM", StorageID: storage.ID, ProfileID: profile.ID, RelativeRoot: "/", ProviderRootID: storage.RootPath, Enabled: false, Recursive: true, STRMEnabled: true, STRMLocalRoot: projection, MetadataArtifactsEnabled: &metadata, TransferMode: models.MediaLibraryTransferCopy}
	created, err := service.Create(context.Background(), actor, input, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !created.STRMEnabled || !created.SignedProxyEnabled || !created.MetadataArtifactsEnabled || created.STRMLocalPath != filepath.Clean(projection) || created.UploadSidecars {
		t.Fatalf("artifact policy=%+v", created.MediaLibrary)
	}

	input.Name = "Missing projection"
	input.RelativeRoot = "/other"
	input.ProviderRootID = "cloud-root"
	input.STRMLocalRoot = ""
	if _, err := service.Create(context.Background(), actor, input, RequestContext{}); ErrorCode(err) != CodeMediaLibraryPathInvalid {
		t.Fatalf("missing projection code=%q err=%v", ErrorCode(err), err)
	}

	input.Name = "Upload conflict"
	input.STRMLocalRoot = projection
	input.UploadSidecars = true
	if _, err := service.Create(context.Background(), actor, input, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("STRM upload conflict code=%q err=%v", ErrorCode(err), err)
	}
}

func TestMediaLibraryImportPolicyAndOrder(t *testing.T) {
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	firstInput := testLibraryInput("First", storage, profile, false)
	firstInput.TransferMode = models.MediaLibraryTransferCopy
	firstInput.ConflictPolicy = models.MediaLibraryConflictRename
	first, err := service.Create(context.Background(), actor, firstInput, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	secondStorage := models.Storage{Name: "Second storage", NameNormalized: "second storage", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`}
	if err := db.Create(&secondStorage).Error; err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(context.Background(), actor, testLibraryInput("Second", secondStorage, profile, false), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if first.TransferMode != models.MediaLibraryTransferCopy || first.ConflictPolicy != models.MediaLibraryConflictRename || first.SortOrder >= second.SortOrder {
		t.Fatalf("unexpected import/order details: first=%+v second=%+v", first.MediaLibrary, second.MediaLibrary)
	}
	ordered, err := service.Reorder(actor, []uint{second.ID, first.ID}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 2 || ordered[0].ID != second.ID || ordered[0].SortOrder != 1 || ordered[1].ID != first.ID {
		t.Fatalf("ordered=%+v", ordered)
	}
	invalid := testLibraryInput("Second", secondStorage, profile, false)
	invalid.MovieDirectoryTemplate = "../{title}"
	if _, err := service.Update(context.Background(), actor, second.ID, invalid, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("invalid template code=%q err=%v", ErrorCode(err), err)
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

func TestPan115MediaLibraryScanKeepsFileIdentityAcrossRename(t *testing.T) {
	modified := time.Now().UTC().Truncate(time.Second)
	driver := &fakeCloudDriver{
		items:    map[string]cloud.Item{"cloud-root": {ID: "cloud-root", ParentID: "0", Name: "媒体", IsDir: true}},
		children: map[string][]cloud.Item{"cloud-root": {{ID: "video-id", ParentID: "cloud-root", Name: "Before.2026.mkv", Size: 128, ModifiedAt: modified}}},
	}
	db, _, connections, actor := newConnectionTestService(t, driver)
	for _, permission := range []string{authz.PermissionMediaLibrariesRead, authz.PermissionMediaLibrariesCreate, authz.PermissionMediaLibrariesScan} {
		actor.Permissions[permission] = struct{}{}
	}
	connection, err := connections.Create(actor, ConnectionInput{Name: "115 scan account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	storageService := NewStorageService(db, NewAuditService(db))
	storageService.SetConnectionService(connections)
	storage, err := storageService.CreateContext(context.Background(), actor, StorageInput{Name: "115 scan root", Type: models.StorageTypePan115, RootPath: "cloud-root", RootDisplayPath: "/媒体", ConnectionID: &connection.ID, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Order("id").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	service := NewMediaLibraryService(db, NewAuditService(db), zerolog.Nop())
	service.SetConnectionService(connections)
	t.Cleanup(service.Close)
	library, err := service.Create(context.Background(), actor, MediaLibraryInput{Name: "115 media", StorageID: storage.ID, ProfileID: profile.ID, RelativeRoot: "/", ProviderRootID: storage.RootPath, Enabled: false, Recursive: true, VideoExtensions: []string{".mkv"}, TransferMode: models.MediaLibraryTransferCopy}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ScanNow(context.Background(), actor, library.ID)
	if err != nil || first.Added != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	driver.children["cloud-root"] = []cloud.Item{{ID: "video-id", ParentID: "cloud-root", Name: "After.2026.mkv", Size: 128, ModifiedAt: modified}}
	second, err := service.ScanNow(context.Background(), actor, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Added != 0 || second.Updated != 1 || second.Removed != 0 {
		t.Fatalf("rename run=%+v", second)
	}
	var entries []models.MediaLibraryEntry
	if err := db.Where("library_id = ?", library.ID).Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ProviderID != "video-id" || entries[0].RelativePath != "/After.2026.mkv" {
		t.Fatalf("entries=%+v", entries)
	}
}
