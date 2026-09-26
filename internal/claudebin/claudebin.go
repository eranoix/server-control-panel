// Package claudebin resolves the path to the `claude` CLI without depending on
// the PATH the process inherited.
//
// Why it exists: the control plane runs as a systemd service, whose PATH is
// systemd's default (/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin) —
// WITHOUT ~/.local/bin, which is where Claude Code's native installer puts the
// binary. The paths that exec `claude` DIRECTLY (dtach/systemd-run and
// exec.Command, with no login shell to source the profile) died with
//
//	dtach: could not execute claude: No such file or directory
//
// which reached the front end as the inscrutable "create session: exit status 1"
// from the "Work now" button. Ordinary terminal sessions did not break because
// they spawn `bash -l`, which rebuilds the PATH from the profile — masking the
// problem in exactly the most-used flows.
//
// Resolving it here makes every AI spawn independent of the supervisor's PATH.
package claudebin

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// EnvOverride is the escape hatch for pointing at a specific binary (e.g. a
// test build, or an installation outside the known paths).
const EnvOverride = "VPSM_CLAUDE_BIN"

var (
	mu     sync.Mutex
	cached string
)

// candidates lists the known Claude Code installations, in the order an operator
// would expect them to win: the per-user native installer first, then the system
// prefixes (npm -g, package).
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

// Path returns the ABSOLUTE path to the `claude` CLI, or a bare "claude" when
// nothing was found (preserving the old behavior instead of failing early: if the
// binary shows up in the child's PATH, it still works).
//
// Only an ABSOLUTE result is cached — so an installation done after the server
// booted is seen on the next call, with no restart.
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

// Dir returns the resolved binary's directory, or "" when resolution fell back to
// the relative form. Used to stitch that directory into the PATH of AI sessions —
// Claude Code itself (hooks, statusline, subagents) calls `claude` by name, so it
// is not enough for the spawn's argv to be absolute.
func Dir() string {
	p := Path()
	if !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Dir(p)
}

// PathEnv returns the PATH value an AI session should receive: the process's
// current PATH with `claude`'s directory in front (without duplicating). It
// returns "" when there is nothing to add — the caller then injects no variable,
// letting the child inherit the env normally.
func PathEnv() string {
	dir := Dir()
	if dir == "" {
		return ""
	}
	cur := os.Getenv("PATH")
	if cur == "" {
		return dir
	}
	for _, seg := range filepath.SplitList(cur) {
		if seg == dir {
			return ""
		}
	}
	return dir + string(os.PathListSeparator) + cur
}
