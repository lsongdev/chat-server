package delivery

import (
	"encoding/json"
	"time"
)

const Subprotocol = "delivery.v1"

type inboundEnvelope struct {
	Op        string           `json:"op"`
	ID        string           `json:"id,omitempty"`
	RoomID    string           `json:"room_id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Profile   Profile          `json:"profile,omitempty"`
	Data      json.RawMessage  `json:"data,omitempty"`
	ExpiresAt *time.Time       `json:"expires_at,omitempty"`
	Stream    *StreamPosition  `json:"stream,omitempty"`
	Rooms     map[string]int64 `json:"rooms,omitempty"`
}

type helloEnvelope struct {
	Op              string `json:"op"`
	Protocol        string `json:"protocol"`
	ConnectionID    string `json:"connection_id"`
	IdentityID      string `json:"identity_id"`
	MaxMessageBytes int    `json:"max_message_bytes"`
}

type ackEnvelope struct {
	Op       string        `json:"op"`
	ID       string        `json:"id"`
	Status   ReceiptStatus `json:"status"`
	EventID  string        `json:"event_id,omitempty"`
	Sequence int64         `json:"sequence,omitempty"`
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
	Stream    *StreamPosition `json:"stream,omitempty"`
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

func wireEvent(message Message) eventEnvelope {
	return eventEnvelope{
		Op: "event", RoomID: message.RoomID, ID: message.ID, PublishID: message.PublishID,
		Name: message.Name, Profile: message.Profile, Sequence: message.Sequence,
		ActorID: message.ActorID, Data: cloneJSON(message.Data), CreatedAt: message.CreatedAt,
		ExpiresAt: message.ExpiresAt, Stream: message.Stream,
	}
}
