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

		needsSnap := running && kind == "lxc"
		snaps := []string{}
		if needsSnap {
			if list, err := cli.SnapshotList(ctx, node, no.VMID, kind); err == nil {
				for _, sn := range list {
					if sn.Name != "" && sn.Name != "current" {
						snaps = append(snaps, sn.Name)
					}
				}
			}
		}
		writeJSON(w, map[string]any{
			"needs_snapshot": needsSnap,
			"snapshots":      snaps,
			"origin":         id,
			"origin_name":    no.Name,
			"type":           kind,
			"next_id":        nextID,
			"suggestion":     suggestName(no.Name),
			"on":             isGuestRunning(no),
		})
		return
	}

	var body struct {
		NewID    int    `json:"new_id"`
		Name     string `json:"name"`
		Snapshot string `json:"snapshot"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.NewID <= 0 {
		writeErr(w, 400, "new_id missing — ask for the next free one with GET first")
		return
	}

	r.auditEvent(req, auth.UserFrom(req), "pve.clone",
		fmt.Sprintf("source=%s target=%d name=%s snap=%s status=requested", id, body.NewID, body.Name, body.Snapshot))

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

	writeJSON(w, map[string]any{
		"origin": id, "destination": body.NewID, "upid": upid, "status": "accepted",
		"node": node,
	})
}

func (r *Router) nodeBackup(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	no, ok := r.inventoryGuest(w, st, id)
	if !ok {
		return
	}
	var body struct {
		Storage  string `json:"storage"`
		Mode     string `json:"mode"`
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
		"node_id": id, "storage": body.Storage, "mode": body.Mode,
		"upid": upid, "status": "accepted", "node": node,
	})
}

func waitTask(ctx context.Context, cli hypervisorOps, node, upid string) (warnings string, err error) {
	if e := cli.WaitTask(ctx, node, upid); e != nil {
		if a, ok := pve.AsTaskWarning(e); ok {
			return a.Exit, nil
		}
		return "", e
	}
	return "", nil
}

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

	if no.Transport != inventory.TransportPVEAPI {
		writeJSON(w, map[string]any{
			"node": id, "markdown": "", "origin": "outside-pve",
			"reason": "this node is not a guest of this hypervisor, so the PVE note does not apply to it",
		})
		return
	}

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
		r.auditEvent(req, auth.UserFrom(req), "pve.note",
			fmt.Sprintf("node=%s bytes=%d status=requested", id, len(body.Markdown)))
		if err := cli.SetDescription(req.Context(), node, vmid, kind, body.Markdown); err != nil {
			r.auditEvent(req, auth.UserFrom(req), "pve.note",
				fmt.Sprintf("node=%s status=refused error=%s", id, err.Error()))
			writeErr(w, pveErrorCode(err), "hypervisor refused to write the note: "+err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "pve.note",
			fmt.Sprintf("node=%s bytes=%d status=ok", id, len(body.Markdown)))
		origin := "pve-notes"
		if strings.TrimSpace(body.Markdown) == "" {
			origin = "empty"
		}
		writeJSON(w, map[string]any{"node": id, "markdown": body.Markdown, "origin": origin, "status": "ok"})
		return
	}

	txt, err := cli.Description(req.Context(), node, vmid, kind)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			writeJSON(w, map[string]any{
				"node": id, "markdown": "", "origin": "nonexistent",
				"reason": "this node no longer exists on the hypervisor — the panel still lists it because " +
					"the inventory is refreshed once per cycle, and the next cycle removes it",
			})
			return
		}
		writeErr(w, pveErrorCode(err), "could not read the note on the hypervisor: "+err.Error())
		return
	}
	origin := "pve-notes"
	if strings.TrimSpace(txt) == "" {
		origin = "empty"
	}
	writeJSON(w, map[string]any{"node": id, "markdown": txt, "origin": origin})
}

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

func isGuestRunning(n inventory.Node) bool {
	st := strings.ToLower(strings.TrimSpace(n.Status.Value))
	return st == "running" || st == "online"
}
