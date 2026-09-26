// backend_dtach.go — dtachBackend: the transparent engine.
//
// dtach only keeps a process alive behind a unix socket (attach/detach), WITHOUT
// managing the screen — that is what gives xterm.js back native
// selection/scroll/right-click. What dtach does NOT provide (a session catalogue,
// has/list/rename/kill) is rebuilt here on top of the session registry + the
// socket file + process liveness. Survival: the dtach master is born inside a
// `systemd-run --scope --slice=user.slice`, EXACTLY as the previous engine did —
// it survives a service restart, a deploy and a logout.
//
// Isolation (replacing the dedicated `-L vpsmgr` socket): every socket lives in a
// dedicated directory <DataDir>/session-sox/, far from the SSH/screen sockets.
package pty

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// errDtachRenameCollision mirrors the refusal the old rename gave when the new
// name already designates a live/registered session.
var errDtachRenameCollision = errors.New("a session with that name already exists")

// dtachBackend implements SessionBackend on top of dtach + registry.
type dtachBackend struct {
	dataDir string
	reg     *Registry
}

func newDtachBackend(dataDir string, reg *Registry) dtachBackend {
	return dtachBackend{dataDir: dataDir, reg: reg}
}

func (dtachBackend) Kind() string { return "dtach" }

// sessionSoxDir is the dedicated socket directory (isolation vs SSH/screen).
func sessionSoxDir(dataDir string) string { return filepath.Join(dataDir, "session-sox") }

// socketPathFor is the DEFAULT socket path of a new session. After a rename the
// real socket lives in the registry (record.Socket) — decoupled from the display
// name — so callers should prefer the registry whenever there is an entry.
func socketPathFor(dataDir, name string) string {
	return filepath.Join(sessionSoxDir(dataDir), safeSessionName(name)+".sock")
}

// resolveSocket returns the socket to use for `name`: the one recorded in the
// registry (if there is one) or the default derived from the name.
func (b dtachBackend) resolveSocket(name string) string {
	if rec, ok := b.reg.Get(name); ok && rec.Socket != "" {
		return rec.Socket
	}
	return socketPathFor(b.dataDir, name)
}

// flockSession takes a per-session exclusive lock (the file <sock>.lock) and
// returns the release function. It serialises the check-alive→remove→spawn→wait
// sequence of master creation: without it, two concurrent Attach/CreateDetached
// on the SAME session both saw "!alive", both called os.Remove(sock) — one of
// them removing the LIVE socket the other had just created — and both attached
// to the second master, leaving the first one ORPHANED (a shell running,
// unreachable). The lock is per socket, so different sessions do not serialise
// against each other. Failing to open the lock → a no-op (it degrades to the old
// behaviour instead of blocking the attach).
func flockSession(sock string) func() {
	lf, err := os.OpenFile(sock+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX)
	return func() {
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		_ = lf.Close()
	}
}

// socketAlive reports whether a dtach master is listening on the socket. An
// orphaned socket file (a master SIGKILLed without cleaning up) exists but
// refuses connections → treated as dead. Dial+close is the canonical liveness
// check for a unix socket; the master merely sees a client that attached and left
// (inert).
func socketAlive(sock string) bool {
	if sock == "" {
		return false
	}
	if _, err := os.Stat(sock); err != nil {
		return false
	}
	c, err := net.DialTimeout("unix", sock, 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// dtachMasterArgs assembles the argv of the `systemd-run … dtach -n …` that
// CREATES the master (detached) sandboxed in user.slice — the persistent process
// that survives a service restart (as the previous engine did). Extracted so it
// can be tested without spawning (mirroring the previous engine's test).
// sdrunPath=="" → bare dtach (fallback without systemd-run). The -r winch does
// NOT belong here: redraw is the client's business.
//
// Options: -n creates it detached; -E disables the detach character (Ctrl-\
// passes through to the shell → transparent); -z does not intercept suspend.
func dtachMasterArgs(sdrunPath, dtachPath, sock string, cmd, env []string, cwd string) []string {
	dtachArgs := []string{"-n", sock, "-E", "-z"}
	dtachArgs = append(dtachArgs, cmd...)

	if sdrunPath == "" {
		// Without systemd-run there is no `--setenv`: the env goes through the argv,
		// via `env`. It used to be silently DISCARDED here, which brought the session
		// up on the wrong Claude account (CLAUDE_CONFIG_DIR lost) instead of failing
		// visibly.
		if len(env) > 0 {
			argv := []string{"env"}
			argv = append(argv, env...)
			argv = append(argv, dtachPath)
			return append(argv, dtachArgs...)
		}
		return append([]string{dtachPath}, dtachArgs...)
	}
	sd := []string{"--quiet", "--collect", "--scope", "--slice=user.slice"}
	for _, e := range env {
		sd = append(sd, "--setenv="+e)
	}
	if cwd != "" {
		sd = append(sd, "--working-directory="+cwd)
	}
	sd = append(sd, "--", dtachPath)
	sd = append(sd, dtachArgs...)
	return sd
}

// startDtachMaster runs the command that creates a session's master and
// returns its error. `run` executes the command (Attach only waits for it,
// CreateDetached also keeps its output).
//
// systemd-run comes first because its scope keeps the master alive across a
// service restart. But a SYSTEM scope needs privileges: run by an ordinary
// user it fails with "Interactive authentication required" before dtach
// ever starts. Attach used to ignore that error and attach a client to a
// socket nobody listened on, so there was no master, no recorder and no
// server screen, and a window in frame mode stayed blank. When the scoped
// launch fails and the socket is still dead, the master is started with bare
// dtach, the path already taken on a machine without systemd-run.
func startDtachMaster(dtachPath, sock string, cmd, env []string, cwd string, run func(*exec.Cmd) error) error {
	launch := func(sdrunPath string) error {
		margs := dtachMasterArgs(sdrunPath, dtachPath, sock, cmd, env, cwd)
		var mcmd *exec.Cmd
		if sdrunPath == "" {
			mcmd = exec.Command(margs[0], margs[1:]...)
		} else {
			mcmd = exec.Command(sdrunPath, margs[1:]...)
		}
		// Covers the fallback without systemd-run, where --working-directory is absent.
		if cwd != "" {
			mcmd.Dir = cwd
		}
		return run(mcmd)
	}
	sdrunPath, _ := exec.LookPath("systemd-run")
	err := launch(sdrunPath)
	if err != nil && sdrunPath != "" && !socketAlive(sock) {
		err = launch("")
	}
	return err
}

// dtachClientArgs assembles the argv of the CLIENT that attaches (`dtach -a`).
// The client does NOT go through the scope — it lives in the caller's cgroup (the
// terminal process): if it dies (tab/WS closed), it only DETACHES; the master
// persists. -E/-z keep the transparency; -r winch redraws via SIGWINCH on attach.
func dtachClientArgs(dtachPath, sock string) []string {
	return []string{dtachPath, "-a", sock, "-E", "-z", "-r", "winch"}
}

// Attach makes sure the MASTER is alive (creating it detached in user.slice when
// needed) and returns the *exec.Cmd of the CLIENT (`dtach -a`), ready for
// pty.Start. Split -n/-a (not a single-shot -A) so that the master is a process
// independent of the client — exactly the pattern of the session-attach.sh
// wrapper, whose survival across a restart was validated empirically. Side
// effect: it may spawn the master.
func (b dtachBackend) Attach(name string, cmd []string, env []string, cwd string) (*exec.Cmd, error) {
	name = safeSessionName(name)
	if len(cmd) == 0 {
		cmd = []string{"/bin/bash", "-l"}
	}
	sock := b.resolveSocket(name)
	_ = os.MkdirAll(sessionSoxDir(b.dataDir), 0o700)

	dtachPath, err := exec.LookPath("dtach")
	if err != nil {
		// No dtach: degrade to a bare shell (as the previous fallback did).
		return exec.Command(cmd[0], cmd[1:]...), nil
	}

	// Serialises master creation per session (anti-race against an orphaned master).
	unlock := flockSession(sock)
	defer unlock()

	// Master missing/orphaned → clean up the dead socket file and create the master detached.
	if !socketAlive(sock) {
		_ = os.Remove(sock)
		// `dtach -n` forks the master and returns; Run() waits for that return (fast).
		// Stdout/err go to /dev/null (zero-value Cmd) → it holds none of the server's fds.
		// A failure still shows up below, as a socket that never comes alive.
		_ = startDtachMaster(dtachPath, sock, cmd, env, cwd, (*exec.Cmd).Run)
		// Wait for the master to start listening (up to ~3s) before attaching the client.
		for i := 0; i < 30; i++ {
			if socketAlive(sock) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = b.reg.Put(SessionRecord{Name: name, Socket: sock, Backend: "dtach"})
	}

	// The client that attaches (in the caller's cgroup; dying = detach, master persists).
	cargs := dtachClientArgs(dtachPath, sock)
	return exec.Command(cargs[0], cargs[1:]...), nil
}

// CreateDetached creates the session detached (master in user.slice) running
// argv, with no client. It reuses Attach's recipe (an `-n` master). A no-op if it
// is already alive.
func (b dtachBackend) CreateDetached(name string, argv []string, env []string, cwd string) error {
	name = safeSessionName(name)
	if len(argv) == 0 {
		argv = []string{"/bin/bash", "-l"}
	}
	sock := b.resolveSocket(name)
	_ = os.MkdirAll(sessionSoxDir(b.dataDir), 0o700)
	unlock := flockSession(sock)
	defer unlock()
	if socketAlive(sock) {
		return nil // already exists (attach-or-create)
	}
	_ = os.Remove(sock)
	dtachPath, err := exec.LookPath("dtach")
	if err != nil {
		return err
	}
	// CombinedOutput (not Run): without it the stderr went to /dev/null and the real
	// cause — e.g. `dtach: could not execute claude: No such file or directory`
	// — reached the front end as an undecipherable "exit status 1".
	var out []byte
	if err := startDtachMaster(dtachPath, sock, argv, env, cwd, func(c *exec.Cmd) error {
		var runErr error
		out, runErr = c.CombinedOutput()
		return runErr
	}); err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%w: %s", err, firstLine(msg))
		}
		return err
	}
	for i := 0; i < 30; i++ {
		if socketAlive(sock) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return b.reg.Put(SessionRecord{Name: name, Socket: sock, Backend: "dtach"})
}

// PasteAndEnter injects text as a bracketed paste + Enter, via `dtach -p` (which
// copies stdin into the socket = the session's input). Replaces
// load-buffer/paste-buffer/send-keys.
func (b dtachBackend) PasteAndEnter(name, text string) error {
	sock := b.resolveSocket(safeSessionName(name))
	dtachPath, err := exec.LookPath("dtach")
	if err != nil {
		return err
	}
	// ESC[200~ … ESC[201~ = bracketed paste (Claude Code treats it as a paste, not
	// as keystrokes → it preserves multiple lines without submitting them one by one).
	if err := dtachPipe(dtachPath, sock, []byte("\x1b[200~"+text+"\x1b[201~")); err != nil {
		return err
	}
	// Slack for Claude to debounce the paste before the Enter (\r).
	time.Sleep(400 * time.Millisecond)
	return dtachPipe(dtachPath, sock, []byte("\r"))
}

// dtachPipe copies `data` into the session's socket (input) via `dtach -p`.
func dtachPipe(dtachPath, sock string, data []byte) error {
	c := exec.Command(dtachPath, "-p", sock)
	c.Stdin = bytes.NewReader(data)
	return c.Run()
}

// SessionSendText injects `text` RAW into the active session's input (via
// `dtach -p`), without bracketed paste. Used to send shell commands (e.g.
// `export …\n`) — it plays the role of the old key-sending. Best-effort.
func SessionSendText(name, text string) error {
	sock := activeSocket(safeSessionName(name))
	dtachPath, err := exec.LookPath("dtach")
	if err != nil {
		return err
	}
	return dtachPipe(dtachPath, sock, []byte(text))
}

// Has: a live session == a socket with a listener. Authoritative (it ignores a stale registry).
func (b dtachBackend) Has(name string) (bool, error) {
	return socketAlive(b.resolveSocket(safeSessionName(name))), nil
}

// List: the registry's dtach sessions that are still alive. Same shape as the old
// listing (name/tab/created/attached). attached stays false — dtach does not
// expose a client count; the UI does not depend on it to work.
func (b dtachBackend) List() ([]map[string]any, error) {
	out := make([]map[string]any, 0)
	seen := map[string]bool{} // by SOCKET path, so as not to duplicate
	for _, rec := range b.reg.List() {
		if rec.Backend != "dtach" {
			continue
		}
		if !socketAlive(rec.Socket) {
			continue
		}
		out = append(out, map[string]any{
			"name":     rec.Name,
			"tab":      rec.Name,
			"created":  rec.Created,
			"attached": false,
		})
		seen[rec.Socket] = true
	}
	// Sessions that are ALIVE but UNREGISTERED (e.g. created by code-server's
	// vpsm-session-attach, which does NOT write the registry) vanished from the
	// site's list — that is what made "Vpsm" disappear. Sweep the sox dir and include
	// any live socket not already covered by the registry, using the file name as
	// the name.
	soxDir := sessionSoxDir(b.dataDir)
	if ents, err := os.ReadDir(soxDir); err == nil {
		for _, e := range ents {
			nm := e.Name()
			if !strings.HasSuffix(nm, ".sock") {
				continue
			}
			sock := filepath.Join(soxDir, nm)
			if seen[sock] || !socketAlive(sock) {
				continue
			}
			name := strings.TrimSuffix(nm, ".sock")
			out = append(out, map[string]any{
				"name":     name,
				"tab":      name,
				"created":  int64(0),
				"attached": false,
			})
			seen[sock] = true
		}
	}
	return out, nil
}

// Kill: kills whoever holds the socket (the master + attached clients) and cleans
// up the socket file + the registry. Idempotent. `fuser -k` reaches the master
// even though we never stored its pid (the `-A` master is not easy to capture).
func (b dtachBackend) Kill(name string) error {
	name = safeSessionName(name)
	sock := b.resolveSocket(name)
	if sock != "" {
		if fuser, err := exec.LookPath("fuser"); err == nil {
			// -k kills; -TERM first (graceful). Ignores the error (nobody is holding it).
			_ = exec.Command(fuser, "-k", "-TERM", sock).Run()
		} else if pkill, err := exec.LookPath("pkill"); err == nil {
			// Fallback: match the socket in dtach's argv.
			_ = exec.Command(pkill, "-TERM", "-f", "dtach.*"+sock).Run()
		}
		_ = os.Remove(sock)
		_ = os.Remove(sock + ".lock") // clean up the lock file left by the master's creation
	}
	return b.reg.Delete(name)
}

// Rename: purely registry (the display name is decoupled from the socket). The
// physical socket stays where it is; the record travels with it. Refuses if
// newName already exists and is alive (parity with the previous rename).
func (b dtachBackend) Rename(old, newName string) error {
	old = safeSessionName(old)
	newName = safeSessionName(newName)
	if old == "" || newName == "" || old == newName {
		return nil
	}
	if socketAlive(b.resolveSocket(newName)) || b.reg.Has(newName) {
		return errDtachRenameCollision
	}
	return b.reg.Rename(old, newName)
}

// LogPath: the tee's log (the single source for scrollback/preview/backup).
func (b dtachBackend) LogPath(dataDir, user, name string) string {
	return sessionLogPath(dataDir, user, name)
}

// firstLine returns the first non-empty line of s, truncated — enough to identify
// the failure in a toast, without dumping a whole log into the UI.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			if len(ln) > 200 {
				return ln[:200]
			}
			return ln
		}
	}
	return ""
}
