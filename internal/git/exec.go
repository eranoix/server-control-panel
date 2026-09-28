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

const execTimeout = 45 * time.Second

var errLocked = errors.New("git: repository busy (index locked)")

type runResult struct {
	Stdout string
	Stderr string
	Code   int
}

func run(ctx context.Context, repoPath string, args ...string) (runResult, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
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
			return res, nil
		}
		return res, err
	}
	return res, nil
}

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

func clampLimit(n, def int) int {
	if n <= 0 {
		return def
	}
	if n > 2000 {
		return 2000
	}
	return n
}

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

func resolveGitDir(repoPath string) (string, error) {
	dot := filepath.Join(repoPath, ".git")
	fi, err := os.Stat(dot)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
		return dot, nil
	}
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

func withRepoWriteLock(repoPath string, fn func() error) error {
	mu := repoMutex(repoPath)
	mu.Lock()
	defer mu.Unlock()

	gitDir, err := resolveGitDir(repoPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(gitDir, "index.lock")); err == nil {
		return errLocked
	}
	lockPath := filepath.Join(gitDir, "panel-git.lock")
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
