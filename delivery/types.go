package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrAlreadyExists    = errors.New("already exists")
	ErrNotFound         = errors.New("not found")
	ErrPermissionDenied = errors.New("permission denied")
	ErrInvalid          = errors.New("invalid input")
)

type Identity struct {
	ID string `json:"id"`
}

type Room struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type Capability string

const (
	Receive       Capability = "room.receive"
	Publish       Capability = "message.publish"
	ReadHistory   Capability = "history.read"
	ManageMembers Capability = "members.manage"
	ManageRoom    Capability = "room.manage"
)

type Grants map[Capability]bool

func OwnerGrants() Grants {
	return Grants{Receive: true, Publish: true, ReadHistory: true, ManageMembers: true, ManageRoom: true}
}

func MemberGrants() Grants {
	return Grants{Receive: true, Publish: true, ReadHistory: true}
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

type Profile string

const (
	Durable   Profile = "durable"
	Ephemeral Profile = "ephemeral"
	Stream    Profile = "stream"
)

type StreamPosition struct {
	ID    string `json:"id"`
	Seq   uint64 `json:"seq"`
	Final bool   `json:"final"`
}

type Message struct {
	ID        string          `json:"id"`
	PublishID string          `json:"publish_id,omitempty"`
	RoomID    string          `json:"room_id"`
	ActorID   string          `json:"actor_id"`
	Name      string          `json:"name"`
	Profile   Profile         `json:"profile"`
	Sequence  int64           `json:"sequence,omitempty"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
	Stream    *StreamPosition `json:"stream,omitempty"`
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

type PublishRequest struct {
	ID        string
	RoomID    string
	ActorID   string
	Name      string
	Profile   Profile
	Data      json.RawMessage
	ExpiresAt *time.Time
	Stream    *StreamPosition
}

type ReceiptStatus string

const (
	Accepted  ReceiptStatus = "accepted"
	Committed ReceiptStatus = "committed"
)

type Receipt struct {
	PublishID string        `json:"publish_id"`
	MessageID string        `json:"message_id,omitempty"`
	Status    ReceiptStatus `json:"status"`
	Sequence  int64         `json:"sequence,omitempty"`
	Message   *Message      `json:"-"`
}

type ClientPublish struct {
	IdentityID string
	ID         string
	RoomID     string
	Name       string
	Profile    Profile
	Data       json.RawMessage
	ExpiresAt  *time.Time
	Stream     *StreamPosition
}

type ClientPublishHandler func(context.Context, ClientPublish) (PublishRequest, error)
