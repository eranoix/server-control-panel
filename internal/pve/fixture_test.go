package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"testing"
)

const (
	fixtureRoot  = "testdata/cluster-resources.json"
	fixtureToken = "testdata/cluster-resources-token.json"
)

type resource struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
	Node   string `json:"node"`
	Status string `json:"status"`
}

func readFixture(t *testing.T, path string) []resource {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var env struct {
		Data []resource `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(env.Data) == 0 {
		t.Fatalf("%s: empty \"data\" envelope", path)
	}
	return env.Data
}

func guestVMIDs(rs []resource) []int {
	var out []int
	for _, r := range rs {
		if r.Type == "qemu" || r.Type == "lxc" {
			out = append(out, r.VMID)
		}
	}
	sort.Ints(out)
	return out
}

func TestFixtureShape(t *testing.T) {
	expected := []int{100, 201, 202, 203, 204, 205, 206, 207, 208}

	for _, path := range []string{fixtureRoot, fixtureToken} {
		t.Run(path, func(t *testing.T) {
			rs := readFixture(t, path)
			got := guestVMIDs(rs)
			if fmt.Sprint(got) != fmt.Sprint(expected) {
				t.Errorf("set of VMIDs = %v, want %v (missing/extra say who)", got, expected)
			}
			for _, r := range rs {
				if r.Type != "qemu" && r.Type != "lxc" {
					continue
				}
				if r.VMID <= 0 {
					t.Errorf("%s: vmid = %d", r.ID, r.VMID)
				}
				if want := fmt.Sprintf("%s/%d", r.Type, r.VMID); r.ID != want {
					t.Errorf("id = %q, want %q", r.ID, want)
				}
				if r.Name == "" || r.Node == "" || r.Status == "" {
					t.Errorf("%s: required field empty (%+v)", r.ID, r)
				}
			}
		})
	}
}

func TestFixtureViewsDiffer(t *testing.T) {
	root := readFixture(t, fixtureRoot)
	tok := readFixture(t, fixtureToken)

	kinds := func(rs []resource) map[string]int {
		m := map[string]int{}
		for _, r := range rs {
			m[r.Type]++
		}
		return m
	}
	tr, tt := kinds(root), kinds(tok)
	if tr["storage"] == 0 {
		t.Error("the root's view lost the storage entries — the parser is left with nothing to ignore")
	}
	if tt["storage"] != 0 || tt["network"] != 0 {
		t.Errorf("the token's view brought storage/network (%v) — the privsep ACL should have filtered them", tt)
	}
	if len(root) <= len(tok) {
		t.Errorf("root has %d entries and the token %d — the root's view is the superset", len(root), len(tok))
	}
}

func TestClientDecodesFixture(t *testing.T) {
	raw, err := os.ReadFile(fixtureToken)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/cluster/resources" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	})

	var rs []resource
	if err := c.do(context.Background(), http.MethodGet, "/api2/json/cluster/resources", &rs); err != nil {
		t.Fatalf("do: %v", err)
	}
	if got, want := fmt.Sprint(guestVMIDs(rs)), "[100 201 202 203 204 205 206 207 208]"; got != want {
		t.Fatalf("decoded VMIDs = %s, want %s", got, want)
	}
}

func TestMissingDataBodyFails(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"other":[]}`))
	})
	var rs []resource
	err := c.do(context.Background(), http.MethodGet, "/api2/json/cluster/resources", &rs)
	if err == nil {
		t.Fatal("a 200 with no \"data\" was accepted as success")
	}
	if pe, ok := err.(*Error); !ok || pe.Kind != KindHypervisor {
		t.Fatalf("error = %v (%T), want KindHypervisor", err, err)
	}
}
