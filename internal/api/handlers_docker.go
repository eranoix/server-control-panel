package api

// handlers_docker.go — the Docker + Compose engine
//
// Covers: dockerReady, info/disk/containers/images/volumes/networks,
// container actions, compose (file CRUD + actions), prune/pull,
// log/stats streams over WebSocket and wsWriter (the stream helper).
//
// Extracted from api.go. It stays on *Router because it uses r.docker/audit/mustPrimary.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/volume"
	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
)

// ---------- Docker ----------

func (r *Router) dockerReady(w http.ResponseWriter) bool {
	if r.docker == nil {
		writeErr(w, 503, "docker unavailable")
		return false
	}
	return true
}

func (r *Router) handleDockerInfo(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	info, err := r.docker.Info(req.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, info)
}

func (r *Router) handleDiskUsage(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	d, err := r.docker.DiskUsage(req.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, d)
}

func (r *Router) handleContainers(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	list, err := r.docker.ListContainers(req.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, sanitizeList(list, "ID"))
}

func (r *Router) handleContainerAction(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	path := strings.TrimPrefix(req.URL.Path, "/api/docker/containers/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, 400, "container id required")
		return
	}
	id := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	ctx := req.Context()
	var err error
	var result interface{}
	switch action {
	case "", "inspect":
		result, err = r.docker.Inspect(ctx, id)
	case "stats":
		result, err = r.docker.Stats(ctx, id)
	case "top":
		result, err = r.docker.Top(ctx, id)
	case "start":
		err = r.docker.Start(ctx, id)
	case "stop":
		err = r.docker.Stop(ctx, id)
	case "restart":
		err = r.docker.Restart(ctx, id)
	case "kill":
		err = r.docker.Kill(ctx, id)
	case "pause":
		err = r.docker.Pause(ctx, id)
	case "unpause":
		err = r.docker.Unpause(ctx, id)
	case "remove":
		err = r.docker.Remove(ctx, id, req.URL.Query().Get("force") == "1")
	case "logs":
		tail := req.URL.Query().Get("tail")
		if tail == "" {
			tail = "300"
		}
		s, e := r.docker.Logs(ctx, id, tail)
		if e != nil {
			writeErr(w, 500, e.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(s))
		return
	default:
		writeErr(w, 400, "unknown action: "+action)
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if result == nil {
		result = map[string]string{"status": "ok"}
	}
	if action != "" && action != "inspect" && action != "stats" && action != "top" {
		r.auditEvent(req, auth.UserFrom(req), "container."+action, id)
	}
	writeJSON(w, result)
}

func (r *Router) handleImages(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	if req.Method == http.MethodDelete {
		id := req.URL.Query().Get("id")
		if id == "" {
			writeErr(w, 400, "id required")
			return
		}
		if err := r.docker.ImageRemove(req.Context(), id, req.URL.Query().Get("force") == "1"); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "image.remove", id)
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}
	list, err := r.docker.Images(req.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, sanitizeList(list, "ID"))
}

func (r *Router) handleVolumes(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	v, err := r.docker.Volumes(req.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// volume.ListResponse exposes Volumes []*Volume + Warnings []string. The
	// frontend iterates with :key="v.Name" — we sanitise the inner slice without
	// losing Warnings.
	if vr, ok := v.(volume.ListResponse); ok {
		vr.Volumes = sanitizeList(vr.Volumes, "Name").([]*volume.Volume)
		writeJSON(w, vr)
		return
	}
	writeJSON(w, v)
}

func (r *Router) handleNetworks(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	n, err := r.docker.Networks(req.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, sanitizeList(n, "ID"))
}

func (r *Router) handleCompose(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	p, err := r.docker.ListComposeProjects(req.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, sanitizeList(p, "Name"))
}

// handleComposeFile reads/writes a project's compose.yml file.
// GET ?project=<name>&path=<config_file>  → text/plain
// POST {project, path, content}             → 204 (atomic write via tmp+rename)
//
// Validation: path MUST belong to the WorkingDir of the project returned by the
// docker-compose project label. It blocks path traversal and writing outside
// the project.
//
// SECURITY: primary-only, for the same reason as handleComposeAction. POST
// allows writing YAML on the host as root — any "command: rm -rf /" line runs
// on the next compose up. GET returns the raw file, which usually contains
// `environment:` with secrets, references to `.env`, registry tokens, and so on.
// Without that gate, any secondary admin has RCE + a secret leak through the API.
func (r *Router) handleComposeFile(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.dockerReady(w) {
		return
	}
	switch req.Method {
	case http.MethodGet:
		projectName := req.URL.Query().Get("project")
		pathParam := req.URL.Query().Get("path")
		if projectName == "" {
			writeErr(w, 400, "project required")
			return
		}
		file, err := r.resolveComposeFile(req, projectName, pathParam)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		data, err := os.ReadFile(file)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		// Capped at 1MB so as not to take mobile down with enormous YAMLs (very rare).
		if len(data) > 1024*1024 {
			data = data[:1024*1024]
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Compose-File", filepath.Base(file))
		_, _ = w.Write(data)
		return
	case http.MethodPost:
		var body struct {
			Project string `json:"project"`
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if body.Project == "" {
			writeErr(w, 400, "project required")
			return
		}
		// Limit (1MB)
		if len(body.Content) > 1024*1024 {
			writeErr(w, 413, "content too large (>1MB)")
			return
		}
		file, err := r.resolveComposeFile(req, body.Project, body.Path)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		// Atomic write: tmp + rename. Preserves the existing file's permissions.
		mode := os.FileMode(0o644)
		if fi, err := os.Stat(file); err == nil {
			mode = fi.Mode().Perm()
		}
		tmp := file + ".tmp." + strconv.FormatInt(time.Now().UnixNano(), 10)
		if err := os.WriteFile(tmp, []byte(body.Content), mode); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if err := os.Rename(tmp, file); err != nil {
			_ = os.Remove(tmp)
			writeErr(w, 500, err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "compose.file.write", body.Project+":"+filepath.Base(file))
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// resolveComposeFile resolves the safe absolute path of a project's compose
// file. It guarantees the path is inside the known WorkingDir (the compose
// label). Returns an error when path traversal is detected or the project is
// unknown.
func (r *Router) resolveComposeFile(req *http.Request, projectName, pathParam string) (string, error) {
	projects, err := r.docker.ListComposeProjects(req.Context())
	if err != nil {
		return "", err
	}
	var workingDir, configFiles string
	for _, p := range projects {
		if p.Name == projectName {
			workingDir = p.WorkingDir
			configFiles = p.ConfigFiles
			break
		}
	}
	if workingDir == "" {
		return "", errors.New("project not found or has no working_dir label")
	}
	// Resolve symlinks + clean, as defence in depth against ../ hopping.
	wd, err := filepath.Abs(workingDir)
	if err != nil {
		return "", err
	}
	var candidate string
	if pathParam != "" {
		// If the path is absolute, require it to be inside the WorkingDir.
		// If it is relative, join it with the WorkingDir.
		if filepath.IsAbs(pathParam) {
			candidate = pathParam
		} else {
			candidate = filepath.Join(wd, pathParam)
		}
	} else {
		// Default: the first config_files label, or docker-compose.yml inside the WorkingDir.
		if configFiles != "" {
			// configFiles may hold several separated by ","; take the first.
			parts := strings.SplitN(configFiles, ",", 2)
			candidate = strings.TrimSpace(parts[0])
		}
		if candidate == "" {
			candidate = filepath.Join(wd, "docker-compose.yml")
			if _, err := os.Stat(candidate); err != nil {
				candidate = filepath.Join(wd, "compose.yml")
			}
		}
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	// Boundary check: abs MUST sit under wd (after Clean). Blocks traversal.
	rel, err := filepath.Rel(wd, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("path outside project working_dir")
	}
	// Only valid extensions are accepted
	ext := strings.ToLower(filepath.Ext(abs))
	if ext != ".yml" && ext != ".yaml" {
		return "", errors.New("only .yml/.yaml files supported")
	}
	return abs, nil
}

func (r *Router) handleComposeAction(w http.ResponseWriter, req *http.Request) {
	// SECURITY: primary-only — working_dir is literally any absolute path on the
	// host. A non-primary user could point at /etc/cron.daily (or any dir with a
	// docker-compose.yml) and run compose up/down and the rest as root
	// (vps-manager). Without the gate, any authenticated login effectively has
	// RCE through the path.
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.dockerReady(w) {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	var body struct {
		Project    string `json:"project"`
		WorkingDir string `json:"working_dir"`
		Action     string `json:"action"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	out, err := r.docker.ComposeAction(body.Project, body.WorkingDir, body.Action)
	resp := map[string]any{"output": out, "ok": err == nil}
	if err != nil {
		resp["error"] = err.Error()
	}
	r.auditEvent(req, auth.UserFrom(req), "compose."+body.Action, body.Project)
	writeJSON(w, resp)
}

func (r *Router) handlePrune(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	if !r.dockerReady(w) {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	kind := req.URL.Query().Get("kind")
	ctx := req.Context()
	var result interface{}
	var err error
	switch kind {
	case "containers":
		result, err = r.docker.PruneContainers(ctx)
	case "images":
		result, err = r.docker.PruneImages(ctx)
	case "volumes":
		result, err = r.docker.PruneVolumes(ctx)
	case "networks":
		result, err = r.docker.PruneNetworks(ctx)
	case "build":
		result, err = r.docker.PruneBuildCache(ctx)
	case "all", "":
		result, err = r.docker.PruneAll(ctx)
	default:
		writeErr(w, 400, "unknown kind")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "prune."+kind, "")
	writeJSON(w, result)
}

func (r *Router) handlePull(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	ref := req.URL.Query().Get("ref")
	if ref == "" {
		writeErr(w, 400, "ref required")
		return
	}
	if !pullRefAllowed(ref) {
		writeErr(w, 400, "image ref not in allowlist (docker.io, ghcr.io, quay.io, lscr.io, registry.k8s.io, mcr.microsoft.com)")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	err := r.docker.PullImage(req.Context(), ref, func(line string) {
		_, _ = io.WriteString(w, line+"\n")
		if flusher != nil {
			flusher.Flush()
		}
	})
	if err != nil {
		_, _ = io.WriteString(w, `{"error":"`+err.Error()+`"}`+"\n")
	}
	r.auditEvent(req, auth.UserFrom(req), "image.pull", ref)
}

// A keepalive identical to pty.go's: it survives proxies/firewalls with 30s idle
// and detects dead clients at the TCP level. Without it, half-open WS
// connections leak goroutines until the kernel's keepalive fires (hours).
const (
	dockerWsPingPeriod = 25 * time.Second
	dockerWsPongWait   = 45 * time.Second
	dockerWsWriteWait  = 10 * time.Second
)

// runDockerStream is the pattern shared by handleLogStream/handleStatsStream:
// the WS upgrade, a ctx tied to the request, a ping/pong heartbeat, and errors
// propagated to the log + the client. stream() wraps the r.docker.StreamX call.
func (r *Router) runDockerStream(w http.ResponseWriter, req *http.Request, label, id string, stream func(context.Context, *wsWriter) error) {
	conn, err := wsUpgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	_ = conn.SetReadDeadline(time.Now().Add(dockerWsPongWait))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(dockerWsPongWait))
		return nil
	})

	pw := &wsWriter{conn: conn}

	// Read pump: detects the client closing; the end of the pump cancels the ctx.
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Ping pump: keeps the connection alive behind idle proxies and detects a
	// dead client at the TCP level (a write error becomes a cancel).
	go func() {
		t := time.NewTicker(dockerWsPingPeriod)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pw.mu.Lock()
				_ = conn.SetWriteDeadline(time.Now().Add(dockerWsWriteWait))
				err := conn.WriteMessage(websocket.PingMessage, nil)
				pw.mu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	if err := stream(ctx, pw); err != nil && ctx.Err() == nil {
		log.Printf("%s %s: %v", label, id, err)
		pw.mu.Lock()
		_ = conn.SetWriteDeadline(time.Now().Add(dockerWsWriteWait))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"stream interrupted"}`))
		pw.mu.Unlock()
	}
}

func (r *Router) handleLogStream(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/ws/logs/")
	if id == "" {
		writeErr(w, 400, "id required")
		return
	}
	r.runDockerStream(w, req, "StreamLogs", id, func(ctx context.Context, pw *wsWriter) error {
		return r.docker.StreamLogs(ctx, id, pw, "300")
	})
}

func (r *Router) handleStatsStream(w http.ResponseWriter, req *http.Request) {
	if !r.dockerReady(w) {
		return
	}
	id := strings.TrimPrefix(req.URL.Path, "/ws/stats/")
	if id == "" {
		writeErr(w, 400, "id required")
		return
	}
	r.runDockerStream(w, req, "StreamStats", id, func(ctx context.Context, pw *wsWriter) error {
		return r.docker.StreamStats(ctx, id, pw)
	})
}

type wsWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.TextMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}
