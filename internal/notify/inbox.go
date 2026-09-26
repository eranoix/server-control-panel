package notify

// In-app inbox + dynamic channel registration. The inbox is a second ring (next
// to history) that the InAppChannel feeds and the web UI polls to drive the
// notification bell. AddChannelImpl lets the app wire the InAppChannel AFTER the
// Router exists (it needs InboxAdd as its sink), copy-on-write so the worker's
// lock-free read of the impls snapshot stays race-clean.

// InboxAdd records an event in the in-app inbox. Called from the worker via the
// InAppChannel sink; guarded by its own mutex (never the worker-only locks).
func (r *Router) InboxAdd(ev Event) {
	r.inboxMu.Lock()
	r.inbox.push(ev)
	r.inboxMu.Unlock()
}

// Inbox returns the in-app inbox newest-first, up to limit (0 -> 50).
func (r *Router) Inbox(limit int) []Event {
	r.inboxMu.Lock()
	all := r.inbox.snapshot()
	r.inboxMu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// AddChannelImpl registers (or replaces) a channel implementation by type after
// construction. Copy-on-write under cfgMu so handle()'s snapshot read is safe.
func (r *Router) AddChannelImpl(typ string, ch Channel) {
	r.cfgMu.Lock()
	next := make(map[string]Channel, len(r.channels)+1)
	for k, v := range r.channels {
		next[k] = v
	}
	next[typ] = ch
	r.channels = next
	r.cfgMu.Unlock()
}

// ChannelImplTypes returns the registered channel type ids (for the UI catalog).
func (r *Router) ChannelImplTypes() []string {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	out := make([]string, 0, len(r.channels))
	for t := range r.channels {
		out = append(out, t)
	}
	return out
}
