package messaging

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lsongdev/chat-server/delivery"
)

type Options struct {
	Authenticate        delivery.Authenticator
	Store               Store
	Bus                 Bus
	Limits              Limits
	OriginCheck         func(*http.Request) bool
	HandleClientPublish ClientPublishHandler
}

// Engine adds reusable room, membership and capability policy around the
// deliberately small delivery kernel.
type Engine struct {
	delivery *delivery.Engine
	store    Store
}

func New(options Options) (*Engine, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("messaging: Store is required")
	}
	adapter := frameworkAdapter{store: options.Store}
	kernel, err := delivery.New(delivery.Options{
		Authenticate: options.Authenticate, Access: adapter, Store: adapter,
		Bus: options.Bus, Limits: options.Limits, OriginCheck: options.OriginCheck,
		HandleClientPublish: options.HandleClientPublish,
	})
	if err != nil {
		return nil, err
	}
	return &Engine{delivery: kernel, store: options.Store}, nil
}

func (e *Engine) Handler() http.Handler { return e.delivery.Handler() }
func (e *Engine) Close() error          { return e.delivery.Close() }

func (e *Engine) Publish(ctx context.Context, publish Publish) (Event, error) {
	return e.delivery.Publish(ctx, publish)
}

func (e *Engine) EventsAfter(ctx context.Context, identityID, roomID string, sequence int64, limit int) ([]Event, error) {
	return e.delivery.EventsAfter(ctx, identityID, roomID, sequence, limit)
}

func (e *Engine) Broadcast(ctx context.Context, event Event) error {
	return e.delivery.Broadcast(ctx, event)
}

func (e *Engine) RefreshIdentity(ctx context.Context, identityID string) error {
	return e.delivery.RefreshIdentity(ctx, identityID)
}

func (e *Engine) InvalidateRoom(roomID string) { e.delivery.InvalidateRoom(roomID) }

func (e *Engine) CreateRoom(ctx context.Context, input CreateRoom) (Room, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.CreatorID = strings.TrimSpace(input.CreatorID)
	if !validID(input.ID) || !validID(input.CreatorID) {
		return Room{}, ErrInvalid
	}
	createdAt := time.Now().UTC()
	room := Room{ID: input.ID, CreatedAt: createdAt}
	creator := Member{RoomID: input.ID, IdentityID: input.CreatorID, Grants: OwnerGrants(), CreatedAt: createdAt}
	if err := e.store.CreateRoom(ctx, room, creator); err != nil {
		return Room{}, err
	}
	_ = e.RefreshIdentity(ctx, input.CreatorID)
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
	member := Member{RoomID: input.RoomID, IdentityID: input.IdentityID, Grants: input.Grants.Clone(), CreatedAt: time.Now().UTC()}
	if err := e.store.AddMember(ctx, member); err != nil {
		return err
	}
	_ = e.RefreshIdentity(ctx, input.IdentityID)
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
	member, err := e.store.Member(ctx, input.RoomID, input.IdentityID)
	if err != nil {
		return err
	}
	member.Grants = input.Grants.Clone()
	if err := e.store.UpdateMember(ctx, member); err != nil {
		return err
	}
	_ = e.RefreshIdentity(ctx, input.IdentityID)
	return nil
}

func (e *Engine) RemoveMember(ctx context.Context, input RemoveMember) error {
	if _, err := e.authorizedMember(ctx, input.RoomID, input.ActorID, ManageMembers); err != nil {
		return err
	}
	if err := e.store.RemoveMember(ctx, input.RoomID, input.IdentityID); err != nil {
		return err
	}
	_ = e.RefreshIdentity(ctx, input.IdentityID)
	return nil
}

func (e *Engine) DeleteRoom(ctx context.Context, actorID, roomID string) error {
	if _, err := e.authorizedMember(ctx, roomID, actorID, ManageRoom); err != nil {
		return err
	}
	if err := e.store.DeleteRoom(ctx, roomID); err != nil {
		return err
	}
	e.InvalidateRoom(roomID)
	return nil
}

func (e *Engine) authorizedMember(ctx context.Context, roomID, identityID string, capabilities ...Capability) (Member, error) {
	if !validID(roomID) || !validID(identityID) {
		return Member{}, ErrInvalid
	}
	member, err := e.store.Member(ctx, roomID, identityID)
	if errors.Is(err, ErrNotFound) {
		return Member{}, ErrPermissionDenied
	}
	if err != nil {
		return Member{}, err
	}
	for _, capability := range capabilities {
		if !member.Grants.Allows(capability) {
			return Member{}, ErrPermissionDenied
		}
	}
	return member, nil
}

func validID(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= 255
}

func validGrants(grants Grants) bool {
	if len(grants) == 0 {
		return false
	}
	for capability, allowed := range grants {
		if allowed && capability != Receive && capability != PublishEvents && capability != ReadHistory && capability != ManageMembers && capability != ManageRoom {
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
