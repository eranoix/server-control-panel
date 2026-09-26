package metrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Rule is a threshold-based alert rule evaluated against a metric Snapshot.
type Rule struct {
	Name string `json:"name"`
	// Metric is the canonical catalog key (e.g. "sys.cpu", "claude.cost.today",
	// "jobs.failed.total"). When empty, the legacy Field is mapped to a sys.* key
	// for backward compatibility with rules created before the generic catalog.
	Metric    string  `json:"metric,omitempty"`
	Field     string  `json:"field,omitempty"` // legacy: cpu|mem_pct|load1|disk_pct
	Op        string  `json:"op"`
	Threshold float64 `json:"threshold"`
	Duration  int     `json:"duration"`
	LastFired int64   `json:"last_fired"`
	// Severity drives notification routing on fire. Empty means warning
	// (legacy default). One of: "", "info", "warning", "critical".
	Severity string `json:"severity,omitempty"`
	// ActiveSince > 0 ⇒ rule is in the "firing" state since this unix instant.
	// Edge discriminator: the rule notifies ONCE on the 0→active transition and
	// only re-arms (back to 0) once the metric normalizes. Persisted alongside
	// LastFired so an episode-in-progress survives a deploy/restart instead of
	// re-notifying from scratch.
	ActiveSince int64 `json:"active_since,omitempty"`
	// RearmMargin is an optional hysteresis/deadband (in the metric's own unit).
	// 0 (default) re-arms as soon as the condition clears. With a margin, the
	// metric must cross BACK past the threshold by RearmMargin before the rule
	// re-arms, so a value oscillating around the threshold doesn't flap
	// fire→resolved→fire. Applied on the safe side of the operator.
	RearmMargin float64 `json:"rearm_margin,omitempty"`
	// RenotifySec optionally re-sends a reminder every RenotifySec seconds WHILE
	// a rule stays firing. 0 (default) means edge-only: notify once, stay silent
	// until recovery. Opt-in for operators who want a periodic nag on a sustained
	// alert; the reminder carries the same episode but a unique dedup key so the
	// Router throttle doesn't swallow it.
	RenotifySec int `json:"renotify_sec,omitempty"`
}

// metricKey resolves the snapshot key this rule evaluates against: the explicit
// Metric, or the legacy Field mapped to its sys.* equivalent.
func (r Rule) metricKey() string {
	if r.Metric != "" {
		return r.Metric
	}
	switch r.Field {
	case "cpu":
		return "sys.cpu"
	case "mem_pct":
		return "sys.mem_pct"
	case "load1":
		return "sys.load1"
	case "disk_pct":
		return "sys.disk.max"
	}
	return r.Field
}

// Fire represents a triggered alert. With edge-triggered evaluation a Fire is
// emitted on a state transition: Resolved=false marks the metric crossing the
// threshold (start of an episode), Resolved=true marks it normalizing (end of
// the same episode). Episode is the episode identity (the ActiveSince that
// opened it) so the notify layer can dedup per episode rather than per rule.
type Fire struct {
	Rule     string
	Time     int64
	Value    float64
	Severity string `json:"severity,omitempty"`
	Episode  int64  `json:"episode,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
}

// RuleStatus augments a Rule with its live evaluation state for the UI. The
// embedded Rule is anonymous, so encoding/json flattens it. MetricKey is the
// resolved snapshot key; Label/Unit are filled by the handler from the catalog.
type RuleStatus struct {
	Rule
	State        string  `json:"state"` // normal | pending | firing | nodata
	CurrentValue float64 `json:"current_value"`
	PendingSince int64   `json:"pending_since,omitempty"`
	MetricKey    string  `json:"metric_key"`
	Label        string  `json:"label,omitempty"`
	Unit         string  `json:"unit,omitempty"`
}

// staleAfter is how long without a fresh sample before a rule reports "nodata"
// instead of a (misleadingly green) "normal". The sampler runs every 5s, so
// this tolerates several missed beats before crying nodata.
const staleAfter = 30 // seconds

// Engine holds rules and tracks per-rule condition state.
type Engine struct {
	mu        sync.RWMutex
	rules     map[string]Rule
	firstTrue map[string]int64
	lastSnap  Snapshot // last Snapshot seen by Evaluate, for live Status()
}

// NewEngine returns an empty alert Engine.
func NewEngine() *Engine {
	return &Engine{
		rules:     make(map[string]Rule),
		firstTrue: make(map[string]int64),
	}
}

func validField(f string) bool {
	switch f {
	case "cpu", "mem_pct", "load1", "disk_pct":
		return true
	}
	return false
}

func validOp(o string) bool {
	switch o {
	case ">", ">=", "<", "<=":
		return true
	}
	return false
}

func validSeverity(s string) bool {
	switch s {
	case "", "info", "warning", "critical":
		return true
	}
	return false
}

// AddRule validates and stores r. A rule must target either a canonical Metric
// key (validated against the catalog by the caller) or a legacy Field.
func (e *Engine) AddRule(r Rule) error {
	if r.Name == "" {
		return errors.New("rule name required")
	}
	if r.Metric == "" && !validField(r.Field) {
		return fmt.Errorf("invalid field %q", r.Field)
	}
	if !validOp(r.Op) {
		return fmt.Errorf("invalid op %q", r.Op)
	}
	if !validSeverity(r.Severity) {
		return fmt.Errorf("invalid severity %q", r.Severity)
	}
	if r.RearmMargin < 0 {
		return fmt.Errorf("rearm_margin must be >= 0, got %v", r.RearmMargin)
	}
	if r.RenotifySec < 0 {
		return fmt.Errorf("renotify_sec must be >= 0, got %d", r.RenotifySec)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules[r.Name] = r
	// Restore the firing-window anchor from persisted state so a rule loaded
	// mid-episode (ActiveSince set) reports "firing" rather than "pending" after
	// a restart, and the edge guard (ActiveSince != 0) suppresses a re-notify.
	// A fresh/edited rule (no ActiveSince) re-arms — editing a rule via the FE,
	// which omits active_since, intentionally resets its episode.
	if r.ActiveSince != 0 {
		e.firstTrue[r.Name] = r.ActiveSince
	} else {
		delete(e.firstTrue, r.Name)
	}
	return nil
}

// RemoveRule deletes the rule named name.
func (e *Engine) RemoveRule(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.rules, name)
	delete(e.firstTrue, name)
}

// List returns a snapshot of current rules.
func (e *Engine) List() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Rule, 0, len(e.rules))
	for _, r := range e.rules {
		out = append(out, r)
	}
	return out
}

func fieldValue(p Point, field string) float64 {
	switch field {
	case "cpu":
		return p.CPU
	case "mem_pct":
		return p.MemPct
	case "load1":
		return p.Load1
	case "disk_pct":
		return p.DiskPct
	}
	return 0
}

func cmp(v, t float64, op string) bool {
	switch op {
	case ">":
		return v > t
	case ">=":
		return v >= t
	case "<":
		return v < t
	case "<=":
		return v <= t
	}
	return false
}

// rearmAllowed reports whether v has cleared the threshold by at least
// r.RearmMargin (hysteresis/deadband), so a rule re-arms only after the metric
// has decisively recovered. It is consulted only when the condition is already
// false. With RearmMargin 0 it re-arms as soon as the condition clears (it
// reduces exactly to "condition is false"). The margin is applied on the side
// the metric must return to: below Threshold-margin for >/>=; above
// Threshold+margin for </<=.
func rearmAllowed(v float64, r Rule) bool {
	switch r.Op {
	case ">", ">=":
		return v <= r.Threshold-r.RearmMargin
	case "<", "<=":
		return v >= r.Threshold+r.RearmMargin
	}
	return true
}

// Evaluate applies the snapshot to every rule and returns any state-transition
// fires. Evaluation is EDGE-TRIGGERED, not time-based: a rule fires exactly ONCE
// when it crosses its threshold (after Duration), stays silent while the metric
// remains above (no matter how many ticks), and emits a single Resolved fire
// when the metric normalizes — only then does it re-arm to fire again. The
// firing state lives in r.ActiveSince, persisted by the caller, so an episode in
// progress survives a deploy/restart without re-notifying.
//
// A rule whose metric key is absent from the snapshot reports nodata in Status
// and neither fires nor resolves: a collector gap must not be mistaken for a
// recovery, and a metric returning still-high must not re-notify.
func (e *Engine) Evaluate(snap Snapshot) []Fire {
	now := time.Now().Unix()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastSnap = snap
	var fires []Fire
	for name, r := range e.rules {
		v, ok := snap.Values[r.metricKey()]
		cond := ok && cmp(v, r.Threshold, r.Op)
		switch {
		case cond:
			if e.firstTrue[name] == 0 {
				e.firstTrue[name] = now
			}
			switch {
			case now-e.firstTrue[name] >= int64(r.Duration) && r.ActiveSince == 0:
				// Edge normal/pending → firing: notify once.
				r.ActiveSince = now
				r.LastFired = now
				e.rules[name] = r
				fires = append(fires, Fire{Rule: name, Time: now, Value: v, Severity: r.Severity, Episode: now})
			case r.ActiveSince != 0 && r.RenotifySec > 0 && now-r.LastFired >= int64(r.RenotifySec):
				// Opt-in reminder while held. Same episode, new LastFired; the
				// notify layer keys the reminder uniquely (Time != Episode) so
				// the Router throttle doesn't swallow it. With RenotifySec 0
				// (default) this never runs — the rule stays edge-only silent.
				r.LastFired = now
				e.rules[name] = r
				fires = append(fires, Fire{Rule: name, Time: now, Value: v, Severity: r.Severity, Episode: r.ActiveSince})
			}
			// While ActiveSince != 0 and no reminder is due, stay silent no
			// matter how long the condition holds.
		case ok && r.ActiveSince != 0 && rearmAllowed(v, r):
			// Metric present and back past the threshold (clear of the deadband)
			// while an episode is open: edge firing → normal. Emit a resolved
			// fire and re-arm.
			ep := r.ActiveSince
			r.ActiveSince = 0
			e.rules[name] = r
			delete(e.firstTrue, name)
			fires = append(fires, Fire{Rule: name, Time: now, Value: v, Severity: r.Severity, Episode: ep, Resolved: true})
		default:
			// One of: below threshold but still within the deadband (episode
			// stays open, no fire); nodata mid-episode (keep the episode so a
			// returning still-high metric doesn't re-notify); or simply normal.
			// Clear the pending window only when no episode is open.
			if r.ActiveSince == 0 {
				delete(e.firstTrue, name)
			}
		}
	}
	return fires
}

// Status returns every rule with its live evaluation state derived from the
// last Snapshot seen by Evaluate. State is one of normal/pending/firing/nodata
// (nodata when the snapshot is stale OR the rule's metric key is absent).
// Concurrency-safe with Evaluate: this takes RLock, Evaluate takes Lock.
func (e *Engine) Status() []RuleStatus {
	now := time.Now().Unix()
	e.mu.RLock()
	defer e.mu.RUnlock()
	snapStale := e.lastSnap.T == 0 || now-e.lastSnap.T > staleAfter
	out := make([]RuleStatus, 0, len(e.rules))
	for name, r := range e.rules {
		key := r.metricKey()
		v, ok := e.lastSnap.Values[key]
		st := RuleStatus{Rule: r, State: "normal", CurrentValue: v, MetricKey: key}
		switch {
		case snapStale || !ok:
			st.State = "nodata"
		case cmp(v, r.Threshold, r.Op):
			ft := e.firstTrue[name]
			if ft == 0 {
				ft = now // condition true but not yet recorded; treat as just-now
			}
			st.PendingSince = ft
			if now-ft >= int64(r.Duration) {
				st.State = "firing"
			} else {
				st.State = "pending"
			}
		case r.ActiveSince != 0:
			// Condition currently false but the episode is still open: the metric
			// dipped below the threshold yet hasn't cleared the RearmMargin
			// deadband, so the alert is still firing (and hasn't sent a recovery).
			// Surface it as firing so the UI matches the notification state.
			st.State = "firing"
			st.PendingSince = r.ActiveSince
		}
		out = append(out, st)
	}
	return out
}

// Save writes the current rules to path atomically (tmp + rename). Best-effort
// persistence so rules survive restarts/deploys.
func (e *Engine) Save(path string) error {
	data, err := json.MarshalIndent(e.List(), "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads rules from path and registers each via AddRule, preserving
// LastFired. A missing file is a no-op (fail-open). Individual invalid rules
// are skipped rather than failing the whole load.
func (e *Engine) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var rules []Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		return err
	}
	for _, r := range rules {
		_ = e.AddRule(r) // skip invalid, keep the rest
	}
	return nil
}
