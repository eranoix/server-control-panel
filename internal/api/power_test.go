package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"server-control-panel/internal/inventory"
)

func powerRouter(t *testing.T, seen *[]string) *Router {
	t.Helper()
	fake := &fakePVE{seen: seen}
	r, st := newProxmoxRouter(t, defaultVault(), fake)
	if err := st.Replace(func(iv *inventory.Inventory) {
		iv.Hypervisor.Node = "pve"
		iv.Nodes = []inventory.Node{
			{ID: "node/pve", Name: "pve", Kind: inventory.NodeKindHost, Transport: inventory.TransportPVEAPI},
			{ID: "lxc/201", Name: "games", VMID: 201, Kind: inventory.NodeKindGuest,
				Transport: inventory.TransportPVEAPI, Status: inventory.Observe("running", testNow)},
			{ID: "lxc/204", Name: "lab", VMID: 204, Kind: inventory.NodeKindGuest,
				Transport: inventory.TransportPVEAPI, Status: inventory.Observe("stopped", testNow)},
		}
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

func calledPower(seen []string) bool {
	for _, m := range seen {
		if strings.HasPrefix(m, "pve.node-power:") {
			return true
		}
	}
	return false
}

func TestPowerNeverFiresOnGET(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodHead} {
		var seen []string
		r := powerRouter(t, &seen)
		w := httptest.NewRecorder()
		r.handleProxmox(w, req(t, method, "/api/proxmox/power?command=shutdown", ""))
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, expected 405", method, w.Code)
		}
		if calledPower(seen) {
			t.Fatalf("%s REACHED the hypervisor — the route fired for a method it should not have", method)
		}
	}
}

func TestPowerRejectsCommandOutsideAllowlist(t *testing.T) {
	for _, cmd := range []string{"", "poweroff", "halt", "REBOOT", "reboot ", "stop",
		"reboot;shutdown", "../../access/users"} {
		var seen []string
		r := powerRouter(t, &seen)
		w := httptest.NewRecorder()
		r.handleProxmox(w, req(t, http.MethodPost, "/api/proxmox/power?command="+url.QueryEscape(cmd), ""))
		if w.Code != http.StatusBadRequest {
			t.Errorf("command=%q = %d, expected 400", cmd, w.Code)
		}
		if calledPower(seen) {
			t.Fatalf("command=%q REACHED the hypervisor", cmd)
		}
	}
}

func TestPowerAuditsFirstAndReturnsAffected(t *testing.T) {
	var seen []string
	r := powerRouter(t, &seen)
	w := httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodPost, "/api/proxmox/power?command=reboot", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("= %d: %s", w.Code, w.Body.String())
	}
	if !calledPower(seen) {
		t.Fatal("the command did NOT reach the hypervisor")
	}
	body := w.Body.String()
	if !strings.Contains(body, "lxc/201") {
		t.Error("the guest that is ON did not appear among the affected ones")
	}
	if strings.Contains(body, "lxc/204") {
		t.Error("a STOPPED guest appeared among the affected ones — it is already off")
	}
	if !strings.Contains(body, "loses contact") {
		t.Error("the response does not warn that the panel loses the hypervisor")
	}
}
