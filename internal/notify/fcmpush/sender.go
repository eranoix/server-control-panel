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

const fcmDefaultBaseURL = "https://fcm.googleapis.com"

type SendOptions struct {
	Priority string
}

type DeviceToken struct {
	DeviceID string
	Token    string
}

type DeviceStore interface {
	TokensForUser(user string) []DeviceToken
	AllTokens() []DeviceToken
}

type Sender struct {
	store   DeviceStore
	cred    *credential
	http    *http.Client
	baseURL string
}

func NewSender(ctx context.Context, saJSON []byte, store DeviceStore) (*Sender, error) {
	cred, err := newCredential(ctx, saJSON)
	if err != nil {
		return nil, err
	}
	return &Sender{store: store, cred: cred, http: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (s *Sender) SendToUser(ctx context.Context, user string, payload []byte, opts SendOptions, allowDevice func(deviceID string) bool) int {
	if s == nil || s.store == nil {
		return 0
	}
	return s.send(ctx, s.store.TokensForUser(user), payload, opts, allowDevice)
}

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
			continue
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
			log.Printf("[fcmpush] invalid/expired token (device=%s status=%d) — consider DELETE /api/mobile/v1/notify/devices/%s", t.DeviceID, resp.StatusCode, t.DeviceID)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return delivered
}

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
