package videocall

import (
	"sync"
	"time"
)

type PresenceHub struct {
	mu   sync.RWMutex
	subs map[string]map[*PresenceSub]struct{}

	RingPolicy func(user, deviceID string, now int64) bool
}

type PresenceSub struct {
	User     string
	DeviceID string
	Ch       chan PresenceEvent
	Done     chan struct{}
}

type PresenceEvent struct {
	Type     string `json:"type"`
	RoomID   string `json:"room_id,omitempty"`
	RoomName string `json:"room_name,omitempty"`
	From     string `json:"from,omitempty"`
	CallID   string `json:"call_id,omitempty"`
}

func NewPresenceHub() *PresenceHub {
	return &PresenceHub{subs: make(map[string]map[*PresenceSub]struct{})}
}

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

func (p *PresenceHub) deliver(sub *PresenceSub, ev PresenceEvent) {
	select {
	case sub.Ch <- ev:
	default:
	}
}

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
				continue
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

func (p *PresenceHub) IsOnline(user string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	set, ok := p.subs[user]
	return ok && len(set) > 0
}

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
