package api

// handlers_health.go — health/readiness/diagnostics + handleConfig + TLS cert
//
// Covers:
//   - handleHealth (/api/health, public; the basis of deploy.sh's auto-rollback)
//   - handleHealthDetailed (/api/health/detailed, admin only)
//   - handleVPSMHealth (/api/vpsm-health, info about the running binary)
//   - readCertExpiry + tlsMode (TLS helpers)
//   - handleConfig (/api/config, the info that can be exposed to the frontend)
//
// Extracted from api.go. It stays on *Router because it uses r.cfg/r.whatsappMgr/etc.

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/clauderouter"
	"server-control-panel/internal/config"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/whatsapp"
)

// handleHealth is the deploy/probe health endpoint. Unauthenticated by design
// so the deploy script can poll it. Returns 200 when all subsystems are
// healthy, 503 with details when something's degraded. The deploy script's
// rollback hinges on this returning ≥500 to signal "new binary is broken".
func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	checks := map[string]string{}
	ok := true
	// 1. Config still parses (re-load on demand)
	if _, err := config.Load(); err != nil {
		checks["config"] = err.Error()
		ok = false
	} else {
		checks["config"] = "ok"
	}
	// 2. Surface degraded mode (loaded from backup)
	if r.cfg.LoadedFromBackup {
		checks["config_from_backup"] = r.cfg.LoadedBackupName
		// degraded but service still works; don't flip ok=false
	}
	// 3. Audit log writable
	if r.audit != nil {
		path := filepath.Join(r.cfg.DataDir, "audit.log")
		if af, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			_ = af.Close()
			checks["audit"] = "ok"
		} else {
			checks["audit"] = err.Error()
			ok = false
		}
	} else {
		checks["audit"] = "disabled"
	}
	// 4. Secrets vault openable
	if r.secrets != nil {
		checks["secrets"] = "ok"
	} else {
		checks["secrets"] = "disabled"
	}
	// 5. WhatsApp (optional — a failure here does not take overall health down).
	// In multi-tenant, ONE user with a WORKING session is enough to report connected.
	if r.whatsappMgr != nil && r.cfg != nil {
		bestStatus := "disabled"
		anyWorking := false
		anyQR := false
		anyReachable := false
		for _, u := range r.cfg.AllUsers() {
			su, err := scope.New(u.Username)
			if err != nil {
				continue
			}
			svc := r.whatsappMgr.LookupRunning(su)
			if svc == nil {
				continue
			}
			st := svc.Store.State()
			if st.WAHAReachable {
				anyReachable = true
			}
			if st.Status == whatsapp.StatusWorking {
				anyWorking = true
			}
			if st.Status == whatsapp.StatusScanQR {
				anyQR = true
			}
		}
		switch {
		case anyWorking:
			bestStatus = "connected"
		case anyQR:
			bestStatus = "awaiting_qr"
		case !anyReachable:
			bestStatus = "waha_unreachable"
		default:
			bestStatus = "idle"
		}
		// REAL liveness of the whatsmeow daemon. The statuses above come from
		// Store.State(), which is a cache: with the daemon dead it freezes on
		// the last value and the dashboard goes on saying "connected" while
		// every send fails with connection refused. The probe beats the cache —
		// if the daemon does not answer, nothing else matters.
		var wadUsers []scope.User
		for _, u := range r.cfg.AllUsers() {
			if su, err := scope.New(u.Username); err == nil {
				wadUsers = append(wadUsers, su)
			}
		}
		if whatsapp.DaemonEnabled(wadUsers) {
			ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
			err := whatsapp.DaemonAlive(ctx)
			cancel()
			if err != nil {
				bestStatus = "wad_down"
			}
		}
		checks["whatsapp"] = bestStatus
	} else {
		checks["whatsapp"] = "disabled"
	}
	// 6. Docker daemon (optional — degraded ≠ ok=false; a deploy only fails on fatal)
	if r.docker != nil {
		checks["docker"] = "ok"
	} else {
		checks["docker"] = "unavailable"
	}
	// 7. dtach available (the session engine behind the terminal panels —
	// degraded but not fatal). dtach is the only session engine.
	if _, err := exec.LookPath("dtach"); err == nil {
		checks["dtach"] = "ok"
	} else {
		checks["dtach"] = "missing"
	}
	// The ACTIVE session engine — dtach only (informational).
	checks["session_backend"] = "dtach"
	// 8. claude-router upstream — informational only; router mode missing is degraded.
	cr := clauderouter.New()
	if h := cr.Healthz(); h.Reachable {
		checks["claude_router"] = "ok"
	} else {
		checks["claude_router"] = "unreachable"
	}
	// 8b. PROACTIVE integrity of the router's env. The router reads /etc/claude-router/env
	// (a symlink -> users/<primary>.env) and caches BRIDGE_API_KEY in memory at boot.
	// If the symlink becomes circular or broken (see migrate.go), the CURRENT router stays
	// alive on the cached key, but the NEXT restart takes it down. Detecting it here fires
	// the alert BEFORE that. Degraded by design: it NEVER sets ok=false -- otherwise a
	// broken env would revert EVERY deploy (deploy.sh is health-gated + auto-rollback).
	checks["claude_router_env"] = cr.EnvIntegrity()
	// 9. Supabase/GoTrue — the auth backend. If it goes down, login fails silently.
	// A FAIL here TAKES overall health down (ok=false) because without auth there is no system.
	if sb := r.auth.SupabaseClient(); sb != nil {
		hctx, hcancel := context.WithTimeout(req.Context(), 2*time.Second)
		if err := sb.Health(hctx); err != nil {
			checks["supabase"] = "unreachable: " + err.Error()
			ok = false
		} else {
			checks["supabase"] = "ok"
		}
		hcancel()
	} else {
		checks["supabase"] = "disabled"
	}
	// 10. Acme writer (final destination: improvement_requests /
	// time_entries). First stage: live mode blocked until the schema/URL
	// 12. Session collector. An informational stub: "on" when the collector is
	// enabled by config, "off" otherwise. CRITICAL: like fllr/gmail, it NEVER
	// sets ok=false (additive/inert by design). Locked by TestSmokeHealthSessionStub.
	if r.cfg.SessionCollectorEnabled {
		checks["session_collector"] = "on"
	} else {
		checks["session_collector"] = "off"
	}
	resp := map[string]any{"ok": ok, "time": time.Now().Unix(), "checks": checks}
	// build identifies THIS binary. The frontend compares it with the stamp that
	// came in the page's <meta vpsm-build>: an open tab goes on running the old JS
	// forever after a deploy (the ?v=<stamp> is only re-evaluated on a reload),
	// so without this every frontend fix stays invisible to anyone who does not
	// reload — which is exactly what happened once.
	resp["build"] = buildStamp
	// Queue depth, in a sibling field (checks is map[string]string). The
	// deploy script polls this to drain in-flight jobs before restarting so
	// a deploy doesn't interrupt them. running+queued==0 ⇒ safe to restart.
	if r.queue != nil {
		rn, qd := r.queue.Counts()
		resp["queue"] = map[string]int{"running": rn, "queued": qd}
	}
	if !ok {
		w.WriteHeader(503)
	}
	writeJSON(w, resp)
}

// healthSubsystem is the per-subsystem shape of handleHealthDetailed AND of
// healthDetailedSnapshot — the same aggregation of checks, exposed twice:
// in the full JSON served at /api/health/detailed, and summarised (through
// healthDetailedSnapshot) for the mobile BFF at GET /api/mobile/v1/ops/status
// and its live channel — never a second list of checks.
type healthSubsystem struct {
	OK        bool   `json:"ok"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// computeHealthSubsystems runs each subsystem check once. Extracted from
// handleHealthDetailed so healthDetailedSnapshot can reuse it without
// duplicating the list of checks.
func (r *Router) computeHealthSubsystems() map[string]healthSubsystem {
	out := map[string]healthSubsystem{}
	measure := func(fn func() (string, error)) healthSubsystem {
		start := time.Now()
		status, err := fn()
		lat := time.Since(start).Milliseconds()
		s := healthSubsystem{LatencyMs: lat, Status: status, OK: err == nil}
		if err != nil {
			s.Error = err.Error()
		}
		return s
	}
	out["config"] = measure(func() (string, error) {
		_, err := config.Load()
		if err != nil {
			return "fail", err
		}
		return "ok", nil
	})
	out["audit"] = measure(func() (string, error) {
		if r.audit == nil {
			return "disabled", nil
		}
		path := filepath.Join(r.cfg.DataDir, "audit.log")
		af, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return "fail", err
		}
		_ = af.Close()
		return "ok", nil
	})
	out["secrets"] = measure(func() (string, error) {
		if r.secrets == nil {
			return "disabled", nil
		}
		return "ok", nil
	})
	out["docker"] = measure(func() (string, error) {
		if r.docker == nil {
			return "unavailable", nil
		}
		return "ok", nil
	})
	// THE OLD ENGINE'S CHECK IS GONE. The session engine is `dtach` —
	// `NewSessionBackend` returns `newDtachBackend` and there is no selector any
	// more. A green check for a binary nobody invokes is not reassuring, it is
	// noise: it asserts a dependency that does not exist, and on the day it went
	// red it would send somebody to fix the wrong thing. What matters here is
	// `dtach`, which is still checked.
	out["dtach"] = measure(func() (string, error) {
		if _, err := exec.LookPath("dtach"); err != nil {
			return "missing", err
		}
		return "ok", nil
	})
	out["claude_router"] = measure(func() (string, error) {
		h := clauderouter.New().Healthz()
		if !h.Reachable {
			return "unreachable", nil
		}
		return "ok", nil
	})
	out["whatsapp"] = measure(func() (string, error) {
		if r.whatsappMgr == nil {
			return "disabled", nil
		}
		anyWorking := false
		for _, u := range r.cfg.AllUsers() {
			su, err := scope.New(u.Username)
			if err != nil {
				continue
			}
			svc := r.whatsappMgr.LookupRunning(su)
			if svc == nil {
				continue
			}
			if svc.Store.State().Status == whatsapp.StatusWorking {
				anyWorking = true
				break
			}
		}
		// The same probe as /api/health: the Status above is the Store's cache
		// and lies when the daemon is down.
		var wadUsers []scope.User
		for _, u := range r.cfg.AllUsers() {
			if su, err := scope.New(u.Username); err == nil {
				wadUsers = append(wadUsers, su)
			}
		}
		if whatsapp.DaemonEnabled(wadUsers) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := whatsapp.DaemonAlive(ctx)
			cancel()
			if err != nil {
				return "wad_down", err
			}
		}
		if anyWorking {
			return "connected", nil
		}
		return "idle", nil
	})
	out["videocall"] = measure(func() (string, error) {
		if r.videocall == nil {
			return "disabled", nil
		}
		return "ok", nil
	})
	return out
}

// handleHealthDetailed returns each subsystem with timing + last_error for a
// granular UI. Stable shape: {subsystem: {ok, status, latency_ms, error}}.
func (r *Router) handleHealthDetailed(w http.ResponseWriter, _ *http.Request) {
	out := r.computeHealthSubsystems()
	writeJSON(w, map[string]any{
		"time":       time.Now().Unix(),
		"subsystems": out,
	})
}

// healthDetailedSnapshot reuses computeHealthSubsystems (the same checks as
// /api/health/detailed) and projects the result into the shape the mobile
// BFF needs (internal/mobilebff.Deps.HealthDetailed): a name→status map and
// an aggregate ok. `ok` here is the app's ops screen's OWN definition (the
// AND of every subsystem) — deliberately different from /api/health's
// semantics, which ignores certain informational checks (fllr, gmail, ...)
// so as not to take the deploy health-gate down; the mobile ops screen is
// not consulted by deploy.sh, so it can afford to be stricter.
func (r *Router) healthDetailedSnapshot() (ok bool, checks map[string]string) {
	subs := r.computeHealthSubsystems()
	checks = make(map[string]string, len(subs))
	ok = true
	for name, s := range subs {
		checks[name] = s.Status
		if !s.OK {
			ok = false
		}
	}
	return ok, checks
}

// handleVPSMHealth surfaces self-monitoring info: process uptime, TLS cert
// expiration (when configured), TOTP coverage, and last-login summary from
// the audit log. The frontend dashboard uses this to display a "system card".
func (r *Router) handleVPSMHealth(w http.ResponseWriter, req *http.Request) {
	out := map[string]any{
		"uptime_seconds":            int64(time.Since(processStart).Seconds()),
		"version":                   "vps-manager", // could embed build tag here later
		"tls_enabled":               r.cfg.TLSEnabled,
		"tls_mode":                  tlsMode(r.cfg),
		"config_loaded_from_backup": r.cfg.LoadedFromBackup,
		"config_backup_name":        r.cfg.LoadedBackupName,
	}
	// User accounts + 2FA enrolment count — read under cfgMu so concurrent
	// SetPassword/AddUser writes don't tear the slice.
	r.cfgMu.Lock()
	users := r.cfg.AllUsers()
	r.cfgMu.Unlock()
	totp := 0
	for _, u := range users {
		if u.TOTPSecret != "" {
			totp++
		}
	}
	out["users_count"] = len(users)
	out["users_totp_enrolled"] = totp

	// TLS cert expiration when manual or self-signed
	if r.cfg.TLSEnabled && r.cfg.TLSDomain == "" {
		cert := r.cfg.TLSCert
		if cert == "" {
			cert = filepath.Join(r.cfg.DataDir, "tls.crt")
		}
		if exp, err := readCertExpiry(cert); err == nil {
			out["tls_cert_path"] = cert
			out["tls_expires_at"] = exp.Unix()
			out["tls_expires_in_days"] = int(time.Until(exp).Hours() / 24)
		}
	}
	if r.cfg.TLSEnabled && r.cfg.TLSDomain != "" {
		out["tls_domain"] = r.cfg.TLSDomain
		// certmagic stores the cert under data/certmagic/...; surfacing the
		// exact path would couple us to its layout — keep it conceptual.
	}

	// Last successful login from audit log (Tail returns newest first)
	if r.audit != nil {
		for _, e := range r.audit.Tail(100) {
			if e.Action == "login.ok" {
				out["last_login_user"] = e.User
				out["last_login_at"] = e.Time
				out["last_login_ip"] = e.IP
				break
			}
		}
	}

	writeJSON(w, out)
}

// readCertExpiry parses a PEM cert file and returns its NotAfter.
func readCertExpiry(path string) (time.Time, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return time.Time{}, fmt.Errorf("no PEM block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}

func tlsMode(c *config.Config) string {
	if !c.TLSEnabled {
		return "off"
	}
	if c.TLSDomain != "" {
		return "letsencrypt"
	}
	if c.TLSCert != "" {
		return "manual"
	}
	return "self-signed"
}

// processStart is captured at init() so handleVPSMHealth can report uptime.
var processStart = time.Now()

func (r *Router) handleConfig(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, map[string]any{
		"listen":      r.cfg.Listen,
		"data_dir":    r.cfg.DataDir,
		"username":    auth.UserFrom(req),
		"claude_home": r.cfg.ClaudeHome,
	})
}
