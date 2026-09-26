package inventory

import "time"

// pollclock.go — the SECOND clock of the inventory.
//
// # Why two clocks, and not one
//
// `age_seconds` answers "how long ago did I learn this about this node". It is
// the right question, and on its own it is ambiguous: a node whose data is 40
// min old may be
//
//	(a) a mute node, with the panel asking every 30 s and getting no answer, or
//	(b) a panel that stopped asking — and then the ELEVEN nodes age together,
//	    all innocent, all accused by the same screen.
//
// What breaks the tie is the instant of the last ATTEMPT. It is global (there
// is a single loop), and it is stamped even when the attempt fails: stamping
// only the success would reproduce the defect, because a poller that has been
// failing for half an hour would be indistinguishable from a poller that died
// half an hour ago.
//
// # Why this file, and not freshness.go
//
// `freshness.go` is frozen: an empty `git diff` on it is an acceptance
// criterion and has been re-checked across three waves. This file CONSUMES the
// functions exported from there (AgeSeconds, Stale) without changing a line —
// the vocabulary of age keeps a single owner.

// PollView is the state of the LOOP, not of a node. No `omitempty`, for the
// same reason as Observed: a field that vanishes from the payload puts the
// screen back to having no data.
type PollView struct {
	ObservedAt int64 `json:"observed_at"` // unix of the last attempt; 0 = never
	AgeSeconds int64 `json:"age_seconds"` // -1 = never attempted
	Stale      bool  `json:"stale"`       // attempt older than the TTL
	// Error is the reason the LAST attempt failed, empty when it succeeded.
	// "I tried" and "I tried and could not" are different states and the screen
	// needs both: the first, with stale data, accuses the node; the second accuses
	// the path to it.
	Error string `json:"error"`
}

// ViewPoll resolves the age of the attempt on the SERVER, at the instant of
// serialization — sibling of View and of ViewHypervisor, and for the same reason.
func ViewPoll(inv Inventory, ttl time.Duration, now time.Time) PollView {
	return PollView{
		ObservedAt: inv.LastPollAt,
		AgeSeconds: AgeSeconds(inv.LastPollAt, now),
		Stale:      Stale(inv.LastPollAt, ttl, now),
		Error:      inv.LastPollError,
	}
}
