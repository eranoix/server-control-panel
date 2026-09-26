package videocall

// handlers_invite.go — magic-link invites + PIN flow + kick + WhatsApp share
//
// HandleInvite (POST /api/videocall/invite — mint magic-link),
// HandleInviteConsume (POST /api/videocall/invite/consume — exchange the
// link for a temporary JWT), HandlePIN (POST /api/videocall/pin — owner
// generates/revokes a short PIN), HandleKick (POST /api/videocall/kick — owner
// expels a member), pinAllow (per-IP rate limit), HandleJoinByPIN
// (POST /api/videocall/join-by-pin — guest joins via PIN),
// HandleInviteWhatsApp (POST /api/videocall/invite/whatsapp — sends the
// invite over WhatsApp).
//
// Extracted from handlers.go (keeps the same *Service receiver).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/auth"
)

// HandleInvite is the protected endpoint that mints a magic-link token.
// Only the room owner can invite. The returned URL embeds the token in the
// hash so it never appears in server logs.
//
// POST /api/videocall/invite  body: {room_id, ttl_min?}
// Response: {url, expires_at}
func (s *Service) HandleInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.InviteIssuer == nil {
		http.Error(w, "invite issuer not configured", http.StatusInternalServerError)
		return
	}
	var req struct {
		RoomID string `json:"room_id"`
		TTLMin int    `json:"ttl_min"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	// RoomForUser: a stranger who is not a member gets a 404 (existence is not
	// leaked); a member who is not the owner gets an explicit 403 ("only owner can invite").
	room, ok := s.RoomForUser(user, req.RoomID)
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}
	if room.Owner != user {
		http.Error(w, "only owner can invite", http.StatusForbidden)
		return
	}
	ttl := time.Duration(req.TTLMin) * time.Minute
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 60 * time.Minute
	}
	tok, jti, err := s.InviteIssuer.IssueVideocallInviteToken(user, room.ID, ttl)
	if err != nil {
		http.Error(w, "issue: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.InviteSessionsCk != nil {
		// Track the jti so single-use can be enforced via tombstone on consume.
		s.InviteSessionsCk.Add(jti, "INVITE:"+user, time.Now().Add(ttl).Unix())
	}
	s.audit("videocall.invite_issued", user, room.ID)
	// Build the URL. Scheme is HTTPS in real deploys; we leave it to the
	// client to prepend host since this server may sit behind a TLS proxy.
	host := r.Host
	scheme := "https"
	if r.TLS == nil && !strings.Contains(host, ":") {
		scheme = "http"
	}
	url := scheme + "://" + host + "/#videocall=join&token=" + tok
	writeJSONHTTP(w, map[string]any{
		"url":        url,
		"token":      tok,
		"expires_at": time.Now().Add(ttl).Unix(),
		"room_id":    room.ID,
	})
}

// HandleInviteConsume is the PUBLIC endpoint that the invitee hits when they
// open the magic-link URL. The invitee MUST already be authenticated to the
// panel — this endpoint just upgrades them from "not a member" to "member"
// of the specific room the invite is for. The invite jti is tombstoned so
// it can't be reused.
//
// This is wired in api.NewRouter's PROTECTED mux even though the invite
// token is what authorizes the action — we want the invitee's regular
// session as audit trail. The room_id comes from inside the invite token,
// not from the request, so an attacker can't aim it at a different room.
//
// POST /api/videocall/invite/consume  body: {token}
// Response: {room: Room}
func (s *Service) HandleInviteConsume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.InviteIssuer == nil {
		http.Error(w, "invite issuer not configured", http.StatusInternalServerError)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	// Uniform error so "forged token" cannot be told apart from "the room is
	// gone": any failure becomes the same "invalid invite". That stops an
	// attacker from crafting tokens to infer the existence of rooms.
	const genericInviteErr = "invalid invite"
	roomID, jti, err := s.InviteIssuer.VerifyVideocallInviteToken(req.Token)
	if err != nil {
		http.Error(w, genericInviteErr, http.StatusUnauthorized)
		return
	}
	if s.InviteSessionsCk != nil && jti != "" && !s.InviteSessionsCk.IsValid(jti) {
		http.Error(w, genericInviteErr, http.StatusUnauthorized)
		return
	}
	room, ok := s.Room(roomID)
	if !ok {
		http.Error(w, genericInviteErr, http.StatusUnauthorized)
		return
	}
	// Add invitee to members (idempotent). We bypass AddMember's owner check
	// because the invite itself IS the owner's grant. Use a direct write.
	s.mu.Lock()
	r2, ok := s.rooms[roomID]
	if !ok {
		s.mu.Unlock()
		http.Error(w, genericInviteErr, http.StatusUnauthorized)
		return
	}
	if r2.Owner != user && !containsString(r2.Members, user) {
		r2.Members = append(r2.Members, user)
		s.dirty = true
	}
	roomCopy := *r2
	s.mu.Unlock()
	if s.InviteSessionsCk != nil && jti != "" {
		s.InviteSessionsCk.Tombstone(jti)
	}
	s.audit("videocall.invite_consumed", user, roomID)
	writeJSONHTTP(w, map[string]any{"room": roomCopy})
	_ = room // silence unused
}

// HandlePIN manages a room's PIN (gen/regen/revoke). Owner-only.
//
//	POST   /api/videocall/pin     body: {room_id, ttl_hours}      → generate or regenerate
//	DELETE /api/videocall/pin     body: {room_id}                  → revoke
func (s *Service) HandlePIN(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			RoomID   string `json:"room_id"`
			TTLHours int    `json:"ttl_hours"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		pin, err := s.SetPIN(user, req.RoomID, req.TTLHours)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.audit("videocall.pin_generated", user, req.RoomID)
		room, _ := s.Room(req.RoomID)
		writeJSONHTTP(w, map[string]any{
			"pin":        pin,
			"expires_at": room.PINExpiresAt,
			"room_id":    req.RoomID,
		})
	case http.MethodDelete:
		var req struct {
			RoomID string `json:"room_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if err := s.RevokePIN(user, req.RoomID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.audit("videocall.pin_revoked", user, req.RoomID)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleKick expels a peer from the room. Owner-only. Body: {room_id, peer_id}.
// The server validates (a) the caller owns the room AND (b) the target peer is in
// that room. The owner may not expel themselves — use the "end call" button for that.
func (s *Service) HandleKick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		RoomID string `json:"room_id"`
		PeerID string `json:"peer_id"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.RoomID == "" || req.PeerID == "" {
		http.Error(w, "missing room_id or peer_id", http.StatusBadRequest)
		return
	}
	// Owner-only.
	room, ok := s.Room(req.RoomID)
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}
	if room.Owner != user {
		http.Error(w, "only the room owner can kick", http.StatusForbidden)
		return
	}
	// The target peer has to be in the SAME room (sanity + ACL).
	peers := s.Hub.PeersInRoomWithUser(req.RoomID)
	var targetUser string
	var found bool
	for _, p := range peers {
		if p.ID == req.PeerID {
			targetUser = p.User
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "peer not in this room", http.StatusNotFound)
		return
	}
	// Do not let the owner expel themselves by accident (the UI already hides the
	// button for yourself, but defensive: the target peer's user must NOT be the owner).
	if targetUser == user {
		http.Error(w, "cannot kick yourself", http.StatusBadRequest)
		return
	}
	reason := req.Reason
	if reason == "" {
		reason = "The room owner removed you from the call."
	}
	if _, ok := s.Hub.KickPeer(req.PeerID, reason); !ok {
		http.Error(w, "peer not found", http.StatusNotFound)
		return
	}
	s.audit("videocall.kicked", user, req.RoomID+" "+sanitizeAuditField(targetUser))
	w.WriteHeader(http.StatusNoContent)
}

// pinRateLimiter is a trivial per-IP limiter to defend the public
// join-by-PIN endpoint against brute force. 5 attempts/min/IP.
//
// It is not defence in depth — in real production we would use fail2ban or
// cloudflare turnstile. For a "call with my wife" scope, it is enough.
var pinRateLimiter = struct {
	mu      sync.Mutex
	buckets map[string]*pinBucket
}{buckets: make(map[string]*pinBucket)}

type pinBucket struct {
	hits []time.Time
}

func pinAllow(ip string) bool {
	pinRateLimiter.mu.Lock()
	defer pinRateLimiter.mu.Unlock()
	b, ok := pinRateLimiter.buckets[ip]
	if !ok {
		b = &pinBucket{}
		pinRateLimiter.buckets[ip] = b
	}
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	kept := b.hits[:0]
	for _, t := range b.hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	b.hits = kept
	allowed := len(b.hits) < 5
	if allowed {
		b.hits = append(b.hits, now)
	}
	// Memory leak fix: if the bucket is left empty after the trim, drop it from
	// the map. Without this, an attacker cycling through IPs polluted the map
	// indefinitely.
	if len(b.hits) == 0 {
		delete(pinRateLimiter.buckets, ip)
	}
	return allowed
}

// HandleJoinByPIN is PUBLIC (no auth). It accepts {pin, display_name} and
// issues a guest JWT good only for entering that room via /ws/videocall.
//
// POST /api/videocall/join-by-pin  body: {pin, display_name}
// Response 200: {room_id, room_name, token, expires_at}
// Response 401: invalid PIN (it does not distinguish "does not exist" from
//
//	"expired", so as not to help brute force).
//
// Response 429: rate limit.
func (s *Service) HandleJoinByPIN(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ip := auth.ClientIP(r)
	if !pinAllow(ip) {
		http.Error(w, "too many attempts — wait 1 minute", http.StatusTooManyRequests)
		return
	}
	if s.InviteIssuer == nil {
		http.Error(w, "guest tokens unavailable", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		PIN         string `json:"pin"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	pin := strings.ToUpper(strings.TrimSpace(req.PIN))
	displayName := strings.TrimSpace(req.DisplayName)
	if len(displayName) > 60 {
		displayName = displayName[:60]
	}
	if displayName == "" {
		displayName = "Convidado"
	}
	room, ok := s.LookupByPIN(pin)
	if !ok {
		// Same message for "does not exist" and "expired" — it does not help
		// brute force tell them apart.
		http.Error(w, "invalid or expired PIN", http.StatusUnauthorized)
		return
	}
	// Guest TTL: 4h. Enough for a normal call without lasting forever.
	const guestTTL = 4 * time.Hour
	tok, jti, err := s.InviteIssuer.IssueVideocallGuestToken(displayName, room.ID, guestTTL)
	if err != nil {
		http.Error(w, "issue: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// CRITICAL FIX: register the jti in InviteSessionsCk so revocation is
	// possible. Without it, RevokePIN did not take down guest tokens already
	// issued — a PIN leaked in a screenshot kept access for 4h even after
	// revocation. HandleGuestWS checks IsValid before accepting a connection.
	if s.InviteSessionsCk != nil && jti != "" {
		s.InviteSessionsCk.Add(jti, "GUEST:"+room.ID, time.Now().Add(guestTTL).Unix())
		s.guestMu.Lock()
		if s.guestJTIs == nil {
			s.guestJTIs = make(map[string][]string)
		}
		s.guestJTIs[room.ID] = append(s.guestJTIs[room.ID], jti)
		s.guestMu.Unlock()
	}
	// Sanitize displayName before logging (minor log injection: the name can
	// carry a newline/quotes and pollute the audit.log JSON-lines).
	s.audit("videocall.guest_joined", "guest:"+sanitizeAuditField(displayName), room.ID)
	writeJSONHTTP(w, map[string]any{
		"room_id":    room.ID,
		"room_name":  room.Name,
		"token":      tok,
		"expires_at": time.Now().Add(guestTTL).Unix(),
	})
}

// HandleInviteWhatsApp mints a magic-link invite AND sends it via the
// WhatsApp gateway (WAHA) to the recipient JID. The user picks the JID
// from the WhatsApp chats list in the UI; we only verify it's a non-empty
// string and let WAHA do the deep validation.
//
// POST /api/videocall/invite/whatsapp  body: {room_id, ttl_min?, to_jid, message?}
func (s *Service) HandleInviteWhatsApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.WhatsAppSender == nil {
		http.Error(w, "WhatsApp not configured — install the WAHA gateway", http.StatusServiceUnavailable)
		return
	}
	if s.InviteIssuer == nil {
		http.Error(w, "invite issuer not configured", http.StatusInternalServerError)
		return
	}
	var req struct {
		RoomID  string `json:"room_id"`
		TTLMin  int    `json:"ttl_min"`
		ToJID   string `json:"to_jid"`
		Message string `json:"message"` // optional custom message; default below
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ToJID) == "" {
		http.Error(w, "missing to_jid", http.StatusBadRequest)
		return
	}
	// Same as HandleInvite: a stranger gets 404, a non-owner member gets 403.
	room, ok := s.RoomForUser(user, req.RoomID)
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}
	if room.Owner != user {
		http.Error(w, "only owner can invite", http.StatusForbidden)
		return
	}
	ttl := time.Duration(req.TTLMin) * time.Minute
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = 60 * time.Minute
	}
	tok, jti, err := s.InviteIssuer.IssueVideocallInviteToken(user, room.ID, ttl)
	if err != nil {
		http.Error(w, "issue: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.InviteSessionsCk != nil {
		s.InviteSessionsCk.Add(jti, "INVITE:"+user, time.Now().Add(ttl).Unix())
	}
	host := r.Host
	scheme := "https"
	if r.TLS == nil && !strings.Contains(host, ":") {
		scheme = "http"
	}
	url := scheme + "://" + host + "/#videocall=join&token=" + tok
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		msg = fmt.Sprintf("Come talk to me in room *%s*: %s\n(link expires in %d min)", room.Name, url, int(ttl.Minutes()))
	} else {
		msg = msg + "\n" + url
	}
	if err := s.WhatsAppSender(room.Owner, req.ToJID, msg); err != nil {
		http.Error(w, "whatsapp: "+err.Error(), http.StatusBadGateway)
		return
	}
	s.audit("videocall.invite_whatsapp", user, room.ID)
	writeJSONHTTP(w, map[string]any{
		"url":        url,
		"expires_at": time.Now().Add(ttl).Unix(),
		"to_jid":     req.ToJID,
	})
}
