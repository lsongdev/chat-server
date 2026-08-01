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
	AvatarURL     string    `json:"avatar_url"`
	Status        string    `json:"status"`
}

type Conversation struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title,omitempty"`
	LastSeq     int64     `json:"last_seq"`
	LastReadSeq int64     `json:"last_read_seq"`
	JoinedSeq   int64     `json:"joined_seq"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Member struct {
	UserID        uuid.UUID `json:"user_id"`
	Username      string    `json:"username,omitempty"`
	DisplayName   string    `json:"display_name,omitempty"`
	Email         string    `json:"email,omitempty"`
	EmailVerified bool      `json:"email_verified"`
	PictureURL    string    `json:"picture_url,omitempty"`
	AvatarURL     string    `json:"avatar_url"`
	Role          string    `json:"role"`
	Status        string    `json:"status"`
	JoinedSeq     int64     `json:"joined_seq"`
}

type UserLookup struct {
	UserID        uuid.UUID `json:"user_id"`
	Username      string    `json:"username,omitempty"`
	DisplayName   string    `json:"display_name,omitempty"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	PictureURL    string    `json:"picture_url,omitempty"`
	AvatarURL     string    `json:"avatar_url"`
}

type Contact struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Email      string     `json:"email"`
	Note       string     `json:"note,omitempty"`
	LinkedUser *uuid.UUID `json:"linked_user_id,omitempty"`
	AvatarURL  string     `json:"avatar_url"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type Event struct {
	ConversationID  uuid.UUID       `json:"conversation_id"`
	Seq             int64           `json:"seq"`
	ID              uuid.UUID       `json:"id"`
	SenderID        *uuid.UUID      `json:"sender_id,omitempty"`
	ClientMessageID *uuid.UUID      `json:"client_message_id,omitempty"`
	SenderEmail     string          `json:"sender_email,omitempty"`
	SenderName      string          `json:"sender_name,omitempty"`
	Type            string          `json:"type"`
	Payload         json.RawMessage `json:"payload"`
	CreatedAt       time.Time       `json:"created_at"`
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
