package notify

import (
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"server-control-panel/internal/webpush"
)

// TestPushChannel_EndToEndThroughRouter proves the push channel end to end,
// through the SAME rule/throttle/dedup engine the other five notify channels
// share — not a bespoke path. It builds a real Router, a real ChannelDef +
// Rule via the Alertas-tab CRUD paths (UpsertChannel/UpsertRule), a real
// *webpush.Store with a real EC P-256 subscriber keypair, and Dispatches a
// metric.threshold Event. It asserts the real, encrypted HTTP POST webpush-go
// sends over the wire reaches a fake push endpoint with the aes128gcm
// envelope headers a real push service would require — i.e. the previously
// dead alert-fired payload is now actually published by real Go code.
func TestPushChannel_EndToEndThroughRouter(t *testing.T) {
	var mu sync.Mutex
	var gotReq *http.Request
	var gotBodyLen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		gotReq = req.Clone(req.Context())
		b := make([]byte, 0, 4096)
		buf := make([]byte, 4096)
		for {
			n, err := req.Body.Read(buf)
			b = append(b, buf[:n]...)
			if err != nil {
				break
			}
		}
		gotBodyLen = len(b)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	store, err := webpush.Open(t.TempDir())
	if err != nil {
		t.Fatalf("webpush.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.Add(&webpush.PushSubscription{
		User:     "sam",
		Endpoint: srv.URL + "/ep1",
		Keys:     genSubscriberKeysForTest(t),
	})

	h := newHarness(t, map[string]Channel{
		TypePush: NewPushChannel(NewWebpushSender(store), nil),
	}, Options{})

	ch, err := h.r.UpsertChannel(ChannelDef{
		Name: "Push (teste)", Type: TypePush, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpsertChannel: %v", err)
	}
	if _, err := h.r.UpsertRule(Rule{
		Name: "Métricas → push", Enabled: true,
		TypePrefix: "metric.", Channels: []string{ch.ID},
	}); err != nil {
		t.Fatalf("UpsertRule: %v", err)
	}

	h.r.Dispatch(Event{
		Type:     TypeMetricThreshold,
		Severity: SeverityWarning,
		Source:   "metrics",
		Title:    "Metric: cpu",
		Body:     "valor 95.00 cruzou o limite",
		Labels:   map[string]string{"rule": "cpu"},
		DedupKey: "metric:cpu:1",
	})
	h.wait(t, 1)

	mu.Lock()
	defer mu.Unlock()
	if gotReq == nil {
		t.Fatal("fake push endpoint never received a request — delivery did not reach the wire")
	}
	if enc := gotReq.Header.Get("Content-Encoding"); enc != "aes128gcm" {
		t.Fatalf("Content-Encoding = %q, want aes128gcm (real webpush-go encryption path)", enc)
	}
	if auth := gotReq.Header.Get("Authorization"); auth == "" {
		t.Fatal("missing Authorization header (expected a real VAPID JWT)")
	}
	if gotBodyLen == 0 {
		t.Fatal("empty encrypted body — expected a non-empty aes128gcm envelope")
	}
}

// genSubscriberKeysForTest mirrors internal/webpush's own genSubscriberKeys
// test helper: a real EC P-256 keypair + random auth secret in the
// base64url-without-padding form PushManager.subscribe() produces, so
// webpush-go's real ECDH+HKDF encryption path is exercised, not mocked.
func genSubscriberKeysForTest(t *testing.T) webpush.PushSubscriptionKeys {
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
