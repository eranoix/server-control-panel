package api

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var worktreeSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

func ticketSlug(key string) string {
	s := strings.ToLower(strings.TrimSpace(key))
	s = worktreeSlugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s
}

func gitCmdEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
	)
}

func runGitOK(ctx context.Context, repoPath string, args ...string) bool {
	full := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = gitCmdEnv()
	return cmd.Run() == nil
}

func isGitRepo(ctx context.Context, repoPath string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--is-inside-work-tree")
	cmd.Env = gitCmdEnv()
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func worktreeRegistered(ctx context.Context, repoPath, wt string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "list", "--porcelain")
	cmd.Env = gitCmdEnv()
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	want := filepath.Clean(wt)
	for _, ln := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(ln, "worktree ") {
			if filepath.Clean(strings.TrimPrefix(ln, "worktree ")) == want {
				return true
			}
		}
	}
	return false
}

func ensureTicketWorktree(ctx context.Context, repoPath, key string) (string, bool) {
	if repoPath == "" || !isGitRepo(ctx, repoPath) {
		return repoPath, false
	}
	slug := ticketSlug(key)
	if slug == "" {
		return repoPath, false
	}
	wt := filepath.Join(repoPath, ".claude", "worktrees", "agent-"+slug)
	branch := "agent/" + slug

	if isDir(wt) {
		if worktreeRegistered(ctx, repoPath, wt) {
			return wt, true
		}
		return repoPath, false
	}

	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		return repoPath, false
	}

	if runGitOK(ctx, repoPath, "worktree", "add", "-b", branch, wt, "HEAD") {
		return wt, true
	}
	if runGitOK(ctx, repoPath, "worktree", "add", wt, branch) {
		return wt, true
	}
	uniq := branch + "-" + time.Now().UTC().Format("20060102-150405")
	if runGitOK(ctx, repoPath, "worktree", "add", "-b", uniq, wt, "HEAD") {
		return wt, true
	}
	return repoPath, false
}
