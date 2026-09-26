package whatsapp

import "io"

// Backend is the transport the Service uses to talk to a WhatsApp engine. It was
// extracted from the concrete WAHA *Client so the Service can run against either
// WAHA (the legacy REST engine) or the free whatsmeow daemon (meowClient),
// chosen per-user. The method set is exactly what Service/handlers call today;
// both implementations satisfy it (compile-time assertions below).
//
// Receiving stays unchanged: both engines deliver inbound events to the existing
// /api/whatsapp/webhook/<user> handlers (WAHA posts them; the daemon mimics the
// same envelope), so webhook.go / Store / Broadcaster don't depend on Backend.
type Backend interface {
	// Session lifecycle
	GetSession() (*wahaSession, error)
	StartSession() error
	RestartSession() error
	StopSession() error
	LogoutSession() error
	GetQR() (string, error)
	EnsureExtraWebhook(extraURL, hmacKey string, events []string) error

	// Chats / contacts
	ListChatsOverview() ([]wahaChatOverview, error)
	ListAllContacts() ([]wahaContact, error)
	GetContact(jid string) (*wahaContact, error)
	MarkChatRead(chatJID string) error
	PinChat(chatJID string, pin bool) error
	ArchiveChat(chatJID string, archive bool) error
	MuteChat(chatJID string, mute bool) error
	BlockContact(jid string, block bool) error
	CheckNumber(phone string) (jid string, onWA bool, err error)
	GroupInfo(jid string) (name string, participants []WAGroupParticipant, err error)

	// Messages
	SendText(chatJID, text, quotedID string) (string, error)
	SendFile(chatJID, msgType, filename, mime, caption, quotedID string, data []byte) (string, error)
	React(chatJID, msgID, emoji, senderJID string) error
	StarMessage(chatJID, msgID string, star bool) error
	DeleteMessage(chatJID, msgID, mode string) error
	EditMessage(chatJID, msgID, text string) error
	ForwardMessage(srcMsgID, dstChatJID string) (string, error)

	// History / media
	GetChatMessages(jid string, limit int) ([]wahaHistoryMsg, error)
	GetChatMessagesPaged(jid string, limit, offset int) ([]wahaHistoryMsg, error)
	GetChatMessagesWithMedia(jid string, limit int) ([]wahaHistoryMsg, error)
	DownloadFile(fileURL string, dst io.Writer) (int64, string, error)
	GetProfilePicture(jid string) (string, error)
	// RequestHistory asks for history on demand (recovering old media keys).
	// A no-op on WAHA; implemented on the whatsmeow daemon.
	RequestHistory(chatJID, refMsgID string, fromMe bool, ts int64, count int) error
	// ResendMessage asks for a specific message to be resent (recovering the
	// media key of an older image). A no-op on WAHA; the whatsmeow daemon
	// implements it.
	ResendMessage(chatJID, senderJID, msgID string) error

	// Presence
	SubscribePresence(jid string) error
	SendTyping(jid string, typing bool) error
}

// WAGroupParticipant describes a member of a WhatsApp group, as returned by
// Backend.GroupInfo. IsAdmin covers both admin and super-admin (the owner).
type WAGroupParticipant struct {
	JID     string `json:"jid"`
	Name    string `json:"name,omitempty"`
	IsAdmin bool   `json:"is_admin"`
}

// Compile-time guarantees that both engines implement the contract.
var (
	_ Backend = (*Client)(nil)
	_ Backend = (*meowClient)(nil)
)
