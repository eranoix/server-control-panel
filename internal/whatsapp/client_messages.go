package whatsapp

// client_messages.go — WAHA REST message operations
//
// Send: SendText (text), SendFile (multipart upload), normalizeOutboundJID,
// parseFlexibleID (the legacy id).
// Reactions/replies/forward/delete/star: React, StarMessage, DeleteMessage,
// ForwardMessage.
// History: GetChatMessages(Paged|WithMedia), getChatMessages.
// Media: DownloadFile, DownloadMedia, GetProfilePicture.
// Misc: SubscribePresence, SendTyping, base64Encode.
//
// Extracted from client.go (it keeps the same *Client receiver).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// errWAHAUnsupported: the new operations only exist in the whatsmeow
// backend (daemon). WAHA is rollback-only — it degrades with a clear error.
var errWAHAUnsupported = errors.New("not supported on the WAHA backend")

// --- Sending ---

// SendText sends a plain text message. Returns the WAHA-assigned message ID.
//
// WAHA's GOWS engine returns `id` as either a plain string ("true_<chat>_<id>")
// or as a legacy object ({_serialized: ...}). We accept both by decoding into
// json.RawMessage and trying both shapes.
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

// normalizeOutboundJID prepares a JID for sending to WAHA. The WAHA client
// accepts both "@c.us" and "@s.whatsapp.net" — we convert to
// "@s.whatsapp.net" (the canonical form the GOWS engine uses internally) to
// reduce routing surprises on the server.
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

// parseFlexibleID extracts the message ID from WAHA's response, supporting
// both the new string format and the legacy {_serialized: ...} object.
func parseFlexibleID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first (most common in modern WAHA).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Fallback: legacy object form.
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

// SendFile uploads an attachment and sends it to the chat. data is the raw
// file bytes; filename/mime are used for WhatsApp metadata. caption is shown
// below the media. msgType is "image"/"document"/"voice"/"video". quotedID, if
// set, makes the media a reply to that message.
//
// WAHA/GOWS media endpoints are JSON-only: the file goes in `file:{mimetype,
// filename, data}` where `data` is RAW base64 (no data: URI prefix), with
// `caption`/`reply_to` at the top level. `convert:true` makes WAHA transcode
// voice/video into the ogg/opus the WhatsApp protocol requires. Multipart is
// not accepted, so this reuses the shared do() JSON transport (same path as
// SendText), which sets Content-Type: application/json + X-Api-Key.
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

// --- Reactions, replies, forwarding, deletion, stars ---

// React adds (emoji != "") or removes (emoji == "") a reaction on a message.
// Empty string is the official "remove reaction" payload per WAHA docs.
// RequestHistory is a no-op on WAHA (on-demand history sync belongs to the
// whatsmeow daemon alone).
func (c *Client) RequestHistory(_, _ string, _ bool, _ int64, _ int) error { return nil }

// ResendMessage is a no-op on WAHA (on-demand resend belongs to the whatsmeow daemon alone).
func (c *Client) ResendMessage(_, _, _ string) error { return nil }

func (c *Client) React(chatJID, msgID, emoji, _ string) error {
	// senderJID is ignored on WAHA (the gateway resolves the author
	// internally); only the whatsmeow backend needs the sender spelled out.
	body := map[string]any{
		"messageId": msgID,
		"reaction":  emoji,
		"session":   c.SessionID,
	}
	return c.do("PUT", "/api/reaction", body, nil)
}

// StarMessage flags/unflags a message as starred (favorited). WAHA exposes
// it under /api/star with `star: bool`.
func (c *Client) StarMessage(chatJID, msgID string, star bool) error {
	body := map[string]any{
		"messageId": msgID,
		"star":      star,
		"session":   c.SessionID,
	}
	return c.do("PUT", "/api/star", body, nil)
}

// DeleteMessage removes a message. mode=="me" deletes only locally (for me),
// mode=="everyone" recalls it for all participants (WhatsApp's 1h limit
// applies — sender-side only).
func (c *Client) DeleteMessage(chatJID, msgID, mode string) error {
	body := map[string]any{
		"chatId":    chatJID,
		"messageId": msgID,
		"session":   c.SessionID,
	}
	// WAHA: DELETE /api/{session}/chats/{chatId}/messages/{messageId}
	// Both modes use same endpoint; "everyone" is the default sender behavior.
	path := "/api/" + c.SessionID + "/chats/" + chatJID + "/messages/" + msgID
	return c.do("DELETE", path, body, nil)
}

// ForwardMessage forwards an existing message to a different chat.
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

// GetChatMessages fetches the latest `limit` messages of a chat from WAHA.
// Used by the deep import — this does not arrive over the webhook, it is an
// explicit pull. WAHA returns messages newest-first; we keep that order.
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
	MimeType string `json:"mimetype,omitempty"`
	Filename string `json:"filename,omitempty"`
	Ack      int    `json:"ack"`
	// Replies: WAHA returns `quotedMsgId` in the top-level payload. Without
	// capturing it, 100% of the backfill's replies lost their reference (no
	// quoted_id in the store at all, even with obvious replies in the
	// conversation).
	QuotedID string `json:"quotedMsgId,omitempty"`
	// RawJSON is filled in by the client after unmarshalling — the backfill
	// writes it into Message.RawJSON for parity with the webhook (so it can be
	// reprocessed later). It does not come from WAHA's JSON; it is an
	// internal field.
	RawJSON json.RawMessage `json:"-"`
	// GOWS engine: the top-level `type` comes back empty even for
	// image/video/audio. The real type is in `_data.Info.MediaType`
	// (image/video/audio/document).
	Data *struct {
		Info *struct {
			Type      string `json:"Type"`
			MediaType string `json:"MediaType"`
		} `json:"Info,omitempty"`
	} `json:"_data,omitempty"`
}

func (c *Client) GetChatMessages(jid string, limit int) ([]wahaHistoryMsg, error) {
	return c.getChatMessages(jid, limit, 0, false)
}

// GetChatMessagesPaged is the version with an offset — WAHA paginates the
// history once `limit` is reached. The deep sync (deep_sync) calls it in a loop.
func (c *Client) GetChatMessagesPaged(jid string, limit, offset int) ([]wahaHistoryMsg, error) {
	return c.getChatMessages(jid, limit, offset, false)
}

// GetChatMessagesWithMedia is GetChatMessages but asks WAHA to download the
// messages' media too (downloadMedia=true). WAHA saves the files under
// /tmp/whatsapp-files/<session>/ inside the container and returns the URL as
// http://localhost:3000/api/files/<session>/<shortId>.<ext>. Used by the
// on-demand download endpoint to force the download before copying into the
// local MediaRoot.
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
	// Decode as an array of raws first, to preserve the original JSON of each
	// message (RawJSON is used to reprocess types when a bug turns up later —
	// parity with what the webhook already does).
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

// DownloadFile fetches a file from WAHA's /api/files/... URL and writes it to
// dst as a stream. Used by the Service to copy media WAHA has downloaded
// (/tmp/whatsapp-files/... inside the container) into our MediaRoot.
// fileURL must be a complete URL (the one WAHA returns in media.url).
func (c *Client) DownloadFile(fileURL string, dst io.Writer) (int64, string, error) {
	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return 0, "", err
	}
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}
	// Reuses the transport but with a longer timeout — downloads can run to MB.
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

// GetProfilePicture returns the URL of the contact's profile picture, or
// "" if there isn't one set (the contact has no avatar, or privacy hides it).
// WAHA returns `{ url: "..." }` for /api/{session}/contacts/profile-picture.
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

// SubscribePresence asks WAHA to start delivering presence events (online,
// offline, composing, recording, last-seen) for the given JID via the
// `presence.update` webhook. Subscription is best-effort and per-chat.
func (c *Client) SubscribePresence(jid string) error {
	body := map[string]any{
		"chatId":  jid,
		"session": c.SessionID,
	}
	return c.do("POST", "/api/"+c.SessionID+"/presence/"+jid+"/subscribe", body, nil)
}

// SendTyping reports OUR presence to a chat: "composing" or "paused".
// Used so the contact's WhatsApp shows "Jordan is typing…" while we
// compose a message.
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

// --- Chat operations: pin, archive, mute ---

// PinChat marks a chat as pinned (sticks to top of the list). Persists on the
// WhatsApp side, mirrored to all linked devices.
func (c *Client) PinChat(chatJID string, pin bool) error {
	endpoint := "/api/" + c.SessionID + "/chats/" + chatJID + "/pin"
	if !pin {
		endpoint = "/api/" + c.SessionID + "/chats/" + chatJID + "/unpin"
	}
	return c.do("POST", endpoint, nil, nil)
}

// ArchiveChat moves the chat to the Archived list (hides from main view).
func (c *Client) ArchiveChat(chatJID string, archive bool) error {
	endpoint := "/api/" + c.SessionID + "/chats/" + chatJID + "/archive"
	if !archive {
		endpoint = "/api/" + c.SessionID + "/chats/" + chatJID + "/unarchive"
	}
	return c.do("POST", endpoint, nil, nil)
}

// --- Operations supported only on the whatsmeow (daemon) backend ---
// WAHA is rollback-only; these degrade to a clear error instead of calling
// endpoints WAHA does not expose in a stable way.

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

// DownloadMedia fetches an attachment by URL (from a webhook payload). WAHA
// rewrites media references to internal URLs; we proxy bytes through.
func (c *Client) DownloadMedia(mediaURL string) ([]byte, string, error) {
	// mediaURL may be relative ("/api/files/...") or absolute. WAHA returns it
	// in the webhook with the same host:port we use.
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20)) // 100 MiB cap
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// base64Encode without depending on encoding/base64 import in this file (we
// keep client.go free of stdlib bloat). Wrapper kept short.
func base64Encode(b []byte) string {
	return stdEncode(b)
}
