package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// TypeTelegram is the channel type id for Telegram bots.
const TypeTelegram = "telegram"

// TelegramChannel delivers via the Telegram Bot API
// (POST https://api.telegram.org/bot<token>/sendMessage). Stateless; one shared
// http.Client. The bot must already exist (created via @BotFather) and the
// target chat must have messaged the bot at least once (Telegram requirement).
type TelegramChannel struct{ client *http.Client }

func NewTelegramChannel() *TelegramChannel { return &TelegramChannel{client: &http.Client{}} }

func (t *TelegramChannel) Name() string { return TypeTelegram }

func (t *TelegramChannel) Send(ctx context.Context, ev Event, cfg ChannelConfig) error {
	if cfg.BotToken == "" {
		return errMissing("telegram", "bot_token")
	}
	if cfg.ChatID == "" {
		return errMissing("telegram", "chat_id")
	}
	body, _ := json.Marshal(map[string]any{
		"chat_id":                  cfg.ChatID,
		"text":                     FormatText(ev),
		"disable_web_page_preview": true,
	})
	url := "https://api.telegram.org/bot" + cfg.BotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram HTTP %d: %s", resp.StatusCode, b)
	}
	return nil
}

// errMissing builds a uniform "<channel>: <field> não configurado" error.
func errMissing(channel, field string) error {
	return fmt.Errorf("%s: %s not configured", channel, field)
}
