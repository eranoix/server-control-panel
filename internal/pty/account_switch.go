package pty

import (
	"os"
	"server-control-panel/internal/claudever"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func SessionClaudePID(session string) int {
	session = safeSessionName(session)
	if session == "" {
		return 0
	}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	socks := SessionSockets()
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, _ := os.ReadFile("/proc/" + e.Name() + "/comm")
		if strings.TrimSpace(string(comm)) != "claude" {
			continue
		}
		if claudever.AncestorByArgv(pid, socks) == session {
			return pid
		}
	}
	return 0
}

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func RespawnClaudeInSession(session, setEnvCmd string) (respawned, resumed bool) {
	pid := SessionClaudePID(session)
	if pid <= 0 {
		return false, false
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 40; i++ {
		if !pidAlive(pid) {
			resumed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pidAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		time.Sleep(300 * time.Millisecond)
	}
	_ = SessionSendText(session, "\x15"+setEnvCmd+" && claude --continue\n")
	return true, resumed
}
