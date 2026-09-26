package whatsapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a thin HTTP wrapper over WAHA's REST API. WAHA binds to
// 127.0.0.1:3000 by default; all calls authenticate via the X-Api-Key header.
//
// We intentionally do NOT use a request-level circuit breaker — the engine is
// local, latency is dominated by the WhatsApp upstream (which WAHA already
// retries internally), and the state poller already detects extended outages
// and surfaces them via the UI.
type Client struct {
	BaseURL   string
	APIKey    string
	SessionID string // "default" for WAHA Core (single session)
	httpc     *http.Client
}

// NewClient builds a Client with the standard short timeout. Use NewUploadClient
// for media uploads where the upload itself may dominate latency.
func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:3000"
	}
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		APIKey:    apiKey,
		SessionID: "default",
		httpc:     &http.Client{Timeout: httpTimeout},
	}
}

// withTimeout returns a clone with a custom timeout (used for large uploads).
func (c *Client) withTimeout(d time.Duration) *Client {
	clone := *c
	clone.httpc = &http.Client{Timeout: d}
	return &clone
}

// do executes a request, handling JSON marshal/unmarshal + WAHA's error envelope.
// dest may be nil if the caller does not care about the response body.
func (c *Client) do(method, path string, body, dest any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, reqBody)
	if err != nil {
		return err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("waha %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 400 {
		// WAHA returns {"error": "...", "message": "..."} on errors.
		var werr struct {
			Error   string `json:"error"`
			Message string `json:"message"`
			Detail  string `json:"detail"`
		}
		_ = json.Unmarshal(respBytes, &werr)
		msg := werr.Message
		if msg == "" {
			msg = werr.Error
		}
		if msg == "" {
			msg = strings.TrimSpace(string(respBytes))
			if len(msg) > 200 {
				msg = msg[:200] + "..."
			}
		}
		return fmt.Errorf("waha %s %s: %d %s", method, path, resp.StatusCode, msg)
	}
	if dest != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, dest); err != nil {
			return fmt.Errorf("waha decode %s: %w", path, err)
		}
	}
	return nil
}

// --- Session lifecycle ---

type wahaSession struct {
	Name   string         `json:"name"`
	Status Status         `json:"status"`
	Config map[string]any `json:"config,omitempty"`
	Me     struct {
		ID       string `json:"id"`
		PushName string `json:"pushName"`
	} `json:"me"`
	Engine struct {
		Engine string `json:"engine"`
	} `json:"engine"`
}

// GetSession returns the current WAHA session info. ErrSessionNotFound is
// returned (wrapped) when the session has never been created.
func (c *Client) GetSession() (*wahaSession, error) {
	var s wahaSession
	err := c.do("GET", "/api/sessions/"+c.SessionID, nil, &s)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	return &s, nil
}

var ErrSessionNotFound = errors.New("waha session not found")

// StartSession ensures the default session is created and running. Handles
// the four cases WAHA exposes:
//
//  1. Session doesn't exist:           POST /api/sessions/ (creates + auto-starts)
//  2. Session exists, stopped:         POST /api/sessions/{name}/start
//  3. Session exists, FAILED:          POST /api/sessions/{name}/restart  (the
//     /start endpoint returns 422 "already started" — FAILED ≠ stopped from
//     WAHA's POV, it's "started but unhealthy")
//  4. Session running (WORKING/SCAN_QR_CODE/STARTING): no-op
//
// Idempotent — safe to call repeatedly. 422 "already started" responses are
// swallowed because the goal state (running) is already met.
func (c *Client) StartSession() error {
	sess, err := c.GetSession()
	if err != nil && err != ErrSessionNotFound {
		return err
	}
	if err == ErrSessionNotFound {
		body := map[string]any{
			"name":  c.SessionID,
			"start": true,
			"config": map[string]any{
				"webhooks": []map[string]any{}, // global env-var hook handles all sessions
			},
		}
		return c.do("POST", "/api/sessions/", body, nil)
	}
	switch sess.Status {
	case StatusWorking, StatusScanQR, StatusStarting:
		return nil // already in motion
	case StatusFailed:
		return c.RestartSession() // force reset
	default:
		// STOPPED or anything else: plain start.
		err := c.do("POST", "/api/sessions/"+c.SessionID+"/start", nil, nil)
		if err == nil {
			return nil
		}
		// Idempotency: WAHA returns 422 when the session is already started,
		// which means the goal is met — squelch.
		if strings.Contains(err.Error(), "already started") {
			return nil
		}
		return err
	}
}

// EnsureExtraWebhook makes sure the session has an additional webhook
// registered with URL=extraURL. Idempotent: if the URL is already in the
// config.webhooks list it returns without doing anything. Otherwise it POSTs
// to /api/sessions/ (update mode) with webhooks holding the new entry PLUS
// the existing ones.
//
// It does not remove the global env var (WHATSAPP_HOOK_URL in the container)
// — that keeps firing independently. The result: WAHA fires at BOTH
// destinations (env + per-session) without deduplicating — exactly what a
// v1 + v2 sharing the same container needs.
//
// An empty events list subscribes to the same events as the env var
// (session.status, message, message.any, message.ack, message.revoked).
// An empty hmacKey means no HMAC on the extra webhook (not recommended in
// production).
func (c *Client) EnsureExtraWebhook(extraURL, hmacKey string, events []string) error {
	if extraURL == "" {
		return nil
	}
	if len(events) == 0 {
		events = []string{"session.status", "message", "message.any", "message.ack", "message.revoked"}
	}
	// Read the current config to preserve existing webhooks and check idempotency.
	var current sessionWithConfig
	if err := c.do("GET", "/api/sessions/"+c.SessionID, nil, &current); err != nil {
		return fmt.Errorf("get session config: %w", err)
	}
	// Idempotency: URL already there? Skip.
	for _, w := range current.Config.Webhooks {
		if w.URL == extraURL {
			return nil
		}
	}
	// Append the new webhook entry. The existing ones are kept so we do not
	// regress configuration made by hand through the API.
	newWebhook := webhookEntry{
		URL:    extraURL,
		Events: events,
	}
	if hmacKey != "" {
		newWebhook.HMAC = &webhookHMAC{Key: hmacKey}
	}
	newWebhooks := append([]webhookEntry{}, current.Config.Webhooks...)
	newWebhooks = append(newWebhooks, newWebhook)
	// PUT /api/sessions/{name} = update an existing session. WAHA rejects a
	// POST (422 "already exists, use PUT to update"). It preserves the session
	// state (WORKING and so on) and does not drop the connection.
	body := map[string]any{
		"config": map[string]any{
			"webhooks": newWebhooks,
		},
	}
	return c.do("PUT", "/api/sessions/"+c.SessionID, body, nil)
}

// Helper structs for reading and serialising WAHA's config.webhooks.
type sessionWithConfig struct {
	Name   string        `json:"name"`
	Status string        `json:"status"`
	Config sessionConfig `json:"config"`
}
type sessionConfig struct {
	Webhooks []webhookEntry `json:"webhooks"`
}
type webhookEntry struct {
	URL    string       `json:"url"`
	Events []string     `json:"events"`
	HMAC   *webhookHMAC `json:"hmac,omitempty"`
}
type webhookHMAC struct {
	Key string `json:"key"`
}

// RestartSession forces a hard restart of the session (useful when stuck in
// FAILED or to recover from protocol-side desync). Preserves the linked
// device — does not re-pair.
func (c *Client) RestartSession() error {
	return c.do("POST", "/api/sessions/"+c.SessionID+"/restart", nil, nil)
}

// StopSession halts the session but keeps the linked-device pairing (so
// restart resumes without QR). Use LogoutSession to fully unpair.
func (c *Client) StopSession() error {
	return c.do("POST", "/api/sessions/"+c.SessionID+"/stop", nil, nil)
}

// LogoutSession unlinks the device on WhatsApp's side. After this, a new
// QR is required to re-pair.
func (c *Client) LogoutSession() error {
	return c.do("POST", "/api/sessions/"+c.SessionID+"/logout", nil, nil)
}

// GetQR fetches the current QR as a base64 data: URL. Returns ("", nil) when
// the session isn't in SCAN_QR_CODE state.
func (c *Client) GetQR() (string, error) {
	// WAHA exposes the QR as raw image bytes at /api/<session>/auth/qr.
	req, err := http.NewRequest("GET", c.BaseURL+"/api/"+c.SessionID+"/auth/qr?format=image", nil)
	if err != nil {
		return "", err
	}
	if c.APIKey != "" {
		req.Header.Set("X-Api-Key", c.APIKey)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 422 {
		return "", nil // no QR available right now
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("qr: %d", resp.StatusCode)
	}
	imgBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/png"
	}
	return "data:" + mimeType + ";base64," + base64Encode(imgBytes), nil
}

// --- Chats ---

// wahaChatOverview matches WAHA's /api/{session}/chats/overview response:
// the public chat list with names already resolved from the WhatsApp address
// book (when the user has saved the contact). Falls back to push-name for
// unknown contacts. This is what we want for displaying the chat list.
type wahaChatOverview struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Picture     string `json:"picture,omitempty"` // URL do CDN do WhatsApp; pode ficar vazio
	IsGroup     bool   `json:"isGroup"`
	UnreadCount int    `json:"unreadCount"`
	Archived    bool   `json:"archived"`
	Pinned      bool   `json:"pinned"`
	Muted       bool   `json:"muted"`
}

// ListChatsOverview returns the chat list with display names suitable for UI.
// Endpoint: GET /api/{session}/chats/overview (faster, lighter than /chats).
// Falls back to the heavier /chats endpoint if /overview is missing on
// older WAHA versions.
func (c *Client) ListChatsOverview() ([]wahaChatOverview, error) {
	var out []wahaChatOverview
	err := c.do("GET", "/api/"+c.SessionID+"/chats/overview?limit=500", nil, &out)
	if err == nil && len(out) > 0 {
		return out, nil
	}
	// Fallback: heavier /chats endpoint (returns full chat objects). We map
	// down to the subset we need.
	type heavyChat struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		IsGroup bool   `json:"isGroup"`
	}
	var heavy []heavyChat
	if ferr := c.do("GET", "/api/"+c.SessionID+"/chats?limit=500", nil, &heavy); ferr == nil {
		mapped := make([]wahaChatOverview, 0, len(heavy))
		for _, h := range heavy {
			mapped = append(mapped, wahaChatOverview{ID: h.ID, Name: h.Name, IsGroup: h.IsGroup})
		}
		return mapped, nil
	}
	return out, err
}

// wahaContact is WAHA's contact representation. `name` comes from the user's
// WhatsApp address book; `pushname` is what the contact set as their own
// display name in WhatsApp. Either is a better label than the raw phone JID.
type wahaContact struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PushName string `json:"pushname"`
	Number   string `json:"number"`
	IsMe     bool   `json:"isMe"`
}

// GetContact fetches a single contact by JID. Used as a fallback when a
// message arrives for a chat with no resolved name yet.
func (c *Client) GetContact(jid string) (*wahaContact, error) {
	q := url.Values{}
	q.Set("contactId", jid)
	q.Set("session", c.SessionID)
	var out wahaContact
	if err := c.do("GET", "/api/contacts?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAllContacts returns every contact in the address book plus the @lid
// identities WAHA has already seen. Critical for resolving chats in @lid form
// (WhatsApp's new opaque identifier, with no visible number) — the address
// book's `name` rarely comes through here, but `pushname` (the display name
// the user set in WhatsApp) is populated for most of them.
//
// A global endpoint (`/api/contacts/all`) with `session` in the query —
// distinct from `/api/{session}/contacts/all`, which returns an empty or
// broken list on GOWS.
func (c *Client) ListAllContacts() ([]wahaContact, error) {
	q := url.Values{}
	q.Set("session", c.SessionID)
	q.Set("limit", "5000")
	var out []wahaContact
	if err := c.do("GET", "/api/contacts/all?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Best display label for a contact: address-book Name first, then PushName,
// then number, then JID. Empty if all fields blank.
func (k *wahaContact) Label() string {
	if k.Name != "" {
		return k.Name
	}
	if k.PushName != "" {
		return k.PushName
	}
	if k.Number != "" {
		return k.Number
	}
	return k.ID
}

// MarkChatRead sends a read receipt for the given chat (clears unread badge
// on the phone too).
func (c *Client) MarkChatRead(chatJID string) error {
	body := map[string]any{
		"chatId":  chatJID,
		"session": c.SessionID,
	}
	return c.do("POST", "/api/sendSeen", body, nil)
}
