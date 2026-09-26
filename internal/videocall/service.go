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

// Service is the durable side of videocall — manages rooms, persists them,
// and exposes lookups. The ephemeral side (connected peers, signaling) lives
// in *Hub. Service holds a *Hub reference so handlers only need one
// dependency to do everything.
type Service struct {
	mu    sync.RWMutex
	rooms map[string]*Room
	path  string // data/videocalls/rooms.json
	dirty bool

	// flusherStop signals the flusher goroutine to shut down. Close() closes
	// it and waits (via flusherDone) for the last save to happen before
	// returning — without that, dirty changes from the last 5s window
	// vanished on shutdown.
	flusherStop chan struct{}
	flusherDone chan struct{}

	// Call history (bandwidth + duration per session). Persisted to
	// data/videocalls/sessions.json. Append-only, capped at last 500
	// sessions to bound disk usage.
	histMu    sync.Mutex
	hist      []CallSession
	histPath  string // data/videocalls/sessions.json
	histDirty bool

	Hub      *Hub
	Presence *PresenceHub
	Push     *webpush.Store // nil if VAPID key generation failed; feature degrades gracefully
	// FCM fans the same ring/cancel events out to native Android devices
	// (killed-app reachability Web Push cannot offer), alongside Push. Typed
	// as fcmRingSender — a narrow, consumer-defined interface — rather than
	// the concrete *fcmpush.Sender, because fcmpush.Sender's fields are all
	// unexported: nothing outside that package can construct a fake one for
	// tests. *fcmpush.Sender satisfies this interface structurally; nil
	// degrades the fan-out to a no-op, matching Push's own nil-safety.
	FCM  fcmRingSender
	TURN *TURNConfig

	// Calls is the registry of LIVE calls. It is what knows the difference
	// between "someone is calling" and "someone reconnected" — without it,
	// every restart of the process became a fresh ringer in mid-call.
	Calls *callRegistry
	// Devices holds the PER-DEVICE ringing policy ("do not ring on this
	// computer"), consulted in the presence fan-out.
	Devices *deviceStore

	// Call session heartbeat: it renews LastActiveAt while there are peers and
	// ends the calls whose grace window blew.
	callTickerStop chan struct{}
	callTickerDone chan struct{}

	// Optional audit + invite issuer. Wired in api.NewRouter so the
	// videocall package stays independent of internal/auth.
	AuditFn          func(action, user, target string)
	InviteIssuer     InviteIssuer
	InviteSessionsCk InviteSessionsCheck

	// Guest JTIs per roomID. We keep the index so RevokePIN can tombstone
	// every jti issued for the room (without it, a leaked PIN stayed valid
	// until exp). It has a lock of its own so it does not clash with the
	// rooms histMu/mu.
	guestMu   sync.Mutex
	guestJTIs map[string][]string // roomID → []jti

	// Cloud recording store (nil = feature disabled gracefully).
	Recordings *recordingStore

	// WhatsApp sender — bound to internal/whatsapp.Manager.ForUser(user).Client.SendText.
	// user is the room's owner (multi-tenant: the invite goes out over HIS
	// WhatsApp, not another profile's). nil when no user has WAHA configured;
	// the UI then hides the "Enviar via WhatsApp" CTA.
	WhatsAppSender func(user, jid, text string) error
}

// CallSession is one finished videocall — written when the client POSTs
// /api/videocall/sessions on hangup. Used by the dashboard history chart.
type CallSession struct {
	RoomID         string `json:"room_id"`
	RoomName       string `json:"room_name,omitempty"`
	User           string `json:"user"`
	StartedAt      int64  `json:"started_at"` // unix seconds
	DurationS      int64  `json:"duration_s"`
	BytesSent      int64  `json:"bytes_sent"`
	BytesRecv      int64  `json:"bytes_recv"`
	Codec          string `json:"codec,omitempty"`
	ConnectionType string `json:"connection_type,omitempty"` // direct | relay
}

// InviteIssuer mints + verifies videocall_invite JWTs. The auth package
// implements this; videocall depends only on the interface so the two
// packages don't cyclic-import.
type InviteIssuer interface {
	IssueVideocallInviteToken(inviter, roomID string, ttl time.Duration) (token, jti string, err error)
	VerifyVideocallInviteToken(token string) (roomID, jti string, err error)
	// Guest tokens are for anonymous users who join via PIN (public access).
	IssueVideocallGuestToken(displayName, roomID string, ttl time.Duration) (token, jti string, err error)
	VerifyVideocallGuestToken(token string) (roomID, displayName string, err error)
	// ExtractJTI parses the token (without full validation) and returns the jti.
	// Used by HandleGuestWS to check revocation in InviteSessionsCk.
	ExtractJTI(token string) (string, error)
}

// InviteSessionsCheck reuses the existing sessions store for single-use
// invite tracking. A minted invite is Add()ed; on consume the jti is
// checked and then Revoke()d so a re-use is rejected as tombstoned.
type InviteSessionsCheck interface {
	Add(jti, user string, expiresAt int64)
	IsValid(jti string) bool // exists AND not tombstoned
	Tombstone(jti string)
}

// Options for Open. DataDir is the vps-manager data root; rooms.json will
// live under DataDir/videocalls/.
type Options struct {
	DataDir string
	TURN    *TURNConfig
	// Push, when non-nil, is used as the shared *webpush.Store instead of
	// videocall opening one of its own (this avoids instantiating a second
	// VAPID pair, which would orphan the existing subscriptions). nil keeps
	// the long-standing behaviour: videocall opens and manages its own store
	// under DataDir/videocalls/.
	Push *webpush.Store
	// FCM, when non-nil, is the *fcmpush.Sender already built in
	// internal/api/notify_wire.go (the same one used by notify's "push"
	// channel) — videocall NEVER builds a Sender of its own nor reads the FCM
	// credential directly; it only receives the ready instance and hands it on
	// to the Service. nil (no FCM credential provisioned) keeps the
	// long-standing behaviour: only Web Push rings.
	FCM fcmRingSender
}

// fcmRingSender is the seam Service.FCM uses to push a data-only ring/cancel
// event to a user's native Android devices. Defined here (consumer side),
// exactly like fcmpush.DeviceStore and notify's own pushSender: a
// *fcmpush.Sender satisfies this structurally without videocall importing
// anything from fcmpush's internals, and tests can supply a fake without
// needing a real Google OAuth2 service-account credential (fcmpush.Sender's
// fields are all unexported, so it cannot be white-box-constructed from
// outside its own package).
type fcmRingSender interface {
	SendDataToUser(ctx context.Context, user string, data map[string]string, opts fcmpush.DataOptions, allowDevice func(deviceID string) bool) int
}

// Open loads (or creates) the rooms persistence file and returns a Service
// with a fresh Hub. Mirrors the pattern of sessions.Open — start a 5s
// background flusher that batches writes off the hot path.
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
	// The per-device policy is consulted by the ringing fan-out. It stays a
	// callback (rather than an import) so the PresenceHub remains a dumb bus,
	// testable without the on-disk store.
	s.Presence.RingPolicy = func(user, deviceID string, now int64) bool {
		return s.Devices.ShouldRing(user, deviceID, now)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	_ = s.loadHistory() // tolerant: empty if missing or corrupt
	// Push notifications (off-app): VAPID keys generated lazily on first
	// Open. Failure to init is non-fatal — the feature just stays unavailable
	// (UI hides the "Receber off-app" toggle when Push is nil). If the
	// caller injected a shared store (Options.Push), reuse it instead of
	// opening a second one — a second Open() here would generate its own
	// VAPID keypair and orphan every subscription already tied to the
	// shared store's key.
	if opt.Push != nil {
		s.Push = opt.Push
	} else if ps, err := webpush.Open(root); err == nil {
		s.Push = ps
	}
	go s.flusher()
	go s.callHeartbeat()
	return s, nil
}

// ActiveCall exposes a room's live call (nil-safe). Used by the UI to show
// "in call" and by the tests.
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
		return nil // tolerant
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

// RecordCallSession appends a finished call to the persistent history.
// Caller is the API layer after the client POSTs final stats.
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
	// Cap at last 500 sessions
	if len(s.hist) > 500 {
		s.hist = s.hist[len(s.hist)-500:]
	}
	s.histDirty = true
	s.histMu.Unlock()
	_ = s.saveHistory()
}

// HistoryForUser returns the sessions where `user` was a participant,
// newest first, capped at `limit` (max 200).
func (s *Service) HistoryForUser(user string, limit int) []CallSession {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	s.histMu.Lock()
	defer s.histMu.Unlock()
	out := make([]CallSession, 0, limit)
	// Walk newest first.
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
		// Same tolerance as sessions: corrupt file = start fresh, never refuse
		// to boot. Audit log will still record this via the surrounding handler.
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
			// final flush before dying — guarantees the dirty state of the
			// last window reaches disk.
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
