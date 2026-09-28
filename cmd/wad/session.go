package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	sqlited "modernc.org/sqlite"
)

func init() {
	sql.Register("sqlite3", &sqlited.Driver{})
}

type session struct {
	user      string
	dbPath    string
	push      *pusher
	log       waLog.Logger
	container *sqlstore.Container

	mu     sync.RWMutex
	client *whatsmeow.Client
	qrCode string
	status string

	mediaMu     sync.Mutex
	msgs        map[string]*stashedMsg
	msgOrder    []string
	mediaStashN int
	lastIn      map[string]lastInbound
	unread      map[string][]types.MessageID
}

type stashedMsg struct {
	msg    *waE2E.Message
	sender string
	fromMe bool
}

type lastInbound struct {
	id     string
	sender string
}

func newSession(user, dbPath string, push *pusher) *session {
	return &session{
		user:   user,
		dbPath: dbPath,
		push:   push,
		log:    waLog.Stdout("wad:"+user, "INFO", true),
		status: "STARTING",
		msgs:   map[string]*stashedMsg{},
		lastIn: map[string]lastInbound{},
		unread: map[string][]types.MessageID{},
	}
}

func (s *session) connect(ctx context.Context) error {
	dsn := "file:" + s.dbPath + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	s.container = sqlstore.NewWithDB(db, "sqlite3", s.log)
	if err := s.container.Upgrade(ctx); err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	device, err := s.container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("device: %w", err)
	}

	cli := whatsmeow.NewClient(device, s.log)
	cli.AddEventHandler(s.handleEvent)
	s.mu.Lock()
	s.client = cli
	s.mu.Unlock()

	if device.ID == nil {
		s.setStatus("SCAN_QR_CODE")
		go s.pairLoop(ctx, cli)
		return nil
	}
	if err := cli.Connect(); err != nil {
		s.setStatus("FAILED")
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

func (s *session) pairLoop(ctx context.Context, cli *whatsmeow.Client) {
	for {
		qrChan, err := cli.GetQRChannel(ctx)
		if err != nil {
			s.log.Warnf("pair: GetQRChannel: %v", err)
			return
		}
		if err := cli.Connect(); err != nil {
			s.setStatus("FAILED")
			return
		}
		outcome := "closed"
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				s.mu.Lock()
				s.qrCode = evt.Code
				s.mu.Unlock()
				s.pushScanQR()
			case "success":
				s.mu.Lock()
				s.qrCode = ""
				s.mu.Unlock()
				outcome = "success"
			default:
				outcome = evt.Event
			}
		}
		if outcome == "success" || ctx.Err() != nil {
			return
		}
		s.log.Infof("pair: the QR channel closed (%s); regenerating a fresh QR", outcome)
		s.mu.Lock()
		s.qrCode = ""
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (s *session) pushScanQR() {
	s.mu.Lock()
	s.status = "SCAN_QR_CODE"
	s.mu.Unlock()
	s.push.sessionStatus(s.user, "SCAN_QR_CODE", s.selfPhone(), s.selfPushName())
}

func (s *session) setStatus(st string) {
	s.mu.Lock()
	changed := s.status != st
	s.status = st
	s.mu.Unlock()
	if changed {
		s.push.sessionStatus(s.user, st, s.selfPhone(), s.selfPushName())
	}
}

func (s *session) selfPhone() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client != nil && s.client.Store != nil && s.client.Store.ID != nil {
		return s.client.Store.ID.User
	}
	return ""
}

func (s *session) selfPushName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client != nil && s.client.Store != nil {
		return s.client.Store.PushName
	}
	return ""
}

func (s *session) snapshotStatus() (status, phone, pushName, qr string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status, s.selfPhoneLocked(), s.selfPushNameLocked(), s.qrCode
}

func (s *session) selfPhoneLocked() string {
	if s.client != nil && s.client.Store != nil && s.client.Store.ID != nil {
		return s.client.Store.ID.User
	}
	return ""
}
func (s *session) selfPushNameLocked() string {
	if s.client != nil && s.client.Store != nil {
		return s.client.Store.PushName
	}
	return ""
}

func normalizeJID(raw string) (types.JID, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasSuffix(raw, "@c.us") {
		raw = strings.TrimSuffix(raw, "@c.us") + "@s.whatsapp.net"
	}
	if !strings.Contains(raw, "@") {
		raw = raw + "@s.whatsapp.net"
	}
	return types.ParseJID(raw)
}

type sendReq struct {
	Type     string `json:"type"`
	ChatID   string `json:"chatId"`
	Text     string `json:"text"`
	Caption  string `json:"caption"`
	Filename string `json:"filename"`
	Mimetype string `json:"mimetype"`
	DataB64  string `json:"dataB64"`
	QuotedID string `json:"quotedId"`
}

func (s *session) send(ctx context.Context, req sendReq, data []byte) (string, error) {
	s.mu.RLock()
	cli := s.client
	s.mu.RUnlock()
	if cli == nil || !cli.IsConnected() {
		return "", fmt.Errorf("not connected")
	}
	to, err := normalizeJID(req.ChatID)
	if err != nil {
		return "", fmt.Errorf("invalid jid: %w", err)
	}

	ci := s.replyContext(req.QuotedID)
	var msg *waE2E.Message
	switch req.Type {
	case "", "text":
		if ci != nil {
			msg = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(req.Text), ContextInfo: ci}}
		} else {
			msg = &waE2E.Message{Conversation: proto.String(req.Text)}
		}
	case "image":
		up, err := cli.Upload(ctx, data, whatsmeow.MediaImage)
		if err != nil {
			return "", fmt.Errorf("upload: %w", err)
		}
		msg = &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, Mimetype: proto.String(orDefault(req.Mimetype, "image/jpeg")),
			Caption: strPtrOrNil(req.Caption), FileEncSHA256: up.FileEncSHA256,
			FileSHA256: up.FileSHA256, FileLength: proto.Uint64(uint64(len(data))), ContextInfo: ci,
		}}
	case "video":
		up, err := cli.Upload(ctx, data, whatsmeow.MediaVideo)
		if err != nil {
			return "", fmt.Errorf("upload: %w", err)
		}
		msg = &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, Mimetype: proto.String(orDefault(req.Mimetype, "video/mp4")),
			Caption: strPtrOrNil(req.Caption), FileEncSHA256: up.FileEncSHA256,
			FileSHA256: up.FileSHA256, FileLength: proto.Uint64(uint64(len(data))), ContextInfo: ci,
		}}
	case "document":
		up, err := cli.Upload(ctx, data, whatsmeow.MediaDocument)
		if err != nil {
			return "", fmt.Errorf("upload: %w", err)
		}
		msg = &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, Mimetype: proto.String(orDefault(req.Mimetype, "application/octet-stream")),
			FileName: strPtrOrNil(req.Filename), Caption: strPtrOrNil(req.Caption),
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(uint64(len(data))), ContextInfo: ci,
		}}
	case "voice":
		up, err := cli.Upload(ctx, data, whatsmeow.MediaAudio)
		if err != nil {
			return "", fmt.Errorf("upload: %w", err)
		}
		msg = &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath),
			MediaKey: up.MediaKey, Mimetype: proto.String(orDefault(req.Mimetype, "audio/ogg; codecs=opus")),
			PTT: proto.Bool(true), FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256,
			FileLength: proto.Uint64(uint64(len(data))), ContextInfo: ci,
		}}
	default:
		return "", fmt.Errorf("unsupported type: %s", req.Type)
	}

	resp, err := cli.SendMessage(ctx, to, msg)
	if err != nil {
		return "", fmt.Errorf("send: %w", err)
	}
	s.pushOwnSent(resp.ID, to, req, msg)
	return resp.ID, nil
}

func (s *session) pushOwnSent(id string, to types.JID, req sendReq, msg *waE2E.Message) {
	selfJID := ""
	if sid := s.selfStoreID(); sid != nil {
		selfJID = sid.ToNonAD().String()
	}
	chat := to.ToNonAD().String()
	p := wahaMsgOut{ID: id, FromMe: true, Timestamp: time.Now().Unix(), Ack: 1}
	if to.Server == types.GroupServer {
		p.From, p.To = chat, selfJID
	} else {
		p.From, p.To = selfJID, chat
	}
	p.Data.Info.Sender = selfJID
	fillMessageContent(&p, msg)
	s.stashMsg(id, msg, selfJID, true)
	if p.HasMedia {
		p.MediaURL = "/api/files/" + id
	}
	if req.Caption != "" && p.Body == "" {
		p.Body = req.Caption
	}
	p.QuotedID = req.QuotedID
	s.push.event(s.user, "message", p)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func strPtrOrNil(v string) *string {
	if v == "" {
		return nil
	}
	return proto.String(v)
}

func (s *session) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		s.setStatus("WORKING")
	case *events.Disconnected:
	case *events.StreamReplaced:
		s.log.Warnf("StreamReplaced: another client took over the device; retrying the connection in 15s")
		s.setStatus("FAILED")
		go func() {
			time.Sleep(15 * time.Second)
			if c := s.cli(); c != nil && !c.IsConnected() {
				if err := c.Connect(); err != nil {
					s.log.Errorf("reconnect after StreamReplaced failed: %v", err)
				}
			}
		}()
	case *events.LoggedOut:
		s.setStatus("SCAN_QR_CODE")
		if c := s.cli(); c != nil {
			go func() {
				c.Disconnect()
				s.pairLoop(context.Background(), c)
			}()
		}
	case *events.Message:
		s.onMessage(v)
	case *events.HistorySync:
		s.onHistorySync(v)
	case *events.Receipt:
		s.onReceipt(v)
	case *events.Presence:
		state := "available"
		if v.Unavailable {
			state = "unavailable"
		}
		var lastSeen int64
		if !v.LastSeen.IsZero() {
			lastSeen = v.LastSeen.Unix()
		}
		s.push.presence(s.user, v.From.ToNonAD().String(), "", state, lastSeen)
	case *events.ChatPresence:
		state := string(v.State)
		if v.State == types.ChatPresencePaused {
			state = "available"
		} else if v.Media == types.ChatPresenceMediaAudio {
			state = "recording"
		}
		chatJID := v.Chat.ToNonAD().String()
		participant := chatJID
		if !v.Sender.IsEmpty() {
			participant = v.Sender.ToNonAD().String()
		}
		s.push.presence(s.user, chatJID, participant, state, 0)
	}
}

func (s *session) onMessage(evt *events.Message) {
	info := evt.Info
	chat := info.Chat.ToNonAD().String()
	isGroup := info.Chat.Server == types.GroupServer
	sender := info.Sender.ToNonAD().String()
	selfJID := ""
	if id := s.selfStoreID(); id != nil {
		selfJID = id.ToNonAD().String()
	}
	m := evt.Message
	if m == nil {
		return
	}

	if rxn := m.GetReactionMessage(); rxn != nil && rxn.GetKey() != nil {
		s.push.reaction(s.user, chat, rxn.GetKey().GetID(), sender, rxn.GetText(), info.Timestamp.Unix())
		return
	}
	if pm := m.GetProtocolMessage(); pm != nil && pm.GetType() == waE2E.ProtocolMessage_REVOKE {
		if k := pm.GetKey(); k != nil && k.GetID() != "" {
			s.push.event(s.user, "message.revoked", ackOut{ID: k.GetID(), From: chat})
		}
		return
	}

	p := wahaMsgOut{
		ID:         info.ID,
		FromMe:     info.IsFromMe,
		Timestamp:  info.Timestamp.Unix(),
		NotifyName: info.PushName,
	}
	if isGroup {
		p.From = chat
		p.To = selfJID
	} else if info.IsFromMe {
		p.From = selfJID
		p.To = chat
	} else {
		p.From = chat
		p.To = selfJID
	}
	p.Data.Info.Sender = sender

	fillMessageContent(&p, m)
	if p.ID == "" || p.From == "" {
		return
	}
	s.stashMsg(info.ID, m, sender, info.IsFromMe)
	if !info.IsFromMe {
		s.mediaMu.Lock()
		s.lastIn[chat] = lastInbound{id: info.ID, sender: sender}
		ids := append(s.unread[chat], types.MessageID(info.ID))
		if len(ids) > 200 {
			ids = ids[len(ids)-200:]
		}
		s.unread[chat] = ids
		if len(s.lastIn) > 5000 {
			for k := range s.lastIn {
				delete(s.lastIn, k)
				delete(s.unread, k)
				if len(s.lastIn) <= 4000 {
					break
				}
			}
		}
		s.mediaMu.Unlock()
	}
	if evt.UnavailableRequestID != "" {
		if mime := mediaMimeOf(m); mime != "" {
			s.push.event(s.user, "media.recovered", mediaRecoveredOut{Chat: chat, ID: info.ID, Mimetype: mime})
			s.log.Infof("resend recovered media chat=%s msg=%s", chat, info.ID)
		}
		return
	}
	if p.HasMedia {
		p.MediaURL = "/api/files/" + info.ID
	}
	s.push.event(s.user, "message", p)
}

type mediaRecoveredOut struct {
	Chat     string `json:"chat"`
	ID       string `json:"id"`
	Mimetype string `json:"mimetype"`
}

func (s *session) onHistorySync(evt *events.HistorySync) {
	if evt == nil || evt.Data == nil {
		return
	}
	selfJID := ""
	if id := s.selfStoreID(); id != nil {
		selfJID = id.ToNonAD().String()
	}
	convs := evt.Data.GetConversations()
	s.log.Infof("history sync received: type=%s conversations=%d", evt.Data.GetSyncType().String(), len(convs))
	recovered := 0
	backfilled := 0
	for _, conv := range convs {
		chatJID := conv.GetID()
		isGroup := strings.HasSuffix(chatJID, "@g.us")
		for _, hmsg := range conv.GetMessages() {
			wmi := hmsg.GetMessage()
			if wmi == nil {
				continue
			}
			key := wmi.GetKey()
			m := wmi.GetMessage()
			if key == nil || key.GetID() == "" || m == nil {
				continue
			}
			fromMe := key.GetFromMe()
			sender := key.GetParticipant()
			if sender == "" {
				if fromMe {
					sender = selfJID
				} else {
					sender = chatJID
				}
			}
			s.stashMsg(key.GetID(), m, sender, fromMe)
			if mime := mediaMimeOf(m); mime != "" {
				recovered++
				s.push.event(s.user, "media.recovered", mediaRecoveredOut{
					Chat: chatJID, ID: key.GetID(), Mimetype: mime,
				})
			}
			p := wahaMsgOut{
				ID:         key.GetID(),
				FromMe:     fromMe,
				Timestamp:  int64(wmi.GetMessageTimestamp()),
				NotifyName: wmi.GetPushName(),
			}
			if isGroup {
				p.From, p.To = chatJID, selfJID
			} else if fromMe {
				p.From, p.To = selfJID, chatJID
			} else {
				p.From, p.To = chatJID, selfJID
			}
			p.Data.Info.Sender = sender
			fillMessageContent(&p, m)
			if p.From == "" || (!p.HasMedia && p.Body == "") {
				continue
			}
			if p.HasMedia {
				p.MediaURL = "/api/files/" + key.GetID()
			}
			s.push.event(s.user, "message", p)
			backfilled++
		}
	}
	if recovered > 0 {
		s.log.Infof("history sync: %d media item(s) with a recovered key", recovered)
	}
	if backfilled > 0 {
		s.log.Infof("history sync: %d message(s) re-emitted to backfill the Store", backfilled)
	}
}

func (s *session) stashMsg(id string, m *waE2E.Message, sender string, fromMe bool) {
	s.mediaMu.Lock()
	if _, ok := s.msgs[id]; ok {
		s.mediaMu.Unlock()
		return
	}
	s.msgs[id] = &stashedMsg{msg: m, sender: sender, fromMe: fromMe}
	s.msgOrder = append(s.msgOrder, id)
	for len(s.msgOrder) > 3000 {
		old := s.msgOrder[0]
		s.msgOrder = s.msgOrder[1:]
		delete(s.msgs, old)
	}
	prune := false
	if hasDownloadableMedia(m) {
		s.mediaStashN++
		prune = s.mediaStashN%pruneEvery == 0
	}
	s.mediaMu.Unlock()

	if hasDownloadableMedia(m) {
		s.persistStashedMedia(id, sender, fromMe, m)
		if prune {
			go s.pruneMediaStash()
		}
	}
}

func (s *session) getStashed(id string) *stashedMsg {
	s.mediaMu.Lock()
	sm := s.msgs[id]
	s.mediaMu.Unlock()
	if sm != nil {
		return sm
	}
	return s.loadPersistedStash(id)
}

func fillMessageContent(p *wahaMsgOut, m *waE2E.Message) {
	if m == nil {
		return
	}
	m = unwrapMsg(m)
	switch {
	case m.Conversation != nil && *m.Conversation != "":
		p.Type = "chat"
		p.Body = m.GetConversation()
	case m.ExtendedTextMessage != nil:
		p.Type = "chat"
		p.Body = m.ExtendedTextMessage.GetText()
		if ci := m.ExtendedTextMessage.GetContextInfo(); ci != nil {
			p.QuotedID = ci.GetStanzaID()
		}
	case m.ImageMessage != nil:
		p.Type = "image"
		p.HasMedia = true
		p.Caption = m.ImageMessage.GetCaption()
		p.MimeType = m.ImageMessage.GetMimetype()
		p.Body = p.Caption
	case m.VideoMessage != nil:
		p.Type = "video"
		p.HasMedia = true
		p.Caption = m.VideoMessage.GetCaption()
		p.MimeType = m.VideoMessage.GetMimetype()
		p.Body = p.Caption
	case m.AudioMessage != nil:
		if m.AudioMessage.GetPTT() {
			p.Type = "ptt"
		} else {
			p.Type = "audio"
		}
		p.HasMedia = true
		p.MimeType = m.AudioMessage.GetMimetype()
	case m.DocumentMessage != nil:
		p.Type = "document"
		p.HasMedia = true
		p.Caption = m.DocumentMessage.GetCaption()
		p.MimeType = m.DocumentMessage.GetMimetype()
		p.Filename = m.DocumentMessage.GetFileName()
		p.Body = p.Caption
	case m.StickerMessage != nil:
		p.Type = "sticker"
		p.HasMedia = true
		p.MimeType = m.StickerMessage.GetMimetype()
	default:
		p.Type = "chat"
	}
}

func (s *session) onReceipt(evt *events.Receipt) {
	var ack int
	switch evt.Type {
	case types.ReceiptTypeDelivered:
		ack = 2
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		ack = 3
	case types.ReceiptTypePlayed:
		ack = 4
	default:
		return
	}
	chat := evt.Chat.ToNonAD().String()
	for _, id := range evt.MessageIDs {
		s.push.event(s.user, "message.ack", ackOut{ID: id, From: chat, Ack: ack})
	}
}

func (s *session) selfStoreID() *types.JID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.client != nil && s.client.Store != nil {
		return s.client.Store.ID
	}
	return nil
}

func (s *session) close() {
	s.mu.RLock()
	cli := s.client
	s.mu.RUnlock()
	if cli != nil {
		cli.Disconnect()
	}
}

var _ = time.Second
