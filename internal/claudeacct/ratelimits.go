package claudeacct

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RateWindow struct {
	Key            string  `json:"key"`
	Label          string  `json:"label"`
	UtilizationPct float64 `json:"utilization_pct"`
	RemainingPct   float64 `json:"remaining_pct"`
	ResetsAt       int64   `json:"resets_at"`
}

type ExtraUsage struct {
	Enabled      bool    `json:"enabled"`
	MonthlyLimit float64 `json:"monthly_limit"`
	UsedCredits  float64 `json:"used_credits"`
	Currency     string  `json:"currency"`
}

type RateLimitStatus struct {
	AccountID  string       `json:"account_id"`
	Label      string       `json:"label"`
	Tier       string       `json:"tier"`
	LoggedIn   bool         `json:"logged_in"`
	Error      string       `json:"error,omitempty"`
	Stale      bool         `json:"stale,omitempty"`
	Windows    []RateWindow `json:"windows"`
	ExtraUsage *ExtraUsage  `json:"extra_usage,omitempty"`
	FetchedAt  int64        `json:"fetched_at"`
}

const oauthUsageURL = "https://api.anthropic.com/api/oauth/usage"

var rlHTTP = &http.Client{Timeout: 8 * time.Second}

type rateCacheEntry struct {
	good      *RateLimitStatus
	goodAt    time.Time
	lastErr   RateLimitStatus
	nextRetry time.Time
}

var (
	rateMu    sync.Mutex
	rateCache = map[string]rateCacheEntry{}
)

const (
	rateTTL    = 5 * time.Minute
	rateErrTTL = 90 * time.Second
)

func (s *Store) rateDiskPath() string {
	return filepath.Join(filepath.Dir(s.path), "claude_ratelimits.json")
}

func (s *Store) RateLimits(acct Account) RateLimitStatus {
	if ls := s.LoginStatus(acct.ID); ls.IdentityMismatch {
		return RateLimitStatus{
			AccountID: acct.ID,
			Label:     acct.Label,
			LoggedIn:  false,
			Error:     "waiting for login" + asOtherAccount(ls.Email),
			Windows:   []RateWindow{},
			FetchedAt: time.Now().Unix(),
		}
	}
	now := time.Now()
	diskPath := s.rateDiskPath()

	rateMu.Lock()
	e := rateCache[acct.ID]
	rateMu.Unlock()

	if e.good == nil {
		if g := loadRateGood(acct.ID, diskPath); g != nil {
			e.good = g
			e.goodAt = time.Unix(g.FetchedAt, 0)
			rateMu.Lock()
			cur := rateCache[acct.ID]
			if cur.good == nil {
				cur.good, cur.goodAt = g, e.goodAt
				rateCache[acct.ID] = cur
			}
			rateMu.Unlock()
		}
	}

	if e.good != nil && now.Sub(e.goodAt) < rateTTL {
		return *e.good
	}
	if now.Before(e.nextRetry) {
		if e.good != nil {
			out := *e.good
			out.Stale = true
			return out
		}
		return e.lastErr
	}

	st, retryAfter := s.fetchRateLimits(acct)

	rateMu.Lock()
	ne := rateCache[acct.ID]
	if st.Error == "" && st.LoggedIn {
		c := st
		ne.good, ne.goodAt, ne.nextRetry, ne.lastErr = &c, now, time.Time{}, RateLimitStatus{}
	} else {
		backoff := rateErrTTL
		if retryAfter > backoff {
			backoff = retryAfter
		}
		ne.lastErr, ne.nextRetry = st, now.Add(backoff)
	}
	rateCache[acct.ID] = ne
	good := ne.good
	rateMu.Unlock()

	if st.Error == "" && st.LoggedIn {
		saveRateGood(acct.ID, st, diskPath)
		return st
	}
	if good != nil {
		out := *good
		out.Stale = true
		return out
	}
	return st
}

func loadRateGood(id, path string) *RateLimitStatus {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]RateLimitStatus
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	if st, ok := m[id]; ok && st.LoggedIn {
		st.Stale = true
		return &st
	}
	return nil
}

func saveRateGood(id string, st RateLimitStatus, path string) {
	m := map[string]RateLimitStatus{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	st.Stale = false
	m[id] = st
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (s *Store) fetchRateLimits(acct Account) (RateLimitStatus, time.Duration) {
	st := RateLimitStatus{
		AccountID: acct.ID,
		Label:     acct.Label,
		FetchedAt: time.Now().Unix(),
		Windows:   []RateWindow{},
	}
	credPath, _ := s.pathsFor(acct)
	token, tier := readOauthToken(credPath)
	st.Tier = prettyTier(tier)
	if token == "" {
		st.Error = "not signed in — sign in to this account"
		return st, 0
	}

	req, err := http.NewRequest(http.MethodGet, oauthUsageURL, nil)
	if err != nil {
		st.Error = "internal error"
		return st, 0
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := rlHTTP.Do(req)
	if err != nil {
		st.Error = "unavailable (network)"
		return st, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		st.Error = "token expired — run a job/session to refresh it"
		return st, 0
	}
	if resp.StatusCode != 200 {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		log.Printf("[ratelimits] acct=%s status=%d retry-after=%q body=%q",
			acct.ID, resp.StatusCode, resp.Header.Get("Retry-After"), strings.TrimSpace(string(body)))
		st.Error = "unavailable (HTTP " + strconv.Itoa(resp.StatusCode) + ")"
		return st, retryAfter
	}

	var doc oauthUsageDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		st.Error = "invalid response"
		return st, 0
	}
	st.LoggedIn = true

	for _, w := range []struct {
		key, label string
		src        *usageWindow
	}{
		{"five_hour", "5 hours", doc.FiveHour},
		{"seven_day", "Weekly", doc.SevenDay},
		{"seven_day_opus", "Weekly · Opus", doc.SevenDayOpus},
		{"seven_day_sonnet", "Weekly · Sonnet", doc.SevenDaySonnet},
		{"seven_day_oauth_apps", "Weekly · apps", doc.SevenDayOAuthApps},
	} {
		if w.src == nil {
			continue
		}
		st.Windows = append(st.Windows, RateWindow{
			Key:            w.key,
			Label:          w.label,
			UtilizationPct: w.src.Utilization,
			RemainingPct:   clampPct(100 - w.src.Utilization),
			ResetsAt:       parseResetUnix(w.src.ResetsAt),
		})
	}
	if doc.ExtraUsage != nil && doc.ExtraUsage.IsEnabled {
		st.ExtraUsage = &ExtraUsage{
			Enabled:      true,
			MonthlyLimit: doc.ExtraUsage.MonthlyLimit,
			UsedCredits:  doc.ExtraUsage.UsedCredits,
			Currency:     doc.ExtraUsage.Currency,
		}
	}
	return st, 0
}

func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		d := time.Duration(secs) * time.Second
		if d > 10*time.Minute {
			d = 10 * time.Minute
		}
		return d
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			if d > 10*time.Minute {
				d = 10 * time.Minute
			}
			return d
		}
	}
	return 0
}

type usageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type oauthUsageDoc struct {
	FiveHour          *usageWindow `json:"five_hour"`
	SevenDay          *usageWindow `json:"seven_day"`
	SevenDayOpus      *usageWindow `json:"seven_day_opus"`
	SevenDaySonnet    *usageWindow `json:"seven_day_sonnet"`
	SevenDayOAuthApps *usageWindow `json:"seven_day_oauth_apps"`
	ExtraUsage        *struct {
		IsEnabled    bool    `json:"is_enabled"`
		MonthlyLimit float64 `json:"monthly_limit"`
		UsedCredits  float64 `json:"used_credits"`
		Currency     string  `json:"currency"`
	} `json:"extra_usage"`
}

func readOauthToken(path string) (token, tier string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var doc struct {
		ClaudeAiOauth *struct {
			AccessToken   string `json:"accessToken"`
			RateLimitTier string `json:"rateLimitTier"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.ClaudeAiOauth == nil {
		return "", ""
	}
	return doc.ClaudeAiOauth.AccessToken, doc.ClaudeAiOauth.RateLimitTier
}

func prettyTier(raw string) string {
	if raw == "" {
		return ""
	}
	if i := strings.LastIndex(raw, "_"); i >= 0 && i < len(raw)-1 {
		last := raw[i+1:]
		if strings.HasSuffix(last, "x") {
			return last
		}
	}
	return raw
}

func parseResetUnix(s string) int64 {
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix()
	}
	return 0
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
