package mobilebff

import (
	"encoding/json"
	"sync"
)

type Envelope struct {
	V       int             `json:"v"`
	Channel string          `json:"channel"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type hubConn struct {
	id   uint64
	user string
	send func(Envelope) error

	mu   sync.Mutex
	subs map[string]bool
}

func (c *hubConn) subscribe(channel string) {
	c.mu.Lock()
	c.subs[channel] = true
	c.mu.Unlock()
}

func (c *hubConn) unsubscribe(channel string) {
	c.mu.Lock()
	delete(c.subs, channel)
	c.mu.Unlock()
}

func (c *hubConn) subscribed(channel string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subs[channel]
}

type Hub struct {
	mu    sync.RWMutex
	conns map[uint64]*hubConn
	next  uint64
}

func NewHub() *Hub { return &Hub{conns: make(map[uint64]*hubConn)} }

func (h *Hub) register(user string, send func(Envelope) error) *hubConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	c := &hubConn{id: h.next, user: user, send: send, subs: make(map[string]bool)}
	h.conns[c.id] = c
	return c
}

func (h *Hub) unregister(c *hubConn) {
	h.mu.Lock()
	delete(h.conns, c.id)
	h.mu.Unlock()
}

func (h *Hub) Publish(channel string, env Envelope, visible func(user string) bool) {
	env.Channel = channel
	if env.V == 0 {
		env.V = 1
	}
	h.mu.RLock()
	targets := make([]*hubConn, 0, len(h.conns))
	for _, c := range h.conns {
		if !c.subscribed(channel) {
			continue
		}
		if visible != nil && !visible(c.user) {
			continue
		}
		targets = append(targets, c)
	}
	h.mu.RUnlock()
	for _, c := range targets {
		_ = c.send(env)
	}
}

func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

func (h *Hub) HasSubscriber(channel string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if c.subscribed(channel) {
			return true
		}
	}
	return false
}
