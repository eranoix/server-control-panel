package whatsapp

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// stdEncode wraps base64.StdEncoding so client.go doesn't need to import the
// package directly (keeps imports tight).
func stdEncode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// chatDir returns the on-disk subdirectory used for a chat's message logs.
// We hash the JID (rather than embedding it raw) because group JIDs include
// participant phones separated by hyphens — long, ugly, and OS-unfriendly.
// SHA-256 truncated to 16 hex chars is plenty for personal-scale uniqueness.
func chatDir(jid string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(jid)))
	return hex.EncodeToString(sum[:8])
}

// truncatePreview clamps a string to N runes for use as last-msg preview.
func truncatePreview(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// normalizeJID converts WhatsApp JIDs to the canonical form the UI uses.
// WAHA exposes the same chat under two different suffixes:
//   - /chats/overview            uses "@c.us"
//   - message webhooks           use "@s.whatsapp.net"
//   - GOWS internally            uses "@s.whatsapp.net"
//
// Without normalisation you send to "5522X@c.us" and the reply creates a new
// "5522X@s.whatsapp.net" chat — two chats for the same contact. We adopt
// "@c.us" as the canonical form (the one /chats/overview returns, and the one
// the end user sees in the sidebar).
//
// Groups (@g.us), broadcasts (@broadcast) and linked-ids (@lid) are left intact.
func normalizeJID(jid string) string {
	if jid == "" {
		return jid
	}
	if strings.HasSuffix(jid, "@s.whatsapp.net") {
		user := strings.TrimSuffix(jid, "@s.whatsapp.net")
		// Strip the ":NN" (device-id) suffix — GOWS sometimes includes it in
		// Sender: "5511555010101:13@s.whatsapp.net". The chat belongs to the
		// PERSON, not the device.
		if i := strings.IndexByte(user, ':'); i > 0 {
			user = user[:i]
		}
		return user + "@c.us"
	}
	return jid
}

// isGroupJID reports whether jid belongs to a group (@g.us). In groups WAHA
// puts the group itself in `from` (proven: messages received from a group are
// filed correctly under `from`). The `to` of a fromMe group message comes
// back as the account's OWN number — using it dumps the message into the chat
// with ourselves.
func isGroupJID(jid string) bool { return strings.HasSuffix(jid, "@g.us") }

// resolveChatJID picks the conversation's JID from the from/to fields of a
// WAHA event (message/ack/revoke), group-aware.
//
//   - If either side is @g.us, the group WINS — it is the conversation. (This
//     fixes the bug where messages sent to a group ended up in the chat with
//     ourselves, because the `to` of a fromMe message was our own number.)
//   - outbound / preferTo (fromMe): use `to` (the peer).
//   - inbound: use `from` (the peer).
//   - an empty `to` falls back to `from`.
func resolveChatJID(from, to string, preferTo bool) string {
	if isGroupJID(from) {
		return from
	}
	if isGroupJID(to) {
		return to
	}
	if preferTo && to != "" {
		return to
	}
	if from != "" {
		return from
	}
	return to
}

// MergeMessageDirs is the public wrapper that holds the per-chat appendLock
// (src + dst) for the duration of the merge. Without it, a concurrent
// AppendMessage on src would open a handle on files being removed, or one on
// dst would write into the same JSONL the merge was rewriting. Use this path
// rather than mergeMessageDirs directly from callers outside MergeJIDs.
func (s *Store) MergeMessageDirs(srcJID, dstJID string) {
	srcLockI, _ := s.appendLocks.LoadOrStore(normalizeJID(srcJID), &sync.Mutex{})
	dstLockI, _ := s.appendLocks.LoadOrStore(normalizeJID(dstJID), &sync.Mutex{})
	srcLock := srcLockI.(*sync.Mutex)
	dstLock := dstLockI.(*sync.Mutex)
	// A deterministic order, to avoid a deadlock if a merge runs the other way
	// round (src/dst swapped in another caller).
	if srcJID < dstJID {
		srcLock.Lock()
		dstLock.Lock()
	} else if srcJID > dstJID {
		dstLock.Lock()
		srcLock.Lock()
	} else {
		// Same JID — no-op (defensive).
		return
	}
	defer srcLock.Unlock()
	defer dstLock.Unlock()
	mergeMessageDirs(s.Root, srcJID, dstJID)
}

// mergeMessageDirs concatenates the JSONL files from srcJID's message
// directory into dstJID's. Used when MergeJIDs absorbs two chats into one —
// src's history has to become part of dst's. It creates the dst dir when
// missing, appends month by month, and removes the src dir at the end. Best
// effort: write errors are swallowed (they do not fail the chat-list merge).
func mergeMessageDirs(storeRoot, srcJID, dstJID string) {
	srcDir := filepath.Join(storeRoot, "messages", chatDir(srcJID))
	dstDir := filepath.Join(storeRoot, "messages", chatDir(dstJID))
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return // src does not exist = nothing to merge
	}
	_ = os.MkdirAll(dstDir, 0o700)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		srcPath := filepath.Join(srcDir, name)
		dstPath := filepath.Join(dstDir, name)
		srcData, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}
		// Dedup: if the merge runs twice without finishing (a restart halfway),
		// messages would be duplicated in the JSONL. Read the IDs already present
		// in the destination before appending, and skip src lines whose ID is
		// already there.
		seenIDs := map[string]bool{}
		if dstData, derr := os.ReadFile(dstPath); derr == nil {
			for _, line := range bytes.Split(dstData, []byte("\n")) {
				if i := bytes.Index(line, []byte(`"id":"`)); i >= 0 {
					rest := line[i+6:]
					if j := bytes.IndexByte(rest, '"'); j > 0 {
						seenIDs[string(rest[:j])] = true
					}
				}
			}
		}
		// Filter src, dropping lines already in dst.
		var toAppend bytes.Buffer
		for _, line := range bytes.Split(srcData, []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			id := ""
			if i := bytes.Index(line, []byte(`"id":"`)); i >= 0 {
				rest := line[i+6:]
				if j := bytes.IndexByte(rest, '"'); j > 0 {
					id = string(rest[:j])
				}
			}
			if id != "" && seenIDs[id] {
				continue
			}
			toAppend.Write(line)
			toAppend.WriteByte('\n')
		}
		if toAppend.Len() > 0 {
			f, err := os.OpenFile(dstPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				continue
			}
			_, _ = f.Write(toAppend.Bytes())
			_ = f.Sync()
			_ = f.Close()
		}
		_ = os.Remove(srcPath)
	}
	_ = os.Remove(srcDir)
}

// safeMediaPath rejects path traversal / absolute paths / NULs / backslashes
// so the media handler can safely join the request param with MediaRoot.
func safeMediaPath(rel string) bool {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return false
	}
	if strings.Contains(rel, "..") {
		return false
	}
	if strings.HasPrefix(rel, "/") {
		return false
	}
	if strings.ContainsAny(rel, "\\\x00") {
		return false
	}
	return true
}
