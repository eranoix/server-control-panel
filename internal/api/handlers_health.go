package api

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
	"server-control-panel/internal/config"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/whatsapp"
)

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	checks := map[string]string{}
	ok := true
	if _, err := config.Load(); err != nil {
		checks["config"] = err.Error()
		ok = false
	} else {
		checks["config"] = "ok"
	}
	if r.cfg.LoadedFromBackup {
		checks["config_from_backup"] = r.cfg.LoadedBackupName
	}
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
	if r.secrets != nil {
		checks["secrets"] = "ok"
	} else {
		checks["secrets"] = "disabled"
	}
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
	if r.docker != nil {
		checks["docker"] = "ok"
	} else {
		checks["docker"] = "unavailable"
	}
	if _, err := exec.LookPath("dtach"); err == nil {
		checks["dtach"] = "ok"
	} else {
		checks["dtach"] = "missing"
	}
	checks["session_backend"] = "dtach"
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
	if r.cfg.SessionCollectorEnabled {
		checks["session_collector"] = "on"
	} else {
		checks["session_collector"] = "off"
	}
	resp := map[string]any{"ok": ok, "time": time.Now().Unix(), "checks": checks}
	resp["build"] = buildStamp
	if r.queue != nil {
		rn, qd := r.queue.Counts()
		resp["queue"] = map[string]int{"running": rn, "queued": qd}
	}
	if !ok {
		w.WriteHeader(503)
	}
	writeJSON(w, resp)
}

type healthSubsystem struct {
	OK        bool   `json:"ok"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

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
	out["dtach"] = measure(func() (string, error) {
		if _, err := exec.LookPath("dtach"); err != nil {
			return "missing", err
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

func (r *Router) handleHealthDetailed(w http.ResponseWriter, _ *http.Request) {
	out := r.computeHealthSubsystems()
	writeJSON(w, map[string]any{
		"time":       time.Now().Unix(),
		"subsystems": out,
	})
}

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

func (r *Router) handlePanelHealth(w http.ResponseWriter, req *http.Request) {
	out := map[string]any{
		"uptime_seconds":            int64(time.Since(processStart).Seconds()),
		"version":                   "server-control-panel",
		"tls_enabled":               r.cfg.TLSEnabled,
		"tls_mode":                  tlsMode(r.cfg),
		"config_loaded_from_backup": r.cfg.LoadedFromBackup,
		"config_backup_name":        r.cfg.LoadedBackupName,
	}
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
	}

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

var processStart = time.Now()

func (r *Router) handleConfig(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, map[string]any{
		"listen":      r.cfg.Listen,
		"data_dir":    r.cfg.DataDir,
		"username":    auth.UserFrom(req),
		"claude_home": r.cfg.ClaudeHome,
	})
}
