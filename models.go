package main

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID            uuid.UUID `json:"id"`
	Subject       string    `json:"-"`
	Username      string    `json:"username,omitempty"`
	DisplayName   string    `json:"display_name,omitempty"`
	Email         string    `json:"email,omitempty"`
	EmailVerified bool      `json:"email_verified"`
	PictureURL    string    `json:"picture_url,omitempty"`
	Status        string    `json:"status"`
}

type Conversation struct {
	ID          uuid.UUID `json:"id"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title,omitempty"`
	LastSeq     int64     `json:"last_seq"`
	LastReadSeq int64     `json:"last_read_seq"`
	JoinedSeq   int64     `json:"joined_seq"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Member struct {
	UserID      uuid.UUID `json:"user_id"`
	Username    string    `json:"username,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	PictureURL  string    `json:"picture_url,omitempty"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	JoinedSeq   int64     `json:"joined_seq"`
}

type Event struct {
	ConversationID uuid.UUID       `json:"conversation_id"`
	Seq            int64           `json:"seq"`
	ID             uuid.UUID       `json:"id"`
	SenderID       *uuid.UUID      `json:"sender_id,omitempty"`
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
	CreatedAt      time.Time       `json:"created_at"`
}

type LoginAttempt struct {
	Nonce        string
	CodeVerifier string
	ReturnTo     string
}

type OIDCClaims struct {
	Subject       string `json:"sub"`
	Nonce         string `json:"nonce"`
	Name          string `json:"name"`
	Username      string `json:"preferred_username"`
	Picture       string `json:"picture"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}
