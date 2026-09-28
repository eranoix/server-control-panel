package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
)

type spyVault struct {
	data        map[string]string
	reads       []string
	unreachable bool
}

func (c *spyVault) Get(k string) (string, bool) {
	c.reads = append(c.reads, k)
	v, ok := c.data[k]
	return v, ok
}
func (c *spyVault) Delete(k string) error { delete(c.data, k); return nil }

func (c *spyVault) wasRead(key string) bool {
	for _, k := range c.reads {
		if k == key {
			return true
		}
	}
	return false
}
func (c *spyVault) readAnyWithPrefix(pref string) string {
	for _, k := range c.reads {
		if strings.HasPrefix(k, pref) {
			return k
		}
	}
	return ""
}

func testHypervisor(now int64) inventory.Hypervisor {
	return inventory.Hypervisor{
		Node:      "pve",
		Version:   inventory.Observe("pve-manager/9.2.2/abcdef", now),
		Uptime:    inventory.Observe(int64(123456), now),
		Load:      inventory.Observe([3]float64{1.14, 1.55, 1.70}, now),
		MemTotal:  inventory.Observe(int64(67200000000), now),
		MemUsed:   inventory.Observe(int64(40100000000), now),
		RootTotal: inventory.Observe(int64(100000000000), now),
		RootUsed:  inventory.Observe(int64(20000000000), now),
		KSMShared: inventory.Observe(int64(4096), now),
	}
}

func newProxmoxRouter(t *testing.T, vault *spyVault, fake *fakePVE) (*Router, *inventory.Store) {
	t.Helper()
	r, st := newNodesRouter(t, []inventory.Node{
		testNode("lxc/207", "apps", 207, testNow-10),
		testNode("lxc/204", "lab", 204, testNow-10),
	})
	if err := st.Replace(func(iv *inventory.Inventory) {
		iv.Hypervisor = testHypervisor(testNow - 30)
	}); err != nil {
		t.Fatal(err)
	}
	r.nodeVaultFn = func() (nodeVault, error) {
		if vault == nil || vault.unreachable {
			return nil, fmt.Errorf("vault is down")
		}
		return vault, nil
	}
	if fake != nil {
		r.pveDial = func(tokenValue string) (hypervisorOps, error) { return fake, nil }
	}
	return r, st
}

func defaultVault() *spyVault {
	return &spyVault{data: map[string]string{
		"pve_token_audit":     "panel@pve!audit=s3cr3t",
		"pve_token_admin":     "panel@pve!admin=s3cr3t",
		"pve_token_node_apps": "panel@pve!node-apps=s3cr3t",
		"pve_token_node_lab":  "panel@pve!node-lab=s3cr3t",
	}}
}

func callPVX(t *testing.T, r *Router, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.handleProxmox(w, req(t, method, path, body))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestHealthComesFromStoreWithoutCallingHypervisor(t *testing.T) {
	r, _ := newProxmoxRouter(t, defaultVault(), nil)
	r.pveDial = func(tokenValue string) (hypervisorOps, error) {
		t.Fatal("GET /api/proxmox dialed the hypervisor — health must come from the STORE")
		return nil, nil
	}

	w, out := callPVX(t, r, http.MethodGet, "/api/proxmox", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	h, _ := out["hypervisor"].(map[string]any)
	if h == nil {
		t.Fatalf("response without hypervisor: %s", w.Body)
	}
	age, hasAge := h["age_seconds"]
	if !hasAge {
		t.Fatal("hypervisor without age_seconds — the browser would go back to subtracting clocks")
	}
	if age.(float64) != 30 {
		t.Errorf("age_seconds = %v, want 30 (stamp of testNow-30)", age)
	}
	if _, ok := h["stale"]; !ok {
		t.Error("hypervisor without stale")
	}
	if h["node"] != "pve" {
		t.Errorf("node = %v", h["node"])
	}
	if _, ok := out["ttl_seconds"]; !ok {
		t.Error("response without ttl_seconds")
	}
}

func TestTasksUseAuditToken(t *testing.T) {
	routes := []string{
		"/api/proxmox/tasks?errors=1&limit=10",
		"/api/proxmox/tasks/log?upid=UPID:pve:1:2:3:vzsnapshot:204:panel@pve!node-lab:",
		"/api/proxmox/disks",
		"/api/proxmox/permissions",
	}
	for _, route := range routes {
		t.Run(route, func(t *testing.T) {
			vault := defaultVault()
			fake := &fakePVE{
				tasks:    []pve.Task{{UPID: "UPID:x", Type: "push_file", Status: "failed"}},
				logLines: []string{"line"},
				disks:    []pve.Disk{{Model: "Lexar NQ790 1TB", Health: "PASSED"}},
				perms:    map[string]map[string]int{"/vms/204": {"VM.Audit": 1}},
			}
			r, _ := newProxmoxRouter(t, vault, fake)

			w, _ := callPVX(t, r, http.MethodGet, route, "")
			if w.Code != 200 {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			if !vault.wasRead(pveSecretAudit) {
				t.Errorf("keys read = %v, want it to contain %q", vault.reads, pveSecretAudit)
			}
			if k := vault.readAnyWithPrefix(pveSecretNodePrefix); k != "" {
				t.Errorf("the route read %q — the node token returns 200 with len=0 on this route (Sys.Audit on /nodes), i.e. an empty screen LYING", k)
			}
		})
	}
}

func TestSnapshotUsesNodeToken(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/proxmox/snapshots?node=lxc/207"},
		{http.MethodPost, "/api/proxmox/snapshots?node=lxc/207&name=pvx-test"},
		{http.MethodDelete, "/api/proxmox/snapshots?node=lxc/207&name=pvx-test"},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			vault := defaultVault()
			fake := &fakePVE{upid: "UPID:pve:1:2:3:vzsnapshot:207:panel@pve!node-apps:"}
			r, _ := newProxmoxRouter(t, vault, fake)

			w, _ := callPVX(t, r, tc.method, tc.path, "")
			if w.Code != 200 {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			if !vault.wasRead("pve_token_node_apps") {
				t.Errorf("keys read = %v, want to contain pve_token_node_apps (whoever acts is the node)", vault.reads)
			}
			if vault.wasRead(pveSecretAudit) {
				t.Errorf("keys read = %v — snapshot does NOT use the audit token", vault.reads)
			}
		})
	}
}

func TestSnapshotRespondsOnlyAfterWaitTask(t *testing.T) {
	t.Run("order", func(t *testing.T) {
		var seen []string
		fake := &fakePVE{upid: "UPID:abc", seen: &seen}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)

		w, out := callPVX(t, r, http.MethodPost, "/api/proxmox/snapshots?node=lxc/204&name=pvx-drill", "")
		if w.Code != 200 {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body)
		}
		want := []string{"pve.snapcreate:pvx-drill", "pve.wait:UPID:abc"}
		if fmt.Sprint(seen) != fmt.Sprint(want) {
			t.Fatalf("sequence = %v, want %v — the 200 came out before proof of completion", seen, want)
		}
		if out["upid"] != "UPID:abc" {
			t.Errorf("response without the UPID: %s", w.Body)
		}
	})

	t.Run("wait failure becomes 502 with the exitstatus", func(t *testing.T) {
		fake := &fakePVE{
			upid: "UPID:abc",
			waitErr: &pve.Error{Kind: pve.KindHypervisor, Path: "/tasks",
				Body: "snapshot feature is not available"},
		}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)

		w, _ := callPVX(t, r, http.MethodPost, "/api/proxmox/snapshots?node=lxc/204&name=pvx-drill", "")
		if w.Code != 502 {
			t.Fatalf("status = %d, want 502", w.Code)
		}
		if !strings.Contains(w.Body.String(), "snapshot feature is not available") {
			t.Errorf("body = %s — the PVE exitstatus is the only clue to the real reason", w.Body)
		}
	})
}

func TestInvalidNameNeverReachesHypervisor(t *testing.T) {
	for _, name := range []string{"1abc", "with space", "with/slash", ""} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			var seen []string
			fake := &fakePVE{upid: "UPID:abc", seen: &seen}
			r, _ := newProxmoxRouter(t, defaultVault(), fake)

			w, _ := callPVX(t, r, http.MethodPost,
				"/api/proxmox/snapshots?node=lxc/204&name="+url.QueryEscape(name), "")
			if w.Code != 400 {
				t.Errorf("status = %d, want 400 (body=%s)", w.Code, w.Body)
			}
			if len(seen) != 0 {
				t.Errorf("the hypervisor was called (%v) with a rejected name", seen)
			}
		})
	}
}

func TestUnreachableVaultIsNotEmptyList(t *testing.T) {
	t.Run("unreachable = 503", func(t *testing.T) {
		vault := defaultVault()
		vault.unreachable = true
		r, _ := newProxmoxRouter(t, vault, &fakePVE{})
		w, _ := callPVX(t, r, http.MethodGet, "/api/proxmox/tasks", "")
		if w.Code != 503 {
			t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "\"tasks\":[]") {
			t.Error("vault being down turned into an empty list — exactly the false-green that phase 7 forbade")
		}
	})
	t.Run("missing = 409", func(t *testing.T) {
		vault := &spyVault{data: map[string]string{}}
		r, _ := newProxmoxRouter(t, vault, &fakePVE{})
		w, _ := callPVX(t, r, http.MethodGet, "/api/proxmox/tasks", "")
		if w.Code != 409 {
			t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body)
		}
	})
}

func TestTaskLimitIsServerSide(t *testing.T) {
	var seen []string
	fake := &fakePVE{seen: &seen}
	r, _ := newProxmoxRouter(t, defaultVault(), fake)

	if w, _ := callPVX(t, r, http.MethodGet, "/api/proxmox/tasks?limit=99999&errors=1", ""); w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if len(seen) == 0 || !strings.Contains(seen[0], "errors=true") {
		t.Fatalf("marks = %v, want errors=true", seen)
	}
	if strings.Contains(seen[0], "limit=99999") {
		t.Errorf("marks = %v — the client's limit went through intact", seen)
	}
	if !strings.Contains(seen[0], fmt.Sprintf("limit=%d", pve.MaxTasks)) {
		t.Errorf("marks = %v, want limit=%d (the server's cap)", seen, pve.MaxTasks)
	}
}

func TestWrongMethodGives405(t *testing.T) {
	r, _ := newProxmoxRouter(t, defaultVault(), &fakePVE{})
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/proxmox", 405},
		{http.MethodDelete, "/api/proxmox/tasks", 405},
		{http.MethodPut, "/api/proxmox/snapshots?node=lxc/204&name=x", 405},
		{http.MethodGet, "/api/proxmox/does-not-exist", 404},
		{http.MethodGet, "/api/proxmox/snapshots?node=lxc/999", 404},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w, _ := callPVX(t, r, tc.method, tc.path, "")
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (body=%s)", w.Code, tc.want, w.Body)
			}
		})
	}
}

func TestPermissionsExplainWave2(t *testing.T) {
	fake := &fakePVE{perms: map[string]map[string]int{
		"/vms/204": {"VM.Audit": 1, "VM.Snapshot": 1},
		"/nodes":   {"Sys.Audit": 1},
	}}
	r, _ := newProxmoxRouter(t, defaultVault(), fake)
	w, out := callPVX(t, r, http.MethodGet, "/api/proxmox/permissions", "")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if out["storage_visible"] != false {
		t.Errorf("storage_visible = %v, want false — it's what explains wave 2 on the screen", out["storage_visible"])
	}
	if m, _ := out["permissions"].(map[string]any); len(m) != 2 {
		t.Errorf("permissions = %v", out["permissions"])
	}
}

func TestHealthNeverObservedSaysSo(t *testing.T) {
	r, _ := newNodesRouter(t, nil)
	r.nodeVaultFn = func() (nodeVault, error) { return defaultVault(), nil }
	r.inventoryNow = func() time.Time { return time.Unix(testNow, 0) }

	w, out := callPVX(t, r, http.MethodGet, "/api/proxmox", "")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	h, _ := out["hypervisor"].(map[string]any)
	if h["age_seconds"].(float64) != -1 {
		t.Errorf("age_seconds = %v, want -1", h["age_seconds"])
	}
	if h["stale"] != true {
		t.Errorf("stale = %v, want true", h["stale"])
	}
}

func TestRollbackRespondsOnlyAfterWaitTask(t *testing.T) {
	t.Run("order", func(t *testing.T) {
		var seen []string
		fake := &fakePVE{upid: "UPID:roll", seen: &seen}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)

		w, out := callPVX(t, r, http.MethodPost,
			"/api/proxmox/snapshots/rollback?node=lxc/204&name=before-cutover", "")
		if w.Code != 200 {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body)
		}
		want := []string{"pve.snaprollback:before-cutover", "pve.wait:UPID:roll"}
		if fmt.Sprint(seen) != fmt.Sprint(want) {
			t.Fatalf("sequence = %v, want %v — the 200 came out before proof of completion", seen, want)
		}
		if out["action"] != "rollback" || out["upid"] != "UPID:roll" {
			t.Errorf("response = %s", w.Body)
		}
	})

	t.Run("wait failure becomes 502 with the exitstatus", func(t *testing.T) {
		fake := &fakePVE{upid: "UPID:roll", waitErr: &pve.Error{Kind: pve.KindHypervisor, Path: "/tasks",
			Body: "snapshot 'before-cutover' does not exist"}}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)

		w, _ := callPVX(t, r, http.MethodPost,
			"/api/proxmox/snapshots/rollback?node=lxc/204&name=before-cutover", "")
		if w.Code != 502 {
			t.Fatalf("status = %d, want 502", w.Code)
		}
		if !strings.Contains(w.Body.String(), "does not exist") {
			t.Errorf("body = %s — the PVE exitstatus is the only clue to the real reason", w.Body)
		}
	})
}

func TestRollbackUsesNodeToken(t *testing.T) {
	vault := defaultVault()
	r, _ := newProxmoxRouter(t, vault, &fakePVE{upid: "UPID:roll"})

	w, _ := callPVX(t, r, http.MethodPost,
		"/api/proxmox/snapshots/rollback?node=lxc/207&name=before-cutover", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if !vault.wasRead("pve_token_node_apps") {
		t.Errorf("keys read = %v, want to contain pve_token_node_apps", vault.reads)
	}
	if vault.wasRead(pveSecretAudit) {
		t.Errorf("keys read = %v — rollback does NOT use the audit token (403 measured)", vault.reads)
	}
}

func TestRollbackOnlyAcceptsPOST(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		var seen []string
		fake := &fakePVE{upid: "UPID:roll", seen: &seen}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)
		w, _ := callPVX(t, r, m, "/api/proxmox/snapshots/rollback?node=lxc/204&name=before-cutover", "")
		if w.Code != 405 {
			t.Errorf("%s: status = %d, want 405", m, w.Code)
		}
		if len(seen) != 0 {
			t.Errorf("%s: the hypervisor was called (%v)", m, seen)
		}
	}
}

func TestRollbackInvalidNameNeverReachesHypervisor(t *testing.T) {
	for _, name := range []string{"", "1abc", "with space", "with/slash", "../lxc/207"} {
		var seen []string
		fake := &fakePVE{upid: "UPID:roll", seen: &seen}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)
		w, _ := callPVX(t, r, http.MethodPost,
			"/api/proxmox/snapshots/rollback?node=lxc/204&name="+url.QueryEscape(name), "")
		if w.Code != 400 {
			t.Errorf("%q: status = %d, want 400 (body=%s)", name, w.Code, w.Body)
		}
		if len(seen) != 0 {
			t.Errorf("%q: the hypervisor was called (%v) with a rejected name", name, seen)
		}
	}
}

// 🔴 TestNoRouteOffersSuspend. Measured on this host:
// `vzsuspend 204` ended in `lxc-checkpoint -n 204 -s -D /var/lib/vz/dump
// failed: exit code 1` (CRIU) and the CT stayed `running`. A button that always
// errors trains the operator to ignore errors — and the next error, the real one,
// goes unnoticed. Suspend stays OUT until CRIU works on this host, and this test
// is what stops it coming back by absent-mindedness.
func TestNoRouteOffersSuspend(t *testing.T) {
	for _, path := range []string{
		"/api/proxmox/snapshots/suspend?node=lxc/204",
		"/api/proxmox/suspend?node=lxc/204",
	} {
		var seen []string
		fake := &fakePVE{upid: "UPID:x", seen: &seen}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)
		w, _ := callPVX(t, r, http.MethodPost, path, "")
		if w.Code != 404 {
			t.Errorf("%s: status = %d, want 404 — suspend is broken on this host (CRIU)", path, w.Code)
		}
		if len(seen) != 0 {
			t.Errorf("%s: it called the hypervisor (%v)", path, seen)
		}
	}
}
