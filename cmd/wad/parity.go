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

// unwrapMsg unwraps "view once" messages, exposing the real inner media/
// content. WhatsApp wraps the content in a FutureProofMessage in three variants
// (legacy, V2 and the V2 extension); all of them keep the real Message inside.
// Returns m unchanged when it is not view-once.
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

// replyContext builds the ContextInfo for a reply to quotedID (nil if not a
// reply / unknown). Needs the quoted message + its author to render the quote.
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

// contacts returns all known contacts (for the server's name refresh).
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

// downloadMedia fetches an inbound media message's bytes via whatsmeow (which
// downloads by media keys, not a URL). The message was stashed in onMessage.
func (s *session) downloadMedia(ctx context.Context, msgID string) ([]byte, string, error) {
	sm := s.getStashed(msgID)
	if sm == nil || sm.msg == nil {
		return nil, "", fmt.Errorf("media not found (id=%s)", msgID)
	}
	m := unwrapMsg(sm.msg) // unwraps view-once before the media type switch
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

// actionReq is the body for non-message ops.
type actionReq struct {
	Action string `json:"action"` // react/delete/typing/subscribe/markread/forward/pin/archive/star/mute/block/edit/logout
	ChatID string `json:"chatId"`
	MsgID  string `json:"msgId"`
	Emoji  string `json:"emoji"`
	Typing bool   `json:"typing"`
	Mode   string `json:"mode"`   // delete: "me" | "everyone"
	On     bool   `json:"on"`     // pin/archive/star/mute/block toggle
	Text   string `json:"text"`   // edit: the new text content
	Sender string `json:"sender"` // react/star: author of the target msg (from the server store)
	TS     int64  `json:"ts"`     // histsync: timestamp of the reference message
	Count  int    `json:"count"`  // histsync: quantas msgs antigas pedir
}

func (s *session) action(ctx context.Context, r actionReq) error {
	cli := s.cli()
	if cli == nil || !cli.IsConnected() {
		return fmt.Errorf("not connected")
	}
	if r.Action == "logout" {
		return cli.Logout(ctx) // does not use chat; unlinks the device
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
		// sender = the REAL author of the reacted-to message. In a group, passing
		// the chat as sender breaks the reaction (key.Participant becomes the
		// group's JID). We prefer the sender coming from the server's STORE
		// (r.Sender, authoritative and survives a restart); then the in-memory
		// stash and, last, the chat (fine for 1:1 / our own).
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
		// Echo of our OWN reaction: WhatsApp does NOT redeliver your own reaction,
		// so without this it never shows up in the panel. Push it as if it had
		// arrived (fromJID = self) — the frontend applies it through the same
		// reaction handler. An empty emoji means removal (the frontend handler
		// takes care of the splice).
		if self.User != "" {
			s.push.reaction(s.user, r.ChatID, r.MsgID, self.ToNonAD().String(), r.Emoji, time.Now().Unix())
		}
		return nil
	case "histsync":
		// Recovers history/media keys ON DEMAND: asks WhatsApp for the `Count`
		// messages BEFORE the reference message (the newest in the chat). The
		// answer arrives as events.HistorySync (onHistorySync) carrying the protos
		// + media keys → old images become downloadable again.
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
		// Asks the primary device to RESEND one specific message
		// (PLACEHOLDER_MESSAGE_RESEND). The answer arrives as a normal
		// events.Message (via handlePlaceholderResendResponse → onMessage) ALREADY
		// CARRYING the media keys → recovers old images whose key the daemon lost.
		// It is per-message and precise (unlike history sync, which only pulls OLDER msgs).
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
		// "delete for me" is local only (the server hides it via Store.MarkDeleted) —
		// nothing goes on the wire. "delete for everyone" = revoke (only on YOUR msgs → self).
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
		// chat = destination; marks ALL tracked unread inbound messages (not just the
		// last one). lastIn still resolves the sender (in a group, MarkRead needs the author).
		chatKey := chat.ToNonAD().String()
		s.mediaMu.Lock()
		ids := s.unread[chatKey]
		li := s.lastIn[chatKey]
		delete(s.unread, chatKey) // limpa o lote marcado
		s.mediaMu.Unlock()
		if len(ids) == 0 {
			return nil // nothing to mark
		}
		sender := chat
		if li.sender != "" {
			if sj, err := normalizeJID(li.sender); err == nil {
				sender = sj
			}
		}
		return cli.MarkRead(ctx, ids, time.Now(), chat, sender)
	case "forward":
		// MsgID = source (looked up in the global stash); chat = destination.
		sm := s.getStashed(r.MsgID)
		if sm == nil || sm.msg == nil {
			return fmt.Errorf("original message not found (forward)")
		}
		resp, err := cli.SendMessage(ctx, chat, sm.msg)
		if err != nil {
			return err
		}
		// Persist in the server's Store (same as a normal send).
		s.pushOwnSent(resp.ID, chat, sendReq{}, sm.msg)
		return nil
	case "block":
		// Block/unblock the contact. r.On = block, !r.On = unblock. The updated
		// *Blocklist that comes back is ignored (the state is reflected by
		// whatsmeow's BlocklistChange events).
		action := events.BlocklistChangeActionUnblock
		if r.On {
			action = events.BlocklistChangeActionBlock
		}
		_, err := cli.UpdateBlocklist(ctx, chat, action)
		return err
	case "edit":
		// Edits an already-sent message, replacing the text with r.Text.
		newContent := &waE2E.Message{Conversation: proto.String(r.Text)}
		msg := cli.BuildEdit(chat, types.MessageID(r.MsgID), newContent)
		_, err := cli.SendMessage(ctx, chat, msg)
		return err
	default:
		return fmt.Errorf("unknown action: %s", r.Action)
	}
}

// profilePic returns the avatar URL for a JID ("" if none).
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

// checkOut is checkNumber's response: whether the phone number is on WhatsApp
// plus the canonical JID.
type checkOut struct {
	JID  string `json:"jid"`
	OnWA bool   `json:"onWA"`
}

// checkNumber checks whether a phone number (digits only, international format)
// is registered on WhatsApp, returning the canonical JID.
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

// groupParticipantOut is a group participant in the shape the API exposes.
type groupParticipantOut struct {
	JID     string `json:"jid"`
	IsAdmin bool   `json:"isAdmin"`
}

// groupInfoOut is groupInfo's response: the group's name, topic (description)
// and participants.
type groupInfoOut struct {
	Name         string                `json:"name"`
	Topic        string                `json:"topic"`
	Participants []groupParticipantOut `json:"participants"`
}

// groupInfo fetches a group's basic metadata (name, topic, participants).
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
