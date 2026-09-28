package videocall

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/notify/fcmpush"
)

var (
	ringStatsMu sync.Mutex
	ringStats   = map[string]int64{}
)

func incRingStat(reason string) {
	ringStatsMu.Lock()
	ringStats[reason]++
	ringStatsMu.Unlock()
}

func RingStats() map[string]int64 {
	ringStatsMu.Lock()
	defer ringStatsMu.Unlock()
	out := make(map[string]int64, len(ringStats))
	for k, v := range ringStats {
		out[k] = v
	}
	return out
}

const (
	ringReasonDeduped = "deduped"
	ringReasonMuted   = "muted"
)

func (s *Service) announceJoin(roomID, user, displayName, clientID string, resume bool) {
	now := time.Now().Unix()

	dec := ringDecision{Reason: ringReasonOngoing}
	if s.Calls != nil {
		dec = s.Calls.OnJoin(roomID, user, clientID, resume, now)
		if s.Calls.Dirty() {
			_ = s.Calls.save()
		}
	}

	room, ok := s.Room(roomID)
	if !ok {
		return
	}

	if s.Presence != nil && !strings.HasPrefix(user, "guest:") {
		s.Presence.NotifyUser(user, PresenceEvent{
			Type:     "call-answered-elsewhere",
			RoomID:   roomID,
			RoomName: room.Name,
			CallID:   dec.CallID,
		})
	}

	if !dec.Ring {
		incRingStat(dec.Reason)
		log.Printf("videocall: ring suppressed room=%s from=%s reason=%s call=%s",
			roomID, user, dec.Reason, dec.CallID)
		return
	}

	ev := PresenceEvent{
		Type:     "incoming-call",
		RoomID:   roomID,
		RoomName: room.Name,
		From:     displayName,
		CallID:   dec.CallID,
	}

	recipients := make([]string, 0, len(room.Members)+1)
	deduped := 0
	for _, r := range append([]string{room.Owner}, room.Members...) {
		if r == "" || r == user {
			continue
		}
		if s.Calls != nil && !s.Calls.AllowRing(roomID, r, now) {
			deduped++
			continue
		}
		recipients = append(recipients, r)
	}
	if deduped > 0 {
		incRingStat(ringReasonDeduped)
	}
	if len(recipients) == 0 {
		return
	}

	var rang []string
	if s.Presence != nil {
		rang = s.Presence.Ring(recipients, user, ev)
	}
	incRingStat(ringReasonNewCall)
	s.audit("videocall.ring", user, roomID)
	log.Printf("videocall: ring room=%s from=%s call=%s recipients=%d delivered=%d deduped=%d",
		roomID, user, dec.CallID, len(recipients), len(rang), deduped)

	if s.Push != nil {
		for _, recipient := range recipients {
			if s.Presence != nil && s.Presence.WantsRing(recipient) {
				continue
			}
			go s.SendIncomingCall(recipient, ev, func(deviceID string) bool {
				return s.Devices.ShouldRing(recipient, deviceID, time.Now().Unix())
			})
		}
	}

	if s.FCM != nil {
		for _, recipient := range recipients {
			if s.Presence != nil && s.Presence.WantsRing(recipient) {
				continue
			}
			r := recipient
			go s.FCM.SendDataToUser(context.Background(), r, map[string]string{
				"type":        "incoming-call",
				"room_id":     roomID,
				"room_name":   room.Name,
				"caller_name": displayName,
				"call_id":     dec.CallID,
			}, fcmpush.DataOptions{
				CollapseKey: "call-" + roomID,
				TTLSeconds:  pushTTL,
			}, func(deviceID string) bool {
				return s.Devices.ShouldRing(r, deviceID, time.Now().Unix())
			})
		}
	}
}

func (s *Service) onPeerGone(roomID, clientID string, graceful bool) {
	if s.Calls == nil {
		return
	}
	ended, callID := s.Calls.OnPeerGone(roomID, clientID, graceful, time.Now().Unix())
	if s.Calls.Dirty() {
		_ = s.Calls.save()
	}
	if ended {
		s.broadcastCallEnded(roomID, callID)
	}
}

func (s *Service) broadcastCallEnded(roomID, callID string) {
	if s.Presence == nil {
		return
	}
	room, ok := s.Room(roomID)
	if !ok {
		return
	}
	s.Presence.NotifyUsers(append([]string{room.Owner}, room.Members...), "", PresenceEvent{
		Type:     "call-ended",
		RoomID:   roomID,
		RoomName: room.Name,
		CallID:   callID,
	})
	log.Printf("videocall: call ended room=%s call=%s", roomID, callID)

	if s.FCM != nil {
		for _, recipient := range append([]string{room.Owner}, room.Members...) {
			if recipient == "" {
				continue
			}
			r := recipient
			go s.FCM.SendDataToUser(context.Background(), r, map[string]string{
				"type":    "call-ended",
				"room_id": roomID,
				"call_id": callID,
			}, fcmpush.DataOptions{CollapseKey: "call-" + roomID}, nil)
		}
	}
}

func (s *Service) callHeartbeat() {
	defer close(s.callTickerDone)
	t := time.NewTicker(liveCallTouchSec * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.callTickerStop:
			s.touchLiveCalls()
			return
		case <-t.C:
			s.touchLiveCalls()
			for _, ended := range s.Calls.GC(time.Now().Unix()) {
				s.broadcastCallEnded(ended.RoomID, ended.CallID)
			}
			if s.Calls.Dirty() {
				_ = s.Calls.save()
			}
		}
	}
}

func (s *Service) touchLiveCalls() {
	if s.Calls == nil || s.Hub == nil {
		return
	}
	rooms := s.Hub.OccupiedRooms()
	if len(rooms) == 0 {
		return
	}
	s.Calls.Touch(rooms, time.Now().Unix())
}
