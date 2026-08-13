package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 50 * time.Second
	maxWSFrameSize = 64 << 10
)

type Hub struct {
	mu             sync.RWMutex
	byUser         map[uuid.UUID]map[*Client]struct{}
	byConversation map[uuid.UUID]map[*Client]struct{}
}

func NewHub() *Hub {
	return &Hub{
		byUser:         make(map[uuid.UUID]map[*Client]struct{}),
		byConversation: make(map[uuid.UUID]map[*Client]struct{}),
	}
}

func (h *Hub) Register(client *Client, conversationIDs []uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byUser[client.userID] == nil {
		h.byUser[client.userID] = make(map[*Client]struct{})
	}
	h.byUser[client.userID][client] = struct{}{}
	for _, id := range conversationIDs {
		if h.byConversation[id] == nil {
			h.byConversation[id] = make(map[*Client]struct{})
		}
		h.byConversation[id][client] = struct{}{}
		client.conversations[id] = struct{}{}
	}
}

func (h *Hub) Unregister(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if clients := h.byUser[client.userID]; clients != nil {
		delete(clients, client)
		if len(clients) == 0 {
			delete(h.byUser, client.userID)
		}
	}
	for id := range client.conversations {
		if clients := h.byConversation[id]; clients != nil {
			delete(clients, client)
			if len(clients) == 0 {
				delete(h.byConversation, id)
			}
		}
	}
}

func (h *Hub) AddUserConversation(userID, conversationID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.byUser[userID] {
		if h.byConversation[conversationID] == nil {
			h.byConversation[conversationID] = make(map[*Client]struct{})
		}
		h.byConversation[conversationID][client] = struct{}{}
		client.conversations[conversationID] = struct{}{}
	}
}

func (h *Hub) RemoveUserConversation(userID, conversationID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.byUser[userID] {
		delete(client.conversations, conversationID)
		if clients := h.byConversation[conversationID]; clients != nil {
			delete(clients, client)
			if len(clients) == 0 {
				delete(h.byConversation, conversationID)
			}
		}
	}
}

func (h *Hub) RemoveConversation(conversationID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.byConversation[conversationID] {
		delete(client.conversations, conversationID)
	}
	delete(h.byConversation, conversationID)
}

func (h *Hub) Broadcast(conversationID uuid.UUID, message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.byConversation[conversationID] {
		select {
		case client.send <- payload:
		default:
			client.closeSlow()
		}
	}
}

func (h *Hub) BroadcastChanged(conversationID uuid.UUID, lastSeq int64) {
	h.Broadcast(conversationID, map[string]any{
		"type": "conversation.changed", "conversation_id": conversationID, "last_seq": lastSeq,
	})
}

func (h *Hub) BroadcastDeleted(conversationID uuid.UUID) {
	h.Broadcast(conversationID, map[string]any{
		"type": "conversation.deleted", "conversation_id": conversationID,
	})
}

type Client struct {
	hub            *Hub
	connection     *websocket.Conn
	userID         uuid.UUID
	send           chan []byte
	conversations  map[uuid.UUID]struct{}
	closeOnce      sync.Once
	unregisterOnce sync.Once
	doneOnce       sync.Once
	done           chan struct{}
}

func (c *Client) readPump() {
	defer c.shutdown()
	c.connection.SetReadLimit(maxWSFrameSize)
	_ = c.connection.SetReadDeadline(time.Now().Add(pongWait))
	c.connection.SetPongHandler(func(string) error {
		return c.connection.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.connection.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.Debug("websocket read ended", "error", err, "user_id", c.userID)
			}
			return
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer c.shutdown()
	for {
		select {
		case payload := <-c.send:
			_ = c.connection.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.connection.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.connection.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			_ = c.connection.WriteMessage(websocket.CloseMessage, []byte{})
			return
		}
	}
}

func (c *Client) reply(message any) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	select {
	case c.send <- payload:
	default:
		c.closeSlow()
	}
}

func (c *Client) closeSlow() {
	c.closeOnce.Do(func() { _ = c.connection.Close() })
}

func (c *Client) shutdown() {
	c.closeOnce.Do(func() { _ = c.connection.Close() })
	c.doneOnce.Do(func() { close(c.done) })
	c.unregisterOnce.Do(func() { c.hub.Unregister(c) })
}

type WebSocketHandler struct {
	store    *Store
	hub      *Hub
	upgrader websocket.Upgrader
}

func NewWebSocketHandler(store *Store, hub *Hub, cfg Config) *WebSocketHandler {
	return &WebSocketHandler{
		store: store, hub: hub,
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 5 * time.Second,
			CheckOrigin: func(r *http.Request) bool {
				_, ok := cfg.AllowedOrigins[r.Header.Get("Origin")]
				return ok
			},
		},
	}
}

func (h *WebSocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication_required", "sign in to continue")
		return
	}
	conversationIDs, err := h.store.ActiveConversationIDs(r.Context(), user.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	connection, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &Client{
		hub: h.hub, connection: connection,
		userID: user.ID, send: make(chan []byte, 64), conversations: make(map[uuid.UUID]struct{}),
		done: make(chan struct{}),
	}
	h.hub.Register(client, conversationIDs)
	client.reply(map[string]any{"type": "hello", "protocol_version": 2, "user_id": user.ID})
	go client.writePump()
	client.readPump()
}
