package notify

import "context"

// ChannelConfig holds the per-destination settings a Channel needs to deliver.
// It is a flat union of every channel type's fields; only the ones relevant to
// a given ChannelDef.Type are read by that type's Channel impl.
type ChannelConfig struct {
	// WhatsApp
	FromUser string `json:"from_user,omitempty"` // scope user whose WAHA session sends
	ChatJID  string `json:"chat_jid,omitempty"`  // destination chat (e.g. "<num>@c.us")
	// Telegram
	BotToken string `json:"bot_token,omitempty"` // SECRET — bot token from @BotFather
	ChatID   string `json:"chat_id,omitempty"`   // numeric chat/group id
	// E-mail (SMTP)
	SMTPHost string `json:"smtp_host,omitempty"`
	SMTPPort int    `json:"smtp_port,omitempty"`
	SMTPUser string `json:"smtp_user,omitempty"`
	SMTPPass string `json:"smtp_pass,omitempty"` // SECRET
	From     string `json:"from,omitempty"`      // e-mail From address
	To       string `json:"to,omitempty"`        // e-mail To address (comma-separated)
	// Push
	// ToUser restricts delivery to one user; empty sends to EVERY stored
	// subscription.
	ToUser string `json:"to_user,omitempty"`
	// ToDevice narrows it further, to a single device_id belonging to ToUser;
	// empty sends to every registered device of that user (per-device routing
	// — see PushChannel.Send in pushchannel.go, which combines this with the
	// allowDevice check driven by preference and Rule).
	ToDevice string `json:"to_device,omitempty"`
	// Webhook
	URL string `json:"url,omitempty"` // POST target; receives the Event as JSON
	// 🔴 FixedBody REPLACES the POST body with a FIXED text — the whole event
	// is NOT sent.
	//
	// It exists because not every destination is private. An ntfy topic on the
	// free plan is PUBLIC: anyone who guesses the name reads everything that
	// passes through it. A webhook that dumps the Event as JSON sends it the
	// title, the body, the labels and the name of whatever broke — in other
	// words a live map of the house's problems, for anyone to read.
	//
	// With FixedBody the same destination becomes an honest "wake up": it
	// arrives anywhere, carries no intelligence at all, and the detail goes out
	// over an authenticated channel. It is the same design the PVE webhook
	// already uses (a fixed body in base64).
	FixedBody string `json:"fixed_body,omitempty"`
}

// secretFields lists ChannelConfig keys that hold credentials. They are redacted
// when listing channels (GET) and preserved across an edit when left blank, so
// secrets never round-trip through the browser. See Router.redactSecrets and
// UpsertChannel.
func (c ChannelConfig) hasSecret(field string) bool {
	switch field {
	case "bot_token":
		return c.BotToken != ""
	case "smtp_pass":
		return c.SMTPPass != ""
	}
	return false
}

// Channel is a delivery backend. Implementations are stateless w.r.t. routing
// (the Router owns throttle/dedup/breaker); a Channel only knows how to turn
// one Event into one outbound message using cfg, honoring ctx's deadline.
//
// Send MUST respect ctx cancellation (the Router gives it a 5s timeout) and
// return a non-nil error on failure so the circuit breaker can trip.
type Channel interface {
	// Name is the channel TYPE id ("whatsapp"), matched against ChannelDef.Type.
	Name() string
	Send(ctx context.Context, ev Event, cfg ChannelConfig) error
}

// ChannelDef is a configured, persisted destination instance — one row in the
// "channels" list the operator manages in the Alertas tab. Its Type selects
// which registered Channel impl delivers it; Config carries that impl's
// settings. Many ChannelDefs can share one Type (e.g. two WhatsApp chats).
type ChannelDef struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`    // human label
	Type    string        `json:"type"`    // "whatsapp"
	Enabled bool          `json:"enabled"` // disabled channels are skipped, not deleted
	Config  ChannelConfig `json:"config"`
}
