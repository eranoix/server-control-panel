package claudebin

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

const EnvOverride = "PANEL_CLAUDE_BIN"

var (
	mu     sync.Mutex
	cached string
)

func candidates() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".local", "bin", "claude"))
	}
	return append(out,
		"/root/.local/bin/claude",
		"/usr/local/bin/claude",
		"/usr/bin/claude",
		"/opt/claude/bin/claude",
	)
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

func Path() string {
	mu.Lock()
	defer mu.Unlock()
	if cached != "" {
		return cached
	}
	if p := os.Getenv(EnvOverride); p != "" && executable(p) {
		cached = p
		return cached
	}
	if p, err := exec.LookPath("claude"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			cached = abs
			return cached
		}
	}
	for _, p := range candidates() {
		if executable(p) {
			cached = p
			return cached
		}
	}
	return "claude"
}

func Dir() string {
	p := Path()
	if !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Dir(p)
}

func PathEnv() string {
	dir := Dir()
	if dir == "" {
		return ""
	}
	cur := os.Getenv("PATH")
	if cur == "" {
		return dir
	}
	for _, secret := range filepath.SplitList(cur) {
		if secret == dir {
			return ""
		}
	}
	return dir + string(os.PathListSeparator) + cur
}
