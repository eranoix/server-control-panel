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

// handlers_manutencao.go — clone a guest, and ask for a copy to be stored.
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 THESE TWO DO NOT WAIT FOR THE TASK TO FINISH, AND THE RESPONSE SAYS SO.
//
// The rest of the dashboard follows the rule to the letter: PVE's POST returns
// 200 with a UPID as soon as the task is CREATED, so start/stop/reboot only
// answer success after WaitTask. That does not work here: a full 14 GB clone
// takes minutes, and holding the HTTP request open for minutes is the same as
// having no button at all — the browser gives up first.
//
// The way out is NOT to fake success. It is for the response to tell the truth
// that exists on this side: the task was ACCEPTED by the hypervisor, and this
// is its UPID. The dashboard opens that task's log right away, so the operator
// follows the real outcome instead of reading an "ok" nobody checked.
//
// 🔴 AND BOTH USE THE DASHBOARD'S TOKEN, NOT THE NODE'S. Measured, not assumed:
// cloning requires VM.Allocate on /vms/<newid> — a path that does not exist yet
// and where the node's token has no ACL at all — and storing a copy requires
// Datastore.AllocateSpace on /storage/<name>, likewise. The node's token would
// give 403 on both, and that 403 would reach the operator as a "mysterious
// failure".
// ────────────────────────────────────────────────────────────────────────────

// clienteDeManutencao returns the client carrying the DASHBOARD's token and,
// with it, the reason in plain language when it cannot — because "it did not
// work" with no reason is the difference between the operator fixing it in a
// minute and raising a ticket with themselves.
func (r *Router) clienteDeManutencao(w http.ResponseWriter) (hypervisorOps, bool) {
	valor, estado := r.tokenDoCofre(pveSecretPainel)
	switch estado {
	case vaultInalcancavel:
		writeErr(w, 503, "vault unreachable — the panel credential could not be read")
		return nil, false
	case vaultAusente:
		writeErr(w, 409, "clone and store copy use the panel credential ("+pveSecretPainel+
			"), which is not in the vault — the node token will not do because it has neither VM.Allocate nor Datastore.AllocateSpace")
		return nil, false
	}
	cli, err := r.dial(valor)
	if err != nil {
		writeErr(w, 503, "hypervisor not configured: "+err.Error())
		return nil, false
	}
	return cli, true
}

// guestDoInventario resolves the id from the screen into a real guest. The host
// and an external node land here with DIFFERENT messages, because the
// operator's actions differ: one is not clonable by nature, the other does not
// even belong to this hypervisor.
func (r *Router) guestDoInventario(w http.ResponseWriter, st *inventory.Store, id string) (inventory.Node, bool) {
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return inventory.Node{}, false
	}
	no, ok := achaNo(inv, id)
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

// cloneDoNo: GET prepares, POST executes.
//
// The GET exists so that nobody has to TYPE the destination VMID. What knows
// which id is free is the hypervisor (/cluster/nextid); guessing from the list
// on screen races anything else that allocates in the meantime.
func (r *Router) cloneDoNo(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	no, ok := r.guestDoInventario(w, st, id)
	if !ok {
		return
	}
	cli, ok := r.clienteDeManutencao(w)
	if !ok {
		return
	}
	tipo, node := tipoEHost(no)
	ctx := req.Context()

	if req.Method == http.MethodGet {
		prox, err := cli.NextID(ctx)
		if err != nil {
			writeErr(w, codigoDoErroPVE(err), "could not ask for the next free id: "+err.Error())
			return
		}
		ligado := ehGuestLigado(no)

		// 🔴 A RUNNING CONTAINER ONLY CLONES FROM A SNAPSHOT — A MEASURED RULE.
		//
		// It appears in no grep of this machine's PVE source: what revealed it
		// was the live run, with the hypervisor refusing the clone and saying
		// "Full clone of a running container is only possible from a snapshot".
		// It does not hold for a VM (qemu) — PVE clones a running VM.
		//
		// The screen needs to know BEFORE the operator clicks, otherwise they
		// fill in the form and take an error from the hypervisor in the face.
		precisaSnap := ligado && tipo == "lxc"
		snaps := []string{}
		if precisaSnap {
			if lista, err := cli.SnapshotList(ctx, node, no.VMID, tipo); err == nil {
				for _, sn := range lista {
					// "current" is PVE's "You are here!" pseudo-entry, not a
					// real snapshot.
					if sn.Name != "" && sn.Name != "current" {
						snaps = append(snaps, sn.Name)
					}
				}
			}
		}
		writeJSON(w, map[string]any{
			"precisa_snapshot": precisaSnap,
			"snapshots":        snaps,
			"origem":           id,
			"origem_nome":      no.Name,
			"tipo":             tipo,
			"next_id":          prox,
			"sugestao":         sugestaoDeNome(no.Name),
			// The warning travels with the data because it depends on the guest's
			// STATE, and the screen must not recompute a consistency rule on its own.
			"ligado": ehGuestLigado(no),
		})
		return
	}

	var body struct {
		NovoID   int    `json:"novo_id"`
		Nome     string `json:"nome"`
		Snapshot string `json:"snapshot"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	if body.NovoID <= 0 {
		writeErr(w, 400, "novo_id missing — ask for the next free one with GET first")
		return
	}

	// AUDIT BEFORE firing: the clone can take minutes and the response may never
	// reach the operator (tab closed, network dropping). The record of what was
	// ASKED FOR must not depend on what was ANSWERED.
	r.auditEvent(req, auth.UserFrom(req), "pve.clone",
		fmt.Sprintf("origem=%s destino=%d nome=%s snap=%s status=pedido", id, body.NovoID, body.Nome, body.Snapshot))

	// Refuse HERE, with the message that resolves it, instead of letting the
	// hypervisor return its own. The difference is that this one says what to DO.
	if ehGuestLigado(no) && tipo == "lxc" && strings.TrimSpace(body.Snapshot) == "" {
		writeErr(w, 409, "this container is RUNNING: the hypervisor only clones a running container from a snapshot. "+
			"Take a snapshot in this same tab and pick it, or shut the guest down first.")
		return
	}
	upid, err := cli.Clone(ctx, node, no.VMID, tipo, body.NovoID, strings.TrimSpace(body.Nome), strings.TrimSpace(body.Snapshot))
	if err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.clone",
			fmt.Sprintf("origem=%s destino=%d status=recusado erro=%s", id, body.NovoID, err.Error()))
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the clone: "+err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "pve.clone",
		fmt.Sprintf("origem=%s destino=%d upid=%s status=aceita", id, body.NovoID, upid))

	// 🔴 "accepted", never "ok". See the file header.
	writeJSON(w, map[string]any{
		"origem": id, "destino": body.NovoID, "upid": upid, "status": "aceita",
		"node": node,
	})
}

// backupDoNo tells the hypervisor to store a copy NOW.
func (r *Router) backupDoNo(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	no, ok := r.guestDoInventario(w, st, id)
	if !ok {
		return
	}
	var body struct {
		Storage  string `json:"storage"`
		Modo     string `json:"modo"`
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
	if body.Modo == "" {
		body.Modo = "snapshot"
	}
	if body.Compress == "" {
		body.Compress = "zstd"
	}

	cli, ok := r.clienteDeManutencao(w)
	if !ok {
		return
	}
	_, node := tipoEHost(no)

	r.auditEvent(req, auth.UserFrom(req), "pve.backup",
		fmt.Sprintf("no=%s storage=%s modo=%s status=pedido", id, body.Storage, body.Modo))

	upid, err := cli.VZDump(req.Context(), node, no.VMID, body.Storage, body.Modo, body.Compress)
	if err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.backup",
			fmt.Sprintf("node=%s storage=%s status=refused error=%s", id, body.Storage, err.Error()))
		writeErr(w, codigoDoErroPVE(err), "hypervisor refused the backup: "+err.Error())
		return
	}
	r.auditEvent(req, auth.UserFrom(req), "pve.backup",
		fmt.Sprintf("no=%s storage=%s upid=%s status=aceita", id, body.Storage, upid))

	writeJSON(w, map[string]any{
		"node_id": id, "storage": body.Storage, "modo": body.Modo,
		"upid": upid, "status": "aceita", "node": node,
	})
}

// esperaTarefa is the ONLY place in the package that interprets the end of a
// hypervisor task. It exists because of a defect measured in the live run: the
// clone transferred 768 MB, created the guest and finished on `WARNINGS: 1` —
// and the dashboard would have told the operator it had failed.
//
// The rule, now in a single place:
//
//	exitstatus "OK"           → success, no message
//	exitstatus "WARNINGS: n"  → success WITH a message, and the message GOES to
//	                            the screen
//	anything else             → failure
//
// Three callers had the same `if err := WaitTask(...); err != nil` line, and
// three copies would diverge the day someone touched one of them.
func esperaTarefa(ctx context.Context, cli hypervisorOps, node, upid string) (avisos string, err error) {
	if e := cli.WaitTask(ctx, node, upid); e != nil {
		if a, ok := pve.TarefaComAvisos(e); ok {
			return a.Exit, nil
		}
		return "", e
	}
	return "", nil
}

// notaDoNo delivers the explanation of what that node DOES.
//
// 🔴 THE ORIGIN TRAVELS WITH THE TEXT. The screen has to be able to say WHERE
// it came from — "this is the Proxmox note" is different from "this is
// something the dashboard wrote". Without the origin, an empty note and an
// unreachable note become the same thing on screen, and they are opposite
// problems: one asks for somebody to write it, the other for somebody to fix
// the access.
func (r *Router) notaDoNo(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
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

	// An external node is no hypervisor's guest: there is no PVE config to read.
	// That is an absence of SOURCE, not a failure — and the screen says something
	// different.
	if no.Transport != inventory.TransportPVEAPI {
		writeJSON(w, map[string]any{
			"node": id, "markdown": "", "origem": "fora-do-pve",
			"motivo": "este nó não é guest deste hipervisor — a nota do PVE não se aplica a ele",
		})
		return
	}

	// 🔴 READING ASKS FOR THE READ CREDENTIAL; WRITING REQUIRES THE DASHBOARD'S.
	//
	// Today the two coincide, and the comment says so rather than pretending to
	// a prettier design: `segredoDeLeituraDoHipervisor()` PREFERS the dashboard's
	// token when it exists in the vault. The separation matters on the day it
	// does NOT exist — and that day is precisely the day of a revocation or of a
	// half-configured vault.
	//
	// On that day: READING keeps working (it falls back to the audit token) and
	// WRITING refuses, naming the key that is missing. A screen that loses the
	// node's EXPLANATION along with permission to edit it would be worse than one
	// that loses only the edit — and that is the difference the pin asserts.
	var cli hypervisorOps
	var ok2 bool
	if req.Method == http.MethodPut {
		cli, ok2 = r.clienteDeManutencao(w)
		if !ok2 {
			return
		}
	} else {
		valor, estado := r.tokenDoCofre(r.segredoDeLeituraDoHipervisor())
		if estado != vaultOK {
			writeErr(w, 503, "hypervisor read credential: "+estado)
			return
		}
		var err error
		cli, err = r.dial(valor)
		if err != nil {
			writeErr(w, 503, "hypervisor not configured: "+err.Error())
			return
		}
	}

	tipo, node := tipoEHost(no)
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
		if len(body.Markdown) > pve.TamanhoMaximoDaNota {
			writeErr(w, 400, fmt.Sprintf("note is %d bytes — the cap is %d, because a note is meant to be READ",
				len(body.Markdown), pve.TamanhoMaximoDaNota))
			return
		}
		// AUDIT the size and the target, never the CONTENT: the note describes the
		// house (addresses, what each box holds), and the audit trail is read by
		// more people and kept for longer than the note itself.
		r.auditEvent(req, auth.UserFrom(req), "pve.nota",
			fmt.Sprintf("no=%s bytes=%d status=pedido", id, len(body.Markdown)))
		if err := cli.SetDescricao(req.Context(), node, vmid, tipo, body.Markdown); err != nil {
			r.auditEvent(req, auth.UserFrom(req), "pve.nota",
				fmt.Sprintf("node=%s status=refused error=%s", id, err.Error()))
			writeErr(w, codigoDoErroPVE(err), "hypervisor refused to write the note: "+err.Error())
			return
		}
		r.auditEvent(req, auth.UserFrom(req), "pve.nota",
			fmt.Sprintf("no=%s bytes=%d status=ok", id, len(body.Markdown)))
		origem := "pve-notes"
		if strings.TrimSpace(body.Markdown) == "" {
			origem = "vazia"
		}
		writeJSON(w, map[string]any{"node": id, "markdown": body.Markdown, "origem": origem, "status": "ok"})
		return
	}

	txt, err := cli.Descricao(req.Context(), node, vmid, tipo)
	if err != nil {
		// 🔴 A NODE THAT DISAPPEARED FROM THE HYPERVISOR GETS ITS OWN ANSWER.
		//
		// Found by the live run: the inventory keeps the node for one cycle after
		// it stops existing (someone deleted the guest from the Proxmox screen),
		// and PVE answers 500 with "Configuration file
		// 'nodes/pve/lxc/101.conf' does not exist". Passing that through gives
		// the operator a message in Perl about a file path, when what happened is
		// simple and it is what they need to know: that box does not exist any
		// more.
		//
		// The match is by text, and that is deliberately fragile IN ONE DIRECTION
		// ONLY: if PVE's message changes, the raw error comes back — worse, but
		// not wrong. The opposite (assuming "it is gone" for any failure) would
		// hide a hypervisor that is down.
		if strings.Contains(err.Error(), "does not exist") {
			writeJSON(w, map[string]any{
				"node": id, "markdown": "", "origem": "inexistente",
				"motivo": "this node no longer exists on the hypervisor — the panel still lists it because " +
					"the inventory is refreshed once per cycle, and the next cycle removes it",
			})
			return
		}
		writeErr(w, codigoDoErroPVE(err), "could not read the note on the hypervisor: "+err.Error())
		return
	}
	origem := "pve-notes"
	if strings.TrimSpace(txt) == "" {
		// 🔴 EMPTY IS NOT AN ERROR. A guest with no note is a guest nobody has
		// described, and the screen has to say WHERE to write one instead of
		// showing a blank.
		origem = "vazia"
	}
	writeJSON(w, map[string]any{"node": id, "markdown": txt, "origem": origem})
}

// sugestaoDeNome builds the clone's name from the source's name, already inside
// the hostname rules — because the operator should not discover that "cópia de
// lab" is invalid only after typing it.
func sugestaoDeNome(origem string) string {
	limpo := make([]rune, 0, len(origem)+6)
	for _, r := range strings.ToLower(origem) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			limpo = append(limpo, r)
		case r == '-' && len(limpo) > 0:
			limpo = append(limpo, r)
		}
	}
	base := strings.Trim(string(limpo), "-")
	if base == "" {
		base = "clone"
	}
	s := base + "-copia"
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.Trim(s, "-")
}

// ehGuestLigado answers the question that changes the confirmation's WARNING:
// copying a running guest produces a crash-consistent copy, as if the cable had
// been pulled midway. That is not a reason to forbid it — it is a reason to WARN.
func ehGuestLigado(n inventory.Node) bool {
	st := strings.ToLower(strings.TrimSpace(n.Status.Value))
	return st == "running" || st == "online"
}
