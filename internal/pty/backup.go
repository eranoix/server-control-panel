package pty

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

type PaneSnapshot struct {
	Index      int    `json:"index"`
	CWD        string `json:"cwd"`
	Cmd        string `json:"cmd"`
	Scrollback string `json:"scrollback,omitempty"`
}

type WindowSnapshot struct {
	Index  int            `json:"index"`
	Name   string         `json:"name"`
	Layout string         `json:"layout"`
	Panes  []PaneSnapshot `json:"panes"`
}

type SessionSnapshot struct {
	Name    string           `json:"name"`
	Windows []WindowSnapshot `json:"windows"`
}

type Backup struct {
	ID       string            `json:"id"`
	Created  int64             `json:"created"`
	Source   string            `json:"source,omitempty"`
	Sessions []SessionSnapshot `json:"sessions"`
}

const DefaultScrollbackLines = 2000

func SnapshotSession(name string, scrollbackLines int) (SessionSnapshot, error) {
	name = safeSessionName(name)
	return snapshotDtachSession(name, scrollbackLines), nil
}

func RestoreSession(s SessionSnapshot, scratchDir string) error {
	name := safeSessionName(s.Name)
	if name == "" || len(s.Windows) == 0 {
		return nil
	}
	return restoreDtachSession(name, s, scratchDir)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func snapshotDtachSession(name string, scrollbackLines int) SessionSnapshot {
	pane := PaneSnapshot{Index: 0, Cmd: "dtach", CWD: resolveSessionCWD(name)}
	if scrollbackLines > 0 {
		pane.Scrollback = dtachSessionScrollback(name, scrollbackLines)
	}
	return SessionSnapshot{
		Name:    name,
		Windows: []WindowSnapshot{{Index: 0, Name: name, Panes: []PaneSnapshot{pane}}},
	}
}

func dtachSessionScrollback(name string, lines int) string {
	name = safeSessionName(name)
	if user := activeOwner(name); user != "" {
		return tailSessionLog(activeDD(), user, name, lines, false)
	}
	matches, _ := filepath.Glob(filepath.Join(activeDD(), "users", "*", "session-logs", name+".log"))
	if len(matches) != 1 {
		return ""
	}
	data := readLogRotated(matches[0])
	if len(data) == 0 {
		return ""
	}
	rows := strings.Split(stripANSI(string(data)), "\n")
	if lines > 0 && len(rows) > lines {
		rows = rows[len(rows)-lines:]
	}
	return strings.Join(rows, "\n")
}

func restoreDtachSession(name string, s SessionSnapshot, scratchDir string) error {
	if alive, _ := SessionHas(name); alive {
		return nil
	}
	var scrollback, cmd, cwd string
	if len(s.Windows) > 0 && len(s.Windows[0].Panes) > 0 {
		p := s.Windows[0].Panes[0]
		scrollback, cmd, cwd = p.Scrollback, p.Cmd, p.CWD
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	if err := SessionCreateDetached(name, []string{shell, "-l"}, nil, cwd); err != nil {
		return err
	}
	if scratchDir == "" || strings.TrimSpace(scrollback) == "" {
		return nil
	}
	fname := filepath.Join(scratchDir, safeSessionName(name)+".log")
	header := "===== scrollback restored (" + cmd + ") =====\n"
	if err := os.WriteFile(fname, []byte(header+scrollback), 0o600); err != nil {
		return nil
	}
	time.Sleep(400 * time.Millisecond)
	_ = SessionPasteAndEnter(name, "clear; cat "+shellQuote(fname))
	return nil
}
