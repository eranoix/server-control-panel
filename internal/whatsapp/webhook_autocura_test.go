package whatsapp

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"sync"
	"testing"
)

func subscribe(secret string, body []byte) string {
	m := hmac.New(sha512.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// The hole that was still open: the secret stayed cached on the *Service
// FOREVER. Once it diverged from the daemon, EVERY incoming message was
// discarded until someone restarted vps-manager — in silence, with the panel
// still saying "connected". It happened in the wild: both sides stable, no
// restart, two real messages lost.
func TestWebhookReloadsStaleSecretInsteadOfDropping(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	fresh := "segredo-novo-do-vault"

	s := &Service{
		hmacSecret:  "segredo-velho-cacheado",
		HMACRefresh: func() string { return fresh },
	}
	if !s.hmacMatches(body, subscribe(fresh, body)) {
		t.Fatal("it refused a valid signature: the vault reload did not run")
	}
	// And it adopts the new value, so we do not pay for the reload on every
	// message that follows.
	if s.currentHMAC() != fresh {
		t.Fatalf("secret in use = %q, want %q (the new one has to be adopted)", s.currentHMAC(), fresh)
	}
}

// Reloading must NOT turn into "accepts anything": a signature from a secret
// that is neither the cached one nor the vault's stays refused.
func TestWebhookRejectsSignatureFromUnknownSecret(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	s := &Service{
		hmacSecret:  "cacheado",
		HMACRefresh: func() string { return "do-vault" },
	}
	if s.hmacMatches(body, subscribe("segredo-de-um-atacante", body)) {
		t.Fatal("it accepted a signature from an unknown secret")
	}
	if s.currentHMAC() != "cacheado" {
		t.Fatal("it swapped the cached secret despite the refusal")
	}
}

// The happy path pays for no reload at all.
func TestWebhookDoesNotReloadWhenCachedSecretWorks(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	called := 0
	s := &Service{
		hmacSecret:  "bom",
		HMACRefresh: func() string { called++; return "outro" },
	}
	if !s.hmacMatches(body, subscribe("bom", body)) {
		t.Fatal("it refused a valid signature from the cached secret")
	}
	if called != 0 {
		t.Fatalf("it reloaded %d time(s) on the happy path", called)
	}
}

// With no reloader (nil) the old behaviour holds — no panic.
func TestWebhookWithoutReloaderDoesNotBreak(t *testing.T) {
	body := []byte(`x`)
	s := &Service{hmacSecret: "a"}
	if s.hmacMatches(body, subscribe("b", body)) {
		t.Fatal("it accepted a wrong signature")
	}
	if !s.hmacMatches(body, subscribe("a", body)) {
		t.Fatal("it refused the right signature")
	}
}

// Rotating the secret is a WRITE on the *Service, and HandleWebhook runs in a
// goroutine PER REQUEST — two messages arriving together with a rotation were a
// write concurrent with a read, that is, a data race under Go's memory model.
// The other tests in this file are sequential, which is why -race passed
// without exposing anything. This one exercises the real case; it runs under
// `go test -race`.
func TestWebhookConcurrentRotationIsNotDataRace(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	fresh := "segredo-novo-do-vault"
	s := &Service{
		hmacSecret:  "segredo-velho-cacheado",
		HMACRefresh: func() string { return fresh },
	}
	signed := subscribe(fresh, body)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !s.hmacMatches(body, signed) {
				t.Error("it refused a valid signature under concurrency")
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.currentHMAC()
		}()
	}
	wg.Wait()

	if s.currentHMAC() != fresh {
		t.Fatalf("secret in use = %q, want %q", s.currentHMAC(), fresh)
	}
}
