// account_switch.go — LIVE switching of a session's Claude account.
//
// Switching the account of an already RUNNING `claude` cannot be done in-process: claude
// reads CLAUDE_CONFIG_DIR at boot. So the "live swap" is a RESPAWN: it kills the
// session's claude, and bash (which returns to the prompt) relaunches `claude --continue`
// on the new account. The CONVERSATION is preserved because projects/ is shared between
// the accounts (a symlink to the same /root/.claude-shared/projects) — the --continue
// reopens exactly the same conversation, now with the chosen account's credential.
package pty

import (
	"os"
	"server-control-panel/internal/claudever"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SessionClaudePID finds the PID of the `claude` running in the session. 0 if there is none.
//
// The previous version looked for `VPSM_SESSION=<session>` in the environ — a
// variable NO point in the code ever writes (grep: two readers, zero writers). In
// other words, this function ALWAYS returned 0, and with it the live account
// switch never respawned claude: it fell silently into the "there was no claude
// running" branch and only injected the setEnv, which then applied only to the
// next `claude` typed by hand.
//
// It now uses the same anchor as the version panel: walk up the process tree to
// the session's master, recognised by the socket path in its argv (see
// SessionSockets). It works for sessions that ALREADY EXIST, without recreating them.
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
		if claudever.AncestralPorArgv(pid, socks) == session {
			return pid
		}
	}
	return 0
}

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// RespawnClaudeInSession switches the account LIVE: if a claude is running in the
// session, it sends SIGTERM (SIGKILL if it insists), waits for it to leave and
// injects `<setEnvCmd> && claude --continue` into the bash (which has returned to
// the prompt). Returns (respawned, resumed): respawned=false if there was NO
// claude running (the caller then only injects the setEnv, which applies to the
// next `claude`); resumed=true if claude exited cleanly before the relaunch.
func RespawnClaudeInSession(session, setEnvCmd string) (respawned, resumed bool) {
	pid := SessionClaudePID(session)
	if pid <= 0 {
		return false, false
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for i := 0; i < 40; i++ { // wait up to ~4s for a graceful exit
		if !pidAlive(pid) {
			resumed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pidAlive(pid) { // stubborn → SIGKILL to make sure bash returns to the prompt
		_ = syscall.Kill(pid, syscall.SIGKILL)
		time.Sleep(300 * time.Millisecond)
	}
	// \x15 (Ctrl-U) clears the line; runs the setEnv and relaunches claude in bash.
	_ = SessionSendText(session, "\x15"+setEnvCmd+" && claude --continue\n")
	return true, resumed
}
