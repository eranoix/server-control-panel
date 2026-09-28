package mobilebff

import (
	"context"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/whatsapp"
)

func init() { Register("whatsapp", registerWhatsapp) }

type whatsappSvc interface {
	ListChats() []whatsapp.Chat
	MessagesForDisplay(jid string, opts whatsapp.MessagesQuery) ([]whatsapp.Message, bool, error)
	SendTextDedup(chatJID, text, quotedID, clientMsgID string) (string, error)
	MarkRead(jid string) error
	ServeAvatar(w http.ResponseWriter, r *http.Request, jid string)
	DownloadMediaForMessage(chatJID, msgID string) (rel, mimeType, filename string, size int64, err error)
	ServeMediaRel(w http.ResponseWriter, r *http.Request, rel string)
	SendFileDedup(chatJID, msgType, filename, mimeType, caption, quotedID, clientMsgID string, data []byte) (string, error)
}

type whatsappResolver func(ctx context.Context) (whatsappSvc, error)

func managerResolver(mgr *whatsapp.Manager) whatsappResolver {
	return func(ctx context.Context) (whatsappSvc, error) {
		if mgr == nil {
			return nil, huma.Error503ServiceUnavailable("whatsapp not configured")
		}
		raw := auth.UserFromContext(ctx)
		if raw == "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		u, err := scope.New(raw)
		if err != nil {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		svc, err := mgr.ForUser(u)
		if err != nil {
			return nil, huma.Error503ServiceUnavailable(err.Error())
		}
		return svc, nil
	}
}

type ChatSummary struct {
	Jid                string `json:"jid"`
	Name               string `json:"name"`
	IsGroup            bool   `json:"is_group"`
	Unread             int    `json:"unread"`
	LastMessageAt      int64  `json:"last_message_at,omitempty"`
	LastMessagePreview string `json:"last_message_preview,omitempty"`
	AvatarUrl          string `json:"avatar_url,omitempty"`
}

func chatToSummary(c whatsapp.Chat) ChatSummary {
	return ChatSummary{
		Jid:                c.JID,
		Name:               c.Name,
		IsGroup:            c.IsGroup,
		Unread:             c.UnreadCount,
		LastMessageAt:      c.LastMsgTS,
		LastMessagePreview: c.LastMsgBody,
		AvatarUrl:          c.AvatarURL,
	}
}

type chatsOutput struct {
	Body []ChatSummary
}

type MediaView struct {
	Url      string `json:"url"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Filename string `json:"filename,omitempty"`
	Duration int    `json:"duration,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

type MessageView struct {
	ID        string              `json:"id"`
	ChatJID   string              `json:"chat_jid"`
	FromMe    bool                `json:"from_me"`
	Sender    string              `json:"sender,omitempty"`
	Text      string              `json:"text,omitempty"`
	Type      string              `json:"type"`
	TS        int64               `json:"ts"`
	Ack       int                 `json:"ack"`
	QuotedID  string              `json:"quoted_id,omitempty"`
	Media     *MediaView          `json:"media,omitempty"`
	Reactions []whatsapp.Reaction `json:"reactions,omitempty"`
}

func messageToView(jid string, m whatsapp.Message) MessageView {
	v := MessageView{
		ID:        m.ID,
		ChatJID:   m.ChatJID,
		FromMe:    m.FromMe,
		Sender:    m.FromJID,
		Text:      m.Body,
		Type:      m.Type,
		TS:        m.TS,
		Ack:       m.Ack,
		QuotedID:  m.QuotedID,
		Reactions: m.Reactions,
	}
	if m.Media != nil {
		v.Media = &MediaView{
			Url:      Prefix + "/whatsapp/chats/" + jid + "/media/" + m.ID,
			MimeType: m.Media.MimeType,
			Size:     m.Media.Size,
			Filename: m.Media.Filename,
			Duration: m.Media.Duration,
			Width:    m.Media.Width,
			Height:   m.Media.Height,
		}
	}
	return v
}

type MessagesResponse struct {
	Messages    []MessageView `json:"messages"`
	Backfilling bool          `json:"backfilling"`
}

type messagesInput struct {
	JID    string `path:"jid"`
	Before int64  `query:"before"`
	Limit  int    `query:"limit"`
}

type messagesOutput struct {
	Body MessagesResponse
}

type SendMessageRequest struct {
	Text        string `json:"text"`
	ClientMsgID string `json:"client_msg_id"`
	QuotedID    string `json:"quoted_id,omitempty"`
}

type sendMessageInput struct {
	JID  string `path:"jid"`
	Body SendMessageRequest
}

type SendMessageResponse struct {
	ID string `json:"id"`
}

type sendMessageOutput struct {
	Body SendMessageResponse
}

type readInput struct {
	JID string `path:"jid"`
}

type readOutput struct{}

type avatarInput struct {
	JID string `path:"jid"`
}

func registerWhatsapp(api huma.API, deps Deps) {
	registerWhatsappWithResolver(api, managerResolver(deps.WhatsAppMgr))
}

func registerWhatsappWithResolver(api huma.API, resolve whatsappResolver) {
	huma.Register(api, huma.Operation{
		OperationID: "listWhatsAppChats",
		Method:      http.MethodGet,
		Path:        "/whatsapp/chats",
		Summary:     "WhatsApp chat list with preview and unread badge",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, listChatsHandler(resolve))

	huma.Register(api, huma.Operation{
		OperationID: "getWhatsAppMessages",
		Method:      http.MethodGet,
		Path:        "/whatsapp/chats/{jid}/messages",
		Summary:     "Message history of a chat, with automatic backfill",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusInternalServerError},
	}, getMessagesHandler(resolve))

	huma.Register(api, huma.Operation{
		OperationID: "sendWhatsAppMessage",
		Method:      http.MethodPost,
		Path:        "/whatsapp/chats/{jid}/messages",
		Summary:     "Sends a text message; idempotent by client_msg_id",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusBadGateway},
	}, sendMessageHandler(resolve))

	huma.Register(api, huma.Operation{
		OperationID: "markWhatsAppChatRead",
		Method:      http.MethodPost,
		Path:        "/whatsapp/chats/{jid}/read",
		Summary:     "Marks a chat as read",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusBadGateway},
	}, markReadHandler(resolve))

	huma.Register(api, huma.Operation{
		OperationID: "getWhatsAppAvatar",
		Method:      http.MethodGet,
		Path:        "/whatsapp/chats/{jid}/avatar",
		Summary:     "Contact/group profile photo (same-origin anti-SSRF proxy)",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, avatarHandler(resolve))
}

func listChatsHandler(resolve whatsappResolver) func(ctx context.Context, input *struct{}) (*chatsOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*chatsOutput, error) {
		svc, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		chats := svc.ListChats()
		out := make([]ChatSummary, len(chats))
		for i, c := range chats {
			out[i] = chatToSummary(c)
		}
		return &chatsOutput{Body: out}, nil
	}
}

func getMessagesHandler(resolve whatsappResolver) func(ctx context.Context, input *messagesInput) (*messagesOutput, error) {
	return func(ctx context.Context, input *messagesInput) (*messagesOutput, error) {
		svc, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		msgs, backfilling, err := svc.MessagesForDisplay(input.JID, whatsapp.MessagesQuery{Before: input.Before, Limit: input.Limit})
		if err != nil {
			log.Printf("mobilebff: error reading messages from %q: %v", input.JID, err)
			return nil, huma.Error500InternalServerError("internal error")
		}
		views := make([]MessageView, len(msgs))
		for i, m := range msgs {
			views[i] = messageToView(input.JID, m)
		}
		return &messagesOutput{Body: MessagesResponse{Messages: views, Backfilling: backfilling}}, nil
	}
}

func sendMessageHandler(resolve whatsappResolver) func(ctx context.Context, input *sendMessageInput) (*sendMessageOutput, error) {
	return func(ctx context.Context, input *sendMessageInput) (*sendMessageOutput, error) {
		svc, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		id, err := svc.SendTextDedup(input.JID, input.Body.Text, input.Body.QuotedID, input.Body.ClientMsgID)
		if err != nil {
			log.Printf("mobilebff: error sending a message to %q: %v", input.JID, err)
			return nil, huma.Error502BadGateway("upstream error")
		}
		return &sendMessageOutput{Body: SendMessageResponse{ID: id}}, nil
	}
}

func markReadHandler(resolve whatsappResolver) func(ctx context.Context, input *readInput) (*readOutput, error) {
	return func(ctx context.Context, input *readInput) (*readOutput, error) {
		svc, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		if err := svc.MarkRead(input.JID); err != nil {
			log.Printf("mobilebff: error marking %q as read: %v", input.JID, err)
			return nil, huma.Error502BadGateway("upstream error")
		}
		return &readOutput{}, nil
	}
}

func avatarHandler(resolve whatsappResolver) func(ctx context.Context, input *avatarInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *avatarInput) (*huma.StreamResponse, error) {
		svc, err := resolve(ctx)
		if err != nil {
			return nil, err
		}
		return &huma.StreamResponse{
			Body: func(hctx huma.Context) {
				req, w := humago.Unwrap(hctx)
				svc.ServeAvatar(w, req, input.JID)
			},
		}, nil
	}
}
