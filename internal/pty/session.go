package pty

import (
	"sync"

	"server-control-panel/internal/claudever"
)

var (
	activeMu      sync.RWMutex
	activeBackend SessionBackend
	activeDataDir string
	activeReg     *Registry
	activeOwn     *Ownership
)

func SetActiveOwnership(o *Ownership) {
	activeMu.Lock()
	defer activeMu.Unlock()
	activeOwn = o
}

func activeOwner(name string) string {
	activeMu.RLock()
	o := activeOwn
	activeMu.RUnlock()
	return o.Owner(name)
}

var activeCWDFn func(string) string

func SetActiveCWDResolver(fn func(string) string) {
	activeMu.Lock()
	activeCWDFn = fn
	activeMu.Unlock()
}

func resolveSessionCWD(name string) string {
	activeMu.RLock()
	fn := activeCWDFn
	activeMu.RUnlock()
	if fn == nil {
		return ""
	}
	return fn(name)
}

func InitSessionBackend(dataDir string, reg *Registry) {
	activeMu.Lock()
	defer activeMu.Unlock()
	activeDataDir = dataDir
	activeReg = reg
	activeBackend = NewSessionBackend(dataDir, reg)
}

func ActiveBackend() SessionBackend {
	activeMu.RLock()
	defer activeMu.RUnlock()
	if activeBackend == nil {
		return newDtachBackend(activeDataDir, activeReg)
	}
	return activeBackend
}

func activeDD() string {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return activeDataDir
}

func activeSocket(name string) string {
	activeMu.RLock()
	dd, reg := activeDataDir, activeReg
	activeMu.RUnlock()
	if reg != nil {
		if rec, ok := reg.Get(name); ok && rec.Socket != "" {
			return rec.Socket
		}
	}
	return socketPathFor(dd, name)
}

func SessionHas(name string) (bool, error) { return ActiveBackend().Has(name) }

func SessionKill(name string) error { return ActiveBackend().Kill(name) }

func SessionRename(old, newName string) error { return ActiveBackend().Rename(old, newName) }

func SessionListAll() ([]map[string]any, error) { return ActiveBackend().List() }

func SessionCreateDetached(name string, argv, env []string, cwd string) error {
	return ActiveBackend().CreateDetached(name, argv, env, cwd)
}

func SessionPasteAndEnter(name, text string) error {
	return ActiveBackend().PasteAndEnter(name, text)
}

func SessionTail(name string, lines int) string {
	return dtachSessionScrollback(name, lines)
}

func SessionScrollback(user, name string, lines int, escapes bool) string {
	return tailSessionLog(activeDD(), user, name, lines, escapes)
}

func SessionRawLogTail(user, name string, maxBytes int) ([]byte, int) {
	return rawLogTail(activeDD(), user, name, maxBytes)
}

func SessionSockets() map[string]string {
	out := map[string]string{}
	sess, err := SessionListAll()
	if err != nil {
		return out
	}
	dd := activeDD()
	activeMu.RLock()
	reg := activeReg
	activeMu.RUnlock()
	for _, s := range sess {
		name, _ := s["name"].(string)
		if name == "" {
			continue
		}
		sock := ""
		if reg != nil {
			if rec, ok := reg.Get(name); ok && rec.Socket != "" {
				sock = rec.Socket
			}
		}
		if sock == "" {
			sock = socketPathFor(dd, name)
		}
		out[sock] = name
	}
	return out
}

func SessionOf(pid int) string {
	return claudever.AncestorByArgv(pid, SessionSockets())
}
