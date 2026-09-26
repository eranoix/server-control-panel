package claudeacct

import (
	"testing"
	"time"
)

// On a fetch error (e.g. HTTP 429), RateLimits must serve the last-known-good
// result marked Stale, instead of collapsing to "unavailable". This is the
// non-regression guard for the quota-availability fix.
func TestRateLimitsServesStaleOnError(t *testing.T) {
	s := &Store{}
	acct := Account{ID: "test-stale-acct"}
	good := RateLimitStatus{
		AccountID: acct.ID, LoggedIn: true,
		Windows: []RateWindow{{Key: "five_hour", UtilizationPct: 42}},
	}
	rateMu.Lock()
	rateCache[acct.ID] = rateCacheEntry{
		good:      &good,                                            // we have an older good result
		goodAt:    time.Now().Add(-10 * time.Minute),                // stale (> rateTTL) but usable
		lastErr:   RateLimitStatus{Error: "unavailable (HTTP 429)"}, // last attempt errored
		nextRetry: time.Now().Add(5 * time.Minute),                  // still backing off
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

// A fresh good result (within rateTTL) is served directly, not marked stale.
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
