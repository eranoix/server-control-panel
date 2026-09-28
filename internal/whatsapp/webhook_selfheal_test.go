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

func TestWebhookReloadsStaleSecretInsteadOfDropping(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	fresh := "new-secret-from-vault"

	s := &Service{
		hmacSecret:  "old-cached-secret",
		HMACRefresh: func() string { return fresh },
	}
	if !s.hmacMatches(body, subscribe(fresh, body)) {
		t.Fatal("it refused a valid signature: the vault reload did not run")
	}
	if s.currentHMAC() != fresh {
		t.Fatalf("secret in use = %q, want %q (the new one has to be adopted)", s.currentHMAC(), fresh)
	}
}

func TestWebhookRejectsSignatureFromUnknownSecret(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	s := &Service{
		hmacSecret:  "cached",
		HMACRefresh: func() string { return "do-vault" },
	}
	if s.hmacMatches(body, subscribe("attacker-secret", body)) {
		t.Fatal("it accepted a signature from an unknown secret")
	}
	if s.currentHMAC() != "cached" {
		t.Fatal("it swapped the cached secret despite the refusal")
	}
}

func TestWebhookDoesNotReloadWhenCachedSecretWorks(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	called := 0
	s := &Service{
		hmacSecret:  "bom",
		HMACRefresh: func() string { called++; return "other" },
	}
	if !s.hmacMatches(body, subscribe("bom", body)) {
		t.Fatal("it refused a valid signature from the cached secret")
	}
	if called != 0 {
		t.Fatalf("it reloaded %d time(s) on the happy path", called)
	}
}

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

func TestWebhookConcurrentRotationIsNotDataRace(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	fresh := "new-secret-from-vault"
	s := &Service{
		hmacSecret:  "old-cached-secret",
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
