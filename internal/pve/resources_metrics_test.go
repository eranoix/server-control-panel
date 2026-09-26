package pve

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

// resources_metrics_test.go — the pin for the per-guest COUNTERS.
//
// # Why they come in now, after having been excluded on purpose
//
// The original comment in resources.go said counters were left out because
// "the inventory publishes IDENTITY and STATE, not telemetry; whoever wants a
// time series has Prometheus". The sentence is still right about the SERIES. It
// was wrong about STATE: how much RAM a guest is using RIGHT NOW is state, not
// series — and it was exactly that state the screen did not have.
//
// The measurement that decided it (audit token, the home hypervisor): qemu/208
// `dev` was sitting at 7.19 GB of 8.59 GB of RAM — 83.7% — and no screen in the
// panel showed it. This is not a layout preference: it is data the hypervisor
// delivers in every response and that the panel threw away on every tick.

// TestClusterResourcesTrazContadoresPorGuest proves that the parser stopped
// discarding what the hypervisor sends. The assertion is on the TOKEN fixture —
// the view the panel actually receives.
func TestClusterResourcesTrazContadoresPorGuest(t *testing.T) {
	raw, err := os.ReadFile(fixtureToken)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})
	rs, err := c.ClusterResources(context.Background())
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}

	porID := map[string]Resource{}
	for _, r := range rs {
		porID[r.ID] = r
	}

	// What the fixture (measured live) says about the lxc/201 `games`.
	g, ok := porID["lxc/201"]
	if !ok {
		t.Fatal("lxc/201 disappeared from the fixture")
	}
	casos := []struct {
		campo string
		got   int64
		quer  int64
	}{
		{"maxcpu", int64(g.MaxCPU), 8},
		{"mem", g.Mem, 2293985280},
		{"maxmem", g.MaxMem, 17179869184},
		{"disk", g.Disk, 11409686528},
		{"maxdisk", g.MaxDisk, 51539607552},
		{"netin", g.NetIn, 6179177398},
		{"netout", g.NetOut, 4447249313},
		{"diskread", g.DiskRead, 1868767232},
		{"diskwrite", g.DiskWrite, 504193024},
	}
	for _, c := range casos {
		if c.got != c.quer {
			t.Errorf("lxc/201.%s = %d, want %d — the field is not reaching the parser", c.campo, c.got, c.quer)
		}
	}
	if g.CPU < 0.076 || g.CPU > 0.077 {
		t.Errorf("lxc/201.cpu = %v, want ~0.0764 (a fraction already normalized by the cores)", g.CPU)
	}
	if g.Template != 0 {
		t.Errorf("lxc/201.template = %d, want 0", g.Template)
	}
}

// 🔴 TestDiscoDeQemuVemZeroNaFonte is the pin for the TRAP, and it lives here
// on purpose: if one day the hypervisor starts reporting QEMU disk (guest agent
// installed), this test fails and forces a REVIEW of the "not reported" rule
// instead of leaving it lying in silence.
//
// Measured on both QEMU guests of this house (qemu/100 `painel` and qemu/208
// `dev`): `disk: 0` with a real `maxdisk`. On the EIGHT LXC, real disk.
func TestDiscoDeQemuVemZeroNaFonte(t *testing.T) {
	raw, err := os.ReadFile(fixtureToken)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data []struct {
			ID      string `json:"id"`
			Type    string `json:"type"`
			Disk    int64  `json:"disk"`
			MaxDisk int64  `json:"maxdisk"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	vistos := 0
	for _, r := range env.Data {
		switch r.Type {
		case "qemu":
			vistos++
			if r.Disk != 0 {
				t.Errorf("%s: disk = %d — QEMU started reporting disk; the 'not reported' rule needs revisiting",
					r.ID, r.Disk)
			}
			if r.MaxDisk <= 0 {
				t.Errorf("%s: maxdisk = %d — with no capacity you cannot even say 'not reported'", r.ID, r.MaxDisk)
			}
		case "lxc":
			vistos++
			if r.Disk <= 0 {
				t.Errorf("%s: disk = %d — LXC always reports usage in this house; a zero here changes the rule", r.ID, r.Disk)
			}
		}
	}
	if vistos < 9 {
		t.Fatalf("only %d guests in the fixture — the assertion lost its reach", vistos)
	}
}
