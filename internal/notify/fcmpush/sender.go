// Package fcmpush is notify's second push-notification backend: Android
// devices reached via Firebase Cloud Messaging (native push — works even
// with the app fully closed), sitting next to internal/webpush's
// browser-facing Web Push backend behind the SAME "push" notify.Channel via
// a fan-out pushSender (see internal/notify/pushchannel.go's
// FanOutSenders/NewFCMSender). Sender knows nothing about Rules,
// throttling, or dedup — the same division of labor internal/webpush.Store
// already keeps; internal/notify.Router owns all of that.
package fcmpush

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// fcmDefaultBaseURL is the real FCM HTTP v1 host. Overridable per-Sender
// (unexported baseURL field) only so tests can point at an httptest.Server
// instead — production code always gets the zero value and therefore this
// default.
const fcmDefaultBaseURL = "https://fcm.googleapis.com"

// SendOptions parametrizes one FCM send. Unlike Web Push, FCM v1 has no
// per-message TTL in the envelope this package uses; Priority maps to
// AndroidConfig.Priority ("high"/"normal") — FCM's equivalent of
// notify.SendOptions.Critical (see pushchannel.go's toFCM, the only place
// this package's vocabulary meets notify's, mirroring toWebpush).
type SendOptions struct {
	Priority string // "high" or "normal"; empty defaults to "normal"
}

// DeviceToken is one registered Android device's FCM destination.
type DeviceToken struct {
	DeviceID string
	Token    string
}

// DeviceStore is the seam Sender uses to resolve which FCM tokens belong to
// a user, or to enumerate every registered token for a broadcast send.
// Defined here (consumer side), exactly like notify's own pushSender
// interface: a mobilebff.DeviceTokenStore-backed adapter (wired in
// internal/api/notify_wire.go) satisfies this structurally — this package
// never imports internal/mobilebff.
type DeviceStore interface {
	TokensForUser(user string) []DeviceToken
	AllTokens() []DeviceToken
}

// Sender implements the same SendToUser/SendToAll method set as
// internal/webpush.Store, so internal/notify/pushchannel.go can hold it as
// a second pushSender behind the identical "push" notify.Channel.
type Sender struct {
	store DeviceStore
	cred  *credential
	http  *http.Client
	// baseURL overrides fcmDefaultBaseURL; only ever set by tests in this
	// package (white-box construction), never by production callers.
	baseURL string
}

// NewSender builds a Sender from the FCM service-account JSON (saJSON,
// loaded by the caller from the vault — see internal/api/notify_wire.go)
// and store (the device-token lookup; may be nil, degrading every send to
// zero deliveries instead of panicking, matching NewWebpushSender(nil)'s
// degrade-on-missing-config behavior).
func NewSender(ctx context.Context, saJSON []byte, store DeviceStore) (*Sender, error) {
	cred, err := newCredential(ctx, saJSON)
	if err != nil {
		return nil, err
	}
	return &Sender{store: store, cred: cred, http: &http.Client{Timeout: 10 * time.Second}}, nil
}

// SendToUser fires payload to every FCM-registered device for user.
// Returns the number of pushes FCM accepted (HTTP 2xx). allowDevice
// filters by device policy; nil = send to every one of the user's devices.
func (s *Sender) SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int {
	if s == nil || s.store == nil {
		return 0
	}
	return s.send(ctx, s.store.TokensForUser(user), payload, opts, allowDevice)
}

// SendToAll fires payload to every registered device, regardless of user.
// Returns the number of pushes FCM accepted (HTTP 2xx). allowDevice
// filters by device policy; nil = send to everyone.
func (s *Sender) SendToAll(ctx context.Context, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int {
	if s == nil || s.store == nil {
		return 0
	}
	return s.send(ctx, s.store.AllTokens(), payload, opts, allowDevice)
}

func (s *Sender) send(ctx context.Context, tokens []DeviceToken, payload []byte, opts SendOptions, allowDevice func(string) bool) int {
	return s.deliver(ctx, tokens, allowDevice, func(token string) ([]byte, error) {
		return buildMessageBody(token, payload, opts)
	})
}

// deliver is the token-iteration/auth/HTTP loop shared by every send-shaped
// method this package exposes (SendToUser/SendToAll here, and
// SendDataToUser/SendDataToAll in data_message.go) — the ONE place that
// mints the bearer token, walks tokens, applies allowDevice, and interprets
// the FCM v1 HTTP response. bodyFor builds the per-token request body; it is
// the only thing that differs between a generic alert message and a
// data-only call-ring message.
func (s *Sender) deliver(ctx context.Context, tokens []DeviceToken, allowDevice func(string) bool, bodyFor func(token string) ([]byte, error)) int {
	if len(tokens) == 0 {
		return 0
	}
	bearer, err := s.cred.bearer()
	if err != nil {
		log.Printf("[fcmpush] token: %v", err)
		return 0
	}
	base := s.baseURL
	if base == "" {
		base = fcmDefaultBaseURL
	}
	url := fmt.Sprintf("%s/v1/projects/%s/messages:send", base, s.cred.projectID)
	delivered := 0
	for _, t := range tokens {
		if allowDevice != nil && !allowDevice(t.DeviceID) {
			continue // dispositivo silenciado pelo dono
		}
		body, err := bodyFor(t.Token)
		if err != nil {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		req.Header.Set("Authorization", bearer)
		resp, err := s.http.Do(req)
		if err != nil {
			continue
		}
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			delivered++
		case resp.StatusCode == 404 || resp.StatusCode == 410:
			// Token no longer valid (app uninstalled / token rotated) —
			// FCM's own signal to stop sending, mirroring webpush.Store's
			// 410/404 auto-cleanup. This package holds no persistence of
			// its own (DeviceStore owns that), so it only reports the
			// condition instead of self-healing the registry.
			log.Printf("[fcmpush] invalid/expired token (device=%s status=%d) — consider DELETE /api/mobile/v1/notify/devices/%s", t.DeviceID, resp.StatusCode, t.DeviceID)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return delivered
}

// buildMessageBody shapes the FCM HTTP v1 request body:
//
//	{"message": {"token": "...", "data": {...}, "android": {"priority": "..."}}}
//
// payload (the already-marshaled generic notify JSON built by
// pushchannel.go's buildPushPayload) becomes the FCM "data" payload
// (string-keyed, string-valued, per FCM v1's data-message contract) rather
// than "notification" — the Android client's own notification-building
// code decides the UI, matching how the existing webpush "push" listener
// already renders from a raw JSON data payload instead of the browser's
// default auto-rendered notification.
func buildMessageBody(token string, payload []byte, opts SendOptions) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	data := make(map[string]string, len(raw))
	for k, v := range raw {
		if sv, ok := v.(string); ok {
			data[k] = sv
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		data[k] = string(b)
	}
	priority := opts.Priority
	if priority == "" {
		priority = "normal"
	}
	msg := map[string]any{
		"message": map[string]any{
			"token": token,
			"data":  data,
			"android": map[string]any{
				"priority": priority,
			},
		},
	}
	return json.Marshal(msg)
}
