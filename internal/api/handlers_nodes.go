package api

// handlers_nodes.go — the multi-node inventory API and per-node revocation.
//
// Two rules govern this file, and both exist because the alternative has
// already bitten someone in this repository:
//
//  1. AGE IS BORN HERE. `age_seconds` and `stale` are computed on the server,
//     at serialization time (inventory.View). The browser only FORMATS. This
//     project spans two machines and a tailnet; the clock on the operator's
//     machine is not a controlled variable, and a client running fast would
//     make everything look expired.
//
//  2. THREE VAULT STATES, NEVER TWO. `handlers_ai.go:186` returns `""` both
//     for "vault unreachable" and for "key missing" — that exact collapse is
//     what produced a logged defect: the dashboard said "no credential" when
//     the problem was the whole vault being down. Here: vault unavailable →
//     503 on mutations and an explicit state on reads; key missing →
//     credential "ausente"; key present → "ok".
//     The inventory stays READABLE in all three cases: a dead vault must not
//     wipe the node list off the screen.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
	"server-control-panel/internal/scope"
)

// Vault keys written by the credential provisioning tool (bin/pve-credencial).
// Each value is the WHOLE token in the form "lab@pve!<name>=<secret>", which is
// what the PVEAPIToken header requires — storing the bare secret was the defect
// that only a live call revealed.
const (
	pveSecretAdmin = "pve_token_admin"
	pveSecretAudit = "pve_token_audit"
	// 🔴 pve_token_painel is the FULL-ACCESS token, created by an explicit
	// decision of the operator. It REPLACES the audit token on hypervisor reads
	// whenever it exists, and the audit token stays the fallback when it does not.
	//
	// The fallback is not a courtesy: without it, a machine where this token was
	// never provisioned — a clone of the repo, a test environment, the VPS itself
	// before the migration — would lose the entire screen instead of losing the
	// two routes only that token reaches. And undoing the grant goes back to
	// deleting ONE key from the vault, without touching code.
	pveSecretPainel     = "pve_token_painel"
	pveSecretNodePrefix = "pve_token_node_"
	pveTokenUser        = "lab@pve"
	pveTokenNodePrefix  = "node-"
)

// Vault states. There are THREE because merging two of them is the defect this
// file exists in order not to repeat.
const (
	vaultOK           = "ok"
	vaultAusente      = "ausente"
	vaultInalcancavel = "inalcancavel"
)

// hypervisorOps is the slice of the PVE client these handlers use. A local
// interface so a test can inject a double without a mock framework — and so the
// type makes it explicit that the handler cannot do more than this.
type hypervisorOps interface {
	Start(ctx context.Context, node string, vmid int, typ string) (string, error)
	Stop(ctx context.Context, node string, vmid int, typ string) (string, error)
	Shutdown(ctx context.Context, node string, vmid int, typ string) (string, error)
	WaitTask(ctx context.Context, node, upid string) error
	DeleteToken(ctx context.Context, user, tokenID string) error
	ListTokens(ctx context.Context, user string) ([]pve.TokenInfo, error)
	ClusterResources(ctx context.Context) ([]pve.Resource, error)

	// The Proxmox tab. It lives in the SAME interface and the SAME dial(): two
	// interfaces would be two truths about what the dashboard can do on the
	// hypervisor, and the second would age in silence.
	NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error)
	TaskList(ctx context.Context, node string, opt pve.TaskListOptions) ([]pve.Task, error)
	TaskLog(ctx context.Context, node, upid string) ([]string, error)
	DisksList(ctx context.Context, node string) ([]pve.Disk, error)

	// Pool topology. Answers "what do I lose if this disk dies?" — the link
	// between `ZFSList` (the pool) and `DisksList` (the physical disk), which
	// neither of the two gave on its own.
	ZFSList(ctx context.Context, node string) ([]pve.ZPool, error)
	ZFSTopologia(ctx context.Context, node, pool string) (pve.ZPoolTopologia, error)

	// Backup freshness. It only started answering once full access was granted:
	// before that the token got an empty list about a full datastore, and the
	// screen had no way to know the chain had died.
	BackupsDoDatastore(ctx context.Context, node, storage string) (pve.FrescorDeBackup, error)
	// Without this the screen confuses "disarmed on purpose" with "failed", and
	// permanent red trains people to ignore it — the disease that has already
	// cost this lab the credibility of its alarm channel.
	JobsDeBackup(ctx context.Context) ([]pve.JobDeBackup, error)

	// Parity with the Proxmox screen. The last four only answer since full
	// access was granted.
	RRDNode(ctx context.Context, node string, j pve.JanelaRRD) ([]pve.PontoRRD, error)
	RRDGuest(ctx context.Context, node string, vmid int, typ string, j pve.JanelaRRD) ([]pve.PontoRRD, error)
	Network(ctx context.Context, node string) ([]pve.Interface, error)
	DNS(ctx context.Context, node string) (pve.DNSInfo, error)
	Time(ctx context.Context, node string) (pve.TimeInfo, error)
	Certificados(ctx context.Context, node string) ([]pve.Certificado, error)
	Pacotes(ctx context.Context, node string) ([]pve.Pacote, error)
	Syslog(ctx context.Context, node string, limite int) ([]pve.LinhaSyslog, error)
	Permissions(ctx context.Context) (map[string]map[string]int, error)
	SnapshotList(ctx context.Context, node string, vmid int, typ string) ([]pve.Snapshot, error)
	SnapshotCreate(ctx context.Context, node string, vmid int, typ, nome, descricao string) (string, error)
	SnapshotDelete(ctx context.Context, node string, vmid int, typ, nome string) (string, error)

	// Console and rollback. ConsoleAttach returns an ALREADY AUTHENTICATED
	// connection: neither the ticket nor the port crosses this boundary, and that
	// is why the signature does not mention them — what is not returned cannot be
	// handed to the browser by accident.
	ConsoleAttach(ctx context.Context, node string, vmid int, typ string) (pve.ConsoleConn, string, error)
	// Shell on the hypervisor ITSELF — the "Shell" button of the Proxmox screen.
	// Requires Sys.Console, which only exists since full access was granted.
	ConsoleAttachNode(ctx context.Context, node string) (pve.ConsoleConn, string, error)

	// Power on the hypervisor ITSELF. The one action in the dashboard whose
	// mistake has no remote undo: the machine has no IPMI, and the one routing
	// the admin network is the machine itself.
	NodePower(ctx context.Context, node string, cmd pve.ComandoDeEnergia) (string, error)
	SnapshotRollback(ctx context.Context, node string, vmid int, typ, nome string) (string, error)

	// Maintenance. The three were born together but do NOT use the same
	// credential: Reboot goes through the node's token (VM.PowerMgmt, which it
	// already had), Clone and VZDump go through the dashboard's token, because
	// they require VM.Allocate on /vms/<newid> and Datastore.AllocateSpace on
	// /storage/<name> — paths where the node's token has no ACL at all.
	Reboot(ctx context.Context, node string, vmid int, typ string) (string, error)
	NextID(ctx context.Context) (int, error)
	Clone(ctx context.Context, node string, vmid int, typ string, novoID int, nome, snapname string) (string, error)
	VZDump(ctx context.Context, node string, vmid int, storage, modo, compress string) (string, error)

	// The note that EXPLAINS what the box does. It comes from PVE's `description`
	// field (the "Notes" of the native screen), which was already filled in on
	// every guest and on the hypervisor itself. The dashboard READS it; it does
	// not invent a second description that would diverge from the first the next
	// day.
	Descricao(ctx context.Context, node string, vmid int, typ string) (string, error)

	// Writing the note is the ONLY operation in this batch that changes
	// CONFIGURATION. It goes through the dashboard's token, like clone and
	// backup: the same route serves guest and hypervisor, and the hypervisor case
	// is NODE-scoped, not guest-scoped.
	SetDescricao(ctx context.Context, node string, vmid int, typ, texto string) error
}

// nodeVault is the slice of the vault we need. Get returns (value, exists) —
// and it is `exists` that separates "missing" from "unreachable", because
// whoever cannot reach the vault never gets as far as calling Get.
type nodeVault interface {
	Get(key string) (string, bool)
	Delete(key string) error
}

// inventoryStoreOrNil follows the deployStoreOrNil shape: a missing subsystem
// answers 503, never a panic and never a lying empty list.
func (r *Router) inventoryStoreOrNil(w http.ResponseWriter) *inventory.Store {
	if r.inventoryStore == nil {
		writeErr(w, 503, "inventory unavailable")
		return nil
	}
	return r.inventoryStore
}

// nodeVaultOrErr resolves the operator's vault. An error here means
// "unreachable" — that is a state, not an emptiness.
func (r *Router) nodeVaultOrErr() (nodeVault, error) {
	if r.nodeVaultFn != nil {
		return r.nodeVaultFn()
	}
	if r.secrets == nil {
		return nil, errors.New("vault unavailable")
	}
	return scope.NewUserVault(r.secrets, scope.User(r.cfg.Primary)), nil
}

// tokenDoCofre returns the token value and the STATE of the vault. The three
// possible returns are disjoint and the caller picks the HTTP status from them.
func (r *Router) tokenDoCofre(chave string) (valor, estado string) {
	v, err := r.nodeVaultOrErr()
	if err != nil {
		return "", vaultInalcancavel
	}
	s, ok := v.Get(chave)
	if !ok || strings.TrimSpace(s) == "" {
		return "", vaultAusente
	}
	return s, vaultOK
}

// dial builds a PVE client from a vault token. The descriptor (base_url, pinned
// CA, ServerName, address to dial) comes from data/pve/pve.json — never from
// argv and never from a plaintext env var.
func (r *Router) dial(tokenValor string) (hypervisorOps, error) {
	if r.pveDial != nil {
		return r.pveDial(tokenValor)
	}
	if r.pveConfig == nil {
		return nil, errors.New("data/pve/pve.json missing — hypervisor not configured")
	}
	cfg := *r.pveConfig
	cfg.TokenID = tokenValor
	cfg.Secret = ""
	return pve.New(cfg)
}

// slugDoNo turns an inventory ID into the trailing part of the token name,
// which is how the provisioning tool named them: lxc/207 "apps" → node-apps.
// The slug comes from the node's NAME, not from the VMID: the token was created
// by name.
func slugDoNo(n inventory.Node) string {
	s := strings.ToLower(strings.TrimSpace(n.Name))
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, s)
	return strings.Trim(s, "-")
}

func chaveDoNo(n inventory.Node) string {
	return pveSecretNodePrefix + strings.ReplaceAll(slugDoNo(n), "-", "_")
}
func tokenIDDoNo(n inventory.Node) string { return pveTokenNodePrefix + slugDoNo(n) }

// segredoDeLeituraDoHipervisor picks the token the dashboard READS the
// hypervisor with: the full-access one when it exists in the vault, the audit
// one when it does not.
//
// It is ONE function, and every read path goes through it. Two copies of this
// choice — one in the poller and one in the handler — would diverge the day
// someone touched one of them, and the symptom would be a screen showing fresh
// data on half the panels and 403 on the other half.
func (r *Router) segredoDeLeituraDoHipervisor() string {
	if _, estado := r.tokenDoCofre(pveSecretPainel); estado == vaultOK {
		return pveSecretPainel
	}
	return pveSecretAudit
}

// chaveDeCredencial says WHICH vault key answers for a node. It is ONE function
// because having two — one in the poller's source and one in the handler's read
// — has already produced a measured defect: the host held `lab@pve!audit` in
// the inventory and the screen said "no credential (ausente)", because the
// handler was looking for `pve_token_node_pve`, which does not exist. A screen
// that shows an expiry date and "no credential" at the same time is a screen
// that contradicts itself.
//
// The hypervisor has no "per-node" token: what observes it is the audit one.
func chaveDeCredencial(n inventory.Node) string {
	if n.Kind == inventory.NodeKindHost {
		return pveSecretAudit
	}
	return chaveDoNo(n)
}

// handleNodes routes /api/nodes and its subroutes.
//
// A PVE guest ID contains a slash ("lxc/207"), so the path cannot be sliced
// naively: the verb is the LAST segment when that segment is a known one, and
// everything before it is the ID.
func (r *Router) handleNodes(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	st := r.inventoryStoreOrNil(w)
	if st == nil {
		return
	}

	resto := strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/nodes"), "/")
	if resto == "" {
		if req.Method != http.MethodGet {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.listaNos(w, st)
		return
	}

	partes := strings.Split(resto, "/")
	verbo := ""
	if n := len(partes); n > 1 {
		switch partes[n-1] {
		case "power", "credential", "clone", "backup", "nota":
			verbo = partes[n-1]
			partes = partes[:n-1]
		}
	}
	id := strings.Join(partes, "/")

	switch verbo {
	case "":
		if req.Method != http.MethodGet {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.detalheDoNo(w, st, id)
	case "power":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.powerDoNo(w, req, st, id)
	case "nota":
		// GET reads, PUT writes. PUT and not POST because the note is a field of
		// a resource that already exists — and it is the verb PVE itself requires
		// further down.
		if req.Method != http.MethodGet && req.Method != http.MethodPut {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.notaDoNo(w, req, st, id)
	case "clone":
		// GET prepares (next free id + suggested name), POST executes. Keeping
		// both on the same verb keeps preparation and execution next to each
		// other: splitting them would invite preparing in one place and executing
		// in another, with the id coming from anywhere at all.
		if req.Method != http.MethodGet && req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.cloneDoNo(w, req, st, id)
	case "backup":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.backupDoNo(w, req, st, id)
	case "credential":
		if req.Method != http.MethodDelete {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.revogaCredencial(w, req, st, id)
	}
}

// agora is the handler's clock. Injectable so a test can prove expiry without
// waiting — the same reason as in freshness.go and in the poller.
func (r *Router) agora() time.Time {
	if r.inventoryNow != nil {
		return r.inventoryNow()
	}
	return time.Now()
}

func (r *Router) ttl() time.Duration {
	if r.inventoryPoller != nil {
		// The SAME TTL as the loop: two different values would make the screen
		// disagree with the poller about what has expired.
		return r.inventoryPoller.TTL()
	}
	return 90 * time.Second
}

// listaNos delivers the views with the age ALREADY resolved.
func (r *Router) listaNos(w http.ResponseWriter, st *inventory.Store) {
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	vistas := inventory.View(inv, r.ttl(), r.agora())
	// An unavailable vault does NOT wipe the list: it becomes a state stamped on
	// every node that speaks the PVE API. A readable inventory is the floor.
	estadoCofre := r.enriqueceCofre(vistas)
	writeJSON(w, map[string]any{
		"nodes":       naoNil(vistas),
		"ttl_seconds": int64(r.ttl().Seconds()),
		"vault":       estadoCofre,
		"observed_at": r.agora().Unix(),
		// 🔴 The SECOND clock (pollclock.go). Each node's `age_seconds` says how
		// long the dashboard has KNOWN that; `poll` says how long ago it ASKED.
		// With only one of them, a mute node and a dead poller look like the same
		// screen — and the second hypothesis accuses every node at once, all of
		// them innocent.
		"poll": inventory.ViewPoll(inv, r.ttl(), r.agora()),
	})
}

// enriqueceCofre reconciles what the model holds against what the vault HAS
// right now, and returns the vault's global state. The three cases stay apart:
//
//	vault down  → state "inalcancavel"; credentials stay as the model left
//	              them (we do not invent "ausente" for failing to look)
//	key gone    → credential "ausente" on that node
//	key present → keep what the model says (ok/revogada/expirada)
func (r *Router) enriqueceCofre(vistas []inventory.NodeView) string {
	v, err := r.nodeVaultOrErr()
	if err != nil {
		return vaultInalcancavel
	}
	for i := range vistas {
		if vistas[i].Transport != inventory.TransportPVEAPI {
			continue
		}
		if vistas[i].Credential.State == inventory.CredRevogada {
			// 🔴 REVOKED BEATS MISSING. After a successful revocation the key is
			// GONE from the vault — that is step 3 of the revocation procedure.
			// Without this guard, the revocation itself would erase its own record
			// and the screen would say "ausente" (= never had a credential) for a
			// token the operator has just revoked. Found by TestRevokeIsolation.
			continue
		}
		valor, ok := v.Get(chaveDeCredencial(vistas[i].Node))
		if !ok || strings.TrimSpace(valor) == "" {
			// A key that is not in the vault is a MISSING credential — and that is
			// different from revoked (which is a recorded action) and from expired
			// (which is the calendar).
			vistas[i].Credential.State = inventory.CredAusente
			vistas[i].Credential.TokenID = ""
		}
	}
	return vaultOK
}

func (r *Router) detalheDoNo(w http.ResponseWriter, st *inventory.Store, id string) {
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	vistas := inventory.View(inv, r.ttl(), r.agora())
	estadoCofre := r.enriqueceCofre(vistas)
	idx := -1
	for i := range vistas {
		if vistas[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	writeJSON(w, map[string]any{
		"node":        vistas[idx],
		"services":    naoNilS(filtraServices(inv.Services, id)),
		"deployments": naoNilD(filtraDeployments(inv.Deployments, id)),
		"jobs":        naoNilJ(filtraJobs(inv.Jobs, id)),
		"vault":       estadoCofre,
		"ttl_seconds": int64(r.ttl().Seconds()),
	})
}

func filtraServices(in []inventory.Service, nodeID string) []inventory.Service {
	var out []inventory.Service
	for _, s := range in {
		if s.NodeID == nodeID {
			out = append(out, s)
		}
	}
	return out
}

func filtraDeployments(in []inventory.Deployment, nodeID string) []inventory.Deployment {
	var out []inventory.Deployment
	for _, d := range in {
		if d.NodeID == nodeID {
			out = append(out, d)
		}
	}
	return out
}

func filtraJobs(in []inventory.JobRef, nodeID string) []inventory.JobRef {
	var out []inventory.JobRef
	for _, j := range in {
		if j.NodeID == nodeID {
			out = append(out, j)
		}
	}
	return out
}

func naoNil(v []inventory.NodeView) []inventory.NodeView {
	if v == nil {
		return []inventory.NodeView{}
	}
	return v
}
func naoNilS(v []inventory.Service) []inventory.Service {
	if v == nil {
		return []inventory.Service{}
	}
	return v
}
func naoNilD(v []inventory.Deployment) []inventory.Deployment {
	if v == nil {
		return []inventory.Deployment{}
	}
	return v
}
func naoNilJ(v []inventory.JobRef) []inventory.JobRef {
	if v == nil {
		return []inventory.JobRef{}
	}
	return v
}

func achaNo(inv inventory.Inventory, id string) (inventory.Node, bool) {
	for _, n := range inv.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return inventory.Node{}, false
}

// powerDoNo runs start/stop/shutdown and only answers success after the
// hypervisor's task has finished well.
//
// 🔴 PVE's POST returns 200 with a UPID as soon as the TASK IS CREATED — the VM
// can still fail to come up right afterwards. Passing that 200 along would be
// the screen saying "powered on" for a VM that did not power on.
func (r *Router) powerDoNo(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	acao := strings.ToLower(strings.TrimSpace(body.Action))
	switch acao {
	case "start", "stop", "shutdown", "reboot":
	default:
		writeErr(w, 400, "invalid action: "+acao+" (start|stop|shutdown|reboot)")
		return
	}

	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	no, ok := achaNo(inv, id)
	if !ok {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	if no.Kind != inventory.NodeKindGuest || no.VMID <= 0 {
		writeErr(w, 400, "node "+id+" is not a guest of the hypervisor")
		return
	}

	valor, estado := r.tokenDoCofre(chaveDoNo(no))
	switch estado {
	case vaultInalcancavel:
		// Distinct from "ausente" ON PURPOSE: one is the vault being down, the
		// other is a credential that does not exist. They call for opposite
		// actions from the operator.
		writeErr(w, 503, "vault unreachable — the node credential could not be read")
		return
	case vaultAusente:
		writeErr(w, 409, "missing credential for node "+id)
		return
	}

	cli, err := r.dial(valor)
	if err != nil {
		writeErr(w, 503, "hypervisor not configured: "+err.Error())
		return
	}

	tipo, node := tipoEHost(no)
	ctx := req.Context()
	var upid string
	switch acao {
	case "start":
		upid, err = cli.Start(ctx, node, no.VMID, tipo)
	case "stop":
		upid, err = cli.Stop(ctx, node, no.VMID, tipo)
	case "shutdown":
		upid, err = cli.Shutdown(ctx, node, no.VMID, tipo)
	case "reboot":
		// Reboot WAITS for the task, unlike clone and backup. The reason is the
		// same one that justifies WaitTask on shutdown: a guest that ignores the
		// request from the inside stays powered on, and the task is the only place
		// where that shows up. A "rebooted" for something that did not reboot is
		// worse than no error at all.
		upid, err = cli.Reboot(ctx, node, no.VMID, tipo)
	}
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused "+acao+": "+err.Error())
		return
	}

	// The proof that it happened: the task finished, and it finished doing what
	// was asked. `WARNINGS: n` counts as done — see esperaTarefa.
	avisos, err := esperaTarefa(ctx, cli, node, upid)
	if err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.power", fmt.Sprintf("node=%s action=%s upid=%s status=falhou", id, acao, upid))
		// The exitstatus is attached EXPLICITLY, without depending on how
		// pve.Error.Error() formats it: when Status is 0 (which is the case for a
		// task that ended badly), that formatter takes the Err branch and the
		// Body — which carries the exitstatus — never shows up. The real reason
		// for the failure is the only clue the operator has.
		writeErr(w, 502, "task "+upid+" did not finish cleanly: "+detalheDoErroPVE(err))
		return
	}

	r.auditEvent(req, auth.UserFrom(req), "pve.power",
		fmt.Sprintf("node=%s action=%s upid=%s status=ok avisos=%s", id, acao, upid, avisos))
	// The warning does NOT disappear: it travels together with the success,
	// because whoever does not see it here will not see it anywhere.
	writeJSON(w, map[string]any{"node": id, "action": acao, "upid": upid, "status": "ok", "avisos": avisos})
}

// tipoEHost returns the guest's type ("lxc"|"qemu") and the hypervisor node,
// both derived from the ID that discovery recorded ("lxc/207").
func tipoEHost(n inventory.Node) (tipo, host string) {
	tipo = "lxc"
	if i := strings.Index(n.ID, "/"); i > 0 {
		tipo = n.ID[:i]
	}
	// The host is the hypervisor's name; discovery records it as the node
	// "node/<name>". PVE guests only have one hypervisor in this topology.
	return tipo, "pve"
}

// detalheDoErroPVE returns the error message plus the body the hypervisor sent,
// without duplicating it when it is already there.
func detalheDoErroPVE(err error) string {
	msg := err.Error()
	var pe *pve.Error
	if errors.As(err, &pe) {
		if corpo := strings.TrimSpace(pe.Body); corpo != "" && !strings.Contains(msg, corpo) {
			msg += " [" + corpo + "]"
		}
	}
	return msg
}

func codigoDoErroPVE(err error) int {
	var pe *pve.Error
	if !errors.As(err, &pe) {
		return 502
	}
	switch pe.Kind {
	case pve.KindNoCredential:
		return 401
	case pve.KindForbidden:
		return 403
	case pve.KindUnreachable:
		return 504
	default:
		return 502
	}
}

// 🔴 revogaCredencial is the whole revocation procedure, and the ORDER is the
// point.
//
//  1. DELETE the token on PVE, with the ADMIN token (a disjoint role)
//  2. CONFIRMATION: a real call with the just-revoked operational token has to
//     return 401. PVE re-reads user.cfg on every request, with no TTL, so the
//     "under a minute" requirement comes out BY CONSTRUCTION — and any cache
//     in the dashboard would only make it worse. That is why there is no cache
//     of credential state here.
//  3. ONLY THEN delete it from the vault. Last, because internal/secrets does
//     a read-modify-write of the whole map WITHOUT flock: a concurrent write
//     can RESURRECT the key.
//  4. RE-CHECK that the key is gone — that is the detection of the
//     resurrection above.
//
// Inverted, the order produces the worst possible state: a clean vault with the
// token ALIVE on the hypervisor. An orphan credential nobody can revoke any
// more, because nobody knows any longer that it exists.
func (r *Router) revogaCredencial(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	no, ok := achaNo(inv, id)
	if !ok {
		writeErr(w, 404, "node not found: "+id)
		return
	}

	cofre, err := r.nodeVaultOrErr()
	if err != nil {
		writeErr(w, 503, "vault unreachable — revocation aborted before touching the hypervisor")
		return
	}
	valorAdmin, estadoAdmin := r.tokenDoCofre(pveSecretAdmin)
	if estadoAdmin != vaultOK {
		writeErr(w, 409, "admin token ("+pveSecretAdmin+") "+estadoAdmin+" — without it there is no way to revoke")
		return
	}
	chaveNo := chaveDoNo(no)
	valorNo, estadoNo := r.tokenDoCofre(chaveNo)

	admin, err := r.dial(valorAdmin)
	if err != nil {
		writeErr(w, 503, "hypervisor not configured: "+err.Error())
		return
	}
	ctx := req.Context()

	// Step 1 — the hypervisor first.
	if err := admin.DeleteToken(ctx, pveTokenUser, tokenIDDoNo(no)); err != nil {
		writeErr(w, codigoDoErroPVE(err), "step=pve.delete failed: "+err.Error()+" (the vault was NOT touched)")
		return
	}

	// Step 2 — confirm with the revoked token itself. Without this proof, the
	// dashboard would be saying "revoked" on the strength of its own optimism.
	if estadoNo == vaultOK {
		operacional, err := r.dial(valorNo)
		if err == nil {
			_, errProva := operacional.ClusterResources(ctx)
			var pe *pve.Error
			if !errors.As(errProva, &pe) || pe.Kind != pve.KindNoCredential {
				writeErr(w, 502, "step=pve.confirm401 failed: the revoked token still answers ("+fmt.Sprint(errProva)+") — the vault was NOT touched")
				return
			}
		}
	}

	// Step 3 — the vault last.
	if err := cofre.Delete(chaveNo); err != nil {
		writeErr(w, 500, "step=vault.delete failed: "+err.Error()+" (token ALREADY revoked on the hypervisor)")
		return
	}

	// Step 4 — did the key really disappear? (resurrection by race)
	if _, ainda := cofre.Get(chaveNo); ainda {
		writeErr(w, 500, "step=vault.recheck failed: the key "+chaveNo+" reappeared in the vault (concurrent write)")
		return
	}

	// The screen shows the REAL state, immediately — never a cache.
	if err := st.Replace(func(iv *inventory.Inventory) {
		for i := range iv.Nodes {
			if iv.Nodes[i].ID == id {
				iv.Nodes[i].Credential.State = inventory.CredRevogada
				iv.Nodes[i].Credential.TokenID = ""
			}
		}
	}); err != nil {
		writeErr(w, 500, "revoked, but the inventory could not be updated: "+err.Error())
		return
	}

	r.auditEvent(req, auth.UserFrom(req), "pve.token.revoked", fmt.Sprintf("node=%s token=%s", id, tokenIDDoNo(no)))
	writeJSON(w, map[string]any{
		"node":   id,
		"token":  tokenIDDoNo(no),
		"state":  inventory.CredRevogada,
		"passos": []string{"pve.delete", "pve.confirm401", "vault.delete", "vault.recheck"},
	})
}

// loadPVEDescriptor reads data/pve/pve.json — the PUBLIC descriptor of the
// hypervisor (base_url, address to dial, ServerName and the path to the pinned
// CA). No secret lives in it: the token always comes from the vault.
//
// Absence is a legitimate state and NOT a fatal error: with no descriptor the
// inventory carries on with the nodes from seeds.json alone. A dashboard that
// refuses to start because the hypervisor is not configured is the opposite of
// what this work promises — the dashboard is precisely where you go to look
// when something has gone down.
func loadPVEDescriptor(dataDir string) (*pve.Config, error) {
	path := filepath.Join(dataDir, "pve", "pve.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hypervisor descriptor missing (%s)", path)
	}
	var d struct {
		BaseURL    string `json:"base_url"`
		Resolve    string `json:"resolve"`
		ServerName string `json:"server_name"`
		CAFile     string `json:"ca_file"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("hypervisor descriptor %s malformed: %w", path, err)
	}
	ca := d.CAFile
	if ca != "" && !filepath.IsAbs(ca) {
		// The descriptor stores the path relative to the dashboard's root;
		// resolving it here avoids depending on the process's working directory.
		ca = filepath.Join(filepath.Dir(filepath.Dir(dataDir)), ca)
		if _, err := os.Stat(ca); err != nil {
			ca = filepath.Join(dataDir, "pve", filepath.Base(d.CAFile))
		}
	}
	return &pve.Config{
		BaseURL: d.BaseURL, Resolve: d.Resolve, ServerName: d.ServerName, CAFile: ca,
	}, nil
}

// startInventoryPoller starts the discovery loop, in the same block as the
// other collectors. With no store or no descriptor it simply does not start —
// and the route keeps serving whatever is on disk, with the age growing, which
// is exactly what the operator is meant to see.
func (r *Router) startInventoryPoller(ctx context.Context) {
	if r.inventoryStore == nil {
		return
	}
	valor, estado := r.tokenDoCofre(r.segredoDeLeituraDoHipervisor())
	if estado != vaultOK || r.pveConfig == nil {
		log.Printf("inventory: poller not started (vault=%s, descriptor=%v) — the screen will show the age growing",
			estado, r.pveConfig != nil)
		return
	}
	cfg := *r.pveConfig
	cfg.TokenID = valor
	cli, err := pve.New(cfg)
	if err != nil {
		log.Printf("inventory: invalid PVE client (%v) — poller not started", err)
		return
	}
	p := inventory.NewPoller(r.inventoryStore, cli, inventory.Sources{
		Seeds:       inventory.SeedsSource(r.cfg.DataDir),
		Credentials: r.credentialSource(),
		// The other sources join in when there is something to aggregate; nil is
		// safe.
	}, inventory.PollerConfig{})
	r.inventoryPoller = p
	go p.Run(ctx)
}

// ── the poller's credential source ───────────────────────────────────────────
//
// 🔴 This is the fix for the defect that only the LIVE call revealed: the
// poller never filled Node.Credential, so credentialState() returned "ausente"
// for EVERY node — with a vault full of valid tokens — and Expire stayed 0,
// which made the "expires in N days" warning IMPOSSIBLE to fire. The unit tests
// did not catch it because the fixtures already came with Credential filled in
// by hand.
//
// The assembly respects the layers: internal/inventory cannot reach the vault
// and internal/pve cannot reach the vault; what joins the two halves is this
// handler, the only place that knows both.
//
// # Why the expiry is cached and the token id is not
//
// The token id comes from the VAULT — a local, cheap read, done on every tick
// so that a revocation shows up on screen on the next one. The expiry comes
// from the HYPERVISOR and only the ADMIN token can read it (the audit one gets
// 403 — that is the role disjunction working, measured). Using the revocation
// credential every 30 s to read a date that only changes when someone recreates
// a token would widen the exposure of the most powerful token in the lab for no
// gain at all. Hence the one-hour cache.
const expiresCacheTTL = time.Hour

func (r *Router) credentialSource() func([]inventory.Node) (map[string]inventory.Credential, error) {
	var mu sync.Mutex
	var expires map[string]int64
	var lidoEm time.Time

	return func(nodes []inventory.Node) (map[string]inventory.Credential, error) {
		// 🔴 The vault is an IN-MEMORY map loaded at boot. A secret written by
		// ANOTHER process (`vpsmctl secrets set`, `bin/pve-credencial --apply`)
		// was invisible until the next restart — measured: the revocation drill
		// recreated the node's token and the dashboard went on saying "revogada"
		// with the key already back in the vault AND on the hypervisor. In the
		// common case this is one os.Stat.
		if r.secrets != nil {
			if _, err := r.secrets.ReloadIfChanged(); err != nil {
				log.Printf("inventory: the vault could not be re-read (%v) — carrying on with the in-memory copy", err)
			}
		}
		v, err := r.nodeVaultOrErr()
		if err != nil {
			// An unreachable vault is NOT a missing credential (the collapse in
			// handlers_ai.go:186). The poller keeps the last known state.
			return nil, err
		}

		mu.Lock()
		if expires == nil || time.Since(lidoEm) > expiresCacheTTL {
			if m, err := r.tokenExpires(); err != nil {
				log.Printf("inventory: token expiry unavailable (%v) — the screen will show the state without a deadline", err)
			} else {
				expires, lidoEm = m, time.Now()
			}
		}
		exp := expires
		mu.Unlock()

		out := make(map[string]inventory.Credential, len(nodes))
		for _, n := range nodes {
			if n.Transport != inventory.TransportPVEAPI {
				continue
			}
			valor, ok := v.Get(chaveDeCredencial(n))
			if !ok || strings.TrimSpace(valor) == "" {
				continue
			}
			tokenID, _, err := pve.SplitTokenValue(valor)
			if err != nil {
				// A value outside the "<tokenid>=<secret>" form is the old
				// provisioning defect. Better to assert nothing than to assert
				// something wrong.
				continue
			}
			out[n.ID] = inventory.Credential{TokenID: tokenID, Expire: exp[nomeDoToken(tokenID)]}
		}
		return out, nil
	}
}

// nomeDoToken extracts the token name from the full id: "lab@pve!node-lab" →
// "node-lab", which is how PVE returns it in /access/users/{u}/token.
func nomeDoToken(tokenID string) string {
	if i := strings.Index(tokenID, "!"); i >= 0 {
		return tokenID[i+1:]
	}
	return tokenID
}

// tokenExpires reads the expiry dates on the hypervisor. Requires the ADMIN
// token: the audit one gets 403 on this route, and that refusal is the role
// disjunction doing its job.
func (r *Router) tokenExpires() (map[string]int64, error) {
	valor, estado := r.tokenDoCofre(pveSecretAdmin)
	if estado != vaultOK {
		return nil, fmt.Errorf("token admin %s: %s", pveSecretAdmin, estado)
	}
	cli, err := r.dial(valor)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	toks, err := cli.ListTokens(ctx, pveTokenUser)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(toks))
	for _, t := range toks {
		out[t.TokenID] = t.Expire
	}
	return out, nil
}
