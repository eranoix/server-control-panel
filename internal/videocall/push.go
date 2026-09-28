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

const pushTTL = 60

type PushSubscription = webpush.PushSubscription
type PushSubscriptionKeys = webpush.PushSubscriptionKeys

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
		Topic:   "videocall-" + ev.RoomID,
		Urgency: webpushgo.UrgencyHigh,
	}, allowDevice)
}

func (s *Service) HandlePushPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil {
		http.Error(w, "push not initialized", http.StatusServiceUnavailable)
		return
	}
	writeJSONHTTP(w, map[string]string{"public_key": s.Push.PublicKey()})
}

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

func authUserFromContext(r *http.Request) string {
	return authUserFunc(r)
}

var authUserFunc func(r *http.Request) string

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
	if df, err := os.Open(filepath.Dir(path)); err == nil {
		_ = df.Sync()
		_ = df.Close()
	}
	return nil
}
