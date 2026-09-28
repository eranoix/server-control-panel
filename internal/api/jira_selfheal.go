package api

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

func claudeProjectDirSlug(cwd string) string {
	var b strings.Builder
	for _, c := range cwd {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func claudeProjectHasMessages(configDir, cwd string) bool {
	if strings.TrimSpace(cwd) == "" {
		return true
	}
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return true
		}
		configDir = filepath.Join(home, ".claude")
	}
	dir := filepath.Join(configDir, "projects", claudeProjectDirSlug(cwd))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return !os.IsNotExist(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		head := make([]byte, 256*1024)
		n, _ := io.ReadFull(f, head)
		f.Close()
		if strings.Contains(string(head[:n]), `"type":"user"`) {
			return true
		}
	}
	return false
}
