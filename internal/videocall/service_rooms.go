package videocall

import (
	"errors"
	"sort"
	"strings"
	"time"
)

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

func (s *Service) RenameRoom(owner, roomID, newName string) error {
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
	if removed {
		s.evictUserFromRoom(roomID, member)
	}
	return nil
}

func (s *Service) evictUserFromRoom(roomID, user string) {
	for _, id := range s.Hub.PeersInRoom(roomID) {
		if u, ok := s.Hub.PeerUser(id); ok && u == user {
			s.Hub.Leave(id)
		}
	}
}

func (s *Service) Room(roomID string) (Room, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rooms[roomID]
	if !ok {
		return Room{}, false
	}
	return *r, true
}

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
