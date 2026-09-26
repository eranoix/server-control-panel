package mobilebff

import (
	"encoding/json"
	"sync"
)

// events_hub.go is the connection registry behind GET /ws/mobile-events
// a single multiplexed socket where the client
// subscribes/unsubscribes to named channels ("notify.inbox",
// "queue.job:<id>", ...) instead of one socket per concern. The Hub itself
// knows NOTHING about jobs, notify events, or any other domain — it only
// tracks which connection wants which channel name and fans out an
// Envelope to the ones that do, gated by an optional per-call visibility
// predicate the PUBLISHER supplies. This keeps ownership/RBAC decisions
// (e.g. "only the job's owner or an admin sees queue.job:<id>") entirely on
// the caller side, exactly like handleQueueWS's own ownership check.

// Envelope is the wire shape for every server->client frame on
// /ws/mobile-events after the initial subscribe/unsubscribe control
// exchange, matching ARCHITECTURE.md verbatim:
//
//	{"v": 1, "channel": "queue.job:abc123", "type": "progress", "data": {"pct": 42}}
type Envelope struct {
	V       int             `json:"v"`
	Channel string          `json:"channel"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// hubConn is one registered connection: the authenticated user (for a
// publisher's visibility predicate), the set of channels it currently
// wants, and a send function that writes one Envelope to the underlying
// socket. send is supplied by the WS handler and already serializes
// concurrent writers (the handler's own ack writes, the ping ticker, and
// Hub.Publish calls from unrelated goroutines all share one mutex there) —
// the Hub never touches the socket directly.
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

// Hub is the /ws/mobile-events connection registry. Zero value is not
// usable — construct with NewHub. Safe for concurrent use: register/
// unregister/Publish may all be called from different goroutines (each WS
// connection's own read loop, plus any producer — the notify.Router worker,
// a queue job callback, etc. — calling Publish).
type Hub struct {
	mu    sync.RWMutex
	conns map[uint64]*hubConn
	next  uint64
}

// NewHub constructs an empty Hub.
func NewHub() *Hub { return &Hub{conns: make(map[uint64]*hubConn)} }

// register adds a new connection for user, whose outbound frames are
// written via send. Returns the hubConn the caller (the WS handler) keeps
// for subscribe/unsubscribe calls, and must pass to unregister on
// disconnect.
func (h *Hub) register(user string, send func(Envelope) error) *hubConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	c := &hubConn{id: h.next, user: user, send: send, subs: make(map[string]bool)}
	h.conns[c.id] = c
	return c
}

// unregister removes a connection. Safe to call once per registered
// connection; a second call is a harmless no-op (map delete of an absent
// key). Must be called on every disconnect path (clean close, abrupt
// close, read error) so a dead socket never keeps receiving Publish calls
// or leaks in the registry.
func (h *Hub) unregister(c *hubConn) {
	h.mu.Lock()
	delete(h.conns, c.id)
	h.mu.Unlock()
}

// Publish fans env out to every currently-registered connection subscribed
// to channel, additionally gated by visible(user) when visible is non-nil
// (nil visible = every subscriber receives it). env.Channel/env.V are set
// here so callers don't have to repeat them; env.Type/env.Data carry the
// payload.
//
// Publish does NOT buffer or replay: a connection that subscribes after an
// event was published never sees it here (see notify.Router.Inbox /
// GET /api/notify/inbox for the pollable history a mobile client uses to
// fill any gap across a reconnect) — this is what makes "reconnect never
// duplicates an event" true by construction: there is no per-channel log
// for the Hub to replay from.
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

// Len reports the number of currently-registered connections. Test seam
// (also useful for a future /api/mobile/v1 health/debug surface).
func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// HasSubscriber reports whether at least one currently-registered connection
// is subscribed to channel. Lets a periodic publisher (e.g. the ops.health
// ticker, events_bridge_ops.go) skip doing any work — computing a status
// snapshot, running health checks — when nobody is listening.
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
