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

// Identity is the only user concept understood by Delivery.
type Identity struct {
	ID string `json:"id"`
}

// Profile selects durable delivery or best-effort realtime delivery.
// It remains part of the wire contract; business meaning stays in the host.
type Profile string

const (
	Durable   Profile = "durable"
	Ephemeral Profile = "ephemeral"
)

// Publish is a validated request to create or fan out an event.
type Publish struct {
	ID        string
	RoomID    string
	ActorID   string
	Name      string
	Profile   Profile
	Data      json.RawMessage
	ExpiresAt *time.Time
}

// Event is the fact routed by Delivery. Durable events have a Sequence;
// ephemeral events may expire and are never returned by Store.EventsAfter.
type Event struct {
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
}

// ClientPublish is an untrusted wire request plus authenticated identity.
type ClientPublish struct {
	IdentityID string
	ID         string
	RoomID     string
	Name       string
	Profile    Profile
	Data       json.RawMessage
}

type ClientPublishHandler func(context.Context, ClientPublish) (Publish, error)
