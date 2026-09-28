package nodeagent

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type opKey struct{ name, result string }

type Metrics struct {
	No    string
	start time.Time

	mu  sync.Mutex
	ops map[opKey]uint64
}

func NewMetrics(no string) *Metrics {
	return &Metrics{No: no, start: time.Now(), ops: map[opKey]uint64{}}
}

func (m *Metrics) Count(name, result string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.ops[opKey{name, result}]++
	m.mu.Unlock()
}

func (m *Metrics) Render() string {
	var b strings.Builder
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	fmt.Fprintf(&b, "# HELP node_agent_uptime_seconds Time since the agent started.\n")
	fmt.Fprintf(&b, "# TYPE node_agent_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "node_agent_uptime_seconds{no=%q} %.0f\n", m.No, time.Since(m.start).Seconds())

	fmt.Fprintf(&b, "# HELP node_agent_goroutines Live goroutines.\n")
	fmt.Fprintf(&b, "# TYPE node_agent_goroutines gauge\n")
	fmt.Fprintf(&b, "node_agent_goroutines{no=%q} %d\n", m.No, runtime.NumGoroutine())

	fmt.Fprintf(&b, "# HELP node_agent_mem_bytes Allocated memory in use.\n")
	fmt.Fprintf(&b, "# TYPE node_agent_mem_bytes gauge\n")
	fmt.Fprintf(&b, "node_agent_mem_bytes{no=%q} %d\n", m.No, mem.Alloc)

	fmt.Fprintf(&b, "# HELP node_agent_ops_total Operations executed, by name and result.\n")
	fmt.Fprintf(&b, "# TYPE node_agent_ops_total counter\n")

	m.mu.Lock()
	keys := make([]opKey, 0, len(m.ops))
	for k := range m.ops {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return keys[i].result < keys[j].result
	})
	for _, k := range keys {
		fmt.Fprintf(&b, "node_agent_ops_total{no=%q,op=%q,result=%q} %d\n", m.No, k.name, k.result, m.ops[k])
	}
	m.mu.Unlock()

	return b.String()
}
