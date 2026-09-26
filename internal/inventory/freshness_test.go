package inventory

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// t0 is the fixed instant of the tests. The clock is INJECTED — no test in
// this file waits for wall-clock time to pass.
var t0 = time.Date(2026, 8, 18, 23, 30, 0, 0, time.UTC)

// TestFreshness — age is born on the SERVER, with an injected clock, and
// expiry is proved in milliseconds instead of waiting out the TTL.
func TestFreshness(t *testing.T) {
	c := NewClock()
	c.now = func() time.Time { return t0 } // only possible inside the SAME package

	const ttl = 90 * time.Second

	velho := Node{ID: "lxc/207", Name: "apps", Transport: TransportPVEAPI, Kind: NodeKindGuest,
		Status: Observe("running", t0.Add(-5*time.Minute).Unix()),
		Uptime: Observe(int64(86400), t0.Add(-5*time.Minute).Unix()),
	}
	novo := Node{ID: "qemu/208", Name: "dev", Transport: TransportPVEAPI, Kind: NodeKindGuest,
		Status: Observe("running", t0.Add(-30*time.Second).Unix()),
		Uptime: Observe(int64(3600), t0.Add(-30*time.Second).Unix()),
	}
	vistas := c.View(Inventory{Nodes: []Node{velho, novo}}, ttl)
	if len(vistas) != 2 {
		t.Fatalf("View returned %d entries, want 2", len(vistas))
	}

	if vistas[0].AgeSeconds != 300 {
		t.Fatalf("age of the old node = %d s, want 300", vistas[0].AgeSeconds)
	}
	if !vistas[0].Stale {
		t.Fatalf("a node seen 300 s ago with a 90 s TTL had to be expired")
	}
	// A negative pair is mandatory: without it the test would pass with `Stale =
	// true` nailed down, and "everything stale" is just as much of a lie as
	// "everything fresh".
	if vistas[1].AgeSeconds != 30 {
		t.Fatalf("age of the new node = %d s, want 30", vistas[1].AgeSeconds)
	}
	if vistas[1].Stale {
		t.Fatal("a node seen 30 s ago with a 90 s TTL canNOT be expired")
	}

	// Advancing the injected clock by 10 min expires what was fresh — with no new
	// poll and no waiting (it is the antidote to leaning on the wall clock).
	c.now = func() time.Time { return t0.Add(10 * time.Minute) }
	depois := c.View(Inventory{Nodes: []Node{novo}}, ttl)
	if depois[0].AgeSeconds != 630 {
		t.Fatalf("age after advancing the clock = %d s, want 630", depois[0].AgeSeconds)
	}
	if !depois[0].Stale {
		t.Fatal("the node had to expire when the clock advanced")
	}

	// The NODE's age is that of the most RECENT timestamp: if any field was
	// updated, the panel heard the node at that instant.
	misto := Node{ID: "lxc/205", Name: "observ", Transport: TransportPVEAPI, Kind: NodeKindGuest,
		Status: Observe("running", t0.Add(-10*time.Second).Unix()),
		Uptime: Observe(int64(1), t0.Add(-1*time.Hour).Unix()),
	}
	c.now = func() time.Time { return t0 }
	if v := c.View(Inventory{Nodes: []Node{misto}}, ttl); v[0].AgeSeconds != 10 {
		t.Fatalf("age with different timestamps = %d, want 10 (the most recent)", v[0].AgeSeconds)
	}

	// 🔴 Never observed is NOT "0 s ago". Zero on the screen reads as just-seen —
	// the exact false green this pin exists to forbid. The marker is a negative
	// age.
	nunca := Node{ID: "lxc/299", Name: "novo", Transport: TransportSSH, Kind: NodeKindGuest}
	v := c.View(Inventory{Nodes: []Node{nunca}}, ttl)
	if v[0].AgeSeconds >= 0 {
		t.Fatalf("a never-observed node returned age %d — 0 or positive reads as fresh data", v[0].AgeSeconds)
	}
	if !v[0].Stale {
		t.Fatal("a never-observed node has to be expired")
	}
}

// TestCredentialStates — the FOUR states exist separately. The hypervisor's
// 401 is indistinguishable between revoked and expired; what breaks the tie is
// the `expire` stored locally, and it is what feeds the expiry warning.
func TestCredentialStates(t *testing.T) {
	casos := []struct {
		nome  string
		cred  Credential
		quero string
	}{
		{"bom", Credential{TokenID: "lab@pve!audit", Expire: t0.Add(30 * 24 * time.Hour).Unix()}, "ok"},
		{"sem-expire-declarado", Credential{TokenID: "lab@pve!audit"}, "ok"},
		{"ausente-do-cofre", Credential{}, "ausente"},
		{"expirada", Credential{TokenID: "lab@pve!audit", Expire: t0.Add(-time.Second).Unix()}, "expirada"},
		{"revogada", Credential{TokenID: "lab@pve!audit", State: CredRevogada}, "revogada"},
		{"revogada-e-expirada", Credential{TokenID: "lab@pve!audit", State: CredRevogada,
			Expire: t0.Add(-time.Hour).Unix()}, "revogada"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := credentialState(c.cred, t0); got != c.quero {
				t.Fatalf("state = %q, want %q", got, c.quero)
			}
		})
	}

	// The strings are a screen contract. Changing them here changes what the
	// operator reads — and the four have to be DISTINCT from one another.
	vistos := map[string]bool{}
	for _, s := range []string{CredOK, CredAusente, CredRevogada, CredExpirada} {
		if vistos[s] {
			t.Fatalf("state %q duplicated — two states merged into one", s)
		}
		vistos[s] = true
	}
	if CredOK != "ok" || CredAusente != "ausente" || CredRevogada != "revogada" || CredExpirada != "expirada" {
		t.Fatalf("the literals changed: %q %q %q %q — the screen depends on them",
			CredOK, CredAusente, CredRevogada, CredExpirada)
	}

	// View resolves the state, so nobody has to recompute it in the route.
	c := NewClock()
	c.now = func() time.Time { return t0 }
	inv := Inventory{Nodes: []Node{{ID: "lxc/207", Name: "apps", Transport: TransportPVEAPI,
		Kind: NodeKindGuest, Credential: Credential{TokenID: "lab@pve!audit", Expire: t0.Add(-time.Second).Unix()}}}}
	if got := c.View(inv, time.Minute)[0].Credential.State; got != CredExpirada {
		t.Fatalf("View did not resolve the credential state: %q", got)
	}
}

// TestViewAlwaysCarriesAge — the pin extended to the view: what the route
// serializes carries age_seconds AND observed_at, always.
func TestViewAlwaysCarriesAge(t *testing.T) {
	c := NewClock()
	c.now = func() time.Time { return t0 }
	inv := Inventory{Nodes: []Node{
		{ID: "lxc/207", Name: "apps", Transport: TransportPVEAPI, Kind: NodeKindGuest,
			Status: Observe("running", t0.Add(-time.Minute).Unix())},
		{ID: "lxc/299", Name: "nunca-visto", Transport: TransportSSH, Kind: NodeKindGuest},
		// 🔴 This case is what gives the pin teeth: observed NOW, the age is
		// 0 and stale is false — the two values the omission tag would erase
		// from the JSON. Without it the test would pass even with the tag in
		// place (measured).
		{ID: "lxc/204", Name: "lab", Transport: TransportPVEAPI, Kind: NodeKindGuest,
			Status: Observe("running", t0.Unix())},
	}}
	b, err := json.Marshal(c.View(inv, 90*time.Second))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var entradas []map[string]json.RawMessage
	if err := json.Unmarshal(b, &entradas); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entradas) != 3 {
		t.Fatalf("%d entries, want 3", len(entradas))
	}
	for i, e := range entradas {
		for _, chave := range []string{"id", "age_seconds", "stale", "status", "credential"} {
			if _, ok := e[chave]; !ok {
				t.Fatalf("entry %d (%s) does not have %q: %s", i, e["id"], chave, b)
			}
		}
		var status map[string]any
		if err := json.Unmarshal(e["status"], &status); err != nil {
			t.Fatalf("status of entry %d: %v", i, err)
		}
		if _, ok := status["observed_at"]; !ok {
			t.Fatalf("entry %d (%s) lost status.observed_at: %s", i, e["id"], b)
		}
	}
}

// TestFreshnessNaoLeORelogio — the pin for the injectable clock.
//
// A direct call to time.Now() on the calculation path hands the test back to
// the wall clock: expiry would again demand waiting (which is forbidden) and
// age would stop being reproducible. The clock comes in through a parameter or
// through the Clock's field (moulded on internal/telemetry/sink.go:37), and
// the only place that knows time.Now is the constructor.
func TestFreshnessNaoLeORelogio(t *testing.T) {
	b, err := os.ReadFile("freshness.go")
	if err != nil {
		t.Fatalf("reading its own source: %v", err)
	}
	src := string(b)
	if n := len(regexp.MustCompile(`time\.Now\(\)`).FindAllString(src, -1)); n != 0 {
		t.Fatalf("freshness.go calls time.Now() %d time(s) — the clock has to be injected", n)
	}
	if !strings.Contains(src, "now func() time.Time") {
		t.Fatal("freshness.go lost the injectable clock field (pattern from telemetry/sink.go:37)")
	}
}
