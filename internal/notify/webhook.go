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

const TypeWebhook = "webhook"

type WebhookChannel struct{ client *http.Client }

func NewWebhookChannel() *WebhookChannel { return &WebhookChannel{client: &http.Client{}} }

func (w *WebhookChannel) Name() string { return TypeWebhook }

func (w *WebhookChannel) Send(ctx context.Context, ev Event, cfg ChannelConfig) error {
	if cfg.URL == "" {
		return errMissing("webhook", "url")
	}
	if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
		return fmt.Errorf("webhook: url must be http(s)")
	}
	body := []byte(cfg.FixedBody)
	kind := "text/plain; charset=utf-8"
	if cfg.FixedBody == "" {
		body, _ = json.Marshal(ev)
		kind = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", kind)
	req.Header.Set("User-Agent", "server-control-panel-notify/1")
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
