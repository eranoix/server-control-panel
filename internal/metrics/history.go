package metrics

import "sync"

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

type Ring struct {
	mu   sync.RWMutex
	buf  []Point
	cap  int
	head int
	size int
}

func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = 1440
	}
	return &Ring{
		buf: make([]Point, capacity),
		cap: capacity,
	}
}

func (r *Ring) Push(p Point) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.head] = p
	r.head = (r.head + 1) % r.cap
	if r.size < r.cap {
		r.size++
	}
}

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

func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}

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
