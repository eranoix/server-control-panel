package pty

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var errPermMode = errors.New("invalid permission mode (use plan|acceptEdits|default)")

var permModeAllow = map[string]bool{
	"plan":        true,
	"acceptEdits": true,
	"default":     true,
}

func ValidPermMode(m string) bool { return permModeAllow[m] }

func sessionPermModePath(dataDir string) string {
	return filepath.Join(dataDir, "session-permmode.json")
}

var permModeMu sync.Mutex

func loadPermModes(dataDir string) map[string]string {
	m := map[string]string{}
	if data, err := os.ReadFile(sessionPermModePath(dataDir)); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &m)
		if m == nil {
			m = map[string]string{}
		}
	}
	return m
}

func SessionPermMode(dataDir, session string) string {
	if dataDir == "" || session == "" {
		return ""
	}
	permModeMu.Lock()
	defer permModeMu.Unlock()
	mode := loadPermModes(dataDir)[safeSessionName(session)]
	if !ValidPermMode(mode) {
		return ""
	}
	return mode
}

func SetSessionPermMode(dataDir, session, mode string) error {
	session = safeSessionName(session)
	permModeMu.Lock()
	defer permModeMu.Unlock()
	m := loadPermModes(dataDir)
	if mode == "" {
		delete(m, session)
	} else {
		if !ValidPermMode(mode) {
			return errPermMode
		}
		m[session] = mode
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := sessionPermModePath(dataDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, sessionPermModePath(dataDir))
}

func permModeArgs(session string) []string {
	if mode := SessionPermMode(activeDD(), session); mode != "" {
		return []string{"--permission-mode", mode}
	}
	return nil
}
