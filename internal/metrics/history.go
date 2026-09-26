package metrics

import "sync"

// Point is a single sample of system metrics at time T (unix seconds).
type Point struct {
	T       int64
	CPU     float64
	MemUsed uint64
	MemPct  float64
	Load1   float64
	NetSent uint64
	NetRecv uint64
	DiskPct float64
}

// Ring is a fixed-capacity in-memory ring buffer of Points, safe for
// concurrent use.
type Ring struct {
	mu   sync.RWMutex
	buf  []Point
	cap  int
	head int // next write index
	size int
}

// NewRing constructs a Ring with the given capacity. If capacity is <= 0 it
// defaults to 1440 (2 hours at a 5s sample interval).
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = 1440
	}
	return &Ring{
		buf: make([]Point, capacity),
		cap: capacity,
	}
}

// Push appends p to the ring, overwriting the oldest entry when full.
func (r *Ring) Push(p Point) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.head] = p
	r.head = (r.head + 1) % r.cap
	if r.size < r.cap {
		r.size++
	}
}

// Snapshot returns a copy of the points ordered from oldest to newest.
func (r *Ring) Snapshot() []Point {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Point, r.size)
	if r.size == 0 {
		return out
	}
	start := r.head - r.size
	if start < 0 {
		start += r.cap
	}
	for i := 0; i < r.size; i++ {
		out[i] = r.buf[(start+i)%r.cap]
	}
	return out
}

// Len returns the number of points currently stored.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}

// Last returns the most recently pushed point, or false if the ring is empty.
func (r *Ring) Last() (Point, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return Point{}, false
	}
	idx := r.head - 1
	if idx < 0 {
		idx += r.cap
	}
	return r.buf[idx], true
}
