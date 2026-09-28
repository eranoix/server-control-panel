package pve

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func TestClusterResourcesBringsPerGuestCounters(t *testing.T) {
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

	byID := map[string]Resource{}
	for _, r := range rs {
		byID[r.ID] = r
	}

	g, ok := byID["lxc/201"]
	if !ok {
		t.Fatal("lxc/201 disappeared from the fixture")
	}
	cases := []struct {
		field string
		got   int64
		want  int64
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
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("lxc/201.%s = %d, want %d — the field is not reaching the parser", c.field, c.got, c.want)
		}
	}
	if g.CPU < 0.076 || g.CPU > 0.077 {
		t.Errorf("lxc/201.cpu = %v, want ~0.0764 (a fraction already normalized by the cores)", g.CPU)
	}
	if g.Template != 0 {
		t.Errorf("lxc/201.template = %d, want 0", g.Template)
	}
}

func TestQemuDiskIsZeroAtSource(t *testing.T) {
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
	seen := 0
	for _, r := range env.Data {
		switch r.Type {
		case "qemu":
			seen++
			if r.Disk != 0 {
				t.Errorf("%s: disk = %d — QEMU started reporting disk; the 'not reported' rule needs revisiting",
					r.ID, r.Disk)
			}
			if r.MaxDisk <= 0 {
				t.Errorf("%s: maxdisk = %d — with no capacity you cannot even say 'not reported'", r.ID, r.MaxDisk)
			}
		case "lxc":
			seen++
			if r.Disk <= 0 {
				t.Errorf("%s: disk = %d — LXC always reports usage in this house; a zero here changes the rule", r.ID, r.Disk)
			}
		}
	}
	if seen < 9 {
		t.Fatalf("only %d guests in the fixture — the assertion lost its reach", seen)
	}
}
