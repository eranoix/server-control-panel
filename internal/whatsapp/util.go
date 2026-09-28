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

func stdEncode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func chatDir(jid string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(jid)))
	return hex.EncodeToString(sum[:8])
}

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

func normalizeJID(jid string) string {
	if jid == "" {
		return jid
	}
	if strings.HasSuffix(jid, "@s.whatsapp.net") {
		user := strings.TrimSuffix(jid, "@s.whatsapp.net")
		if i := strings.IndexByte(user, ':'); i > 0 {
			user = user[:i]
		}
		return user + "@c.us"
	}
	return jid
}

func isGroupJID(jid string) bool { return strings.HasSuffix(jid, "@g.us") }

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

func (s *Store) MergeMessageDirs(srcJID, dstJID string) {
	srcLockI, _ := s.appendLocks.LoadOrStore(normalizeJID(srcJID), &sync.Mutex{})
	dstLockI, _ := s.appendLocks.LoadOrStore(normalizeJID(dstJID), &sync.Mutex{})
	srcLock := srcLockI.(*sync.Mutex)
	dstLock := dstLockI.(*sync.Mutex)
	if srcJID < dstJID {
		srcLock.Lock()
		dstLock.Lock()
	} else if srcJID > dstJID {
		dstLock.Lock()
		srcLock.Lock()
	} else {
		return
	}
	defer srcLock.Unlock()
	defer dstLock.Unlock()
	mergeMessageDirs(s.Root, srcJID, dstJID)
}

func mergeMessageDirs(storeRoot, srcJID, dstJID string) {
	srcDir := filepath.Join(storeRoot, "messages", chatDir(srcJID))
	dstDir := filepath.Join(storeRoot, "messages", chatDir(dstJID))
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
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
