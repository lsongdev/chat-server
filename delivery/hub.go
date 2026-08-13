package delivery

import (
	"encoding/json"
	"sync"
)

type outboundLane uint8

const (
	laneControl outboundLane = iota
	laneDurable
	laneRealtime
)

type connectionHub struct {
	mu         sync.RWMutex
	byIdentity map[string]map[*socketClient]struct{}
	byRoom     map[string]map[*socketClient]struct{}
}

func newConnectionHub() *connectionHub {
	return &connectionHub{
		byIdentity: make(map[string]map[*socketClient]struct{}),
		byRoom:     make(map[string]map[*socketClient]struct{}),
	}
}

func (h *connectionHub) register(client *socketClient, roomIDs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byIdentity[client.identityID] == nil {
		h.byIdentity[client.identityID] = make(map[*socketClient]struct{})
	}
	h.byIdentity[client.identityID][client] = struct{}{}
	for _, roomID := range roomIDs {
		h.addRoomLocked(client, roomID)
	}
}

func (h *connectionHub) unregister(client *socketClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if clients := h.byIdentity[client.identityID]; clients != nil {
		delete(clients, client)
		if len(clients) == 0 {
			delete(h.byIdentity, client.identityID)
		}
	}
	for roomID := range client.rooms {
		h.removeRoomLocked(client, roomID)
	}
}

func (h *connectionHub) replaceIdentityRooms(identityID string, roomIDs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	wanted := make(map[string]struct{}, len(roomIDs))
	for _, roomID := range roomIDs {
		wanted[roomID] = struct{}{}
	}
	for client := range h.byIdentity[identityID] {
		for roomID := range client.rooms {
			if _, keep := wanted[roomID]; !keep {
				h.removeRoomLocked(client, roomID)
				client.enqueue(laneControl, roomEnvelope{Op: "room.removed", RoomID: roomID, Reason: "membership_removed"})
			}
		}
		for roomID := range wanted {
			if _, exists := client.rooms[roomID]; !exists {
				h.addRoomLocked(client, roomID)
				client.enqueue(laneControl, roomEnvelope{Op: "room.added", RoomID: roomID})
			}
		}
	}
}

func (h *connectionHub) removeRoom(roomID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.byRoom[roomID] {
		delete(client.rooms, roomID)
		client.enqueue(laneControl, roomEnvelope{Op: "room.removed", RoomID: roomID, Reason: "room_deleted"})
	}
	delete(h.byRoom, roomID)
}

func (h *connectionHub) broadcast(roomID string, message eventEnvelope) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	lane := laneRealtime
	if message.Profile == Durable {
		lane = laneDurable
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for client := range h.byRoom[roomID] {
		client.enqueueBytes(lane, payload)
	}
}

func (h *connectionHub) closeAll() {
	h.mu.RLock()
	clients := make([]*socketClient, 0)
	for _, group := range h.byIdentity {
		for client := range group {
			clients = append(clients, client)
		}
	}
	h.mu.RUnlock()
	for _, client := range clients {
		client.close()
	}
}

func (h *connectionHub) addRoomLocked(client *socketClient, roomID string) {
	if h.byRoom[roomID] == nil {
		h.byRoom[roomID] = make(map[*socketClient]struct{})
	}
	h.byRoom[roomID][client] = struct{}{}
	client.rooms[roomID] = struct{}{}
}

func (h *connectionHub) removeRoomLocked(client *socketClient, roomID string) {
	delete(client.rooms, roomID)
	if clients := h.byRoom[roomID]; clients != nil {
		delete(clients, client)
		if len(clients) == 0 {
			delete(h.byRoom, roomID)
		}
	}
}
