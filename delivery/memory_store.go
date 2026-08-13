package delivery

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"
)

type MemoryStore struct {
	mu          sync.RWMutex
	rooms       map[string]Room
	members     map[string]map[string]Member
	events      map[string][]Message
	idempotency map[string]Message
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		rooms:       make(map[string]Room),
		members:     make(map[string]map[string]Member),
		events:      make(map[string][]Message),
		idempotency: make(map[string]Message),
	}
}

func (s *MemoryStore) CreateRoom(_ context.Context, room Room, creator Member) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rooms[room.ID]; exists {
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
	delete(s.events, roomID)
	for key, message := range s.idempotency {
		if message.RoomID == roomID {
			delete(s.idempotency, key)
		}
	}
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
		member, exists := members[identityID]
		if exists && member.Grants.Allows(Receive) {
			rooms = append(rooms, s.rooms[roomID])
		}
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].ID < rooms[j].ID })
	return rooms, nil
}

func (s *MemoryStore) Append(_ context.Context, publish PublishRequest) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rooms[publish.RoomID]; !exists {
		return Message{}, ErrNotFound
	}
	key := publish.RoomID + "\x00" + publish.ActorID + "\x00" + publish.ID
	if message, exists := s.idempotency[key]; exists {
		return cloneMessage(message), nil
	}
	sequence := int64(len(s.events[publish.RoomID]) + 1)
	message := Message{
		ID: uuid.NewString(), PublishID: publish.ID, RoomID: publish.RoomID,
		ActorID: publish.ActorID, Name: publish.Name, Profile: Durable,
		Sequence: sequence, Data: cloneJSON(publish.Data), CreatedAt: nowUTC(),
	}
	s.events[publish.RoomID] = append(s.events[publish.RoomID], message)
	s.idempotency[key] = message
	return cloneMessage(message), nil
}

func (s *MemoryStore) EventsAfter(_ context.Context, roomID string, sequence int64, limit int) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, exists := s.rooms[roomID]; !exists {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		return []Message{}, nil
	}
	events := s.events[roomID]
	result := make([]Message, 0, min(limit, len(events)))
	for _, message := range events {
		if message.Sequence > sequence {
			result = append(result, cloneMessage(message))
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
	if _, exists := s.rooms[roomID]; !exists {
		return 0, ErrNotFound
	}
	return int64(len(s.events[roomID])), nil
}

func cloneMember(member Member) Member {
	member.Grants = member.Grants.Clone()
	return member
}

func cloneMessage(message Message) Message {
	message.Data = cloneJSON(message.Data)
	if message.Stream != nil {
		stream := *message.Stream
		message.Stream = &stream
	}
	return message
}

func cloneJSON(value []byte) []byte { return append([]byte(nil), value...) }
