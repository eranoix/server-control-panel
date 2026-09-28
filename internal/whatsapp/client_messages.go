package whatsapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var errWAHAUnsupported = errors.New("not supported on the WAHA backend")

func (c *Client) SendText(chatJID, text, quotedID string) (string, error) {
	chatJID = normalizeOutboundJID(chatJID)
	body := map[string]any{
		"chatId":  chatJID,
		"text":    text,
		"session": c.SessionID,
	}
	if quotedID != "" {
		body["reply_to"] = quotedID
	}
	var out struct {
		ID json.RawMessage `json:"id"`
	}
	if err := c.do("POST", "/api/sendText", body, &out); err != nil {
		return "", err
	}
	return parseFlexibleID(out.ID), nil
}

func normalizeOutboundJID(jid string) string {
	if jid == "" {
		return jid
	}
	if strings.HasSuffix(jid, "@c.us") {
		user := strings.TrimSuffix(jid, "@c.us")
		return user + "@s.whatsapp.net"
	}
	return jid
}

func parseFlexibleID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		Serialized string `json:"_serialized"`
		ID         string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Serialized != "" {
			return obj.Serialized
		}
		return obj.ID
	}
	return ""
}

func (c *Client) SendFile(chatJID, msgType, filename, mime, caption, quotedID string, data []byte) (string, error) {
	chatJID = normalizeOutboundJID(chatJID)
	endpoint := map[string]string{
		"image":    "/api/sendImage",
		"document": "/api/sendFile",
		"voice":    "/api/sendVoice",
		"video":    "/api/sendVideo",
	}[msgType]
	if endpoint == "" {
		endpoint = "/api/sendImage"
	}
	if filename == "" {
		filename = "file"
	}
	if mime == "" {
		mime = "application/octet-stream"
	}

	file := map[string]any{
		"mimetype": mime,
		"filename": filename,
		"data":     base64Encode(data),
	}
	body := map[string]any{
		"session": c.SessionID,
		"chatId":  chatJID,
		"file":    file,
	}
	if caption != "" {
		body["caption"] = caption
	}
	if quotedID != "" {
		body["reply_to"] = quotedID
	}
	if msgType == "voice" || msgType == "video" {
		body["convert"] = true
	}

	var out struct {
		ID json.RawMessage `json:"id"`
	}
	if err := c.withTimeout(uploadTimeout).do("POST", endpoint, body, &out); err != nil {
		return "", err
	}
	return parseFlexibleID(out.ID), nil
}

func (c *Client) RequestHistory(_, _ string, _ bool, _ int64, _ int) error { return nil }

func (c *Client) ResendMessage(_, _, _ string) error { return nil }

func (c *Client) React(chatJID, msgID, emoji, _ string) error {
	body := map[string]any{
		"messageId": msgID,
		"reaction":  emoji,
		"session":   c.SessionID,
	}
	return c.do("PUT", "/api/reaction", body, nil)
}

func (c *Client) StarMessage(chatJID, msgID string, star bool) error {
	body := map[string]any{
		"messageId": msgID,
		"star":      star,
		"session":   c.SessionID,
	}
	return c.do("PUT", "/api/star", body, nil)
}

func (c *Client) DeleteMessage(chatJID, msgID, mode string) error {
	body := map[string]any{
		"chatId":    chatJID,
		"messageId": msgID,
		"session":   c.SessionID,
	}
	path := "/api/" + c.SessionID + "/chats/" + chatJID + "/messages/" + msgID
	return c.do("DELETE", path, body, nil)
}

func (c *Client) ForwardMessage(srcMsgID, dstChatJID string) (string, error) {
	body := map[string]any{
		"chatId":    dstChatJID,
		"messageId": srcMsgID,
		"session":   c.SessionID,
	}
	var out struct {
		ID json.RawMessage `json:"id"`
	}
	if err := c.do("POST", "/api/forwardMessage", body, &out); err != nil {
		return "", err
	}
	return parseFlexibleID(out.ID), nil
}

type wahaHistoryMsg struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	FromMe    bool   `json:"fromMe"`
	Body      string `json:"body"`
	Caption   string `json:"caption"`
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	HasMedia  bool   `json:"hasMedia"`
	MediaURL  string `json:"mediaUrl,omitempty"`
	Media     *struct {
		URL      string `json:"url,omitempty"`
		MimeType string `json:"mimetype,omitempty"`
		Filename string `json:"filename,omitempty"`
	} `json:"media,omitempty"`
	MimeType string          `json:"mimetype,omitempty"`
	Filename string          `json:"filename,omitempty"`
	Ack      int             `json:"ack"`
	QuotedID string          `json:"quotedMsgId,omitempty"`
	RawJSON  json.RawMessage `json:"-"`
	Data     *struct {
		Info *struct {
			Type      string `json:"Type"`
			MediaType string `json:"MediaType"`
		} `json:"Info,omitempty"`
	} `json:"_data,omitempty"`
}

func (c *Client) GetChatMessages(jid string, limit int) ([]wahaHistoryMsg, error) {
	return c.getChatMessages(jid, limit, 0, false)
}

func (c *Client) GetChatMessagesPaged(jid string, limit, offset int) ([]wahaHistoryMsg, error) {
	return c.getChatMessages(jid, limit, offset, false)
}

func (c *Client) GetChatMessagesWithMedia(jid string, limit int) ([]wahaHistoryMsg, error) {
	return c.getChatMessages(jid, limit, 0, true)
}

func (c *Client) getChatMessages(jid string, limit, offset int, downloadMedia bool) ([]wahaHistoryMsg, error) {
	if limit <= 0 {
		limit = 100
	}
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}
	if downloadMedia {
		q.Set("downloadMedia", "true")
	} else {
		q.Set("downloadMedia", "false")
	}
	path := "/api/" + c.SessionID + "/chats/" + url.PathEscape(jid) + "/messages?" + q.Encode()
	var rawList []json.RawMessage
	if err := c.do("GET", path, nil, &rawList); err != nil {
		return nil, err
	}
	out := make([]wahaHistoryMsg, 0, len(rawList))
	for _, raw := range rawList {
		var m wahaHistoryMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		m.RawJSON = raw
		out = append(out, m)
	}
	return out, nil
}

func (c *Client) DownloadFile(fileURL string, dst io.Writer) (int64, string, error) {
	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return 0, "", err
	}
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}
	client := &http.Client{Timeout: uploadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("waha file %s: status %d", fileURL, resp.StatusCode)
	}
	n, err := io.Copy(dst, resp.Body)
	if err != nil {
		return n, "", err
	}
	return n, resp.Header.Get("Content-Type"), nil
}

func (c *Client) GetProfilePicture(jid string) (string, error) {
	q := url.Values{}
	q.Set("contactId", jid)
	q.Set("session", c.SessionID)
	q.Set("refresh", "false")
	var out struct {
		URL string `json:"url"`
	}
	if err := c.do("GET", "/api/contacts/profile-picture?"+q.Encode(), nil, &out); err != nil {
		return "", err
	}
	return out.URL, nil
}

func (c *Client) SubscribePresence(jid string) error {
	body := map[string]any{
		"chatId":  jid,
		"session": c.SessionID,
	}
	return c.do("POST", "/api/"+c.SessionID+"/presence/"+jid+"/subscribe", body, nil)
}

func (c *Client) SendTyping(jid string, typing bool) error {
	state := "paused"
	if typing {
		state = "composing"
	}
	body := map[string]any{
		"chatId":   jid,
		"presence": state,
		"session":  c.SessionID,
	}
	return c.do("POST", "/api/"+c.SessionID+"/presence", body, nil)
}

func (c *Client) PinChat(chatJID string, pin bool) error {
	endpoint := "/api/" + c.SessionID + "/chats/" + chatJID + "/pin"
	if !pin {
		endpoint = "/api/" + c.SessionID + "/chats/" + chatJID + "/unpin"
	}
	return c.do("POST", endpoint, nil, nil)
}

func (c *Client) ArchiveChat(chatJID string, archive bool) error {
	endpoint := "/api/" + c.SessionID + "/chats/" + chatJID + "/archive"
	if !archive {
		endpoint = "/api/" + c.SessionID + "/chats/" + chatJID + "/unarchive"
	}
	return c.do("POST", endpoint, nil, nil)
}

func (c *Client) MuteChat(string, bool) error     { return errWAHAUnsupported }
func (c *Client) BlockContact(string, bool) error { return errWAHAUnsupported }
func (c *Client) EditMessage(string, string, string) error {
	return errWAHAUnsupported
}
func (c *Client) CheckNumber(string) (string, bool, error) {
	return "", false, errWAHAUnsupported
}
func (c *Client) GroupInfo(string) (string, []WAGroupParticipant, error) {
	return "", nil, errWAHAUnsupported
}

func (c *Client) DownloadMedia(mediaURL string) ([]byte, string, error) {
	if strings.HasPrefix(mediaURL, "/") {
		mediaURL = c.BaseURL + mediaURL
	}
	req, err := http.NewRequest("GET", mediaURL, nil)
	if err != nil {
		return nil, "", err
	}
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}
	uploadClient := c.withTimeout(uploadTimeout)
	resp, err := uploadClient.httpc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("download media %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func base64Encode(b []byte) string {
	return stdEncode(b)
}
