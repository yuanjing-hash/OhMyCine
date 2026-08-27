package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

func TestFollowSnapshotValidationAndRunSnapshotAreStable(t *testing.T) {
	queue, actor, clock := queueFixture(t)
	for _, permission := range []string{authz.PermissionDiscoveryRead, authz.PermissionMediaLibrariesRead, authz.PermissionDownloadsCreate, authz.PermissionFollowsReadOwn, authz.PermissionFollowsCreate, authz.PermissionFollowsUpdateOwn, authz.PermissionFollowsDeleteOwn, authz.PermissionFollowsExecuteOwn} {
		actor.Permissions[permission] = struct{}{}
	}
	storage := models.Storage{Name: "Follow storage", NameNormalized: "follow-storage", Type: models.StorageTypeLocal, RootPath: t.TempDir(), RootPathNormalized: "follow-storage", Enabled: true, Capabilities: "{}"}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	profile := models.MediaClassificationProfile{Name: "Follow profile", NameNormalized: "follow-profile", Kind: models.MediaClassificationProfileKindCustom, SchemaVersion: 1, RulesJSON: `{"version":1,"groups":[]}`, BuiltinRecognitionPacksJSON: `[]`, RecognitionRulesJSON: `[]`, Revision: 1}
	if err := queue.db.Create(&profile).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Follow library", NameNormalized: "follow-library", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: 1, RelativeRoot: "/", Enabled: true, Status: models.MediaLibraryStatusListening, VideoExtensionsJSON: `[]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`}
	if err := queue.db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	downloader := models.Downloader{ID: "follow-downloader", Name: "Follow downloader", NameNormalized: "follow-downloader", Type: models.DownloaderTypeFake, Enabled: true, CapabilitiesJSON: `{}`}
	if err := queue.db.Create(&downloader).Error; err != nil {
		t.Fatal(err)
	}
	site := models.Site{Name: "Follow site", NameNormalized: "follow-site", Kind: "pttime", BaseURL: "https://follow.example.test", CredentialCiphertext: "encrypted", Enabled: true, Priority: 1, TimeoutSeconds: 12, RateLimitPerMinute: 12, Revision: 1}
	if err := queue.db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	service := NewFollowService(queue.db, queue.audit, queue, nil, NewAuthorizationService(queue.db))
	service.now = clock.Now
	snapshot := FollowExecutionSnapshot{Version: 99, Seasons: []int{2, 1, 2}, SiteIDs: []uint{site.ID, site.ID}, DownloaderID: downloader.ID, MediaLibraryID: library.ID, Schedule: FollowSchedule{Kind: "interval", Minutes: 60}, Filters: FollowFilters{IncludeKeywords: []string{" WEB ", "WEB"}, ExcludeKeywords: []string{}, Resolutions: []string{}, VideoCodecs: []string{}, Qualities: []string{}, ReleaseGroups: []string{}, ExcludeReleaseGroups: []string{}, MinSeeders: 1}, MaxResourcesPerRun: 3, DownloadPriority: 20}
	normalized, raw, err := service.validateSnapshot(actor, 100, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Version != 1 || len(normalized.Seasons) != 2 || normalized.Seasons[0] != 1 || len(normalized.SiteIDs) != 1 || len(normalized.Filters.IncludeKeywords) != 1 {
		t.Fatalf("normalized=%+v", normalized)
	}
	pan115Downloader := models.Downloader{ID: "follow-pan115", Name: "115", NameNormalized: "follow-pan115", Type: models.DownloaderTypePan115Offline, Enabled: true, CapabilitiesJSON: `{}`}
	if err := queue.db.Create(&pan115Downloader).Error; err != nil {
		t.Fatal(err)
	}
	pan115Snapshot := snapshot
	pan115Snapshot.DownloaderID = pan115Downloader.ID
	if _, _, err := service.validateSnapshot(actor, 100, pan115Snapshot); ErrorCode(err) != CodeFollowConfigurationInvalid {
		t.Fatalf("115 downloader accepted for PT follow: %v", err)
	}
	connection := models.Connection{Name: "Follow 115", NameNormalized: "follow-115", Provider: models.ConnectionProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1}
	if err := queue.db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	sourceStorage := models.Storage{Name: "Follow 115 downloads", NameNormalized: "follow-115-downloads", Type: models.StorageTypePan115, RootPath: "downloads", RootPathNormalized: "pan115:follow-downloads", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{}`}
	targetStorage := models.Storage{Name: "Follow 115 TV", NameNormalized: "follow-115-tv", Type: models.StorageTypePan115, RootPath: "tv", RootPathNormalized: "pan115:follow-tv", ConnectionID: &connection.ID, Enabled: true, Capabilities: `{}`}
	if err := queue.db.Create(&sourceStorage).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Create(&targetStorage).Error; err != nil {
		t.Fatal(err)
	}
	pan115Downloader.StorageID = &sourceStorage.ID
	if err := queue.db.Save(&pan115Downloader).Error; err != nil {
		t.Fatal(err)
	}
	pan115Library := models.MediaLibrary{Name: "Follow 115 library", NameNormalized: "follow-115-library", StorageID: targetStorage.ID, ProfileID: profile.ID, ProfileRevision: 1, RelativeRoot: "/", ProviderRootID: "tv", TransferMode: models.MediaLibraryTransferMove, Enabled: true, Status: models.MediaLibraryStatusListening, VideoExtensionsJSON: `[]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`}
	if err := queue.db.Create(&pan115Library).Error; err != nil {
		t.Fatal(err)
	}
	btSite := models.Site{Name: "Follow Mikan", NameNormalized: "follow-mikan", Kind: "mikan", BaseURL: "https://mikanani.me", Enabled: true, Priority: 2, TimeoutSeconds: 12, RateLimitPerMinute: 12, Revision: 1}
	if err := queue.db.Create(&btSite).Error; err != nil {
		t.Fatal(err)
	}
	bt115Snapshot := snapshot
	bt115Snapshot.SiteIDs = []uint{btSite.ID}
	bt115Snapshot.DownloaderID = pan115Downloader.ID
	bt115Snapshot.MediaLibraryID = pan115Library.ID
	if _, _, err := service.validateSnapshot(actor, 100, bt115Snapshot); err != nil {
		t.Fatalf("115 downloader rejected authoritative BT follow: %v", err)
	}
	bt115Snapshot.SiteIDs = []uint{btSite.ID, site.ID}
	if _, _, err := service.validateSnapshot(actor, 100, bt115Snapshot); ErrorCode(err) != CodeFollowConfigurationInvalid {
		t.Fatalf("115 downloader accepted mixed BT/PT follow: %v", err)
	}
	now := clock.Now()
	record := models.FollowSubscription{ID: "follow-stable", OwnerID: actor.User.ID, MediaType: "tv", TMDBID: 100, Title: "Stable", Status: models.FollowStatusActive, Revision: 1, ExecutionSnapshotJSON: string(raw), NextRunAt: &now, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	jobID, err := service.Enqueue(nilContext{}, actor, record.ID, "manual", RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var searchAudits int64
	if err := queue.db.Model(&models.AuditLog{}).Where("action = ? AND target_id = ?", "follow.search", record.ID).Count(&searchAudits).Error; err != nil || searchAudits != 1 {
		t.Fatalf("follow.search audits=%d err=%v", searchAudits, err)
	}
	unsafe := snapshot
	unsafe.Filters.IncludeKeywords = []string{"magnet:?xt=urn:btih:secret"}
	if _, _, err := service.validateSnapshot(actor, 100, unsafe); ErrorCode(err) != CodeFollowConfigurationInvalid {
		t.Fatalf("unsafe snapshot error=%v", err)
	}
	changed := normalized
	changed.Schedule.Minutes = 1440
	changedRaw, _ := json.Marshal(changed)
	if err := queue.db.Model(&record).Updates(map[string]any{"execution_snapshot_json": string(changedRaw), "revision": 2}).Error; err != nil {
		t.Fatal(err)
	}
	var run models.FollowRun
	if err := queue.db.First(&run, "job_id = ?", jobID).Error; err != nil {
		t.Fatal(err)
	}
	var captured FollowExecutionSnapshot
	if err := json.Unmarshal([]byte(run.ExecutionSnapshotJSON), &captured); err != nil {
		t.Fatal(err)
	}
	if captured.Schedule.Minutes != 60 || run.SubscriptionRevision != 1 {
		t.Fatalf("run snapshot drifted: %+v", captured)
	}
}

// nilContext implements context.Context without importing helpers into the
// production API test; enqueue itself performs no external call.
type nilContext struct{}

func (nilContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (nilContext) Done() <-chan struct{}       { return nil }
func (nilContext) Err() error                  { return nil }
func (nilContext) Value(any) any               { return nil }

func TestFollowCandidateFilteringAndDeterministicSetCover(t *testing.T) {
	seeders := 8
	published := time.Now().UTC().Add(-time.Hour)
	snapshot := FollowExecutionSnapshot{Filters: FollowFilters{Resolutions: []string{"1080p"}, VideoCodecs: []string{"HEVC"}, IncludeKeywords: []string{"WEB"}, ExcludeKeywords: []string{"CAM"}, ExcludeReleaseGroups: []string{}, MinSeeders: 1}, MaxResourcesPerRun: 2}
	item := SiteSearchResult{Title: "Fixture.Show.S01E01-E03.1080p.WEB-DL.HEVC-GROUP", SizeBytes: 1024, Published: &published, Seeders: &seeders, Specifications: SiteRecognitionSpecifications{Resolution: "1080p", VideoCodec: "H.265/HEVC", Source: "WEB-DL", ReleaseGroup: "GROUP"}}
	candidate, reason, ok := buildFollowCandidate(item, 2, 1, 1, snapshot)
	if !ok {
		t.Fatalf("candidate rejected reason=%s", reason)
	}
	if len(candidate.Episodes) != 3 {
		t.Fatalf("episodes=%v", candidate.Episodes)
	}
	missing := map[[2]int]struct{}{{1, 1}: {}, {1, 2}: {}, {1, 3}: {}, {1, 4}: {}}
	single := candidate
	single.SitePriority = 0
	single.Fingerprint = "single"
	single.Episodes = []int{4}
	selected := selectFollowCandidates([]followCandidate{single, candidate}, missing, 1)
	if len(selected) != 1 || selected[0].Fingerprint != candidate.Fingerprint {
		t.Fatalf("set cover selected=%+v", selected)
	}
	blocked := item
	blocked.Title += " CAM"
	if _, reason, ok := buildFollowCandidate(blocked, 2, 1, 1, snapshot); ok || reason != "exclude_keyword" {
		t.Fatalf("exclude result ok=%v reason=%s", ok, reason)
	}
	missingMetadata := item
	missingMetadata.Specifications.Resolution = ""
	if _, reason, ok := buildFollowCandidate(missingMetadata, 2, 1, 1, snapshot); ok || reason != "resolution" {
		t.Fatalf("missing resolution passed an explicit filter: ok=%v reason=%s", ok, reason)
	}
	exclusionOnly := snapshot
	exclusionOnly.Filters.Resolutions = nil
	exclusionOnly.Filters.VideoCodecs = nil
	exclusionOnly.Filters.IncludeKeywords = nil
	exclusionOnly.Filters.ExcludeReleaseGroups = []string{"BLOCKED"}
	missingMetadata.Specifications.ReleaseGroup = ""
	if _, reason, ok := buildFollowCandidate(missingMetadata, 2, 1, 1, exclusionOnly); !ok {
		t.Fatalf("missing release group falsely matched exclusion: reason=%s", reason)
	}
}

func TestFollowCompleteSeasonCandidateCoversBoundedEpisodes(t *testing.T) {
	seeders := 8
	snapshot := FollowExecutionSnapshot{Filters: FollowFilters{ExcludeReleaseGroups: []string{}, MinSeeders: 1}}
	item := SiteSearchResult{
		Title:   "Fixture.Show.S01.Complete.Season.1080p.WEB-DL",
		Seeders: &seeders,
	}
	candidate, reason, ok := buildFollowCandidate(item, 1, 0, 1, snapshot)
	if !ok {
		t.Fatalf("complete season candidate rejected reason=%s", reason)
	}
	if len(candidate.Episodes) != 200 || candidate.Episodes[0] != 1 || candidate.Episodes[len(candidate.Episodes)-1] != 200 {
		t.Fatalf("unexpected complete season episode bounds: first=%d last=%d count=%d", candidate.Episodes[0], candidate.Episodes[len(candidate.Episodes)-1], len(candidate.Episodes))
	}
	exact := candidate
	exact.Fingerprint = "exact"
	exact.SitePriority = 10
	exact.Episodes = []int{1, 2, 3}
	missing := map[[2]int]struct{}{{1, 1}: {}, {1, 2}: {}, {1, 3}: {}}
	selected := selectFollowCandidates([]followCandidate{candidate, exact}, missing, 1)
	if len(selected) != 1 || selected[0].Fingerprint != "exact" {
		t.Fatalf("exact package should beat an excess complete-season package: %+v", selected)
	}
}

func TestFollowResourceFingerprintIsPrivateAndStable(t *testing.T) {
	first := privateResultFingerprint(1, "torrent-secret")
	if first == privateResultFingerprint(2, "torrent-secret") || first == privateResultFingerprint(1, "other-secret") {
		t.Fatal("private resource fingerprint did not bind both site and torrent identity")
	}
	encoded, err := json.Marshal(SiteSearchResult{ResourceFingerprint: first, Title: "Fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), first) || strings.Contains(string(encoded), "resource_fingerprint") {
		t.Fatalf("private resource fingerprint leaked through public JSON: %s", encoded)
	}
}

func TestFollowStaleFinishCannotOverwritePausedSubscription(t *testing.T) {
	queue, actor, clock := queueFixture(t)
	snapshot := FollowExecutionSnapshot{Version: 1, Seasons: []int{1}, Schedule: FollowSchedule{Kind: "interval", Minutes: 60}}
	raw, _ := json.Marshal(snapshot)
	now := clock.Now()
	subscription := models.FollowSubscription{ID: "follow-paused-finish", OwnerID: actor.User.ID, MediaType: "tv", TMDBID: 100, Title: "Paused", Status: models.FollowStatusPaused, Revision: 1, LifecycleRevision: 2, ExecutionSnapshotJSON: string(raw), CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&subscription).Error; err != nil {
		t.Fatal(err)
	}
	job, err := queue.Enqueue(EnqueueJobInput{OwnerID: actor.User.ID, JobType: JobTypeFollowSearch, DisplayName: "Paused finish", ResourceKey: "follow:" + subscription.ID, Payload: map[string]string{"subscription_id": subscription.ID}})
	if err != nil {
		t.Fatal(err)
	}
	run := models.FollowRun{ID: "follow-stale-run", SubscriptionID: subscription.ID, OwnerID: actor.User.ID, SubscriptionRevision: 1, LifecycleRevision: 1, ExecutionSnapshotJSON: string(raw), JobID: job.ID, Trigger: "manual", Status: models.FollowRunRunning, MissingSnapshotJSON: "[]", FilterSummaryJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	follows := NewFollowService(queue.db, queue.audit, queue, nil, NewAuthorizationService(queue.db))
	follows.now = clock.Now
	result := NewFollowSearchWorker(follows, nil).finish(run, subscription, models.FollowRunCompleted, models.FollowStatusCompleted, "", "", snapshot, 0, 0, map[string]int{})
	if result.ErrorCode != "" {
		t.Fatalf("finish result=%+v", result)
	}
	var current models.FollowSubscription
	if err := queue.db.First(&current, "id = ?", subscription.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != models.FollowStatusPaused || current.LifecycleRevision != 2 {
		t.Fatalf("stale finish overwrote paused subscription: %+v", current)
	}
}

func TestFollowOwnAndAllPermissionMatrixDoesNotRequireReadForMutations(t *testing.T) {
	queue, owner, clock := queueFixture(t)
	otherUser := models.User{Username: "follow-other", UsernameNormalized: "follow-other", DisplayName: "Other", PasswordHash: "x", Status: models.UserStatusActive, CreatedAt: clock.Now(), UpdatedAt: clock.Now()}
	if err := queue.db.Create(&otherUser).Error; err != nil {
		t.Fatal(err)
	}
	snapshotRaw, _ := json.Marshal(FollowExecutionSnapshot{Version: 1, Seasons: []int{1}, Schedule: FollowSchedule{Kind: "interval", Minutes: 60}})
	now := clock.Now()
	record := models.FollowSubscription{ID: "follow-permission", OwnerID: owner.User.ID, MediaType: "tv", TMDBID: 100, Title: "Permission", Status: models.FollowStatusActive, Revision: 1, LifecycleRevision: 1, ExecutionSnapshotJSON: string(snapshotRaw), NextRunAt: &now, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	service := NewFollowService(queue.db, queue.audit, queue, nil, NewAuthorizationService(queue.db))
	service.now = clock.Now
	owner.Permissions = map[string]struct{}{authz.PermissionFollowsReadOwn: {}}
	if _, err := service.Get(owner, record.ID); err != nil {
		t.Fatal(err)
	}
	other := Actor{User: otherUser, Permissions: map[string]struct{}{authz.PermissionFollowsReadOwn: {}}}
	if _, err := service.Get(other, record.ID); ErrorCode(err) != CodePermissionDenied {
		t.Fatalf("foreign own read error=%v", err)
	}
	other.Permissions = map[string]struct{}{authz.PermissionFollowsReadAll: {}}
	if _, err := service.Get(other, record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetPaused(other, record.ID, true, RequestContext{}); ErrorCode(err) != CodePermissionDenied {
		t.Fatalf("read_all mutated follow: %v", err)
	}
	other.Permissions = map[string]struct{}{authz.PermissionFollowsUpdateAll: {}}
	paused, err := service.SetPaused(other, record.ID, true, RequestContext{})
	if err != nil || paused.Status != models.FollowStatusPaused {
		t.Fatalf("update_all pause=%+v err=%v", paused, err)
	}
	var pausedRecord models.FollowSubscription
	if err := queue.db.First(&pausedRecord, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetPaused(other, record.ID, true, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	var duplicatePause models.FollowSubscription
	if err := queue.db.First(&duplicatePause, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if duplicatePause.LifecycleRevision != pausedRecord.LifecycleRevision {
		t.Fatalf("idempotent pause advanced lifecycle: %d -> %d", pausedRecord.LifecycleRevision, duplicatePause.LifecycleRevision)
	}
	if _, err := service.SetPaused(other, record.ID, false, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	other.Permissions = map[string]struct{}{authz.PermissionFollowsExecuteAll: {}}
	if _, err := service.Enqueue(context.Background(), other, record.ID, "manual", RequestContext{}); err != nil {
		t.Fatal(err)
	}
	other.Permissions = map[string]struct{}{authz.PermissionFollowsDeleteAll: {}}
	if err := service.Delete(other, record.ID, RequestContext{}); err != nil {
		t.Fatal(err)
	}
}

func TestFollowWorkerUsesCoverageIdentitySearchAndDownloadPipeline(t *testing.T) {
	current := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/tv/100":
			_, _ = writer.Write([]byte(`{"id":100,"name":"示例剧","original_name":"Fixture Show","original_language":"en","first_air_date":"2026-01-01","seasons":[{"id":1,"season_number":1,"name":"第 1 季","episode_count":3,"poster_path":"/s1.jpg"}],"alternative_titles":{"results":[]},"translations":{"translations":[]}}`))
		case "/tv/100/season/1":
			_, _ = writer.Write([]byte(`{"season_number":1,"episodes":[{"id":11,"season_number":1,"episode_number":1,"name":"已入库","air_date":"2026-01-01"},{"id":12,"season_number":1,"episode_number":2,"name":"待下载","air_date":"2026-01-08"},{"id":13,"season_number":1,"episode_number":3,"name":"后续播出","air_date":"2026-08-20"}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()

	sites, adapter, fixtureActor, store, downloads, downloaders := siteFixture(t)
	queue := downloads.queue
	metadata := NewMetadataSettingsService(queue.db, queue.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", upstream.URL, upstream.Client())
	}
	sites.SetMetadataSettings(metadata)
	adapter.searchTitle = "Fixture.Show.S01E02.1080p.WEB-DL.HEVC-GROUP"

	var administrator models.Role
	if err := queue.db.First(&administrator, "code = ?", authz.RoleAdministrator).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Create(&models.UserRole{UserID: fixtureActor.User.ID, RoleID: administrator.ID, CreatedAt: current}).Error; err != nil {
		t.Fatal(err)
	}
	actor, err := NewAuthorizationService(queue.db).Resolve(fixtureActor.User.ID)
	if err != nil {
		t.Fatal(err)
	}

	var profile models.MediaClassificationProfile
	if err := queue.db.First(&profile, "code = ?", "default-v1").Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	storage := models.Storage{Name: "Follow target", NameNormalized: "follow-target", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: current, UpdatedAt: current}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	lastScan := current.Add(-time.Hour)
	library := models.MediaLibrary{Name: "Follow library", NameNormalized: "follow-library", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", SortOrder: 1, Enabled: true, Status: models.MediaLibraryStatusListening, BaselineGeneration: 1, LastSuccessfulScanAt: &lastScan, TransferMode: models.MediaLibraryTransferMove, ConflictPolicy: models.MediaLibraryConflictSkip, VideoExtensionsJSON: `[]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`}
	if err := queue.db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	finished := lastScan
	if err := queue.db.Create(&models.MediaLibraryScanRun{LibraryID: library.ID, Kind: "full", Status: "success", Generation: 1, StartedAt: lastScan, FinishedAt: &finished}).Error; err != nil {
		t.Fatal(err)
	}
	season, episodeOne := 1, 1
	tmdbID := int64(100)
	entryOne := models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/fixture/s01e01.mkv", ProviderID: "episode-one", MediaType: "tv", Title: "示例剧", WorkKey: "series:tmdb:100", Season: &season, Episode: &episodeOne, MatchStatus: mediaRecognitionStatusMatched, TMDBID: &tmdbID, LastGeneration: 1}
	if err := queue.db.Create(&entryOne).Error; err != nil {
		t.Fatal(err)
	}

	site, err := sites.Create(context.Background(), actor, validSiteInput("Follow PT", "https://follow-worker.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	downloader, err := downloaders.Create(actor, DownloaderInput{Name: "Follow qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://follow-qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	coverage := NewMediaCoverageService(queue.db, metadata)
	coverage.now = func() time.Time { return current }
	follows := NewFollowService(queue.db, queue.audit, queue, coverage, NewAuthorizationService(queue.db))
	follows.now = func() time.Time { return current }
	snapshot := FollowExecutionSnapshot{
		Seasons:            []int{1},
		SiteIDs:            []uint{site.ID},
		DownloaderID:       downloader.ID,
		MediaLibraryID:     library.ID,
		Schedule:           FollowSchedule{Kind: "interval", Minutes: 60},
		Filters:            FollowFilters{Resolutions: []string{"1080p"}, VideoCodecs: []string{"hevc"}, ExcludeReleaseGroups: []string{}, MinSeeders: 1},
		MaxResourcesPerRun: 2,
	}
	invalidSeason := snapshot
	invalidSeason.Seasons = []int{2}
	if _, err := follows.Create(context.Background(), actor, CreateFollowInput{TMDBID: 100, Title: "示例剧", Snapshot: invalidSeason}, RequestContext{}); ErrorCode(err) != CodeFollowConfigurationInvalid {
		t.Fatalf("nonexistent season create error=%v", err)
	}
	subscription, err := follows.Create(context.Background(), actor, CreateFollowInput{TMDBID: 100, Title: "示例剧", Snapshot: snapshot}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.FollowSubscription{}).Where("id = ?", subscription.ID).Update("status", models.FollowStatusBlocked).Error; err != nil {
		t.Fatal(err)
	}
	subscription, err = follows.Update(context.Background(), actor, subscription.ID, UpdateFollowInput{Revision: subscription.Revision, Snapshot: snapshot}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if subscription.Status != models.FollowStatusActive || subscription.NextRunAt == nil || !subscription.NextRunAt.Equal(current) {
		t.Fatalf("blocked update did not reactivate immediately: %+v", subscription)
	}
	worker := NewFollowSearchWorker(follows, sites)
	runNext := func() models.FollowRun {
		t.Helper()
		claimed, claimErr := queue.Claim([]string{JobTypeFollowSearch})
		if claimErr != nil || claimed == nil {
			t.Fatalf("claim=%+v err=%v", claimed, claimErr)
		}
		result := worker.Run(context.Background(), &providerWakeRuntime{}, *claimed)
		if result.ErrorCode != "" || result.ErrorMessage != "" {
			t.Fatalf("worker result=%+v", result)
		}
		if err := queue.Complete(claimed.Job.ID, claimed.LeaseToken); err != nil {
			t.Fatal(err)
		}
		var run models.FollowRun
		if err := queue.db.First(&run, "job_id = ?", claimed.Job.ID).Error; err != nil {
			t.Fatal(err)
		}
		return run
	}

	adapter.downloadStarted = make(chan struct{}, 1)
	adapter.downloadRelease = make(chan struct{})
	pausedClaim, err := queue.Claim([]string{JobTypeFollowSearch})
	if err != nil || pausedClaim == nil {
		t.Fatalf("pause-race claim=%+v err=%v", pausedClaim, err)
	}
	pausedResult := make(chan WorkerResult, 1)
	go func() {
		pausedResult <- worker.Run(context.Background(), &providerWakeRuntime{}, *pausedClaim)
	}()
	select {
	case <-adapter.downloadStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("follow worker did not reach site download")
	}
	if _, err := follows.SetPaused(actor, subscription.ID, true, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	// Resuming before the blocked site call returns must not revive the old run.
	if _, err := follows.SetPaused(actor, subscription.ID, false, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	close(adapter.downloadRelease)
	result := <-pausedResult
	if result.ErrorCode != "" || result.ErrorMessage != "" {
		t.Fatalf("paused worker result=%+v", result)
	}
	if err := queue.Complete(pausedClaim.Job.ID, pausedClaim.LeaseToken); err != nil {
		t.Fatal(err)
	}
	var cancelledRun models.FollowRun
	if err := queue.db.First(&cancelledRun, "job_id = ?", pausedClaim.Job.ID).Error; err != nil || cancelledRun.Status != models.FollowRunCancelled {
		t.Fatalf("paused run=%+v err=%v", cancelledRun, err)
	}
	var pausedDownloads int64
	if err := queue.db.Model(&models.DownloadTask{}).Where("follow_subscription_id = ?", subscription.ID).Count(&pausedDownloads).Error; err != nil || pausedDownloads != 0 {
		t.Fatalf("pause race submitted downloads=%d err=%v", pausedDownloads, err)
	}
	adapter.downloadStarted = nil
	adapter.downloadRelease = nil
	if _, err := follows.Enqueue(context.Background(), actor, subscription.ID, "manual", RequestContext{}); err != nil {
		t.Fatal(err)
	}

	firstRun := runNext()
	if firstRun.Status != models.FollowRunSubmitted || firstRun.Selected != 1 {
		t.Fatalf("first run=%+v", firstRun)
	}
	var task models.DownloadTask
	if err := queue.db.First(&task, "follow_subscription_id = ?", subscription.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.TargetLibraryID == nil || *task.TargetLibraryID != library.ID || task.RecognitionOverrideTMDBID == nil || *task.RecognitionOverrideTMDBID != 100 || task.RecognitionOverrideSeason == nil || *task.RecognitionOverrideSeason != 1 || task.RecognitionOverrideEpisode == nil || *task.RecognitionOverrideEpisode != 2 {
		t.Fatalf("follow download lost pipeline identity/target: %+v", task)
	}
	var claim models.FollowEpisodeClaim
	if err := queue.db.First(&claim, "subscription_id = ? AND season_number = ? AND episode_number = ?", subscription.ID, 1, 2).Error; err != nil || claim.DownloadTaskID == nil || *claim.DownloadTaskID != task.ID {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	currentSummary, err := follows.Get(actor, subscription.ID)
	if err != nil || currentSummary.ProgressTarget != 2 || currentSummary.ProgressPresent != 1 || currentSummary.ProgressMissing != 1 {
		t.Fatalf("initial progress=%+v err=%v", currentSummary, err)
	}

	episodeTwo := 2
	entryTwo := entryOne
	entryTwo.ID = 0
	entryTwo.RelativePath = "/fixture/s01e02.mkv"
	entryTwo.ProviderID = "episode-two"
	entryTwo.Episode = &episodeTwo
	if err := queue.db.Create(&entryTwo).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := follows.Enqueue(context.Background(), actor, subscription.ID, "manual", RequestContext{}); err != nil {
		t.Fatal(err)
	}
	completedRun := runNext()
	if completedRun.Status != models.FollowRunCompleted {
		t.Fatalf("completed run=%+v", completedRun)
	}
	completed, err := follows.Get(actor, subscription.ID)
	if err != nil || completed.Status != models.FollowStatusCompleted || completed.ProgressMissing != 0 {
		t.Fatalf("completed subscription=%+v err=%v", completed, err)
	}

	current = time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	if err := follows.EnqueueDue(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	newEpisodeRun := runNext()
	if newEpisodeRun.Status != models.FollowRunNoMatch {
		t.Fatalf("newly aired run=%+v", newEpisodeRun)
	}
	reactivated, err := follows.Get(actor, subscription.ID)
	if err != nil || reactivated.Status != models.FollowStatusActive || reactivated.ProgressTarget != 3 || reactivated.ProgressPresent != 2 || reactivated.ProgressMissing != 1 {
		t.Fatalf("reactivated subscription=%+v err=%v", reactivated, err)
	}
	var downloadCount int64
	if err := queue.db.Model(&models.DownloadTask{}).Where("follow_subscription_id = ?", subscription.ID).Count(&downloadCount).Error; err != nil || downloadCount != 1 {
		t.Fatalf("download count=%d err=%v", downloadCount, err)
	}
	if _, err := follows.Enqueue(context.Background(), actor, subscription.ID, "manual", RequestContext{}); err != nil {
		t.Fatal(err)
	}
	if err := follows.Delete(actor, subscription.ID, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	stale, err := queue.Claim([]string{JobTypeFollowSearch})
	if err != nil || stale == nil {
		t.Fatalf("deleted follow claim=%+v err=%v", stale, err)
	}
	staleResult := worker.Run(context.Background(), &providerWakeRuntime{}, *stale)
	if staleResult.ErrorCode != "" || staleResult.ErrorMessage != "" {
		t.Fatalf("deleted follow retried instead of becoming a no-op: %+v", staleResult)
	}
	if err := queue.Complete(stale.Job.ID, stale.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := queue.db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatalf("deleting follow removed submitted download: %v", err)
	}
}
