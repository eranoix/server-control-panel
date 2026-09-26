package videocall

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"
)

// LiveCall is a room's LIVE call — the missing piece that lets the server
// tell the difference between "someone is calling" and "someone just
// reconnected".
//
// Root cause: before this, the server decided to ring by looking at
// `len(existing) == 0` at Join time, i.e. "I am the first live peer in the
// hub → this is a new call". Since the hub is 100% in memory, every restart
// of the process (a deploy, ~2s) emptied it: the first client to reconnect
// — and reconnection has been automatic for a while — looked like a first
// joiner and fired a ring at ALL the room's members, in the middle of a call
// that was already running. The audit log showed 4 rings in 8min, each one
// matching a deploy.
//
// LiveCall fixes that by giving the call an identity that:
//
//   - has a grace window: it survives `liveCallGraceSec` with NO peer
//     connected, which is the window in which the process restarts and the
//     clients reconnect;
//   - is PERSISTED to disk (data/videocalls/active-calls.json) and reread at
//     boot — without that the grace window would not help, because the
//     restart takes the memory with it;
//   - knows its participants by ClientID (a stable identity across
//     reconnections), so a rejoin by the same client is never mistaken for a
//     new call.
//
// The LastActiveAt field is kept fresh by a heartbeat (Service.callTicker)
// while there are peers in the hub, and is written one last time in Close().
// In a 40-minute call with no join/leave at all, that is what guarantees the
// deploy at minute 40 still lands inside the grace window.
type LiveCall struct {
	RoomID       string           `json:"room_id"`
	CallID       string           `json:"call_id"`
	StartedAt    int64            `json:"started_at"`
	LastActiveAt int64            `json:"last_active_at"`
	Participants map[string]int64 `json:"participants"` // clientID -> unix of the last sign of life
	Users        map[string]int64 `json:"users"`        // username -> unix of the first join
}

const (
	// liveCallGraceSec is how long the call survives with no peer connected.
	// It has to comfortably cover a restart plus the client's reconnect backoff
	// (RECONNECT_BACKOFF_MAX = 30s in videocall.js). 90s leaves room for a slow
	// deploy without keeping a dead call alive long enough to swallow a genuine
	// incoming call right afterwards.
	liveCallGraceSec = 90
	// ringDedupSec is the minimum interval between two rings for the SAME
	// recipient in the SAME room. A second net: even if the session logic has a
	// hole, the user never takes more than one ring per minute.
	ringDedupSec = 60
	// liveCallTouchSec is the period of the heartbeat that keeps LastActiveAt
	// fresh while the call is alive.
	liveCallTouchSec = 15
)

// ringReason explains the decision taken at join. It goes to the audit log and
// to the vpsm_videocall_rings_total{reason} metric — it is what turns "it keeps
// ringing" into a number, before and after the fix.
const (
	ringReasonNewCall    = "new-call"    // a genuinely new call → RINGS
	ringReasonOngoing    = "ongoing"     // joined a call already active → no ring
	ringReasonRejoin     = "rejoin"      // same ClientID reconnecting → no ring
	ringReasonResumeHint = "resume-hint" // client declared a reopen → no ring
)

// ringDecision is the registry's verdict for a join.
type ringDecision struct {
	Ring   bool
	CallID string
	Reason string
	// New says the call session was created just now (useful for the audit).
	New bool
}

// callRegistry holds the live calls plus the ring dedup memory.
// It has a lock of its own: nothing here touches the rooms lock (Service.mu) nor
// the Hub's, and the calls come from the Join/Leave path, which is already rare.
type callRegistry struct {
	mu       sync.Mutex
	calls    map[string]*LiveCall // roomID -> live call
	lastRing map[string]int64     // roomID + "\x00" + recipient -> unix of the last ring
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

// load rereads the live calls from disk. Entries whose LastActiveAt has already
// left the grace window are discarded on read: yesterday's call must not silence
// today's ring. Tolerant of a corrupt file (starts empty, same as rooms).
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
		return nil // tolerant
	}
	now := time.Now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range arr {
		if c == nil || c.RoomID == "" || c.CallID == "" {
			continue
		}
		if now-c.LastActiveAt > liveCallGraceSec {
			continue // outside the grace window: a dead call does not come back
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

// save writes atomically. Called at the points of change (join/leave/end) and in
// the heartbeat when there is something dirty — the file is tiny and joins are rare.
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

// live returns the room's live call, or nil if it does not exist / has already
// left the grace window. The caller MUST hold r.mu.
func (r *callRegistry) liveLocked(roomID string, now int64) *LiveCall {
	c, ok := r.calls[roomID]
	if !ok {
		return nil
	}
	if now-c.LastActiveAt > liveCallGraceSec {
		return nil // expired; the GC removes it
	}
	return c
}

// OnJoin registers the peer in the room's call and returns the ring decision.
//
// The rule is: RING ONLY when the call session is born. A reconnection by the
// same ClientID, joining an already active call, and a reopen declared by the
// client (`resume=1`) never ring.
//
// `resume` is a HINT from the client (videocall.js only sends it from inside
// reopenSignaling). It can only LOWER the ring, never raise it — so a tampered
// client can at most silence its own incoming call, which is exactly what a
// "do not alert me" button would do anyway.
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

	// No live call at all: this is a new session. Ring — unless the client has
	// declared that this is the resumption of a call it believes to be in
	// progress (e.g. the grace window blew because the server was down longer
	// than expected).
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

// OnPeerGone accounts for a peer leaving.
//
// `graceful` distinguishes the two worlds:
//
//   - true  → the client sent "leave" (the user hung up on purpose). The
//     participant leaves at once; if it was the last one, the call ENDS right
//     away, and the pending "incoming call" modals on the other devices are
//     cleared.
//   - false → the connection simply dropped (network, deploy, tab closed). The
//     call STAYS alive inside the grace window, waiting for the reconnect — and
//     that is what keeps a rejoin from turning into a ringer.
//
// Returns (ended, callID): ended=true when the call ended just now.
func (r *callRegistry) OnPeerGone(roomID, clientID string, graceful bool, now int64) (bool, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.calls[roomID]
	if !ok {
		return false, ""
	}
	if !graceful {
		// A drop: do not touch the participants. LastActiveAt stays the
		// reference, and the heartbeat stops renewing once the hub empties —
		// so the GC ends the call when the grace window blows.
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

// Touch renews LastActiveAt for the rooms that have a live peer in the hub.
// Without it, a long call with no join/leave would age out and the deploy at
// minute 40 would fall outside the grace window — ringing all over again.
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

// GC removes the calls whose grace window blew and returns the ended IDs, so the
// caller can tell the members (clearing the phantom modal).
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

// AllowRing applies the per-recipient dedup. It returns true (and stamps) only
// if that recipient has not been rung in that room within the last ringDedupSec.
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
	// Opportunistic pruning: the dedup memory does not need to keep anything
	// older than the window itself.
	for k, ts := range r.lastRing {
		if now-ts > ringDedupSec*4 {
			delete(r.lastRing, k)
		}
	}
	return true
}

// ActiveCall returns a copy of the room's live call (so /api/videocall/rooms can
// show "in call", and for the tests).
func (r *callRegistry) ActiveCall(roomID string, now int64) (LiveCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.liveLocked(roomID, now)
	if c == nil {
		return LiveCall{}, false
	}
	return *c, true
}

// Dirty reports whether there is a pending change to be written.
func (r *callRegistry) Dirty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dirty
}
