package messaging

import (
	"time"

	"github.com/lsongdev/chat-server/delivery"
)

// Transport primitives are defined by the delivery kernel and surfaced here
// so applications can depend on the messaging framework alone.
type Identity = delivery.Identity
type Profile = delivery.Profile
type Publish = delivery.Publish
type Event = delivery.Event
type ClientPublish = delivery.ClientPublish
type ClientPublishHandler = delivery.ClientPublishHandler
type Limits = delivery.Limits
type Bus = delivery.Bus

const (
	Durable     = delivery.Durable
	Ephemeral   = delivery.Ephemeral
	Subprotocol = delivery.Subprotocol
)

var (
	ErrAlreadyExists    = delivery.ErrAlreadyExists
	ErrNotFound         = delivery.ErrNotFound
	ErrPermissionDenied = delivery.ErrPermissionDenied
	ErrInvalid          = delivery.ErrInvalid
)

type Room struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type Capability string

const (
	Receive       Capability = "room.receive"
	PublishEvents Capability = "message.publish"
	ReadHistory   Capability = "history.read"
	ManageMembers Capability = "members.manage"
	ManageRoom    Capability = "room.manage"
)

type Grants map[Capability]bool

func OwnerGrants() Grants {
	return Grants{Receive: true, PublishEvents: true, ReadHistory: true, ManageMembers: true, ManageRoom: true}
}

func MemberGrants() Grants {
	return Grants{Receive: true, PublishEvents: true, ReadHistory: true}
}

func (g Grants) Allows(capability Capability) bool { return g[capability] }

func (g Grants) Clone() Grants {
	clone := make(Grants, len(g))
	for capability, allowed := range g {
		clone[capability] = allowed
	}
	return clone
}

type Member struct {
	RoomID       string    `json:"room_id"`
	IdentityID   string    `json:"identity_id"`
	Grants       Grants    `json:"grants"`
	HistoryStart int64     `json:"history_start,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type CreateRoom struct {
	ID        string
	CreatorID string
}

type AddMember struct {
	ActorID    string
	RoomID     string
	IdentityID string
	Grants     Grants
}

type UpdateMember = AddMember

type RemoveMember struct {
	ActorID    string
	RoomID     string
	IdentityID string
}
