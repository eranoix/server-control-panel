package whatsapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"text/template"
	"time"

	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
)

const composeTemplatePath = "/opt/panel/scripts/whatsapp/docker-compose.tmpl.yml"

type ManagerOptions struct {
	DataDir string

	ContainerRoot string

	Vault *secrets.Store

	SelfBaseURL string
}

type Manager struct {
	opts     ManagerOptions
	registry *Registry

	mu       sync.RWMutex
	services map[scope.User]*Service
}

func NewManager(opts ManagerOptions) (*Manager, error) {
	if opts.DataDir == "" {
		return nil, errors.New("whatsapp manager: DataDir required")
	}
	if opts.ContainerRoot == "" {
		opts.ContainerRoot = "/var/lib/panel-whatsapp"
	}
	if opts.Vault == nil {
		return nil, errors.New("whatsapp manager: Vault required")
	}
	reg, err := NewRegistry(opts.ContainerRoot)
	if err != nil {
		return nil, fmt.Errorf("whatsapp manager registry: %w", err)
	}
	return &Manager{
		opts:     opts,
		registry: reg,
		services: map[scope.User]*Service{},
	}, nil
}

func (m *Manager) Registry() *Registry { return m.registry }

func (m *Manager) ForUser(u scope.User) (*Service, error) {
	m.mu.RLock()
	if svc, ok := m.services[u]; ok {
		m.mu.RUnlock()
		return svc, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if svc, ok := m.services[u]; ok {
		return svc, nil
	}
	svc, err := m.buildService(u)
	if err != nil {
		return nil, err
	}
	m.services[u] = svc
	return svc, nil
}

func (m *Manager) buildService(u scope.User) (*Service, error) {
	paths := scope.PathsFor(m.opts.DataDir, u)
	uv := scope.NewUserVault(m.opts.Vault, u)
	apiKey, ok1 := uv.Get("waha_api_key")
	hmacSec, ok2 := uv.Get("waha_hmac_secret")
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("whatsapp: vault keys missing for user %s (provision first)", u)
	}
	port, ok := m.registry.Lookup(u.String())
	if !ok {
		return nil, fmt.Errorf("whatsapp: no port allocated for user %s (provision first)", u)
	}
	opts := Options{
		StoreRoot:   paths.Whatsapp,
		MediaRoot:   paths.WhatsappMedia,
		GowsDBPath:  filepath.Join(paths.WhatsappContainer, "sessions/gows/default/gows.db"),
		WAHABaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		WAHAAPIKey:  apiKey,
		HMACSecret:  hmacSec,
		HMACRefresh: func() string {
			v, _ := scope.NewUserVault(m.opts.Vault, u).Get("waha_hmac_secret")
			return v
		},
		ServiceUnit: fmt.Sprintf("panel-whatsapp@%s.service", u.String()),
	}
	if m.opts.SelfBaseURL != "" {
		opts.ExtraWebhookURL = strings.TrimRight(m.opts.SelfBaseURL, "/") + "/api/whatsapp/webhook/" + u.String()
	}
	if mc := maybeMeowBackend(u); mc != nil {
		opts.Backend = mc
		opts.GowsDBPath = filepath.Join(wadStateDir(), u.String(), "session.db")
	}
	return New(opts)
}

func wadStateDir() string {
	if v := os.Getenv("WAD_STATE_DIR"); v != "" {
		return v
	}
	if testing.Testing() {
		return filepath.Join(os.TempDir(), "panel-wad-test")
	}
	return "/var/lib/panel-wad"
}
func wadBaseURL() string {
	if v := os.Getenv("WAD_BASE_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:8769"
}

func maybeMeowBackend(u scope.User) Backend {
	dir := filepath.Join(wadStateDir(), u.String())
	if _, err := os.Stat(filepath.Join(dir, "enabled")); err != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		log.Printf("whatsapp: wad enabled for %s but meta.json is unreadable: %v", u, err)
		return nil
	}
	var meta struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.APIKey == "" {
		log.Printf("whatsapp: invalid wad meta.json for %s", u)
		return nil
	}
	log.Printf("whatsapp: user %s is using the whatsmeow backend (daemon %s)", u, wadBaseURL())
	return newMeowClient(wadBaseURL(), u.String(), meta.APIKey)
}

func (m *Manager) Has(u scope.User) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.services[u]
	return ok
}

func (m *Manager) LookupRunning(u scope.User) *Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.services[u]
}

func (m *Manager) Provision(u scope.User) error {
	paths := scope.PathsFor(m.opts.DataDir, u)
	if err := scope.EnsureDirs(paths); err != nil {
		return fmt.Errorf("provision dirs: %w", err)
	}
	if err := scope.EnsureWhatsappContainerDirs(paths); err != nil {
		return fmt.Errorf("provision container dirs: %w", err)
	}
	port, err := m.registry.Alloc(u.String())
	if err != nil {
		return fmt.Errorf("provision port: %w", err)
	}
	uv := scope.NewUserVault(m.opts.Vault, u)
	if _, ok := uv.Get("waha_api_key"); !ok {
		key, err := randomHex32()
		if err != nil {
			return fmt.Errorf("provision waha_api_key: %w", err)
		}
		if err := uv.Set("waha_api_key", key); err != nil {
			return fmt.Errorf("provision vault waha_api_key: %w", err)
		}
	}
	if _, ok := uv.Get("waha_hmac_secret"); !ok {
		key, err := randomHex32()
		if err != nil {
			return fmt.Errorf("provision waha_hmac_secret: %w", err)
		}
		if err := uv.Set("waha_hmac_secret", key); err != nil {
			return fmt.Errorf("provision vault waha_hmac_secret: %w", err)
		}
	}
	_ = port
	if err := m.provisionWad(u); err != nil {
		return fmt.Errorf("provision wad: %w", err)
	}
	return nil
}

func (m *Manager) provisionWad(u scope.User) error {
	uv := scope.NewUserVault(m.opts.Vault, u)
	hmacSec, okHmac := uv.Get("waha_hmac_secret")
	if !okHmac || hmacSec == "" {
		return fmt.Errorf("waha_hmac_secret missing/empty in the vault of %s — "+
			"writing meta.json without it would make the daemon lose every incoming message", u)
	}
	apiKey, ok := uv.Get("wad_api_key")
	if !ok || apiKey == "" {
		k, err := randomHex32()
		if err != nil {
			return err
		}
		apiKey = k
		_ = uv.Set("wad_api_key", apiKey)
	}
	dir := filepath.Join(wadStateDir(), u.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]string{"hmac_secret": hmacSec, "api_key": apiKey})
	metaPath := filepath.Join(dir, "meta.json")
	prev, _ := os.ReadFile(metaPath)
	changed := !bytes.Equal(bytes.TrimSpace(prev), bytes.TrimSpace(meta))
	if err := writeFileAtomic(metaPath, meta, 0o600); err != nil {
		return err
	}
	dbPath := filepath.Join(dir, "session.db")
	if _, err := os.Stat(dbPath); err != nil {
		if f, e := os.OpenFile(dbPath, os.O_CREATE|os.O_WRONLY, 0o600); e == nil {
			_ = f.Close()
		}
	}
	enabledPath := filepath.Join(dir, "enabled")
	if _, err := os.Stat(enabledPath); err != nil {
		changed = true
	}
	if err := writeFileAtomic(enabledPath, []byte("1\n"), 0o600); err != nil {
		return err
	}
	if !changed {
		return nil
	}
	restartWadDaemon()
	return nil
}

var restartWadDaemon = func() {
	if testing.Testing() {
		return
	}
	if _, err := os.Stat("/usr/bin/systemctl"); err != nil {
		return
	}
	_, _ = exec.Command("/usr/bin/systemctl", "restart", "panel-wad.service").CombinedOutput()
}

func (m *Manager) renderCompose(u scope.User, port int) error {
	tmpl, err := template.ParseFiles(composeTemplatePath)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{
		"User": u.String(),
		"Port": port,
	}); err != nil {
		return fmt.Errorf("exec template: %w", err)
	}
	dst := filepath.Join(m.opts.ContainerRoot, u.String(), "docker-compose.yml")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(dst, buf.Bytes(), 0o644)
}

func (m *Manager) renderEnvFile(u scope.User, port int) error {
	uv := scope.NewUserVault(m.opts.Vault, u)
	apiKey, _ := uv.Get("waha_api_key")
	hmacSec, _ := uv.Get("waha_hmac_secret")
	body := fmt.Sprintf("WAHA_API_KEY=%s\nWAHA_HMAC_SECRET=%s\nPORT=%d\n", apiKey, hmacSec, port)
	dst := filepath.Join(m.opts.ContainerRoot, u.String(), ".env")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(dst, []byte(body), 0o600)
}

func (m *Manager) WarmUp(u scope.User) {
	if _, err := os.Stat("/usr/bin/systemctl"); err != nil {
		return
	}
	if maybeMeowBackend(u) != nil {
		return
	}
	unit := fmt.Sprintf("panel-whatsapp@%s.service", u.String())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/bin/systemctl", "start", unit).CombinedOutput()
		if err != nil {
			log.Printf("whatsapp warmup %s: %v: %s", unit, err, strings.TrimSpace(string(out)))
		}
	}()
}

func (m *Manager) Decommission(u scope.User) error {
	m.mu.Lock()
	if svc := m.services[u]; svc != nil {
		svc.Close()
		delete(m.services, u)
	}
	m.mu.Unlock()

	if _, err := os.Stat("/usr/bin/systemctl"); err == nil {
		unit := fmt.Sprintf("panel-whatsapp@%s.service", u.String())
		_, _ = exec.Command("/usr/bin/systemctl", "stop", unit).CombinedOutput()
		_, _ = exec.Command("/usr/bin/systemctl", "disable", unit).CombinedOutput()
	}

	_ = m.registry.Release(u.String())

	uv := scope.NewUserVault(m.opts.Vault, u)
	for _, k := range uv.List() {
		_ = uv.Delete(k)
	}

	return nil
}

func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func randomHex32() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (m *Manager) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/whatsapp/webhook/")
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		http.Error(w, "missing user in path", http.StatusNotFound)
		return
	}
	u, err := scope.New(rest)
	if err != nil {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	svc, err := m.ForUser(u)
	if err != nil {
		log.Printf("whatsapp webhook for %s: %v", u, err)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	svc.HandleWebhook(w, r)
}

func (m *Manager) ProtectedHandler(audit func(*http.Request, string, string), userFn func(*http.Request) (scope.User, bool)) http.Handler {
	if audit == nil {
		audit = func(*http.Request, string, string) {}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := userFn(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		svc, err := m.ForUser(u)
		if err != nil {
			log.Printf("whatsapp protected for %s: %v", u, err)
			http.Error(w, "whatsapp service not ready", http.StatusServiceUnavailable)
			return
		}
		svc.protectedMuxOnce.Do(func() {
			svc.protectedMux = svc.BuildProtectedMux(audit)
		})
		svc.protectedMux.ServeHTTP(w, r)
	})
}

func (m *Manager) AvatarHandler(userFn func(*http.Request) (scope.User, bool)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := userFn(r)
		if !ok {
			http.NotFound(w, r)
			return
		}
		svc, err := m.ForUser(u)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		svc.handleAvatar(w, r)
	})
}

func DaemonEnabled(users []scope.User) bool {
	for _, u := range users {
		if _, err := os.Stat(filepath.Join(wadStateDir(), u.String(), "enabled")); err == nil {
			return true
		}
	}
	return false
}

var daemonProbeClient = &http.Client{Timeout: 3 * time.Second}

func DaemonAlive(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wadBaseURL()+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := daemonProbeClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wad /healthz: HTTP %d", resp.StatusCode)
	}
	return nil
}
