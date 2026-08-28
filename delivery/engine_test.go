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

type testPolicy struct {
	mu      sync.RWMutex
	rooms   map[string]map[string]int64
	blocked map[string]bool
}

func newTestPolicy() *testPolicy {
	return &testPolicy{rooms: make(map[string]map[string]int64), blocked: make(map[string]bool)}
}

func (p *testPolicy) grant(identityID, roomID string, historyStart int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rooms[identityID] == nil {
		p.rooms[identityID] = make(map[string]int64)
	}
	p.rooms[identityID][roomID] = historyStart
}

func (p *testPolicy) revoke(identityID, roomID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.rooms[identityID], roomID)
}

func (p *testPolicy) Routes(_ context.Context, identityID string) ([]string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	rooms := make([]string, 0, len(p.rooms[identityID]))
	for roomID := range p.rooms[identityID] {
		rooms = append(rooms, roomID)
	}
	sort.Strings(rooms)
	return rooms, nil
}

func (p *testPolicy) CanPublish(_ context.Context, identityID, roomID string) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.rooms[identityID][roomID]; !ok || p.blocked[identityID+"\x00"+roomID] {
		return ErrPermissionDenied
	}
	return nil
}

func (p *testPolicy) HistoryStart(_ context.Context, identityID, roomID string) (int64, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	start, ok := p.rooms[identityID][roomID]
	if !ok {
		return 0, ErrPermissionDenied
	}
	return start, nil
}

func testEngine(t *testing.T) (*Engine, *MemoryStore, *testPolicy) {
	t.Helper()
	store := NewMemoryStore()
	policy := newTestPolicy()
	engine, err := New(Options{
		Authenticate: func(_ context.Context, request *http.Request) (Identity, error) {
			identityID := request.Header.Get("X-Identity")
			if identityID == "" {
				return Identity{}, ErrPermissionDenied
			}
			return Identity{ID: identityID}, nil
		},
		Access: policy,
		Store:  store,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, store, policy
}

func TestEngineAuthorizationAndIdempotentPublish(t *testing.T) {
	engine, _, policy := testEngine(t)
	policy.grant("alice", "room-1", 0)
	publish := Publish{ID: "publish-1", RoomID: "room-1", ActorID: "alice", Name: "message.created", Profile: Durable, Data: json.RawMessage(`{"text":"hello"}`)}
	first, err := engine.Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Sequence != 1 || second.Sequence != 1 {
		t.Fatalf("events = %#v and %#v", first, second)
	}
	publish.ActorID = "mallory"
	if _, err := engine.Publish(context.Background(), publish); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("unauthorized publish error = %v", err)
	}
}

func TestMemoryStoreAllocatesContiguousSequenceConcurrently(t *testing.T) {
	store := NewMemoryStore()
	const count = 64
	sequences := make(chan int64, count)
	var group sync.WaitGroup
	for index := 0; index < count; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			event, err := store.Append(context.Background(), Publish{
				ID:     "publish-" + strings.Repeat("x", index) + string(rune('a'+index%26)),
				RoomID: "room", ActorID: "alice", Name: "message.created", Profile: Durable, Data: json.RawMessage(`{"ok":true}`),
			})
			if err != nil {
				t.Errorf("append %d: %v", index, err)
				return
			}
			sequences <- event.Sequence
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
	engine, _, policy := testEngine(t)
	policy.grant("alice", "room-1", 0)
	policy.grant("bob", "room-1", 0)
	server := httptest.NewServer(engine.Handler())
	t.Cleanup(server.Close)
	alice := dialDelivery(t, server.URL, "alice")
	defer alice.Close()
	bob := dialDelivery(t, server.URL, "bob")
	readUntilOp(t, alice, "hello")
	readUntilOp(t, bob, "hello")
	if err := alice.WriteJSON(map[string]any{"op": "publish", "id": "client-1", "room_id": "room-1", "name": "message.created", "profile": "durable", "data": map[string]any{"text": "hello"}}); err != nil {
		t.Fatal(err)
	}
	aliceMessages := readUntilOps(t, alice, "ack", "event")
	bobEvent := readUntilOp(t, bob, "event")
	if aliceMessages["ack"]["status"] != "committed" || bobEvent["sequence"] != float64(1) {
		t.Fatalf("alice = %#v, bob = %#v", aliceMessages, bobEvent)
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
		t.Fatalf("resume = %#v", messages)
	}
}

func TestWebSocketAcceptsUTF8JSONBinaryFrame(t *testing.T) {
	engine, _, policy := testEngine(t)
	policy.grant("alice", "room-1", 0)
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

func TestWebSocketEphemeralAndRoutingRefresh(t *testing.T) {
	engine, _, policy := testEngine(t)
	policy.grant("alice", "room-1", 0)
	policy.grant("bob", "room-1", 0)
	server := httptest.NewServer(engine.Handler())
	defer server.Close()
	alice := dialDelivery(t, server.URL, "alice")
	defer alice.Close()
	bob := dialDelivery(t, server.URL, "bob")
	defer bob.Close()
	readUntilOp(t, alice, "hello")
	readUntilOp(t, bob, "hello")
	if err := alice.WriteJSON(map[string]any{"op": "publish", "id": "signal-1", "room_id": "room-1", "name": "rtc.signal", "profile": "ephemeral", "data": map[string]any{"kind": "ice"}}); err != nil {
		t.Fatal(err)
	}
	readUntilOps(t, alice, "ack", "event")
	if event := readUntilOp(t, bob, "event"); event["profile"] != "ephemeral" {
		t.Fatalf("event = %#v", event)
	}
	policy.revoke("bob", "room-1")
	if err := engine.RefreshIdentity(context.Background(), "bob"); err != nil {
		t.Fatal(err)
	}
	if removed := readUntilOp(t, bob, "room.removed"); removed["room_id"] != "room-1" {
		t.Fatalf("removed = %#v", removed)
	}
}

func TestWebSocketRequiresDeliverySubprotocol(t *testing.T) {
	engine, _, _ := testEngine(t)
	server := httptest.NewServer(engine.Handler())
	defer server.Close()
	header := http.Header{"X-Identity": []string{"alice"}}
	_, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), header)
	if err == nil || response == nil || response.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("response = %#v, err = %v", response, err)
	}
}

func dialDelivery(t *testing.T, serverURL, identityID string) *websocket.Conn {
	t.Helper()
	header := http.Header{"X-Identity": []string{identityID}}
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{Subprotocol}
	connection, response, err := dialer.Dial("ws"+strings.TrimPrefix(serverURL, "http"), header)
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
