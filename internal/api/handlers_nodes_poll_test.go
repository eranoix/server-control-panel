package api

import (
	"net/http"
	"testing"

	"server-control-panel/internal/inventory"
)

// 🔴 TestListaNosPublicaOSegundoRelogio — the route has to deliver BOTH clocks,
// not one. A node gone stale from a dead poller (our failure) and one gone stale
// from a dead node (its failure) produce an identical `age_seconds`; what
// separates them is the age of the poller's last ATTEMPT, and it only exists on
// the screen if it leaves from here.
func TestListaNosPublicaOSegundoRelogio(t *testing.T) {
	r, st := novoRouterDeNos(t, []inventory.Node{
		noDeTeste("lxc/207", "apps", 207, agoraDeTeste-600), // dado de 10 min
	})
	// The poller TRIED 4 s ago and failed. The two facts together are the
	// diagnosis: the panel is alive, it is the hypervisor that does not answer.
	if err := st.Replace(func(iv *inventory.Inventory) {
		iv.LastPollAt = agoraDeTeste - 4
		iv.LastPollError = "descoberta: hipervisor mudo"
	}); err != nil {
		t.Fatal(err)
	}

	w, out := chama(t, r, http.MethodGet, "/api/nodes", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	poll, ok := out["poll"].(map[string]any)
	if !ok {
		t.Fatalf("response without the `poll` object — the screen is left with just a clock: %s", w.Body)
	}
	if got := poll["age_seconds"]; got != float64(4) {
		t.Errorf("poll.age_seconds = %v, want 4", got)
	}
	if got := poll["error"]; got != "descoberta: hipervisor mudo" {
		t.Errorf("poll.error = %v — without the reason, 'tried' and 'tried and failed' become the same screen", got)
	}

	// And the NODE's age stays its own, not the attempt's.
	nodes, _ := out["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	n := nodes[0].(map[string]any)
	if got := n["age_seconds"]; got != float64(600) {
		t.Fatalf("the node was rejuvenated by the attempt's clock: age_seconds = %v, want 600", got)
	}
}

// TestPollNuncaTentadoNaoViraAgora — a freshly created inventory (no tick yet)
// has to show up as "never observed", never as "0 s ago".
func TestPollNuncaTentadoNaoViraAgora(t *testing.T) {
	r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste)})
	_, out := chama(t, r, http.MethodGet, "/api/nodes", "")
	poll, ok := out["poll"].(map[string]any)
	if !ok {
		t.Fatalf("response without `poll`")
	}
	if got := poll["age_seconds"]; got != float64(-1) {
		t.Fatalf("poll.age_seconds = %v, want -1 — 0 reads as 'just ran'", got)
	}
}
