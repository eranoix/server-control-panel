package api

import (
	"net/http"
	"strconv"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
)

type pvxOp int

const (
	opHypervisorRead pvxOp = iota
	opGuest
)

func (r *Router) keyForOp(op pvxOp, no inventory.Node) string {
	if op == opGuest {
		return nodeKey(no)
	}
	return r.hypervisorReadSecret()
}

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
		r.hypervisorPower(w, req, inv)
	case "rrd":
		if !getOnly() {
			return
		}
		r.timeSeries(w, req, inv)
	case "system":
		if !getOnly() {
			return
		}
		r.nodeSystem(w, req, inv)
	case "packages":
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
		if !getOnly() {
			return
		}
		r.backupFreshness(w, req, inv)
	case "zfs/topology":
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
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.guestRollback(w, req, inv)
	default:
		writeErr(w, 404, "unknown route: /api/proxmox/"+rest)
	}
}

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

func hypervisorNodeOr409(w http.ResponseWriter, inv inventory.Inventory) (string, bool) {
	name := hypervisorNameInInventory(inv)
	if name == "" {
		writeErr(w, 409, "hypervisor not discovered yet — the poller has not completed a tick")
		return "", false
	}
	return name, true
}

func (r *Router) hypervisorHealth(w http.ResponseWriter, inv inventory.Inventory) {
	writeJSON(w, map[string]any{
		"hypervisor":  inventory.ViewHypervisor(inv.Hypervisor, r.ttl(), r.now()),
		"ttl_seconds": int64(r.ttl().Seconds()),
		"observed_at": r.now().Unix(),
	})
}

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

	user := auth.UserFrom(req)
	r.auditEvent(req, user, "pve.node-power", string(cmd)+" "+node)

	upid, err := cli.NodePower(req.Context(), node, cmd)
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused "+string(cmd)+": "+pveErrorDetail(err))
		return
	}

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
		"guests_affected": affected,
		"observed_at":     r.now().Unix(),
		"warning":         "the command was accepted by the hypervisor; from here on the panel loses contact with it",
	})
}

func (r *Router) timeSeries(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	q := req.URL.Query()
	window, ok := pve.ValidRRDWindow(q.Get("window"))
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
		writeJSON(w, map[string]any{"target": node, "scope": "node", "window": string(window),
			"points": pts, "observed_at": r.now().Unix()})
		return
	}

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
	writeJSON(w, map[string]any{"target": no.ID, "scope": "guest", "window": string(window),
		"points": pts, "observed_at": r.now().Unix()})
}

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
		out["network_error"] = pveErrorDetail(err)
	} else {
		out["network"] = v
	}
	if v, err := cli.DNS(ctx, node); err != nil {
		out["dns_error"] = pveErrorDetail(err)
	} else {
		out["dns"] = v
	}
	if v, err := cli.Time(ctx, node); err != nil {
		out["time_error"] = pveErrorDetail(err)
	} else {
		out["time"] = v
	}
	if v, err := cli.Certificates(ctx, node); err != nil {
		out["certificates_error"] = pveErrorDetail(err)
	} else {
		out["certificates"] = v
	}
	writeJSON(w, out)
}

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
	writeJSON(w, map[string]any{"node": node, "packages": ps, "observed_at": r.now().Unix()})
}

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
	writeJSON(w, map[string]any{"node": node, "lines": lines, "limit": limit,
		"observed_at": r.now().Unix()})
}

func (r *Router) backupFreshness(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	node, ok := hypervisorNodeOr409(w, inv)
	if !ok {
		return
	}
	cli, ok := r.clientForOp(w, opHypervisorRead, inventory.Node{})
	if !ok {
		return
	}
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
	type agenda struct{ state, schedule string }
	jobs := map[string]agenda{}
	if js, err := cli.BackupJobs(req.Context()); err == nil {
		for _, j := range js {
			if j.Storage == "" {
				continue
			}
			if j.IsScheduled() {
				jobs[j.Storage] = agenda{"active", j.Schedule}
			} else {
				jobs[j.Storage] = agenda{"disarmed", j.Schedule}
			}
		}
	}
	scheduleState := func(st string) (string, string) {
		if a, ok := jobs[st]; ok {
			return a.state, a.schedule
		}
		return "outside-pve", ""
	}

	output := make([]pve.BackupFreshness, 0, len(targets))
	for _, st := range targets {
		f, err := cli.DatastoreBackups(req.Context(), node, st)
		if err != nil {
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
			tops = append(tops, pve.ZPoolTopology{
				Name: p.Name, State: "UNKNOWN",
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

type diskView struct {
	pve.Disk
	WearoutPct *float64 `json:"wearout_pct"`
}

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
		"storage_visible": storage,
		"observed_at":     r.now().Unix(),
	})
}

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
		writeErr(w, 502, "task "+upid+" did not finish cleanly: "+pveErrorDetail(err))
		return
	}

	r.auditEvent(req, auth.UserFrom(req), "pve.snapshot",
		"node="+id+" action="+action+" name="+name+" upid="+upid+" status=ok")
	writeJSON(w, map[string]any{"node": id, "action": action, "name": name, "upid": upid, "status": "ok"})
}

func (r *Router) guestRollback(w http.ResponseWriter, req *http.Request, inv inventory.Inventory) {
	q := req.URL.Query()
	id := strings.TrimSpace(q.Get("node"))
	name := strings.TrimSpace(q.Get("name"))

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
