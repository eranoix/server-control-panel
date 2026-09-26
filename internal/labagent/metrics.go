package labagent

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Hand-rolled Prometheus exporter, cast from internal/api/metrics_export.go.
//
// WHY NOT client_golang: the `metrics_export.go` already running in production
// proves the format is trivial — `# HELP`, `# TYPE`, one line per series. A new
// dependency in a binary whose reason to exist is being AUDITABLE costs supply
// chain for no gain at all. The project's own rules list client_golang as an
// option; this is the measured justification for not exercising it here.

type chaveOp struct{ nome, resultado string }

// Metricas accumulates what the agent publishes.
type Metricas struct {
	No     string
	inicio time.Time

	mu  sync.Mutex
	ops map[chaveOp]uint64
}

func NovasMetricas(no string) *Metricas {
	return &Metricas{No: no, inicio: time.Now(), ops: map[chaveOp]uint64{}}
}

// Conta records one execution by operation name and outcome
// (ok / erro / desconhecida / grande).
func (m *Metricas) Conta(nome, resultado string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.ops[chaveOp{nome, resultado}]++
	m.mu.Unlock()
}

// Render returns the body of /metrics.
func (m *Metricas) Render() string {
	var b strings.Builder
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	fmt.Fprintf(&b, "# HELP lab_agent_uptime_seconds Time since the agent started.\n")
	fmt.Fprintf(&b, "# TYPE lab_agent_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "lab_agent_uptime_seconds{no=%q} %.0f\n", m.No, time.Since(m.inicio).Seconds())

	fmt.Fprintf(&b, "# HELP lab_agent_goroutines Live goroutines.\n")
	fmt.Fprintf(&b, "# TYPE lab_agent_goroutines gauge\n")
	fmt.Fprintf(&b, "lab_agent_goroutines{no=%q} %d\n", m.No, runtime.NumGoroutine())

	fmt.Fprintf(&b, "# HELP lab_agent_mem_bytes Allocated memory in use.\n")
	fmt.Fprintf(&b, "# TYPE lab_agent_mem_bytes gauge\n")
	fmt.Fprintf(&b, "lab_agent_mem_bytes{no=%q} %d\n", m.No, mem.Alloc)

	fmt.Fprintf(&b, "# HELP lab_agent_ops_total Operations executed, by name and result.\n")
	fmt.Fprintf(&b, "# TYPE lab_agent_ops_total counter\n")

	m.mu.Lock()
	chaves := make([]chaveOp, 0, len(m.ops))
	for k := range m.ops {
		chaves = append(chaves, k)
	}
	// Stable ordering: metric output whose order changes on every scrape is
	// noise in a diff and gets in the way of any manual comparison.
	sort.Slice(chaves, func(i, j int) bool {
		if chaves[i].nome != chaves[j].nome {
			return chaves[i].nome < chaves[j].nome
		}
		return chaves[i].resultado < chaves[j].resultado
	})
	for _, k := range chaves {
		fmt.Fprintf(&b, "lab_agent_ops_total{no=%q,op=%q,resultado=%q} %d\n", m.No, k.nome, k.resultado, m.ops[k])
	}
	m.mu.Unlock()

	return b.String()
}
