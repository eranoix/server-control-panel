package videocall

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/auth"
)

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
	s.audit("videocall.invite_issued", user, room.ID)
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
	_ = room
}

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
	room, ok := s.Room(req.RoomID)
	if !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}
	if room.Owner != user {
		http.Error(w, "only the room owner can kick", http.StatusForbidden)
		return
	}
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
	if len(b.hits) == 0 {
		delete(pinRateLimiter.buckets, ip)
	}
	return allowed
}

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
		displayName = "Guest"
	}
	room, ok := s.LookupByPIN(pin)
	if !ok {
		http.Error(w, "invalid or expired PIN", http.StatusUnauthorized)
		return
	}
	const guestTTL = 4 * time.Hour
	tok, jti, err := s.InviteIssuer.IssueVideocallGuestToken(displayName, room.ID, guestTTL)
	if err != nil {
		http.Error(w, "issue: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.InviteSessionsCk != nil && jti != "" {
		s.InviteSessionsCk.Add(jti, "GUEST:"+room.ID, time.Now().Add(guestTTL).Unix())
		s.guestMu.Lock()
		if s.guestJTIs == nil {
			s.guestJTIs = make(map[string][]string)
		}
		s.guestJTIs[room.ID] = append(s.guestJTIs[room.ID], jti)
		s.guestMu.Unlock()
	}
	s.audit("videocall.guest_joined", "guest:"+sanitizeAuditField(displayName), room.ID)
	writeJSONHTTP(w, map[string]any{
		"room_id":    room.ID,
		"room_name":  room.Name,
		"token":      tok,
		"expires_at": time.Now().Add(guestTTL).Unix(),
	})
}

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
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.ToJID) == "" {
		http.Error(w, "missing to_jid", http.StatusBadRequest)
		return
	}
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
