package inventory

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 8, 18, 23, 30, 0, 0, time.UTC)

func TestFreshness(t *testing.T) {
	c := NewClock()
	c.now = func() time.Time { return t0 }

	const ttl = 90 * time.Second

	stale := Node{ID: "lxc/207", Name: "apps", Transport: TransportPVEAPI, Kind: NodeKindGuest,
		Status: Observe("running", t0.Add(-5*time.Minute).Unix()),
		Uptime: Observe(int64(86400), t0.Add(-5*time.Minute).Unix()),
	}
	fresh := Node{ID: "qemu/208", Name: "dev", Transport: TransportPVEAPI, Kind: NodeKindGuest,
		Status: Observe("running", t0.Add(-30*time.Second).Unix()),
		Uptime: Observe(int64(3600), t0.Add(-30*time.Second).Unix()),
	}
	seen := c.View(Inventory{Nodes: []Node{stale, fresh}}, ttl)
	if len(seen) != 2 {
		t.Fatalf("View returned %d entries, want 2", len(seen))
	}

	if seen[0].AgeSeconds != 300 {
		t.Fatalf("age of the old node = %d s, want 300", seen[0].AgeSeconds)
	}
	if !seen[0].Stale {
		t.Fatalf("a node seen 300 s ago with a 90 s TTL had to be expired")
	}
	if seen[1].AgeSeconds != 30 {
		t.Fatalf("age of the new node = %d s, want 30", seen[1].AgeSeconds)
	}
	if seen[1].Stale {
		t.Fatal("a node seen 30 s ago with a 90 s TTL canNOT be expired")
	}

	c.now = func() time.Time { return t0.Add(10 * time.Minute) }
	after := c.View(Inventory{Nodes: []Node{fresh}}, ttl)
	if after[0].AgeSeconds != 630 {
		t.Fatalf("age after advancing the clock = %d s, want 630", after[0].AgeSeconds)
	}
	if !after[0].Stale {
		t.Fatal("the node had to expire when the clock advanced")
	}

	mixed := Node{ID: "lxc/205", Name: "observ", Transport: TransportPVEAPI, Kind: NodeKindGuest,
		Status: Observe("running", t0.Add(-10*time.Second).Unix()),
		Uptime: Observe(int64(1), t0.Add(-1*time.Hour).Unix()),
	}
	c.now = func() time.Time { return t0 }
	if v := c.View(Inventory{Nodes: []Node{mixed}}, ttl); v[0].AgeSeconds != 10 {
		t.Fatalf("age with different timestamps = %d, want 10 (the most recent)", v[0].AgeSeconds)
	}

	never := Node{ID: "lxc/299", Name: "new", Transport: TransportSSH, Kind: NodeKindGuest}
	v := c.View(Inventory{Nodes: []Node{never}}, ttl)
	if v[0].AgeSeconds >= 0 {
		t.Fatalf("a never-observed node returned age %d — 0 or positive reads as fresh data", v[0].AgeSeconds)
	}
	if !v[0].Stale {
		t.Fatal("a never-observed node has to be expired")
	}
}

func TestCredentialStates(t *testing.T) {
	cases := []struct {
		name string
		cred Credential
		want string
	}{
		{"bom", Credential{TokenID: "panel@pve!audit", Expire: t0.Add(30 * 24 * time.Hour).Unix()}, "ok"},
		{"no-declared-expire", Credential{TokenID: "panel@pve!audit"}, "ok"},
		{"missing-from-vault", Credential{}, "absent"},
		{"expired", Credential{TokenID: "panel@pve!audit", Expire: t0.Add(-time.Second).Unix()}, "expired"},
		{"revoked", Credential{TokenID: "panel@pve!audit", State: CredRevoked}, "revoked"},
		{"revoked-and-expired", Credential{TokenID: "panel@pve!audit", State: CredRevoked,
			Expire: t0.Add(-time.Hour).Unix()}, "revoked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := credentialState(c.cred, t0); got != c.want {
				t.Fatalf("state = %q, want %q", got, c.want)
			}
		})
	}

	seen := map[string]bool{}
	for _, s := range []string{CredOK, CredMissing, CredRevoked, CredExpired} {
		if seen[s] {
			t.Fatalf("state %q duplicated — two states merged into one", s)
		}
		seen[s] = true
	}
	if CredOK != "ok" || CredMissing != "absent" || CredRevoked != "revoked" || CredExpired != "expired" {
		t.Fatalf("the literals changed: %q %q %q %q — the screen depends on them",
			CredOK, CredMissing, CredRevoked, CredExpired)
	}

	c := NewClock()
	c.now = func() time.Time { return t0 }
	inv := Inventory{Nodes: []Node{{ID: "lxc/207", Name: "apps", Transport: TransportPVEAPI,
		Kind: NodeKindGuest, Credential: Credential{TokenID: "panel@pve!audit", Expire: t0.Add(-time.Second).Unix()}}}}
	if got := c.View(inv, time.Minute)[0].Credential.State; got != CredExpired {
		t.Fatalf("View did not resolve the credential state: %q", got)
	}
}

func TestViewAlwaysCarriesAge(t *testing.T) {
	c := NewClock()
	c.now = func() time.Time { return t0 }
	inv := Inventory{Nodes: []Node{
		{ID: "lxc/207", Name: "apps", Transport: TransportPVEAPI, Kind: NodeKindGuest,
			Status: Observe("running", t0.Add(-time.Minute).Unix())},
		{ID: "lxc/299", Name: "never-seen", Transport: TransportSSH, Kind: NodeKindGuest},
		{ID: "lxc/204", Name: "lab", Transport: TransportPVEAPI, Kind: NodeKindGuest,
			Status: Observe("running", t0.Unix())},
	}}
	b, err := json.Marshal(c.View(inv, 90*time.Second))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(b, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("%d entries, want 3", len(entries))
	}
	for i, e := range entries {
		for _, key := range []string{"id", "age_seconds", "stale", "status", "credential"} {
			if _, ok := e[key]; !ok {
				t.Fatalf("entry %d (%s) does not have %q: %s", i, e["id"], key, b)
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

func TestFreshnessDoesNotReadClock(t *testing.T) {
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
