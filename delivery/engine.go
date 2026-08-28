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
	Access              Access
	Store               Store
	Bus                 Bus
	Limits              Limits
	OriginCheck         func(*http.Request) bool
	HandleClientPublish ClientPublishHandler
}

type Engine struct {
	authenticate        Authenticator
	access              Access
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
	if options.Access == nil {
		return nil, fmt.Errorf("delivery: Access is required")
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
		options.HandleClientPublish = func(_ context.Context, command ClientPublish) (Publish, error) {
			return Publish{
				ID: command.ID, RoomID: command.RoomID, ActorID: command.IdentityID,
				Name: command.Name, Profile: command.Profile, Data: command.Data,
			}, nil
		}
	}
	engine := &Engine{
		authenticate: options.Authenticate, access: options.Access,
		store: options.Store, bus: options.Bus, limits: options.Limits,
		originCheck: options.OriginCheck, handleClientPublish: options.HandleClientPublish,
		hub: newConnectionHub(),
	}
	unsubscribe, err := engine.bus.Subscribe(engine.deliverBusEvent)
	if err != nil {
		return nil, fmt.Errorf("delivery: subscribe bus: %w", err)
	}
	engine.unsubscribe = unsubscribe
	return engine, nil
}

func (e *Engine) Handler() http.Handler { return &webSocketHandler{engine: e} }

func (e *Engine) Close() error {
	e.closeOnce.Do(func() {
		if e.unsubscribe != nil {
			e.unsubscribe()
		}
		e.hub.closeAll()
	})
	return nil
}

// Publish validates, authorizes, persists/fans out and returns the resulting
// event. The layer above owns routing policy; Delivery owns transport
// semantics such as idempotency, commit-before-ack and realtime fanout.
func (e *Engine) Publish(ctx context.Context, publish Publish) (Event, error) {
	if err := e.validatePublish(publish); err != nil {
		return Event{}, err
	}
	if err := e.canPublish(ctx, publish.ActorID, publish.RoomID); err != nil {
		return Event{}, err
	}
	return e.publishAuthorized(ctx, publish)
}

func (e *Engine) publishAuthorized(ctx context.Context, publish Publish) (Event, error) {
	switch publish.Profile {
	case Durable:
		event, err := e.store.Append(ctx, publish)
		if err != nil {
			return Event{}, err
		}
		// The commit remains successful if live fanout fails. Cursor recovery
		// repairs the missed notification.
		_ = e.bus.Publish(ctx, event)
		return event, nil
	case Ephemeral:
		event := Event{
			ID: uuid.NewString(), PublishID: publish.ID, RoomID: publish.RoomID,
			ActorID: publish.ActorID, Name: publish.Name, Profile: Ephemeral,
			Data: cloneJSON(publish.Data), CreatedAt: nowUTC(), ExpiresAt: publish.ExpiresAt,
		}
		if err := e.bus.Publish(ctx, event); err != nil {
			return Event{}, err
		}
		return event, nil
	default:
		return Event{}, ErrInvalid
	}
}

func (e *Engine) publishFromClient(ctx context.Context, command ClientPublish) (Event, error) {
	if err := e.canPublish(ctx, command.IdentityID, command.RoomID); err != nil {
		return Event{}, err
	}
	publish, err := e.handleClientPublish(ctx, command)
	if err != nil {
		return Event{}, err
	}
	// Authentication and the wire envelope own these security boundaries.
	publish.ActorID = command.IdentityID
	publish.RoomID = command.RoomID
	publish.ID = command.ID
	if err := e.validatePublish(publish); err != nil {
		return Event{}, err
	}
	return e.publishAuthorized(ctx, publish)
}

func (e *Engine) EventsAfter(ctx context.Context, identityID, roomID string, sequence int64, limit int) ([]Event, error) {
	historyStart, err := e.historyStart(ctx, identityID, roomID)
	if err != nil {
		return nil, err
	}
	if sequence < 0 {
		return nil, ErrInvalid
	}
	sequence = max(sequence, historyStart-1)
	if limit <= 0 || limit > defaultHistoryPageSize {
		limit = defaultHistoryPageSize
	}
	return e.store.EventsAfter(ctx, roomID, sequence, limit)
}

// Broadcast fans out a durable event committed by the host's own transaction.
// It does not append the event again.
func (e *Engine) Broadcast(ctx context.Context, event Event) error {
	if event.Profile != Durable || !validID(event.ID) || !validID(event.RoomID) ||
		event.Sequence <= 0 || !messageNamePattern.MatchString(event.Name) ||
		len(event.Data) == 0 || len(event.Data) > e.limits.MaxMessageBytes || !json.Valid(event.Data) {
		return ErrInvalid
	}
	return e.bus.Publish(ctx, event)
}

// RefreshIdentity reapplies the host's current room routing to live sockets.
func (e *Engine) RefreshIdentity(ctx context.Context, identityID string) error {
	if !validID(identityID) {
		return ErrInvalid
	}
	rooms, err := e.access.Routes(ctx, identityID)
	if err != nil {
		return err
	}
	for _, roomID := range rooms {
		if !validID(roomID) {
			return ErrInvalid
		}
	}
	e.hub.replaceIdentityRooms(identityID, rooms)
	return nil
}

// InvalidateRoom removes a deleted room from all live routing projections.
func (e *Engine) InvalidateRoom(roomID string) {
	e.hub.removeRoom(roomID)
}

func (e *Engine) canPublish(ctx context.Context, identityID, roomID string) error {
	if !validID(identityID) || !validID(roomID) {
		return ErrInvalid
	}
	err := e.access.CanPublish(ctx, identityID, roomID)
	if errors.Is(err, ErrNotFound) {
		return ErrPermissionDenied
	}
	return err
}

func (e *Engine) historyStart(ctx context.Context, identityID, roomID string) (int64, error) {
	if !validID(identityID) || !validID(roomID) {
		return 0, ErrInvalid
	}
	start, err := e.access.HistoryStart(ctx, identityID, roomID)
	if errors.Is(err, ErrNotFound) {
		return 0, ErrPermissionDenied
	}
	if err != nil {
		return 0, err
	}
	if start < 0 {
		return 0, ErrInvalid
	}
	return start, nil
}

func (e *Engine) validatePublish(publish Publish) error {
	if !validID(publish.ID) || !validID(publish.RoomID) || !validID(publish.ActorID) ||
		!messageNamePattern.MatchString(publish.Name) || len(publish.Data) == 0 ||
		len(publish.Data) > e.limits.MaxMessageBytes || !json.Valid(publish.Data) {
		return ErrInvalid
	}
	if publish.Profile != Durable && publish.Profile != Ephemeral {
		return ErrInvalid
	}
	if publish.Profile == Durable && publish.ExpiresAt != nil {
		return ErrInvalid
	}
	if publish.ExpiresAt != nil && publish.ExpiresAt.Before(time.Now()) {
		return ErrInvalid
	}
	return nil
}

func (e *Engine) deliverBusEvent(event Event) {
	if event.ExpiresAt != nil && time.Now().After(*event.ExpiresAt) {
		return
	}
	e.hub.broadcast(event.RoomID, wireEvent(event))
}

func validID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 255
}

func cloneJSON(value []byte) []byte { return append([]byte(nil), value...) }

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
