package services

import (
	"sync"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
)

type JobEvent struct {
	Type    string    `json:"-"`
	JobID   string    `json:"job_id"`
	JobType string    `json:"job_type,omitempty"`
	OwnerID *uint     `json:"owner_id"`
	Status  string    `json:"status,omitempty"`
	At      time.Time `json:"at"`
}
type JobEventEnvelope struct {
	Type string   `json:"type"`
	Data JobEvent `json:"data"`
}
type queueSubscriber struct {
	actor        Actor
	ch           chan JobEvent
	lastProgress map[string]time.Time
}
type QueueEventHub struct {
	mu          sync.Mutex
	next        uint64
	subscribers map[uint64]*queueSubscriber
}

func NewQueueEventHub() *QueueEventHub {
	return &QueueEventHub{subscribers: map[uint64]*queueSubscriber{}}
}
func (h *QueueEventHub) Subscribe(actor Actor) (<-chan JobEvent, func()) {
	h.mu.Lock()
	h.next++
	id := h.next
	sub := &queueSubscriber{actor: actor, ch: make(chan JobEvent, 32), lastProgress: map[string]time.Time{}}
	h.subscribers[id] = sub
	h.mu.Unlock()
	return sub.ch, func() {
		h.mu.Lock()
		if current := h.subscribers[id]; current != nil {
			delete(h.subscribers, id)
			close(current.ch)
		}
		h.mu.Unlock()
	}
}
func (h *QueueEventHub) Publish(event JobEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sub := range h.subscribers {
		if !canReceiveJobEvent(sub.actor, event) {
			continue
		}
		if event.Type == "job.progress" {
			if last := sub.lastProgress[event.JobID]; event.At.Sub(last) < time.Second {
				continue
			}
			sub.lastProgress[event.JobID] = event.At
		}
		select {
		case sub.ch <- event:
		default:
		}
	}
}

func canReceiveJobEvent(actor Actor, event JobEvent) bool {
	if event.JobType == JobTypeFollowSearch {
		owned := event.OwnerID != nil && *event.OwnerID == actor.User.ID
		return actor.Can(authz.PermissionFollowsReadAll) || (owned && actor.Can(authz.PermissionFollowsReadOwn))
	}
	if actor.Can(authz.PermissionJobsReadAll) {
		return true
	}
	owned := event.OwnerID != nil && *event.OwnerID == actor.User.ID
	if owned && actor.Can(authz.PermissionJobsReadOwn) {
		return true
	}
	if event.JobType != "transfer" {
		return false
	}
	return actor.Can(authz.PermissionTransfersReadAll) || (owned && actor.Can(authz.PermissionTransfersReadOwn))
}
