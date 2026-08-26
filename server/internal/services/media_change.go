package services

import (
	"errors"
	"sync"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultMediaChangeRetention = 4096

// MediaChangeService owns the single transactional boundary for user-visible
// Server library changes. Its in-memory channel is only a wake hint; SQLite is
// always authoritative for reconnect and restart recovery.
type MediaChangeService struct {
	db       *gorm.DB
	mu       sync.Mutex
	wake     chan struct{}
	onReady  func(uint, uint64)
	retained int
}

func NewMediaChangeService(db *gorm.DB) *MediaChangeService {
	return &MediaChangeService{db: db, wake: make(chan struct{}, 1), retained: defaultMediaChangeRetention}
}

func (s *MediaChangeService) SetReadyHandler(handler func(uint, uint64)) {
	s.mu.Lock()
	s.onReady = handler
	s.mu.Unlock()
}

func (s *MediaChangeService) Wakeups() <-chan struct{} { return s.wake }

func (s *MediaChangeService) RecordTx(tx *gorm.DB, libraryID uint, generation uint64, kind string, ready bool) (models.MediaLibraryChange, error) {
	if kind != models.MediaLibraryChangeCatalog && kind != models.MediaLibraryChangeMetadata && kind != models.MediaLibraryChangeRemoval {
		return models.MediaLibraryChange{}, appError(CodeInvalidRequest, "媒体变更类型无效", nil)
	}
	var library models.MediaLibrary
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&library, libraryID).Error; err != nil {
		return models.MediaLibraryChange{}, err
	}
	library.ContentRevision++
	if err := tx.Model(&library).Update("content_revision", library.ContentRevision).Error; err != nil {
		return models.MediaLibraryChange{}, err
	}
	now := time.Now().UTC()
	state := models.MediaLibraryChangePending
	var readyAt *time.Time
	if ready {
		state, readyAt = models.MediaLibraryChangeReady, &now
	}
	change := models.MediaLibraryChange{LibraryID: libraryID, Revision: library.ContentRevision, Kind: kind, State: state, Generation: generation, ReadyAt: readyAt, CreatedAt: now}
	if err := tx.Create(&change).Error; err != nil {
		return models.MediaLibraryChange{}, err
	}
	if ready {
		if err := s.advanceTargetsTx(tx, libraryID, change.Revision, now); err != nil {
			return models.MediaLibraryChange{}, err
		}
	}
	return change, nil
}

func (s *MediaChangeService) MarkGenerationReadyTx(tx *gorm.DB, libraryID uint, generation uint64) ([]models.MediaLibraryChange, error) {
	var changes []models.MediaLibraryChange
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("library_id = ? AND generation = ? AND state = ?", libraryID, generation, models.MediaLibraryChangePending).Order("revision").Find(&changes).Error; err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		// Artifact jobs intentionally coalesce to the newest complete generation.
		// A no-op scan can therefore finish a newer artifact generation without
		// owning a change row of its own. In that case the newest older pending
		// change is still represented by the completed artifacts and must not be
		// deleted silently. Older pending rows are superseded by that projection.
		var carried models.MediaLibraryChange
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("library_id = ? AND generation < ? AND state = ?", libraryID, generation, models.MediaLibraryChangePending).
			Order("revision DESC").First(&carried).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		changes = []models.MediaLibraryChange{carried}
		if err := tx.Where("library_id = ? AND generation < ? AND state = ? AND sequence <> ?", libraryID, generation, models.MediaLibraryChangePending, carried.Sequence).Delete(&models.MediaLibraryChange{}).Error; err != nil {
			return nil, err
		}
	} else if err := tx.Where("library_id = ? AND generation < ? AND state = ?", libraryID, generation, models.MediaLibraryChangePending).Delete(&models.MediaLibraryChange{}).Error; err != nil {
		// A change in the completed generation supersedes every older pending
		// projection; only this generation is externally observable.
		return nil, err
	}
	now := time.Now().UTC()
	for index := range changes {
		if err := tx.Model(&models.MediaLibraryChange{}).Where("sequence = ? AND state = ?", changes[index].Sequence, models.MediaLibraryChangePending).Updates(map[string]any{"state": models.MediaLibraryChangeReady, "ready_at": now}).Error; err != nil {
			return nil, err
		}
		changes[index].State, changes[index].ReadyAt = models.MediaLibraryChangeReady, &now
	}
	latest := changes[len(changes)-1].Revision
	if err := s.advanceTargetsTx(tx, libraryID, latest, now); err != nil {
		return nil, err
	}
	return changes, nil
}

func (s *MediaChangeService) advanceTargetsTx(tx *gorm.DB, libraryID uint, revision uint64, now time.Time) error {
	return tx.Model(&models.MediaServerRefreshTarget{}).
		Where("library_id = ? AND enabled = ? AND desired_revision < ?", libraryID, true, revision).
		Updates(map[string]any{"desired_revision": revision, "updated_at": now}).Error
}

// NotifyCommitted must be called only after the transaction that wrote or
// readied the change has committed.
func (s *MediaChangeService) NotifyCommitted(libraryID uint, revision uint64) {
	select {
	case s.wake <- struct{}{}:
	default:
	}
	s.mu.Lock()
	handler := s.onReady
	s.mu.Unlock()
	if handler != nil {
		handler(libraryID, revision)
	}
	go s.prune()
}

func (s *MediaChangeService) prune() {
	if s.retained <= 0 {
		return
	}
	var cutoff uint64
	if err := s.db.Model(&models.MediaLibraryChange{}).Where("state = ?", models.MediaLibraryChangeReady).
		Order("sequence DESC").Offset(s.retained).Limit(1).Pluck("sequence", &cutoff).Error; err != nil || cutoff == 0 {
		return
	}
	latestPerLibrary := s.db.Model(&models.MediaLibraryChange{}).
		Select("MAX(sequence)").Where("state = ?", models.MediaLibraryChangeReady).Group("library_id")
	_ = s.db.Where("state = ? AND sequence <= ? AND sequence NOT IN (?)", models.MediaLibraryChangeReady, cutoff, latestPerLibrary).
		Delete(&models.MediaLibraryChange{}).Error
}

type MediaChangePage struct {
	Changes        []models.MediaLibraryChange
	LatestSequence uint64
	OldestSequence uint64
	ResyncRequired bool
}

func (s *MediaChangeService) ReadyAfter(cursor uint64, limit int) (MediaChangePage, error) {
	if limit < 1 || limit > 256 {
		limit = 256
	}
	var page MediaChangePage
	if err := s.db.Model(&models.MediaLibraryChange{}).Where("state = ?", models.MediaLibraryChangeReady).Select("COALESCE(MIN(sequence),0), COALESCE(MAX(sequence),0)").Row().Scan(&page.OldestSequence, &page.LatestSequence); err != nil {
		return page, err
	}
	if cursor > 0 && page.OldestSequence > 0 && cursor+1 < page.OldestSequence {
		page.ResyncRequired = true
		return page, nil
	}
	err := s.db.Where("state = ? AND sequence > ?", models.MediaLibraryChangeReady, cursor).Order("sequence").Limit(limit).Find(&page.Changes).Error
	return page, err
}
