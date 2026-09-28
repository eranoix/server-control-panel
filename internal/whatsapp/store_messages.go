package whatsapp

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

func (s *Store) messageFile(jid string, ts int64) string {
	dt := time.Unix(ts, 0).UTC()
	return filepath.Join(s.Root, "messages", chatDir(jid), dt.Format("2006-01")+".jsonl")
}

func (s *Store) HasMessage(chatJID, id string, ts int64) (bool, error) {
	chatJID = normalizeJID(chatJID)
	if id == "" {
		return false, errors.New("HasMessage: empty id")
	}
	if ts == 0 {
		ts = time.Now().Unix()
	}
	candidates := []int64{ts}
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

func (s *Store) AppendMessage(m Message) error {
	m.ChatJID = normalizeJID(m.ChatJID)
	m.FromJID = normalizeJID(m.FromJID)
	if m.ID == "" || m.TS == 0 {
		return errors.New("message missing id/ts")
	}
	lockI, _ := s.appendLocks.LoadOrStore(m.ChatJID, &sync.Mutex{})
	lock := lockI.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	path := s.messageFile(m.ChatJID, m.TS)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if data, rerr := os.ReadFile(path); rerr == nil {
		idMarker := []byte(`"id":"` + m.ID + `"`)
		if bytes.Contains(data, idMarker) {
			return nil
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
		_ = f.Truncate(startSize)
		return errors.New("partial write — truncated to last consistent state")
	}
	return f.Sync()
}

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
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	out := make([]Message, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, name := range files {
		msgs, err := readJSONLReverse(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, m := range msgs {
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
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (s *Store) UpdateAck(chatJID, msgID string, ack int) error {
	return s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if m.Ack == ack {
			return false
		}
		m.Ack = ack
		return true
	})
}

func (s *Store) UpdateBody(chatJID, msgID, body string) error {
	return s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if m.Body == body {
			return false
		}
		m.Body = body
		return true
	})
}

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
		if strings.HasPrefix(full, filepath.Clean(s.MediaRoot)+string(os.PathSeparator)) {
			_ = os.Remove(full)
		}
	}
	return err
}

func (s *Store) AddReaction(chatJID, msgID, fromJID, emoji string, ts int64) error {
	return s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if fromJID == "" {
			return false
		}
		if ts == 0 {
			ts = time.Now().Unix()
		}
		for i, r := range m.Reactions {
			if r.From == fromJID {
				if emoji == "" {
					m.Reactions = append(m.Reactions[:i], m.Reactions[i+1:]...)
					return true
				}
				if r.Emoji == emoji {
					return false
				}
				m.Reactions[i].Emoji = emoji
				m.Reactions[i].TS = ts
				return true
			}
		}
		if emoji == "" {
			return false
		}
		m.Reactions = append(m.Reactions, Reaction{From: fromJID, Emoji: emoji, TS: ts})
		return true
	})
}

func (s *Store) ReclassifyMessage(chatJID, msgID, newType string) error {
	if newType == "" {
		return nil
	}
	return s.mutateMessage(chatJID, msgID, func(m *Message) bool {
		if m.Type == newType {
			return false
		}
		if m.Type != "text" && m.Type != "document" && m.Type != "media" {
			return false
		}
		m.Type = newType
		return true
	})
}

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
				return true, nil
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
