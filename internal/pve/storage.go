package pve

// storage.go — the node's two CAPACITY routes, and the privilege verdict that
// says whether they have anything to tell.
//
// ─────────────────────────────────────────────────────────────────────────────
// 🔴 WHY THESE TWO ROUTES ONLY SHOW UP NOW
//
// The first measurement, taken with the lab@pve!audit token and the ACL in
// force at the time:
//
//	GET /nodes/pve/storage    → 200, data: []      ← NOT an error
//	GET /nodes/pve/disks/zfs  → 403
//
// The first line is this whole file's problem in one: without privilege on
// /storage the hypervisor does not refuse, it AGREES and returns nothing. A
// screen fed only by that list would say "no storage" about a server with four
// — and it would say it in green, with no error, no log, and no 403 for anyone
// to investigate.
//
// After `pveum acl modify / --roles PVEAuditor --tokens lab@pve!audit
// --propagate 1` (applied by the operator), both return real data: 4 storages
// and 2 zpools. But the TRAP did not leave with the ACL — it comes back the day
// the privilege is withdrawn, and it comes back silent. That is why
// PodeAuditarDatastore is still here, and why it measures PRIVILEGE.
// ─────────────────────────────────────────────────────────────────────────────
//
// Scope: capacity and health, never content. Listing a datastore's volumes
// (/nodes/{n}/storage/{id}/content) and backup evidence belong to separate
// work, and both the cost and the shape of the response are different there.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Storage is one row of /nodes/{node}/storage.
//
// 🔴 Active, Enabled and Shared are int because the hypervisor sends 0|1, not a
// JSON boolean. Declaring `bool` would make the unmarshal of the WHOLE LIST
// fail — the same disaster Disk.Wearout avoids on the other side (node.go).
// Whoever wants a boolean uses Ativo()/Habilitado()/Compartilhado().
//
// UsedFraction arrives READY from the hypervisor (0.0686… = 6.9%). It is
// preferred over used/total because for `pbs` storage the two are not the same
// sum: PBS reports the remote datastore's space, and the fraction is what it
// computes itself. Recomputing it here would be disagreeing with the source.
type Storage struct {
	Storage      string  `json:"storage"` // "local-zfs"
	Type         string  `json:"type"`    // "zfspool" | "dir" | "pbs" | …
	Content      string  `json:"content"` // "images,rootdir" — a comma-separated list, not an array
	Total        int64   `json:"total"`
	Used         int64   `json:"used"`
	Avail        int64   `json:"avail"`
	UsedFraction float64 `json:"used_fraction"`
	Active       int     `json:"active"`
	Enabled      int     `json:"enabled"`
	Shared       int     `json:"shared"`
}

func (s Storage) Ativo() bool         { return s.Active == 1 }
func (s Storage) Habilitado() bool    { return s.Enabled == 1 }
func (s Storage) Compartilhado() bool { return s.Shared == 1 }

// Conteudos splits the `content` field into the list the screen consumes, in a
// stable order. Doing this here and not in the browser is the same rule as
// loadavg in inventory/hypervisor.go: the screen FORMATS, it does not interpret.
func (s Storage) Conteudos() []string {
	out := []string{}
	for _, c := range strings.Split(s.Content, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// StorageList returns the capacity of each of the node's storages. Measured: 168 ms.
//
// ⚠️ Reading a 200 with an empty list here does NOT mean "there is no storage".
// It means "there is no storage THIS TOKEN CAN SEE". PodeAuditarDatastore breaks the tie.
func (c *Client) StorageList(ctx context.Context, node string) ([]Storage, error) {
	if node == "" {
		return nil, fmt.Errorf("pve: empty node in StorageList")
	}
	var ss []Storage
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/storage"
	if err := c.do(ctx, http.MethodGet, p, &ss); err != nil {
		return nil, err
	}
	// The hypervisor promises no ordering. A document that changes order on every
	// tick becomes noise in the diff of the persisted inventory — the same reason as ordenaPorID().
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].Storage < ss[j].Storage })
	return ss, nil
}

// ZPool is one row of /nodes/{node}/disks/zfs.
//
// 🔴 Health is the reason this type exists. The whole lab lands on a
// SINGLE-DISK pool with no redundancy: the pool leaving ONLINE is the most
// expensive news in the laboratory, and it is exactly the news that today only
// reaches whoever opens the Proxmox UI — which the operator cannot get to.
//
// Frag is a whole PERCENTAGE (17 = 17%) and zero is a MEASUREMENT, not absence.
type ZPool struct {
	Name   string  `json:"name"`
	Health string  `json:"health"` // "ONLINE" | "DEGRADED" | "FAULTED" | …
	Size   int64   `json:"size"`
	Alloc  int64   `json:"alloc"`
	Free   int64   `json:"free"`
	Frag   int     `json:"frag"`
	Dedup  float64 `json:"dedup"`
}

// Saudavel is the only verdict this package emits about a pool: ONLINE, and
// nothing else. DEGRADED, FAULTED, SUSPENDED, UNAVAIL and REMOVED are all "no" —
// and none of them may be normalised to green anywhere along the path.
func (p ZPool) Saudavel() bool { return p.Health == "ONLINE" }

// ZFSList returns the node's ZFS pools. Measured: 96 ms — it fits inside the
// poller tick, unlike /disks/list (597 ms), which was left on demand.
func (c *Client) ZFSList(ctx context.Context, node string) ([]ZPool, error) {
	if node == "" {
		return nil, fmt.Errorf("pve: empty node in ZFSList")
	}
	var ps []ZPool
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/disks/zfs"
	if err := c.do(ctx, http.MethodGet, p, &ps); err != nil {
		return nil, err
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps, nil
}

// privsDeDatastore are the privileges that make /nodes/{n}/storage answer with
// content. Datastore.Audit is the read one; the allocation privileges imply it
// (whoever can write to the datastore can read it), and listing them keeps a
// token more powerful than audit from being read as blind.
var privsDeDatastore = []string{
	"Datastore.Audit",
	"Datastore.Allocate",
	"Datastore.AllocateSpace",
	"Datastore.AllocateTemplate",
}

// PodeAuditarDatastore says whether a map from /access/permissions authorises
// READING storage capacity. It is the ONLY source of that verdict in the panel
// — the /permissions handler and the poller call this function, never a copy of
// the rule. Two truths about the same question have already produced a measured
// defect in this codebase (see chaveDeCredencial in internal/api/handlers_nodes.go).
//
// 🔴 The verdict is by PRIVILEGE, not by the presence of a path. The first
// version answered `strings.HasPrefix(caminho, "/storage")`, and that only says
// the token has SOME privilege there: a token with PVEVMUser propagated from the
// root puts /storage in the map without Datastore.Audit, the list comes back 200
// with [], and the screen would announce "it can already see /storage" over an
// empty block. The false green is back, now stamped by the guard itself.
//
// The paths that count are the ones that COVER storage: the root (which
// propagates), /storage itself, and each named storage. `/storagefoo` is none of
// the three.
func PodeAuditarDatastore(perms map[string]map[string]int) bool {
	for caminho, privs := range perms {
		if !cobreStorage(caminho) {
			continue
		}
		for _, p := range privsDeDatastore {
			if privs[p] == 1 {
				return true
			}
		}
	}
	return false
}

// cobreStorage says whether an ACL path reaches the datastores. Cutting at
// "/storage/" (with the slash) is what stops a neighbouring path from getting in
// by prefix — the classic defect of whoever uses a raw HasPrefix.
func cobreStorage(caminho string) bool {
	return caminho == "/" || caminho == "/storage" || strings.HasPrefix(caminho, "/storage/")
}

// ─────────────────────────────────────────────────────────────────────────────
// Pool topology
//
// 🔴 THE MISSING LINK. `ZFSList` says there is an `rpool` with 70 GB allocated;
// `DiskList` says there is a 1 TB Lexar NVMe. Nothing said that one lived inside
// the other — and that is precisely the question that matters when a disk starts
// to fail: "what do I lose?".
//
// `/nodes/{n}/disks/zfs/{pool}` returns the vdev tree. THREE things come out of
// it that no other call gives:
//
//  1. the physical device holding the pool up (the by-id path, which carries the
//     serial — that is what allows pool ↔ disk to be matched);
//  2. the read/write/cksum error counters PER device, which are the signal of
//     silent rot in a pool with no mirror;
//  3. REDUNDANCY, derived from the topology instead of assumed. In this
//     laboratory it is zero, and the project constraints say so — but a screen
//     that asserts "no redundancy" by hardcode lies on the day somebody adds a
//     mirror.

// ZDispositivo is a leaf of the vdev tree: a real physical device.
type ZDispositivo struct {
	Caminho string `json:"caminho"` // /dev/disk/by-id/nvme-eui.…-part3
	Estado  string `json:"estado"`  // ONLINE | DEGRADED | FAULTED | UNAVAIL | REMOVED
	Read    int64  `json:"read"`
	Write   int64  `json:"write"`
	Cksum   int64  `json:"cksum"`
}

// ZVdev is a group of devices: `mirror-0`, `raidz1-0`, or the pool itself when
// the disks hang off the root with no group at all.
type ZVdev struct {
	Nome         string         `json:"nome"`
	Tipo         string         `json:"tipo"` // mirror | raidz1 | raidz2 | raidz3 | listra | especial
	Redundante   bool           `json:"redundante"`
	Dispositivos []ZDispositivo `json:"dispositivos"`
}

// ZPoolTopologia is the complete verdict about a pool.
type ZPoolTopologia struct {
	Nome       string  `json:"nome"`
	Estado     string  `json:"estado"`
	Erros      string  `json:"erros"` // "No known data errors" | a description
	Vdevs      []ZVdev `json:"vdevs"`
	Redundante bool    `json:"redundante"`
	NDisp      int     `json:"n_dispositivos"`
	// ErrosContados sums read+write+cksum across ALL devices. In a pool with no
	// mirror, any one of them different from zero is lost data — there is no second
	// copy to rebuild from.
	ErrosContados int64 `json:"erros_contados"`
}

// zfsNo is the raw shape of a tree node as the hypervisor returns it.
type zfsNo struct {
	Name     string  `json:"name"`
	State    string  `json:"state"`
	Leaf     int     `json:"leaf"`
	Read     int64   `json:"read"`
	Write    int64   `json:"write"`
	Cksum    int64   `json:"cksum"`
	Msg      string  `json:"msg"`
	Children []zfsNo `json:"children"`
}

type zfsDetalheCru struct {
	Name     string  `json:"name"`
	State    string  `json:"state"`
	Errors   string  `json:"errors"`
	Children []zfsNo `json:"children"`
}

// tipoDeVdev classifies a group by its name, the way `zpool status` writes it.
//
// Only mirror and raidz survive the loss of one device. `listra` (the pool
// hanging disks straight off the root) and anything unknown do NOT count as
// redundancy: when in doubt the verdict is "does not protect", because the error
// in the other direction makes the operator trust a mirror that does not exist.
func tipoDeVdev(nome string) (string, bool) {
	switch {
	case strings.HasPrefix(nome, "mirror"):
		return "mirror", true
	case strings.HasPrefix(nome, "raidz3"):
		return "raidz3", true
	case strings.HasPrefix(nome, "raidz2"):
		return "raidz2", true
	case strings.HasPrefix(nome, "raidz"):
		return "raidz1", true
	case strings.HasPrefix(nome, "log"), strings.HasPrefix(nome, "cache"),
		strings.HasPrefix(nome, "spare"), strings.HasPrefix(nome, "special"):
		return "especial", false
	default:
		return "listra", false
	}
}

// ZFSTopologia reads a pool's vdev tree and emits the verdict.
func (c *Client) ZFSTopologia(ctx context.Context, node, pool string) (ZPoolTopologia, error) {
	var out ZPoolTopologia
	if node == "" || pool == "" {
		return out, fmt.Errorf("pve: empty node or pool in ZFSTopologia")
	}
	var cru zfsDetalheCru
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/disks/zfs/" + url.PathEscape(pool)
	if err := c.do(ctx, http.MethodGet, p, &cru); err != nil {
		return out, err
	}
	out.Nome, out.Estado, out.Erros = cru.Name, cru.State, cru.Errors
	if out.Nome == "" {
		out.Nome = pool
	}

	// The root the hypervisor returns is a node named after the pool; the real
	// vdevs are its children. Going down one level is what separates "the pool"
	// from "the pool's disk groups".
	raiz := cru.Children
	if len(raiz) == 1 && raiz[0].Leaf == 0 && raiz[0].Name == out.Nome {
		raiz = raiz[0].Children
	}

	for _, n := range raiz {
		if n.Leaf == 1 {
			// A disk hanging straight off the root: it is a stripe, with no protection.
			out.Vdevs = append(out.Vdevs, ZVdev{
				Nome: n.Name, Tipo: "listra", Redundante: false,
				Dispositivos: []ZDispositivo{{Caminho: n.Name, Estado: n.State,
					Read: n.Read, Write: n.Write, Cksum: n.Cksum}},
			})
			continue
		}
		tipo, red := tipoDeVdev(n.Name)
		v := ZVdev{Nome: n.Name, Tipo: tipo, Redundante: red}
		for _, f := range n.Children {
			if f.Leaf != 1 {
				continue
			}
			v.Dispositivos = append(v.Dispositivos, ZDispositivo{
				Caminho: f.Name, Estado: f.State, Read: f.Read, Write: f.Write, Cksum: f.Cksum,
			})
		}
		out.Vdevs = append(out.Vdevs, v)
	}

	for _, v := range out.Vdevs {
		if v.Tipo == "especial" {
			continue // log/cache/spare do not hold the pool's data
		}
		if v.Redundante {
			out.Redundante = true
		}
		out.NDisp += len(v.Dispositivos)
		for _, d := range v.Dispositivos {
			out.ErrosContados += d.Read + d.Write + d.Cksum
		}
	}
	return out, nil
}

// SerialDoCaminho extracts the serial from a `/dev/disk/by-id/...` path, which
// is what allows a pool device to be matched to a `DiskList` row.
//
// The two formats measured in this laboratory:
//
//	/dev/disk/by-id/usb-Seagate_Expansion_NAA9N1KZ-0:0-part1  → NAA9N1KZ
//	/dev/disk/by-id/nvme-eui.0000000625124629caf25b035000017e-part3 → (none)
//
// The NVMe addressed by `eui.` carries no readable serial; returning empty is
// the right answer — matching by serial simply does not happen for it, and the
// screen falls back to matching by type. Inventing a serial would make the
// screen tie the pool to the WRONG disk, which is worse than not tying it.
func SerialDoCaminho(caminho string) string {
	base := caminho
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, "-0:0")
	for _, suf := range []string{"-part1", "-part2", "-part3", "-part4", "-part5", "-part6", "-part7", "-part8", "-part9"} {
		base = strings.TrimSuffix(base, suf)
	}
	base = strings.TrimSuffix(base, "-0:0")
	if strings.HasPrefix(base, "nvme-eui.") {
		return ""
	}
	if i := strings.LastIndex(base, "_"); i >= 0 {
		return base[i+1:]
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// Backup freshness
//
// 🔴 THIS CALL DID NOT EXIST UNTIL THE ACL WAS WIDENED. Until then the panel's
// token had `PVEAuditor` on `/` with propagate=1 — it passed the permission
// check with no error — and still received `{"data":[]}` about a datastore with
// 46.9 GB used. Only `root@pam` could see the content. Once the operator granted
// full access, the listing started answering: 65 backups, 9 guests.
//
// This matters because the off-site chain has already died for 14 days in
// silence, and the screen had no way to know. "Having a copy" and "being able to
// say when the last one was" are different things, and only the second becomes
// an alarm.

// BackupItem is one copy in the datastore.
type BackupItem struct {
	VolID  string `json:"volid"`
	VMID   int    `json:"vmid"`
	CTime  int64  `json:"ctime"` // unix epoch
	Size   int64  `json:"size"`
	Format string `json:"format"`
	Notes  string `json:"notes"`
}

// FrescorDeBackup is the per-datastore verdict.
type FrescorDeBackup struct {
	Storage string `json:"storage"`
	Total   int    `json:"total"`
	// UltimoCTime is 0 when THERE IS NO BACKUP AT ALL — and zero here is absence,
	// not "1970". Whoever consumes it has to tell the two apart: an empty datastore
	// and a datastore with an ancient backup call for opposite actions.
	UltimoCTime int64  `json:"ultimo_ctime"`
	Guests      []int  `json:"guests"`
	Erro        string `json:"erro,omitempty"`

	// 🔴 Agendado separates "deliberately disarmed" from "failed", and that
	// distinction is the difference between a panel you can trust and a permanent
	// red that trains you to ignore it. This lab's `backupusb` has had no new copy
	// for two weeks because the operator turned the schedule off in a deliberate,
	// dated decision — the layer was DISARMED, it did not fail.
	// 🔴 THREE STATES, NOT TWO — and the live proof caught this before the deploy.
	//
	//   "ativo"       the hypervisor job exists and is on
	//   "desarmado"   the hypervisor job exists and is OFF (somebody decided that)
	//   "fora-do-pve" there is NO hypervisor job for this datastore
	//
	// The third is the one a boolean would get wrong. In this laboratory `pbs`
	// receives a copy every day and has NO job in jobs.cfg: what feeds it is
	// `lab-offsite.sh` on a systemd timer on the host, invisible to
	// /cluster/backup. A boolean "scheduled" would paint a live layer as disarmed —
	// erring in the opposite direction from the one I was trying to fix.
	//
	// "No job in the hypervisor" means "this panel does not know who schedules it",
	// and saying that is more honest than asserting either of the other two.
	Agendamento string `json:"agendamento"`
	Schedule    string `json:"schedule,omitempty"`

	// MesmoDiscoQue names the other layers that share the physical disk. Two layers
	// on the same disk are ONE layer with two names: the disk failing takes both
	// together, and that is what the operator needs to see before trusting a count
	// of "two copies".
	MesmoDiscoQue []string `json:"mesmo_disco_que,omitempty"`
}

// BackupsDoDatastore lists a datastore's copies and summarises freshness.
func (c *Client) BackupsDoDatastore(ctx context.Context, node, storage string) (FrescorDeBackup, error) {
	out := FrescorDeBackup{Storage: storage}
	if node == "" || storage == "" {
		return out, fmt.Errorf("pve: empty node or storage in BackupsDoDatastore")
	}
	var itens []BackupItem
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) +
		"/content?content=backup"
	if err := c.do(ctx, http.MethodGet, p, &itens); err != nil {
		return out, err
	}
	out.Total = len(itens)
	vistos := map[int]bool{}
	for _, it := range itens {
		if it.CTime > out.UltimoCTime {
			out.UltimoCTime = it.CTime
		}
		if it.VMID > 0 && !vistos[it.VMID] {
			vistos[it.VMID] = true
			out.Guests = append(out.Guests, it.VMID)
		}
	}
	sort.Ints(out.Guests)
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Backup jobs
//
// 🔴 WITHOUT THIS, THE SCREEN CONFUSES "DISARMED" WITH "FAILED" — and permanent red
// trains people to ignore it, which is the disease that has already cost this lab's
// alarm channel its credibility.
//
// The `backupusb` datastore of this laboratory has had no fresh copy for two weeks,
// and that is NOT a failure: the schedule was turned off by a dated decision of the
// operator, after PBS was up, a full backup taken, verify by
// content and a restore rehearsal. The layer was DISARMED, not demolished — the
// job still exists with `enabled 0`, the storage is still declared and mounted
// and no dump was deleted, precisely to preserve the way back.
//
// A screen that paints this red is wrong about the fact, not about the colour.

// JobDeBackup is one entry of /cluster/backup.
type JobDeBackup struct {
	ID      string `json:"id"`
	Storage string `json:"storage"`
	// 🔴 A POINTER, not an int. `enabled` is OPTIONAL with `default => 1`: absent
	// means ON. An `int` would read absence as zero and the screen would call a job
	// that runs every day "disarmed" — a screen that says there is no backup when
	// there is is the most expensive lie it can tell.
	Enabled  *int   `json:"enabled"`
	Schedule string `json:"schedule"`
	Comment  string `json:"comment"`
	All      int    `json:"all"`
}

// Agendado says whether this job actually fires. An absent `enabled` in the
// hypervisor means ON — the field only shows up when somebody turns it off — so
// the read has to distinguish "explicit 0" from "field absent". Treating absence
// as off would make the screen call a layer that runs every day disarmed.
func (j JobDeBackup) Agendado() bool {
	if j.Schedule == "" {
		return false // with no schedule it does not fire, enabled or not
	}
	return j.Enabled == nil || *j.Enabled != 0
}

// JobsDeBackup lists the cluster's vzdump jobs.
func (c *Client) JobsDeBackup(ctx context.Context) ([]JobDeBackup, error) {
	var js []JobDeBackup
	if err := c.do(ctx, http.MethodGet, "/api2/json/cluster/backup", &js); err != nil {
		return nil, err
	}
	return js, nil
}
