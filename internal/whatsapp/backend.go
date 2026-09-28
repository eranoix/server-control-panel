package whatsapp

import "io"

type Backend interface {
	GetSession() (*wahaSession, error)
	StartSession() error
	RestartSession() error
	StopSession() error
	LogoutSession() error
	GetQR() (string, error)
	EnsureExtraWebhook(extraURL, hmacKey string, events []string) error

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

	SendText(chatJID, text, quotedID string) (string, error)
	SendFile(chatJID, msgType, filename, mime, caption, quotedID string, data []byte) (string, error)
	React(chatJID, msgID, emoji, senderJID string) error
	StarMessage(chatJID, msgID string, star bool) error
	DeleteMessage(chatJID, msgID, mode string) error
	EditMessage(chatJID, msgID, text string) error
	ForwardMessage(srcMsgID, dstChatJID string) (string, error)

	GetChatMessages(jid string, limit int) ([]wahaHistoryMsg, error)
	GetChatMessagesPaged(jid string, limit, offset int) ([]wahaHistoryMsg, error)
	GetChatMessagesWithMedia(jid string, limit int) ([]wahaHistoryMsg, error)
	DownloadFile(fileURL string, dst io.Writer) (int64, string, error)
	GetProfilePicture(jid string) (string, error)
	RequestHistory(chatJID, refMsgID string, fromMe bool, ts int64, count int) error
	ResendMessage(chatJID, senderJID, msgID string) error

	SubscribePresence(jid string) error
	SendTyping(jid string, typing bool) error
}

type WAGroupParticipant struct {
	JID     string `json:"jid"`
	Name    string `json:"name,omitempty"`
	IsAdmin bool   `json:"is_admin"`
}

var (
	_ Backend = (*Client)(nil)
	_ Backend = (*meowClient)(nil)
)
