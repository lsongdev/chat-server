package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestWebSocketMessageFlow(t *testing.T) {
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
	user, err := store.UpsertOIDCUser(ctx, "https://ws-test.example", OIDCClaims{Subject: uuid.NewString(), Name: "WS User", Email: "ws-" + uuid.NewString() + "@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	conversation, _, err := store.CreateConversation(ctx, user.ID, "WebSocket test")
	if err != nil {
		t.Fatal(err)
	}

	origin := "http://chat.test"
	cfg := Config{AllowedOrigins: map[string]struct{}{origin: {}}, MaxMessageBytes: 8192}
	handler := NewWebSocketHandler(store, NewHub(), cfg)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	header := http.Header{"Origin": []string{origin}}
	connection, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello map[string]any
	if err := connection.ReadJSON(&hello); err != nil || hello["type"] != "hello" {
		t.Fatalf("missing hello: %#v %v", hello, err)
	}
	clientMessageID := uuid.New()
	requestID := uuid.NewString()
	if err := connection.WriteJSON(map[string]any{
		"type": "message.send", "request_id": requestID,
		"conversation_id": conversation.ID, "client_message_id": clientMessageID,
		"content": map[string]string{"text": "hello websocket"},
	}); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := connection.ReadJSON(&stored); err != nil || stored["type"] != "message.stored" {
		t.Fatalf("missing stored acknowledgement: %#v %v", stored, err)
	}
	var broadcast map[string]any
	if err := connection.ReadJSON(&broadcast); err != nil || broadcast["type"] != "conversation.event" {
		t.Fatalf("missing broadcast: %#v %v", broadcast, err)
	}
	events, err := store.ListEvents(ctx, user.ID, conversation.ID, 1, 10)
	if err != nil || len(events) != 1 || events[0].Type != "message.created" {
		t.Fatalf("websocket message was not persisted: %#v %v", events, err)
	}
}
