package main

import (
	"context"
	"encoding/json"
	"errors"
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

type Client struct {
	hub            *Hub
	store          *Store
	config         Config
	connection     *websocket.Conn
	userID         uuid.UUID
	send           chan []byte
	conversations  map[uuid.UUID]struct{}
	closeOnce      sync.Once
	unregisterOnce sync.Once
	doneOnce       sync.Once
	done           chan struct{}
	messageWindow  time.Time
	messageCount   int
}

type wsRequest struct {
	Type            string          `json:"type"`
	RequestID       string          `json:"request_id"`
	ConversationID  uuid.UUID       `json:"conversation_id"`
	ClientMessageID uuid.UUID       `json:"client_message_id"`
	Content         json.RawMessage `json:"content"`
	Seq             int64           `json:"seq"`
}

func (c *Client) readPump(ctx context.Context) {
	defer c.shutdown()
	c.connection.SetReadLimit(maxWSFrameSize)
	_ = c.connection.SetReadDeadline(time.Now().Add(pongWait))
	c.connection.SetPongHandler(func(string) error {
		return c.connection.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		var request wsRequest
		if err := c.connection.ReadJSON(&request); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				slog.Debug("websocket read ended", "error", err, "user_id", c.userID)
			}
			return
		}
		switch request.Type {
		case "message.send":
			c.handleSend(ctx, request)
		case "read.update":
			c.handleRead(ctx, request)
		default:
			c.reply(map[string]any{"type": "error", "request_id": request.RequestID, "code": "unknown_event_type"})
		}
	}
}

func (c *Client) handleSend(ctx context.Context, request wsRequest) {
	now := time.Now()
	if c.messageWindow.IsZero() || now.Sub(c.messageWindow) >= time.Minute {
		c.messageWindow, c.messageCount = now, 0
	}
	if c.messageCount >= 120 {
		c.reply(map[string]any{"type": "error", "request_id": request.RequestID, "code": "rate_limited"})
		return
	}
	c.messageCount++
	var content struct {
		Text string `json:"text"`
	}
	if request.ConversationID == uuid.Nil || request.ClientMessageID == uuid.Nil || json.Unmarshal(request.Content, &content) != nil {
		c.reply(map[string]any{"type": "error", "request_id": request.RequestID, "code": "invalid_request"})
		return
	}
	if len(content.Text) == 0 || len([]byte(content.Text)) > c.config.MaxMessageBytes {
		c.reply(map[string]any{"type": "error", "request_id": request.RequestID, "code": "invalid_message"})
		return
	}
	event, err := c.store.AppendMessage(ctx, c.userID, request.ConversationID, request.ClientMessageID, content.Text)
	if err != nil {
		code := "store_failed"
		if errors.Is(err, ErrForbidden) {
			code = "forbidden"
		}
		c.reply(map[string]any{"type": "error", "request_id": request.RequestID, "code": code})
		return
	}
	c.reply(map[string]any{
		"type": "message.stored", "request_id": request.RequestID,
		"conversation_id": event.ConversationID, "seq": event.Seq, "message_id": event.ID,
	})
	c.hub.Broadcast(event.ConversationID, map[string]any{"type": "conversation.event", "event": event})
}

func (c *Client) handleRead(ctx context.Context, request wsRequest) {
	if err := c.store.UpdateRead(ctx, c.userID, request.ConversationID, request.Seq); err != nil {
		c.reply(map[string]any{"type": "error", "request_id": request.RequestID, "code": "invalid_sequence"})
		return
	}
	c.reply(map[string]any{"type": "read.updated", "request_id": request.RequestID, "conversation_id": request.ConversationID, "seq": request.Seq})
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
	config   Config
	upgrader websocket.Upgrader
}

func NewWebSocketHandler(store *Store, hub *Hub, cfg Config) *WebSocketHandler {
	return &WebSocketHandler{
		store: store, hub: hub, config: cfg,
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
		hub: h.hub, store: h.store, config: h.config, connection: connection,
		userID: user.ID, send: make(chan []byte, 64), conversations: make(map[uuid.UUID]struct{}),
		done: make(chan struct{}),
	}
	h.hub.Register(client, conversationIDs)
	client.reply(map[string]any{"type": "hello", "protocol_version": 1, "user_id": user.ID})
	go client.writePump()
	client.readPump(r.Context())
}
