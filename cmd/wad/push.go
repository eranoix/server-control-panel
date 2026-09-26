package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// pusher emits WAHA-compatible webhook events to the vps-manager server, so the
// existing internal/whatsapp/webhook.go handlers (and Store/Broadcaster) keep
// working unchanged. It signs each POST with the per-user HMAC secret.
type pusher struct {
	base     string // e.g. http://127.0.0.1:8765
	secretOf func(user string) string
	reloadOf func(user string) (string, bool)
	httpc    *http.Client
}

func newPusher(base string, secretOf func(string) string) *pusher {
	return &pusher{base: base, secretOf: secretOf, httpc: &http.Client{Timeout: 15 * time.Second}}
}

// envelope mirrors webhookEnvelope on the server side.
type envelope struct {
	Event     string      `json:"event"`
	Session   string      `json:"session"`
	Payload   interface{} `json:"payload"`
	Timestamp int64       `json:"timestamp"`
}

// wahaMsgOut mirrors wahaMessagePayload (the fields the server reads).
type wahaMsgOut struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	To         string `json:"to"`
	FromMe     bool   `json:"fromMe"`
	Body       string `json:"body"`
	Caption    string `json:"caption,omitempty"`
	Type       string `json:"type"`
	Timestamp  int64  `json:"timestamp"`
	HasMedia   bool   `json:"hasMedia"`
	MediaURL   string `json:"mediaUrl,omitempty"`
	MimeType   string `json:"mimetype,omitempty"`
	Filename   string `json:"filename,omitempty"`
	QuotedID   string `json:"quotedMsgId,omitempty"`
	Ack        int    `json:"ack"`
	NotifyName string `json:"notifyName,omitempty"`
	Data       struct {
		Info struct {
			Type      string `json:"Type,omitempty"`
			MediaType string `json:"MediaType,omitempty"`
			Sender    string `json:"Sender,omitempty"`
		} `json:"Info"`
	} `json:"_data"`
}

// ackOut mirrors ackPayload.
type ackOut struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to,omitempty"`
	Ack  int    `json:"ack"`
}

func (p *pusher) event(user, event string, payload interface{}) {
	body, err := json.Marshal(envelope{
		Event: event, Session: "default", Payload: payload, Timestamp: time.Now().Unix(),
	})
	if err != nil {
		return
	}
	// Asynchronous delivery with retry: the daemon is the ONLY source of inbound
	// events (the server no longer polls). If vps-manager is restarting (the very
	// deploy scenario this daemon exists to survive), a single delivery attempt
	// would lose the message forever (whatsmeow does not redeliver). Retry with
	// backoff covers the restart window; async so it never blocks whatsmeow's event loop.
	secret := p.secretOf(user)
	url := p.base + "/api/whatsapp/webhook/" + user
	go p.deliver(user, event, url, secret, body)
}

// reloadSecret is injected by the manager: it re-reads meta.json from disk and
// reports whether the secret changed. See manager.recarregaMeta.
func (p *pusher) reloadSecret(user string) (string, bool) {
	if p.reloadOf == nil {
		return "", false
	}
	return p.reloadOf(user)
}

func (p *pusher) deliver(user, event, url, secret string, body []byte) {
	// ~ 0.5+1+2+4+5 = backoff covering a health-gated restart (a few seconds).
	backoffs := []time.Duration{0, 500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second}
	var lastErr error
	for i, wait := range backoffs {
		if wait > 0 {
			time.Sleep(wait)
		}
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if secret != "" {
			mac := hmac.New(sha512.New, []byte(secret))
			mac.Write(body)
			req.Header.Set("X-Webhook-Hmac", hex.EncodeToString(mac.Sum(nil)))
		}
		resp, err := p.httpc.Do(req)
		if err == nil {
			code := resp.StatusCode
			// The body says WHICH 401 this is ("missing hmac" vs "bad hmac"); throwing
			// it away was what turned diagnosis into guesswork.
			corpo, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			if code >= 200 && code < 300 {
				return // sucesso
			}
			if code == http.StatusUnauthorized || code == http.StatusForbidden {
				// The in-memory secret may be stale — the panel rewrites meta.json
				// on every boot. Re-reading and retrying ONCE fixes by itself what
				// used to need a manual restart, and without it the message is lost
				// (whatsmeow does not redeliver a refused event).
				if novo, mudou := p.reloadSecret(user); mudou {
					log.Printf("wad push %s/%s: HTTP %d — secret reloaded from disk, retrying", user, event, code)
					secret = novo
					continue
				}
			}
			if code < 500 {
				// 4xx is permanent (retrying is pointless) — BUT dropping it
				// silently hides message loss for days. The real case: a 401
				// "bad hmac" when meta.json's hmac_secret diverges from the vault
				// (e.g. clobbered by a non-canonical worktree). whatsmeow does NOT
				// redeliver, so the message is gone for good. Log LOUD with the
				// hint for the fix instead of swallowing it.
				// The hint has to reflect what the SERVER answered: "missing
				// hmac" (empty secret on this side) and "bad hmac" (mismatched
				// secrets) have opposite causes, and a single catch-all message
				// sent you investigating the wrong one — comparing secrets that
				// were already identical.
				hint := ""
				if code == http.StatusUnauthorized || code == http.StatusForbidden {
					motivo := strings.TrimSpace(string(corpo))
					switch {
					case strings.Contains(motivo, "missing"):
						hint = " (the daemon did not sign it: hmac_secret is EMPTY in the meta.json of " + user + ")"
					case strings.Contains(motivo, "bad hmac"):
						hint = " (the secrets DIFFER between meta.json and the running panel, and re-reading the disk did not fix it — the panel is holding a stale secret in cache; restart vps-manager)"
					default:
						hint = " (response: " + motivo + ")"
					}
				}
				log.Printf("wad push %s/%s: DROPPED — HTTP %d permanent%s", user, event, code, hint)
				return
			}
			lastErr = fmt.Errorf("HTTP %d", code)
		} else {
			lastErr = err
		}
		_ = i
	}
	log.Printf("wad push %s/%s: gave up after retries: %v", user, event, lastErr)
}

// reaction emits a `message` event in the WAHA reaction shape (the server
// attaches it to the target message instead of creating a bubble).
func (p *pusher) reaction(user, chat, targetMsgID, fromJID, emoji string, ts int64) {
	type keyT struct {
		ID          string `json:"ID"`
		Participant string `json:"Participant"`
	}
	type rxnT struct {
		Key               *keyT  `json:"Key"`
		Text              string `json:"Text"`
		SenderTimestampMS int64  `json:"SenderTimestampMS"`
	}
	payload := map[string]any{
		"id": targetMsgID, "from": chat, "to": "", "fromMe": false, "timestamp": ts,
		"_data": map[string]any{
			"Info":    map[string]any{"Type": "reaction", "Sender": fromJID},
			"Message": map[string]any{"reactionMessage": rxnT{Key: &keyT{ID: targetMsgID, Participant: fromJID}, Text: emoji, SenderTimestampMS: ts * 1000}},
		},
	}
	p.event(user, "message", payload)
}

// presence emits a `presence.update` event for a 1:1 contact or chat. The
// participant identifies the actor behind the state (in a group it is the real
// typist, not the group's JID); when empty, it falls back to chatJID itself
// (the 1:1 case).
func (p *pusher) presence(user, chatJID, participantJID, state string, lastSeen int64) {
	type prT struct {
		Participant string `json:"participant"`
		LastKnown   string `json:"lastKnownPresence"`
		LastSeen    int64  `json:"lastSeen"`
	}
	if participantJID == "" {
		participantJID = chatJID
	}
	p.event(user, "presence.update", struct {
		ID        string `json:"id"`
		Presences []prT  `json:"presences"`
	}{ID: chatJID, Presences: []prT{{Participant: participantJID, LastKnown: state, LastSeen: lastSeen}}})
}

func (p *pusher) sessionStatus(user, status, phone, pushName string) {
	type meT struct {
		ID       string `json:"id"`
		PushName string `json:"pushName"`
	}
	type engT struct {
		Engine string `json:"engine"`
	}
	id := ""
	if phone != "" {
		id = phone + "@c.us"
	}
	p.event(user, "session.status", struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Engine engT   `json:"engine"`
		Me     meT    `json:"me"`
	}{Name: "default", Status: status, Engine: engT{Engine: "WHATSMEOW"}, Me: meT{ID: id, PushName: pushName}})
}
