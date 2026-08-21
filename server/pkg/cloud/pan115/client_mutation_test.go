package pan115

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	pan115sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"golang.org/x/time/rate"
)

type mutationTestSDK struct {
	*bulkSDK
	mkdirParent string
	mkdirName   string
	moveParent  string
	moveItems   []string
	copyParent  string
	copyItems   []string
	renameID    string
	renameName  string
	deleted     []string
	cleanedPassword string
	cleanedIDs  []string
	recycleItems []pan115sdk.RecycleBinItem
	cleanErr error
	err         error
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
	return &Client{sdk: sdk, mutationRate: rate.NewLimiter(rate.Inf, 1), callSlots: make(chan struct{}, maxInFlightCalls), now: time.Now, jitter: func() time.Duration { return 0 }}
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
