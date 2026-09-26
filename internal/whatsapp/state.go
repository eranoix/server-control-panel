package whatsapp

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

// startPoller runs a background goroutine that polls WAHA's session endpoint
// at pollInterval. It serves three purposes:
//
//  1. Reconcile state when webhooks are lost (e.g. WAHA restart, network
//     hiccup) — the next poll catches up.
//  2. Detect WAHA being unreachable (container crash) and flag the UI.
//  3. Sync chat display names from WAHA's address book so the sidebar shows
//     "Maria" instead of "5511999998888". Names only change when the user
//     adds/edits contacts on the phone — 30s poll is overkill but cheap.
//
// Webhooks remain the primary signal; this is the safety net.
func (s *Service) startPoller(ctx context.Context) {
	go func() {
		// Initial poll right away so the UI has fresh data on boot.
		s.pollOnce()
		s.syncChatNames()         // initial name pull right after first status
		s.syncChatFlagsFromGOWS() // archived/pinned/muted from the internal SQLite
		// Clear orphaned @lid chats right at boot — any mapping gows.db knows
		// about is resolved here. Without this, orphans stuck around until the
		// user pressed Sync.
		_ = s.consolidateLIDChats()
		// Register ExtraWebhookURL (when set) in WAHA's per-session config.
		// Idempotent: a no-op if it is already registered. Critical in a v1+v2
		// setup sharing containers — without it the v2 never receives a webhook in
		// real time (the WHATSAPP_HOOK_URL env var points only at the v1).
		s.ensureExtraWebhook()
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		// A slower ticker to re-check the webhook config (in case WAHA reset or
		// the session was recreated).
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

// ensureExtraWebhook is the logged, safe-guarded wrapper around
// Client.EnsureExtraWebhook. No ExtraWebhookURL = no-op.
func (s *Service) ensureExtraWebhook() {
	if s == nil || s.Client == nil || s.ExtraWebhookURL == "" {
		return
	}
	// It is only worth trying to register when the session exists and is in a
	// stable state (FAILED/STOPPED do accept a config update, but if the session
	// was never created the POST /api/sessions/ would recreate it — out of scope).
	sess, err := s.Client.GetSession()
	if err != nil || sess == nil {
		return
	}
	if err := s.Client.EnsureExtraWebhook(s.ExtraWebhookURL, s.hmacAtual(), s.ExtraWebhookEvents); err != nil {
		log.Printf("whatsapp: ensure extra webhook %s: %v", s.ExtraWebhookURL, err)
	}
}

// DeepImport rebuilds the local store from scratch out of WAHA: chats with
// their names (overview + contacts/all) plus the N most recent messages of
// each chat. Used by the /api/whatsapp/admin/wipe endpoint when the user
// wants to re-import.
//
// historyPerChat=0 turns the history pull off (names only). Typical values
// are 50-200 — enough to scroll up in the UI without calling /messages.
func (s *Service) DeepImport(historyPerChat int) error {
	if s.Client == nil {
		return nil
	}
	if err := s.Store.WipeImport(); err != nil {
		return err
	}
	// 1) Chat overview with names
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
		// WAHA's overview already returns the picture URL (WhatsApp's CDN).
		// Persisting it in the store now avoids firing
		// /api/contacts/profile-picture for every contact later (which returned
		// 429 when the panel opened).
		if c.Picture != "" {
			_ = s.Store.MergeChatAvatar(c.ID, c.Picture)
		}
	}
	// 2) Contacts/all (resolves @lid via pushname for chats with no name)
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
	// 3) Message history per chat (top N — so the UI has something to scroll)
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
			// Update the chat preview with the most recent message (msgs[0]).
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
	// Sync the archived/pinned/muted flags from Whatsmeow's SQLite.
	s.syncChatFlagsFromGOWS()
	// Broadcast so the UI refreshes.
	st := s.Store.State()
	s.Broadcaster.Send(WSEvent{Kind: "status", State: &st, TS: time.Now().Unix()})
	return nil
}

// gowsDBPath is Whatsmeow's internal SQLite (the GOWS engine), where the chat
// flags (archived/pinned/muted) live — information WAHA Core does not expose
// over REST. We read it read-only and merge it into the Store.
const gowsDBPath = "/var/lib/vpsm-whatsapp/sessions/gows/default/gows.db"

// syncChatFlagsFromGOWS is a backwards-compatible alias for syncChatNames —
// all of the SQLite reading now lives there (flags included). Kept so
// external references do not break.
func (s *Service) syncChatFlagsFromGOWS() { s.syncChatNames() }

// syncChatNames uses Whatsmeow's internal SQLite (the GOWS engine) as the
// source of truth for contact names, group names, the @lid -> phone mapping,
// the archived/pinned/muted flags, AND deduplication of duplicate chats.
//
// Why SQLite and not WAHA's REST API?
//
//   - WAHA Core's /chats/overview returns only id/name/picture/lastMessage —
//     no flags, and incomplete names for @lid entries.
//   - /contacts/all returns 1300+ entries, but the address book `name` is
//     empty on almost all of them (only `pushname` is populated).
//   - Whatsmeow keeps everything in SQLite: whatsmeow_contacts (full_name +
//     first_name + business_name + push_name), gows_groups (the group name),
//     whatsmeow_lid_map (3500+ @lid->phone mappings), whatsmeow_chat_settings
//     (archived/pinned/muted). All local. All free.
//
// Order of execution:
//  1. Read a full snapshot from SQLite
//  2. For each chat in the Store, resolve its name from the snapshot
//  3. If a @lid maps to a @c.us that already exists in the Store, MERGE
//     (absorb it)
//  4. Apply the flags (archived/pinned/muted)
//  5. As a last-resort fallback, still fetch WAHA's chat overview to pick up
//     any contact or group SQLite did not know about
//
// A no-op when the session is not WORKING.
func (s *Service) syncChatNames() {
	st := s.Store.State()
	if st.Status != StatusWorking {
		return
	}
	snap := readGOWS(s.gowsDB())

	// Refresh the in-memory @lid -> @c.us cache so the webhook converts JIDs
	// immediately as messages arrive, without waiting 30s.
	s.updateLidCache(snap.lidToPN)

	// The current list of chats in the Store.
	chats := s.Store.ListChats()

	// Step 1: dedup — if any @lid maps onto an existing @c.us, merge it.
	// This is what removes the repeated contacts the user complained about.
	for _, c := range chats {
		if !strings.HasSuffix(c.JID, "@lid") {
			continue
		}
		canon := snap.canonicalJID(c.JID)
		if canon == c.JID {
			continue // no mapping available
		}
		// canon = the matching @c.us. If it also exists in the store, merge.
		// If it does not, rename (MergeJIDs covers both cases).
		_ = s.Store.MergeJIDs(c.JID, canon)
	}

	// Re-read after the merge — the list may now be shorter and hold new JIDs.
	chats = s.Store.ListChats()

	// Step 2: resolve the name for each chat.
	for _, c := range chats {
		if name, ok := snap.resolveName(c.JID); ok && name != "" {
			_ = s.Store.MergeChatName(c.JID, name, c.IsGroup)
		}
	}

	// Step 3: apply the flags (archived/pinned/muted).
	for jid, f := range snap.settings {
		// Settings may carry @lid as well — normalise to the canonical form the
		// Store knows.
		canon := snap.canonicalJID(jid)
		_ = s.Store.MergeChatFlags(canon, f.archived, f.pinned, f.muted)
	}

	// Step 4: fall back to WAHA's chat overview — this covers chats SQLite did
	// not know about (rarer, but it happens for new conversations GOWS has not
	// written yet). It only fills in what is missing; it never overwrites.
	if s.Client != nil {
		if wahaChats, err := s.Client.ListChatsOverview(); err == nil {
			for _, wc := range wahaChats {
				if wc.ID == "" || wc.ID == "status@broadcast" {
					continue
				}
				if wc.Name == "" {
					continue
				}
				// Only fill in when the chat still has no name — never overwrite a name
				// resolved from SQLite, which is more reliable.
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
				// The avatar from the overview (a WhatsApp CDN URL). This covers ALL
				// chats, not just new ones, so it updates when a contact changes their
				// picture. MergeChatAvatar is a no-op when the URL is unchanged.
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
		// Don't broadcast on every miss to avoid noise; the UI's own pill
		// reflects the periodic /api/whatsapp/status fetch.
		return
	}
	st, _ := s.Store.SetState(func(st *State) {
		st.WAHAReachable = true
		st.Status = sess.Status
		if sess.Engine.Engine != "" {
			st.Engine = sess.Engine.Engine
		}
		if sess.Me.ID != "" {
			// "55119...@c.us" → "55119..."
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

	// When in SCAN_QR_CODE and we don't have a fresh QR cached, fetch one.
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

// isNetErr returns true when the error looks like a transport failure
// (connection refused, timeout) rather than an HTTP-status error from WAHA.
// We use this to distinguish "WAHA is reachable but the session doesn't
// exist" from "WAHA container is down".
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
	// case-insensitive substring; tiny so we don't import strings just for this
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

// init-time log so service startup is visible in journalctl.
func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}
