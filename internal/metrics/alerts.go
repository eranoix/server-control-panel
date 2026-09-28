package metrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

type Rule struct {
	Name        string  `json:"name"`
	Metric      string  `json:"metric,omitempty"`
	Field       string  `json:"field,omitempty"`
	Op          string  `json:"op"`
	Threshold   float64 `json:"threshold"`
	Duration    int     `json:"duration"`
	LastFired   int64   `json:"last_fired"`
	Severity    string  `json:"severity,omitempty"`
	ActiveSince int64   `json:"active_since,omitempty"`
	RearmMargin float64 `json:"rearm_margin,omitempty"`
	RenotifySec int     `json:"renotify_sec,omitempty"`
}

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

type Fire struct {
	Rule     string
	Time     int64
	Value    float64
	Severity string `json:"severity,omitempty"`
	Episode  int64  `json:"episode,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
}

type RuleStatus struct {
	Rule
	State        string  `json:"state"`
	CurrentValue float64 `json:"current_value"`
	PendingSince int64   `json:"pending_since,omitempty"`
	MetricKey    string  `json:"metric_key"`
	Label        string  `json:"label,omitempty"`
	Unit         string  `json:"unit,omitempty"`
}

const staleAfter = 30

type Engine struct {
	mu        sync.RWMutex
	rules     map[string]Rule
	firstTrue map[string]int64
	lastSnap  Snapshot
}

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
	if r.ActiveSince != 0 {
		e.firstTrue[r.Name] = r.ActiveSince
	} else {
		delete(e.firstTrue, r.Name)
	}
	return nil
}

func (e *Engine) RemoveRule(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.rules, name)
	delete(e.firstTrue, name)
}

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

func rearmAllowed(v float64, r Rule) bool {
	switch r.Op {
	case ">", ">=":
		return v <= r.Threshold-r.RearmMargin
	case "<", "<=":
		return v >= r.Threshold+r.RearmMargin
	}
	return true
}

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
				r.ActiveSince = now
				r.LastFired = now
				e.rules[name] = r
				fires = append(fires, Fire{Rule: name, Time: now, Value: v, Severity: r.Severity, Episode: now})
			case r.ActiveSince != 0 && r.RenotifySec > 0 && now-r.LastFired >= int64(r.RenotifySec):
				r.LastFired = now
				e.rules[name] = r
				fires = append(fires, Fire{Rule: name, Time: now, Value: v, Severity: r.Severity, Episode: r.ActiveSince})
			}
		case ok && r.ActiveSince != 0 && rearmAllowed(v, r):
			ep := r.ActiveSince
			r.ActiveSince = 0
			e.rules[name] = r
			delete(e.firstTrue, name)
			fires = append(fires, Fire{Rule: name, Time: now, Value: v, Severity: r.Severity, Episode: ep, Resolved: true})
		default:
			if r.ActiveSince == 0 {
				delete(e.firstTrue, name)
			}
		}
	}
	return fires
}

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
				ft = now
			}
			st.PendingSince = ft
			if now-ft >= int64(r.Duration) {
				st.State = "firing"
			} else {
				st.State = "pending"
			}
		case r.ActiveSince != 0:
			st.State = "firing"
			st.PendingSince = r.ActiveSince
		}
		out = append(out, st)
	}
	return out
}

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
		_ = e.AddRule(r)
	}
	return nil
}
