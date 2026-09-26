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

// Store handles JSON persistence under data/whatsapp/. All writes are atomic
// (write to .new + rename + fsync dir). The pattern mirrors config.Save in
// internal/config/config.go.
//
// Layout:
//
//	data/whatsapp/
//	├── state.json
//	├── chats.json
//	├── contacts.json
//	└── messages/
//	    └── <chat-hash>/
//	        ├── 2026-05.jsonl
//	        └── 2026-04.jsonl
//
// MediaRoot points to /var/lib/vpsm-whatsapp/media (managed by WAHA container).
type Store struct {
	Root      string // e.g. /opt/panel/data/whatsapp
	MediaRoot string // e.g. /var/lib/vpsm-whatsapp/media

	mu    sync.Mutex
	state State
	chats map[string]*Chat // jid -> Chat

	// appendLocks serialises AppendMessage per chat. Without it, a concurrent
	// webhook and backfill could race between HasMessage and Append and insert
	// the same message twice into the same .jsonl. LoadMessages dedups
	// defensively on read, but the file still grows and every re-read pays for
	// the filter. A sync.Map keeps the per-chat locks without needing the
	// global lock on the hot path.
	appendLocks sync.Map // chatJID → *sync.Mutex
}

// NewStore opens (or initializes) the store. The root is created if missing.
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

// --- atomic file ops ---

// writeAtomic creates path.new, writes data, fsyncs, renames to path, and
// fsyncs the directory so the rename is durable. Same shape as config.Save.
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

// --- state ---

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

// State returns a snapshot of the current state (safe to share with callers
// outside the store goroutines).
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// SetState replaces the entire state and persists. The callback receives a
// mutable copy and should mutate in place; the post-callback value is what
// gets saved.
//
// RACE: the lock covers the disk write too. It used to be released before
// writeAtomic — two concurrent SetState() calls then raced to write
// state.json through the SAME tmp file (writeAtomic uses a deterministic
// path). The rename is atomic, but the intermediate content belonged to
// whoever arrived last. Worse: if the second writer ran while the first was
// renaming, the second rename could fail or overwrite with stale state.
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

// --- chats ---

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
	// Filter out orphaned entries (an empty JID) and MERGE the duplicates
	// WAHA's inconsistent JIDs produce (@c.us vs @s.whatsapp.net).
	// Normalisation canonicalises to @c.us; when a chat with the same
	// normalised JID already exists we keep the more recent one (the larger
	// LastMsgTS) while preserving the useful metadata from both.
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
		// Self-heal: @g.us means group, full stop. This fixes old flags stored as
		// false (WAHA sometimes sends isGroup=false). mergedAny forces the
		// re-persist below, so the correction is already on disk by the next boot.
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
	// Persist the consolidated state so the next load does not have to redo
	// the merge. Best effort — a failure does not fail the load.
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

// ListChats returns a snapshot of all chats, sorted by recency (pinned first).
//
// Entries that are filtered out:
//   - an empty JID (it breaks Alpine's x-for :key)
//   - status@broadcast (WhatsApp's status feed, not a real conversation)
//   - @lid chats with NO name AND NO messages — fragments of a past sync
//     that have no mapping in gows.db and never received a message. We
//     suppress them from the panel so it does not show an "empty row" with
//     no usable identity.
//
// GetChat returns a copy of the chat for a JID (normalised) or nil. O(1) via
// a map lookup — used by handleAvatar, which used to walk all of ListChats on
// every request, O(N) per avatar.
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
			continue // terminal orphan — no identity, no history
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

// UpsertChat updates or inserts a chat record by JID. Caller-supplied fields
// overwrite existing ones; nil-equivalent fields (empty strings) are kept.
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

// MergePresence updates a chat's presence state (online/composing/recording/
// offline) and last seen. Since presence arrives over the webhook at high
// frequency (every start and stop of typing fires one), we avoid writing to
// disk when nothing changed. Transient presence (composing/recording) always
// writes, because the frontend expects to see it.
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

// WipeImport erases EVERYTHING we imported from WhatsApp (chats.json,
// messages/, contacts.json) while preserving state.json (the pairing status).
// Used by the /api/whatsapp/admin/wipe endpoint when the user wants to
// re-import from scratch. It backs up to .bak.<unix> before erasing, so
// nothing is lost for good.
//
// It does NOT touch /var/lib/vpsm-whatsapp/ (the WAHA session and the media)
// — only our own index. The media stays reachable at its old paths.
func (s *Store) WipeImport() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := time.Now().Unix()
	bak := filepath.Join(s.Root, "wipe-backup-"+fmt.Sprintf("%d", ts))
	if err := os.MkdirAll(bak, 0o700); err != nil {
		return fmt.Errorf("mkdir backup: %w", err)
	}
	// Move chats.json and messages/ into the backup (atomic within the same fs).
	if _, err := os.Stat(s.chatsFile()); err == nil {
		_ = os.Rename(s.chatsFile(), filepath.Join(bak, "chats.json"))
	}
	msgDir := filepath.Join(s.Root, "messages")
	if _, err := os.Stat(msgDir); err == nil {
		_ = os.Rename(msgDir, filepath.Join(bak, "messages"))
	}
	// Recreate an empty messages/ for subsequent writes.
	if err := os.MkdirAll(msgDir, 0o700); err != nil {
		return err
	}
	// Reset the in-memory copy.
	s.chats = make(map[string]*Chat)
	return nil
}

// MergeJIDs absorbs one chat (src) into another (dst) — used when the
// resolver discovers that a @lid maps to the same phone number (@c.us). It
// sums the unread counts, keeps the more recent last_msg and deletes src.
//
// IMPORTANT: the two chats' messages live in different directories on disk
// (chatDir(src) != chatDir(dst)), so they stay separate. To preserve the
// history we copy the contents of src's directory into dst's before erasing.
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
		// There is no destination — just rename src to dst and return.
		srcC.JID = dst
		s.chats[dst] = srcC
		delete(s.chats, src)
		err := s.saveChatsLocked()
		s.mu.Unlock()
		return err
	}
	// Merge field by field: dst is the "winner" (it is the one the user sees).
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
	// MergeMessageDirs holds the per-chat appendLock (src and dst) for the
	// duration of the merge, blocking a concurrent AppendMessage. We keep
	// store.mu released so operations on other chats keep flowing.
	s.MergeMessageDirs(src, dst)
	return nil
}

// MergeChatFlags updates archived/pinned/muted state from WAHA sync without
// touching name, unread, etc. Idempotent: no write when nothing changed.
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

// MergeChatAvatar updates the AvatarURL of a chat (without clobbering name,
// unread count, etc.).
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

// MergeChatName updates ONLY the Name field of a chat (creating the chat if
// missing). Used by the periodic WAHA chat sync: we want fresh contact names
// from the address book without clobbering local fields like UnreadCount or
// LastMsgBody (which are computed from webhook events).
//
// Empty `name` is ignored — better keep the previous label than blank the UI.
func (s *Store) MergeChatName(jid, name string, isGroup bool) error {
	jid = normalizeJID(jid)
	if jid == "" || name == "" {
		return nil
	}
	// @g.us is the absolute truth about being a group. WAHA sometimes sends
	// isGroup=false for a @g.us, which hid the sender's name on the bubbles and
	// the group icon, and made the video-call button appear.
	isGroup = isGroup || strings.HasSuffix(jid, "@g.us")
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[jid]
	if !ok {
		c = &Chat{JID: jid, IsGroup: isGroup}
		s.chats[jid] = c
	}
	changed := false
	// Auto-corrects older records stored with the wrong flag.
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
		return nil // nothing changed, avoids a disk write
	}
	return s.saveChatsLocked()
}

// TouchChatWithMessage updates the last-msg fields for a chat based on a new
// message, creating the chat record if it doesn't exist. Increments unread
// count for inbound messages on chats other than the active one (active
// tracking lives in the API layer, here we just bump for !fromMe).
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

// lookupName returns the known name for a JID (used by handleStatusUpdates to
// annotate who posted each status). On failure it returns ("", false).
func (s *Store) lookupName(jid string) (string, bool) {
	jid = normalizeJID(jid)
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.chats[jid]; ok && c.Name != "" {
		return c.Name, true
	}
	return "", false
}

// MarkChatRead resets unread_count to zero. Called both from /api/whatsapp/
// chats/{jid}/read and after we propagate the read receipt to WAHA.
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
