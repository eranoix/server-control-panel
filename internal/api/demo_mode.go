package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
)

var (
	demoOnce    sync.Once
	demoEnabled bool
)

func demoMode() bool {
	demoOnce.Do(func() {
		v := strings.TrimSpace(os.Getenv("DEMO_MODE"))
		demoEnabled = v != "" && v != "0" && !strings.EqualFold(v, "false")
	})
	return demoEnabled
}

var demoReadableAPI = []string{
	"/api/health",
	"/api/metrics",
	"/api/system",
	"/api/docker",
	"/api/deploy",
	"/api/scheduler",
	"/api/audit",
	"/api/queue",
	"/api/procs",
	"/api/nodes",
	"/api/proxmox",
	"/api/gameservers",
	"/api/todos",
	"/api/user/prefs",
	"/api/auth/session",
	"/api/auth/me",
}

var demoPublicPrefixes = []string{
	"/assets/", "/vendor/", "/fonts/", "/icon-", "/apple-touch-icon",
	"/favicon", "/manifest.webmanifest", "/sw.js", "/_docs",
	"/tailwind.css", "/telemetry.js",
}

var demoWritablePaths = []string{
	"/api/auth/login",
	"/api/auth/refresh-cookie",
	"/api/auth/logout",
}

var demoDeniedPrefixes = []string{
	"/recovery", "/_code/", "/_port/", "/browser/", "/browser-persistent/",
	"/ws/", "/_internal/", "/api/agent/hook", "/metrics",
}

func hasAnyPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func demoAllows(req *http.Request) bool {
	path := req.URL.Path

	if hasAnyPrefix(path, demoDeniedPrefixes) {
		return false
	}

	if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return false
	}

	switch req.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodPost:
		for _, p := range demoWritablePaths {
			if path == p {
				return true
			}
		}
		return false
	default:
		return false
	}

	if path == "/" || !strings.HasPrefix(path, "/api/") {
		return hasAnyPrefix(path, demoPublicPrefixes) || path == "/" ||
			path == "/android/install" || path == "/.well-known/assetlinks.json"
	}
	return hasAnyPrefix(path, demoReadableAPI)
}

func demoDeny(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Demo-Mode", "read-only")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": "read-only demo",
		"demo":  true,
		"hint":  "This public demo serves fabricated data and refuses writes, terminals and shell access. For the full thing, run it yourself without DEMO_MODE (see 'Running it for real' in the README).",
	})
}
