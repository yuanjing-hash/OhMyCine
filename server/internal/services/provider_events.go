package services

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	providerLifeStream       = "life"
	providerEventBatchLimit  = 200
	providerEventProcessSize = 500
)

type ProviderEventNotifier interface {
	ProviderEventsChanged(context.Context, uint, []models.ProviderEvent) error
}

type ProviderEventService struct {
	db        *gorm.DB
	notifiers []ProviderEventNotifier
}

func NewProviderEventService(db *gorm.DB, notifiers ...ProviderEventNotifier) *ProviderEventService {
	active := make([]ProviderEventNotifier, 0, len(notifiers))
	for _, notifier := range notifiers {
		if notifier != nil {
			active = append(active, notifier)
		}
	}
	return &ProviderEventService{db: db, notifiers: active}
}

// IngestOnce fetches one bounded provider page. Cursor advancement and inbox
// insertion share a transaction, so a crash can only replay safely.
func (s *ProviderEventService) IngestOnce(ctx context.Context, connectionID uint, source cloudpkg.ChangeSource) (int, bool, error) {
	if connectionID == 0 || source == nil {
		return 0, false, errors.New("provider event source is invalid")
	}
	cursor, err := s.cursor(connectionID, providerLifeStream)
	if err != nil {
		return 0, false, err
	}
	page, err := source.Changes(ctx, cursor, providerEventBatchLimit)
	if err != nil {
		return 0, false, err
	}
	events := append([]cloudpkg.ChangeEvent(nil), page.Events...)
	sort.SliceStable(events, func(i, j int) bool {
		return compareCursor(cloudpkg.ChangeCursor{Time: events[i].Time, ID: events[i].ID}, cloudpkg.ChangeCursor{Time: events[j].Time, ID: events[j].ID}) < 0
	})
	rows := make([]models.ProviderEvent, 0, len(events))
	now := time.Now().UTC()
	for _, event := range events {
		row, valid := normalizeProviderEvent(connectionID, providerLifeStream, event, now)
		if !valid || compareCursor(cloudpkg.ChangeCursor{Time: row.EventTime, ID: row.ProviderEventID}, cursor) <= 0 {
			continue
		}
		rows = append(rows, row)
	}
	next := page.NextCursor
	if next.Time.IsZero() && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = cloudpkg.ChangeCursor{Time: last.EventTime, ID: last.ProviderEventID}
	}
	if compareCursor(next, cursor) < 0 {
		return 0, false, errors.New("provider event cursor moved backwards")
	}
	inserted := int64(0)
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index := range rows {
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows[index])
			if result.Error != nil {
				return result.Error
			}
			inserted += result.RowsAffected
		}
		if compareCursor(next, cursor) == 0 {
			return nil
		}
		record := models.ProviderCursor{ConnectionID: connectionID, Stream: providerLifeStream, CursorTime: next.Time.UTC(), CursorID: next.ID, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "connection_id"}, {Name: "stream"}}, DoUpdates: clause.AssignmentColumns([]string{"cursor_time", "cursor_id", "updated_at"})}).Create(&record).Error
	})
	return int(inserted), page.HasMore, err
}

// ProcessPending notifies every registered consumer only after durable inbox
// persistence. Any failed notification leaves records pending for recovery.
func (s *ProviderEventService) ProcessPending(ctx context.Context, connectionID uint) (int, error) {
	if len(s.notifiers) == 0 {
		return 0, nil
	}
	var rows []models.ProviderEvent
	if err := s.db.WithContext(ctx).Where("connection_id = ? AND stream = ? AND processed_at IS NULL", connectionID, providerLifeStream).Order("event_time, provider_event_id, id").Limit(providerEventProcessSize).Find(&rows).Error; err != nil || len(rows) == 0 {
		return 0, err
	}
	var notificationErrors []error
	for _, notifier := range s.notifiers {
		if err := notifier.ProviderEventsChanged(ctx, connectionID, rows); err != nil {
			notificationErrors = append(notificationErrors, err)
		}
	}
	if len(notificationErrors) > 0 {
		return 0, errors.Join(notificationErrors...)
	}
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&models.ProviderEvent{}).Where("id IN ? AND processed_at IS NULL", ids).Update("processed_at", now)
	return int(result.RowsAffected), result.Error
}

func (s *ProviderEventService) cursor(connectionID uint, stream string) (cloudpkg.ChangeCursor, error) {
	var record models.ProviderCursor
	err := s.db.Where("connection_id = ? AND stream = ?", connectionID, stream).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cloudpkg.ChangeCursor{}, nil
	}
	return cloudpkg.ChangeCursor{Time: record.CursorTime.UTC(), ID: record.CursorID}, err
}

func normalizeProviderEvent(connectionID uint, stream string, event cloudpkg.ChangeEvent, now time.Time) (models.ProviderEvent, bool) {
	event.ID = strings.TrimSpace(event.ID)
	event.ItemID = strings.TrimSpace(event.ItemID)
	event.ParentID = strings.TrimSpace(event.ParentID)
	event.PreviousParentID = strings.TrimSpace(event.PreviousParentID)
	event.Name = strings.TrimSpace(event.Name)
	if event.ID == "" || event.ItemID == "" || event.Time.IsZero() || len(event.ID) > 128 || len(event.ItemID) > 128 || len(event.ParentID) > 128 || len(event.PreviousParentID) > 128 || len(event.Name) > 512 || strings.ContainsAny(event.ID+event.ItemID+event.ParentID+event.PreviousParentID+event.Name, "\x00\r\n") {
		return models.ProviderEvent{}, false
	}
	switch event.Kind {
	case cloudpkg.ChangeCreated, cloudpkg.ChangeMoved, cloudpkg.ChangeRenamed, cloudpkg.ChangeDeleted:
	default:
		return models.ProviderEvent{}, false
	}
	payload, _ := json.Marshal(map[string]string{"kind": event.Kind, "item_id": event.ItemID, "parent_id": event.ParentID, "previous_parent_id": event.PreviousParentID, "name": event.Name})
	return models.ProviderEvent{ConnectionID: connectionID, Stream: stream, ProviderEventID: event.ID, EventTime: event.Time.UTC(), Kind: event.Kind, ItemID: event.ItemID, ParentID: event.ParentID, PreviousParentID: event.PreviousParentID, Name: event.Name, PayloadJSON: string(payload), CreatedAt: now}, true
}

func compareCursor(a, b cloudpkg.ChangeCursor) int {
	if a.Time.Before(b.Time) {
		return -1
	}
	if a.Time.After(b.Time) {
		return 1
	}
	if !decimalCursorID(a.ID) || !decimalCursorID(b.ID) {
		return strings.Compare(a.ID, b.ID)
	}
	aID, bID := strings.TrimLeft(a.ID, "0"), strings.TrimLeft(b.ID, "0")
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

func decimalCursorID(value string) bool {
	if value == "" {
		return true
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
