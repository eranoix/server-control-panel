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

	help("panel_process_uptime_seconds", "gauge", "server-control-panel process uptime in seconds.")
	gauge("panel_process_uptime_seconds", now-r.startTime)

	help("panel_process_memory_bytes", "gauge", "Resident memory of the process (Alloc).")
	gauge("panel_process_memory_bytes", memStats.Alloc)

	help("panel_process_memory_sys_bytes", "gauge", "Total memory reserved from the system.")
	gauge("panel_process_memory_sys_bytes", memStats.Sys)

	help("panel_goroutines", "gauge", "Number of live goroutines.")
	gauge("panel_goroutines", runtime.NumGoroutine())

	help("panel_gc_pause_seconds_total", "counter", "Sum of GC pauses in seconds.")
	gauge("panel_gc_pause_seconds_total", float64(memStats.PauseTotalNs)/1e9)

	help("panel_login_attempts_total", "counter", "Login attempts (successful or failed).")
	counter("panel_login_attempts_total", atomic.LoadInt64(&metricLoginAttempts))

	help("panel_login_failures_total", "counter", "Login failures (bad credentials/lockout).")
	counter("panel_login_failures_total", atomic.LoadInt64(&metricLoginFailures))

	help("panel_audit_events_total", "counter", "Audit log events emitted.")
	counter("panel_audit_events_total", atomic.LoadInt64(&metricAuditEvents))

	help("panel_ws_connections_active", "gauge", "Live WebSockets (PTY, WhatsApp, video call, STT, presence).")
	counter("panel_ws_connections_active", atomic.LoadInt64(&metricWSConnections))

	help("panel_http_requests_total", "counter", "HTTP requests served.")
	counter("panel_http_requests_total", atomic.LoadInt64(&metricHTTPRequests))

	if r.videocall != nil {
		help("panel_videocall_rings_total", "counter", "Video call ring decisions, by reason.")
		stats := videocall.RingStats()
		reasons := make([]string, 0, len(stats))
		for reason := range stats {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		for _, reason := range reasons {
			counter("panel_videocall_rings_total{reason=\""+reason+"\"}", stats[reason])
		}
	}

	help("panel_subsystem_up", "gauge", "1 if the subsystem is operational, 0 otherwise.")
	upVal := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	gauge("panel_subsystem_up{name=\"docker\"}", upVal(r.docker != nil))
	gauge("panel_subsystem_up{name=\"audit\"}", upVal(r.audit != nil))
	gauge("panel_subsystem_up{name=\"secrets\"}", upVal(r.secrets != nil))
	gauge("panel_subsystem_up{name=\"videocall\"}", upVal(r.videocall != nil))
	gauge("panel_subsystem_up{name=\"whatsapp\"}", upVal(r.whatsappMgr != nil))

	_, _ = w.Write([]byte(sb.String()))
}
