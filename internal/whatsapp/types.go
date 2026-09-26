// Package whatsapp integrates the vps-manager with a self-hosted WAHA gateway
// (https://github.com/devlikeapro/waha) running on 127.0.0.1, exposing the
// WhatsApp Web protocol as REST + webhook. The gateway is pinned to the GOWS
// engine (Whatsmeow, Go-based) for production-grade stability.
//
// Design notes:
//   - Single session ("default"). Multi-account would require WAHA Plus.
//   - Persistence is JSON on disk (no SQLite) to match the existing project
//     pattern (see internal/config/config.go).
//   - State writes are atomic (tmp + rename + fsync) — same approach as
//     config.Save.
//   - The webhook path is the ONLY public endpoint (HMAC-verified); everything
//     else lives behind auth.Middleware.
package whatsapp

import "time"

// Status enumerates the connection state. The values match what WAHA emits in
// `session.status` events, with the addition of UNPAIRED for the "no session
// has ever been created" state and FAILED for unrecoverable errors.
type Status string

const (
	StatusUnpaired Status = "UNPAIRED"     // never paired, no QR
	StatusStarting Status = "STARTING"     // container booting
	StatusScanQR   Status = "SCAN_QR_CODE" // QR available, awaiting phone scan
	StatusWorking  Status = "WORKING"      // connected & syncing
	StatusFailed   Status = "FAILED"       // unrecoverable; user action needed
	StatusStopped  Status = "STOPPED"      // session intentionally stopped
)

// State is what /api/whatsapp/status returns and what's persisted in
// data/whatsapp/state.json. Mutations go through Store.SetState (atomic save).
type State struct {
	Status        Status `json:"status"`
	Phone         string `json:"phone,omitempty"`     // "5511..." (digits only)
	PushName      string `json:"push_name,omitempty"` // WhatsApp display name
	Engine        string `json:"engine,omitempty"`    // GOWS / NOWEB / WEBJS
	WAHAVersion   string `json:"waha_version,omitempty"`
	LastSyncTS    int64  `json:"last_sync_ts,omitempty"`
	LastQRTS      int64  `json:"last_qr_ts,omitempty"`
	QRDataURL     string `json:"qr_data_url,omitempty"` // base64 data: URL; only when SCAN_QR_CODE
	HookOK        bool   `json:"hook_ok"`
	HookLastErr   string `json:"hook_last_err,omitempty"`
	HookLastTS    int64  `json:"hook_last_ts,omitempty"`
	WAHAReachable bool   `json:"waha_reachable"`
	Enabled       bool   `json:"enabled"` // false until installer runs
}

// Chat is a thread of messages (1-to-1 contact OR group). JID format:
// "55119...@s.whatsapp.net" for individual, "...@g.us" for groups.
type Chat struct {
	JID         string `json:"jid"`
	Name        string `json:"name"`
	IsGroup     bool   `json:"is_group"`
	LastMsgID   string `json:"last_msg_id,omitempty"`
	LastMsgTS   int64  `json:"last_msg_ts,omitempty"`
	LastMsgBody string `json:"last_msg_body,omitempty"` // truncated preview
	UnreadCount int    `json:"unread_count"`
	Archived    bool   `json:"archived,omitempty"`
	Pinned      bool   `json:"pinned,omitempty"`
	Muted       bool   `json:"muted,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`   // profile picture, served via /api/whatsapp/avatar/<jid>
	Presence    string `json:"presence,omitempty"`     // online / offline / composing / recording
	PresenceTS  int64  `json:"presence_ts,omitempty"`  // when last presence was reported (UNIX)
	LastSeenTS  int64  `json:"last_seen_ts,omitempty"` // best-effort: UNIX time of last online state
	UpdatedAt   int64  `json:"updated_at"`
}

// Message is a single WhatsApp message. Mirrors WAHA's webhook event shape
// closely so re-ingest after restart is straightforward.
type Message struct {
	ID        string     `json:"id"`
	ChatJID   string     `json:"chat"`
	FromJID   string     `json:"from,omitempty"`
	FromMe    bool       `json:"from_me"`
	TS        int64      `json:"ts"`
	Type      string     `json:"type"`           // text / image / audio / document / video / sticker / location / contact / system
	Body      string     `json:"body,omitempty"` // text or caption
	Media     *Media     `json:"media,omitempty"`
	QuotedID  string     `json:"quoted_id,omitempty"`
	Ack       int        `json:"ack"` // 0=pending 1=server 2=device 3=read 4=played
	Deleted   bool       `json:"deleted,omitempty"`
	Reactions []Reaction `json:"reactions,omitempty"` // 1 per (from, emoji); empty emoji = removed
	RawJSON   string     `json:"raw,omitempty"`       // original webhook payload (truncated)
}

// Reaction represents an emoji reaction applied to a message. WhatsApp allows
// one reaction per user per message — repeated updates from the same `From`
// OVERWRITE rather than accumulate. An empty reaction (emoji="") means the
// user removed theirs.
type Reaction struct {
	From  string `json:"from"`  // reactor JID (@c.us or canonicalized @lid)
	Emoji string `json:"emoji"` // "" = removed (does not render)
	TS    int64  `json:"ts"`    // unix seconds of the last update
}

// Media references a downloaded attachment on disk. Path is relative to the
// media root (/var/lib/vpsm-whatsapp/media) — the HTTP handler validates and
// serves under /api/whatsapp/media/<path>.
type Media struct {
	Path     string `json:"path"`
	MimeType string `json:"mime,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Filename string `json:"filename,omitempty"`
	Duration int    `json:"duration,omitempty"` // seconds, for audio/video
	Width    int    `json:"w,omitempty"`
	Height   int    `json:"h,omitempty"`
}

// Contact metadata, used to enrich chat list (avatar, display name).
type Contact struct {
	JID        string `json:"jid"`
	Name       string `json:"name,omitempty"`      // saved-contact name
	PushName   string `json:"push_name,omitempty"` // user-set in WhatsApp
	IsBusiness bool   `json:"is_business,omitempty"`
	AvatarURL  string `json:"avatar_url,omitempty"` // served from local cache
	UpdatedAt  int64  `json:"updated_at"`
}

// WSEvent is what the broadcaster pushes to connected UI clients. The Kind
// discriminates payload; the UI dispatches per kind.
type WSEvent struct {
	Kind          string   `json:"kind"` // status / message / ack / qr / chat / presence / revoked / reaction
	State         *State   `json:"state,omitempty"`
	Message       *Message `json:"message,omitempty"`
	Chat          *Chat    `json:"chat,omitempty"`
	AckID         string   `json:"ack_id,omitempty"`
	AckN          int      `json:"ack_n,omitempty"`
	ChatJID       string   `json:"chat_jid,omitempty"`
	Presence      string   `json:"presence,omitempty"` // available/unavailable/composing/recording
	LastSeenTS    int64    `json:"last_seen_ts,omitempty"`
	ReactionFrom  string   `json:"reaction_from,omitempty"`  // JID of whoever reacted
	ReactionEmoji string   `json:"reaction_emoji,omitempty"` // "" = removida
	TS            int64    `json:"ts"`
}

// pollInterval governs how often we poll WAHA for session status. 30s is a
// balance between freshness and noise — webhooks deliver real-time updates;
// the poll is the safety net.
const pollInterval = 30 * time.Second

// httpTimeout is the default HTTP client timeout for normal WAHA calls.
// Uploads use a longer timeout (uploadTimeout) to accommodate large media.
const httpTimeout = 10 * time.Second
const uploadTimeout = 120 * time.Second
