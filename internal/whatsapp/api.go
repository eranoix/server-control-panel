package whatsapp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// REST endpoints exposed via Service.RegisterHTTP. Caller (api.NewRouter)
// wires the public webhook + the protected ones separately so auth.Middleware
// is applied to everything except /api/whatsapp/webhook.

// (The dead RegisterPublic was removed — the public routes
// /api/whatsapp/webhook and /api/whatsapp/avatar/ are wired by the Manager
// through api.go: HandleWebhook and the avatar handler. RegisterPublic was
// still exported but had no callers, and the bare path
// "/api/whatsapp/webhook" without a trailing slash does not even match how
// Manager.HandleWebhook registers "/api/whatsapp/webhook/".)

// BuildProtectedMux lazily builds an *http.ServeMux carrying ALL of the
// Service's protected routes plus /ws/whatsapp. The Manager uses it to route
// authenticated requests to the right user's mux. Unlike RegisterProtected it
// does NOT touch an external mux — it returns a fresh one.
//
// Calling it more than once is safe (each call returns a new mux), but the
// Manager caches it behind a sync.Once in Service.protectedMux to avoid
// paying the cost per request.
func (s *Service) BuildProtectedMux(audit func(req *http.Request, action, target string)) http.Handler {
	mux := http.NewServeMux()
	s.RegisterProtected(mux, audit)
	return mux
}

// RegisterProtected attaches the authenticated endpoints. Audit callbacks
// (audit) are wired in by the router so we don't depend on the auth pkg here.
func (s *Service) RegisterProtected(mux *http.ServeMux, audit func(req *http.Request, action, target string)) {
	if audit == nil {
		audit = func(*http.Request, string, string) {}
	}
	mux.HandleFunc("/api/whatsapp/status", s.handleStatus)
	mux.HandleFunc("/api/whatsapp/start", s.wrapAudit(audit, "whatsapp.start", s.handleStart))
	mux.HandleFunc("/api/whatsapp/stop", s.wrapAudit(audit, "whatsapp.stop", s.handleStop))
	// session/stop only stops the WAHA session without taking the container
	// down (used by the "Cancel QR" button in the frontend; it used to bring
	// the whole systemd unit down and then required a restart).
	mux.HandleFunc("/api/whatsapp/session/stop", s.wrapAudit(audit, "whatsapp.session.stop", s.handleSessionStop))
	mux.HandleFunc("/api/whatsapp/restart", s.wrapAudit(audit, "whatsapp.restart", s.handleRestart))
	mux.HandleFunc("/api/whatsapp/logout", s.wrapAudit(audit, "whatsapp.logout", s.handleLogout))
	mux.HandleFunc("/api/whatsapp/qr/refresh", s.handleQRRefresh)
	mux.HandleFunc("/api/whatsapp/chats", s.handleChats)
	mux.HandleFunc("/api/whatsapp/chats/sync", s.handleChatsSync)
	mux.HandleFunc("/api/whatsapp/sync", s.handleFullSync)
	mux.HandleFunc("/api/whatsapp/messages/react", s.wrapAudit(audit, "whatsapp.react", s.handleReact))
	mux.HandleFunc("/api/whatsapp/messages/star", s.wrapAudit(audit, "whatsapp.star", s.handleStar))
	mux.HandleFunc("/api/whatsapp/messages/delete", s.wrapAudit(audit, "whatsapp.delete", s.handleDelete))
	mux.HandleFunc("/api/whatsapp/messages/forward", s.wrapAudit(audit, "whatsapp.forward", s.handleForward))
	mux.HandleFunc("/api/whatsapp/messages/edit", s.wrapAudit(audit, "whatsapp.edit", s.handleEditMessage))
	mux.HandleFunc("/api/whatsapp/messages/download/", s.handleMessageDownload)
	mux.HandleFunc("/api/whatsapp/chat/pin", s.wrapAudit(audit, "whatsapp.pin", s.handlePin))
	mux.HandleFunc("/api/whatsapp/chat/archive", s.wrapAudit(audit, "whatsapp.archive", s.handleArchive))
	mux.HandleFunc("/api/whatsapp/chat/mute", s.wrapAudit(audit, "whatsapp.mute", s.handleMute))
	mux.HandleFunc("/api/whatsapp/chat/block", s.wrapAudit(audit, "whatsapp.block", s.handleBlock))
	mux.HandleFunc("/api/whatsapp/check-number", s.handleCheckNumber)
	mux.HandleFunc("/api/whatsapp/group-info", s.handleGroupInfo)
	mux.HandleFunc("/api/whatsapp/chat/subscribe-presence", s.handleSubscribePresence)
	mux.HandleFunc("/api/whatsapp/chat/typing", s.handleTyping)
	// NOTE: /api/whatsapp/avatar/ moved to RegisterPublic — see comment there.
	mux.HandleFunc("/api/whatsapp/admin/wipe", s.wrapAudit(audit, "whatsapp.wipe", s.handleWipe))
	mux.HandleFunc("/api/whatsapp/admin/webhooks", s.handleAdminWebhooks)
	mux.HandleFunc("/api/whatsapp/status-updates", s.handleStatusUpdates)
	mux.HandleFunc("/api/whatsapp/admin/mark-all-read", s.wrapAudit(audit, "whatsapp.mark.all.read", s.handleMarkAllRead))
	mux.HandleFunc("/api/whatsapp/chats/", s.handleChatRoutes(audit))
	mux.HandleFunc("/api/whatsapp/media/", s.handleMedia)
	mux.HandleFunc("/ws/whatsapp", s.HandleWS)
}

// wrapAudit appends an audit-log entry after the wrapped handler returns
// successfully (HTTP 2xx). Failures don't generate audit noise.
func (s *Service) wrapAudit(audit func(*http.Request, string, string), action string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &auditRecorder{ResponseWriter: w, status: 200}
		h(rec, r)
		if rec.status < 300 {
			audit(r, action, "")
		}
	}
}

type auditRecorder struct {
	http.ResponseWriter
	status int
}

func (a *auditRecorder) WriteHeader(c int) {
	a.status = c
	a.ResponseWriter.WriteHeader(c)
}

// --- handlers ---

func (s *Service) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st := s.Store.State()
	st.WAHAReachable = s.SystemctlActive() && st.WAHAReachable
	writeJSONResp(w, st)
}

func (s *Service) handleStart(w http.ResponseWriter, _ *http.Request) {
	// whatsmeow backend: do NOT bring the WAHA container up (it would steal the
	// daemon's stream). Only (re)connect the session on the daemon via
	// StartSession (-> /reload).
	if _, isMeow := s.Client.(*meowClient); !isMeow {
		if err := s.SystemctlStart(); err != nil {
			writeErrResp(w, http.StatusInternalServerError, "systemctl start: "+err.Error())
			return
		}
		// Give the container a moment to bind before talking to it.
		time.Sleep(2 * time.Second)
	}
	if err := s.Client.StartSession(); err != nil {
		writeErrResp(w, http.StatusBadGateway, "start session: "+err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

func (s *Service) handleStop(w http.ResponseWriter, _ *http.Request) {
	if err := s.Client.StopSession(); err != nil {
		// Continue with container stop regardless.
	}
	if err := s.SystemctlStop(); err != nil {
		writeErrResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

func (s *Service) handleRestart(w http.ResponseWriter, _ *http.Request) {
	// whatsmeow backend: do NOT restart the WAHA container (it would steal the
	// daemon's stream). Reconnect the session on the daemon via RestartSession
	// (-> /reload).
	if _, isMeow := s.Client.(*meowClient); isMeow {
		if err := s.Client.RestartSession(); err != nil {
			writeErrResp(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSONResp(w, map[string]any{"ok": true})
		return
	}
	if err := s.SystemctlRestart(); err != nil {
		writeErrResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

// handleSessionStop stops only the WAHA session, leaving the container up.
// Cancelling QR pairing goes through this endpoint — it keeps WAHA running
// and ready for another /restart without needing a SystemctlStart afterwards.
func (s *Service) handleSessionStop(w http.ResponseWriter, _ *http.Request) {
	if err := s.Client.StopSession(); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

func (s *Service) handleLogout(w http.ResponseWriter, _ *http.Request) {
	if err := s.Client.LogoutSession(); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	_, _ = s.Store.SetState(func(st *State) {
		st.Status = StatusUnpaired
		st.Phone = ""
		st.PushName = ""
		st.QRDataURL = ""
	})
	writeJSONResp(w, map[string]any{"ok": true})
}

func (s *Service) handleQRRefresh(w http.ResponseWriter, _ *http.Request) {
	qr, err := s.Client.GetQR()
	if err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	if qr != "" {
		_, _ = s.Store.SetState(func(st *State) {
			st.QRDataURL = qr
			st.LastQRTS = time.Now().Unix()
		})
	}
	writeJSONResp(w, map[string]any{"qr": qr})
}

func (s *Service) handleChats(w http.ResponseWriter, _ *http.Request) {
	chats := s.Store.ListChats()
	writeJSONResp(w, map[string]any{"chats": chats})
}

// handleChatsSync forces an immediate pull of the chat list from WAHA to
// refresh names (for when the user has just saved a contact on their phone
// and does not want to wait for the 30s poll cycle). Idempotent, best effort.
func (s *Service) handleChatsSync(w http.ResponseWriter, _ *http.Request) {
	s.syncChatNames()
	writeJSONResp(w, map[string]any{"chats": s.Store.ListChats()})
}

// handleFullSync is the "Sync everything" action — a heavy pull that closes
// the whole gap between our store and WAHA without destroying anything.
// Unlike /chats/sync (names and flags only) and /admin/wipe (which erases and
// re-imports):
//  1. Re-syncs names and flags via syncChatNames (overview + the GOWS DB)
//  2. For each chat, backfillFromWAHA(jid, 50) — pulls the last 50 messages
//     and merges them into the store (deduped by ID in FindMessage).
//
// Synchronous — the frontend shows a spinner. For ~100 chats x 50 messages
// that is roughly 30-60s worst case (one WAHA round trip per chat). Returns
// per-chat counts.
func (s *Service) handleFullSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limitPerChat, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limitPerChat <= 0 {
		limitPerChat = 200
	}
	if limitPerChat > 2000 {
		limitPerChat = 2000 // ceiling: 10 WAHA pages per chat (safety)
	}
	// 1) Names and flags (the same source the regular poll uses). This step also
	// populates the lidCache (@lid -> @c.us) from the GOWS SQLite.
	s.syncChatNames()

	// 2) Consolidate @lid shadow chats into @c.us. This runs before the backfill,
	// because the backfill now writes everything under @c.us — without
	// consolidating first, older messages from when the webhook wrote to @lid
	// would be left orphaned. MergeJIDs moves both the chat record and the
	// messages on disk.
	chatsMerged := s.consolidateLIDChats()

	// 3) Recent history per chat — now PARALLELISED. Sequentially this was
	// ~150ms x 400 chats = 60s of blocking; with a pool of 6 it drops to
	// ~10-15s. The cap is deliberately conservative so it does not saturate
	// WAHA Core (the GOWS engine is single-process, and high concurrency
	// degrades throughput).
	chats := s.Store.ListChats()
	var (
		totalNew     int64
		chatsScanned int64
		chatsFailed  int64
		wg           sync.WaitGroup
		sem          = make(chan struct{}, 6) // pool size
	)
	for _, c := range chats {
		if c.JID == "" || c.JID == "status@broadcast" {
			continue
		}
		jid := c.JID
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			added := s.backfillFromWAHACounted(jid, limitPerChat)
			if added < 0 {
				atomic.AddInt64(&chatsFailed, 1)
				return
			}
			atomic.AddInt64(&chatsScanned, 1)
			atomic.AddInt64(&totalNew, int64(added))
		}()
	}
	wg.Wait()

	// 2d) Close the gap with an ON-DEMAND history sync (whatsmeow backend):
	// the backfill above is a no-op on the daemon (GetChatMessages* is a stub),
	// so we ask WhatsApp for the REAL history of the most recent chats — where
	// the user's conversations and the gaps land (a window of webhook 401s, for
	// instance). Capped and debounced (requestHistoryGap) so it does not trip
	// the primary device's anti-spam. Asynchronous: the missing messages arrive
	// via HistorySync -> broadcast.
	cand := make([]*Chat, 0, len(chats))
	for _, c := range chats {
		if c.JID != "" && c.JID != "status@broadcast" {
			cand = append(cand, c)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].LastMsgTS > cand[j].LastMsgTS })
	const maxHistChats = 40
	for i := 0; i < len(cand) && i < maxHistChats; i++ {
		s.requestHistoryGap(cand[i].JID, 100)
	}

	// 3) Broadcast the updated state so the UI re-renders the list (avatars + order).
	st := s.Store.State()
	st.LastSyncTS = time.Now().Unix()
	_, _ = s.Store.SetState(func(state *State) { state.LastSyncTS = st.LastSyncTS })
	s.Broadcaster.Send(WSEvent{Kind: "status", State: &st, TS: time.Now().Unix()})

	writeJSONResp(w, map[string]any{
		"ok":             true,
		"chats_scanned":  atomic.LoadInt64(&chatsScanned),
		"chats_failed":   atomic.LoadInt64(&chatsFailed),
		"messages_added": atomic.LoadInt64(&totalNew),
		"chats_merged":   chatsMerged,
		"limit_per_chat": limitPerChat,
	})
}

// consolidateLIDChats walks the lidCache (@lid -> @c.us) and, for each pair,
// merges the @lid chat into the @c.us one. It covers two cases:
//
//  1. The @lid still exists as a chat record in chats.json -> Store.MergeJIDs
//     absorbs it (moves the messages on disk, merges the chat fields, drops
//     the source).
//  2. The @lid is NO LONGER in chats.json (an earlier sync removed the record
//     but new messages kept arriving through the webhook under @lid before
//     the lidCache was populated) -> MergeJIDs is a no-op, but calling
//     mergeMessageDirs directly migrates the orphaned messages off disk anyway.
//
// Without case (2), 1929 messages sat stranded across 73 orphaned directories
// on a user's system. Returns the number of directories actually migrated.
func (s *Service) consolidateLIDChats() int {
	s.lidMu.RLock()
	pairs := make(map[string]string, len(s.lidCache))
	for k, v := range s.lidCache {
		if k != "" && v != "" && k != v {
			pairs[k] = v
		}
	}
	s.lidMu.RUnlock()
	merged := 0
	for src, dst := range pairs {
		// (1) Try MergeJIDs first — if the chat record exists, it merges.
		_ = s.Store.MergeJIDs(src, dst)
		// (2) Always try to move the messages on disk, even when MergeJIDs was a
		// no-op. mergeMessageDirs early-returns when srcDir does not exist, so it
		// is safe to call for every pair (idempotent).
		srcDir := filepath.Join(s.Store.Root, "messages", chatDir(src))
		if _, err := os.Stat(srcDir); err == nil {
			// The per-chat-locked version. Raw mergeMessageDirs raced against a
			// concurrent AppendMessage on the source or destination.
			s.Store.MergeMessageDirs(src, dst)
			merged++
		}
	}
	return merged
}

// --- WhatsApp-like operations on messages: react, star, delete, forward ---

type msgOpBody struct {
	ChatJID   string `json:"chat_jid"`
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji,omitempty"`
	Star      bool   `json:"star,omitempty"`
	Mode      string `json:"mode,omitempty"` // "me" or "everyone"
	DestJID   string `json:"dest_jid,omitempty"`
}

func decodeMsgOp(r *http.Request) (*msgOpBody, error) {
	var b msgOpBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&b); err != nil {
		return nil, err
	}
	if b.MessageID == "" {
		return nil, errors.New("missing message_id")
	}
	return &b, nil
}

func (s *Service) handleReact(w http.ResponseWriter, r *http.Request) {
	b, err := decodeMsgOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	// The authoritative sender from the store (the author of the target
	// message). The whatsmeow backend needs it to react in a GROUP
	// (key.Participant); without this it depended on the daemon's in-memory
	// stash, which is wiped on every restart.
	sender := ""
	if m, _ := s.Store.FindMessage(b.ChatJID, b.MessageID); m != nil {
		sender = m.FromJID
	}
	if err := s.Client.React(b.ChatJID, b.MessageID, b.Emoji, sender); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

func (s *Service) handleStar(w http.ResponseWriter, r *http.Request) {
	b, err := decodeMsgOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Client.StarMessage(b.ChatJID, b.MessageID, b.Star); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	b, err := decodeMsgOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if b.ChatJID == "" {
		writeErrResp(w, http.StatusBadRequest, "missing chat_jid")
		return
	}
	if b.Mode == "" {
		b.Mode = "everyone"
	}
	if err := s.Client.DeleteMessage(b.ChatJID, b.MessageID, b.Mode); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	// Local mark too (covers the "for me" case + speeds up UI for "everyone").
	_ = s.Store.MarkDeleted(b.ChatJID, b.MessageID)
	writeJSONResp(w, map[string]any{"ok": true})
}

func (s *Service) handleForward(w http.ResponseWriter, r *http.Request) {
	b, err := decodeMsgOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if b.DestJID == "" {
		writeErrResp(w, http.StatusBadRequest, "missing dest_jid")
		return
	}
	id, err := s.Client.ForwardMessage(b.MessageID, b.DestJID)
	if err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"id": id})
}

// --- Operations on chats: pin, archive ---

type chatOpBody struct {
	ChatJID string `json:"chat_jid"`
	Value   bool   `json:"value"`
}

func decodeChatOp(r *http.Request) (*chatOpBody, error) {
	var b chatOpBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&b); err != nil {
		return nil, err
	}
	if b.ChatJID == "" {
		return nil, errors.New("missing chat_jid")
	}
	return &b, nil
}

func (s *Service) handlePin(w http.ResponseWriter, r *http.Request) {
	b, err := decodeChatOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Client.PinChat(b.ChatJID, b.Value); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	// Reflect it locally — the periodic sync catches up later.
	cs := s.Store.ListChats()
	for _, c := range cs {
		if c.JID == b.ChatJID {
			c.Pinned = b.Value
			_ = s.Store.UpsertChat(*c)
			break
		}
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

// handleMute mutes and unmutes a chat. Only the whatsmeow backend supports
// it; WAHA degrades with a clear error (502). Reflects Chat.Muted locally.
func (s *Service) handleMute(w http.ResponseWriter, r *http.Request) {
	b, err := decodeChatOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Client.MuteChat(b.ChatJID, b.Value); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	// Reflect it locally — the periodic sync catches up later.
	cs := s.Store.ListChats()
	for _, c := range cs {
		if c.JID == b.ChatJID {
			c.Muted = b.Value
			_ = s.Store.UpsertChat(*c)
			break
		}
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

// handleBlock blocks and unblocks a contact. There is no dedicated store
// field; it just propagates to the backend and returns ok.
func (s *Service) handleBlock(w http.ResponseWriter, r *http.Request) {
	b, err := decodeChatOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Client.BlockContact(b.ChatJID, b.Value); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

// handleEditMessage edits the text or caption of an already-sent message
// (WhatsApp's 15min window). Only whatsmeow supports it. It reflects the new
// Body in the store and re-broadcasts the message so the UI updates without
// a refetch.
func (s *Service) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChatJID string `json:"chat_jid"`
		MsgID   string `json:"msg_id"`
		Text    string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.ChatJID == "" || body.MsgID == "" {
		writeErrResp(w, http.StatusBadRequest, "missing chat_jid or msg_id")
		return
	}
	if err := s.Client.EditMessage(body.ChatJID, body.MsgID, body.Text); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	// Reflect it locally: update the stored Body and re-broadcast the message.
	_ = s.Store.UpdateBody(body.ChatJID, body.MsgID, body.Text)
	if m, _ := s.Store.FindMessage(body.ChatJID, body.MsgID); m != nil {
		s.Broadcaster.Send(WSEvent{Kind: "message", Message: m, TS: time.Now().Unix()})
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

// handleCheckNumber asks whether a phone number is on WhatsApp and returns
// the canonical JID. Query: ?phone=<digits>. Only whatsmeow supports it.
func (s *Service) handleCheckNumber(w http.ResponseWriter, r *http.Request) {
	phone := r.URL.Query().Get("phone")
	if phone == "" {
		writeErrResp(w, http.StatusBadRequest, "missing phone")
		return
	}
	jid, onWA, err := s.Client.CheckNumber(phone)
	if err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"jid": jid, "on_wa": onWA})
}

// handleGroupInfo returns a group's name and participants. Query: ?jid=<jid>.
// Only whatsmeow supports it.
func (s *Service) handleGroupInfo(w http.ResponseWriter, r *http.Request) {
	jid := r.URL.Query().Get("jid")
	if jid == "" {
		writeErrResp(w, http.StatusBadRequest, "missing jid")
		return
	}
	name, participants, err := s.Client.GroupInfo(jid)
	if err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	if participants == nil {
		participants = []WAGroupParticipant{}
	}
	writeJSONResp(w, map[string]any{"name": name, "participants": participants})
}

// handleSubscribePresence asks WAHA to start delivering presence events for a
// chat. Idempotent.
func (s *Service) handleSubscribePresence(w http.ResponseWriter, r *http.Request) {
	b, err := decodeChatOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Client.SubscribePresence(b.ChatJID); err != nil {
		// Not fatal: WAHA may not support it for this chat. Just record it.
		writeJSONResp(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

// handleTyping reports our presence (composing/paused) to the chat — the
// contact sees "X is typing..." on their own WhatsApp.
func (s *Service) handleTyping(w http.ResponseWriter, r *http.Request) {
	b, err := decodeChatOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = s.Client.SendTyping(b.ChatJID, b.Value)
	writeJSONResp(w, map[string]any{"ok": true})
}

// handleAdminWebhooks exposes the current state of the WAHA session's webhook
// config. It diagnoses multi-instance setups (v1+v2) where only one instance
// receives events in real time. It shows:
//   - configured_url: the URL THIS instance should be registered under to receive events
//   - registered: whether that URL appears in config.webhooks
//   - webhooks: the full list of registered URLs (the global env var shows up
//     as absent here — only per-session entries are visible through the API)
//
// When registered=false and configured_url!="", the user can call
// /api/whatsapp/admin/webhooks?reregister=1 (POST) to force a re-register
// without waiting for the 2min tick.
func (s *Service) handleAdminWebhooks(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"configured_url":    s.ExtraWebhookURL,
		"configured_events": s.ExtraWebhookEvents,
		"registered":        false,
		"webhooks":          []any{},
	}
	// WAHA-specific diagnostics (lists the webhooks registered on the WAHA
	// session). On the whatsmeow backend the daemon pushes directly, so this
	// panel does not apply.
	if waha, ok := s.Client.(*Client); ok {
		// Reuses the sessionWithConfig struct from client.go (exported within the package).
		var sess sessionWithConfig
		if err := waha.do("GET", "/api/sessions/"+waha.SessionID, nil, &sess); err == nil {
			whList := make([]map[string]any, 0, len(sess.Config.Webhooks))
			registered := false
			for _, wh := range sess.Config.Webhooks {
				whList = append(whList, map[string]any{
					"url":    wh.URL,
					"events": wh.Events,
				})
				if wh.URL == s.ExtraWebhookURL {
					registered = true
				}
			}
			resp["webhooks"] = whList
			resp["registered"] = registered
			resp["session_status"] = sess.Status
		} else {
			resp["error"] = err.Error()
		}
	}
	// Force a re-register on POST?reregister=1
	if r.Method == http.MethodPost && r.URL.Query().Get("reregister") == "1" {
		s.ensureExtraWebhook()
		// re-check
		if waha, ok := s.Client.(*Client); ok {
			var sess sessionWithConfig
			if err := waha.do("GET", "/api/sessions/"+waha.SessionID, nil, &sess); err == nil {
				for _, wh := range sess.Config.Webhooks {
					if wh.URL == s.ExtraWebhookURL {
						resp["registered"] = true
						break
					}
				}
			}
		}
		resp["reregistered"] = true
	}
	writeJSONResp(w, resp)
}

// handleStatusUpdates returns WhatsApp's Status (Stories) feed. Messages from
// the special JID "status@broadcast" are pulled straight from WAHA and
// grouped by sender. Returns {by_sender: [{jid, name, items:[...]}]}.
func (s *Service) handleStatusUpdates(w http.ResponseWriter, _ *http.Request) {
	if s.Client == nil {
		writeJSONResp(w, map[string]any{"by_sender": []any{}})
		return
	}
	msgs, err := s.Client.GetChatMessages("status@broadcast", 200)
	if err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	type item struct {
		ID    string `json:"id"`
		TS    int64  `json:"ts"`
		Type  string `json:"type"`
		Body  string `json:"body"`
		Media *Media `json:"media,omitempty"`
	}
	type sender struct {
		JID   string `json:"jid"`
		Name  string `json:"name"`
		Items []item `json:"items"`
	}
	bySender := map[string]*sender{}
	for _, m := range msgs {
		// Under status@broadcast, the individual "from" is each sender.
		sjid := normalizeJID(m.From)
		if sjid == "" || sjid == "status@broadcast" {
			continue
		}
		sd, ok := bySender[sjid]
		if !ok {
			label := ""
			if ch, ok2 := s.Store.lookupName(sjid); ok2 {
				label = ch
			}
			sd = &sender{JID: sjid, Name: label}
			bySender[sjid] = sd
		}
		it := item{ID: m.ID, TS: m.Timestamp, Type: normalizeType(m.Type), Body: m.Body}
		if m.HasMedia {
			it.Media = &Media{MimeType: m.MimeType, Filename: m.Filename, Path: s.Store.localMediaRel(m.MediaURL)}
		}
		sd.Items = append(sd.Items, it)
	}
	out := make([]*sender, 0, len(bySender))
	for _, s := range bySender {
		out = append(out, s)
	}
	writeJSONResp(w, map[string]any{"by_sender": out})
}

// handleMarkAllRead zeroes the local unread_count on ALL chats and propagates
// it to WhatsApp via /api/sendSeen for every chat with unread > 0. Idempotent.
// The sendSeen calls run in sequence (with a short pause between them) so
// they do not blow WAHA's rate limit — for large inboxes this can take a few
// seconds.
//
// Optional body: {"local_only": true} -> skips WAHA and only zeroes our own
// store (useful to clear the UI without marking anything as seen on the phone).
func (s *Service) handleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LocalOnly bool `json:"local_only"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body)
	chats := s.Store.ListChats()
	marked := 0
	for _, c := range chats {
		if c.UnreadCount <= 0 {
			continue
		}
		marked++
		if !body.LocalOnly && s.Client != nil {
			// Best effort: an error on one chat does not stop the others.
			_ = s.Client.MarkChatRead(c.JID)
		}
		_ = s.Store.MarkChatRead(c.JID)
	}
	writeJSONResp(w, map[string]any{"ok": true, "marked": marked, "local_only": body.LocalOnly})
}

// handleWipe erases the local record (chats.json + messages/) and kicks off a
// deep re-import through WAHA. The session and the media are preserved.
// Optional body: {"history_per_chat": 100}. Defaults to 50.
func (s *Service) handleWipe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HistoryPerChat int `json:"history_per_chat"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body)
	if body.HistoryPerChat == 0 {
		body.HistoryPerChat = 50
	}
	if err := s.DeepImport(body.HistoryPerChat); err != nil {
		writeErrResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONResp(w, map[string]any{"ok": true, "chats": len(s.Store.ListChats())})
}

func (s *Service) handleArchive(w http.ResponseWriter, r *http.Request) {
	b, err := decodeChatOp(r)
	if err != nil {
		writeErrResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Client.ArchiveChat(b.ChatJID, b.Value); err != nil {
		writeErrResp(w, http.StatusBadGateway, err.Error())
		return
	}
	cs := s.Store.ListChats()
	for _, c := range cs {
		if c.JID == b.ChatJID {
			c.Archived = b.Value
			_ = s.Store.UpsertChat(*c)
			break
		}
	}
	writeJSONResp(w, map[string]any{"ok": true})
}

// --- helpers ---

func writeJSONResp(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErrResp(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
