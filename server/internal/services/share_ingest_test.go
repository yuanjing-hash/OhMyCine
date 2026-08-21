package services

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

type shareIngestDriver struct{ *fakeCloudDriver }

func (d *shareIngestDriver) Capabilities() cloudpkg.Capabilities {
	return cloudpkg.Capabilities{NetworkDrive: true, DirectoryList: true, NativeOfflineDownload: true, ShareReceive: true, CreateDirectory: true, Move: true, Copy: true, Rename: true, Recycle: true}
}
func (d *shareIngestDriver) CreateDirectory(context.Context, string, string) (cloudpkg.Item, error) {
	return cloudpkg.Item{}, nil
}
func (d *shareIngestDriver) Move(context.Context, string, string) error   { return nil }
func (d *shareIngestDriver) Copy(context.Context, string, string) error   { return nil }
func (d *shareIngestDriver) Rename(context.Context, string, string) error { return nil }
func (d *shareIngestDriver) Recycle(context.Context, string) error        { return nil }

type shareIngestFixture struct {
	db         *gorm.DB
	store      *credential.Store
	actor      Actor
	storage    models.Storage
	profile    models.MediaClassificationProfile
	downloader models.Downloader
	driver     *shareIngestDriver
	libraries  *MediaLibraryService
	downloads  *DownloadService
}

func newShareIngestFixture(t *testing.T) shareIngestFixture {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "share-ingest.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	store, err := credential.Open(filepath.Join(t.TempDir(), "credential.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	base := &fakeCloudDriver{items: map[string]cloudpkg.Item{
		"storage": {ID: "storage", ParentID: "0", Name: "媒体盘", IsDir: true},
		"library": {ID: "library", ParentID: "storage", Name: "媒体库", IsDir: true},
		"intake":  {ID: "intake", ParentID: "storage", Name: "中转", IsDir: true},
		"nested":  {ID: "nested", ParentID: "intake", Name: "中转子目录", IsDir: true},
		"other":   {ID: "other", ParentID: "storage", Name: "其它媒体库", IsDir: true},
		"manual":  {ID: "manual", ParentID: "intake", Name: "Seven.Samurai.1954", IsDir: true},
	}, children: map[string][]cloudpkg.Item{}}
	driver := &shareIngestDriver{fakeCloudDriver: base}
	cloudRegistry := cloudpkg.NewRegistry()
	if err := cloudRegistry.Register(cloudpkg.ProviderPan115, func(cloudpkg.Config) (cloudpkg.Driver, error) { return driver, nil }); err != nil {
		t.Fatal(err)
	}
	audit := NewAuditService(db)
	connections := NewConnectionService(db, audit, store, cloudRegistry, zerolog.Nop())
	user := models.User{Username: "share-ingest", UsernameNormalized: "share-ingest", DisplayName: "Share Ingest", PasswordHash: "test", Status: models.UserStatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	permissions := map[string]struct{}{}
	for _, code := range []string{authz.PermissionConnectionsRead, authz.PermissionConnectionsCreate, authz.PermissionStoragesCreate, authz.PermissionStoragesRead, authz.PermissionMediaLibrariesRead, authz.PermissionMediaLibrariesCreate, authz.PermissionMediaLibrariesUpdate, authz.PermissionDownloadersDelete, authz.PermissionDownloadsCreate} {
		permissions[code] = struct{}{}
	}
	actor := Actor{User: user, Permissions: permissions}
	connection, err := connections.Create(actor, ConnectionInput{Name: "115", Provider: cloudpkg.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	storageService := NewStorageService(db, audit)
	storageService.SetConnectionService(connections)
	storageSummary, err := storageService.CreateContext(context.Background(), actor, StorageInput{Name: "115 root", Type: models.StorageTypePan115, RootPath: "storage", RootDisplayPath: "/媒体盘", ConnectionID: &connection.ID, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var storage models.Storage
	if err := db.First(&storage, storageSummary.ID).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	capabilities := downloadpkg.Capabilities{Cancel: true, DeleteData: true, NativeOffline: true, ShareReceive: true, OutputConstraint: downloadpkg.OutputConstraintProviderStorage}
	capabilitiesJSON, _ := json.Marshal(capabilities)
	now := time.Now().UTC()
	downloader := models.Downloader{ID: "115-downloader", Name: "115 下载器", NameNormalized: "115 下载器", Type: models.DownloaderTypePan115Offline, StorageID: &storage.ID, ProviderDirectoryID: "intake", ProviderDirectoryPath: "/中转", Enabled: true, CapabilitiesJSON: string(capabilitiesJSON), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&downloader).Error; err != nil {
		t.Fatal(err)
	}
	downloadRegistry := downloadpkg.NewRegistry()
	client := &stubDownloadClient{}
	if err := downloadRegistry.Register(models.DownloaderTypePan115Offline, capabilities, func(downloadpkg.Config) (downloadpkg.Client, error) { return client, nil }); err != nil {
		t.Fatal(err)
	}
	downloaders := NewDownloaderService(db, audit, store, downloadRegistry)
	downloaders.SetConnectionService(connections)
	queue := NewQueueService(db, audit)
	downloads := NewDownloadService(db, audit, store, downloaders, NewDownloadSettingsService(db, audit), queue, zerolog.Nop())
	libraries := NewMediaLibraryService(db, audit, zerolog.Nop())
	libraries.SetConnectionService(connections)
	libraries.SetIngestEnqueuer(downloads)
	t.Cleanup(libraries.Close)
	return shareIngestFixture{db: db, store: store, actor: actor, storage: storage, profile: profile, downloader: downloader, driver: driver, libraries: libraries, downloads: downloads}
}

type recordingIngestEnqueuer struct{ items []string }

func (r *recordingIngestEnqueuer) AdoptProviderItem(_ context.Context, _ uint, itemID, _ string) (bool, error) {
	r.items = append(r.items, itemID)
	return true, nil
}

func (f shareIngestFixture) createLibrary(t *testing.T, name, finalID, intakeID, intakePath string) MediaLibraryDetail {
	t.Helper()
	item, err := f.libraries.Create(context.Background(), f.actor, MediaLibraryInput{Name: name, StorageID: f.storage.ID, ProfileID: f.profile.ID, RelativeRoot: "/" + name, ProviderRootID: finalID, Enabled: false, Recursive: true, TransferMode: models.MediaLibraryTransferCopy, IngestEnabled: true, IngestDownloaderID: f.downloader.ID, IngestProviderRootID: intakeID, IngestRelativeRoot: intakePath}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestMediaLibraryIngestValidatesOverlapAndDownloaderReference(t *testing.T) {
	fixture := newShareIngestFixture(t)
	library := fixture.createLibrary(t, "电影", "library", "intake", "/中转")
	if !library.IngestEnabled || library.IngestDownloaderID == nil || *library.IngestDownloaderID != fixture.downloader.ID || library.IngestRelativeRoot != "/中转" {
		t.Fatalf("ingest detail=%+v", library.MediaLibrary)
	}
	if err := fixture.downloads.downloader.Delete(fixture.actor, fixture.downloader.ID, RequestContext{}); ErrorCode(err) != CodeDownloaderInUse {
		t.Fatalf("delete referenced downloader error=%v", err)
	}
	_, err := fixture.libraries.Create(context.Background(), fixture.actor, MediaLibraryInput{Name: "冲突库", StorageID: fixture.storage.ID, ProfileID: fixture.profile.ID, RelativeRoot: "/其它", ProviderRootID: "other", Enabled: false, Recursive: true, TransferMode: models.MediaLibraryTransferCopy, IngestEnabled: true, IngestDownloaderID: fixture.downloader.ID, IngestProviderRootID: "nested", IngestRelativeRoot: "/中转/子目录"}, RequestContext{})
	if ErrorCode(err) != CodeMediaLibraryOverlap {
		t.Fatalf("nested intake overlap error=%v", err)
	}
}

func TestShareAndProviderAdoptionUseEncryptedImmutableIntakeSnapshot(t *testing.T) {
	fixture := newShareIngestFixture(t)
	library := fixture.createLibrary(t, "电影", "library", "intake", "/中转")
	if err := fixture.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	zero := uint(0)
	shareLink := "https://115.com/s/example?password=abcd"
	created, err := fixture.downloads.Submit(context.Background(), fixture.actor, SubmitDownloadInput{DownloaderID: fixture.downloader.ID, MediaLibraryID: &zero, Source: DownloadSourceInput{Kind: downloadpkg.SourcePan115Share, URL: shareLink}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var shareTask models.DownloadTask
	if err := fixture.db.First(&shareTask, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if shareTask.SourceOrigin != models.DownloadSourceOriginShare || shareTask.StagingProviderDirectoryID != "intake" || strings.Contains(shareTask.SourceCiphertext, shareLink) {
		t.Fatalf("share snapshot leaked or drifted: %+v", shareTask)
	}
	publicSummary, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicSummary), shareLink) || strings.Contains(string(publicSummary), "intake") {
		t.Fatalf("private share facts leaked into public summary: %s", publicSummary)
	}
	var shareAudit models.AuditLog
	if err := fixture.db.Where("target_type = ? AND target_id = ?", "download_task", created.ID).First(&shareAudit).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shareAudit.Metadata, shareLink) || strings.Contains(shareAudit.Metadata, "intake") {
		t.Fatalf("private share facts leaked into audit metadata: %s", shareAudit.Metadata)
	}
	plaintext, err := fixture.store.Decrypt(downloadSourcePurpose(shareTask.ID), shareTask.SourceCiphertext)
	if err != nil || !strings.Contains(plaintext, shareLink) {
		t.Fatalf("encrypted share source cannot round-trip: %q err=%v", plaintext, err)
	}
	wasCreated, err := fixture.downloads.AdoptProviderItem(context.Background(), library.ID, "manual", "Seven.Samurai.1954")
	if err != nil || !wasCreated {
		t.Fatalf("first adoption created=%v err=%v", wasCreated, err)
	}
	wasCreated, err = fixture.downloads.AdoptProviderItem(context.Background(), library.ID, "manual", "Seven.Samurai.1954")
	if err != nil || wasCreated {
		t.Fatalf("duplicate adoption created=%v err=%v", wasCreated, err)
	}
	var adopted models.DownloadTask
	if err := fixture.db.Where("source_origin = ?", models.DownloadSourceOriginProviderIngest).First(&adopted).Error; err != nil {
		t.Fatal(err)
	}
	if adopted.IngestSourceKey == "" || adopted.StagingProviderDirectoryID != "intake" || strings.Contains(adopted.SourceCiphertext, "manual") {
		t.Fatalf("adopted snapshot leaked or drifted: %+v", adopted)
	}
	var job models.Job
	if err := fixture.db.First(&job, "id = ?", adopted.JobID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(job.PayloadJSON, "manual") || strings.Contains(job.PayloadJSON, "intake") || strings.Contains(job.PayloadJSON, "115.com") {
		t.Fatalf("private provider facts leaked into job payload: %s", job.PayloadJSON)
	}
}

func TestOrdinaryPan115OfflineDownloadKeepsDownloaderStagingDirectory(t *testing.T) {
	fixture := newShareIngestFixture(t)
	library := fixture.createLibrary(t, "电影", "library", "intake", "/中转")
	if err := fixture.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}

	created, err := fixture.downloads.Submit(context.Background(), fixture.actor, SubmitDownloadInput{
		DownloaderID:   fixture.downloader.ID,
		MediaLibraryID: &library.ID,
		Source: DownloadSourceInput{
			Kind: downloadpkg.SourceURL,
			URL:  "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		},
	}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := fixture.db.First(&task, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.StagingProviderDirectoryID != "" {
		t.Fatalf("ordinary 115 offline task unexpectedly overrides staging directory with %q", task.StagingProviderDirectoryID)
	}
}

func TestShareAndAdoptedWorkersUseShareIngestLogOperation(t *testing.T) {
	if got := downloadOperation(models.DownloaderTypePan115Offline, models.DownloadSourceOriginShare); got != serverlog.OperationPan115ShareIngest {
		t.Fatalf("share operation=%+v", got)
	}
	if got := downloadOperation(models.DownloaderTypePan115Offline, models.DownloadSourceOriginProviderIngest); got != serverlog.OperationPan115ShareIngest {
		t.Fatalf("adopted operation=%+v", got)
	}
	if got := downloadOperation(models.DownloaderTypePan115Offline, models.DownloadSourceOriginUser); got != serverlog.OperationPan115OfflineDownload {
		t.Fatalf("ordinary offline operation=%+v", got)
	}
}

func TestMediaLibraryIngestSweepUsesDirectChildrenAndSkipsSystemFolders(t *testing.T) {
	fixture := newShareIngestFixture(t)
	library := fixture.createLibrary(t, "电影", "library", "intake", "/中转")
	fixture.driver.children["intake"] = []cloudpkg.Item{
		{ID: "system", ParentID: "intake", Name: "omc-task-id", IsDir: true},
		{ID: "manual", ParentID: "intake", Name: "Seven.Samurai.1954", IsDir: true},
	}
	if err := fixture.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &recordingIngestEnqueuer{}
	fixture.libraries.SetIngestEnqueuer(recorder)
	if err := fixture.libraries.sweepIngest(context.Background(), library.ID); err != nil {
		t.Fatal(err)
	}
	if len(recorder.items) != 1 || recorder.items[0] != "manual" {
		t.Fatalf("adopted items=%v, want [manual]", recorder.items)
	}
}
