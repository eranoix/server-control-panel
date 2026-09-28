package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type userMeta struct {
	HMACSecret string `json:"hmac_secret"`
	APIKey     string `json:"api_key"`
}

type manager struct {
	stateDir string
	push     *pusher

	mu       sync.RWMutex
	sessions map[string]*session
	metadata map[string]userMeta
}

func main() {
	stateDir := envOr("WAD_STATE_DIR", "/var/lib/panel-wad")
	listen := envOr("WAD_LISTEN", "127.0.0.1:8769")
	webhookBase := envOr("WAD_WEBHOOK_BASE", "http://127.0.0.1:8765")

	m := &manager{
		stateDir: stateDir,
		sessions: map[string]*session{},
		metadata: map[string]userMeta{},
	}
	m.push = newPusher(webhookBase, m.hmacSecretOf)
	m.push.reloadOf = m.reloadMeta

	if err := m.loadAll(context.Background()); err != nil {
		log.Printf("wad: loadAll: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /u/{user}/send", m.auth(m.handleSend))
	mux.HandleFunc("POST /u/{user}/action", m.auth(m.handleAction))
	mux.HandleFunc("GET /u/{user}/status", m.auth(m.handleStatus))
	mux.HandleFunc("GET /u/{user}/qr", m.auth(m.handleQR))
	mux.HandleFunc("GET /u/{user}/profile-pic", m.auth(m.handleProfilePic))
	mux.HandleFunc("GET /u/{user}/check", m.auth(m.handleCheck))
	mux.HandleFunc("GET /u/{user}/group-info", m.auth(m.handleGroupInfo))
	mux.HandleFunc("GET /u/{user}/contacts", m.auth(m.handleContacts))
	mux.HandleFunc("GET /u/{user}/api/files/{msgid}", m.auth(m.handleMedia))
	mux.HandleFunc("POST /u/{user}/reload", m.auth(m.handleReload))

	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("wad: listening on %s, webhook→%s, sessions=%d", listen, webhookBase, len(m.sessions))
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("wad: %v", err)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func (m *manager) loadAll(ctx context.Context) error {
	entries, err := os.ReadDir(m.stateDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		user := e.Name()
		if err := m.loadUser(ctx, user); err != nil {
			log.Printf("wad: user %s: %v", user, err)
		}
	}
	return nil
}

func (m *manager) loadUser(ctx context.Context, user string) error {
	dir := filepath.Join(m.stateDir, user)
	dbPath := filepath.Join(dir, "session.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("no session.db")
	}
	metaRaw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return fmt.Errorf("no meta.json")
	}
	var meta userMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return fmt.Errorf("invalid meta: %w", err)
	}
	sess := newSession(user, dbPath, m.push)
	go sess.pruneMediaStash()

	m.mu.Lock()
	old := m.sessions[user]
	m.metadata[user] = meta
	m.sessions[user] = sess
	m.mu.Unlock()

	if old != nil {
		old.close()
	}

	go func() {
		if err := sess.connect(context.Background()); err != nil {
			log.Printf("wad: connect %s: %v", user, err)
		}
	}()
	return nil
}

func (m *manager) hmacSecretOf(user string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metadata[user].HMACSecret
}

func (m *manager) reloadMeta(user string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(m.stateDir, user, "meta.json"))
	if err != nil {
		return "", false
	}
	var meta userMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.metadata[user].HMACSecret
	if meta.HMACSecret == "" || meta.HMACSecret == old {
		return old, false
	}
	current := m.metadata[user]
	current.HMACSecret = meta.HMACSecret
	m.metadata[user] = current
	return meta.HMACSecret, true
}

func (m *manager) sessionOf(user string) *session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[user]
}

func (m *manager) auth(h func(http.ResponseWriter, *http.Request, *session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := r.PathValue("user")
		m.mu.RLock()
		meta, okMeta := m.metadata[user]
		sess := m.sessions[user]
		m.mu.RUnlock()
		if !okMeta || sess == nil {
			http.Error(w, "unknown user", http.StatusNotFound)
			return
		}
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if meta.APIKey == "" || tok != meta.APIKey {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r, sess)
	}
}

func (m *manager) handleSend(w http.ResponseWriter, r *http.Request, sess *session) {
	var req sendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	var data []byte
	if req.DataB64 != "" {
		var err error
		data, err = base64.StdEncoding.DecodeString(req.DataB64)
		if err != nil {
			http.Error(w, "bad base64", http.StatusBadRequest)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	id, err := sess.send(ctx, req, data)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (m *manager) handleAction(w http.ResponseWriter, r *http.Request, sess *session) {
	var req actionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := sess.action(ctx, req); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *manager) handleProfilePic(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	url, err := sess.profilePic(ctx, r.URL.Query().Get("jid"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"url": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": url})
}

func (m *manager) handleCheck(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := sess.checkNumber(ctx, r.URL.Query().Get("phone"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jid": res.JID, "onWA": res.OnWA})
}

func (m *manager) handleGroupInfo(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	gi, err := sess.groupInfo(ctx, r.URL.Query().Get("jid"))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, gi)
}

func (m *manager) handleContacts(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	list, err := sess.contacts(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (m *manager) handleMedia(w http.ResponseWriter, r *http.Request, sess *session) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	data, mime, err := sess.downloadMedia(ctx, r.PathValue("msgid"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	safe := "application/octet-stream"
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0])) {
	case "image/png", "image/jpeg", "image/webp", "image/gif",
		"audio/ogg", "audio/mpeg", "audio/mp4", "audio/aac",
		"video/mp4", "video/3gpp", "application/pdf":
		safe = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	}
	w.Header().Set("Content-Type", safe)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (m *manager) handleStatus(w http.ResponseWriter, r *http.Request, sess *session) {
	status, phone, pushName, _ := sess.snapshotStatus()
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "phone": phone, "pushName": pushName, "engine": "WHATSMEOW",
	})
}

func (m *manager) handleQR(w http.ResponseWriter, r *http.Request, sess *session) {
	_, _, _, qr := sess.snapshotStatus()
	writeJSON(w, http.StatusOK, map[string]any{"qr": qr})
}

func (m *manager) handleReload(w http.ResponseWriter, r *http.Request, sess *session) {
	if err := m.loadUser(r.Context(), sess.user); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
