package videocall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"server-control-panel/internal/notify/fcmpush"
	"server-control-panel/internal/webpush"
)

type Service struct {
	mu    sync.RWMutex
	rooms map[string]*Room
	path  string
	dirty bool

	flusherStop chan struct{}
	flusherDone chan struct{}

	histMu    sync.Mutex
	hist      []CallSession
	histPath  string
	histDirty bool

	Hub      *Hub
	Presence *PresenceHub
	Push     *webpush.Store
	FCM      fcmRingSender
	TURN     *TURNConfig

	Calls   *callRegistry
	Devices *deviceStore

	callTickerStop chan struct{}
	callTickerDone chan struct{}

	AuditFn          func(action, user, target string)
	InviteIssuer     InviteIssuer
	InviteSessionsCk InviteSessionsCheck

	guestMu   sync.Mutex
	guestJTIs map[string][]string

	Recordings *recordingStore

	WhatsAppSender func(user, jid, text string) error
}

type CallSession struct {
	RoomID         string `json:"room_id"`
	RoomName       string `json:"room_name,omitempty"`
	User           string `json:"user"`
	StartedAt      int64  `json:"started_at"`
	DurationS      int64  `json:"duration_s"`
	BytesSent      int64  `json:"bytes_sent"`
	BytesRecv      int64  `json:"bytes_recv"`
	Codec          string `json:"codec,omitempty"`
	ConnectionType string `json:"connection_type,omitempty"`
}

type InviteIssuer interface {
	IssueVideocallInviteToken(inviter, roomID string, ttl time.Duration) (token, jti string, err error)
	VerifyVideocallInviteToken(token string) (roomID, jti string, err error)
	IssueVideocallGuestToken(displayName, roomID string, ttl time.Duration) (token, jti string, err error)
	VerifyVideocallGuestToken(token string) (roomID, displayName string, err error)
	ExtractJTI(token string) (string, error)
}

type InviteSessionsCheck interface {
	Add(jti, user string, expiresAt int64)
	IsValid(jti string) bool
	Tombstone(jti string)
}

type Options struct {
	DataDir string
	TURN    *TURNConfig
	Push    *webpush.Store
	FCM     fcmRingSender
}

type fcmRingSender interface {
	SendDataToUser(ctx context.Context, user string, data map[string]string, opts fcmpush.DataOptions, allowDevice func(deviceID string) bool) int
}

func Open(opt Options) (*Service, error) {
	root := filepath.Join(opt.DataDir, "videocalls")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	s := &Service{
		rooms:          make(map[string]*Room),
		path:           filepath.Join(root, "rooms.json"),
		histPath:       filepath.Join(root, "sessions.json"),
		Hub:            NewHub(4),
		Presence:       NewPresenceHub(),
		TURN:           opt.TURN,
		flusherStop:    make(chan struct{}),
		flusherDone:    make(chan struct{}),
		Calls:          newCallRegistry(filepath.Join(root, "active-calls.json")),
		Devices:        newDeviceStore(filepath.Join(root, "ring-devices.json")),
		callTickerStop: make(chan struct{}),
		callTickerDone: make(chan struct{}),
		FCM:            opt.FCM,
	}
	s.Presence.RingPolicy = func(user, deviceID string, now int64) bool {
		return s.Devices.ShouldRing(user, deviceID, now)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	_ = s.loadHistory()
	if opt.Push != nil {
		s.Push = opt.Push
	} else if ps, err := webpush.Open(root); err == nil {
		s.Push = ps
	}
	go s.flusher()
	go s.callHeartbeat()
	return s, nil
}

func (s *Service) ActiveCall(roomID string) (LiveCall, bool) {
	if s == nil || s.Calls == nil {
		return LiveCall{}, false
	}
	return s.Calls.ActiveCall(roomID, time.Now().Unix())
}

func (s *Service) loadHistory() error {
	b, err := os.ReadFile(s.histPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var arr []CallSession
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil
	}
	s.histMu.Lock()
	s.hist = arr
	s.histMu.Unlock()
	return nil
}

func (s *Service) saveHistory() error {
	s.histMu.Lock()
	arr := append([]CallSession(nil), s.hist...)
	s.histMu.Unlock()
	b, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.histPath + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	_ = f.Close()
	if err := os.Rename(tmp, s.histPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Service) RecordCallSession(cs CallSession) {
	if cs.StartedAt == 0 {
		cs.StartedAt = time.Now().Unix() - cs.DurationS
	}
	r, ok := s.Room(cs.RoomID)
	if ok {
		cs.RoomName = r.Name
	}
	s.histMu.Lock()
	s.hist = append(s.hist, cs)
	if len(s.hist) > 500 {
		s.hist = s.hist[len(s.hist)-500:]
	}
	s.histDirty = true
	s.histMu.Unlock()
	_ = s.saveHistory()
}

func (s *Service) HistoryForUser(user string, limit int) []CallSession {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	s.histMu.Lock()
	defer s.histMu.Unlock()
	out := make([]CallSession, 0, limit)
	for i := len(s.hist) - 1; i >= 0 && len(out) < limit; i-- {
		if s.hist[i].User == user {
			out = append(out, s.hist[i])
		}
	}
	return out
}

func (s *Service) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var arr []*Room
	if err := json.Unmarshal(b, &arr); err != nil {
		return nil
	}
	for _, r := range arr {
		if r.ID == "" {
			continue
		}
		s.rooms[r.ID] = r
	}
	return nil
}

func (s *Service) flusher() {
	defer close(s.flusherDone)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	gcCounter := 0
	flushOnce := func() {
		s.mu.Lock()
		need := s.dirty
		s.dirty = false
		s.mu.Unlock()
		if need {
			_ = s.save()
		}
	}
	for {
		select {
		case <-s.flusherStop:
			flushOnce()
			return
		case <-t.C:
			gcCounter++
			if gcCounter >= 12 {
				gcCounter = 0
				s.gcExpiredPINs()
			}
			flushOnce()
		}
	}
}

func (s *Service) save() error {
	s.mu.RLock()
	arr := make([]*Room, 0, len(s.rooms))
	for _, r := range s.rooms {
		arr = append(arr, r)
	}
	s.mu.RUnlock()
	sort.Slice(arr, func(i, j int) bool { return arr[i].CreatedAt < arr[j].CreatedAt })
	b, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".new"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	_ = f.Close()
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if df, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
