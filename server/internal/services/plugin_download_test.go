package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"gorm.io/gorm"
)

type fakePluginAssetGateway struct {
	assets map[string]struct {
		body        []byte
		contentType string
	}
}

func (g fakePluginAssetGateway) OpenAssetForPluginConnection(_ context.Context, pluginID, connectionID, ref, method, rangeHeader string) (*hostapi.AssetStream, error) {
	asset, ok := g.assets[ref]
	if !ok || pluginID == "" || connectionID == "" || method != http.MethodGet || rangeHeader != "" {
		return nil, errors.New("unexpected asset request")
	}
	return &hostapi.AssetStream{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {asset.contentType}}, Body: io.NopCloser(bytes.NewReader(asset.body))}, nil
}

type fakeMediaTool struct{ calls int }

func (*fakeMediaTool) Version(context.Context) (string, error) { return "fixture", nil }
func (tool *fakeMediaTool) MergeDASH(_ context.Context, video, audio, output string) error {
	tool.calls++
	videoBody, err := os.ReadFile(video)
	if err != nil {
		return err
	}
	audioBody, err := os.ReadFile(audio)
	if err != nil {
		return err
	}
	return os.WriteFile(output, append(videoBody, audioBody...), 0o600)
}

type pluginDownloadTestRuntime struct{}

func (pluginDownloadTestRuntime) Heartbeat(*float64, *int64, *int64, *float64, *int64) error {
	return nil
}
func (pluginDownloadTestRuntime) Checkpoint(any) error { return nil }

func validPluginDownloadPlan() (contract.DownloadPlan, downloadSourceEnvelope) {
	source := downloadSourceEnvelope{Kind: "plugin_plan", PluginConnectionID: uuid.NewString(), PluginItemID: "item", PluginSegmentID: "segment", PluginVersionID: "version", PluginVariantID: "1080p"}
	videoRef, audioRef := uuid.NewString(), uuid.NewString()
	plan := contract.DownloadPlan{
		WorkID: source.PluginItemID, SegmentID: source.PluginSegmentID, VersionID: source.PluginVersionID, VariantID: source.PluginVariantID,
		SuggestedFileName: "Example.2026.1080p.mkv",
		Assets: []contract.DownloadAsset{
			{ID: "video", Kind: "video", URLRef: videoRef, ExpectedContentType: "video/mp4", ExpectedBytes: 100},
			{ID: "audio", Kind: "audio", URLRef: audioRef, ExpectedContentType: "audio/mp4", ExpectedBytes: 10},
		},
		Merge: &contract.DownloadMerge{Kind: "dash-av", VideoAssetID: "video", AudioAssetID: "audio"},
	}
	return plan, source
}

func TestValidatePluginDownloadPlan(t *testing.T) {
	plan, source := validPluginDownloadPlan()
	if err := validateDownloadPlan(plan, source); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*contract.DownloadPlan)
	}{
		{"path traversal", func(plan *contract.DownloadPlan) { plan.SuggestedFileName = "../escape.mkv" }},
		{"raw URL", func(plan *contract.DownloadPlan) { plan.Assets[0].URLRef = "https://example.com/video" }},
		{"header reference", func(plan *contract.DownloadPlan) { plan.Assets[0].HeadersRef = "secret" }},
		{"duplicate asset", func(plan *contract.DownloadPlan) { plan.Assets[1].ID = plan.Assets[0].ID }},
		{"unreferenced audio", func(plan *contract.DownloadPlan) {
			plan.Assets = append(plan.Assets, contract.DownloadAsset{ID: "audio-extra", Kind: "audio", URLRef: uuid.NewString(), ExpectedContentType: "audio/mp4", ExpectedBytes: 10})
		}},
		{"arbitrary merge", func(plan *contract.DownloadPlan) { plan.Merge.Kind = "command" }},
		{"identity mismatch", func(plan *contract.DownloadPlan) { plan.SegmentID = "other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := plan
			candidate.Assets = append([]contract.DownloadAsset(nil), plan.Assets...)
			merge := *plan.Merge
			candidate.Merge = &merge
			test.mutate(&candidate)
			if err := validateDownloadPlan(candidate, source); err == nil {
				t.Fatal("unsafe plan was accepted")
			}
		})
	}
}

func TestPluginDownloadManagedCleanupIsTaskScoped(t *testing.T) {
	staging := t.TempDir()
	taskID := uuid.NewString()
	taskRoot := filepath.Join(staging, pluginDownloadRootName, taskID)
	if err := os.MkdirAll(taskRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(taskRoot, "part.bin")
	outside := filepath.Join(staging, "keep.bin")
	if err := os.WriteFile(inside, []byte("part"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeManagedTaskRoot(staging, taskRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(taskRoot); !os.IsNotExist(err) {
		t.Fatalf("task root remains: %v", err)
	}
	if body, err := os.ReadFile(outside); err != nil || string(body) != "keep" {
		t.Fatalf("cleanup escaped managed task root: body=%q err=%v", body, err)
	}
	if err := removeManagedTaskRoot(staging, staging); err == nil {
		t.Fatal("broad cleanup target was accepted")
	}
}

func TestPluginDownloadManagedCleanupRejectsTaskRootLink(t *testing.T) {
	staging := t.TempDir()
	taskID := uuid.NewString()
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep.bin")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	managedParent := filepath.Join(staging, pluginDownloadRootName)
	if err := os.MkdirAll(managedParent, 0o700); err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Join(managedParent, taskID)
	if err := os.Symlink(outside, taskRoot); err != nil {
		t.Skipf("task-root link fixture is unavailable: %v", err)
	}
	if err := removeManagedTaskRoot(staging, taskRoot); err == nil {
		t.Fatal("linked task root was accepted for cleanup")
	}
	if body, err := os.ReadFile(keep); err != nil || string(body) != "keep" {
		t.Fatalf("linked cleanup escaped managed root: body=%q err=%v", body, err)
	}
}

func TestPluginDownloadTaskRootUsesOnlyTaskIdentity(t *testing.T) {
	executor := &PluginDownloadExecutor{}
	staging := t.TempDir()
	task := models.DownloadTask{ID: uuid.NewString(), StagingAbsolutePath: staging}
	root, err := executor.taskRoot(task)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(staging, pluginDownloadRootName, task.ID)
	if filepath.Clean(root) != filepath.Clean(expected) {
		t.Fatalf("root=%q expected=%q", root, expected)
	}
	task.ID = ".."
	if _, err := executor.taskRoot(task); err == nil {
		t.Fatal("unsafe task identity was accepted")
	}
}

func TestPluginDanmakuSidecarIsPreservedOnlyWithManagedName(t *testing.T) {
	manifest := downloadManifestForPluginTest("Movie.2026.mkv", "Movie.2026.danmaku-1.xml")
	selected, err := selectDownloadPackageManifest(manifest, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Files) != 2 {
		t.Fatalf("managed danmaku was not preserved: %+v", selected.Files)
	}
	manifest.Files[1].RelativePath = "unrelated.xml"
	selected, err = selectDownloadPackageManifest(manifest, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Files) != 1 {
		t.Fatalf("unrelated XML was accepted: %+v", selected.Files)
	}
}

func TestPluginDownloadExecutesDASHAndBuildsManagedManifest(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	plan, _ := validPluginDownloadPlan()
	plan.Assets[0].ExpectedBytes = 5
	plan.Assets[1].ExpectedBytes = 5
	subtitleRef, danmakuRef := uuid.NewString(), uuid.NewString()
	plan.Assets = append(plan.Assets,
		contract.DownloadAsset{ID: "subtitle", Kind: "subtitle", URLRef: subtitleRef, ExpectedContentType: "text/vtt", ExpectedBytes: 7},
		contract.DownloadAsset{ID: "danmaku", Kind: "danmaku", URLRef: danmakuRef, ExpectedContentType: "application/xml", ExpectedBytes: 4},
	)
	assets := fakePluginAssetGateway{assets: map[string]struct {
		body        []byte
		contentType string
	}{
		plan.Assets[0].URLRef: {[]byte("video"), "video/mp4"},
		plan.Assets[1].URLRef: {[]byte("audio"), "audio/mp4"},
		subtitleRef:           {[]byte("WEBVTT\n"), "text/vtt"},
		danmakuRef:            {[]byte("<d/>"), "application/xml"},
	}}
	tool := &fakeMediaTool{}
	executor := &PluginDownloadExecutor{downloads: downloads, assets: assets, tool: tool}
	task := models.DownloadTask{ID: uuid.NewString(), ProviderType: models.DownloaderTypePluginHTTP, StagingAbsolutePath: t.TempDir(), PluginConnectionID: uuid.NewString()}
	root := filepath.Join(task.StagingAbsolutePath, pluginDownloadRootName, task.ID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, source, err := executor.executePlan(context.Background(), pluginDownloadTestRuntime{}, &task, root, "org.ohmycine.fixture", plan)
	if err != nil {
		t.Fatal(err)
	}
	if tool.calls != 1 || !manifest.Complete || len(manifest.Files) != 3 || len(source.Files) != 3 {
		t.Fatalf("unexpected execution result calls=%d manifest=%+v source=%+v", tool.calls, manifest, source)
	}
	for _, file := range manifest.Files {
		if filepath.IsAbs(file.RelativePath) || strings.Contains(file.RelativePath, "/") || strings.Contains(file.RelativePath, "\\") {
			t.Fatalf("unsafe manifest path: %q", file.RelativePath)
		}
	}
}

func TestPluginProviderArtifactsAreHostGeneratedAndAddedToBothManifests(t *testing.T) {
	root := t.TempDir()
	posterRef, fanartRef := uuid.NewString(), uuid.NewString()
	jpeg := []byte{0xff, 0xd8, 0x01, 0xff, 0xd9}
	executor := &PluginDownloadExecutor{assets: fakePluginAssetGateway{assets: map[string]struct {
		body        []byte
		contentType string
	}{
		posterRef: {jpeg, "image/jpeg"},
		fanartRef: {jpeg, "image/jpeg"},
	}}}
	snapshot := contract.ProviderMetadataSnapshot{
		Version: 1, WorkID: "BV1fixture", SegmentID: "cid-123", Kind: "video", Title: "测试视频", OriginalTitle: "Fixture Video",
		Overview: "由插件提供结构化元数据，产物由 Server 生成。", Author: "UP 主", PublishedAt: "2026-08-20T00:00:00Z", DurationSeconds: 120,
		Genres: []string{"纪录片"}, Tags: []string{"测试"}, UniqueIDs: map[string]string{"bvid": "BV1fixture", "cid": "cid-123"},
		Artwork: []contract.ProviderArtwork{{Kind: "poster", AssetRef: posterRef}, {Kind: "fanart", AssetRef: fanartRef}},
	}
	manifest := downloadpkg.Manifest{Name: "Fixture", Complete: true, Files: []downloadpkg.File{{RelativePath: "Fixture.mp4", Size: 8}}}
	task := models.DownloadTask{ID: uuid.NewString(), PluginConnectionID: uuid.NewString()}
	selected, source, err := executor.attachProviderArtifacts(context.Background(), &task, root, "org.ohmycine.fixture", snapshot, manifest, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Files) != 4 || len(source.Files) != 4 {
		t.Fatalf("selected=%+v source=%+v", selected.Files, source.Files)
	}
	for _, name := range []string{"Fixture.nfo", "Fixture-poster.jpg", "Fixture-fanart.jpg"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	nfoBody, err := os.ReadFile(filepath.Join(root, "Fixture.nfo"))
	if err != nil {
		t.Fatal(err)
	}
	nfoText := string(nfoBody)
	for _, expected := range []string{"<title>测试视频</title>", `<uniqueid type="bvid" default="true">BV1fixture</uniqueid>`, `<uniqueid type="cid">cid-123</uniqueid>`, "<director>UP 主</director>"} {
		if !strings.Contains(nfoText, expected) {
			t.Fatalf("NFO missing %q: %s", expected, nfoText)
		}
	}
	// Once Host-owned artifacts exist, a retry must not depend on the plugin
	// runtime or its short-lived opaque artwork references.
	executor.assets = fakePluginAssetGateway{assets: map[string]struct {
		body        []byte
		contentType string
	}{}}
	if _, _, err := executor.attachProviderArtifacts(context.Background(), &task, root, "org.ohmycine.fixture", snapshot, manifest, manifest); err != nil {
		t.Fatalf("materialized provider artifacts were not reusable: %v", err)
	}
}

func TestProviderMetadataSnapshotSurvivesUnavailablePluginAndBindsConnection(t *testing.T) {
	downloads, _, _, _, _ := downloadFixture(t)
	source := downloadSourceEnvelope{Kind: "plugin_plan", PluginConnectionID: "connection-one", PluginItemID: "work", PluginSegmentID: "segment", PluginVersionID: "version"}
	snapshot := contract.ProviderMetadataSnapshot{Version: 1, WorkID: source.PluginItemID, SegmentID: source.PluginSegmentID, Kind: "video", Title: "Snapshot", UniqueIDs: map[string]string{"provider": "work"}}
	raw, err := json.Marshal(providerMetadataEnvelope{Version: 1, PluginID: "org.ohmycine.fixture", PluginVersion: "1.0.0", ConnectionID: source.PluginConnectionID, Snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	task := models.DownloadTask{PluginID: "org.ohmycine.fixture", PluginVersion: "1.0.0", PluginConnectionID: source.PluginConnectionID, ProviderMetadataJSON: string(raw)}
	// plugins is deliberately nil: a durable snapshot must remain usable after
	// the package is disabled, upgraded, or uninstalled.
	executor := &PluginDownloadExecutor{downloads: downloads}
	resolved, err := executor.resolveProviderMetadata(context.Background(), &task, source)
	if err != nil || resolved == nil || resolved.Title != snapshot.Title {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	otherSource := source
	otherSource.PluginConnectionID = "connection-two"
	if _, err := executor.resolveProviderMetadata(context.Background(), &task, otherSource); ErrorCode(err) != CodePluginResponseInvalid {
		t.Fatalf("cross-connection snapshot error=%v code=%q", err, ErrorCode(err))
	}
}

func TestEnsurePluginProvenanceBackfillsOnlyFullyLegacyTask(t *testing.T) {
	downloads, _, queue, actor, _ := downloadFixture(t)
	now := time.Now().UTC()
	pluginID := "org.ohmycine.legacy-fixture"
	pluginPackage := models.PluginPackage{PluginID: pluginID, Version: "1.2.3", RepositoryOwner: "example", RepositoryRepo: "plugins", RegistryCommit: strings.Repeat("a", 40), RegistryEntryJSON: `{}`, ManifestURL: "https://github.com/example/plugins/releases/download/v1/manifest.json", PackageURL: "https://github.com/example/plugins/releases/download/v1/plugin.omcp", PackageSHA256: strings.Repeat("b", 64), ExtractedTreeSHA256: strings.Repeat("c", 64), ManifestJSON: `{}`, PackagePath: filepath.Join(t.TempDir(), "package"), VerifiedAt: now, CreatedAt: now}
	if err := downloads.db.Create(&pluginPackage).Error; err != nil {
		t.Fatal(err)
	}
	installation := models.PluginInstallation{PluginID: pluginID, ActivePackageID: pluginPackage.ID, Status: models.PluginInstallationEnabled, Revision: 1, RuntimeGeneration: 1, InstalledAt: now, UpdatedAt: now, EnabledAt: &now}
	if err := downloads.db.Create(&installation).Error; err != nil {
		t.Fatal(err)
	}
	connection := models.PluginConnection{ID: uuid.NewString(), PluginID: pluginID, Name: "Legacy", ConfigJSON: `{}`, CredentialMode: models.PluginCredentialModeNone, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := downloads.db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	task := models.DownloadTask{ID: uuid.NewString(), OwnerID: actor.User.ID, ProviderType: models.DownloaderTypePluginHTTP, DisplayName: "Legacy", Phase: models.DownloadTaskStatusQueued, CreatedAt: now, UpdatedAt: now}
	_, err := queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: task.DisplayName, Payload: downloadJobPayload{DownloadTaskID: task.ID}}, func(tx *gorm.DB, job models.Job) error {
		task.JobID = job.ID
		return tx.Create(&task).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := &PluginDownloadExecutor{downloads: downloads}
	if err := executor.ensurePluginProvenance(&task, connection); err != nil {
		t.Fatal(err)
	}
	if task.PluginID != pluginID || task.PluginVersion != "1.2.3" || task.PluginConnectionID != connection.ID {
		t.Fatalf("task=%+v", task)
	}
	var persisted models.DownloadTask
	if err := downloads.db.First(&persisted, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.PluginID != pluginID || persisted.PluginVersion != "1.2.3" || persisted.PluginConnectionID != connection.ID {
		t.Fatalf("persisted=%+v", persisted)
	}
	metadataSource := downloadSourceEnvelope{Kind: "plugin_plan", PluginConnectionID: connection.ID, PluginItemID: "work", PluginSegmentID: "segment", PluginVersionID: "version"}
	metadata := contract.ProviderMetadataSnapshot{Version: 1, WorkID: "work", SegmentID: "segment", Kind: "video", Title: "Fixture", UniqueIDs: map[string]string{"fixture": "work"}}
	if err := executor.persistProviderMetadata(&task, metadataSource, metadata); err != nil {
		t.Fatal(err)
	}
	if task.ProviderMetadataJSON == "" {
		t.Fatal("durable metadata was not synchronized back to the active download task")
	}

	partial := models.DownloadTask{ID: uuid.NewString(), OwnerID: actor.User.ID, ProviderType: models.DownloaderTypePluginHTTP, PluginID: pluginID, DisplayName: "Partial", Phase: models.DownloadTaskStatusQueued, CreatedAt: now, UpdatedAt: now}
	_, err = queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: partial.DisplayName, Payload: downloadJobPayload{DownloadTaskID: partial.ID}}, func(tx *gorm.DB, job models.Job) error {
		partial.JobID = job.ID
		return tx.Create(&partial).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.ensurePluginProvenance(&partial, connection); ErrorCode(err) != CodePluginResponseInvalid {
		t.Fatalf("partial provenance error=%v code=%q", err, ErrorCode(err))
	}
}

func downloadManifestForPluginTest(video, sidecar string) downloadpkg.Manifest {
	return downloadpkg.Manifest{Name: "Movie.2026", Complete: true, Files: []downloadpkg.File{{RelativePath: video, Size: minimumAutomaticTransferVideoBytes + 1}, {RelativePath: sidecar, Size: 1024}}}
}
