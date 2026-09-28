package whatsapp

import "time"

type Status string

const (
	StatusUnpaired Status = "UNPAIRED"
	StatusStarting Status = "STARTING"
	StatusScanQR   Status = "SCAN_QR_CODE"
	StatusWorking  Status = "WORKING"
	StatusFailed   Status = "FAILED"
	StatusStopped  Status = "STOPPED"
)

type State struct {
	Status        Status `json:"status"`
	Phone         string `json:"phone,omitempty"`
	PushName      string `json:"push_name,omitempty"`
	Engine        string `json:"engine,omitempty"`
	WAHAVersion   string `json:"waha_version,omitempty"`
	LastSyncTS    int64  `json:"last_sync_ts,omitempty"`
	LastQRTS      int64  `json:"last_qr_ts,omitempty"`
	QRDataURL     string `json:"qr_data_url,omitempty"`
	HookOK        bool   `json:"hook_ok"`
	HookLastErr   string `json:"hook_last_err,omitempty"`
	HookLastTS    int64  `json:"hook_last_ts,omitempty"`
	WAHAReachable bool   `json:"waha_reachable"`
	Enabled       bool   `json:"enabled"`
}

type Chat struct {
	JID         string `json:"jid"`
	Name        string `json:"name"`
	IsGroup     bool   `json:"is_group"`
	LastMsgID   string `json:"last_msg_id,omitempty"`
	LastMsgTS   int64  `json:"last_msg_ts,omitempty"`
	LastMsgBody string `json:"last_msg_body,omitempty"`
	UnreadCount int    `json:"unread_count"`
	Archived    bool   `json:"archived,omitempty"`
	Pinned      bool   `json:"pinned,omitempty"`
	Muted       bool   `json:"muted,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	Presence    string `json:"presence,omitempty"`
	PresenceTS  int64  `json:"presence_ts,omitempty"`
	LastSeenTS  int64  `json:"last_seen_ts,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

type Message struct {
	ID        string     `json:"id"`
	ChatJID   string     `json:"chat"`
	FromJID   string     `json:"from,omitempty"`
	FromMe    bool       `json:"from_me"`
	TS        int64      `json:"ts"`
	Type      string     `json:"type"`
	Body      string     `json:"body,omitempty"`
	Media     *Media     `json:"media,omitempty"`
	QuotedID  string     `json:"quoted_id,omitempty"`
	Ack       int        `json:"ack"`
	Deleted   bool       `json:"deleted,omitempty"`
	Reactions []Reaction `json:"reactions,omitempty"`
	RawJSON   string     `json:"raw,omitempty"`
}

type Reaction struct {
	From  string `json:"from"`
	Emoji string `json:"emoji"`
	TS    int64  `json:"ts"`
}

type Media struct {
	Path     string `json:"path"`
	MimeType string `json:"mime,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Filename string `json:"filename,omitempty"`
	Duration int    `json:"duration,omitempty"`
	Width    int    `json:"w,omitempty"`
	Height   int    `json:"h,omitempty"`
}

type Contact struct {
	JID        string `json:"jid"`
	Name       string `json:"name,omitempty"`
	PushName   string `json:"push_name,omitempty"`
	IsBusiness bool   `json:"is_business,omitempty"`
	AvatarURL  string `json:"avatar_url,omitempty"`
	UpdatedAt  int64  `json:"updated_at"`
}

type WSEvent struct {
	Kind          string   `json:"kind"`
	State         *State   `json:"state,omitempty"`
	Message       *Message `json:"message,omitempty"`
	Chat          *Chat    `json:"chat,omitempty"`
	AckID         string   `json:"ack_id,omitempty"`
	AckN          int      `json:"ack_n,omitempty"`
	ChatJID       string   `json:"chat_jid,omitempty"`
	Presence      string   `json:"presence,omitempty"`
	LastSeenTS    int64    `json:"last_seen_ts,omitempty"`
	ReactionFrom  string   `json:"reaction_from,omitempty"`
	ReactionEmoji string   `json:"reaction_emoji,omitempty"`
	TS            int64    `json:"ts"`
}

const pollInterval = 30 * time.Second

const httpTimeout = 10 * time.Second
const uploadTimeout = 120 * time.Second
