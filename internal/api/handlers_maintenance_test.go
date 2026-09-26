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

// maintenanceRouter builds the router with a fake vault and a fake hypervisor.
// NO test in this file clones or backs up for real: the fake COUNTS the calls,
// so we can assert "nothing was fired" without ever firing anything.
func maintenanceRouter(t *testing.T, nodes []inventory.Node, withPanel bool) (*Router, *[]string) {
	t.Helper()
	var seen []string
	r, _ := newNodesRouter(t, nodes)
	data := map[string]string{
		"pve_token_audit":     "lab@pve!audit=a",
		"pve_token_node_apps": "lab@pve!node-apps=s",
		"pve_token_node_lab":  "lab@pve!node-lab=s",
	}
	if withPanel {
		data["pve_token_painel"] = "lab@pve!painel=p"
	}
	r.nodeVaultFn = func() (nodeVault, error) { return &fakeVault{seen: &seen, data: data}, nil }
	r.pveDial = func(value string) (hypervisorOps, error) {
		return &fakePVE{seen: &seen, usedToken: value}, nil
	}
	return r, &seen
}

func testGuest() []inventory.Node {
	n := testNode("lxc/207", "apps", 207, testNow)
	n.Status.Value = "running"
	return []inventory.Node{n}
}

// TestCloneUsesPanelTokenNotNodeToken: the node token has neither VM.Allocate on
// /vms/<newid> nor Datastore.AllocateSpace, so using it would yield an opaque 403.
func TestCloneUsesPanelTokenNotNodeToken(t *testing.T) {
	r, seen := maintenanceRouter(t, testGuest(), true)
	w, out := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"nome":"apps-copy","snapshot":"base"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	used := strings.Join(*seen, " ")
	if !strings.Contains(used, "token=lab@pve!painel") {
		t.Errorf("the clone did not use the panel's credential — calls: %v", *seen)
	}
	if strings.Contains(used, "token=lab@pve!node-apps") {
		t.Errorf("the clone used the NODE's token, which has no VM.Allocate — calls: %v", *seen)
	}
	if !strings.Contains(used, "pve.clone:lxc/207->991:apps-copy:snap=base") {
		t.Errorf("the clone did not reach the hypervisor with source, destination and name: %v", *seen)
	}
	// Accepted, never "ok": the clone was not waited for.
	if out["status"] != "aceita" {
		t.Errorf("status = %v, want \"aceita\" — the task was not awaited", out["status"])
	}
	if out["upid"] == "" || out["upid"] == nil {
		t.Error("response without UPID — without it the operator has no way to follow along")
	}
}

// TestCloneWithoutPanelCredentialExplainsWhy: the refusal names the missing key.
func TestCloneWithoutPanelCredentialExplainsWhy(t *testing.T) {
	r, seen := maintenanceRouter(t, testGuest(), false)
	w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"snapshot":"base"}`)
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

// TestCloneGETFiresNothing: the GET only asks for the next free id; loading the
// screen must never create guests.
func TestCloneGETFiresNothing(t *testing.T) {
	r, seen := maintenanceRouter(t, testGuest(), true)
	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/lxc/207/clone", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if out["next_id"] == nil {
		t.Error("GET did not return next_id — the operator would have to TYPE the id")
	}
	if out["sugestao"] != "apps-copy" {
		t.Errorf("suggestion = %v, want apps-copy", out["sugestao"])
	}
	if out["ligado"] != true {
		t.Errorf("ligado = %v — the crash-consistent-copy warning depends on this", out["ligado"])
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.clone") {
			t.Fatalf("the GET cloned: %v", *seen)
		}
	}
}

// TestCloneOfRunningContainerRequiresSnapshot: PVE refuses a full clone of a
// running container without `snapname`. The panel refuses first, with a message
// that says what to do (take a snapshot, or shut the guest down).
func TestCloneOfRunningContainerRequiresSnapshot(t *testing.T) {
	r, seen := maintenanceRouter(t, testGuest(), true)
	w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"nome":"x"}`)
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
	r2, seen2 := maintenanceRouter(t, testGuest(), true)
	w2, _ := callAPI(t, r2, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"nome":"x","snapshot":"before-upgrade"}`)
	if w2.Code != 200 {
		t.Fatalf("with snapshot: status = %d: %s", w2.Code, w2.Body)
	}
	if !strings.Contains(strings.Join(*seen2, " "), "snap=before-upgrade") {
		t.Errorf("the snapname did not reach the hypervisor: %v", *seen2)
	}
}

// TestCloneOfStoppedGuestNeedsNoSnapshot: the snapshot requirement only applies
// to a running container.
func TestCloneOfStoppedGuestNeedsNoSnapshot(t *testing.T) {
	n := testNode("lxc/207", "apps", 207, testNow)
	n.Status.Value = "stopped"
	r, seen := maintenanceRouter(t, []inventory.Node{n}, true)
	w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"novo_id":991,"nome":"x"}`)
	if w.Code != 200 {
		t.Fatalf("guest stopped: status = %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(strings.Join(*seen, " "), "pve.clone") {
		t.Errorf("did not clone: %v", *seen)
	}
}

// TestCloneGETWarnsSnapshotNeeded: the screen learns about the requirement before
// the form is submitted.
func TestCloneGETWarnsSnapshotNeeded(t *testing.T) {
	r, _ := maintenanceRouter(t, testGuest(), true)
	_, out := callAPI(t, r, http.MethodGet, "/api/nodes/lxc/207/clone", "")
	if out["precisa_snapshot"] != true {
		t.Errorf("precisa_snapshot = %v for a running CT, want true", out["precisa_snapshot"])
	}
	if out["snapshots"] == nil {
		t.Error("without the snapshot list the screen has nothing to offer")
	}
}

// TestCloneRejectsWithoutDestination: without a target id (from the GET), nothing
// is fired.
func TestCloneRejectsWithoutDestination(t *testing.T) {
	r, seen := maintenanceRouter(t, testGuest(), true)
	w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/clone", `{"nome":"x"}`)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.clone") {
			t.Errorf("cloned without a destination: %v", *seen)
		}
	}
}

func TestBackupPassesModeAndCompressionAndUsesPanel(t *testing.T) {
	r, seen := maintenanceRouter(t, testGuest(), true)
	w, out := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/backup", `{"storage":"pbs","modo":"snapshot","compress":"zstd"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	used := strings.Join(*seen, " ")
	if !strings.Contains(used, "pve.vzdump:207:pbs:snapshot:zstd") {
		t.Errorf("vzdump did not receive the parameters: %v", *seen)
	}
	if !strings.Contains(used, "token=lab@pve!painel") {
		t.Errorf("backup did not use the panel's credential: %v", *seen)
	}
	if out["status"] != "aceita" {
		t.Errorf("status = %v, want \"aceita\"", out["status"])
	}
}

// TestBackupDefaultsAreUnsurprising: the default is `snapshot`+`zstd`, which does
// not stop the guest (a `stop` default would power it off).
func TestBackupDefaultsAreUnsurprising(t *testing.T) {
	r, seen := maintenanceRouter(t, testGuest(), true)
	if w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/backup", `{"storage":"pbs"}`); w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(strings.Join(*seen, " "), "pve.vzdump:207:pbs:snapshot:zstd") {
		t.Errorf("wrong default: %v", *seen)
	}
}

// TestBackupRejectsWithoutStorage: where the copy goes cannot be guessed.
func TestBackupRejectsWithoutStorage(t *testing.T) {
	r, seen := maintenanceRouter(t, testGuest(), true)
	w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/backup", `{}`)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.vzdump") {
			t.Errorf("backed up without a storage: %v", *seen)
		}
	}
}

// TestMaintenanceRejectsHostWithDistinctReason: the host and an external node get
// different refusals because the operator's next step differs.
func TestMaintenanceRejectsHostWithDistinctReason(t *testing.T) {
	host := testNode("node/pve", "pve", 0, testNow)
	host.Kind = inventory.NodeKindHost
	r, seen := maintenanceRouter(t, []inventory.Node{host}, true)
	for _, route := range []string{"/api/nodes/node/pve/clone", "/api/nodes/node/pve/backup"} {
		w, _ := callAPI(t, r, http.MethodPost, route, `{"storage":"pbs","novo_id":991}`)
		if w.Code != 400 {
			t.Errorf("%s: status = %d, want 400", route, w.Code)
		}
		if !strings.Contains(w.Body.String(), "hypervisor is not a guest") {
			t.Errorf("%s: reason too generic: %s", route, w.Body)
		}
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.clone") || strings.HasPrefix(c, "pve.vzdump") {
			t.Errorf("fired on the host: %v", *seen)
		}
	}
}

// TestRebootGoesThroughWaitTask: reboot waits for the task, because a guest that
// ignores the request stays up and only the task result shows it.
func TestRebootGoesThroughWaitTask(t *testing.T) {
	r, seen := maintenanceRouter(t, testGuest(), true)
	w, out := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"reboot"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	used := strings.Join(*seen, " ")
	if !strings.Contains(used, "pve.reboot:lxc/207") {
		t.Errorf("reboot did not reach the hypervisor: %v", *seen)
	}
	if !strings.Contains(used, "pve.wait") {
		t.Errorf("reboot did NOT wait for the task — a guest that ignores ACPI would be reported as rebooted: %v", *seen)
	}
	// Reboot uses the NODE's token, not the panel's: it is the node acting on itself.
	if !strings.Contains(used, "token=lab@pve!node-apps") {
		t.Errorf("reboot did not use the node's token: %v", *seen)
	}
	if out["action"] != "reboot" || out["status"] != "ok" {
		t.Errorf("response = %v", out)
	}
}

// TestRebootOutsideAllowlistNeverDials: anything that is not one of the four
// actions never reaches the hypervisor. Normalization (trim + lowercase) runs
// before an exact match, so it loosens nothing; both halves are asserted.
func TestRebootOutsideAllowlistNeverDials(t *testing.T) {
	t.Run("normalization accepted, and that is on purpose", func(t *testing.T) {
		for _, action := range []string{"reboot", " reboot ", "REBOOT", "Reboot", "reboot\n", "\treboot"} {
			r, seen := maintenanceRouter(t, testGuest(), true)
			body, _ := json.Marshal(map[string]string{"action": action})
			w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/power", string(body))
			if w.Code != 200 {
				t.Errorf("action %q: status = %d, want 200 — normalization exists for this", action, w.Code)
			}
			if !strings.Contains(strings.Join(*seen, " "), "pve.reboot") {
				t.Errorf("action %q: did not restart", action)
			}
		}
	})
	t.Run("the rest dies BEFORE dialing", func(t *testing.T) {
		for _, action := range []string{"reboot;stop", "restart", "../../status/stop", "reboot stop", ""} {
			r, seen := maintenanceRouter(t, testGuest(), true)
			body, _ := json.Marshal(map[string]string{"action": action})
			w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/power", string(body))
			if w.Code != 400 {
				t.Errorf("action %q: status = %d, want 400", action, w.Code)
			}
			for _, c := range *seen {
				if strings.HasPrefix(c, "pve.") {
					t.Errorf("action %q reached the hypervisor: %v", action, *seen)
				}
			}
		}
	})
}

func noteRouter(t *testing.T, nodes []inventory.Node, text string) (*Router, *[]string) {
	t.Helper()
	var seen []string
	r, _ := newNodesRouter(t, nodes)
	r.nodeVaultFn = func() (nodeVault, error) {
		return &fakeVault{seen: &seen, data: map[string]string{
			"pve_token_painel": "lab@pve!painel=p",
			"pve_token_audit":  "lab@pve!audit=a",
		}}, nil
	}
	r.pveDial = func(value string) (hypervisorOps, error) {
		return &fakePVE{seen: &seen, usedToken: value, description: text}, nil
	}
	return r, &seen
}

// TestGuestNoteComesFromPVE: the note is read from the hypervisor, the single
// source of truth.
func TestGuestNoteComesFromPVE(t *testing.T) {
	const text = "## apps: the applications\n\n**What it does:** nothing yet."
	r, seen := noteRouter(t, testGuest(), text)
	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/lxc/207/nota", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if out["markdown"] != text {
		t.Errorf("markdown = %q", out["markdown"])
	}
	if out["origem"] != "pve-notes" {
		t.Errorf("origem = %v, want pve-notes — the screen needs to know WHERE it came from", out["origem"])
	}
	if !strings.Contains(strings.Join(*seen, " "), "pve.description:lxc/207") {
		t.Errorf("did not read the right guest's description: %v", *seen)
	}
}

// TestHOSTNoteReadsNODEConfig: the hypervisor's own note lives at
// /nodes/<node>/config, not under a guest vmid.
func TestHOSTNoteReadsNODEConfig(t *testing.T) {
	// VMID 999, not 0, so the test fails if the handler used the inventory vmid.
	host := testNode("node/pve", "pve", 999, testNow)
	host.Kind = inventory.NodeKindHost
	r, seen := noteRouter(t, []inventory.Node{host}, "# pve: the home server")
	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/node/pve/nota", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(out["markdown"].(string), "home server") {
		t.Errorf("markdown = %v", out["markdown"])
	}
	// vmid 0 tells the client to read /nodes/<node>/config.
	used := strings.Join(*seen, " ")
	if !strings.Contains(used, "/0 ") && !strings.HasSuffix(used, "/0") {
		t.Errorf("did not ask for the NODE's config (vmid 0): %v", *seen)
	}
	if strings.Contains(used, "/999") {
		t.Errorf("asked for the config of GUEST 999; the hypervisor is not a guest, and its note "+
			"lives at /nodes/<node>/config: %v", *seen)
	}
}

// TestEmptyNoteIsNotError: "empty" must stay distinct from "could not be read".
func TestEmptyNoteIsNotError(t *testing.T) {
	r, _ := noteRouter(t, testGuest(), "   \n  ")
	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/lxc/207/nota", "")
	if w.Code != 200 {
		t.Fatalf("empty note turned into an error: status = %d", w.Code)
	}
	if out["origem"] != "vazia" {
		t.Errorf("origem = %v, want \"vazia\"", out["origem"])
	}
}

// TestNoteOfEXTERNALNodeSkipsPVE: an external node has no PVE config, which is an
// absent source rather than a failure.
func TestNoteOfEXTERNALNodeSkipsPVE(t *testing.T) {
	ext := testNode("canary", "canary", 0, testNow)
	ext.Kind = inventory.NodeKindExternal
	ext.Transport = inventory.TransportAgent
	r, seen := noteRouter(t, []inventory.Node{ext}, "should not be read")
	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/canary/nota", "")
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
		if strings.HasPrefix(c, "pve.description") {
			t.Errorf("went to the PVE for the config of a node that isn't its guest: %v", *seen)
		}
	}
}

// TestNoteOfNodeGoneFromHypervisor: the inventory lists a deleted guest for one
// more cycle; the PVE 500 about a missing config file becomes a plain message.
func TestNoteOfNodeGoneFromHypervisor(t *testing.T) {
	r, _ := noteRouter(t, testGuest(), "")
	r.pveDial = func(value string) (hypervisorOps, error) {
		return &fakePVE{verbErr: errors.New(
			"pve /api2/json/nodes/pve/lxc/207/config: erro_hipervisor (500) " +
				`{"data":null,"message":"Configuration file 'nodes/pve/lxc/207.conf' does not exist\n"}`)}, nil
	}
	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/lxc/207/nota", "")
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if out["origem"] != "inexistente" {
		t.Errorf("origem = %v, want \"inexistente\"", out["origem"])
	}
	reason, _ := out["motivo"].(string)
	if !strings.Contains(reason, "no longer exists") {
		t.Errorf("motivo = %q — does not say what happened", reason)
	}
	if strings.Contains(reason, "Configuration file") || strings.Contains(reason, ".conf") {
		t.Errorf("the raw PVE message leaked to the screen: %q", reason)
	}
}

// TestNoteWithHypervisorDownIsStillError is the negative control: a hypervisor
// that is down must not be reported as a deleted guest.
func TestNoteWithHypervisorDownIsStillError(t *testing.T) {
	r, _ := noteRouter(t, testGuest(), "")
	r.pveDial = func(value string) (hypervisorOps, error) {
		return &fakePVE{verbErr: errors.New("pve: dial tcp 198.51.100.20:8006: i/o timeout")}, nil
	}
	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/lxc/207/nota", "")
	if w.Code == 200 {
		t.Fatalf("unreachable hypervisor responded 200 with origem=%v — failure turned into 'vanished'", out["origem"])
	}
}

// TestWriteNoteUsesWRITECredential: writing (VM.Config.Options) goes through the
// panel credential; reading goes through the read credential.
func TestWriteNoteUsesWRITECredential(t *testing.T) {
	const text = "## apps\n\n**What it does:** serves the applications."
	r, seen := noteRouter(t, testGuest(), "")
	body, _ := json.Marshal(map[string]string{"markdown": text})
	w, out := callAPI(t, r, http.MethodPut, "/api/nodes/lxc/207/nota", string(body))
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	used := strings.Join(*seen, " ")
	if !strings.Contains(used, "pve.set-description:lxc/207") {
		t.Errorf("did not write: %v", *seen)
	}
	if !strings.Contains(used, "token=lab@pve!painel") {
		t.Errorf("wrote with the wrong credential: %v", *seen)
	}
	if out["origem"] != "pve-notes" {
		t.Errorf("origem = %v", out["origem"])
	}

	// hypervisorReadSecret() prefers the panel token when present, so the paths
	// only differ without it: reading falls back to the audit token, writing is
	// refused and names the missing key.
	r2, seen2 := noteRouter(t, testGuest(), text)
	r2.nodeVaultFn = func() (nodeVault, error) {
		return &fakeVault{seen: seen2, data: map[string]string{"pve_token_audit": "lab@pve!audit=a"}}, nil
	}
	if w, out := callAPI(t, r2, http.MethodGet, "/api/nodes/lxc/207/nota", ""); w.Code != 200 || out["markdown"] != text {
		t.Errorf("without the panel token, READING stopped working: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(strings.Join(*seen2, " "), "token=lab@pve!audit") {
		t.Errorf("READING did not fall back to the audit credential: %v", *seen2)
	}
	w2, _ := callAPI(t, r2, http.MethodPut, "/api/nodes/lxc/207/nota", string(body))
	if w2.Code != 409 {
		t.Errorf("without the panel token, WRITING returned %d — want 409", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "pve_token_painel") {
		t.Errorf("the refusal does not name the missing key: %s", w2.Body)
	}
}

// TestWriteNoteRejectsHugeText: the size cap is enforced before calling the
// hypervisor.
func TestWriteNoteRejectsHugeText(t *testing.T) {
	r, seen := noteRouter(t, testGuest(), "")
	body, _ := json.Marshal(map[string]string{"markdown": strings.Repeat("a", pve.MaxNoteSize+1)})
	w, _ := callAPI(t, r, http.MethodPut, "/api/nodes/lxc/207/nota", string(body))
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	for _, c := range *seen {
		if strings.HasPrefix(c, "pve.set-description") {
			t.Errorf("sent the giant text to the hypervisor: %v", *seen)
		}
	}
}

// TestNoteTrailOmitsCONTENT: the audit trail records size and target, never the
// note's content.
func TestNoteTrailOmitsCONTENT(t *testing.T) {
	const secret = "the safe is behind the painting in the living room"
	r, _ := noteRouter(t, testGuest(), "")
	al, err := auth.NewAuditLog(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	r.audit = al
	body, _ := json.Marshal(map[string]string{"markdown": "## x\n\n" + secret})
	if w, _ := callAPI(t, r, http.MethodPut, "/api/nodes/lxc/207/nota", string(body)); w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	var lines []string
	for _, e := range al.Tail(50) {
		lines = append(lines, e.Action+" "+e.Target)
	}
	joined := strings.Join(lines, " | ")
	if strings.Contains(joined, secret) {
		t.Errorf("the note's content leaked into the trail: %s", joined)
	}
	if !strings.Contains(joined, "pve.nota") || !strings.Contains(joined, "bytes=") {
		t.Errorf("the trail did not record the write: %s", joined)
	}
}
