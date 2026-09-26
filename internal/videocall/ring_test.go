package videocall

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"server-control-panel/internal/notify/fcmpush"
	"server-control-panel/internal/webpush"
)

// fcmCall records one SendDataToUser invocation observed by fakeFCMSender.
type fcmCall struct {
	user    string
	data    map[string]string
	opts    fcmpush.DataOptions
	allowed bool // result of allowDevice("probe-device"), if allowDevice != nil
}

// fakeFCMSender implements the videocall-local fcmRingSender interface.
// fcmpush.Sender's fields are all unexported (white-box test construction
// only works from within the fcmpush package itself), so a real Sender
// cannot be faked from here — this is why announceJoin/broadcastCallEnded
// depend on the fcmRingSender interface instead of the concrete type.
type fakeFCMSender struct {
	calls chan fcmCall
}

func newFakeFCMSender() *fakeFCMSender {
	return &fakeFCMSender{calls: make(chan fcmCall, 8)}
}

func (f *fakeFCMSender) SendDataToUser(_ context.Context, user string, data map[string]string, opts fcmpush.DataOptions, allowDevice func(deviceID string) bool) int {
	allowed := true
	if allowDevice != nil {
		allowed = allowDevice("probe-device")
	}
	f.calls <- fcmCall{user: user, data: data, opts: opts, allowed: allowed}
	if allowed {
		return 1
	}
	return 0
}

func waitFCMCall(t *testing.T, ch chan fcmCall, d time.Duration) (fcmCall, bool) {
	t.Helper()
	select {
	case c := <-ch:
		return c, true
	case <-time.After(d):
		return fcmCall{}, false
	}
}

func expectNoFCMCall(t *testing.T, ch chan fcmCall, d time.Duration) {
	t.Helper()
	select {
	case c := <-ch:
		t.Fatalf("FCM should not have been called: %+v", c)
	case <-time.After(d):
	}
}

// genPushKeys builds a real EC P-256 keypair + 16-byte auth secret in
// base64url form — webpush-go's real ECDH+HKDF path rejects malformed keys
// before ever reaching the network, so a fixture must carry valid ones.
// Duplicated from internal/webpush/store_test.go's genSubscriberKeys
// (unexported, package-private there) — same precedent as push.go's
// atomicWriteJSON doc comment for this codebase's intentional small
// cross-package duplications.
func genPushKeys(t *testing.T) webpush.PushSubscriptionKeys {
	t.Helper()
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	pub := elliptic.Marshal(elliptic.P256(), x, y)
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("genauth: %v", err)
	}
	return webpush.PushSubscriptionKeys{
		P256dh: base64.RawURLEncoding.EncodeToString(pub),
		Auth:   base64.RawURLEncoding.EncodeToString(auth),
	}
}

// TestAnnounceJoin_FansOutToBothPushAndFCM proves the FCM fan-out this plan
// adds is genuinely ADDITIVE: it fires alongside the pre-existing Web Push
// fan-out, for the same recipient, without either one suppressing the
// other — a native Android device and a browser both get reached from one
// ring.
func TestAnnounceJoin_FansOutToBothPushAndFCM(t *testing.T) {
	var pushHits int
	pushSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pushHits++
		w.WriteHeader(http.StatusCreated)
	}))
	defer pushSrv.Close()

	dir := t.TempDir()
	pushStore, err := webpush.Open(dir)
	if err != nil {
		t.Fatalf("webpush.Open: %v", err)
	}
	pushStore.Add(&webpush.PushSubscription{
		User:     "jordan",
		Endpoint: pushSrv.URL + "/sub1",
		Keys:     genPushKeys(t),
		DeviceID: "jordan-phone",
	})

	fcm := newFakeFCMSender()
	s, err := Open(Options{DataDir: dir, Push: pushStore, FCM: fcm})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	room, err := s.CreateRoom("sam", "Sala")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("sam", room.ID, "jordan"); err != nil {
		t.Fatal(err)
	}
	// jordan has no in-tab presence sub at all — WantsRing("jordan") is false,
	// so BOTH off-app paths (Push and FCM) must fire for her.
	s.announceJoin(room.ID, "sam", "Sam", "cid-sam", false)

	call, ok := waitFCMCall(t, fcm.calls, 2*time.Second)
	if !ok {
		t.Fatal("FCM SendDataToUser was not called — the fan-out did not happen")
	}
	if call.user != "jordan" {
		t.Fatalf("FCM user = %q, want jordan", call.user)
	}
	if call.data["type"] != "incoming-call" || call.data["room_id"] != room.ID {
		t.Fatalf("FCM data = %#v, want type=incoming-call room_id=%s", call.data, room.ID)
	}
	if call.data["call_id"] == "" {
		t.Fatal("FCM data must carry call_id — it is what lets a later cancel correlate with this ring")
	}
	if call.opts.CollapseKey != "call-"+room.ID {
		t.Fatalf("FCM CollapseKey = %q, want call-%s", call.opts.CollapseKey, room.ID)
	}

	deadline := time.Now().Add(2 * time.Second)
	for pushHits == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if pushHits == 0 {
		t.Fatal("the Web Push endpoint was not called — the existing fan-out regressed")
	}
}

// TestAnnounceJoin_FCMNilDoesNotPanic proves Service.FCM is optional — a
// Service opened without an FCM credential provisioned (the common case
// until Phase 6's vault secret is set) must ring exactly as before, with no
// FCM call attempted and no panic.
func TestAnnounceJoin_FCMNilDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(Options{DataDir: dir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if s.FCM != nil {
		t.Fatal("Options{} without FCM must leave Service.FCM nil")
	}

	room, err := s.CreateRoom("sam", "Sala")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("sam", room.ID, "jordan"); err != nil {
		t.Fatal(err)
	}

	// Must not panic.
	s.announceJoin(room.ID, "sam", "Sam", "cid-sam", false)
}

// TestBroadcastCallEnded_SendsFCMCancel proves the call_id/cancellation gap
// this plan documents is actually closed on the server side: when a call
// ends, every FCM-reachable recipient gets a data-only "call-ended" push
// carrying the same call_id and collapse_key as the original ring, so the
// native client can correlate and cancel/clear the ringing UI instead of
// being stuck ringing forever.
func TestBroadcastCallEnded_SendsFCMCancel(t *testing.T) {
	dir := t.TempDir()
	fcm := newFakeFCMSender()
	s, err := Open(Options{DataDir: dir, FCM: fcm})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	room, err := s.CreateRoom("sam", "Sala")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("sam", room.ID, "jordan"); err != nil {
		t.Fatal(err)
	}

	s.broadcastCallEnded(room.ID, "call-123")

	seen := map[string]fcmCall{}
	for i := 0; i < 2; i++ {
		c, ok := waitFCMCall(t, fcm.calls, time.Second)
		if !ok {
			break
		}
		seen[c.user] = c
	}
	for _, want := range []string{"sam", "jordan"} {
		c, ok := seen[want]
		if !ok {
			t.Fatalf("the FCM cancel did not reach %s: %#v", want, seen)
		}
		if c.data["type"] != "call-ended" || c.data["call_id"] != "call-123" {
			t.Fatalf("FCM cancel data for %s = %#v, want type=call-ended call_id=call-123", want, c.data)
		}
		if c.opts.CollapseKey != "call-"+room.ID {
			t.Fatalf("FCM cancel CollapseKey for %s = %q, want call-%s", want, c.opts.CollapseKey, room.ID)
		}
	}
}
