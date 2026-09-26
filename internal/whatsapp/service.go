package whatsapp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Service is the top-level wiring for the WhatsApp integration. It owns the
// HTTP client to WAHA, the on-disk store, the WS broadcaster, and the
// background poller. One Service per process — created by api.NewRouter.
type Service struct {
	Store       *Store
	Client      Backend // WAHA (*Client) or whatsmeow daemon (*meowClient), per user
	Broadcaster *Broadcaster
	// hmacSecret is the secret shared with the webhook. It lives behind hmacMu
	// and is DELIBERATELY unexported: hmacConfere REWRITES it when the vault
	// rotates, and HandleWebhook reads it on a PER-REQUEST goroutine — two
	// messages arriving alongside a rotation would be a write racing a read,
	// which the Go memory model does not allow. Unexported so nobody can touch
	// it without going through the lock; use hmacAtual() to read and
	// hmacTroca() to adopt a new one.
	hmacMu     sync.RWMutex
	hmacSecret string
	// HMACRefresh re-reads the secret from the SOURCE (the vault) and returns the
	// current value. nil = no refresh (the old behaviour). See Service.hmacAtual.
	HMACRefresh func() string
	ServiceUnit string // systemd unit name, e.g. "vpsm-whatsapp.service"
	GowsDBPath  string // the user's Whatsmeow SQLite; empty = legacy gowsDBPath const
	// ExtraWebhookURL/Events registers an additional webhook destination through
	// the per-session config (POST /api/sessions/). Without touching the
	// container's global env var, this lets N control-plane instances (v1 + v2)
	// receive the same webhook in parallel. Set via Options.
	ExtraWebhookURL    string
	ExtraWebhookEvents []string

	// lidCache holds the @lid -> @c.us mapping in memory so the message webhook
	// can convert JIDs immediately, without waiting for the 30s sync. Refreshed
	// by syncChatNames every time it reads Whatsmeow's SQLite. Access is guarded
	// by lidMu — writes are rare (once per poll), reads happen on every message.
	lidMu    sync.RWMutex
	lidCache map[string]string // ex: "100000000000001@lid" → "5511555010102@c.us"

	// recoverAt debounces the on-demand history sync per chat, so we do not ask
	// for history every time a conversation is opened. chatJID -> unix ts of the
	// last request.
	recoverMu sync.Mutex
	recoverAt map[string]int64

	// avatarCache keeps every <img src=/api/whatsapp/avatar/<jid>> from hitting
	// WAHA each time. Without it, opening the panel with ~200 contacts fires
	// ~200 concurrent requests and WAHA answers 429 across the board (see the
	// console log). url="" is a negative cache entry (a contact with no avatar).
	avatarMu    sync.RWMutex
	avatarCache map[string]avatarEntry

	// downloadSF collapses concurrent calls to DownloadMediaForMessage for the
	// SAME (chatJID,msgID) into a single in-flight download — without it,
	// opening the same not-yet-cached media in two tabs or screens would fire a
	// duplicate download against WhatsApp. The zero value is usable as-is (no
	// init needed). See service_export.go.
	downloadSF singleflight.Group

	// protectedMux is this Service's REST/WS router, built lazily by
	// Manager.ProtectedHandler when the user's first request arrives. Cached to
	// avoid rebuilding it per request (the paths are fixed).
	protectedMuxOnce sync.Once
	protectedMux     http.Handler

	// downloadJobs is the single queue of media download jobs. The webhook (a
	// message with hasMedia=true) and /sync enqueue here; a worker pool calls
	// Client.DownloadFile, writes to MediaRoot, updates the Store and broadcasts
	// a "message" WSEvent so the UI re-renders inline. Buffered so it absorbs
	// spikes without blocking the producer.
	downloadJobs chan downloadJob

	// sendDedupe — a cache of recent sends keyed by client_msg_id, defending
	// against a double-click or a client-side retry turning into a duplicate on
	// WhatsApp. The 60s TTL is generous (the auto-clean GC runs every minute).
	// The map stays small (usually <50 entries in typical use).
	sendDedupeMu sync.Mutex
	sendDedupe   map[string]sendDedupeEntry

	cancel context.CancelFunc
}

// sendDedupeEntry records the outcome of an earlier send. The same
// client_msg_id arriving again inside the window returns the same ID, so
// nothing is duplicated.
type sendDedupeEntry struct {
	ServerMsgID string
	ExpiresAt   time.Time
}

// sendDedupeCheck reports whether a client_msg_id was sent recently.
// Returns (server_msg_id, true) on a hit; ("", false) on a miss.
// Best effort: 60s TTL, in-memory only, lost on restart.
func (s *Service) sendDedupeCheck(clientMsgID string) (string, bool) {
	if clientMsgID == "" {
		return "", false
	}
	s.sendDedupeMu.Lock()
	defer s.sendDedupeMu.Unlock()
	entry, ok := s.sendDedupe[clientMsgID]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return "", false
	}
	return entry.ServerMsgID, true
}

// sendDedupeRemember records a successful send for future dedup.
func (s *Service) sendDedupeRemember(clientMsgID, serverMsgID string) {
	if clientMsgID == "" {
		return
	}
	s.sendDedupeMu.Lock()
	defer s.sendDedupeMu.Unlock()
	if s.sendDedupe == nil {
		s.sendDedupe = make(map[string]sendDedupeEntry)
	}
	s.sendDedupe[clientMsgID] = sendDedupeEntry{
		ServerMsgID: serverMsgID,
		ExpiresAt:   time.Now().Add(60 * time.Second),
	}
	// Opportunistic GC: once past 200 entries, expire everything already due.
	if len(s.sendDedupe) > 200 {
		now := time.Now()
		for k, v := range s.sendDedupe {
			if now.After(v.ExpiresAt) {
				delete(s.sendDedupe, k)
			}
		}
	}
}

// downloadJob describes one media file to fetch from WAHA onto local disk.
type downloadJob struct {
	ChatJID  string
	MsgID    string
	WAHAURL  string // URL returned by WAHA (encrypted CDN OR /api/files/)
	MimeType string
	Filename string
}

type avatarEntry struct {
	url     string // "" == negative (no avatar)
	expires time.Time
}

// CanonicalChatJID converts an @lid JID to its matching @c.us when a mapping
// is known. Used by the webhook to avoid creating duplicate chats.
// Fallback: returns normalizeJID(jid) — no loss for chats without a mapping.
func (s *Service) CanonicalChatJID(jid string) string {
	jid = normalizeJID(jid)
	if !strings.HasSuffix(jid, "@lid") {
		return jid
	}
	s.lidMu.RLock()
	out, ok := s.lidCache[jid]
	s.lidMu.RUnlock()
	if ok && out != "" {
		return out
	}
	return jid
}

// updateLidCache swaps the cache for the latest snapshot. Atomic.
func (s *Service) updateLidCache(m map[string]string) {
	s.lidMu.Lock()
	s.lidCache = m
	s.lidMu.Unlock()
}

// gowsDB returns this Service's path to Whatsmeow's SQLite. Empty (the
// default in the legacy single-tenant layout) -> the global const; set by the
// Manager (multi-tenant) -> per-user.
func (s *Service) gowsDB() string {
	if s.GowsDBPath != "" {
		return s.GowsDBPath
	}
	return gowsDBPath
}

// CanonicalChatJIDLazy behaves like CanonicalChatJID but falls back to a
// just-in-time query against Whatsmeow's SQLite when the cache has no
// mapping. That covers the gap between the webhook receiving messages and
// the first syncChatNames populating the cache (a cold start, or a new
// contact the sync has not seen yet). The result is written back to the cache
// so later lookups are instant.
func (s *Service) CanonicalChatJIDLazy(jid string) string {
	jid = normalizeJID(jid)
	if !strings.HasSuffix(jid, "@lid") {
		return jid
	}
	// Cache hit?
	s.lidMu.RLock()
	out, ok := s.lidCache[jid]
	s.lidMu.RUnlock()
	if ok && out != "" {
		return out
	}
	// Lazy lookup in SQLite. The raw lid (no suffix) lives in the `lid` column.
	lidRaw := strings.TrimSuffix(jid, "@lid")
	dbPath := s.gowsDB()
	if dbPath == "" {
		return jid
	}
	if _, err := os.Stat(dbPath); err != nil {
		return jid
	}
	uri := "file:" + dbPath + "?mode=ro&immutable=0"
	// SQL injection defence: lidRaw comes from WAHA's webhook and is untrusted.
	// The sqlite3 CLI offers no parameterisation, so we have to escape quotes and
	// reject payloads carrying suspicious characters.
	if strings.ContainsAny(lidRaw, "'\";\\") {
		return jid
	}
	escapedLID := strings.ReplaceAll(lidRaw, "'", "''")
	cmd := exec.Command("sqlite3", "-readonly", uri,
		"SELECT pn FROM whatsmeow_lid_map WHERE lid='"+escapedLID+"';")
	cmd.Stderr = nil
	output, err := cmd.Output()
	if err != nil {
		return jid
	}
	pn := strings.TrimSpace(string(output))
	if pn == "" {
		return jid // no known mapping — it stays @lid
	}
	canon := pn + "@c.us"
	// Memoise it for next time.
	s.lidMu.Lock()
	if s.lidCache == nil {
		s.lidCache = map[string]string{}
	}
	s.lidCache[jid] = canon
	s.lidMu.Unlock()
	return canon
}

// mediaRecoverItem identifies one media item to recover: message id + author.
type mediaRecoverItem struct {
	ID     string
	Sender string // message author (Message.FromJID) — to build the resend MessageKey
}

// RecoverChatMedia recovers the media keys of older images and videos whose
// file is no longer on disk: it asks WhatsApp to RESEND each specific message
// (Backend.ResendMessage -> PLACEHOLDER_MESSAGE_RESEND). The reply arrives as
// an ordinary message that ALREADY CARRIES the key (onMessage persists it,
// and media.recovered triggers the download). Debounced per chat (one burst
// every 2min) and capped so it never floods the primary device.
func (s *Service) RecoverChatMedia(jid string, items []mediaRecoverItem) {
	if s.Client == nil || s.Store == nil || len(items) == 0 {
		return
	}
	now := time.Now().Unix()
	s.recoverMu.Lock()
	if s.recoverAt == nil {
		s.recoverAt = map[string]int64{}
	}
	if last := s.recoverAt[jid]; now-last < 120 {
		s.recoverMu.Unlock()
		return
	}
	s.recoverAt[jid] = now
	s.recoverMu.Unlock()

	const maxPerBurst = 30
	for i, it := range items {
		if i >= maxPerBurst {
			break
		}
		sender := it.Sender
		if sender == "" {
			sender = jid // 1:1: author = the chat itself
		}
		_ = s.Client.ResendMessage(jid, sender, it.ID)
	}
}

// EnqueueDownload asks a worker to fetch the media in the background.
// Non-blocking and best effort: if the queue is full it drops the job
// silently — the user can still press "Download" on the bubble. Called by the
// webhook when a message with hasMedia arrives, and by /sync for media that
// comes back from WAHA with a URL.
func (s *Service) EnqueueDownload(chatJID, msgID, wahaURL, mimeType, filename string) {
	if msgID == "" || wahaURL == "" {
		return
	}
	select {
	case s.downloadJobs <- downloadJob{ChatJID: chatJID, MsgID: msgID, WAHAURL: wahaURL, MimeType: mimeType, Filename: filename}:
	default:
		// Queue full — drop it. The user can press "Download" manually.
	}
}

// runDownloadWorker is one worker's loop in the pool. It exits when ctx is
// cancelled (Service.Close()). For each job:
//  1. Download via Client.DownloadFile to a deterministic path under MediaRoot.
//  2. Store.UpdateMessageMedia (path + mime + size).
//  3. Broadcast a "message" WSEvent carrying the updated message — the
//     frontend swaps the "Download" button for an inline player or image
//     without a refresh.
func (s *Service) runDownloadWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.downloadJobs:
			s.processDownloadJob(job)
		}
	}
}

func (s *Service) processDownloadJob(job downloadJob) {
	if s.Client == nil || s.Store == nil {
		return
	}
	// A raw CDN URL (mmg.whatsapp.net) is only decipherable by WAHA. When the
	// URL comes from that domain instead of /api/files/, force WAHA's internal
	// download first (GetChatMessagesWithMedia returns a usable URL).
	wahaURL := job.WAHAURL
	if !strings.Contains(wahaURL, "/api/files/") {
		msgs, err := s.Client.GetChatMessagesWithMedia(job.ChatJID, 50)
		if err != nil {
			return
		}
		for _, wm := range msgs {
			if wm.ID == job.MsgID && wm.Media != nil && wm.Media.URL != "" {
				wahaURL = wm.Media.URL
				if job.MimeType == "" {
					job.MimeType = wm.Media.MimeType
				}
				if job.Filename == "" {
					job.Filename = wm.Media.Filename
				}
				break
			}
		}
		if !strings.Contains(wahaURL, "/api/files/") {
			return // WAHA could not download it — the user can retry via the UI
		}
	}
	// Destination path: <chatDir>/<safeID>.<ext>
	ext := mediaExtFor(job.MimeType, job.Filename, wahaURL)
	rel := filepath.Join(chatDir(job.ChatJID), sanitizeMsgID(job.MsgID)+ext)
	full := filepath.Join(s.Store.MediaRoot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return
	}
	// Already there (idempotency): just re-broadcast and return.
	if st, err := os.Stat(full); err == nil && !st.IsDir() {
		_ = s.Store.UpdateMessageMedia(job.ChatJID, job.MsgID, rel, job.MimeType, job.Filename, st.Size())
		s.broadcastMsgUpdated(job.ChatJID, job.MsgID)
		return
	}
	out, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	n, _, derr := s.Client.DownloadFile(wahaURL, out)
	_ = out.Close()
	if derr != nil {
		_ = os.Remove(full)
		return
	}
	_ = s.Store.UpdateMessageMedia(job.ChatJID, job.MsgID, rel, job.MimeType, job.Filename, n)
	s.broadcastMsgUpdated(job.ChatJID, job.MsgID)
}

// broadcastMsgUpdated re-sends the message with Media filled in so the UI can
// swap the "Download" button for an inline player. Best effort: if the
// message is not in the store (a race with a delete?), just skip it.
func (s *Service) broadcastMsgUpdated(chatJID, msgID string) {
	m, err := s.Store.FindMessage(chatJID, msgID)
	if err != nil || m == nil {
		return
	}
	s.Broadcaster.Send(WSEvent{Kind: "message", Message: m, TS: time.Now().Unix()})
}

// runMediaCleanup sweeps MediaRoot every 24h and removes files that have no
// corresponding message in the store (deleted ones, or historical orphans).
// It runs first 5min after start, then daily — so it catches litter from
// restart loops without waiting a full day. Cancelled by ctx (Service.Close()).
func (s *Service) runMediaCleanup(ctx context.Context) {
	if s.Store == nil || s.Store.MediaRoot == "" {
		return
	}
	first := time.NewTimer(5 * time.Minute)
	defer first.Stop()
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			s.cleanupOrphanMedia()
		case <-tick.C:
			s.cleanupOrphanMedia()
		}
	}
}

// cleanupOrphanMedia scans MediaRoot/<chatHash>/* and removes files that match
// no message reference in the store. Best effort.
//
// Pagination fix: it used to read only the first 1000 messages per chat. In
// chats with more than 1000 messages, media referenced by older messages was
// marked as orphaned and DELETED even though it was still in use. It now
// paginates until the JSONL runs out (LoadMessages with before<=oldest_seen
// until it returns []). A 10k-messages-per-chat cap acts as a safety net (a
// chat with 50k+ messages is very rare; if it happens, the next cycle picks
// up where this one stopped).
func (s *Service) cleanupOrphanMedia() {
	root := s.Store.MediaRoot
	if root == "" {
		return
	}
	const pageSize = 500
	const maxIter = 20 // 500 × 20 = 10k msgs per chat
	referenced := make(map[string]bool)
	for _, c := range s.Store.ListChats() {
		var beforeTS int64 = 0
		for iter := 0; iter < maxIter; iter++ {
			msgs, err := s.Store.LoadMessages(c.JID, beforeTS, pageSize)
			if err != nil || len(msgs) == 0 {
				break
			}
			// Walks the messages (DESC by ts in LoadMessages). Marks them referenced
			// and captures the oldest ts for the next iteration.
			var oldestSeen int64 = 0
			for _, m := range msgs {
				if m.Media != nil && m.Media.Path != "" {
					referenced[m.Media.Path] = true
				}
				if oldestSeen == 0 || m.TS < oldestSeen {
					oldestSeen = m.TS
				}
			}
			if len(msgs) < pageSize {
				break // end of history
			}
			if oldestSeen <= 0 || oldestSeen == beforeTS {
				break // safety: TS did not advance, avoids an infinite loop
			}
			beforeTS = oldestSeen
		}
	}
	chatDirs, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, cd := range chatDirs {
		if !cd.IsDir() {
			continue
		}
		dirPath := filepath.Join(root, cd.Name())
		files, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			rel := filepath.Join(cd.Name(), f.Name())
			if referenced[rel] {
				continue
			}
			_ = os.Remove(filepath.Join(dirPath, f.Name()))
		}
	}
}

// Options configures the service. Empty values fall back to safe defaults.
type Options struct {
	StoreRoot   string // data/whatsapp/
	MediaRoot   string // /var/lib/vpsm-whatsapp/media/
	GowsDBPath  string // /var/lib/vpsm-whatsapp/<user>/sessions/gows/default/gows.db; vazio = legacy default
	WAHABaseURL string // http://127.0.0.1:3000
	WAHAAPIKey  string // X-Api-Key
	HMACSecret  string // shared with WAHA's WHATSAPP_HOOK_HMAC
	// HMACRefresh: see Service.HMACRefresh.
	HMACRefresh func() string
	ServiceUnit string // systemd unit to manage; defaults to vpsm-whatsapp.service
	// ExtraWebhookURL: an additional URL to register as a per-session webhook.
	// The real use case: a v2 (port 8766) sharing WAHA containers with a v1
	// (port 8765, via the WHATSAPP_HOOK_URL env var). Without this option the v2
	// never receives webhooks, so new messages never show up in real time.
	// With ExtraWebhookURL set, the v2 calls POST /api/sessions/ (update) and
	// adds a SECOND destination to the session's config.webhooks — WAHA then
	// fires at both independently. Zero impact on the v1.
	ExtraWebhookURL string
	// ExtraWebhookEvents: the list of events to register on the extra webhook.
	// Empty = the same set as WHATSAPP_HOOK_EVENTS in the template
	// (session.status, message, message.any, message.ack, message.revoked).
	ExtraWebhookEvents []string
	// Backend, when set, replaces the default WAHA client. Used to migrate a user
	// onto the whatsmeow daemon (meowClient) without touching the rest of the
	// Service. Empty = WAHA (NewClient).
	Backend Backend
}

// New constructs and starts the service. The poller goroutine runs until
// the returned Close() is called (or process exit).
func New(opts Options) (*Service, error) {
	if opts.StoreRoot == "" {
		return nil, fmt.Errorf("whatsapp: StoreRoot required")
	}
	if opts.MediaRoot == "" {
		opts.MediaRoot = "/var/lib/vpsm-whatsapp/media"
	}
	if opts.WAHABaseURL == "" {
		opts.WAHABaseURL = "http://127.0.0.1:3000"
	}
	if opts.ServiceUnit == "" {
		opts.ServiceUnit = "vpsm-whatsapp.service"
	}
	store, err := NewStore(opts.StoreRoot, opts.MediaRoot)
	if err != nil {
		return nil, err
	}
	backend := opts.Backend
	if backend == nil {
		backend = NewClient(opts.WAHABaseURL, opts.WAHAAPIKey)
	}
	svc := &Service{
		Store:              store,
		Client:             backend,
		Broadcaster:        NewBroadcaster(),
		hmacSecret:         opts.HMACSecret,
		HMACRefresh:        opts.HMACRefresh,
		ServiceUnit:        opts.ServiceUnit,
		GowsDBPath:         opts.GowsDBPath,
		ExtraWebhookURL:    opts.ExtraWebhookURL,
		ExtraWebhookEvents: opts.ExtraWebhookEvents,
		lidCache:           map[string]string{},
		avatarCache:        map[string]avatarEntry{},
		downloadJobs:       make(chan downloadJob, 256), // 256 queued jobs — above the "100 photos at once" peaks
	}
	// Mark enabled true so the UI doesn't show "WhatsApp not configured" once
	// the service is wired (even if WAHA itself isn't running yet).
	_, _ = svc.Store.SetState(func(st *State) { st.Enabled = true })

	ctx, cancel := context.WithCancel(context.Background())
	svc.cancel = cancel
	svc.startPoller(ctx)
	// A fixed pool of 4 workers drains downloadJobs. Each worker downloads,
	// writes to disk, updates the store and broadcasts. WAHA Core handles
	// concurrency (the GOWS subprocess), but 4 is enough without saturating it —
	// every job is I/O-bound and dominated by WhatsApp's CDN.
	for i := 0; i < 4; i++ {
		go svc.runDownloadWorker(ctx)
	}
	// Periodic cleanup: removes files under MediaRoot that no longer have a
	// message referencing them (deleted, or orphaned by some historical bug).
	// Runs once a day, plus once 5min after start (to catch a restart loop).
	go svc.runMediaCleanup(ctx)
	return svc, nil
}

// Close stops the background poller. The HTTP server should drain in-flight
// requests separately (it owns the listener).
func (s *Service) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

// SystemctlRestart runs `systemctl restart <unit>`. Used by /api/whatsapp/
// restart and the CLI subcommand.
func (s *Service) SystemctlRestart() error {
	return systemctl("restart", s.ServiceUnit)
}

// SystemctlStart starts the unit (used after install before first pairing).
func (s *Service) SystemctlStart() error {
	return systemctl("start", s.ServiceUnit)
}

// SystemctlStop halts the unit.
func (s *Service) SystemctlStop() error {
	return systemctl("stop", s.ServiceUnit)
}

// SystemctlActive returns true when `systemctl is-active` reports the unit
// as active.
func (s *Service) SystemctlActive() bool {
	out, err := exec.Command("/usr/bin/systemctl", "is-active", s.ServiceUnit).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "active"
}

func systemctl(action, unit string) error {
	if _, err := os.Stat("/usr/bin/systemctl"); err != nil {
		return fmt.Errorf("systemctl unavailable: %w", err)
	}
	out, err := exec.Command("/usr/bin/systemctl", action, unit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s %s: %s: %w", action, unit, strings.TrimSpace(string(out)), err)
	}
	return nil
}
