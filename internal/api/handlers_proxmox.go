package api

// handlers_proxmox.go — the native "Proxmox" tab.
//
// The operator works fully remotely and has no way to reach the Proxmox UI from home.
// Until now the dashboard knew nothing about the hypervisor: no RAM, no load,
// no tasks, no disks. The cost of that was measured, not assumed —
// GET /nodes/pve/tasks?errors=1&limit=10 returned TEN real failures nobody had
// seen, among them `push_file 207 failed to open …/provision.sh` and
// `vzsnapshot 201 snapshot feature is not available`.
//
// ─────────────────────────────────────────────────────────────────────────────
// 🔴 THE RULE THAT GOVERNS THIS FILE: each route has ONE token, and the choice
// lives in a single function (chaveParaOperacao).
//
// Measured against the home hypervisor:
//
//	GET /nodes/pve/tasks?limit=50  with lab@pve!audit      → 200, 10 tasks
//	GET /nodes/pve/tasks?limit=50  with lab@pve!node-apps  → 200, len=0
//
// The second case is NOT an error. Tasks.pm:40-45 requires Sys.Audit on /nodes,
// which the LabOperador role does not have, and PVE answers 200 with an empty
// list. Whoever reuses the node's token "because it owns the guest" gets no
// 403, no 401 and no log: they get an empty screen that lies — the perfect
// false green.
//
// Snapshot is the INVERSE: what acts on a guest is the NODE's credential, and a
// live run proved it, with the UPID carrying `lab@pve!node-lab`.
//
// Two divergent sources for the same question have already produced a measured
// defect in this codebase (see chaveDeCredencial in handlers_nodes.go). That is
// why the choice is ONE function, and why TestTarefasUsamOTokenAudit pins it by
// the KEY THAT WAS READ.
// ─────────────────────────────────────────────────────────────────────────────
//
// Scope of the first pass: everything that did not require a new ACL on the
// hypervisor.
//
// 🔴 AND THE TEXT THAT USED TO STAND HERE WAS FALSE. It said storage capacity,
// backup evidence and zpool state "require `pveum acl modify /storage`". They
// do not. Read in this hypervisor's own Perl source: `/nodes/{n}/disks/zfs`
// wants **Sys.Audit on `/`** (Disks/ZFS.pm:62-64), and `/nodes/{n}/storage`
// requires nothing at all to answer — it FILTERS the list storage by storage
// against `Datastore.Audit` (Storage/Status.pm:72-76). An ACL on `/storage`
// would have unlocked half of it and left the other half silent; that is why
// the ACL that worked was PVEAuditor on `/` with --propagate 1. An absence
// declared with the wrong reason teaches the wrong thing to the next session —
// and the next session acts.
//
// ─────────────────────────────────────────────────────────────────────────────
// A LATER PASS added storage capacity and zpool state.
//
// The operator granted the ACL:
//
//	pveum acl modify / --roles PVEAuditor --tokens lab@pve!audit --propagate 1
//
// MEASURED effect with lab@pve!audit, the same day:
//
//	GET /nodes/pve/storage    200 with []  →  200 with 4 items
//	GET /nodes/pve/disks/zfs  403          →  200 with 2 items
//	GET /nodes/pve/apt/update 403          →  403 (needs Sys.Modify — still out)
//
// 🔴 THE PERMISSION GUARD WAS NOT DELETED — IT STARTED PASSING.
//
// /nodes/{n}/storage answers 200 with an EMPTY list when the privilege is
// missing; it does not answer 403. "No storage" and "not allowed to see
// storage" are the same answer on the wire, and the second one is the one that
// requires the operator to act. The tie-breaker is the verdict from
// /access/permissions, and it now travels INSIDE the capacity response
// (`datastore_audit`), with a timestamp. Deleting the check because it passes
// today would mean pulling out the detector on the day the alarm stopped
// ringing: the privilege can be revoked, and the regression would come back
// silent.
//
// The verdict has ONE source — pve.PodeAuditarDatastore — used both by the
// poller and by the /permissions route. And it measures PRIVILEGE, not the
// presence of the "/storage" path: presence only says the token holds SOME
// privilege there, and a token with /storage in its map but without
// Datastore.Audit would return an empty list while the screen announced that it
// "can already see /storage".
//
// Deliberately left out of that pass: datastore contents and backup evidence
// (/cluster/backup has answered 200 ever since the ACL, but it is another
// screen and another cost) and apt/packages, which still require Sys.Modify.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
)

// operacaoPVX says what is about to be done on the hypervisor. There are two
// cases because there are two disjoint roles, not because there are two routes.
type operacaoPVX int

const (
	// opLeituraHipervisor: tasks, task log, disks and permissions. All of them
	// require Sys.Audit on /nodes — only the AUDIT token has it.
	opLeituraHipervisor operacaoPVX = iota
	// opGuest: snapshots (list, create, delete). Requires VM.Snapshot on the
	// guest — only the NODE's token has it, and it is the one that has to show up
	// in the UPID.
	opGuest
)

// chaveParaOperacao is the ONLY function that decides which vault key opens
// which operation. See the block at the top of the file for the measurement
// that justifies it.
//
// It became a method because the choice of the READ token came to depend on the
// vault (does `pve_token_painel` exist?), and a free function has no way to ask.
func (r *Router) chaveParaOperacao(op operacaoPVX, no inventory.Node) string {
	if op == opGuest {
		return chaveDoNo(no)
	}
	return r.segredoDeLeituraDoHipervisor()
}

// clienteParaOperacao resolves token + client, keeping the THREE vault states
// apart. A vault that is down (503) and a credential that does not exist (409)
// call for opposite actions from the operator; collapsing the two into one
// empty result is the same mistake as handlers_ai.go:186, which already caused
// a logged defect.
func (r *Router) clienteParaOperacao(w http.ResponseWriter, op operacaoPVX, no inventory.Node) (hypervisorOps, bool) {
	chave := r.chaveParaOperacao(op, no)
	valor, estado := r.tokenDoCofre(chave)
	switch estado {
	case vaultInalcancavel:
		writeErr(w, 503, "vault unreachable — the credential ("+chave+") could not be read")
		return nil, false
	case vaultAusente:
		writeErr(w, 409, "missing credential in the vault: "+chave)
		return nil, false
	}
	cli, err := r.dial(valor)
	if err != nil {
		writeErr(w, 503, "hypervisor not configured: "+err.Error())
		return nil, false
	}
	return cli, true
}

// handleProxmox routes /api/proxmox and its subroutes. Same shape as
// handleNodes: primary first, store second, routing by path suffix.
func (r *Router) handleProxmox(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	st := r.inventoryStoreOrNil(w)
	if st == nil {
		return
	}
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	resto := strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/proxmox"), "/")
	somenteGET := func() bool {
		if req.Method != http.MethodGet {
			writeErr(w, 405, "method not allowed")
			return false
		}
		return true
	}

	switch resto {
	case "":
		if !somenteGET() {
			return
		}
		r.saudeDoHipervisor(w, inv)
	case "tasks":
		if !somenteGET() {
			return
		}
		r.tarefasDoHipervisor(w, req, inv)
	case "tasks/log":
		if !somenteGET() {
			return
		}
		r.logDaTarefa(w, req, inv)
	case "disks":
		if !somenteGET() {
			return
		}
		r.discosDoHipervisor(w, req, inv)
	case "storage":
		if !somenteGET() {
			return
		}
		r.capacidadeDeStorage(w, inv)
	case "zfs":
		if !somenteGET() {
			return
		}
		r.estadoDosZPools(w, inv)
	case "power":
		// 🔴 POST ONLY, never GET. A hypervisor shutdown reachable by GET would
		// be reachable by a link, by browser prefetch and by anything that
		// follows a URL — and this is the one command in the dashboard whose
		// mistake has no remote undo.
		r.energiaDoHipervisor(w, req, inv)
	case "rrd":
		// Series for the NODE or for a guest, depending on ?node=. Live: a chart
		// is only worth anything if it shows now.
		if !somenteGET() {
			return
		}
		r.serieTemporal(w, req, inv)
	case "sistema":
		// Network, DNS, time, certificates — the answers to "what is the bridge's
		// IP?" and "when does the certificate expire?" that the remote operator
		// did not have.
		if !somenteGET() {
			return
		}
		r.sistemaDoNo(w, req, inv)
	case "pacotes":
		if !somenteGET() {
			return
		}
		r.pacotesDoNo(w, req, inv)
	case "syslog":
		if !somenteGET() {
			return
		}
		r.syslogDoNo(w, req, inv)
	case "backup":
		// LIVE and on demand: this is the answer to "when was the last copy?",
		// and it changes once a day. Putting it in the poller would cost two
		// calls per tick.
		if !somenteGET() {
			return
		}
		r.frescorDeBackup(w, req, inv)
	case "zfs/topologia":
		// LIVE and on demand: pool topology rarely changes, and the tab is opened
		// deliberately. Putting this in the poller would cost two calls per tick
		// to answer a question that does not change between ticks.
		if !somenteGET() {
			return
		}
		r.topologiaDosZPools(w, req, inv)
	case "permissions":
		if !somenteGET() {
			return
		}
		r.permissoesDoToken(w, req, inv)
	case "snapshots":
		r.snapshotsDoGuest(w, req, inv)
	case "snapshots/rollback":
		// 🔴 POST ONLY. A rollback reachable by GET would be reachable by browser
		// prefetch, by a crawler and by a link pasted into a chat.
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.rollbackDoGuest(w, req, inv)
	default:
		writeErr(w, 404, "unknown route: /api/proxmox/"+resto)
	}
}

// nomeDoHipervisorNoInventario returns the name of the discovered host. No
// literal hostname lives here: either the poller has already stamped the
// hypervisor's document, or the name comes from the `host` node that discovery
// created.
func nomeDoHipervisorNoInventario(inv inventory.Inventory) string {
	if s := strings.TrimSpace(inv.Hypervisor.Node); s != "" {
		return s
	}
	for _, n := range inv.Nodes {
		if n.Kind == inventory.NodeKindHost && strings.TrimSpace(n.Name) != "" {
			return n.Name
		}
	}
	return ""
}

// noDoHipervisorOu409 resolves the host name or explains why it cannot.
func noDoHipervisorOu409(w http.ResponseWriter, inv inventory.Inventory) (string, bool) {
	nome := nomeDoHipervisorNoInventario(inv)
	if nome == "" {
		// Never "pve" by default: guessing a name would make the route return the
		// hypervisor's error instead of the real state ("the poller has not
		// discovered it yet").
		writeErr(w, 409, "hypervisor not discovered yet — the poller has not completed a tick")
		return "", false
	}
	return nome, true
}

// saudeDoHipervisor answers from the STORE, without touching the hypervisor.
// Health is a heartbeat and the poller is what collects it (one tick every
// 30 s); calling from here would turn every screen refresh into a live request
// AND, worse, would make the displayed age always "0 s", hiding precisely the
// hypervisor that has gone mute.
func (r *Router) saudeDoHipervisor(w http.ResponseWriter, inv inventory.Inventory) {
	writeJSON(w, map[string]any{
		"hypervisor":  inventory.ViewHypervisor(inv.Hypervisor, r.ttl(), r.agora()),
		"ttl_seconds": int64(r.ttl().Seconds()),
		"observed_at": r.agora().Unix(),
	})
}

// capacidadeDeStorage answers from the STORE, without touching the hypervisor —
// same rule as health, and for the same reason.
//
// 🔴 Capacity is a HEARTBEAT, not navigation. A pool that fills slowly only
// gives itself away through repeated observation; a pool that STOPS being
// observed only gives itself away through a growing age. If this route dialled
// out, the displayed age would always be "0 s" — and "0 s" over a number from
// half an hour ago is exactly the stale data presented as live that the
// acceptance criteria forbid.
//
// The cost of keeping this on the tick was measured: /nodes/{n}/storage 168 ms
// and /nodes/{n}/disks/zfs 96 ms, against the 92 ms of the health call that was
// already there — and against the 597 ms of /disks/list, which is why that one
// stayed on demand.
//
// The response carries `datastore_audit` BECAUSE `pools: []` is ambiguous on
// its own.
func (r *Router) capacidadeDeStorage(w http.ResponseWriter, inv inventory.Inventory) {
	v := inventory.ViewStorage(inv.Hypervisor, r.ttl(), r.agora())
	writeJSON(w, map[string]any{
		"node":            nomeDoHipervisorNoInventario(inv),
		"pools":           v.Pools,
		"datastore_audit": v.DatastoreAudit,
		"age_seconds":     v.AgeSeconds,
		"stale":           v.Stale,
		"ttl_seconds":     int64(r.ttl().Seconds()),
		"observed_at":     r.agora().Unix(),
	})
}

// estadoDosZPools answers from the STORE, with its OWN timestamp — its age is
// neither health's nor storage's: the three calls fail independently.
//
// 🔴 It is the most important route in the lab and the easiest to
// underestimate: the whole server lands on a SINGLE-DISK pool, with no
// redundancy. A pool leaving ONLINE is the most expensive news in the house,
// and until now that news only existed in the Proxmox UI — which the operator,
// working fully remotely, cannot reach.
func (r *Router) estadoDosZPools(w http.ResponseWriter, inv inventory.Inventory) {
	v := inventory.ViewZPools(inv.Hypervisor, r.ttl(), r.agora())
	writeJSON(w, map[string]any{
		"node":        nomeDoHipervisorNoInventario(inv),
		"pools":       v.Pools,
		"age_seconds": v.AgeSeconds,
		"stale":       v.Stale,
		"ttl_seconds": int64(r.ttl().Seconds()),
		"observed_at": r.agora().Unix(),
	})
}

// energiaDoHipervisor reboots or shuts down the node ITSELF.
//
// 🔴 THE ONE ACTION IN THE DASHBOARD WHOSE MISTAKE HAS NO REMOTE UNDO. This
// machine has no IPMI, and Wake-on-LAN is no help: the host itself is what
// routes the admin network (it runs the Tailscale subnet router), so once it is
// down there is nowhere left to wake it from. Bringing it back means walking to
// the machine.
//
// The operator asked for this feature knowing that — it is on the record. What
// the code owes is: (1) an allowlist on the command, (2) auditing BEFORE
// firing, and (3) not waiting for the task to finish.
//
// (2) is what matters when something goes wrong: if the audit record were only
// written AFTERWARDS, a successful shutdown would take the record down with it
// — and nobody would know who pressed the button, because the machine that
// would hold the answer is the one that powered off.
//
// (3) is because the "reboot" task only finishes when the machine COMES BACK.
// Waiting for it would hold the request open precisely while the other end
// dies; what can still be asserted from this side is that the command was
// ACCEPTED.
func (r *Router) energiaDoHipervisor(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed — hypervisor power is POST")
		return
	}
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	cmd, ok := pve.ComandoDeEnergiaValido(req.URL.Query().Get("command"))
	if !ok {
		writeErr(w, 400, "invalid command — only 'reboot' or 'shutdown'")
		return
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}

	// Audit BEFORE firing. See the comment above: there may be no "afterwards".
	usuario := auth.UserFrom(req)
	r.auditEvent(req, usuario, "pve.node-power", string(cmd)+" "+node)

	upid, err := cli.NodePower(req.Context(), node, cmd)
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused "+string(cmd)+": "+detalheDoErroPVE(err))
		return
	}

	// Return WHO goes down with it. The operator has just ordered the shutdown
	// of the machine hosting these guests, and the list is their last chance to
	// see what that means — including in a log, later.
	var afetados []string
	for _, n := range inv.Nodes {
		if n.Kind != inventory.NodeKindGuest || n.VMID <= 0 {
			continue
		}
		if st := n.Status.Value; st == "running" || st == "online" {
			afetados = append(afetados, n.ID)
		}
	}
	writeJSON(w, map[string]any{
		"node": node, "command": string(cmd), "upid": upid,
		"guests_afetados": afetados,
		"observed_at":     r.agora().Unix(),
		"aviso":           "the command was accepted by the hypervisor; from here on the panel loses contact with it",
	})
}

// serieTemporal returns the series for the node or for a guest.
//
// 🔴 THE WINDOW GOES THROUGH AN ALLOWLIST. `timeframe` ends up in the
// hypervisor's URL; without the allowlist, a string coming from the dashboard's
// query string would become a path inside PVE. pve.JanelaRRDValida is the only
// source of that list.
//
// The series arrives ALREADY AGGREGATED from RRD: the dashboard does not
// interpolate, does not resample and does not invent points. A hole in the
// series is a real hole — the hypervisor was off, or RRD had no data yet.
// Filling it with zeros would make a power cut look like an idle period.
func (r *Router) serieTemporal(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	q := req.URL.Query()
	janela, ok := pve.JanelaRRDValida(q.Get("janela"))
	if !ok {
		janela = pve.JanelaHora
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}

	alvo := strings.TrimSpace(q.Get("node"))
	if alvo == "" {
		pts, err := cli.RRDNode(req.Context(), node, janela)
		if err != nil {
			writeErr(w, codigoDoErroPVE(err), "hypervisor refused the node series: "+detalheDoErroPVE(err))
			return
		}
		writeJSON(w, map[string]any{"alvo": node, "escopo": "node", "janela": string(janela),
			"pontos": pts, "observed_at": r.agora().Unix()})
		return
	}

	// Guest: the id carries the type ("lxc/207"), and that is where the PVE path
	// comes from. Accepting the type as a separate query parameter would let the
	// client claim an LXC is QEMU — and the hypervisor would answer 500 about a
	// guest that does exist.
	no, achado := achaNo(inv, alvo)
	if !achado || no.VMID <= 0 {
		writeErr(w, 404, "unknown guest in the inventory: "+alvo)
		return
	}
	typ := strings.SplitN(no.ID, "/", 2)[0]
	pts, err := cli.RRDGuest(req.Context(), node, no.VMID, typ, janela)
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the guest series: "+detalheDoErroPVE(err))
		return
	}
	writeJSON(w, map[string]any{"alvo": no.ID, "escopo": "guest", "janela": string(janela),
		"pontos": pts, "observed_at": r.agora().Unix()})
}

// sistemaDoNo joins network, DNS, time and certificates into a single response.
//
// That is four calls to the hypervisor, and one isolated failure does NOT take
// the others down: each block carries its own error. A response that dies whole
// because DNS did not answer would hide the bridge's IP, which may well be
// exactly what the operator came looking for.
func (r *Router) sistemaDoNo(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}
	ctx := req.Context()
	out := map[string]any{"node": node, "observed_at": r.agora().Unix()}

	if v, err := cli.Network(ctx, node); err != nil {
		out["network_erro"] = detalheDoErroPVE(err)
	} else {
		out["network"] = v
	}
	if v, err := cli.DNS(ctx, node); err != nil {
		out["dns_erro"] = detalheDoErroPVE(err)
	} else {
		out["dns"] = v
	}
	if v, err := cli.Time(ctx, node); err != nil {
		out["time_erro"] = detalheDoErroPVE(err)
	} else {
		out["time"] = v
	}
	if v, err := cli.Certificados(ctx, node); err != nil {
		out["certificados_erro"] = detalheDoErroPVE(err)
	} else {
		out["certificados"] = v
	}
	writeJSON(w, out)
}

// pacotesDoNo lists the installed packages with their versions.
func (r *Router) pacotesDoNo(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}
	ps, err := cli.Pacotes(req.Context(), node)
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the package list: "+detalheDoErroPVE(err))
		return
	}
	writeJSON(w, map[string]any{"node": node, "pacotes": ps, "observed_at": r.agora().Unix()})
}

// syslogDoNo returns the last lines of the hypervisor's journal.
//
// The cap is pve.MaxSyslog and it is applied HERE and again in the client —
// with the SAME constant, never with a copy of the number. Two independent caps
// diverge the day someone touches one of them.
func (r *Router) syslogDoNo(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}
	limite := pve.MaxSyslog
	if n, err := strconv.Atoi(req.URL.Query().Get("limit")); err == nil && n > 0 && n < limite {
		limite = n
	}
	linhas, err := cli.Syslog(req.Context(), node, limite)
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the syslog: "+detalheDoErroPVE(err))
		return
	}
	writeJSON(w, map[string]any{"node": node, "linhas": linhas, "limite": limite,
		"observed_at": r.agora().Unix()})
}

// frescorDeBackup answers "when was the last copy, and of how many guests".
//
// 🔴 This lab's off-site chain has already died for 14 DAYS in silence, and
// nothing on screen could say so. Having a backup and being able to say when
// the last one ran are different things — only the second one becomes an alarm.
//
// The two datastores are different layers and show up SEPARATELY on purpose:
// `pbs` is layer 2 (deduplicated, with verification) and `backupusb` is the
// rotation. Merging the two into a single number would hide exactly the case
// that matters — one layer fresh and the other stopped.
func (r *Router) frescorDeBackup(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}
	// The backup datastores come from the inventory, not from a fixed list: a
	// name nailed into the code stops existing the day the operator renames the
	// datastore, and the symptom would be the screen saying "no backup" about a
	// datastore that is full — the most expensive lie this screen can tell.
	v := inventory.ViewStorage(inv.Hypervisor, r.ttl(), r.agora())
	var alvos []string
	for _, p := range v.Pools {
		for _, c := range p.Content {
			if c == "backup" {
				alvos = append(alvos, p.ID)
				break
			}
		}
	}
	// 🔴 WHAT IS SCHEDULED. Without this the screen paints red over a layer the
	// operator disarmed by a dated decision — and permanent red trains people to
	// ignore it, which is the disease that has already cost this lab the
	// credibility of its alarm channel. Failing to read the jobs does NOT take
	// freshness down: what is lost is the distinction, not the number.
	// map: storage -> {state, schedule}. Only what HAS a job in PVE goes in; the
	// absence is meaningful and is read as "outside-pve" further down.
	type agenda struct{ estado, schedule string }
	jobs := map[string]agenda{}
	if js, err := cli.JobsDeBackup(req.Context()); err == nil {
		for _, j := range js {
			if j.Storage == "" {
				continue
			}
			if j.Agendado() {
				jobs[j.Storage] = agenda{"ativo", j.Schedule}
			} else {
				// The job exists and is switched off: SOMEBODY decided that. It
				// is different from there being no job at all, and it is the
				// distinction that prevents permanent red over a layer that was
				// disarmed on purpose.
				jobs[j.Storage] = agenda{"desarmado", j.Schedule}
			}
		}
	}
	estadoDaAgenda := func(st string) (string, string) {
		if a, ok := jobs[st]; ok {
			return a.estado, a.schedule
		}
		return "fora-do-pve", ""
	}

	saida := make([]pve.FrescorDeBackup, 0, len(alvos))
	for _, st := range alvos {
		f, err := cli.BackupsDoDatastore(req.Context(), node, st)
		if err != nil {
			// An unreadable datastore must not take the others down, nor turn into
			// silence: it goes into the list saying it failed, with the reason.
			est, sch := estadoDaAgenda(st)
			saida = append(saida, pve.FrescorDeBackup{Storage: st, Erro: detalheDoErroPVE(err),
				Agendamento: est, Schedule: sch})
			continue
		}
		f.Agendamento, f.Schedule = estadoDaAgenda(st)
		saida = append(saida, f)
	}
	writeJSON(w, map[string]any{
		"node": node, "datastores": saida, "observed_at": r.agora().Unix(),
	})
}

// topologiaDosZPools answers HOW each pool is built — and, as a consequence,
// what is lost when a disk dies.
//
// 🔴 This is the question the screen could not answer. `zfs` says there is an
// `rpool` with 70 GB allocated; `disks` says there is a 1 TB Lexar NVMe.
// Nothing tied the two together. In a lab with a SINGLE DISK and NO MIRROR,
// "what do I lose if this disk dies?" is the most expensive question there is —
// and it had no answer on screen.
//
// Redundancy here is DERIVED from the topology, never asserted by fixed text. A
// screen that says "no redundancy" from a hardcoded string starts lying the day
// someone adds the second NVMe the plan defers to next year.
func (r *Router) topologiaDosZPools(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}
	pools, err := cli.ZFSList(req.Context(), node)
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the pool list: "+detalheDoErroPVE(err))
		return
	}
	tops := make([]pve.ZPoolTopologia, 0, len(pools))
	for _, p := range pools {
		t, err := cli.ZFSTopologia(req.Context(), node, p.Name)
		if err != nil {
			// An unreadable pool must not take the others down — nor turn into
			// silence. It goes into the list saying it was not read, with the reason.
			tops = append(tops, pve.ZPoolTopologia{
				Nome: p.Name, Estado: "DESCONHECIDO",
				Erros: "could not read the topology: " + detalheDoErroPVE(err),
			})
			continue
		}
		tops = append(tops, t)
	}
	writeJSON(w, map[string]any{
		"node": node, "pools": tops, "observed_at": r.agora().Unix(),
	})
}

// tarefasDoHipervisor lists the NODE's tasks — LIVE, with the audit token.
func (r *Router) tarefasDoHipervisor(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}
	q := req.URL.Query()
	opt := pve.TaskListOptions{
		ErrorsOnly: q.Get("errors") == "1",
		TypeFilter: q.Get("typefilter"),
	}
	// The client's limit is a SUGGESTION. It is capped HERE, at the edge, and
	// again in internal/pve — but with the SAME constant (pve.MaxTasks), never
	// with a copy of the number: two independent caps diverge the day someone
	// touches one of them. Capping at the edge is what makes `?limit=99999` stop
	// existing before it becomes a request.
	if n, err := strconv.Atoi(q.Get("limit")); err == nil {
		if n > pve.MaxTasks {
			n = pve.MaxTasks
		}
		opt.Limit = n
	}
	if n, err := strconv.Atoi(q.Get("vmid")); err == nil {
		opt.VMID = n
	}

	tarefas, err := cli.TaskList(req.Context(), node, opt)
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the task list: "+detalheDoErroPVE(err))
		return
	}
	if tarefas == nil {
		tarefas = []pve.Task{}
	}
	writeJSON(w, map[string]any{
		"node": node, "tasks": tarefas,
		"errors_only": opt.ErrorsOnly,
		"observed_at": r.agora().Unix(),
	})
}

// logDaTarefa opens the log of ONE task — LIVE, with the audit token.
//
// The UPID goes in the QUERY, not in the path: it contains ':' and ServeMux
// does not slice that without pain. And there is no limit parameter: the cap
// (200 lines) belongs to the server, for the same reason internal/pve does not
// accept it in the signature.
func (r *Router) logDaTarefa(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	upid := strings.TrimSpace(req.URL.Query().Get("upid"))
	if upid == "" {
		writeErr(w, 400, "upid is required")
		return
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}
	linhas, err := cli.TaskLog(req.Context(), node, upid)
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the log: "+detalheDoErroPVE(err))
		return
	}
	if linhas == nil {
		linhas = []string{}
	}
	writeJSON(w, map[string]any{"node": node, "upid": upid, "lines": linhas})
}

// discoView adds the NORMALIZED wearout to the disk. PVE sends a number for an
// SSD that reports remaining life and the string "N/A" for a disk that reports
// none; shipping the raw field to the browser would push the type check into
// the screen, which is where it turns into a forgotten `typeof x === 'number'`.
type discoView struct {
	pve.Disk
	WearoutPct *float64 `json:"wearout_pct"`
}

// discosDoHipervisor lists the physical disks with SMART — LIVE, audit token.
// Measured at 597 ms, the most expensive route of the set, which is why it is
// on demand and never enters the poller's tick.
func (r *Router) discosDoHipervisor(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := noDoHipervisorOu409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}
	discos, err := cli.DisksList(req.Context(), node)
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the disk list: "+detalheDoErroPVE(err))
		return
	}
	vistas := make([]discoView, 0, len(discos))
	for _, d := range discos {
		v := discoView{Disk: d}
		if pct, temDado := d.WearoutPct(); temDado {
			p := pct
			v.WearoutPct = &p
		}
		vistas = append(vistas, v)
	}
	writeJSON(w, map[string]any{"node": node, "disks": vistas, "observed_at": r.agora().Unix()})
}

// permissoesDoToken answers "what can this token do" — LIVE, audit token.
//
// 🔴 This is the route that explains ON SCREEN, by measurement rather than by
// promise, what the dashboard can and cannot see of the hypervisor. At first it
// documented the ABSENCE of privilege on /storage; once the ACL was granted it
// documents its PRESENCE — and the text on screen switches by itself, because
// what changed was the measurement, not the code.
//
// `storage_visivel` comes from pve.PodeAuditarDatastore, the SAME function the
// poller uses to stamp `datastore_audit`. A copy of the rule here would give two
// truths about the same question, and the second would age in silence — the
// defect chaveDeCredencial (handlers_nodes.go) has already cost this codebase.
func (r *Router) permissoesDoToken(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	cli, ok := r.clienteParaOperacao(w, opLeituraHipervisor, inventory.Node{})
	if !ok {
		return
	}
	perms, err := cli.Permissions(req.Context())
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the permissions: "+detalheDoErroPVE(err))
		return
	}
	storage := pve.PodeAuditarDatastore(perms)
	if perms == nil {
		perms = map[string]map[string]int{}
	}
	writeJSON(w, map[string]any{
		"permissions":     perms,
		"storage_visivel": storage,
		"observed_at":     r.agora().Unix(),
	})
}

// snapshotsDoGuest lists, creates and deletes snapshots — LIVE, with the NODE's
// token.
//
// 🔴 POST and DELETE only answer 200 after WaitTask (status "stopped" AND
// exitstatus "OK"). PVE's POST returns 200 as soon as the TASK IS CREATED:
// passing that 200 straight through would be the screen saying "snapshot ready"
// for a snapshot that may not even have started.
func (r *Router) snapshotsDoGuest(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	switch req.Method {
	case http.MethodGet, http.MethodPost, http.MethodDelete:
	default:
		writeErr(w, 405, "method not allowed")
		return
	}
	q := req.URL.Query()
	id := strings.TrimSpace(q.Get("node"))
	nome := strings.TrimSpace(q.Get("name"))

	// The name is rejected BEFORE anything else: it comes from the screen and it
	// is what builds the resource path on the hypervisor. Validating after
	// dialling out would spend a connection just to find out the screen sent
	// garbage.
	if req.Method != http.MethodGet {
		if err := pve.NomeDeSnapshotValido(nome); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}

	no, achou := achaNo(inv, id)
	if !achou {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	if no.Kind != inventory.NodeKindGuest || no.VMID <= 0 {
		writeErr(w, 400, "node "+id+" is not a guest of the hypervisor")
		return
	}

	cli, ok := r.clienteParaOperacao(w, opGuest, no)
	if !ok {
		return
	}
	tipo, host := tipoEHost(no)
	ctx := req.Context()

	if req.Method == http.MethodGet {
		snaps, err := cli.SnapshotList(ctx, host, no.VMID, tipo)
		if err != nil {
			writeErr(w, codigoDoErroPVE(err), "hypervisor refused the snapshot list: "+detalheDoErroPVE(err))
			return
		}
		// PVE includes the pseudo-entry "current" ("You are here!"), which is no
		// snapshot at all. Filtering here, rather than on screen, saves every
		// consumer from having to remember it.
		vistos := make([]pve.Snapshot, 0, len(snaps))
		for _, s := range snaps {
			if s.Name != "current" {
				vistos = append(vistos, s)
			}
		}
		writeJSON(w, map[string]any{"node": id, "snapshots": vistos, "observed_at": r.agora().Unix()})
		return
	}

	var upid string
	var err error
	acao := "create"
	if req.Method == http.MethodPost {
		upid, err = cli.SnapshotCreate(ctx, host, no.VMID, tipo, nome, strings.TrimSpace(q.Get("desc")))
	} else {
		acao = "delete"
		upid, err = cli.SnapshotDelete(ctx, host, no.VMID, tipo, nome)
	}
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused snapshot "+acao+": "+detalheDoErroPVE(err))
		return
	}

	if _, err := esperaTarefa(ctx, cli, host, upid); err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.snapshot",
			"node="+id+" acao="+acao+" nome="+nome+" upid="+upid+" status=falhou")
		// The exitstatus goes in EXPLICITLY: when Status is 0, the pve.Error
		// formatter takes the Err branch and the Body — which carries the real
		// reason — never shows up on its own.
		writeErr(w, 502, "task "+upid+" did not finish cleanly: "+detalheDoErroPVE(err))
		return
	}

	// A mutation on the hypervisor with NO trail is a mutation nobody can
	// reconstruct afterwards.
	r.auditEvent(req, auth.UserFrom(req), "pve.snapshot",
		"node="+id+" acao="+acao+" nome="+nome+" upid="+upid+" status=ok")
	writeJSON(w, map[string]any{"node": id, "action": acao, "name": nome, "upid": upid, "status": "ok"})
}

// rollbackDoGuest returns the guest to a snapshot's state — LIVE, NODE token.
//
// ─────────────────────────────────────────────────────────────────────────────
// 🔴 THE FINDING: the privilege WAS ALREADY GRANTED and the dashboard did not
// show it.
//
// The LabOperador role has `VM.Snapshot`, and `Qemu.pm:6301` /
// `LXC/Snapshot.pm:275` accept `VM.Snapshot` for ROLLBACK — not only for create
// and delete. Measured without touching a single ACL:
//
//	node-lab → POST …/lxc/204/snapshot/<nonexistent>/rollback → 200 + UPID
//	audit    → the SAME POST → 403 (/vms/204, VM.Snapshot|VM.Snapshot.Rollback)
//	node-lab → the same POST on /lxc/207 → 403  (isolation by vmid)
//
// In other words: the power to discard everything since the snapshot was there,
// nobody could see it, and no screen recorded anyone using it. Hiding it did
// not make it unreachable — it only made it unauditable. Exposing it behind a
// type-to-confirm prompt and an audit trail is the safer of the two designs.
//
// 🔴 AND "THE TASK WAS CREATED" ≠ "THE TASK SUCCEEDED" SHOWED UP HERE IN
// MEASURED FORM. A rollback to a NONEXISTENT snapshot returned **200 with a
// UPID**; only the task status told the truth:
//
//	status=stopped  exitstatus="snapshot '<name>' does not exist"
//
// Passing that 200 along would be the screen saying "restored" for a rollback
// that never happened — on a guest the operator would go on believing sat in an
// earlier state. That is why this route's 200 only goes out after WaitTask.
// ─────────────────────────────────────────────────────────────────────────────
func (r *Router) rollbackDoGuest(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	q := req.URL.Query()
	id := strings.TrimSpace(q.Get("node"))
	nome := strings.TrimSpace(q.Get("name"))

	// The name is rejected BEFORE anything else: it comes from the screen and it
	// chooses WHICH state the guest will take on. Validating after dialling out
	// would spend a connection just to find out the screen sent garbage.
	if err := pve.NomeDeSnapshotValido(nome); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	no, achou := achaNo(inv, id)
	if !achou {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	if no.Kind != inventory.NodeKindGuest || no.VMID <= 0 {
		writeErr(w, 400, "node "+id+" is not a guest of the hypervisor")
		return
	}

	cli, ok := r.clienteParaOperacao(w, opGuest, no)
	if !ok {
		return
	}
	tipo, host := tipoEHost(no)
	ctx := req.Context()

	upid, err := cli.SnapshotRollback(ctx, host, no.VMID, tipo, nome)
	if err != nil {
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the rollback: "+detalheDoErroPVE(err))
		return
	}
	if _, err := esperaTarefa(ctx, cli, host, upid); err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.snapshot",
			"node="+id+" acao=rollback nome="+nome+" upid="+upid+" status=falhou")
		writeErr(w, 502, "task "+upid+" did not finish cleanly: "+detalheDoErroPVE(err))
		return
	}

	// The trail is mandatory: this is the most destructive mutation this
	// dashboard fires, and what it erases has no second copy — the pool is
	// single-disk.
	r.auditEvent(req, auth.UserFrom(req), "pve.snapshot",
		"node="+id+" acao=rollback nome="+nome+" upid="+upid+" status=ok")
	writeJSON(w, map[string]any{"node": id, "action": "rollback", "name": nome, "upid": upid, "status": "ok"})
}

// 🔴 SUSPEND DOES NOT EXIST IN THIS DASHBOARD, AND THAT IS DELIBERATE.
//
// Measured against this host: the `vzsuspend 204` task ended in
// `lxc-checkpoint -n 204 -s -D /var/lib/vz/dump failed: exit code 1` — CRIU
// cannot checkpoint this container — and the CT stayed `running`.
//
// A button that ALWAYS errors is not an unfinished feature: it is training to
// ignore errors. The operator learns that this particular red is normal, and
// the next red, the real one, goes unnoticed. Suspend comes back when CRIU
// works on this host, measured — not before. TestNenhumaRotaOfereceSuspend is
// the pin.
