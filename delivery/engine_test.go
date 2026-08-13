package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testEngine(t *testing.T) (*Engine, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	engine, err := New(Options{
		Authenticate: func(_ context.Context, request *http.Request) (Identity, error) {
			identityID := request.Header.Get("X-Identity")
			if identityID == "" {
				return Identity{}, ErrPermissionDenied
			}
			return Identity{ID: identityID}, nil
		},
		Store: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, store
}

func TestEngineRoomPermissionsAndIdempotentPublish(t *testing.T) {
	engine, _ := testEngine(t)
	ctx := context.Background()
	if _, err := engine.CreateRoom(ctx, CreateRoom{ID: "room-1", CreatorID: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.AddMember(ctx, AddMember{ActorID: "alice", RoomID: "room-1", IdentityID: "bob", Grants: MemberGrants()}); err != nil {
		t.Fatal(err)
	}
	if err := engine.AddMember(ctx, AddMember{ActorID: "bob", RoomID: "room-1", IdentityID: "mallory", Grants: MemberGrants()}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("member management error = %v, want permission denied", err)
	}
	adminGrants := Grants{Receive: true, Publish: true, ReadHistory: true, ManageMembers: true}
	if err := engine.AddMember(ctx, AddMember{ActorID: "alice", RoomID: "room-1", IdentityID: "admin", Grants: adminGrants}); err != nil {
		t.Fatal(err)
	}
	if err := engine.AddMember(ctx, AddMember{ActorID: "admin", RoomID: "room-1", IdentityID: "owner-2", Grants: OwnerGrants()}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("capability escalation error = %v, want permission denied", err)
	}

	publish := PublishRequest{
		ID: "publish-1", RoomID: "room-1", ActorID: "bob", Name: "message.created",
		Profile: Durable, Data: json.RawMessage(`{"text":"hello"}`),
	}
	first, err := engine.Publish(ctx, publish)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Publish(ctx, publish)
	if err != nil {
		t.Fatal(err)
	}
	if first.PublishID != second.PublishID || first.MessageID != second.MessageID ||
		first.Sequence != second.Sequence || first.Status != Committed || first.Sequence != 1 {
		t.Fatalf("receipts = %#v and %#v", first, second)
	}
	events, err := engine.EventsAfter(ctx, "bob", "room-1", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].PublishID != "publish-1" {
		t.Fatalf("events = %#v", events)
	}
}

func TestMemoryStoreAllocatesContiguousSequenceConcurrently(t *testing.T) {
	store := NewMemoryStore()
	createdAt := nowUTC()
	if err := store.CreateRoom(context.Background(), Room{ID: "room", CreatedAt: createdAt}, Member{
		RoomID: "room", IdentityID: "alice", Grants: OwnerGrants(), CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	const count = 64
	sequences := make(chan int64, count)
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			message, err := store.Append(context.Background(), PublishRequest{
				ID:     "publish-" + strings.Repeat("x", index) + string(rune('a'+index%26)),
				RoomID: "room", ActorID: "alice", Name: "message.created",
				Profile: Durable, Data: json.RawMessage(`{"ok":true}`),
			})
			if err != nil {
				t.Errorf("append %d: %v", index, err)
				return
			}
			sequences <- message.Sequence
		}(index)
	}
	group.Wait()
	close(sequences)
	got := make([]int, 0, count)
	for sequence := range sequences {
		got = append(got, int(sequence))
	}
	sort.Ints(got)
	for index, sequence := range got {
		if sequence != index+1 {
			t.Fatalf("sequence[%d] = %d", index, sequence)
		}
	}
}

func TestWebSocketPublishFanoutAndResume(t *testing.T) {
	engine, _ := testEngine(t)
	ctx := context.Background()
	if _, err := engine.CreateRoom(ctx, CreateRoom{ID: "room-1", CreatorID: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.AddMember(ctx, AddMember{ActorID: "alice", RoomID: "room-1", IdentityID: "bob", Grants: MemberGrants()}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine.Handler())
	t.Cleanup(server.Close)

	alice := dialDelivery(t, server.URL, "alice")
	defer alice.Close()
	bob := dialDelivery(t, server.URL, "bob")
	readUntilOp(t, alice, "hello")
	readUntilOp(t, bob, "hello")
	if err := alice.WriteJSON(map[string]any{
		"op": "publish", "id": "client-1", "room_id": "room-1",
		"name": "message.created", "profile": "durable", "data": map[string]any{"text": "hello"},
	}); err != nil {
		t.Fatal(err)
	}
	aliceMessages := readUntilOps(t, alice, "ack", "event")
	bobEvent := readUntilOp(t, bob, "event")
	if aliceMessages["ack"]["status"] != "committed" || bobEvent["sequence"] != float64(1) {
		t.Fatalf("alice = %#v, bob event = %#v", aliceMessages, bobEvent)
	}
	if bobEvent["publish_id"] != "client-1" || bobEvent["actor_id"] != "alice" {
		t.Fatalf("event = %#v", bobEvent)
	}
	_ = bob.Close()

	bob = dialDelivery(t, server.URL, "bob")
	defer bob.Close()
	readUntilOp(t, bob, "hello")
	if err := bob.WriteJSON(map[string]any{"op": "resume", "rooms": map[string]any{"room-1": 0}}); err != nil {
		t.Fatal(err)
	}
	messages := readUntilOps(t, bob, "sync.begin", "event", "sync.end")
	if messages["event"]["recovered"] != true || messages["sync.end"]["sequence"] != float64(1) {
		t.Fatalf("resume messages = %#v", messages)
	}
}

func TestWebSocketAcceptsUTF8JSONBinaryFrame(t *testing.T) {
	engine, _ := testEngine(t)
	ctx := context.Background()
	if _, err := engine.CreateRoom(ctx, CreateRoom{ID: "room-1", CreatorID: "alice"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine.Handler())
	defer server.Close()
	alice := dialDelivery(t, server.URL, "alice")
	defer alice.Close()
	readUntilOp(t, alice, "hello")
	payload := []byte(`{"op":"publish","id":"binary-1","room_id":"room-1","name":"message.created","profile":"durable","data":{"text":"hello"}}`)
	if err := alice.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatal(err)
	}
	messages := readUntilOps(t, alice, "ack", "event")
	if messages["ack"]["id"] != "binary-1" || messages["event"]["publish_id"] != "binary-1" {
		t.Fatalf("binary publish = %#v", messages)
	}
}

func TestWebSocketEphemeralIsNotRecoveredAndMembershipRevokesRoute(t *testing.T) {
	engine, _ := testEngine(t)
	ctx := context.Background()
	_, _ = engine.CreateRoom(ctx, CreateRoom{ID: "room-1", CreatorID: "alice"})
	if err := engine.AddMember(ctx, AddMember{ActorID: "alice", RoomID: "room-1", IdentityID: "bob", Grants: MemberGrants()}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine.Handler())
	t.Cleanup(server.Close)
	alice := dialDelivery(t, server.URL, "alice")
	defer alice.Close()
	bob := dialDelivery(t, server.URL, "bob")
	defer bob.Close()
	readUntilOp(t, alice, "hello")
	readUntilOp(t, bob, "hello")

	if err := alice.WriteJSON(map[string]any{
		"op": "publish", "id": "signal-1", "room_id": "room-1",
		"name": "rtc.signal", "profile": "ephemeral", "data": map[string]any{"kind": "ice"},
	}); err != nil {
		t.Fatal(err)
	}
	readUntilOps(t, alice, "ack", "event")
	if event := readUntilOp(t, bob, "event"); event["profile"] != "ephemeral" {
		t.Fatalf("event = %#v", event)
	}
	if err := engine.RemoveMember(ctx, RemoveMember{ActorID: "alice", RoomID: "room-1", IdentityID: "bob"}); err != nil {
		t.Fatal(err)
	}
	removed := readUntilOp(t, bob, "room.removed")
	if removed["room_id"] != "room-1" {
		t.Fatalf("removed = %#v", removed)
	}
	if err := bob.WriteJSON(map[string]any{
		"op": "publish", "id": "blocked", "room_id": "room-1",
		"name": "message.created", "profile": "durable", "data": map[string]any{"text": "no"},
	}); err != nil {
		t.Fatal(err)
	}
	denied := readUntilOp(t, bob, "error")
	errorValue := denied["error"].(map[string]any)
	if errorValue["code"] != "permission_denied" {
		t.Fatalf("error = %#v", denied)
	}
}

func TestWebSocketRequiresDeliverySubprotocol(t *testing.T) {
	engine, _ := testEngine(t)
	server := httptest.NewServer(engine.Handler())
	defer server.Close()
	header := http.Header{"X-Identity": []string{"alice"}}
	_, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), header)
	if err == nil {
		t.Fatal("connection without delivery.v1 unexpectedly succeeded")
	}
	if response == nil || response.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("response = %#v, want status %d", response, http.StatusUpgradeRequired)
	}
}

func dialDelivery(t *testing.T, serverURL, identityID string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(serverURL, "http")
	header := http.Header{"X-Identity": []string{identityID}}
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{Subprotocol}
	connection, response, err := dialer.Dial(url, header)
	if err != nil {
		if response != nil {
			t.Fatalf("dial: %v (status %d)", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	return connection
}

func readUntilOp(t *testing.T, connection *websocket.Conn, wanted string) map[string]any {
	t.Helper()
	for attempts := 0; attempts < 20; attempts++ {
		var message map[string]any
		if err := connection.ReadJSON(&message); err != nil {
			t.Fatalf("read %s: %v", wanted, err)
		}
		if message["op"] == wanted {
			return message
		}
	}
	t.Fatalf("did not receive %s", wanted)
	return nil
}

func readUntilOps(t *testing.T, connection *websocket.Conn, wanted ...string) map[string]map[string]any {
	t.Helper()
	remaining := make(map[string]struct{}, len(wanted))
	for _, op := range wanted {
		remaining[op] = struct{}{}
	}
	result := make(map[string]map[string]any, len(wanted))
	for attempts := 0; attempts < 40 && len(remaining) > 0; attempts++ {
		var message map[string]any
		if err := connection.ReadJSON(&message); err != nil {
			t.Fatalf("read %v: %v", wanted, err)
		}
		op, _ := message["op"].(string)
		if _, exists := remaining[op]; exists {
			result[op] = message
			delete(remaining, op)
		}
	}
	if len(remaining) != 0 {
		t.Fatalf("missing operations: %v", remaining)
	}
	return result
}
