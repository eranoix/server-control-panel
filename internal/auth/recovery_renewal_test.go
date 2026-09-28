package auth

import (
	"testing"
	"time"
)

func service(t *testing.T) *Service {
	t.Helper()
	return New("test-secret-with-enough-length-1234", nil)
}

func TestRenewPreservesLoginInstant(t *testing.T) {
	s := service(t)
	login := time.Now().Add(-3 * time.Hour)

	tok, err := s.IssueRecoveryTokenFrom("sam", 30*time.Minute, login)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	for i := 0; i < 2; i++ {
		start, err := s.RecoveryTokenStart(tok)
		if err != nil {
			t.Fatalf("read start: %v", err)
		}
		tok, err = s.IssueRecoveryTokenFrom("sam", 30*time.Minute, start)
		if err != nil {
			t.Fatalf("renew: %v", err)
		}
	}

	start, err := s.RecoveryTokenStart(tok)
	if err != nil {
		t.Fatalf("read start after renewals: %v", err)
	}
	if delta := start.Sub(login); delta > time.Second || delta < -time.Second {
		t.Errorf("the login instant moved %v across renewals — the absolute ceiling would cease to exist", delta)
	}
	if time.Since(start) < 3*time.Hour {
		t.Errorf("the session appears to be %v old; it should keep the 3h since login", time.Since(start))
	}
}

func TestRenewedTokenStaysRecovery(t *testing.T) {
	s := service(t)
	tok, err := s.IssueRecoveryTokenFrom("sam", 30*time.Minute, time.Now())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	user, err := s.VerifyRecoveryToken(tok)
	if err != nil || user != "sam" {
		t.Fatalf("verify: user=%q err=%v", user, err)
	}
	if _, err := s.Parse(tok); err == nil {
		t.Error("recovery token was accepted as a normal session — privilege escalation")
	}
}

func TestOldTokenWithoutStartStillWorks(t *testing.T) {
	s := service(t)
	tok, err := s.IssueRecoveryToken("sam", 30*time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	start, err := s.RecoveryTokenStart(tok)
	if err != nil {
		t.Fatalf("token without `ini` should fall back to `iat`: %v", err)
	}
	if time.Since(start) > time.Minute {
		t.Errorf("start read wrong: %v ago", time.Since(start))
	}
}

func TestExpiredTokenCannotRenew(t *testing.T) {
	s := service(t)
	tok, err := s.IssueRecoveryTokenFrom("sam", -time.Minute, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := s.VerifyRecoveryToken(tok); err == nil {
		t.Error("expired token was accepted")
	}
	if _, err := s.RecoveryTokenStart(tok); err == nil {
		t.Error("expired token returned a start instant — a dead one could be renewed")
	}
}
