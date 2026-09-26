// spawn.go — process spawners for the AI workflows (Claude / Jira-AI).
//
// Each Spawn* creates a DETACHED session (no client) running the desired CLI,
// sandboxed in user.slice (systemd-run --scope) — inheriting the server's env BUT
// not inheriting the caller's pty. The websocket client reattaches through the
// normal HostShell to interact. Creation goes through the active backend (dtach)
// via the Session* dispatchers (session.go), so it works on either engine.
//
// Migration note: the argv is now SPLIT (["claude","--resume",uuid]) instead of
// a single shell-split string — safer (anti-injection) and required by dtach,
// which execs the command directly (no shell).
package pty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"server-control-panel/internal/aimodel"
	"server-control-panel/internal/claudebin"
)

// validatedModel trims and allowlist-checks a model id for the interactive tier
// (anti-injection guard) — `model` can originate from an HTTP request,
// so it MUST be constrained before use. Returns ("", nil) for empty (inherit the
// process default), (model, nil) for an allowed value, or an error for a
// non-empty value outside the allowlist (surfaced, not silently dropped).
func validatedModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", nil
	}
	if !aimodel.Allowed(model) {
		return "", errors.New("invalid model: " + model)
	}
	return model, nil
}

// SpawnClaudeSession creates a detached session that runs `claude` — optionally
// resuming a previous conversation via `--resume <uuid>`. The session is
// detached so the websocket client can attach later via the normal HostShell
// flow. Returns the sanitised session name on success.
//
// The new session inherits the system env, including whatever ANTHROPIC_BASE_URL
// settings.json provides to Claude Code. Routing between OAuth/Proxy is governed
// centrally by the claude-router. claudeConfigDir pins the "fork"
// consumer's account via CLAUDE_CONFIG_DIR; "" inherits the default.
//
// model selects the tier; "" inherits the process default (Opus).
func SpawnClaudeSession(sessionName, resumeUUID, claudeConfigDir, model string) (string, error) {
	sessionName = safeSessionName(sessionName)
	if sessionName == "" {
		return "", errors.New("invalid session name")
	}
	argv := []string{claudebin.Path()}
	if resumeUUID != "" {
		if !isValidSessionUUID(resumeUUID) {
			return "", errors.New("invalid resume uuid")
		}
		argv = append(argv, "--resume", resumeUUID)
	}
	if m, err := validatedModel(model); err != nil {
		return "", err
	} else if m != "" {
		argv = append(argv, "--model", m)
	}
	// VPSM Wave-3 #53: forward the session's stored permission mode (if any).
	argv = append(argv, permModeArgs(sessionName)...)
	if err := SessionCreateDetached(sessionName, argv, claudeConfigEnv(claudeConfigDir), ""); err != nil {
		return "", err
	}
	return sessionName, nil
}

// SpawnLoginShell creates a detached login shell for a Claude account so the
// operator can run `claude` → /login (or `claude setup-token`) to (re)mint that
// account's credential — all subsequent `claude` runs in the pane use it.
//
// claudeConfigDir selects the account layout:
//   - non-empty → pin CLAUDE_CONFIG_DIR to that dir (provisioned accounts).
//   - ""        → the DEFAULT account ("jordan"): HOME mode ($HOME/.claude). We
//     must NOT set CLAUDE_CONFIG_DIR (it flips Claude Code into config-dir
//     layout); we also strip any inherited one (via `env -u`) so the login
//     reliably targets /root/.claude regardless of the server's environment.
func SpawnLoginShell(sessionName, claudeConfigDir string) (string, error) {
	sessionName = safeSessionName(sessionName)
	if sessionName == "" {
		return "", errors.New("invalid session name")
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	if claudeConfigDir != "" {
		return sessionName, SessionCreateDetached(sessionName, []string{shell, "-l"}, claudeConfigEnv(claudeConfigDir), "")
	}
	// Default account: HOME mode. Strip any leaked CLAUDE_CONFIG_DIR. Feito via
	// `env -u CLAUDE_CONFIG_DIR` no argv — o dtach exec o comando direto.
	return sessionName, SessionCreateDetached(sessionName, []string{"env", "-u", "CLAUDE_CONFIG_DIR", shell, "-l"}, claudeConfigEnv(""), "")
}

// sessionTerm is the TERM of every spawned session — the SAME value the ordinary
// terminal's client injects (pty.go, HostShell). See claudeConfigEnv.
const sessionTerm = "xterm-256color"

// claudeConfigEnv returns the session env: it pins CLAUDE_CONFIG_DIR
// when dir != "", stitches the `claude` CLI directory into PATH and declares
// TERM. dtach maps it to `--setenv`.
//
// PATH matters even with an already absolute argv (claudebin.Path): Claude Code
// itself calls `claude` by name in hooks, the statusline and subagents, and
// systemd's PATH does not include ~/.local/bin. Without this the session comes up but those
// internal paths fail silently.
//
// TERM is the SAME trap all over again (see the PATH note above): a systemd
// service's env has no TERM, and what fixes that on ordinary terminals is the
// SHELL — `bash -l` sources /root/.bashrc, which does
// `case "$TERM" in ""|dumb) export TERM=xterm-256color`. The spawners here
// exec the CLI DIRECTLY (dtach → claude, no shell in between), so no
// profile runs and TERM arrives EMPTY. With an empty TERM, Claude Code's
// supports-color resolves level 0 and the pane comes up MONOCHROME — that was the symptom of
// the Jira "Trabalhar agora" button, black-and-white while the ordinary terminal (born
// from `bash -l`) was in colour. Declaring it here is the fix at the root: it covers
// all four spawners (work/jira, fork, restart, login) at once.
func claudeConfigEnv(dir string) []string {
	env := []string{"TERM=" + sessionTerm}
	if dir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+dir)
	}
	if p := claudebin.PathEnv(); p != "" {
		env = append(env, "PATH="+p)
	}
	return env
}

// SpawnClaudeWorkSession creates a detached session running `claude` with
// cwd=repoPath (so all tool calls operate there) and, when initialPrompt is
// non-empty, injects it as the first prompt via bracketed paste — the assistant
// starts already knowing the context. This is the shared "work session" spawn
// path reused by the Jira flow (SpawnJiraWorkSession), the queue's scheduled
// agent routines (kind agent_routine) and `vpsmctl agent-fixbuild`.
//
// model selects the tier; "" inherits the process default (Opus).
// The session's stored permission mode, if any, is forwarded.
func SpawnClaudeWorkSession(sessionName, repoPath, initialPrompt, claudeConfigDir, model string) (string, error) {
	sessionName = safeSessionName(sessionName)
	if sessionName == "" {
		return "", errors.New("invalid session name")
	}
	m, err := validatedModel(model)
	if err != nil {
		return "", err
	}
	// 1. Create detached session with cwd=repo, claude as the only window.
	argv := []string{claudebin.Path()}
	if m != "" {
		argv = append(argv, "--model", m)
	}
	argv = append(argv, permModeArgs(sessionName)...)

	// 2. The prompt goes as a POSITIONAL ARGUMENT (`claude [options] [prompt]`),
	// delivered by the exec in the spawn itself.
	//
	// It used to be pasted (bracketed paste) after a fixed sleep, betting that the
	// TUI would already be accepting input. When Claude Code takes longer to come
	// up — typically while loading MCP servers — the paste fell into the void and
	// the session stayed alive, with claude running and the input box EMPTY: that
	// was the bug of the "Work on it now" button, which created the session without
	// starting the work. As an argument there is no race left to lose.
	prompt := strings.TrimSpace(initialPrompt)
	viaArgv := prompt != "" && len(prompt) <= maxArgvPrompt
	if viaArgv {
		argv = append(argv, prompt)
	}

	if err := SessionCreateDetached(sessionName, argv, claudeConfigEnv(claudeConfigDir), repoPath); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	if prompt == "" || viaArgv {
		return sessionName, nil
	}

	// 3. A fallback only for a giant prompt that does not fit in argv (ARG_MAX). It
	// keeps the old paste — with the same timing fragility, but that beats failing
	// the spawn, and in practice no ticket prompt comes anywhere near this.
	waitClaudeReady(sessionName, 20*time.Second)
	time.Sleep(600 * time.Millisecond)
	if err := SessionPasteAndEnter(sessionName, prompt); err != nil {
		return sessionName, fmt.Errorf("paste prompt: %w", err)
	}
	return sessionName, nil
}

// maxArgvPrompt: a conservative ceiling for a prompt carried in argv. The
// kernel's per-argument limit (MAX_ARG_STRLEN) is 128KB; 96KB leaves room for
// the rest of the argv.
const maxArgvPrompt = 96 * 1024

// SpawnJiraWorkSession creates a detached session dedicated to working on a
// specific Jira ticket. Thin wrapper over SpawnClaudeWorkSession kept for the
// Jira call site (and its clearer name at that site).
//
// model selects the tier; "" inherits the process default (Opus).
func SpawnJiraWorkSession(sessionName, repoPath, initialPrompt, claudeConfigDir, model string) (string, error) {
	return SpawnClaudeWorkSession(sessionName, repoPath, initialPrompt, claudeConfigDir, model)
}

// waitClaudeReady gives Claude Code's TUI some slack to become ready for input
// before the prompt is pasted. The session is created DETACHED (no client
// attached), so there is no pty log to poll for readiness (the tee only runs with
// a client) — it falls back to a short fixed sleep and moves on; the paste
// happens afterwards either way.
func waitClaudeReady(sessionName string, timeout time.Duration) bool {
	time.Sleep(3 * time.Second)
	return false
}

// writeTmpFile creates a 0600 file in /tmp with the given content prefix.
// Returns its absolute path. Caller is responsible for os.Remove.
func writeTmpFile(prefix, content string) (string, error) {
	f, err := os.CreateTemp("", prefix+"*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// RestartClaudeSession kills a session (if it exists) and recreates it running
// `claude --continue`. Any websocket client attached to the old session is
// dropped and must reconnect — the conversation history is restored by Claude
// Code's --continue from its JSONL.
//
// model selects the tier; "" inherits the process default (Opus).
func RestartClaudeSession(sessionName, claudeConfigDir, model string) error {
	sessionName = safeSessionName(sessionName)
	if sessionName == "" {
		return errors.New("invalid session name")
	}
	argv := []string{claudebin.Path(), "--continue"}
	if m, err := validatedModel(model); err != nil {
		return err
	} else if m != "" {
		argv = append(argv, "--model", m)
	}
	argv = append(argv, permModeArgs(sessionName)...)
	// Idempotent: kill errors when missing, which is fine. CreateDetached is
	// attach-or-create, so kill-first guarantees a FRESH pane running --continue.
	_ = SessionKill(sessionName)
	time.Sleep(200 * time.Millisecond)
	return SessionCreateDetached(sessionName, argv, claudeConfigEnv(claudeConfigDir), "")
}

// isValidSessionUUID validates the 8-4-4-4-12 hex shape used by Claude Code's
// JSONL filenames. Keeps shell metachars out of the --resume argument.
func isValidSessionUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// Exec runs a one-shot command and returns its output (stdout+stderr merged).
func Exec(cmd string) (string, error) {
	c := exec.Command("/bin/bash", "-lc", cmd)
	out, err := c.CombinedOutput()
	if err != nil {
		if strings.TrimSpace(string(out)) == "" {
			return err.Error(), err
		}
		return string(out), err
	}
	return string(out), nil
}
