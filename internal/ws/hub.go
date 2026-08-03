package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type EventType string

const (
	EventAlert     EventType = "alert"
	EventAudit     EventType = "audit"
	EventMonitor   EventType = "monitor"
	EventCMDB      EventType = "cmdb"
	EventITSM      EventType = "itsm"
	EventHeartbeat EventType = "heartbeat"
	EventTopology  EventType = "topology"
)

type Event struct {
	Type    EventType `json:"type"`
	Payload any       `json:"payload"`
	Time    int64     `json:"time"`
}

type client struct {
	conn   *websocket.Conn
	send   chan []byte
	done   chan struct{}
	tenant string
	remote string
}

type Hub struct {
	mu        sync.RWMutex
	clients   map[*client]bool
	broadcast chan []byte
}

func NewHub() *Hub {
	h := &Hub{
		clients:   make(map[*client]bool),
		broadcast: make(chan []byte, 256),
	}
	go h.run()
	return h
}

func (h *Hub) run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					close(c.done)
					delete(h.clients, c)
				}
			}
			h.mu.RUnlock()
		case <-ticker.C:
			h.broadcastHeartbeat()
		}
	}
}

func (h *Hub) broadcastHeartbeat() {
	data, _ := json.Marshal(Event{
		Type: EventHeartbeat,
		Time: time.Now().UnixMilli(),
	})
	h.broadcast <- data
}

func (h *Hub) Broadcast(evt Event) {
	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("ws marshal event", "error", err)
		return
	}
	h.broadcast <- data
}

func (h *Hub) BroadcastToTenant(evt any, tenantID string) {
	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("ws marshal event", "error", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if c.tenant == tenantID {
			select {
			case c.send <- data:
			default:
				close(c.done)
				delete(h.clients, c)
			}
		}
	}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		slog.Error("ws accept", "error", err)
		return
	}

	c := &client{
		conn:   conn,
		send:   make(chan []byte, 64),
		done:   make(chan struct{}),
		tenant: r.Header.Get("X-Tenant-ID"),
		remote: r.RemoteAddr,
	}

	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()

	slog.Info("ws client connected", "remote", c.remote, "tenant", c.tenant)

	go h.writePump(c)
	h.readPump(c)
}

func (h *Hub) writePump(c *client) {
	ticker := time.NewTicker(15 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close(websocket.StatusNormalClosure, "")
		h.removeClient(c)
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			ctx, cancel := writeCtx()
			err := c.conn.Write(ctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				slog.Error("ws write", "error", err)
				return
			}
		case <-ticker.C:
			ctx, cancel := writeCtx()
			err := c.conn.Write(ctx, websocket.MessageText, []byte(`{"type":"ping"}`))
			cancel()
			if err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

func (h *Hub) readPump(c *client) {
	defer func() {
		h.removeClient(c)
	}()

	for {
		ctx, cancel := readCtx()
		_, _, err := c.conn.Read(ctx)
		cancel()
		if err != nil {
			break
		}
	}
}

func (h *Hub) removeClient(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func readCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 60*time.Second)
}

func writeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
