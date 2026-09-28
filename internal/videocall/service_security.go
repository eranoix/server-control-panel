package videocall

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (s *Service) MintTURN(user string, ttl time.Duration) TURNCredentials {
	return s.TURN.MintTURNCredentials(user, ttl)
}

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

func (s *Service) LookupByPIN(pin string) (Room, bool) {
	if pin == "" {
		return Room{}, false
	}
	now := time.Now().Unix()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found *Room
	for _, r := range s.rooms {
		if r.PIN == pin && (r.PINExpiresAt == 0 || r.PINExpiresAt >= now) {
			found = r
		}
	}
	if found == nil {
		return Room{}, false
	}
	return *found, true
}

const pinGCCutoff = 30 * time.Second

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

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
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
	if s.Hub != nil {
		for _, snap := range s.Hub.SnapshotPeers() {
			if snap.Peer.markLeaveAudited() {
				s.audit("videocall.leave", snap.User, snap.RoomID)
			}
		}
	}
	if s.flusherStop != nil {
		select {
		case <-s.flusherStop:
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
		return "Room"
	}
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

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
