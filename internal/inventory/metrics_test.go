package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"server-control-panel/internal/pve"
)

func resourcesWithCounters() []pve.Resource {
	return []pve.Resource{
		{
			ID: "lxc/207", VMID: 207, Name: "apps", Node: "pve", Type: "lxc",
			Status: "running", Uptime: 169846,
			CPU: 0.00973562209063064, MaxCPU: 4,
			Mem: 4284424192, MaxMem: 10737418240, MemHost: 0,
			Disk: 12362973184, MaxDisk: 51539607552,
			NetIn: 8612076650, NetOut: 618642490,
			DiskRead: 233443328, DiskWrite: 0,
		},
		{
			ID: "qemu/208", VMID: 208, Name: "dev", Node: "pve", Type: "qemu",
			Status: "running", Uptime: 769848,
			CPU: 0.00458085203094519, MaxCPU: 4,
			Mem: 7185268736, MaxMem: 8589934592, MemHost: 7820808192,
			Disk: 0, MaxDisk: 34359738368,
			NetIn: 3214744546, NetOut: 1925087968,
			DiskRead: 465998946, DiskWrite: 12526250496,
		},
	}
}

func TestGuestMetricsAreStamped(t *testing.T) {
	f := &fakePVE{resources: resourcesWithCounters()}
	p, st, _ := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	nodes := nodesByID(t, st)
	n, ok := nodes["lxc/207"]
	if !ok {
		t.Fatal("lxc/207 was not discovered")
	}
	now := int64(1800000000)

	if n.MemUsed.Value != 4284424192 || n.MemTotal.Value != 10737418240 {
		t.Errorf("memory = %d/%d, want 4284424192/10737418240", n.MemUsed.Value, n.MemTotal.Value)
	}
	if n.CPUFrac.Value < 0.0097 || n.CPUFrac.Value > 0.0098 {
		t.Errorf("cpu = %v, want ~0.00974 (a fraction, not a percentage)", n.CPUFrac.Value)
	}
	if n.CPUCores.Value != 4 {
		t.Errorf("cores = %d, want 4", n.CPUCores.Value)
	}
	if n.DiskUsed.Value != 12362973184 || n.DiskTotal.Value != 51539607552 {
		t.Errorf("disk = %d/%d", n.DiskUsed.Value, n.DiskTotal.Value)
	}
	if n.NetIn.Value != 8612076650 || n.NetOut.Value != 618642490 {
		t.Errorf("network = %d/%d", n.NetIn.Value, n.NetOut.Value)
	}

	for _, c := range []struct {
		name string
		at   int64
	}{
		{"cpu", n.CPUFrac.ObservedAt}, {"cpu_cores", n.CPUCores.ObservedAt},
		{"mem_used", n.MemUsed.ObservedAt}, {"mem_total", n.MemTotal.ObservedAt},
		{"disk_used", n.DiskUsed.ObservedAt}, {"disk_total", n.DiskTotal.ObservedAt},
		{"net_in", n.NetIn.ObservedAt}, {"net_out", n.NetOut.ObservedAt},
		{"disk_read", n.DiskRead.ObservedAt}, {"disk_write", n.DiskWrite.ObservedAt},
		{"mem_host", n.MemHost.ObservedAt},
	} {
		if c.at != now {
			t.Errorf("%s.observed_at = %d, want %d (the same as status = %d)", c.name, c.at, now, n.Status.ObservedAt)
		}
	}
}

func TestUnreportedDiskNeverBecomesZero(t *testing.T) {
	f := &fakePVE{resources: resourcesWithCounters()}
	p, st, _ := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	nodes := nodesByID(t, st)

	qemu := nodes["qemu/208"]
	if qemu.DiskUsed.Value != NotReported {
		t.Fatalf("qemu/208.disk_used = %d, want %d (NotReported) — 0 reads as 'empty disk'",
			qemu.DiskUsed.Value, NotReported)
	}
	if qemu.DiskTotal.Value != 34359738368 {
		t.Errorf("qemu/208.disk_total = %d — the capacity is known even with no agent", qemu.DiskTotal.Value)
	}
	if qemu.DiskUsed.ObservedAt == 0 {
		t.Error("qemu/208.disk_used has no timestamp — 'not reported' is an observation, not the absence of one")
	}

	lxc := nodes["lxc/207"]
	if lxc.DiskUsed.Value != 12362973184 {
		t.Errorf("lxc/207.disk_used = %d — the QEMU rule cannot contaminate LXC", lxc.DiskUsed.Value)
	}

	if lxc.MemHost.Value != NotReported {
		t.Errorf("lxc/207.mem_host = %d, want %d — LXC does not report host RAM", lxc.MemHost.Value, NotReported)
	}
	if qemu.MemHost.Value != 7820808192 {
		t.Errorf("qemu/208.mem_host = %d, want 7820808192", qemu.MemHost.Value)
	}
}

func TestNetworkRateDoesNotSpanGap(t *testing.T) {
	base := resourcesWithCounters()
	netIn0 := base[0].NetIn
	f := &fakePVE{resources: base}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{Interval: 30 * time.Second})

	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nodesByID(t, st)["lxc/207"].NetInRate.Value; got != NotReported {
		t.Fatalf("the first observation produced rate %d — there is nothing to derive it from", got)
	}

	rel.advance(30 * time.Second)
	f.mu.Lock()
	f.resources[0].NetIn = netIn0 + 3000000
	f.mu.Unlock()
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nodesByID(t, st)["lxc/207"].NetInRate.Value; got != 100000 {
		t.Fatalf("rate = %d B/s, want 100000", got)
	}

	rel.advance(10 * time.Minute)
	f.mu.Lock()
	f.resources[0].NetIn = netIn0 + 9000000
	f.mu.Unlock()
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nodesByID(t, st)["lxc/207"].NetInRate.Value; got != NotReported {
		t.Fatalf("rate = %d after a 10-minute hole — want %d: an average over blind minutes is an invented rate",
			got, NotReported)
	}

	rel.advance(30 * time.Second)
	f.mu.Lock()
	f.resources[0].NetIn = 1000
	f.mu.Unlock()
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := nodesByID(t, st)["lxc/207"].NetInRate.Value; got != NotReported {
		t.Fatalf("a counter that reset to zero produced rate %d — a guest restart is not negative traffic", got)
	}
}

func TestDiscoveryFailureKeepsCounters(t *testing.T) {
	f := &fakePVE{resources: resourcesWithCounters()}
	p, st, rel := newTestPoller(t, f, Sources{}, PollerConfig{})
	if err := p.tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := nodesByID(t, st)["lxc/207"]

	rel.advance(5 * time.Minute)
	f.mu.Lock()
	f.failure = errors.New("hypervisor silent")
	f.mu.Unlock()
	if err := p.tick(context.Background()); err == nil {
		t.Fatal("the tick should have failed")
	}

	after := nodesByID(t, st)["lxc/207"]
	if after.MemUsed.Value != before.MemUsed.Value || after.MemUsed.ObservedAt != before.MemUsed.ObservedAt {
		t.Fatalf("mem_used changed with the hypervisor mute: before=%+v after=%+v", before.MemUsed, after.MemUsed)
	}
	if after.DiskUsed.Value != before.DiskUsed.Value {
		t.Fatalf("disk_used changed with the hypervisor mute")
	}
}
