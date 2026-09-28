//go:build live

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
	"server-control-panel/internal/scope"
	"server-control-panel/internal/secrets"
)

func pvuGETNodes(t *testing.T, r *Router) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	rq := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	r.handleNodes(w, rq.WithContext(auth.WithUser(rq.Context(), r.cfg.Primary)))
	if w.Code != 200 {
		t.Fatalf("GET /api/nodes = %d: %s", w.Code, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unreadable payload: %v", err)
	}
	return out
}

func TestLivePVUCountersAndTwoClocks(t *testing.T) {
	if os.Getenv("LAB_PVU_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_PVU_LIVE=1")
	}
	t0 := time.Now().UTC()
	r, cancel := liveRouter(t)
	defer cancel()

	out := pvuGETNodes(t, r)
	nodes, _ := out["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatal("no nodes — the proof has nothing to talk about")
	}

	fields := []string{"cpu_frac", "cpu_cores", "mem_used", "mem_total", "mem_host",
		"disk_used", "disk_total", "net_in", "net_out", "disk_read", "disk_write",
		"net_in_rate", "net_out_rate"}
	guests, stamped := 0, 0
	var lines []string
	for _, raw := range nodes {
		n, _ := raw.(map[string]any)
		if n["kind"] != "guest" {
			continue
		}
		guests++
		for _, c := range fields {
			obs, ok := n[c].(map[string]any)
			if !ok {
				t.Fatalf("%v: field %q missing or without an Observed envelope: %v", n["id"], c, n[c])
			}
			if _, ok := obs["observed_at"]; !ok {
				t.Fatalf("%v.%s without observed_at — the stamp is inescapable", n["id"], c)
			}
		}
		if n["mem_used"].(map[string]any)["observed_at"].(float64) > 0 {
			stamped++
		}
		mu := n["mem_used"].(map[string]any)["value"].(float64)
		mt := n["mem_total"].(map[string]any)["value"].(float64)
		du := n["disk_used"].(map[string]any)["value"].(float64)
		cpu := n["cpu_frac"].(map[string]any)["value"].(float64)
		pctRAM := 0.0
		if mt > 0 {
			pctRAM = mu / mt * 100
		}
		disk := "not reported"
		if du >= 0 {
			disk = fmt.Sprintf("%.1f GB", du/1e9)
		}
		lines = append(lines, fmt.Sprintf("%-10v cpu %5.2f%%  ram %5.1f%% (%.2f/%.2f GB)  disco %s",
			n["id"], cpu*100, pctRAM, mu/1e9, mt/1e9, disk))
	}
	for _, l := range lines {
		t.Log(l)
	}
	if guests == 0 || stamped != guests {
		t.Fatalf("%d guests, %d with a memory stamp — all of them should have one", guests, stamped)
	}

	qemus, lxcs := 0, 0
	for _, raw := range nodes {
		n, _ := raw.(map[string]any)
		id, _ := n["id"].(string)
		if n["kind"] != "guest" {
			continue
		}
		du := n["disk_used"].(map[string]any)["value"].(float64)
		dt := n["disk_total"].(map[string]any)["value"].(float64)
		switch {
		case strings.HasPrefix(id, "qemu/"):
			qemus++
			if du != float64(inventory.NotReported) {
				t.Errorf("%s: disk_used = %.0f — QEMU has started reporting disk; the rule needs to be REVISED", id, du)
			}
			if dt <= 0 {
				t.Errorf("%s: disk_total = %.0f — capacity is known even without the agent", id, dt)
			}
		case strings.HasPrefix(id, "lxc/"):
			lxcs++
			if du <= 0 {
				t.Errorf("%s: disk_used = %.0f — LXC reports usage here; zero here changes the rule", id, du)
			}
		}
	}
	t.Logf("disk: %d QEMU as 'not reported' · %d LXC with real usage", qemus, lxcs)
	if qemus == 0 || lxcs == 0 {
		t.Fatal("the proof needs both kinds to be worth anything")
	}

	poll, ok := out["poll"].(map[string]any)
	if !ok {
		t.Fatal("payload without `poll` — the screen is left with only one clock")
	}
	attemptAge := poll["age_seconds"].(float64)
	if attemptAge < 0 {
		t.Fatalf("poll.age_seconds = %v — the poller just ran in this test", attemptAge)
	}
	if e, _ := poll["error"].(string); e != "" {
		t.Logf("⚠️ the poller's last attempt logged an error: %q", e)
	}
	var nodeAges []float64
	for _, raw := range nodes {
		n, _ := raw.(map[string]any)
		nodeAges = append(nodeAges, n["age_seconds"].(float64))
	}
	t.Logf("two clocks: poller attempt = %.0fs · node ages = %v", attemptAge, nodeAges)

	if err := r.inventoryStore.Replace(func(iv *inventory.Inventory) {
		iv.LastPollAt = iv.LastPollAt - 3600
		iv.LastPollError = "negative control of the live probe (not a real failure)"
	}); err != nil {
		t.Fatal(err)
	}
	out2 := pvuGETNodes(t, r)
	poll2 := out2["poll"].(map[string]any)
	if poll2["age_seconds"].(float64) < attemptAge+3500 {
		t.Fatalf("the attempt's clock did not age: %v → %v",
			attemptAge, poll2["age_seconds"])
	}
	nodes2, _ := out2["nodes"].([]any)
	for i, raw := range nodes2 {
		n, _ := raw.(map[string]any)
		if got := n["age_seconds"].(float64); got > nodeAges[i]+5 {
			t.Fatalf("%v aged along with the attempt (%v → %v) — the clocks are glued together",
				n["id"], nodeAges[i], got)
		}
	}
	if poll2["error"].(string) == "" {
		t.Fatal("the reason did not survive into the payload")
	}
	t.Logf("negative control: attempt %.0fs → %.0fs, and no node aged along with it",
		attemptAge, poll2["age_seconds"].(float64))

	noBaseline, withRate := 0, 0
	for _, raw := range nodes {
		n, _ := raw.(map[string]any)
		if n["kind"] != "guest" {
			continue
		}
		v := n["net_in_rate"].(map[string]any)["value"].(float64)
		if v < 0 {
			noBaseline++
		} else {
			withRate++
		}
		if n["net_in"].(map[string]any)["value"].(float64) <= 0 {
			t.Errorf("%v: accumulated net_in = 0 — the counter is not arriving", n["id"])
		}
	}
	t.Logf("network rate: %d with a baseline, %d without one (store's first observation)", withRate, noBaseline)

	t.Logf("proof window: [t0=%s t1=%s]", t0.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
}

func TestLivePVURateDerivesFromTWORealObservations(t *testing.T) {
	if os.Getenv("LAB_PVU_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_PVU_LIVE=1")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	vault, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := loadPVEDescriptor(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	value, ok := scope.NewUserVault(vault, scope.User(cfg.Primary)).Get(pveSecretAudit)
	if !ok {
		t.Fatal("no audit token in the vault")
	}
	pcfg := *pc
	pcfg.TokenID = value
	cli, err := pve.New(pcfg)
	if err != nil {
		t.Fatal(err)
	}
	st, err := inventory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := inventory.NewPoller(st, cli, inventory.Sources{}, inventory.PollerConfig{
		Interval: 2 * time.Second,
	})
	go p.Run(ctx)

	deadline := time.Now().Add(25 * time.Second)
	var withBaseline, noBaseline []string
	for {
		inv, err := st.Snapshot()
		if err == nil {
			withBaseline, noBaseline = nil, nil
			for _, n := range inv.Nodes {
				if n.Kind != inventory.NodeKindGuest {
					continue
				}
				if n.NetInRate.Value >= 0 {
					withBaseline = append(withBaseline, fmt.Sprintf("%s ↓%dB/s ↑%dB/s", n.ID, n.NetInRate.Value, n.NetOutRate.Value))
				} else {
					noBaseline = append(noBaseline, n.ID)
				}
			}
		}
		if len(withBaseline) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no rate derived in 25 s — without a baseline: %v", noBaseline)
		}
		time.Sleep(300 * time.Millisecond)
	}
	for _, l := range withBaseline {
		t.Log("rate derived from two real observations:" + l)
	}
	t.Logf("%d guests with a rate, %d still without a baseline", len(withBaseline), len(noBaseline))

	inv, _ := st.Snapshot()
	target := ""
	for _, n := range inv.Nodes {
		if n.Kind == inventory.NodeKindGuest && n.NetInRate.Value >= 0 {
			target = n.ID
			break
		}
	}
	if target == "" {
		t.Fatal("no target for the negative control")
	}
	if err := st.Replace(func(iv *inventory.Inventory) {
		for i := range iv.Nodes {
			if iv.Nodes[i].ID == target {
				iv.Nodes[i].NetIn.ObservedAt -= 3600
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for {
		inv, _ := st.Snapshot()
		for _, n := range inv.Nodes {
			if n.ID != target {
				continue
			}
			if n.NetIn.ObservedAt > 0 && n.NetInRate.Value == inventory.NotReported {
				t.Logf("negative control: a 1h hole in %s erased the rate (value = %d, and not 0)",
					target, n.NetInRate.Value)
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the hole did NOT erase %s's rate — an average over blind minutes would keep being published", target)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
