package pan115

import (
	"context"
	"errors"
	"testing"
	"time"

	pan115sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"golang.org/x/time/rate"
)

const testOfflineHash = "0123456789abcdef0123456789abcdef01234567"

type offlineRecoverySDK struct {
	*bulkSDK
	addHashes []string
	addErr    error
	tasks     []*pan115sdk.OfflineTask
	pageTasks map[int64][]*pan115sdk.OfflineTask
	pageCount int64
	addCalls  int
	listCalls int
}

func (s *offlineRecoverySDK) AddOfflineTaskURIs([]string, string, ...pan115sdk.OfflineOption) ([]string, error) {
	s.addCalls++
	return append([]string(nil), s.addHashes...), s.addErr
}

func (s *offlineRecoverySDK) ListOfflineTask(page int64) (pan115sdk.OfflineTaskResp, error) {
	s.listCalls++
	tasks := s.tasks
	if s.pageTasks != nil {
		tasks = s.pageTasks[page]
	}
	pageCount := s.pageCount
	if pageCount < 1 {
		pageCount = 1
	}
	return pan115sdk.OfflineTaskResp{Page: page, PageCount: pageCount, Tasks: tasks}, nil
}

func newOfflineTestClient(sdk sdkClient) *Client {
	return &Client{
		sdk:         sdk,
		offlineRate: rate.NewLimiter(rate.Inf, 1),
		callSlots:   make(chan struct{}, maxInFlightCalls),
		now:         time.Now,
		jitter:      func() time.Duration { return 0 },
	}
}

func TestNewUses115BrowserUserAgentForCookieAPIs(t *testing.T) {
	driver, err := New(cloud.Config{Cookie: "UID=1; CID=cid; SEID=seid"})
	if err != nil {
		t.Fatal(err)
	}
	client := driver.(*Client)
	adapter := client.sdk.(*sdkAdapter)
	if got := adapter.Client.Header.Get("User-Agent"); got != pan115sdk.UA115Browser {
		t.Fatalf("User-Agent = %q, want %q", got, pan115sdk.UA115Browser)
	}
}

func TestSubmitOfflineUsesReturnedIdentityWithoutRecovery(t *testing.T) {
	sdk := &offlineRecoverySDK{bulkSDK: &bulkSDK{}, addHashes: []string{testOfflineHash}}
	task, err := newOfflineTestClient(sdk).SubmitOffline(context.Background(), "magnet:?xt=urn:btih:"+testOfflineHash, "root")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != testOfflineHash || sdk.addCalls != 1 || sdk.listCalls != 0 {
		t.Fatalf("task=%+v add=%d list=%d", task, sdk.addCalls, sdk.listCalls)
	}
}

func TestSubmitOfflineRecoversExistingMagnetAfterAmbiguousAddFailure(t *testing.T) {
	sdk := &offlineRecoverySDK{
		bulkSDK: &bulkSDK{},
		addErr:  errors.New("upstream response could not be decoded"),
		tasks:   []*pan115sdk.OfflineTask{{InfoHash: testOfflineHash, Status: 2, DelFileId: "output"}},
	}
	task, err := newOfflineTestClient(sdk).SubmitOffline(context.Background(), "magnet:?XT=URN:BTIH:"+testOfflineHash, "root")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != testOfflineHash || !task.Completed || task.OutputItemID != "output" || sdk.addCalls != 1 || sdk.listCalls != 1 {
		t.Fatalf("task=%+v add=%d list=%d", task, sdk.addCalls, sdk.listCalls)
	}
}

func TestSubmitOfflineRecoversProviderDuplicateBeyondLegacyFivePageWindow(t *testing.T) {
	sdk := &offlineRecoverySDK{
		bulkSDK:   &bulkSDK{},
		addErr:    pan115sdk.ErrOfflineTaskExisted,
		pageCount: 8,
		pageTasks: map[int64][]*pan115sdk.OfflineTask{8: {{InfoHash: testOfflineHash, Status: 0}}},
	}
	task, err := newOfflineTestClient(sdk).SubmitOffline(context.Background(), "magnet:?xt=urn:btih:"+testOfflineHash, "root")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != testOfflineHash || task.Status != "queued" || sdk.addCalls != 1 || sdk.listCalls != 8 {
		t.Fatalf("task=%+v add=%d list=%d", task, sdk.addCalls, sdk.listCalls)
	}
}

func TestSubmitOfflineReturnsStableConflictWhenDuplicateCannotBeAdopted(t *testing.T) {
	sdk := &offlineRecoverySDK{bulkSDK: &bulkSDK{}, addErr: pan115sdk.ErrOfflineTaskExisted}
	_, err := newOfflineTestClient(sdk).SubmitOffline(context.Background(), "magnet:?xt=urn:btih:"+testOfflineHash, "root")
	if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeOfflineTaskExists || retryable {
		t.Fatalf("error code=%q retryable=%t err=%v", code, retryable, err)
	}
	if sdk.addCalls != 1 || sdk.listCalls != 1 {
		t.Fatalf("add=%d list=%d", sdk.addCalls, sdk.listCalls)
	}
}

func TestGetOfflineFallsBackWhenCachedTaskPageHasShifted(t *testing.T) {
	sdk := &offlineRecoverySDK{
		bulkSDK:   &bulkSDK{},
		pageCount: 3,
		pageTasks: map[int64][]*pan115sdk.OfflineTask{3: {{InfoHash: testOfflineHash, Status: 2, DelFileId: "output"}}},
	}
	client := newOfflineTestClient(sdk)
	client.offlinePages.Store(testOfflineHash, int64(2))
	task, err := client.GetOffline(context.Background(), testOfflineHash)
	if err != nil {
		t.Fatal(err)
	}
	if !task.Completed || task.OutputItemID != "output" || sdk.listCalls != 3 {
		t.Fatalf("task=%+v list=%d", task, sdk.listCalls)
	}
}

func TestSubmitOfflinePreservesFailureWhenNoExistingTaskCanBeRecovered(t *testing.T) {
	sdk := &offlineRecoverySDK{bulkSDK: &bulkSDK{}, addErr: errors.New("offline unavailable")}
	_, err := newOfflineTestClient(sdk).SubmitOffline(context.Background(), "https://example.test/file", "root")
	if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeUnavailable || !retryable {
		t.Fatalf("error code=%q retryable=%t err=%v", code, retryable, err)
	}
	if sdk.listCalls != 0 {
		t.Fatalf("non-magnet recovery listed %d pages", sdk.listCalls)
	}
}

func TestOfflineInfoHashAcceptsHexAndBase32(t *testing.T) {
	for _, uri := range []string{
		"magnet:?xt=urn:btih:" + testOfflineHash,
		"MAGNET:?XT=urn:btih:AERUKZ4JVPG66AJDIVTYTK6N54ASGRLH",
	} {
		if got, ok := offlineInfoHash(uri); !ok || got != testOfflineHash {
			t.Fatalf("offlineInfoHash(%q) = %q, %t", uri, got, ok)
		}
	}
	for _, uri := range []string{"https://example.test/file", "magnet:?dn=missing", "magnet:?xt=urn:btih:not-a-hash"} {
		if got, ok := offlineInfoHash(uri); ok || got != "" {
			t.Fatalf("offlineInfoHash(%q) = %q, %t", uri, got, ok)
		}
	}
}

func TestMapOfflineTaskUsesCurrentProviderStatusSemantics(t *testing.T) {
	tests := []struct {
		providerStatus int
		status         string
		completed      bool
		failed         bool
	}{
		{providerStatus: 0, status: "queued"},
		{providerStatus: 1, status: "downloading"},
		{providerStatus: 2, status: "completed", completed: true},
		{providerStatus: 3, status: "queued"},
		{providerStatus: -1, status: "failed", failed: true},
	}
	for _, item := range tests {
		task := mapOfflineTask(&pan115sdk.OfflineTask{InfoHash: testOfflineHash, Status: item.providerStatus})
		if task.Status != item.status || task.Completed != item.completed || task.Failed != item.failed || task.ProviderStatus != item.providerStatus {
			t.Fatalf("provider status %d mapped to %+v", item.providerStatus, task)
		}
	}
}

func TestMapOfflineSubmissionErrorsKeepsPermanentFailuresOutOfRetryQueue(t *testing.T) {
	tests := []struct {
		err  error
		code string
	}{
		{err: pan115sdk.ErrOfflineNoTimes, code: cloud.CodeOfflineNoQuota},
		{err: pan115sdk.ErrOfflineInvalidLink, code: cloud.CodeOfflineBadLink},
	}
	for _, item := range tests {
		code, retryable := cloud.ErrorInfo(mapError(item.err))
		if code != item.code || retryable {
			t.Fatalf("mapError(%v) = %q, %t", item.err, code, retryable)
		}
	}
}
