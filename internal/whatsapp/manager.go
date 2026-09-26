package whatsapp

// manager.go — multi-tenant WAHA orchestration. Every profile gets one
// *Service running against one WAHA Core container on a dedicated port
// handed out by the Registry. The Manager:
//
//   - resolves user -> Service (lazily created in ForUser)
//   - provisions the user's infrastructure (dirs, compose, .env, vault keys, systemctl)
//   - decommissions it (WhatsApp logout, stop container, archive state, release port)
//   - routes webhooks by path (/api/whatsapp/webhook/<user>) — HMAC is the user's own
//   - hands out a per-user WS Broadcaster (events NEVER cross profiles)
//
// Defence in depth: ports come from the Registry (file-locked), the HMAC
// secret from the user's vault namespace, the store under
// data/users/<user>/whatsapp, the container under
// /var/lib/vpsm-whatsapp/<user>. Every layer checks the user.
//
// Bootstrap: api.NewRouter calls NewManager(...) ONCE; the first profiles are
// lazily warmed at login (Manager.WarmUp). The systemctl enable done during
// Provision brings the unit up together with the host after a reboot.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
)

// composeTemplatePath is the on-disk template rendered per user. The path is
// absolute because the binary may run from systemd's CWD (root /).
const composeTemplatePath = "/opt/panel/scripts/whatsapp/docker-compose.tmpl.yml"

// ManagerOptions configures a Manager.
type ManagerOptions struct {
	// DataDir is the base directory of the control plane (cfg.DataDir). Each
	// user's store lives under <DataDir>/users/<u>/whatsapp.
	DataDir string

	// ContainerRoot is where the WAHA containers keep their state. In
	// production: /var/lib/vpsm-whatsapp. Each user gets <ContainerRoot>/<u>/.
	ContainerRoot string

	// Vault is the global secret store. The Manager goes through
	// scope.UserVault to read and write "<u>:waha_api_key" and friends.
	Vault *secrets.Store

	// SelfBaseURL is the control plane's public URL, without a trailing slash —
	// e.g. "http://127.0.0.1:8766". When set, the Service registers an
	// additional per-session webhook pointing at
	// <SelfBaseURL>/api/whatsapp/webhook/<u> so it receives events in real
	// time. Without it, secondary instances (a v2 sharing the WAHA container
	// with the v1 on :8765) depend on the overview polling alone to refresh —
	// new messages never show up live. Empty = legacy behaviour (only the
	// global WHATSAPP_HOOK_URL env var).
	SelfBaseURL string
}

// Manager keeps one live *Service per user. Concurrency: mu guards the map
// (provision/decommission are rare); ForUser takes a read lock on the hot
// path.
type Manager struct {
	opts     ManagerOptions
	registry *Registry

	mu       sync.RWMutex
	services map[scope.User]*Service
}

// NewManager builds the Manager. ContainerRoot is created with 0o755 if it is
// missing. It starts no containers — Provision and WarmUp do that.
func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.DataDir == "" {
		return nil, errors.New("whatsapp manager: DataDir required")
	}
	if opts.ContainerRoot == "" {
		opts.ContainerRoot = "/var/lib/vpsm-whatsapp"
	}
	if opts.Vault == nil {
		return nil, errors.New("whatsapp manager: Vault required")
	}
	reg, err := NewRegistry(opts.ContainerRoot)
	if err != nil {
		return nil, fmt.Errorf("whatsapp manager registry: %w", err)
	}
	return &Manager{
		opts:     opts,
		registry: reg,
		services: map[scope.User]*Service{},
	}, nil
}

// Registry exposes the port registry — used by tests and diagnostics, not on
// the hot path.
func (m *Manager) Registry() *Registry { return m.registry }

// ForUser returns the user's *Service, creating it lazily on the first call.
// It returns nil plus an error when: the user has no port allocated (Provision
// never ran), vault keys are missing, or building the Service failed.
//
// The Service it creates stays cached for the rest of the process. Reuse is
// safe because Service has its own internal mutex.
func (m *Manager) ForUser(u scope.User) (*Service, error) {
	m.mu.RLock()
	if svc, ok := m.services[u]; ok {
		m.mu.RUnlock()
		return svc, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	// re-check after upgrading to the exclusive lock
	if svc, ok := m.services[u]; ok {
		return svc, nil
	}
	svc, err := m.buildService(u)
	if err != nil {
		return nil, err
	}
	m.services[u] = svc
	return svc, nil
}

// buildService assembles a *Service for the user. Assumes the caller holds mu.
func (m *Manager) buildService(u scope.User) (*Service, error) {
	paths := scope.PathsFor(m.opts.DataDir, u)
	uv := scope.NewUserVault(m.opts.Vault, u)
	apiKey, ok1 := uv.Get("waha_api_key")
	hmacSec, ok2 := uv.Get("waha_hmac_secret")
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("whatsapp: vault keys missing for user %s (provision first)", u)
	}
	port, ok := m.registry.Lookup(u.String())
	if !ok {
		return nil, fmt.Errorf("whatsapp: no port allocated for user %s (provision first)", u)
	}
	opts := Options{
		StoreRoot: paths.Whatsapp,
		MediaRoot: paths.WhatsappMedia,
		// Per-user: /var/lib/vpsm-whatsapp/<user>/sessions/gows/default/gows.db
		// Empty keeps the legacy default of the single-tenant layout.
		GowsDBPath:  filepath.Join(paths.WhatsappContainer, "sessions/gows/default/gows.db"),
		WAHABaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		WAHAAPIKey:  apiKey,
		HMACSecret:  hmacSec,
		// Source of truth for when the cached value goes stale (see
		// Service.hmacConfere). Reads the vault at call time, not the value
		// captured here.
		HMACRefresh: func() string {
			v, _ := scope.NewUserVault(m.opts.Vault, u).Get("waha_hmac_secret")
			return v
		},
		ServiceUnit: fmt.Sprintf("vpsm-whatsapp@%s.service", u.String()),
	}
	// When SelfBaseURL is set (a v2 that knows its own host:port), register a
	// per-session webhook pointing back here — required in multi-instance
	// deployments that share a WAHA container. The container env keeps the
	// global webhook (v1); the per-session one is additive, it does not
	// replace it.
	if m.opts.SelfBaseURL != "" {
		opts.ExtraWebhookURL = strings.TrimRight(m.opts.SelfBaseURL, "/") + "/api/whatsapp/webhook/" + u.String()
	}
	// Migration: if this user has been cut over to the whatsmeow daemon
	// (cmd/wad), swap the WAHA backend for the meowClient. The daemon pushes
	// events in the WAHA envelope to the same webhook (HMACSecret =
	// waha_hmac_secret), so the rest of the Service is unchanged. The flag is
	// file-based, with no vault or endpoint involved:
	// /var/lib/vpsm-wad/<user>/enabled + meta.json{api_key}.
	if mc := maybeMeowBackend(u); mc != nil {
		opts.Backend = mc
		// The @lid->@c.us resolver has to read the daemon's LIVE database, not
		// WAHA's gows.db (frozen at cutover). Without this, a @lid from a contact
		// or group member first seen AFTER the migration never resolves and the
		// conversation turns into an orphaned shadow chat.
		opts.GowsDBPath = filepath.Join(wadStateDir(), u.String(), "session.db")
	}
	return New(opts)
}

// wadStateDir / wadBaseURL mirror the daemon's own defaults (cmd/wad).
// Overridable by env for tests.
func wadStateDir() string {
	if v := os.Getenv("WAD_STATE_DIR"); v != "" {
		return v
	}
	// HARD GUARD — never take the production path under `go test`.
	//
	// Without this, any test that builds a Router (newSmokeRouter does, with
	// SchemaVersion 2 and a temporary DataDir) runs the WhatsApp bootstrap, and
	// Provision writes into the PRODUCTION /var/lib/vpsm-wad — despite the
	// isolated DataDir, because this path ignored DataDir entirely. The test's
	// vault is brand new, so Provision GENERATES a fresh waha_hmac_secret and
	// writes it into production's meta.json; the daemon reloads and starts
	// signing with the test's secret while the running panel still holds the
	// real one. Measured result: `hmac mismatch` on 100% of webhooks and 201
	// events (63 real messages) DISCARDED over 36h — whatsmeow does not
	// redeliver, so they are gone for good. `go test ./internal/api/` on its
	// own produced 77 daemon restarts.
	if testing.Testing() {
		return filepath.Join(os.TempDir(), "vpsm-wad-test")
	}
	return "/var/lib/vpsm-wad"
}
func wadBaseURL() string {
	if v := os.Getenv("WAD_BASE_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:8769"
}

// maybeMeowBackend returns a meowClient when the user has been migrated to the
// daemon (<wadStateDir>/<user>/enabled exists and meta.json carries an
// api_key), otherwise nil.
func maybeMeowBackend(u scope.User) Backend {
	dir := filepath.Join(wadStateDir(), u.String())
	if _, err := os.Stat(filepath.Join(dir, "enabled")); err != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		log.Printf("whatsapp: wad enabled for %s but meta.json is unreadable: %v", u, err)
		return nil
	}
	var meta struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.APIKey == "" {
		log.Printf("whatsapp: invalid wad meta.json for %s", u)
		return nil
	}
	log.Printf("whatsapp: user %s is using the whatsmeow backend (daemon %s)", u, wadBaseURL())
	return newMeowClient(wadBaseURL(), u.String(), meta.APIKey)
}

// Has reports whether a Service already exists for the user, without creating one.
func (m *Manager) Has(u scope.User) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.services[u]
	return ok
}

// LookupRunning returns the already-cached Service, or nil. It never creates one.
func (m *Manager) LookupRunning(u scope.User) *Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.services[u]
}

// Provision prepares ALL of the user's infrastructure for running WhatsApp:
//
//  1. Creates dirs (data/users/<u>/whatsapp, /var/lib/vpsm-whatsapp/<u>/{sessions,media,files})
//  2. Allocates a port in the Registry (idempotent)
//  3. Generates waha_api_key and waha_hmac_secret in the vault if absent
//  4. Renders docker-compose.yml and .env into /var/lib/vpsm-whatsapp/<u>/
//  5. systemctl enable vpsm-whatsapp@<u>.service (does not start it yet — WarmUp does)
//
// Idempotent. Re-running rewrites compose/env, which is useful when the
// template changes.
func (m *Manager) Provision(u scope.User) error {
	paths := scope.PathsFor(m.opts.DataDir, u)
	if err := scope.EnsureDirs(paths); err != nil {
		return fmt.Errorf("provision dirs: %w", err)
	}
	if err := scope.EnsureWhatsappContainerDirs(paths); err != nil {
		return fmt.Errorf("provision container dirs: %w", err)
	}
	port, err := m.registry.Alloc(u.String())
	if err != nil {
		return fmt.Errorf("provision port: %w", err)
	}
	uv := scope.NewUserVault(m.opts.Vault, u)
	if _, ok := uv.Get("waha_api_key"); !ok {
		key, err := randomHex32()
		if err != nil {
			return fmt.Errorf("provision waha_api_key: %w", err)
		}
		if err := uv.Set("waha_api_key", key); err != nil {
			return fmt.Errorf("provision vault waha_api_key: %w", err)
		}
	}
	if _, ok := uv.Get("waha_hmac_secret"); !ok {
		key, err := randomHex32()
		if err != nil {
			return fmt.Errorf("provision waha_hmac_secret: %w", err)
		}
		if err := uv.Set("waha_hmac_secret", key); err != nil {
			return fmt.Errorf("provision vault waha_hmac_secret: %w", err)
		}
	}
	_ = port // reserved in the registry (buildService.Lookup); with WAHA removed it is unused.
	// WAHA REMOVED: it provisions the whatsmeow daemon directly. With no gows.db
	// to copy (a new user), the daemon creates a new device and pairs via QR.
	if err := m.provisionWad(u); err != nil {
		return fmt.Errorf("provision wad: %w", err)
	}
	return nil
}

// provisionWad prepares the whatsmeow daemon's state for the user: meta.json
// (the vault's HMAC plus an api key), the `enabled` flag (which makes
// buildService route through the daemon) and an empty session.db (new device
// -> QR).
//
// Restarting the daemon is CONDITIONAL: only when this provision actually
// changed state the daemon reads at boot (a new or changed meta.json, or a
// missing `enabled` flag). It used to be unconditional, and since Provision
// runs in a LOOP over users during server bootstrap (api.go), every boot fired
// N restarts of the SAME daemon within milliseconds — straight into systemd's
// rate limit (5 starts / 10s), which leaves the unit in `start-limit-hit`, a
// state Restart=always does not recover from. Genuinely idempotent now:
// reprovisioning with nothing changed never touches the daemon.
func (m *Manager) provisionWad(u scope.User) error {
	uv := scope.NewUserVault(m.opts.Vault, u)
	// Failing here is BETTER than carrying on: with an empty hmac_secret the
	// daemon does not send the X-Webhook-Hmac header, the panel (which does have
	// a secret) answers 401 "missing hmac", and EVERY inbound message is
	// discarded — silently, because the daemon treats 4xx as permanent and never
	// redelivers. The `_` that used to be here turned an unavailable vault into
	// total message loss.
	hmacSec, okHmac := uv.Get("waha_hmac_secret")
	if !okHmac || hmacSec == "" {
		return fmt.Errorf("waha_hmac_secret missing/empty in the vault of %s — "+
			"escrever meta.json sem ele faria o daemon perder toda mensagem recebida", u)
	}
	apiKey, ok := uv.Get("wad_api_key")
	if !ok || apiKey == "" {
		k, err := randomHex32()
		if err != nil {
			return err
		}
		apiKey = k
		_ = uv.Set("wad_api_key", apiKey)
	}
	dir := filepath.Join(wadStateDir(), u.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{"hmac_secret": hmacSec, "api_key": apiKey})
	metaPath := filepath.Join(dir, "meta.json")
	prev, _ := os.ReadFile(metaPath) // absent → prev nil → counts as a change
	changed := !bytes.Equal(bytes.TrimSpace(prev), bytes.TrimSpace(meta))
	if err := writeFileAtomic(metaPath, meta, 0o600); err != nil {
		return err
	}
	// An empty session.db = a fresh sqlite DB -> GetFirstDevice creates a device -> pairLoop QR.
	dbPath := filepath.Join(dir, "session.db")
	if _, err := os.Stat(dbPath); err != nil {
		if f, e := os.OpenFile(dbPath, os.O_CREATE|os.O_WRONLY, 0o600); e == nil {
			_ = f.Close()
		}
	}
	enabledPath := filepath.Join(dir, "enabled")
	if _, err := os.Stat(enabledPath); err != nil {
		changed = true // user now being routed through the daemon
	}
	if err := writeFileAtomic(enabledPath, []byte("1\n"), 0o600); err != nil {
		return err
	}
	if !changed {
		return nil
	}
	// Reload the daemon so it picks up the new user (best effort). The daemon
	// only scans the state dir at boot, so a new user really does require a
	// restart — but now only when there IS a new user.
	restartWadDaemon()
	return nil
}

// restartWadDaemon is a var so the test can count restarts without touching the
// machine's systemd.
var restartWadDaemon = func() {
	// A second barrier, independent of the first: even if a test points
	// WAD_STATE_DIR at a temp dir, restarting the production daemon takes the
	// user's WhatsApp down in the middle of the suite. That is what caused 34
	// minutes of downtime — the source of a burst that went unexplained at the
	// time.
	if testing.Testing() {
		return
	}
	if _, err := os.Stat("/usr/bin/systemctl"); err != nil {
		return
	}
	_, _ = exec.Command("/usr/bin/systemctl", "restart", "vpsm-wad.service").CombinedOutput()
}

// renderCompose renders docker-compose.tmpl.yml into
// /var/lib/vpsm-whatsapp/<u>/docker-compose.yml.
func (m *Manager) renderCompose(u scope.User, port int) error {
	tmpl, err := template.ParseFiles(composeTemplatePath)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{
		"User": u.String(),
		"Port": port,
	}); err != nil {
		return fmt.Errorf("exec template: %w", err)
	}
	dst := filepath.Join(m.opts.ContainerRoot, u.String(), "docker-compose.yml")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(dst, buf.Bytes(), 0o644)
}

// renderEnvFile writes the .env holding WAHA_API_KEY and WAHA_HMAC_SECRET (read
// from the namespaced vault). Mode 0o600 — systemd reads this file as root.
func (m *Manager) renderEnvFile(u scope.User, port int) error {
	uv := scope.NewUserVault(m.opts.Vault, u)
	apiKey, _ := uv.Get("waha_api_key")
	hmacSec, _ := uv.Get("waha_hmac_secret")
	body := fmt.Sprintf("WAHA_API_KEY=%s\nWAHA_HMAC_SECRET=%s\nPORT=%d\n", apiKey, hmacSec, port)
	dst := filepath.Join(m.opts.ContainerRoot, u.String(), ".env")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(dst, []byte(body), 0o600)
}

// WarmUp fires a systemctl start for the user in the background. Idempotent:
// a no-op if it is already running. A failed start is logged but does not
// block the login — the UI shows "STARTING" via /api/whatsapp/status until
// the container answers.
//
// NewRouter does not call this directly — the login handler does.
func (m *Manager) WarmUp(u scope.User) {
	if _, err := os.Stat("/usr/bin/systemctl"); err != nil {
		return
	}
	// User migrated to the whatsmeow daemon: do NOT bring the WAHA container
	// up. It would reconnect with the SAME device and steal the daemon's
	// stream (StreamReplaced), after which sending fails with "not connected".
	// The daemon is persistent (it has its own systemd unit) and already up;
	// there is nothing to warm.
	if maybeMeowBackend(u) != nil {
		return
	}
	unit := fmt.Sprintf("vpsm-whatsapp@%s.service", u.String())
	go func() {
		// 30s timeout — systemctl start rarely takes more than 5s. If it hangs
		// (unit-failed loop, dependency timeout, shutdown) this aborts instead
		// of leaking the goroutine.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/bin/systemctl", "start", unit).CombinedOutput()
		if err != nil {
			log.Printf("whatsapp warmup %s: %v: %s", unit, err, strings.TrimSpace(string(out)))
		}
	}()
}

// Decommission shuts EVERYTHING of the user's down and archives the state under
// .archive/<u>-<ts>:
//
//  1. (best effort) WhatsApp logout (revokes the pairing on Meta's servers)
//  2. systemctl disable --now vpsm-whatsapp@<u>
//  3. mv /var/lib/vpsm-whatsapp/<u> -> /var/lib/vpsm-whatsapp/.archive/<u>-<ts>
//  4. mv data/users/<u>/whatsapp -> data/users/.archive/<u>-<ts>/whatsapp
//  5. registry.Release(u)
//  6. delete the vault keys "<u>:*"
//
// The caller (the user-delete handler in api.go) is responsible for audit
// logging. An external cron job clears .archive/* after 30 days.
func (m *Manager) Decommission(u scope.User) error {
	// 1. Drop it from the cache first, so nothing hits the Service we are tearing down.
	m.mu.Lock()
	if svc := m.services[u]; svc != nil {
		svc.Close()
		delete(m.services, u)
	}
	m.mu.Unlock()

	// 2. systemctl disable --now
	if _, err := os.Stat("/usr/bin/systemctl"); err == nil {
		unit := fmt.Sprintf("vpsm-whatsapp@%s.service", u.String())
		// Order: stop, then disable. Errors are logged.
		_, _ = exec.Command("/usr/bin/systemctl", "stop", unit).CombinedOutput()
		_, _ = exec.Command("/usr/bin/systemctl", "disable", unit).CombinedOutput()
	}

	// 3. Release the port — an I/O error here does not block the rest.
	_ = m.registry.Release(u.String())

	// 4. Delete the vault keys "<u>:waha_*".
	uv := scope.NewUserVault(m.opts.Vault, u)
	for _, k := range uv.List() {
		_ = uv.Delete(k)
	}

	return nil
}

// writeFileAtomic writes a tmp file and renames it. Unexported — only for
// renderCompose and renderEnvFile in here. (A similar version exists in
// internal/config/migrate.go, but duplicating it avoids an import cycle.)
func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// randomHex32 generates 32 random bytes and returns them as hex (64 chars).
// Used by Provision for WAHA_API_KEY and WAHA_HMAC_SECRET.
func randomHex32() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HandleWebhook is the public entry point that WAHA fires at the control plane.
//
// Expected path: /api/whatsapp/webhook/<user>. The user comes from the URL,
// NOT from the payload — WAHA Core emits session="default" on every profile,
// so the payload cannot tell them apart. The HMAC is verified with that
// user's own secret (namespaced in the vault), so leaking A's secret does not
// let anyone forge an event for B.
//
// 503 when: the user has no Service (Provision never ran) or ForUser failed
// internally. WAHA retries — once Provision finishes, the next event gets in.
func (m *Manager) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/webhook/")
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		http.Error(w, "missing user in path", http.StatusNotFound)
		return
	}
	u, err := scope.New(rest)
	if err != nil {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	svc, err := m.ForUser(u)
	if err != nil {
		// 503: WAHA will retry. Provision may still be in flight.
		log.Printf("whatsapp webhook for %s: %v", u, err)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	svc.HandleWebhook(w, r)
}

// ProtectedHandler returns an http.Handler that dispatches the request to the
// authenticated user's Service mux. userFn is injected by the caller
// (api.NewRouter) to avoid the circular dependency auth -> scope -> whatsapp
// -> auth.
//
// audit is the logging callback; same signature Service.RegisterProtected uses.
// Each Service keeps its own cached mux (m.serviceMux).
func (m *Manager) ProtectedHandler(audit func(*http.Request, string, string), userFn func(*http.Request) (scope.User, bool)) http.Handler {
	if audit == nil {
		audit = func(*http.Request, string, string) {}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := userFn(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		svc, err := m.ForUser(u)
		if err != nil {
			// Non-fatal: the user may be provisioning, or the Service may have failed.
			// Log it and return 503 — the UI shows a temporary error and retries.
			log.Printf("whatsapp protected for %s: %v", u, err)
			http.Error(w, "whatsapp service not ready", http.StatusServiceUnavailable)
			return
		}
		svc.protectedMuxOnce.Do(func() {
			svc.protectedMux = svc.BuildProtectedMux(audit)
		})
		svc.protectedMux.ServeHTTP(w, r)
	})
}

// AvatarHandler returns the handler for /api/whatsapp/avatar/<jid>, which
// dispatches to the user's Service. Deliberately public (no auth) because
// <img src=...> does not send an Authorization header — the same reason as
// the original design in api.go.
//
// userFn fallback: when the user is not authenticated (an <img> without a
// cookie), we would try to resolve them from the Referer or a parameter. For
// now there is no fallback — it only serves authenticated requests (the
// avatar breaks, but it is safe).
func (m *Manager) AvatarHandler(userFn func(*http.Request) (scope.User, bool)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := userFn(r)
		if !ok {
			http.NotFound(w, r)
			return
		}
		svc, err := m.ForUser(u)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		svc.handleAvatar(w, r)
	})
}

// DaemonEnabled reports whether ANY user is routed through the whatsmeow
// daemon (<wadStateDir>/<user>/enabled exists). Cheap: a stat, no network I/O.
func DaemonEnabled(users []scope.User) bool {
	for _, u := range users {
		if _, err := os.Stat(filepath.Join(wadStateDir(), u.String(), "enabled")); err == nil {
			return true
		}
	}
	return false
}

// daemonProbeClient is shared so the probe can reuse the connection: /api/health
// is public and hammered in a loop by the deploy, and a fresh Client per call
// would strand sockets in TIME_WAIT for nothing.
var daemonProbeClient = &http.Client{Timeout: 3 * time.Second}

// DaemonAlive does a REAL liveness probe against the wad daemon (GET /healthz,
// no auth). Returns nil when the daemon answers 2xx.
//
// It exists because /api/health only read Store.State() from memory — a cache
// of the last known state. With the daemon dead that cache kept saying
// "working", so the panel reported whatsapp:"connected" while every send
// failed with "connection refused". That is how 34 minutes of WhatsApp
// downtime went by without a single signal — and a deploy still sailed
// through the health gate in the middle of it. A cache is not liveness.
func DaemonAlive(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wadBaseURL()+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := daemonProbeClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wad /healthz: HTTP %d", resp.StatusCode)
	}
	return nil
}
