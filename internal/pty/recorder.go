package pty

import (
	"log"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

type recorder struct {
	cmd       *exec.Cmd
	ptmx      *os.File
	screen    *sessionScreen
	closeOnce sync.Once
}

func (g *recorder) stop() {
	g.closeOnce.Do(func() {
		if g.ptmx != nil {
			_ = g.ptmx.Close()
		}
		if g.cmd != nil && g.cmd.Process != nil {
			_ = g.cmd.Process.Kill()
			_ = g.cmd.Wait()
		}
	})
}

var (
	recordersMu sync.Mutex
	recorders   = map[string]*recorder{}
)

func EnsureRecorder(dataDir, user, name string, reg *Registry) {
	if dataDir == "" || user == "" || name == "" {
		return
	}
	key := sessionLogPath(dataDir, user, name)

	recordersMu.Lock()
	if _, already := recorders[key]; already {
		recordersMu.Unlock()
		return
	}
	recorders[key] = nil
	recordersMu.Unlock()

	g, err := openRecorder(dataDir, user, name, reg, key)
	recordersMu.Lock()
	if err != nil {
		delete(recorders, key)
	} else {
		recorders[key] = g
	}
	recordersMu.Unlock()
}

func openRecorder(dataDir, user, name string, reg *Registry, key string) (*recorder, error) {
	backend := NewSessionBackend(dataDir, reg)
	if alive, err := backend.Has(name); err != nil || !alive {
		return nil, os.ErrNotExist
	}
	cmd, err := backend.Attach(name, nil, nil, "")
	if err != nil || cmd == nil {
		return nil, os.ErrInvalid
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	g := &recorder{cmd: cmd, ptmx: ptmx}

	tee, session, id, release := acquireSessionLog(dataDir, user, name)
	screen := newSessionScreen(dataDir, user, name)
	g.screen = screen
	if session != nil {
		apply := func(cols, rows uint16) {
			if cols < 2 || rows < 1 {
				return
			}
			_ = pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
			screen.resize(cols, rows)
		}
		if c, r := session.registerApplier(id, apply); c > 0 && r > 0 {
			apply(c, r)
		}
	}

	go func() {
		defer func() {
			release()
			screen.closeOnce()
			g.stop()
			recordersMu.Lock()
			if current := recorders[key]; current == g {
				delete(recorders, key)
			}
			recordersMu.Unlock()
		}()
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_, _ = tee.Write(buf[:n])
				screen.feed(buf[:n])
				screen.flushNotice()
			}
			if err != nil {
				return
			}
		}
	}()
	return g, nil
}

func screenOf(dataDir, user, name string) *sessionScreen {
	if dataDir == "" || user == "" || name == "" {
		return nil
	}
	key := sessionLogPath(dataDir, user, name)
	recordersMu.Lock()
	defer recordersMu.Unlock()
	if g := recorders[key]; g != nil {
		return g.screen
	}
	return nil
}

func StopRecorder(dataDir, user, name string) {
	if dataDir == "" || user == "" || name == "" {
		return
	}
	key := sessionLogPath(dataDir, user, name)
	recordersMu.Lock()
	g := recorders[key]
	delete(recorders, key)
	recordersMu.Unlock()
	if g != nil {
		g.stop()
	}
}

func EnsureLiveSessionRecorders(dataDir string, reg *Registry, own *Ownership, primary string) {
	if dataDir == "" || own == nil {
		return
	}
	sessions, err := NewSessionBackend(dataDir, reg).List()
	if err != nil {
		return
	}
	attached := 0
	for _, s := range sessions {
		name, _ := s["name"].(string)
		if name == "" {
			continue
		}
		owner := own.Owner(name)
		if owner == "" || owner == AudienceAll {
			owner = primary
		}
		if owner == "" {
			continue
		}
		EnsureRecorder(dataDir, owner, name, reg)
		attached++
	}
	if attached > 0 {
		log.Printf("[pty] log recorder attached to %d session(s) already alive", attached)
	}
}
