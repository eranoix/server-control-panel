package webpush

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStore_VAPIDPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	p1, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	pub1 := p1.PublicKey()
	if pub1 == "" {
		t.Fatal("empty public key")
	}
	// Reopen — should reuse the same keys (rotating would invalidate
	// existing subscriptions).
	p2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p2.PublicKey() != pub1 {
		t.Fatalf("VAPID key rotated across reload: %q vs %q", pub1, p2.PublicKey())
	}
}

func TestStore_AddDedupeByEndpoint(t *testing.T) {
	p, _ := Open(t.TempDir())
	p.Add(&PushSubscription{User: "alice", Endpoint: "https://push.example/abc", Keys: PushSubscriptionKeys{P256dh: "k1", Auth: "a1"}})
	p.Add(&PushSubscription{User: "alice", Endpoint: "https://push.example/abc", Keys: PushSubscriptionKeys{P256dh: "k2", Auth: "a2"}})
	got := p.ForUser("alice")
	if len(got) != 1 {
		t.Fatalf("expected 1 sub (dedupe by endpoint), got %d", len(got))
	}
	if got[0].Keys.P256dh != "k2" {
		t.Fatalf("expected re-subscribe to overwrite keys, got %+v", got[0])
	}
}

func TestStore_RemoveEndpointGone(t *testing.T) {
	p, _ := Open(t.TempDir())
	p.Add(&PushSubscription{User: "alice", Endpoint: "https://push.example/x", Keys: PushSubscriptionKeys{P256dh: "k", Auth: "a"}})
	if len(p.ForUser("alice")) != 1 {
		t.Fatal("add failed")
	}
	p.Remove("https://push.example/x")
	if len(p.ForUser("alice")) != 0 {
		t.Fatal("remove failed")
	}
	// Removing an unknown endpoint is a no-op (not an error).
	p.Remove("https://push.example/unknown")
}

func TestStore_PersistsSubsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	p1, _ := Open(dir)
	p1.Add(&PushSubscription{User: "alice", Endpoint: "https://push.example/1", Keys: PushSubscriptionKeys{P256dh: "k", Auth: "a"}})
	p1.Add(&PushSubscription{User: "bob", Endpoint: "https://push.example/2", Keys: PushSubscriptionKeys{P256dh: "k", Auth: "a"}})
	p2, _ := Open(dir)
	if len(p2.ForUser("alice")) != 1 || len(p2.ForUser("bob")) != 1 {
		t.Fatalf("subs not persisted: alice=%d bob=%d", len(p2.ForUser("alice")), len(p2.ForUser("bob")))
	}
}

func TestStore_SendWithoutSubs_NoCrash(t *testing.T) {
	p, _ := Open(t.TempDir())
	n := p.SendToUser(context.Background(), "alice", []byte(`{"type":"incoming-call"}`), SendOptions{TTL: 60}, nil)
	if n != 0 {
		t.Fatalf("expected 0 deliveries when no subs, got %d", n)
	}
}

// genSubscriberKeys generates a valid EC P-256 keypair + random auth secret
// in the base64url-without-padding form the browser's PushManager.subscribe()
// produces — needed so webpush-go's real encryption path (ECDH + HKDF) does
// not reject the subscription before ever reaching the network.
func genSubscriberKeys(t *testing.T) PushSubscriptionKeys {
	t.Helper()
	priv, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	_ = priv
	pub := elliptic.Marshal(elliptic.P256(), x, y)
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("genauth: %v", err)
	}
	return PushSubscriptionKeys{
		P256dh: base64.RawURLEncoding.EncodeToString(pub),
		Auth:   base64.RawURLEncoding.EncodeToString(auth),
	}
}

// TestStore_SendToAll_SkipsFilteredDevice proves allowDevice actually gates
// delivery attempts: a fake push endpoint (httptest.Server) always answers
// 201, so the returned delivered-count directly reflects how many
// subscriptions were attempted — one blocked device must not be counted.
func TestStore_SendToAll_SkipsFilteredDevice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	p, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	p.Add(&PushSubscription{User: "alice", Endpoint: srv.URL + "/allowed", Keys: genSubscriberKeys(t), DeviceID: "dev-allowed"})
	p.Add(&PushSubscription{User: "bob", Endpoint: srv.URL + "/blocked", Keys: genSubscriberKeys(t), DeviceID: "dev-blocked"})

	allowDevice := func(deviceID string) bool { return deviceID != "dev-blocked" }
	n := p.SendToAll(context.Background(), []byte(`{"type":"alert-fired"}`), SendOptions{TTL: 60}, allowDevice)
	if n != 1 {
		t.Fatalf("expected exactly 1 delivery (filtered device must not be attempted), got %d", n)
	}
}
