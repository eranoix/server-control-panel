package api

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// claudeProjectDirSlug reproduces the folder name Claude Code uses under
// <config>/projects/: every non-alphanumeric character becomes '-'. E.g.:
// /opt/panel/.claude/worktrees/feature-x
//
//	-> -opt-panel--claude-worktrees-feature-x
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

// claudeProjectHasMessages says whether a Claude Code conversation ALREADY exists
// in that cwd. The transcript (.jsonl) only gains a "type":"user" line when a
// message is actually received, so its absence means the session
// came up but never started working.
//
// It reads only the beginning of each file: the transcript is chronological, so the
// user's first message is at the top — there is no reason to scan MBs.
//
// When in doubt it returns true (do not self-heal): erring towards "leave it alone"
// preserves existing work, whereas erring the other way would kill a live session.
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
		// a missing folder = Claude Code never wrote anything there
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
