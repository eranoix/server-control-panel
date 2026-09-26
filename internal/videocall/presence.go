package videocall

import (
	"sync"
	"time"
)

// PresenceHub is the "panel-wide" notification bus for videocall. It is
// SEPARATE from the per-room signaling Hub:
//
//   - Hub  tracks who's IN a room and routes peer-to-peer signaling.
//   - PresenceHub tracks who's logged into the panel and pushes room-level
//     events to them (e.g. "someone is calling you in room X").
//
// One user can have multiple presence connections (laptop + phone) — we
// fan-out to all of them. Connections are anonymous channels: the hub
// doesn't know what the consumer is; the WS adapter wraps the channel and
// pumps to the socket.
//
// The fan-out is no longer blind. Each sub carries the DeviceID of its
// device, and RINGING events go through RingPolicy (the per-device policy,
// in devices.go). CONTROL events — "answered on another device", "call
// ended" — ignore the policy on purpose: they CLEAR state on the screen,
// and silencing a cleanup would only leave a phantom modal behind.
type PresenceHub struct {
	mu   sync.RWMutex
	subs map[string]map[*PresenceSub]struct{} // user -> set of subs

	// RingPolicy decides whether a specific device should ring. nil = it always
	// rings (graceful degradation when the deviceStore did not come up).
	RingPolicy func(user, deviceID string, now int64) bool
}

// PresenceSub is one subscription (one WS conn). Send is non-blocking with a
// generous queue; if it fills, the consumer is slow and we drop — analogous
// to Hub.deliver.
//
// Cleanup contract (important):
//   - Whoever calls Subscribe MUST call Unsubscribe when they are done.
//   - The consumer loop MUST `select` on `<-sub.Done` in every branch —
//     relying on `Ch` being closed alone causes a silent leak, because
//     Unsubscribe closes `Done`, not `Ch`.
//   - Send on `Ch` is non-blocking (drop-on-full); the reader may be slow.
type PresenceSub struct {
	User string
	// DeviceID identifies the DEVICE (not the tab): a stable uuid in
	// localStorage. Empty on old clients — in that case the per-device policy
	// does not apply and the sub rings normally.
	DeviceID string
	Ch       chan PresenceEvent
	Done     chan struct{}
}

// PresenceEvent shapes server→client messages on the presence channel.
// Kept narrow on purpose; new event kinds get a new field rather than a
// JSON.RawMessage payload so the schema is self-documenting.
//
// Kinds:
//
//	hello                   — the connection is live (handshake)
//	incoming-call           — someone started a call in a room of yours
//	call-answered-elsewhere — YOU answered on another device; stop ringing
//	call-ended              — the call is over; clear the pending modal
//
// CallID identifies the call SESSION (not the room): it is what lets the
// client deduplicate rings and match a cancellation to the right ring.
type PresenceEvent struct {
	Type     string `json:"type"`
	RoomID   string `json:"room_id,omitempty"`
	RoomName string `json:"room_name,omitempty"`
	From     string `json:"from,omitempty"` // username of the caller / joiner
	CallID   string `json:"call_id,omitempty"`
}

func NewPresenceHub() *PresenceHub {
	return &PresenceHub{subs: make(map[string]map[*PresenceSub]struct{})}
}

// Subscribe registers a new presence subscription for `user`. The returned
// *PresenceSub MUST be passed to Unsubscribe when the connection closes.
func (p *PresenceHub) Subscribe(user, deviceID string) *PresenceSub {
	sub := &PresenceSub{
		User:     user,
		DeviceID: deviceID,
		Ch:       make(chan PresenceEvent, 32),
		Done:     make(chan struct{}),
	}
	p.mu.Lock()
	set, ok := p.subs[user]
	if !ok {
		set = make(map[*PresenceSub]struct{})
		p.subs[user] = set
	}
	set[sub] = struct{}{}
	p.mu.Unlock()
	return sub
}

func (p *PresenceHub) Unsubscribe(sub *PresenceSub) {
	if sub == nil {
		return
	}
	p.mu.Lock()
	if set, ok := p.subs[sub.User]; ok {
		delete(set, sub)
		if len(set) == 0 {
			delete(p.subs, sub.User)
		}
	}
	p.mu.Unlock()
	close(sub.Done)
}

// deliver enqueues non-blocking. The caller holds p.mu (read).
func (p *PresenceHub) deliver(sub *PresenceSub, ev PresenceEvent) {
	select {
	case sub.Ch <- ev:
	default:
		// queue full → drop. Subscriber is slow; their next
		// reconnect will re-snapshot via /api/videocall/rooms.
	}
}

// NotifyUsers fans `ev` out to every active subscription of every user in
// `users`, with no policy filter — use it for CONTROL events (call-ended)
// that have to arrive even on a silenced device, otherwise the pending modal
// never goes away. Skips empty user names and `excludeUser`.
func (p *PresenceHub) NotifyUsers(users []string, excludeUser string, ev PresenceEvent) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, u := range users {
		if u == "" || u == excludeUser {
			continue
		}
		for sub := range p.subs[u] {
			p.deliver(sub, ev)
		}
	}
}

// NotifyUser delivers to ALL connections of ONE user, with no exclusion and no
// policy. It is the "call-answered-elsewhere" channel: when Sam answers on the
// Mac, HIS other devices have to stop ringing.
func (p *PresenceHub) NotifyUser(user string, ev PresenceEvent) {
	if user == "" {
		return
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for sub := range p.subs[user] {
		p.deliver(sub, ev)
	}
}

// Ring is the RINGING fan-out: it applies the per-device policy and returns the
// users who actually received at least one ring (the rest are either offline or
// have silenced every device). The return value feeds the audit and the metric —
// it is how you prove, afterwards, that the ringing stopped happening.
func (p *PresenceHub) Ring(users []string, excludeUser string, ev PresenceEvent) []string {
	now := time.Now().Unix()
	var rang []string
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, u := range users {
		if u == "" || u == excludeUser {
			continue
		}
		delivered := false
		for sub := range p.subs[u] {
			if p.RingPolicy != nil && !p.RingPolicy(u, sub.DeviceID, now) {
				continue // device silenced by its owner
			}
			p.deliver(sub, ev)
			delivered = true
		}
		if delivered {
			rang = append(rang, u)
		}
	}
	return rang
}

// IsOnline reports whether `user` has at least one live presence sub.
// Handy for UI badges ("alice is online") later — not used today.
func (p *PresenceHub) IsOnline(user string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	set, ok := p.subs[user]
	return ok && len(set) > 0
}

// WantsRing reports whether the user has at least one device ONLINE and NOT
// silenced. It is the Web Push gate: if the panel is open but the user has
// silenced that device, the off-app push still makes sense — without it,
// silencing the Mac would silence the phone along with it.
func (p *PresenceHub) WantsRing(user string) bool {
	now := time.Now().Unix()
	p.mu.RLock()
	defer p.mu.RUnlock()
	for sub := range p.subs[user] {
		if p.RingPolicy == nil || p.RingPolicy(user, sub.DeviceID, now) {
			return true
		}
	}
	return false
}
