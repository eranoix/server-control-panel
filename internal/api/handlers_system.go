package api

// handlers_system.go — host observability and operations
//
// Covers system state (stats/history/listening/connections),
// systemd unit management (units/status/journal/restart/action),
// apt + reboot, a viewer for local logs (/var/log) and UFW.
//
// Extracted from api.go. It stays on *Router because it uses mustPrimary/audit/cfg.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/sysextra"
	"server-control-panel/internal/system"
	"server-control-panel/internal/wsorigin"
)

// ---------- System ----------

// statsTTL: the window in which one /api/stats collection is reused. Shorter
// than the frontend's poll (5s) so that a lone tab keeps seeing fresh data, but
// long enough for N concurrent tabs to share ONE collection.
const statsTTL = 3 * time.Second

var (
	statsCacheMu  sync.Mutex
	statsCached   *system.Stats
	statsCachedAt time.Time
)

// collectStatsCached serialises and memoises system.Collect for statsTTL.
// system.Collect blocks for about 200ms sampling the CPU and walks ALL of /proc;
// without this, every open tab paid that cost every 5s (N tabs = N times the
// load on the host). The mutex also acts as singleflight: concurrent requests
// on a cache miss wait for the collection in progress instead of firing several.
func collectStatsCached(ctx context.Context) (*system.Stats, error) {
	statsCacheMu.Lock()
	defer statsCacheMu.Unlock()
	if statsCached != nil && time.Since(statsCachedAt) < statsTTL {
		return statsCached, nil
	}
	s, err := system.Collect(ctx)
	if err != nil {
		return nil, err
	}
	// The frontend iterates disks/net/top_procs in x-for; deduplicating and
	// dropping entries with an empty key avoids the "reading 'after'" crash in Alpine.
	s.Disks = sanitizeList(s.Disks, "Mount").([]system.DiskInfo)
	s.Net = sanitizeList(s.Net, "Name").([]system.NetInfo)
	s.TopProcs = sanitizeList(s.TopProcs, "PID").([]system.ProcInfo)
	statsCached, statsCachedAt = s, time.Now()
	return s, nil
}

func (r *Router) handleStats(w http.ResponseWriter, req *http.Request) {
	// Cached: the snapshot is read-only once it has been stored, so handing the
	// same pointer to concurrent requests is safe.
	s, err := collectStatsCached(req.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, s)
}

func (r *Router) handleHistory(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, r.ring.Snapshot())
}

func (r *Router) handleListening(w http.ResponseWriter, req *http.Request) {
	p, err := sysextra.Listening()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// The frontend uses :key="p.Local". ss(8) rarely returns lines with no Local,
	// but it can duplicate when there are several sockets per port (TCP+TCP6).
	writeJSON(w, sanitizeList(p, "Local"))
}

func (r *Router) handleConnections(w http.ResponseWriter, req *http.Request) {
	c, err := sysextra.Connections()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, c)
}

func (r *Router) handleUnits(w http.ResponseWriter, req *http.Request) {
	u, err := sysextra.ListUnits()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// systemd units with an empty Name (the legend lines of `list-units`) have to
	// be dropped before they become :key="u.Name" in the frontend.
	writeJSON(w, sanitizeList(u, "Name"))
}

func (r *Router) handleUnitStatus(w http.ResponseWriter, req *http.Request) {
	name := req.URL.Query().Get("unit")
	if name == "" {
		writeErr(w, 400, "unit required")
		return
	}
	out, err := sysextra.Status(name)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func (r *Router) handleJournal(w http.ResponseWriter, req *http.Request) {
	name := req.URL.Query().Get("unit")
	if name == "" {
		writeErr(w, 400, "unit required")
		return
	}
	lines := 300
	if v := req.URL.Query().Get("lines"); v != "" {
		var n int
		if _, e := fmtSscan(v, &n); e == nil && n > 0 {
			lines = n
		}
	}
	out, err := sysextra.Journal(name, lines)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, out)
}

func (r *Router) handleUnitRestart(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	name := req.URL.Query().Get("unit")
	if name == "" {
		writeErr(w, 400, "unit required")
		return
	}
	out, err := sysextra.SystemdRestart(name)
	if err != nil {
		writeErr(w, 500, err.Error()+" "+out)
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "systemd.restart", name)
	writeJSON(w, map[string]string{"status": "ok", "output": out})
}

// handleUnitAction performs start/stop/enable/disable/restart on a systemd unit.
// All non-restart ops shell out to systemctl directly so we don't need new
// helpers in sysextra. The unit name is sanitized to [A-Za-z0-9_.@-].
func (r *Router) handleUnitAction(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	name := req.URL.Query().Get("unit")
	action := req.URL.Query().Get("action")
	if name == "" || action == "" {
		writeErr(w, 400, "unit and action required")
		return
	}
	if !validUnitName(name) {
		writeErr(w, 400, "invalid unit name")
		return
	}
	allowed := map[string]bool{
		"start": true, "stop": true, "restart": true,
		"enable": true, "disable": true, "reload": true,
	}
	if !allowed[action] {
		writeErr(w, 400, "invalid action")
		return
	}
	out, err := execCmd("systemctl", action, name)
	if err != nil {
		writeErr(w, 500, err.Error()+" "+out)
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "systemd."+action, name)
	writeJSON(w, map[string]string{"status": "ok", "output": out})
}

func validUnitName(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	// It cannot start with '-' or '@' (ambiguous with a CLI flag or an empty
	// instance). systemd accepts the template `foo@inst.service` — `@` only in
	// the middle, counted exactly once. No spaces, no ';', no `|`, no `$`.
	if s[0] == '-' || s[0] == '@' || s[0] == '.' {
		return false
	}
	atCount := 0
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		case c == '@':
			atCount++
			if atCount > 1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// handleApt runs apt-get with the given action. Output is captured (potentially
// long-running). Body: {"action":"update|upgrade|autoremove","assume_yes":true}.
// Only `update`, `upgrade`, and `autoremove` are accepted; others (purge, install,
// remove a package by name) require typing the package which we don't surface.
func (r *Router) handleApt(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	args := []string{"-o", "DPkg::Lock::Timeout=60"}
	switch body.Action {
	case "update":
		args = append(args, "update")
	case "upgrade":
		args = append(args, "-y", "upgrade")
	case "autoremove":
		args = append(args, "-y", "autoremove")
	default:
		writeErr(w, 400, "action must be update|upgrade|autoremove")
		return
	}
	out, err := execCmdLong("apt-get", args...)
	r.auditEvent(req, auth.UserFrom(req), "apt."+body.Action, "")
	if err != nil {
		// Still return the output — useful for the user even on failure.
		writeJSON(w, map[string]any{"status": "error", "error": err.Error(), "output": out})
		return
	}
	writeJSON(w, map[string]any{"status": "ok", "output": out})
}

// handleReboot triggers a system reboot. Confirmation is the caller's
// responsibility (the UI shows a HEAVY confirm modal).
func (r *Router) handleReboot(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	user := auth.UserFrom(req)
	r.auditEvent(req, user, "system.reboot", "")
	// Schedule a brief delay so we can return the 200 first.
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("reboot goroutine panic: %v", rec)
			}
		}()
		time.Sleep(500 * time.Millisecond)
		_, _ = execCmd("systemctl", "reboot")
	}()
	writeJSON(w, map[string]string{"status": "scheduled"})
}

// ---------- system logs, ufw, cron, compose wizard ----------

// allowedLogPaths whitelists which /var/log files we'll serve. Avoids
// turning the endpoint into "read any file as root".
var allowedLogPaths = map[string]bool{
	"/var/log/syslog":            true,
	"/var/log/auth.log":          true,
	"/var/log/kern.log":          true,
	"/var/log/dpkg.log":          true,
	"/var/log/ufw.log":           true,
	"/var/log/nginx/access.log":  true,
	"/var/log/nginx/error.log":   true,
	"/var/log/docker.log":        true,
	"/opt/panel/data/audit.log":  true,
	"/opt/panel/data/deploy.log": true,
}

// handleSystemLogs lists the available log files (existing ones in the whitelist).
func (r *Router) handleSystemLogs(w http.ResponseWriter, req *http.Request) {
	type logInfo struct {
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Modified int64  `json:"modified"`
	}
	out := make([]logInfo, 0)
	for p := range allowedLogPaths {
		if fi, err := os.Stat(p); err == nil {
			out = append(out, logInfo{Path: p, Size: fi.Size(), Modified: fi.ModTime().Unix()})
		}
	}
	writeJSON(w, sanitizeList(out, "Path"))
}

// handleSystemLogTail streams `tail -F <path>` via WebSocket. The path must
// be in allowedLogPaths.
func (r *Router) handleSystemLogTail(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Query().Get("path")
	if !allowedLogPaths[path] {
		http.Error(w, "log not allowed", 400)
		return
	}
	conn, err := wsUpgrader.Upgrade(w, req, wsorigin.SecHeaders())
	if err != nil {
		return
	}
	defer conn.Close()

	// Mobile WS: NAT timeouts on 4G drop the connection silently; with no
	// ping/pong and no ReadDeadline, the tail keeps running until the TCP keepalive (2h).
	const (
		pongWait   = 60 * time.Second
		pingPeriod = 25 * time.Second
		writeWait  = 10 * time.Second
	)
	conn.SetReadLimit(8 * 1024) // the client only sends close/ping; 8KB is plenty
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	// Single-writer mutex: gorilla/websocket forbids concurrent WriteMessage.
	// Before this fix, the ping goroutine and the main copy loop could race
	// — one truncating the other and corrupting the framed stream.
	var writeMu sync.Mutex
	safeWrite := func(mt int, p []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(mt, p)
	}
	cmd := exec.CommandContext(ctx, "tail", "-n", "200", "-F", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = safeWrite(websocket.TextMessage, []byte("erro: "+err.Error()))
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		_ = safeWrite(websocket.TextMessage, []byte("start: "+err.Error()))
		return
	}
	// Reader: any incoming message (including close) terminates tail.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()
	// Ping ticker: keeps the connection alive behind NAT/proxies.
	pingTicker := time.NewTicker(pingPeriod)
	defer pingTicker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-pingTicker.C:
				if err := safeWrite(websocket.PingMessage, nil); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	buf := make([]byte, 8192)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			if werr := safeWrite(websocket.TextMessage, buf[:n]); werr != nil {
				cancel()
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// handleUFW returns the current UFW status + rules. Output is the literal
// `ufw status numbered`, which is what most admins recognise.
func (r *Router) handleUFW(w http.ResponseWriter, req *http.Request) {
	out, err := execCmd("ufw", "status", "numbered")
	if err != nil {
		// ufw not installed is a common state — return graceful info.
		writeJSON(w, map[string]any{"installed": false, "error": err.Error(), "output": out})
		return
	}
	enabled := strings.Contains(out, "Status: active")
	writeJSON(w, map[string]any{"installed": true, "enabled": enabled, "output": out})
}

// handleUFWRule adds or removes a firewall rule. Body:
//
//	{ action: "allow"|"deny"|"reject"|"delete", spec: "22/tcp" or "from 10.0.0.0/8" }
//
// The spec is forwarded verbatim to ufw, so the caller is expected to follow
// ufw syntax. Audit is captured. Toggling enable/disable uses action="enable"
// or "disable".
func (r *Router) handleUFWRule(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Action string `json:"action"`
		Spec   string `json:"spec"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.Action == "" {
		writeErr(w, 400, "action required")
		return
	}

	var args []string
	switch body.Action {
	case "enable":
		args = []string{"--force", "enable"}
	case "disable":
		args = []string{"disable"}
	case "allow", "deny", "reject":
		if body.Spec == "" {
			writeErr(w, 400, "spec required")
			return
		}
		// Split on spaces — ufw accepts multi-token specs like "from 1.2.3.4 to any port 22".
		args = append([]string{body.Action}, strings.Fields(body.Spec)...)
	case "delete":
		if body.Spec == "" {
			writeErr(w, 400, "spec required (rule number or full rule)")
			return
		}
		args = append([]string{"--force", "delete"}, strings.Fields(body.Spec)...)
	default:
		writeErr(w, 400, "invalid action")
		return
	}

	out, err := execCmd("ufw", args...)
	r.auditEvent(req, auth.UserFrom(req), "ufw."+body.Action, body.Spec)
	if err != nil {
		writeErr(w, 500, err.Error()+" "+out)
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "output": out})
}
