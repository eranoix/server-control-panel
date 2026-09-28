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

type fcmCall struct {
	user    string
	data    map[string]string
	opts    fcmpush.DataOptions
	allowed bool
}

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

	room, err := s.CreateRoom("sam", "Room")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("sam", room.ID, "jordan"); err != nil {
		t.Fatal(err)
	}
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

	room, err := s.CreateRoom("sam", "Room")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember("sam", room.ID, "jordan"); err != nil {
		t.Fatal(err)
	}

	s.announceJoin(room.ID, "sam", "Sam", "cid-sam", false)
}

func TestBroadcastCallEnded_SendsFCMCancel(t *testing.T) {
	dir := t.TempDir()
	fcm := newFakeFCMSender()
	s, err := Open(Options{DataDir: dir, FCM: fcm})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	room, err := s.CreateRoom("sam", "Room")
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
