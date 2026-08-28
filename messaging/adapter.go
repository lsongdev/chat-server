package messaging

import (
	"context"
	"errors"

	"github.com/lsongdev/chat-server/delivery"
)

// frameworkAdapter translates generic messaging policy into the three narrow
// authorization questions asked by the delivery kernel.
type frameworkAdapter struct{ store Store }

func (a frameworkAdapter) Routes(ctx context.Context, identityID string) ([]string, error) {
	rooms, err := a.store.RoomsForIdentity(ctx, identityID)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(rooms))
	for _, room := range rooms {
		member, err := a.store.Member(ctx, room.ID, identityID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		if member.Grants.Allows(Receive) {
			result = append(result, room.ID)
		}
	}
	return result, nil
}

func (a frameworkAdapter) CanPublish(ctx context.Context, identityID, roomID string) error {
	member, err := a.store.Member(ctx, roomID, identityID)
	if err != nil {
		return err
	}
	if !member.Grants.Allows(PublishEvents) {
		return ErrPermissionDenied
	}
	return nil
}

func (a frameworkAdapter) HistoryStart(ctx context.Context, identityID, roomID string) (int64, error) {
	member, err := a.store.Member(ctx, roomID, identityID)
	if err != nil {
		return 0, err
	}
	if !member.Grants.Allows(Receive) || !member.Grants.Allows(ReadHistory) {
		return 0, ErrPermissionDenied
	}
	return member.HistoryStart, nil
}

func (a frameworkAdapter) Append(ctx context.Context, publish Publish) (Event, error) {
	return a.store.Append(ctx, publish)
}

func (a frameworkAdapter) EventsAfter(ctx context.Context, roomID string, sequence int64, limit int) ([]Event, error) {
	return a.store.EventsAfter(ctx, roomID, sequence, limit)
}

func (a frameworkAdapter) HeadSequence(ctx context.Context, roomID string) (int64, error) {
	return a.store.HeadSequence(ctx, roomID)
}

var _ delivery.Access = frameworkAdapter{}
var _ delivery.Store = frameworkAdapter{}
