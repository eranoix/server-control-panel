package notify

import "context"

// TypeInApp is the channel type id for in-site notifications (the topbar bell).
const TypeInApp = "inapp"

// InAppChannel "delivers" by pushing the event into the Router's in-app inbox,
// which the web UI polls (GET /api/notify/inbox) to drive the notification bell
// + toasts. No external I/O — delivery is local and always succeeds (the breaker
// never trips on it). The sink is the Router's InboxAdd, wired after the Router
// is constructed (see api.initNotify).
type InAppChannel struct{ sink func(Event) }

func NewInAppChannel(sink func(Event)) *InAppChannel { return &InAppChannel{sink: sink} }

func (i *InAppChannel) Name() string { return TypeInApp }

func (i *InAppChannel) Send(_ context.Context, ev Event, _ ChannelConfig) error {
	if i.sink != nil {
		i.sink(ev)
	}
	return nil
}
