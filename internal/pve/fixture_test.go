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

// The fixtures in testdata/ are the FIRST testdata/ under internal/ in this
// repo — the ten existing httptest tests embed their JSON inline. The precedent
// is deliberate: this is the REAL shape of /cluster/resources from this
// hypervisor, measured live, and more than one piece of work downstream
// consumes it.
//
// There are two of them, because the answer depends on WHO is asking:
//   - cluster-resources.json ....... the root's view (pvesh, on the host): 15
//     entries, including storage and network, which the inventory has to
//     ignore.
//   - cluster-resources-token.json . the view of the lab@pve!audit token, which
//     is the one the panel actually receives: 10 entries — the token's ACL
//     (PVEAuditor on /vms and /nodes, without /storage) filters storage and
//     network BEFORE the response.
//
// Keeping only the first would make a parser test pass over data the panel
// never sees; keeping only the second would leave the type filter with no
// exercise at all.
const (
	fixtureRoot  = "testdata/cluster-resources.json"
	fixtureToken = "testdata/cluster-resources-token.json"
)

// recurso is the slice of /cluster/resources that the inventory uses. Counter
// fields are left out on purpose: what is proved here is the SHAPE.
type recurso struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
	Node   string `json:"node"`
	Status string `json:"status"`
}

func lerFixture(t *testing.T, path string) []recurso {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var env struct {
		Data []recurso `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(env.Data) == 0 {
		t.Fatalf("%s: empty \"data\" envelope", path)
	}
	return env.Data
}

func vmidsDeGuests(rs []recurso) []int {
	var out []int
	for _, r := range rs {
		if r.Type == "qemu" || r.Type == "lxc" {
			out = append(out, r.VMID)
		}
	}
	sort.Ints(out)
	return out
}

// TestFixtureShape compares SET against set — never a count. A magic number
// would turn a new guest into a failure and a removed guest into a wrong pass;
// the set says exactly WHO came in or went out.
func TestFixtureShape(t *testing.T) {
	esperado := []int{100, 201, 202, 203, 204, 205, 206, 207, 208}

	for _, path := range []string{fixtureRoot, fixtureToken} {
		t.Run(path, func(t *testing.T) {
			rs := lerFixture(t, path)
			got := vmidsDeGuests(rs)
			if fmt.Sprint(got) != fmt.Sprint(esperado) {
				t.Errorf("set of VMIDs = %v, want %v (missing/extra say who)", got, esperado)
			}
			for _, r := range rs {
				if r.Type != "qemu" && r.Type != "lxc" {
					continue
				}
				if r.VMID <= 0 {
					t.Errorf("%s: vmid = %d", r.ID, r.VMID)
				}
				if quer := fmt.Sprintf("%s/%d", r.Type, r.VMID); r.ID != quer {
					t.Errorf("id = %q, want %q", r.ID, quer)
				}
				if r.Name == "" || r.Node == "" || r.Status == "" {
					t.Errorf("%s: required field empty (%+v)", r.ID, r)
				}
			}
		})
	}
}

// TestFixtureVisoesDiferem pins the measured difference between the two views.
// If the two ever become identical, one of them was re-recorded from the wrong
// source — and the type-filter test would become decorative.
func TestFixtureVisoesDiferem(t *testing.T) {
	root := lerFixture(t, fixtureRoot)
	tok := lerFixture(t, fixtureToken)

	tipos := func(rs []recurso) map[string]int {
		m := map[string]int{}
		for _, r := range rs {
			m[r.Type]++
		}
		return m
	}
	tr, tt := tipos(root), tipos(tok)
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

// TestClientDecodificaFixture closes the loop: the client's own do(), serving
// the REAL fixture, has to return the guests. It proves that the {"data":…}
// envelope and the field cut match the actual hypervisor, not a convenience
// JSON written by hand.
func TestClientDecodificaFixture(t *testing.T) {
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

	var rs []recurso
	if err := c.do(context.Background(), http.MethodGet, "/api2/json/cluster/resources", &rs); err != nil {
		t.Fatalf("do: %v", err)
	}
	if got, quer := fmt.Sprint(vmidsDeGuests(rs)), "[100 201 202 203 204 205 206 207 208]"; got != quer {
		t.Fatalf("decoded VMIDs = %s, want %s", got, quer)
	}
}

// TestSemCorpoDataFalha: a 200 response without the envelope must not turn into
// a silent "empty list" — an empty inventory presented as truth is the false
// green the freshness criterion exists to forbid.
func TestSemCorpoDataFalha(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"outra":[]}`))
	})
	var rs []recurso
	err := c.do(context.Background(), http.MethodGet, "/api2/json/cluster/resources", &rs)
	if err == nil {
		t.Fatal("a 200 with no \"data\" was accepted as success")
	}
	if pe, ok := err.(*Error); !ok || pe.Kind != KindHypervisor {
		t.Fatalf("error = %v (%T), want KindHypervisor", err, err)
	}
}
