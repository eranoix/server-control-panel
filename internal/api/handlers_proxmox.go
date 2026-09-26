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
// lives in a single function (keyForOp).
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
// defect in this codebase (see credentialKey in handlers_nodes.go). That is
// why the choice is ONE function, and why TestTasksUseAuditToken pins it by
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
// The verdict has ONE source — pve.CanAuditDatastore — used both by the
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

// pvxOp says what is about to be done on the hypervisor. There are two
// cases because there are two disjoint roles, not because there are two routes.
type pvxOp int

const (
	// opHypervisorRead: tasks, task log, disks and permissions. All of them
	// require Sys.Audit on /nodes — only the AUDIT token has it.
	opHypervisorRead pvxOp = iota
	// opGuest: snapshots (list, create, delete). Requires VM.Snapshot on the
	// guest — only the NODE's token has it, and it is the one that has to show up
	// in the UPID.
	opGuest
)

// keyForOp is the ONLY function that decides which vault key opens
// which operation. See the block at the top of the file for the measurement
// that justifies it.
//
// It became a method because the choice of the READ token came to depend on the
// vault (does `pve_token_painel` exist?), and a free function has no way to ask.
func (r *Router) keyForOp(op pvxOp, no inventory.Node) string {
	if op == opGuest {
		return nodeKey(no)
	}
	return r.hypervisorReadSecret()
}

// clientForOp resolves token + client, keeping the THREE vault states
// apart. A vault that is down (503) and a credential that does not exist (409)
// call for opposite actions from the operator; collapsing the two into one
// empty result is the same mistake as handlers_ai.go:186, which already caused
// a logged defect.
func (r *Router) clientForOp(w http.ResponseWriter, op pvxOp, no inventory.Node) (hypervisorOps, bool) {
	key := r.keyForOp(op, no)
	value, state := r.vaultToken(key)
	switch state {
	case vaultUnreachable:
		writeErr(w, 503, "vault unreachable — the credential ("+key+") could not be read")
		return nil, false
	case vaultMissing:
		writeErr(w, 409, "missing credential in the vault: "+key)
		return nil, false
	}
	cli, err := r.dial(value)
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

	rest := strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/proxmox"), "/")
	getOnly := func() bool {
		if req.Method != http.MethodGet {
			writeErr(w, 405, "method not allowed")
			return false
		}
		return true
	}

	switch rest {
	case "":
		if !getOnly() {
			return
		}
		r.hypervisorHealth(w, inv)
	case "tasks":
		if !getOnly() {
			return
		}
		r.hypervisorTasks(w, req, inv)
	case "tasks/log":
		if !getOnly() {
			return
		}
		r.taskLog(w, req, inv)
	case "disks":
		if !getOnly() {
			return
		}
		r.hypervisorDisks(w, req, inv)
	case "storage":
		if !getOnly() {
			return
		}
		r.storageCapacity(w, inv)
	case "zfs":
		if !getOnly() {
			return
		}
		r.zpoolState(w, inv)
	case "power":
		// 🔴 POST ONLY, never GET. A hypervisor shutdown reachable by GET would
		// be reachable by a link, by browser prefetch and by anything that
		// follows a URL — and this is the one command in the dashboard whose
		// mistake has no remote undo.
		r.hypervisorPower(w, req, inv)
	case "rrd":
		// Series for the NODE or for a guest, depending on ?node=. Live: a chart
		// is only worth anything if it shows now.
		if !getOnly() {
			return
		}
		r.timeSeries(w, req, inv)
	case "sistema":
		// Network, DNS, time, certificates — the answers to "what is the bridge's
		// IP?" and "when does the certificate expire?" that the remote operator
		// did not have.
		if !getOnly() {
			return
		}
		r.nodeSystem(w, req, inv)
	case "pacotes":
		if !getOnly() {
			return
		}
		r.nodePackages(w, req, inv)
	case "syslog":
		if !getOnly() {
			return
		}
		r.nodeSyslog(w, req, inv)
	case "backup":
		// LIVE and on demand: this is the answer to "when was the last copy?",
		// and it changes once a day. Putting it in the poller would cost two
		// calls per tick.
		if !getOnly() {
			return
		}
		r.backupFreshness(w, req, inv)
	case "zfs/topologia":
		// LIVE and on demand: pool topology rarely changes, and the tab is opened
		// deliberately. Putting this in the poller would cost two calls per tick
		// to answer a question that does not change between ticks.
		if !getOnly() {
			return
		}
		r.zpoolTopology(w, req, inv)
	case "permissions":
		if !getOnly() {
			return
		}
		r.tokenPermissions(w, req, inv)
	case "snapshots":
		r.guestSnapshots(w, req, inv)
	case "snapshots/rollback":
		// 🔴 POST ONLY. A rollback reachable by GET would be reachable by browser
		// prefetch, by a crawler and by a link pasted into a chat.
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.guestRollback(w, req, inv)
	default:
		writeErr(w, 404, "unknown route: /api/proxmox/"+rest)
	}
}

// hypervisorNameInInventory returns the name of the discovered host. No
// literal hostname lives here: either the poller has already stamped the
// hypervisor's document, or the name comes from the `host` node that discovery
// created.
func hypervisorNameInInventory(inv inventory.Inventory) string {
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

// hypervisorNodeOr409 resolves the host name or explains why it cannot.
func hypervisorNodeOr409(w http.ResponseWriter, inv inventory.Inventory) (string, bool) {
	name := hypervisorNameInInventory(inv)
	if name == "" {
		// Never "pve" by default: guessing a name would make the route return the
		// hypervisor's error instead of the real state ("the poller has not
		// discovered it yet").
		writeErr(w, 409, "hypervisor not discovered yet — the poller has not completed a tick")
		return "", false
	}
	return name, true
}

// hypervisorHealth answers from the STORE, without touching the hypervisor.
// Health is a heartbeat and the poller is what collects it (one tick every
// 30 s); calling from here would turn every screen refresh into a live request
// AND, worse, would make the displayed age always "0 s", hiding precisely the
// hypervisor that has gone mute.
func (r *Router) hypervisorHealth(w http.ResponseWriter, inv inventory.Inventory) {
	writeJSON(w, map[string]any{
		"hypervisor":  inventory.ViewHypervisor(inv.Hypervisor, r.ttl(), r.now()),
		"ttl_seconds": int64(r.ttl().Seconds()),
		"observed_at": r.now().Unix(),
	})
}

// storageCapacity answers from the STORE, without touching the hypervisor —
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
func (r *Router) storageCapacity(w http.ResponseWriter, inv inventory.Inventory) {
	v := inventory.ViewStorage(inv.Hypervisor, r.ttl(), r.now())
	writeJSON(w, map[string]any{
		"node":            hypervisorNameInInventory(inv),
		"pools":           v.Pools,
		"datastore_audit": v.DatastoreAudit,
		"age_seconds":     v.AgeSeconds,
		"stale":           v.Stale,
		"ttl_seconds":     int64(r.ttl().Seconds()),
		"observed_at":     r.now().Unix(),
	})
}

// zpoolState answers from the STORE, with its OWN timestamp — its age is
// neither health's nor storage's: the three calls fail independently.
//
// 🔴 It is the most important route in the lab and the easiest to
// underestimate: the whole server lands on a SINGLE-DISK pool, with no
// redundancy. A pool leaving ONLINE is the most expensive news in the house,
// and until now that news only existed in the Proxmox UI — which the operator,
// working fully remotely, cannot reach.
func (r *Router) zpoolState(w http.ResponseWriter, inv inventory.Inventory) {
	v := inventory.ViewZPools(inv.Hypervisor, r.ttl(), r.now())
	writeJSON(w, map[string]any{
		"node":        hypervisorNameInInventory(inv),
		"pools":       v.Pools,
		"age_seconds": v.AgeSeconds,
		"stale":       v.Stale,
		"ttl_seconds": int64(r.ttl().Seconds()),
		"observed_at": r.now().Unix(),
	})
}

// hypervisorPower reboots or shuts down the node ITSELF.
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
func (r *Router) hypervisorPower(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	if req.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed — hypervisor power is POST")
		return
	}
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	cmd, ok := pve.ValidPowerCommand(req.URL.Query().Get("command"))
	if !ok {
		writeErr(w, 400, "invalid command — only 'reboot' or 'shutdown'")
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}

	// Audit BEFORE firing. See the comment above: there may be no "afterwards".
	user := auth.UserFrom(req)
	r.auditEvent(req, user, "pve.node-power", string(cmd)+" "+node)

	upid, err := cli.NodePower(req.Context(), node, cmd)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused "+string(cmd)+": "+pveErrorDetail(err))
		return
	}

	// Return WHO goes down with it. The operator has just ordered the shutdown
	// of the machine hosting these guests, and the list is their last chance to
	// see what that means — including in a log, later.
	var affected []string
	for _, n := range inv.Nodes {
		if n.Kind != inventory.NodeKindGuest || n.VMID <= 0 {
			continue
		}
		if st := n.Status.Value; st == "running" || st == "online" {
			affected = append(affected, n.ID)
		}
	}
	writeJSON(w, map[string]any{
		"node": node, "command": string(cmd), "upid": upid,
		"guests_afetados": affected,
		"observed_at":     r.now().Unix(),
		"aviso":           "the command was accepted by the hypervisor; from here on the panel loses contact with it",
	})
}

// timeSeries returns the series for the node or for a guest.
//
// 🔴 THE WINDOW GOES THROUGH AN ALLOWLIST. `timeframe` ends up in the
// hypervisor's URL; without the allowlist, a string coming from the dashboard's
// query string would become a path inside PVE. pve.ValidRRDWindow is the only
// source of that list.
//
// The series arrives ALREADY AGGREGATED from RRD: the dashboard does not
// interpolate, does not resample and does not invent points. A hole in the
// series is a real hole — the hypervisor was off, or RRD had no data yet.
// Filling it with zeros would make a power cut look like an idle period.
func (r *Router) timeSeries(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	q := req.URL.Query()
	window, ok := pve.ValidRRDWindow(q.Get("janela"))
	if !ok {
		window = pve.WindowHour
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}

	target := strings.TrimSpace(q.Get("node"))
	if target == "" {
		pts, err := cli.RRDNode(req.Context(), node, window)
		if err != nil {
			writeErr(w, pveErrorCode(err), "hypervisor refused the node series: "+pveErrorDetail(err))
			return
		}
		writeJSON(w, map[string]any{"alvo": node, "escopo": "node", "janela": string(window),
			"pontos": pts, "observed_at": r.now().Unix()})
		return
	}

	// Guest: the id carries the type ("lxc/207"), and that is where the PVE path
	// comes from. Accepting the type as a separate query parameter would let the
	// client claim an LXC is QEMU — and the hypervisor would answer 500 about a
	// guest that does exist.
	no, hit := findNode(inv, target)
	if !hit || no.VMID <= 0 {
		writeErr(w, 404, "unknown guest in the inventory: "+target)
		return
	}
	typ := strings.SplitN(no.ID, "/", 2)[0]
	pts, err := cli.RRDGuest(req.Context(), node, no.VMID, typ, window)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused the guest series: "+pveErrorDetail(err))
		return
	}
	writeJSON(w, map[string]any{"alvo": no.ID, "escopo": "guest", "janela": string(window),
		"pontos": pts, "observed_at": r.now().Unix()})
}

// nodeSystem joins network, DNS, time and certificates into a single response.
//
// That is four calls to the hypervisor, and one isolated failure does NOT take
// the others down: each block carries its own error. A response that dies whole
// because DNS did not answer would hide the bridge's IP, which may well be
// exactly what the operator came looking for.
func (r *Router) nodeSystem(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}
	ctx := req.Context()
	out := map[string]any{"node": node, "observed_at": r.now().Unix()}

	if v, err := cli.Network(ctx, node); err != nil {
		out["network_erro"] = pveErrorDetail(err)
	} else {
		out["network"] = v
	}
	if v, err := cli.DNS(ctx, node); err != nil {
		out["dns_erro"] = pveErrorDetail(err)
	} else {
		out["dns"] = v
	}
	if v, err := cli.Time(ctx, node); err != nil {
		out["time_erro"] = pveErrorDetail(err)
	} else {
		out["time"] = v
	}
	if v, err := cli.Certificates(ctx, node); err != nil {
		out["certificados_erro"] = pveErrorDetail(err)
	} else {
		out["certificados"] = v
	}
	writeJSON(w, out)
}

// nodePackages lists the installed packages with their versions.
func (r *Router) nodePackages(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}
	ps, err := cli.Packages(req.Context(), node)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused the package list: "+pveErrorDetail(err))
		return
	}
	writeJSON(w, map[string]any{"node": node, "pacotes": ps, "observed_at": r.now().Unix()})
}

// nodeSyslog returns the last lines of the hypervisor's journal.
//
// The cap is pve.MaxSyslog and it is applied HERE and again in the client —
// with the SAME constant, never with a copy of the number. Two independent caps
// diverge the day someone touches one of them.
func (r *Router) nodeSyslog(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}
	limit := pve.MaxSyslog
	if n, err := strconv.Atoi(req.URL.Query().Get("limit")); err == nil && n > 0 && n < limit {
		limit = n
	}
	lines, err := cli.Syslog(req.Context(), node, limit)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused the syslog: "+pveErrorDetail(err))
		return
	}
	writeJSON(w, map[string]any{"node": node, "linhas": lines, "limite": limit,
		"observed_at": r.now().Unix()})
}

// backupFreshness answers "when was the last copy, and of how many guests".
//
// 🔴 This lab's off-site chain has already died for 14 DAYS in silence, and
// nothing on screen could say so. Having a backup and being able to say when
// the last one ran are different things — only the second one becomes an alarm.
//
// The two datastores are different layers and show up SEPARATELY on purpose:
// `pbs` is layer 2 (deduplicated, with verification) and `backupusb` is the
// rotation. Merging the two into a single number would hide exactly the case
// that matters — one layer fresh and the other stopped.
func (r *Router) backupFreshness(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}
	// The backup datastores come from the inventory, not from a fixed list: a
	// name nailed into the code stops existing the day the operator renames the
	// datastore, and the symptom would be the screen saying "no backup" about a
	// datastore that is full — the most expensive lie this screen can tell.
	v := inventory.ViewStorage(inv.Hypervisor, r.ttl(), r.now())
	var targets []string
	for _, p := range v.Pools {
		for _, c := range p.Content {
			if c == "backup" {
				targets = append(targets, p.ID)
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
	type agenda struct{ state, schedule string }
	jobs := map[string]agenda{}
	if js, err := cli.BackupJobs(req.Context()); err == nil {
		for _, j := range js {
			if j.Storage == "" {
				continue
			}
			if j.IsScheduled() {
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
	scheduleState := func(st string) (string, string) {
		if a, ok := jobs[st]; ok {
			return a.state, a.schedule
		}
		return "fora-do-pve", ""
	}

	output := make([]pve.BackupFreshness, 0, len(targets))
	for _, st := range targets {
		f, err := cli.DatastoreBackups(req.Context(), node, st)
		if err != nil {
			// An unreadable datastore must not take the others down, nor turn into
			// silence: it goes into the list saying it failed, with the reason.
			est, sch := scheduleState(st)
			output = append(output, pve.BackupFreshness{Storage: st, Error: pveErrorDetail(err),
				Scheduler: est, Schedule: sch})
			continue
		}
		f.Scheduler, f.Schedule = scheduleState(st)
		output = append(output, f)
	}
	writeJSON(w, map[string]any{
		"node": node, "datastores": output, "observed_at": r.now().Unix(),
	})
}

// zpoolTopology answers HOW each pool is built — and, as a consequence,
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
func (r *Router) zpoolTopology(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}
	pools, err := cli.ZFSList(req.Context(), node)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused the pool list: "+pveErrorDetail(err))
		return
	}
	tops := make([]pve.ZPoolTopology, 0, len(pools))
	for _, p := range pools {
		t, err := cli.ZFSTopology(req.Context(), node, p.Name)
		if err != nil {
			// An unreadable pool must not take the others down — nor turn into
			// silence. It goes into the list saying it was not read, with the reason.
			tops = append(tops, pve.ZPoolTopology{
				Name: p.Name, State: "DESCONHECIDO",
				Errors: "could not read the topology: " + pveErrorDetail(err),
			})
			continue
		}
		tops = append(tops, t)
	}
	writeJSON(w, map[string]any{
		"node": node, "pools": tops, "observed_at": r.now().Unix(),
	})
}

// hypervisorTasks lists the NODE's tasks — LIVE, with the audit token.
func (r *Router) hypervisorTasks(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
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

	tasks, err := cli.TaskList(req.Context(), node, opt)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused the task list: "+pveErrorDetail(err))
		return
	}
	if tasks == nil {
		tasks = []pve.Task{}
	}
	writeJSON(w, map[string]any{
		"node": node, "tasks": tasks,
		"errors_only": opt.ErrorsOnly,
		"observed_at": r.now().Unix(),
	})
}

// taskLog opens the log of ONE task — LIVE, with the audit token.
//
// The UPID goes in the QUERY, not in the path: it contains ':' and ServeMux
// does not slice that without pain. And there is no limit parameter: the cap
// (200 lines) belongs to the server, for the same reason internal/pve does not
// accept it in the signature.
func (r *Router) taskLog(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	upid := strings.TrimSpace(req.URL.Query().Get("upid"))
	if upid == "" {
		writeErr(w, 400, "upid is required")
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}
	lines, err := cli.TaskLog(req.Context(), node, upid)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused the log: "+pveErrorDetail(err))
		return
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, map[string]any{"node": node, "upid": upid, "lines": lines})
}

// diskView adds the NORMALIZED wearout to the disk. PVE sends a number for an
// SSD that reports remaining life and the string "N/A" for a disk that reports
// none; shipping the raw field to the browser would push the type check into
// the screen, which is where it turns into a forgotten `typeof x === 'number'`.
type diskView struct {
	pve.Disk
	WearoutPct *float64 `json:"wearout_pct"`
}

// hypervisorDisks lists the physical disks with SMART — LIVE, audit token.
// Measured at 597 ms, the most expensive route of the set, which is why it is
// on demand and never enters the poller's tick.
func (r *Router) hypervisorDisks(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}
	disks, err := cli.DisksList(req.Context(), node)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused the disk list: "+pveErrorDetail(err))
		return
	}
	seen := make([]diskView, 0, len(disks))
	for _, d := range disks {
		v := diskView{Disk: d}
		if pct, hasData := d.WearoutPct(); hasData {
			p := pct
			v.WearoutPct = &p
		}
		seen = append(seen, v)
	}
	writeJSON(w, map[string]any{"node": node, "disks": seen, "observed_at": r.now().Unix()})
}

// tokenPermissions answers "what can this token do" — LIVE, audit token.
//
// 🔴 This is the route that explains ON SCREEN, by measurement rather than by
// promise, what the dashboard can and cannot see of the hypervisor. At first it
// documented the ABSENCE of privilege on /storage; once the ACL was granted it
// documents its PRESENCE — and the text on screen switches by itself, because
// what changed was the measurement, not the code.
//
// `storage_visivel` comes from pve.CanAuditDatastore, the SAME function the
// poller uses to stamp `datastore_audit`. A copy of the rule here would give two
// truths about the same question, and the second would age in silence — the
// defect credentialKey (handlers_nodes.go) has already cost this codebase.
func (r *Router) tokenPermissions(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}
	perms, err := cli.Permissions(req.Context())
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused the permissions: "+pveErrorDetail(err))
		return
	}
	storage := pve.CanAuditDatastore(perms)
	if perms == nil {
		perms = map[string]map[string]int{}
	}
	writeJSON(w, map[string]any{
		"permissions":     perms,
		"storage_visivel": storage,
		"observed_at":     r.now().Unix(),
	})
}

// guestSnapshots lists, creates and deletes snapshots — LIVE, with the NODE's
// token.
//
// 🔴 POST and DELETE only answer 200 after WaitTask (status "stopped" AND
// exitstatus "OK"). PVE's POST returns 200 as soon as the TASK IS CREATED:
// passing that 200 straight through would be the screen saying "snapshot ready"
// for a snapshot that may not even have started.
func (r *Router) guestSnapshots(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	switch req.Method {
	case http.MethodGet, http.MethodPost, http.MethodDelete:
	default:
		writeErr(w, 405, "method not allowed")
		return
	}
	q := req.URL.Query()
	id := strings.TrimSpace(q.Get("node"))
	name := strings.TrimSpace(q.Get("name"))

	// The name is rejected BEFORE anything else: it comes from the screen and it
	// is what builds the resource path on the hypervisor. Validating after
	// dialling out would spend a connection just to find out the screen sent
	// garbage.
	if req.Method != http.MethodGet {
		if err := pve.ValidSnapshotName(name); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}

	no, found := findNode(inv, id)
	if !found {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	if no.Kind != inventory.NodeKindGuest || no.VMID <= 0 {
		writeErr(w, 400, "node "+id+" is not a guest of the hypervisor")
		return
	}

	cli, ok := r.clientForOp(w, opGuest, no)
	if !ok {
		return
	}
	kind, host := kindAndHost(no)
	ctx := req.Context()

	if req.Method == http.MethodGet {
		snaps, err := cli.SnapshotList(ctx, host, no.VMID, kind)
		if err != nil {
			writeErr(w, pveErrorCode(err), "hypervisor refused the snapshot list: "+pveErrorDetail(err))
			return
		}
		// PVE includes the pseudo-entry "current" ("You are here!"), which is no
		// snapshot at all. Filtering here, rather than on screen, saves every
		// consumer from having to remember it.
		seen := make([]pve.Snapshot, 0, len(snaps))
		for _, s := range snaps {
			if s.Name != "current" {
				seen = append(seen, s)
			}
		}
		writeJSON(w, map[string]any{"node": id, "snapshots": seen, "observed_at": r.now().Unix()})
		return
	}

	var upid string
	var err error
	action := "create"
	if req.Method == http.MethodPost {
		upid, err = cli.SnapshotCreate(ctx, host, no.VMID, kind, name, strings.TrimSpace(q.Get("desc")))
	} else {
		action = "delete"
		upid, err = cli.SnapshotDelete(ctx, host, no.VMID, kind, name)
	}
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused snapshot "+action+": "+pveErrorDetail(err))
		return
	}

	if _, err := waitTask(ctx, cli, host, upid); err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.snapshot",
			"node="+id+" action="+action+" name="+name+" upid="+upid+" status=failed")
		// The exitstatus goes in EXPLICITLY: when Status is 0, the pve.Error
		// formatter takes the Err branch and the Body — which carries the real
		// reason — never shows up on its own.
		writeErr(w, 502, "task "+upid+" did not finish cleanly: "+pveErrorDetail(err))
		return
	}

	// A mutation on the hypervisor with NO trail is a mutation nobody can
	// reconstruct afterwards.
	r.auditEvent(req, auth.UserFrom(req), "pve.snapshot",
		"node="+id+" action="+action+" name="+name+" upid="+upid+" status=ok")
	writeJSON(w, map[string]any{"node": id, "action": action, "name": name, "upid": upid, "status": "ok"})
}

// guestRollback returns the guest to a snapshot's state — LIVE, NODE token.
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
func (r *Router) guestRollback(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	q := req.URL.Query()
	id := strings.TrimSpace(q.Get("node"))
	name := strings.TrimSpace(q.Get("name"))

	// The name is rejected BEFORE anything else: it comes from the screen and it
	// chooses WHICH state the guest will take on. Validating after dialling out
	// would spend a connection just to find out the screen sent garbage.
	if err := pve.ValidSnapshotName(name); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	no, found := findNode(inv, id)
	if !found {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	if no.Kind != inventory.NodeKindGuest || no.VMID <= 0 {
		writeErr(w, 400, "node "+id+" is not a guest of the hypervisor")
		return
	}

	cli, ok := r.clientForOp(w, opGuest, no)
	if !ok {
		return
	}
	kind, host := kindAndHost(no)
	ctx := req.Context()

	upid, err := cli.SnapshotRollback(ctx, host, no.VMID, kind, name)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused the rollback: "+pveErrorDetail(err))
		return
	}
	if _, err := waitTask(ctx, cli, host, upid); err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.snapshot",
			"node="+id+" action=rollback name="+name+" upid="+upid+" status=failed")
		writeErr(w, 502, "task "+upid+" did not finish cleanly: "+pveErrorDetail(err))
		return
	}

	// The trail is mandatory: this is the most destructive mutation this
	// dashboard fires, and what it erases has no second copy — the pool is
	// single-disk.
	r.auditEvent(req, auth.UserFrom(req), "pve.snapshot",
		"node="+id+" action=rollback name="+name+" upid="+upid+" status=ok")
	writeJSON(w, map[string]any{"node": id, "action": "rollback", "name": name, "upid": upid, "status": "ok"})
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
// works on this host, measured — not before. TestNoRouteOffersSuspend is
// the pin.
