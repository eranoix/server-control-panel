package notify

import "context"

type ChannelConfig struct {
	FromUser  string `json:"from_user,omitempty"`
	ChatJID   string `json:"chat_jid,omitempty"`
	BotToken  string `json:"bot_token,omitempty"`
	ChatID    string `json:"chat_id,omitempty"`
	SMTPHost  string `json:"smtp_host,omitempty"`
	SMTPPort  int    `json:"smtp_port,omitempty"`
	SMTPUser  string `json:"smtp_user,omitempty"`
	SMTPPass  string `json:"smtp_pass,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	ToUser    string `json:"to_user,omitempty"`
	ToDevice  string `json:"to_device,omitempty"`
	URL       string `json:"url,omitempty"`
	FixedBody string `json:"fixed_body,omitempty"`
}

func (c ChannelConfig) hasSecret(field string) bool {
	switch field {
	case "bot_token":
		return c.BotToken != ""
	case "smtp_pass":
		return c.SMTPPass != ""
	}
	return false
}

type Channel interface {
	Name() string
	Send(ctx context.Context, ev Event, cfg ChannelConfig) error
}

type ChannelDef struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Type    string        `json:"type"`
	Enabled bool          `json:"enabled"`
	Config  ChannelConfig `json:"config"`
}
