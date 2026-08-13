package services

import (
	"sync"
	"time"
)

type JobEvent struct {
	Type    string    `json:"-"`
	JobID   string    `json:"job_id"`
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
		if !sub.actor.Can("jobs.read_all") && (event.OwnerID == nil || !sub.actor.Can("jobs.read_own") || *event.OwnerID != sub.actor.User.ID) {
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
