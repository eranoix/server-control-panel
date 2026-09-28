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

const (
	wsPongWait   = 45 * time.Second
	wsPingPeriod = 25 * time.Second
	wsWriteWait  = 10 * time.Second
	wsReadLimit  = 64 * 1024
	sendBuf      = 64
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     wsorigin.CheckSameHost,
}

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

	clientID := sanitizeClientID(r.URL.Query().Get("client_id"))
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
		errMsg, errType := mapJoinError(err)
		_ = writeJSON(conn, SignalingMsg{Type: errType, Error: errMsg})
		return
	}
	s.audit("videocall.join", user, roomID)
	s.announceJoin(roomID, user, user, clientID, resume)
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
		return nil
	}
}

func mapJoinError(err error) (string, string) {
	switch {
	case errors.Is(err, ErrRoomFull):
		return "Room is full — the 4-person limit was reached.", "error-full"
	case errors.Is(err, ErrAlreadyJoined):
		return "Your account is already in this room in another tab. Close that one to join here.", "error-conflict"
	default:
		return "Could not join the room: " + err.Error(), "error"
	}
}

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
	if s.InviteSessionsCk != nil {
		jti, jerr := s.InviteIssuer.ExtractJTI(tok)
		if jerr == nil && jti != "" && !s.InviteSessionsCk.IsValid(jti) {
			http.Error(w, "token revoked", http.StatusUnauthorized)
			return
		}
	}
	roomID := r.URL.Query().Get("room_id")
	if roomID != "" && roomID != tokenRoom {
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

	user := "guest:" + displayName
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
		errMsg, errType := mapJoinError(err)
		_ = writeJSON(conn, SignalingMsg{Type: errType, Error: errMsg})
		return
	}
	s.audit("videocall.guest_joined_ws", user, roomID)
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
				if err := s.Hub.Forward(peer.ID, msg); err != nil {
					_ = pushErr(peer, err.Error())
				}
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

	deviceID := sanitizeDeviceID(r.URL.Query().Get("device_id"))
	label := sanitizeDeviceLabel(r.URL.Query().Get("device_label"))
	if s.Devices != nil && deviceID != "" {
		s.Devices.Seen(user, deviceID, label)
	}
	sub := s.Presence.Subscribe(user, deviceID)
	defer s.Presence.Unsubscribe(sub)

	if err := writeJSON(conn, PresenceEvent{Type: "hello"}); err != nil {
		return
	}

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

func (s *Service) audit(action, user, target string) {
	if s == nil || s.AuditFn == nil {
		return
	}
	s.AuditFn(action, user, target)
}

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
