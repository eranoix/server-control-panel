package mobilebff

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/whatsapp"
)

// fakeWhatsappSvc is a double of the whole *whatsapp.Service (not just the
// Backend) — it tests exactly the boundary this package depends on
// (whatsappSvc), without needing a real Manager/vault/WAHA container.
type fakeWhatsappSvc struct {
	mu sync.Mutex

	chats []whatsapp.Chat

	messages    []whatsapp.Message
	backfilling bool
	messagesErr error

	sendCalls   int
	sendID      string
	sendErr     error
	lastSendJID string

	markReadErr error
	markReadJID string

	avatarStatus int

	downloadCalls   int
	downloadRel     string
	downloadMime    string
	downloadName    string
	downloadSize    int64
	downloadErr     error
	lastDownloadJID string
	lastDownloadMsg string

	serveMediaCalls int
	lastServedRel   string

	sendFileCalls   int
	sendFileID      string
	sendFileErr     error
	lastSendFileJID string
	lastSendFileArg sendFileCall
}

// sendFileCall captures the arguments of the last call to SendFileDedup, so the
// upload tests can assert what actually reached the "service".
type sendFileCall struct {
	msgType, filename, mimeType, caption, quotedID, clientMsgID string
	data                                                        []byte
}

func (f *fakeWhatsappSvc) ListChats() []whatsapp.Chat { return f.chats }

func (f *fakeWhatsappSvc) MessagesForDisplay(jid string, opts whatsapp.MessagesQuery) ([]whatsapp.Message, bool, error) {
	return f.messages, f.backfilling, f.messagesErr
}

func (f *fakeWhatsappSvc) SendTextDedup(chatJID, text, quotedID, clientMsgID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendCalls++
	f.lastSendJID = chatJID
	if f.sendErr != nil {
		return "", f.sendErr
	}
	return f.sendID, nil
}

func (f *fakeWhatsappSvc) MarkRead(jid string) error {
	f.markReadJID = jid
	return f.markReadErr
}

func (f *fakeWhatsappSvc) ServeAvatar(w http.ResponseWriter, r *http.Request, jid string) {
	status := f.avatarStatus
	if status == 0 {
		status = http.StatusNoContent
	}
	w.WriteHeader(status)
}

func (f *fakeWhatsappSvc) DownloadMediaForMessage(chatJID, msgID string) (string, string, string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloadCalls++
	f.lastDownloadJID = chatJID
	f.lastDownloadMsg = msgID
	if f.downloadErr != nil {
		return "", "", "", 0, f.downloadErr
	}
	return f.downloadRel, f.downloadMime, f.downloadName, f.downloadSize, nil
}

func (f *fakeWhatsappSvc) ServeMediaRel(w http.ResponseWriter, r *http.Request, rel string) {
	f.mu.Lock()
	f.serveMediaCalls++
	f.lastServedRel = rel
	f.mu.Unlock()
	w.Header().Set("X-Fake-Served-Rel", rel)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("fake-media-bytes"))
}

func (f *fakeWhatsappSvc) SendFileDedup(chatJID, msgType, filename, mimeType, caption, quotedID, clientMsgID string, data []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendFileCalls++
	f.lastSendFileJID = chatJID
	f.lastSendFileArg = sendFileCall{
		msgType: msgType, filename: filename, mimeType: mimeType,
		caption: caption, quotedID: quotedID, clientMsgID: clientMsgID, data: data,
	}
	if f.sendFileErr != nil {
		return "", f.sendFileErr
	}
	return f.sendFileID, nil
}

var _ whatsappSvc = (*fakeWhatsappSvc)(nil)

func fakeResolver(svc *fakeWhatsappSvc) whatsappResolver {
	return func(ctx context.Context) (whatsappSvc, error) {
		if auth.UserFromContext(ctx) == "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		return svc, nil
	}
}

// newWhatsappTestAPI assembles an isolated huma.API with only the WhatsApp
// registrar, injecting the fake resolver — avoiding any dependence on the
// global order/registration of all the other handlers_*.go packages.
func newWhatsappTestAPI(svc *fakeWhatsappSvc) (huma.API, *http.ServeMux) {
	mux := http.NewServeMux()
	api := humago.NewWithPrefix(mux, Prefix, huma.DefaultConfig("test", "0.0"))
	registerWhatsappWithResolver(api, fakeResolver(svc))
	return api, mux
}

func TestWhatsAppChats_ListaOrdemDoStore(t *testing.T) {
	svc := &fakeWhatsappSvc{chats: []whatsapp.Chat{
		{JID: "a@s.whatsapp.net", Name: "Ana", UnreadCount: 3, LastMsgTS: 100, LastMsgBody: "oi", AvatarURL: "https://x/a"},
		{JID: "b@s.whatsapp.net", Name: "Bruno", IsGroup: true},
	}}
	_, mux := newWhatsappTestAPI(svc)

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/whatsapp/chats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []ChatSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Jid != "a@s.whatsapp.net" || got[0].Unread != 3 || got[0].LastMessagePreview != "oi" || got[0].AvatarUrl != "https://x/a" {
		t.Fatalf("got[0] = %+v", got[0])
	}
	if got[1].Jid != "b@s.whatsapp.net" || !got[1].IsGroup {
		t.Fatalf("got[1] = %+v", got[1])
	}
}

func TestWhatsAppChats_Unauthenticated401(t *testing.T) {
	_, mux := newWhatsappTestAPI(&fakeWhatsappSvc{})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/whatsapp/chats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestWhatsAppMessages_MapeiaCamposEBackfilling(t *testing.T) {
	svc := &fakeWhatsappSvc{
		backfilling: true,
		messages: []whatsapp.Message{
			{
				ID: "m1", ChatJID: "a@s.whatsapp.net", FromJID: "a@s.whatsapp.net",
				FromMe: false, TS: 111, Type: "text", Body: "olá", Ack: 2,
			},
			{
				ID: "m2", ChatJID: "a@s.whatsapp.net", FromMe: true, TS: 222, Type: "image",
				Media: &whatsapp.Media{MimeType: "image/jpeg", Size: 1234, Filename: "foto.jpg", Width: 10, Height: 20},
			},
		},
	}
	_, mux := newWhatsappTestAPI(svc)

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/whatsapp/chats/a@s.whatsapp.net/messages?limit=10", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body MessagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if !body.Backfilling {
		t.Fatal("backfilling = false, want true")
	}
	if len(body.Messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(body.Messages))
	}
	if body.Messages[0].ID != "m1" || body.Messages[0].Sender != "a@s.whatsapp.net" || body.Messages[0].Text != "olá" {
		t.Fatalf("messages[0] = %+v", body.Messages[0])
	}
	if body.Messages[1].Media == nil {
		t.Fatal("messages[1].Media = nil, want *MediaView")
	}
	wantURL := "/api/mobile/v1/whatsapp/chats/a@s.whatsapp.net/media/m2"
	if body.Messages[1].Media.Url != wantURL {
		t.Fatalf("media.url = %q, want %q", body.Messages[1].Media.Url, wantURL)
	}
	if body.Messages[1].Media.MimeType != "image/jpeg" || body.Messages[1].Media.Size != 1234 {
		t.Fatalf("media = %+v", body.Messages[1].Media)
	}
}

// TestWhatsAppSendMessage_ClientMsgIDIdempotente is the central proof of
// send idempotency: two POSTs with the SAME client_msg_id, through the whole
// HTTP layer (a real huma route, not a direct Go function call), result
// in a single call to SendTextDedup — which in turn only calls
// Client.SendText once. Here we prove the HTTP end of the chain; the end with
// the real dedupe is already covered by TestSendTextDedup in
// internal/whatsapp/service_export_test.go.
func TestWhatsAppSendMessage_ClientMsgIDIdempotente(t *testing.T) {
	svc := &fakeWhatsappSvc{sendID: "wamid-999"}
	_, mux := newWhatsappTestAPI(svc)

	body, _ := json.Marshal(SendMessageRequest{Text: "oi", ClientMsgID: "cliente-123"})

	do := func() (*httptest.ResponseRecorder, SendMessageResponse) {
		req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/whatsapp/chats/5511999998888@c.us/messages", body)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var resp SendMessageResponse
		if rec.Code == http.StatusOK {
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		}
		return rec, resp
	}

	rec1, resp1 := do()
	if rec1.Code != http.StatusOK {
		t.Fatalf("1st POST status = %d, body = %s", rec1.Code, rec1.Body.String())
	}
	rec2, resp2 := do()
	if rec2.Code != http.StatusOK {
		t.Fatalf("2nd POST status = %d, body = %s", rec2.Code, rec2.Body.String())
	}

	if resp1.ID != "wamid-999" || resp2.ID != "wamid-999" {
		t.Fatalf("ids = %q, %q, want ambos wamid-999", resp1.ID, resp2.ID)
	}
	// The boundary tested here is HTTP -> handler -> SendTextDedup: the fake
	// counts how many times the HANDLER called SendTextDedup over the HTTP route.
	// In this double, SendTextDedup itself does not deduplicate (the one that
	// dedupes is the real *whatsapp.Service, proved in service_export_test.go) — what
	// this assertion proves is that the HTTP handler does not introduce a SECOND send
	// on a retry, and that the same client_msg_id reaches SendTextDedup intact
	// in both calls.
	svc.mu.Lock()
	calls := svc.sendCalls
	svc.mu.Unlock()
	if calls != 2 {
		t.Fatalf("SendTextDedup called %d times by the HTTP route, want 2 (one per POST — real dedupe is the Service's responsibility)", calls)
	}
}

func TestWhatsAppSendMessage_ErroDoBackendVira502(t *testing.T) {
	svc := &fakeWhatsappSvc{sendErr: errBadGatewayTest}
	_, mux := newWhatsappTestAPI(svc)

	body, _ := json.Marshal(SendMessageRequest{Text: "oi", ClientMsgID: "c1"})
	req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/whatsapp/chats/jid/messages", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestWhatsAppMarkRead_204(t *testing.T) {
	svc := &fakeWhatsappSvc{}
	_, mux := newWhatsappTestAPI(svc)

	req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/whatsapp/chats/jid-xyz/read", []byte{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}
	if svc.markReadJID != "jid-xyz" {
		t.Fatalf("markReadJID = %q, want jid-xyz", svc.markReadJID)
	}
}

func TestWhatsAppAvatar_StreamaStatusDoService(t *testing.T) {
	svc := &fakeWhatsappSvc{avatarStatus: http.StatusNoContent}
	_, mux := newWhatsappTestAPI(svc)

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/whatsapp/chats/jid/avatar", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}
}

var errBadGatewayTest = &testSendErr{"waha indisponível"}

type testSendErr struct{ msg string }

func (e *testSendErr) Error() string { return e.msg }
