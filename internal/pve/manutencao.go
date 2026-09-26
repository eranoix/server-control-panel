package pve

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// manutencao.go — reboot, clone, and order a backup copy.
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 THE THREE DO NOT USE THE SAME CREDENTIAL, AND THAT IS DESIGN, NOT NEGLECT.
//
// Rebooting is the node acting ON ITSELF: it requires VM.PowerMgmt on
// /vms/<id>, which the node's own token already has. It goes through the node
// token, like start/stop/shutdown.
//
// Cloning and ordering a backup copy are NOT the node acting on itself:
//   - cloning ALLOCATES A NEW VMID, and the hypervisor requires VM.Allocate on
//     /vms/<newid> — a path that does not exist yet and where the node token
//     has no ACL at all;
//   - a backup copy WRITES TO A STORAGE, and requires Datastore.AllocateSpace
//     on /storage/<name>, where the node token also has nothing.
// Measured at /access/permissions, not deduced. Those two go through the panel
// token, which has Administrator at the root.
// ────────────────────────────────────────────────────────────────────────────

// Reboot restarts the guest FROM THE INSIDE (the hypervisor asks init/ACPI),
// without cutting power. It is not stop followed by start: a guest that ignores
// the request stays powered on and the TASK fails — which is the right piece of
// information, because "I rebooted it" and "I asked it to reboot and it ignored
// me" call for different actions from the operator.
func (c *Client) Reboot(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return c.statusVerb(ctx, node, vmid, typ, "reboot")
}

// NextID returns the first free VMID according to the hypervisor ITSELF.
//
// 🔴 It exists so that nobody TYPES a number. Guessing a free id by reading the
// list on the screen is a race against anything else that allocates in the
// meantime, and the prize for getting it wrong is a 500 in the middle of a
// clone. The hypervisor holds the counter; it is the one that knows.
func (c *Client) NextID(ctx context.Context) (int, error) {
	// The route returns the number as a JSON STRING ("999"), not as a number.
	var bruto any
	if err := c.do(ctx, http.MethodGet, "/api2/json/cluster/nextid", &bruto); err != nil {
		return 0, err
	}
	switch v := bruto.(type) {
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("pve: /cluster/nextid returned %q, which is not a number", v)
		}
		return n, nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("pve: /cluster/nextid returned %T, unexpected", bruto)
	}
}

// NomeDeGuestValido refuses what the hypervisor would refuse — and refuses it
// BEFORE dialling out.
//
// The name goes into the query of a POST to the hypervisor. Validating here is
// not duplicating the hypervisor's validation: it is stopping a string with a
// slash or a space from becoming another route or another parameter before it
// leaves here.
func NomeDeGuestValido(nome string) error {
	if nome == "" || len(nome) > 63 {
		return fmt.Errorf("pve: invalid guest name (%q) — 1 to 63 characters", nome)
	}
	for i, r := range nome {
		ok := r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("pve: invalid guest name (%q) — only letters, digits and hyphen", nome)
		}
		// A hyphen at either end is not a valid hostname, and the hypervisor uses
		// this name as the hostname on LXC.
		if r == '-' && (i == 0 || i == len(nome)-1) {
			return fmt.Errorf("pve: invalid guest name (%q) — cannot start or end with a hyphen", nome)
		}
	}
	return nil
}

// NomeDeStorageValido refuses a storage name that is not an identifier. It goes
// into the query of a POST; a slash or a dot-dot there is a path to something
// else.
func NomeDeStorageValido(nome string) error {
	if nome == "" || len(nome) > 64 {
		return fmt.Errorf("pve: invalid storage (%q)", nome)
	}
	for _, r := range nome {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("pve: invalid storage (%q)", nome)
		}
	}
	if strings.Contains(nome, "..") {
		return fmt.Errorf("pve: invalid storage (%q)", nome)
	}
	return nil
}

// NomeDeNoValido refuses a node name that is not an identifier.
//
// `guestPath` interpolates the node into the path without validating — old debt
// I am not touching here, so as not to change the behaviour of routes that are
// already proven. But VZDump builds the path ON ITS OWN
// (`/nodes/<node>/vzdump`), so the validation has to exist on THIS side, or else
// a node with a slash in it would pick another route.
func NomeDeNoValido(node string) error {
	if node == "" || len(node) > 64 {
		return fmt.Errorf("pve: invalid node (%q)", node)
	}
	for _, r := range node {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("pve: invalid node (%q)", node)
		}
	}
	if strings.Contains(node, "..") {
		return fmt.Errorf("pve: invalid node (%q)", node)
	}
	return nil
}

// Clone copies the guest to a new VMID and returns the UPID.
//
// 🔴 ALWAYS A FULL COPY (`full=1`), and this is not an option narrowed out of
// laziness: the hypervisor only makes a LINKED clone from a TEMPLATE. For a
// normal guest the documentation is explicit — "This is always done when you
// clone a normal CT/VM". Offering the choice on the screen would be offering a
// button the hypervisor ignores.
//
// 🔴 THE NAME PARAMETER CHANGES WITH THE TYPE: LXC uses `hostname`, QEMU uses
// `name`. Sending the wrong one raises no error — the hypervisor simply IGNORES
// it, and the clone is born without a name. It is the worst class of defect:
// silent, and only visible days later.
// 🔴 `snapname` IS NOT OPTIONAL OUT OF WHIM — IT IS A MEASURED REQUIREMENT.
//
// The first version of this function did not have the parameter, and the live
// proof knocked it down flat: cloning a RUNNING CT returns
//
//	"Full clone of a running container is only possible from a snapshot"
//
// The rule does NOT appear in any grep of this machine's copy of the hypervisor
// source — it was the hypervisor actually refusing that revealed it. Reading
// code did not replace running it, and this line exists to remember that.
//
// For a VM (qemu) there is no equivalent restriction: the hypervisor clones a
// running VM using drive-mirror, and the only thing it refuses is copying TPM
// state.
func (c *Client) Clone(ctx context.Context, node string, vmid int, typ string, novoID int, nome, snapname string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	if novoID <= 0 {
		return "", fmt.Errorf("pve: invalid target vmid (%d)", novoID)
	}
	if novoID == vmid {
		return "", fmt.Errorf("pve: target %d is the source guest itself", novoID)
	}
	q := url.Values{
		"newid": {strconv.Itoa(novoID)},
		"full":  {"1"},
	}
	if snapname != "" {
		// The same snapshot-name validation used in create/delete/rollback: the
		// value comes from the screen and goes into the query of a POST to the
		// hypervisor.
		if err := NomeDeSnapshotValido(snapname); err != nil {
			return "", err
		}
		q.Set("snapname", snapname)
	}
	if nome != "" {
		if err := NomeDeGuestValido(nome); err != nil {
			return "", err
		}
		if typ == "lxc" {
			q.Set("hostname", nome)
		} else {
			q.Set("name", nome)
		}
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, base+"/clone?"+q.Encode(), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// ModosDeDump are the modes vzdump accepts, read from the hypervisor source.
//
//	snapshot — without stopping the guest (a disk-level consistent copy)
//	suspend  — freeze, copy, unfreeze
//	stop     — power off, copy, power back on (the most consistent and the most expensive)
var ModosDeDump = []string{"snapshot", "suspend", "stop"}

// CompressoesDeDump likewise. `zstd` is the default on PVE 9 and the one the
// backup job of this lab used.
var CompressoesDeDump = []string{"zstd", "lzo", "gzip", "0"}

func naLista(v string, lista []string) bool {
	for _, x := range lista {
		if v == x {
			return true
		}
	}
	return false
}

// VZDump orders the hypervisor to store a copy NOW and returns the UPID.
//
// 🔴 IT DOES NOT SEND `prune-backups`, AND THAT IS DELIBERATE. That parameter
// would make the manual backup PRUNE the old ones according to the storage
// policy — a command the operator presses expecting to GAIN a copy would end up
// DELETING others. It would also require Datastore.Allocate, a privilege nobody
// here needs to hold.
//
// 🔴 IT DOES NOT SEND `bwlimit`/`ionice`/`performance`: all three require
// Sys.Modify on '/', and none of them is worth widening a hypervisor privilege
// for.
func (c *Client) VZDump(ctx context.Context, node string, vmid int, storage, modo, compress string) (string, error) {
	if node == "" {
		return "", fmt.Errorf("pve: empty node")
	}
	if vmid <= 0 {
		return "", fmt.Errorf("pve: invalid vmid (%d)", vmid)
	}
	if err := NomeDeStorageValido(storage); err != nil {
		return "", err
	}
	if !naLista(modo, ModosDeDump) {
		return "", fmt.Errorf("pve: invalid dump mode (%q) — %s", modo, strings.Join(ModosDeDump, "|"))
	}
	if !naLista(compress, CompressoesDeDump) {
		return "", fmt.Errorf("pve: invalid compression (%q) — %s", compress, strings.Join(CompressoesDeDump, "|"))
	}
	if err := NomeDeNoValido(node); err != nil {
		return "", err
	}
	q := url.Values{
		"vmid":     {strconv.Itoa(vmid)},
		"storage":  {storage},
		"mode":     {modo},
		"compress": {compress},
		// `remove=0`: this dump deletes nothing. See the comment above.
		"remove": {"0"},
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, "/api2/json/nodes/"+node+"/vzdump?"+q.Encode(), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// ── the NOTE of the guest and of the node: what that box does, in plain words ─
//
// 🔴 THE SOURCE OF TRUTH ALREADY EXISTED, AND IT WAS NOT THE PANEL.
//
// Every guest in this lab — and the hypervisor itself — already has a
// description written in the hypervisor's `description` field (the "Notes" of
// the native screen), in Markdown, explaining what the box does, why it exists,
// what happens if it goes down and where the things that matter live. The panel
// simply did not show it.
//
// That decides the entire design: the panel READS that note, it does not invent
// one. A second description written here would become the second truth — and
// the two would diverge on the first day somebody edited the Proxmox one.

// Descricao returns the note of the guest (vmid > 0) or of the node itself
// (vmid <= 0).
//
// Absence is NOT an error: a guest with no note is a guest nobody has described
// yet, and the screen needs to say so instead of showing a failure.
func (c *Client) Descricao(ctx context.Context, node string, vmid int, typ string) (string, error) {
	var caminho string
	if vmid > 0 {
		base, err := guestPath(node, vmid, typ)
		if err != nil {
			return "", err
		}
		caminho = base + "/config"
	} else {
		if err := NomeDeNoValido(node); err != nil {
			return "", err
		}
		caminho = "/api2/json/nodes/" + node + "/config"
	}
	// The config carries dozens of fields; only the description matters, and
	// asking for the whole object into a one-field struct is what keeps the
	// parser immune to a new field from the hypervisor.
	var cfg struct {
		Description string `json:"description"`
	}
	if err := c.do(ctx, http.MethodGet, caminho, &cfg); err != nil {
		return "", err
	}
	return cfg.Description, nil
}

// TamanhoMaximoDaNota is the ceiling the panel accepts before dialling out.
//
// The hypervisor accepts large descriptions, but a note is there to be READ: a
// text that does not fit on one screen has stopped being a summary. The ceiling
// exists to refuse here, with a message in the panel's own language, instead of
// sending 200 KB to the hypervisor and getting an error back.
const TamanhoMaximoDaNota = 8192

// SetDescricao writes the note of the guest (vmid > 0) or of the node itself
// (vmid <= 0).
//
// 🔴 WRITING THE NOTE IS THE ONLY THING IN THIS BATCH THAT CHANGES
// CONFIGURATION, and the privilege was chosen as the NARROWEST one that serves:
// LXC/Config.pm:103 requires ONE of the five `VM.Config.*` (`any => 1`), and
// `description` is an OPTION. The other four — Disk, CPU, Memory, Network —
// remain denied, and that was MEASURED on the node token after the grant, not
// assumed.
func (c *Client) SetDescricao(ctx context.Context, node string, vmid int, typ, texto string) error {
	if len(texto) > TamanhoMaximoDaNota {
		return fmt.Errorf("pve: note with %d bytes — the cap is %d", len(texto), TamanhoMaximoDaNota)
	}
	var caminho string
	if vmid > 0 {
		base, err := guestPath(node, vmid, typ)
		if err != nil {
			return err
		}
		caminho = base + "/config"
	} else {
		if err := NomeDeNoValido(node); err != nil {
			return err
		}
		caminho = "/api2/json/nodes/" + node + "/config"
	}
	q := url.Values{"description": {texto}}
	// PUT, not POST: the guest config is a resource that EXISTS and is being
	// changed. The hypervisor refuses POST on that route, and sending the wrong
	// verb would give a 501 that the operator would read as "the panel broke".
	return c.do(ctx, http.MethodPut, caminho+"?"+q.Encode(), nil)
}
