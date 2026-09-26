package api

// Demo mode — the public demo of this control plane.
//
// This file exists because the product it guards is a root-level control plane:
// it allocates PTYs, execs into containers, proxies an IDE and exposes a
// recovery console. Publishing a live instance of that without a gate is
// handing a shell to the internet.
//
// The gate is an allowlist, and it sits in Router.ServeHTTP — the one point
// every request passes through. That placement is the whole design. A denylist
// applied per route would be the obvious mistake: 266 API paths are registered
// today, and the first one added after this file was written would arrive
// unguarded. Here, a new route is denied until someone deliberately adds it
// below.
//
// Three independent rules, each sufficient on its own for the class it covers:
//
//   1. Method. Only GET and HEAD, plus the three POSTs the session needs.
//      Structural: it covers every mutating route that exists or will exist.
//   2. Upgrade. Any request asking to become a WebSocket is refused, whatever
//      the path. Terminals, container attach and audio streams all ride that
//      upgrade, so refusing it closes the interactive surface as a class.
//   3. Path. Reads are allowlisted by family. Needed because some GETs are
//      dangerous on their own — reading the secret vault, or the IDE proxy,
//      which embeds a terminal of its own.
//
// Demo mode is off unless DEMO_MODE is set, so a normal deployment never pays
// for any of this.

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

// demoMode reports whether this process is serving the public demo.
// Read once: the answer cannot change during a process lifetime, and checking
// the environment on every request would be a syscall in the hot path.
func demoMode() bool {
	demoOnce.Do(func() {
		v := strings.TrimSpace(os.Getenv("DEMO_MODE"))
		demoEnabled = v != "" && v != "0" && !strings.EqualFold(v, "false")
	})
	return demoEnabled
}

// demoReadableAPI lists the API families a visitor may read.
//
// Everything absent is denied. The list is deliberately short: it covers the
// panels the demo seeds with fabricated data, and nothing else. Families left
// out on purpose, with the reason, so a future reader does not "fix" them:
//
//	/api/terminal   allocates PTYs
//	/api/claude     agent control and provider account state
//	/api/users      account management; enumerates real operators
//	/api/backup     can read and write archives of the data directory
//	/api/admin      secret vault
//	/api/session    session tokens
//	/api/notify     channel configuration, which carries push credentials
//
// The remaining absentees (jira, videocall, whatsapp, datasaver, tunnel,
// private-ai, adguard, browser-instances, ai) are subsystems the demo does not
// configure. Denying them here keeps the allowlist honest about what the demo
// actually shows.
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

// demoPublicPrefixes are non-API paths a visitor may fetch: the single-page
// app, its assets, the PWA plumbing and the architecture write-up.
var demoPublicPrefixes = []string{
	"/assets/", "/vendor/", "/fonts/", "/icon-", "/apple-touch-icon",
	"/favicon", "/manifest.webmanifest", "/sw.js", "/_docs",
	// Root-level files the page itself loads. Without the stylesheet the whole
	// demo rendered unstyled, and nothing failed: the gate answered 403 with a
	// polite JSON body and the browser simply drew the page without Tailwind.
	"/tailwind.css", "/telemetry.js",
}

// demoWritablePaths are the only non-idempotent requests allowed: without them
// nobody could log in, and a demo you cannot enter is not a demo.
var demoWritablePaths = []string{
	"/api/auth/login",
	"/api/auth/refresh-cookie",
	"/api/auth/logout",
}

// demoDeniedPrefixes are refused before anything else. Each one reaches a
// shell: the recovery console, the IDE proxy (which has an integrated
// terminal), the port forwarder (which reaches any local service) and the
// tunnelled browser (which reaches the network the host sits on).
//
// The method and upgrade rules would already stop most of this. They are listed
// anyway, because a path this dangerous should not depend on a second rule
// holding.
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

// demoAllows applies the three rules, in order of bluntness.
func demoAllows(req *http.Request) bool {
	path := req.URL.Path

	// Rule 3a: paths that reach a shell, refused regardless of anything else.
	if hasAnyPrefix(path, demoDeniedPrefixes) {
		return false
	}

	// Rule 2: anything asking to become a WebSocket. Checked by header rather
	// than by path, so it also covers upgrades on paths not listed above.
	if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return false
	}

	// Rule 1: method. The narrow exception is logging in.
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

	// Rule 3b: reads. The SPA itself and its assets are public.
	if path == "/" || !strings.HasPrefix(path, "/api/") {
		return hasAnyPrefix(path, demoPublicPrefixes) || path == "/" ||
			path == "/android/install" || path == "/.well-known/assetlinks.json"
	}
	return hasAnyPrefix(path, demoReadableAPI)
}

// demoDeny answers a refused request.
//
// It returns 403 with a machine-readable body rather than closing the
// connection, so the front end can render "read-only demo" where a panel would
// have been instead of showing a generic failure. A demo that looks broken
// reads as a broken product.
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
