package whatsapp

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// webhookEnvelope is the WAHA event wrapper. Payload is opaque JSON whose
// shape depends on `event` — we type the events we handle.
type webhookEnvelope struct {
	Event     string          `json:"event"`
	Session   string          `json:"session"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp int64           `json:"timestamp"`
}

// HandleWebhook is the WAHA -> control plane event endpoint. This is the ONLY
// whatsapp route outside auth.Middleware; HMAC-SHA512 over the raw body in
// `X-Webhook-Hmac` stands in for auth.
//
// Return codes (WAHA retries on anything that is not 2xx):
//   - 200 when processed successfully
//   - 400/401 on a PERMANENT error (bad HMAC, broken JSON) — retrying is pointless
//   - 503 on an INTERNAL error (store I/O, panic) — WAHA redelivers and we get
//     another go. Answering 200 in those cases hides the problem and loses the
//     message.
func (s *Service) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if s.hmacAtual() != "" {
		got := r.Header.Get("X-Webhook-Hmac")
		if got == "" {
			s.markHookErr("missing X-Webhook-Hmac")
			http.Error(w, "missing hmac", http.StatusUnauthorized)
			return
		}
		if !s.hmacConfere(body, got) {
			s.markHookErr("hmac mismatch")
			http.Error(w, "bad hmac", http.StatusUnauthorized)
			return
		}
	}

	var env webhookEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		s.markHookErr("parse: " + err.Error())
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	_, _ = s.Store.SetState(func(st *State) {
		st.HookOK = true
		st.HookLastErr = ""
		st.HookLastTS = time.Now().Unix()
	})

	switch env.Event {
	case "session.status":
		s.handleSessionStatus(env.Payload)
	case "message", "message.any":
		s.handleMessage(env.Payload, env.Event)
	case "message.ack":
		s.handleAck(env.Payload)
	case "message.revoked":
		s.handleRevoked(env.Payload)
	case "presence.update":
		s.handlePresence(env.Payload)
	case "media.recovered":
		s.handleMediaRecovered(env.Payload)
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleMediaRecovered: the daemon recovered an old message's media key (via
// history sync). This enqueues the download — the worker fetches it through
// /api/files/<id>, writes it to MediaRoot and broadcasts, so the bubble swaps
// "Download" for the live image.
func (s *Service) handleMediaRecovered(raw json.RawMessage) {
	var p struct {
		Chat     string `json:"chat"`
		ID       string `json:"id"`
		Mimetype string `json:"mimetype"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.ID == "" || p.Chat == "" {
		return
	}
	chat := s.CanonicalChatJID(p.Chat)
	s.EnqueueDownload(chat, p.ID, "/api/files/"+p.ID, p.Mimetype, "")
}

// markHookErr surfaces a webhook-processing failure to the UI without flipping
// the connection state itself.
func (s *Service) markHookErr(msg string) {
	log.Printf("whatsapp webhook: %s", msg)
	_, _ = s.Store.SetState(func(st *State) {
		st.HookOK = false
		st.HookLastErr = msg
		st.HookLastTS = time.Now().Unix()
	})
}

// --- per-event handlers ---

type sessionStatusPayload struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Engine struct {
		Engine string `json:"engine"`
	} `json:"engine"`
	Me struct {
		ID       string `json:"id"`
		PushName string `json:"pushName"`
	} `json:"me"`
}

func (s *Service) handleSessionStatus(raw json.RawMessage) {
	var p sessionStatusPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	st, _ := s.Store.SetState(func(st *State) {
		st.Status = p.Status
		if p.Engine.Engine != "" {
			st.Engine = p.Engine.Engine
		}
		if p.Me.ID != "" {
			// "55119...@c.us" → "55119..."
			st.Phone = strings.SplitN(p.Me.ID, "@", 2)[0]
		}
		if p.Me.PushName != "" {
			st.PushName = p.Me.PushName
		}
		if p.Status == StatusWorking {
			st.LastSyncTS = time.Now().Unix()
			st.QRDataURL = ""
		}
		if p.Status == StatusScanQR {
			st.LastQRTS = time.Now().Unix()
		}
	})
	// Pull fresh QR when entering SCAN_QR_CODE.
	if p.Status == StatusScanQR && s.Client != nil {
		if qr, err := s.Client.GetQR(); err == nil && qr != "" {
			st, _ = s.Store.SetState(func(st *State) { st.QRDataURL = qr })
		}
	}
	s.Broadcaster.Send(WSEvent{Kind: "status", State: &st, TS: time.Now().Unix()})
}

// wahaMessagePayload captures the commonly-used fields. WAHA emits many more
// edge-case fields; we keep the original JSON in Message.RawJSON for debugging
// or future-proofing without burying every minor field into the schema.
//
// GOWS engine quirk: the top-level `type` field is empty and `media` is null
// until /api/chats/.../messages?downloadMedia=true is called. The real media
// type lives in `_data.Info.MediaType` (image/video/audio/document) and a
// `Type=media` marker in `_data.Info.Type`. We unmarshal that nested struct
// too so handleMessage can fill Message.Type even without auto-download.
type wahaMessagePayload struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	FromMe    bool   `json:"fromMe"`
	Body      string `json:"body"`
	Caption   string `json:"caption"`
	Type      string `json:"type"` // chat / image / audio / ptt / video / document / sticker / location / vcard
	Timestamp int64  `json:"timestamp"`
	HasMedia  bool   `json:"hasMedia"`
	MediaURL  string `json:"mediaUrl,omitempty"`
	Media     *struct {
		URL      string `json:"url,omitempty"`
		MimeType string `json:"mimetype,omitempty"`
		Filename string `json:"filename,omitempty"`
	} `json:"media,omitempty"`
	MimeType   string `json:"mimetype,omitempty"`
	Filename   string `json:"filename,omitempty"`
	QuotedID   string `json:"quotedMsgId,omitempty"`
	Ack        int    `json:"ack"`
	NotifyName string `json:"notifyName,omitempty"` // sender's display name (WhatsApp push name)
	Data       *struct {
		Info *struct {
			Type      string `json:"Type"`      // "media" / "text" / "reaction" / etc
			MediaType string `json:"MediaType"` // image / video / audio / document / sticker / ptt
			Sender    string `json:"Sender"`    // sender JID (in groups: the participant, not the group)
		} `json:"Info,omitempty"`
		Message *struct {
			ReactionMessage *struct {
				Key *struct {
					ID          string `json:"ID"`
					RemoteJID   string `json:"RemoteJID"`
					FromMe      bool   `json:"FromMe"`
					Participant string `json:"Participant"`
				} `json:"Key"`
				Text              string `json:"Text"`              // emoji ("" = remove)
				SenderTimestampMS int64  `json:"SenderTimestampMS"` // ms unix
			} `json:"reactionMessage,omitempty"`
		} `json:"Message,omitempty"`
	} `json:"_data,omitempty"`
}

func (s *Service) handleMessage(raw json.RawMessage, _ string) {
	var p wahaMessagePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		s.markHookErr("msg parse: " + err.Error())
		return
	}
	// Resolve the conversation in a group-aware way: outbound 1:1 -> To (the
	// peer); inbound -> From (the peer); a group -> the @g.us side (NEVER the To,
	// which on a fromMe group message is our own number, dumping everything into
	// the chat with ourselves).
	chat := resolveChatJID(p.From, p.To, p.FromMe)
	// Canonicalise: normalise @s.whatsapp.net -> @c.us AND resolve @lid -> @c.us
	// through the in-memory whatsmeow_lid_map cache. Without converting right
	// here, a reply to a @c.us creates a new chat under @lid that stays orphaned
	// until the next 30s poll merges it.
	chat = s.CanonicalChatJIDLazy(chat) // lazy lookup in gows.db on a cache miss
	if chat == "" || p.ID == "" {
		return
	}
	// Reaction: WhatsApp sends one message event per emoji applied. It must NOT
	// become a separate bubble — attach the Reaction to the target message.
	// Without this, reactions would litter the UI as "empty bubbles" (filtered
	// in the frontend now, but still wasting an AppendMessage plus a broadcast).
	// Detected via _data.Info.Type=="reaction" + _data.Message.reactionMessage.
	if p.Data != nil && p.Data.Info != nil && p.Data.Info.Type == "reaction" &&
		p.Data.Message != nil && p.Data.Message.ReactionMessage != nil {
		rxn := p.Data.Message.ReactionMessage
		if rxn.Key != nil && rxn.Key.ID != "" {
			emoji := rxn.Text
			// In a group the sender is in _data.Info.Sender or rxn.Key.Participant
			// (in a 1-on-1 both can be empty — we fall back to p.From). Without this,
			// group reactions were attributed to the group itself (the chip showed
			// "5522xxxxx@g.us" instead of the reactor's name).
			fromRaw := ""
			if p.Data.Info.Sender != "" {
				fromRaw = p.Data.Info.Sender
			} else if rxn.Key.Participant != "" {
				fromRaw = rxn.Key.Participant
			} else {
				fromRaw = p.From
			}
			from := s.CanonicalChatJIDLazy(fromRaw)
			ts := rxn.SenderTimestampMS / 1000
			if ts == 0 {
				ts = p.Timestamp
			}
			if ts == 0 {
				ts = time.Now().Unix()
			}
			if err := s.Store.AddReaction(chat, rxn.Key.ID, from, emoji, ts); err == nil {
				s.Broadcaster.Send(WSEvent{
					Kind: "reaction", ChatJID: chat, AckID: rxn.Key.ID,
					ReactionFrom: from, ReactionEmoji: emoji, TS: ts,
				})
			}
		}
		return
	}
	// Idempotency: WAHA retries the webhook on a network failure. If the message
	// is already in the store, return without doing anything. Append is expensive
	// (lock + flush) and triggers a broadcast that re-renders the UI — duplicating
	// it is bad UX, not just wasted I/O.
	if exists, err := s.Store.HasMessage(chat, p.ID, p.Timestamp); err == nil && exists {
		return
	}
	body := p.Body
	if body == "" && p.Caption != "" {
		body = p.Caption
	}
	// The GOWS engine emits an empty `type` for media; the real type is in
	// _data.Info.MediaType. An explicit fallback keeps us from classifying a
	// photo/audio/video as "text" and losing the right rendering.
	rawType := p.Type
	if rawType == "" && p.Data != nil && p.Data.Info != nil && p.Data.Info.MediaType != "" {
		rawType = p.Data.Info.MediaType
	}
	// Media but no MediaType (sticker, location, etc.) — mark it as "media" so
	// the UI at least shows a placeholder instead of empty text.
	if rawType == "" && p.HasMedia {
		rawType = "media"
	}
	m := Message{
		ID:       p.ID,
		ChatJID:  chat,
		FromJID:  s.CanonicalChatJID(p.From),
		FromMe:   p.FromMe,
		TS:       p.Timestamp,
		Type:     normalizeType(rawType),
		Body:     body,
		QuotedID: p.QuotedID,
		Ack:      p.Ack,
	}
	if m.TS == 0 {
		m.TS = time.Now().Unix()
	}
	if len(raw) > 4096 {
		m.RawJSON = string(raw[:4096]) + "...(trunc)"
	} else {
		m.RawJSON = string(raw)
	}
	mediaWAHAURL := ""
	if p.HasMedia {
		// MimeType may arrive top-level OR inside media{}; take whichever is there.
		mime := p.MimeType
		filename := p.Filename
		url := p.MediaURL
		if p.Media != nil {
			if mime == "" {
				mime = p.Media.MimeType
			}
			if filename == "" {
				filename = p.Media.Filename
			}
			if url == "" {
				url = p.Media.URL
			}
		}
		mediaWAHAURL = url
		// Path STAYS empty until the worker downloads it — the frontend shows a
		// "Download" button as a placeholder until the WSEvent{Kind:"message"}
		// broadcast arrives with Media.Path filled in. Mime and filename help the
		// UI pick an icon.
		m.Media = &Media{
			MimeType: mime,
			Filename: filename,
			Path:     "",
		}
	}
	if err := s.Store.AppendMessage(m); err != nil {
		s.markHookErr("append: " + err.Error())
		return
	}
	// Auto-download of inbound IMAGES as they arrive (a user requirement):
	// images and stickers should appear on their own, without the user pressing
	// "Download" or "Sync". Previously only opening the chat (handleMessagesList)
	// enqueued the download, so media arriving while the chat was already open
	// stayed stuck on the placeholder. normalizeType maps sticker->"image", so
	// this gate covers images and stickers alike. LARGE media (video, document,
	// audio) stays on demand (the "Download" button, or opening the chat) so we
	// do not burn bandwidth and disk on chats the user never opens. The worker
	// downloads in the background and RE-broadcasts with Media.Path set, so the
	// bubble swaps the placeholder for a live <img>. Non-blocking, best effort.
	if p.HasMedia && m.Type == "image" && m.Media != nil {
		s.EnqueueDownload(chat, m.ID, "/api/files/"+m.ID, m.Media.MimeType, m.Media.Filename)
	}
	_ = mediaWAHAURL
	if err := s.Store.TouchChatWithMessage(m); err != nil {
		s.markHookErr("touch: " + err.Error())
	}
	// Best effort: when the message arrives carrying `notifyName` (the sender's
	// WhatsApp push name) and the chat has no Name resolved from the address
	// book yet, use the push name as a fallback. The periodic name sync
	// (state.go:syncChatNames) eventually overwrites it with the name saved in
	// the address book, once that is available.
	if !p.FromMe && p.NotifyName != "" {
		_ = s.Store.MergeChatName(chat, p.NotifyName, false)
	}
	s.Broadcaster.Send(WSEvent{Kind: "message", Message: &m, TS: time.Now().Unix()})
}

type ackPayload struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Ack  int    `json:"ack"`
}

func (s *Service) handleAck(raw json.RawMessage) {
	var p ackPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	// Group-aware (same as handleMessage): otherwise the ack of a group message
	// resolved to our own number and did not match the message filed under the group.
	chat := resolveChatJID(p.From, p.To, true)
	chat = s.CanonicalChatJID(chat)
	if p.ID == "" || chat == "" {
		return
	}
	_ = s.Store.UpdateAck(chat, p.ID, p.Ack)
	s.Broadcaster.Send(WSEvent{Kind: "ack", AckID: p.ID, AckN: p.Ack, TS: time.Now().Unix()})
}

func (s *Service) handleRevoked(raw json.RawMessage) {
	var p struct {
		ID   string `json:"id"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	chat := resolveChatJID(p.From, p.To, true) // group-aware (see handleAck)
	chat = s.CanonicalChatJID(chat)
	if p.ID == "" {
		return
	}
	_ = s.Store.MarkDeleted(chat, p.ID)
	s.Broadcaster.Send(WSEvent{Kind: "revoked", AckID: p.ID, TS: time.Now().Unix()})
}

// presencePayload mirrors WAHA's `presence.update` event. The `presences[]`
// list holds one item per participant (a group may have several; a 1-on-1
// only has the contact). Each says online/offline + lastSeen + whether they
// are typing or recording a voice note.
type presencePayload struct {
	ID        string `json:"id"` // chat JID
	Presences []struct {
		Participant string `json:"participant"`
		LastKnown   string `json:"lastKnownPresence"` // available/unavailable/composing/recording
		LastSeen    int64  `json:"lastSeen"`
	} `json:"presences"`
}

func (s *Service) handlePresence(raw json.RawMessage) {
	var p presencePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	p.ID = s.CanonicalChatJID(p.ID)
	if p.ID == "" || len(p.Presences) == 0 {
		return
	}
	// For a 1-on-1, take the first (and only) one. For groups we aggregate: if
	// any participant is composing or recording, show that. Otherwise use the
	// first one available.
	state := ""
	lastSeen := int64(0)
	for _, pr := range p.Presences {
		if pr.LastKnown == "composing" || pr.LastKnown == "recording" {
			state = pr.LastKnown
			break
		}
		if state == "" {
			state = pr.LastKnown
		}
		if pr.LastSeen > lastSeen {
			lastSeen = pr.LastSeen
		}
	}
	_ = s.Store.MergePresence(p.ID, state, lastSeen)
	s.Broadcaster.Send(WSEvent{
		Kind:    "presence",
		ChatJID: p.ID, Presence: state, LastSeenTS: lastSeen,
		TS: time.Now().Unix(),
	})
}

// normalizeType maps WAHA's wire types to our canonical types. It accepts
// both `payload.type` (WEBJS mode) and `_data.Info.MediaType` (GOWS mode),
// and "media" as a fallback when hasMedia=true without a specific MediaType.
func normalizeType(t string) string {
	switch t {
	case "chat", "":
		return "text"
	case "url", "extendedTextMessage":
		// A WhatsApp link preview: the body already carries the URL and text, so
		// the frontend renders it as ordinary text (CSS linkify auto-links it).
		return "text"
	case "ptt", "audio":
		// GOWS distinguishes ptt (a voice note) from audio (a file). The frontend
		// treats both as an audio control — mapping to "voice" is enough to reuse
		// the same bubble. An audio file still lands on the control, which is fine.
		if t == "audio" {
			return "audio"
		}
		return "voice"
	case "sticker":
		// A sticker is WebP, so the image bubble covers it. The different size
		// (max-height 160px) is handled by frontend CSS where needed.
		return "image"
	case "vcard":
		return "contact"
	case "media":
		return "document" // generic fallback — a bubble with icon + filename
	default:
		return t
	}
}

// AckLabel maps the integer ack code to a human label (exported for CLI use).
func AckLabel(a int) string {
	switch {
	case a >= 4:
		return "played"
	case a >= 3:
		return "read"
	case a >= 2:
		return "device"
	case a >= 1:
		return "server"
	default:
		return "pending"
	}
}

// hmacConfere validates the signature against the in-memory secret and, if
// that fails, RE-READS the secret from the source (the vault) and tries again
// before refusing.
//
// The re-read exists because the secret was cached on the *Service forever:
// once the panel and the daemon diverged, EVERY inbound message was discarded
// until somebody restarted the control plane — and nobody notices, because
// the discard is silent and the panel keeps reporting "connected". It
// happened with both sides stable and no restart, and two real messages were
// lost; the daemon already knew how to recover by re-reading disk, but the
// panel held no half of that contract. Now it does, and the self-healing
// closes on both sides.
//
// Zero cost on the happy path: the reload only runs when the comparison fails.
func (s *Service) hmacConfere(body []byte, got string) bool {
	atual := s.hmacAtual()
	if hmacBate(atual, body, got) {
		return true
	}
	if s.HMACRefresh == nil {
		return false
	}
	novo := s.HMACRefresh()
	if novo == "" || novo == atual {
		return false
	}
	if !hmacBate(novo, body, got) {
		return false
	}
	log.Printf("whatsapp webhook: secret reloaded from the vault (the cached one was stale) — event accepted instead of dropped")
	s.hmacTroca(novo)
	return true
}

// hmacAtual returns the secret in use, under a read lock. It is the ONLY way
// to read the field — see the comment on Service.hmacSecret.
func (s *Service) hmacAtual() string {
	s.hmacMu.RLock()
	defer s.hmacMu.RUnlock()
	return s.hmacSecret
}

// hmacTroca adopts the secret reloaded from the vault, under a write lock.
func (s *Service) hmacTroca(novo string) {
	s.hmacMu.Lock()
	defer s.hmacMu.Unlock()
	s.hmacSecret = novo
}

func hmacBate(secret string, body []byte, got string) bool {
	if secret == "" {
		return false
	}
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(got))
}
