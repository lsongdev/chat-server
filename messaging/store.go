package messaging

import "context"

// Store owns generic messaging rooms, membership policy and the durable event
// log. Applications may project these operations onto their own domain tables.
type Store interface {
	CreateRoom(context.Context, Room, Member) error
	DeleteRoom(context.Context, string) error
	Room(context.Context, string) (Room, error)
	AddMember(context.Context, Member) error
	UpdateMember(context.Context, Member) error
	RemoveMember(context.Context, string, string) error
	Member(context.Context, string, string) (Member, error)
	RoomsForIdentity(context.Context, string) ([]Room, error)

	Append(context.Context, Publish) (Event, error)
	EventsAfter(context.Context, string, int64, int) ([]Event, error)
	HeadSequence(context.Context, string) (int64, error)
}
