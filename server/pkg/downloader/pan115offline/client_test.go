package pan115offline

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

type fakeDriver struct {
	submittedURI string
	directoryID  string
	cancelFiles  bool
	task         cloud.OfflineTask
	items        map[string]cloud.Item
	statErr      error
	submitErr    error
}

type fakeShareDriver struct {
	*fakeDriver
	children     map[string][]cloud.Item
	shareURL     string
	receiveCalls int
	receivedRoot string
	recycled     string
}

func (f *fakeShareDriver) Capabilities() cloud.Capabilities {
	return cloud.Capabilities{NativeOfflineDownload: true, ShareReceive: true, CreateDirectory: true, Move: true, Copy: true, Rename: true, Recycle: true}
}
func (f *fakeShareDriver) List(_ context.Context, parent string, request cloud.PageRequest) (cloud.Page, error) {
	items := f.children[parent]
	start := int(request.Offset)
	if start > len(items) {
		start = len(items)
	}
	end := start + int(request.Limit)
	if request.Limit <= 0 || end > len(items) {
		end = len(items)
	}
	return cloud.Page{Items: append([]cloud.Item(nil), items[start:end]...), Offset: request.Offset, HasMore: end < len(items)}, nil
}
func (f *fakeShareDriver) InspectShare(_ context.Context, raw string) (cloud.ShareSnapshot, error) {
	f.shareURL = raw
	return cloud.ShareSnapshot{ShareCode: "share-code", ReceiveCode: "abcd", Items: []cloud.ShareItem{{ID: "shared-movie", Name: "Movie.mkv"}}}, nil
}
func (f *fakeShareDriver) ReceiveShare(_ context.Context, _ cloud.ShareSnapshot, root string) error {
	f.receiveCalls++
	f.receivedRoot = root
	item := cloud.Item{ID: "received-movie", ParentID: root, Name: "Movie.mkv", Size: 1024}
	f.items[item.ID] = item
	f.children[root] = []cloud.Item{item}
	return nil
}
func (f *fakeShareDriver) CreateDirectory(_ context.Context, parent, name string) (cloud.Item, error) {
	item := cloud.Item{ID: "share-task-root", ParentID: parent, Name: name, IsDir: true}
	f.items[item.ID] = item
	f.children[parent] = append(f.children[parent], item)
	return item, nil
}
func (f *fakeShareDriver) Move(context.Context, string, string) error   { return nil }
func (f *fakeShareDriver) Copy(context.Context, string, string) error   { return nil }
func (f *fakeShareDriver) Rename(context.Context, string, string) error { return nil }
func (f *fakeShareDriver) Recycle(_ context.Context, id string) error {
	f.recycled = id
	return nil
}

func newFakeShareDriver() *fakeShareDriver {
	base := &fakeDriver{items: map[string]cloud.Item{
		"root":   {ID: "root", ParentID: "0", Name: "root", IsDir: true},
		"intake": {ID: "intake", ParentID: "root", Name: "intake", IsDir: true},
	}}
	return &fakeShareDriver{fakeDriver: base, children: map[string][]cloud.Item{}}
}

func (f *fakeDriver) Provider() string { return cloud.ProviderPan115 }
func (f *fakeDriver) Capabilities() cloud.Capabilities {
	return cloud.Capabilities{NativeOfflineDownload: true}
}
func (f *fakeDriver) Probe(context.Context) (cloud.Account, error) { return cloud.Account{}, nil }
func (f *fakeDriver) List(context.Context, string, cloud.PageRequest) (cloud.Page, error) {
	return cloud.Page{}, nil
}
func (f *fakeDriver) Stat(_ context.Context, id string) (cloud.Item, error) {
	if f.statErr != nil {
		return cloud.Item{}, f.statErr
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return cloud.Item{ID: id, ParentID: "root", IsDir: true}, nil
}
func (f *fakeDriver) DirectURL(context.Context, cloud.DirectURLRequest) (cloud.TemporaryURL, error) {
	return cloud.TemporaryURL{Headers: http.Header{}, ExpiresAt: time.Now()}, nil
}
func (f *fakeDriver) SubmitOffline(_ context.Context, uri, directoryID string) (cloud.OfflineTask, error) {
	f.submittedURI, f.directoryID = uri, directoryID
	return f.task, f.submitErr
}

func TestNativeOfflineRejectsUnavailableTargetBeforeProviderSubmission(t *testing.T) {
	driver := &fakeDriver{statErr: cloud.Error(cloud.CodeNotFound, false, errors.New("missing"))}
	client, err := New(downloader.Config{CloudDriver: driver, ProviderStorageRootID: "root", ProviderDirectoryID: "moved-directory"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}})
	code, retryable := downloader.ErrorInfo(err)
	if code != "downloader_storage_unavailable" || retryable || driver.submittedURI != "" {
		t.Fatalf("code=%q retryable=%v submitted=%q err=%v", code, retryable, driver.submittedURI, err)
	}
}

func TestNativeOfflineRejectsTargetMovedOutsideStorageRoot(t *testing.T) {
	driver := &fakeDriver{items: map[string]cloud.Item{
		"target": {ID: "target", ParentID: "other-root", IsDir: true},
	}}
	client, err := New(downloader.Config{CloudDriver: driver, ProviderStorageRootID: "selected-root", ProviderDirectoryID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}})
	code, retryable := downloader.ErrorInfo(err)
	if code != "downloader_storage_unavailable" || retryable || driver.submittedURI != "" {
		t.Fatalf("code=%q retryable=%v submitted=%q err=%v", code, retryable, driver.submittedURI, err)
	}
}

func TestNativeOfflineAcceptsTargetBelowAccountRoot(t *testing.T) {
	driver := &fakeDriver{items: map[string]cloud.Item{
		"target": {ID: "target", ParentID: "0", IsDir: true},
	}}
	client, err := New(downloader.Config{CloudDriver: driver, ProviderStorageRootID: "0", ProviderDirectoryID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.Test(context.Background())
	if err != nil || health.Version != "115 原生离线" {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	_, err = client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}})
	if err != nil || driver.submittedURI == "" || driver.directoryID != "target" {
		t.Fatalf("submit err=%v uri=%q directory=%q", err, driver.submittedURI, driver.directoryID)
	}
}

func TestNativeOfflineAcceptsAccountRootAsTarget(t *testing.T) {
	driver := &fakeDriver{}
	client, err := New(downloader.Config{CloudDriver: driver, ProviderStorageRootID: "0", ProviderDirectoryID: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err = client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}})
	if err != nil || driver.directoryID != "0" {
		t.Fatalf("submit err=%v directory=%q", err, driver.directoryID)
	}
}

func TestNativeOfflinePreservesProviderErrorClassification(t *testing.T) {
	driver := &fakeDriver{submitErr: cloud.Error(cloud.CodeResponseInvalid, false, errors.New("malformed provider response"))}
	client, err := New(downloader.Config{CloudDriver: driver, ProviderStorageRootID: "root", ProviderDirectoryID: "target-id"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}})
	code, retryable := downloader.ErrorInfo(err)
	if code != "downloader_response_invalid" || retryable {
		t.Fatalf("code=%q retryable=%v err=%v", code, retryable, err)
	}
}

func TestNativeOfflineMapsPermanentProviderSubmissionErrors(t *testing.T) {
	tests := []struct {
		providerCode string
		wantCode     string
	}{
		{providerCode: cloud.CodeOfflineNoQuota, wantCode: "downloader_quota_exhausted"},
		{providerCode: cloud.CodeOfflineBadLink, wantCode: "downloader_source_invalid"},
		{providerCode: cloud.CodeOfflineTaskExists, wantCode: "downloader_task_exists"},
	}
	for _, item := range tests {
		driver := &fakeDriver{submitErr: cloud.Error(item.providerCode, false, errors.New("provider rejected submission"))}
		client, err := New(downloader.Config{CloudDriver: driver, ProviderStorageRootID: "root", ProviderDirectoryID: "target-id"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}})
		code, retryable := downloader.ErrorInfo(err)
		if code != item.wantCode || retryable {
			t.Fatalf("provider code %q mapped to %q, %t", item.providerCode, code, retryable)
		}
	}
}

func (f *fakeDriver) GetOffline(context.Context, string) (cloud.OfflineTask, error) {
	return f.task, nil
}
func (f *fakeDriver) CancelOffline(_ context.Context, _ string, deleteFiles bool) error {
	f.cancelFiles = deleteFiles
	return nil
}

func TestNativeOfflineMapsProviderStorageTask(t *testing.T) {
	if Capabilities.Seeding || Capabilities.Pause || Capabilities.Resume || Capabilities.UploadSpeed {
		t.Fatalf("115 native offline downloader must not expose seeding controls: %+v", Capabilities)
	}
	if !Capabilities.NativeOffline || Capabilities.OutputConstraint != downloader.OutputConstraintProviderStorage {
		t.Fatalf("unexpected native offline capabilities: %+v", Capabilities)
	}
	progress, total := .25, int64(400)
	driver := &fakeDriver{task: cloud.OfflineTask{ID: "hash", Name: "Movie", Status: "downloading", Progress: &progress, BytesTotal: &total, OutputItemID: "file-id"}}
	client, err := New(downloader.Config{CloudDriver: driver, ProviderStorageRootID: "root", ProviderDirectoryID: "target-id"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceURL, URL: "magnet:?xt=urn:btih:abc"}})
	if err != nil {
		t.Fatal(err)
	}
	if driver.directoryID != "target-id" || task.ID != "hash" || task.OutputItemID != "file-id" || task.BytesCompleted == nil || *task.BytesCompleted != 100 {
		t.Fatalf("unexpected task mapping: %#v", task)
	}
	if err := client.Cancel(context.Background(), task.ID, true); err != nil || !driver.cancelFiles {
		t.Fatalf("cancel delete_data was not forwarded: %v", err)
	}
}

func TestShareSubmissionUsesStableTaskDirectoryAndReconcilesRetry(t *testing.T) {
	driver := newFakeShareDriver()
	client, err := New(downloader.Config{CloudDriver: driver, ProviderStorageRootID: "root", ProviderDirectoryID: "intake"})
	if err != nil {
		t.Fatal(err)
	}
	request := downloader.SubmitRequest{
		Source:              downloader.Source{Kind: downloader.SourcePan115Share, URL: "https://115.com/s/share-code?password=abcd"},
		Tag:                 "omc-task-id",
		ProviderDirectoryID: "intake",
	}
	first, err := client.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "share:share-task-root" || !first.Completed || driver.receiveCalls != 1 || driver.receivedRoot != "share-task-root" || driver.shareURL != request.Source.URL {
		t.Fatalf("task=%+v receive_calls=%d root=%q url=%q", first, driver.receiveCalls, driver.receivedRoot, driver.shareURL)
	}
	manifest, err := client.(downloader.ManifestClient).Manifest(context.Background(), first.ID)
	if err != nil || !manifest.Complete || len(manifest.Files) != 1 || manifest.Files[0].ProviderItemID != "received-movie" {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}

	second, err := client.Submit(context.Background(), request)
	if err != nil || second.ID != first.ID || driver.receiveCalls != 1 {
		t.Fatalf("retry task=%+v receive_calls=%d err=%v", second, driver.receiveCalls, err)
	}
}

func TestProviderItemAdoptionRevalidatesIdentityAndDestructiveCancel(t *testing.T) {
	driver := newFakeShareDriver()
	manual := cloud.Item{ID: "manual", ParentID: "intake", Name: "Seven.Samurai.1954", IsDir: true}
	driver.items[manual.ID] = manual
	driver.children["intake"] = []cloud.Item{manual}
	client, err := New(downloader.Config{CloudDriver: driver, ProviderStorageRootID: "root", ProviderDirectoryID: "intake"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceProviderItem, ProviderItemID: "manual"}, ProviderDirectoryID: "intake"})
	if err != nil || task.ID != "ingest:manual" || !task.Completed {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	if err := client.Cancel(context.Background(), task.ID, true); err != nil || driver.recycled != "manual" {
		t.Fatalf("cancel err=%v recycled=%q", err, driver.recycled)
	}

	driver.items["manual"] = cloud.Item{ID: "different", ParentID: "intake", Name: "Seven.Samurai.1954", IsDir: true}
	_, err = client.Submit(context.Background(), downloader.SubmitRequest{Source: downloader.Source{Kind: downloader.SourceProviderItem, ProviderItemID: "manual"}, ProviderDirectoryID: "intake"})
	if code, retryable := downloader.ErrorInfo(err); code != "downloader_response_invalid" || retryable {
		t.Fatalf("identity mismatch code=%q retryable=%t err=%v", code, retryable, err)
	}
}
