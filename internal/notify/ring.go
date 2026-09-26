package notify

// ring is a fixed-capacity circular buffer of Events used for the in-app
// history feed and rule dry-runs. It is NOT thread-safe on its own; the Router
// guards every access with histMu (Dispatch writes, History/DryRun read).
type ring struct {
	buf   []Event
	next  int // index of the next write
	count int // number of valid entries (<= len(buf))
}

func newRing(capacity int) *ring {
	if capacity < 1 {
		capacity = 1
	}
	return &ring{buf: make([]Event, capacity)}
}

func (r *ring) push(ev Event) {
	r.buf[r.next] = ev
	r.next = (r.next + 1) % len(r.buf)
	if r.count < len(r.buf) {
		r.count++
	}
}

// snapshot returns the buffered events newest-first.
func (r *ring) snapshot() []Event {
	out := make([]Event, 0, r.count)
	n := len(r.buf)
	for i := 0; i < r.count; i++ {
		idx := ((r.next-1-i)%n + n) % n
		out = append(out, r.buf[idx])
	}
	return out
}
