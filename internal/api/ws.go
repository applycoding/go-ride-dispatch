package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

const maxConnections = 1024

// Hub fans trip events out to connected riders and drivers.
// Origin checks are disabled: this demo has no authentication.
type Hub struct {
	log     *slog.Logger
	mu      sync.Mutex
	clients map[string][]*client
}

type client struct {
	id     string
	send   chan []byte
	cancel context.CancelFunc
}

type outbound struct {
	Type string      `json:"type"`
	Trip domain.Trip `json:"trip,omitempty"`
	Role string      `json:"role,omitempty"`
	ID   string      `json:"actor_id,omitempty"`
}

// NewHub returns an empty connection hub.
func NewHub(log *slog.Logger) *Hub {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Hub{log: log, clients: make(map[string][]*client)}
}

// OnTrip delivers a committed trip to the rider and the assigned driver.
func (h *Hub) OnTrip(_ context.Context, trip domain.Trip, eventType string) {
	body, err := json.Marshal(outbound{Type: eventType, Trip: trip})
	if err != nil {
		h.log.Error("marshal trip event", "err", err)
		return
	}
	for _, c := range h.recipients(trip.RiderID, trip.DriverID) {
		select {
		case c.send <- body:
		default:
			c.cancel()
		}
	}
}

// ServeHTTP upgrades GET /v1/ws?role=rider|driver&actor_id=...
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	actorID := r.URL.Query().Get("actor_id")
	if (role != "rider" && role != "driver") || actorID == "" || len(actorID) > 64 {
		http.Error(w, "role and actor_id are required", http.StatusBadRequest)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		h.log.Debug("websocket accept", "err", err)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	c := &client{id: actorID, send: make(chan []byte, 8), cancel: cancel}
	if !h.register(c) {
		cancel()
		_ = conn.Close(websocket.StatusTryAgainLater, "busy")
		return
	}
	defer func() {
		cancel()
		h.unregister(c)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	hello, err := json.Marshal(outbound{Type: "hello", Role: role, ID: actorID})
	if err != nil {
		return
	}
	c.send <- hello

	errc := make(chan error, 1)
	go func() {
		errc <- writeLoop(ctx, conn, c.send)
		cancel()
	}()
	_ = readLoop(ctx, conn)
	cancel()
	<-errc
}

func (h *Hub) register(c *client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, list := range h.clients {
		n += len(list)
	}
	if n >= maxConnections {
		return false
	}
	h.clients[c.id] = append(h.clients[c.id], c)
	return true
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.clients[c.id]
	kept := list[:0]
	for _, other := range list {
		if other != c {
			kept = append(kept, other)
		}
	}
	if len(kept) == 0 {
		delete(h.clients, c.id)
		return
	}
	h.clients[c.id] = kept
}

func (h *Hub) recipients(ids ...string) []*client {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[*client]struct{}{}
	var out []*client
	for _, id := range ids {
		if id == "" {
			continue
		}
		for _, c := range h.clients[id] {
			if _, ok := seen[c]; ok {
				continue
			}
			seen[c] = struct{}{}
			out = append(out, c)
		}
	}
	return out
}

func writeLoop(ctx context.Context, conn *websocket.Conn, send <-chan []byte) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-send:
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

func readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return err
		}
	}
}
