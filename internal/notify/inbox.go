package notify

func (r *Router) InboxAdd(ev Event) {
	r.inboxMu.Lock()
	r.inbox.push(ev)
	r.inboxMu.Unlock()
}

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

func (r *Router) ChannelImplTypes() []string {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	out := make([]string, 0, len(r.channels))
	for t := range r.channels {
		out = append(out, t)
	}
	return out
}
