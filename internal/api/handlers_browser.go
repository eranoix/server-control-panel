package api

// handlers_browser.go — the web browser (Ultraviolet/Wisp) + the persistent
// browser + per-session bandwidth tracking.
//
// Covers:
//   - browserProxy (reverse proxy to Ultraviolet+Wisp on :8090)
//   - browserPersistentProxy + browserInstancesPath / For + browserInstance
//     + lookupBrowserInstancePort + handleBrowserInstances / Action
//     + isSafeInstanceName + browserState / Resize + browserInstanceEnvPath
//     / ComposePath + readVNCResolutionFromEnv + writeBrowserEnv
//     + dockerComposeRecreate + waitBrowserHealthy
//   - handleSessionBandwidth / Reset (usage per jti via httpmw.BWSnapshot)
//
// Extracted from api.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/httpmw"
)

// browserProxy reverse-proxies /browser/* to the local Ultraviolet+Wisp Node
// service on 127.0.0.1:8090, stripping the /browser prefix. It transparently
// supports the WebSocket upgrade used by the Wisp transport.
func browserProxy() http.Handler {
	target, _ := url.Parse("http://127.0.0.1:8090")
	proxy := httputil.NewSingleHostReverseProxy(target)
	base := proxy.Director
	proxy.Director = func(req *http.Request) {
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/browser")
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		base(req)
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "browser unavailable: "+err.Error(), http.StatusBadGateway)
	}
	return proxy
}

// browserPersistentProxy reverse-proxies /browser-persistent/<instance>/* to
// the noVNC server of the matching instance. The port is discovered through
// <DataDir>/users/<user>/browser-instances.json (per-user after the v1→v2
// migration). It keeps compatibility: /browser-persistent/* with no instance
// name goes to "default".
//
// Each instance runs on its own port (default=6901, the next ones from 6902...)
// and has its own volume → cookies and sessions isolated per service (whatsapp,
// gmail, and so on).
//
// FlushInterval=-1 keeps the framebuffer WebSocket (binary) unbuffered.
//
// A critical HTTP handler — do not remove it without updating the tests in api_browser_test.go.
func (r *Router) browserPersistentProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Expected path: /browser-persistent/<instance>/... or /browser-persistent/...
		// (no instance = default).
		rest := strings.TrimPrefix(req.URL.Path, "/browser-persistent")
		rest = strings.TrimPrefix(rest, "/")
		instance := "default"
		var upstreamPath string
		if rest == "" {
			upstreamPath = "/"
		} else {
			parts := strings.SplitN(rest, "/", 2)
			candidate := parts[0]
			// FIX security: validate the name BEFORE lookupBrowserInstancePort —
			// otherwise path-traversal characters in the name slipped past the
			// lookup (which only looks for a literal match, but the segment still
			// became part of upstreamPath). We now reject early.
			if isSafeInstanceName(candidate) {
				if port := r.lookupBrowserInstancePort(req, candidate); port > 0 {
					instance = candidate
					if len(parts) > 1 {
						upstreamPath = "/" + parts[1]
					} else {
						upstreamPath = "/"
					}
				} else {
					upstreamPath = "/" + rest
				}
			} else {
				// Unsafe candidate: treat it as a path of the default instance.
				// Block paths with `..` in the final upstream path.
				upstreamPath = "/" + rest
			}
		}
		// Final sanity check on upstreamPath. If it contains `..`, reject.
		if strings.Contains(upstreamPath, "..") {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		port := r.lookupBrowserInstancePort(req, instance)
		if port == 0 {
			http.Error(w, "persistent browser: instance '"+instance+"' is not configured", http.StatusNotFound)
			return
		}
		target, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.FlushInterval = -1
		base := proxy.Director
		proxy.Director = func(rq *http.Request) {
			rq.URL.Path = upstreamPath
			base(rq)
		}
		// Isolation headers — defence in depth against a malicious site browsed
		// inside Vivaldi (the iframe) trying to attack the parent.
		// COOP=same-origin: the window can only exchange postMessage same-origin.
		// COEP=require-corp: subresources must declare CORP — this blocks leaks.
		// The parent does not use SharedArrayBuffer so we need not keep the
		// iframe cross-origin-isolated, but COOP prevents Spectre-style attacks
		// that need window references between windows.
		proxy.ModifyResponse = func(resp *http.Response) error {
			// Only override when upstream did not set it — avoids clashing with noVNC.
			h := resp.Header
			if h.Get("Cross-Origin-Resource-Policy") == "" {
				h.Set("Cross-Origin-Resource-Policy", "same-origin")
			}
			if h.Get("X-Frame-Options") == "" {
				h.Set("X-Frame-Options", "SAMEORIGIN")
			}
			if h.Get("Referrer-Policy") == "" {
				h.Set("Referrer-Policy", "no-referrer")
			}
			return nil
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "persistent browser unavailable: "+err.Error(), http.StatusBadGateway)
		}
		proxy.ServeHTTP(w, req)
	})
}

// browserInstancesPath returns the per-user file path, or "" when the request
// is not authenticated (in production that only happens if somebody removes
// auth.Middleware from the route; the caller treats "" as "no instances").
func (r *Router) browserInstancesPath(req *http.Request) string {
	user := auth.UserFrom(req)
	if user == "" {
		return ""
	}
	return filepath.Join(r.cfg.DataDir, "users", user, "browser-instances.json")
}

// browserInstancesFor loads the instances declared by the request's user. It
// reads the file on every call (it is small and rarely touched).
func (r *Router) browserInstancesFor(req *http.Request) []browserInstance {
	path := r.browserInstancesPath(req)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg struct {
		Instances []browserInstance `json:"instances"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	return cfg.Instances
}

// lookupBrowserInstancePort returns an instance's port by name, within the
// scope of the request's user. Returns 0 when it does not exist.
func (r *Router) lookupBrowserInstancePort(req *http.Request, name string) int {
	for _, inst := range r.browserInstancesFor(req) {
		if inst.Name == name {
			return inst.Port
		}
	}
	return 0
}

type browserInstance struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Port        int    `json:"port"`
	Description string `json:"description,omitempty"`
}

func (r *Router) handleBrowserInstances(w http.ResponseWriter, req *http.Request) {
	insts := r.browserInstancesFor(req)
	writeJSON(w, map[string]any{"instances": sanitizeList(insts, "Name")})
}

// handleBrowserInstanceAction dispatches the browser instance's sub-endpoints:
//   - GET  /api/browser-instances/{name}/state  → current resolution + running
//   - POST /api/browser-instances/{name}/resize → sets VNC_RESOLUTION, recreates it
//
// Expected layout on disk: /opt/browser-instances/{name}/docker-compose.yml.
// The docker-compose.yml must reference ${VNC_RESOLUTION:-1920x1080} (otherwise
// the value in .env is ignored).
func (r *Router) handleBrowserInstanceAction(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimPrefix(req.URL.Path, "/api/browser-instances/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		writeErr(w, 404, "not found")
		return
	}
	name, action := parts[0], parts[1]
	if !isSafeInstanceName(name) {
		writeErr(w, 400, "invalid instance name")
		return
	}
	if r.lookupBrowserInstancePort(req, name) == 0 {
		writeErr(w, 404, "unknown instance")
		return
	}
	switch action {
	case "state":
		if req.Method != http.MethodGet {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.browserState(w, req, name)
	case "resize":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.browserResize(w, req, name)
	default:
		writeErr(w, 404, "unknown action")
	}
}

func isSafeInstanceName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, c := range s {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// browserComposeDir is the canonical location of the per-instance compose files.
const browserComposeDir = "/opt/browser-instances"

func browserInstanceEnvPath(name string) string {
	return filepath.Join(browserComposeDir, name, ".env")
}

func browserInstanceComposePath(name string) string {
	return filepath.Join(browserComposeDir, name, "docker-compose.yml")
}

// readVNCResolutionFromEnv reads VNC_RESOLUTION from the instance's .env.
// Returns "" when the file does not exist or the key is absent.
func readVNCResolutionFromEnv(name string) string {
	data, err := os.ReadFile(browserInstanceEnvPath(name))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "VNC_RESOLUTION=") {
			v := strings.TrimPrefix(line, "VNC_RESOLUTION=")
			return strings.Trim(v, "\"'")
		}
	}
	return ""
}

func (r *Router) browserState(w http.ResponseWriter, req *http.Request, name string) {
	res := readVNCResolutionFromEnv(name)
	if res == "" {
		// .env missing, or without the key: assume the default declared in the docker-compose.
		res = "1920x1080"
	}
	port := r.lookupBrowserInstancePort(req, name)
	running := false
	if port > 0 {
		// Quick probe (200ms) — success means noVNC is serving.
		client := &http.Client{Timeout: 400 * time.Millisecond}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err == nil {
			resp.Body.Close()
			running = resp.StatusCode < 500
		}
	}
	writeJSON(w, map[string]any{
		"name":       name,
		"resolution": res,
		"running":    running,
	})
}

func (r *Router) browserResize(w http.ResponseWriter, req *http.Request, name string) {
	var body struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.Width < 240 || body.Width > 3840 || body.Height < 320 || body.Height > 2160 {
		writeErr(w, 400, "resolution out of range (W:240-3840, H:320-2160)")
		return
	}
	// Vivaldi/Xvnc needs multiples of 2 — round to an even number.
	body.Width &^= 1
	body.Height &^= 1
	newRes := fmt.Sprintf("%dx%d", body.Width, body.Height)
	current := readVNCResolutionFromEnv(name)
	if current == newRes {
		// No change: just answer with the current state.
		writeJSON(w, map[string]any{
			"ok":         true,
			"resolution": newRes,
			"recreated":  false,
		})
		return
	}
	if err := writeBrowserEnv(name, newRes); err != nil {
		writeErr(w, 500, "failed to write .env: "+err.Error())
		return
	}
	if err := dockerComposeRecreate(name); err != nil {
		writeErr(w, 500, "docker compose failed: "+err.Error())
		return
	}
	if err := r.waitBrowserHealthy(req, name, 25*time.Second); err != nil {
		writeErr(w, 504, "container did not become healthy in time: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"ok":         true,
		"resolution": newRes,
		"recreated":  true,
	})
}

// writeBrowserEnv atomically writes VNC_RESOLUTION=<res> into .env, preserving
// the other keys that are already there.
func writeBrowserEnv(name, res string) error {
	dir := filepath.Join(browserComposeDir, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("instance dir not found: %w", err)
	}
	envPath := browserInstanceEnvPath(name)
	existing, _ := os.ReadFile(envPath)
	lines := strings.Split(string(existing), "\n")
	out := make([]string, 0, len(lines)+1)
	replaced := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "VNC_RESOLUTION=") {
			out = append(out, "VNC_RESOLUTION="+res)
			replaced = true
			continue
		}
		if line != "" || trim == "" && len(out) > 0 {
			out = append(out, line)
		}
	}
	if !replaced {
		out = append(out, "VNC_RESOLUTION="+res)
	}
	final := strings.Join(out, "\n")
	if !strings.HasSuffix(final, "\n") {
		final += "\n"
	}
	tmp := envPath + ".new"
	if err := os.WriteFile(tmp, []byte(final), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, envPath)
}

func dockerComposeRecreate(name string) error {
	composePath := browserInstanceComposePath(name)
	if _, err := os.Stat(composePath); err != nil {
		return fmt.Errorf("compose file not found: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", composePath,
		"up", "-d", "--force-recreate", "--no-build")
	cmd.Env = append(os.Environ(), "DOCKER_CLI_HINTS=false")
	out, err := cmd.CombinedOutput()
	if err != nil {
		snippet := strings.TrimSpace(string(out))
		if len(snippet) > 400 {
			snippet = snippet[:400] + "…"
		}
		return fmt.Errorf("%w: %s", err, snippet)
	}
	return nil
}

func (r *Router) waitBrowserHealthy(req *http.Request, name string, timeout time.Duration) error {
	port := r.lookupBrowserInstancePort(req, name)
	if port == 0 {
		return fmt.Errorf("instance port unknown")
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

func (r *Router) handleSessionBandwidth(w http.ResponseWriter, req *http.Request) {
	jti := auth.JTIFrom(req)
	s := httpmw.BWSnapshot(jti)
	writeJSON(w, map[string]any{
		"bytes_in":  s.BytesIn,
		"bytes_out": s.BytesOut,
		"since":     s.Since,
	})
}

func (r *Router) handleSessionBandwidthReset(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	jti := auth.JTIFrom(req)
	httpmw.BWReset(jti)
	writeJSON(w, map[string]any{"ok": true})
}
