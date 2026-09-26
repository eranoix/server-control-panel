package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// TypeWebhook is the channel type id for generic outbound webhooks.
const TypeWebhook = "webhook"

// WebhookChannel POSTs the Event as JSON to a configured URL — the generic
// escape hatch (Slack/Discord incoming webhooks, n8n, custom endpoints, …).
type WebhookChannel struct{ client *http.Client }

func NewWebhookChannel() *WebhookChannel { return &WebhookChannel{client: &http.Client{}} }

func (w *WebhookChannel) Name() string { return TypeWebhook }

func (w *WebhookChannel) Send(ctx context.Context, ev Event, cfg ChannelConfig) error {
	if cfg.URL == "" {
		return errMissing("webhook", "url")
	}
	// Minimal guard: only http(s). The destination is configured by the primary
	// admin on their own host, so we don't impose an allowlist — but we refuse
	// non-http schemes (file://, gopher://, …) outright.
	if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
		return fmt.Errorf("webhook: url must be http(s)")
	}
	// 🔴 A FIXED BODY WINS, AND IT NEVER MIXES WITH THE EVENT.
	//
	// When the destination is public (a free-plan ntfy topic, say), what goes
	// out is EXACTLY the configured text — nothing from the Event crosses over.
	// Attaching "just the title" or "just the severity" would already be telling
	// the public what broke and when; the only safe version is the one that
	// carries nothing.
	body := []byte(cfg.FixedBody)
	tipo := "text/plain; charset=utf-8"
	if cfg.FixedBody == "" {
		body, _ = json.Marshal(ev)
		tipo = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", tipo)
	req.Header.Set("User-Agent", "vps-manager-notify/1")
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("webhook HTTP %d: %s", resp.StatusCode, b)
	}
	return nil
}
