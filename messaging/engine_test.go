package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestFrameworkOwnsRoomMembershipAndCapabilities(t *testing.T) {
	store := NewMemoryStore()
	engine, err := New(Options{
		Authenticate: func(_ context.Context, request *http.Request) (Identity, error) {
			return Identity{ID: request.Header.Get("X-Identity")}, nil
		},
		Store: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.CreateRoom(ctx, CreateRoom{ID: "room-1", CreatorID: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.AddMember(ctx, AddMember{ActorID: "alice", RoomID: "room-1", IdentityID: "bob", Grants: MemberGrants()}); err != nil {
		t.Fatal(err)
	}
	if err := engine.AddMember(ctx, AddMember{ActorID: "bob", RoomID: "room-1", IdentityID: "mallory", Grants: MemberGrants()}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("member management error = %v", err)
	}
	admin := Grants{Receive: true, PublishEvents: true, ReadHistory: true, ManageMembers: true}
	if err := engine.AddMember(ctx, AddMember{ActorID: "alice", RoomID: "room-1", IdentityID: "admin", Grants: admin}); err != nil {
		t.Fatal(err)
	}
	if err := engine.AddMember(ctx, AddMember{ActorID: "admin", RoomID: "room-1", IdentityID: "owner-2", Grants: OwnerGrants()}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("capability escalation error = %v", err)
	}
	publish := Publish{ID: "publish-1", RoomID: "room-1", ActorID: "bob", Name: "message.created", Profile: Durable, Data: json.RawMessage(`{"text":"hello"}`)}
	first, err := engine.Publish(ctx, publish)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Publish(ctx, publish)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Sequence != 1 || second.Sequence != 1 {
		t.Fatalf("events = %#v and %#v", first, second)
	}
}
