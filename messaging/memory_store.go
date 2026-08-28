package messaging

import (
	"context"
	"sort"
	"sync"

	"github.com/lsongdev/chat-server/delivery"
)

// MemoryStore is a complete in-memory messaging Store for tests, examples and
// single-process ephemeral applications. Durable event mechanics are delegated
// to the delivery kernel's in-memory event log.
type MemoryStore struct {
	mu      sync.RWMutex
	rooms   map[string]Room
	members map[string]map[string]Member
	deleted map[string]struct{}
	events  *delivery.MemoryStore
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		rooms: make(map[string]Room), members: make(map[string]map[string]Member),
		deleted: make(map[string]struct{}),
		events:  delivery.NewMemoryStore(),
	}
}

func (s *MemoryStore) CreateRoom(_ context.Context, room Room, creator Member) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rooms[room.ID]; exists {
		return ErrAlreadyExists
	}
	if _, exists := s.deleted[room.ID]; exists {
		return ErrAlreadyExists
	}
	s.rooms[room.ID] = room
	s.members[room.ID] = map[string]Member{creator.IdentityID: cloneMember(creator)}
	return nil
}

func (s *MemoryStore) DeleteRoom(_ context.Context, roomID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rooms[roomID]; !exists {
		return ErrNotFound
	}
	delete(s.rooms, roomID)
	delete(s.members, roomID)
	s.deleted[roomID] = struct{}{}
	return nil
}

func (s *MemoryStore) Room(_ context.Context, roomID string) (Room, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	room, exists := s.rooms[roomID]
	if !exists {
		return Room{}, ErrNotFound
	}
	return room, nil
}

func (s *MemoryStore) AddMember(_ context.Context, member Member) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	members, exists := s.members[member.RoomID]
	if !exists {
		return ErrNotFound
	}
	if _, exists := members[member.IdentityID]; exists {
		return ErrAlreadyExists
	}
	members[member.IdentityID] = cloneMember(member)
	return nil
}

func (s *MemoryStore) UpdateMember(_ context.Context, member Member) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	members, exists := s.members[member.RoomID]
	if !exists {
		return ErrNotFound
	}
	if _, exists := members[member.IdentityID]; !exists {
		return ErrNotFound
	}
	members[member.IdentityID] = cloneMember(member)
	return nil
}

func (s *MemoryStore) RemoveMember(_ context.Context, roomID, identityID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	members, exists := s.members[roomID]
	if !exists {
		return ErrNotFound
	}
	if _, exists := members[identityID]; !exists {
		return ErrNotFound
	}
	delete(members, identityID)
	return nil
}

func (s *MemoryStore) Member(_ context.Context, roomID, identityID string) (Member, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	members, exists := s.members[roomID]
	if !exists {
		return Member{}, ErrNotFound
	}
	member, exists := members[identityID]
	if !exists {
		return Member{}, ErrNotFound
	}
	return cloneMember(member), nil
}

func (s *MemoryStore) RoomsForIdentity(_ context.Context, identityID string) ([]Room, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rooms := make([]Room, 0)
	for roomID, members := range s.members {
		if _, exists := members[identityID]; exists {
			rooms = append(rooms, s.rooms[roomID])
		}
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].ID < rooms[j].ID })
	return rooms, nil
}

func (s *MemoryStore) Append(ctx context.Context, publish Publish) (Event, error) {
	return s.events.Append(ctx, publish)
}

func (s *MemoryStore) EventsAfter(ctx context.Context, roomID string, after int64, limit int) ([]Event, error) {
	return s.events.EventsAfter(ctx, roomID, after, limit)
}

func (s *MemoryStore) HeadSequence(ctx context.Context, roomID string) (int64, error) {
	return s.events.HeadSequence(ctx, roomID)
}

func cloneMember(member Member) Member {
	member.Grants = member.Grants.Clone()
	return member
}

var _ Store = (*MemoryStore)(nil)
