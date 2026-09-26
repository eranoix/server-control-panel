package api

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"server-control-panel/internal/videocall"
)

// /metrics Prometheus-compatible text exposition.
//
// We do not import github.com/prometheus/client_golang, to keep the binary small
// — we generate the format by hand (it is simple text). Covers:
//   - vpsm_process_uptime_seconds            (gauge)
//   - vpsm_process_memory_bytes              (gauge)
//   - vpsm_goroutines                        (gauge)
//   - vpsm_login_attempts_total              (counter)
//   - vpsm_login_failures_total              (counter)
//   - vpsm_http_requests_total{method,path}  (counter — top paths)
//   - vpsm_ws_connections_active             (gauge)
//   - vpsm_audit_events_total                (counter)
//   - vpsm_subsystem_up{name}                (gauge — 1/0)
//
// To integrate with Grafana/Prometheus, scrape `/metrics` (or via an
// internal IP/path if it sits behind a reverse proxy).

// Exposed counters & gauges. Use atomic — the scrape's read races with
// hot-path writes. Not persisted — reset on restart.
var (
	metricLoginAttempts int64
	metricLoginFailures int64
	metricAuditEvents   int64
	metricWSConnections int64
	metricHTTPRequests  int64
)

func incMetricLoginAttempt() { atomic.AddInt64(&metricLoginAttempts, 1) }
func incMetricLoginFailure() { atomic.AddInt64(&metricLoginFailures, 1) }
func incMetricAuditEvent()   { atomic.AddInt64(&metricAuditEvents, 1) }
func incMetricWSConn()       { atomic.AddInt64(&metricWSConnections, 1) }
func decMetricWSConn()       { atomic.AddInt64(&metricWSConnections, -1) }
func incMetricHTTPReq()      { atomic.AddInt64(&metricHTTPRequests, 1) }

func (r *Router) handlePrometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var sb strings.Builder
	now := time.Now().Unix()

	help := func(name, htype, desc string) {
		sb.WriteString("# HELP " + name + " " + desc + "\n")
		sb.WriteString("# TYPE " + name + " " + htype + "\n")
	}
	gauge := func(name string, val any) {
		fmt.Fprintf(&sb, "%s %v\n", name, val)
	}
	counter := func(name string, val int64) {
		gauge(name, strconv.FormatInt(val, 10))
	}

	help("vpsm_process_uptime_seconds", "gauge", "vps-manager process uptime in seconds.")
	gauge("vpsm_process_uptime_seconds", now-r.startTime)

	help("vpsm_process_memory_bytes", "gauge", "Resident memory of the process (Alloc).")
	gauge("vpsm_process_memory_bytes", memStats.Alloc)

	help("vpsm_process_memory_sys_bytes", "gauge", "Total memory reserved from the system.")
	gauge("vpsm_process_memory_sys_bytes", memStats.Sys)

	help("vpsm_goroutines", "gauge", "Number of live goroutines.")
	gauge("vpsm_goroutines", runtime.NumGoroutine())

	help("vpsm_gc_pause_seconds_total", "counter", "Sum of GC pauses in seconds.")
	gauge("vpsm_gc_pause_seconds_total", float64(memStats.PauseTotalNs)/1e9)

	help("vpsm_login_attempts_total", "counter", "Login attempts (successful or failed).")
	counter("vpsm_login_attempts_total", atomic.LoadInt64(&metricLoginAttempts))

	help("vpsm_login_failures_total", "counter", "Login failures (bad credentials/lockout).")
	counter("vpsm_login_failures_total", atomic.LoadInt64(&metricLoginFailures))

	help("vpsm_audit_events_total", "counter", "Audit log events emitted.")
	counter("vpsm_audit_events_total", atomic.LoadInt64(&metricAuditEvents))

	help("vpsm_ws_connections_active", "gauge", "Live WebSockets (PTY, WhatsApp, video call, STT, presence).")
	counter("vpsm_ws_connections_active", atomic.LoadInt64(&metricWSConnections))

	help("vpsm_http_requests_total", "counter", "HTTP requests served.")
	counter("vpsm_http_requests_total", atomic.LoadInt64(&metricHTTPRequests))

	// Video-call ringing, broken down by reason. `new-call` is a real
	// ring; `rejoin`/`ongoing`/`resume-hint` are the rings the fix
	// suppressed (before, each of them turned into a phantom notification in the
	// middle of the call). If `new-call` fires with nobody calling, it regressed.
	if r.videocall != nil {
		help("vpsm_videocall_rings_total", "counter", "Video call ring decisions, by reason.")
		stats := videocall.RingStats()
		reasons := make([]string, 0, len(stats))
		for reason := range stats {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			counter("vpsm_videocall_rings_total{reason=\""+reason+"\"}", stats[reason])
		}
	}

	// Subsystem up gauges — 1 se operacional, 0 se desabilitado/degraded.
	help("vpsm_subsystem_up", "gauge", "1 if the subsystem is operational, 0 otherwise.")
	upVal := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	gauge("vpsm_subsystem_up{name=\"docker\"}", upVal(r.docker != nil))
	gauge("vpsm_subsystem_up{name=\"audit\"}", upVal(r.audit != nil))
	gauge("vpsm_subsystem_up{name=\"secrets\"}", upVal(r.secrets != nil))
	gauge("vpsm_subsystem_up{name=\"videocall\"}", upVal(r.videocall != nil))
	gauge("vpsm_subsystem_up{name=\"whatsapp\"}", upVal(r.whatsappMgr != nil))

	_, _ = w.Write([]byte(sb.String()))
}
