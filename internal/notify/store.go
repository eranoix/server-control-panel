package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// This file holds persistence (load/save under <DataDir>/notify/) and the
// public CRUD surface the API layer (handlers_notify.go, Commit 6) drives.
// Config mutations are copy-on-write under cfgMu: build a fresh slice/map, swap
// the pointer, persist. The worker never sees a half-mutated config.

func (r *Router) rulesPath() string    { return filepath.Join(r.dir, "rules.json") }
func (r *Router) channelsPath() string { return filepath.Join(r.dir, "channels.json") }

// load reads persisted rules + channels. Missing files are not an error (first
// boot starts empty); corrupt files are.
func (r *Router) load() error {
	var rules []Rule
	if err := readJSON(r.rulesPath(), &rules); err != nil {
		return err
	}
	var chans []ChannelDef
	if err := readJSON(r.channelsPath(), &chans); err != nil {
		return err
	}
	r.cfgMu.Lock()
	if rules != nil {
		r.rules = rules
	}
	r.channelsC = map[string]ChannelDef{}
	for _, c := range chans {
		r.channelsC[c.ID] = c
	}
	r.cfgMu.Unlock()
	return nil
}

// readJSON unmarshals path into v; a non-existent file is a no-op (v unchanged).
func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("notify: %s corrupt: %w", filepath.Base(path), err)
	}
	return nil
}

// writeJSONAtomic persists v to path via tmp+fsync+rename (mirrors
// scheduler.saveLocked): without the Sync the rename can update the inode
// before the bytes hit disk, so a power-loss could resurrect an empty file.
func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// saveRulesLocked / saveChannelsLocked persist the current config. Call under
// cfgMu.Lock (they read r.rules / r.channelsC).
func (r *Router) saveRulesLocked() error {
	return writeJSONAtomic(r.rulesPath(), r.rules)
}
func (r *Router) saveChannelsLocked() error {
	list := make([]ChannelDef, 0, len(r.channelsC))
	for _, c := range r.channelsC {
		list = append(list, c)
	}
	return writeJSONAtomic(r.channelsPath(), list)
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ── Rules CRUD ───────────────────────────────────────────────────────────────

// Rules returns a copy of the configured rules.
func (r *Router) Rules() []Rule {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	out := make([]Rule, len(r.rules))
	copy(out, r.rules)
	return out
}

// UpsertRule inserts (empty ID) or replaces a rule by ID, persists, and returns
// the stored rule. Copy-on-write: a brand-new slice is published.
func (r *Router) UpsertRule(rl Rule) (Rule, error) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	if rl.ID == "" {
		rl.ID = newID()
	}
	next := make([]Rule, 0, len(r.rules)+1)
	replaced := false
	for _, existing := range r.rules {
		if existing.ID == rl.ID {
			next = append(next, rl)
			replaced = true
		} else {
			next = append(next, existing)
		}
	}
	if !replaced {
		next = append(next, rl)
	}
	r.rules = next
	if err := r.saveRulesLocked(); err != nil {
		return Rule{}, err
	}
	return rl, nil
}

// DeleteRule removes a rule by ID (no-op if absent) and persists.
func (r *Router) DeleteRule(id string) error {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	next := make([]Rule, 0, len(r.rules))
	for _, existing := range r.rules {
		if existing.ID != id {
			next = append(next, existing)
		}
	}
	r.rules = next
	return r.saveRulesLocked()
}

// ── Channels CRUD ────────────────────────────────────────────────────────────

// ChannelDefs returns a copy of the configured destinations.
func (r *Router) ChannelDefs() []ChannelDef {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	out := make([]ChannelDef, 0, len(r.channelsC))
	for _, c := range r.channelsC {
		out = append(out, c)
	}
	return out
}

// ChannelDefsRedacted is ChannelDefs with secret fields blanked — what the GET
// endpoint returns so credentials never round-trip through the browser. A blank
// secret on a subsequent edit means "keep" (see UpsertChannel).
func (r *Router) ChannelDefsRedacted() []ChannelDef {
	defs := r.ChannelDefs()
	for i := range defs {
		defs[i].Config.BotToken = ""
		defs[i].Config.SMTPPass = ""
	}
	return defs
}

// UpsertChannel inserts (empty ID) or replaces a channel by ID and persists.
func (r *Router) UpsertChannel(c ChannelDef) (ChannelDef, error) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	if c.ID == "" {
		c.ID = newID()
	}
	// Secret preservation: the GET listing redacts BotToken/SMTPPass, so an edit
	// round-trips them blank. Treat blank-on-update as "keep existing" so the
	// operator doesn't have to re-type secrets to change other fields.
	if old, ok := r.channelsC[c.ID]; ok {
		if c.Config.BotToken == "" {
			c.Config.BotToken = old.Config.BotToken
		}
		if c.Config.SMTPPass == "" {
			c.Config.SMTPPass = old.Config.SMTPPass
		}
	}
	next := make(map[string]ChannelDef, len(r.channelsC)+1)
	for k, v := range r.channelsC {
		next[k] = v
	}
	next[c.ID] = c
	r.channelsC = next
	if err := r.saveChannelsLocked(); err != nil {
		return ChannelDef{}, err
	}
	return c, nil
}

// DeleteChannel removes a channel by ID and persists.
func (r *Router) DeleteChannel(id string) error {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	next := make(map[string]ChannelDef, len(r.channelsC))
	for k, v := range r.channelsC {
		if k != id {
			next[k] = v
		}
	}
	r.channelsC = next
	return r.saveChannelsLocked()
}

// TestChannel sends a synthetic event straight through a configured channel,
// bypassing rules/throttle/breaker — the "test" button in the UI. Returns the
// channel impl's error (or a not-found / no-impl error). Synchronous so the
// caller learns the real delivery outcome.
// SendToChannels delivers ev directly to the given channel IDs, best-effort and
// async (one goroutine per channel) so callers under a lock never block. Used by
// the scheduler's per-job "notify on finish".
func (r *Router) SendToChannels(ids []string, ev Event) {
	for _, id := range ids {
		id := id
		go func() { _ = r.TestChannel(id, ev) }()
	}
}

func (r *Router) TestChannel(id string, ev Event) error {
	r.cfgMu.RLock()
	def, ok := r.channelsC[id]
	r.cfgMu.RUnlock()
	if !ok {
		return fmt.Errorf("notify: channel %q not found", id)
	}
	impl := r.channels[def.Type]
	if impl == nil {
		return fmt.Errorf("notify: no impl for channel type %q", def.Type)
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.sendTO)
	defer cancel()
	return impl.Send(ctx, ev, def.Config)
}

// ── History / dry-run ────────────────────────────────────────────────────────

// HistoryFilter narrows the history feed. Zero value = everything (newest
// first, up to Limit).
type HistoryFilter struct {
	Source string // exact source match if non-empty
	Type   string // exact type match if non-empty
	Limit  int    // 0 -> 200
}

// History returns recent events newest-first, filtered.
func (r *Router) History(f HistoryFilter) []Event {
	r.histMu.Lock()
	all := r.history.snapshot()
	r.histMu.Unlock()

	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	out := make([]Event, 0, limit)
	for _, ev := range all {
		if f.Source != "" && ev.Source != f.Source {
			continue
		}
		if f.Type != "" && ev.Type != f.Type {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// DryRun returns the recent history events that the given rule WOULD match,
// without sending anything — the "test rule" preview. Builds confidence before
// saving a rule.
func (r *Router) DryRun(rl Rule) []Event {
	// Force-enable for preview: an operator dry-runs a draft they haven't
	// enabled yet, and wants to see what it would catch.
	rl.Enabled = true
	r.histMu.Lock()
	all := r.history.snapshot()
	r.histMu.Unlock()

	out := make([]Event, 0, 32)
	for _, ev := range all {
		if rl.matches(ev) {
			out = append(out, ev)
		}
	}
	return out
}
