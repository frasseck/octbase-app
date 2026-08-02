// Package sse implements the Server-Sent Events hub for real-time collaboration.
// The hub fans domain events out to all connected SSE clients for the relevant
// project. It also maintains a presence map for connected clients.
package sse

import (
	"encoding/json"
	"sync"
)

// Event is a typed domain event broadcast to SSE clients.
type Event struct {
	ProjectID string
	Payload   map[string]any
}

// client represents a connected SSE subscriber.
type client struct {
	projectID string
	userID    string
	ch        chan []byte
}

// Hub manages SSE client connections per project.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	publish chan Event
	reg     chan *client
	unreg   chan *client
}

// NewHub creates a new Hub ready to run.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[*client]struct{}),
		publish: make(chan Event, 256),
		reg:     make(chan *client, 16),
		unreg:   make(chan *client, 16),
	}
}

// Run processes registrations and broadcasts in a single goroutine.
// Call as: go hub.Run()
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.reg:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()

		case c := <-h.unreg:
			h.mu.Lock()
			delete(h.clients, c)
			h.mu.Unlock()
			close(c.ch)

		case ev := <-h.publish:
			b, _ := json.Marshal(ev.Payload)
			data := append([]byte("data: "), b...)
			data = append(data, '\n', '\n')

			h.mu.RLock()
			for c := range h.clients {
				if c.projectID == ev.ProjectID {
					select {
					case c.ch <- data:
					default:
						// Slow client — skip this event rather than blocking.
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Publish broadcasts an event to all clients connected for the project.
func (h *Hub) Publish(projectID string, payload map[string]any) {
	h.publish <- Event{ProjectID: projectID, Payload: payload}
}

// Subscribe registers a new client and returns its message channel.
func (h *Hub) Subscribe(projectID, userID string) *client {
	c := &client{
		projectID: projectID,
		userID:    userID,
		ch:        make(chan []byte, 32),
	}
	h.reg <- c
	return c
}

// Unsubscribe removes the client from the hub.
func (h *Hub) Unsubscribe(c *client) {
	h.unreg <- c
}

// Presence returns a list of currently connected viewers for a project.
func (h *Hub) Presence(projectID string) []map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var viewers []map[string]any
	for c := range h.clients {
		if c.projectID == projectID {
			viewers = append(viewers, map[string]any{"userId": c.userID})
		}
	}
	if viewers == nil {
		viewers = []map[string]any{}
	}
	return viewers
}

// Chan returns the receive channel for incoming messages.
func (c *client) Chan() <-chan []byte { return c.ch }
