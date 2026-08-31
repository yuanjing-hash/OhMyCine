package pan115

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	pan115sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"golang.org/x/time/rate"
)

func TestInteractiveReadDoesNotWaitBehindBackgroundReservation(t *testing.T) {
	sdk := &bulkSDK{}
	unlimited := rate.NewLimiter(rate.Inf, 1)
	client := &Client{
		sdk: sdk, listRate: unlimited, interactiveRate: rate.NewLimiter(rate.Inf, 1), pipelineRate: rate.NewLimiter(rate.Inf, 1),
		callSlots: make(chan struct{}, maxInFlightCalls), backgroundRead: make(chan struct{}, 1), now: time.Now, jitter: func() time.Duration { return 0 },
	}
	client.backgroundRead <- struct{}{}
	interactiveCtx, cancel := context.WithTimeout(cloud.WithReadClass(context.Background(), cloud.ReadClassInteractive), time.Second)
	defer cancel()
	if _, err := client.List(interactiveCtx, "0", cloud.PageRequest{Limit: 1}); err != nil {
		t.Fatalf("interactive read waited behind background reservation: %v", err)
	}
	backgroundCtx, cancelBackground := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelBackground()
	if _, err := client.List(backgroundCtx, "0", cloud.PageRequest{Limit: 1}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("background read bypassed reservation: %v", err)
	}
	<-client.backgroundRead
}

type bulkSDK struct {
	mu            sync.Mutex
	getFileCalls  int
	listPageCalls int
	fileCalls     int
	folderCalls   int
	files         []pan115sdk.File
	folders       []bulkFolder
}

func (s *bulkSDK) CookieCheck() error                    { return nil }
func (s *bulkSDK) GetUser() (*pan115sdk.UserInfo, error) { return &pan115sdk.UserInfo{}, nil }
func (s *bulkSDK) GetInfo() (pan115sdk.InfoData, error)  { return pan115sdk.InfoData{}, nil }
func (s *bulkSDK) DownloadWithUA(string, string) (*pan115sdk.DownloadInfo, error) {
	return &pan115sdk.DownloadInfo{}, nil
}
func (s *bulkSDK) AddOfflineTaskURIs([]string, string, ...pan115sdk.OfflineOption) ([]string, error) {
	return nil, nil
}
func (s *bulkSDK) ListOfflineTask(int64) (pan115sdk.OfflineTaskResp, error) {
	return pan115sdk.OfflineTaskResp{}, nil
}
func (s *bulkSDK) DeleteOfflineTasks([]string, bool) error { return nil }
func (s *bulkSDK) ListPage(string, int64, int64, ...pan115sdk.ListOption) (*[]pan115sdk.File, error) {
	s.mu.Lock()
	s.listPageCalls++
	s.mu.Unlock()
	items := []pan115sdk.File{}
	return &items, nil
}
func (s *bulkSDK) GetFile(id string) (*pan115sdk.File, error) {
	s.mu.Lock()
	s.getFileCalls++
	s.mu.Unlock()
	return &pan115sdk.File{FileID: id, ParentID: "0", Name: "媒体", IsDirectory: true, PickCode: "folder-pickcode"}, nil
}
func (s *bulkSDK) ListTreeFiles(string, int64, int64) ([]pan115sdk.File, int64, error) {
	s.mu.Lock()
	s.fileCalls++
	s.mu.Unlock()
	return append([]pan115sdk.File(nil), s.files...), int64(len(s.files)), nil
}
func (s *bulkSDK) ListTreeFolders(string, int64, int64) ([]bulkFolder, bool, error) {
	s.mu.Lock()
	s.folderCalls++
	s.mu.Unlock()
	return append([]bulkFolder(nil), s.folders...), false, nil
}
func (s *bulkSDK) ListLifeEvents(int) (lifeEventBatch, error) {
	return lifeEventBatch{}, nil
}

func TestListTreeUsesRecursiveBulkStreamsAndBuildsPaths(t *testing.T) {
	modified := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	sdk := &bulkSDK{
		folders: []bulkFolder{{ID: "movies", ParentID: "root", Name: "电影"}},
		files: []pan115sdk.File{
			{FileID: "one", ParentID: "movies", Name: "One.2026.mkv", Size: 10, UpdateTime: modified},
			{FileID: "two", ParentID: "root", Name: "Two.2026.mp4", Size: 20, UpdateTime: modified},
		},
	}
	client := &Client{
		sdk: sdk, listRate: rate.NewLimiter(rate.Inf, 1), bulkRate: rate.NewLimiter(rate.Inf, 1),
		callSlots: make(chan struct{}, maxInFlightCalls), now: time.Now, jitter: func() time.Duration { return 0 },
	}
	result, err := client.ListTree(context.Background(), "root", 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Partial || len(result.Entries) != 2 {
		t.Fatalf("result=%+v", result)
	}
	paths := map[string]string{}
	for _, entry := range result.Entries {
		paths[entry.ID] = entry.RelativePath
	}
	if paths["one"] != "/电影/One.2026.mkv" || paths["two"] != "/Two.2026.mp4" {
		t.Fatalf("paths=%+v", paths)
	}
	sdk.mu.Lock()
	defer sdk.mu.Unlock()
	if sdk.getFileCalls != 1 || sdk.fileCalls != 1 || sdk.folderCalls != 1 || sdk.listPageCalls != 0 {
		t.Fatalf("get=%d files=%d folders=%d list=%d", sdk.getFileCalls, sdk.fileCalls, sdk.folderCalls, sdk.listPageCalls)
	}
}

func TestListTreeCapsProviderResultsAsPartial(t *testing.T) {
	sdk := &bulkSDK{files: []pan115sdk.File{
		{FileID: "one", ParentID: "root", Name: "One.mkv"},
		{FileID: "two", ParentID: "root", Name: "Two.mkv"},
	}}
	client := &Client{
		sdk: sdk, listRate: rate.NewLimiter(rate.Inf, 1), bulkRate: rate.NewLimiter(rate.Inf, 1),
		callSlots: make(chan struct{}, maxInFlightCalls), now: time.Now, jitter: func() time.Duration { return 0 },
	}
	result, err := client.ListTree(context.Background(), "root", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Partial || len(result.Entries) != 1 {
		t.Fatalf("result=%+v", result)
	}
}
