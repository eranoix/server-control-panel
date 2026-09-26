package videocall

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	webpushgo "github.com/SherClockHolmes/webpush-go"

	"server-control-panel/internal/webpush"
)

// Push delivers off-app notifications via the W3C Web Push protocol. It
// kicks in for room members who do NOT currently have an active presence
// WebSocket (i.e. the panel is closed or PWA in background). When the
// panel IS open, the existing in-tab ring covers it without burning push
// budget.
//
// The VAPID keypair + subscription store live in internal/webpush (shared
// with any other channel that wants to push to a browser); this file only
// keeps the videocall-specific bits: the HTTP subscribe/unsubscribe
// endpoints and the incoming-call payload.
//
// Storage:
//   data/videocalls/vapid.json       — public/private VAPID keypair (single)
//   data/videocalls/push-subs.json   — array of subscriptions, keyed by endpoint
//
// Lifecycle:
//   - VAPID keys generated on first Open() if vapid.json missing.
//   - Subs added via /api/videocall/push/subscribe.
//   - Server attempts delivery on incoming-call when user is offline.
//   - 410 Gone / 404 Not Found from the push service → sub deleted.

const pushTTL = 60 // seconds — incoming-call is time-sensitive; no point pushing later

// PushSubscription/PushSubscriptionKeys are aliases for the type in
// internal/webpush — kept here so every existing call site in this package
// (and the JSON shape in the wiring) keeps compiling unchanged after the
// store was extracted into internal/webpush.
type PushSubscription = webpush.PushSubscription
type PushSubscriptionKeys = webpush.PushSubscriptionKeys

// SendIncomingCall fires a Web Push notification to every active sub for
// `user`. Returns the number of pushes successfully delivered (HTTP 2xx).
// Subscriptions returning 410/404 are auto-removed (handled inside
// internal/webpush.Store).
// `allowDevice` filters by device policy; nil = send to everyone.
func (s *Service) SendIncomingCall(user string, ev PresenceEvent, allowDevice func(deviceID string) bool) int {
	if s.Push == nil {
		return 0
	}
	payload, err := json.Marshal(map[string]any{
		"type":      "incoming-call",
		"room_id":   ev.RoomID,
		"room_name": ev.RoomName,
		"from":      ev.From,
		"ts":        time.Now().Unix(),
	})
	if err != nil {
		return 0
	}
	return s.Push.SendToUser(context.Background(), user, payload, webpush.SendOptions{
		TTL:     pushTTL,
		Topic:   "videocall-" + ev.RoomID, // collapses duplicates at the push service
		Urgency: webpushgo.UrgencyHigh,
	}, allowDevice)
}

// ---- HTTP handlers -----------------------------------------------------

// HandlePushPublicKey returns the VAPID public key so the browser can call
// PushManager.subscribe({ applicationServerKey: <decoded> }). Unprotected
// only in the sense that any logged-in user can read it — the key is meant
// to be public.
func (s *Service) HandlePushPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		http.Error(w, "push not initialized", http.StatusServiceUnavailable)
		return
	}
	writeJSONHTTP(w, map[string]string{"public_key": s.Push.PublicKey()})
}

// HandlePushSubscribe stores a PushSubscription minted by the browser.
// Body: the JSON returned by PushSubscription.toJSON() — { endpoint, keys: { p256dh, auth } }.
func (s *Service) HandlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Push == nil {
		http.Error(w, "push not initialized", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Endpoint string               `json:"endpoint"`
		Keys     PushSubscriptionKeys `json:"keys"`
		DeviceID string               `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		http.Error(w, "incomplete subscription", http.StatusBadRequest)
		return
	}
	// User is set from auth context (never trust client-supplied).
	user := authUserFromContext(r)
	if user == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.Push.Add(&PushSubscription{
		User:      user,
		Endpoint:  req.Endpoint,
		Keys:      req.Keys,
		DeviceID:  sanitizeDeviceID(req.DeviceID),
		UserAgent: r.Header.Get("User-Agent"),
	})
	s.audit("videocall.push_subscribe", user, "")
	w.WriteHeader(http.StatusNoContent)
}

// HandlePushUnsubscribe removes a subscription. Idempotent — unsubscribing
// a non-existent endpoint is a no-op (still returns 204).
func (s *Service) HandlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.Push == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Endpoint != "" {
		s.Push.Remove(req.Endpoint)
		s.audit("videocall.push_unsubscribe", authUserFromContext(r), "")
	}
	w.WriteHeader(http.StatusNoContent)
}

// authUserFromContext extracts the username from the request context.
// Wrapped so push.go doesn't directly import auth — keeps the package
// boundaries tidy. The real implementation lives in handlers.go.
func authUserFromContext(r *http.Request) string {
	return authUserFunc(r)
}

// authUserFunc is set in handlers.go init() to avoid the import cycle that
// would happen if push.go imported internal/auth directly.
var authUserFunc func(r *http.Request) string

// ---- utilities ---------------------------------------------------------

// atomicWriteJSON has an equivalent copy in internal/webpush — kept here
// because devices.go, livecall.go and recordings.go still call this function
// directly from inside the videocall package.
func atomicWriteJSON(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Fsync the parent directory so the link is guaranteed persisted (without
	// it, on a crash between the Rename and shutdown, the file can disappear
	// even though Sync ran).
	if df, err := os.Open(filepath.Dir(path)); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
