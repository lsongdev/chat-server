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

func TestWebSocketChangeNotification(t *testing.T) {
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
	conversation, _, err := store.CreateConversation(ctx, user.ID, uuid.New(), "WebSocket test")
	if err != nil {
		t.Fatal(err)
	}

	origin := "http://chat.test"
	cfg := Config{AllowedOrigins: map[string]struct{}{origin: {}}, MaxMessageBytes: 8192}
	hub := NewHub()
	handler := NewWebSocketHandler(store, hub, cfg)
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
	if err := connection.ReadJSON(&hello); err != nil || hello["type"] != "hello" || hello["protocol_version"] != float64(2) {
		t.Fatalf("missing hello: %#v %v", hello, err)
	}
	hub.BroadcastChanged(conversation.ID, conversation.LastSeq)
	var changed map[string]any
	if err := connection.ReadJSON(&changed); err != nil || changed["type"] != "conversation.changed" {
		t.Fatalf("missing change notification: %#v %v", changed, err)
	}
}
