// Package git implements the visual Git client of VPS Manager: reading
// (commit graph, status, diff, branches) and writing (stage, commit, branch,
// checkout, discard) over an allowlist of repositories, each one with its own
// policy (read-only/write) and expected commit identity.
//
// The project's pattern = shell-out through os/exec (there is no go-git in
// go.mod). Every route is primary-gated (httpx.MustPrimary) and audited. The
// security boundary is twofold: (1) the repo has to be in the allowlist
// resolved from config; (2) write paths are jailed to the repo root. git.go
// never runs a shell — always exec.Command("git","-C",repo,argv...) with
// separate arguments, so there is no injection through metacharacters.
package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// execTimeout bounds any git invocation. Local read/write operations finish
// in milliseconds; 45s is ample slack that still kills a stuck git (e.g. an
// interactive hook) without hanging the handler forever.
const execTimeout = 45 * time.Second

// errLocked signals index contention (another process/session writing to the
// same repo). The handler translates it to HTTP 409 — it never leaks raw stderr.
var errLocked = errors.New("git: repository busy (index locked)")

// runResult captures the output of one git invocation.
type runResult struct {
	Stdout string
	Stderr string
	Code   int
}

// run executes `git -C <repoPath> <args...>` with no shell, under a timeout.
// It always returns a filled runResult (even on error) so the caller can
// decide the message; err != nil only on a process/timeout failure (not on
// exit!=0, which comes back in Code — git uses non-zero exit codes for
// ordinary cases such as "no differences").
func run(ctx context.Context, repoPath string, args ...string) (runResult, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// A minimal, non-interactive environment: no pager, no credential/terminal
	// prompt that could hang the process.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	res := runResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() == context.DeadlineExceeded {
		return res, ctx.Err()
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.Code = ee.ExitCode()
			return res, nil // exit!=0 is not a process failure
		}
		return res, err // a real spawn failure
	}
	return res, nil
}

// runEnv is like run, but injects extra environment variables (e.g.
// GIT_SEQUENCE_EDITOR/GIT_EDITOR, to drive the interactive rebase without
// opening an editor). extraEnv = "KEY=value" pairs.
func runEnv(ctx context.Context, repoPath string, extraEnv []string, args ...string) (runResult, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	res := runResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() == context.DeadlineExceeded {
		return res, ctx.Err()
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.Code = ee.ExitCode()
			return res, nil
		}
		return res, err
	}
	return res, nil
}

// runStdin is like run, but feeds `stdin` into the git process (used by
// `git apply` to apply a patch/hunk coming from the client). The same
// guarantees of timeout, non-interactive environment and exit-code handling.
func runStdin(ctx context.Context, repoPath, stdin string, args ...string) (runResult, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
	)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	res := runResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() == context.DeadlineExceeded {
		return res, ctx.Err()
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.Code = ee.ExitCode()
			return res, nil
		}
		return res, err
	}
	return res, nil
}

// ---- Sanitization of arguments coming from the client ----

// refRe validates ref/branch names. It refuses an empty string, a leading
// "-" char (which stops it from turning into a flag), and allows only the
// safe subset of git branch-name chars. ".." is refused separately (range/traversal).
var refRe = regexp.MustCompile(`^[A-Za-z0-9._/][A-Za-z0-9._/-]*$`)

func validRef(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	if strings.Contains(s, "..") {
		return false
	}
	return refRe.MatchString(s)
}

// validRelPath validates a relative path inside the repo (for diff/write).
// It refuses absolute paths, ".." traversal and NUL. It is always passed
// AFTER "--" in the argv so that it can never be read as a flag, and the
// write resolves the real path against the repo root (jail) as a second barrier.
func validRelPath(p string) bool {
	if p == "" || len(p) > 4096 {
		return false
	}
	if strings.ContainsRune(p, 0) {
		return false
	}
	if filepath.IsAbs(p) {
		return false
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return false
	}
	return true
}

// clampLimit pins the graph's commit count to [1,2000]. Default 200 when it
// is absent/invalid.
func clampLimit(n, def int) int {
	if n <= 0 {
		return def
	}
	if n > 2000 {
		return 2000
	}
	return n
}

// ---- Write locking ----

// repoMu serializes concurrent writes from the SAME process by repoPath. The
// flock covers inter-process; this mutex covers intra-app (two simultaneous
// requests from the primary). It does not protect against an external git —
// that is what the flock + the index.lock check are for.
var (
	repoMuMap = map[string]*sync.Mutex{}
	repoMuLk  sync.Mutex
)

func repoMutex(repoPath string) *sync.Mutex {
	repoMuLk.Lock()
	defer repoMuLk.Unlock()
	m, ok := repoMuMap[repoPath]
	if !ok {
		m = &sync.Mutex{}
		repoMuMap[repoPath] = m
	}
	return m
}

// resolveGitDir returns the repo's real .git directory. It accepts a ".git"
// directory (a normal repo) OR a ".git" file (a worktree: it contains
// "gitdir: <path>"). Used to locate index.lock and the dedicated lock.
func resolveGitDir(repoPath string) (string, error) {
	dot := filepath.Join(repoPath, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return dot, nil
	}
	// .git is a file (worktree): "gitdir: /real/path"
	b, err := os.ReadFile(dot)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(b))
	const pfx = "gitdir:"
	if !strings.HasPrefix(line, pfx) {
		return "", errors.New("git: .git file without a gitdir directive")
	}
	gd := strings.TrimSpace(strings.TrimPrefix(line, pfx))
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(repoPath, gd)
	}
	return gd, nil
}

// withRepoWriteLock serializes a write: it takes the intra-process mutex,
// refuses (errLocked) when an index.lock already exists in the repo, and takes
// a non-blocking exclusive flock on the dedicated .git/vpsm-git.lock. All of
// that goes away when fn returns. Any contention becomes errLocked → a clean 409.
func withRepoWriteLock(repoPath string, fn func() error) error {
	mu := repoMutex(repoPath)
	mu.Lock()
	defer mu.Unlock()

	gitDir, err := resolveGitDir(repoPath)
	if err != nil {
		return err
	}
	// Another git (CLI, deploy, another session) holding the index locked?
	if _, err := os.Stat(filepath.Join(gitDir, "index.lock")); err == nil {
		return errLocked
	}
	lockPath := filepath.Join(gitDir, "vpsm-git.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errLocked
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
