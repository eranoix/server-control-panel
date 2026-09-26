package mobilebff

// ops_metrics_test.go covers the "resources" half of GET /ops/status: the exact
// shape of the JSON (raw/formatted pairs), the mount filter that exists so the
// dashboard does not open shouting "disk full" because of the snaps, the choice
// of uplink interface, the derivation of the network rate from two samples, and
// the absence contract (with no dependency wired, the `system` field drops out
// of the bytes — it never becomes a block of zeros).
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/system"
)

// fakeStats is a stable, realistic snapshot (taken from this host, with the
// snaps preserved on purpose — they are what proves the filter).
func fakeStats() *system.Stats {
	return &system.Stats{
		Host: system.HostInfo{
			Hostname: "host01", Platform: "ubuntu", PlatformVer: "24.04",
			Kernel: "6.8.0-137-generic", Uptime: 1602225, Architecture: "x86_64",
		},
		CPU: system.CPUInfo{
			Model: "AMD EPYC 9355P 32-Core Processor", Cores: 8,
			Overall: 71.06872293177322, Steal: 0.5, Iowait: 1.25,
		},
		Memory: system.MemInfo{Total: 33653854208, Used: 20932243456, Free: 11828940800, UsedPercent: 62.19865138366264},
		Swap:   system.MemInfo{Total: 8589930496, Used: 8589320192, Free: 610304, UsedPercent: 99.9928951229549},
		Load:   system.LoadInfo{Load1: 4.52, Load5: 4.49, Load15: 4.99},
		Disks: []system.DiskInfo{
			{Mount: "/", FSType: "ext4", Total: 414921494528, Used: 303620763648, Free: 111283953664, UsedPercent: 73.1784313311828},
			{Mount: "/snap/chromium/3499", FSType: "squashfs", Total: 197394432, Used: 197394432, UsedPercent: 100},
			{Mount: "/snap/core22/2411", FSType: "squashfs", Total: 77594624, Used: 77594624, UsedPercent: 100},
			{Mount: "/boot", FSType: "ext4", Total: 923156480, Used: 300000000, Free: 623156480, UsedPercent: 32.5},
			{Mount: "/boot/efi", FSType: "vfat", Total: 109395456, Used: 6377472, Free: 103017984, UsedPercent: 5.83},
			{Mount: "/run/lock", FSType: "tmpfs", Total: 5242880, Used: 0, UsedPercent: 0},
		},
		Net: []system.NetInfo{
			{Name: "lo", BytesSent: 205677054749, BytesRecv: 205677054749},
			{Name: "eth0", BytesSent: 637851387030, BytesRecv: 640396606829},
			{Name: "tailscale0", BytesSent: 5546735287, BytesRecv: 39991862081},
			{Name: "docker0", BytesSent: 1319338185, BytesRecv: 5623249},
			{Name: "br-80eca7e55e24", BytesSent: 11953768883, BytesRecv: 37303140667},
			{Name: "vethc467fca", BytesSent: 53653603849, BytesRecv: 49432863189},
		},
	}
}

func statsDep(s *system.Stats) func(context.Context) (*system.Stats, error) {
	return func(context.Context) (*system.Stats, error) { return s, nil }
}

// getOpsStatusJSON exercises the real route (mux + admin gate) and returns the
// body already decoded as a generic map, so the assertions can speak about a
// field's PRESENCE/ABSENCE, not just about values after unmarshalling.
func getOpsStatusJSON(t *testing.T, deps Deps, user string) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux, deps)
	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/status", nil)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	return body
}

// TestOpsStatus_System_Shape is the complete contract of the `system` block as
// the app sees it: a single GET brings CPU, load, memory, swap, disk, uptime and
// the server clock — each number in a raw/formatted pair.
func TestOpsStatus_System_Shape(t *testing.T) {
	deps := Deps{
		Cfg:            adminCfg(),
		HealthDetailed: fakeHealthDetailed(true),
		SysStats:       statsDep(fakeStats()),
	}
	var body OpsStatus
	mux := http.NewServeMux()
	Mount(mux, deps)
	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/status", nil)
	req = req.WithContext(auth.WithUser(req.Context(), testPrimary))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	sys := body.System
	if sys == nil {
		t.Fatal("system missing with SysStats on")
	}
	if sys.Hostname != "host01" || sys.Platform != "ubuntu 24.04" {
		t.Errorf("host = %q / %q", sys.Hostname, sys.Platform)
	}
	if sys.UptimeSeconds != 1602225 || sys.UptimeText != "18d 13h 3m" {
		t.Errorf("uptime = %d / %q, want 1602225 / \"18d 13h 3m\"", sys.UptimeSeconds, sys.UptimeText)
	}
	if sys.ServerTimeEpoch == 0 || sys.ServerTime == "" {
		t.Errorf("server_time = %q / %d, want preenchidos", sys.ServerTime, sys.ServerTimeEpoch)
	}
	if _, err := time.Parse(time.RFC3339, sys.ServerTime); err != nil {
		t.Errorf("server_time %q is not RFC3339: %v", sys.ServerTime, err)
	}
	if sys.CPU.Cores != 8 || sys.CPU.UsedPercent < 71 || sys.CPU.UsedPercent > 72 {
		t.Errorf("cpu = %#v", sys.CPU)
	}
	if sys.CPU.Load1 != 4.52 || sys.CPU.Load5 != 4.49 || sys.CPU.Load15 != 4.99 {
		t.Errorf("load = %v/%v/%v, want 4.52/4.49/4.99", sys.CPU.Load1, sys.CPU.Load5, sys.CPU.Load15)
	}
	if sys.CPU.Steal != 0.5 || sys.CPU.Iowait != 1.25 {
		t.Errorf("steal/iowait = %v/%v", sys.CPU.Steal, sys.CPU.Iowait)
	}
	// Raw AND formatted: the client draws the bar from the raw and the label from the text.
	if sys.Memory.Total != 33653854208 || sys.Memory.TotalText != "31.3 GiB" {
		t.Errorf("memory total = %d / %q", sys.Memory.Total, sys.Memory.TotalText)
	}
	if sys.Memory.UsedText != "19.5 GiB" {
		t.Errorf("memory used_text = %q, want 19.5 GiB", sys.Memory.UsedText)
	}
	if sys.Swap.Total != 8589930496 || sys.Swap.UsedPercent < 99 {
		t.Errorf("swap = %#v", sys.Swap)
	}
}

// TestOpsStatus_System_DisksFiltradosEOrdenados: the 2 snaps (squashfs, 100% by
// construction) and the tmpfs drop out; "/" comes first and the rest run from
// fullest to emptiest.
func TestOpsStatus_System_DisksFiltradosEOrdenados(t *testing.T) {
	got := relevantDisks(fakeStats().Disks)
	var mounts []string
	for _, d := range got {
		mounts = append(mounts, d.Mount)
	}
	want := []string{"/", "/boot", "/boot/efi"}
	if len(mounts) != len(want) {
		t.Fatalf("mounts = %v, want %v", mounts, want)
	}
	for i := range want {
		if mounts[i] != want[i] {
			t.Fatalf("mounts = %v, want %v", mounts, want)
		}
	}
	if got[0].TotalText != "386.4 GiB" || got[0].UsedText != "282.8 GiB" {
		t.Errorf("/ textos = %q / %q", got[0].TotalText, got[0].UsedText)
	}
	if got[0].FSType != "ext4" || got[0].Free != 111283953664 {
		t.Errorf("/ = %#v", got[0])
	}
}

// TestRelevantDisks_MaisCheioPrimeiro proves the ordering is independent of the
// input order, with "/" not in first position at the source.
func TestRelevantDisks_MaisCheioPrimeiro(t *testing.T) {
	in := []system.DiskInfo{
		{Mount: "/var", FSType: "ext4", Total: 100, Used: 10, UsedPercent: 10},
		{Mount: "/data", FSType: "xfs", Total: 100, Used: 90, UsedPercent: 90},
		{Mount: "/", FSType: "ext4", Total: 100, Used: 50, UsedPercent: 50},
	}
	got := relevantDisks(in)
	want := []string{"/", "/data", "/var"}
	for i, w := range want {
		if got[i].Mount != w {
			t.Fatalf("order = %v (expected %v)", got, want)
		}
	}
}

// TestPickUplink_IgnoraVirtuais: lo/docker0/br-*/veth*/tailscale0 can never be
// the card's interface — counting them would double the same byte.
func TestPickUplink_IgnoraVirtuais(t *testing.T) {
	up, ok := pickUplink(fakeStats().Net)
	if !ok || up.Name != "eth0" {
		t.Fatalf("uplink = %q (ok=%v), want eth0", up.Name, ok)
	}
	if _, ok := pickUplink([]system.NetInfo{{Name: "lo"}, {Name: "docker0"}}); ok {
		t.Error("uplink found on a host with only virtual interfaces")
	}
}

// TestNetRateTracker_PrecisaDeDuasAmostras: the first sample publishes the
// cumulative counters but NO rate (absent ≠ 0 B/s); the second derives bytes/s
// from the delta.
func TestNetRateTracker_PrecisaDeDuasAmostras(t *testing.T) {
	tr := &netRateTracker{}
	t0 := time.Unix(1700000000, 0)
	ifaces := []system.NetInfo{{Name: "eth0", BytesSent: 1000, BytesRecv: 2000}}

	first := tr.sample(ifaces, t0)
	if first == nil || first.Interface != "eth0" {
		t.Fatalf("first sample = %#v", first)
	}
	if first.BytesSent != 1000 || first.BytesRecv != 2000 {
		t.Errorf("contadores acumulados = %d/%d", first.BytesSent, first.BytesRecv)
	}
	if first.SentRate != nil || first.RecvRate != nil || first.SentRateText != "" {
		t.Errorf("the first sample published a rate: %#v", first)
	}

	// +10s, +20480 sent (2 KiB/s), +102400 received (10 KiB/s).
	second := tr.sample([]system.NetInfo{{Name: "eth0", BytesSent: 1000 + 20480, BytesRecv: 2000 + 102400}}, t0.Add(10*time.Second))
	if second.SentRate == nil || *second.SentRate != 2048 {
		t.Fatalf("sent_rate = %v, want 2048", second.SentRate)
	}
	if second.RecvRate == nil || *second.RecvRate != 10240 {
		t.Fatalf("recv_rate = %v, want 10240", second.RecvRate)
	}
	if second.SentRateText != "2.0 KiB/s" || second.RecvRateText != "10.0 KiB/s" {
		t.Errorf("textos = %q / %q", second.SentRateText, second.RecvRateText)
	}
}

// TestNetRateTracker_MesmaColetaNaoDerrubaTaxa is the guard for the 3s cache:
// the endpoint may be called twice inside the same window and receive the SAME
// counters. Recomputing there would give a false 0 B/s, and moving the baseline
// would shrink the next real sample's dt (an inflated rate). Here the previous
// rate is repeated and the next computation stays correct.
func TestNetRateTracker_MesmaColetaNaoDerrubaTaxa(t *testing.T) {
	tr := &netRateTracker{}
	t0 := time.Unix(1700000000, 0)
	tr.sample([]system.NetInfo{{Name: "eth0", BytesSent: 0, BytesRecv: 0}}, t0)
	second := tr.sample([]system.NetInfo{{Name: "eth0", BytesSent: 10240, BytesRecv: 0}}, t0.Add(10*time.Second))
	if second.SentRate == nil || *second.SentRate != 1024 {
		t.Fatalf("sent_rate = %v, want 1024", second.SentRate)
	}

	// The same collection served from the cache 2s later: rate preserved, baseline still.
	cached := tr.sample([]system.NetInfo{{Name: "eth0", BytesSent: 10240, BytesRecv: 0}}, t0.Add(12*time.Second))
	if cached.SentRate == nil || *cached.SentRate != 1024 {
		t.Fatalf("taxa cacheada = %v, want 1024 preservado", cached.SentRate)
	}

	// A fresh sample at t0+20s: the delta has to be measured against t0+10s (the
	// last REAL collection), not against t0+12s. 10240 bytes / 10s = 1024 B/s.
	third := tr.sample([]system.NetInfo{{Name: "eth0", BytesSent: 20480, BytesRecv: 0}}, t0.Add(20*time.Second))
	if third.SentRate == nil || *third.SentRate != 1024 {
		t.Fatalf("sent_rate = %v, want 1024 (baseline must not have moved)", third.SentRate)
	}
}

// TestNetRateTracker_ContadorRegrediu: the NIC/host restarted. Rebaseline in
// silence, without publishing a negative or absurd rate.
func TestNetRateTracker_ContadorRegrediu(t *testing.T) {
	tr := &netRateTracker{}
	t0 := time.Unix(1700000000, 0)
	tr.sample([]system.NetInfo{{Name: "eth0", BytesSent: 10_000_000, BytesRecv: 10_000_000}}, t0)
	tr.sample([]system.NetInfo{{Name: "eth0", BytesSent: 10_100_000, BytesRecv: 10_100_000}}, t0.Add(10*time.Second))
	after := tr.sample([]system.NetInfo{{Name: "eth0", BytesSent: 500, BytesRecv: 500}}, t0.Add(20*time.Second))
	if after.SentRate != nil || after.RecvRate != nil {
		t.Fatalf("rate published after counter reset: %#v", after)
	}
}

// TestOpsStatus_System_AusenteSemDependencia is the absence contract: with no
// SysStats wired, the "system" key does not exist in the serialized bytes — the
// app tells "not available" apart from "idle server", which a block of zeros
// would make indistinguishable.
func TestOpsStatus_System_AusenteSemDependencia(t *testing.T) {
	body := getOpsStatusJSON(t, Deps{Cfg: adminCfg(), HealthDetailed: fakeHealthDetailed(true)}, testPrimary)
	if _, ok := body["system"]; ok {
		t.Fatalf("system key present without SysStats: %#v", body["system"])
	}
	// The rest of /ops/status stays intact — the extension must not have cost a
	// single field of the contract the app already consumes.
	if body["health_ok"] != true {
		t.Errorf("health_ok = %#v, want true", body["health_ok"])
	}
	if _, ok := body["alerts"]; !ok {
		t.Error("alerts disappeared from the contract")
	}
}

// TestOpsStatus_System_AusenteQuandoColetaFalha: a collection that errors must
// turn neither into zeros nor into a 500 — the rest of the status (health,
// queue, alerts) still lets the app paint the screen.
func TestOpsStatus_System_AusenteQuandoColetaFalha(t *testing.T) {
	deps := Deps{
		Cfg:            adminCfg(),
		HealthDetailed: fakeHealthDetailed(true),
		SysStats: func(context.Context) (*system.Stats, error) {
			return nil, errors.New("gopsutil indisponível")
		},
	}
	body := getOpsStatusJSON(t, deps, testPrimary)
	if _, ok := body["system"]; ok {
		t.Fatalf("system key present with collection failing: %#v", body["system"])
	}
	if body["health_ok"] != true {
		t.Errorf("health_ok = %#v, want true", body["health_ok"])
	}
}

// TestOpsStatus_System_NaoVazaParaNaoAdmin: the resources inherit EXACTLY
// /ops/status's gate (admin ⇒ 403 for everyone else), with no new policy. A
// non-admin does not get a trimmed `system`: they get no response at all.
func TestOpsStatus_System_NaoVazaParaNaoAdmin(t *testing.T) {
	deps := Deps{
		Cfg:            adminCfg(),
		HealthDetailed: fakeHealthDetailed(true),
		SysStats:       statsDep(fakeStats()),
	}
	mux := http.NewServeMux()
	Mount(mux, deps)
	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/ops/status", nil)
	req = req.WithContext(auth.WithUser(req.Context(), "someone-else"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "host01") || strings.Contains(body, "used_percent") {
		t.Fatalf("403 body leaked resources: %s", body)
	}
}

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0s"},
		{45, "45s"},
		{60, "1m"},
		{3600, "1h"},
		{3660, "1h 1m"},
		{86400, "1d"},
		{1602225, "18d 13h 3m"},
	}
	for _, c := range cases {
		if got := formatUptime(c.in); got != c.want {
			t.Errorf("formatUptime(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatBytes guarantees the SAME output as formatDockerBytes/
// formatSecurityBytes — the app must not see "1.0 KiB" on one screen and
// something else on another for the same number.
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{33653854208, "31.3 GiB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
