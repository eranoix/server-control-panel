package videocall

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/wsorigin"
)

// Keepalive parameters mirror internal/whatsapp/ws.go — survives idle proxies
// and browser tab throttling.
const (
	wsPongWait   = 45 * time.Second
	wsPingPeriod = 25 * time.Second
	wsWriteWait  = 10 * time.Second
	// Read limit guards against a misbehaving client streaming megabytes of
	// "ICE candidate". Real WebRTC signaling payloads cap out around 5KB.
	wsReadLimit = 64 * 1024
	// SendCh buffer. Generous because ICE candidates burst in the first
	// second of a call (often 10-20). Dropped overflow is handled by the
	// pong-timeout eviction loop.
	sendBuf = 64
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Anti-CSRF: reject a WS upgrade whose Origin differs from the Host.
	CheckOrigin: wsorigin.CheckSameHost,
}

// HandleWS is the entrypoint for /ws/videocall. The caller (Router) must
// ensure auth.Middleware ran first — we trust auth.UserFrom(r).
//
// Lifecycle:
//  1. Validate user can join the room (ACL via Service.CanJoin).
//  2. Upgrade to WS.
//  3. Register a Peer with the Hub, emit `joined` snapshot to the client and
//     `peer-joined` to existing room peers.
//  4. Spawn a writer goroutine that pumps SendCh + emits keepalive pings.
//  5. Reader loop on the main goroutine: parse + dispatch to Hub.
//  6. Cleanup on exit: Leave the hub (broadcasts peer-left), then close.
func (s *Service) HandleWS(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	roomID := r.URL.Query().Get("room_id")
	if roomID == "" {
		http.Error(w, "missing room_id", http.StatusBadRequest)
		return
	}
	if !s.CanJoin(user, roomID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	// Stable client identity. The front end persists a uuid in sessionStorage
	// (per tab, per room) and sends it in ?client_id=. Sanitize + fall back to
	// randomID() if missing/invalid (old clients without the param land in that
	// fallback → behaviour identical to before, with no eviction).
	clientID := sanitizeClientID(r.URL.Query().Get("client_id"))
	if clientID == "" {
		clientID = randomID()
	}
	// `resume=1` is the hint that this WS is the REOPENING of a call already
	// in progress (videocall.js only sends it from inside reopenSignaling),
	// not a new call. It can only LOWER the ringer — see
	// callRegistry.OnJoin.
	resume := r.URL.Query().Get("resume") == "1"
	peer := &Peer{
		ID:       randomID(),
		ClientID: clientID,
		User:     user,
		RoomID:   roomID,
		SendCh:   make(chan SignalingMsg, sendBuf),
		closed:   make(chan struct{}),
	}
	existing, err := s.Hub.Join(peer)
	if err != nil {
		// Map the Hub's errors to user-facing PT-BR messages. It also keeps the
		// specific type ("error-full", "error-conflict") so the UI can act
		// distinctly (e.g. offer to close the other tab).
		errMsg, errType := mapJoinErrorPT(err)
		_ = writeJSON(conn, SignalingMsg{Type: errType, Error: errMsg})
		return
	}
	s.audit("videocall.join", user, roomID)
	// The decision to ring lives in announceJoin, which consults the persisted
	// call SESSION instead of inferring "new call" from `len(existing) == 0`.
	// That inference turned every reconnect into a ring — and every deploy
	// empties the hub, so each deploy in the middle of a call rang the phone
	// all over again.
	s.announceJoin(roomID, user, user, clientID, resume)
	// `graceful` distinguishes "the user hung up" (the client sends `leave`) from
	// "the connection dropped" (deploy, network, tab closed). Only the first ends
	// the call session at once; the second keeps it alive inside the grace window,
	// waiting for the reconnect — which is what stops a rejoin from becoming a ring.
	graceful := false
	defer func() {
		s.Hub.Leave(peer.ID)
		s.Hub.cleanupRate(peer.ID)
		s.onPeerGone(roomID, peer.ClientID, graceful)
		if peer.markLeaveAudited() {
			s.audit("videocall.leave", user, roomID)
		}
	}()

	room, _ := s.Room(roomID)
	join := JoinResponse{
		PeerID:         peer.ID,
		Room:           room,
		Peers:          existing,
		TURN:           s.MintTURN(user, time.Hour),
		PolitenessSeed: peer.ID,
	}
	if err := writeJSON(conn, SignalingMsg{Type: "joined", Payload: mustJSON(join)}); err != nil {
		return
	}
	graceful = s.runWSLoop(conn, peer, user, roomID)
}

func pushErr(p *Peer, errMsg string) error {
	return pushTo(p, SignalingMsg{Type: "error", Error: errMsg})
}

func pushTo(p *Peer, msg SignalingMsg) error {
	select {
	case p.SendCh <- msg:
		return nil
	case <-p.closed:
		return nil
	default:
		return nil // drop, same policy as Hub.deliver
	}
}

// mapJoinErrorPT maps Hub errors to user-facing PT-BR messages. It returns
// (message, type). The specific type lets the UI act distinctly.
func mapJoinErrorPT(err error) (string, string) {
	switch {
	case errors.Is(err, ErrRoomFull):
		return "Room is full — the 4-person limit was reached.", "error-full"
	case errors.Is(err, ErrAlreadyJoined):
		return "Your account is already in this room in another tab. Close that one to join here.", "error-conflict"
	default:
		return "Could not join the room: " + err.Error(), "error"
	}
}

// HandleGuestWS is the public entry point for the signaling WS. Unlike
// HandleWS, this handler MUST validate the token itself — there is no
// auth.Middleware above it. It accepts ONLY kind=videocall_guest tokens and
// confines the user to the room embedded in the token. Display name comes from the token.
func (s *Service) HandleGuestWS(w http.ResponseWriter, r *http.Request) {
	if s.InviteIssuer == nil {
		http.Error(w, "guest disabled", http.StatusServiceUnavailable)
		return
	}
	tok := r.URL.Query().Get("token")
	if tok == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	tokenRoom, displayName, err := s.InviteIssuer.VerifyVideocallGuestToken(tok)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	// CRITICAL FIX: validate the jti against InviteSessionsCk. RevokePIN
	// tombstones every GUEST:<roomID> jti issued, taking down guest tokens
	// already handed out when the owner revokes the PIN.
	if s.InviteSessionsCk != nil {
		jti, jerr := s.InviteIssuer.ExtractJTI(tok)
		if jerr == nil && jti != "" && !s.InviteSessionsCk.IsValid(jti) {
			http.Error(w, "token revoked", http.StatusUnauthorized)
			return
		}
	}
	roomID := r.URL.Query().Get("room_id")
	if roomID != "" && roomID != tokenRoom {
		// The token and the query disagree — do not trust it.
		http.Error(w, "token/room mismatch", http.StatusForbidden)
		return
	}
	if roomID == "" {
		roomID = tokenRoom
	}
	if _, ok := s.Room(roomID); !ok {
		http.Error(w, "room not found", http.StatusNotFound)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(wsReadLimit)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	// The user identifier visible to peers: "guest:Esposa". It distinguishes
	// them from a real user (who comes with no prefix).
	user := "guest:" + displayName
	// The guest's stable identity = the token's jti. The front end reuses the
	// SAME this.token when the WS reconnects (videocall.js), so the jti is stable
	// and lets us evict the ghost of the previous connection. The jti is random
	// per issuance (each PIN POST mints a new token), so two distinct guests on
	// the same PIN have distinct jtis → no eviction war. Falls back to randomID()
	// if the jti is missing/errors (a full page reload already becomes a new tile).
	clientID := ""
	if jti, jerr := s.InviteIssuer.ExtractJTI(tok); jerr == nil {
		clientID = sanitizeClientID(jti)
	}
	if clientID == "" {
		clientID = randomID()
	}
	resume := r.URL.Query().Get("resume") == "1"
	peer := &Peer{
		ID:       randomID(),
		ClientID: clientID,
		User:     user,
		RoomID:   roomID,
		SendCh:   make(chan SignalingMsg, sendBuf),
		closed:   make(chan struct{}),
	}
	existing, err := s.Hub.Join(peer)
	if err != nil {
		errMsg, errType := mapJoinErrorPT(err)
		_ = writeJSON(conn, SignalingMsg{Type: errType, Error: errMsg})
		return
	}
	s.audit("videocall.guest_joined_ws", user, roomID)
	// Same decision as the authenticated path (see HandleWS): what rings the
	// bell is the call session, not "I am the first in the hub".
	s.announceJoin(roomID, user, displayName, clientID, resume)
	graceful := false
	defer func() {
		s.Hub.Leave(peer.ID)
		s.Hub.cleanupRate(peer.ID)
		s.onPeerGone(roomID, peer.ClientID, graceful)
		if peer.markLeaveAudited() {
			s.audit("videocall.guest_left", user, roomID)
		}
	}()
	room, _ := s.Room(roomID)
	join := JoinResponse{
		PeerID: peer.ID, Room: room, Peers: existing,
		TURN:           s.MintTURN(user, time.Hour),
		PolitenessSeed: peer.ID,
	}
	if err := writeJSON(conn, SignalingMsg{Type: "joined", Payload: mustJSON(join)}); err != nil {
		return
	}
	graceful = s.runWSLoop(conn, peer, user, roomID)
}

// runWSLoop is the shared reader/writer core between HandleWS (logged-in
// users) and HandleGuestWS (guests via PIN). Extracted to avoid massive
// duplication between the two entry points.
//
// Shutdown synchronization: the writer goroutine listens on peer.SendCh /
// ticker / peer.closed / readerExit. When the reader (the loop below) ends (a
// network error or a "leave" message), we close readerExit and WAIT for the
// writer to finish before returning. Without that, the writer would stay alive
// until the caller's `defer Hub.Leave` closed peer.closed — a small window
// where the writer writes to a conn the reader already abandoned (no leak, but loose).
// It returns `graceful`: true when the client ended on purpose (a "leave"
// message, sent on the user's hangup), false when the connection died. That
// bit separates "hung up" from "dropped" for the call session.
func (s *Service) runWSLoop(conn *websocket.Conn, peer *Peer, user, roomID string) bool {
	writerDone := make(chan struct{})
	readerExit := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("videocall writer goroutine: recovered panic: %v", rec)
			}
		}()
		ticker := time.NewTicker(wsPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case msg := <-peer.SendCh:
				if err := writeJSON(conn, msg); err != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			case <-peer.closed:
				return
			case <-readerExit:
				return
			}
		}
	}()
	defer func() {
		close(readerExit)
		<-writerDone
	}()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg SignalingMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		// "reset": o cliente que detectou a propria conexao travada (ICE
		// parado sem candidato local) pede ao outro lado que recrie a dele.
		case "offer", "answer", "ice", "chat", "reset":
			if msg.To == "" {
				continue
			}
			if err := s.Hub.Forward(peer.ID, msg); err != nil {
				_ = pushErr(peer, err.Error())
			}
		case "state":
			s.auditStateMsg(user, roomID, msg)
			if err := s.Hub.Broadcast(peer.ID, msg); err != nil {
				_ = pushErr(peer, err.Error())
			}
		// Owner-only actions. The server VALIDATES that the sender owns the room
		// before relaying — a malicious peer cannot forge mute/kick.
		// Payload is opaque to the server, but type carries the semantics.
		//   owner-mute     → force mute (audio off) on a specific peer
		//   owner-unmute   → request unmute (the peer may refuse — privacy)
		//   owner-camera   → force camera off on a specific peer
		//   owner-kick     → expel the peer (the server closes its conn)
		//   owner-lock     → broadcast to the room: locked=true/false (informational;
		//                    the server enforces at /join via Service.Join)
		//   owner-transfer → broadcast: the new owner is payload.userId
		case "owner-mute", "owner-unmute", "owner-camera", "owner-kick":
			if !s.IsRoomOwner(roomID, user) {
				_ = pushErr(peer, "forbidden: not the room owner")
				s.audit("videocall.owner_action_denied:"+msg.Type, user, roomID)
				continue
			}
			if msg.To == "" {
				_ = pushErr(peer, "owner action: 'to' is required")
				continue
			}
			s.audit("videocall.owner_action:"+msg.Type, user, roomID)
			if msg.Type == "owner-kick" {
				// Send "kicked" to the target peer and force a close. Hub.Leave fires
				// peer-left to the others automatically.
				if err := s.Hub.Forward(peer.ID, msg); err != nil {
					_ = pushErr(peer, err.Error())
				}
				// Schedule the disconnect 200ms later so the target has a chance to
				// see the message before the close.
				go func(targetID string) {
					time.Sleep(200 * time.Millisecond)
					s.Hub.Disconnect(targetID)
				}(msg.To)
			} else {
				if err := s.Hub.Forward(peer.ID, msg); err != nil {
					_ = pushErr(peer, err.Error())
				}
			}
		case "owner-lock", "owner-transfer":
			if !s.IsRoomOwner(roomID, user) {
				_ = pushErr(peer, "forbidden: not the room owner")
				s.audit("videocall.owner_action_denied:"+msg.Type, user, roomID)
				continue
			}
			s.audit("videocall.owner_action:"+msg.Type, user, roomID)
			if err := s.Hub.Broadcast(peer.ID, msg); err != nil {
				_ = pushErr(peer, err.Error())
			}
		case "leave":
			return true
		case "ping":
			_ = pushTo(peer, SignalingMsg{Type: "pong"})
		}
	}
	return false
}

// HandlePresenceWS is the long-lived presence connection a logged-in panel
// user opens on page load. It receives "incoming-call" events when someone
// joins a room they're a member of. No body — purely server→client.
//
// Lighter than full PWA push: works only when the panel is open in a tab
// (or installed PWA), but doesn't need VAPID/service-worker plumbing.
func (s *Service) HandlePresenceWS(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetReadLimit(4 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		return nil
	})

	// Presence now identifies itself by DEVICE (a stable uuid in localStorage
	// plus a human-readable label derived from the UA). That is what lets the
	// owner say "do not ring on this computer" without losing the call on the
	// others. An old client sends nothing → empty deviceID → it always rings.
	deviceID := sanitizeDeviceID(r.URL.Query().Get("device_id"))
	label := sanitizeDeviceLabel(r.URL.Query().Get("device_label"))
	if s.Devices != nil && deviceID != "" {
		s.Devices.Seen(user, deviceID, label)
	}
	sub := s.Presence.Subscribe(user, deviceID)
	defer s.Presence.Unsubscribe(sub)

	// Initial hello so the client knows the connection is live.
	if err := writeJSON(conn, PresenceEvent{Type: "hello"}); err != nil {
		return
	}

	// Discard reader (presence is unidirectional) so the pong handler stays
	// alive. Same pattern as whatsapp/ws.go.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				_ = conn.Close()
				return
			}
		}
	}()

	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case ev := <-sub.Ch:
			if err := writeJSON(conn, ev); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-sub.Done:
			return
		}
	}
}

// audit fires the configured callback if any, ignoring nil. Centralized so
// every callsite is a one-liner.
func (s *Service) audit(action, user, target string) {
	if s == nil || s.AuditFn == nil {
		return
	}
	s.AuditFn(action, user, target)
}

// auditStateMsg pulls a couple of meaningful action keys out of the
// `state` signaling payload — recording start/stop and screen share
// start/stop. Mic/cam toggles are noisy and skipped.
func (s *Service) auditStateMsg(user, roomID string, msg SignalingMsg) {
	if s == nil || s.AuditFn == nil {
		return
	}
	var p map[string]any
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return
	}
	if v, ok := p["recording"].(string); ok {
		if v == "on" {
			s.AuditFn("videocall.recording_start", user, roomID)
		} else if v == "off" {
			s.AuditFn("videocall.recording_stop", user, roomID)
		}
	}
	if v, ok := p["screen"].(string); ok {
		if v == "on" {
			s.AuditFn("videocall.screenshare_start", user, roomID)
		} else if v == "off" {
			s.AuditFn("videocall.screenshare_stop", user, roomID)
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
