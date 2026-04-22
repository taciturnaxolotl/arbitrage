package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"tangled.sh/dunkirk.sh/arbitrage/controlplane/internal/models"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// StoreInterface allows the Hub to process command results/acks
// without importing the full store package (avoids circular deps).
type StoreInterface interface {
	CompleteCommand(result models.CommandResult) (*models.Command, bool)
	AckCommand(commandID string) (*models.Command, bool)
}

type Hub struct {
	mu       sync.RWMutex
	clients  map[string]*WSClient
	byClient map[string]*WSClient
	store    StoreInterface
}

type WSClient struct {
	ID       string
	ClientID string // empty for dashboard browsers, set for remote clients
	Conn     *websocket.Conn
	Send     chan []byte
	hub      *Hub
}

type Message struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

func NewHub(s StoreInterface) *Hub {
	return &Hub{
		clients:  make(map[string]*WSClient),
		byClient: make(map[string]*WSClient),
		store:    s,
	}
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}

	client := &WSClient{
		ID:   uuid.New().String(),
		Conn: conn,
		Send: make(chan []byte, 256),
		hub:  h,
	}

	h.mu.Lock()
	h.clients[client.ID] = client
	h.mu.Unlock()

	go client.writePump()
	go client.readPump()
}

// HandleClientWebSocket upgrades a remote client (authenticated with Basic Auth).
// The clientID is extracted from the auth context.
func (h *Hub) HandleClientWebSocket(w http.ResponseWriter, r *http.Request, clientID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("client websocket upgrade error: %v", err)
		return
	}

	client := &WSClient{
		ID:       uuid.New().String(),
		ClientID: clientID,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		hub:      h,
	}

	h.mu.Lock()
	// Evict existing WS connection for this client if any
	if old, ok := h.byClient[clientID]; ok {
		delete(h.byClient, clientID)
		delete(h.clients, old.ID)
		close(old.Send)
	}
	h.clients[client.ID] = client
	h.byClient[clientID] = client
	h.mu.Unlock()

	log.Printf("client %s connected via websocket", clientID)

	go client.writePump()
	go client.readPump()
}

func (c *WSClient) readPump() {
	defer func() {
		c.hub.Unregister(c)
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(1 << 20) // 1MB for command results
	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("ws read error: %v", err)
			}
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("ws unmarshal error: %v", err)
			continue
		}

		switch msg.Type {
		case "ping":
			c.Send <- []byte(`{"type":"pong"}`)
		case "command_result":
			// Client reporting command result — process it through the store
			var result struct {
				CommandID string `json:"command_id"`
				Result    any    `json:"result"`
				Error     string `json:"error"`
			}
			if err := json.Unmarshal(msg.Data, &result); err != nil {
				log.Printf("ws command_result unmarshal error: %v", err)
				continue
			}
			if result.CommandID != "" && c.hub.store != nil {
				cmd, ok := c.hub.store.CompleteCommand(models.CommandResult{
					CommandID: result.CommandID,
					Result:    result.Result,
					Error:    result.Error,
				})
				if ok {
					c.hub.Broadcast("command_completed", cmd)
				}
			}

		case "command_ack":
			var ack struct {
				CommandID string `json:"command_id"`
			}
			if err := json.Unmarshal(msg.Data, &ack); err != nil {
				log.Printf("ws command_ack unmarshal error: %v", err)
				continue
			}
			if ack.CommandID != "" && c.hub.store != nil {
				cmd, ok := c.hub.store.AckCommand(ack.CommandID)
				if ok {
					c.hub.Broadcast("command_acknowledged", cmd)
				}
			}
		}
	}
}

func (c *WSClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Hub) Unregister(client *WSClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[client.ID]; ok {
		close(client.Send)
		delete(h.clients, client.ID)
		if client.ClientID != "" {
			if h.byClient[client.ClientID] == client {
				delete(h.byClient, client.ClientID)
			}
		}
	}
}

func (h *Hub) Broadcast(msgType string, data interface{}) {
	msg := Message{Type: msgType}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			log.Printf("ws broadcast marshal error: %v", err)
			return
		}
		msg.Data = raw
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("ws broadcast marshal error: %v", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, client := range h.clients {
		if client.ClientID != "" {
			continue // skip remote clients on dashboard broadcasts
		}
		select {
		case client.Send <- payload:
		default:
			go h.Unregister(client)
		}
	}
}

// SendToClient sends a message to a specific remote client by clientID.
// Returns true if the client is connected and the message was queued.
func (h *Hub) SendToClient(clientID string, msgType string, data interface{}) bool {
	msg := Message{Type: msgType}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			log.Printf("ws send to client marshal error: %v", err)
			return false
		}
		msg.Data = raw
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("ws send to client marshal error: %v", err)
		return false
	}

	h.mu.RLock()
	client, ok := h.byClient[clientID]
	h.mu.RUnlock()

	if !ok {
		return false
	}

	select {
	case client.Send <- payload:
		return true
	default:
		go h.Unregister(client)
		return false
	}
}

// IsClientConnected returns true if a remote client has an active WS connection.
func (h *Hub) IsClientConnected(clientID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.byClient[clientID]
	return ok
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
