package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

type recordingArtifactCleanup struct {
	runIDs []string
	result ArtifactCleanupResult
}

func (c *recordingArtifactCleanup) AutoCleanup(_ context.Context, runID string) ArtifactCleanupResult {
	c.runIDs = append(c.runIDs, runID)
	return c.result
}

func TestMediaArtifactWorkerResumesCleanupForCompletedRun(t *testing.T) {
	service, queue, _, library, root := strmManagementFixture(t)
	_, identity, err := canonicalProjectionRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	policyJSON, err := json.Marshal(mediaArtifactPolicy{LibraryID: library.ID, Generation: library.ArtifactGeneration, StorageID: library.StorageID, ProjectionRoot: root, ProjectionRootIdentity: identity, TargetKind: models.MediaArtifactTargetLocalProjection, STRMEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: "completed-cleanup-recovery", LibraryID: library.ID, Generation: library.ArtifactGeneration, PolicyJSON: string(policyJSON), Status: models.MediaArtifactStatusCompleted, CleanupStatus: models.MediaArtifactCleanupRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := service.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	cleanup := &recordingArtifactCleanup{}
	artifacts := NewMediaArtifactService(service.db, queue, nil, zerolog.Nop())
	artifacts.SetCleanupService(cleanup)
	payload, _ := json.Marshal(mediaArtifactJobPayload{ArtifactRunID: run.ID})
	result := NewMediaArtifactWorker(artifacts).Run(context.Background(), &providerWakeRuntime{}, ClaimedJob{Job: models.Job{PayloadJSON: string(payload)}})
	if result.ErrorCode != "" || len(cleanup.runIDs) != 1 || cleanup.runIDs[0] != run.ID {
		t.Fatalf("result=%+v cleanup=%v", result, cleanup.runIDs)
	}
}

func TestMediaArtifactWorkerGeneratesManagedSTRMAndPreservesUnmanagedFile(t *testing.T) {
	assetServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/subtitle" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = response.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\n字幕\n"))
	}))
	defer assetServer.Close()
	driver := &fakeCloudDriver{signedProxy: true, directURL: assetServer.URL + "/subtitle", items: map[string]cloud.Item{
		"movie-file":    {ID: "movie-file", ParentID: "library-root", Name: "Movie.mkv", PickCode: "movie-pickcode", Size: 100},
		"disc-file":     {ID: "disc-file", ParentID: "library-root", Name: "Disc.iso", PickCode: "disc-pickcode", Size: 200},
		"subtitle-file": {ID: "subtitle-file", ParentID: "movie-dir", Name: "Movie.zh-CN.srt", PickCode: "subtitle-pickcode", Size: 42},
	}}
	db, store, connections, actor := newConnectionTestService(t, driver)
	connection, err := connections.Create(actor, ConnectionInput{Name: "Artifact account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Artifact cloud", NameNormalized: "artifact-cloud", Type: models.StorageTypePan115, RootPath: "storage-root", RootDisplayPath: "/媒体", RootPathNormalized: "pan115:artifact", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{"temporary_direct_url":true,"signed_proxy":true}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	projection := t.TempDir()
	library := models.MediaLibrary{Name: "Artifact library", NameNormalized: "artifact-library", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", ProviderRootID: "library-root", Enabled: true, Recursive: true, VideoExtensionsJSON: `[".mkv",".iso"]`, IgnorePatternsJSON: `[]`, STRMEnabled: true, STRMLocalRoot: projection, SignedProxyEnabled: true, MetadataArtifactsEnabled: true, Status: models.MediaLibraryStatusListening, ArtifactStatus: models.MediaArtifactStatusIdle, DirtyGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Select("*").Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	metadataJSON, err := marshalRecognitionMetadata(MediaRecognitionResult{Metadata: classification.Metadata{MediaType: classification.MediaTypeMovie, OriginalLanguage: "en"}, Snapshot: tmdb.Snapshot{Version: 1, TMDBID: 42, MediaType: "movie", Title: "Movie", OriginalTitle: "Movie", ReleaseDate: "2026-01-01"}})
	if err != nil {
		t.Fatal(err)
	}
	tmdbID, confidence := int64(42), .98
	recognition := models.MediaLibraryRecognition{LibraryID: library.ID, SourceKey: strings.Repeat("a", 64), InputFingerprint: strings.Repeat("b", 64), ProfileID: profile.ID, ProfileRevision: profile.Revision, Status: mediaRecognitionStatusMatched, MediaType: "movie", Title: "Movie", TMDBID: &tmdbID, Confidence: &confidence, MetadataJSON: metadataJSON, LastGeneration: 2, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&recognition).Error; err != nil {
		t.Fatal(err)
	}
	entries := []models.MediaLibraryEntry{
		{LibraryID: library.ID, RelativePath: "/Movies/Movie.mkv", ProviderID: "movie-file", RecognitionID: &recognition.ID, Size: 100, ModifiedAt: now, MediaType: "movie", Title: "Movie", MatchStatus: "matched", LastGeneration: 1, CreatedAt: now, UpdatedAt: now},
		{LibraryID: library.ID, RelativePath: "/Discs/Disc.iso", ProviderID: "disc-file", Size: 200, ModifiedAt: now, MediaType: "movie", Title: "Disc", MatchStatus: "unrecognized", LastGeneration: 1, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}
	sourceAsset := models.MediaLibrarySourceAsset{LibraryID: library.ID, Generation: 2, ProviderID: "subtitle-file", ParentProviderID: "movie-dir", RelativePath: "/Movies/Movie.zh-CN.srt", Name: "Movie.zh-CN.srt", Extension: ".srt", Size: 42, ModifiedAt: now, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&sourceAsset).Error; err != nil {
		t.Fatal(err)
	}
	unmanaged := filepath.Join(projection, "Movies", "Unmanaged.strm")
	if err := os.MkdirAll(filepath.Dir(unmanaged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unmanaged, []byte("keep-me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries = append(entries, models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/Movies/Unmanaged.mkv", ProviderID: "movie-file", Size: 100, ModifiedAt: now, MediaType: "movie", Title: "Unmanaged", MatchStatus: "unrecognized", LastGeneration: 1, CreatedAt: now, UpdatedAt: now})
	if err := db.Create(&entries[2]).Error; err != nil {
		t.Fatal(err)
	}
	queue := NewQueueService(db, NewAuditService(db))
	proxy, err := NewSignedProxyService(db, store, connections, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	artifacts := NewMediaArtifactService(db, queue, proxy, zerolog.Nop())
	artifacts.SetConnectionService(connections)
	cleanup := &recordingArtifactCleanup{}
	artifacts.SetCleanupService(cleanup)
	scanFinished := time.Now().UTC()
	partialScan := models.MediaLibraryScanRun{LibraryID: library.ID, Kind: "incremental", Status: "success", Generation: 1, Partial: true, StartedAt: now, FinishedAt: &scanFinished}
	if err := db.Create(&partialScan).Error; err != nil {
		t.Fatal(err)
	}
	scan := models.MediaLibraryScanRun{LibraryID: library.ID, Kind: "full", Status: "success", Generation: 2, StartedAt: now, FinishedAt: &scanFinished}
	if err := db.Create(&scan).Error; err != nil {
		t.Fatal(err)
	}
	if err := artifacts.ScheduleGeneration(library.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := artifacts.ScheduleGeneration(library.ID, 2); err != nil {
		t.Fatal(err)
	}
	var queuedJobs int64
	if err := db.Model(&models.Job{}).Where("job_type = ?", JobTypeMediaArtifact).Count(&queuedJobs).Error; err != nil || queuedJobs != 1 {
		t.Fatalf("coalesced jobs=%d err=%v", queuedJobs, err)
	}
	claimed, err := queue.Claim([]string{JobTypeMediaArtifact})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewMediaArtifactWorker(artifacts).Run(context.Background(), &providerWakeRuntime{}, *claimed)
	if result.ErrorCode != "" || result.RetryAt != nil {
		t.Fatalf("worker result=%+v", result)
	}
	for _, relative := range []string{filepath.Join("Movies", "Movie.strm"), filepath.Join("Discs", "Disc.iso.strm")} {
		content, err := os.ReadFile(filepath.Join(projection, relative))
		if err != nil {
			t.Fatal(err)
		}
		value := string(content)
		if !strings.HasPrefix(value, "https://media.example.test/proxy/strm/") || strings.Contains(value, "movie-file") || strings.Contains(value, "disc-file") || strings.Contains(value, "pickcode") {
			t.Fatalf("unsafe STRM %s=%q", relative, value)
		}
	}
	nfoContent, err := os.ReadFile(filepath.Join(projection, "Movies", "Movie.nfo"))
	if err != nil || !strings.Contains(string(nfoContent), "<title>Movie</title>") || strings.Contains(string(nfoContent), "movie-file") {
		t.Fatalf("NFO content=%q err=%v", nfoContent, err)
	}
	subtitle, err := os.ReadFile(filepath.Join(projection, "Movies", "Movie.zh-CN.srt"))
	if err != nil || !strings.Contains(string(subtitle), "字幕") {
		t.Fatalf("subtitle=%q err=%v", subtitle, err)
	}
	if content, err := os.ReadFile(unmanaged); err != nil || string(content) != "keep-me\n" {
		t.Fatalf("unmanaged content=%q err=%v", content, err)
	}
	var run models.MediaArtifactRun
	if err := db.Where("library_id = ? AND generation = ?", library.ID, 2).First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != models.MediaArtifactStatusCompleted || run.WrittenCount != 4 || run.SkippedCount != 1 || run.FailedCount != 0 {
		t.Fatalf("artifact run=%+v", run)
	}
	var policy mediaArtifactPolicy
	if err := json.Unmarshal([]byte(run.PolicyJSON), &policy); err != nil || policy.ScanRunID != scan.ID || policy.ScanKind != "full" || policy.ScanPartial || !policy.CleanupEligible || policy.ProjectionRootIdentity == "" {
		t.Fatalf("policy=%+v err=%v", policy, err)
	}
	if len(cleanup.runIDs) != 1 || cleanup.runIDs[0] != run.ID {
		t.Fatalf("cleanup calls=%v, want completed run %s", cleanup.runIDs, run.ID)
	}
	if run.JobID == nil || *run.JobID != claimed.Job.ID {
		t.Fatalf("latest run job=%v, want %s", run.JobID, claimed.Job.ID)
	}
	var superseded models.MediaArtifactRun
	if err := db.Where("library_id = ? AND generation = ?", library.ID, 1).First(&superseded).Error; err != nil {
		t.Fatal(err)
	}
	if superseded.Status != models.MediaArtifactStatusSuperseded || superseded.JobID != nil {
		t.Fatalf("superseded run=%+v", superseded)
	}
	var supersededPolicy mediaArtifactPolicy
	if err := json.Unmarshal([]byte(superseded.PolicyJSON), &supersededPolicy); err != nil || supersededPolicy.ScanRunID != partialScan.ID || !supersededPolicy.ScanPartial || supersededPolicy.CleanupEligible {
		t.Fatalf("superseded policy=%+v err=%v", supersededPolicy, err)
	}
	var manifestCount int64
	if err := db.Model(&models.MediaArtifact{}).Where("library_id = ?", library.ID).Count(&manifestCount).Error; err != nil || manifestCount != 4 {
		t.Fatalf("manifest count=%d err=%v", manifestCount, err)
	}
	var refreshed models.MediaLibrary
	if err := db.First(&refreshed, library.ID).Error; err != nil || refreshed.ArtifactAppliedGeneration != 2 || refreshed.ArtifactStatus != models.MediaArtifactStatusCompleted {
		t.Fatalf("library artifact state=%+v err=%v", refreshed, err)
	}
}

func TestMediaArtifactCompletionCASDoesNotInvalidateNewerManifest(t *testing.T) {
	service, queue, _, library, root := strmManagementFixture(t)
	artifacts := NewMediaArtifactService(service.db, queue, nil, zerolog.Nop())
	now := time.Now().UTC()
	oldPolicy := mediaArtifactPolicy{LibraryID: library.ID, Generation: 1, StorageID: library.StorageID, StorageType: models.StorageTypePan115, ProjectionRoot: root, TargetKind: models.MediaArtifactTargetLocalProjection, STRMEnabled: true}
	_, oldPolicy.ProjectionRootIdentity, _ = canonicalProjectionRoot(root)
	policyJSON, err := json.Marshal(oldPolicy)
	if err != nil {
		t.Fatal(err)
	}
	oldRun := models.MediaArtifactRun{ID: "artifact-old-generation", LibraryID: library.ID, Generation: 1, PolicyJSON: string(policyJSON), Status: models.MediaArtifactStatusRunning, CreatedAt: now, UpdatedAt: now}
	newRun := models.MediaArtifactRun{ID: "artifact-new-generation", LibraryID: library.ID, Generation: 2, PolicyJSON: string(policyJSON), Status: models.MediaArtifactStatusQueued, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&oldRun).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Create(&newRun).Error; err != nil {
		t.Fatal(err)
	}
	active := models.MediaArtifact{OpaqueID: "newer-active-artifact", RunID: newRun.ID, LibraryID: library.ID, Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/newer.strm", Managed: true, Active: true, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&active).Error; err != nil {
		t.Fatal(err)
	}
	result := artifacts.generateArtifacts(context.Background(), &providerWakeRuntime{}, oldRun, oldPolicy)
	if result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
	if err := service.db.First(&oldRun, "id = ?", oldRun.ID).Error; err != nil || oldRun.Status != models.MediaArtifactStatusSuperseded {
		t.Fatalf("old run=%+v err=%v", oldRun, err)
	}
	if err := service.db.First(&active, active.ID).Error; err != nil || !active.Active {
		t.Fatalf("newer artifact=%+v err=%v", active, err)
	}
}

func TestSTRMRelativePathPreservesISOExtension(t *testing.T) {
	tests := map[string]string{"/Movie.mkv": "/Movie.strm", "/Disc.iso": "/Disc.iso.strm", "/Series/Show.S01E01.m2ts": "/Series/Show.S01E01.strm"}
	for source, want := range tests {
		if got, err := strmRelativePath(source); err != nil || got != want {
			t.Fatalf("source=%q got=%q err=%v", source, got, err)
		}
	}
	if _, err := strmRelativePath("../../escape.mkv"); err == nil {
		t.Fatal("traversal path accepted")
	}
}

func TestMediaArtifactWorkerWritesLocalAdjacentNFO(t *testing.T) {
	_, db, _, storage, profile := mediaLibraryTestService(t)
	now := time.Now().UTC()
	library := models.MediaLibrary{Name: "Local artifacts", NameNormalized: "local-artifacts", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", Enabled: true, Recursive: true, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, MetadataArtifactsEnabled: true, Status: models.MediaLibraryStatusListening, ArtifactStatus: models.MediaArtifactStatusIdle, DirtyGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Select("*").Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	metadataJSON, err := marshalRecognitionMetadata(MediaRecognitionResult{Metadata: classification.Metadata{MediaType: classification.MediaTypeMovie}, Snapshot: tmdb.Snapshot{Version: 1, TMDBID: 346, MediaType: "movie", Title: "七武士", ReleaseDate: "1954-04-26", PosterPath: "/poster.jpg"}})
	if err != nil {
		t.Fatal(err)
	}
	tmdbID, confidence := int64(346), .98
	recognition := models.MediaLibraryRecognition{LibraryID: library.ID, SourceKey: strings.Repeat("c", 64), InputFingerprint: strings.Repeat("d", 64), ProfileID: profile.ID, ProfileRevision: profile.Revision, Status: mediaRecognitionStatusMatched, MediaType: "movie", Title: "七武士", TMDBID: &tmdbID, Confidence: &confidence, MetadataJSON: metadataJSON, LastGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&recognition).Error; err != nil {
		t.Fatal(err)
	}
	entry := models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/Seven.Samurai.1954.mkv", RecognitionID: &recognition.ID, Size: 100, ModifiedAt: now, MediaType: "movie", Title: "七武士", MatchStatus: mediaRecognitionStatusMatched, LastGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	queue := NewQueueService(db, NewAuditService(db))
	artifacts := NewMediaArtifactService(db, queue, nil, zerolog.Nop())
	imageServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/w780/poster.jpg" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "image/jpeg")
		_, _ = response.Write([]byte{0xff, 0xd8, 0xff, 0xdb, 1, 2, 3})
	}))
	defer imageServer.Close()
	imageClient, err := tmdb.NewForTest("test-token", imageServer.URL, imageServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	metadata := NewMetadataSettingsService(db, NewAuditService(db), nil, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "deployment-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) { return imageClient, nil }
	artifacts.SetMetadataSettingsService(metadata)
	if err := artifacts.ScheduleGeneration(library.ID, 1); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{JobTypeMediaArtifact})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewMediaArtifactWorker(artifacts).Run(context.Background(), &providerWakeRuntime{}, *claimed)
	if result.ErrorCode != "" {
		t.Fatalf("result=%+v", result)
	}
	content, err := os.ReadFile(filepath.Join(storage.RootPath, "Seven.Samurai.1954.nfo"))
	if err != nil || !strings.Contains(string(content), "<title>七武士</title>") {
		t.Fatalf("content=%q err=%v", content, err)
	}
	poster, err := os.ReadFile(filepath.Join(storage.RootPath, "Seven.Samurai.1954-poster.jpg"))
	if err != nil || len(poster) != 7 || poster[0] != 0xff {
		t.Fatalf("poster=%v err=%v", poster, err)
	}
	if _, err := os.Stat(filepath.Join(storage.RootPath, "Seven.Samurai.1954.strm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local library unexpectedly generated STRM: %v", err)
	}
}
