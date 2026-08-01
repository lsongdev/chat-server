package main

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestHubRoutesByConversation(t *testing.T) {
	hub := NewHub()
	conversationA := uuid.New()
	conversationB := uuid.New()
	clientA := &Client{userID: uuid.New(), send: make(chan []byte, 1), conversations: make(map[uuid.UUID]struct{})}
	clientB := &Client{userID: uuid.New(), send: make(chan []byte, 1), conversations: make(map[uuid.UUID]struct{})}
	hub.Register(clientA, []uuid.UUID{conversationA})
	hub.Register(clientB, []uuid.UUID{conversationB})

	hub.Broadcast(conversationA, map[string]string{"type": "hello"})
	select {
	case payload := <-clientA.send:
		var message map[string]string
		if err := json.Unmarshal(payload, &message); err != nil || message["type"] != "hello" {
			t.Fatalf("unexpected payload: %s", payload)
		}
	default:
		t.Fatal("conversation member did not receive broadcast")
	}
	select {
	case <-clientB.send:
		t.Fatal("unrelated conversation received broadcast")
	default:
	}
}
