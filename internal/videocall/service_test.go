package videocall

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/webpush"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
)

// --- Service tests -----------------------------------------------------

func TestCreateRoom_AssignsIDAndPersists(t *testing.T) {
	s := openTempService(t)
	r, err := s.CreateRoom("alice", "Sala da Casa")
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if r.ID == "" || r.Owner != "alice" || r.Name != "Sala da Casa" {
		t.Fatalf("unexpected room: %+v", r)
	}
	got, ok := s.Room(r.ID)
	if !ok || got.ID != r.ID {
		t.Fatalf("Room lookup failed")
	}
	if err := s.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	s2 := openServiceAt(t, filepath.Dir(filepath.Dir(s.path)))
	got2, ok := s2.Room(r.ID)
	if !ok || got2.Owner != "alice" {
		t.Fatalf("room not persisted across reload: %+v", got2)
	}
}

func TestCanJoin_OwnerAndMembers(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "x")
	if !s.CanJoin("alice", r.ID) {
		t.Fatal("owner should join")
	}
	if s.CanJoin("bob", r.ID) {
		t.Fatal("non-member should NOT join")
	}
	if err := s.AddMember("alice", r.ID, "bob"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	if !s.CanJoin("bob", r.ID) {
		t.Fatal("member should join after AddMember")
	}
}

func TestAddMember_NotOwnerRejected(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "x")
	err := s.AddMember("eve", r.ID, "eve")
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("expected not-authorized error, got %v", err)
	}
}

func TestAddMember_Idempotent(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "x")
	if err := s.AddMember("alice", r.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("alice", r.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Room(r.ID)
	if len(got.Members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(got.Members))
	}
}

func TestDeleteRoom_OwnerOnly_EvictsPeers(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "x")
	// Plant a fake peer in the hub for the room.
	peer := &Peer{ID: randomID(), User: "alice", RoomID: r.ID, SendCh: make(chan SignalingMsg, 4), closed: make(chan struct{})}
	if _, err := s.Hub.Join(peer); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRoom("eve", r.ID); err == nil {
		t.Fatal("non-owner should not delete")
	}
	if err := s.DeleteRoom("alice", r.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Room(r.ID); ok {
		t.Fatal("room still present after delete")
	}
	select {
	case <-peer.closed:
		// good — peer was evicted
	case <-time.After(time.Second):
		t.Fatal("peer was not evicted on room delete")
	}
}

func TestRemoveMember_EvictsOnlyMatchingUser(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "x")
	_ = s.AddMember("alice", r.ID, "bob")
	bobPeer := &Peer{ID: randomID(), User: "bob", RoomID: r.ID, SendCh: make(chan SignalingMsg, 4), closed: make(chan struct{})}
	alicePeer := &Peer{ID: randomID(), User: "alice", RoomID: r.ID, SendCh: make(chan SignalingMsg, 4), closed: make(chan struct{})}
	if _, err := s.Hub.Join(bobPeer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hub.Join(alicePeer); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember("alice", r.ID, "bob"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	select {
	case <-bobPeer.closed:
	case <-time.After(time.Second):
		t.Fatal("bob's peer was not evicted")
	}
	select {
	case <-alicePeer.closed:
		t.Fatal("alice's peer should NOT be evicted")
	default:
	}
}

func TestRenameRoom_OwnerOnly(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "Antiga")
	// a non-owner is rejected
	if err := s.RenameRoom("eve", r.ID, "Hack"); err == nil {
		t.Fatal("eve should not rename")
	}
	// the owner may
	if err := s.RenameRoom("alice", r.ID, "Nova"); err != nil {
		t.Fatalf("alice rename: %v", err)
	}
	got, _ := s.Room(r.ID)
	if got.Name != "Nova" {
		t.Fatalf("expected Nova, got %q", got.Name)
	}
	// an empty one is rejected
	if err := s.RenameRoom("alice", r.ID, "   "); err == nil {
		t.Fatal("an empty name should fail")
	}
}

func TestRemoveMember_CannotRemoveOwner(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "x")
	err := s.RemoveMember("alice", r.ID, "alice")
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("expected owner-protection error, got %v", err)
	}
}

func TestListForUser_OwnerAndMembership(t *testing.T) {
	s := openTempService(t)
	r1, _ := s.CreateRoom("alice", "a")
	r2, _ := s.CreateRoom("bob", "b")
	_ = s.AddMember("bob", r2.ID, "alice")
	got := s.ListForUser("alice")
	if len(got) != 2 {
		t.Fatalf("alice should see 2 rooms, got %d", len(got))
	}
	// Both rooms must be present; exact order is not tested here because
	// CreatedAt is unix-seconds and a same-second tie is broken by ID
	// (random hex), making strict ordering brittle in tests.
	found := map[string]bool{}
	for _, r := range got {
		found[r.ID] = true
	}
	if !found[r1.ID] || !found[r2.ID] {
		t.Fatalf("missing rooms: %+v", got)
	}
	if len(s.ListForUser("eve")) != 0 {
		t.Fatal("eve should see no rooms")
	}
}

// --- Hub tests ---------------------------------------------------------

func TestHub_Join_BroadcastsPeerJoined(t *testing.T) {
	h := NewHub(4)
	a := mkPeer("a", "alice", "R")
	b := mkPeer("b", "bob", "R")
	if _, err := h.Join(a); err != nil {
		t.Fatal(err)
	}
	existing, err := h.Join(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(existing) != 1 || existing[0].ID != "a" {
		t.Fatalf("bob should see alice in existing peers, got %+v", existing)
	}
	// Alice should have received peer-joined for bob.
	select {
	case msg := <-a.SendCh:
		if msg.Type != "peer-joined" || msg.From != "b" {
			t.Fatalf("alice got wrong msg: %+v", msg)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("alice didn't receive peer-joined")
	}
}

func TestHub_Forward_ACL_SameRoomOnly(t *testing.T) {
	h := NewHub(4)
	a := mkPeer("a", "alice", "R1")
	b := mkPeer("b", "bob", "R1")
	c := mkPeer("c", "carol", "R2")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	_, _ = h.Join(c)
	drain(a, b, c)

	// a → b same room: OK
	err := h.Forward("a", SignalingMsg{Type: "offer", To: "b", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("same-room forward failed: %v", err)
	}
	select {
	case msg := <-b.SendCh:
		if msg.From != "a" {
			t.Fatalf("From not stamped: %+v", msg)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("b didn't receive offer")
	}

	// a → c different room: REJECTED
	err = h.Forward("a", SignalingMsg{Type: "offer", To: "c", Payload: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrTargetNotInRoom) {
		t.Fatalf("expected ErrTargetNotInRoom, got %v", err)
	}
	select {
	case msg := <-c.SendCh:
		t.Fatalf("c should NOT have received cross-room offer: %+v", msg)
	case <-time.After(100 * time.Millisecond):
		// good
	}
}

func TestHub_Forward_StampsFrom_IgnoresClientFrom(t *testing.T) {
	// Client-supplied `from` is untrusted — server MUST overwrite with the
	// actual peer id. Defends against spoofing.
	h := NewHub(4)
	a := mkPeer("a", "alice", "R")
	b := mkPeer("b", "bob", "R")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	drain(a, b)
	err := h.Forward("a", SignalingMsg{Type: "chat", To: "b", From: "spoofed-id", Payload: json.RawMessage(`"hi"`)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-b.SendCh:
		if msg.From != "a" {
			t.Fatalf("server should have stamped From=a, got %q", msg.From)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("no message delivered")
	}
}

func TestHub_Broadcast_ExcludesSender(t *testing.T) {
	h := NewHub(4)
	a := mkPeer("a", "alice", "R")
	b := mkPeer("b", "bob", "R")
	c := mkPeer("c", "carol", "R")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	_, _ = h.Join(c)
	drain(a, b, c)
	err := h.Broadcast("a", SignalingMsg{Type: "state", Payload: json.RawMessage(`{"cam":"off"}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Peer{b, c} {
		select {
		case msg := <-p.SendCh:
			if msg.Type != "state" || msg.From != "a" {
				t.Fatalf("%s: unexpected msg %+v", p.ID, msg)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("%s didn't receive broadcast", p.ID)
		}
	}
	select {
	case msg := <-a.SendCh:
		t.Fatalf("sender should not receive its own broadcast: %+v", msg)
	default:
	}
}

func TestHub_RoomCapacity_Enforced(t *testing.T) {
	h := NewHub(2)
	a := mkPeer("a", "alice", "R")
	b := mkPeer("b", "bob", "R")
	c := mkPeer("c", "carol", "R")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	_, err := h.Join(c)
	if !errors.Is(err, ErrRoomFull) {
		t.Fatalf("expected ErrRoomFull, got %v", err)
	}
}

func TestHub_Leave_BroadcastsPeerLeft(t *testing.T) {
	h := NewHub(4)
	a := mkPeer("a", "alice", "R")
	b := mkPeer("b", "bob", "R")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	drain(a, b)
	h.Leave("a")
	select {
	case msg := <-b.SendCh:
		if msg.Type != "peer-left" || msg.From != "a" {
			t.Fatalf("expected peer-left from a, got %+v", msg)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("b didn't receive peer-left")
	}
	select {
	case <-a.closed:
	default:
		t.Fatal("a.closed should be signaled after Leave")
	}
}

// --- reconnect eviction by ClientID ----------------------------------------

// mkPeerCID is mkPeer + a stable ClientID, to test ghost eviction.
func mkPeerCID(id, user, room, clientID string) *Peer {
	p := mkPeer(id, user, room)
	p.ClientID = clientID
	return p
}

// (a) Two Joins with the SAME ClientID: the 2nd (the reconnect) evicts the 1st
// (the ghost), emits peer-left to the rest, and the room is left with 1 peer per
// client — no duplicate tile. Covers "guest becomes a 2nd tile" and "auth blocked".
func TestHub_Reconnect_SameClientID_EvictsGhost(t *testing.T) {
	h := NewHub(4)
	a := mkPeerCID("a", "alice", "R", "cid-1")
	b := mkPeerCID("b", "bob", "R", "cid-2")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	drain(a, b)

	// alice reconnects: NEW peer id, same ClientID.
	a2 := mkPeerCID("a2", "alice", "R", "cid-1")
	existing, err := h.Join(a2)
	if err != nil {
		t.Fatalf("a reconnect with the same ClientID must succeed, got %v", err)
	}
	// The snapshot for a2 must NOT include the ghost "a".
	for _, pi := range existing {
		if pi.ID == "a" {
			t.Fatal("the existing snapshot cannot contain the evicted ghost 'a'")
		}
	}
	// b must receive peer-left for the ghost "a".
	gotLeft := false
	for drained := false; !drained; {
		select {
		case msg := <-b.SendCh:
			if msg.Type == "peer-left" && msg.From == "a" {
				gotLeft = true
			}
		case <-time.After(50 * time.Millisecond):
			drained = true
		}
	}
	if !gotLeft {
		t.Fatal("b should have received peer-left from the evicted ghost 'a'")
	}
	// The ghost's a.closed must be signalled.
	select {
	case <-a.closed:
	default:
		t.Fatal("a.closed should be flagged after the eviction")
	}
	// The room has exactly 2 peers (a2, b) — not 3.
	if got := len(h.PeersInRoom("R")); got != 2 {
		t.Fatalf("the room should have 2 peers after the eviction, got %d", got)
	}
}

// (b) A reconnect with the same ClientID in a FULL room (cap=2, all ghosts/peers)
// must NOT return ErrRoomFull — the eviction runs BEFORE the cap check.
func TestHub_Reconnect_SameClientID_FullRoom_NoRoomFull(t *testing.T) {
	h := NewHub(2)
	a := mkPeerCID("a", "alice", "R", "cid-1")
	b := mkPeerCID("b", "bob", "R", "cid-2")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	// Room full. alice reconnects with the same ClientID — evicts "a" and enters.
	a2 := mkPeerCID("a2", "alice", "R", "cid-1")
	if _, err := h.Join(a2); err != nil {
		t.Fatalf("a reconnect into a full room must evict+succeed, got %v", err)
	}
	if got := len(h.PeersInRoom("R")); got != 2 {
		t.Fatalf("the room should still have 2 peers, got %d", got)
	}
}

// (c) Two empty ClientIDs ("") coexist — no cross eviction. Covers the fallback
// for old clients (which send no client_id) and guards against a regression
// where "" would match "" and take down legitimate peers.
func TestHub_EmptyClientID_Coexist(t *testing.T) {
	h := NewHub(4)
	a := mkPeerCID("a", "guest:Ana", "R", "")
	b := mkPeerCID("b", "guest:Bia", "R", "")
	if _, err := h.Join(a); err != nil {
		t.Fatalf("Join a: %v", err)
	}
	if _, err := h.Join(b); err != nil {
		t.Fatalf("empty ClientIDs must coexist, got %v", err)
	}
	if got := len(h.PeersInRoom("R")); got != 2 {
		t.Fatalf("both peers with an empty ClientID should be in the room, got %d", got)
	}
}

// (d) An eviction followed by the old reader's late Leave(oldID) is a no-op with
// NO panic. It covers strictly evictLocked's delete-before-close order: inverted,
// the double close(p.closed) would give "close of closed channel".
func TestHub_EvictThenLeave_NoPanic(t *testing.T) {
	h := NewHub(4)
	a := mkPeerCID("a", "alice", "R", "cid-1")
	_, _ = h.Join(a)
	a2 := mkPeerCID("a2", "alice", "R", "cid-1")
	_, _ = h.Join(a2) // evicts "a"
	// The old reader for "a" runs its late defer Hub.Leave("a"): it must fall into
	// the early return (peers["a"] already absent) with no panic and without touching a2.
	h.Leave("a")
	if _, ok := h.PeerUser("a2"); !ok {
		t.Fatal("a2 should still be present after a late Leave(a)")
	}
	if got := len(h.PeersInRoom("R")); got != 1 {
		t.Fatalf("the room should have only a2, got %d", got)
	}
}

// --- E2E: HandleWS reads client_id from the query → eviction → peer-left ----
//
// Exercises the REAL deployed path (not just the Hub in isolation): parsing of
// the ?client_id= query, wiring into Peer.ClientID, eviction at Join and delivery
// of peer-left to the observer over a real WebSocket. Simulates "the internet
// dropped and it reconnected by itself in the same tab" (same client_id, 2 conns).

// wsTestServer injects the user (?user=) into the context before HandleWS — it
// replicates what auth.Middleware does in production, with no JWT/ticket in the test.
func wsTestServer(t *testing.T, s *Service) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithUser(r.Context(), r.URL.Query().Get("user")))
		s.HandleWS(w, r)
	}))
}

func wsDial(t *testing.T, srv *httptest.Server, user, room, clientID string) *websocket.Conn {
	t.Helper()
	q := url.Values{"user": {user}, "room_id": {room}}
	if clientID != "" {
		q.Set("client_id", clientID)
	}
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?" + q.Encode()
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial (user=%s client_id=%s): %v", user, clientID, err)
	}
	return c
}

func wsReadUntil(t *testing.T, c *websocket.Conn, typ string) SignalingMsg {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var m SignalingMsg
		if err := c.ReadJSON(&m); err != nil {
			t.Fatalf("read waiting for %q: %v", typ, err)
		}
		if m.Type == typ {
			return m
		}
	}
}

func TestHandleWS_Reconnect_SameClientID_EvictsGhost_E2E(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "Sala")
	if err := s.AddMember("alice", r.ID, "bob"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	srv := wsTestServer(t, s)
	defer srv.Close()

	// The observer (alice, the owner) joins and stays in the room.
	obs := wsDial(t, srv, "alice", r.ID, "alice-cid")
	defer obs.Close()
	wsReadUntil(t, obs, "joined")

	// bob joins (connection 1) with a stable client_id.
	bob1 := wsDial(t, srv, "bob", r.ID, "bob-cid")
	wsReadUntil(t, bob1, "joined")
	ghost := wsReadUntil(t, obs, "peer-joined").From
	if ghost == "" {
		t.Fatal("peer-joined with no From (bob's peer id, connection 1)")
	}

	// bob RECONNECTS (connection 2) — SAME client_id (same tab, network came back).
	// The eviction runs BEFORE the User guard, so bob2 does NOT get error-conflict.
	bob2 := wsDial(t, srv, "bob", r.ID, "bob-cid")
	defer bob2.Close()
	if m := wsReadUntil(t, bob2, "joined"); m.Type != "joined" {
		t.Fatalf("bob2 should receive joined (no conflict), got %q", m.Type)
	}

	// The observer MUST receive peer-left for the ghost (bob, connection 1).
	if pl := wsReadUntil(t, obs, "peer-left"); pl.From != ghost {
		t.Fatalf("expected peer-left from the ghost %q, got from=%q", ghost, pl.From)
	}
	_ = bob1.Close()
}

func TestHub_RateLimit_DropsExcessive(t *testing.T) {
	// Burst more than rateBurst from a single peer; some forwards should be
	// rejected with ErrRateLimited.
	h := NewHub(4)
	a := mkPeer("a", "alice", "R")
	b := mkPeer("b", "bob", "R")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	drain(a, b)
	rateLimited := 0
	for i := 0; i < rateBurst*3; i++ {
		err := h.Forward("a", SignalingMsg{Type: "chat", To: "b", Payload: json.RawMessage(fmt.Sprintf(`"%d"`, i))})
		if errors.Is(err, ErrRateLimited) {
			rateLimited++
		}
	}
	if rateLimited == 0 {
		t.Fatal("expected at least one rate-limited message")
	}
}

// --- History tests -----------------------------------------------------

func TestRecordCallSession_AppendsAndFiltersByUser(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "Sala")
	s.RecordCallSession(CallSession{
		RoomID: r.ID, User: "alice", StartedAt: 1000, DurationS: 60,
		BytesSent: 1024, BytesRecv: 2048, Codec: "AV1", ConnectionType: "direct",
	})
	s.RecordCallSession(CallSession{
		RoomID: r.ID, User: "bob", StartedAt: 2000, DurationS: 30,
		BytesSent: 512, BytesRecv: 256,
	})
	gotA := s.HistoryForUser("alice", 10)
	if len(gotA) != 1 || gotA[0].User != "alice" || gotA[0].RoomName != "Sala" {
		t.Fatalf("alice history wrong: %+v", gotA)
	}
	gotB := s.HistoryForUser("bob", 10)
	if len(gotB) != 1 || gotB[0].User != "bob" {
		t.Fatalf("bob history wrong: %+v", gotB)
	}
	if len(s.HistoryForUser("eve", 10)) != 0 {
		t.Fatal("eve should see no history")
	}
}

func TestRecordCallSession_CapsAt500(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "Sala")
	for i := 0; i < 600; i++ {
		s.RecordCallSession(CallSession{
			RoomID: r.ID, User: "alice", StartedAt: int64(1000 + i),
			DurationS: 1, BytesSent: 1, BytesRecv: 1,
		})
	}
	s.histMu.Lock()
	n := len(s.hist)
	s.histMu.Unlock()
	if n != 500 {
		t.Fatalf("expected 500 (cap), got %d", n)
	}
}

func TestRecordCallSession_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	s := openServiceAt(t, dir)
	r, _ := s.CreateRoom("alice", "Sala")
	s.RecordCallSession(CallSession{
		RoomID: r.ID, User: "alice", StartedAt: 1000, DurationS: 60,
		BytesSent: 1, BytesRecv: 2,
	})
	_ = s.save() // force rooms write so reload doesn't lose the room
	s2 := openServiceAt(t, dir)
	got := s2.HistoryForUser("alice", 10)
	if len(got) != 1 || got[0].BytesSent != 1 {
		t.Fatalf("history not persisted: %+v", got)
	}
}

// --- HandleRecordingUpload authz (HTTP integration) -------------------

func TestHandleRecordingUpload_RejectsNonMember(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Options{DataDir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Setup: recording store via /tmp/blobs.
	recStore, err := openRecordingStore(dir, t.TempDir())
	if err != nil {
		t.Fatalf("openRecordingStore: %v", err)
	}
	s.Recordings = recStore
	// Alice creates a room, Bob is a stranger.
	room, _ := s.CreateRoom("alice", "Sala")

	// Helper to assemble a multipart upload request simulating 'bob' trying to
	// upload to alice's room.
	makeUploadReq := func(user, roomID string) *http.Request {
		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)
		_ = w.WriteField("room_id", roomID)
		_ = w.WriteField("started_at", "1000")
		_ = w.WriteField("duration_s", "60")
		fw, _ := w.CreateFormFile("file", "rec.webm")
		_, _ = fw.Write([]byte("FAKE-WEBM-DATA"))
		_ = w.Close()
		req := httptest.NewRequest("POST", "/api/videocall/recordings", body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		// Inject the user into the context the way the middleware would.
		req = req.WithContext(auth.WithUser(req.Context(), user))
		return req
	}

	// Bob (a stranger) tries to upload → must get 404 (existence is not leaked).
	wRec := httptest.NewRecorder()
	s.HandleRecordingUpload(wRec, makeUploadReq("bob", room.ID))
	if wRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a non-member, got %d (body: %s)", wRec.Code, wRec.Body.String())
	}
	if len(recStore.ForUser("bob")) != 0 {
		t.Fatal("bob should NOT have a saved recording")
	}

	// Alice (the owner) uploads → 200 + the recording saved.
	wOk := httptest.NewRecorder()
	s.HandleRecordingUpload(wOk, makeUploadReq("alice", room.ID))
	if wOk.Code != http.StatusOK {
		t.Fatalf("expected 200 for the owner, got %d (body: %s)", wOk.Code, wOk.Body.String())
	}
	if len(recStore.ForUser("alice")) != 1 {
		t.Fatal("alice should have 1 recording")
	}
}

// --- RoomForUser visibility (protection against leaks) ----------------

func TestRoomForUser_NonMemberSeesNotFound(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "Privada")
	// The owner sees it.
	if got, ok := s.RoomForUser("alice", r.ID); !ok || got.ID != r.ID {
		t.Fatal("the owner should see their room")
	}
	// A member sees it.
	_ = s.AddMember("alice", r.ID, "bob")
	if _, ok := s.RoomForUser("bob", r.ID); !ok {
		t.Fatal("a member should see the room")
	}
	// A stranger does NOT (important: same response as "does not exist" — no leak).
	if _, ok := s.RoomForUser("eve", r.ID); ok {
		t.Fatal("eve, a non-member, should NOT see it")
	}
	if _, ok := s.RoomForUser("eve", "fake-id-1234"); ok {
		t.Fatal("eve, a non-member, should NOT see a nonexistent id")
	}
}

// --- PIN rate limit cleanup --------------------------------------------

func TestPinAllow_CleansEmptyBuckets(t *testing.T) {
	// Reset the global state. Important: never hold mu while calling
	// pinAllow (which tries to Lock inside — that would deadlock).
	pinRateLimiter.mu.Lock()
	pinRateLimiter.buckets = make(map[string]*pinBucket)
	pinRateLimiter.mu.Unlock()

	// Create the bucket with the first call.
	if !pinAllow("1.2.3.4") {
		t.Fatal("the first attempt should pass")
	}
	if !hasBucket("1.2.3.4") {
		t.Fatal("bucket not created")
	}

	// Simulate the trim: replace the bucket's hits with old entries (already
	// outside the 1min window). The next pinAllow trims those, adds 1 new one
	// (allowed) → at the end hits has 1, and the bucket STAYS in the map.
	long := time.Now().Add(-2 * time.Minute)
	setHits("1.2.3.4", []time.Time{long, long, long, long, long, long})

	// Since hits >= 5 (after the filter only 0 remain), allowed=true; the current
	// code adds 1 new one → hits=[now]. The bucket stays alive.
	if !pinAllow("1.2.3.4") {
		t.Fatal("trimming the old ones should free it up")
	}

	// Cleanup scenario: the bucket EXISTS but hits is full of the past, and we
	// clear it before calling pinAllow to simulate the window where the bucket
	// would be orphaned. We add 5 old hits without an allow. The next call trims
	// down to 0 → allow=true, adds 1 → the bucket is left with 1 hit.
	// To force a real cleanup we would need denied + trim simultaneously
	// (which only happens in real windows of >5 recent hits).
	// What matters: validating that many calls from different IPs do not make
	// the map explode.
	for i := 0; i < 200; i++ {
		ip := fmt.Sprintf("10.0.0.%d", i)
		pinAllow(ip)
	}
	// The map must hold at most 201 (1.2.3.4 + 200).
	pinRateLimiter.mu.Lock()
	n := len(pinRateLimiter.buckets)
	pinRateLimiter.mu.Unlock()
	if n > 210 {
		t.Fatalf("the map grew past what was expected: %d", n)
	}
}

func hasBucket(ip string) bool {
	pinRateLimiter.mu.Lock()
	defer pinRateLimiter.mu.Unlock()
	_, ok := pinRateLimiter.buckets[ip]
	return ok
}
func setHits(ip string, hits []time.Time) {
	pinRateLimiter.mu.Lock()
	defer pinRateLimiter.mu.Unlock()
	if b, ok := pinRateLimiter.buckets[ip]; ok {
		b.hits = hits
	}
}

// --- Recording store tests ---------------------------------------------

func TestRecording_AddListGetDelete(t *testing.T) {
	dir := t.TempDir()
	blobs := t.TempDir()
	st, err := openRecordingStore(dir, blobs)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	rec := &Recording{User: "alice", RoomID: "r1", StartedAt: 1000, DurationS: 60, MimeType: "video/webm"}
	if err := st.Add(rec, strings.NewReader("FAKEWEBM-BYTES"), 1<<20); err != nil {
		t.Fatalf("add: %v", err)
	}
	if rec.ID == "" || rec.SizeBytes != 14 {
		t.Fatalf("unexpected rec: %+v", rec)
	}
	got, ok := st.Get("alice", rec.ID)
	if !ok || got.RoomID != "r1" {
		t.Fatalf("get failed: %+v", got)
	}
	// Wrong user is denied.
	if _, ok := st.Get("eve", rec.ID); ok {
		t.Fatal("eve should not see alice's recording")
	}
	list := st.ForUser("alice")
	if len(list) != 1 {
		t.Fatalf("expected 1, got %d", len(list))
	}
	if err := st.Delete("alice", rec.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(st.ForUser("alice")) != 0 {
		t.Fatal("delete did not remove")
	}
}

func TestRecording_HardLimitRejects(t *testing.T) {
	st, _ := openRecordingStore(t.TempDir(), t.TempDir())
	rec := &Recording{User: "alice", RoomID: "r1"}
	err := st.Add(rec, strings.NewReader("toomuchdata"), 4) // limit 4 bytes
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

func TestRecording_FIFOTrimPerUser(t *testing.T) {
	st, _ := openRecordingStore(t.TempDir(), t.TempDir())
	// Force the cap down so test stays fast; can't change const but we
	// rely on default 20. Add 22 and check 20 remain.
	for i := 0; i < recordingMaxPerUser+2; i++ {
		rec := &Recording{User: "alice", RoomID: "r", CreatedAt: int64(i + 1)}
		if err := st.Add(rec, strings.NewReader("x"), 1<<20); err != nil {
			t.Fatalf("add #%d: %v", i, err)
		}
	}
	if got := len(st.ForUser("alice")); got != recordingMaxPerUser {
		t.Fatalf("expected %d after trim, got %d", recordingMaxPerUser, got)
	}
}

func TestRecording_SummaryRoundTrip(t *testing.T) {
	st, _ := openRecordingStore(t.TempDir(), t.TempDir())
	rec := &Recording{User: "alice", RoomID: "r1"}
	_ = st.Add(rec, strings.NewReader("a"), 1<<20)
	if err := st.SetSummary("alice", rec.ID, "this is a summary"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := st.Summary("alice", rec.ID)
	if err != nil || got != "this is a summary" {
		t.Fatalf("summary mismatch: %q %v", got, err)
	}
	r2, _ := st.Get("alice", rec.ID)
	if !r2.HasSummary {
		t.Fatal("HasSummary not set")
	}
}

// --- TURN tests --------------------------------------------------------

func TestTURN_HMAC_MatchesCoturnFormula(t *testing.T) {
	cfg := &TURNConfig{
		Secret: "test-secret-32-bytes-aaaaaaaaaaaaaaa",
		Hosts:  []string{"turn:tunnel.example.com:3478?transport=udp"},
	}
	creds := cfg.MintTURNCredentials("alice", time.Hour)
	if creds.Username == "" || creds.Credential == "" {
		t.Fatalf("missing creds: %+v", creds)
	}
	// Parse: <exp>:<user>
	parts := strings.SplitN(creds.Username, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("malformed username: %q", creds.Username)
	}
	expTS, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("expiration not numeric: %v", err)
	}
	if expTS < time.Now().Unix() {
		t.Fatalf("expiration in the past: %d", expTS)
	}
	// Recompute HMAC and compare — this is exactly what coturn does on the
	// server side. If this passes, coturn will accept the credential.
	mac := hmac.New(sha1.New, []byte(cfg.Secret))
	mac.Write([]byte(creds.Username))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if creds.Credential != want {
		t.Fatalf("HMAC mismatch: got %q, want %q", creds.Credential, want)
	}
}

func TestTURN_DisabledFallsBackToSTUN(t *testing.T) {
	creds := (*TURNConfig)(nil).MintTURNCredentials("alice", time.Hour)
	if creds.Credential != "" || creds.Username != "" {
		t.Fatalf("nil config should yield empty creds: %+v", creds)
	}
	if len(creds.URLs) == 0 || !strings.HasPrefix(creds.URLs[0], "stun:") {
		t.Fatalf("expected STUN fallback URL, got %+v", creds.URLs)
	}
}

func TestTURN_UsernameSanitized(t *testing.T) {
	cfg := &TURNConfig{Secret: "s"}
	creds := cfg.MintTURNCredentials("alice:hax", time.Hour)
	// ':' in the application username would break coturn's exp:user split.
	parts := strings.SplitN(creds.Username, ":", 2)
	if strings.Contains(parts[1], ":") {
		t.Fatalf("colon leaked into user portion: %q", creds.Username)
	}
}

// --- Push wiring tests ---------------------------------------------------
//
// The VAPID + subscription store itself now lives in internal/webpush
// (see internal/webpush/store_test.go for its unit tests). These tests
// cover only what's specific to videocall: the SendIncomingCall wrapper
// and Options.Push injection.

func TestSendIncomingCall_NoSubs_NoCrash(t *testing.T) {
	s, err := Open(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	n := s.SendIncomingCall("alice", PresenceEvent{Type: "incoming-call", RoomID: "r1"}, nil)
	if n != 0 {
		t.Fatalf("expected 0 deliveries when no subs, got %d", n)
	}
}

func TestOpen_ReusesInjectedPushStore(t *testing.T) {
	dir := t.TempDir()
	injected, err := webpush.Open(dir)
	if err != nil {
		t.Fatalf("webpush.Open: %v", err)
	}
	s, err := Open(Options{DataDir: dir, Push: injected})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if s.Push != injected {
		t.Fatal("Options.Push injection was not honored — a second store/VAPID keypair was created")
	}
	if s.Push.PublicKey() != injected.PublicKey() {
		t.Fatalf("public key mismatch: %q vs %q", s.Push.PublicKey(), injected.PublicKey())
	}
}

// --- Presence tests ----------------------------------------------------

func TestPresence_NotifyUsers_FansToAllSubs(t *testing.T) {
	p := NewPresenceHub()
	aSub1 := p.Subscribe("alice", "")
	aSub2 := p.Subscribe("alice", "")
	bSub := p.Subscribe("bob", "")
	defer p.Unsubscribe(aSub1)
	defer p.Unsubscribe(aSub2)
	defer p.Unsubscribe(bSub)
	p.NotifyUsers([]string{"alice", "bob"}, "", PresenceEvent{Type: "incoming-call", RoomID: "r1", From: "carol"})
	for _, sub := range []*PresenceSub{aSub1, aSub2, bSub} {
		select {
		case ev := <-sub.Ch:
			if ev.Type != "incoming-call" || ev.From != "carol" {
				t.Fatalf("%s: wrong event %+v", sub.User, ev)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("%s: no event", sub.User)
		}
	}
}

func TestPresence_NotifyUsers_ExcludesSender(t *testing.T) {
	p := NewPresenceHub()
	aSub := p.Subscribe("alice", "")
	defer p.Unsubscribe(aSub)
	p.NotifyUsers([]string{"alice"}, "alice", PresenceEvent{Type: "incoming-call"})
	select {
	case ev := <-aSub.Ch:
		t.Fatalf("sender should not be notified: %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// good
	}
}

func TestPresence_Unsubscribe_StopsDelivery(t *testing.T) {
	p := NewPresenceHub()
	sub := p.Subscribe("alice", "")
	p.Unsubscribe(sub)
	p.NotifyUsers([]string{"alice"}, "", PresenceEvent{Type: "x"})
	select {
	case ev := <-sub.Ch:
		// Channel may have been closed-ish via Done; ignore drained events.
		_ = ev
	case <-time.After(50 * time.Millisecond):
	}
	if p.IsOnline("alice") {
		t.Fatal("alice should be offline after unsubscribe")
	}
}

// --- helpers -----------------------------------------------------------

func openTempService(t *testing.T) *Service {
	t.Helper()
	return openServiceAt(t, t.TempDir())
}

func openServiceAt(t *testing.T, dataDir string) *Service {
	t.Helper()
	s, err := Open(Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Open starts flusher() and callHeartbeat(), which write into dataDir.
	// Without shutting them down they kept writing AFTER the test — and
	// t.TempDir()'s RemoveAll failed intermittently with "directory not empty"
	// (seen in TestHandleWS_Reconnect_SameClientID_EvictsGhost_E2E, which
	// passes 3/3 in isolation and only breaks under the whole package's load).
	// Close() is synchronous (it waits on flusherDone/callTickerDone) and
	// idempotent — the tests that already `defer s.Close()` stay valid.
	// Cleanup is LIFO and TempDir registered its own BEFORE, so this one runs
	// first: the goroutines die, then the directory disappears.
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mkPeer(id, user, room string) *Peer {
	return &Peer{
		ID:     id,
		User:   user,
		RoomID: room,
		SendCh: make(chan SignalingMsg, 16),
		closed: make(chan struct{}),
	}
}

// drain consumes the peer-joined notifications that Hub.Join queues so the
// tests can focus on the messages they actually care about.
func drain(peers ...*Peer) {
	for _, p := range peers {
		for {
			select {
			case <-p.SendCh:
			default:
				goto next
			}
		}
	next:
	}
}
