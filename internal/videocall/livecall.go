package videocall

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"
)

type LiveCall struct {
	RoomID       string           `json:"room_id"`
	CallID       string           `json:"call_id"`
	StartedAt    int64            `json:"started_at"`
	LastActiveAt int64            `json:"last_active_at"`
	Participants map[string]int64 `json:"participants"`
	Users        map[string]int64 `json:"users"`
}

const (
	liveCallGraceSec = 90
	ringDedupSec     = 60
	liveCallTouchSec = 15
)

const (
	ringReasonNewCall    = "new-call"
	ringReasonOngoing    = "ongoing"
	ringReasonRejoin     = "rejoin"
	ringReasonResumeHint = "resume-hint"
)

type ringDecision struct {
	Ring   bool
	CallID string
	Reason string
	New    bool
}

type callRegistry struct {
	mu       sync.Mutex
	calls    map[string]*LiveCall
	lastRing map[string]int64
	path     string
	dirty    bool
}

func newCallRegistry(path string) *callRegistry {
	r := &callRegistry{
		calls:    make(map[string]*LiveCall),
		lastRing: make(map[string]int64),
		path:     path,
	}
	_ = r.load()
	return r
}

func (r *callRegistry) load() error {
	b, err := os.ReadFile(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var arr []*LiveCall
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil
	}
	now := time.Now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range arr {
		if c == nil || c.RoomID == "" || c.CallID == "" {
			continue
		}
		if now-c.LastActiveAt > liveCallGraceSec {
			continue
		}
		if c.Participants == nil {
			c.Participants = make(map[string]int64)
		}
		if c.Users == nil {
			c.Users = make(map[string]int64)
		}
		r.calls[c.RoomID] = c
	}
	return nil
}

func (r *callRegistry) save() error {
	r.mu.Lock()
	arr := make([]*LiveCall, 0, len(r.calls))
	for _, c := range r.calls {
		arr = append(arr, c)
	}
	r.dirty = false
	r.mu.Unlock()
	sort.Slice(arr, func(i, j int) bool { return arr[i].RoomID < arr[j].RoomID })
	return atomicWriteJSON(r.path, arr, 0o600)
}

func (r *callRegistry) liveLocked(roomID string, now int64) *LiveCall {
	c, ok := r.calls[roomID]
	if !ok {
		return nil
	}
	if now-c.LastActiveAt > liveCallGraceSec {
		return nil
	}
	return c
}

func (r *callRegistry) OnJoin(roomID, user, clientID string, resume bool, now int64) ringDecision {
	r.mu.Lock()
	defer r.mu.Unlock()

	if c := r.liveLocked(roomID, now); c != nil {
		reason := ringReasonOngoing
		if clientID != "" {
			if _, known := c.Participants[clientID]; known {
				reason = ringReasonRejoin
			}
		}
		if clientID != "" {
			c.Participants[clientID] = now
		}
		if user != "" {
			if _, seen := c.Users[user]; !seen {
				c.Users[user] = now
			}
		}
		c.LastActiveAt = now
		r.dirty = true
		return ringDecision{Ring: false, CallID: c.CallID, Reason: reason}
	}

	c := &LiveCall{
		RoomID:       roomID,
		CallID:       randomID(),
		StartedAt:    now,
		LastActiveAt: now,
		Participants: make(map[string]int64),
		Users:        make(map[string]int64),
	}
	if clientID != "" {
		c.Participants[clientID] = now
	}
	if user != "" {
		c.Users[user] = now
	}
	r.calls[roomID] = c
	r.dirty = true
	if resume {
		return ringDecision{Ring: false, CallID: c.CallID, Reason: ringReasonResumeHint, New: true}
	}
	return ringDecision{Ring: true, CallID: c.CallID, Reason: ringReasonNewCall, New: true}
}

func (r *callRegistry) OnPeerGone(roomID, clientID string, graceful bool, now int64) (bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.calls[roomID]
	if !ok {
		return false, ""
	}
	if !graceful {
		r.dirty = true
		return false, ""
	}
	if clientID != "" {
		delete(c.Participants, clientID)
	}
	c.LastActiveAt = now
	r.dirty = true
	if len(c.Participants) == 0 {
		delete(r.calls, roomID)
		return true, c.CallID
	}
	return false, c.CallID
}

func (r *callRegistry) Touch(roomIDs []string, now int64) {
	if len(roomIDs) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range roomIDs {
		if c, ok := r.calls[id]; ok {
			c.LastActiveAt = now
			r.dirty = true
		}
	}
}

func (r *callRegistry) GC(now int64) []LiveCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ended []LiveCall
	for id, c := range r.calls {
		if now-c.LastActiveAt > liveCallGraceSec {
			ended = append(ended, *c)
			delete(r.calls, id)
			r.dirty = true
		}
	}
	sort.Slice(ended, func(i, j int) bool { return ended[i].RoomID < ended[j].RoomID })
	return ended
}

func (r *callRegistry) AllowRing(roomID, recipient string, now int64) bool {
	if recipient == "" {
		return false
	}
	key := roomID + "\x00" + recipient
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, ok := r.lastRing[key]; ok && now-last < ringDedupSec {
		return false
	}
	r.lastRing[key] = now
	for k, ts := range r.lastRing {
		if now-ts > ringDedupSec*4 {
			delete(r.lastRing, k)
		}
	}
	return true
}

func (r *callRegistry) ActiveCall(roomID string, now int64) (LiveCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.liveLocked(roomID, now)
	if c == nil {
		return LiveCall{}, false
	}
	return *c, true
}

func (r *callRegistry) Dirty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dirty
}
