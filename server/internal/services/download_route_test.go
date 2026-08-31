package services

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

type routeReadCloudDriver struct{ *fakeMutationCloudDriver }

func newRouteReadCloudDriver() *routeReadCloudDriver {
	return &routeReadCloudDriver{fakeMutationCloudDriver: newFakeMutationCloudDriver()}
}

func (f *routeReadCloudDriver) Capabilities() cloudpkg.Capabilities {
	capabilities := f.fakeMutationCloudDriver.Capabilities()
	capabilities.TemporaryDirectURL = true
	return capabilities
}

func (f *routeReadCloudDriver) OpenRead(context.Context, cloudpkg.ReadRequest) (cloudpkg.ReadResult, error) {
	size := int64(0)
	return cloudpkg.ReadResult{Body: io.NopCloser(strings.NewReader("")), OffsetAccepted: true, TotalSize: &size}, nil
}

func TestSelectTransferRouteUsesStableDataSourceIdentity(t *testing.T) {
	local := localDataSourceIdentity()
	panAOne := models.DataSourceIdentity{Kind: models.DataSourceKindProvider, ProviderType: models.StorageTypePan115, ConnectionIdentity: "1", StorageScope: "10"}
	panATwo := models.DataSourceIdentity{Kind: models.DataSourceKindProvider, ProviderType: models.StorageTypePan115, ConnectionIdentity: "1", StorageScope: "20"}
	panB := models.DataSourceIdentity{Kind: models.DataSourceKindProvider, ProviderType: models.StorageTypePan115, ConnectionIdentity: "2", StorageScope: "10"}
	for _, test := range []struct {
		name           string
		source, target models.DataSourceIdentity
		want           string
	}{
		{name: "local to local", source: local, target: local, want: models.TransferRouteSameSourceLocal},
		{name: "same account different roots", source: panAOne, target: panATwo, want: models.TransferRouteSameSourceProvider},
		{name: "local to provider", source: local, target: panAOne, want: models.TransferRouteCrossSource},
		{name: "provider to local", source: panAOne, target: local, want: models.TransferRouteCrossSource},
		{name: "different accounts", source: panAOne, target: panB, want: models.TransferRouteCrossSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := selectTransferRoute(test.source, test.target); got != test.want {
				t.Fatalf("route=%q want=%q", got, test.want)
			}
		})
	}
}

func TestRoutePreviewAllowsAuthoritativePublicBTTorrentForPan115(t *testing.T) {
	downloads, _, queue, _, _ := downloadFixture(t)
	now := time.Now().UTC()
	mikan := models.Site{Name: "Mikan", NameNormalized: "route-mikan-" + uuid.NewString(), Kind: "mikan", BaseURL: "https://mikanani.me", Enabled: true, Priority: 1, TimeoutSeconds: 10, RateLimitPerMinute: 10, Revision: 1, CreatedAt: now, UpdatedAt: now}
	pt := models.Site{Name: "PT", NameNormalized: "route-pt-" + uuid.NewString(), Kind: "nexusphp", BaseURL: "https://pt.example.test", Enabled: true, Priority: 1, TimeoutSeconds: 10, RateLimitPerMinute: 10, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&mikan).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Create(&pt).Error; err != nil {
		t.Fatal(err)
	}
	pan := models.Downloader{Type: models.DownloaderTypePan115Offline}
	if err := downloads.validatePreviewSource(pan, downloader.SourceTorrent, &mikan.ID); err != nil {
		t.Fatalf("authoritative public BT preview rejected: %v", err)
	}
	if err := downloads.validatePreviewSource(pan, downloader.SourceTorrent, &pt.ID); ErrorCode(err) != CodeDownloadSourceInvalid {
		t.Fatalf("PT preview error=%v", err)
	}
	if err := downloads.validatePreviewSource(pan, downloader.SourceTorrent, nil); ErrorCode(err) != CodeDownloadSourceInvalid {
		t.Fatalf("untrusted manual torrent preview error=%v", err)
	}
}

func TestManagedStagingPreviewReportsRequiredAndAvailableBytes(t *testing.T) {
	downloads, _, queue, _, _ := downloadFixture(t)
	root := t.TempDir()
	if err := queue.db.Model(&models.DownloadSettings{}).Where("id = ?", 1).Updates(map[string]any{"absolute_path": root, "storage_id": nil, "relative_path": "/"}).Error; err != nil {
		t.Fatal(err)
	}
	expected := int64(1)
	option := DownloadRouteTargetOption{RequiresManagedStaging: true}
	if err := downloads.applyManagedStagingPreview(context.Background(), models.DownloaderTypePan115Offline, &expected, &option); err != nil {
		t.Fatal(err)
	}
	if option.RequiredBytes == nil || *option.RequiredBytes != expected+int64(crossSourceMinimumFreeBytes) || option.AvailableBytes == nil || *option.AvailableBytes <= 0 {
		t.Fatalf("space preview=%+v", option)
	}
}

func TestDownloadTargetSnapshotAllowsCrossSourceAndPreservesSamePan115(t *testing.T) {
	downloads, downloaders, queue, actor, _ := downloadFixture(t)
	now := time.Now().UTC()
	connectionA := createRouteConnection(t, queue, "a", now)
	connectionB := createRouteConnection(t, queue, "b", now)
	readA := newRouteReadCloudDriver()
	uploadB := newFakeUploadCloudDriver()
	for _, driver := range []*fakeMutationCloudDriver{readA.fakeMutationCloudDriver, uploadB.fakeMutationCloudDriver} {
		driver.items["0"] = cloudpkg.Item{ID: "0", IsDir: true}
	}
	readA.items["source-root"] = cloudpkg.Item{ID: "source-root", ParentID: "0", IsDir: true}
	readA.items["source-downloads"] = cloudpkg.Item{ID: "source-downloads", ParentID: "source-root", IsDir: true}
	readA.items["source-library"] = cloudpkg.Item{ID: "source-library", ParentID: "source-root", IsDir: true}
	uploadB.items["target-root"] = cloudpkg.Item{ID: "target-root", ParentID: "0", IsDir: true}
	uploadB.items["target-library"] = cloudpkg.Item{ID: "target-library", ParentID: "target-root", IsDir: true}
	downloaders.connections = &ConnectionService{db: queue.db, drivers: map[uint]cloudpkg.Driver{connectionA.ID: readA, connectionB.ID: uploadB}}

	sourceStorage := createRouteStorage(t, queue, "source", connectionA.ID, "source-root", now)
	sameStorage := createRouteStorage(t, queue, "same", connectionA.ID, "source-root", now)
	targetStorage := createRouteStorage(t, queue, "target", connectionB.ID, "target-root", now)
	var profile models.MediaClassificationProfile
	if err := queue.db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	sameLibrary := createRouteLibrary(t, queue, "same", sameStorage.ID, profile.ID, "source-library", now)
	targetLibrary := createRouteLibrary(t, queue, "target", targetStorage.ID, profile.ID, "target-library", now)
	localRoot := t.TempDir()
	localStorage := models.Storage{Name: "local", NameNormalized: "route-local-" + uuid.NewString(), Type: models.StorageTypeLocal, RootPath: localRoot, RootPathNormalized: strings.ToLower(localRoot), Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&localStorage).Error; err != nil {
		t.Fatal(err)
	}
	localLibrary := createRouteLibrary(t, queue, "local", localStorage.ID, profile.ID, "", now)
	panDownloader := models.Downloader{ID: "pan-route", Type: models.DownloaderTypePan115Offline, StorageID: &sourceStorage.ID, ProviderDirectoryID: "source-downloads", Enabled: true}
	if err := queue.db.Create(&panDownloader).Error; err != nil {
		t.Fatal(err)
	}
	beforeReadStats, beforeUploadStats := readA.statCalls, uploadB.statCalls
	preview, err := downloads.PreviewRoutes(context.Background(), actor, DownloadRoutePreviewInput{DownloaderID: panDownloader.ID, SourceKind: downloader.SourceURL})
	if err != nil || len(preview.Options) < 3 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if readA.statCalls != beforeReadStats || uploadB.statCalls != beforeUploadStats {
		t.Fatalf("route preview called provider: source=%d->%d target=%d->%d", beforeReadStats, readA.statCalls, beforeUploadStats, uploadB.statCalls)
	}

	if target, _, err := downloads.snapshotDownloadTarget(context.Background(), panDownloader, sameLibrary, downloader.SourceURL); err != nil || target.RouteKind != models.TransferRouteSameSourceProvider {
		t.Fatalf("same-account route=%+v err=%v", target, err)
	}
	if target, _, err := downloads.snapshotDownloadTarget(context.Background(), panDownloader, localLibrary, downloader.SourceURL); err != nil || target.RouteKind != models.TransferRouteCrossSource {
		t.Fatalf("115-to-local route=%+v err=%v", target, err)
	}
	if target, _, err := downloads.snapshotDownloadTarget(context.Background(), panDownloader, targetLibrary, downloader.SourceURL); err != nil || target.RouteKind != models.TransferRouteCrossSource {
		t.Fatalf("115-A-to-115-B route=%+v err=%v", target, err)
	}
	qbit := models.Downloader{ID: "qbit-route", Type: models.DownloaderTypeQBittorrent, Enabled: true}
	if target, _, err := downloads.snapshotDownloadTarget(context.Background(), qbit, targetLibrary, downloader.SourceTorrent); err != nil || target.RouteKind != models.TransferRouteCrossSource {
		t.Fatalf("local-PT-to-115 route=%+v err=%v", target, err)
	}
	if err := queue.db.Model(&models.DownloadSettings{}).Where("id = ?", 1).Updates(map[string]any{"absolute_path": t.TempDir(), "storage_id": nil, "relative_path": "/"}).Error; err != nil {
		t.Fatal(err)
	}
	follows := NewFollowService(queue.db, queue.audit, queue, nil, NewAuthorizationService(queue.db))
	follows.SetDownloadService(downloads)
	btSite := models.Site{Kind: "mikan", Enabled: true}
	ptSite := models.Site{Kind: "pttime", Enabled: true}
	if err := follows.validateFollowRoute(context.Background(), panDownloader, localLibrary, []models.Site{btSite}); err != nil {
		t.Fatalf("follow 115-to-local route rejected: %v", err)
	}
	if err := follows.validateFollowRoute(context.Background(), qbit, targetLibrary, []models.Site{ptSite}); err != nil {
		t.Fatalf("follow local-PT-to-115 route rejected: %v", err)
	}
	if err := follows.validateFollowRoute(context.Background(), panDownloader, localLibrary, []models.Site{ptSite}); ErrorCode(err) != CodeFollowConfigurationInvalid {
		t.Fatalf("follow PT-to-115 downloader accepted: %v", err)
	}
	if _, _, err := downloads.resolveDownloadTarget(context.Background(), qbit, 0, downloader.SourceURL); ErrorCode(err) != CodeMediaLibraryStorageUnavailable {
		t.Fatalf("implicit sorted fallback survived: %v", err)
	}
}

func createRouteConnection(t *testing.T, queue *QueueService, suffix string, now time.Time) models.Connection {
	t.Helper()
	record := models.Connection{Name: "115-" + suffix, NameNormalized: "route-connection-" + suffix + "-" + uuid.NewString(), Provider: cloudpkg.ProviderPan115, CredentialCiphertext: "encrypted", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	return record
}

func createRouteStorage(t *testing.T, queue *QueueService, suffix string, connectionID uint, root string, now time.Time) models.Storage {
	t.Helper()
	record := models.Storage{Name: suffix, NameNormalized: "route-storage-" + suffix + "-" + uuid.NewString(), Type: models.StorageTypePan115, RootPath: root, RootPathNormalized: "route:" + suffix + ":" + uuid.NewString(), ConnectionID: &connectionID, Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	return record
}

func createRouteLibrary(t *testing.T, queue *QueueService, suffix string, storageID, profileID uint, providerRoot string, now time.Time) models.MediaLibrary {
	t.Helper()
	record := models.MediaLibrary{Name: suffix, NameNormalized: "route-library-" + suffix + "-" + uuid.NewString(), StorageID: storageID, ProfileID: profileID, ProfileRevision: 1, RelativeRoot: "/", ProviderRootID: providerRoot, TransferMode: models.MediaLibraryTransferCopy, ConflictPolicy: models.MediaLibraryConflictAsk, Enabled: true, Recursive: true, VideoExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, CreatedAt: now, UpdatedAt: now}
	if err := queue.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	return record
}
