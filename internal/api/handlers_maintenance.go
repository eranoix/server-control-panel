package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
)

// Clone a guest and store a backup copy.
//
// Neither handler waits for the task to finish: a full clone takes minutes, longer
// than a browser holds a request. The response reports the task as ACCEPTED with
// its UPID (never "ok"), and the dashboard follows the task log for the outcome.
//
// Both use the dashboard's token, not the node's: cloning needs VM.Allocate on
// /vms/<newid> and backup needs Datastore.AllocateSpace on /storage/<name>, and
// the node token has neither (it would get 403).

// maintenanceClient returns a client carrying the dashboard's token, or writes
// a plain-language reason when it cannot.
func (r *Router) maintenanceClient(w http.ResponseWriter) (hypervisorOps, bool) {
	value, state := r.vaultToken(pveSecretPanel)
	switch state {
	case vaultUnreachable:
		writeErr(w, 503, "vault unreachable — the panel credential could not be read")
		return nil, false
	case vaultMissing:
		writeErr(w, 409, "clone and store copy use the panel credential ("+pveSecretPanel+
			"), which is not in the vault — the node token will not do because it has neither VM.Allocate nor Datastore.AllocateSpace")
		return nil, false
	}
	cli, err := r.dial(value)
	if err != nil {
		writeErr(w, 503, "hypervisor not configured: "+err.Error())
		return nil, false
	}
	return cli, true
}

// inventoryGuest resolves the id from the screen into a real guest. The host
// and an external node land here with DIFFERENT messages, because the
// operator's actions differ: one is not clonable by nature, the other does not
// even belong to this hypervisor.
func (r *Router) inventoryGuest(w http.ResponseWriter, st *inventory.Store, id string) (inventory.Node, bool) {
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return inventory.Node{}, false
	}
	no, ok := findNode(inv, id)
	if !ok {
		writeErr(w, 404, "node not found: "+id)
		return inventory.Node{}, false
	}
	if no.Kind == inventory.NodeKindHost {
		writeErr(w, 400, "the hypervisor is not a guest — there is nothing here to clone or store")
		return inventory.Node{}, false
	}
	if no.Kind != inventory.NodeKindGuest || no.VMID <= 0 {
		writeErr(w, 400, "node "+id+" is not a guest of this hypervisor")
		return inventory.Node{}, false
	}
	return no, true
}

// nodeClone: GET prepares, POST executes.
//
// The GET exists so that nobody has to TYPE the destination VMID. What knows
// which id is free is the hypervisor (/cluster/nextid); guessing from the list
// on screen races anything else that allocates in the meantime.
func (r *Router) nodeClone(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	no, ok := r.inventoryGuest(w, st, id)
	if !ok {
		return
	}
	cli, ok := r.maintenanceClient(w)
	if !ok {
		return
	}
	kind, node := kindAndHost(no)
	ctx := req.Context()

	if req.Method == http.MethodGet {
		nextID, err := cli.NextID(ctx)
		if err != nil {
			writeErr(w, pveErrorCode(err), "could not ask for the next free id: "+err.Error())
			return
		}
		running := isGuestRunning(no)

		// PVE only clones a running container (not a VM) from a snapshot, so the
		// screen needs to know before the operator submits the form.
		needsSnap := running && kind == "lxc"
		snaps := []string{}
		if needsSnap {
			if list, err := cli.SnapshotList(ctx, node, no.VMID, kind); err == nil {
				for _, sn := range list {
					// "current" is PVE's "You are here!" pseudo-entry, not a
					// real snapshot.
					if sn.Name != "" && sn.Name != "current" {
						snaps = append(snaps, sn.Name)
					}
				}
			}
		}
		writeJSON(w, map[string]any{
			"precisa_snapshot": needsSnap,
			"snapshots":        snaps,
			"origem":           id,
			"origem_nome":      no.Name,
			"tipo":             kind,
			"next_id":          nextID,
			"sugestao":         suggestName(no.Name),
			// The warning travels with the data because it depends on the guest's
			// STATE, and the screen must not recompute a consistency rule on its own.
			"ligado": isGuestRunning(no),
		})
		return
	}

	var body struct {
		NewID    int    `json:"novo_id"`
		Name     string `json:"nome"`
		Snapshot string `json:"snapshot"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.NewID <= 0 {
		writeErr(w, 400, "novo_id missing — ask for the next free one with GET first")
		return
	}

	// AUDIT BEFORE firing: the clone can take minutes and the response may never
	// reach the operator (tab closed, network dropping). The record of what was
	// ASKED FOR must not depend on what was ANSWERED.
	r.auditEvent(req, auth.UserFrom(req), "pve.clone",
		fmt.Sprintf("source=%s target=%d name=%s snap=%s status=requested", id, body.NewID, body.Name, body.Snapshot))

	// Refuse HERE, with the message that resolves it, instead of letting the
	// hypervisor return its own. The difference is that this one says what to DO.
	if isGuestRunning(no) && kind == "lxc" && strings.TrimSpace(body.Snapshot) == "" {
		writeErr(w, 409, "this container is RUNNING: the hypervisor only clones a running container from a snapshot. "+
			"Take a snapshot in this same tab and pick it, or shut the guest down first.")
		return
	}
	upid, err := cli.Clone(ctx, node, no.VMID, kind, body.NewID, strings.TrimSpace(body.Name), strings.TrimSpace(body.Snapshot))
	if err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.clone",
			fmt.Sprintf("source=%s target=%d status=refused error=%s", id, body.NewID, err.Error()))
		writeErr(w, pveErrorCode(err), "hypervisor refused the clone: "+err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "pve.clone",
		fmt.Sprintf("source=%s target=%d upid=%s status=accepted", id, body.NewID, upid))

	// Reported as accepted, never "ok": see the file header.
	writeJSON(w, map[string]any{
		"origem": id, "destino": body.NewID, "upid": upid, "status": "aceita",
		"node": node,
	})
}

// nodeBackup tells the hypervisor to store a copy NOW.
func (r *Router) nodeBackup(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	no, ok := r.inventoryGuest(w, st, id)
	if !ok {
		return
	}
	var body struct {
		Storage  string `json:"storage"`
		Mode     string `json:"modo"`
		Compress string `json:"compress"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if strings.TrimSpace(body.Storage) == "" {
		writeErr(w, 400, "storage missing — choose where the copy goes")
		return
	}
	// Defaults that hold no surprises: `snapshot` does not stop the guest, and
	// `zstd` is PVE 9's and what this lab's disabled job used.
	if body.Mode == "" {
		body.Mode = "snapshot"
	}
	if body.Compress == "" {
		body.Compress = "zstd"
	}

	cli, ok := r.maintenanceClient(w)
	if !ok {
		return
	}
	_, node := kindAndHost(no)

	r.auditEvent(req, auth.UserFrom(req), "pve.backup",
		fmt.Sprintf("node=%s storage=%s mode=%s status=requested", id, body.Storage, body.Mode))

	upid, err := cli.VZDump(req.Context(), node, no.VMID, body.Storage, body.Mode, body.Compress)
	if err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.backup",
			fmt.Sprintf("node=%s storage=%s status=refused error=%s", id, body.Storage, err.Error()))
		writeErr(w, pveErrorCode(err), "hypervisor refused the backup: "+err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "pve.backup",
		fmt.Sprintf("node=%s storage=%s upid=%s status=accepted", id, body.Storage, upid))

	writeJSON(w, map[string]any{
		"node_id": id, "storage": body.Storage, "modo": body.Mode,
		"upid": upid, "status": "aceita", "node": node,
	})
}

// waitTask is the only place in the package that interprets the end of a
// hypervisor task. A task ending in `WARNINGS: n` did succeed:
//
//	exitstatus "OK"           → success, no message
//	exitstatus "WARNINGS: n"  → success WITH a message, and the message GOES to
//	                            the screen
//	anything else             → failure
func waitTask(ctx context.Context, cli hypervisorOps, node, upid string) (warnings string, err error) {
	if e := cli.WaitTask(ctx, node, upid); e != nil {
		if a, ok := pve.AsTaskWarning(e); ok {
			return a.Exit, nil
		}
		return "", e
	}
	return "", nil
}

// nodeNote delivers the explanation of what that node DOES.
//
// The response carries the note's origin so the screen can tell an empty note
// (someone should write one) from an unreachable one (someone should fix access).
func (r *Router) nodeNote(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	no, ok := findNode(inv, id)
	if !ok {
		writeErr(w, 404, "node not found: "+id)
		return
	}

	// An external node has no PVE config to read: an absent source, not a failure.
	if no.Transport != inventory.TransportPVEAPI {
		writeJSON(w, map[string]any{
			"node": id, "markdown": "", "origem": "fora-do-pve",
			"motivo": "this node is not a guest of this hypervisor, so the PVE note does not apply to it",
		})
		return
	}

	// Reading uses the read credential; writing requires the dashboard's. When the
	// dashboard token is missing from the vault, reading still works (it falls back
	// to the audit token) and only writing is refused.
	var cli hypervisorOps
	var ok2 bool
	if req.Method == http.MethodPut {
		cli, ok2 = r.maintenanceClient(w)
		if !ok2 {
			return
		}
	} else {
		value, state := r.vaultToken(r.hypervisorReadSecret())
		if state != vaultOK {
			writeErr(w, 503, "hypervisor read credential: "+state)
			return
		}
		var err error
		cli, err = r.dial(value)
		if err != nil {
			writeErr(w, 503, "hypervisor not configured: "+err.Error())
			return
		}
	}

	kind, node := kindAndHost(no)
	vmid := no.VMID
	if no.Kind == inventory.NodeKindHost {
		// The hypervisor has its own note, in /nodes/<node>/config. `vmid <= 0`
		// is what tells the client to read the NODE's and not a guest's.
		vmid = 0
	}
	if req.Method == http.MethodPut {
		var body struct {
			Markdown string `json:"markdown"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		if len(body.Markdown) > pve.MaxNoteSize {
			writeErr(w, 400, fmt.Sprintf("note is %d bytes — the cap is %d, because a note is meant to be READ",
				len(body.Markdown), pve.MaxNoteSize))
			return
		}
		// Audit the size and target, never the content: the note may describe the
		// network, and the audit trail is kept longer and read more widely.
		r.auditEvent(req, auth.UserFrom(req), "pve.nota",
			fmt.Sprintf("node=%s bytes=%d status=requested", id, len(body.Markdown)))
		if err := cli.SetDescription(req.Context(), node, vmid, kind, body.Markdown); err != nil {
			r.auditEvent(req, auth.UserFrom(req), "pve.nota",
				fmt.Sprintf("node=%s status=refused error=%s", id, err.Error()))
			writeErr(w, pveErrorCode(err), "hypervisor refused to write the note: "+err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "pve.nota",
			fmt.Sprintf("node=%s bytes=%d status=ok", id, len(body.Markdown)))
		origin := "pve-notes"
		if strings.TrimSpace(body.Markdown) == "" {
			origin = "vazia"
		}
		writeJSON(w, map[string]any{"node": id, "markdown": body.Markdown, "origem": origin, "status": "ok"})
		return
	}

	txt, err := cli.Description(req.Context(), node, vmid, kind)
	if err != nil {
		// The inventory keeps a deleted guest for one cycle, and PVE then answers
		// 500 "Configuration file ... does not exist". Match that text only: if the
		// message changes the raw error comes back, which is worse but never hides
		// a hypervisor that is down.
		if strings.Contains(err.Error(), "does not exist") {
			writeJSON(w, map[string]any{
				"node": id, "markdown": "", "origem": "inexistente",
				"motivo": "this node no longer exists on the hypervisor — the panel still lists it because " +
					"the inventory is refreshed once per cycle, and the next cycle removes it",
			})
			return
		}
		writeErr(w, pveErrorCode(err), "could not read the note on the hypervisor: "+err.Error())
		return
	}
	origin := "pve-notes"
	if strings.TrimSpace(txt) == "" {
		// Empty is not an error: nobody has described this guest yet.
		origin = "vazia"
	}
	writeJSON(w, map[string]any{"node": id, "markdown": txt, "origem": origin})
}

// suggestName builds the clone's name from the source's name, already valid as
// a hostname.
func suggestName(origin string) string {
	clean := make([]rune, 0, len(origin)+6)
	for _, r := range strings.ToLower(origin) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			clean = append(clean, r)
		case r == '-' && len(clean) > 0:
			clean = append(clean, r)
		}
	}
	base := strings.Trim(string(clean), "-")
	if base == "" {
		base = "clone"
	}
	s := base + "-copy"
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.Trim(s, "-")
}

// isGuestRunning decides the confirmation warning: copying a running guest
// yields a crash-consistent copy, which is allowed but worth a warning.
func isGuestRunning(n inventory.Node) bool {
	st := strings.ToLower(strings.TrimSpace(n.Status.Value))
	return st == "running" || st == "online"
}
