package api

import (
	"net/http"
	"testing"

	"server-control-panel/internal/inventory"
)

func TestListNodesPublishesSecondClock(t *testing.T) {
	r, st := newNodesRouter(t, []inventory.Node{
		testNode("lxc/207", "apps", 207, testNow-600),
	})
	if err := st.Replace(func(iv *inventory.Inventory) {
		iv.LastPollAt = testNow - 4
		iv.LastPollError = "discovery: hypervisor silent"
	}); err != nil {
		t.Fatal(err)
	}

	w, out := callAPI(t, r, http.MethodGet, "/api/nodes", "")
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
	if got := poll["error"]; got != "discovery: hypervisor silent" {
		t.Errorf("poll.error = %v — without the reason, 'tried' and 'tried and failed' become the same screen", got)
	}

	nodes, _ := out["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	n := nodes[0].(map[string]any)
	if got := n["age_seconds"]; got != float64(600) {
		t.Fatalf("the node was rejuvenated by the attempt's clock: age_seconds = %v, want 600", got)
	}
}

func TestNeverAttemptedPollIsNotNow(t *testing.T) {
	r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow)})
	_, out := callAPI(t, r, http.MethodGet, "/api/nodes", "")
	poll, ok := out["poll"].(map[string]any)
	if !ok {
		t.Fatalf("response without `poll`")
	}
	if got := poll["age_seconds"]; got != float64(-1) {
		t.Fatalf("poll.age_seconds = %v, want -1 — 0 reads as 'just ran'", got)
	}
}
