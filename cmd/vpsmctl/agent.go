package main

// agent.go — agent-action subcommands. These are the integration
// surface the cockpit shells out to (/opt/panel/bin/vpsmctl <subcmd>):
//
//	agent-ship     — ticket→PR "ship": verify, commit, push, open PR (or print compare URL)
//	agent-fixbuild — run the build; on failure spawn a Claude session to fix it
//	agent-budget   — show/set alert-only spend ceilings
//	agent-permmode — set a session's Claude permission mode
//
// Anti-injection: ticket/user/session/branch values are ALWAYS passed
// as discrete argv elements to git/gh (never shell-concatenated). Only the
// operator-supplied verify/build command is run through a shell (bash -lc), same
// posture as internal/pty.Exec.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"server-control-panel/internal/config"
	ptysvc "server-control-panel/internal/pty"
)

// ─── shared helpers ─────────────────────────────────────────────────────────

// sessionCWD resolves a dtach session name → its recorded cwd via
// <DataDir>/session-cwd.json (the same sidecar the server writes at spawn).
// Errors clearly when the session has no mapping.
func sessionCWD(dataDir, session string) (string, error) {
	path := filepath.Join(dataDir, "session-cwd.json")
	m := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("no cwd map (%s): %w — was the session created by vps-manager?", path, err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("cwd map corrupted (%s): %w", path, err)
	}
	if cwd := m[session]; cwd != "" {
		return cwd, nil
	}
	if cwd := m[ptysvc.SafeSessionName(session)]; cwd != "" {
		return cwd, nil
	}
	return "", fmt.Errorf("session %q has no cwd in %s — cannot locate the worktree", session, path)
}

// writeSessionCWD best-effort records name→cwd in the sidecar (used after
// spawning a fix session). Whole-map rewrite, atomic temp+rename.
func writeSessionCWD(dataDir, name, cwd string) error {
	path := filepath.Join(dataDir, "session-cwd.json")
	m := map[string]string{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &m)
		if m == nil {
			m = map[string]string{}
		}
	}
	m[name] = cwd
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// git runs `git -C dir <args...>` (argv-exec) and returns trimmed combined output.
func git(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// gitInsideWorkTree reports whether dir is inside a git work tree.
func gitInsideWorkTree(dir string) bool {
	out, err := git(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// runShellCapture runs an operator-supplied command line in dir via bash -lc and
// returns combined output + error. The command is operator config (a verify or
// build line), NOT ticket/user/session input.
func runShellCapture(dir, cmdline string) (string, error) {
	cmd := exec.Command("/bin/bash", "-lc", cmdline)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ticketFromSession extracts the ticket key from a "vpsm-<user>-jira-<KEY>"
// session name. Returns "" when the name doesn't match that shape.
func ticketFromSession(session string) string {
	rest, ok := strings.CutPrefix(session, "vpsm-")
	if !ok {
		return ""
	}
	i := strings.Index(rest, "-jira-")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(rest[i+len("-jira-"):])
}

// remoteToCompareURL turns an origin remote URL into a GitHub-style compare URL
// for branch. Handles https and ssh (scp-like) forms; returns "" when it can't.
func remoteToCompareURL(remote, branch string) string {
	remote = strings.TrimSpace(remote)
	remote = strings.TrimSuffix(remote, ".git")
	var base string
	switch {
	case strings.HasPrefix(remote, "https://"):
		base = remote
	case strings.HasPrefix(remote, "git@"):
		// git@github.com:owner/repo → https://github.com/owner/repo
		if i := strings.Index(remote, ":"); i > 0 {
			host := strings.TrimPrefix(remote[:i], "git@")
			base = "https://" + host + "/" + remote[i+1:]
		}
	case strings.HasPrefix(remote, "ssh://"):
		base = "https://" + strings.TrimPrefix(remote, "ssh://")
		base = strings.Replace(base, "git@", "", 1)
	}
	if base == "" {
		return ""
	}
	return base + "/compare/" + branch + "?expand=1"
}

// ghAvailable reports whether the gh CLI is installed AND authenticated.
func ghAvailable() bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	cmd := exec.Command("gh", "auth", "status")
	return cmd.Run() == nil
}

// ─── agent-ship ─────────────────────────────────────────────────────────────

func cmdAgentShip(args []string) error {
	fs := flag.NewFlagSet("agent-ship", flag.ContinueOnError)
	session := fs.String("session", "", "dtach session name (required)")
	user := fs.String("user", "", "user label (optional)")
	title := fs.String("title", "", "commit/PR title (default: 'agent: ship <ticket>')")
	noPR := fs.Bool("no-pr", false, "do not open a PR even when gh is available")
	verify := fs.String("verify", "", "verification command (default: go build ./... when it is a Go module)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = *user
	if *session == "" {
		return fmt.Errorf("usage: vpsmctl agent-ship --session <name> [--user U] [--title T] [--no-pr] [--verify CMD]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cwd, err := sessionCWD(cfg.DataDir, *session)
	if err != nil {
		return err
	}
	if !gitInsideWorkTree(cwd) {
		return fmt.Errorf("%s is not a git repository — nothing to ship", cwd)
	}
	branch, err := git(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("git branch: %s", branch)
	}
	if branch == "" || branch == "HEAD" {
		return fmt.Errorf("detached HEAD at %s — check out a branch before shipping", cwd)
	}
	ticket := ticketFromSession(*session)

	// 1. Verify (abort on failure — never ship broken code).
	if err := runShipVerify(cwd, *verify); err != nil {
		return err
	}

	// 2. Stage + commit (skip commit when nothing is staged; still push).
	if _, err := git(cwd, "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if _, staged := git(cwd, "diff", "--cached", "--quiet"); staged != nil {
		// non-nil error from --quiet means there ARE staged changes → commit.
		msg := strings.TrimSpace(*title)
		if msg == "" {
			if ticket != "" {
				msg = "agent: ship " + ticket
			} else {
				msg = "agent: ship work from " + *session
			}
		}
		if out, err := git(cwd, "commit", "-m", msg); err != nil {
			return fmt.Errorf("git commit failed: %s", out)
		}
		fmt.Printf("commit: %s\n", msg)
	} else {
		fmt.Println("nothing new to commit — publishing the branch as it stands")
	}

	// 3. Push (never force). -u sets upstream on first push; re-runs push new commits.
	if out, err := git(cwd, "push", "-u", "origin", branch); err != nil {
		return fmt.Errorf("git push failed: %s", out)
	}
	fmt.Printf("branch published: %s\n", branch)

	// 4. PR via gh (if available+authed and not suppressed), else compare URL.
	if !*noPR && ghAvailable() {
		prTitle := strings.TrimSpace(*title)
		if prTitle == "" {
			if ticket != "" {
				prTitle = "Ship " + ticket
			} else {
				prTitle = "Agent ship: " + branch
			}
		}
		body := "Automated ship"
		if ticket != "" {
			body += " for " + ticket
		}
		body += " (session " + *session + ", branch " + branch + ")."
		cmd := exec.Command("gh", "pr", "create", "--fill", "--title", prTitle, "--body", body, "--head", branch)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		fmt.Print(string(out))
		if err != nil {
			// gh failed (e.g. PR already exists) — fall through to compare URL.
			fmt.Fprintln(os.Stderr, "warning: gh pr create failed; printing the compare URL")
		} else {
			return nil
		}
	}
	if remote, err := git(cwd, "remote", "get-url", "origin"); err == nil {
		if url := remoteToCompareURL(remote, branch); url != "" {
			fmt.Printf("open the PR at: %s\n", url)
		}
	}
	return nil
}

// runShipVerify runs the configurable verify command. When --verify is empty it
// defaults to `go build ./...` for a Go module and is skipped otherwise.
func runShipVerify(cwd, verify string) error {
	cmdline := strings.TrimSpace(verify)
	if cmdline == "" {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err != nil {
			fmt.Println("verify: no go.mod — skipped")
			return nil
		}
		cmdline = "go build ./..."
	}
	fmt.Printf("verify: %s\n", cmdline)
	if out, err := runShellCapture(cwd, cmdline); err != nil {
		return fmt.Errorf("verify FAILED — aborting ship (broken code will not be published):\n%s", strings.TrimSpace(out))
	}
	fmt.Println("verify: OK")
	return nil
}

// ─── agent-fixbuild ─────────────────────────────────────────────────────────

func cmdAgentFixbuild(args []string) error {
	fs := flag.NewFlagSet("agent-fixbuild", flag.ContinueOnError)
	session := fs.String("session", "", "dtach session name (required)")
	buildCmd := fs.String("cmd", "", "build command (default: go build ./...)")
	user := fs.String("user", "", "user label (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = *user
	if *session == "" {
		return fmt.Errorf("usage: vpsmctl agent-fixbuild --session <name> [--cmd CMD] [--user U]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cwd, err := sessionCWD(cfg.DataDir, *session)
	if err != nil {
		return err
	}
	cmdline := strings.TrimSpace(*buildCmd)
	if cmdline == "" {
		cmdline = "go build ./..."
	}
	fmt.Printf("build: %s (in %s)\n", cmdline, cwd)
	out, berr := runShellCapture(cwd, cmdline)
	if berr == nil {
		fmt.Println("build OK, nothing to fix")
		return nil
	}
	// Build failed → spawn a NEW Claude session in the worktree to fix it.
	reg, _ := ptysvc.LoadRegistry(filepath.Join(cfg.DataDir, "session-registry.json"))
	ptysvc.InitSessionBackend(cfg.DataDir, reg)

	fixName := ptysvc.SafeSessionName(*session + "-fix")
	prompt := "the build failed, fix it:\n" + strings.TrimSpace(out)
	created, err := ptysvc.SpawnClaudeWorkSession(fixName, cwd, prompt, "", "")
	if err != nil {
		return fmt.Errorf("spawn fix session: %w", err)
	}
	_ = writeSessionCWD(cfg.DataDir, created, cwd) // best-effort telemetry mapping
	fmt.Fprintln(os.Stderr, "build failed — fix session started:")
	fmt.Println(created) // session name on stdout (contract for the caller)
	return nil
}

// ─── agent-budget ───────────────────────────────────────────────────────────

// agentBudgetFile mirrors api.AgentBudget's JSON contract (same file).
type agentBudgetFile struct {
	DailyUSD   float64 `json:"daily_usd,omitempty"`
	MonthlyUSD float64 `json:"monthly_usd,omitempty"`
	SessionUSD float64 `json:"session_usd,omitempty"`
}

func cmdAgentBudget(args []string) error {
	fs := flag.NewFlagSet("agent-budget", flag.ContinueOnError)
	show := fs.Bool("show", false, "show the current caps")
	setDaily := fs.Float64("set-daily", -1, "daily cap USD (0 = off)")
	setMonthly := fs.Float64("set-monthly", -1, "monthly cap USD (0 = off)")
	setSession := fs.Float64("set-session", -1, "per-session cap USD (0 = off)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_ = *show
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	path := filepath.Join(cfg.DataDir, "agent-budget.json")
	var b agentBudgetFile
	if data, rerr := os.ReadFile(path); rerr == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &b)
	}
	changed := false
	if *setDaily >= 0 {
		b.DailyUSD = *setDaily
		changed = true
	}
	if *setMonthly >= 0 {
		b.MonthlyUSD = *setMonthly
		changed = true
	}
	if *setSession >= 0 {
		b.SessionUSD = *setSession
		changed = true
	}
	if changed {
		data, merr := json.MarshalIndent(b, "", "  ")
		if merr != nil {
			return merr
		}
		if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
		fmt.Println("caps updated.")
	}
	fmt.Println("Agent spend caps (ALERT ONLY — no session is ever interrupted):")
	fmt.Printf("  daily:       %s\n", budgetLabel(b.DailyUSD))
	fmt.Printf("  monthly:     %s\n", budgetLabel(b.MonthlyUSD))
	fmt.Printf("  per session: %s\n", budgetLabel(b.SessionUSD))
	fmt.Println("(auto-halt is a FUTURE option; today the alert travels the notification spine.)")
	return nil
}

func budgetLabel(v float64) string {
	if v <= 0 {
		return "off"
	}
	return fmt.Sprintf("$%.2f", v)
}

// ─── agent-permmode ─────────────────────────────────────────────────────────

func cmdAgentPermmode(args []string) error {
	fs := flag.NewFlagSet("agent-permmode", flag.ContinueOnError)
	session := fs.String("session", "", "dtach session name (required)")
	mode := fs.String("mode", "", "plan | acceptEdits | default (empty = clear)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *session == "" {
		return fmt.Errorf("usage: vpsmctl agent-permmode --session <name> --mode <plan|acceptEdits|default>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	m := strings.TrimSpace(*mode)
	if err := ptysvc.SetSessionPermMode(cfg.DataDir, *session, m); err != nil {
		return err
	}
	if m == "" {
		fmt.Printf("permission mode cleared for %s (claude falls back to its own default)\n", *session)
	} else {
		fmt.Printf("permission mode for %s = %s (applied on the next spawn/restart)\n", *session, m)
	}
	return nil
}
