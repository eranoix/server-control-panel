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
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"server-control-panel/internal/webpush"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
)

func TestCreateRoom_AssignsIDAndPersists(t *testing.T) {
	s := openTempService(t)
	r, err := s.CreateRoom("alice", "Living Room")
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if r.ID == "" || r.Owner != "alice" || r.Name != "Living Room" {
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
	r, _ := s.CreateRoom("alice", "Old")
	if err := s.RenameRoom("eve", r.ID, "Hack"); err == nil {
		t.Fatal("eve should not rename")
	}
	if err := s.RenameRoom("alice", r.ID, "Nova"); err != nil {
		t.Fatalf("alice rename: %v", err)
	}
	got, _ := s.Room(r.ID)
	if got.Name != "Nova" {
		t.Fatalf("expected Nova, got %q", got.Name)
	}
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

	err = h.Forward("a", SignalingMsg{Type: "offer", To: "c", Payload: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrTargetNotInRoom) {
		t.Fatalf("expected ErrTargetNotInRoom, got %v", err)
	}
	select {
	case msg := <-c.SendCh:
		t.Fatalf("c should NOT have received cross-room offer: %+v", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHub_Forward_StampsFrom_IgnoresClientFrom(t *testing.T) {
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

func mkPeerCID(id, user, room, clientID string) *Peer {
	p := mkPeer(id, user, room)
	p.ClientID = clientID
	return p
}

func TestHub_Reconnect_SameClientID_EvictsGhost(t *testing.T) {
	h := NewHub(4)
	a := mkPeerCID("a", "alice", "R", "cid-1")
	b := mkPeerCID("b", "bob", "R", "cid-2")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	drain(a, b)

	a2 := mkPeerCID("a2", "alice", "R", "cid-1")
	existing, err := h.Join(a2)
	if err != nil {
		t.Fatalf("a reconnect with the same ClientID must succeed, got %v", err)
	}
	for _, pi := range existing {
		if pi.ID == "a" {
			t.Fatal("the existing snapshot cannot contain the evicted ghost 'a'")
		}
	}
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
	select {
	case <-a.closed:
	default:
		t.Fatal("a.closed should be flagged after the eviction")
	}
	if got := len(h.PeersInRoom("R")); got != 2 {
		t.Fatalf("the room should have 2 peers after the eviction, got %d", got)
	}
}

func TestHub_Reconnect_SameClientID_FullRoom_NoRoomFull(t *testing.T) {
	h := NewHub(2)
	a := mkPeerCID("a", "alice", "R", "cid-1")
	b := mkPeerCID("b", "bob", "R", "cid-2")
	_, _ = h.Join(a)
	_, _ = h.Join(b)
	a2 := mkPeerCID("a2", "alice", "R", "cid-1")
	if _, err := h.Join(a2); err != nil {
		t.Fatalf("a reconnect into a full room must evict+succeed, got %v", err)
	}
	if got := len(h.PeersInRoom("R")); got != 2 {
		t.Fatalf("the room should still have 2 peers, got %d", got)
	}
}

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

func TestHub_EvictThenLeave_NoPanic(t *testing.T) {
	h := NewHub(4)
	a := mkPeerCID("a", "alice", "R", "cid-1")
	_, _ = h.Join(a)
	a2 := mkPeerCID("a2", "alice", "R", "cid-1")
	_, _ = h.Join(a2)
	h.Leave("a")
	if _, ok := h.PeerUser("a2"); !ok {
		t.Fatal("a2 should still be present after a late Leave(a)")
	}
	if got := len(h.PeersInRoom("R")); got != 1 {
		t.Fatalf("the room should have only a2, got %d", got)
	}
}

func wsTestServer(t *testing.T, s *Service) *httptest.Server {
	t.Helper()
	var (
		handlers sync.WaitGroup
		connsMu  sync.Mutex
		hijacked []net.Conn
	)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlers.Add(1)
		defer handlers.Done()
		r = r.WithContext(auth.WithUser(r.Context(), r.URL.Query().Get("user")))
		s.HandleWS(w, r)
	}))
	srv.Config.ConnState = func(c net.Conn, state http.ConnState) {
		if state == http.StateHijacked {
			connsMu.Lock()
			hijacked = append(hijacked, c)
			connsMu.Unlock()
		}
	}
	srv.Start()
	t.Cleanup(func() {
		srv.Close()
		connsMu.Lock()
		for _, c := range hijacked {
			_ = c.Close()
		}
		connsMu.Unlock()
		handlers.Wait()
	})
	return srv
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
	r, _ := s.CreateRoom("alice", "Room")
	if err := s.AddMember("alice", r.ID, "bob"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}
	srv := wsTestServer(t, s)
	defer srv.Close()

	obs := wsDial(t, srv, "alice", r.ID, "alice-cid")
	defer obs.Close()
	wsReadUntil(t, obs, "joined")

	bob1 := wsDial(t, srv, "bob", r.ID, "bob-cid")
	wsReadUntil(t, bob1, "joined")
	ghost := wsReadUntil(t, obs, "peer-joined").From
	if ghost == "" {
		t.Fatal("peer-joined with no From (bob's peer id, connection 1)")
	}

	bob2 := wsDial(t, srv, "bob", r.ID, "bob-cid")
	defer bob2.Close()
	if m := wsReadUntil(t, bob2, "joined"); m.Type != "joined" {
		t.Fatalf("bob2 should receive joined (no conflict), got %q", m.Type)
	}

	if pl := wsReadUntil(t, obs, "peer-left"); pl.From != ghost {
		t.Fatalf("expected peer-left from the ghost %q, got from=%q", ghost, pl.From)
	}
	_ = bob1.Close()
}

func TestHub_RateLimit_DropsExcessive(t *testing.T) {
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

func TestRecordCallSession_AppendsAndFiltersByUser(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "Room")
	s.RecordCallSession(CallSession{
		RoomID: r.ID, User: "alice", StartedAt: 1000, DurationS: 60,
		BytesSent: 1024, BytesRecv: 2048, Codec: "AV1", ConnectionType: "direct",
	})
	s.RecordCallSession(CallSession{
		RoomID: r.ID, User: "bob", StartedAt: 2000, DurationS: 30,
		BytesSent: 512, BytesRecv: 256,
	})
	gotA := s.HistoryForUser("alice", 10)
	if len(gotA) != 1 || gotA[0].User != "alice" || gotA[0].RoomName != "Room" {
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
	r, _ := s.CreateRoom("alice", "Room")
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
	r, _ := s.CreateRoom("alice", "Room")
	s.RecordCallSession(CallSession{
		RoomID: r.ID, User: "alice", StartedAt: 1000, DurationS: 60,
		BytesSent: 1, BytesRecv: 2,
	})
	_ = s.save()
	s2 := openServiceAt(t, dir)
	got := s2.HistoryForUser("alice", 10)
	if len(got) != 1 || got[0].BytesSent != 1 {
		t.Fatalf("history not persisted: %+v", got)
	}
}

func TestHandleRecordingUpload_RejectsNonMember(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Options{DataDir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	recStore, err := openRecordingStore(dir, t.TempDir())
	if err != nil {
		t.Fatalf("openRecordingStore: %v", err)
	}
	s.Recordings = recStore
	room, _ := s.CreateRoom("alice", "Room")

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
		req = req.WithContext(auth.WithUser(req.Context(), user))
		return req
	}

	wRec := httptest.NewRecorder()
	s.HandleRecordingUpload(wRec, makeUploadReq("bob", room.ID))
	if wRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a non-member, got %d (body: %s)", wRec.Code, wRec.Body.String())
	}
	if len(recStore.ForUser("bob")) != 0 {
		t.Fatal("bob should NOT have a saved recording")
	}

	wOk := httptest.NewRecorder()
	s.HandleRecordingUpload(wOk, makeUploadReq("alice", room.ID))
	if wOk.Code != http.StatusOK {
		t.Fatalf("expected 200 for the owner, got %d (body: %s)", wOk.Code, wOk.Body.String())
	}
	if len(recStore.ForUser("alice")) != 1 {
		t.Fatal("alice should have 1 recording")
	}
}

func TestRoomForUser_NonMemberSeesNotFound(t *testing.T) {
	s := openTempService(t)
	r, _ := s.CreateRoom("alice", "Private")
	if got, ok := s.RoomForUser("alice", r.ID); !ok || got.ID != r.ID {
		t.Fatal("the owner should see their room")
	}
	_ = s.AddMember("alice", r.ID, "bob")
	if _, ok := s.RoomForUser("bob", r.ID); !ok {
		t.Fatal("a member should see the room")
	}
	if _, ok := s.RoomForUser("eve", r.ID); ok {
		t.Fatal("eve, a non-member, should NOT see it")
	}
	if _, ok := s.RoomForUser("eve", "fake-id-1234"); ok {
		t.Fatal("eve, a non-member, should NOT see a nonexistent id")
	}
}

func TestPinAllow_CleansEmptyBuckets(t *testing.T) {
	pinRateLimiter.mu.Lock()
	pinRateLimiter.buckets = make(map[string]*pinBucket)
	pinRateLimiter.mu.Unlock()

	if !pinAllow("1.2.3.4") {
		t.Fatal("the first attempt should pass")
	}
	if !hasBucket("1.2.3.4") {
		t.Fatal("bucket not created")
	}

	long := time.Now().Add(-2 * time.Minute)
	setHits("1.2.3.4", []time.Time{long, long, long, long, long, long})

	if !pinAllow("1.2.3.4") {
		t.Fatal("trimming the old ones should free it up")
	}

	for i := 0; i < 200; i++ {
		ip := fmt.Sprintf("10.0.0.%d", i)
		pinAllow(ip)
	}
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
	err := st.Add(rec, strings.NewReader("toomuchdata"), 4)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

func TestRecording_FIFOTrimPerUser(t *testing.T) {
	st, _ := openRecordingStore(t.TempDir(), t.TempDir())
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

func TestTURN_HMAC_MatchesCoturnFormula(t *testing.T) {
	cfg := &TURNConfig{
		Secret: "test-secret-32-bytes-aaaaaaaaaaaaaaa",
		Hosts:  []string{"turn:tunnel.example.com:3478?transport=udp"},
	}
	creds := cfg.MintTURNCredentials("alice", time.Hour)
	if creds.Username == "" || creds.Credential == "" {
		t.Fatalf("missing creds: %+v", creds)
	}
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
	parts := strings.SplitN(creds.Username, ":", 2)
	if strings.Contains(parts[1], ":") {
		t.Fatalf("colon leaked into user portion: %q", creds.Username)
	}
}

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
	}
}

func TestPresence_Unsubscribe_StopsDelivery(t *testing.T) {
	p := NewPresenceHub()
	sub := p.Subscribe("alice", "")
	p.Unsubscribe(sub)
	p.NotifyUsers([]string{"alice"}, "", PresenceEvent{Type: "x"})
	select {
	case ev := <-sub.Ch:
		_ = ev
	case <-time.After(50 * time.Millisecond):
	}
	if p.IsOnline("alice") {
		t.Fatal("alice should be offline after unsubscribe")
	}
}

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
