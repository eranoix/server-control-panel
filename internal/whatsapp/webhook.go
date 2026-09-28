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

type webhookEnvelope struct {
	Event     string          `json:"event"`
	Session   string          `json:"session"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp int64           `json:"timestamp"`
}

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

	if s.currentHMAC() != "" {
		got := r.Header.Get("X-Webhook-Hmac")
		if got == "" {
			s.markHookErr("missing X-Webhook-Hmac")
			http.Error(w, "missing hmac", http.StatusUnauthorized)
			return
		}
		if !s.hmacMatches(body, got) {
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

func (s *Service) markHookErr(msg string) {
	log.Printf("whatsapp webhook: %s", msg)
	_, _ = s.Store.SetState(func(st *State) {
		st.HookOK = false
		st.HookLastErr = msg
		st.HookLastTS = time.Now().Unix()
	})
}

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
	if p.Status == StatusScanQR && s.Client != nil {
		if qr, err := s.Client.GetQR(); err == nil && qr != "" {
			st, _ = s.Store.SetState(func(st *State) { st.QRDataURL = qr })
		}
	}
	s.Broadcaster.Send(WSEvent{Kind: "status", State: &st, TS: time.Now().Unix()})
}

type wahaMessagePayload struct {
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
	MimeType   string `json:"mimetype,omitempty"`
	Filename   string `json:"filename,omitempty"`
	QuotedID   string `json:"quotedMsgId,omitempty"`
	Ack        int    `json:"ack"`
	NotifyName string `json:"notifyName,omitempty"`
	Data       *struct {
		Info *struct {
			Type      string `json:"Type"`
			MediaType string `json:"MediaType"`
			Sender    string `json:"Sender"`
		} `json:"Info,omitempty"`
		Message *struct {
			ReactionMessage *struct {
				Key *struct {
					ID          string `json:"ID"`
					RemoteJID   string `json:"RemoteJID"`
					FromMe      bool   `json:"FromMe"`
					Participant string `json:"Participant"`
				} `json:"Key"`
				Text              string `json:"Text"`
				SenderTimestampMS int64  `json:"SenderTimestampMS"`
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
	chat := resolveChatJID(p.From, p.To, p.FromMe)
	chat = s.CanonicalChatJIDLazy(chat)
	if chat == "" || p.ID == "" {
		return
	}
	if p.Data != nil && p.Data.Info != nil && p.Data.Info.Type == "reaction" &&
		p.Data.Message != nil && p.Data.Message.ReactionMessage != nil {
		rxn := p.Data.Message.ReactionMessage
		if rxn.Key != nil && rxn.Key.ID != "" {
			emoji := rxn.Text
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
	if exists, err := s.Store.HasMessage(chat, p.ID, p.Timestamp); err == nil && exists {
		return
	}
	body := p.Body
	if body == "" && p.Caption != "" {
		body = p.Caption
	}
	rawType := p.Type
	if rawType == "" && p.Data != nil && p.Data.Info != nil && p.Data.Info.MediaType != "" {
		rawType = p.Data.Info.MediaType
	}
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
	if p.HasMedia && m.Type == "image" && m.Media != nil {
		s.EnqueueDownload(chat, m.ID, "/api/files/"+m.ID, m.Media.MimeType, m.Media.Filename)
	}
	_ = mediaWAHAURL
	if err := s.Store.TouchChatWithMessage(m); err != nil {
		s.markHookErr("touch: " + err.Error())
	}
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
	chat := resolveChatJID(p.From, p.To, true)
	chat = s.CanonicalChatJID(chat)
	if p.ID == "" {
		return
	}
	_ = s.Store.MarkDeleted(chat, p.ID)
	s.Broadcaster.Send(WSEvent{Kind: "revoked", AckID: p.ID, TS: time.Now().Unix()})
}

type presencePayload struct {
	ID        string `json:"id"`
	Presences []struct {
		Participant string `json:"participant"`
		LastKnown   string `json:"lastKnownPresence"`
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

func normalizeType(t string) string {
	switch t {
	case "chat", "":
		return "text"
	case "url", "extendedTextMessage":
		return "text"
	case "ptt", "audio":
		if t == "audio" {
			return "audio"
		}
		return "voice"
	case "sticker":
		return "image"
	case "vcard":
		return "contact"
	case "media":
		return "document"
	default:
		return t
	}
}

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

func (s *Service) hmacMatches(body []byte, got string) bool {
	current := s.currentHMAC()
	if hmacMatches(current, body, got) {
		return true
	}
	if s.HMACRefresh == nil {
		return false
	}
	fresh := s.HMACRefresh()
	if fresh == "" || fresh == current {
		return false
	}
	if !hmacMatches(fresh, body, got) {
		return false
	}
	log.Printf("whatsapp webhook: secret reloaded from the vault (the cached one was stale) — event accepted instead of dropped")
	s.hmacRotate(fresh)
	return true
}

func (s *Service) currentHMAC() string {
	s.hmacMu.RLock()
	defer s.hmacMu.RUnlock()
	return s.hmacSecret
}

func (s *Service) hmacRotate(fresh string) {
	s.hmacMu.Lock()
	defer s.hmacMu.Unlock()
	s.hmacSecret = fresh
}

func hmacMatches(secret string, body []byte, got string) bool {
	if secret == "" {
		return false
	}
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(got))
}
