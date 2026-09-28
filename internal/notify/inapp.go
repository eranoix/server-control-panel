package notify

import "context"

const TypeInApp = "inapp"

type InAppChannel struct{ sink func(Event) }

func NewInAppChannel(sink func(Event)) *InAppChannel { return &InAppChannel{sink: sink} }

func (i *InAppChannel) Name() string { return TypeInApp }

func (i *InAppChannel) Send(_ context.Context, ev Event, _ ChannelConfig) error {
	if i.sink != nil {
		i.sink(ev)
	}
	return nil
}
