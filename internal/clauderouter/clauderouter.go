// Package clauderouter provides a thin client over the claude-router service:
// reads/writes the on-disk mode flag, hits /healthz, and orchestrates the
// "panic reset" flow that restores a safe OAuth-only baseline.
package clauderouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// the app is consolidated to Claude-only — only oauth mode exists.
	ModeOAuth = "oauth"

	defaultStateFile     = "/var/lib/claude-router/mode"
	defaultHealthURL     = "http://127.0.0.1:8788/healthz"
	defaultServiceName   = "claude-router"
	defaultSettingsPath  = "/root/.claude/settings.json"
	defaultRouterEnvFile = "/etc/claude-router/env"
	systemctl            = "/usr/bin/systemctl"
	probeTimeout         = 3 * time.Second
	panicRestartSettle   = 800 * time.Millisecond
)

type Client struct {
	StateFile     string
	HealthURL     string
	ServiceName   string
	SettingsPath  string
	RouterEnvFile string
	httpc         *http.Client
}

func New() *Client {
	return &Client{
		StateFile:     defaultStateFile,
		HealthURL:     defaultHealthURL,
		ServiceName:   defaultServiceName,
		SettingsPath:  defaultSettingsPath,
		RouterEnvFile: defaultRouterEnvFile,
		httpc:         &http.Client{Timeout: probeTimeout},
	}
}

// ReadMode returns the current router mode from the state file. Any read
// error or unrecognized content yields oauth (the safe default).
func (c *Client) ReadMode() string {
	data, err := os.ReadFile(c.StateFile)
	if err != nil {
		return ModeOAuth
	}
	m := strings.TrimSpace(string(data))
	if !isValidMode(m) {
		return ModeOAuth
	}
	return m
}

func isValidMode(m string) bool {
	return m == ModeOAuth
}

// WriteMode atomically swaps the state file. The router watches the file via
// a simple per-request re-read, so the change takes effect on the next
// upstream call without a restart.
//
// The file is written world-readable (0644): we run as root, but the router
// runs as the `clauderouter` user and silently falls back to oauth on EACCES,
// which made the toggle look like a no-op (see post-mortem 2026-05-25).
func (c *Client) WriteMode(mode string) error {
	if !isValidMode(mode) {
		return fmt.Errorf("invalid mode %q (only oauth is supported)", mode)
	}
	if err := os.MkdirAll(filepath.Dir(c.StateFile), 0o755); err != nil {
		return err
	}
	tmp := c.StateFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(mode+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.StateFile); err != nil {
		return err
	}
	// Defensive: Rename preserves the destination's mode/owner on some
	// filesystems but not when the destination didn't exist. Force-chmod so
	// repeat writes always end up world-readable.
	_ = os.Chmod(c.StateFile, 0o644)
	return nil
}

type Health struct {
	Status        string `json:"status"`
	Mode          string `json:"mode"`
	Version       string `json:"version"`
	OAuthUpstream string `json:"oauth_upstream"`
	TS            string `json:"ts"`
	LatencyMS     int64  `json:"latency_ms"`
	Reachable     bool   `json:"reachable"`
}

// Healthz pings the router's /healthz. Always returns a Health struct, with
// Reachable=false when the router isn't responding.
func (c *Client) Healthz() Health {
	start := time.Now()
	h := Health{Mode: c.ReadMode(), Status: "unknown"}
	resp, err := c.httpc.Get(c.HealthURL)
	h.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		return h
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var live Health
	if jerr := json.Unmarshal(body, &live); jerr == nil {
		live.LatencyMS = h.LatencyMS
		live.Reachable = resp.StatusCode == 200
		return live
	}
	h.Reachable = resp.StatusCode == 200
	return h
}

// EnvIntegrity proactively verifies that RouterEnvFile — the env file the router
// reads at boot (typically /etc/claude-router/env, a symlink into
// users/<primary>.env) — resolves to a REAL, readable regular file holding a
// non-empty BRIDGE_API_KEY. Returns "ok" when healthy, otherwise a
// "broken: <reason>" string. The key VALUE is never read out, returned, or
// logged. Guards the restart landmine: the running router keeps the key cached
// in memory, so a circular/dangling symlink only bites on the NEXT restart --
// surfacing it here lets us alert while the old process still serves.
func (c *Client) EnvIntegrity() string {
	path := c.RouterEnvFile
	if path == "" {
		path = defaultRouterEnvFile
	}
	// Walk the symlink chain by hand so we can tell a circular loop from a
	// dangling target without leaning on OS-specific errno values (ELOOP).
	seen := map[string]struct{}{}
	cur := path
	for i := 0; i < 40; i++ {
		fi, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Absent on the FIRST hop means the env file was never
				// created: this installation does not use the router. That is
				// a configuration state, not a fault, and reporting it as
				// "broken" buries the fault this function exists to catch.
				//
				// Absent on a LATER hop is the real thing -- a symlink whose
				// target is gone. The running process still serves from its
				// cached key, so this only bites on the next restart, which is
				// exactly why it is surfaced here.
				if i == 0 {
					return "not_configured"
				}
				return "broken: dangling symlink"
			}
			return "broken: " + err.Error()
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			if !fi.Mode().IsRegular() {
				return "broken: not a regular file"
			}
			break
		}
		if _, dup := seen[cur]; dup {
			return "broken: circular symlink"
		}
		seen[cur] = struct{}{}
		target, err := os.Readlink(cur)
		if err != nil {
			return "broken: " + err.Error()
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		cur = target
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "broken: unreadable (" + err.Error() + ")"
	}
	if !hasNonEmptyKey(data, "BRIDGE_API_KEY") {
		return "broken: BRIDGE_API_KEY missing or empty"
	}
	return "ok"
}

// hasNonEmptyKey reports whether env-file content assigns a non-empty value to
// key (e.g. BRIDGE_API_KEY=xxx). Tolerates a leading "export " and surrounding
// quotes. The value itself is never returned.
func hasNonEmptyKey(data []byte, key string) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "export ")
		if !strings.HasPrefix(line, key+"=") {
			continue
		}
		val := strings.TrimSpace(line[len(key)+1:])
		val = strings.Trim(val, `"'`)
		if val != "" {
			return true
		}
	}
	return false
}

// Restart runs `systemctl restart claude-router`. Caller must have privilege.
func (c *Client) Restart() error {
	if _, err := os.Stat(systemctl); err != nil {
		return fmt.Errorf("systemctl unavailable: %w", err)
	}
	out, err := exec.Command(systemctl, "restart", c.ServiceName).CombinedOutput()
	if err != nil {
		snippet := strings.TrimSpace(string(out))
		if snippet != "" {
			return fmt.Errorf("restart %s (%s): %w", c.ServiceName, snippet, err)
		}
		return fmt.Errorf("restart %s: %w", c.ServiceName, err)
	}
	return nil
}

// PanicResult captures every step of the panic recovery for the audit log.
type PanicResult struct {
	OK          bool     `json:"ok"`
	Mode        string   `json:"mode"`
	SettingsBak string   `json:"settings_backup,omitempty"`
	Steps       []string `json:"steps"`
	Errors      []string `json:"errors,omitempty"`
}

// Panic forces a clean OAuth baseline:
//  1. Backup ~/.claude/settings.json
//  2. Strip any "env" block (defensive — env vars are what broke things last time)
//  3. State file → oauth
//  4. Restart claude-router
//  5. Verify /healthz returns mode=oauth
//
// Returns OK=true only when every step succeeded. Always returns the Steps log
// so the UI can show what happened.
func (c *Client) Panic() PanicResult {
	res := PanicResult{Steps: []string{}}

	// Step 1+2: backup + strip env from settings.json
	if bak, err := c.cleanSettings(); err != nil {
		res.Errors = append(res.Errors, "settings: "+err.Error())
	} else {
		res.SettingsBak = bak
		res.Steps = append(res.Steps, "settings.json cleaned (backup: "+filepath.Base(bak)+")")
	}

	// Step 3: state file → oauth
	if err := c.WriteMode(ModeOAuth); err != nil {
		res.Errors = append(res.Errors, "state: "+err.Error())
	} else {
		res.Steps = append(res.Steps, "state file → oauth")
	}

	// Step 4: restart router (defensive — also ensures any cached state is dropped)
	if err := c.Restart(); err != nil {
		res.Errors = append(res.Errors, "restart: "+err.Error())
	} else {
		res.Steps = append(res.Steps, "systemctl restart claude-router")
	}

	// Step 5: verify
	time.Sleep(panicRestartSettle)
	h := c.Healthz()
	res.Mode = h.Mode
	if !h.Reachable {
		res.Errors = append(res.Errors, "healthz: router unreachable after restart")
	} else if h.Mode != ModeOAuth {
		res.Errors = append(res.Errors, "healthz: mode is "+h.Mode+" (expected oauth)")
	} else {
		res.Steps = append(res.Steps, "healthz ok, mode=oauth")
	}

	res.OK = len(res.Errors) == 0
	return res
}

// cleanSettings rewrites settings.json removing the "env" block while
// preserving every other top-level key. Creates a timestamped backup first.
// Returns the backup path. If the file is missing, returns no-op success.
func (c *Client) cleanSettings() (string, error) {
	raw, err := os.ReadFile(c.SettingsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var current map[string]any
	if err := json.Unmarshal(raw, &current); err != nil {
		return "", fmt.Errorf("parse settings: %w", err)
	}
	// Backup first
	bak := c.SettingsPath + ".bak." + fmt.Sprintf("%d", time.Now().Unix())
	if err := os.WriteFile(bak, raw, 0o600); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	// Strip ANTHROPIC_API_KEY/ANTHROPIC_AUTH_TOKEN from env but KEEP ANTHROPIC_BASE_URL
	// (the router *needs* settings.json to point at it; removing breaks the design).
	if envBlock, ok := current["env"].(map[string]any); ok {
		delete(envBlock, "ANTHROPIC_API_KEY")
		delete(envBlock, "ANTHROPIC_AUTH_TOKEN")
		if len(envBlock) == 0 {
			delete(current, "env")
		} else {
			current["env"] = envBlock
		}
	}
	out, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return bak, err
	}
	tmp := c.SettingsPath + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return bak, err
	}
	return bak, os.Rename(tmp, c.SettingsPath)
}
