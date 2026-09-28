package inventory

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestTransportValidation(t *testing.T) {
	for _, tr := range []Transport{TransportAgent, TransportPVEAPI, TransportSSH} {
		n := Node{ID: "n1", Name: "n1", Transport: tr, Kind: NodeKindGuest}
		if err := n.Validate(); err != nil {
			t.Fatalf("transport %q should be accepted, got an error: %v", tr, err)
		}
	}

	for _, bad := range []string{"", "docker", "PVE-API", "pve_api", "ssh ", "pve-api2"} {
		n := Node{ID: "n1", Name: "n1", Transport: Transport(bad), Kind: NodeKindGuest}
		err := n.Validate()
		if err == nil {
			t.Fatalf("transport %q was ACCEPTED — the set is not closed", bad)
		}
		if !strings.Contains(err.Error(), strconv.Quote(bad)) {
			t.Fatalf("the transport error does not mention the received value %q: %v", bad, err)
		}
	}

	n := Node{ID: "n1", Name: "n1", Transport: TransportSSH, Kind: NodeKind("vm")}
	if err := n.Validate(); err == nil {
		t.Fatal("NodeKind \"vm\" was accepted — the kind set is not closed")
	}
	if err := (Node{Transport: TransportSSH, Kind: NodeKindHost}).Validate(); err == nil {
		t.Fatal("a Node with no ID was accepted")
	}
}

func TestNodeTransportRoundTrip(t *testing.T) {
	cases := map[Transport]string{
		TransportAgent:  "agent",
		TransportPVEAPI: "pve-api",
		TransportSSH:    "ssh",
	}
	for tr, literal := range cases {
		orig := Node{ID: "lxc/207", Name: "apps", Transport: tr, Kind: NodeKindGuest, VMID: 207}
		b, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(b), `"transport":"`+literal+`"`) {
			t.Fatalf("the transport literal changed: want %q in the JSON, got %s", literal, b)
		}
		var decoded Node
		if err := json.Unmarshal(b, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !reflect.DeepEqual(orig, decoded) {
			t.Fatalf("asymmetric round-trip:\n orig=%+v\n back=%+v", orig, decoded)
		}
		if err := decoded.Validate(); err != nil {
			t.Fatalf("the node did not survive the round-trip: %v", err)
		}
	}

	if !TransportAgent.Valid() {
		t.Fatal("TransportAgent has to exist in the domain even with no implementation (Phase 8)")
	}
}

func TestSerializationPin(t *testing.T) {
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

	nb, err := json.Marshal(Node{})
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(nb, &raw); err != nil {
		t.Fatalf("unmarshal node: %v", err)
	}
	for _, field := range []string{"status", "uptime", "credential", "address", "transport"} {
		if _, ok := raw[field]; !ok {
			t.Fatalf("field %q vanished from the zeroed Node: %s", field, nb)
		}
	}
	for _, field := range []string{"status", "uptime"} {
		var obs map[string]any
		if err := json.Unmarshal(raw[field], &obs); err != nil {
			t.Fatalf("%s is not an object: %v", field, err)
		}
		if _, ok := obs["observed_at"]; !ok {
			t.Fatalf("%s.observed_at vanished from the zeroed Node: %s", field, nb)
		}
	}

	n := Node{
		ID: "qemu/208", Name: "dev", Transport: TransportPVEAPI, Kind: NodeKindGuest, VMID: 208,
		Status: Observed[string]{Value: "running", ObservedAt: 1755561234},
		Uptime: Observed[int64]{Value: 4242, ObservedAt: 1755561234},
	}
	nb2, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Node
	if err := json.Unmarshal(nb2, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(n, decoded) {
		t.Fatalf("asymmetric round-trip:\n orig=%+v\n back=%+v", n, decoded)
	}

	for _, kind := range []reflect.Type{
		reflect.TypeOf(Observed[string]{}),
		reflect.TypeOf(Observed[int64]{}),
	} {
		for i := 0; i < kind.NumField(); i++ {
			if strings.Contains(kind.Field(i).Tag.Get("json"), "omitempty") {
				t.Fatalf("%s.%s has omitempty — the timestamp has to be inescapable",
					kind, kind.Field(i).Name)
			}
		}
	}
}

func TestJobRefIsReference(t *testing.T) {
	kind := reflect.TypeOf(JobRef{})

	wanted := []string{"ID", "Kind", "NodeID", "Source"}
	if kind.NumField() != len(wanted) {
		t.Fatalf("JobRef has %d fields, want exactly %d (%v)", kind.NumField(), len(wanted), wanted)
	}
	for i, name := range wanted {
		f := kind.Field(i)
		if f.Name != name {
			t.Fatalf("field %d is %q, want %q", i, f.Name, name)
		}
		if f.Type.Kind() != reflect.String {
			t.Fatalf("JobRef.%s is %s — a reference only carries a string", name, f.Type.Kind())
		}
	}
	if n := kind.NumMethod(); n != 0 {
		t.Fatalf("JobRef gained %d method(s) — the inventory executes nothing", n)
	}
	if n := reflect.TypeOf(&JobRef{}).NumMethod(); n != 0 {
		t.Fatalf("*JobRef gained %d method(s) — the inventory executes nothing", n)
	}

	j := JobRef{ID: "j-1", Kind: "queue", NodeID: "lxc/207", Source: "scheduler:cron-7"}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"source":"scheduler:cron-7"`) {
		t.Fatalf("Source did not survive the JSON: %s", b)
	}
}
