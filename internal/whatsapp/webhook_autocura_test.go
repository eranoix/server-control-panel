package whatsapp

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"sync"
	"testing"
)

func assina(secret string, body []byte) string {
	m := hmac.New(sha512.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// The hole that was still open: the secret stayed cached on the *Service
// FOREVER. Once it diverged from the daemon, EVERY incoming message was
// discarded until someone restarted vps-manager — in silence, with the panel
// still saying "connected". It happened in the wild: both sides stable, no
// restart, two real messages lost.
func TestWebhookRecarregaSegredoVelhoEmVezDeDescartar(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	novo := "segredo-novo-do-vault"

	s := &Service{
		hmacSecret:  "segredo-velho-cacheado",
		HMACRefresh: func() string { return novo },
	}
	if !s.hmacConfere(body, assina(novo, body)) {
		t.Fatal("it refused a valid signature: the vault reload did not run")
	}
	// And it adopts the new value, so we do not pay for the reload on every
	// message that follows.
	if s.hmacAtual() != novo {
		t.Fatalf("secret in use = %q, want %q (the new one has to be adopted)", s.hmacAtual(), novo)
	}
}

// Reloading must NOT turn into "accepts anything": a signature from a secret
// that is neither the cached one nor the vault's stays refused.
func TestWebhookNaoAceitaAssinaturaDeSegredoDesconhecido(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	s := &Service{
		hmacSecret:  "cacheado",
		HMACRefresh: func() string { return "do-vault" },
	}
	if s.hmacConfere(body, assina("segredo-de-um-atacante", body)) {
		t.Fatal("it accepted a signature from an unknown secret")
	}
	if s.hmacAtual() != "cacheado" {
		t.Fatal("it swapped the cached secret despite the refusal")
	}
}

// The happy path pays for no reload at all.
func TestWebhookNaoRecarregaQuandoOCacheadoJaServe(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	chamou := 0
	s := &Service{
		hmacSecret:  "bom",
		HMACRefresh: func() string { chamou++; return "outro" },
	}
	if !s.hmacConfere(body, assina("bom", body)) {
		t.Fatal("it refused a valid signature from the cached secret")
	}
	if chamou != 0 {
		t.Fatalf("it reloaded %d time(s) on the happy path", chamou)
	}
}

// With no reloader (nil) the old behaviour holds — no panic.
func TestWebhookSemRecarregadorNaoQuebra(t *testing.T) {
	body := []byte(`x`)
	s := &Service{hmacSecret: "a"}
	if s.hmacConfere(body, assina("b", body)) {
		t.Fatal("it accepted a wrong signature")
	}
	if !s.hmacConfere(body, assina("a", body)) {
		t.Fatal("it refused the right signature")
	}
}

// Rotating the secret is a WRITE on the *Service, and HandleWebhook runs in a
// goroutine PER REQUEST — two messages arriving together with a rotation were a
// write concurrent with a read, that is, a data race under Go's memory model.
// The other tests in this file are sequential, which is why -race passed
// without exposing anything. This one exercises the real case; it runs under
// `go test -race`.
func TestWebhookRotacaoConcorrenteNaoEDataRace(t *testing.T) {
	body := []byte(`{"event":"message"}`)
	novo := "segredo-novo-do-vault"
	s := &Service{
		hmacSecret:  "segredo-velho-cacheado",
		HMACRefresh: func() string { return novo },
	}
	assinado := assina(novo, body)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !s.hmacConfere(body, assinado) {
				t.Error("it refused a valid signature under concurrency")
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.hmacAtual()
		}()
	}
	wg.Wait()

	if s.hmacAtual() != novo {
		t.Fatalf("secret in use = %q, want %q", s.hmacAtual(), novo)
	}
}
