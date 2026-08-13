package delivery

import "context"

// Store is the durable source of truth. Append must allocate a room sequence
// and enforce publish idempotency atomically.
type Store interface {
	CreateRoom(context.Context, Room, Member) error
	DeleteRoom(context.Context, string) error
	Room(context.Context, string) (Room, error)

	AddMember(context.Context, Member) error
	UpdateMember(context.Context, Member) error
	RemoveMember(context.Context, string, string) error
	Member(context.Context, string, string) (Member, error)
	RoomsForIdentity(context.Context, string) ([]Room, error)

	Append(context.Context, PublishRequest) (Message, error)
	EventsAfter(context.Context, string, int64, int) ([]Message, error)
	HeadSequence(context.Context, string) (int64, error)
}
