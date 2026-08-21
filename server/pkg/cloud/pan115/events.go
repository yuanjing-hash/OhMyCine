package pan115

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	pan115sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

const (
	lifeEventsWebEndpoint = "https://webapi.115.com/behavior/detail"
	lifeEventsProEndpoint = "https://proapi.115.com/android/behavior/detail"
	lifeEventsReadLimit   = 1000
)

type lifeEventWire struct {
	ID               pan115sdk.IntString   `json:"id"`
	Type             pan115sdk.StringInt   `json:"type"`
	UpdateTime       pan115sdk.StringInt64 `json:"update_time"`
	FileID           pan115sdk.IntString   `json:"file_id"`
	ParentID         pan115sdk.IntString   `json:"parent_id"`
	PreviousParentID pan115sdk.IntString   `json:"previous_parent_id"`
	OldParentID      pan115sdk.IntString   `json:"old_parent_id"`
	FromParentID     pan115sdk.IntString   `json:"from_parent_id"`
	FileName         string                `json:"file_name"`
}

type lifeEventBatch struct {
	Events []lifeEventWire
	Count  int
}

type lifeEventResponse struct {
	pan115sdk.BasicResp
	Data struct {
		Count pan115sdk.StringInt `json:"count"`
		List  []lifeEventWire     `json:"list"`
	} `json:"data"`
}

func (s *sdkAdapter) ListLifeEvents(limit int) (lifeEventBatch, error) {
	if limit <= 0 || limit > lifeEventsReadLimit {
		limit = lifeEventsReadLimit
	}
	request := func(endpoint string) (lifeEventResponse, int, error) {
		result := lifeEventResponse{}
		response, err := s.Client.R().SetQueryParams(map[string]string{
			"type": "", "offset": "0", "limit": strconv.Itoa(limit),
		}).SetResult(&result).ForceContentType("application/json;charset=UTF-8").Get(endpoint)
		if err != nil {
			return result, 0, err
		}
		return result, response.StatusCode(), nil
	}
	result, status, err := request(lifeEventsWebEndpoint)
	if err == nil && status == 405 {
		result, status, err = request(lifeEventsProEndpoint)
	}
	if err != nil {
		return lifeEventBatch{}, err
	}
	if status == 401 || status == 403 {
		return lifeEventBatch{}, pan115sdk.ErrBadCookie
	}
	if status < 200 || status >= 300 {
		return lifeEventBatch{}, fmt.Errorf("115 life request returned HTTP %d", status)
	}
	if !result.State {
		if err := result.Err(); err != nil {
			return lifeEventBatch{}, err
		}
		return lifeEventBatch{}, errors.New("115 life request was rejected")
	}
	count := int(result.Data.Count)
	if count < 0 || len(result.Data.List) > limit {
		return lifeEventBatch{}, errors.New("115 life response is invalid")
	}
	return lifeEventBatch{Events: result.Data.List, Count: count}, nil
}

// Changes maps 115's newest-first life stream into the provider-neutral,
// ascending cursor contract. A new connection anchors at the newest event
// instead of replaying account history; the media-library baseline already
// reconciles existing files.
func (c *Client) Changes(ctx context.Context, cursor cloud.ChangeCursor, limit int) (cloud.ChangePage, error) {
	if limit <= 0 || limit > lifeEventsReadLimit {
		limit = lifeEventsReadLimit
	}
	var batch lifeEventBatch
	if err := c.waitAndCall(ctx, c.eventRate, func() error {
		var err error
		batch, err = c.sdk.ListLifeEvents(lifeEventsReadLimit)
		return err
	}); err != nil {
		return cloud.ChangePage{}, mapError(err)
	}
	events := make([]cloud.ChangeEvent, 0, len(batch.Events))
	latest := cursor
	for _, raw := range batch.Events {
		if position, valid := lifeEventPosition(raw); valid && compareLifePosition(position.Time, position.ID, latest.Time, latest.ID) > 0 {
			latest = position
		}
		event, recognized, err := mapLifeEvent(raw)
		if err != nil {
			return cloud.ChangePage{}, cloud.Error(cloud.CodeResponseInvalid, true, err)
		}
		if recognized {
			events = append(events, event)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		return compareLifePosition(events[i].Time, events[i].ID, events[j].Time, events[j].ID) < 0
	})
	if cursor.Time.IsZero() {
		if latest.Time.IsZero() {
			now := time.Now
			if c.now != nil {
				now = c.now
			}
			return cloud.ChangePage{NextCursor: cloud.ChangeCursor{Time: now().UTC()}}, nil
		}
		return cloud.ChangePage{NextCursor: latest}, nil
	}
	fresh := events[:0]
	for _, event := range events {
		if compareLifePosition(event.Time, event.ID, cursor.Time, cursor.ID) > 0 {
			fresh = append(fresh, event)
		}
	}
	page := cloud.ChangePage{NextCursor: latest}
	if len(fresh) == 0 {
		return page, nil
	}
	if len(fresh) > limit {
		page.HasMore = true
		fresh = fresh[:limit]
	}
	page.Events = append([]cloud.ChangeEvent(nil), fresh...)
	last := fresh[len(fresh)-1]
	if page.HasMore {
		page.NextCursor = cloud.ChangeCursor{Time: last.Time, ID: last.ID}
	}
	return page, nil
}

func lifeEventPosition(raw lifeEventWire) (cloud.ChangeCursor, bool) {
	id := strings.TrimSpace(string(raw.ID))
	if id == "" || !decimalLifeID(id) || int64(raw.UpdateTime) <= 0 || invalidLifeValue(id, 128) {
		return cloud.ChangeCursor{}, false
	}
	return cloud.ChangeCursor{Time: time.Unix(int64(raw.UpdateTime), 0).UTC(), ID: id}, true
}

func mapLifeEvent(raw lifeEventWire) (cloud.ChangeEvent, bool, error) {
	var kind string
	switch int(raw.Type) {
	case 1, 2, 14, 17, 18, 23:
		kind = cloud.ChangeCreated
	case 5, 6:
		kind = cloud.ChangeMoved
	case 20, 24:
		kind = cloud.ChangeRenamed
	case 22:
		kind = cloud.ChangeDeleted
	default:
		return cloud.ChangeEvent{}, false, nil
	}
	id := strings.TrimSpace(string(raw.ID))
	itemID := strings.TrimSpace(string(raw.FileID))
	parentID := strings.TrimSpace(string(raw.ParentID))
	previousParentID := firstNonEmpty(string(raw.PreviousParentID), string(raw.OldParentID), string(raw.FromParentID))
	name := strings.TrimSpace(raw.FileName)
	if id == "" || !decimalLifeID(id) || itemID == "" || int64(raw.UpdateTime) <= 0 || invalidLifeValue(id, 128) || invalidLifeValue(itemID, 128) || invalidLifeValue(parentID, 128) || invalidLifeValue(previousParentID, 128) || invalidLifeValue(name, 512) {
		return cloud.ChangeEvent{}, true, errors.New("115 returned an invalid life event")
	}
	return cloud.ChangeEvent{ID: id, Time: time.Unix(int64(raw.UpdateTime), 0).UTC(), Kind: kind, ItemID: itemID, ParentID: parentID, PreviousParentID: previousParentID, Name: name}, true, nil
}

func decimalLifeID(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func invalidLifeValue(value string, limit int) bool {
	return len(value) > limit || strings.ContainsAny(value, "\x00\r\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func compareLifePosition(aTime time.Time, aID string, bTime time.Time, bID string) int {
	if aTime.Before(bTime) {
		return -1
	}
	if aTime.After(bTime) {
		return 1
	}
	aID, bID = strings.TrimLeft(aID, "0"), strings.TrimLeft(bID, "0")
	if aID == "" {
		aID = "0"
	}
	if bID == "" {
		bID = "0"
	}
	if len(aID) < len(bID) {
		return -1
	}
	if len(aID) > len(bID) {
		return 1
	}
	return strings.Compare(aID, bID)
}
