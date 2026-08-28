package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lsongdev/chat-server/messaging"
)

// ChatMessagingStore projects Chat conversations and roles into the reusable
// messaging framework. The framework adapts them to the delivery kernel.
type ChatMessagingStore struct{ store *Store }

const rtcSignalTTL = 30 * time.Second

func NewChatMessagingStore(store *Store) *ChatMessagingStore {
	return &ChatMessagingStore{store: store}
}

func chatClientPublish(maxMessageBytes int) messaging.ClientPublishHandler {
	return func(_ context.Context, command messaging.ClientPublish) (messaging.Publish, error) {
		publish := messaging.Publish{
			ID: command.ID, RoomID: command.RoomID, ActorID: command.IdentityID,
			Name: command.Name, Profile: command.Profile, Data: command.Data,
		}
		switch {
		case command.Name == "message.created" && command.Profile == messaging.Durable:
			if !validMessageContent(command.Data, maxMessageBytes) {
				return messaging.Publish{}, messaging.ErrInvalid
			}
			return publish, nil
		case command.Name == "rtc.signal" && command.Profile == messaging.Ephemeral:
			var signal struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(command.Data, &signal) != nil || !strings.HasPrefix(signal.Type, "webrtc:") ||
				len(signal.Data) == 0 || string(signal.Data) == "null" {
				return messaging.Publish{}, messaging.ErrInvalid
			}
			expiresAt := time.Now().UTC().Add(rtcSignalTTL)
			publish.ExpiresAt = &expiresAt
			return publish, nil
		default:
			return messaging.Publish{}, messaging.ErrInvalid
		}
	}
}

func (s *ChatMessagingStore) CreateRoom(ctx context.Context, room messaging.Room, creator messaging.Member) error {
	roomID, creatorID, err := parseRoomIdentity(room.ID, creator.IdentityID)
	if err != nil {
		return err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO conversations(id,created_by,last_seq,created_at,updated_at)
		VALUES($1,$2,0,$3,$3)`, roomID, creatorID, room.CreatedAt); err != nil {
		return mapDeliveryStoreError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO conversation_members
		(conversation_id,user_id,role,joined_seq,last_read_seq,status,joined_at,updated_at)
		VALUES($1,$2,'owner',0,0,'active',$3,$3)`, roomID, creatorID, creator.CreatedAt); err != nil {
		return mapDeliveryStoreError(err)
	}
	return tx.Commit()
}

func (s *ChatMessagingStore) DeleteRoom(ctx context.Context, roomID string) error {
	id, err := parseUUID(roomID)
	if err != nil {
		return err
	}
	result, err := s.store.db.ExecContext(ctx, `DELETE FROM conversations WHERE id=$1`, id)
	return requireAffected(result, err)
}

func (s *ChatMessagingStore) Room(ctx context.Context, roomID string) (messaging.Room, error) {
	id, err := parseUUID(roomID)
	if err != nil {
		return messaging.Room{}, err
	}
	var room messaging.Room
	err = s.store.db.QueryRowContext(ctx, `SELECT id::text,created_at FROM conversations WHERE id=$1`, id).Scan(&room.ID, &room.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return messaging.Room{}, messaging.ErrNotFound
	}
	return room, err
}

func (s *ChatMessagingStore) AddMember(ctx context.Context, member messaging.Member) error {
	roomID, identityID, err := parseRoomIdentity(member.RoomID, member.IdentityID)
	if err != nil {
		return err
	}
	result, err := s.store.db.ExecContext(ctx, `INSERT INTO conversation_members
		(conversation_id,user_id,role,joined_seq,last_read_seq,status,joined_at,updated_at)
		SELECT $1,$2,$3,last_seq,last_seq,'active',$4,$4 FROM conversations WHERE id=$1`,
		roomID, identityID, roleForGrants(member.Grants), member.CreatedAt)
	return requireAffected(result, err)
}

func (s *ChatMessagingStore) UpdateMember(ctx context.Context, member messaging.Member) error {
	roomID, identityID, err := parseRoomIdentity(member.RoomID, member.IdentityID)
	if err != nil {
		return err
	}
	result, err := s.store.db.ExecContext(ctx, `UPDATE conversation_members SET role=$3,updated_at=now()
		WHERE conversation_id=$1 AND user_id=$2 AND status='active'`, roomID, identityID, roleForGrants(member.Grants))
	return requireAffected(result, err)
}

func (s *ChatMessagingStore) RemoveMember(ctx context.Context, roomID, identityID string) error {
	roomUUID, identityUUID, err := parseRoomIdentity(roomID, identityID)
	if err != nil {
		return err
	}
	result, err := s.store.db.ExecContext(ctx, `DELETE FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, roomUUID, identityUUID)
	return requireAffected(result, err)
}

func (s *ChatMessagingStore) Member(ctx context.Context, roomID, identityID string) (messaging.Member, error) {
	roomUUID, identityUUID, err := parseRoomIdentity(roomID, identityID)
	if err != nil {
		return messaging.Member{}, err
	}
	var member messaging.Member
	var role, status string
	err = s.store.db.QueryRowContext(ctx, `SELECT conversation_id::text,user_id::text,role,status,joined_seq,joined_at
		FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, roomUUID, identityUUID).Scan(
		&member.RoomID, &member.IdentityID, &role, &status, &member.HistoryStart, &member.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return messaging.Member{}, messaging.ErrNotFound
	}
	if err != nil {
		return messaging.Member{}, err
	}
	if status != "active" {
		return messaging.Member{}, messaging.ErrNotFound
	}
	member.Grants = grantsForRole(role)
	return member, nil
}

func (s *ChatMessagingStore) RoomsForIdentity(ctx context.Context, identityID string) ([]messaging.Room, error) {
	id, err := parseUUID(identityID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT c.id::text,c.created_at FROM conversation_members m
		JOIN conversations c ON c.id=m.conversation_id
		WHERE m.user_id=$1 AND m.status='active' ORDER BY c.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rooms := make([]messaging.Room, 0)
	for rows.Next() {
		var room messaging.Room
		if err := rows.Scan(&room.ID, &room.CreatedAt); err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

func (s *ChatMessagingStore) Append(ctx context.Context, publish messaging.Publish) (messaging.Event, error) {
	roomID, actorID, err := parseRoomIdentity(publish.RoomID, publish.ActorID)
	if err != nil {
		return messaging.Event{}, err
	}
	publishID, err := parseUUID(publish.ID)
	if err != nil {
		return messaging.Event{}, err
	}
	event, err := s.store.AppendDeliveryEvent(ctx, actorID, roomID, publishID, publish.Name, publish.Data)
	if err != nil {
		return messaging.Event{}, mapDeliveryStoreError(err)
	}
	return messagingEvent(event), nil
}

func (s *ChatMessagingStore) EventsAfter(ctx context.Context, roomID string, sequence int64, limit int) ([]messaging.Event, error) {
	id, err := parseUUID(roomID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `SELECT conversation_id::text,seq,id::text,
		COALESCE(sender_id::text,''),COALESCE(client_event_id::text,''),event_type,payload,created_at
		FROM conversation_events WHERE conversation_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, id, sequence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]messaging.Event, 0)
	for rows.Next() {
		var event messaging.Event
		if err := rows.Scan(&event.RoomID, &event.Sequence, &event.ID, &event.ActorID,
			&event.PublishID, &event.Name, &event.Data, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Profile = messaging.Durable
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *ChatMessagingStore) HeadSequence(ctx context.Context, roomID string) (int64, error) {
	id, err := parseUUID(roomID)
	if err != nil {
		return 0, err
	}
	var sequence int64
	err = s.store.db.QueryRowContext(ctx, `SELECT last_seq FROM conversations WHERE id=$1`, id).Scan(&sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, messaging.ErrNotFound
	}
	return sequence, err
}

func messagingEvent(event Event) messaging.Event {
	delivered := messaging.Event{
		ID: event.ID.String(), RoomID: event.ConversationID.String(), Name: event.Type,
		Profile: messaging.Durable, Sequence: event.Seq, Data: event.Payload, CreatedAt: event.CreatedAt,
	}
	if event.SenderID != nil {
		delivered.ActorID = event.SenderID.String()
	}
	if event.ClientMessageID != nil {
		delivered.PublishID = event.ClientMessageID.String()
	}
	return delivered
}

func grantsForRole(role string) messaging.Grants {
	switch role {
	case "owner":
		return messaging.OwnerGrants()
	case "admin":
		return messaging.Grants{messaging.Receive: true, messaging.PublishEvents: true, messaging.ReadHistory: true, messaging.ManageMembers: true}
	default:
		return messaging.MemberGrants()
	}
}

func roleForGrants(grants messaging.Grants) string {
	if grants.Allows(messaging.ManageRoom) {
		return "owner"
	}
	if grants.Allows(messaging.ManageMembers) {
		return "admin"
	}
	return "member"
}

func parseRoomIdentity(roomID, identityID string) (uuid.UUID, uuid.UUID, error) {
	roomUUID, err := parseUUID(roomID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	identityUUID, err := parseUUID(identityID)
	return roomUUID, identityUUID, err
}

func parseUUID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, messaging.ErrInvalid
	}
	return id, nil
}

func mapDeliveryStoreError(err error) error {
	var pgError *pgconn.PgError
	switch {
	case errors.Is(err, ErrNotFound):
		return messaging.ErrNotFound
	case errors.Is(err, ErrForbidden):
		return messaging.ErrPermissionDenied
	case errors.Is(err, ErrConflict), errors.Is(err, ErrAlreadyMember):
		return messaging.ErrAlreadyExists
	case errors.As(err, &pgError) && pgError.Code == "23505":
		return messaging.ErrAlreadyExists
	default:
		return err
	}
}

func requireAffected(result sql.Result, err error) error {
	if err != nil {
		return mapDeliveryStoreError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return messaging.ErrNotFound
	}
	return nil
}

var _ messaging.Store = (*ChatMessagingStore)(nil)
