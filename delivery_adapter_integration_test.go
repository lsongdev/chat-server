package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/lsongdev/chat-server/delivery"
)

func TestDeliveryCoreWithChatPostgresStore(t *testing.T) {
	databaseURL := os.Getenv("CHAT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CHAT_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := OpenStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	testID := uuid.NewString()
	alice, err := store.UpsertOIDCUser(ctx, "https://delivery.example", OIDCClaims{
		Subject: "alice-" + testID, Email: "alice-" + testID + "@example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.UpsertOIDCUser(ctx, "https://delivery.example", OIDCClaims{
		Subject: "bob-" + testID, Email: "bob-" + testID + "@example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	conversation, _, err := store.CreateConversation(ctx, alice.ID, uuid.New(), "Delivery integration")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AddMemberByEmail(ctx, alice.ID, conversation.ID, bob.Email, 10); err != nil {
		t.Fatal(err)
	}

	engine, err := delivery.New(delivery.Options{
		Authenticate: func(_ context.Context, request *http.Request) (delivery.Identity, error) {
			return delivery.Identity{ID: request.Header.Get("X-Test-Identity")}, nil
		},
		Store:               NewChatDeliveryStore(store),
		HandleClientPublish: chatClientPublish(64 << 10),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	server := httptest.NewServer(engine.Handler())
	defer server.Close()

	aliceSocket := dialChatDelivery(t, server.URL, alice.ID)
	defer aliceSocket.Close()
	bobSocket := dialChatDelivery(t, server.URL, bob.ID)
	defer bobSocket.Close()
	readChatDeliveryOp(t, aliceSocket, "hello")
	readChatDeliveryOp(t, bobSocket, "hello")
	if err := aliceSocket.WriteJSON(map[string]any{
		"op": "publish", "id": uuid.New(), "room_id": conversation.ID,
		"name": "conversation.renamed", "profile": "durable",
		"data": map[string]any{"title": "forged"},
	}); err != nil {
		t.Fatal(err)
	}
	invalid := readChatDeliveryOp(t, aliceSocket, "error")
	if invalid["error"].(map[string]any)["code"] != "invalid_message" {
		t.Fatalf("forged business event was not rejected: %#v", invalid)
	}

	publishID := uuid.New()
	if err := aliceSocket.WriteJSON(map[string]any{
		"op": "publish", "id": publishID, "room_id": conversation.ID,
		"name": "message.created", "profile": "durable",
		"data": map[string]any{"type": "text", "text": "postgres delivery"},
	}); err != nil {
		t.Fatal(err)
	}
	ack := readChatDeliveryOp(t, aliceSocket, "ack")
	if ack["status"] != "committed" || ack["sequence"] != float64(3) {
		t.Fatalf("ack = %#v", ack)
	}
	event := readChatDeliveryOp(t, bobSocket, "event")
	if event["publish_id"] != publishID.String() || event["sequence"] != float64(3) {
		t.Fatalf("event = %#v", event)
	}

	// Bob joined at sequence 2. A malicious resume cursor of zero must not
	// reveal the conversation.created event at sequence 1.
	if err := bobSocket.WriteJSON(map[string]any{
		"op": "resume", "rooms": map[string]int64{conversation.ID.String(): 0},
	}); err != nil {
		t.Fatal(err)
	}
	begin := readChatDeliveryOp(t, bobSocket, "sync.begin")
	if begin["after_sequence"] != float64(1) {
		t.Fatalf("history boundary was not clamped: %#v", begin)
	}
	recovered := readChatDeliveryOp(t, bobSocket, "event")
	if recovered["sequence"] != float64(2) || recovered["recovered"] != true {
		t.Fatalf("first recovered event = %#v", recovered)
	}

	events, err := store.ListEvents(ctx, bob.ID, conversation.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].ClientMessageID == nil || *events[1].ClientMessageID != publishID {
		t.Fatalf("database events = %#v", events)
	}
}

func dialChatDelivery(t *testing.T, serverURL string, identityID uuid.UUID) *websocket.Conn {
	t.Helper()
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{delivery.Subprotocol}
	header := http.Header{"X-Test-Identity": []string{identityID.String()}}
	connection, response, err := dialer.Dial("ws"+strings.TrimPrefix(serverURL, "http"), header)
	if err != nil {
		if response != nil {
			t.Fatalf("dial returned %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	return connection
}

func readChatDeliveryOp(t *testing.T, connection *websocket.Conn, wanted string) map[string]any {
	t.Helper()
	for attempts := 0; attempts < 20; attempts++ {
		_, payload, err := connection.ReadMessage()
		if err != nil {
			t.Fatalf("read %s: %v", wanted, err)
		}
		var envelope map[string]any
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope["op"] == wanted {
			return envelope
		}
	}
	t.Fatalf("did not receive %s", wanted)
	return nil
}
