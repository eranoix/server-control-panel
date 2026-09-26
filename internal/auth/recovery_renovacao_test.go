package auth

import (
	"testing"
	"time"
)

// The recovery session can be RENEWED while work is happening
// — being disconnected in the middle of a repair is the worst possible
// moment to repeat password + TOTP. What keeps this from becoming an eternal
// privileged session is the instant of LOGIN surviving the renewals: that is where
// the absolute ceiling comes from. If `ini` were reset on every renewal, the ceiling
// would cease to exist with nothing visibly breaking — which is why the guarantee is
// asserted here.
func servico(t *testing.T) *Service {
	t.Helper()
	return New("segredo-de-teste-com-tamanho-suficiente-1234", nil)
}

func TestRenovarPreservaOInstanteDoLogin(t *testing.T) {
	s := servico(t)
	login := time.Now().Add(-3 * time.Hour)

	tok, err := s.IssueRecoveryTokenFrom("sam", 30*time.Minute, login)
	if err != nil {
		t.Fatalf("emitir: %v", err)
	}
	// Renew twice, as would happen over a long working session.
	for i := 0; i < 2; i++ {
		inicio, err := s.RecoveryTokenStart(tok)
		if err != nil {
			t.Fatalf("read start: %v", err)
		}
		tok, err = s.IssueRecoveryTokenFrom("sam", 30*time.Minute, inicio)
		if err != nil {
			t.Fatalf("renovar: %v", err)
		}
	}

	inicio, err := s.RecoveryTokenStart(tok)
	if err != nil {
		t.Fatalf("read start after renewals: %v", err)
	}
	if delta := inicio.Sub(login); delta > time.Second || delta < -time.Second {
		t.Errorf("the login instant moved %v across renewals — the absolute ceiling would cease to exist", delta)
	}
	if time.Since(inicio) < 3*time.Hour {
		t.Errorf("the session appears to be %v old; it should keep the 3h since login", time.Since(inicio))
	}
}

// A renewed token is still a recovery token and still belongs to the same user —
// no escalating the kind or changing owner along the way.
func TestTokenRenovadoContinuaSendoDeRecuperacao(t *testing.T) {
	s := servico(t)
	tok, err := s.IssueRecoveryTokenFrom("sam", 30*time.Minute, time.Now())
	if err != nil {
		t.Fatalf("emitir: %v", err)
	}
	user, err := s.VerifyRecoveryToken(tok)
	if err != nil || user != "sam" {
		t.Fatalf("verificar: user=%q err=%v", user, err)
	}
	// And it must not pass as a normal session token: Parse() is what the
	// panel's middleware uses, and it rejects kind != "" and != "session".
	if _, err := s.Parse(tok); err == nil {
		t.Error("recovery token was accepted as a normal session — privilege escalation")
	}
}

// Tokens issued BEFORE the `ini` claim existed do not carry it. They have to
// stay renewable, using `iat` (which, for them, is the same instant) —
// otherwise a deploy would disconnect everyone who was in the middle of a
// repair, which is exactly what this work is meant to avoid.
func TestTokenAntigoSemInicioAindaFunciona(t *testing.T) {
	s := servico(t)
	tok, err := s.IssueRecoveryToken("sam", 30*time.Minute)
	if err != nil {
		t.Fatalf("emitir: %v", err)
	}
	inicio, err := s.RecoveryTokenStart(tok)
	if err != nil {
		t.Fatalf("token without `ini` should fall back to `iat`: %v", err)
	}
	if time.Since(inicio) > time.Minute {
		t.Errorf("start read wrong: %v ago", time.Since(inicio))
	}
}

// An expired token does not renew: renewal extends what is ALIVE, it does not
// resurrect what already died.
func TestTokenExpiradoNaoServeParaRenovar(t *testing.T) {
	s := servico(t)
	tok, err := s.IssueRecoveryTokenFrom("sam", -time.Minute, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("emitir: %v", err)
	}
	if _, err := s.VerifyRecoveryToken(tok); err == nil {
		t.Error("expired token was accepted")
	}
	if _, err := s.RecoveryTokenStart(tok); err == nil {
		t.Error("expired token returned a start instant — a dead one could be renewed")
	}
}
