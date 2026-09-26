package videocall

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Tests for the phantom ringer.
//
// The bug: the server decided to ring by `len(existing) == 0` at Join, i.e.
// "I am the first live peer in the hub → new call". Since the hub is pure
// memory, every restart of the process emptied it and the first client to
// reconnect (reconnection is automatic) fired a ring in the middle of a call
// already in progress. The tests below pin down each face of that.

// waitEvent reads a presence event with a timeout, so the tests do not depend
// on a sleep.
func waitEvent(t *testing.T, sub *PresenceSub, d time.Duration) (PresenceEvent, bool) {
	t.Helper()
	select {
	case ev := <-sub.Ch:
		return ev, true
	case <-time.After(d):
		return PresenceEvent{}, false
	}
}

// expectNoEvent fails if ANY ring event arrives within the window.
func expectNoRing(t *testing.T, sub *PresenceSub, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev := <-sub.Ch:
			if ev.Type == "incoming-call" {
				t.Fatalf("the ringer rang when it should NOT have: %+v", ev)
			}
			// control events (call-answered-elsewhere/ended) are fine
		case <-deadline:
			return
		}
	}
}

// --- the registry alone -------------------------------------------------

func TestCallRegistry_PrimeiroJoinToca(t *testing.T) {
	r := newCallRegistry(filepath.Join(t.TempDir(), "active-calls.json"))
	now := time.Now().Unix()
	dec := r.OnJoin("R", "alice", "cid-a", false, now)
	if !dec.Ring || dec.Reason != ringReasonNewCall {
		t.Fatalf("the first join should ring: %+v", dec)
	}
	if dec.CallID == "" {
		t.Fatal("a new call needs a CallID")
	}
}

func TestCallRegistry_RejoinDoMesmoClienteNaoToca(t *testing.T) {
	r := newCallRegistry(filepath.Join(t.TempDir(), "active-calls.json"))
	now := time.Now().Unix()
	first := r.OnJoin("R", "alice", "cid-a", false, now)
	again := r.OnJoin("R", "alice", "cid-a", false, now+5)
	if again.Ring {
		t.Fatal("a reconnect from the SAME client must not ring (that was the bug)")
	}
	if again.Reason != ringReasonRejoin {
		t.Fatalf("expected reason %q, got %q", ringReasonRejoin, again.Reason)
	}
	if again.CallID != first.CallID {
		t.Fatalf("a rejoin should adopt the SAME call: %s != %s", again.CallID, first.CallID)
	}
}

func TestCallRegistry_SegundoParticipanteNaoTocaDeNovo(t *testing.T) {
	r := newCallRegistry(filepath.Join(t.TempDir(), "active-calls.json"))
	now := time.Now().Unix()
	r.OnJoin("R", "alice", "cid-a", false, now)
	dec := r.OnJoin("R", "bob", "cid-b", false, now+3)
	if dec.Ring {
		t.Fatal("joining an ALREADY active call must not ring (whoever is there sees peer-joined)")
	}
	if dec.Reason != ringReasonOngoing {
		t.Fatalf("expected reason %q, got %q", ringReasonOngoing, dec.Reason)
	}
}

func TestCallRegistry_ResumeHintSilencia(t *testing.T) {
	r := newCallRegistry(filepath.Join(t.TempDir(), "active-calls.json"))
	now := time.Now().Unix()
	// No live call at all, but the client declares it is resuming.
	dec := r.OnJoin("R", "alice", "cid-a", true, now)
	if dec.Ring {
		t.Fatal("resume=1 has to silence it even with no live session")
	}
	if dec.Reason != ringReasonResumeHint {
		t.Fatalf("expected reason %q, got %q", ringReasonResumeHint, dec.Reason)
	}
}

func TestCallRegistry_GraceExpiraEChamadaNovaToca(t *testing.T) {
	r := newCallRegistry(filepath.Join(t.TempDir(), "active-calls.json"))
	now := time.Now().Unix()
	r.OnJoin("R", "alice", "cid-a", false, now)
	// Well past the grace window: this is a genuinely new call, it MUST ring.
	// The fix must not turn into a permanent silencer.
	dec := r.OnJoin("R", "alice", "cid-a", false, now+liveCallGraceSec+10)
	if !dec.Ring {
		t.Fatal("after the grace period, a new call has to ring again")
	}
}

func TestCallRegistry_QuedaMantemChamada_DesligarEncerra(t *testing.T) {
	r := newCallRegistry(filepath.Join(t.TempDir(), "active-calls.json"))
	now := time.Now().Unix()
	r.OnJoin("R", "alice", "cid-a", false, now)

	// A drop (not graceful): the call stays alive waiting for the reconnect.
	if ended, _ := r.OnPeerGone("R", "cid-a", false, now+1); ended {
		t.Fatal("a dropped connection must NOT end the call (that is the deploy reconnect)")
	}
	if dec := r.OnJoin("R", "alice", "cid-a", false, now+2); dec.Ring {
		t.Fatal("a reconnect after a drop must not ring")
	}

	// Hung up on purpose (the client sent "leave"): ends right away.
	ended, callID := r.OnPeerGone("R", "cid-a", true, now+3)
	if !ended || callID == "" {
		t.Fatalf("a hangup by the last participant should end the call (ended=%v id=%q)", ended, callID)
	}
	if dec := r.OnJoin("R", "alice", "cid-a", false, now+4); !dec.Ring {
		t.Fatal("after hanging up, the next call is a new one and HAS to ring")
	}
}

func TestCallRegistry_DedupPorDestinatario(t *testing.T) {
	r := newCallRegistry(filepath.Join(t.TempDir(), "active-calls.json"))
	now := time.Now().Unix()
	if !r.AllowRing("R", "sam", now) {
		t.Fatal("the first ring should get through")
	}
	if r.AllowRing("R", "sam", now+ringDedupSec-1) {
		t.Fatal("a second ring inside the window should be suppressed")
	}
	if !r.AllowRing("R", "sam", now+ringDedupSec+1) {
		t.Fatal("after the window the ring gets through again")
	}
	if !r.AllowRing("R", "jordan", now) {
		t.Fatal("dedup is PER recipient — another user cannot be affected")
	}
}

func TestCallRegistry_TouchMantemViva(t *testing.T) {
	r := newCallRegistry(filepath.Join(t.TempDir(), "active-calls.json"))
	now := time.Now().Unix()
	r.OnJoin("R", "alice", "cid-a", false, now)
	// A long call with no join/leave at all: it is the heartbeat's Touch that
	// prevents the ageing (otherwise the deploy at minute 40 would ring again).
	r.Touch([]string{"R"}, now+liveCallGraceSec+30)
	if dec := r.OnJoin("R", "alice", "cid-a", false, now+liveCallGraceSec+31); dec.Ring {
		t.Fatal("a call renewed by the heartbeat cannot be read as new")
	}
}

func TestCallRegistry_GCEncerraChamadaVencida(t *testing.T) {
	r := newCallRegistry(filepath.Join(t.TempDir(), "active-calls.json"))
	now := time.Now().Unix()
	r.OnJoin("R", "alice", "cid-a", false, now)
	if got := r.GC(now + 1); len(got) != 0 {
		t.Fatalf("the GC must not end a call inside the grace period: %+v", got)
	}
	got := r.GC(now + liveCallGraceSec + 1)
	if len(got) != 1 || got[0].RoomID != "R" {
		t.Fatalf("the GC should end the expired call, got %+v", got)
	}
}

// --- persistence: the DEPLOY case ---------------------------------------

// TestCallRegistry_SobreviveAoRestart is the test that stands for the reported
// bug: during a call, a deploy restarts the process; the client reconnects on
// its own; the ringer must NOT sound.
func TestCallRegistry_SobreviveAoRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "active-calls.json")

	r1 := newCallRegistry(path)
	now := time.Now().Unix()
	first := r1.OnJoin("R", "alice", "cid-a", false, now)
	if !first.Ring {
		t.Fatal("setup: the original call should have rung")
	}
	// Shutdown: it is the Service's Close() that writes — without that write,
	// the memory goes away and the restart looks like an empty room.
	if err := r1.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// A new process rereads from disk.
	r2 := newCallRegistry(path)
	dec := r2.OnJoin("R", "alice", "cid-a", false, now+3) // ~3s of restart
	if dec.Ring {
		t.Fatal("REGRESSION: a post-restart reconnect rang the doorbell again")
	}
	if dec.CallID != first.CallID {
		t.Fatalf("the call should be the SAME across the restart: %s != %s", dec.CallID, first.CallID)
	}
}

func TestCallRegistry_NaoRessuscitaChamadaAntiga(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "active-calls.json")
	r1 := newCallRegistry(path)
	// A call from "yesterday", written with an old LastActiveAt.
	r1.calls["R"] = &LiveCall{
		RoomID: "R", CallID: "velho",
		StartedAt:    time.Now().Unix() - 86400,
		LastActiveAt: time.Now().Unix() - 86400,
		Participants: map[string]int64{"cid-a": 1},
		Users:        map[string]int64{"alice": 1},
	}
	if err := r1.save(); err != nil {
		t.Fatal(err)
	}
	r2 := newCallRegistry(path)
	if _, ok := r2.ActiveCall("R", time.Now().Unix()); ok {
		t.Fatal("a call outside the grace period cannot be re-read as alive")
	}
	if dec := r2.OnJoin("R", "alice", "cid-a", false, time.Now().Unix()); !dec.Ring {
		t.Fatal("yesterday's call cannot silence today's")
	}
}

func TestCallRegistry_ArquivoCorrompidoNaoDerruba(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "active-calls.json")
	if err := os.WriteFile(path, []byte("{lixo nao json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newCallRegistry(path)
	if dec := r.OnJoin("R", "alice", "cid-a", false, time.Now().Unix()); !dec.Ring {
		t.Fatal("with corrupted state the safe behaviour is to ring, not to go mute")
	}
}

// --- per-device policy --------------------------------------------------

func TestDeviceStore_PoliticaPorAparelho(t *testing.T) {
	d := newDeviceStore(filepath.Join(t.TempDir(), "ring-devices.json"))
	now := time.Now().Unix()

	// An unknown device RINGS — graceful degradation.
	if !d.ShouldRing("sam", "dev-desconhecido", now) {
		t.Fatal("an unknown device has to ring")
	}
	// An old client (with no device_id) does too.
	if !d.ShouldRing("sam", "", now) {
		t.Fatal("a client with no device_id has to ring")
	}

	d.Seen("sam", "dev-win", "Windows — Edge")
	if !d.ShouldRing("sam", "dev-win", now) {
		t.Fatal("a new device is born ringing")
	}

	off := false
	if !d.Set("sam", "dev-win", &off, nil, "") {
		t.Fatal("Set should find the device")
	}
	if d.ShouldRing("sam", "dev-win", now) {
		t.Fatal("a switched-off device must not ring")
	}
	on := true
	d.Set("sam", "dev-win", &on, nil, "")
	if !d.ShouldRing("sam", "dev-win", now) {
		t.Fatal("switching it back on should ring again")
	}

	// A temporary silence expires on its own.
	until := now + 3600
	d.Set("sam", "dev-win", nil, &until, "")
	if d.ShouldRing("sam", "dev-win", now) {
		t.Fatal("inside the temporary silence it must not ring")
	}
	if !d.ShouldRing("sam", "dev-win", now+3601) {
		t.Fatal("once the silence is over, it rings again on its own")
	}

	// Isolation between users.
	if !d.ShouldRing("jordan", "dev-win", now) {
		t.Fatal("the policy is per user — it cannot leak between accounts")
	}
}

func TestDeviceStore_PersisteEEsquece(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ring-devices.json")
	d1 := newDeviceStore(path)
	d1.Seen("sam", "dev-mac", "Mac — Chrome")
	off := false
	d1.Set("sam", "dev-mac", &off, nil, "")

	d2 := newDeviceStore(path)
	if d2.ShouldRing("sam", "dev-mac", time.Now().Unix()) {
		t.Fatal("the user's choice has to survive the restart")
	}
	if !d2.Forget("sam", "dev-mac") {
		t.Fatal("Forget should remove it")
	}
	if !d2.ShouldRing("sam", "dev-mac", time.Now().Unix()) {
		t.Fatal("a forgotten device goes back to the default (it rings)")
	}
}

func TestPresenceHub_RingRespeitaAparelhoSilenciado(t *testing.T) {
	p := NewPresenceHub()
	d := newDeviceStore(filepath.Join(t.TempDir(), "ring-devices.json"))
	p.RingPolicy = func(user, deviceID string, now int64) bool { return d.ShouldRing(user, deviceID, now) }

	d.Seen("sam", "dev-win", "Windows — Edge")
	d.Seen("sam", "dev-mac", "Mac — Chrome")
	off := false
	d.Set("sam", "dev-win", &off, nil, "")

	win := p.Subscribe("sam", "dev-win")
	mac := p.Subscribe("sam", "dev-mac")
	defer p.Unsubscribe(win)
	defer p.Unsubscribe(mac)

	rang := p.Ring([]string{"sam"}, "jordan", PresenceEvent{Type: "incoming-call", RoomID: "R", CallID: "c1"})
	if len(rang) != 1 || rang[0] != "sam" {
		t.Fatalf("sam should receive it (through the Mac): %+v", rang)
	}
	if _, ok := waitEvent(t, mac, 200*time.Millisecond); !ok {
		t.Fatal("the Mac (not silenced) should have rung")
	}
	if ev, ok := waitEvent(t, win, 100*time.Millisecond); ok {
		t.Fatalf("the Windows one is silenced and could NOT ring: %+v", ev)
	}

	// A CONTROL event ignores the policy: clearing the screen is mandatory even
	// on a silenced device.
	p.NotifyUsers([]string{"sam"}, "", PresenceEvent{Type: "call-ended", RoomID: "R", CallID: "c1"})
	if ev, ok := waitEvent(t, win, 200*time.Millisecond); !ok || ev.Type != "call-ended" {
		t.Fatalf("call-ended has to arrive even on the silenced device (ok=%v ev=%+v)", ok, ev)
	}
}

func TestPresenceHub_WantsRing(t *testing.T) {
	p := NewPresenceHub()
	d := newDeviceStore(filepath.Join(t.TempDir(), "ring-devices.json"))
	p.RingPolicy = func(user, deviceID string, now int64) bool { return d.ShouldRing(user, deviceID, now) }
	d.Seen("sam", "dev-win", "Windows")
	off := false
	d.Set("sam", "dev-win", &off, nil, "")
	sub := p.Subscribe("sam", "dev-win")
	defer p.Unsubscribe(sub)

	if !p.IsOnline("sam") {
		t.Fatal("IsOnline is still true — the panel is open")
	}
	if p.WantsRing("sam") {
		t.Fatal("with the only device silenced, WantsRing has to be false (it frees the off-app push)")
	}
}

// --- integration in the Service ----------------------------------------

// TestService_DeployNaoTocaCampainha is the end-to-end test of the reported
// symptom: a call in progress, the process restarts, the other side
// reconnects — and Sam's panel must NOT ring.
func TestService_DeployNaoTocaCampainha(t *testing.T) {
	dir := t.TempDir()
	s := openServiceAt(t, dir)
	room, err := s.CreateRoom("sam", "Nosso Momento")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("sam", room.ID, "jordan"); err != nil {
		t.Fatal(err)
	}

	// Sam with the panel open on two devices (Windows + Mac).
	win := s.Presence.Subscribe("sam", "dev-win")
	mac := s.Presence.Subscribe("sam", "dev-mac")

	// Jordan calls: both devices ring (correct behaviour).
	s.announceJoin(room.ID, "jordan", "jordan", "cid-jordan", false)
	if ev, ok := waitEvent(t, win, time.Second); !ok || ev.Type != "incoming-call" {
		t.Fatalf("the real call had to ring (ok=%v ev=%+v)", ok, ev)
	}
	if _, ok := waitEvent(t, mac, time.Second); !ok {
		t.Fatal("the real call had to ring on the Mac too")
	}
	// Sam answers on the Mac.
	s.announceJoin(room.ID, "sam", "sam", "cid-sam-mac", false)

	// --- DEPLOY: the process dies and comes back ------------------------
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2 := openServiceAt(t, dir)
	win2 := s2.Presence.Subscribe("sam", "dev-win")
	defer s2.Presence.Unsubscribe(win2)

	// Both sides reconnect on their own. Neither may ring.
	s2.announceJoin(room.ID, "jordan", "jordan", "cid-jordan", false)
	s2.announceJoin(room.ID, "sam", "sam", "cid-sam-mac", true)
	expectNoRing(t, win2, 400*time.Millisecond)

	s.Presence.Unsubscribe(win)
	s.Presence.Unsubscribe(mac)
}

// TestService_AtenderEmUmAparelhoCalaOsOutros covers the second half of the
// symptom: I answered on the Mac and Windows kept ringing / trying to connect.
func TestService_AtenderEmUmAparelhoCalaOsOutros(t *testing.T) {
	s := openTempService(t)
	defer s.Close()
	room, _ := s.CreateRoom("sam", "Nosso Momento")
	_ = s.AddMember("sam", room.ID, "jordan")

	win := s.Presence.Subscribe("sam", "dev-win")
	defer s.Presence.Unsubscribe(win)

	s.announceJoin(room.ID, "jordan", "jordan", "cid-jordan", false)
	ev, ok := waitEvent(t, win, time.Second)
	if !ok || ev.Type != "incoming-call" {
		t.Fatalf("setup: the Windows one should have rung (%+v)", ev)
	}
	callID := ev.CallID

	// Sam answers on the Mac → Windows receives the cancellation.
	s.announceJoin(room.ID, "sam", "sam", "cid-sam-mac", false)
	got, ok := waitEvent(t, win, time.Second)
	if !ok || got.Type != "call-answered-elsewhere" {
		t.Fatalf("the Windows one has to be told it was already answered: ok=%v ev=%+v", ok, got)
	}
	if got.CallID != callID {
		t.Fatalf("the cancellation has to match the call that rang: %s != %s", got.CallID, callID)
	}
}

// TestService_DesligarLimpaModalPendente: an ended call has to erase the
// "Call from X" left on the screen of whoever did not answer.
func TestService_DesligarLimpaModalPendente(t *testing.T) {
	s := openTempService(t)
	defer s.Close()
	room, _ := s.CreateRoom("sam", "Nosso Momento")
	_ = s.AddMember("sam", room.ID, "jordan")

	win := s.Presence.Subscribe("sam", "dev-win")
	defer s.Presence.Unsubscribe(win)

	s.announceJoin(room.ID, "jordan", "jordan", "cid-jordan", false)
	if ev, ok := waitEvent(t, win, time.Second); !ok || ev.Type != "incoming-call" {
		t.Fatalf("setup: it should have rung (%+v)", ev)
	}
	// Jordan gives up and hangs up (a graceful exit).
	s.onPeerGone(room.ID, "cid-jordan", true)
	ev, ok := waitEvent(t, win, time.Second)
	if !ok || ev.Type != "call-ended" {
		t.Fatalf("the pending modal has to be cancelled: ok=%v ev=%+v", ok, ev)
	}
}

// TestService_LigacaoDepoisDeDesligarVoltaATocar makes sure the fix did not
// become a permanent silencer.
func TestService_LigacaoDepoisDeDesligarVoltaATocar(t *testing.T) {
	s := openTempService(t)
	defer s.Close()
	room, _ := s.CreateRoom("sam", "Nosso Momento")
	_ = s.AddMember("sam", room.ID, "jordan")
	win := s.Presence.Subscribe("sam", "dev-win")
	defer s.Presence.Unsubscribe(win)

	s.announceJoin(room.ID, "jordan", "jordan", "cid-jordan", false)
	if _, ok := waitEvent(t, win, time.Second); !ok {
		t.Fatal("setup: the first call should ring")
	}
	s.onPeerGone(room.ID, "cid-jordan", true)
	if ev, _ := waitEvent(t, win, time.Second); ev.Type != "call-ended" {
		t.Fatalf("setup: expected call-ended, got %+v", ev)
	}

	// A second call, outside the dedup window.
	s.Calls.mu.Lock()
	for k := range s.Calls.lastRing {
		delete(s.Calls.lastRing, k)
	}
	s.Calls.mu.Unlock()

	s.announceJoin(room.ID, "jordan", "jordan", "cid-jordan", false)
	if ev, ok := waitEvent(t, win, time.Second); !ok || ev.Type != "incoming-call" {
		t.Fatalf("a NEW call after the hangup has to ring: ok=%v ev=%+v", ok, ev)
	}
}

// --- E2E through the real HTTP handler ---------------------------------

// wsDialQ is wsDial with extra query params (we need ?resume=1).
func wsDialQ(t *testing.T, srv *httptest.Server, user, room, clientID string, extra url.Values) *websocket.Conn {
	t.Helper()
	q := url.Values{"user": {user}, "room_id": {room}}
	if clientID != "" {
		q.Set("client_id", clientID)
	}
	for k, vs := range extra {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?" + q.Encode()
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial (user=%s client_id=%s): %v", user, clientID, err)
	}
	return c
}

// TestHandleWS_ReconexaoNaoTocaCampainha_E2E exercises the REAL deployed path
// — query parsing, Peer, Hub.Join and the ring decision — and not just the
// registry in isolation. An earlier test proved that the Hub in isolation can
// pass while the handler is wrong; here the same guarantee is given for the
// ring.
func TestHandleWS_ReconexaoNaoTocaCampainha_E2E(t *testing.T) {
	s := openTempService(t)
	defer s.Close()
	room, _ := s.CreateRoom("sam", "Nosso Momento")
	if err := s.AddMember("sam", room.ID, "jordan"); err != nil {
		t.Fatal(err)
	}
	srv := wsTestServer(t, s)
	defer srv.Close()

	// Sam with the panel open on Windows (presence, outside the call).
	win := s.Presence.Subscribe("sam", "dev-win")
	defer s.Presence.Unsubscribe(win)

	// Jordan really calls → it rings.
	chey1 := wsDialQ(t, srv, "jordan", room.ID, "cid-jordan", nil)
	wsReadUntil(t, chey1, "joined")
	ev, ok := waitEvent(t, win, 2*time.Second)
	if !ok || ev.Type != "incoming-call" {
		t.Fatalf("the real call had to ring: ok=%v ev=%+v", ok, ev)
	}

	// Her connection drops and comes back (same client_id, with resume=1 — which
	// is what reopenSignaling sends). It must NOT ring again.
	_ = chey1.Close()
	chey2 := wsDialQ(t, srv, "jordan", room.ID, "cid-jordan", url.Values{"resume": {"1"}})
	defer chey2.Close()
	wsReadUntil(t, chey2, "joined")
	expectNoRing(t, win, 500*time.Millisecond)
}

// TestHandleWS_LeaveExplicitoEncerraChamada_E2E proves the "hung up" vs
// "dropped" discriminator end to end: the `leave` message the client sends on
// hangup has to end the session (and clear the others' modal).
func TestHandleWS_LeaveExplicitoEncerraChamada_E2E(t *testing.T) {
	s := openTempService(t)
	defer s.Close()
	room, _ := s.CreateRoom("sam", "Nosso Momento")
	if err := s.AddMember("sam", room.ID, "jordan"); err != nil {
		t.Fatal(err)
	}
	srv := wsTestServer(t, s)
	defer srv.Close()

	win := s.Presence.Subscribe("sam", "dev-win")
	defer s.Presence.Unsubscribe(win)

	jordan := wsDialQ(t, srv, "jordan", room.ID, "cid-jordan", nil)
	wsReadUntil(t, jordan, "joined")
	if ev, ok := waitEvent(t, win, 2*time.Second); !ok || ev.Type != "incoming-call" {
		t.Fatalf("setup: it should have rung (%+v)", ev)
	}

	// The user's hangup: videocall.js sends {"type":"leave"} before closing.
	if err := jordan.WriteJSON(SignalingMsg{Type: "leave"}); err != nil {
		t.Fatalf("write leave: %v", err)
	}
	got, ok := waitEvent(t, win, 2*time.Second)
	if !ok || got.Type != "call-ended" {
		t.Fatalf("the hangup has to end the call and clear the modal: ok=%v ev=%+v", ok, got)
	}
	_ = jordan.Close()
}
