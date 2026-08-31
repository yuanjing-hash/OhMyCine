package pan115

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	pan115sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"golang.org/x/time/rate"
)

type mutationTestSDK struct {
	*bulkSDK
	mkdirParent     string
	mkdirName       string
	moveParent      string
	moveItems       []string
	copyParent      string
	copyItems       []string
	renameID        string
	renameName      string
	deleted         []string
	cleanedPassword string
	cleanedIDs      []string
	recycleItems    []pan115sdk.RecycleBinItem
	cleanErr        error
	err             error
}

func (s *mutationTestSDK) Mkdir(parentID, name string) (string, error) {
	s.mkdirParent, s.mkdirName = parentID, name
	return "created-directory", s.err
}
func (s *mutationTestSDK) Move(parentID string, itemIDs ...string) error {
	s.moveParent, s.moveItems = parentID, append([]string(nil), itemIDs...)
	return s.err
}
func (s *mutationTestSDK) Copy(parentID string, itemIDs ...string) error {
	s.copyParent, s.copyItems = parentID, append([]string(nil), itemIDs...)
	return s.err
}
func (s *mutationTestSDK) Rename(itemID, name string) error {
	s.renameID, s.renameName = itemID, name
	return s.err
}
func (s *mutationTestSDK) Delete(itemIDs ...string) error {
	s.deleted = append([]string(nil), itemIDs...)
	return s.err
}
func (s *mutationTestSDK) CleanRecycleBin(password string, itemIDs ...string) error {
	s.cleanedPassword, s.cleanedIDs = password, append([]string(nil), itemIDs...)
	return s.cleanErr
}
func (s *mutationTestSDK) ListRecycleBin(_, _ int) ([]pan115sdk.RecycleBinItem, error) {
	return append([]pan115sdk.RecycleBinItem(nil), s.recycleItems...), nil
}

func newMutationTestClient(sdk sdkClient) *Client {
	unlimited := func() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) }
	return &Client{sdk: sdk, mkdirRate: unlimited(), uploadRate: unlimited(), moveRate: unlimited(), copyRate: unlimited(), renameRate: unlimited(), recycleRate: unlimited(), purgeRate: unlimited(), callSlots: make(chan struct{}, maxInFlightCalls), now: time.Now, jitter: func() time.Duration { return 0 }}
}

func TestMutationAdapterUsesProviderArgumentOrderAndIdentities(t *testing.T) {
	sdk := &mutationTestSDK{bulkSDK: &bulkSDK{}}
	client := newMutationTestClient(sdk)
	capabilities := client.Capabilities()
	if !capabilities.CreateDirectory || !capabilities.Move || !capabilities.Copy || !capabilities.Rename || !capabilities.Recycle {
		t.Fatalf("capabilities=%+v", capabilities)
	}
	directory, err := client.CreateDirectory(context.Background(), "parent", "电影")
	if err != nil || directory.ID != "created-directory" || directory.ParentID != "parent" || directory.Name != "电影" || !directory.IsDir {
		t.Fatalf("directory=%+v err=%v", directory, err)
	}
	if err := client.Move(context.Background(), "move-item", "move-parent"); err != nil {
		t.Fatal(err)
	}
	if err := client.Copy(context.Background(), "copy-item", "copy-parent"); err != nil {
		t.Fatal(err)
	}
	if err := client.Rename(context.Background(), "rename-item", "新名字.mkv"); err != nil {
		t.Fatal(err)
	}
	if err := client.Recycle(context.Background(), "delete-item"); err != nil {
		t.Fatal(err)
	}
	if sdk.mkdirParent != "parent" || sdk.mkdirName != "电影" || sdk.moveParent != "move-parent" || !reflect.DeepEqual(sdk.moveItems, []string{"move-item"}) || sdk.copyParent != "copy-parent" || !reflect.DeepEqual(sdk.copyItems, []string{"copy-item"}) || sdk.renameID != "rename-item" || sdk.renameName != "新名字.mkv" || !reflect.DeepEqual(sdk.deleted, []string{"delete-item"}) {
		t.Fatalf("sdk calls=%+v", sdk)
	}
}

func TestMutationAdapterBatchesBoundedOpaqueIdentities(t *testing.T) {
	sdk := &mutationTestSDK{bulkSDK: &bulkSDK{}}
	client := newMutationTestClient(sdk)
	if err := client.MoveMany(context.Background(), []string{"move-1", "move-2"}, "move-parent"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sdk.moveItems, []string{"move-1", "move-2"}) {
		t.Fatalf("move items=%v", sdk.moveItems)
	}
	if err := client.CopyMany(context.Background(), []string{"copy-1", "copy-2"}, "copy-parent"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sdk.copyItems, []string{"copy-1", "copy-2"}) {
		t.Fatalf("copy items=%v", sdk.copyItems)
	}
	if err := client.RecycleMany(context.Background(), []string{"delete-1", "delete-2"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sdk.deleted, []string{"delete-1", "delete-2"}) {
		t.Fatalf("deleted=%v", sdk.deleted)
	}
	oversized := make([]string, cloud.MaxBatchMutationItems+1)
	for index := range oversized {
		oversized[index] = "item-" + strconv.Itoa(index)
	}
	if err := client.MoveMany(context.Background(), oversized, "parent"); err == nil {
		t.Fatal("oversized mutation was accepted")
	}
	if err := client.RecycleMany(context.Background(), []string{"same", "same"}); err == nil {
		t.Fatal("duplicate identities were accepted")
	}
	maximum := make([]string, cloud.MaxBatchMutationItems)
	for index := range maximum {
		maximum[index] = "maximum-" + strconv.Itoa(index)
	}
	if err := client.MoveMany(context.Background(), maximum, "maximum-parent"); err != nil {
		t.Fatalf("maximum bounded batch was rejected: %v", err)
	}
}

func TestBatchMutationAdapterDoesNotStartAfterContextCancellation(t *testing.T) {
	sdk := &mutationTestSDK{bulkSDK: &bulkSDK{}}
	client := newMutationTestClient(sdk)
	timings := cloud.NewOperationTimingCollector()
	ctx, cancel := context.WithCancel(context.Background())
	ctx = cloud.WithOperationTimingCollector(ctx, timings)
	cancel()
	if err := client.MoveMany(ctx, []string{"item-1", "item-2"}, "parent"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v want context cancellation", err)
	}
	if len(sdk.moveItems) != 0 {
		t.Fatalf("cancelled mutation reached provider: %v", sdk.moveItems)
	}
	if got := timings.Snapshot(); got.ProviderWaitCalls != 1 || got.ProviderCallCalls != 0 {
		t.Fatalf("cancelled timing=%+v", got)
	}
}

func TestBatchMutationAdapterReportsRequestScopedWaitAndCall(t *testing.T) {
	sdk := &mutationTestSDK{bulkSDK: &bulkSDK{}}
	client := newMutationTestClient(sdk)
	timings := cloud.NewOperationTimingCollector()
	ctx := cloud.WithOperationTimingCollector(context.Background(), timings)
	if err := client.MoveMany(ctx, []string{"item-1", "item-2"}, "parent"); err != nil {
		t.Fatal(err)
	}
	got := timings.Snapshot()
	if got.ProviderWaitCalls != 1 || got.ProviderCallCalls != 1 || got.TargetListCalls != 0 || got.BatchMutationCalls != 0 || got.DBCheckpointCalls != 0 {
		t.Fatalf("request-scoped timing=%+v", got)
	}
}

func TestNewClientUsesIndependentMutationLanes(t *testing.T) {
	driver, err := New(cloud.Config{Cookie: "UID=1; CID=cid; SEID=seid"})
	if err != nil {
		t.Fatal(err)
	}
	client := driver.(*Client)
	lanes := []*rate.Limiter{client.mkdirRate, client.uploadRate, client.moveRate, client.copyRate, client.renameRate, client.recycleRate, client.purgeRate}
	seen := map[*rate.Limiter]struct{}{}
	for _, lane := range lanes {
		if lane == nil {
			t.Fatal("nil operation limiter")
		}
		if _, duplicate := seen[lane]; duplicate {
			t.Fatal("unrelated 115 operations share one limiter")
		}
		seen[lane] = struct{}{}
	}
	if client.mkdirRate.Limit() != rate.Inf {
		t.Fatalf("healthy mkdir has fixed pacing: %v", client.mkdirRate.Limit())
	}
	if client.moveRate.Limit() == rate.Inf || client.renameRate.Limit() == rate.Inf || client.recycleRate.Limit() == rate.Inf {
		t.Fatal("destructive mutation lanes lost conservative pacing")
	}
}

func TestMutationAdapterRejectsUnsafeNamesWithoutCallingProvider(t *testing.T) {
	sdk := &mutationTestSDK{bulkSDK: &bulkSDK{}}
	client := newMutationTestClient(sdk)
	for _, name := range []string{"", ".", "..", "folder/name", "line\nbreak"} {
		if _, err := client.CreateDirectory(context.Background(), "parent", name); err == nil {
			t.Fatalf("CreateDirectory accepted %q", name)
		}
		if err := client.Rename(context.Background(), "item", name); err == nil {
			t.Fatalf("Rename accepted %q", name)
		}
	}
	if sdk.mkdirParent != "" || sdk.renameID != "" {
		t.Fatalf("unsafe request reached SDK: %+v", sdk)
	}
}

func TestMutationAdapterMapsRiskFailuresAndUnknownCreateResult(t *testing.T) {
	riskSDK := &mutationTestSDK{bulkSDK: &bulkSDK{}, err: errors.New("HTTP 429 rate limited")}
	err := newMutationTestClient(riskSDK).Move(context.Background(), "item", "parent")
	if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeRateLimited || !retryable {
		t.Fatalf("risk error code=%q retryable=%t err=%v", code, retryable, err)
	}
	unknownSDK := &mutationTestSDK{bulkSDK: &bulkSDK{}}
	empty := &emptyMkdirSDK{mutationTestSDK: unknownSDK}
	_, err = newMutationTestClient(empty).CreateDirectory(context.Background(), "parent", "folder")
	if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeMutationUnknown || !retryable {
		t.Fatalf("unknown result code=%q retryable=%t err=%v", code, retryable, err)
	}
}

func TestBatchMutationAdapterMapsRiskFailureOncePerRequest(t *testing.T) {
	riskSDK := &mutationTestSDK{bulkSDK: &bulkSDK{}, err: errors.New("HTTP 405 risk control")}
	client := newMutationTestClient(riskSDK)
	err := client.MoveMany(context.Background(), []string{"item-1", "item-2", "item-3"}, "parent")
	if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeRateLimited || !retryable {
		t.Fatalf("risk error code=%q retryable=%t err=%v", code, retryable, err)
	}
	if !reflect.DeepEqual(riskSDK.moveItems, []string{"item-1", "item-2", "item-3"}) {
		t.Fatalf("batch request was split per item: %v", riskSDK.moveItems)
	}
}

func TestMutationAdapterPurgesOnlyExactOwnedRecycleItem(t *testing.T) {
	sdk := &mutationTestSDK{bulkSDK: &bulkSDK{}}
	client := newMutationTestClient(sdk)
	client.recyclePassword = "safe-code"
	if err := client.PurgeRecycle(context.Background(), "owned-item"); err != nil {
		t.Fatal(err)
	}
	if sdk.cleanedPassword != "safe-code" || !reflect.DeepEqual(sdk.cleanedIDs, []string{"owned-item"}) {
		t.Fatalf("cleanup call password=%q ids=%v", sdk.cleanedPassword, sdk.cleanedIDs)
	}
	if err := client.PurgeRecycle(context.Background(), ""); err == nil {
		t.Fatal("empty recycle identity was accepted")
	}
	if len(sdk.cleanedIDs) != 1 {
		t.Fatalf("unsafe empty cleanup reached provider: %v", sdk.cleanedIDs)
	}
}

func TestMutationAdapterClearsWholeRecycleBinWithoutItemIDs(t *testing.T) {
	sdk := &mutationTestSDK{}
	client := newMutationTestClient(sdk)
	client.recyclePassword = "safe-code"
	if err := client.ClearRecycleBin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sdk.cleanedPassword != "safe-code" || len(sdk.cleanedIDs) != 0 {
		t.Fatalf("full cleanup call password=%q ids=%v", sdk.cleanedPassword, sdk.cleanedIDs)
	}
}

func TestMutationAdapterTreatsAlreadyPurgedOwnedItemAsIdempotent(t *testing.T) {
	sdk := &mutationTestSDK{bulkSDK: &bulkSDK{}, cleanErr: errors.New("already removed")}
	client := newMutationTestClient(sdk)
	if err := client.PurgeRecycle(context.Background(), "owned-item"); err != nil {
		t.Fatalf("already absent item should reconcile as success: %v", err)
	}
	sdk.recycleItems = []pan115sdk.RecycleBinItem{{FileId: "owned-item"}}
	if err := client.PurgeRecycle(context.Background(), "owned-item"); err == nil {
		t.Fatal("provider failure was hidden while owned item remained in recycle bin")
	}
}

type emptyMkdirSDK struct{ *mutationTestSDK }

func (s *emptyMkdirSDK) Mkdir(parentID, name string) (string, error) {
	s.mkdirParent, s.mkdirName = parentID, name
	return "", nil
}
