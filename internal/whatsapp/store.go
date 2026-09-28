package whatsapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Store struct {
	Root      string
	MediaRoot string

	mu    sync.Mutex
	state State
	chats map[string]*Chat

	appendLocks sync.Map
}

func NewStore(root, mediaRoot string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir store: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "messages"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{Root: root, MediaRoot: mediaRoot, chats: make(map[string]*Chat)}
	if err := s.loadState(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := s.loadChats(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (s *Store) stateFile() string { return filepath.Join(s.Root, "state.json") }
func (s *Store) chatsFile() string { return filepath.Join(s.Root, "chats.json") }

func (s *Store) loadState() error {
	data, err := os.ReadFile(s.stateFile())
	if err != nil {
		return err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
	return nil
}

func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Store) SetState(mutate func(*State)) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.state
	mutate(&cur)
	s.state = cur
	data, err := json.MarshalIndent(cur, "", "  ")
	if err != nil {
		return cur, err
	}
	if err := writeAtomic(s.stateFile(), append(data, '\n'), 0o600); err != nil {
		return cur, err
	}
	return cur, nil
}

func (s *Store) loadChats() error {
	data, err := os.ReadFile(s.chatsFile())
	if err != nil {
		return err
	}
	var list []*Chat
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("parse chats: %w", err)
	}
	s.mu.Lock()
	mergedAny := false
	for _, c := range list {
		if c == nil || c.JID == "" {
			continue
		}
		canon := normalizeJID(c.JID)
		if canon != c.JID {
			mergedAny = true
		}
		c.JID = canon
		if !c.IsGroup && strings.HasSuffix(canon, "@g.us") {
			c.IsGroup = true
			mergedAny = true
		}
		if existing, ok := s.chats[canon]; ok {
			if c.LastMsgTS > existing.LastMsgTS {
				if existing.Name != "" && c.Name == "" {
					c.Name = existing.Name
				}
				if existing.AvatarURL != "" && c.AvatarURL == "" {
					c.AvatarURL = existing.AvatarURL
				}
				c.UnreadCount = existing.UnreadCount + c.UnreadCount
				s.chats[canon] = c
			} else {
				existing.UnreadCount += c.UnreadCount
			}
			continue
		}
		s.chats[canon] = c
	}
	s.mu.Unlock()
	if mergedAny {
		s.mu.Lock()
		_ = s.saveChatsLocked()
		s.mu.Unlock()
	}
	return nil
}

func (s *Store) saveChatsLocked() error {
	list := make([]*Chat, 0, len(s.chats))
	for _, c := range s.chats {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Pinned != list[j].Pinned {
			return list[i].Pinned
		}
		return list[i].LastMsgTS > list[j].LastMsgTS
	})
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.chatsFile(), append(data, '\n'), 0o600)
}

func (s *Store) GetChat(jid string) *Chat {
	jid = normalizeJID(jid)
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[jid]
	if !ok || c == nil {
		return nil
	}
	cp := *c
	return &cp
}

func (s *Store) ListChats() []*Chat {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*Chat, 0, len(s.chats))
	for jid, c := range s.chats {
		if c == nil || c.JID == "" || jid == "" {
			continue
		}
		if c.JID == "status@broadcast" {
			continue
		}
		if strings.HasSuffix(c.JID, "@lid") && c.Name == "" && c.LastMsgTS == 0 {
			continue
		}
		cp := *c
		list = append(list, &cp)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Pinned != list[j].Pinned {
			return list[i].Pinned
		}
		return list[i].LastMsgTS > list[j].LastMsgTS
	})
	return list
}

func (s *Store) UpsertChat(c Chat) error {
	c.JID = normalizeJID(c.JID)
	if c.JID == "" {
		return errors.New("empty JID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.UpdatedAt = time.Now().Unix()
	s.chats[c.JID] = &c
	return s.saveChatsLocked()
}

func (s *Store) MergePresence(jid, presence string, lastSeen int64) error {
	jid = normalizeJID(jid)
	if jid == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[jid]
	if !ok {
		c = &Chat{JID: jid, IsGroup: strings.HasSuffix(jid, "@g.us")}
		s.chats[jid] = c
	}
	c.Presence = presence
	c.PresenceTS = time.Now().Unix()
	if lastSeen > 0 {
		c.LastSeenTS = lastSeen
	}
	c.UpdatedAt = time.Now().Unix()
	return s.saveChatsLocked()
}

func (s *Store) WipeImport() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := time.Now().Unix()
	bak := filepath.Join(s.Root, "wipe-backup-"+fmt.Sprintf("%d", ts))
	if err := os.MkdirAll(bak, 0o700); err != nil {
		return fmt.Errorf("mkdir backup: %w", err)
	}
	if _, err := os.Stat(s.chatsFile()); err == nil {
		_ = os.Rename(s.chatsFile(), filepath.Join(bak, "chats.json"))
	}
	msgDir := filepath.Join(s.Root, "messages")
	if _, err := os.Stat(msgDir); err == nil {
		_ = os.Rename(msgDir, filepath.Join(bak, "messages"))
	}
	if err := os.MkdirAll(msgDir, 0o700); err != nil {
		return err
	}
	s.chats = make(map[string]*Chat)
	return nil
}

func (s *Store) MergeJIDs(src, dst string) error {
	if src == dst || src == "" || dst == "" {
		return nil
	}
	s.mu.Lock()
	srcC, srcOK := s.chats[src]
	dstC, dstOK := s.chats[dst]
	if !srcOK {
		s.mu.Unlock()
		return nil
	}
	if !dstOK {
		srcC.JID = dst
		s.chats[dst] = srcC
		delete(s.chats, src)
		err := s.saveChatsLocked()
		s.mu.Unlock()
		return err
	}
	dstC.UnreadCount += srcC.UnreadCount
	if srcC.LastMsgTS > dstC.LastMsgTS {
		dstC.LastMsgID = srcC.LastMsgID
		dstC.LastMsgTS = srcC.LastMsgTS
		dstC.LastMsgBody = srcC.LastMsgBody
	}
	if dstC.Name == "" && srcC.Name != "" {
		dstC.Name = srcC.Name
	}
	if dstC.AvatarURL == "" && srcC.AvatarURL != "" {
		dstC.AvatarURL = srcC.AvatarURL
	}
	if srcC.Archived {
		dstC.Archived = true
	}
	if srcC.Pinned {
		dstC.Pinned = true
	}
	if srcC.Muted {
		dstC.Muted = true
	}
	dstC.UpdatedAt = time.Now().Unix()
	delete(s.chats, src)
	if err := s.saveChatsLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	s.MergeMessageDirs(src, dst)
	return nil
}

func (s *Store) MergeChatFlags(jid string, archived, pinned, muted bool) error {
	jid = normalizeJID(jid)
	if jid == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[jid]
	if !ok {
		c = &Chat{JID: jid, IsGroup: strings.HasSuffix(jid, "@g.us")}
		s.chats[jid] = c
	}
	if c.Archived == archived && c.Pinned == pinned && c.Muted == muted {
		return nil
	}
	c.Archived = archived
	c.Pinned = pinned
	c.Muted = muted
	c.UpdatedAt = time.Now().Unix()
	return s.saveChatsLocked()
}

func (s *Store) MergeChatAvatar(jid, url string) error {
	jid = normalizeJID(jid)
	if jid == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[jid]
	if !ok {
		c = &Chat{JID: jid, IsGroup: strings.HasSuffix(jid, "@g.us")}
		s.chats[jid] = c
	}
	if c.AvatarURL == url {
		return nil
	}
	c.AvatarURL = url
	c.UpdatedAt = time.Now().Unix()
	return s.saveChatsLocked()
}

func (s *Store) MergeChatName(jid, name string, isGroup bool) error {
	jid = normalizeJID(jid)
	if jid == "" || name == "" {
		return nil
	}
	isGroup = isGroup || strings.HasSuffix(jid, "@g.us")
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[jid]
	if !ok {
		c = &Chat{JID: jid, IsGroup: isGroup}
		s.chats[jid] = c
	}
	changed := false
	if isGroup && !c.IsGroup {
		c.IsGroup = true
		changed = true
	}
	if c.Name != name {
		c.Name = name
		c.UpdatedAt = time.Now().Unix()
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveChatsLocked()
}

func (s *Store) TouchChatWithMessage(m Message) error {
	m.ChatJID = normalizeJID(m.ChatJID)
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[m.ChatJID]
	if !ok {
		c = &Chat{JID: m.ChatJID, IsGroup: strings.HasSuffix(m.ChatJID, "@g.us")}
		s.chats[m.ChatJID] = c
	}
	c.LastMsgID = m.ID
	c.LastMsgTS = m.TS
	c.LastMsgBody = truncatePreview(messagePreview(m), 100)
	if !m.FromMe {
		c.UnreadCount++
	}
	c.UpdatedAt = time.Now().Unix()
	return s.saveChatsLocked()
}

func (s *Store) lookupName(jid string) (string, bool) {
	jid = normalizeJID(jid)
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.chats[jid]; ok && c.Name != "" {
		return c.Name, true
	}
	return "", false
}

func (s *Store) MarkChatRead(jid string) error {
	jid = normalizeJID(jid)
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[jid]
	if !ok {
		return nil
	}
	c.UnreadCount = 0
	c.UpdatedAt = time.Now().Unix()
	return s.saveChatsLocked()
}
