package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultMaxMessageBytes = 64 << 10
	defaultHistoryPageSize = 200
)

var messageNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:\.[a-z][a-z0-9]*)*$`)

type Authenticator func(context.Context, *http.Request) (Identity, error)

type Limits struct {
	MaxMessageBytes int
	MaxResumeRooms  int
}

type Options struct {
	Authenticate        Authenticator
	Store               Store
	Bus                 Bus
	Limits              Limits
	OriginCheck         func(*http.Request) bool
	HandleClientPublish ClientPublishHandler
}

type Engine struct {
	authenticate        Authenticator
	store               Store
	bus                 Bus
	limits              Limits
	originCheck         func(*http.Request) bool
	handleClientPublish ClientPublishHandler
	hub                 *connectionHub
	unsubscribe         func()
	closeOnce           sync.Once
}

func New(options Options) (*Engine, error) {
	if options.Authenticate == nil {
		return nil, fmt.Errorf("delivery: Authenticate is required")
	}
	if options.Store == nil {
		return nil, fmt.Errorf("delivery: Store is required")
	}
	if options.Bus == nil {
		options.Bus = NewMemoryBus()
	}
	if options.Limits.MaxMessageBytes <= 0 {
		options.Limits.MaxMessageBytes = defaultMaxMessageBytes
	}
	if options.Limits.MaxResumeRooms <= 0 {
		options.Limits.MaxResumeRooms = 1000
	}
	if options.HandleClientPublish == nil {
		options.HandleClientPublish = func(_ context.Context, command ClientPublish) (PublishRequest, error) {
			return PublishRequest{
				ID: command.ID, RoomID: command.RoomID, ActorID: command.IdentityID,
				Name: command.Name, Profile: command.Profile, Data: command.Data,
				ExpiresAt: command.ExpiresAt, Stream: command.Stream,
			}, nil
		}
	}
	engine := &Engine{
		authenticate: options.Authenticate, store: options.Store, bus: options.Bus,
		limits: options.Limits, originCheck: options.OriginCheck,
		handleClientPublish: options.HandleClientPublish, hub: newConnectionHub(),
	}
	unsubscribe, err := engine.bus.Subscribe(engine.deliverBusMessage)
	if err != nil {
		return nil, fmt.Errorf("delivery: subscribe bus: %w", err)
	}
	engine.unsubscribe = unsubscribe
	return engine, nil
}

func (e *Engine) Handler() http.Handler { return &webSocketHandler{engine: e} }

func (e *Engine) Close() error {
	var err error
	e.closeOnce.Do(func() {
		if e.unsubscribe != nil {
			e.unsubscribe()
		}
		e.hub.closeAll()
		err = e.bus.Close()
	})
	return err
}

func (e *Engine) CreateRoom(ctx context.Context, input CreateRoom) (Room, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.CreatorID = strings.TrimSpace(input.CreatorID)
	if !validID(input.ID) || !validID(input.CreatorID) {
		return Room{}, ErrInvalid
	}
	createdAt := nowUTC()
	room := Room{ID: input.ID, CreatedAt: createdAt}
	creator := Member{RoomID: input.ID, IdentityID: input.CreatorID, Grants: OwnerGrants(), CreatedAt: createdAt}
	if err := e.store.CreateRoom(ctx, room, creator); err != nil {
		return Room{}, err
	}
	e.refreshIdentity(ctx, input.CreatorID)
	return room, nil
}

func (e *Engine) AddMember(ctx context.Context, input AddMember) error {
	actor, err := e.authorizedMember(ctx, input.RoomID, input.ActorID, ManageMembers)
	if err != nil {
		return err
	}
	if !validID(input.IdentityID) || !validGrants(input.Grants) {
		return ErrInvalid
	}
	if !canGrant(actor.Grants, input.Grants) {
		return ErrPermissionDenied
	}
	member := Member{RoomID: input.RoomID, IdentityID: input.IdentityID, Grants: input.Grants.Clone(), CreatedAt: nowUTC()}
	if err := e.store.AddMember(ctx, member); err != nil {
		return err
	}
	e.refreshIdentity(ctx, input.IdentityID)
	return nil
}

func (e *Engine) UpdateMember(ctx context.Context, input UpdateMember) error {
	actor, err := e.authorizedMember(ctx, input.RoomID, input.ActorID, ManageMembers)
	if err != nil {
		return err
	}
	if !validID(input.IdentityID) || !validGrants(input.Grants) {
		return ErrInvalid
	}
	if !canGrant(actor.Grants, input.Grants) {
		return ErrPermissionDenied
	}
	existing, err := e.store.Member(ctx, input.RoomID, input.IdentityID)
	if err != nil {
		return err
	}
	existing.Grants = input.Grants.Clone()
	if err := e.store.UpdateMember(ctx, existing); err != nil {
		return err
	}
	e.refreshIdentity(ctx, input.IdentityID)
	return nil
}

func (e *Engine) RemoveMember(ctx context.Context, input RemoveMember) error {
	if err := e.require(ctx, input.RoomID, input.ActorID, ManageMembers); err != nil {
		return err
	}
	if err := e.store.RemoveMember(ctx, input.RoomID, input.IdentityID); err != nil {
		return err
	}
	e.refreshIdentity(ctx, input.IdentityID)
	return nil
}

func (e *Engine) DeleteRoom(ctx context.Context, actorID, roomID string) error {
	if err := e.require(ctx, roomID, actorID, ManageRoom); err != nil {
		return err
	}
	if err := e.store.DeleteRoom(ctx, roomID); err != nil {
		return err
	}
	e.hub.removeRoom(roomID)
	return nil
}

func (e *Engine) Publish(ctx context.Context, publish PublishRequest) (Receipt, error) {
	if err := e.validatePublish(publish); err != nil {
		return Receipt{}, err
	}
	if err := e.require(ctx, publish.RoomID, publish.ActorID, Publish); err != nil {
		return Receipt{}, err
	}
	return e.publishAuthorized(ctx, publish)
}

func (e *Engine) publishAuthorized(ctx context.Context, publish PublishRequest) (Receipt, error) {
	switch publish.Profile {
	case Durable:
		message, err := e.store.Append(ctx, publish)
		if err != nil {
			return Receipt{}, err
		}
		if err := e.bus.Publish(ctx, BusMessage{Message: message}); err != nil {
			// The commit remains successful. Cursor recovery repairs missed fanout.
			return Receipt{PublishID: publish.ID, MessageID: message.ID, Status: Committed, Sequence: message.Sequence, Message: &message}, nil
		}
		return Receipt{PublishID: publish.ID, MessageID: message.ID, Status: Committed, Sequence: message.Sequence, Message: &message}, nil
	case Ephemeral:
		message := Message{
			ID: uuid.NewString(), PublishID: publish.ID, RoomID: publish.RoomID,
			ActorID: publish.ActorID, Name: publish.Name, Profile: publish.Profile,
			Data: cloneJSON(publish.Data), CreatedAt: nowUTC(), ExpiresAt: publish.ExpiresAt,
			Stream: publish.Stream,
		}
		if err := e.bus.Publish(ctx, BusMessage{Message: message}); err != nil {
			return Receipt{}, err
		}
		return Receipt{PublishID: publish.ID, MessageID: message.ID, Status: Accepted, Message: &message}, nil
	default:
		return Receipt{}, ErrInvalid
	}
}

func (e *Engine) publishFromClient(ctx context.Context, command ClientPublish) (Receipt, error) {
	if err := e.require(ctx, command.RoomID, command.IdentityID, Publish); err != nil {
		return Receipt{}, err
	}
	publish, err := e.handleClientPublish(ctx, command)
	if err != nil {
		return Receipt{}, err
	}
	// Authentication owns actor identity and the incoming room is the authorization scope.
	publish.ActorID = command.IdentityID
	publish.RoomID = command.RoomID
	// The stable idempotency key belongs to the client envelope. A business
	// validator may transform name/data/profile, but cannot rewrite identity,
	// authorization scope, or acknowledgement correlation.
	publish.ID = command.ID
	if err := e.validatePublish(publish); err != nil {
		return Receipt{}, err
	}
	return e.publishAuthorized(ctx, publish)
}

func (e *Engine) EventsAfter(ctx context.Context, identityID, roomID string, sequence int64, limit int) ([]Message, error) {
	member, err := e.authorizedMember(ctx, roomID, identityID, Receive, ReadHistory)
	if err != nil {
		return nil, err
	}
	if sequence < 0 {
		return nil, ErrInvalid
	}
	sequence = max(sequence, member.HistoryStart-1)
	if limit <= 0 || limit > defaultHistoryPageSize {
		limit = defaultHistoryPageSize
	}
	return e.store.EventsAfter(ctx, roomID, sequence, limit)
}

// NotifyCommitted fans out a durable event that was atomically committed by
// the host application's own transaction. It does not write to Store again.
func (e *Engine) NotifyCommitted(ctx context.Context, message Message) error {
	if message.Profile != Durable || !validID(message.ID) || !validID(message.RoomID) ||
		message.Sequence <= 0 || !messageNamePattern.MatchString(message.Name) ||
		len(message.Data) == 0 || len(message.Data) > e.limits.MaxMessageBytes || !json.Valid(message.Data) {
		return ErrInvalid
	}
	return e.bus.Publish(ctx, BusMessage{Message: message})
}

// RefreshIdentity applies current Store membership to every live connection
// for identityID. Hosts call it after committing membership changes outside
// Engine's generic room API.
func (e *Engine) RefreshIdentity(ctx context.Context, identityID string) error {
	if !validID(identityID) {
		return ErrInvalid
	}
	rooms, err := e.store.RoomsForIdentity(ctx, identityID)
	if err != nil {
		return err
	}
	roomIDs := make([]string, 0, len(rooms))
	for _, room := range rooms {
		roomIDs = append(roomIDs, room.ID)
	}
	e.hub.replaceIdentityRooms(identityID, roomIDs)
	return nil
}

// RemoveRoomRouting revokes a deleted room from every live connection.
func (e *Engine) RemoveRoomRouting(roomID string) {
	e.hub.removeRoom(roomID)
}

func (e *Engine) require(ctx context.Context, roomID, identityID string, capabilities ...Capability) error {
	_, err := e.authorizedMember(ctx, roomID, identityID, capabilities...)
	return err
}

func (e *Engine) authorizedMember(ctx context.Context, roomID, identityID string, capabilities ...Capability) (Member, error) {
	if !validID(roomID) || !validID(identityID) {
		return Member{}, ErrInvalid
	}
	member, err := e.store.Member(ctx, roomID, identityID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Member{}, ErrPermissionDenied
		}
		return Member{}, err
	}
	for _, capability := range capabilities {
		if !member.Grants.Allows(capability) {
			return Member{}, ErrPermissionDenied
		}
	}
	return member, nil
}

func (e *Engine) validatePublish(publish PublishRequest) error {
	if !validID(publish.ID) || !validID(publish.RoomID) || !validID(publish.ActorID) ||
		!messageNamePattern.MatchString(publish.Name) || len(publish.Data) == 0 ||
		len(publish.Data) > e.limits.MaxMessageBytes || !json.Valid(publish.Data) {
		return ErrInvalid
	}
	if publish.Profile != Durable && publish.Profile != Ephemeral {
		return ErrInvalid
	}
	if publish.Profile == Durable && (publish.ExpiresAt != nil || publish.Stream != nil) {
		return ErrInvalid
	}
	if publish.Stream != nil {
		return ErrInvalid
	}
	if publish.ExpiresAt != nil && publish.ExpiresAt.Before(time.Now()) {
		return ErrInvalid
	}
	return nil
}

func (e *Engine) deliverBusMessage(busMessage BusMessage) {
	message := busMessage.Message
	if message.ExpiresAt != nil && time.Now().After(*message.ExpiresAt) {
		return
	}
	e.hub.broadcast(message.RoomID, wireEvent(message))
}

func (e *Engine) refreshIdentity(ctx context.Context, identityID string) {
	_ = e.RefreshIdentity(ctx, identityID)
}

func validID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 255
}

func validGrants(grants Grants) bool {
	if len(grants) == 0 {
		return false
	}
	for capability, allowed := range grants {
		if !allowed {
			continue
		}
		switch capability {
		case Receive, Publish, ReadHistory, ManageMembers, ManageRoom:
		default:
			return false
		}
	}
	return true
}

func canGrant(actor, requested Grants) bool {
	for capability, allowed := range requested {
		if allowed && !actor.Allows(capability) {
			return false
		}
	}
	return true
}

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
