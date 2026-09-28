package whatsapp

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

func (s *Service) startPoller(ctx context.Context) {
	go func() {
		s.pollOnce()
		s.syncChatNames()
		s.syncChatFlagsFromGOWS()
		_ = s.consolidateLIDChats()
		s.ensureExtraWebhook()
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		webhookTicker := time.NewTicker(2 * time.Minute)
		defer webhookTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.pollOnce()
				s.syncChatNames()
				s.syncChatFlagsFromGOWS()
			case <-webhookTicker.C:
				s.ensureExtraWebhook()
			}
		}
	}()
}

func (s *Service) ensureExtraWebhook() {
	if s == nil || s.Client == nil || s.ExtraWebhookURL == "" {
		return
	}
	sess, err := s.Client.GetSession()
	if err != nil || sess == nil {
		return
	}
	if err := s.Client.EnsureExtraWebhook(s.ExtraWebhookURL, s.currentHMAC(), s.ExtraWebhookEvents); err != nil {
		log.Printf("whatsapp: ensure extra webhook %s: %v", s.ExtraWebhookURL, err)
	}
}

func (s *Service) DeepImport(historyPerChat int) error {
	if s.Client == nil {
		return nil
	}
	if err := s.Store.WipeImport(); err != nil {
		return err
	}
	chats, err := s.Client.ListChatsOverview()
	if err != nil {
		return err
	}
	for _, c := range chats {
		if c.ID == "" {
			continue
		}
		if c.ID == "status@broadcast" {
			continue
		}
		if c.Name != "" {
			_ = s.Store.MergeChatName(c.ID, c.Name, c.IsGroup)
		}
		_ = s.Store.MergeChatFlags(c.ID, c.Archived, c.Pinned, c.Muted)
		if c.Picture != "" {
			_ = s.Store.MergeChatAvatar(c.ID, c.Picture)
		}
	}
	if contacts, err := s.Client.ListAllContacts(); err == nil {
		byID := make(map[string]wahaContact, len(contacts))
		for _, ct := range contacts {
			if ct.ID != "" {
				byID[ct.ID] = ct
			}
		}
		for _, ch := range s.Store.ListChats() {
			if ch.Name != "" {
				continue
			}
			if ct, ok := byID[ch.JID]; ok {
				label := ct.Name
				if label == "" {
					label = ct.PushName
				}
				if label != "" {
					_ = s.Store.MergeChatName(ch.JID, label, ch.IsGroup)
				}
			}
		}
	}
	if historyPerChat > 0 {
		for _, ch := range s.Store.ListChats() {
			msgs, err := s.Client.GetChatMessages(ch.JID, historyPerChat)
			if err != nil {
				continue
			}
			for _, m := range msgs {
				if m.ID == "" {
					continue
				}
				chat := m.From
				if m.FromMe && m.To != "" {
					chat = m.To
				}
				if chat == "" {
					chat = ch.JID
				}
				ts := m.Timestamp
				if ts == 0 {
					ts = time.Now().Unix()
				}
				body := m.Body
				if body == "" {
					body = m.Caption
				}
				stored := Message{
					ID:      m.ID,
					ChatJID: chat,
					FromJID: m.From,
					FromMe:  m.FromMe,
					TS:      ts,
					Type:    normalizeType(m.Type),
					Body:    body,
					Ack:     m.Ack,
				}
				if m.HasMedia {
					stored.Media = &Media{MimeType: m.MimeType, Filename: m.Filename, Path: s.Store.localMediaRel(m.MediaURL)}
				}
				_ = s.Store.AppendMessage(stored)
			}
			if len(msgs) > 0 {
				m := msgs[0]
				prev := Message{ID: m.ID, ChatJID: ch.JID, TS: m.Timestamp, Type: normalizeType(m.Type), Body: m.Body, FromMe: m.FromMe}
				if m.HasMedia {
					prev.Media = &Media{MimeType: m.MimeType, Filename: m.Filename}
				}
				_ = s.Store.TouchChatWithMessage(prev)
			}
		}
	}
	s.syncChatFlagsFromGOWS()
	st := s.Store.State()
	s.Broadcaster.Send(WSEvent{Kind: "status", State: &st, TS: time.Now().Unix()})
	return nil
}

const gowsDBPath = "/var/lib/panel-whatsapp/sessions/gows/default/gows.db"

func (s *Service) syncChatFlagsFromGOWS() { s.syncChatNames() }

func (s *Service) syncChatNames() {
	st := s.Store.State()
	if st.Status != StatusWorking {
		return
	}
	snap := readGOWS(s.gowsDB())

	s.updateLidCache(snap.lidToPN)

	chats := s.Store.ListChats()

	for _, c := range chats {
		if !strings.HasSuffix(c.JID, "@lid") {
			continue
		}
		canon := snap.canonicalJID(c.JID)
		if canon == c.JID {
			continue
		}
		_ = s.Store.MergeJIDs(c.JID, canon)
	}

	chats = s.Store.ListChats()

	for _, c := range chats {
		if name, ok := snap.resolveName(c.JID); ok && name != "" {
			_ = s.Store.MergeChatName(c.JID, name, c.IsGroup)
		}
	}

	for jid, f := range snap.settings {
		canon := snap.canonicalJID(jid)
		_ = s.Store.MergeChatFlags(canon, f.archived, f.pinned, f.muted)
	}

	if s.Client != nil {
		if wahaChats, err := s.Client.ListChatsOverview(); err == nil {
			for _, wc := range wahaChats {
				if wc.ID == "" || wc.ID == "status@broadcast" {
					continue
				}
				if wc.Name == "" {
					continue
				}
				existing := s.Store.ListChats()
				canon := snap.canonicalJID(wc.ID)
				hasName := false
				for _, ec := range existing {
					if ec.JID == canon && ec.Name != "" {
						hasName = true
						break
					}
				}
				if !hasName {
					_ = s.Store.MergeChatName(canon, wc.Name, wc.IsGroup)
				}
				if wc.Picture != "" {
					_ = s.Store.MergeChatAvatar(canon, wc.Picture)
				}
			}
		}
	}
}

func (s *Service) pollOnce() {
	if s.Client == nil {
		return
	}
	sess, err := s.Client.GetSession()
	if err != nil {
		reachable := !isNetErr(err)
		_, _ = s.Store.SetState(func(st *State) {
			st.WAHAReachable = reachable
			if errors.Is(err, ErrSessionNotFound) {
				st.Status = StatusUnpaired
			}
		})
		return
	}
	st, _ := s.Store.SetState(func(st *State) {
		st.WAHAReachable = true
		st.Status = sess.Status
		if sess.Engine.Engine != "" {
			st.Engine = sess.Engine.Engine
		}
		if sess.Me.ID != "" {
			st.Phone = splitFirstAt(sess.Me.ID)
		}
		if sess.Me.PushName != "" {
			st.PushName = sess.Me.PushName
		}
		if sess.Status == StatusWorking {
			st.LastSyncTS = time.Now().Unix()
			st.QRDataURL = ""
		}
	})

	if sess.Status == StatusScanQR && st.QRDataURL == "" {
		if qr, err := s.Client.GetQR(); err == nil && qr != "" {
			st, _ = s.Store.SetState(func(st *State) {
				st.QRDataURL = qr
				st.LastQRTS = time.Now().Unix()
			})
		}
	}

	s.Broadcaster.Send(WSEvent{Kind: "status", State: &st, TS: time.Now().Unix()})
}

func isNetErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"connection refused", "no such host", "dial", "timeout", "EOF",
		"i/o timeout", "connect: connection",
	} {
		if containsFold(msg, marker) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			a := s[i+j]
			b := sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func splitFirstAt(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '@' {
			return s[:i]
		}
	}
	return s
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}
