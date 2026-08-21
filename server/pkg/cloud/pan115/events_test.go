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

type lifeSDK struct {
	bulkSDK
	batch lifeEventBatch
	err   error
	calls int
}

func (s *lifeSDK) ListLifeEvents(int) (lifeEventBatch, error) {
	s.calls++
	return s.batch, s.err
}

func newLifeClient(sdk sdkClient) *Client {
	return &Client{sdk: sdk, eventRate: rate.NewLimiter(rate.Inf, 1), callSlots: make(chan struct{}, maxInFlightCalls), now: time.Now, jitter: func() time.Duration { return 0 }}
}

func TestChangesAnchorsNewConnectionWithoutReplayingHistory(t *testing.T) {
	sdk := &lifeSDK{batch: lifeEventBatch{Events: []lifeEventWire{
		{ID: "8", Type: 2, UpdateTime: 100, FileID: "file-8", ParentID: "root", FileName: "old.mkv"},
		{ID: "9", Type: 2, UpdateTime: 101, FileID: "file-9", ParentID: "root", FileName: "latest.mkv"},
	}}}
	page, err := newLifeClient(sdk).Changes(context.Background(), cloud.ChangeCursor{}, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 || page.NextCursor.ID != "9" || page.NextCursor.Time.Unix() != 101 || page.HasMore {
		t.Fatalf("page=%+v", page)
	}
}

func TestChangesMapsAllowlistedKindsAndOrdersNumericIDs(t *testing.T) {
	sdk := &lifeSDK{batch: lifeEventBatch{Events: []lifeEventWire{
		{ID: "10", Type: 24, UpdateTime: 200, FileID: "rename", ParentID: "folder", FileName: "New.mkv"},
		{ID: "9", Type: 6, UpdateTime: 200, FileID: "move", ParentID: "target", OldParentID: "source", FileName: "Move.mkv"},
		{ID: "8", Type: 22, UpdateTime: 199, FileID: "deleted", ParentID: "old", FileName: "Gone.mkv"},
		{ID: "11", Type: 8, UpdateTime: 201, FileID: "browse", ParentID: "folder", FileName: "Ignored.mkv"},
		// Sensitive upstream fields such as pick_code are deliberately absent
		// from lifeEventWire and therefore cannot cross this adapter boundary.
	}}}
	cursor := cloud.ChangeCursor{Time: time.Unix(198, 0).UTC(), ID: "7"}
	page, err := newLifeClient(sdk).Changes(context.Background(), cursor, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 3 || page.Events[0].Kind != cloud.ChangeDeleted || page.Events[1].ID != "9" || page.Events[1].Kind != cloud.ChangeMoved || page.Events[1].PreviousParentID != "source" || page.Events[2].ID != "10" || page.Events[2].Kind != cloud.ChangeRenamed {
		t.Fatalf("events=%+v", page.Events)
	}
	if page.NextCursor.ID != "11" || page.NextCursor.Time.Unix() != 201 {
		t.Fatalf("cursor=%+v", page.NextCursor)
	}
}

func TestChangesReturnsOldestBoundedPage(t *testing.T) {
	sdk := &lifeSDK{batch: lifeEventBatch{Events: []lifeEventWire{
		{ID: "3", Type: 1, UpdateTime: 300, FileID: "three", ParentID: "root", FileName: "3.jpg"},
		{ID: "2", Type: 2, UpdateTime: 300, FileID: "two", ParentID: "root", FileName: "2.mkv"},
		{ID: "1", Type: 17, UpdateTime: 300, FileID: "one", ParentID: "root", FileName: "one"},
	}}}
	page, err := newLifeClient(sdk).Changes(context.Background(), cloud.ChangeCursor{Time: time.Unix(299, 0).UTC()}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Events) != 2 || page.Events[0].ID != "1" || page.Events[1].ID != "2" || page.NextCursor.ID != "2" {
		t.Fatalf("page=%+v", page)
	}
}

func TestChangesMapsProviderFailuresAndRejectsMalformedKnownEvent(t *testing.T) {
	tests := []struct {
		name string
		sdk  *lifeSDK
		code string
	}{
		{name: "rate limited", sdk: &lifeSDK{err: errors.New("HTTP 429")}, code: cloud.CodeRateLimited},
		{name: "auth expired", sdk: &lifeSDK{err: pan115sdk.ErrBadCookie}, code: cloud.CodeAuthExpired},
		{name: "not logged in", sdk: &lifeSDK{err: pan115sdk.ErrNotLogin}, code: cloud.CodeAuthExpired},
		{name: "invalid event", sdk: &lifeSDK{batch: lifeEventBatch{Events: []lifeEventWire{{ID: "1", Type: 2, UpdateTime: 0, FileID: "file"}}}}, code: cloud.CodeResponseInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newLifeClient(test.sdk).Changes(context.Background(), cloud.ChangeCursor{Time: time.Unix(1, 0)}, 200)
			if code, _ := cloud.ErrorInfo(err); err == nil || code != test.code {
				t.Fatalf("code=%q err=%v", code, err)
			}
		})
	}
}
