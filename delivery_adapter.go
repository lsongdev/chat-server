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
	"github.com/lsongdev/chat-server/delivery"
)

// ChatDeliveryStore adapts the existing compact Chat schema to Delivery Core.
// Conversation metadata remains owned by Chat; rooms, active memberships and
// the durable event cursor are projections of those same rows, not duplicates.
type ChatDeliveryStore struct{ store *Store }

const rtcSignalTTL = 30 * time.Second

func NewChatDeliveryStore(store *Store) *ChatDeliveryStore {
	return &ChatDeliveryStore{store: store}
}

// chatClientPublish is the business boundary for untrusted Delivery packets.
// The generic engine owns transport and authorization; Chat owns which facts a
// client may create and the payload rules for those facts.
func chatClientPublish(maxMessageBytes int) delivery.ClientPublishHandler {
	return func(_ context.Context, command delivery.ClientPublish) (delivery.PublishRequest, error) {
		publish := delivery.PublishRequest{
			ID: command.ID, RoomID: command.RoomID, ActorID: command.IdentityID,
			Name: command.Name, Profile: command.Profile, Data: command.Data,
		}
		switch {
		case command.Name == "message.created" && command.Profile == delivery.Durable:
			if !validMessageContent(command.Data, maxMessageBytes) {
				return delivery.PublishRequest{}, delivery.ErrInvalid
			}
			return publish, nil
		case command.Name == "rtc.signal" && command.Profile == delivery.Ephemeral:
			var signal struct {
				Type string          `json:"type"`
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(command.Data, &signal) != nil || !strings.HasPrefix(signal.Type, "webrtc:") ||
				len(signal.Data) == 0 || string(signal.Data) == "null" {
				return delivery.PublishRequest{}, delivery.ErrInvalid
			}
			expiresAt := time.Now().UTC().Add(rtcSignalTTL)
			publish.ExpiresAt = &expiresAt
			return publish, nil
		default:
			return delivery.PublishRequest{}, delivery.ErrInvalid
		}
	}
}

func (s *ChatDeliveryStore) CreateRoom(ctx context.Context, room delivery.Room, creator delivery.Member) error {
	roomID, creatorID, err := parseRoomIdentity(room.ID, creator.IdentityID)
	if err != nil {
		return err
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversations(id,created_by,last_seq,created_at,updated_at)
		VALUES($1,$2,0,$3,$3)`, roomID, creatorID, room.CreatedAt); err != nil {
		return mapDeliveryDatabaseError(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_members(conversation_id,user_id,role,joined_seq,last_read_seq,status,joined_at,updated_at)
		VALUES($1,$2,'owner',0,0,'active',$3,$3)`, roomID, creatorID, creator.CreatedAt); err != nil {
		return mapDeliveryDatabaseError(err)
	}
	return tx.Commit()
}

func (s *ChatDeliveryStore) DeleteRoom(ctx context.Context, roomID string) error {
	id, err := parseUUID(roomID)
	if err != nil {
		return err
	}
	result, err := s.store.db.ExecContext(ctx, `DELETE FROM conversations WHERE id=$1`, id)
	return mapDeliveryResult(result, err)
}

func (s *ChatDeliveryStore) Room(ctx context.Context, roomID string) (delivery.Room, error) {
	id, err := parseUUID(roomID)
	if err != nil {
		return delivery.Room{}, err
	}
	var room delivery.Room
	err = s.store.db.QueryRowContext(ctx, `SELECT id::text,created_at FROM conversations WHERE id=$1`, id).Scan(&room.ID, &room.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery.Room{}, delivery.ErrNotFound
	}
	return room, err
}

func (s *ChatDeliveryStore) AddMember(ctx context.Context, member delivery.Member) error {
	roomID, identityID, err := parseRoomIdentity(member.RoomID, member.IdentityID)
	if err != nil {
		return err
	}
	role := roleForGrants(member.Grants)
	result, err := s.store.db.ExecContext(ctx, `
		INSERT INTO conversation_members(conversation_id,user_id,role,joined_seq,last_read_seq,status,joined_at,updated_at)
		SELECT $1,$2,$3,last_seq,last_seq,'active',$4,$4 FROM conversations WHERE id=$1`,
		roomID, identityID, role, member.CreatedAt)
	return mapDeliveryResult(result, err)
}

func (s *ChatDeliveryStore) UpdateMember(ctx context.Context, member delivery.Member) error {
	roomID, identityID, err := parseRoomIdentity(member.RoomID, member.IdentityID)
	if err != nil {
		return err
	}
	result, err := s.store.db.ExecContext(ctx, `
		UPDATE conversation_members SET role=$3,updated_at=now()
		WHERE conversation_id=$1 AND user_id=$2 AND status='active'`, roomID, identityID, roleForGrants(member.Grants))
	return mapDeliveryResult(result, err)
}

func (s *ChatDeliveryStore) RemoveMember(ctx context.Context, roomID, identityID string) error {
	roomUUID, identityUUID, err := parseRoomIdentity(roomID, identityID)
	if err != nil {
		return err
	}
	result, err := s.store.db.ExecContext(ctx, `DELETE FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, roomUUID, identityUUID)
	return mapDeliveryResult(result, err)
}

func (s *ChatDeliveryStore) Member(ctx context.Context, roomID, identityID string) (delivery.Member, error) {
	roomUUID, identityUUID, err := parseRoomIdentity(roomID, identityID)
	if err != nil {
		return delivery.Member{}, err
	}
	var member delivery.Member
	var role, status string
	err = s.store.db.QueryRowContext(ctx, `
		SELECT conversation_id::text,user_id::text,role,status,joined_seq,joined_at
		FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, roomUUID, identityUUID).Scan(
		&member.RoomID, &member.IdentityID, &role, &status, &member.HistoryStart, &member.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery.Member{}, delivery.ErrNotFound
	}
	if err != nil {
		return delivery.Member{}, err
	}
	if status != "active" {
		return delivery.Member{}, delivery.ErrNotFound
	}
	member.Grants = grantsForRole(role)
	return member, nil
}

func (s *ChatDeliveryStore) RoomsForIdentity(ctx context.Context, identityID string) ([]delivery.Room, error) {
	id, err := parseUUID(identityID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `
		SELECT c.id::text,c.created_at FROM conversation_members m
		JOIN conversations c ON c.id=m.conversation_id
		WHERE m.user_id=$1 AND m.status='active' ORDER BY c.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rooms := make([]delivery.Room, 0)
	for rows.Next() {
		var room delivery.Room
		if err := rows.Scan(&room.ID, &room.CreatedAt); err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

func (s *ChatDeliveryStore) Append(ctx context.Context, publish delivery.PublishRequest) (delivery.Message, error) {
	roomID, actorID, err := parseRoomIdentity(publish.RoomID, publish.ActorID)
	if err != nil {
		return delivery.Message{}, err
	}
	publishID, err := parseUUID(publish.ID)
	if err != nil {
		return delivery.Message{}, err
	}
	event, err := s.store.AppendDeliveryEvent(ctx, actorID, roomID, publishID, publish.Name, publish.Data)
	if err != nil {
		return delivery.Message{}, mapDeliveryStoreError(err)
	}
	return deliveryMessage(event), nil
}

func (s *ChatDeliveryStore) EventsAfter(ctx context.Context, roomID string, sequence int64, limit int) ([]delivery.Message, error) {
	id, err := parseUUID(roomID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.db.QueryContext(ctx, `
		SELECT conversation_id::text,seq,id::text,COALESCE(sender_id::text,''),
			COALESCE(client_event_id::text,''),event_type,payload,created_at
		FROM conversation_events WHERE conversation_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, id, sequence, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := make([]delivery.Message, 0)
	for rows.Next() {
		var message delivery.Message
		if err := rows.Scan(&message.RoomID, &message.Sequence, &message.ID, &message.ActorID,
			&message.PublishID, &message.Name, &message.Data, &message.CreatedAt); err != nil {
			return nil, err
		}
		message.Profile = delivery.Durable
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *ChatDeliveryStore) HeadSequence(ctx context.Context, roomID string) (int64, error) {
	id, err := parseUUID(roomID)
	if err != nil {
		return 0, err
	}
	var sequence int64
	err = s.store.db.QueryRowContext(ctx, `SELECT last_seq FROM conversations WHERE id=$1`, id).Scan(&sequence)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, delivery.ErrNotFound
	}
	return sequence, err
}

func deliveryMessage(event Event) delivery.Message {
	message := delivery.Message{
		ID: event.ID.String(), RoomID: event.ConversationID.String(), Name: event.Type,
		Profile: delivery.Durable, Sequence: event.Seq, Data: event.Payload, CreatedAt: event.CreatedAt,
	}
	if event.SenderID != nil {
		message.ActorID = event.SenderID.String()
	}
	if event.ClientMessageID != nil {
		message.PublishID = event.ClientMessageID.String()
	}
	return message
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
		return uuid.Nil, delivery.ErrInvalid
	}
	return id, nil
}

func grantsForRole(role string) delivery.Grants {
	switch role {
	case "owner":
		return delivery.OwnerGrants()
	case "admin":
		return delivery.Grants{delivery.Receive: true, delivery.Publish: true, delivery.ReadHistory: true, delivery.ManageMembers: true}
	default:
		return delivery.MemberGrants()
	}
}

func roleForGrants(grants delivery.Grants) string {
	if grants.Allows(delivery.ManageRoom) {
		return "owner"
	}
	if grants.Allows(delivery.ManageMembers) {
		return "admin"
	}
	return "member"
}

func mapDeliveryStoreError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return delivery.ErrNotFound
	case errors.Is(err, ErrForbidden):
		return delivery.ErrPermissionDenied
	case errors.Is(err, ErrConflict), errors.Is(err, ErrAlreadyMember):
		return delivery.ErrAlreadyExists
	default:
		return err
	}
}

func mapDeliveryDatabaseError(err error) error {
	if err == nil {
		return nil
	}
	// Database-specific constraint details stay behind the adapter. Callers can
	// retry stable publish IDs, while resource creation conflicts are surfaced.
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return delivery.ErrAlreadyExists
	}
	return err
}

func mapDeliveryResult(result sql.Result, err error) error {
	if err != nil {
		return mapDeliveryDatabaseError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return delivery.ErrNotFound
	}
	return nil
}

var _ delivery.Store = (*ChatDeliveryStore)(nil)
