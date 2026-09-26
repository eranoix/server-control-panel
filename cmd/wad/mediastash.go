package main

// mediastash.go — persisting media keys across daemon restarts.
//
// The download keys of every media message (the CDN's directPath + mediaKey,
// which whatsmeow uses to decrypt) live only in the in-memory stash
// (session.msgs), which is WIPED on every daemon restart — and every deploy
// restarts the daemon. Since media download is MANUAL (the user clicks
// "Baixar" possibly hours/days and several deploys after receiving it), an
// ephemeral stash makes that click 404 forever as soon as the daemon restarts.
// To make the manual download durable, we persist the message proto (which
// carries the keys) to disk per media message, and reload it on demand when the
// in-memory stash misses. The keys are tiny; only messages WITH media are
// persisted, and the directory is pruned to the N newest files.

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// mediaStashMax caps how many protos stay on disk; the prune keeps the newest
// by mtime. Media on WhatsApp's CDN expires within weeks, so old keys become
// dead weight anyway.
const mediaStashMax = 8000

// pruneEvery fires an (asynchronous) prune every N persists, so a long-lived
// daemon does not blow past the cap while waiting for the boot-time prune.
const pruneEvery = 1000

func (s *session) mediaStashDir() string {
	return filepath.Join(filepath.Dir(s.dbPath), "mediastash")
}

// stashFileID sanitises a WhatsApp message ID into a safe file name. WhatsApp
// IDs are usually [0-9A-F], but we defend against any character.
func stashFileID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 128 {
		out = out[:128]
	}
	return out
}

type persistedStash struct {
	Sender string `json:"sender"`
	FromMe bool   `json:"fromMe"`
	Msg    string `json:"msg"` // base64(proto.Marshal(*waE2E.Message))
}

// hasDownloadableMedia reports whether m carries media whatsmeow can download
// (mirrors downloadMedia's type switch, over the message already unwrapped from
// view-once).
func hasDownloadableMedia(m *waE2E.Message) bool {
	if m == nil {
		return false
	}
	u := unwrapMsg(m)
	return u.GetImageMessage() != nil || u.GetVideoMessage() != nil ||
		u.GetAudioMessage() != nil || u.GetDocumentMessage() != nil ||
		u.GetStickerMessage() != nil
}

// mediaMimeOf returns the mimetype of m's downloadable media (or "" if there is
// none). Used in history sync so the server picks the right extension/Content-Type.
func mediaMimeOf(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	u := unwrapMsg(m)
	switch {
	case u.GetImageMessage() != nil:
		return u.GetImageMessage().GetMimetype()
	case u.GetVideoMessage() != nil:
		return u.GetVideoMessage().GetMimetype()
	case u.GetAudioMessage() != nil:
		return u.GetAudioMessage().GetMimetype()
	case u.GetDocumentMessage() != nil:
		return u.GetDocumentMessage().GetMimetype()
	case u.GetStickerMessage() != nil:
		return u.GetStickerMessage().GetMimetype()
	}
	return ""
}

// persistStashedMedia writes a media message's download keys to disk so a later
// "Baixar" works even after a daemon restart. Best-effort: any error is
// swallowed (the in-memory stash still serves this session).
func (s *session) persistStashedMedia(id, sender string, fromMe bool, m *waE2E.Message) {
	raw, err := proto.Marshal(m)
	if err != nil {
		return
	}
	rec := persistedStash{Sender: sender, FromMe: fromMe, Msg: base64.StdEncoding.EncodeToString(raw)}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	dir := s.mediaStashDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	full := filepath.Join(dir, stashFileID(id)+".json")
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, full) // atomic swap
}

// loadPersistedStash reads a persisted media message back from disk. Returns
// nil on any miss/error.
func (s *session) loadPersistedStash(id string) *stashedMsg {
	full := filepath.Join(s.mediaStashDir(), stashFileID(id)+".json")
	data, err := os.ReadFile(full)
	if err != nil {
		return nil
	}
	var rec persistedStash
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(rec.Msg)
	if err != nil {
		return nil
	}
	var m waE2E.Message
	if err := proto.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return &stashedMsg{msg: &m, sender: rec.Sender, fromMe: rec.FromMe}
}

// pruneMediaStash keeps only the mediaStashMax newest files (by mtime),
// deleting the rest. Bounds growth on disk. Best-effort.
func (s *session) pruneMediaStash() {
	dir := s.mediaStashDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) <= mediaStashMax {
		return
	}
	type fe struct {
		name string
		mod  int64
	}
	files := make([]fe, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fe{name: e.Name(), mod: info.ModTime().UnixNano()})
	}
	if len(files) <= mediaStashMax {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod > files[j].mod }) // newest first
	for _, f := range files[mediaStashMax:] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
}
