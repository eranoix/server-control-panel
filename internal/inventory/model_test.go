package inventory

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestTransportValidation — the set of transports is CLOSED.
// Any value outside {agente, pve-api, ssh} fails, and the error quotes the value
// received (without that, a wrong transport becomes a "generic error" in the log and vanishes).
func TestTransportValidation(t *testing.T) {
	for _, tr := range []Transport{TransportAgente, TransportPVEAPI, TransportSSH} {
		n := Node{ID: "n1", Name: "n1", Transport: tr, Kind: NodeKindGuest}
		if err := n.Validate(); err != nil {
			t.Fatalf("transport %q should be accepted, got an error: %v", tr, err)
		}
	}

	// Close neighbours on purpose: empty string, wrong case, wrong separator and a
	// trailing space. All of them are plausible typos.
	for _, ruim := range []string{"", "docker", "PVE-API", "pve_api", "ssh ", "pve-api2"} {
		n := Node{ID: "n1", Name: "n1", Transport: Transport(ruim), Kind: NodeKindGuest}
		err := n.Validate()
		if err == nil {
			t.Fatalf("transport %q was ACCEPTED — the set is not closed", ruim)
		}
		if !strings.Contains(err.Error(), strconv.Quote(ruim)) {
			t.Fatalf("the transport error does not mention the received value %q: %v", ruim, err)
		}
	}

	// Kind is a closed set too.
	n := Node{ID: "n1", Name: "n1", Transport: TransportSSH, Kind: NodeKind("vm")}
	if err := n.Validate(); err == nil {
		t.Fatal("NodeKind \"vm\" was accepted — the kind set is not closed")
	}
	// An empty ID is not a node: with no key, the inventory silently merges entries.
	if err := (Node{Transport: TransportSSH, Kind: NodeKindHost}).Validate(); err == nil {
		t.Fatal("a Node with no ID was accepted")
	}
}

// TestNodeTransportRoundTrip — the transport crosses the JSON with the SAME
// literal that the panel and the live verifier compare against.
func TestNodeTransportRoundTrip(t *testing.T) {
	casos := map[Transport]string{
		TransportAgente: "agente",
		TransportPVEAPI: "pve-api",
		TransportSSH:    "ssh",
	}
	for tr, literal := range casos {
		orig := Node{ID: "lxc/207", Name: "apps", Transport: tr, Kind: NodeKindGuest, VMID: 207}
		b, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(b), `"transport":"`+literal+`"`) {
			t.Fatalf("the transport literal changed: want %q in the JSON, got %s", literal, b)
		}
		var volta Node
		if err := json.Unmarshal(b, &volta); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !reflect.DeepEqual(orig, volta) {
			t.Fatalf("asymmetric round-trip:\n orig=%+v\n back=%+v", orig, volta)
		}
		if err := volta.Validate(); err != nil {
			t.Fatalf("the node did not survive the round-trip: %v", err)
		}
	}

	// `agente` already exists in the DOMAIN, but no agent has been built yet. The
	// value has to be accepted by the model even with no support behind it.
	if !TransportAgente.Valido() {
		t.Fatal("TransportAgente has to exist in the domain even with no implementation (Phase 8)")
	}
}

// TestSerializationPin — the timestamp NEVER disappears from the JSON.
//
// This is the pin for the server-side timestamp: with `omitempty` on
// ObservedAt, a zeroed timestamp vanishes from the payload, the browser does
// not find the field and the screen goes back to showing a number with no age
// — "stale data presented as live", which is exactly the false green this test
// exists to forbid.
func TestSerializationPin(t *testing.T) {
	// Zero value on purpose: it is the case omitempty would erase.
	b, err := json.Marshal(Observed[string]{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["observed_at"]; !ok {
		t.Fatalf("observed_at VANISHED from the JSON on a zero value (omitempty?): %s", b)
	}
	if _, ok := m["value"]; !ok {
		t.Fatalf("value vanished from the JSON on a zero value: %s", b)
	}

	// The same inside a whole Node, which is what the route serializes.
	nb, err := json.Marshal(Node{})
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(nb, &raw); err != nil {
		t.Fatalf("unmarshal node: %v", err)
	}
	for _, campo := range []string{"status", "uptime", "credential", "address", "transport"} {
		if _, ok := raw[campo]; !ok {
			t.Fatalf("field %q vanished from the zeroed Node: %s", campo, nb)
		}
	}
	for _, campo := range []string{"status", "uptime"} {
		var obs map[string]any
		if err := json.Unmarshal(raw[campo], &obs); err != nil {
			t.Fatalf("%s is not an object: %v", campo, err)
		}
		if _, ok := obs["observed_at"]; !ok {
			t.Fatalf("%s.observed_at vanished from the zeroed Node: %s", campo, nb)
		}
	}

	// Symmetry: what goes out comes back identical.
	n := Node{
		ID: "qemu/208", Name: "dev", Transport: TransportPVEAPI, Kind: NodeKindGuest, VMID: 208,
		Status: Observed[string]{Value: "running", ObservedAt: 1755561234},
		Uptime: Observed[int64]{Value: 4242, ObservedAt: 1755561234},
	}
	nb2, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var volta Node
	if err := json.Unmarshal(nb2, &volta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(n, volta) {
		t.Fatalf("asymmetric round-trip:\n orig=%+v\n back=%+v", n, volta)
	}

	// Structural pin: no field of the model may pick up omitempty by carelessness
	// in the types that carry a timestamp.
	for _, tipo := range []reflect.Type{
		reflect.TypeOf(Observed[string]{}),
		reflect.TypeOf(Observed[int64]{}),
	} {
		for i := 0; i < tipo.NumField(); i++ {
			if strings.Contains(tipo.Field(i).Tag.Get("json"), "omitempty") {
				t.Fatalf("%s.%s has omitempty — the timestamp has to be inescapable",
					tipo, tipo.Field(i).Name)
			}
		}
	}
}

// TestJobRefIsReference — JobRef is a REFERENCE, never an executor.
//
// The inventory observes; what executes is internal/queue (queue.Job,
// queue.go:55) and internal/scheduler (scheduler.Job, scheduler.go:30). If
// anyone hangs an execution method or a function field here, this test fails.
func TestJobRefIsReference(t *testing.T) {
	tipo := reflect.TypeOf(JobRef{})

	querido := []string{"ID", "Kind", "NodeID", "Source"}
	if tipo.NumField() != len(querido) {
		t.Fatalf("JobRef has %d fields, want exactly %d (%v)", tipo.NumField(), len(querido), querido)
	}
	for i, nome := range querido {
		f := tipo.Field(i)
		if f.Name != nome {
			t.Fatalf("field %d is %q, want %q", i, f.Name, nome)
		}
		if f.Type.Kind() != reflect.String {
			t.Fatalf("JobRef.%s is %s — a reference only carries a string", nome, f.Type.Kind())
		}
	}
	if n := tipo.NumMethod(); n != 0 {
		t.Fatalf("JobRef gained %d method(s) — the inventory executes nothing", n)
	}
	if n := reflect.TypeOf(&JobRef{}).NumMethod(); n != 0 {
		t.Fatalf("*JobRef gained %d method(s) — the inventory executes nothing", n)
	}

	// The Source convention is the SAME as queue.Job.Source (queue.go:70):
	// "user" | "scheduler:<job_id>". The round-trip proves the literal survives.
	j := JobRef{ID: "j-1", Kind: "queue", NodeID: "lxc/207", Source: "scheduler:cron-7"}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"source":"scheduler:cron-7"`) {
		t.Fatalf("Source did not survive the JSON: %s", b)
	}
}
