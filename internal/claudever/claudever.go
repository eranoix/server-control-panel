package claudever

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Process struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
	Session string `json:"session,omitempty"`
	Current bool   `json:"current"`
	Cwd     string `json:"cwd,omitempty"`
	Target  string `json:"target,omitempty"`
	Ref     string `json:"ref,omitempty"`
}

type State struct {
	Installed string    `json:"installed"`
	Processes []Process `json:"processes"`
	Outdated  int       `json:"outdated"`
	Available bool      `json:"available"`
}

var procRoot = "/proc"

var cliPath = "/root/.local/bin/claude"

func InstalledVersion() string {
	target, err := filepath.EvalSymlinks(cliPath)
	if err != nil {
		return ""
	}
	return versionFromPath(target)
}

func versionFromPath(p string) string {
	if p == "" || !strings.Contains(p, "/claude/versions/") {
		return ""
	}
	base := filepath.Base(p)
	for _, r := range base {
		if (r < '0' || r > '9') && r != '.' {
			return ""
		}
	}
	return base
}

func Detect(sessionOwner func(pid int) string) State {
	e := State{Installed: InstalledVersion(), Processes: []Process{}}
	e.Available = e.Installed != ""

	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return e
	}
	for _, ent := range ents {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid <= 0 {
			continue
		}
		target, err := os.Readlink(filepath.Join(procRoot, ent.Name(), "exe"))
		if err != nil {
			continue
		}
		version := versionFromPath(target)
		if version == "" {
			continue
		}
		if !sameMount(pid) {
			continue
		}
		p := Process{PID: pid, Version: version, Current: !isOlder(version, e.Installed)}
		if cwd, err := os.Readlink(filepath.Join(procRoot, ent.Name(), "cwd")); err == nil {
			p.Cwd = cwd
		}
		if sessionOwner != nil {
			p.Session = sessionOwner(pid)
		}
		if !p.Current {
			e.Outdated++
		}
		e.Processes = append(e.Processes, p)
	}
	return e
}

func isOlder(v, ref string) bool {
	if v == "" || ref == "" || v == ref {
		return false
	}
	pv, pr := strings.Split(v, "."), strings.Split(ref, ".")
	for i := 0; i < len(pv) || i < len(pr); i++ {
		a, b := 0, 0
		if i < len(pv) {
			a, _ = strconv.Atoi(pv[i])
		}
		if i < len(pr) {
			b, _ = strconv.Atoi(pr[i])
		}
		if a != b {
			return a < b
		}
	}
	return false
}

func sameMount(pid int) bool {
	mine, err := os.Readlink(filepath.Join(procRoot, "self", "ns", "mnt"))
	if err != nil {
		return true
	}
	theirs, err := os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "ns", "mnt"))
	if err != nil {
		return true
	}
	return mine == theirs
}

func ParentOf(pid int) int {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[i+2:])
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

func AncestorIn(pid int, targets map[int]bool) int {
	for hop := 0; hop < 32 && pid > 1; hop++ {
		if targets[pid] {
			return pid
		}
		parent := ParentOf(pid)
		if parent == pid || parent <= 0 {
			return 0
		}
		pid = parent
	}
	return 0
}

func AncestorByArgv(pid int, marks map[string]string) string {
	if len(marks) == 0 {
		return ""
	}
	for hop := 0; hop < 32 && pid > 1; hop++ {
		b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
		if err == nil && len(b) > 0 {
			line := strings.ReplaceAll(string(b), "\x00", " ")
			for mark, value := range marks {
				if mark != "" && strings.Contains(line, mark) {
					return value
				}
			}
		}
		parent := ParentOf(pid)
		if parent == pid || parent <= 0 {
			return ""
		}
		pid = parent
	}
	return ""
}

func DetectExternal(markerEnv, target string) []Process {
	outside := []Process{}
	if markerEnv == "" {
		return outside
	}
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return outside
	}
	mark := []byte(markerEnv)
	for _, ent := range ents {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid <= 0 {
			continue
		}
		exe, err := os.Readlink(filepath.Join(procRoot, ent.Name(), "exe"))
		if err != nil {
			continue
		}
		version := versionFromPath(exe)
		if version == "" || sameMount(pid) {
			continue
		}
		env, err := os.ReadFile(filepath.Join(procRoot, ent.Name(), "environ"))
		if err != nil {
			continue
		}
		found := false
		for _, kv := range bytesSplitNUL(env) {
			if string(kv) == string(mark) {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		p := Process{PID: pid, Version: version, Target: target, Current: true}
		if ref, err := os.Readlink(filepath.Join(procRoot, ent.Name(), "root", "root", ".local", "bin", "claude")); err == nil {
			p.Ref = versionFromPath(resolveRel(filepath.Join(procRoot, ent.Name(), "root"), ref))
		}
		if p.Ref != "" {
			p.Current = !isOlder(version, p.Ref)
		}
		if cwd, err := os.Readlink(filepath.Join(procRoot, ent.Name(), "cwd")); err == nil {
			p.Cwd = cwd
		}
		outside = append(outside, p)
	}
	return outside
}

func resolveRel(root, target string) string {
	if strings.HasPrefix(target, "/") {
		return filepath.Join(root, target)
	}
	return target
}

func bytesSplitNUL(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == 0 {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
