package server

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/your-repo/mqtt-switchboard/internal/discovery"
)

// SSEHub manages active browser Server-Sent Events streaming connections
type SSEHub struct {
	mu         sync.Mutex
	clients    map[chan []byte]bool
	register   chan chan []byte
	unregister chan chan []byte
	broadcast  chan []byte
	stopCh     chan struct{}
}

// NewSSEHub creates a new SSE broadcast hub
func NewSSEHub() *SSEHub {
	h := &SSEHub{
		clients:    make(map[chan []byte]bool),
		register:   make(chan chan []byte),
		unregister: make(chan chan []byte),
		broadcast:  make(chan []byte, 32),
		stopCh:     make(chan struct{}),
	}
	go h.run()
	return h
}

func (h *SSEHub) run() {
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.mu.Lock()
			for client := range h.clients {
				select {
				case client <- msg:
				default:
					// Skip slow client to prevent blocking
				}
			}
			h.mu.Unlock()

		case <-heartbeat.C:
			// Send comment ping to prevent intermediate proxy timeout
			h.BroadcastRaw([]byte(": ping\n\n"))

		case <-h.stopCh:
			h.mu.Lock()
			for client := range h.clients {
				close(client)
			}
			h.clients = make(map[chan []byte]bool)
			h.mu.Unlock()
			return
		}
	}
}

// BroadcastDeviceEvent serializes and sends device update to all SSE clients
func (h *SSEHub) BroadcastDeviceEvent(entity *discovery.DeviceEntity) {
	data, err := json.Marshal(entity)
	if err != nil {
		return
	}
	msg := []byte(fmt.Sprintf("event: device\ndata: %s\n\n", data))
	h.BroadcastRaw(msg)
}

// BroadcastRaw sends raw bytes to all connected clients
func (h *SSEHub) BroadcastRaw(msg []byte) {
	select {
	case h.broadcast <- msg:
	default:
	}
}

// Stop cleanly terminates the hub
func (h *SSEHub) Stop() {
	close(h.stopCh)
}
