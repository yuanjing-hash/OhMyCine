package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

func TestMediaCoverageTVUsesLogicalEpisodesAndFailsClosedForMissingFacts(t *testing.T) {
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv/100":
			_, _ = w.Write([]byte(`{"id":100,"name":"测试剧","original_name":"Fixture Show","first_air_date":"2025-01-01","seasons":[{"id":1,"season_number":0,"name":"特别篇","episode_count":1,"poster_path":"/s0.jpg"},{"id":2,"season_number":1,"name":"第 1 季","episode_count":5,"poster_path":"/s1.jpg"}]}`))
		case "/tv/100/season/0":
			_, _ = w.Write([]byte(`{"season_number":0,"episodes":[{"id":1,"season_number":0,"episode_number":1,"name":"特别篇 1","air_date":"2026-01-01"}]}`))
		case "/tv/100/season/1":
			_, _ = w.Write([]byte(`{"season_number":1,"episodes":[{"id":11,"season_number":1,"episode_number":1,"name":"已入库","air_date":"2026-01-01"},{"id":12,"season_number":1,"episode_number":2,"name":"缺失","air_date":"2026-01-08"},{"id":13,"season_number":1,"episode_number":3,"name":"未播","air_date":"2027-01-01"},{"id":14,"season_number":1,"episode_number":4,"name":"日期未知","air_date":""}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	service, actor, completeID, partialID := coverageFixture(t, upstream, now)
	season0, season1, episode1 := 0, 1, 1
	tmdbID := int64(100)
	entries := []models.MediaLibraryEntry{
		{LibraryID: completeID, RelativePath: "/show/s01e01-a.mkv", ProviderID: "private-a", MediaType: "tv", Title: "测试剧", WorkKey: "series:tmdb:100", Season: &season1, Episode: &episode1, MatchStatus: mediaRecognitionStatusMatched, TMDBID: &tmdbID, LastGeneration: 1},
		{LibraryID: partialID, RelativePath: "/show/s01e01-b.mkv", ProviderID: "private-b", MediaType: "tv", Title: "测试剧", WorkKey: "series:tmdb:100", Season: &season1, Episode: &episode1, MatchStatus: mediaRecognitionStatusMatched, TMDBID: &tmdbID, LastGeneration: 1},
		{LibraryID: partialID, RelativePath: "/show/s00e01.mkv", ProviderID: "private-c", MediaType: "tv", Title: "测试剧", WorkKey: "series:tmdb:100", Season: &season0, Episode: &episode1, MatchStatus: mediaRecognitionStatusMatched, TMDBID: &tmdbID, LastGeneration: 1},
	}
	if err := service.db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}
	coverage, err := service.Coverage(context.Background(), actor, "tv", 100)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.TV == nil || len(coverage.TV.Seasons) != 2 || coverage.TV.Counts != (MediaCoverageCounts{Total: 5, Present: 1, Missing: 1, Future: 1, Unknown: 2}) || coverage.Status != "partial" || coverage.Freshness.TMDBState != "partial" {
		t.Fatalf("coverage=%+v", coverage)
	}
	if !coverage.TV.Seasons[0].Special || coverage.TV.Seasons[0].Counts.Present != 1 || len(coverage.TV.Seasons[1].Episodes[0].LibraryIDs) != 2 || coverage.TV.Seasons[1].Episodes[1].Status != "missing" || coverage.TV.Seasons[1].Episodes[2].Status != "future" || coverage.TV.Seasons[1].Episodes[3].Status != "unknown" {
		t.Fatalf("seasons=%+v", coverage.TV.Seasons)
	}
	if coverage.TV.Seasons[0].PosterURL == "" || coverage.Libraries[0].Name == "" || coverage.Freshness.LibraryScanState != "complete" {
		t.Fatalf("safe projection=%+v", coverage)
	}
	encoded, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"library_ids":null`) || strings.Contains(string(encoded), `"episodes":null`) || strings.Contains(string(encoded), `"seasons":null`) || strings.Contains(string(encoded), `"libraries":null`) {
		t.Fatalf("coverage collections must serialize as arrays: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"episode_number":2,"name":"缺失","air_date":"2026-01-08","status":"missing","library_ids":[]`) {
		t.Fatalf("unmatched episode library_ids must serialize as []: %s", encoded)
	}
	for _, forbidden := range []string{"/show/", "private-a", "relative_path", "provider_id", "root_path"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("coverage leaked private catalog fact %q: %s", forbidden, encoded)
		}
	}
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", completeID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.MediaLibraryScanRun{}).Where("library_id = ?", partialID).Update("partial", true).Error; err != nil {
		t.Fatal(err)
	}
	partialOnly, err := service.Coverage(context.Background(), actor, "tv", 100)
	if err != nil {
		t.Fatal(err)
	}
	if partialOnly.TV.Seasons[1].Episodes[1].Status != "unknown" || partialOnly.Freshness.LibraryScanState != "partial" {
		t.Fatalf("partial coverage=%+v", partialOnly)
	}
	if _, err := service.Coverage(context.Background(), Actor{}, "tv", 100); ErrorCode(err) != CodePermissionDenied {
		t.Fatalf("permission err=%v", err)
	}
}

func TestMediaCoverageMoviePresentMissingAndUnknown(t *testing.T) {
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/200" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"id":200,"title":"测试电影","original_title":"Fixture Movie","release_date":"2025-01-01"}`))
	}))
	defer upstream.Close()
	service, actor, completeID, partialID := coverageFixture(t, upstream, now)
	coverage, err := service.Coverage(context.Background(), actor, "movie", 200)
	if err != nil || coverage.Status != "missing" || coverage.Movie == nil || coverage.Movie.Present {
		t.Fatalf("missing=%+v err=%v", coverage, err)
	}
	encoded, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"libraries":null`) || !strings.Contains(string(encoded), `"movie":{"present":false,"library_ids":[]}`) {
		t.Fatalf("missing movie collections must serialize as []: %s", encoded)
	}
	tmdbID := int64(200)
	entry := models.MediaLibraryEntry{LibraryID: partialID, RelativePath: "/movie.mkv", ProviderID: "private", MediaType: "movie", Title: "测试电影", WorkKey: "movie:tmdb:200", MatchStatus: mediaRecognitionStatusMatched, TMDBID: &tmdbID, LastGeneration: 1}
	if err := service.db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	coverage, err = service.Coverage(context.Background(), actor, "movie", 200)
	if err != nil || coverage.Status != "present" || !coverage.Movie.Present || len(coverage.Movie.LibraryIDs) != 1 {
		t.Fatalf("present=%+v err=%v", coverage, err)
	}
	if err := service.db.Where("tmdb_id = ?", tmdbID).Delete(&models.MediaLibraryEntry{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", completeID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.MediaLibraryScanRun{}).Where("library_id = ?", partialID).Update("partial", true).Error; err != nil {
		t.Fatal(err)
	}
	coverage, err = service.Coverage(context.Background(), actor, "movie", 200)
	if err != nil || coverage.Status != "unknown" {
		t.Fatalf("unknown=%+v err=%v", coverage, err)
	}
}

func coverageFixture(t *testing.T, upstream *httptest.Server, now time.Time) (*MediaCoverageService, Actor, uint, uint) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "coverage.db"))
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, sqlErr := db.DB(); sqlErr == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	store, err := credential.Open(filepath.Join(t.TempDir(), "coverage.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	audit := NewAuditService(db)
	metadata := NewMetadataSettingsService(db, audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("token", upstream.URL, upstream.Client())
	}
	storage := models.Storage{Name: "Coverage", NameNormalized: "coverage", Type: models.StorageTypeLocal, RootPath: t.TempDir(), RootPathNormalized: "coverage", Enabled: true, Capabilities: `{}`}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	profile := models.MediaClassificationProfile{Name: "Coverage", NameNormalized: "coverage", Kind: models.MediaClassificationProfileKindCustom, SchemaVersion: 1, Revision: 1, RulesJSON: `[]`, BuiltinRecognitionPacksJSON: `[]`, RecognitionRulesJSON: `[]`}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	last := now.Add(-time.Hour)
	complete := models.MediaLibrary{Name: "完整库", NameNormalized: "complete", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: 1, RelativeRoot: "/", SortOrder: 1, Enabled: true, Status: models.MediaLibraryStatusListening, BaselineGeneration: 1, LastSuccessfulScanAt: &last, VideoExtensionsJSON: `[]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`}
	partial := models.MediaLibrary{Name: "部分库", NameNormalized: "partial", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: 1, RelativeRoot: "/partial", SortOrder: 2, Enabled: true, Status: models.MediaLibraryStatusListening, BaselineGeneration: 1, LastSuccessfulScanAt: &last, VideoExtensionsJSON: `[]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`}
	if err := db.Create(&complete).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&partial).Error; err != nil {
		t.Fatal(err)
	}
	finished := last
	runs := []models.MediaLibraryScanRun{{LibraryID: complete.ID, Kind: "full", Status: "success", Generation: 1, StartedAt: last, FinishedAt: &finished}, {LibraryID: partial.ID, Kind: "incremental", Status: "success", Generation: 1, StartedAt: last, FinishedAt: &finished}}
	if err := db.Create(&runs).Error; err != nil {
		t.Fatal(err)
	}
	service := NewMediaCoverageService(db, metadata)
	service.now = func() time.Time { return now }
	actor := Actor{User: models.User{ID: 1}, Permissions: map[string]struct{}{authz.PermissionDiscoveryRead: {}, authz.PermissionMediaLibrariesRead: {}}}
	return service, actor, complete.ID, partial.ID
}
