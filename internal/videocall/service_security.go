package videocall

// service_security.go — MintTURN + PIN flow + helpers + Close
//
// MintTURN (a Service wrapper delegating to TURN.MintTURNCredentials).
// SetPIN / RevokePIN / LookupByPIN — the short-PIN flow for guests.
// gcExpiredPINs (cleans up in the background). randomPIN/randomID. Close (final
// flush of the state to disk). Sanitization helpers and (mustJSON/
// containsString/sanitizeName/sanitizeAuditField).
//
// Extracted from service.go.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// MintTURN returns ephemeral TURN credentials valid for ttl. Thin wrapper
// over TURNConfig.MintTURNCredentials so handlers don't poke at internals.
func (s *Service) MintTURN(user string, ttl time.Duration) TURNCredentials {
	return s.TURN.MintTURNCredentials(user, ttl)
}

// SetPIN generates (or regenerates) a short PIN for the room. Owner only.
// ttlHours=0 → no expiry (manual control). It returns the PIN in plaintext for
// the UI to show — the caller has already validated that the user is the owner.
func (s *Service) SetPIN(owner, roomID string, ttlHours int) (string, error) {
	pin := randomPIN(8)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return "", errors.New("room not found")
	}
	if r.Owner != owner {
		return "", errors.New("not authorized")
	}
	r.PIN = pin
	if ttlHours > 0 {
		r.PINExpiresAt = time.Now().Add(time.Duration(ttlHours) * time.Hour).Unix()
	} else {
		r.PINExpiresAt = 0
	}
	s.dirty = true
	return pin, nil
}

// RevokePIN clears the PIN. Owner-only. It also tombstones ALL the guest JTIs
// already issued for that room — guest tokens handed out before the revocation
// stop working immediately (HandleGuestWS checks IsValid on the upgrade).
// Before this fix, a leaked PIN kept access for 4h even after revocation.
// Hosts already connected carry on until a natural hangup (their WS was
// already accepted).
func (s *Service) RevokePIN(owner, roomID string) error {
	s.mu.Lock()
	r, ok := s.rooms[roomID]
	if !ok {
		s.mu.Unlock()
		return errors.New("room not found")
	}
	if r.Owner != owner {
		s.mu.Unlock()
		return errors.New("not authorized")
	}
	r.PIN = ""
	r.PINExpiresAt = 0
	s.dirty = true
	s.mu.Unlock()

	// Tombstone ALL guest jtis registered for this room.
	if s.InviteSessionsCk != nil {
		s.guestMu.Lock()
		jtis := s.guestJTIs[roomID]
		delete(s.guestJTIs, roomID)
		s.guestMu.Unlock()
		for _, jti := range jtis {
			s.InviteSessionsCk.Tombstone(jti)
		}
	}
	return nil
}

// LookupByPIN returns the room whose PIN matches. Careful: the caller MUST
// rate-limit this (see HandleJoinByPIN). It iterates the map COMPLETELY (no
// early return on a match) to equalize timing — an attacker cannot tell
// "found it fast" from "ran through everything".
func (s *Service) LookupByPIN(pin string) (Room, bool) {
	if pin == "" {
		return Room{}, false
	}
	now := time.Now().Unix()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found *Room
	for _, r := range s.rooms {
		// Always compare ALL of them, even after a match — it avoids a timing leak.
		// subtle.ConstantTimeCompare would be ideal but requires equal lengths.
		if r.PIN == pin && (r.PINExpiresAt == 0 || r.PINExpiresAt >= now) {
			found = r
		}
	}
	if found == nil {
		return Room{}, false
	}
	return *found, true
}

// pinGCCutoff is how long after expiry the PIN gets zeroed from disk. A small
// buffer so it does not race with the last legitimate request.
const pinGCCutoff = 30 * time.Second

// gcExpiredPINs clears PIN+PINExpiresAt from rooms that are past their time.
// It runs in the flusher to avoid extra load. Before the GC, the lookup already
// rejects (LookupByPIN checks exp), but keeping the old PIN on disk is waste and
// potential info disclosure if rooms.json leaks.
// (The UI side already hid expired ones; what was missing was clearing the disk.)
func (s *Service) gcExpiredPINs() {
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rooms {
		if r.PIN != "" && r.PINExpiresAt > 0 && r.PINExpiresAt+int64(pinGCCutoff.Seconds()) < now {
			r.PIN = ""
			r.PINExpiresAt = 0
			s.dirty = true
		}
	}
}

// helpers ---------------------------------------------------------------

// randomPIN generates an alphanumeric code without confusing chars (0/O/1/I/l).
// 8 chars from a 32-symbol alphabet = ~40 bits — basically impossible to
// brute-force with a rate limit on the endpoint.
func randomPIN(n int) string {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(out)
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sanitizeClientID cleans the client_id coming from the query (auth) or from the
// jti (guest): it keeps only [A-Za-z0-9] and cuts at 64 chars. It returns "" if
// nothing survives — the caller falls back to randomID(). This guarantees the
// ClientID is a predictable map key and blocks injection of control chars into
// audit/log. The jti is a UUID with hyphens; stripping them preserves uniqueness
// and determinism (same token → same jti → same result, stable across reconnects).
func sanitizeClientID(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b = append(b, c)
		}
	}
	return string(b)
}

// Close shuts down the subsystems that own a background goroutine (push/recording
// flushers) and signals the main flusher to exit with a final flush.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	// CRITICAL step of the fix: stopping the heartbeat does one last touch on
	// LastActiveAt and writes the live calls out. That is what makes a deploy
	// (a ~2s restart) land INSIDE the grace window when the clients reconnect —
	// without that final write, the call in progress "aged" during the restart
	// and the first reconnect was read as a new call all over again.
	if s.callTickerStop != nil {
		select {
		case <-s.callTickerStop:
		default:
			close(s.callTickerStop)
			<-s.callTickerDone
		}
	}
	if s.Calls != nil {
		_ = s.Calls.save()
	}
	// Close the audit log of the calls in progress. On SIGTERM the hijacked WS
	// goroutines die without running their defers, so the audit was left with a
	// join and no leave (that is how the diagnosis spotted the restarts). The CAS
	// in markLeaveAudited guarantees a single line, even if a late defer still
	// manages to run.
	if s.Hub != nil {
		for _, snap := range s.Hub.SnapshotPeers() {
			if snap.Peer.markLeaveAudited() {
				s.audit("videocall.leave", snap.User, snap.RoomID)
			}
		}
	}
	// Signal the flusher to shut down with a final flush. Idempotent — a double
	// close would panic, so it uses a select default to detect a channel that is
	// already closed.
	if s.flusherStop != nil {
		select {
		case <-s.flusherStop:
			// already closed, skip
		default:
			close(s.flusherStop)
			<-s.flusherDone
		}
	}
	if s.Push != nil {
		_ = s.Push.Close()
	}
	if s.Recordings != nil {
		_ = s.Recordings.Close()
	}
	return nil
}

// sanitizeAuditField escapes the characters that pollute the audit.log
// JSON-lines. It replaces control chars (\n, \r, \t), quotes and backslash
// with '?'. It truncates by runes — cutting UTF-8 in half breaks log parsing.
func sanitizeAuditField(s string) string {
	runes := []rune(s)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	out := make([]rune, 0, len(runes))
	for _, c := range runes {
		if c < 0x20 || c == '"' || c == '\\' || c == 0x7f {
			out = append(out, '?')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Sala"
	}
	// Strip control chars, keep printable + accents. Cap at 80 RUNES (not
	// bytes) — len(s) used to cut in the middle of a multi-byte UTF-8 sequence
	// (a 4-byte emoji), producing an invalid string.
	out := make([]rune, 0, 80)
	for _, r := range s {
		if r >= 0x20 && r != 0x7f {
			out = append(out, r)
			if len(out) >= 80 {
				break
			}
		}
	}
	return string(out)
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// mustJSON marshals v or returns null. The name is historical — it does NOT
// panic (it returns `null` on failure), which matches SignalingMsg.Payload
// being json.RawMessage (the downstream Unmarshal then yields nil). It is
// only used for known structs where Marshal realistically never fails.
// Renaming it to jsonOrNull was considered but would touch too many call
// sites.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
