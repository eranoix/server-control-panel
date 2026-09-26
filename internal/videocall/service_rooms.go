package videocall

// service_rooms.go — Room CRUD + membership
//
// CreateRoom/DeleteRoom/RenameRoom, AddMember/RemoveMember,
// evictUserFromRoom (removal helper with cleanup), Room/RoomForUser
// (lookups), IsRoomOwner, CanJoin (entry gate),
// ListForUser (rooms visible to the user).
//
// Extracted from service.go (keeps the same *Service receiver).

import (
	"errors"
	"sort"
	"strings"
	"time"
)

// CreateRoom inserts a new room owned by `user`. Name is sanitized but not
// validated for uniqueness — collisions are fine, the ID is the real key.
func (s *Service) CreateRoom(user, name string) (*Room, error) {
	if user == "" {
		return nil, errors.New("owner required")
	}
	r := &Room{
		ID:        randomID(),
		Name:      sanitizeName(name),
		Owner:     user,
		Members:   []string{},
		CreatedAt: time.Now().Unix(),
	}
	s.mu.Lock()
	s.rooms[r.ID] = r
	s.dirty = true
	s.mu.Unlock()
	return r, nil
}

// DeleteRoom removes a room. Only the owner can delete it. Active peers in
// the room get evicted via Hub.Leave so their connections close gracefully.
func (s *Service) DeleteRoom(user, roomID string) error {
	s.mu.Lock()
	r, ok := s.rooms[roomID]
	if !ok {
		s.mu.Unlock()
		return errors.New("room not found")
	}
	if r.Owner != user {
		s.mu.Unlock()
		return errors.New("not authorized")
	}
	delete(s.rooms, roomID)
	s.dirty = true
	s.mu.Unlock()
	for _, peerID := range s.Hub.PeersInRoom(roomID) {
		s.Hub.Leave(peerID)
	}
	return nil
}

// RenameRoom changes a room's name. Owner-only. It keeps the ID and everything
// else — only the display label changes. It validates the name with the same
// sanitizer used in CreateRoom.
func (s *Service) RenameRoom(owner, roomID, newName string) error {
	// CreateRoom accepts an empty name (it defaults to "Sala"); here in rename
	// we want to be stricter — empty = error, to avoid an accident.
	if strings.TrimSpace(newName) == "" {
		return errors.New("invalid name")
	}
	clean := sanitizeName(newName)
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return errors.New("room not found")
	}
	if r.Owner != owner {
		return errors.New("not authorized")
	}
	r.Name = clean
	s.dirty = true
	return nil
}

// AddMember grants `member` permanent access to `roomID`. Only owner can.
// Idempotent — adding the same username twice is a no-op.
func (s *Service) AddMember(owner, roomID, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return errors.New("room not found")
	}
	if r.Owner != owner {
		return errors.New("not authorized")
	}
	for _, m := range r.Members {
		if m == member {
			return nil
		}
	}
	r.Members = append(r.Members, member)
	s.dirty = true
	return nil
}

// RemoveMember revokes a member's access. Active peer connections of that
// user in the room are evicted via Hub.Leave so they disconnect immediately.
// Only the owner can remove members; the owner cannot remove themselves
// (use DeleteRoom for that).
func (s *Service) RemoveMember(owner, roomID, member string) error {
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
	if member == r.Owner {
		s.mu.Unlock()
		return errors.New("cannot remove owner")
	}
	kept := r.Members[:0]
	removed := false
	for _, m := range r.Members {
		if m == member {
			removed = true
			continue
		}
		kept = append(kept, m)
	}
	r.Members = kept
	if removed {
		s.dirty = true
	}
	s.mu.Unlock()
	// Evict any active connections of `member` in this room. Walk Hub state
	// AFTER releasing the room lock to avoid lock ordering surprises.
	if removed {
		s.evictUserFromRoom(roomID, member)
	}
	return nil
}

// evictUserFromRoom kicks every active peer in `roomID` whose User == user.
// Used by RemoveMember.
func (s *Service) evictUserFromRoom(roomID, user string) {
	for _, id := range s.Hub.PeersInRoom(roomID) {
		if u, ok := s.Hub.PeerUser(id); ok && u == user {
			s.Hub.Leave(id)
		}
	}
}

// Room returns a copy of the room record (so callers can't mutate state
// without going through AddMember/DeleteRoom).
func (s *Service) Room(roomID string) (Room, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return Room{}, false
	}
	return *r, true
}

// RoomForUser is the tenant-safe variant of Room. It returns the room only if
// user is owner or member; otherwise (Room{}, false), identical to "not found".
// GET-by-ID handlers MUST use this — Room() leaks existence to strangers.
func (s *Service) RoomForUser(user, roomID string) (Room, bool) {
	r, ok := s.Room(roomID)
	if !ok {
		return Room{}, false
	}
	if r.Owner == user || containsString(r.Members, user) {
		return r, true
	}
	return Room{}, false
}

// IsRoomOwner reports whether `user` owns the room `roomID`. Used by the
// signaling layer to authorize moderation actions (mute/kick). Guests arriving
// via PIN or invite are NEVER owner; even if they were later invited as a
// regular member through the UI, ownership is checked at the moment of the action.
func (s *Service) IsRoomOwner(roomID, user string) bool {
	if user == "" {
		return false
	}
	r, ok := s.Room(roomID)
	if !ok {
		return false
	}
	return r.Owner == user
}

// CanJoin reports whether `user` is allowed in `roomID` based on the durable
// ACL (owner or member). Magic-link invite tokens are handled separately by
// the HTTP /api/videocall/join handler — they short-circuit this check.
func (s *Service) CanJoin(user, roomID string) bool {
	r, ok := s.Room(roomID)
	if !ok {
		return false
	}
	if r.Owner == user {
		return true
	}
	for _, m := range r.Members {
		if m == user {
			return true
		}
	}
	return false
}

// ListForUser returns rooms the user owns OR is a member of, newest first.
// Secondary sort on ID keeps the order stable when CreatedAt collides (two
// rooms created in the same second).
func (s *Service) ListForUser(user string) []Room {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Room, 0)
	for _, r := range s.rooms {
		if r.Owner == user || containsString(r.Members, user) {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	return out
}
