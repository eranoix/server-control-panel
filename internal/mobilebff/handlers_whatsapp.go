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

// registerWhatsapp registers WhatsApp's five screen routes — the app's only
// way of touching WhatsApp: always through here, never /api/whatsapp/*
// (that other surface belongs to the web panel and uses a cookie, not a
// Bearer). No domain logic lives here: each handler resolves the
// authenticated user's *whatsapp.Service (the same `ForUser(scope.User)` the
// panel already uses) and calls one of the four wrappers in
// internal/whatsapp/service_export.go directly — repeating here what that
// file already does would be exactly the fork that took the previous mobile
// surface down.
func init() { Register("whatsapp", registerWhatsapp) }

// whatsappSvc is the minimal slice of *whatsapp.Service that this file calls —
// an interface, not the concrete type, so that tests can substitute a fake
// without needing a real *whatsapp.Manager (which requires a vault, a WAHA
// container and so on). *whatsapp.Service satisfies this through the methods
// already exported in service_export.go.
type whatsappSvc interface {
	ListChats() []whatsapp.Chat
	MessagesForDisplay(jid string, opts whatsapp.MessagesQuery) ([]whatsapp.Message, bool, error)
	SendTextDedup(chatJID, text, quotedID, clientMsgID string) (string, error)
	MarkRead(jid string) error
	ServeAvatar(w http.ResponseWriter, r *http.Request, jid string)
	// DownloadMediaForMessage, ServeMediaRel and SendFileDedup are the same
	// wrappers from internal/whatsapp/service_export.go that the panel uses —
	// see handlers_whatsapp_media.go.
	DownloadMediaForMessage(chatJID, msgID string) (rel, mimeType, filename string, size int64, err error)
	ServeMediaRel(w http.ResponseWriter, r *http.Request, rel string)
	SendFileDedup(chatJID, msgType, filename, mimeType, caption, quotedID, clientMsgID string, data []byte) (string, error)
}

// whatsappResolver resolves the *whatsapp.Service of the current request's
// authenticated user. A signature of its own (rather than coupling directly to
// *whatsapp.Manager) so the test can inject a fake resolver without having to
// build a real Manager.
type whatsappResolver func(ctx context.Context) (whatsappSvc, error)

// managerResolver adapts a real *whatsapp.Manager to whatsappResolver — the
// same userFromReq -> scope.New -> ForUser chain internal/api/api.go already
// uses (vc.WhatsAppSender, the panel's v2 avatar handler); no new identity
// mechanism.
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

// ChatSummary is the projection of whatsapp.Chat for the app's conversation
// list — the same fields the panel already shows (name, preview, unread,
// avatar), renamed to keys stable for the mobile contract. No new field: it is
// the same Store.ListChats() read the panel does.
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

// MediaView projects whatsapp.Media for the app: instead of the local Path (a
// server storage detail), it points at the media endpoint — this part only
// draws the pointer, it does not serve the bytes.
type MediaView struct {
	Url      string `json:"url"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Filename string `json:"filename,omitempty"`
	Duration int    `json:"duration,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

// MessageView is the projection of whatsapp.Message for the app — the same
// fields the panel already stores/displays, with JSON keys stable for the
// mobile contract (the panel uses different ones for historical compatibility).
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

// readOutput has no Body: huma answers 204 automatically (the same contract
// /api/whatsapp/*'s handleMarkRead already uses — mark-read returns no body).
type readOutput struct{}

type avatarInput struct {
	JID string `path:"jid"`
}

func registerWhatsapp(api huma.API, deps Deps) {
	registerWhatsappWithResolver(api, managerResolver(deps.WhatsAppMgr))
}

// registerWhatsappWithResolver exists separately from registerWhatsapp so that
// tests can inject a fake whatsappResolver (without a real *whatsapp.Manager,
// which requires a vault + WAHA container) and still exercise the real huma
// route registration — the same code tree that runs in production.
func registerWhatsappWithResolver(api huma.API, resolve whatsappResolver) {
	huma.Register(api, huma.Operation{
		OperationID: "listWhatsAppChats",
		Method:      http.MethodGet,
		Path:        "/whatsapp/chats",
		Summary:     "Lista de conversas do WhatsApp com preview e badge de não lidas",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, listChatsHandler(resolve))

	huma.Register(api, huma.Operation{
		OperationID: "getWhatsAppMessages",
		Method:      http.MethodGet,
		Path:        "/whatsapp/chats/{jid}/messages",
		Summary:     "Histórico de mensagens de uma conversa, com backfill automático",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusInternalServerError},
	}, getMessagesHandler(resolve))

	huma.Register(api, huma.Operation{
		OperationID: "sendWhatsAppMessage",
		Method:      http.MethodPost,
		Path:        "/whatsapp/chats/{jid}/messages",
		Summary:     "Envia uma mensagem de texto; idempotente por client_msg_id",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusBadGateway},
	}, sendMessageHandler(resolve))

	huma.Register(api, huma.Operation{
		OperationID: "markWhatsAppChatRead",
		Method:      http.MethodPost,
		Path:        "/whatsapp/chats/{jid}/read",
		Summary:     "Marca uma conversa como lida",
		Tags:        []string{"mobile", "whatsapp"},
		Middlewares: huma.Middlewares{requireAuth},
		Errors:      []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusBadGateway},
	}, markReadHandler(resolve))

	huma.Register(api, huma.Operation{
		OperationID: "getWhatsAppAvatar",
		Method:      http.MethodGet,
		Path:        "/whatsapp/chats/{jid}/avatar",
		Summary:     "Foto de perfil do contato/grupo — proxy same-origin anti-SSRF",
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
			// Never echo err.Error() to the mobile client: it may contain a file
			// path (local store) or an internal detail of the service. The full
			// error goes only to the server log — the same stance as
			// handlers_screens.go/handlers_actions.go.
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
			// Never echo err.Error() to the client — same stance as above.
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
			// Never echo err.Error() to the client — same stance as above.
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
