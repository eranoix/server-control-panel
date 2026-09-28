package inventory

import "time"

type PollView struct {
	ObservedAt int64  `json:"observed_at"`
	AgeSeconds int64  `json:"age_seconds"`
	Stale      bool   `json:"stale"`
	Error      string `json:"error"`
}

func ViewPoll(inv Inventory, ttl time.Duration, now time.Time) PollView {
	return PollView{
		ObservedAt: inv.LastPollAt,
		AgeSeconds: AgeSeconds(inv.LastPollAt, now),
		Stale:      Stale(inv.LastPollAt, ttl, now),
		Error:      inv.LastPollError,
	}
}
