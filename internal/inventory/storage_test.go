package inventory

import (
	"reflect"
	"testing"
	"time"

	"server-control-panel/internal/pve"
)

// storage_test.go — the pins for capacity and zpool in the inventory.
//
// Two defects are defended here, and both have already bitten this repo:
//
//  1. 🔴 AGE HITCHING A RIDE. The hypervisor was split off from the Node
//     because observedAtDoNo fuses together the timestamps of things that are
//     not observed together. Storage and zpool come out of TWO OTHER calls —
//     if their timestamp went into observedAtDoHipervisor, a /status that
//     still answers would keep the card green while capacity ages in silence.
//     Same defect, one layer up.
//
//  2. 🔴 AN EMPTY LIST LYING. /nodes/{n}/storage returns 200 with [] when the
//     token has no privilege. "No storage" and "no permission to see storage"
//     are the same answer on the wire and opposite readings on the screen,
//     and what tells them apart is the privilege verdict. It travels in the
//     SAME document, with a timestamp of its own, so that no screen is ever
//     built out of half a truth.

func poolsDeTeste() []pve.Storage {
	return []pve.Storage{
		{Storage: "local-zfs", Type: "zfspool", Content: "images,rootdir",
			Total: 978416107520, Used: 67198091264, Avail: 911218016256,
			UsedFraction: 0.068680483433912, Active: 1, Enabled: 1, Shared: 0},
		{Storage: "pbs", Type: "pbs", Content: "backup",
			Total: 916405092352, Used: 46299873280, Avail: 870105219072,
			UsedFraction: 0.0505233697045147, Active: 1, Enabled: 1, Shared: 1},
	}
}

func zpoolsDeTeste() []pve.ZPool {
	return []pve.ZPool{
		{Name: "backup", Health: "ONLINE", Size: 996432412672, Alloc: 95457288192, Free: 900975124480, Frag: 0},
		{Name: "rpool", Health: "ONLINE", Size: 1013612281856, Alloc: 70999646208, Free: 942612635648, Frag: 17},
	}
}

// -------------------------------------------------- timestamp of its OWN ----

// 🔴 TestIdadeDoStorageNaoPegaCaronaNaSaude is the central test of this file.
//
// Real scenario: the node's /status keeps answering (the poller stamps it on
// every tick), but /nodes/pve/storage has started failing. If the age of the
// capacity block comes out of the hypervisor's timestamp, the screen shows
// 6.9% used with a "live" badge over a number from half an hour ago.
func TestIdadeDoStorageNaoPegaCaronaNaSaude(t *testing.T) {
	var inv Inventory
	// t=1,800,000,000: capacity is observed.
	aplicaStorage(&inv, poolsDeTeste(), 1800000000)
	aplicaZPools(&inv, zpoolsDeTeste(), 1800000000)
	// t=1,800,000,300 (5 min later): ONLY health answers.
	aplicaHipervisor(&inv, "pve", statusDeTeste(), 1800000300)

	agora := time.Unix(1800000300, 0)
	if v := ViewHypervisor(inv.Hypervisor, 90*time.Second, agora); v.AgeSeconds != 0 {
		t.Errorf("health age = %d, want 0 — it was JUST observed", v.AgeSeconds)
	}
	vs := ViewStorage(inv.Hypervisor, 90*time.Second, agora)
	if vs.AgeSeconds != 300 {
		t.Errorf("storage age = %d, want 300 — it is riding on the health timestamp (A-5)", vs.AgeSeconds)
	}
	if !vs.Stale {
		t.Error("a 5-minute-old storage with a 90 s TTL has to count as expired")
	}
	vz := ViewZPools(inv.Hypervisor, 90*time.Second, agora)
	if vz.AgeSeconds != 300 {
		t.Errorf("zpools age = %d, want 300 (its own timestamp)", vz.AgeSeconds)
	}
}

// 🔴 TestCarimboDaSaudeClassificaTodoCampo is the STRUCTURAL version of the
// test above. It discovers, field by field through reflection, which
// measurements feed observedAtDoHipervisor — and demands that the set be
// EXACTLY the declared one.
//
// Without it, the next field added to the document lands in one of the two
// lists by accident and nobody finds out: if it joins health without
// deserving to, it rejuvenates the card; if it stays out without deserving
// to, its age never counts. The classification becomes a mandatory DECISION
// rather than an oversight.
func TestCarimboDaSaudeClassificaTodoCampo(t *testing.T) {
	// Outside health on purpose: each one comes from its OWN call to the
	// hypervisor and has its own view (ViewStorage/ViewZPools).
	foraDaSaude := map[string]bool{
		"Storage":        true,
		"ZPools":         true,
		"DatastoreAudit": true,
	}

	tp := reflect.TypeOf(Hypervisor{})
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.Name == "Node" {
			continue // identity, not a measurement
		}
		// Builds a Hypervisor with ONLY this field stamped and asks whether the
		// health function sees it.
		h := reflect.New(tp).Elem()
		h.Field(i).FieldByName("ObservedAt").SetInt(1800000000)
		visto := observedAtDoHipervisor(h.Interface().(Hypervisor)) == 1800000000

		if foraDaSaude[f.Name] && visto {
			t.Errorf("%s entra no carimbo da SAÚDE — ele rejuvenesceria o card do hipervisor "+
				"com uma observação que não é dele (A-5)", f.Name)
		}
		if !foraDaSaude[f.Name] && !visto {
			t.Errorf("%s NÃO entra no carimbo da saúde — a idade dele nunca contaria "+
				"(ou ele é campo novo que ninguém classificou)", f.Name)
		}
	}
}

// TestNuncaObservadoDizIssoNosBlocosNovos: -1 is the marker, and never 0.
func TestNuncaObservadoDizIssoNosBlocosNovos(t *testing.T) {
	agora := time.Unix(1800000000, 0)
	vs := ViewStorage(Hypervisor{}, 90*time.Second, agora)
	if vs.AgeSeconds != -1 || !vs.Stale {
		t.Errorf("never-observed storage = age %d stale %v, want -1/true", vs.AgeSeconds, vs.Stale)
	}
	if vs.Pools == nil {
		t.Error("Pools nil becomes `null` in the JSON; the screen needs [] to say 'nothing here'")
	}
	vz := ViewZPools(Hypervisor{}, 90*time.Second, agora)
	if vz.AgeSeconds != -1 || !vz.Stale {
		t.Errorf("never-observed zpools = age %d stale %v, want -1/true", vz.AgeSeconds, vz.Stale)
	}
	if vz.Pools == nil {
		t.Error("Pools nil becomes `null` in the JSON")
	}
}

// ------------------------------------------------- empty ≠ no permission ----

// 🔴 TestVazioComPrivilegioNaoEIgualAVazioSemPrivilegio: BOTH lists are empty
// and the two screens have to be different. It is the whole trap in a single
// test.
func TestVazioComPrivilegioNaoEIgualAVazioSemPrivilegio(t *testing.T) {
	agora := time.Unix(1800000000, 0)

	var comPriv Inventory
	aplicaStorage(&comPriv, nil, 1800000000)
	aplicaDatastoreAudit(&comPriv, true, 1800000000)

	var semPriv Inventory
	aplicaStorage(&semPriv, nil, 1800000000)
	aplicaDatastoreAudit(&semPriv, false, 1800000000)

	a := ViewStorage(comPriv.Hypervisor, 90*time.Second, agora)
	b := ViewStorage(semPriv.Hypervisor, 90*time.Second, agora)
	if len(a.Pools) != 0 || len(b.Pools) != 0 {
		t.Fatal("both lists have to be empty — that is the premise")
	}
	if a.DatastoreAudit.Value == b.DatastoreAudit.Value {
		t.Fatal("the privilege verdict does not tell the two kinds of empty apart — the screen has no way to")
	}
	if !a.DatastoreAudit.Value {
		t.Error("with privilege, datastore_audit has to be true (empty = there really is no storage)")
	}
	if b.DatastoreAudit.Value {
		t.Error("without privilege, datastore_audit has to be false (empty = the list was filtered by the ACL)")
	}
	if a.DatastoreAudit.ObservedAt != 1800000000 {
		t.Error("the verdict travels WITHOUT a timestamp — an old verdict presented as live")
	}
}

// TestVereditoNuncaObservadoNaoMenteDeVerde: with no observation at all, the
// timestamp is 0. Whoever reads it has to be able to say "I do not know yet"
// instead of "not allowed".
func TestVereditoNuncaObservadoNaoMenteDeVerde(t *testing.T) {
	v := ViewStorage(Hypervisor{}, 90*time.Second, time.Unix(1800000000, 0))
	if v.DatastoreAudit.ObservedAt != 0 {
		t.Errorf("ObservedAt = %d, want 0 (never observed)", v.DatastoreAudit.ObservedAt)
	}
	if v.DatastoreAudit.Value {
		t.Error("a never-observed verdict cannot be born true")
	}
}

// -------------------------------------------------------- data conversion ---

// TestPoolsChegamNormalizados: `content` becomes a list, 0|1 becomes a boolean
// and the fraction becomes a percentage. The screen FORMATS; it does not
// interpret.
func TestPoolsChegamNormalizados(t *testing.T) {
	var inv Inventory
	aplicaStorage(&inv, poolsDeTeste(), 1800000000)
	ps := inv.Hypervisor.Storage.Value
	if len(ps) != 2 {
		t.Fatalf("len = %d", len(ps))
	}
	lz := ps[0]
	if lz.ID != "local-zfs" {
		t.Fatalf("order/ID = %q, want local-zfs first", lz.ID)
	}
	if len(lz.Content) != 2 || lz.Content[0] != "images" || lz.Content[1] != "rootdir" {
		t.Errorf("Content = %v, want [images rootdir]", lz.Content)
	}
	if !lz.Ativo || !lz.Habilitado || lz.Compartilhado {
		t.Errorf("flags = active %v enabled %v shared %v", lz.Ativo, lz.Habilitado, lz.Compartilhado)
	}
	if lz.UsedPct < 6.8 || lz.UsedPct > 7.0 {
		t.Errorf("UsedPct = %v, want ~6.87 (from the PVE's used_fraction)", lz.UsedPct)
	}
	if ps[1].ID != "pbs" || !ps[1].Compartilhado {
		t.Errorf("pbs = %+v, want shared", ps[1])
	}
}

// 🔴 TestUsedPctDegradaParaAContaQuandoOPVENaoManda: the day a hypervisor
// upgrade stops sending `used_fraction`, a FULL disk would show up at 0% — an
// empty, green bar. The fallback is the arithmetic, never zero.
func TestUsedPctDegradaParaAContaQuandoOPVENaoManda(t *testing.T) {
	var inv Inventory
	aplicaStorage(&inv, []pve.Storage{{
		Storage: "quase-cheio", Type: "dir",
		Total: 1000, Used: 950, Avail: 50, UsedFraction: 0, Active: 1, Enabled: 1,
	}}, 1800000000)
	p := inv.Hypervisor.Storage.Value[0]
	if p.UsedPct < 94.9 || p.UsedPct > 95.1 {
		t.Errorf("UsedPct = %v, want 95 — with no used_fraction, used/total is the way out, not 0", p.UsedPct)
	}
	// A total of zero must not become a division by zero, nor 100%.
	var inv2 Inventory
	aplicaStorage(&inv2, []pve.Storage{{Storage: "vazio", Total: 0, Used: 0}}, 1800000000)
	if got := inv2.Hypervisor.Storage.Value[0].UsedPct; got != 0 {
		t.Errorf("UsedPct of a storage with no total = %v, want 0", got)
	}
}

// TestZPoolsChegamComSaudeLiteral: DEGRADED becomes nothing but DEGRADED.
func TestZPoolsChegamComSaudeLiteral(t *testing.T) {
	var inv Inventory
	aplicaZPools(&inv, []pve.ZPool{
		{Name: "rpool", Health: "DEGRADED", Size: 100, Alloc: 40, Free: 60, Frag: 3},
	}, 1800000000)
	p := inv.Hypervisor.ZPools.Value[0]
	if p.Health != "DEGRADED" {
		t.Errorf("Health = %q", p.Health)
	}
	if p.Saudavel {
		t.Error("Saudavel = true for DEGRADED — on a single-disk pool that is the most expensive news in the lab")
	}
	if p.FragPct != 3 {
		t.Errorf("FragPct = %d, want 3", p.FragPct)
	}
}
