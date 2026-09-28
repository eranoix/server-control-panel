package whatsapp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

type meowClient struct {
	baseURL string
	user    string
	apiKey  string
	httpc   *http.Client
}

func newMeowClient(baseURL, user, apiKey string) *meowClient {
	return &meowClient{
		baseURL: baseURL, user: user, apiKey: apiKey,
		httpc: &http.Client{Timeout: 130 * time.Second},
	}
}

func (m *meowClient) do(method, path string, body, dest any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, m.baseURL+"/u/"+m.user+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("wad %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error != "" {
			return fmt.Errorf("wad %s: %s", path, e.Error)
		}
		return fmt.Errorf("wad %s: HTTP %d", path, resp.StatusCode)
	}
	if dest != nil && len(raw) > 0 {
		return json.Unmarshal(raw, dest)
	}
	return nil
}

type wadSendReq struct {
	Type     string `json:"type"`
	ChatID   string `json:"chatId"`
	Text     string `json:"text,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Filename string `json:"filename,omitempty"`
	Mimetype string `json:"mimetype,omitempty"`
	DataB64  string `json:"dataB64,omitempty"`
	QuotedID string `json:"quotedId,omitempty"`
}

func (m *meowClient) SendText(chatJID, text, quotedID string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := m.do("POST", "/send", wadSendReq{Type: "text", ChatID: chatJID, Text: text, QuotedID: quotedID}, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (m *meowClient) SendFile(chatJID, msgType, filename, mime, caption, quotedID string, data []byte) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	req := wadSendReq{
		Type: msgType, ChatID: chatJID, Filename: filename, Mimetype: mime,
		Caption: caption, QuotedID: quotedID, DataB64: base64.StdEncoding.EncodeToString(data),
	}
	if err := m.do("POST", "/send", req, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (m *meowClient) GetSession() (*wahaSession, error) {
	var st struct {
		Status   string `json:"status"`
		Phone    string `json:"phone"`
		PushName string `json:"pushName"`
		Engine   string `json:"engine"`
	}
	if err := m.do("GET", "/status", nil, &st); err != nil {
		return nil, err
	}
	s := &wahaSession{Name: "default", Status: Status(st.Status)}
	if st.Phone != "" {
		s.Me.ID = st.Phone + "@c.us"
	}
	s.Me.PushName = st.PushName
	s.Engine.Engine = orStr(st.Engine, "WHATSMEOW")
	return s, nil
}

func (m *meowClient) GetQR() (string, error) {
	var out struct {
		QR string `json:"qr"`
	}
	if err := m.do("GET", "/qr", nil, &out); err != nil {
		return "", err
	}
	if out.QR == "" {
		return "", nil
	}
	if strings.HasPrefix(out.QR, "data:") {
		return out.QR, nil
	}
	png, err := qrcode.Encode(out.QR, qrcode.Medium, 280)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

func (m *meowClient) StartSession() error   { return m.do("POST", "/reload", struct{}{}, nil) }
func (m *meowClient) RestartSession() error { return m.do("POST", "/reload", struct{}{}, nil) }

func (m *meowClient) StopSession() error                               { return nil }
func (m *meowClient) EnsureExtraWebhook(_, _ string, _ []string) error { return nil }

func (m *meowClient) ListChatsOverview() ([]wahaChatOverview, error) { return nil, nil }
func (m *meowClient) GetContact(string) (*wahaContact, error)        { return nil, nil }

func (m *meowClient) PinChat(chatJID string, pin bool) error {
	return m.do("POST", "/action", wadActionReq{Action: "pin", ChatID: chatJID, On: pin}, nil)
}
func (m *meowClient) ArchiveChat(chatJID string, archive bool) error {
	return m.do("POST", "/action", wadActionReq{Action: "archive", ChatID: chatJID, On: archive}, nil)
}
func (m *meowClient) StarMessage(chatJID, msgID string, star bool) error {
	return m.do("POST", "/action", wadActionReq{Action: "star", ChatID: chatJID, MsgID: msgID, On: star}, nil)
}
func (m *meowClient) LogoutSession() error {
	return m.do("POST", "/action", wadActionReq{Action: "logout"}, nil)
}

func (m *meowClient) MarkChatRead(chatJID string) error {
	return m.do("POST", "/action", wadActionReq{Action: "markread", ChatID: chatJID}, nil)
}
func (m *meowClient) ForwardMessage(srcMsgID, dstChatJID string) (string, error) {
	return "", m.do("POST", "/action", wadActionReq{Action: "forward", ChatID: dstChatJID, MsgID: srcMsgID}, nil)
}
func (m *meowClient) ListAllContacts() ([]wahaContact, error) {
	var raw []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		PushName string `json:"pushname"`
	}
	if err := m.do("GET", "/contacts", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]wahaContact, 0, len(raw))
	for _, c := range raw {
		out = append(out, wahaContact{ID: c.ID, Name: c.Name, PushName: c.PushName})
	}
	return out, nil
}
func (m *meowClient) GetChatMessages(string, int) ([]wahaHistoryMsg, error) { return nil, nil }
func (m *meowClient) GetChatMessagesPaged(string, int, int) ([]wahaHistoryMsg, error) {
	return nil, nil
}
func (m *meowClient) GetChatMessagesWithMedia(string, int) ([]wahaHistoryMsg, error) {
	return nil, nil
}

type wadActionReq struct {
	Action string `json:"action"`
	ChatID string `json:"chatId"`
	MsgID  string `json:"msgId,omitempty"`
	Emoji  string `json:"emoji,omitempty"`
	Typing bool   `json:"typing,omitempty"`
	Mode   string `json:"mode,omitempty"`
	On     bool   `json:"on,omitempty"`
	Text   string `json:"text,omitempty"`
	Sender string `json:"sender,omitempty"`
	TS     int64  `json:"ts,omitempty"`
	Count  int    `json:"count,omitempty"`
}

func (m *meowClient) React(chatJID, msgID, emoji, senderJID string) error {
	return m.do("POST", "/action", wadActionReq{Action: "react", ChatID: chatJID, MsgID: msgID, Emoji: emoji, Sender: senderJID}, nil)
}

func (m *meowClient) RequestHistory(chatJID, refMsgID string, fromMe bool, ts int64, count int) error {
	return m.do("POST", "/action", wadActionReq{Action: "histsync", ChatID: chatJID, MsgID: refMsgID, On: fromMe, TS: ts, Count: count}, nil)
}

func (m *meowClient) ResendMessage(chatJID, senderJID, msgID string) error {
	return m.do("POST", "/action", wadActionReq{Action: "resendmsg", ChatID: chatJID, MsgID: msgID, Sender: senderJID}, nil)
}
func (m *meowClient) DeleteMessage(chatJID, msgID, mode string) error {
	return m.do("POST", "/action", wadActionReq{Action: "delete", ChatID: chatJID, MsgID: msgID, Mode: mode}, nil)
}
func (m *meowClient) SendTyping(jid string, typing bool) error {
	return m.do("POST", "/action", wadActionReq{Action: "typing", ChatID: jid, Typing: typing}, nil)
}
func (m *meowClient) SubscribePresence(jid string) error {
	return m.do("POST", "/action", wadActionReq{Action: "subscribe", ChatID: jid}, nil)
}
func (m *meowClient) GetProfilePicture(jid string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := m.do("GET", "/profile-pic?jid="+url.QueryEscape(jid), nil, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

func (m *meowClient) MuteChat(chatJID string, mute bool) error {
	return m.do("POST", "/action", wadActionReq{Action: "mute", ChatID: chatJID, On: mute}, nil)
}

func (m *meowClient) BlockContact(jid string, block bool) error {
	return m.do("POST", "/action", wadActionReq{Action: "block", ChatID: jid, On: block}, nil)
}

func (m *meowClient) EditMessage(chatJID, msgID, text string) error {
	return m.do("POST", "/action", wadActionReq{Action: "edit", ChatID: chatJID, MsgID: msgID, Text: text}, nil)
}

func (m *meowClient) CheckNumber(phone string) (string, bool, error) {
	var out struct {
		JID  string `json:"jid"`
		OnWA bool   `json:"onWA"`
	}
	if err := m.do("GET", "/check?phone="+url.QueryEscape(phone), nil, &out); err != nil {
		return "", false, err
	}
	return out.JID, out.OnWA, nil
}

func (m *meowClient) GroupInfo(jid string) (string, []WAGroupParticipant, error) {
	var out struct {
		Name         string `json:"name"`
		Topic        string `json:"topic"`
		Participants []struct {
			JID     string `json:"jid"`
			Name    string `json:"name"`
			IsAdmin bool   `json:"isAdmin"`
		} `json:"participants"`
	}
	if err := m.do("GET", "/group-info?jid="+url.QueryEscape(jid), nil, &out); err != nil {
		return "", nil, err
	}
	parts := make([]WAGroupParticipant, 0, len(out.Participants))
	for _, p := range out.Participants {
		parts = append(parts, WAGroupParticipant{JID: p.JID, Name: p.Name, IsAdmin: p.IsAdmin})
	}
	return out.Name, parts, nil
}

func (m *meowClient) DownloadFile(fileURL string, dst io.Writer) (int64, string, error) {
	idx := strings.Index(fileURL, "/api/files/")
	if idx < 0 {
		return 0, "", errPhase("download media (unexpected url)")
	}
	msgID := fileURL[idx+len("/api/files/"):]
	req, err := http.NewRequest("GET", m.baseURL+"/u/"+m.user+"/api/files/"+msgID, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	resp, err := m.httpc.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, "", fmt.Errorf("wad media: HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(dst, resp.Body)
	return n, resp.Header.Get("Content-Type"), err
}

func errPhase(what string) error {
	return fmt.Errorf("%s not yet available in the whatsmeow daemon (migrating)", what)
}

func orStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
