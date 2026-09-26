package videocall

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/auth"
)

// Wires the cross-package user-from-context resolution used by push.go.
// push.go can't import internal/auth without dragging the dependency into
// a file we'd rather keep minimal. This indirection costs nothing at runtime.
func init() {
	authUserFunc = func(r *http.Request) string { return auth.UserFrom(r) }
}

// HandleRooms dispatches GET/POST/DELETE on /api/videocall/rooms.
//
//	GET    /api/videocall/rooms          — list rooms this user owns or is member of
//	POST   /api/videocall/rooms          — body: {name}; creates room owned by caller
//	DELETE /api/videocall/rooms?id=<id>  — owner only; evicts active peers
func (s *Service) HandleRooms(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSONHTTP(w, s.ListForUser(user))
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		room, err := s.CreateRoom(user, req.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSONHTTP(w, room)
	case http.MethodPatch:
		// Rename a room. Body: {id, name}.
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if err := s.RenameRoom(user, req.ID, req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.audit("videocall.room_renamed", user, req.ID)
		room, _ := s.Room(req.ID)
		writeJSONHTTP(w, room)
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if err := s.DeleteRoom(user, id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleMembers manages a room's member list.
//
//	POST   /api/videocall/members          — body: {room_id, member}; add
//	DELETE /api/videocall/members?room_id=&member=  — remove
//
// Both require the caller to be the room owner.
func (s *Service) HandleMembers(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			RoomID string `json:"room_id"`
			Member string `json:"member"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		req.Member = strings.TrimSpace(req.Member)
		if req.Member == "" {
			http.Error(w, "member required", http.StatusBadRequest)
			return
		}
		if err := s.AddMember(user, req.RoomID, req.Member); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		roomID := r.URL.Query().Get("room_id")
		member := r.URL.Query().Get("member")
		if err := s.RemoveMember(user, roomID, member); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleTURN returns ephemeral TURN credentials valid for 1h. The frontend
// fetches these immediately before opening RTCPeerConnection so the creds
// are fresh. Re-fetched if a call lasts longer than the TTL.
func (s *Service) HandleTURN(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	creds := s.MintTURN(user, time.Hour)
	writeJSONHTTP(w, creds)
}

// HandleHistory returns the user's recent call sessions for the bandwidth
// dashboard. GET /api/videocall/history?limit=50
func (s *Service) HandleHistory(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	writeJSONHTTP(w, s.HistoryForUser(user, limit))
}

// HandleRecordSession persists a finished call's totals. Posted by the
// client on hangup. Untrusted input — the user can only record sessions
// for themselves (server stamps `user` from auth context).
//
// POST /api/videocall/sessions  body: CallSession (subset; user is overridden)
func (s *Service) HandleRecordSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user := auth.UserFrom(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var cs CallSession
	if err := json.NewDecoder(r.Body).Decode(&cs); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	cs.User = user // never trust client-supplied user
	// Sanity caps — reject obviously bogus values. Also upper caps on
	// BytesSent/Recv: a malicious client could send 1e18 and pollute the
	// history dashboard. 100 GB is a generous ceiling (a 24h call at
	// 1080p ~ 50 GB).
	const maxBytes int64 = 100 * 1024 * 1024 * 1024 // 100 GB
	if cs.DurationS < 0 || cs.DurationS > 24*3600 {
		cs.DurationS = 0
	}
	if cs.BytesSent < 0 || cs.BytesSent > maxBytes {
		cs.BytesSent = 0
	}
	if cs.BytesRecv < 0 || cs.BytesRecv > maxBytes {
		cs.BytesRecv = 0
	}
	s.RecordCallSession(cs)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSONHTTP(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
