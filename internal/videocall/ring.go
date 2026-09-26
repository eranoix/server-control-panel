package videocall

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/notify/fcmpush"
)

// Ringer instrumentation. Every ring decision is counted by reason and exposed
// in vpsm_videocall_rings_total{reason}. It is what turns "it keeps ringing"
// into a number — before and after the fix — and what denounces a future
// regression without depending on someone complaining.
var (
	ringStatsMu sync.Mutex
	ringStats   = map[string]int64{}
)

func incRingStat(reason string) {
	ringStatsMu.Lock()
	ringStats[reason]++
	ringStatsMu.Unlock()
}

// RingStats returns the counters by reason (new-call, rejoin, ongoing,
// resume-hint, deduped, muted). Read by the Prometheus export.
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
	ringReasonDeduped = "deduped" // suppressed by the per-recipient dedup window
	ringReasonMuted   = "muted"   // every one of the recipient's devices is silenced
)

// announceJoin is the ONLY place that decides to ring. That decision used to be
// `len(existing) == 0`, duplicated between HandleWS and HandleGuestWS; now it
// consults the persisted call session.
//
// Order of the steps (each one resolves a reported symptom):
//
//  1. Decide against the SESSION: reconnect/deploy/joining a call in progress
//     do not ring. Kills "it keeps generating notifications".
//  2. Tell the user's OTHER devices that they answered here
//     (call-answered-elsewhere). Kills "it rings on Windows after I have
//     already answered on the Mac".
//  3. When it does ring, respect the per-device policy and the per-recipient
//     dedup. Kills "it rings on a device I never chose".
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

	// (2) "Answered here." A guest has no other devices in the panel (the user
	// is synthetic, "guest:<name>"), so this only applies to a real account.
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
		log.Printf("videocall: ring suprimido room=%s from=%s reason=%s call=%s",
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

	// (3) Per-recipient dedup BEFORE the fan-out: a second net in case the
	// session logic has a hole, so that the worst case stays "one ring a
	// minute", never a burst.
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
	log.Printf("videocall: ring room=%s from=%s call=%s destinatarios=%d entregues=%d deduped=%d",
		roomID, user, dec.CallID, len(recipients), len(rang), deduped)

	// Web Push (off-app) for whoever does NOT have a device ringing right now.
	// Note WantsRing (and not IsOnline): if the panel is open but the user has
	// silenced that computer, the push on the phone still makes sense —
	// otherwise silencing the Mac would silence the phone along with it.
	if s.Push != nil {
		for _, recipient := range recipients {
			if s.Presence != nil && s.Presence.WantsRing(recipient) {
				continue // already ringing in-tab
			}
			go s.SendIncomingCall(recipient, ev, func(deviceID string) bool {
				return s.Devices.ShouldRing(recipient, deviceID, time.Now().Unix())
			})
		}
	}

	// FCM (native Android, off-app): the same fan-out as the Web Push above, so the
	// native app reaches the device even when closed/killed (which Web Push does not
	// guarantee). call_id travels in the payload because PresenceEvent.CallID already
	// exists and is already populated (ev.CallID) — carrying it here does not change
	// the payload the Web client already receives (that one stays in push.go,
	// untouched), it only lets the Android client correlate a future cancellation with
	// the ring that originated it. A per-room CollapseKey makes a re-ring of the same
	// call replace the previous one in the FCM queue instead of stacking up.
	//
	// The key is "caller_name", not "from": the FCM SDK reserves "from" for the
	// message sender and strips it from RemoteMessage.getData() on the client —
	// using that key would make the Android app never receive the caller's name.
	if s.FCM != nil {
		for _, recipient := range recipients {
			if s.Presence != nil && s.Presence.WantsRing(recipient) {
				continue // already ringing in-tab
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

// onPeerGone closes the call's life cycle when a peer disappears.
//
// Only a GRACEFUL exit (the client sent "leave") ends the session; a dropped
// connection leaves the call alive inside the grace window, waiting for the
// reconnect. Without that distinction there is no way to tell "hung up" from
// "the server restarted" — the difference between clearing the screen and ringing.
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

// broadcastCallEnded clears the pending "incoming call" modals on every member.
// Without it the modal stays on the screen forever (only the SOUND stopped, at
// 20s) and clicking it tries to join a call that is already over.
//
// It uses NotifyUsers (no device policy) on purpose: this is a cleanup event,
// and silencing a cleanup only leaves rubbish on the screen.
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
	log.Printf("videocall: chamada encerrada room=%s call=%s", roomID, callID)

	// FCM: without this, a native recipient whose process already received the
	// earlier data-only "incoming-call" (see announceJoin) is left with the ringer
	// sounding indefinitely after the caller gives up — the web panel solves that
	// via NotifyUsers above (WebSocket), but nothing clears the FCM side without
	// this dedicated push. Same CollapseKey as the original ring: if "call-ended"
	// reaches FCM before "incoming-call" has left the queue, it replaces it there
	// (it never gets to ring); if it has already been delivered, it is up to the
	// Android app — on receiving this data message — to cancel the notification /
	// active Telecom call for that call_id.
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

// callHeartbeat keeps the live calls "fresh" and ends the ones whose grace
// window blew.
//
// The touch is the detail that makes the fix hold on a LONG call: LastActiveAt
// would only change on join/leave, so in a 40-minute call with nobody entering
// or leaving, the deploy at minute 40 fell outside the grace and rang again.
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

// touchLiveCalls renews the rooms that have a peer connected right now.
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
