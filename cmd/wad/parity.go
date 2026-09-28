package main

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func unwrapMsg(m *waE2E.Message) *waE2E.Message {
	if m == nil {
		return nil
	}
	if inner := m.GetViewOnceMessage().GetMessage(); inner != nil {
		return inner
	}
	if inner := m.GetViewOnceMessageV2().GetMessage(); inner != nil {
		return inner
	}
	if inner := m.GetViewOnceMessageV2Extension().GetMessage(); inner != nil {
		return inner
	}
	return m
}

func (s *session) replyContext(quotedID string) *waE2E.ContextInfo {
	if quotedID == "" {
		return nil
	}
	ci := &waE2E.ContextInfo{StanzaID: proto.String(quotedID)}
	if sm := s.getStashed(quotedID); sm != nil {
		ci.QuotedMessage = sm.msg
		if sm.sender != "" {
			if jid, err := normalizeJID(sm.sender); err == nil {
				ci.Participant = proto.String(jid.String())
			}
		}
	}
	return ci
}

type contactOut struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PushName string `json:"pushname"`
}

func (s *session) contacts(ctx context.Context) ([]contactOut, error) {
	cli := s.cli()
	if cli == nil {
		return nil, fmt.Errorf("not connected")
	}
	all, err := cli.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]contactOut, 0, len(all))
	for jid, ci := range all {
		name := ci.FullName
		if name == "" {
			name = ci.FirstName
		}
		out = append(out, contactOut{ID: jid.ToNonAD().String(), Name: name, PushName: ci.PushName})
	}
	return out, nil
}

func (s *session) cli() *whatsmeow.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

func (s *session) downloadMedia(ctx context.Context, msgID string) ([]byte, string, error) {
	sm := s.getStashed(msgID)
	if sm == nil || sm.msg == nil {
		return nil, "", fmt.Errorf("media not found (id=%s)", msgID)
	}
	m := unwrapMsg(sm.msg)
	cli := s.cli()
	if cli == nil {
		return nil, "", fmt.Errorf("not connected")
	}
	var dl whatsmeow.DownloadableMessage
	var mime string
	switch {
	case m.ImageMessage != nil:
		dl, mime = m.ImageMessage, m.ImageMessage.GetMimetype()
	case m.VideoMessage != nil:
		dl, mime = m.VideoMessage, m.VideoMessage.GetMimetype()
	case m.AudioMessage != nil:
		dl, mime = m.AudioMessage, m.AudioMessage.GetMimetype()
	case m.DocumentMessage != nil:
		dl, mime = m.DocumentMessage, m.DocumentMessage.GetMimetype()
	case m.StickerMessage != nil:
		dl, mime = m.StickerMessage, m.StickerMessage.GetMimetype()
	default:
		return nil, "", fmt.Errorf("no downloadable media")
	}
	data, err := cli.Download(ctx, dl)
	if err != nil {
		return nil, "", err
	}
	return data, mime, nil
}

type actionReq struct {
	Action string `json:"action"`
	ChatID string `json:"chatId"`
	MsgID  string `json:"msgId"`
	Emoji  string `json:"emoji"`
	Typing bool   `json:"typing"`
	Mode   string `json:"mode"`
	On     bool   `json:"on"`
	Text   string `json:"text"`
	Sender string `json:"sender"`
	TS     int64  `json:"ts"`
	Count  int    `json:"count"`
}

func (s *session) action(ctx context.Context, r actionReq) error {
	cli := s.cli()
	if cli == nil || !cli.IsConnected() {
		return fmt.Errorf("not connected")
	}
	if r.Action == "logout" {
		return cli.Logout(ctx)
	}
	chat, err := normalizeJID(r.ChatID)
	if err != nil {
		return err
	}
	self := types.JID{}
	if id := s.selfStoreID(); id != nil {
		self = *id
	}
	switch r.Action {
	case "react":
		sender := chat
		if r.Sender != "" {
			if sj, err := normalizeJID(r.Sender); err == nil {
				sender = sj
			}
		} else if sm := s.getStashed(r.MsgID); sm != nil && sm.sender != "" {
			if sj, err := normalizeJID(sm.sender); err == nil {
				sender = sj
			}
		}
		msg := cli.BuildReaction(chat, sender, r.MsgID, r.Emoji)
		if _, err := cli.SendMessage(ctx, chat, msg); err != nil {
			return err
		}
		if self.User != "" {
			s.push.reaction(s.user, r.ChatID, r.MsgID, self.ToNonAD().String(), r.Emoji, time.Now().Unix())
		}
		return nil
	case "histsync":
		if r.MsgID == "" {
			return fmt.Errorf("histsync: no reference msg")
		}
		own := s.selfStoreID()
		if own == nil {
			return fmt.Errorf("histsync: no device")
		}
		count := r.Count
		if count <= 0 || count > 200 {
			count = 60
		}
		info := &types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, IsFromMe: r.On},
			ID:            r.MsgID,
			Timestamp:     time.Unix(r.TS, 0),
		}
		reqMsg := cli.BuildHistorySyncRequest(info, count)
		resp, err := cli.SendMessage(ctx, own.ToNonAD(), reqMsg, whatsmeow.SendRequestExtra{Peer: true})
		s.log.Infof("histsync: request sent chat=%s ref=%s count=%d id=%s err=%v", chat, r.MsgID, count, resp.ID, err)
		return err
	case "resendmsg":
		if r.MsgID == "" {
			return fmt.Errorf("resendmsg: no msg")
		}
		own := s.selfStoreID()
		if own == nil {
			return fmt.Errorf("resendmsg: no device")
		}
		sender := chat
		if r.Sender != "" {
			if sj, err := normalizeJID(r.Sender); err == nil {
				sender = sj
			}
		}
		reqMsg := cli.BuildUnavailableMessageRequest(chat, sender, r.MsgID)
		resp, err := cli.SendMessage(ctx, own.ToNonAD(), reqMsg, whatsmeow.SendRequestExtra{Peer: true})
		s.log.Infof("resendmsg: request chat=%s msg=%s sender=%s reqid=%s err=%v", chat, r.MsgID, sender, resp.ID, err)
		return err
	case "delete":
		if r.Mode == "me" {
			return nil
		}
		msg := cli.BuildRevoke(chat, self, r.MsgID)
		_, err := cli.SendMessage(ctx, chat, msg)
		return err
	case "pin":
		return cli.SendAppState(ctx, appstate.BuildPin(chat, r.On))
	case "archive":
		return cli.SendAppState(ctx, appstate.BuildArchive(chat, r.On, time.Now(), nil))
	case "mute":
		return cli.SendAppState(ctx, appstate.BuildMute(chat, r.On, 0))
	case "star":
		sender := chat
		fromMe := false
		if sm := s.getStashed(r.MsgID); sm != nil {
			if sm.sender != "" {
				if sj, err := normalizeJID(sm.sender); err == nil {
					sender = sj
				}
			}
			fromMe = sm.fromMe
		}
		return cli.SendAppState(ctx, appstate.BuildStar(chat, sender, types.MessageID(r.MsgID), fromMe, r.On))
	case "typing":
		st := types.ChatPresencePaused
		if r.Typing {
			st = types.ChatPresenceComposing
		}
		return cli.SendChatPresence(ctx, chat, st, types.ChatPresenceMediaText)
	case "subscribe":
		return cli.SubscribePresence(ctx, chat)
	case "markread":
		chatKey := chat.ToNonAD().String()
		s.mediaMu.Lock()
		ids := s.unread[chatKey]
		li := s.lastIn[chatKey]
		delete(s.unread, chatKey)
		s.mediaMu.Unlock()
		if len(ids) == 0 {
			return nil
		}
		sender := chat
		if li.sender != "" {
			if sj, err := normalizeJID(li.sender); err == nil {
				sender = sj
			}
		}
		return cli.MarkRead(ctx, ids, time.Now(), chat, sender)
	case "forward":
		sm := s.getStashed(r.MsgID)
		if sm == nil || sm.msg == nil {
			return fmt.Errorf("original message not found (forward)")
		}
		resp, err := cli.SendMessage(ctx, chat, sm.msg)
		if err != nil {
			return err
		}
		s.pushOwnSent(resp.ID, chat, sendReq{}, sm.msg)
		return nil
	case "block":
		action := events.BlocklistChangeActionUnblock
		if r.On {
			action = events.BlocklistChangeActionBlock
		}
		_, err := cli.UpdateBlocklist(ctx, chat, action)
		return err
	case "edit":
		newContent := &waE2E.Message{Conversation: proto.String(r.Text)}
		msg := cli.BuildEdit(chat, types.MessageID(r.MsgID), newContent)
		_, err := cli.SendMessage(ctx, chat, msg)
		return err
	default:
		return fmt.Errorf("unknown action: %s", r.Action)
	}
}

func (s *session) profilePic(ctx context.Context, jidStr string) (string, error) {
	cli := s.cli()
	if cli == nil {
		return "", fmt.Errorf("not connected")
	}
	jid, err := normalizeJID(jidStr)
	if err != nil {
		return "", err
	}
	info, err := cli.GetProfilePictureInfo(ctx, jid, nil)
	if err != nil || info == nil {
		return "", err
	}
	return info.URL, nil
}

type checkOut struct {
	JID  string `json:"jid"`
	OnWA bool   `json:"onWA"`
}

func (s *session) checkNumber(ctx context.Context, phone string) (checkOut, error) {
	cli := s.cli()
	if cli == nil {
		return checkOut{}, fmt.Errorf("not connected")
	}
	res, err := cli.IsOnWhatsApp(ctx, []string{phone})
	if err != nil {
		return checkOut{}, err
	}
	if len(res) == 0 {
		return checkOut{JID: "", OnWA: false}, nil
	}
	return checkOut{JID: res[0].JID.ToNonAD().String(), OnWA: res[0].IsIn}, nil
}

type groupParticipantOut struct {
	JID     string `json:"jid"`
	IsAdmin bool   `json:"isAdmin"`
}

type groupInfoOut struct {
	Name         string                `json:"name"`
	Topic        string                `json:"topic"`
	Participants []groupParticipantOut `json:"participants"`
}

func (s *session) groupInfo(ctx context.Context, jidStr string) (groupInfoOut, error) {
	cli := s.cli()
	if cli == nil {
		return groupInfoOut{}, fmt.Errorf("not connected")
	}
	jid, err := normalizeJID(jidStr)
	if err != nil {
		return groupInfoOut{}, fmt.Errorf("invalid jid: %w", err)
	}
	gi, err := cli.GetGroupInfo(ctx, jid)
	if err != nil {
		return groupInfoOut{}, err
	}
	out := groupInfoOut{
		Name:         gi.GroupName.Name,
		Topic:        gi.GroupTopic.Topic,
		Participants: make([]groupParticipantOut, 0, len(gi.Participants)),
	}
	for _, pt := range gi.Participants {
		out.Participants = append(out.Participants, groupParticipantOut{
			JID:     pt.JID.ToNonAD().String(),
			IsAdmin: pt.IsAdmin || pt.IsSuperAdmin,
		})
	}
	return out, nil
}
