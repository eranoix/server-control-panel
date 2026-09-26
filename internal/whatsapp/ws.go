package whatsapp

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/wsorigin"
)

// Keepalive parameters mirror internal/pty/pty.go — survives idle proxies
// (30s timeouts) and browser tab throttling.
const (
	wsPongWait   = 45 * time.Second
	wsPingPeriod = 25 * time.Second
	wsWriteWait  = 10 * time.Second
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Anti-CSRF: reject a WS upgrade whose Origin differs from the Host.
	CheckOrigin: wsorigin.CheckSameHost,
}

// Broadcaster fans events out to all connected UI clients. Send() is
// non-blocking: a slow client gets dropped rather than stalling the producer.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[*subscriber]struct{}
}

type subscriber struct {
	ch   chan WSEvent
	done chan struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[*subscriber]struct{})}
}

// Send is the producer-side entry point. Non-blocking per subscriber;
// dropped events are lost (next reconnect refetches state via /api/whatsapp/
// status anyway, so transient loss is fine).
//
// Malformed events — ones missing the key field the frontend uses in
// x-for :key — are DISCARDED here; otherwise a single truncated webhook
// brings Alpine.js down with "Cannot read properties of undefined (reading
// 'after')". We sanitise at the source rather than defensively in the
// frontend: if it got here without an ID or JID, there was nothing to render
// anyway.
func (b *Broadcaster) Send(ev WSEvent) {
	if !validWSEvent(ev) {
		return
	}
	if ev.TS == 0 {
		ev.TS = time.Now().Unix()
	}
	b.mu.RLock()
	for sub := range b.subs {
		select {
		case sub.ch <- ev:
		default:
			// queue full → drop. The subscriber will reconcile on next
			// /api/whatsapp/status poll or full chat list refresh.
		}
	}
	b.mu.RUnlock()
}

// validWSEvent makes sure the event carries every field the frontend needs
// to render without crashing (Alpine.js's x-for :key tolerates neither
// undefined nor duplicates — see internal/api/sanitize.go).
func validWSEvent(ev WSEvent) bool {
	switch ev.Kind {
	case "":
		return false
	case "status", "qr":
		return ev.State != nil
	case "message":
		return ev.Message != nil && ev.Message.ID != "" && ev.Message.ChatJID != ""
	case "chat":
		return ev.Chat != nil && ev.Chat.JID != ""
	case "ack", "revoked":
		return ev.AckID != ""
	default:
		// An unknown Kind is let through — the UI should ignore what it does not
		// recognise, and being permissive here avoids blocking future features.
		return true
	}
}

func (b *Broadcaster) subCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func (b *Broadcaster) subscribe() *subscriber {
	sub := &subscriber{
		ch:   make(chan WSEvent, 64),
		done: make(chan struct{}),
	}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub
}

func (b *Broadcaster) unsubscribe(sub *subscriber) {
	b.mu.Lock()
	delete(b.subs, sub)
	b.mu.Unlock()
	close(sub.done)
}

// HandleWS upgrades the request to a WebSocket and pumps events from the
// broadcaster. Caller (Router) must ensure auth.Middleware ran first.
func (s *Service) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetReadLimit(1 << 20)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	sub := s.Broadcaster.subscribe()
	defer s.Broadcaster.unsubscribe(sub)

	// Push an initial state snapshot so the client doesn't need a separate
	// REST call to bootstrap.
	st := s.Store.State()
	if err := writeJSON(conn, WSEvent{Kind: "status", State: &st, TS: time.Now().Unix()}); err != nil {
		return
	}

	// Reader goroutine: discards messages but keeps the pong handler alive.
	// Panic recovery — ReadMessage can raise on a corrupted frame.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[whatsapp/ws] reader panic recovered: %v", r)
			}
			_ = conn.Close()
		}()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case ev := <-sub.ch:
			if err := writeJSON(conn, ev); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-sub.done:
			return
		}
	}
}

func writeJSON(conn *websocket.Conn, v any) error {
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}
