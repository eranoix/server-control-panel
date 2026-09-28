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

type Service struct {
	Store              *Store
	Client             Backend
	Broadcaster        *Broadcaster
	hmacMu             sync.RWMutex
	hmacSecret         string
	HMACRefresh        func() string
	ServiceUnit        string
	GowsDBPath         string
	ExtraWebhookURL    string
	ExtraWebhookEvents []string

	lidMu    sync.RWMutex
	lidCache map[string]string

	recoverMu sync.Mutex
	recoverAt map[string]int64

	avatarMu    sync.RWMutex
	avatarCache map[string]avatarEntry

	downloadSF singleflight.Group

	protectedMuxOnce sync.Once
	protectedMux     http.Handler

	downloadJobs chan downloadJob

	sendDedupeMu sync.Mutex
	sendDedupe   map[string]sendDedupeEntry

	cancel context.CancelFunc
}

type sendDedupeEntry struct {
	ServerMsgID string
	ExpiresAt   time.Time
}

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
	if len(s.sendDedupe) > 200 {
		now := time.Now()
		for k, v := range s.sendDedupe {
			if now.After(v.ExpiresAt) {
				delete(s.sendDedupe, k)
			}
		}
	}
}

type downloadJob struct {
	ChatJID  string
	MsgID    string
	WAHAURL  string
	MimeType string
	Filename string
}

type avatarEntry struct {
	url     string
	expires time.Time
}

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

func (s *Service) updateLidCache(m map[string]string) {
	s.lidMu.Lock()
	s.lidCache = m
	s.lidMu.Unlock()
}

func (s *Service) gowsDB() string {
	if s.GowsDBPath != "" {
		return s.GowsDBPath
	}
	return gowsDBPath
}

func (s *Service) CanonicalChatJIDLazy(jid string) string {
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
	lidRaw := strings.TrimSuffix(jid, "@lid")
	dbPath := s.gowsDB()
	if dbPath == "" {
		return jid
	}
	if _, err := os.Stat(dbPath); err != nil {
		return jid
	}
	uri := "file:" + dbPath + "?mode=ro&immutable=0"
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
		return jid
	}
	canon := pn + "@c.us"
	s.lidMu.Lock()
	if s.lidCache == nil {
		s.lidCache = map[string]string{}
	}
	s.lidCache[jid] = canon
	s.lidMu.Unlock()
	return canon
}

type mediaRecoverItem struct {
	ID     string
	Sender string
}

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
			sender = jid
		}
		_ = s.Client.ResendMessage(jid, sender, it.ID)
	}
}

func (s *Service) EnqueueDownload(chatJID, msgID, wahaURL, mimeType, filename string) {
	if msgID == "" || wahaURL == "" {
		return
	}
	select {
	case s.downloadJobs <- downloadJob{ChatJID: chatJID, MsgID: msgID, WAHAURL: wahaURL, MimeType: mimeType, Filename: filename}:
	default:
	}
}

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
			return
		}
	}
	ext := mediaExtFor(job.MimeType, job.Filename, wahaURL)
	rel := filepath.Join(chatDir(job.ChatJID), sanitizeMsgID(job.MsgID)+ext)
	full := filepath.Join(s.Store.MediaRoot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return
	}
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

func (s *Service) broadcastMsgUpdated(chatJID, msgID string) {
	m, err := s.Store.FindMessage(chatJID, msgID)
	if err != nil || m == nil {
		return
	}
	s.Broadcaster.Send(WSEvent{Kind: "message", Message: m, TS: time.Now().Unix()})
}

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

func (s *Service) cleanupOrphanMedia() {
	root := s.Store.MediaRoot
	if root == "" {
		return
	}
	const pageSize = 500
	const maxIter = 20
	referenced := make(map[string]bool)
	for _, c := range s.Store.ListChats() {
		var beforeTS int64 = 0
		for iter := 0; iter < maxIter; iter++ {
			msgs, err := s.Store.LoadMessages(c.JID, beforeTS, pageSize)
			if err != nil || len(msgs) == 0 {
				break
			}
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
				break
			}
			if oldestSeen <= 0 || oldestSeen == beforeTS {
				break
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

type Options struct {
	StoreRoot          string
	MediaRoot          string
	GowsDBPath         string
	WAHABaseURL        string
	WAHAAPIKey         string
	HMACSecret         string
	HMACRefresh        func() string
	ServiceUnit        string
	ExtraWebhookURL    string
	ExtraWebhookEvents []string
	Backend            Backend
}

func New(opts Options) (*Service, error) {
	if opts.StoreRoot == "" {
		return nil, fmt.Errorf("whatsapp: StoreRoot required")
	}
	if opts.MediaRoot == "" {
		opts.MediaRoot = "/var/lib/panel-whatsapp/media"
	}
	if opts.WAHABaseURL == "" {
		opts.WAHABaseURL = "http://127.0.0.1:3000"
	}
	if opts.ServiceUnit == "" {
		opts.ServiceUnit = "panel-whatsapp.service"
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
		downloadJobs:       make(chan downloadJob, 256),
	}
	_, _ = svc.Store.SetState(func(st *State) { st.Enabled = true })

	ctx, cancel := context.WithCancel(context.Background())
	svc.cancel = cancel
	svc.startPoller(ctx)
	for i := 0; i < 4; i++ {
		go svc.runDownloadWorker(ctx)
	}
	go svc.runMediaCleanup(ctx)
	return svc, nil
}

func (s *Service) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Service) SystemctlRestart() error {
	return systemctl("restart", s.ServiceUnit)
}

func (s *Service) SystemctlStart() error {
	return systemctl("start", s.ServiceUnit)
}

func (s *Service) SystemctlStop() error {
	return systemctl("stop", s.ServiceUnit)
}

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
