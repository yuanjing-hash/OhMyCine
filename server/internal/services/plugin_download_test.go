package services

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

type fakePluginAssetGateway struct {
	assets map[string]struct {
		body        []byte
		contentType string
	}
}

func (g fakePluginAssetGateway) OpenAssetForPlugin(_ context.Context, pluginID, ref, method, rangeHeader string) (*hostapi.AssetStream, error) {
	asset, ok := g.assets[ref]
	if !ok || pluginID == "" || method != http.MethodGet || rangeHeader != "" {
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
	task := models.DownloadTask{ID: uuid.NewString(), ProviderType: models.DownloaderTypePluginHTTP, StagingAbsolutePath: t.TempDir()}
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
		if filepath.IsAbs(file.RelativePath) || !strings.HasPrefix(file.RelativePath, pluginDownloadRootName+"/") {
			t.Fatalf("unsafe manifest path: %q", file.RelativePath)
		}
	}
}

func downloadManifestForPluginTest(video, sidecar string) downloadpkg.Manifest {
	return downloadpkg.Manifest{Name: "Movie.2026", Complete: true, Files: []downloadpkg.File{{RelativePath: video, Size: minimumAutomaticTransferVideoBytes + 1}, {RelativePath: sidecar, Size: 1024}}}
}
