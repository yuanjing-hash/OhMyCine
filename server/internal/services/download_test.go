package services

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

type stubDownloadClient struct {
	mu         sync.Mutex
	gets       int
	submits    int
	source     downloadpkg.Source
	paused     bool
	resumed    bool
	cancelled  bool
	deleteData bool
	cancelErr  error
	getErr     error
	seedTask   *downloadpkg.Task
}

type metadataDownloadClient struct {
	*stubDownloadClient
	metadataOnly      bool
	manifestCalls     int
	category          string
	categoryPath      string
	routedPath        string
	categories        []downloadpkg.Category
	categoryCalls     []string
	keepCategoryPath  bool
	updateCategoryErr error
	setCategoryErr    error
}

type providerWakeRuntime struct {
	heartbeats chan struct{}
	err        error
}

func (r *providerWakeRuntime) Heartbeat(*float64, *int64, *int64, *float64, *int64) error {
	if r.heartbeats != nil {
		select {
		case r.heartbeats <- struct{}{}:
		default:
		}
	}
	return r.err
}

func (*providerWakeRuntime) Checkpoint(any) error { return nil }

func (c *metadataDownloadClient) Submit(ctx context.Context, request downloadpkg.SubmitRequest) (downloadpkg.Task, error) {
	c.metadataOnly = request.MetadataOnly
	return c.stubDownloadClient.Submit(ctx, request)
}

func (c *metadataDownloadClient) Manifest(context.Context, string) (downloadpkg.Manifest, error) {
	c.mu.Lock()
	c.manifestCalls++
	c.mu.Unlock()
	return downloadpkg.Manifest{Name: "Example.Show.S01E01", Complete: true, Files: []downloadpkg.File{{RelativePath: "Example.Show.S01E01/Example.Show.S01E01.mkv", Size: 2 * 1024 * 1024 * 1024}}}, nil
}
func (c *metadataDownloadClient) Categories(context.Context) ([]downloadpkg.Category, error) {
	c.categoryCalls = append(c.categoryCalls, "categories")
	return append([]downloadpkg.Category(nil), c.categories...), nil
}
func (c *metadataDownloadClient) EnsureCategory(_ context.Context, name, savePath string) error {
	c.categoryCalls = append(c.categoryCalls, "create")
	c.category, c.categoryPath = name, savePath
	c.categories = append(c.categories, downloadpkg.Category{Name: name, SavePath: savePath})
	return nil
}
func (c *metadataDownloadClient) UpdateCategory(_ context.Context, name, savePath string) error {
	c.categoryCalls = append(c.categoryCalls, "update")
	if c.updateCategoryErr != nil {
		return c.updateCategoryErr
	}
	c.category, c.categoryPath = name, savePath
	if !c.keepCategoryPath {
		for index := range c.categories {
			if strings.EqualFold(c.categories[index].Name, name) {
				c.categories[index].SavePath = savePath
			}
		}
	}
	return nil
}
func (c *metadataDownloadClient) SetCategory(_ context.Context, _ string, category, savePath string) error {
	c.categoryCalls = append(c.categoryCalls, "set")
	if c.setCategoryErr != nil {
		return c.setCategoryErr
	}
	c.category, c.routedPath = category, savePath
	return nil
}

func (c *metadataDownloadClient) Resume(ctx context.Context, id string) error {
	c.categoryCalls = append(c.categoryCalls, "resume")
	return c.stubDownloadClient.Resume(ctx, id)
}

func (c *stubDownloadClient) Test(context.Context) (downloadpkg.Health, error) {
	return downloadpkg.Health{Version: "stub-v1"}, nil
}
func (c *stubDownloadClient) Submit(_ context.Context, request downloadpkg.SubmitRequest) (downloadpkg.Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.submits++
	c.source = request.Source
	return downloadpkg.Task{ID: "provider-hash", Status: "downloading"}, nil
}
func (c *stubDownloadClient) Get(context.Context, string) (downloadpkg.Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.getErr != nil {
		return downloadpkg.Task{}, c.getErr
	}
	if c.seedTask != nil {
		return *c.seedTask, nil
	}
	c.gets++
	progress := 50.0
	if c.gets > 1 {
		progress = 100
	}
	completed, total, speed, upload, eta := int64(progress), int64(100), int64(20), int64(2), int64(3)
	return downloadpkg.Task{ID: "provider-hash", Status: map[bool]string{true: "completed", false: "downloading"}[progress == 100], Progress: &progress, BytesCompleted: &completed, BytesTotal: &total, DownloadSpeed: &speed, UploadSpeed: &upload, ETASeconds: &eta, Completed: progress == 100}, nil
}
func (c *stubDownloadClient) Pause(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paused = true
	return nil
}
func (c *stubDownloadClient) Resume(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resumed = true
	return nil
}
func (c *stubDownloadClient) Cancel(_ context.Context, _ string, deleteData bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelled, c.deleteData = true, deleteData
	return c.cancelErr
}

func downloadFixture(t *testing.T) (*DownloadService, *DownloaderService, *QueueService, Actor, *stubDownloadClient) {
	t.Helper()
	queue, actor, _ := queueFixture(t)
	for _, code := range []string{authz.PermissionDownloadersRead, authz.PermissionDownloadersCreate, authz.PermissionDownloadersUpdate, authz.PermissionDownloadersDelete, authz.PermissionDownloadersTest, authz.PermissionDownloadsReadOwn, authz.PermissionDownloadsReadAll, authz.PermissionDownloadsCreate} {
		actor.Permissions[code] = struct{}{}
	}
	store, err := credential.Open(filepath.Join(t.TempDir(), "credentials.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	client := &stubDownloadClient{}
	registry := downloadpkg.NewRegistry()
	capabilities := downloadpkg.Capabilities{Pause: true, Resume: true, Cancel: true, DownloadSpeed: true, UploadSpeed: true, ETA: true, Seeding: true}
	if err := registry.Register(models.DownloaderTypeQBittorrent, capabilities, func(downloadpkg.Config) (downloadpkg.Client, error) { return client, nil }); err != nil {
		t.Fatal(err)
	}
	downloaders := NewDownloaderService(queue.db, queue.audit, store, registry)
	settings := NewDownloadSettingsService(queue.db, queue.audit)
	downloads := NewDownloadService(queue.db, queue.audit, store, downloaders, settings, queue, zerolog.Nop())
	downloads.SetSeedingSettings(NewSeedingSettingsService(queue.db, queue.audit))
	return downloads, downloaders, queue, actor, client
}

func TestDownloadQueueResourceDelegatesQBittorrentConcurrency(t *testing.T) {
	qbit := models.Downloader{ID: "qbit", Type: models.DownloaderTypeQBittorrent}
	pan := models.Downloader{ID: "pan", Type: models.DownloaderTypePan115Offline}
	if key := downloadQueueResourceKey(qbit); key != "" {
		t.Fatalf("qBittorrent resource key=%q, want global guard only", key)
	}
	if key := downloadQueueResourceKey(pan); key != "downloader:pan" {
		t.Fatalf("115 resource key=%q", key)
	}
}

func TestPan115DownloadWaitBroadcastsLifeEventToAllWorkers(t *testing.T) {
	service := &DownloadService{providerEvents: newProviderEventWakeHub()}
	worker := &DownloadWorker{service: service, pan115PollInterval: time.Hour, heartbeatInterval: time.Hour}
	const connectionID = uint(115)
	generationA, _ := service.providerEvents.snapshot(connectionID)
	generationB := generationA
	doneA := make(chan error, 1)
	doneB := make(chan error, 1)
	go func() {
		doneA <- worker.waitForPan115Poll(context.Background(), &providerWakeRuntime{}, connectionID, &generationA, "task-a")
	}()
	go func() {
		doneB <- worker.waitForPan115Poll(context.Background(), &providerWakeRuntime{}, connectionID, &generationB, "task-b")
	}()

	if err := service.ProviderEventsChanged(context.Background(), connectionID, []models.ProviderEvent{{Kind: "created", ItemID: "output"}}); err != nil {
		t.Fatal(err)
	}
	for index, done := range []<-chan error{doneA, doneB} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("worker %d wake err=%v", index, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("worker %d was not woken by provider event", index)
		}
	}
	if generationA == 0 || generationB != generationA {
		t.Fatalf("event generations = %d, %d", generationA, generationB)
	}
}

func TestPan115DownloadWaitMaintainsQueueLeaseBetweenFallbackPolls(t *testing.T) {
	service := &DownloadService{providerEvents: newProviderEventWakeHub()}
	worker := &DownloadWorker{service: service, pan115PollInterval: 40 * time.Millisecond, heartbeatInterval: 5 * time.Millisecond}
	runtime := &providerWakeRuntime{heartbeats: make(chan struct{}, 1)}
	generation, _ := service.providerEvents.snapshot(115)
	done := make(chan error, 1)
	go func() { done <- worker.waitForPan115Poll(context.Background(), runtime, 115, &generation, "task-1") }()
	select {
	case <-runtime.heartbeats:
	case <-time.After(time.Second):
		t.Fatal("queue lease was not refreshed while waiting for fallback poll")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fallback poll did not resume the worker")
	}
}

func TestDownloadFailureMessage(t *testing.T) {
	tests := []struct {
		name      string
		code      string
		retryable bool
		want      string
	}{
		{name: "authentication", code: "downloader_auth_failed", want: "下载器认证已失效，请更新连接凭据后重试"},
		{name: "rate limited", code: "downloader_rate_limited", retryable: true, want: "下载器请求受到限速，任务将延后重试"},
		{name: "storage unavailable", code: "downloader_storage_unavailable", want: "下载目标目录不存在或已移动，请重新选择目录"},
		{name: "quota exhausted", code: "downloader_quota_exhausted", want: "115 离线下载配额已耗尽，请检查账号权益后重试"},
		{name: "response invalid", code: "downloader_response_invalid", want: "下载器返回了无法识别的响应，请重新测试连接"},
		{name: "category update unsupported", code: "downloader_category_update_unsupported", want: "当前 qBittorrent 版本不支持更新分类目录，请升级后重试"},
		{name: "category update retryable", code: "downloader_category_update_failed", retryable: true, want: "qBittorrent 分类目录更新暂时失败，任务将自动重试"},
		{name: "category boundary mismatch", code: "downloader_category_outside_staging", want: "qBittorrent 分类目录与该任务的暂存目录不一致，已阻止下载"},
		{name: "generic retryable", code: "downloader_unavailable", retryable: true, want: "下载器暂时不可用，任务将自动重试"},
		{name: "generic terminal", code: "download_failed", want: "下载任务执行失败"},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			if got := downloadFailureMessage(item.code, item.retryable); got != item.want {
				t.Fatalf("downloadFailureMessage(%q, %t) = %q, want %q", item.code, item.retryable, got, item.want)
			}
		})
	}
}

func TestTransferEnqueueFailureDoesNotMasqueradeAsDownloaderUnavailable(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	worker := NewDownloadWorker(downloads)
	task := models.DownloadTask{ID: "transfer-enqueue-failure", ProviderType: models.DownloaderTypePan115Offline}

	terminal := worker.transferEnqueueFailure(task, appError(CodeTransferMediaUnrecognized, "媒体识别结果不可信", nil))
	if terminal.ErrorCode != CodeTransferMediaUnrecognized || terminal.ErrorMessage != "媒体识别结果不可信" || terminal.RetryAt != nil {
		t.Fatalf("terminal=%+v", terminal)
	}

	retryable := worker.transferEnqueueFailure(task, errors.New("temporary queue write failure"))
	if retryable.ErrorCode != "transfer_enqueue_failed" || retryable.RetryAt == nil || retryable.ErrorCode == CodeDownloaderUnavailable {
		t.Fatalf("retryable=%+v", retryable)
	}
}

func TestDownloadSnapshotsSeedingPolicy(t *testing.T) {
	downloads, downloaders, queue, actor, _ := downloadFixture(t)
	root := t.TempDir()
	storage := models.Storage{Name: "Policy staging", NameNormalized: "policy staging", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	if err := queue.db.Model(&models.SeedingSettings{}).Where("id = ?", 1).Updates(map[string]any{"enabled": true, "minimum_seed_minutes": 120, "minimum_ratio": 1.5, "completion_mode": models.SeedingCompletionAny}).Error; err != nil {
		t.Fatal(err)
	}
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "Policy qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://127.0.0.1:8080", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:policy"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := queue.db.First(&task, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !task.SeedingCleanupEnabled || task.SeedingMinimumMinutes != 120 || task.SeedingMinimumRatio != 1.5 || task.SeedingCompletionMode != models.SeedingCompletionAny {
		t.Fatalf("seeding snapshot=%+v", task)
	}
}

func TestDownloadDisablesSeedingPolicyForNonSeedingProvider(t *testing.T) {
	downloads, downloaders, queue, actor, client := downloadFixture(t)
	if err := downloaders.registry.Register(models.DownloaderTypeFake, downloadpkg.Capabilities{Cancel: true}, func(downloadpkg.Config) (downloadpkg.Client, error) { return client, nil }); err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.SeedingSettings{}).Where("id = ?", 1).Updates(map[string]any{"enabled": true, "minimum_seed_minutes": 120, "minimum_ratio": 1.5, "completion_mode": models.SeedingCompletionAny}).Error; err != nil {
		t.Fatal(err)
	}
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "Non-seeding provider", Type: models.DownloaderTypeFake, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:no-seeding"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := queue.db.First(&task, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.SeedingCleanupEnabled || task.SeedingMinimumMinutes == 120 || task.SeedingMinimumRatio == 1.5 || task.SeedingCompletionMode != models.SeedingCompletionAll {
		t.Fatalf("non-seeding provider retained seeding policy: %+v", task)
	}
}

func configureDownloadStaging(t *testing.T, queue *QueueService, storageID uint) {
	t.Helper()
	var storage models.Storage
	if err := queue.db.First(&storage, storageID).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.DownloadSettings{}).Where("id = ?", 1).Updates(map[string]any{"absolute_path": storage.RootPath, "storage_id": nil, "relative_path": "/"}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestDownloadTargetUsesFirstAvailableLibraryAndKeepsSnapshot(t *testing.T) {
	downloads, downloaders, queue, actor, _ := downloadFixture(t)
	createStorage := func(name, root string, enabled bool) models.Storage {
		storage := models.Storage{Name: name, NameNormalized: strings.ToLower(name), Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`}
		if err := queue.db.Create(&storage).Error; err != nil {
			t.Fatal(err)
		}
		if !enabled {
			if err := queue.db.Model(&storage).Update("enabled", false).Error; err != nil {
				t.Fatal(err)
			}
			storage.Enabled = false
		}
		return storage
	}
	staging := createStorage("Target staging", t.TempDir(), true)
	configureDownloadStaging(t, queue, staging.ID)
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "Target qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	profile.RecognitionRulesJSON = `[{"enabled":true,"media_type":"all","pattern":"^Release\\.","replacement":""}]`
	profile.MovieDirectoryTemplate = "电影/{category}/{title} ({year})"
	profile.MovieFilenameTemplate = "{title} ({year})"
	profile.TVDirectoryTemplate = "剧集/{category}/{title} ({year})/Season {season:02}"
	profile.TVFilenameTemplate = "{title} - S{season:02}E{episode:02}"
	if err := queue.db.Save(&profile).Error; err != nil {
		t.Fatal(err)
	}

	unavailableStorage := createStorage("Unavailable target", t.TempDir(), false)
	availableStorage := createStorage("Available target", t.TempDir(), true)
	manualStorage := createStorage("Manual target", t.TempDir(), true)
	createLibrary := func(name string, storage models.Storage, order int, mode, conflict string) models.MediaLibrary {
		library := models.MediaLibrary{Name: name, NameNormalized: strings.ToLower(name), StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", SortOrder: order, TransferMode: mode, ConflictPolicy: conflict, Enabled: true, Recursive: true, VideoExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, Status: models.MediaLibraryStatusListening}
		if err := queue.db.Create(&library).Error; err != nil {
			t.Fatal(err)
		}
		return library
	}
	createLibrary("Unavailable first", unavailableStorage, 1, models.MediaLibraryTransferMove, models.MediaLibraryConflictAsk)
	selected := createLibrary("Available second", availableStorage, 2, models.MediaLibraryTransferCopy, models.MediaLibraryConflictRename)
	manual := createLibrary("Manual third", manualStorage, 3, models.MediaLibraryTransferSymlink, models.MediaLibraryConflictSkip)

	automaticID := uint(0)
	automatic, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, MediaLibraryID: &automaticID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:auto-target"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if automatic.TargetLibraryID == nil || *automatic.TargetLibraryID != selected.ID || automatic.ProfileID != profile.ID || automatic.TransferMode != models.MediaLibraryTransferCopy || automatic.ConflictPolicy != models.MediaLibraryConflictRename {
		t.Fatalf("automatic target=%+v", automatic)
	}

	manualID := manual.ID
	explicit, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, MediaLibraryID: &manualID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:manual-target"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.TargetLibraryID == nil || *explicit.TargetLibraryID != manual.ID || explicit.TransferMode != models.MediaLibraryTransferSymlink {
		t.Fatalf("explicit target=%+v", explicit)
	}

	if err := queue.db.Model(&models.MediaLibrary{}).Where("id = ?", selected.ID).Updates(map[string]any{"name": "Changed later", "transfer_mode": models.MediaLibraryTransferMove, "conflict_policy": models.MediaLibraryConflictOverwrite}).Error; err != nil {
		t.Fatal(err)
	}
	var persisted models.DownloadTask
	if err := queue.db.First(&persisted, "id = ?", automatic.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.TargetLibraryName != "Available second" || persisted.TransferMode != models.MediaLibraryTransferCopy || persisted.ConflictPolicy != models.MediaLibraryConflictRename || persisted.ProfileRevision != profile.Revision || persisted.ProfileBuiltinRecognitionPacksJSON != profile.BuiltinRecognitionPacksJSON || persisted.ProfileRecognitionRulesJSON != profile.RecognitionRulesJSON || persisted.MovieDirectoryTemplate != profile.MovieDirectoryTemplate || persisted.TVDirectoryTemplate != profile.TVDirectoryTemplate {
		t.Fatalf("download target snapshot changed with library: %+v", persisted)
	}
}

func TestPan115DownloadTargetRequiresSameConnectionAndWritableMode(t *testing.T) {
	downloads, downloaders, queue, _, _ := downloadFixture(t)
	now := time.Now().UTC()
	connectionA := models.Connection{Name: "115 A", NameNormalized: "115-a-target", Provider: cloudpkg.ProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	connectionB := models.Connection{Name: "115 B", NameNormalized: "115-b-target", Provider: cloudpkg.ProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&connectionA).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Create(&connectionB).Error; err != nil {
		t.Fatal(err)
	}
	driver := newFakeMutationCloudDriver()
	for _, item := range []cloudpkg.Item{{ID: "0", IsDir: true}, {ID: "storage-a", ParentID: "0", Name: "a", IsDir: true}, {ID: "library-a", ParentID: "storage-a", Name: "library", IsDir: true}} {
		driver.items[item.ID] = item
	}
	downloaders.connections = &ConnectionService{db: queue.db, drivers: map[uint]cloudpkg.Driver{connectionA.ID: driver, connectionB.ID: newFakeMutationCloudDriver()}}
	sourceStorage := models.Storage{Name: "Source A", NameNormalized: "source-a-target", Type: models.StorageTypePan115, RootPath: "storage-a", RootPathNormalized: "pan115:storage-a", ConnectionID: &connectionA.ID, Enabled: true, Capabilities: `{}`}
	targetStorage := models.Storage{Name: "Target A", NameNormalized: "target-a-target", Type: models.StorageTypePan115, RootPath: "storage-a", RootPathNormalized: "pan115:target-a", ConnectionID: &connectionA.ID, Enabled: true, Capabilities: `{}`}
	if err := queue.db.Create(&sourceStorage).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Create(&targetStorage).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "115 Movies", NameNormalized: "115-movies-target", StorageID: targetStorage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/library", ProviderRootID: "library-a", SortOrder: 1, TransferMode: models.MediaLibraryTransferCopy, ConflictPolicy: models.MediaLibraryConflictAsk, Enabled: true, Recursive: true, VideoExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`}
	if err := queue.db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	provider := models.Downloader{ID: "pan115-target", Name: "115 Offline", NameNormalized: "115-offline-target", Type: models.DownloaderTypePan115Offline, StorageID: &sourceStorage.ID, Enabled: true, CapabilitiesJSON: `{}`}
	localRoot := t.TempDir()
	localStorage := models.Storage{Name: "Local Target", NameNormalized: "local-target-" + strings.ToLower(filepath.Base(localRoot)), Type: models.StorageTypeLocal, RootPath: localRoot, RootPathNormalized: strings.ToLower(localRoot), Enabled: true, Capabilities: `{}`}
	if err := queue.db.Create(&localStorage).Error; err != nil {
		t.Fatal(err)
	}
	localLibrary := models.MediaLibrary{Name: "Local First", NameNormalized: "local-first-" + strings.ToLower(filepath.Base(localRoot)), StorageID: localStorage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", SortOrder: 0, TransferMode: models.MediaLibraryTransferMove, ConflictPolicy: models.MediaLibraryConflictAsk, Enabled: true, Recursive: true, VideoExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`}
	if err := queue.db.Create(&localLibrary).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := downloads.snapshotDownloadTarget(context.Background(), provider, localLibrary); ErrorCode(err) != CodeMediaLibraryStorageUnavailable {
		t.Fatalf("pan115 local-target error=%v", err)
	}
	automatic, _, err := downloads.resolveDownloadTarget(context.Background(), provider, 0, downloadpkg.SourceURL)
	if err != nil || automatic.LibraryID != library.ID || automatic.StorageType != models.StorageTypePan115 {
		t.Fatalf("automatic pan115 target=%+v err=%v", automatic, err)
	}
	target, _, err := downloads.snapshotDownloadTarget(context.Background(), provider, library)
	if err != nil || target.ConnectionID == nil || *target.ConnectionID != connectionA.ID || target.ProviderRootID != "library-a" || target.StorageType != models.StorageTypePan115 {
		t.Fatalf("target=%+v err=%v", target, err)
	}

	library.TransferMode = models.MediaLibraryTransferSymlink
	if _, _, err := downloads.snapshotDownloadTarget(context.Background(), provider, library); ErrorCode(err) != CodeMediaLibraryStorageUnavailable {
		t.Fatalf("symlink error=%v", err)
	}
	library.TransferMode = models.MediaLibraryTransferMove
	targetStorage.ConnectionID = &connectionB.ID
	if err := queue.db.Model(&targetStorage).Update("connection_id", connectionB.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := downloads.snapshotDownloadTarget(context.Background(), provider, library); ErrorCode(err) != CodeMediaLibraryStorageUnavailable {
		t.Fatalf("cross-connection error=%v", err)
	}
	qbit := provider
	qbit.Type = models.DownloaderTypeQBittorrent
	if _, _, err := downloads.snapshotDownloadTarget(context.Background(), qbit, library); ErrorCode(err) != CodeMediaLibraryStorageUnavailable {
		t.Fatalf("qBittorrent cloud-target error=%v", err)
	}
}

func TestDownloaderCredentialsAndDownloadSourceStayEncrypted(t *testing.T) {
	downloads, downloaders, queue, actor, _ := downloadFixture(t)
	root := t.TempDir()
	storage := models.Storage{Name: "Staging", NameNormalized: "staging", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	created, err := downloaders.Create(actor, DownloaderInput{Name: "qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Username: "admin", Password: "super-secret", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !created.UsernameConfigured || !created.PasswordConfigured {
		t.Fatal("credential flags missing")
	}
	var rawDownloader models.Downloader
	if err := queue.db.First(&rawDownloader, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if rawDownloader.UsernameCiphertext == "admin" || rawDownloader.PasswordCiphertext == "super-secret" || strings.Contains(rawDownloader.PasswordCiphertext, "super-secret") {
		t.Fatal("credentials persisted in plaintext")
	}

	item, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: created.ID, DisplayName: "安全测试", Priority: 10, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "https://tracker.example.test/file?id=1&passkey=pt-secret"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := queue.db.First(&task, "id = ?", item.ID).Error; err != nil {
		t.Fatal(err)
	}
	if references, err := downloads.settings.StorageReferences(storage.ID); err != nil || len(references) != 0 {
		t.Fatalf("staging references=%v err=%v", references, err)
	}
	if strings.Contains(task.SourceCiphertext, "pt-secret") {
		t.Fatal("download source persisted in plaintext")
	}
	if task.ProfileID == 0 || task.ProfileRevision != 1 || task.ProfileRulesJSON == "" || task.ProfileBuiltinRecognitionPacksJSON == "" || task.ProfileRecognitionRulesJSON == "" {
		t.Fatalf("classification profile was not snapshotted: %+v", task)
	}
	var job models.Job
	if err := queue.db.First(&job, "id = ?", item.JobID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(job.PayloadJSON, "tracker") || strings.Contains(job.PayloadJSON, "pt-secret") {
		t.Fatal("source leaked into job payload")
	}
	var auditRows []models.AuditLog
	if err := queue.db.Order("id").Find(&auditRows).Error; err != nil {
		t.Fatal(err)
	}
	for _, auditRow := range auditRows {
		if strings.Contains(auditRow.Metadata, "pt-secret") || strings.Contains(auditRow.Metadata, "super-secret") || strings.Contains(auditRow.Metadata, root) || strings.Contains(auditRow.Metadata, "tracker.example") {
			t.Fatalf("audit metadata leaked sensitive download state: %s", auditRow.Metadata)
		}
	}
	var payload downloadJobPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil || payload.DownloadTaskID != item.ID {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
	listed, err := downloads.List(actor, 10)
	if err != nil || len(listed) != 1 || listed[0].DisplayName != "安全测试" {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	if task.StagingAbsolutePath != root || task.StagingStorageID != nil {
		t.Fatalf("new task did not snapshot independent staging path: %+v", task)
	}
}

func TestDeleteTerminalDownloadRemovesProviderDataAndAllLocalQueueFacts(t *testing.T) {
	downloads, downloaders, queue, actor, client := downloadFixture(t)
	actor.Permissions[authz.PermissionJobsControlOwn] = struct{}{}
	root := t.TempDir()
	storage := models.Storage{Name: "Delete staging", NameNormalized: "delete staging", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "Delete qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:delete"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Updates(map[string]any{"provider_task_id": "provider-hash", "phase": models.DownloadTaskStatusFailed}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", created.JobID).Updates(map[string]any{"status": models.JobStatusFailed, "last_error_code": "old", "last_error_message": "old failure"}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := queue.db.Create(&models.JobActionRequest{JobID: created.JobID, Version: 1, ActionType: "legacy", Prompt: "legacy", OptionsJSON: `[]`, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := downloads.Delete(context.Background(), actor, created.ID, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	cancelled, deleteData := client.cancelled, client.deleteData
	client.mu.Unlock()
	if !cancelled || !deleteData {
		t.Fatalf("provider cleanup cancelled=%v deleteData=%v", cancelled, deleteData)
	}
	checks := []struct {
		model any
		where string
		value string
	}{
		{model: &models.DownloadTask{}, where: "id = ?", value: created.ID},
		{model: &models.Job{}, where: "id = ?", value: created.JobID},
		{model: &models.JobActionRequest{}, where: "job_id = ?", value: created.JobID},
		{model: &models.JobAttempt{}, where: "job_id = ?", value: created.JobID},
		{model: &models.JobStatusEvent{}, where: "job_id = ?", value: created.JobID},
	}
	for _, check := range checks {
		var count int64
		if err := queue.db.Model(check.model).Where(check.where, check.value).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("orphan model %T count=%d err=%v", check.model, count, err)
		}
	}
	var audit models.AuditLog
	if err := queue.db.Where("action = ? AND target_id = ?", "download.delete", created.ID).First(&audit).Error; err != nil || strings.Contains(audit.Metadata, root) {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
}

func TestDeleteTerminalDownloadRetainsLocalRecordWhenProviderCleanupCannotBeConfirmed(t *testing.T) {
	downloads, downloaders, queue, actor, client := downloadFixture(t)
	actor.Permissions[authz.PermissionJobsControlOwn] = struct{}{}
	root := t.TempDir()
	storage := models.Storage{Name: "Retain staging", NameNormalized: "retain staging", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "Retain qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:retain"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Updates(map[string]any{"provider_task_id": "provider-hash", "phase": models.DownloadTaskStatusFailed}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", created.JobID).Update("status", models.JobStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	client.cancelErr = downloadpkg.Error("downloader_unavailable", true, errors.New("offline"))
	if err := downloads.Delete(context.Background(), actor, created.ID, RequestContext{}); ErrorCode(err) != CodeDownloaderUnavailable {
		t.Fatalf("provider failure error=%v", err)
	}
	var count int64
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("local record count=%d err=%v", count, err)
	}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Update("downloader_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	if err := downloads.Delete(context.Background(), actor, created.ID, RequestContext{}); ErrorCode(err) != CodeDownloaderUnavailable {
		t.Fatalf("missing downloader reference should retain provider-backed record: %v", err)
	}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Update("downloader_id", provider.ID).Error; err != nil {
		t.Fatal(err)
	}
	client.cancelErr = downloadpkg.Error("downloader_task_not_found", false, errors.New("missing"))
	if err := downloads.Delete(context.Background(), actor, created.ID, RequestContext{}); err != nil {
		t.Fatalf("manual provider deletion was not idempotent: %v", err)
	}
}

func TestDownloaderRejectsAmbiguousCredentialClearAndOversizedConfig(t *testing.T) {
	_, downloaders, _, actor, _ := downloadFixture(t)
	for _, baseURL := range []string{"http://qbit.example.test/?", "http://qbit.example.test/#"} {
		if _, err := downloaders.Create(actor, DownloaderInput{Name: "Unsafe " + baseURL, Type: models.DownloaderTypeQBittorrent, BaseURL: baseURL, Enabled: true}, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
			t.Fatalf("unsafe base URL %q error=%v", baseURL, err)
		}
	}
	created, err := downloaders.Create(actor, DownloaderInput{Name: "qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Username: "admin", Password: "secret", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	password := "replacement"
	if _, err := downloaders.Update(actor, created.ID, UpdateDownloaderInput{Password: &password, ClearPassword: true}, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("clear+password error=%v", err)
	}
	if _, err := downloaders.Create(actor, DownloaderInput{Name: "Oversized", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Password: strings.Repeat("x", 4097), Enabled: true}, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("oversized password error=%v", err)
	}
}

func TestDownloadWorkerPersistsTelemetryAndCancelDeletesProviderData(t *testing.T) {
	downloads, downloaders, queue, actor, client := downloadFixture(t)
	root := t.TempDir()
	storage := models.Storage{Name: "Staging", NameNormalized: "staging", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, DisplayName: "Movie", Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:abcdef"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"download"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	worker := NewDownloadWorker(downloads)
	worker.pollInterval = time.Millisecond
	result := worker.Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.RetryAt != nil {
		t.Fatalf("result=%+v", result)
	}
	if err := queue.Complete(claimed.Job.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := queue.db.First(&task, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Phase != models.DownloadTaskStatusCompleted || task.Progress == nil || *task.Progress != 100 || task.UploadSpeed == nil || *task.UploadSpeed != 2 {
		t.Fatalf("task=%+v", task)
	}

	// Cancellation is destructive by explicit product contract.
	task.FinishedAt, task.Phase = nil, models.DownloadTaskStatusDownloading
	if err := queue.db.Save(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := worker.Interrupt(context.Background(), *claimed, "cancel"); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	cancelled, deleteData := client.cancelled, client.deleteData
	client.mu.Unlock()
	if !cancelled || !deleteData {
		t.Fatalf("cancelled=%v deleteData=%v", cancelled, deleteData)
	}
}

func TestTorrentSourceValidation(t *testing.T) {
	valid := []byte("d4:infod4:name4:testee")
	source, name, err := normalizeDownloadSource(DownloadSourceInput{Kind: downloadpkg.SourceTorrent, Filename: "movie.torrent", Torrent: valid}, "")
	if err != nil || source.Filename != "movie.torrent" || name != "movie" {
		t.Fatalf("source=%+v name=%q err=%v", source, name, err)
	}
	if _, _, err := normalizeDownloadSource(DownloadSourceInput{Kind: downloadpkg.SourceTorrent, Filename: "movie.txt", Torrent: valid}, ""); ErrorCode(err) != CodeDownloadTorrentInvalid {
		t.Fatalf("error=%v", err)
	}
	if _, _, err := normalizeDownloadSource(DownloadSourceInput{Kind: downloadpkg.SourceTorrent, Filename: "movie.torrent", Torrent: make([]byte, downloadpkg.MaxTorrentBytes+1)}, ""); ErrorCode(err) != CodeDownloadTorrentInvalid {
		t.Fatalf("error=%v", err)
	}
}

func TestDownloadWorkerReconcilesExistingProviderTaskWithoutResubmit(t *testing.T) {
	downloads, downloaders, queue, actor, client := downloadFixture(t)
	root := t.TempDir()
	storage := models.Storage{Name: "Staging", NameNormalized: "staging", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, DisplayName: "Resume", Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:resume"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Updates(map[string]any{"provider_task_id": "provider-hash", "phase": models.DownloadTaskStatusDownloading}).Error; err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	client.gets, client.submits = 1, 0 // next provider sample is completed
	client.mu.Unlock()
	claimed, err := queue.Claim([]string{"download"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	worker := NewDownloadWorker(downloads)
	worker.pollInterval = time.Millisecond
	result := worker.Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.RetryAt != nil {
		t.Fatalf("result=%+v", result)
	}
	client.mu.Lock()
	submits := client.submits
	client.mu.Unlock()
	if submits != 0 {
		t.Fatalf("restart reconciliation resubmitted provider task %d time(s)", submits)
	}
}

func TestDownloadWorkerExplicitRetryReplacesFailedPan115TaskWithoutDeletingFiles(t *testing.T) {
	downloads, downloaders, queue, actor, client := downloadFixture(t)
	root := t.TempDir()
	storage := models.Storage{Name: "Staging retry", NameNormalized: "staging retry", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "qBit retry fixture", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, DisplayName: "Retry", Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:retry"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Updates(map[string]any{
		"provider_task_id": "old-provider-hash", "provider_output_id": "partial-output", "provider_status": "failed",
		"phase": models.DownloadTaskStatusFailed, "last_error_code": "downloader_provider_failed", "last_error_message": "failed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := queue.db.First(&task, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	worker := NewDownloadWorker(downloads)
	if err := worker.resetFailedPan115ForExplicitRetry(context.Background(), &task, client, models.DownloaderTypePan115Offline); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	cancelled, deleteData := client.cancelled, client.deleteData
	client.mu.Unlock()
	if !cancelled || deleteData {
		t.Fatalf("cancelled=%v deleteData=%v", cancelled, deleteData)
	}
	var persisted models.DownloadTask
	if err := queue.db.First(&persisted, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ProviderTaskID != "" || persisted.ProviderOutputID != "" || persisted.Phase != models.DownloadTaskStatusQueued || persisted.LastErrorCode != "" {
		t.Fatalf("persisted=%+v", persisted)
	}

	client.mu.Lock()
	client.cancelled = false
	client.mu.Unlock()
	persisted.Phase = models.DownloadTaskStatusFailed
	persisted.ProviderTaskID = "completed-provider-hash"
	persisted.LastErrorCode = CodeTransferMediaUnrecognized
	if err := worker.resetFailedPan115ForExplicitRetry(context.Background(), &persisted, client, models.DownloaderTypePan115Offline); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	cancelled = client.cancelled
	client.mu.Unlock()
	if cancelled {
		t.Fatal("recognition/import failure must not delete or resubmit a completed 115 task")
	}
}

func TestDownloadWorkerAutomaticallyRoutesUnrecognizedWithoutTMDBCredential(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	for _, code := range []string{authz.PermissionDownloadersCreate, authz.PermissionDownloadsCreate, authz.PermissionDownloadsReadAll, authz.PermissionJobsRespond} {
		actor.Permissions[code] = struct{}{}
	}
	store, err := credential.Open(filepath.Join(t.TempDir(), "credentials.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	client := &metadataDownloadClient{stubDownloadClient: &stubDownloadClient{}}
	registry := downloadpkg.NewRegistry()
	if err := registry.Register(models.DownloaderTypeQBittorrent, downloadpkg.Capabilities{}, func(downloadpkg.Config) (downloadpkg.Client, error) { return client, nil }); err != nil {
		t.Fatal(err)
	}
	downloaders := NewDownloaderService(queue.db, queue.audit, store, registry)
	settings := NewDownloadSettingsService(queue.db, queue.audit)
	downloads := NewDownloadService(queue.db, queue.audit, store, downloaders, settings, queue, zerolog.Nop())
	root := t.TempDir()
	storage := models.Storage{Name: "Staging", NameNormalized: "waiting staging", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "qBit metadata", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:metadata"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.Claim([]string{"download"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	worker := NewDownloadWorker(downloads)
	worker.pollInterval = time.Millisecond
	result := worker.Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.Wait != nil || result.ErrorCode != "" || !client.metadataOnly || !client.paused || !client.resumed {
		t.Fatalf("result=%+v metadataOnly=%v paused=%v", result, client.metadataOnly, client.paused)
	}
	if client.category != "未识别" || !strings.HasPrefix(strings.ToLower(client.categoryPath), strings.ToLower(root)) || !providerPathsEqual(client.routedPath, client.categoryPath) {
		t.Fatalf("result=%+v category=%q categoryPath=%q routedPath=%q resumed=%v", result, client.category, client.categoryPath, client.routedPath, client.resumed)
	}
	var task models.DownloadTask
	if err := queue.db.First(&task, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.ScrapeStatus != "completed_unrecognized" && task.ScrapeStatus != "fallback_unrecognized" {
		t.Fatalf("task=%+v", task)
	}
	if task.Phase != models.DownloadTaskStatusCompleted {
		t.Fatalf("completion phase=%q", task.Phase)
	}
	var actionCount int64
	if err := queue.db.Model(&models.JobActionRequest{}).Where("job_id = ?", task.JobID).Count(&actionCount).Error; err != nil || actionCount != 0 {
		t.Fatalf("classification fallback created action requests: count=%d err=%v", actionCount, err)
	}
}

func TestProviderPathComparisonIsCrossPlatformAndRejectsDifferentTargets(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  bool
	}{
		{left: `D:\Staging\电影\`, right: `d:/staging/电影`, want: true},
		{left: `\\NAS\Share\Staging\电影`, right: `//nas/share/staging/电影/`, want: true},
		{left: `/srv/staging/电影/`, right: `/srv/staging/电影`, want: true},
		{left: `/srv/staging/电影`, right: `/srv/staging/other`, want: false},
		{left: `D:\Staging\电影`, right: `/srv/staging/电影`, want: false},
		{left: `relative/path`, right: `relative/path`, want: false},
	}
	for _, test := range tests {
		if got := providerPathsEqual(test.left, test.right); got != test.want {
			t.Fatalf("providerPathsEqual(%q, %q)=%v, want %v", test.left, test.right, got, test.want)
		}
	}
}

func TestRouteCategoryUpdatesExistingCategoryToTaskStagingSnapshotBeforeResume(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	root := t.TempDir()
	client := &metadataDownloadClient{stubDownloadClient: &stubDownloadClient{}, categories: []downloadpkg.Category{{Name: "电影", SavePath: filepath.Join(root, "outside")}}}
	worker := NewDownloadWorker(downloads)
	task := models.DownloadTask{ID: "route-update", ProviderTaskID: "provider-hash", StagingAbsolutePath: root}
	if err := worker.routeCategory(context.Background(), &task, client, root, "电影", "classified", "", ""); err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(root, "电影")
	if !providerPathsEqual(client.categoryPath, expected) || !providerPathsEqual(client.routedPath, expected) || !client.resumed {
		t.Fatalf("categoryPath=%q routedPath=%q resumed=%v", client.categoryPath, client.routedPath, client.resumed)
	}
	if got := strings.Join(client.categoryCalls, ","); got != "categories,update,categories,set,resume" {
		t.Fatalf("category call order=%q", got)
	}
}

func TestRouteCategoryRepairsExistingCategoryWithEmptySavePath(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	root := t.TempDir()
	client := &metadataDownloadClient{stubDownloadClient: &stubDownloadClient{}, categories: []downloadpkg.Category{{Name: "电影"}}}
	worker := NewDownloadWorker(downloads)
	task := models.DownloadTask{ID: "route-empty-path", ProviderTaskID: "provider-hash", StagingAbsolutePath: root}
	if err := worker.routeCategory(context.Background(), &task, client, root, "电影", "classified", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(client.categoryCalls, ","); got != "categories,update,categories,set,resume" {
		t.Fatalf("category call order=%q", got)
	}
}

func TestRouteCategoryCreatesAndVerifiesNewCategoryBeforeResume(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	root := t.TempDir()
	client := &metadataDownloadClient{stubDownloadClient: &stubDownloadClient{}}
	worker := NewDownloadWorker(downloads)
	task := models.DownloadTask{ID: "route-create", ProviderTaskID: "provider-hash", StagingAbsolutePath: root}
	if err := worker.routeCategory(context.Background(), &task, client, root, "剧集", "classified", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(client.categoryCalls, ","); got != "categories,create,categories,set,resume" {
		t.Fatalf("category call order=%q", got)
	}
}

func TestRouteCategoryRejectsProviderThatIgnoresCategoryUpdate(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	root := t.TempDir()
	client := &metadataDownloadClient{stubDownloadClient: &stubDownloadClient{}, categories: []downloadpkg.Category{{Name: "电影", SavePath: filepath.Join(root, "old")}}, keepCategoryPath: true}
	worker := NewDownloadWorker(downloads)
	task := models.DownloadTask{ProviderTaskID: "provider-hash", StagingAbsolutePath: root}
	if err := worker.routeCategory(context.Background(), &task, client, root, "电影", "classified", "", ""); codeOfProviderError(err) != "downloader_category_outside_staging" {
		t.Fatalf("route error=%v", err)
	}
	if client.resumed || client.routedPath != "" {
		t.Fatalf("unverified category was routed/resumed: routedPath=%q resumed=%v", client.routedPath, client.resumed)
	}
	if got := strings.Join(client.categoryCalls, ","); got != "categories,update,categories" {
		t.Fatalf("category call order=%q", got)
	}
}

func TestRouteCategoryDoesNotResumeWhenCategoryUpdateFails(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	root := t.TempDir()
	client := &metadataDownloadClient{
		stubDownloadClient: &stubDownloadClient{},
		categories:         []downloadpkg.Category{{Name: "电影", SavePath: filepath.Join(root, "old")}},
		updateCategoryErr:  downloadpkg.Error("downloader_category_update_failed", true, errors.New("provider rejected update")),
	}
	worker := NewDownloadWorker(downloads)
	task := models.DownloadTask{ProviderTaskID: "provider-hash", StagingAbsolutePath: root}
	err := worker.routeCategory(context.Background(), &task, client, root, "电影", "classified", "", "")
	code, retryable := downloadpkg.ErrorInfo(err)
	if code != "downloader_category_update_failed" || !retryable {
		t.Fatalf("code=%q retryable=%v err=%v", code, retryable, err)
	}
	if client.resumed || client.routedPath != "" {
		t.Fatalf("failed category update was routed/resumed: routedPath=%q resumed=%v", client.routedPath, client.resumed)
	}
	if got := strings.Join(client.categoryCalls, ","); got != "categories,update" {
		t.Fatalf("category call order=%q", got)
	}
}

func TestRouteCategoryRetryReusesImmutableTaskStagingSnapshot(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	root := t.TempDir()
	client := &metadataDownloadClient{
		stubDownloadClient: &stubDownloadClient{},
		categories:         []downloadpkg.Category{{Name: "电影", SavePath: filepath.Join(root, "old")}},
		updateCategoryErr:  downloadpkg.Error("downloader_category_update_failed", true, errors.New("temporary failure")),
	}
	worker := NewDownloadWorker(downloads)
	task := models.DownloadTask{ID: "route-retry", ProviderTaskID: "provider-hash", StagingAbsolutePath: root}
	if err := worker.routeCategory(context.Background(), &task, client, root, "电影", "classified", "", ""); err == nil {
		t.Fatal("first route unexpectedly succeeded")
	}
	client.updateCategoryErr = nil
	client.categoryCalls = nil
	if err := worker.routeCategory(context.Background(), &task, client, root, "电影", "classified", "", ""); err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(root, "电影")
	if !providerPathsEqual(client.categoryPath, expected) || !providerPathsEqual(client.routedPath, expected) || !client.resumed {
		t.Fatalf("categoryPath=%q routedPath=%q resumed=%v", client.categoryPath, client.routedPath, client.resumed)
	}
	if got := strings.Join(client.categoryCalls, ","); got != "categories,update,categories,set,resume" {
		t.Fatalf("retry call order=%q", got)
	}
}

func TestRouteCategoryRejectsResolvedPathOutsideTaskSnapshot(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	snapshot := t.TempDir()
	different := t.TempDir()
	client := &metadataDownloadClient{stubDownloadClient: &stubDownloadClient{}}
	worker := NewDownloadWorker(downloads)
	task := models.DownloadTask{ProviderTaskID: "provider-hash", StagingAbsolutePath: snapshot}
	if err := worker.routeCategory(context.Background(), &task, client, different, "电影", "classified", "", ""); codeOfProviderError(err) != "downloader_category_outside_staging" {
		t.Fatalf("route error=%v", err)
	}
	if len(client.categoryCalls) != 0 || client.resumed {
		t.Fatalf("outside snapshot reached provider: calls=%v resumed=%v", client.categoryCalls, client.resumed)
	}
}

func TestRouteCategoryRejectsMissingTaskStagingSnapshot(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	root := t.TempDir()
	client := &metadataDownloadClient{stubDownloadClient: &stubDownloadClient{}}
	worker := NewDownloadWorker(downloads)
	task := models.DownloadTask{ProviderTaskID: "provider-hash"}
	if err := worker.routeCategory(context.Background(), &task, client, root, "电影", "classified", "", ""); codeOfProviderError(err) != "downloader_category_outside_staging" {
		t.Fatalf("route error=%v", err)
	}
	if len(client.categoryCalls) != 0 || client.resumed {
		t.Fatalf("missing snapshot reached provider: calls=%v resumed=%v", client.categoryCalls, client.resumed)
	}
}

func TestDownloadWorkerLoadPromotesValidatedLegacyStagingSnapshotForStrictRouting(t *testing.T) {
	downloads, downloaders, queue, actor, _ := downloadFixture(t)
	metadataClient := &metadataDownloadClient{stubDownloadClient: &stubDownloadClient{}}
	registry := downloadpkg.NewRegistry()
	if err := registry.Register(models.DownloaderTypeQBittorrent, downloadpkg.Capabilities{}, func(downloadpkg.Config) (downloadpkg.Client, error) { return metadataClient, nil }); err != nil {
		t.Fatal(err)
	}
	downloaders.registry = registry
	root := t.TempDir()
	storage := models.Storage{Name: "Legacy staging", NameNormalized: "legacy staging", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "Legacy qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:legacy-staging"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var job models.Job
	if err := queue.db.First(&job, "id = ?", created.JobID).Error; err != nil {
		t.Fatal(err)
	}
	worker := NewDownloadWorker(downloads)
	claimed := ClaimedJob{Job: job}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Updates(map[string]any{"staging_absolute_path": "", "staging_storage_id": nil, "staging_relative_path": "/"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, err := worker.load(context.Background(), claimed); ErrorCode(err) != CodeDownloadStagingRequired {
		t.Fatalf("missing absolute and legacy snapshot error=%v code=%q", err, ErrorCode(err))
	}
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Updates(map[string]any{"staging_storage_id": storage.ID}).Error; err != nil {
		t.Fatal(err)
	}
	task, _, client, _, savePath, err := worker.load(context.Background(), claimed)
	if err != nil {
		t.Fatal(err)
	}
	if !providerPathsEqual(task.StagingAbsolutePath, root) || !providerPathsEqual(savePath, root) {
		t.Fatalf("promoted=%q savePath=%q root=%q", task.StagingAbsolutePath, savePath, root)
	}
	var persisted models.DownloadTask
	if err := queue.db.First(&persisted, "id = ?", created.ID).Error; err != nil || persisted.StagingAbsolutePath != "" {
		t.Fatalf("worker read persisted promoted path=%q err=%v", persisted.StagingAbsolutePath, err)
	}
	task.ProviderTaskID = "provider-hash"
	if err := worker.routeCategory(context.Background(), &task, client.(downloadpkg.MetadataClient), savePath, "电影", "classified", "", ""); err != nil {
		t.Fatal(err)
	}
	if !metadataClient.resumed || !providerPathsEqual(metadataClient.routedPath, filepath.Join(root, "电影")) {
		t.Fatalf("resumed=%v routedPath=%q", metadataClient.resumed, metadataClient.routedPath)
	}
}

func TestProviderRoutingFailureDoesNotPersistClassifiedState(t *testing.T) {
	downloads, downloaders, queue, actor, _ := downloadFixture(t)
	root := t.TempDir()
	storage := models.Storage{Name: "Routing staging", NameNormalized: "routing staging", Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := queue.db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, storage.ID)
	provider, err := downloaders.Create(actor, DownloaderInput{Name: "Routing qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:routing"}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := queue.db.First(&task, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	task.ProviderTaskID = "provider-hash"
	worker := NewDownloadWorker(downloads)
	confidence, tmdbID := .98, int64(550)
	match := scrapeMatch{Title: "Example", MediaType: "movie", Category: "电影", TMDBID: &tmdbID, Confidence: &confidence, Confident: true}
	if err := worker.persistScrape(&task, match, "matched", 1); err != nil {
		t.Fatal(err)
	}
	client := &metadataDownloadClient{stubDownloadClient: &stubDownloadClient{}, setCategoryErr: downloadpkg.Error("downloader_unavailable", true, errors.New("offline"))}
	if err := worker.routeCategory(context.Background(), &task, client, root, match.Category, "classified", "", ""); err == nil {
		t.Fatal("provider routing failure was ignored")
	}
	var persisted models.DownloadTask
	if err := queue.db.First(&persisted, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ScrapeStatus != "matched" || persisted.ScrapeStatus == "classified" || client.resumed {
		t.Fatalf("routing failure skipped retry state: status=%q resumed=%v", persisted.ScrapeStatus, client.resumed)
	}
}

func codeOfProviderError(err error) string {
	code, _ := downloadpkg.ErrorInfo(err)
	return code
}

func TestSchedulerReconcilesWaitingDownloadCancelBeforeFinalJobState(t *testing.T) {
	for _, test := range []struct {
		name       string
		cancelErr  error
		wantStatus string
	}{
		{name: "provider confirms cancel", wantStatus: "deleted"},
		{name: "provider rejects cancel", cancelErr: downloadpkg.Error("downloader_unavailable", true, errors.New("offline")), wantStatus: models.JobStatusFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			downloads, downloaders, queue, actor, client := downloadFixture(t)
			client.cancelErr = test.cancelErr
			root := t.TempDir()
			storage := models.Storage{Name: "Control staging " + test.name, NameNormalized: "control staging " + test.name, Type: models.StorageTypeLocal, RootPath: root, RootPathNormalized: strings.ToLower(root), Enabled: true, Capabilities: `{}`, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if err := queue.db.Create(&storage).Error; err != nil {
				t.Fatal(err)
			}
			configureDownloadStaging(t, queue, storage.ID)
			provider, err := downloaders.Create(actor, DownloaderInput{Name: "Control qBit " + test.name, Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
			if err != nil {
				t.Fatal(err)
			}
			created, err := downloads.Submit(context.Background(), actor, SubmitDownloadInput{DownloaderID: provider.ID, Source: DownloadSourceInput{Kind: downloadpkg.SourceURL, URL: "magnet:?xt=urn:btih:control"}}, RequestContext{})
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := queue.Claim([]string{"download"})
			if err != nil || claimed == nil {
				t.Fatalf("claim=%+v err=%v", claimed, err)
			}
			if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", created.ID).Updates(map[string]any{"provider_task_id": "provider-hash", "phase": models.DownloadTaskStatusWaiting}).Error; err != nil {
				t.Fatal(err)
			}
			if err := queue.Fail(claimed.Job.ID, claimed.LeaseToken, "test_failure", "测试失败"); err != nil {
				t.Fatal(err)
			}
			controlled, err := queue.Control(actor, claimed.Job.ID, "cancel", RequestContext{})
			if err != nil {
				t.Fatal(err)
			}
			if controlled.Status != models.JobStatusQueued || controlled.InterruptPending != models.JobStatusCancelled || controlled.Action != nil || client.cancelled {
				t.Fatalf("control=%+v providerCalled=%v", controlled, client.cancelled)
			}
			worker := NewDownloadWorker(downloads)
			worker.pollInterval = time.Millisecond
			registry := NewWorkerRegistry()
			if err := registry.Register("download", worker); err != nil {
				t.Fatal(err)
			}
			scheduler := NewScheduler(queue, registry, zerolog.Nop())
			scheduler.tick = 100 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			if err := scheduler.Start(ctx); err != nil {
				cancel()
				t.Fatal(err)
			}
			defer func() { cancel(); scheduler.Close() }()
			queue.wake()
			deadline := time.Now().Add(3 * time.Second)
			var final JobDTO
			for time.Now().Before(deadline) {
				final, err = queue.Get(actor, claimed.Job.ID)
				if test.wantStatus == "deleted" && ErrorCode(err) == CodeNotFound {
					break
				}
				if err == nil && final.Status == test.wantStatus {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			client.mu.Lock()
			providerCancelled, deleteData := client.cancelled, client.deleteData
			client.mu.Unlock()
			deleted := test.wantStatus == "deleted" && ErrorCode(err) == CodeNotFound
			if (!deleted && final.Status != test.wantStatus) || !providerCancelled || !deleteData || (!deleted && (final.InterruptPending != "" || final.CancellationRequested)) {
				var attempts []models.JobAttempt
				_ = queue.db.Where("job_id = ?", claimed.Job.ID).Order("attempt_number").Find(&attempts).Error
				t.Fatalf("final=%+v cancelled=%v deleteData=%v err=%v attempts=%+v", final, providerCancelled, deleteData, err, attempts)
			}
			var task models.DownloadTask
			taskErr := queue.db.First(&task, "id = ?", created.ID).Error
			if test.cancelErr == nil && !errors.Is(taskErr, gorm.ErrRecordNotFound) {
				t.Fatalf("cancelled provider task remained locally: task=%+v err=%v", task, taskErr)
			}
			if test.cancelErr != nil && (taskErr != nil || task.Phase == models.DownloadTaskStatusCancelled) {
				t.Fatalf("failed provider cancellation lost/changed local task: task=%+v err=%v", task, taskErr)
			}
		})
	}
}
