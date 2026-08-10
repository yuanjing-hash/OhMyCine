package services

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

type AuditService struct{ db *gorm.DB }

func NewAuditService(db *gorm.DB) *AuditService { return &AuditService{db: db} }

func (s *AuditService) Record(tx *gorm.DB, actorID *uint, action, targetType, targetID, outcome string, metadata map[string]any, request RequestContext) error {
	if tx == nil {
		tx = s.db
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	return tx.Create(&models.AuditLog{
		ActorID: actorID, Action: action, TargetType: targetType, TargetID: targetID,
		Outcome: outcome, Metadata: string(payload), RequestID: request.RequestID, IPHint: request.IPHint,
	}).Error
}

type AuditEntry struct {
	ID         uint           `json:"id"`
	ActorID    *uint          `json:"actor_id"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Outcome    string         `json:"outcome"`
	Metadata   map[string]any `json:"metadata"`
	RequestID  string         `json:"request_id"`
	IPHint     string         `json:"ip_hint"`
	CreatedAt  any            `json:"created_at"`
}

func (s *AuditService) List(limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var logs []models.AuditLog
	if err := s.db.Order("id DESC").Limit(limit).Find(&logs).Error; err != nil {
		return nil, err
	}
	entries := make([]AuditEntry, 0, len(logs))
	for _, log := range logs {
		metadata := map[string]any{}
		_ = json.Unmarshal([]byte(log.Metadata), &metadata)
		entries = append(entries, AuditEntry{ID: log.ID, ActorID: log.ActorID, Action: log.Action, TargetType: log.TargetType, TargetID: log.TargetID, Outcome: log.Outcome, Metadata: metadata, RequestID: log.RequestID, IPHint: log.IPHint, CreatedAt: log.CreatedAt})
	}
	return entries, nil
}

func uintID(id uint) string { return strconv.FormatUint(uint64(id), 10) }
