package videocall

import (
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Hub struct {
	mu       sync.RWMutex
	peers    map[string]*Peer
	byRoom   map[string]map[string]struct{}
	rateMu   sync.Mutex
	rate     map[string]*rateBucket
	maxPerRm int
}

type Peer struct {
	ID           string
	User         string
	RoomID       string
	ClientID     string
	SendCh       chan SignalingMsg
	closed       chan struct{}
	leaveAudited atomic.Bool
}

func (p *Peer) markLeaveAudited() bool {
	if p == nil {
		return false
	}
	return p.leaveAudited.CompareAndSwap(false, true)
}

type rateBucket struct {
	tokens int
	last   time.Time
}

func NewHub(maxPerRoom int) *Hub {
	if maxPerRoom <= 0 {
		maxPerRoom = 4
	}
	return &Hub{
		peers:    make(map[string]*Peer),
		byRoom:   make(map[string]map[string]struct{}),
		rate:     make(map[string]*rateBucket),
		maxPerRm: maxPerRoom,
	}
}

var (
	ErrRoomFull        = errors.New("room is full")
	ErrPeerNotInRoom   = errors.New("peer not in any room")
	ErrTargetNotInRoom = errors.New("target peer is not in the same room")
	ErrTargetUnknown   = errors.New("target peer not connected")
	ErrRateLimited     = errors.New("rate limit exceeded")
	ErrAlreadyJoined   = errors.New("user already in room")
)

func (h *Hub) Join(p *Peer) ([]PeerInfo, error) {
	if p.RoomID == "" || p.ID == "" {
		return nil, errors.New("peer missing id or room")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.byRoom[p.RoomID]
	if !ok {
		set = make(map[string]struct{})
		h.byRoom[p.RoomID] = set
	}
	if p.ClientID != "" {
		var ghosts []string
		for id := range set {
			if old, ok := h.peers[id]; ok && old.ClientID == p.ClientID {
				ghosts = append(ghosts, id)
			}
		}
		for _, id := range ghosts {
			log.Printf("videocall: evicting ghost peer id=%s clientID=%s room=%s (reconnect by new peer id=%s)", id, p.ClientID, p.RoomID, p.ID)
			h.evictLocked(id, set)
		}
	}
	if len(set) >= h.maxPerRm {
		return nil, ErrRoomFull
	}
	if p.User != "" && !strings.HasPrefix(p.User, "guest:") {
		for id := range set {
			if other, ok := h.peers[id]; ok && other.User == p.User {
				return nil, ErrAlreadyJoined
			}
		}
	}
	existing := make([]PeerInfo, 0, len(set))
	for id := range set {
		if other, ok := h.peers[id]; ok {
			existing = append(existing, PeerInfo{ID: other.ID, User: other.User, ClientID: other.ClientID})
		}
	}
	h.peers[p.ID] = p
	set[p.ID] = struct{}{}
	for id := range set {
		if id == p.ID {
			continue
		}
		other := h.peers[id]
		h.deliver(other, SignalingMsg{
			Type:    "peer-joined",
			From:    p.ID,
			Payload: mustJSON(PeerInfo{ID: p.ID, User: p.User, ClientID: p.ClientID}),
		})
	}
	return existing, nil
}

func (h *Hub) Leave(peerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.peers[peerID]
	if !ok {
		return
	}
	delete(h.peers, peerID)
	if set, ok := h.byRoom[p.RoomID]; ok {
		delete(set, peerID)
		if len(set) == 0 {
			delete(h.byRoom, p.RoomID)
		}
		for id := range set {
			other := h.peers[id]
			h.deliver(other, SignalingMsg{Type: "peer-left", From: peerID})
		}
	}
	close(p.closed)
}

func (h *Hub) OccupiedRooms() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.byRoom))
	for room, set := range h.byRoom {
		if len(set) > 0 {
			out = append(out, room)
		}
	}
	return out
}

type PeerSnapshot struct {
	Peer   *Peer
	User   string
	RoomID string
}

func (h *Hub) SnapshotPeers() []PeerSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]PeerSnapshot, 0, len(h.peers))
	for _, p := range h.peers {
		out = append(out, PeerSnapshot{Peer: p, User: p.User, RoomID: p.RoomID})
	}
	return out
}

func (h *Hub) evictLocked(peerID string, set map[string]struct{}) {
	p, ok := h.peers[peerID]
	if !ok {
		return
	}
	delete(h.peers, peerID)
	delete(set, peerID)
	for id := range set {
		if other, ok := h.peers[id]; ok {
			h.deliver(other, SignalingMsg{Type: "peer-left", From: peerID})
		}
	}
	close(p.closed)
	h.cleanupRate(peerID)
}

func (h *Hub) Disconnect(peerID string) {
	h.mu.RLock()
	_, exists := h.peers[peerID]
	h.mu.RUnlock()
	if !exists {
		return
	}
	h.Leave(peerID)
}

func (h *Hub) Forward(fromID string, msg SignalingMsg) error {
	if !h.allowMsg(fromID) {
		return ErrRateLimited
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	from, ok := h.peers[fromID]
	if !ok {
		return ErrPeerNotInRoom
	}
	to, ok := h.peers[msg.To]
	if !ok {
		return ErrTargetUnknown
	}
	if to.RoomID != from.RoomID {
		return ErrTargetNotInRoom
	}
	msg.From = fromID
	h.deliver(to, msg)
	return nil
}

func (h *Hub) Broadcast(fromID string, msg SignalingMsg) error {
	if !h.allowMsg(fromID) {
		return ErrRateLimited
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	from, ok := h.peers[fromID]
	if !ok {
		return ErrPeerNotInRoom
	}
	msg.From = fromID
	set, ok := h.byRoom[from.RoomID]
	if !ok {
		return nil
	}
	for id := range set {
		if id == fromID {
			continue
		}
		other := h.peers[id]
		h.deliver(other, msg)
	}
	return nil
}

func (h *Hub) PeersInRoom(roomID string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	set := h.byRoom[roomID]
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

func (h *Hub) PeerUser(peerID string) (string, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.peers[peerID]
	if !ok {
		return "", false
	}
	return p.User, true
}

func (h *Hub) KickPeer(peerID, reason string) (string, bool) {
	h.mu.RLock()
	p, ok := h.peers[peerID]
	h.mu.RUnlock()
	if !ok {
		return "", false
	}
	user := p.User
	h.deliver(p, SignalingMsg{Type: "kicked", Error: reason})
	time.AfterFunc(50*time.Millisecond, func() {
		h.Leave(peerID)
	})
	return user, true
}

func (h *Hub) PeersInRoomWithUser(roomID string) []PeerInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	set := h.byRoom[roomID]
	out := make([]PeerInfo, 0, len(set))
	for id := range set {
		if p := h.peers[id]; p != nil {
			out = append(out, PeerInfo{ID: p.ID, User: p.User, ClientID: p.ClientID})
		}
	}
	return out
}

func (h *Hub) deliver(p *Peer, msg SignalingMsg) {
	if p == nil {
		return
	}
	select {
	case <-p.closed:
		return
	case p.SendCh <- msg:
	default:
	}
}

const (
	rateBurst   = 50
	rateRefill  = 50
	rateMinStep = 20 * time.Millisecond
)

func (h *Hub) allowMsg(peerID string) bool {
	h.rateMu.Lock()
	defer h.rateMu.Unlock()
	now := time.Now()
	b, ok := h.rate[peerID]
	if !ok {
		b = &rateBucket{tokens: rateBurst, last: now}
		h.rate[peerID] = b
	}
	dt := now.Sub(b.last)
	if dt > rateMinStep {
		add := int(dt.Seconds() * float64(rateRefill))
		if add > 0 {
			b.tokens += add
			if b.tokens > rateBurst {
				b.tokens = rateBurst
			}
			b.last = now
		}
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

func (h *Hub) cleanupRate(peerID string) {
	h.rateMu.Lock()
	delete(h.rate, peerID)
	h.rateMu.Unlock()
}
