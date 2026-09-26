package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
)

// routerDeManutencao builds the router with a fake vault and a fake hypervisor.
// NO test in this file clones or backs up for real: the fake COUNTS the calls,
// so we can assert "nothing was fired" without ever firing anything.
func routerDeManutencao(t *testing.T, nos []inventory.Node, comPainel bool) (*Router, *[]string) {
	t.Helper()
	var seen []string
	r, _ := novoRouterDeNos(t, nos)
	dados := map[string]string{
		"pve_token_audit":     "lab@pve!audit=a",
		"pve_token_node_apps": "lab@pve!node-apps=s",
		"pve_token_node_lab":  "lab@pve!node-lab=s",
	}
	if comPainel {
		dados["pve_token_painel"] = "lab@pve!painel=p"
	}
	r.nodeVaultFn = func() (nodeVault, error) { return &cofreFalso{seen: &seen, dados: dados}, nil }
	r.pveDial = func(valor string) (hypervisorOps, error) {
		return &pveFalso{seen: &seen, tokenUsado: valor}, nil
	}
	return r, &seen
}

func guestDeTeste() []inventory.Node {
	n := noDeTeste("lxc/207", "apps", 207, agoraDeTeste)
	n.Status.Value = "running"
	return []inventory.Node{n}
}

// 🔴 TestCloneUsaOTokenDoPainelNaoODoNo — the wrong credential here gives no
// pretty error: it gives a 403 from the hypervisor, which reaches the operator
// as "it failed" without saying the problem is privilege. The node token does
// NOT have VM.Allocate on /vms/<newid> nor Datastore.AllocateSpace, and that was
// MEASURED on /access/permissions before this code was written.
func TestCloneUsaOTokenDoPainelNaoODoNo(t *testing.T) {
	r, seen := routerDeManutencao(t, guestDeTeste(), true)
	w, out := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"nome":"apps-copia","snapshot":"base"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	usados := strings.Join(*seen, " ")
	if !strings.Contains(usados, "token=lab@pve!painel") {
		t.Errorf("the clone did not use the panel's credential — calls: %v", *seen)
	}
	if strings.Contains(usados, "token=lab@pve!node-apps") {
		t.Errorf("the clone used the NODE's token, which has no VM.Allocate — calls: %v", *seen)
	}
	if !strings.Contains(usados, "pve.clone:lxc/207->991:apps-copia:snap=base") {
		t.Errorf("the clone did not reach the hypervisor with source, destination and name: %v", *seen)
	}
	// 🔴 "aceita", NEVER "ok": the clone was not waited for, and saying "ok" would
	// be asserting a result nobody checked.
	if out["status"] != "aceita" {
		t.Errorf("status = %v, want \"aceita\" — the task was not awaited", out["status"])
	}
	if out["upid"] == "" || out["upid"] == nil {
		t.Error("response without UPID — without it the operator has no way to follow along")
	}
}

// TestCloneSemCredencialDoPainelExplicaOMotivo — a button that fails without
// saying why is the difference between fixing it in a minute and believing the
// panel is broken.
func TestCloneSemCredencialDoPainelExplicaOMotivo(t *testing.T) {
	r, seen := routerDeManutencao(t, guestDeTeste(), false)
	w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"snapshot":"base"}`)
	if w.Code != 409 {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pve_token_painel") {
		t.Errorf("the body does not name the missing key: %s", w.Body)
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.clone") {
			t.Errorf("cloned even without a credential: %v", *seen)
		}
	}
}

// TestCloneGETNaoDisparaNada — the GET exists to ASK for the next free id. If it
// cloned, merely loading the screen would create guests.
func TestCloneGETNaoDisparaNada(t *testing.T) {
	r, seen := routerDeManutencao(t, guestDeTeste(), true)
	w, out := chama(t, r, http.MethodGet, "/api/nodes/lxc/207/clone", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if out["next_id"] == nil {
		t.Error("GET did not return next_id — the operator would have to TYPE the id")
	}
	if out["sugestao"] != "apps-copia" {
		t.Errorf("suggestion = %v, want apps-copia", out["sugestao"])
	}
	if out["ligado"] != true {
		t.Errorf("ligado = %v — the crash-consistent-copy warning depends on this", out["ligado"])
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.clone") {
			t.Fatalf("🔴 o GET CLONOU: %v", *seen)
		}
	}
}

// 🔴 TestCloneDeContainerLigadoExigeSnapshot — the rule only the LIVE PROOF
// revealed. The hypervisor refuses a full clone of a RUNNING container without
// `snapname`, with "Full clone of a running container is only possible from a
// snapshot", and that sentence turns up in no grep of the PVE source on this
// machine. Reading code was no substitute for running it.
//
// The refusal happens HERE, and not on the hypervisor, for a reason: the message
// from here says what to DO (take a snapshot in this tab, or shut the guest down).
func TestCloneDeContainerLigadoExigeSnapshot(t *testing.T) {
	r, seen := routerDeManutencao(t, guestDeTeste(), true)
	w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"nome":"x"}`)
	if w.Code != 409 {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "snapshot") {
		t.Errorf("the body does not say what to do: %s", w.Body)
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.clone") {
			t.Errorf("it actually cloned — the hypervisor would refuse and the operator would get its error: %v", *seen)
		}
	}

	// With a snapshot it goes through — and the snapname REACHES the hypervisor.
	r2, seen2 := routerDeManutencao(t, guestDeTeste(), true)
	w2, _ := chama(t, r2, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"nome":"x","snapshot":"antes-do-upgrade"}`)
	if w2.Code != 200 {
		t.Fatalf("with snapshot: status = %d: %s", w2.Code, w2.Body)
	}
	if !strings.Contains(strings.Join(*seen2, " "), "snap=antes-do-upgrade") {
		t.Errorf("the snapname did not reach the hypervisor: %v", *seen2)
	}
}

// TestCloneDeGuestDESLIGADONaoPedeSnapshot — the mirror image: the requirement
// belongs to the RUNNING container. Demanding a snapshot of a stopped guest
// would be inventing a restriction the hypervisor does not have.
func TestCloneDeGuestDESLIGADONaoPedeSnapshot(t *testing.T) {
	n := noDeTeste("lxc/207", "apps", 207, agoraDeTeste)
	n.Status.Value = "stopped"
	r, seen := routerDeManutencao(t, []inventory.Node{n}, true)
	w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"nome":"x"}`)
	if w.Code != 200 {
		t.Fatalf("guest stopped: status = %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(strings.Join(*seen, " "), "pve.clone") {
		t.Errorf("did not clone: %v", *seen)
	}
}

// TestCloneGETAvisaQuePrecisaDeSnapshot — the screen has to know BEFORE the
// click, otherwise the operator fills in the form only to be handed an error.
func TestCloneGETAvisaQuePrecisaDeSnapshot(t *testing.T) {
	r, _ := routerDeManutencao(t, guestDeTeste(), true)
	_, out := chama(t, r, http.MethodGet, "/api/nodes/lxc/207/clone", "")
	if out["precisa_snapshot"] != true {
		t.Errorf("precisa_snapshot = %v for a running CT, want true", out["precisa_snapshot"])
	}
	if out["snapshots"] == nil {
		t.Error("without the snapshot list the screen has nothing to offer")
	}
}

// TestCloneRecusaSemDestino — with no novo_id, nothing is fired. The id comes
// from the hypervisor through the GET; accepting its absence would be an
// invitation to make a number up.
func TestCloneRecusaSemDestino(t *testing.T) {
	r, seen := routerDeManutencao(t, guestDeTeste(), true)
	w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"nome":"x"}`)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.clone") {
			t.Errorf("cloned without a destination: %v", *seen)
		}
	}
}

// TestBackupPassaModoECompressaoEUsaOPainel
func TestBackupPassaModoECompressaoEUsaOPainel(t *testing.T) {
	r, seen := routerDeManutencao(t, guestDeTeste(), true)
	w, out := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/backup", `{"storage":"pbs","modo":"snapshot","compress":"zstd"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	usados := strings.Join(*seen, " ")
	if !strings.Contains(usados, "pve.vzdump:207:pbs:snapshot:zstd") {
		t.Errorf("vzdump did not receive the parameters: %v", *seen)
	}
	if !strings.Contains(usados, "token=lab@pve!painel") {
		t.Errorf("backup did not use the panel's credential: %v", *seen)
	}
	if out["status"] != "aceita" {
		t.Errorf("status = %v, want \"aceita\"", out["status"])
	}
}

// TestBackupTemPadraoQueNaoSurpreende — with no mode and no compression, the
// default is `snapshot`+`zstd`: it does not stop the guest and it is the format
// this lab already used. A `stop` default would power off the machine of someone
// who only wanted a copy.
func TestBackupTemPadraoQueNaoSurpreende(t *testing.T) {
	r, seen := routerDeManutencao(t, guestDeTeste(), true)
	if w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/backup", `{"storage":"pbs"}`); w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(strings.Join(*seen, " "), "pve.vzdump:207:pbs:snapshot:zstd") {
		t.Errorf("wrong default: %v", *seen)
	}
}

// TestBackupRecusaSemStorage — WHERE the copy goes cannot be guessed.
func TestBackupRecusaSemStorage(t *testing.T) {
	r, seen := routerDeManutencao(t, guestDeTeste(), true)
	w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/backup", `{}`)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.vzdump") {
			t.Errorf("backed up without a storage: %v", *seen)
		}
	}
}

// 🔴 TestManutencaoRecusaHostEExplicaDiferente — host and external node give the
// SAME refusal today if nobody takes care, and the operator's next action
// differs: one is not clonable by nature, the other is not even on this
// hypervisor.
func TestManutencaoRecusaHostEExplicaDiferente(t *testing.T) {
	host := noDeTeste("node/pve", "pve", 0, agoraDeTeste)
	host.Kind = inventory.NodeKindHost
	r, seen := routerDeManutencao(t, []inventory.Node{host}, true)
	for _, rota := range []string{"/api/nodes/node/pve/clone", "/api/nodes/node/pve/backup"} {
		w, _ := chama(t, r, http.MethodPost, rota, `{"storage":"pbs","novo_id":991}`)
		if w.Code != 400 {
			t.Errorf("%s: status = %d, want 400", rota, w.Code)
		}
		if !strings.Contains(w.Body.String(), "hypervisor is not a guest") {
			t.Errorf("%s: reason too generic: %s", rota, w.Body)
		}
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.clone") || strings.HasPrefix(c, "pve.vzdump") {
			t.Errorf("fired on the host: %v", *seen)
		}
	}
}

// TestRebootPassaPeloWaitTask — reboot is the ONLY one of the three that waits
// for the task, and it has to keep waiting: a guest that ignores the request
// from the inside stays up, and only the task knows that.
func TestRebootPassaPeloWaitTask(t *testing.T) {
	r, seen := routerDeManutencao(t, guestDeTeste(), true)
	w, out := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"reboot"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	usados := strings.Join(*seen, " ")
	if !strings.Contains(usados, "pve.reboot:lxc/207") {
		t.Errorf("reboot did not reach the hypervisor: %v", *seen)
	}
	if !strings.Contains(usados, "pve.wait") {
		t.Errorf("reboot did NOT wait for the task — a guest that ignores ACPI would be reported as rebooted: %v", *seen)
	}
	// Reboot uses the NODE's token, not the panel's: it is the node acting on itself.
	if !strings.Contains(usados, "token=lab@pve!node-apps") {
		t.Errorf("reboot did not use the node's token: %v", *seen)
	}
	if out["action"] != "reboot" || out["status"] != "ok" {
		t.Errorf("response = %v", out)
	}
}

// 🔴 TestRebootForaDaAllowlistNaoDiscara — whatever is NOT one of the four
// actions never reaches the hypervisor.
//
// Normalisation (trim + lowercase) happens BEFORE the allowlist, and that is
// deliberate: it loosens nothing, because what comes next is an EXACT match
// against four strings. "reboot " becomes "reboot" and passes; "reboot;stop"
// becomes nothing at all and dies. The pin asserts both halves, otherwise
// somebody "hardens" it by removing the TrimSpace and breaks the screen without
// gaining any security.
func TestRebootForaDaAllowlistNaoDiscara(t *testing.T) {
	t.Run("normalization accepted, and that is on purpose", func(t *testing.T) {
		for _, acao := range []string{"reboot", " reboot ", "REBOOT", "Reboot", "reboot\n", "\treboot"} {
			r, seen := routerDeManutencao(t, guestDeTeste(), true)
			corpo, _ := json.Marshal(map[string]string{"action": acao})
			w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/power", string(corpo))
			if w.Code != 200 {
				t.Errorf("action %q: status = %d, want 200 — normalization exists for this", acao, w.Code)
			}
			if !strings.Contains(strings.Join(*seen, " "), "pve.reboot") {
				t.Errorf("action %q: did not restart", acao)
			}
		}
	})
	t.Run("the rest dies BEFORE dialing", func(t *testing.T) {
		for _, acao := range []string{"reboot;stop", "restart", "../../status/stop", "reboot stop", ""} {
			r, seen := routerDeManutencao(t, guestDeTeste(), true)
			corpo, _ := json.Marshal(map[string]string{"action": acao})
			w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/power", string(corpo))
			if w.Code != 400 {
				t.Errorf("action %q: status = %d, want 400", acao, w.Code)
			}
			for _, c := range *seen {
				if strings.HasPrefix(c, "pve.") {
					t.Errorf("action %q reached the hypervisor: %v", acao, *seen)
				}
			}
		}
	})
}

// ── a NOTA: o que a caixa faz ───────────────────────────────────────────────

func routerDeNota(t *testing.T, nos []inventory.Node, texto string) (*Router, *[]string) {
	t.Helper()
	var seen []string
	r, _ := novoRouterDeNos(t, nos)
	r.nodeVaultFn = func() (nodeVault, error) {
		return &cofreFalso{seen: &seen, dados: map[string]string{
			"pve_token_painel": "lab@pve!painel=p",
			"pve_token_audit":  "lab@pve!audit=a",
		}}, nil
	}
	r.pveDial = func(valor string) (hypervisorOps, error) {
		return &pveFalso{seen: &seen, tokenUsado: valor, descricao: texto}, nil
	}
	return r, &seen
}

// TestNotaDoGuestVemDoPVE — the note is READ from the hypervisor, never invented
// here. A second description written by the panel would become a second truth,
// and the two would diverge on the first day somebody edited the one in Proxmox.
func TestNotaDoGuestVemDoPVE(t *testing.T) {
	const texto = "## apps — as aplicações\n\n**O que faz:** hoje, nada."
	r, seen := routerDeNota(t, guestDeTeste(), texto)
	w, out := chama(t, r, http.MethodGet, "/api/nodes/lxc/207/nota", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if out["markdown"] != texto {
		t.Errorf("markdown = %q", out["markdown"])
	}
	if out["origem"] != "pve-notes" {
		t.Errorf("origem = %v, want pve-notes — the screen needs to know WHERE it came from", out["origem"])
	}
	if !strings.Contains(strings.Join(*seen, " "), "pve.descricao:lxc/207") {
		t.Errorf("did not read the right guest's description: %v", *seen)
	}
}

// 🔴 TestNotaDoHOSTLeAConfigDoNO — the hypervisor has a note of its OWN, at
// /nodes/<node>/config. If the handler sent the host's vmid (which is 0 in the
// inventory, but the path is a different one), it would read the config of a
// non-existent guest and the hypervisor's screen would stay mute forever.
func TestNotaDoHOSTLeAConfigDoNO(t *testing.T) {
	// 🔴 The host is born with VMID 999 ON PURPOSE, not 0.
	//
	// The first version of this pin used 0 — which is what the real inventory
	// holds — and for that very reason it COULD NOT FAIL: removing the guard from
	// the handler left everything green. A pin that does not bite is documentation
	// disguised as proof, and that disease is what let the screen open black in
	// production.
	//
	// With 999, the pin asserts what the handler really does: for the HOST it
	// ignores the inventory's vmid and asks for the NODE's config.
	host := noDeTeste("node/pve", "pve", 999, agoraDeTeste)
	host.Kind = inventory.NodeKindHost
	r, seen := routerDeNota(t, []inventory.Node{host}, "# pve — o servidor de casa")
	w, out := chama(t, r, http.MethodGet, "/api/nodes/node/pve/nota", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(out["markdown"].(string), "servidor de casa") {
		t.Errorf("markdown = %v", out["markdown"])
	}
	// What matters is the ZERO VMID: it is what tells the client to read
	// /nodes/<node>/config instead of /nodes/<node>/<tipo>/<vmid>/config. The
	// type prefix is irrelevant here and pinning it would be a brittle pin.
	usados := strings.Join(*seen, " ")
	if !strings.Contains(usados, "/0 ") && !strings.HasSuffix(usados, "/0") {
		t.Errorf("did not ask for the NODE's config (vmid 0): %v", *seen)
	}
	if strings.Contains(usados, "/999") {
		t.Errorf("🔴 asked for the config of GUEST 999 — the hypervisor is not a guest, and its note"+
			"mora em /nodes/<node>/config: %v", *seen)
	}
}

// 🔴 TestNotaVaziaNaoEErro — a guest with no note is a guest nobody described.
// The screen needs to say where to write one, and to do that it needs to tell
// "empty" apart from "could not be read". They are opposite problems: one asks
// somebody to write, the other asks somebody to fix access.
func TestNotaVaziaNaoEErro(t *testing.T) {
	r, _ := routerDeNota(t, guestDeTeste(), "   \n  ")
	w, out := chama(t, r, http.MethodGet, "/api/nodes/lxc/207/nota", "")
	if w.Code != 200 {
		t.Fatalf("empty note turned into an error: status = %d", w.Code)
	}
	if out["origem"] != "vazia" {
		t.Errorf("origem = %v, want \"vazia\"", out["origem"])
	}
}

// TestNotaDeNoEXTERNONaoProcuraNoPVE — `canario` is no hypervisor's guest.
// Looking its config up on the PVE would give a 500, and the screen would show a
// failure where what there is is an absent source.
func TestNotaDeNoEXTERNONaoProcuraNoPVE(t *testing.T) {
	ext := noDeTeste("canario", "canario", 0, agoraDeTeste)
	ext.Kind = "externo"
	ext.Transport = "agente"
	r, seen := routerDeNota(t, []inventory.Node{ext}, "nao deveria ser lida")
	w, out := chama(t, r, http.MethodGet, "/api/nodes/canario/nota", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if out["origem"] != "fora-do-pve" {
		t.Errorf("origem = %v, want fora-do-pve", out["origem"])
	}
	if out["motivo"] == nil || out["motivo"] == "" {
		t.Error("without a reason — the screen would show a blank with no explanation")
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.descricao") {
			t.Errorf("went to the PVE for the config of a node that isn't its guest: %v", *seen)
		}
	}
}

// TestNotaDeNoQueSumiuDoHipervisor — the inventory lists the node for one more
// cycle after it is deleted. Passing the PVE's 500 straight through hands the
// operator a Perl message about a file path; what they need to know is that the
// box does not exist any more. Found by the live proof, with the clone from the
// previous proof.
func TestNotaDeNoQueSumiuDoHipervisor(t *testing.T) {
	r, _ := routerDeNota(t, guestDeTeste(), "")
	r.pveDial = func(valor string) (hypervisorOps, error) {
		return &pveFalso{erroVerbo: errors.New(
			"pve /api2/json/nodes/pve/lxc/207/config: erro_hipervisor (500) " +
				`{"data":null,"message":"Configuration file 'nodes/pve/lxc/207.conf' does not exist\n"}`)}, nil
	}
	w, out := chama(t, r, http.MethodGet, "/api/nodes/lxc/207/nota", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if out["origem"] != "inexistente" {
		t.Errorf("origem = %v, want \"inexistente\"", out["origem"])
	}
	motivo, _ := out["motivo"].(string)
	if !strings.Contains(motivo, "no longer exists") {
		t.Errorf("motivo = %q — does not say what happened", motivo)
	}
	if strings.Contains(motivo, "Configuration file") || strings.Contains(motivo, ".conf") {
		t.Errorf("🔴 the Perl message leaked to the screen: %q", motivo)
	}
}

// 🔴 THE NEGATIVE CONTROL: a hypervisor that is down must NOT turn into "it is
// gone". They are opposite problems — one asks somebody to take the node off the
// list, the other asks for help. Confusing them is worse than saying nothing.
func TestNotaComHipervisorForaDoArContinuaSendoErro(t *testing.T) {
	r, _ := routerDeNota(t, guestDeTeste(), "")
	r.pveDial = func(valor string) (hypervisorOps, error) {
		return &pveFalso{erroVerbo: errors.New("pve: dial tcp 198.51.100.20:8006: i/o timeout")}, nil
	}
	w, out := chama(t, r, http.MethodGet, "/api/nodes/lxc/207/nota", "")
	if w.Code == 200 {
		t.Fatalf("unreachable hypervisor responded 200 with origem=%v — failure turned into 'vanished'", out["origem"])
	}
}

// 🔴 TestGravarNotaUsaACredencialDeESCRITA — reading and writing the note use
// DIFFERENT credentials, and the pin exists so nobody "simplifies" that away.
//
// Reading is auditing and goes through the read credential. Writing changes the
// guest's configuration (VM.Config.Options) and goes through the panel's. Using
// the write credential to read would give the screen's most common path a
// privilege it does not need — and an unnecessary privilege is a privilege used
// by mistake one day.
func TestGravarNotaUsaACredencialDeESCRITA(t *testing.T) {
	const texto = "## apps\n\n**O que faz:** serve as aplicações."
	r, seen := routerDeNota(t, guestDeTeste(), "")
	corpo, _ := json.Marshal(map[string]string{"markdown": texto})
	w, out := chama(t, r, http.MethodPut, "/api/nodes/lxc/207/nota", string(corpo))
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	usados := strings.Join(*seen, " ")
	if !strings.Contains(usados, "pve.set-descricao:lxc/207") {
		t.Errorf("did not write: %v", *seen)
	}
	if !strings.Contains(usados, "token=lab@pve!painel") {
		t.Errorf("wrote with the wrong credential: %v", *seen)
	}
	if out["origem"] != "pve-notes" {
		t.Errorf("origem = %v", out["origem"])
	}

	// 🔴 THE DIFFERENCE BETWEEN READING AND WRITING ONLY SHOWS UP WITHOUT THE
	// PANEL TOKEN.
	//
	// The first version of this pin claimed that READING never uses the panel's
	// credential — and it failed, rightly: `segredoDeLeituraDoHipervisor()`
	// PREFERS the panel token when it exists in the vault. In this house, today,
	// reading and writing use the same token, and claiming otherwise was
	// describing a design that does not exist.
	//
	// What actually tells the two paths apart, and is what matters to the
	// operator: WITHOUT the panel token, READING keeps working (it falls back to
	// the audit one) and WRITING refuses, saying which key is missing. A screen
	// that loses the node's explanation along with permission to edit would be
	// worse than one that loses only the editing.
	r2, seen2 := routerDeNota(t, guestDeTeste(), texto)
	r2.nodeVaultFn = func() (nodeVault, error) {
		return &cofreFalso{seen: seen2, dados: map[string]string{"pve_token_audit": "lab@pve!audit=a"}}, nil
	}
	if w, out := chama(t, r2, http.MethodGet, "/api/nodes/lxc/207/nota", ""); w.Code != 200 || out["markdown"] != texto {
		t.Errorf("without the panel token, READING stopped working: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(strings.Join(*seen2, " "), "token=lab@pve!audit") {
		t.Errorf("READING did not fall back to the audit credential: %v", *seen2)
	}
	w2, _ := chama(t, r2, http.MethodPut, "/api/nodes/lxc/207/nota", string(corpo))
	if w2.Code != 409 {
		t.Errorf("without the panel token, WRITING returned %d — want 409", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "pve_token_painel") {
		t.Errorf("the refusal does not name the missing key: %s", w2.Body)
	}
}

// TestGravarNotaRecusaTextoGigante — a note is for READING. The cap refuses
// HERE, in the operator's own language, instead of shipping the text to the
// hypervisor and translating its refusal afterwards.
func TestGravarNotaRecusaTextoGigante(t *testing.T) {
	r, seen := routerDeNota(t, guestDeTeste(), "")
	corpo, _ := json.Marshal(map[string]string{"markdown": strings.Repeat("a", pve.TamanhoMaximoDaNota+1)})
	w, _ := chama(t, r, http.MethodPut, "/api/nodes/lxc/207/nota", string(corpo))
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.set-descricao") {
			t.Errorf("sent the giant text to the hypervisor: %v", *seen)
		}
	}
}

// 🔴 TestTrilhaDaNotaNaoGuardaOCONTEUDO — the note describes the house:
// addresses, what each box holds, what happens if it falls over. The audit trail
// is read by more people and kept for longer than the note itself. It records
// the SIZE and the TARGET; the content stays where it lives.
func TestTrilhaDaNotaNaoGuardaOCONTEUDO(t *testing.T) {
	const segredo = "o cofre fica atras do quadro na sala"
	r, _ := routerDeNota(t, guestDeTeste(), "")
	al, err := auth.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	r.audit = al
	corpo, _ := json.Marshal(map[string]string{"markdown": "## x\n\n" + segredo})
	if w, _ := chama(t, r, http.MethodPut, "/api/nodes/lxc/207/nota", string(corpo)); w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var linhas []string
	for _, e := range al.Tail(50) {
		linhas = append(linhas, e.Action+" "+e.Target)
	}
	junto := strings.Join(linhas, " | ")
	if strings.Contains(junto, segredo) {
		t.Errorf("🔴 the note's content leaked into the trail: %s", junto)
	}
	if !strings.Contains(junto, "pve.nota") || !strings.Contains(junto, "bytes=") {
		t.Errorf("the trail did not record the write: %s", junto)
	}
}
