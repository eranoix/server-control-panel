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

func (r *Router) rulesPath() string    { return filepath.Join(r.dir, "rules.json") }
func (r *Router) channelsPath() string { return filepath.Join(r.dir, "channels.json") }

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

func (r *Router) Rules() []Rule {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	out := make([]Rule, len(r.rules))
	copy(out, r.rules)
	return out
}

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

func (r *Router) ChannelDefs() []ChannelDef {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	out := make([]ChannelDef, 0, len(r.channelsC))
	for _, c := range r.channelsC {
		out = append(out, c)
	}
	return out
}

func (r *Router) ChannelDefsRedacted() []ChannelDef {
	defs := r.ChannelDefs()
	for i := range defs {
		defs[i].Config.BotToken = ""
		defs[i].Config.SMTPPass = ""
	}
	return defs
}

func (r *Router) UpsertChannel(c ChannelDef) (ChannelDef, error) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	if c.ID == "" {
		c.ID = newID()
	}
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

type HistoryFilter struct {
	Source string
	Type   string
	Limit  int
}

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

func (r *Router) DryRun(rl Rule) []Event {
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
