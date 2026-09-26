package singbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// baseConfig mirrors the real tunnel config shape enough to exercise device ops.
const baseConfig = `{
  "log": {"level":"info"},
  "inbounds": [
    {"type":"vless","tag":"vless-ws-in","listen_port":8080,
     "users":[{"uuid":"00000000-0000-0000-0000-000000000000"}],
     "transport":{"type":"ws","path":"/wco1wox"}},
    {"type":"vless","tag":"vless-ws-casa","listen_port":8081,
     "users":[{"uuid":"00000000-0000-0000-0000-000000000000"}],
     "transport":{"type":"ws","path":"/leb1ts"}},
    {"type":"vless","tag":"vless-reality-in","listen_port":8443,
     "users":[{"uuid":"00000000-0000-0000-0000-000000000000","flow":"xtls-rprx-vision"}]}
  ],
  "outbounds":[{"type":"direct","tag":"direct"},{"type":"socks","tag":"casa"}],
  "route":{"rules":[{"action":"resolve","strategy":"ipv4_only"}],"final":"direct"}
}`

func newTestMgr(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte(baseConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	restarts := 0
	m := New(cfg, filepath.Join(dir, "reg.json"), func(ctx context.Context) error { restarts++; return nil })
	return m, cfg
}

func usersOf(t *testing.T, cfg, tag string) []map[string]any {
	t.Helper()
	raw, _ := os.ReadFile(cfg)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid config after the write: %v", err)
	}
	for _, ib := range inbounds(doc) {
		if tg, _ := ib["tag"].(string); tg == tag {
			us, _ := ib["users"].([]any)
			out := []map[string]any{}
			for _, u := range us {
				out = append(out, u.(map[string]any))
			}
			return out
		}
	}
	return nil
}

func TestDeviceLifecycle(t *testing.T) {
	m, cfg := newTestMgr(t)
	ctx := context.Background()

	// Add
	d, err := m.Add(ctx, "pc-sam")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if d.UUID == "" || d.Exit != ExitVPS {
		t.Fatalf("unexpected device: %+v", d)
	}
	// appears in the device inbounds, NOT in ws-casa
	if got := len(usersOf(t, cfg, "vless-ws-in")); got != 2 {
		t.Fatalf("ws-in users = %d, want 2", got)
	}
	if got := len(usersOf(t, cfg, "vless-reality-in")); got != 2 {
		t.Fatalf("reality users = %d, want 2", got)
	}
	if got := len(usersOf(t, cfg, "vless-ws-casa")); got != 1 {
		t.Fatalf("ws-casa users = %d, want 1 (it must not receive devices)", got)
	}
	// reality user ganhou flow
	for _, u := range usersOf(t, cfg, "vless-reality-in") {
		if u["name"] == "pc-sam" && u["flow"] != "xtls-rprx-vision" {
			t.Fatalf("reality device with no flow vision")
		}
	}

	// List
	devs, err := m.List()
	if err != nil || len(devs) != 1 || devs[0].Name != "pc-sam" {
		t.Fatalf("List = %+v, err=%v", devs, err)
	}

	// Link
	link, err := m.Link(devs[0])
	if err != nil || link == "" {
		t.Fatalf("Link: %v", err)
	}
	if !contains(link, "path=%2Fwco1wox") || !contains(link, d.UUID) {
		t.Fatalf("the link does not have the expected path/uuid: %s", link)
	}

	// SetExit → casa
	if err := m.SetExit(ctx, d.UUID, ExitCasa); err != nil {
		t.Fatalf("SetExit casa: %v", err)
	}
	devs, _ = m.List()
	if devs[0].Exit != ExitCasa {
		t.Fatalf("exit did not become casa: %+v", devs[0])
	}
	// and the auth_user carries the name
	if !casaMembersHas(t, cfg, "pc-sam") {
		t.Fatalf("auth_user casa does not contain pc-sam")
	}

	// SetExit → vps (removes it from casa)
	if err := m.SetExit(ctx, d.UUID, ExitVPS); err != nil {
		t.Fatalf("SetExit vps: %v", err)
	}
	if casaMembersHas(t, cfg, "pc-sam") {
		t.Fatalf("auth_user casa still contains pc-sam after going back to vps")
	}

	// Rename
	if err := m.Rename(ctx, d.UUID, "notebook"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	devs, _ = m.List()
	if len(devs) != 1 || devs[0].Name != "notebook" {
		t.Fatalf("rename failed: %+v", devs)
	}

	// Remove
	if err := m.Remove(ctx, d.UUID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	devs, _ = m.List()
	if len(devs) != 0 {
		t.Fatalf("device not removed: %+v", devs)
	}
	// the shared user (unnamed) stays in ws-in — the guard does not let it empty out
	if got := len(usersOf(t, cfg, "vless-ws-in")); got != 1 {
		t.Fatalf("ws-in users after remove = %d, want 1", got)
	}
}

func TestGuardRefusesEmptyInbound(t *testing.T) {
	m, cfg := newTestMgr(t)
	// empty ws-in by hand and try to save via an Add that removes? Instead,
	// we remove the only shared user directly and save.
	raw, _ := os.ReadFile(cfg)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	for _, ib := range inbounds(doc) {
		if tg, _ := ib["tag"].(string); tg == "vless-ws-in" {
			ib["users"] = []any{}
		}
	}
	if err := m.save(doc); err == nil {
		t.Fatalf("the guard should refuse an inbound for a device with no users")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func casaMembersHas(t *testing.T, cfg, name string) bool {
	t.Helper()
	raw, _ := os.ReadFile(cfg)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	return casaMembers(doc)[name]
}

// rulesOf returns the route.rules as maps for assertions.
func rulesOf(t *testing.T, cfg string) []map[string]any {
	t.Helper()
	raw, _ := os.ReadFile(cfg)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid config: %v", err)
	}
	route, _ := doc["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	out := []map[string]any{}
	for _, r := range rules {
		if rm, ok := r.(map[string]any); ok {
			out = append(out, rm)
		}
	}
	return out
}

func hasRule(rules []map[string]any, outbound, user string, wantReject bool) bool {
	for _, rm := range rules {
		if wantReject {
			if act, _ := rm["action"].(string); act != "reject" {
				continue
			}
		} else if out, _ := rm["outbound"].(string); out != outbound {
			continue
		}
		list, _ := rm["auth_user"].([]any)
		for _, v := range list {
			if s, _ := v.(string); s == user {
				return true
			}
		}
	}
	return false
}

// TestDatasaverMatrix exercises the (datasaver on/off) × (exit vps/casa) routing.
func TestDatasaverMatrix(t *testing.T) {
	m, cfg := newTestMgr(t)
	ctx := context.Background()

	pc, _ := m.Add(ctx, "pc")
	cel, _ := m.Add(ctx, "cel")

	// cel → casa exit; pc stays vps
	if err := m.SetExit(ctx, cel.UUID, ExitCasa); err != nil {
		t.Fatal(err)
	}
	// both datasaver ON
	if err := m.SetDatasaver(ctx, pc.UUID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.SetDatasaver(ctx, cel.UUID, true); err != nil {
		t.Fatal(err)
	}

	// List reflects flags
	devs, _ := m.List()
	byName := map[string]Device{}
	for _, d := range devs {
		byName[d.Name] = d
	}
	if !byName["pc"].Datasaver || byName["pc"].Exit != ExitVPS {
		t.Fatalf("pc expected ds+vps: %+v", byName["pc"])
	}
	if !byName["cel"].Datasaver || byName["cel"].Exit != ExitCasa {
		t.Fatalf("cel expected ds+casa: %+v", byName["cel"])
	}

	rules := rulesOf(t, cfg)
	// pc (ds, vps) → proxy-vps ; cel (ds, casa) → proxy-casa
	if !hasRule(rules, "proxy-vps", "pc", false) {
		t.Fatalf("proxy-vps missing for pc: %+v", rules)
	}
	if !hasRule(rules, "proxy-casa", "cel", false) {
		t.Fatalf("proxy-casa missing for cel: %+v", rules)
	}
	// QUIC reject cobre ambos
	if !hasRule(rules, "", "pc", true) || !hasRule(rules, "", "cel", true) {
		t.Fatalf("the QUIC reject is missing for the ds devices: %+v", rules)
	}
	// cel still has casa for non-web
	if !hasRule(rules, "casa", "cel", false) {
		t.Fatalf("cel should keep the casa rule (non-web): %+v", rules)
	}

	// Turn pc's datasaver off → no proxy-vps, and since pc is vps no rule of its own is left
	if err := m.SetDatasaver(ctx, pc.UUID, false); err != nil {
		t.Fatal(err)
	}
	rules = rulesOf(t, cfg)
	if hasRule(rules, "proxy-vps", "pc", false) {
		t.Fatalf("pc's proxy-vps should have disappeared: %+v", rules)
	}
	// the http-casa-in bridge does NOT exist in this baseConfig, but the resolve rule must
	// still be the first one (preserved).
	if act, _ := rules[0]["action"].(string); act != "resolve" {
		t.Fatalf("the resolve rule should still come 1st: %+v", rules[0])
	}
}

func TestProxyEndpoint(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte(`{
	  "inbounds":[{"type":"vless","tag":"vless-ws-in","users":[{"uuid":"x","name":"d"}],"transport":{"type":"ws","path":"/p"}}],
	  "outbounds":[
	    {"type":"direct","tag":"direct"},
	    {"type":"http","tag":"proxy-vps","server":"172.18.0.40","server_port":8080},
	    {"type":"http","tag":"proxy-casa","server":"172.18.0.41","server_port":8080}
	  ],
	  "route":{"rules":[{"action":"resolve","strategy":"ipv4_only"}],"final":"direct"}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(cfg, filepath.Join(dir, "reg.json"), func(ctx context.Context) error { return nil })
	if ep, err := m.ProxyEndpoint(ExitVPS); err != nil || ep != "172.18.0.40:8080" {
		t.Fatalf("ProxyEndpoint(vps) = %q, %v; want 172.18.0.40:8080", ep, err)
	}
	if ep, err := m.ProxyEndpoint(ExitCasa); err != nil || ep != "172.18.0.41:8080" {
		t.Fatalf("ProxyEndpoint(casa) = %q, %v; want 172.18.0.41:8080", ep, err)
	}
	// missing outbound → a clear error, not a panic
	os.WriteFile(cfg, []byte(`{"inbounds":[{"type":"vless","tag":"vless-ws-in","users":[{"uuid":"x","name":"d"}]}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"rules":[],"final":"direct"}}`), 0o644)
	if _, err := m.ProxyEndpoint(ExitCasa); err == nil {
		t.Fatal("ProxyEndpoint should fail when the outbound proxy does not exist")
	}
}

func TestLinkReality(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	os.WriteFile(cfg, []byte(`{
	  "inbounds":[
	    {"type":"vless","tag":"vless-ws-in","users":[{"uuid":"u1","name":"pc"}],"transport":{"type":"ws","path":"/p"}},
	    {"type":"vless","tag":"vless-reality-in","users":[{"uuid":"u1","name":"pc","flow":"xtls-rprx-vision"}],
	     "tls":{"server_name":"www.icloud.com","reality":{"short_id":["bf31e69bee369291"]}}}
	  ],
	  "outbounds":[{"type":"direct","tag":"direct"}],
	  "route":{"rules":[{"action":"resolve","strategy":"ipv4_only"}],"final":"direct"}
	}`), 0o644)
	m := New(cfg, filepath.Join(dir, "reg.json"), func(ctx context.Context) error { return nil })
	link, err := m.LinkReality(Device{Name: "pc", UUID: "u1"}, 2054)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"security=reality", "sni=www.icloud.com", "sid=bf31e69bee369291", "fp=chrome", "flow=xtls-rprx-vision", ":2054", "pc-reality"} {
		if !contains(link, want) {
			t.Fatalf("Reality link with no %q: %s", want, link)
		}
	}
}
