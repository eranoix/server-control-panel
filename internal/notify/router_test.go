package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeChannel records what it was asked to send and can be made to fail or
// block, exercising the breaker and overflow paths.
type fakeChannel struct {
	name    string
	mu      sync.Mutex
	sent    []Event
	failErr error         // if set, every Send returns it
	block   chan struct{} // if non-nil, Send waits on it before returning
}

func (f *fakeChannel) Name() string { return f.name }
func (f *fakeChannel) Send(ctx context.Context, ev Event, cfg ChannelConfig) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.failErr != nil {
		return f.failErr
	}
	f.mu.Lock()
	f.sent = append(f.sent, ev)
	f.mu.Unlock()
	return nil
}
func (f *fakeChannel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// harness wires a Router with a fake clock and an onHandled signal so tests can
// await async processing deterministically.
type harness struct {
	r       *Router
	handled chan Event
	clock   int64
	clockMu sync.Mutex
}

func (h *harness) advance(sec int64) {
	h.clockMu.Lock()
	h.clock += sec
	h.clockMu.Unlock()
}
func (h *harness) now() int64 {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()
	return h.clock
}

// wait blocks until n events have been handled or the test deadline passes.
func (h *harness) wait(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-h.handled:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for handled event %d/%d", i+1, n)
		}
	}
}

func newHarness(t *testing.T, impls map[string]Channel, opts Options) *harness {
	t.Helper()
	h := &harness{handled: make(chan Event, 256), clock: 1_000_000}
	opts.DataDir = t.TempDir()
	opts.Channels = impls
	opts.Now = h.now
	r, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.onHandled = func(ev Event) { h.handled <- ev }
	h.r = r
	t.Cleanup(r.Close)
	return h
}

func ruleAll(id string, channels ...string) Rule {
	return Rule{ID: id, Name: id, Enabled: true, Channels: channels}
}

// TestMatchTypeSeveritySource verifies the three prefix/rank predicates.
func TestMatchTypeSeveritySource(t *testing.T) {
	fc := &fakeChannel{name: "fake"}
	h := newHarness(t, map[string]Channel{"fake": fc}, Options{})
	def, _ := h.r.UpsertChannel(ChannelDef{ID: "c1", Type: "fake", Enabled: true})

	rl := Rule{ID: "r1", Name: "jobs-critical-from-sched", Enabled: true,
		TypePrefix: "job.", MinSeverity: SeverityWarning, SourcePrefix: "scheduler:",
		Channels: []string{def.ID}}
	if _, err := h.r.UpsertRule(rl); err != nil {
		t.Fatal(err)
	}

	// Matches: job.failed + critical + scheduler source.
	h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, Source: "scheduler:abc", DedupKey: "job:1"})
	// Rejected by type.
	h.r.Dispatch(Event{Type: TypeMetricThreshold, Severity: SeverityCritical, Source: "scheduler:abc", DedupKey: "m:1"})
	// Rejected by severity (info < warning).
	h.r.Dispatch(Event{Type: TypeJobDone, Severity: SeverityInfo, Source: "scheduler:abc", DedupKey: "job:2"})
	// Rejected by source.
	h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, Source: "user", DedupKey: "job:3"})
	h.wait(t, 4)

	if got := fc.count(); got != 1 {
		t.Fatalf("expected exactly 1 send (only the fully-matching event), got %d", got)
	}
}

// TestThrottleDedup: a 2nd identical (DedupKey,rule) within the window sends once.
func TestThrottleDedup(t *testing.T) {
	fc := &fakeChannel{name: "fake"}
	h := newHarness(t, map[string]Channel{"fake": fc}, Options{ThrottleWindow: 300})
	def, _ := h.r.UpsertChannel(ChannelDef{ID: "c1", Type: "fake", Enabled: true})
	h.r.UpsertRule(ruleAll("r1", def.ID))

	h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, DedupKey: "job:42"})
	h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, DedupKey: "job:42"})
	h.wait(t, 2)
	if got := fc.count(); got != 1 {
		t.Fatalf("dedup failed: expected 1 send for repeated DedupKey, got %d", got)
	}

	// After the window elapses, the same key sends again.
	h.advance(301)
	h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, DedupKey: "job:42"})
	h.wait(t, 1)
	if got := fc.count(); got != 2 {
		t.Fatalf("post-window resend failed: expected 2 sends, got %d", got)
	}
}

// TestMetricEpisodesNotThrottled: the metric DedupKey is keyed per episode
// ("metric:<rule>:<episode>"), so two DIFFERENT episodes of the SAME rule inside
// the throttle window both send (a recovery-then-recross must notify again),
// while a repeat of the SAME episode within the window is deduped (a held metric
// notifies once). This is the throttle-side half of edge-triggered alerting.
func TestMetricEpisodesNotThrottled(t *testing.T) {
	fc := &fakeChannel{name: "fake"}
	h := newHarness(t, map[string]Channel{"fake": fc}, Options{ThrottleWindow: 300})
	def, _ := h.r.UpsertChannel(ChannelDef{ID: "c1", Type: "fake", Enabled: true})
	h.r.UpsertRule(ruleAll("r1", def.ID))

	// Same rule, episode 100 twice (held) then episode 200 (recrossed) — all
	// well within the 300s window.
	h.r.Dispatch(Event{Type: TypeMetricThreshold, Severity: SeverityWarning, DedupKey: "metric:cpu:100"})
	h.r.Dispatch(Event{Type: TypeMetricThreshold, Severity: SeverityWarning, DedupKey: "metric:cpu:100"})
	h.r.Dispatch(Event{Type: TypeMetricThreshold, Severity: SeverityWarning, DedupKey: "metric:cpu:200"})
	h.wait(t, 3)

	if got := fc.count(); got != 2 {
		t.Fatalf("want 2 sends (episode 100 once + episode 200 once), got %d", got)
	}
}

// TestThrottleEviction: many distinct keys then a window pass -> map drains.
func TestThrottleEviction(t *testing.T) {
	fc := &fakeChannel{name: "fake"}
	h := newHarness(t, map[string]Channel{"fake": fc}, Options{ThrottleWindow: 60})
	def, _ := h.r.UpsertChannel(ChannelDef{ID: "c1", Type: "fake", Enabled: true})
	h.r.UpsertRule(ruleAll("r1", def.ID))

	const n = 50
	for i := 0; i < n; i++ {
		h.r.Dispatch(Event{Type: TypeJobDone, Severity: SeverityInfo, DedupKey: keyN(i)})
	}
	h.wait(t, n)
	if l := h.r.throttleLen(); l != n {
		t.Fatalf("expected %d throttle entries, got %d", n, l)
	}

	// Advance past the window and push one more event: eviction runs on consume
	// and sweeps the stale entries (only the fresh one remains).
	h.advance(61)
	h.r.Dispatch(Event{Type: TypeJobDone, Severity: SeverityInfo, DedupKey: "fresh"})
	h.wait(t, 1)
	if l := h.r.throttleLen(); l > 1 {
		t.Fatalf("expected throttle map to drain to ~0 after window, got %d", l)
	}
}

func keyN(i int) string { return "job:" + string(rune('a'+i%26)) + itoa(i) }
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// TestFanOutTwoChannels: one matching rule routes to two channels -> both get it.
func TestFanOutTwoChannels(t *testing.T) {
	a := &fakeChannel{name: "a"}
	b := &fakeChannel{name: "b"}
	h := newHarness(t, map[string]Channel{"a": a, "b": b}, Options{})
	ca, _ := h.r.UpsertChannel(ChannelDef{ID: "ca", Type: "a", Enabled: true})
	cb, _ := h.r.UpsertChannel(ChannelDef{ID: "cb", Type: "b", Enabled: true})
	h.r.UpsertRule(ruleAll("r1", ca.ID, cb.ID))

	h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, DedupKey: "job:1"})
	h.wait(t, 1)
	if a.count() != 1 || b.count() != 1 {
		t.Fatalf("fan-out failed: a=%d b=%d (want 1,1)", a.count(), b.count())
	}
}

// TestOverflowNonBlocking: with the worker stuck in Send and the buffer full,
// Dispatch must return immediately and increment dropped.
func TestOverflowNonBlocking(t *testing.T) {
	block := make(chan struct{})
	fc := &fakeChannel{name: "fake", block: block}
	h := newHarness(t, map[string]Channel{"fake": fc}, Options{BufSize: 2})
	def, _ := h.r.UpsertChannel(ChannelDef{ID: "c1", Type: "fake", Enabled: true})
	h.r.UpsertRule(ruleAll("r1", def.ID))

	// First event is pulled by the worker and blocks inside Send. Subsequent
	// events fill the buffer (2) then overflow.
	for i := 0; i < 20; i++ {
		start := time.Now()
		h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, DedupKey: keyN(i)})
		if d := time.Since(start); d > 100*time.Millisecond {
			close(block)
			t.Fatalf("Dispatch blocked for %v — must be non-blocking", d)
		}
	}
	if got := h.r.Dropped(); got == 0 {
		close(block)
		t.Fatalf("expected dropped > 0 under overflow, got 0")
	}
	close(block) // release the worker so Close doesn't hang
}

// TestBreakerOpensAndRecovers: a failing channel trips the breaker after N
// failures (further sends skipped), then half-opens after the open window.
func TestBreakerOpensAndRecovers(t *testing.T) {
	fc := &fakeChannel{name: "fake", failErr: errors.New("waha down")}
	h := newHarness(t, map[string]Channel{"fake": fc}, Options{
		BreakerThreshold: 5, BreakerOpenSec: 60, ThrottleWindow: 300,
	})
	def, _ := h.r.UpsertChannel(ChannelDef{ID: "c1", Type: "fake", Enabled: true})
	h.r.UpsertRule(ruleAll("r1", def.ID))

	// 6 distinct events: first 5 call Send (all fail) and trip the breaker; the
	// 6th is skipped because the breaker is open.
	for i := 0; i < 6; i++ {
		h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, DedupKey: keyN(i)})
	}
	h.wait(t, 6)

	// Switch the channel to succeed and advance past the open window: the next
	// event should be attempted again (breaker half-open) and delivered.
	fc.failErr = nil
	h.advance(61)
	h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, DedupKey: "after"})
	h.wait(t, 1)
	if got := fc.count(); got != 1 {
		t.Fatalf("expected exactly 1 successful send after breaker recovery, got %d", got)
	}
}

// TestHistoryRecordedRegardlessOfMatch: Dispatch records history even when no
// rule matches (the dry-run/feed depends on this).
func TestHistoryRecordedRegardlessOfMatch(t *testing.T) {
	h := newHarness(t, map[string]Channel{}, Options{})
	h.r.Dispatch(Event{Type: TypeJobDone, Severity: SeverityInfo, Source: "user", DedupKey: "job:1"})
	h.wait(t, 1)
	hist := h.r.History(HistoryFilter{})
	if len(hist) != 1 || hist[0].Type != TypeJobDone {
		t.Fatalf("history not recorded for unmatched event: %+v", hist)
	}
}

// TestPersistenceRoundTrip: rules/channels survive a Router restart.
func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r1, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := r1.UpsertChannel(ChannelDef{Name: "wa", Type: "whatsapp", Enabled: true,
		Config: ChannelConfig{FromUser: "sam", ChatJID: "123@c.us"}})
	r1.UpsertRule(Rule{Name: "jobs", Enabled: true, TypePrefix: "job.", Channels: []string{ch.ID}})
	r1.Close()

	r2, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if len(r2.Rules()) != 1 {
		t.Fatalf("rules not persisted: %d", len(r2.Rules()))
	}
	if len(r2.ChannelDefs()) != 1 {
		t.Fatalf("channels not persisted: %d", len(r2.ChannelDefs()))
	}
	if r2.ChannelDefs()[0].Config.ChatJID != "123@c.us" {
		t.Fatalf("channel config not persisted: %+v", r2.ChannelDefs()[0])
	}
}

// TestDryRunNoSend: DryRun returns matching history without delivering.
func TestDryRunNoSend(t *testing.T) {
	fc := &fakeChannel{name: "fake"}
	h := newHarness(t, map[string]Channel{"fake": fc}, Options{})
	h.r.Dispatch(Event{Type: TypeJobFailed, Severity: SeverityCritical, Source: "user", DedupKey: "job:1"})
	h.r.Dispatch(Event{Type: TypeMetricThreshold, Severity: SeverityWarning, Source: "metrics", DedupKey: "m:1"})
	h.wait(t, 2)

	preview := h.r.DryRun(Rule{TypePrefix: "job."})
	if len(preview) != 1 || preview[0].Type != TypeJobFailed {
		t.Fatalf("dry-run match wrong: %+v", preview)
	}
	if fc.count() != 0 {
		t.Fatalf("dry-run must not send, sent %d", fc.count())
	}
}
