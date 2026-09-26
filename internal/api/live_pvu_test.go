//go:build live

package api

// live_pvu_test.go — the LIVE proof of the per-guest counters against the home
// hypervisor.
//
// DOUBLE LOCK, like the earlier passes: the `live` build tag AND the
// LAB_PVU_LIVE=1 variable. Unlike the first pass, this proof is READ-ONLY: it
// neither creates nor deletes anything. What is being proved is that the
// dashboard now carries data it used to throw away — and that is measured by
// looking, not by touching.
//
//	run: LAB_PVU_LIVE=1 go test -tags=live -run TestLivePVU ./internal/api/ -v

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

func TestLivePVUContadoresEDoisRelogios(t *testing.T) {
	if os.Getenv("LAB_PVU_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_PVU_LIVE=1")
	}
	t0 := time.Now().UTC()
	r, cancel := routerVivo(t)
	defer cancel()

	out := pvuGETNodes(t, r)
	nos, _ := out["nodes"].([]any)
	if len(nos) == 0 {
		t.Fatal("no nodes — the proof has nothing to talk about")
	}

	// ── 1. the counters arrived, and arrived STAMPED ────────────────────────
	//
	// The assertion is about the SET and the shape, never about an exact value: RAM
	// changes between two ticks, and a nailed-down number would turn a healthy lab
	// into a failure.
	campos := []string{"cpu_frac", "cpu_cores", "mem_used", "mem_total", "mem_host",
		"disk_used", "disk_total", "net_in", "net_out", "disk_read", "disk_write",
		"net_in_rate", "net_out_rate"}
	guests, comCarimbo := 0, 0
	var linhas []string
	for _, raw := range nos {
		n, _ := raw.(map[string]any)
		if n["kind"] != "guest" {
			continue
		}
		guests++
		for _, c := range campos {
			obs, ok := n[c].(map[string]any)
			if !ok {
				t.Fatalf("%v: field %q missing or without an Observed envelope: %v", n["id"], c, n[c])
			}
			if _, ok := obs["observed_at"]; !ok {
				t.Fatalf("%v.%s without observed_at — the stamp is inescapable", n["id"], c)
			}
		}
		if n["mem_used"].(map[string]any)["observed_at"].(float64) > 0 {
			comCarimbo++
		}
		mu := n["mem_used"].(map[string]any)["value"].(float64)
		mt := n["mem_total"].(map[string]any)["value"].(float64)
		du := n["disk_used"].(map[string]any)["value"].(float64)
		cpu := n["cpu_frac"].(map[string]any)["value"].(float64)
		pctRAM := 0.0
		if mt > 0 {
			pctRAM = mu / mt * 100
		}
		disco := "não reportado"
		if du >= 0 {
			disco = fmt.Sprintf("%.1f GB", du/1e9)
		}
		linhas = append(linhas, fmt.Sprintf("%-10v cpu %5.2f%%  ram %5.1f%% (%.2f/%.2f GB)  disco %s",
			n["id"], cpu*100, pctRAM, mu/1e9, mt/1e9, disco))
	}
	for _, l := range linhas {
		t.Log(l)
	}
	if guests == 0 || comCarimbo != guests {
		t.Fatalf("%d guests, %d with a memory stamp — all of them should have one", guests, comCarimbo)
	}

	// ── 2. 🔴 QEMU's disk shows up as ABSENCE, not as zero ──────────────────
	//
	// This is the assertion that matters most here: the hypervisor's `disk: 0` had
	// to become -1 (NaoReportado) and not 0. If the guest agent is ever installed on
	// the VMs this test fails — and that is what is wanted: the rule needs revising,
	// not to go on lying quietly.
	qemus, lxcs := 0, 0
	for _, raw := range nos {
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
			if du != float64(inventory.NaoReportado) {
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

	// ── 3. 🔴 the TWO clocks exist and are independent ──────────────────────
	poll, ok := out["poll"].(map[string]any)
	if !ok {
		t.Fatal("payload without `poll` — the screen is left with only one clock")
	}
	idadeTentativa := poll["age_seconds"].(float64)
	if idadeTentativa < 0 {
		t.Fatalf("poll.age_seconds = %v — the poller just ran in this test", idadeTentativa)
	}
	if e, _ := poll["error"].(string); e != "" {
		t.Logf("⚠️ the poller's last attempt logged an error: %q", e)
	}
	var idadesDeNo []float64
	for _, raw := range nos {
		n, _ := raw.(map[string]any)
		idadesDeNo = append(idadesDeNo, n["age_seconds"].(float64))
	}
	t.Logf("two clocks: poller attempt = %.0fs · node ages = %v", idadeTentativa, idadesDeNo)

	// ── 4. NEGATIVE control for the second clock ────────────────────────────
	//
	// A clock that moves on its own proves nothing. Here the attempt's stamp is
	// PUSHED into the past in the store and the route has to show the larger age —
	// without the nodes' age moving along with it. If the two were glued together,
	// this block fails.
	if err := r.inventoryStore.Replace(func(iv *inventory.Inventory) {
		iv.LastPollAt = iv.LastPollAt - 3600
		iv.LastPollError = "controle negativo da prova viva (não é falha real)"
	}); err != nil {
		t.Fatal(err)
	}
	out2 := pvuGETNodes(t, r)
	poll2 := out2["poll"].(map[string]any)
	if poll2["age_seconds"].(float64) < idadeTentativa+3500 {
		t.Fatalf("the attempt's clock did not age: %v → %v",
			idadeTentativa, poll2["age_seconds"])
	}
	nos2, _ := out2["nodes"].([]any)
	for i, raw := range nos2 {
		n, _ := raw.(map[string]any)
		if got := n["age_seconds"].(float64); got > idadesDeNo[i]+5 {
			t.Fatalf("%v aged along with the attempt (%v → %v) — the clocks are glued together",
				n["id"], idadesDeNo[i], got)
		}
	}
	if poll2["error"].(string) == "" {
		t.Fatal("the reason did not survive into the payload")
	}
	t.Logf("negative control: attempt %.0fs → %.0fs, and no node aged along with it",
		idadeTentativa, poll2["age_seconds"].(float64))

	// ── 5. network rate: either derivable, or a declared absence ────────────
	//
	// On the first tick of a fresh store there IS no earlier observation, so -1 is
	// the right answer. What this block forbids is 0: a zero rate reads as "no
	// traffic", which is a claim about minutes nobody looked at.
	semBase, comTaxa := 0, 0
	for _, raw := range nos {
		n, _ := raw.(map[string]any)
		if n["kind"] != "guest" {
			continue
		}
		v := n["net_in_rate"].(map[string]any)["value"].(float64)
		if v < 0 {
			semBase++
		} else {
			comTaxa++
		}
		if n["net_in"].(map[string]any)["value"].(float64) <= 0 {
			t.Errorf("%v: accumulated net_in = 0 — the counter is not arriving", n["id"])
		}
	}
	t.Logf("network rate: %d with a baseline, %d without one (store's first observation)", comTaxa, semBase)

	t.Logf("proof window: [t0=%s t1=%s]", t0.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
}

// 🔴 TestLivePVUTaxaDerivaDeDUASObservacoesReais — the proof the first test
// cannot give.
//
// With a freshly created store there is ONE observation, and the correct rate is
// -1 ("nothing to derive it from"). That proves the defensive half of the rule
// and none of the useful half: an implementation that ALWAYS returned -1 would
// pass that test.
//
// Here the poller really runs against the hypervisor on a short interval, and the
// wait is ACTIVE on an EVENT (the second tick having stamped), with a deadline —
// it is not a clock wait, and the test dies in 25 s instead of hanging.
func TestLivePVUTaxaDerivaDeDUASObservacoesReais(t *testing.T) {
	if os.Getenv("LAB_PVU_LIVE") != "1" {
		t.Skip("live proof disabled — run with LAB_PVU_LIVE=1")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cofre, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := loadPVEDescriptor(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	valor, ok := scope.NewUserVault(cofre, scope.User(cfg.Primary)).Get(pveSecretAudit)
	if !ok {
		t.Fatal("no audit token in the vault")
	}
	pcfg := *pc
	pcfg.TokenID = valor
	cli, err := pve.New(pcfg)
	if err != nil {
		t.Fatal(err)
	}
	// TEMPORARY store: writing into the production data/inventory would put two
	// processes (this test and the live dashboard) writing the same document.
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

	prazo := time.Now().Add(25 * time.Second)
	var comBase, semBase []string
	for {
		inv, err := st.Snapshot()
		if err == nil {
			comBase, semBase = nil, nil
			for _, n := range inv.Nodes {
				if n.Kind != inventory.NodeKindGuest {
					continue
				}
				if n.NetInRate.Value >= 0 {
					comBase = append(comBase, fmt.Sprintf("%s ↓%dB/s ↑%dB/s", n.ID, n.NetInRate.Value, n.NetOutRate.Value))
				} else {
					semBase = append(semBase, n.ID)
				}
			}
		}
		if len(comBase) > 0 {
			break
		}
		if time.Now().After(prazo) {
			t.Fatalf("no rate derived in 25 s — without a baseline: %v", semBase)
		}
		time.Sleep(300 * time.Millisecond)
	}
	for _, l := range comBase {
		t.Log("rate derived from two real observations:" + l)
	}
	t.Logf("%d guests with a rate, %d still without a baseline", len(comBase), len(semBase))

	// And the NEGATIVE control for the same rule, with no waiting: an artificial
	// hole in the previous stamp has to erase the rate on the next tick.
	inv, _ := st.Snapshot()
	alvo := ""
	for _, n := range inv.Nodes {
		if n.Kind == inventory.NodeKindGuest && n.NetInRate.Value >= 0 {
			alvo = n.ID
			break
		}
	}
	if alvo == "" {
		t.Fatal("no target for the negative control")
	}
	if err := st.Replace(func(iv *inventory.Inventory) {
		for i := range iv.Nodes {
			if iv.Nodes[i].ID == alvo {
				// The previous counter's stamp moves back an hour: the next tick
				// sees a 3600 s "hole" with a 2 s interval configured.
				iv.Nodes[i].NetIn.ObservedAt -= 3600
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	prazo = time.Now().Add(15 * time.Second)
	for {
		inv, _ := st.Snapshot()
		for _, n := range inv.Nodes {
			if n.ID != alvo {
				continue
			}
			if n.NetIn.ObservedAt > 0 && n.NetInRate.Value == inventory.NaoReportado {
				t.Logf("negative control: a 1h hole in %s erased the rate (value = %d, and not 0)",
					alvo, n.NetInRate.Value)
				return
			}
		}
		if time.Now().After(prazo) {
			t.Fatalf("the hole did NOT erase %s's rate — an average over blind minutes would keep being published", alvo)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
