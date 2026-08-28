package delivery

import (
	"encoding/json"
	"time"
)

const Subprotocol = "delivery.v1"

type inboundEnvelope struct {
	Op      string           `json:"op"`
	ID      string           `json:"id,omitempty"`
	RoomID  string           `json:"room_id,omitempty"`
	Name    string           `json:"name,omitempty"`
	Profile Profile          `json:"profile,omitempty"`
	Data    json.RawMessage  `json:"data,omitempty"`
	Rooms   map[string]int64 `json:"rooms,omitempty"`
}

type helloEnvelope struct {
	Op              string `json:"op"`
	Protocol        string `json:"protocol"`
	ConnectionID    string `json:"connection_id"`
	IdentityID      string `json:"identity_id"`
	MaxMessageBytes int    `json:"max_message_bytes"`
}

type ackEnvelope struct {
	Op       string `json:"op"`
	ID       string `json:"id"`
	Status   string `json:"status"`
	EventID  string `json:"event_id,omitempty"`
	Sequence int64  `json:"sequence,omitempty"`
}

type eventEnvelope struct {
	Op        string          `json:"op"`
	RoomID    string          `json:"room_id"`
	ID        string          `json:"id"`
	PublishID string          `json:"publish_id,omitempty"`
	Name      string          `json:"name"`
	Profile   Profile         `json:"profile"`
	Sequence  int64           `json:"sequence,omitempty"`
	ActorID   string          `json:"actor_id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
	Recovered bool            `json:"recovered,omitempty"`
}

type syncEnvelope struct {
	Op            string `json:"op"`
	RoomID        string `json:"room_id"`
	AfterSequence int64  `json:"after_sequence,omitempty"`
	HeadSequence  int64  `json:"head_sequence,omitempty"`
	Sequence      int64  `json:"sequence,omitempty"`
}

type roomEnvelope struct {
	Op     string `json:"op"`
	RoomID string `json:"room_id"`
	Reason string `json:"reason,omitempty"`
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type errorEnvelope struct {
	Op        string    `json:"op"`
	RequestID string    `json:"request_id,omitempty"`
	Error     errorBody `json:"error"`
}

func wireEvent(event Event) eventEnvelope {
	return eventEnvelope{
		Op: "event", RoomID: event.RoomID, ID: event.ID, PublishID: event.PublishID,
		Name: event.Name, Profile: event.Profile, Sequence: event.Sequence,
		ActorID: event.ActorID, Data: cloneJSON(event.Data), CreatedAt: event.CreatedAt,
		ExpiresAt: event.ExpiresAt,
	}
}
