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

type Client struct {
	BaseURL   string
	APIKey    string
	SessionID string
	httpc     *http.Client
}

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

func (c *Client) withTimeout(d time.Duration) *Client {
	clone := *c
	clone.httpc = &http.Client{Timeout: d}
	return &clone
}

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
				"webhooks": []map[string]any{},
			},
		}
		return c.do("POST", "/api/sessions/", body, nil)
	}
	switch sess.Status {
	case StatusWorking, StatusScanQR, StatusStarting:
		return nil
	case StatusFailed:
		return c.RestartSession()
	default:
		err := c.do("POST", "/api/sessions/"+c.SessionID+"/start", nil, nil)
		if err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "already started") {
			return nil
		}
		return err
	}
}

func (c *Client) EnsureExtraWebhook(extraURL, hmacKey string, events []string) error {
	if extraURL == "" {
		return nil
	}
	if len(events) == 0 {
		events = []string{"session.status", "message", "message.any", "message.ack", "message.revoked"}
	}
	var current sessionWithConfig
	if err := c.do("GET", "/api/sessions/"+c.SessionID, nil, &current); err != nil {
		return fmt.Errorf("get session config: %w", err)
	}
	for _, w := range current.Config.Webhooks {
		if w.URL == extraURL {
			return nil
		}
	}
	newWebhook := webhookEntry{
		URL:    extraURL,
		Events: events,
	}
	if hmacKey != "" {
		newWebhook.HMAC = &webhookHMAC{Key: hmacKey}
	}
	newWebhooks := append([]webhookEntry{}, current.Config.Webhooks...)
	newWebhooks = append(newWebhooks, newWebhook)
	body := map[string]any{
		"config": map[string]any{
			"webhooks": newWebhooks,
		},
	}
	return c.do("PUT", "/api/sessions/"+c.SessionID, body, nil)
}

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

func (c *Client) RestartSession() error {
	return c.do("POST", "/api/sessions/"+c.SessionID+"/restart", nil, nil)
}

func (c *Client) StopSession() error {
	return c.do("POST", "/api/sessions/"+c.SessionID+"/stop", nil, nil)
}

func (c *Client) LogoutSession() error {
	return c.do("POST", "/api/sessions/"+c.SessionID+"/logout", nil, nil)
}

func (c *Client) GetQR() (string, error) {
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
		return "", nil
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

type wahaChatOverview struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Picture     string `json:"picture,omitempty"`
	IsGroup     bool   `json:"isGroup"`
	UnreadCount int    `json:"unreadCount"`
	Archived    bool   `json:"archived"`
	Pinned      bool   `json:"pinned"`
	Muted       bool   `json:"muted"`
}

func (c *Client) ListChatsOverview() ([]wahaChatOverview, error) {
	var out []wahaChatOverview
	err := c.do("GET", "/api/"+c.SessionID+"/chats/overview?limit=500", nil, &out)
	if err == nil && len(out) > 0 {
		return out, nil
	}
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

type wahaContact struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PushName string `json:"pushname"`
	Number   string `json:"number"`
	IsMe     bool   `json:"isMe"`
}

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

func (c *Client) MarkChatRead(chatJID string) error {
	body := map[string]any{
		"chatId":  chatJID,
		"session": c.SessionID,
	}
	return c.do("POST", "/api/sendSeen", body, nil)
}
