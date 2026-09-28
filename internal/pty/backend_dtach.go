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

var errDtachRenameCollision = errors.New("a session with that name already exists")

type dtachBackend struct {
	dataDir string
	reg     *Registry
}

func newDtachBackend(dataDir string, reg *Registry) dtachBackend {
	return dtachBackend{dataDir: dataDir, reg: reg}
}

func (dtachBackend) Kind() string { return "dtach" }

func sessionSoxDir(dataDir string) string { return filepath.Join(dataDir, "session-sox") }

func socketPathFor(dataDir, name string) string {
	return filepath.Join(sessionSoxDir(dataDir), safeSessionName(name)+".sock")
}

func (b dtachBackend) resolveSocket(name string) string {
	if rec, ok := b.reg.Get(name); ok && rec.Socket != "" {
		return rec.Socket
	}
	return socketPathFor(b.dataDir, name)
}

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

func dtachMasterArgs(sdrunPath, dtachPath, sock string, cmd, env []string, cwd string) []string {
	dtachArgs := []string{"-n", sock, "-E", "-z"}
	dtachArgs = append(dtachArgs, cmd...)

	if sdrunPath == "" {
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

func startDtachMaster(dtachPath, sock string, cmd, env []string, cwd string, run func(*exec.Cmd) error) error {
	launch := func(sdrunPath string) error {
		margs := dtachMasterArgs(sdrunPath, dtachPath, sock, cmd, env, cwd)
		var mcmd *exec.Cmd
		if sdrunPath == "" {
			mcmd = exec.Command(margs[0], margs[1:]...)
		} else {
			mcmd = exec.Command(sdrunPath, margs[1:]...)
		}
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

func dtachClientArgs(dtachPath, sock string) []string {
	return []string{dtachPath, "-a", sock, "-E", "-z", "-r", "winch"}
}

func (b dtachBackend) Attach(name string, cmd []string, env []string, cwd string) (*exec.Cmd, error) {
	name = safeSessionName(name)
	if len(cmd) == 0 {
		cmd = []string{"/bin/bash", "-l"}
	}
	sock := b.resolveSocket(name)
	_ = os.MkdirAll(sessionSoxDir(b.dataDir), 0o700)

	dtachPath, err := exec.LookPath("dtach")
	if err != nil {
		return exec.Command(cmd[0], cmd[1:]...), nil
	}

	unlock := flockSession(sock)
	defer unlock()

	if !socketAlive(sock) {
		_ = os.Remove(sock)
		_ = startDtachMaster(dtachPath, sock, cmd, env, cwd, (*exec.Cmd).Run)
		for i := 0; i < 30; i++ {
			if socketAlive(sock) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = b.reg.Put(SessionRecord{Name: name, Socket: sock, Backend: "dtach"})
	}

	cargs := dtachClientArgs(dtachPath, sock)
	return exec.Command(cargs[0], cargs[1:]...), nil
}

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
		return nil
	}
	_ = os.Remove(sock)
	dtachPath, err := exec.LookPath("dtach")
	if err != nil {
		return err
	}
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

func (b dtachBackend) PasteAndEnter(name, text string) error {
	sock := b.resolveSocket(safeSessionName(name))
	dtachPath, err := exec.LookPath("dtach")
	if err != nil {
		return err
	}
	if err := dtachPipe(dtachPath, sock, []byte("\x1b[200~"+text+"\x1b[201~")); err != nil {
		return err
	}
	time.Sleep(400 * time.Millisecond)
	return dtachPipe(dtachPath, sock, []byte("\r"))
}

func dtachPipe(dtachPath, sock string, data []byte) error {
	c := exec.Command(dtachPath, "-p", sock)
	c.Stdin = bytes.NewReader(data)
	return c.Run()
}

func SessionSendText(name, text string) error {
	sock := activeSocket(safeSessionName(name))
	dtachPath, err := exec.LookPath("dtach")
	if err != nil {
		return err
	}
	return dtachPipe(dtachPath, sock, []byte(text))
}

func (b dtachBackend) Has(name string) (bool, error) {
	return socketAlive(b.resolveSocket(safeSessionName(name))), nil
}

func (b dtachBackend) List() ([]map[string]any, error) {
	out := make([]map[string]any, 0)
	seen := map[string]bool{}
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

func (b dtachBackend) Kill(name string) error {
	name = safeSessionName(name)
	sock := b.resolveSocket(name)
	if sock != "" {
		if fuser, err := exec.LookPath("fuser"); err == nil {
			_ = exec.Command(fuser, "-k", "-TERM", sock).Run()
		} else if pkill, err := exec.LookPath("pkill"); err == nil {
			_ = exec.Command(pkill, "-TERM", "-f", "dtach.*"+sock).Run()
		}
		_ = os.Remove(sock)
		_ = os.Remove(sock + ".lock")
	}
	return b.reg.Delete(name)
}

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

func (b dtachBackend) LogPath(dataDir, user, name string) string {
	return sessionLogPath(dataDir, user, name)
}

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
