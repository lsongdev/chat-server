package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 50 * time.Second
)

type webSocketHandler struct{ engine *Engine }

func (h *webSocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !supportsSubprotocol(r, Subprotocol) {
		http.Error(w, "WebSocket subprotocol delivery.v1 is required", http.StatusUpgradeRequired)
		return
	}
	identity, err := h.engine.authenticate(r.Context(), r)
	if err != nil || !validID(identity.ID) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	rooms, err := h.engine.store.RoomsForIdentity(r.Context(), identity.ID)
	if err != nil {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	roomIDs := make([]string, 0, len(rooms))
	for _, room := range rooms {
		roomIDs = append(roomIDs, room.ID)
	}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 5 * time.Second,
		Subprotocols:     []string{Subprotocol},
		CheckOrigin: func(request *http.Request) bool {
			return h.engine.originCheck == nil || h.engine.originCheck(request)
		},
	}
	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &socketClient{
		engine: h.engine, connection: connection, connectionID: uuid.NewString(), identityID: identity.ID,
		rooms: make(map[string]struct{}), control: make(chan []byte, 32), durable: make(chan []byte, 128),
		realtime: make(chan []byte, 128), done: make(chan struct{}),
	}
	h.engine.hub.register(client, roomIDs)
	client.enqueue(laneControl, helloEnvelope{
		Op: "hello", Protocol: Subprotocol, ConnectionID: client.connectionID,
		IdentityID: identity.ID, MaxMessageBytes: h.engine.limits.MaxMessageBytes,
	})
	go client.writePump()
	client.readPump(r.Context())
}

func supportsSubprotocol(request *http.Request, expected string) bool {
	for _, protocol := range websocket.Subprotocols(request) {
		if protocol == expected {
			return true
		}
	}
	return false
}

type socketClient struct {
	engine       *Engine
	connection   *websocket.Conn
	connectionID string
	identityID   string
	rooms        map[string]struct{}
	control      chan []byte
	durable      chan []byte
	realtime     chan []byte
	done         chan struct{}
	closeOnce    sync.Once
}

func (c *socketClient) readPump(ctx context.Context) {
	defer c.close()
	c.connection.SetReadLimit(int64(c.engine.limits.MaxMessageBytes + 4096))
	_ = c.connection.SetReadDeadline(time.Now().Add(pongWait))
	c.connection.SetPongHandler(func(string) error {
		return c.connection.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		messageType, payload, err := c.connection.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			c.sendError("", "invalid_message", "only UTF-8 JSON messages are supported", false)
			continue
		}
		var envelope inboundEnvelope
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&envelope); err != nil {
			c.sendError("", "invalid_message", "invalid protocol message", false)
			continue
		}
		switch envelope.Op {
		case "publish":
			c.handlePublish(ctx, envelope)
		case "resume":
			c.handleResume(ctx, envelope)
		default:
			c.sendError(envelope.ID, "invalid_message", "unknown operation", false)
		}
	}
}

func (c *socketClient) handlePublish(ctx context.Context, envelope inboundEnvelope) {
	receipt, err := c.engine.publishFromClient(ctx, ClientPublish{
		IdentityID: c.identityID, ID: envelope.ID, RoomID: envelope.RoomID,
		Name: envelope.Name, Profile: envelope.Profile, Data: envelope.Data,
		ExpiresAt: envelope.ExpiresAt, Stream: envelope.Stream,
	})
	if err != nil {
		c.sendEngineError(envelope.ID, err)
		return
	}
	c.enqueue(laneControl, ackEnvelope{
		Op: "ack", ID: receipt.PublishID, Status: receipt.Status,
		EventID: receipt.MessageID, Sequence: receipt.Sequence,
	})
}

func (c *socketClient) handleResume(ctx context.Context, envelope inboundEnvelope) {
	if len(envelope.Rooms) > c.engine.limits.MaxResumeRooms {
		c.sendError("", "invalid_message", "too many rooms in resume request", false)
		return
	}
	for roomID, after := range envelope.Rooms {
		if after < 0 {
			c.sendError("", "invalid_message", "invalid room cursor", false)
			continue
		}
		member, err := c.engine.authorizedMember(ctx, roomID, c.identityID, Receive, ReadHistory)
		if err != nil {
			c.sendEngineError("", err)
			continue
		}
		after = max(after, member.HistoryStart-1)
		head, err := c.engine.store.HeadSequence(ctx, roomID)
		if err != nil {
			c.sendEngineError("", err)
			continue
		}
		c.enqueue(laneDurable, syncEnvelope{Op: "sync.begin", RoomID: roomID, AfterSequence: after, HeadSequence: head})
		cursor := after
		for cursor < head {
			events, err := c.engine.store.EventsAfter(ctx, roomID, cursor, defaultHistoryPageSize)
			if err != nil {
				c.sendEngineError("", err)
				break
			}
			if len(events) == 0 {
				break
			}
			for _, message := range events {
				if message.Sequence > head {
					break
				}
				event := wireEvent(message)
				event.Recovered = true
				c.enqueue(laneDurable, event)
				cursor = message.Sequence
			}
		}
		c.enqueue(laneDurable, syncEnvelope{Op: "sync.end", RoomID: roomID, Sequence: cursor})
	}
}

func (c *socketClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer c.close()
	for {
		// Give control traffic priority without starving other lanes.
		select {
		case payload := <-c.control:
			if !c.write(payload) {
				return
			}
			continue
		default:
		}
		select {
		case payload := <-c.control:
			if !c.write(payload) {
				return
			}
		case payload := <-c.durable:
			if !c.write(payload) {
				return
			}
		case payload := <-c.realtime:
			if !c.write(payload) {
				return
			}
		case <-ticker.C:
			_ = c.connection.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			_ = c.connection.WriteMessage(websocket.CloseMessage, nil)
			return
		}
	}
}

func (c *socketClient) write(payload []byte) bool {
	_ = c.connection.SetWriteDeadline(time.Now().Add(writeWait))
	return c.connection.WriteMessage(websocket.TextMessage, payload) == nil
}

func (c *socketClient) enqueue(lane outboundLane, message any) {
	payload, err := json.Marshal(message)
	if err == nil {
		c.enqueueBytes(lane, payload)
	}
}

func (c *socketClient) enqueueBytes(lane outboundLane, payload []byte) {
	select {
	case <-c.done:
		return
	default:
	}
	var queue chan []byte
	switch lane {
	case laneControl:
		queue = c.control
	case laneDurable:
		queue = c.durable
	default:
		select {
		case c.realtime <- payload:
		default: // Ephemeral and stream traffic may be dropped under backpressure.
		}
		return
	}
	select {
	case queue <- payload:
	default:
		// Hub fanout can call enqueue while holding a routing lock.
		go c.close()
	}
}

func (c *socketClient) sendEngineError(requestID string, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied), errors.Is(err, ErrNotFound):
		c.sendError(requestID, "permission_denied", "room access denied", false)
	case errors.Is(err, ErrInvalid):
		c.sendError(requestID, "invalid_message", "invalid message", false)
	case errors.Is(err, ErrAlreadyExists):
		c.sendError(requestID, "conflict", "resource already exists", false)
	default:
		c.sendError(requestID, "temporarily_unavailable", "temporarily unavailable", true)
	}
}

func (c *socketClient) sendError(requestID, code, message string, retryable bool) {
	c.enqueue(laneControl, errorEnvelope{
		Op: "error", RequestID: requestID,
		Error: errorBody{Code: code, Message: message, Retryable: retryable},
	})
}

func (c *socketClient) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.connection.Close()
		c.engine.hub.unregister(c)
	})
}
