package api

// jira_worktree.go — VPSM agent-ops #2: worktree-per-agent for "Trabalhar agora".
//
// When the mapped repo path for a ticket is a git repo, the agent session runs
// in a DEDICATED git worktree on its own branch (agent/<ticket-slug>) instead of
// the shared checkout — so concurrent tickets never fight over the working tree
// or HEAD. Guarded and idempotent: non-git paths fall back to the shared path
// unchanged, and reattaching to an existing worktree reuses it in place.
//
// Anti-injection: git is always invoked with argv slices (never a
// shell) and the ticket key is reduced to a strict [a-z0-9-] slug before it
// reaches a branch name or filesystem path.

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

// ticketSlug reduces a Jira key to a collision-resistant, path/branch-safe slug.
func ticketSlug(key string) string {
	s := strings.ToLower(strings.TrimSpace(key))
	s = worktreeSlugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	return s
}

// gitCmdEnv is a minimal, non-interactive git environment (no pager, no
// credential/terminal prompts that could hang the spawn).
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

// worktreeRegistered reports whether wt is a worktree git currently knows about.
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

// ensureTicketWorktree returns the cwd the agent session should use for a ticket
// and whether a dedicated git worktree was set up. Guarded: only when repoPath
// is a git repo; otherwise returns (repoPath, false) unchanged. Idempotent:
// reuses an existing worktree/branch for the ticket on reattach; falls back to
// the shared repo on any git failure so "Trabalhar agora" never hard-breaks.
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

	// Idempotent reuse: an existing, git-registered worktree is used as-is.
	if isDir(wt) {
		if worktreeRegistered(ctx, repoPath, wt) {
			return wt, true
		}
		// Stale directory not tracked by git — don't clobber it; fall back.
		return repoPath, false
	}

	// git worktree add creates the leaf dir but not deep parents.
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		return repoPath, false
	}

	// Fresh branch off the current HEAD. If the branch already exists (a prior
	// run whose worktree was pruned), attach it instead of recreating it. As a
	// final guard against any residual collision, mint a timestamped branch.
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
