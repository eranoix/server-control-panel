package api

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

func (r *Router) browserPersistentProxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rest := strings.TrimPrefix(req.URL.Path, "/browser-persistent")
		rest = strings.TrimPrefix(rest, "/")
		instance := "default"
		var upstreamPath string
		if rest == "" {
			upstreamPath = "/"
		} else {
			parts := strings.SplitN(rest, "/", 2)
			candidate := parts[0]
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
				upstreamPath = "/" + rest
			}
		}
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
		proxy.ModifyResponse = func(resp *http.Response) error {
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

func (r *Router) browserInstancesPath(req *http.Request) string {
	user := auth.UserFrom(req)
	if user == "" {
		return ""
	}
	return filepath.Join(r.cfg.DataDir, "users", user, "browser-instances.json")
}

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

const browserComposeDir = "/opt/browser-instances"

func browserInstanceEnvPath(name string) string {
	return filepath.Join(browserComposeDir, name, ".env")
}

func browserInstanceComposePath(name string) string {
	return filepath.Join(browserComposeDir, name, "docker-compose.yml")
}

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
		res = "1920x1080"
	}
	port := r.lookupBrowserInstancePort(req, name)
	running := false
	if port > 0 {
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
	body.Width &^= 1
	body.Height &^= 1
	newRes := fmt.Sprintf("%dx%d", body.Width, body.Height)
	current := readVNCResolutionFromEnv(name)
	if current == newRes {
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
