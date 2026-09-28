package claudeacct

import (
	"testing"
	"time"
)

func TestRateLimitsServesStaleOnError(t *testing.T) {
	s := &Store{}
	acct := Account{ID: "test-stale-acct"}
	good := RateLimitStatus{
		AccountID: acct.ID, LoggedIn: true,
		Windows: []RateWindow{{Key: "five_hour", UtilizationPct: 42}},
	}
	rateMu.Lock()
	rateCache[acct.ID] = rateCacheEntry{
		good:      &good,
		goodAt:    time.Now().Add(-10 * time.Minute),
		lastErr:   RateLimitStatus{Error: "unavailable (HTTP 429)"},
		nextRetry: time.Now().Add(5 * time.Minute),
	}
	rateMu.Unlock()
	defer func() { rateMu.Lock(); delete(rateCache, acct.ID); rateMu.Unlock() }()

	got := s.RateLimits(acct)
	if got.Error != "" {
		t.Fatalf("expected no error (stale-while-error), got %q", got.Error)
	}
	if !got.Stale {
		t.Fatal("expected Stale=true when serving last-known-good")
	}
	if len(got.Windows) != 1 || got.Windows[0].UtilizationPct != 42 {
		t.Fatalf("expected last-good windows, got %+v", got.Windows)
	}
}

func TestRateLimitsServesFreshGood(t *testing.T) {
	s := &Store{}
	acct := Account{ID: "test-fresh-acct"}
	good := RateLimitStatus{AccountID: acct.ID, LoggedIn: true, Windows: []RateWindow{{Key: "seven_day", UtilizationPct: 7}}}
	rateMu.Lock()
	rateCache[acct.ID] = rateCacheEntry{good: &good, goodAt: time.Now()}
	rateMu.Unlock()
	defer func() { rateMu.Lock(); delete(rateCache, acct.ID); rateMu.Unlock() }()

	got := s.RateLimits(acct)
	if got.Stale {
		t.Fatal("fresh good result must not be marked stale")
	}
	if got.Windows[0].UtilizationPct != 7 {
		t.Fatalf("unexpected windows: %+v", got.Windows)
	}
}
