package delivery

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// MemoryStore is an in-memory durable event log. It intentionally has no room
// or membership model; those belong to a framework layered above Delivery.
type MemoryStore struct {
	mu          sync.RWMutex
	events      map[string][]Event
	idempotency map[string]Event
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{events: make(map[string][]Event), idempotency: make(map[string]Event)}
}

func (s *MemoryStore) Append(_ context.Context, publish Publish) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := publish.RoomID + "\x00" + publish.ActorID + "\x00" + publish.ID
	if event, exists := s.idempotency[key]; exists {
		return cloneEvent(event), nil
	}
	event := Event{
		ID: uuid.NewString(), PublishID: publish.ID, RoomID: publish.RoomID,
		ActorID: publish.ActorID, Name: publish.Name, Profile: Durable,
		Sequence: int64(len(s.events[publish.RoomID]) + 1),
		Data:     cloneJSON(publish.Data), CreatedAt: nowUTC(),
	}
	s.events[publish.RoomID] = append(s.events[publish.RoomID], event)
	s.idempotency[key] = event
	return cloneEvent(event), nil
}

func (s *MemoryStore) EventsAfter(_ context.Context, roomID string, sequence int64, limit int) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		return []Event{}, nil
	}
	result := make([]Event, 0, min(limit, len(s.events[roomID])))
	for _, event := range s.events[roomID] {
		if event.Sequence > sequence {
			result = append(result, cloneEvent(event))
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (s *MemoryStore) HeadSequence(_ context.Context, roomID string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int64(len(s.events[roomID])), nil
}

func cloneEvent(event Event) Event {
	event.Data = cloneJSON(event.Data)
	return event
}
