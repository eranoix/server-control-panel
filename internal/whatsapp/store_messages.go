package whatsapp

// store_messages.go — message persistence (JSONL partitioned by month)
//
// Covers append/load/find/update of Message plus the reverse-read helpers
// (readJSONLReverse), reclassification, media/ack/delete updates, reactions
// and mutateMessage/rewriteIfFound (the atomic JSONL rewrite).
//
// On-disk layout:
//   data/whatsapp/messages/<chat-hash>/YYYY-MM.jsonl
//
// Each line is one JSON Message (the struct lives in types.go). Partitioning
// by month keeps the files small for rewriteIfFound and on-demand backfill.
//
// Extracted from store.go (it keeps the same *Store receiver).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// --- messages (JSONL by month) ---

func (s *Store) messageFile(jid string, ts int64) string {
	dt := time.Unix(ts, 0).UTC()
	return filepath.Join(s.Root, "messages", chatDir(jid), dt.Format("2006-01")+".jsonl")
}

// HasMessage reports whether a message with id is already stored in chatJID's
// log. Used by the webhook handler as a shield against redelivery (WAHA
// resends the event when the handler answers anything but 2xx, or after a
// timeout). Without it, a redelivery duplicates a line in the JSONL —
// LoadMessages only dedups on read.
//
// Implementation: it scans only the month of `ts` (plus the previous month
// when ts falls near the 1st) rather than the whole chat. The cost is
// proportional to the daily volume, not the history. ts=0 scans the current
// month.
func (s *Store) HasMessage(chatJID, id string, ts int64) (bool, error) {
	chatJID = normalizeJID(chatJID)
	if id == "" {
		return false, errors.New("HasMessage: empty id")
	}
	if ts == 0 {
		ts = time.Now().Unix()
	}
	candidates := []int64{ts}
	// A WAHA message delivered just after midnight can arrive with the previous
	// day's ts — inside that window, scan the previous month too.
	if t := time.Unix(ts, 0).UTC(); t.Day() <= 2 {
		candidates = append(candidates, ts-7*86400)
	}
	for _, c := range candidates {
		path := s.messageFile(chatJID, c)
		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return false, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			// A quick substring match before parsing JSON — WAHA ids carry unique
			// prefixes (true_<chat>_<hash>), so a substring collision is practically
			// impossible, but we still parse to confirm.
			line := scanner.Bytes()
			if !bytes.Contains(line, []byte(`"id":"`+id+`"`)) {
				continue
			}
			var m Message
			if err := json.Unmarshal(line, &m); err != nil {
				continue
			}
			if m.ID == id {
				f.Close()
				return true, nil
			}
		}
		f.Close()
		if err := scanner.Err(); err != nil {
			return false, err
		}
	}
	return false, nil
}

// AppendMessage writes a message to the appropriate month-file. Uses O_APPEND
// + fsync. Each chat has its own (lazily created) mutex to serialise
// concurrency — webhook and backfill on the same chat without racing. Inside
// the lock it re-checks whether the ID already exists in the file (a linear
// scan; cheap for monthly files) — without that, two callers could slip
// through the HasMessage->Append window and duplicate lines.
func (s *Store) AppendMessage(m Message) error {
	m.ChatJID = normalizeJID(m.ChatJID)
	m.FromJID = normalizeJID(m.FromJID)
	if m.ID == "" || m.TS == 0 {
		return errors.New("message missing id/ts")
	}
	// Per-chat lock (lazy LoadOrStore — no mutex allocated when one exists).
	lockI, _ := s.appendLocks.LoadOrStore(m.ChatJID, &sync.Mutex{})
	lock := lockI.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	path := s.messageFile(m.ChatJID, m.TS)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Intra-file dedup: when the file exists, scan its IDs before appending.
	// O(n) per call — acceptable for monthly files (a few MB at most).
	if data, rerr := os.ReadFile(path); rerr == nil {
		idMarker := []byte(`"id":"` + m.ID + `"`)
		if bytes.Contains(data, idMarker) {
			return nil // already there, no-op
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	// Snapshot the size before the write. On large messages (a long caption
	// plus ADF) a write can be partial, and a crash mid-write would leave a
	// truncated line in the file — the next read through readJSONLReverse
	// would see invalid JSON. The defence: if Write reports n < len(line),
	// truncate back to the original size.
	startSize := int64(-1)
	if fi, err := f.Stat(); err == nil {
		startSize = fi.Size()
	}
	n, werr := f.Write(line)
	if werr != nil {
		if startSize >= 0 {
			_ = f.Truncate(startSize)
		}
		return werr
	}
	if n != len(line) && startSize >= 0 {
		// Partial write — truncate back and return the error so the caller retries.
		_ = f.Truncate(startSize)
		return errors.New("partial write — truncated to last consistent state")
	}
	return f.Sync()
}

// LoadMessages reads up to `limit` messages for a chat in reverse chronological
// order (newest first), starting from `before` (TS in seconds; 0 = newest).
// Walks JSONL files backwards by month until the limit is reached.
//
// Locks against AppendMessage on the same chat. Without that, a read
// concurrent with a write could pick up a truncated line (Append uses
// O_APPEND, but a crash between fsync and rename leaves a partial line
// visible).
func (s *Store) LoadMessages(chatJID string, before int64, limit int) ([]Message, error) {
	chatJID = normalizeJID(chatJID)
	if limit <= 0 {
		limit = 50
	}
	lockI, _ := s.appendLocks.LoadOrStore(chatJID, &sync.Mutex{})
	lock := lockI.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	dir := filepath.Join(s.Root, "messages", chatDir(chatJID))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Message{}, nil
		}
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".jsonl") {
			files = append(files, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files))) // newest months first

	out := make([]Message, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, name := range files {
		msgs, err := readJSONLReverse(filepath.Join(dir, name))
		if err != nil {
			continue // skip corrupt file rather than fail the whole load
		}
		for _, m := range msgs {
			// Legacy lines (written before AppendMessage validated) may carry an
			// empty ID; the dedup also defends against an append duplicated by a
			// crash between fsync and rename. Without it, Alpine's x-for :key="m.id"
			// crashes.
			if m.ID == "" {
				continue
			}
			if _, dup := seen[m.ID]; dup {
				continue
			}
			if before > 0 && m.TS >= before {
				continue
			}
			seen[m.ID] = struct{}{}
			out = append(out, m)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// readJSONLReverse reads an entire JSONL file and returns its records in
// reverse order. For files under ~10 MB this is fast enough; we don't bother
// with reverse-streaming since per-chat per-month volume is bounded.
func readJSONLReverse(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	var msgs []Message
	for {
		line, err := br.ReadString('\n')
		if len(strings.TrimSpace(line)) > 0 {
			var m Message
			if jerr := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); jerr == nil {
				msgs = append(msgs, m)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
	}
	// reverse in place
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// UpdateAck rewrites a message line in its month file to update its Ack value.
// O(file-size) — fine for personal-scale volumes; revisit if logs balloon.
func (s *Store) UpdateAck(chatJID, msgID string, ack int) error {
	return s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if m.Ack == ack {
			return false
		}
		m.Ack = ack
		return true
	})
}

// UpdateBody rewrites the Body (text/caption) of an already stored message. Used
// by message editing to reflect the new text locally without
// waiting for a re-sync. No-op if the body is already the same.
func (s *Store) UpdateBody(chatJID, msgID, body string) error {
	return s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if m.Body == body {
			return false
		}
		m.Body = body
		return true
	})
}

// MarkDeleted flips Message.Deleted=true for a revoked message. If the
// message had media downloaded into MediaRoot, the file is removed in the
// same gesture — there is no point keeping the bytes of a message the user
// asked to delete.
func (s *Store) MarkDeleted(chatJID, msgID string) error {
	var mediaToDelete string
	err := s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if m.Deleted {
			return false
		}
		m.Deleted = true
		if m.Media != nil && m.Media.Path != "" && safeMediaPath(m.Media.Path) {
			mediaToDelete = m.Media.Path
		}
		return true
	})
	if err == nil && mediaToDelete != "" {
		full := filepath.Join(s.MediaRoot, mediaToDelete)
		// Anchor check: make sure it really is inside MediaRoot (defence in depth
		// against path traversal, even though it was already validated).
		if strings.HasPrefix(full, filepath.Clean(s.MediaRoot)+string(os.PathSeparator)) {
			_ = os.Remove(full)
		}
	}
	return err
}

// AddReaction inserts, updates or removes a reaction on an already-stored
// message. WhatsApp's rule: one reaction per (from, msg). Reacting again
// overwrites; emoji="" removes. ts is refreshed to the latest operation.
// Idempotent — when nothing actually changes (same emoji, same from) it does
// not rewrite disk.
func (s *Store) AddReaction(chatJID, msgID, fromJID, emoji string, ts int64) error {
	return s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if fromJID == "" {
			return false
		}
		if ts == 0 {
			ts = time.Now().Unix()
		}
		// Look for an existing entry from the same `from`.
		for i, r := range m.Reactions {
			if r.From == fromJID {
				if emoji == "" {
					// Remove it.
					m.Reactions = append(m.Reactions[:i], m.Reactions[i+1:]...)
					return true
				}
				if r.Emoji == emoji {
					return false // no-op
				}
				m.Reactions[i].Emoji = emoji
				m.Reactions[i].TS = ts
				return true
			}
		}
		if emoji == "" {
			return false // nothing to remove
		}
		m.Reactions = append(m.Reactions, Reaction{From: fromJID, Emoji: emoji, TS: ts})
		return true
	})
}

// ReclassifyMessage updates the Type of an already-stored message when the
// incoming one differs from what the store holds. Used by the backfill to fix
// messages filed as "text"/"document" because the old webhook or sync failed
// to detect the right MediaType (the GOWS engine returns an empty type in the
// top-level payload). A no-op when newType matches the stored one or is empty.
func (s *Store) ReclassifyMessage(chatJID, msgID, newType string) error {
	if newType == "" {
		return nil
	}
	return s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if m.Type == newType {
			return false
		}
		// Never downgrade: if the store already says "image", do not swap it for
		// "text" just because a pull arrived without a mediaType. text->image,
		// yes. image->text, no.
		if m.Type != "text" && m.Type != "document" && m.Type != "media" {
			return false
		}
		m.Type = newType
		return true
	})
}

// UpdateMessageMedia fills in or refreshes Media.Path (and mime/filename/size)
// on a message that already exists in the store. Used by the on-demand
// download endpoint after copying the bytes from WAHA into MediaRoot.
func (s *Store) UpdateMessageMedia(chatJID, msgID string, mediaPath, mimeType, filename string, size int64) error {
	return s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if m.Media == nil {
			m.Media = &Media{}
		}
		changed := false
		if m.Media.Path != mediaPath {
			m.Media.Path = mediaPath
			changed = true
		}
		if mimeType != "" && m.Media.MimeType != mimeType {
			m.Media.MimeType = mimeType
			changed = true
		}
		if filename != "" && m.Media.Filename != filename {
			m.Media.Filename = filename
			changed = true
		}
		if size > 0 && m.Media.Size != size {
			m.Media.Size = size
			changed = true
		}
		return changed
	})
}

// FindMessage locates a message by ID within a chat, reading the files month
// by month. A linear lookup (the same strategy as mutateMessage) — cheap at
// personal-scale volumes; revisit if it becomes a bottleneck.
func (s *Store) FindMessage(chatJID, msgID string) (*Message, error) {
	chatJID = normalizeJID(chatJID)
	dir := filepath.Join(s.Root, "messages", chatDir(chatJID))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var m Message
			if jerr := json.Unmarshal([]byte(line), &m); jerr != nil {
				continue
			}
			if m.ID == msgID {
				return &m, nil
			}
		}
	}
	return nil, nil
}

// mutateMessage walks the chat's month files looking for `msgID` and applies
// the mutator. The mutator returns true when it changed the record (so we
// know to rewrite the file). Stops after the first match — message IDs are
// globally unique per WhatsApp's protocol.
func (s *Store) mutateMessage(chatJID, msgID string, mut func(*Message) bool) error {
	chatJID = normalizeJID(chatJID)
	dir := filepath.Join(s.Root, "messages", chatDir(chatJID))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		updated, err := rewriteIfFound(path, msgID, mut)
		if err != nil {
			continue
		}
		if updated {
			return nil
		}
	}
	return nil
}

// rewriteIfFound reads `path`, applies `mut` to the line with `msgID`, and
// rewrites atomically if a change was made. Returns whether any line matched.
func rewriteIfFound(path, msgID string, mut func(*Message) bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	lines := strings.Split(string(data), "\n")
	changed := false
	for i, line := range lines {
		if line == "" {
			continue
		}
		var m Message
		if jerr := json.Unmarshal([]byte(line), &m); jerr != nil {
			continue
		}
		if m.ID == msgID {
			if !mut(&m) {
				return true, nil // matched but no change needed
			}
			updated, err := json.Marshal(m)
			if err != nil {
				return false, err
			}
			lines[i] = string(updated)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return true, writeAtomic(path, []byte(strings.Join(lines, "\n")), 0o600)
}

// messagePreview returns a short, human-readable summary of a message for the
// chat list. Falls back to media type indicators ("📷 Image") when no body.
func messagePreview(m Message) string {
	if m.Body != "" {
		return m.Body
	}
	switch m.Type {
	case "image":
		return "📷 Image"
	case "video":
		return "🎬 Video"
	case "audio":
		return "🎵 Audio"
	case "voice":
		return "🎤 Voice message"
	case "document":
		if m.Media != nil && m.Media.Filename != "" {
			return "📎 " + m.Media.Filename
		}
		return "📎 Document"
	case "sticker":
		return "🏷️ Sticker"
	case "location":
		return "📍 Location"
	case "contact":
		return "👤 Contact"
	}
	return ""
}
