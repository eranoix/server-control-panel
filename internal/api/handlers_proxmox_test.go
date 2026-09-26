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

// handlers_proxmox_test.go — the pins for the /api/proxmox/* routes.
//
// The central piece of scaffolding is spyVault: it RECORDS every key read.
// Without it, the mutation this file exists to prevent would slip by unnoticed —
// swapping `pve_token_audit` for `pve_token_node_*` on the tasks route
// produces no error at all, it produces an EMPTY LIST. Measured:
// GET /nodes/pve/tasks?limit=50 with panel@pve!node-apps returns 200 with len=0,
// because Tasks.pm:40-45 requires Sys.Audit on /nodes and the PanelOperator role does not.
// A test that only looked at the HTTP status would say "passed".

// --------------------------------------------------------------- doubles ----

// spyVault is fakeVault with a memory: it keeps the ORDER and the SET of the
// keys read, which is what makes the token choice verifiable by NAME.
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

// ---------------------------------------------------------- scaffolding ----

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

// newProxmoxRouter assembles the router with inventory, hypervisor and spying vault.
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

// ------------------------------------------------------------- the tests ----

// 🔴 TestHealthComesFromStoreWithoutCallingHypervisor: health is a HEARTBEAT, and what
// collects it is the poller. If the route called the hypervisor, every screen
// load (and every 30 s refresh) would become a live request — and, worse, the age
// on display would stop being the stamp's and always read "0 s", hiding
// precisely the hypervisor that has gone mute.
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

// 🔴 TestTasksUseAuditToken is this file's central pin. It asserts by the KEY
// THAT WAS READ, not by the result: with the node token the answer would be 200
// with an empty list, and no assertion about the body would tell that apart from
// "there are no tasks".
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

// 🔴 TestSnapshotUsesNodeToken is the other half: a guest mutation uses the
// NODE's credential (the token rule, proved live with the UPID carrying
// panel@pve!node-lab). Using audit here would give a 403 — noisy, but wrong all the
// same: what acts is not what audits.
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

// 🔴 TestSnapshotRespondsOnlyAfterWaitTask is that PVE pitfall on this route:
// PVE's POST returns 200 with the UPID as soon as the TASK IS CREATED. Passing
// that 200 along would be the screen saying "snapshot ready" for a snapshot that
// may not even have started.
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

// TestInvalidNameNeverReachesHypervisor: the name comes from the SCREEN, and it is
// what builds the resource path on the hypervisor. The refusal happens before any
// call — and the test proves that by the absence of a mark on the double, not by the status.
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

// 🔴 TestUnreachableVaultIsNotEmptyList: a vault that is down and a credential
// that does not exist call for OPPOSITE actions from the operator. Collapsing the
// two into one empty answer is the collapse of handlers_ai.go:186, which already produced a defect.
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

// TestTaskLimitIsServerSide: the client's `limit` is a suggestion. What
// reaches the hypervisor goes through internal/pve's clamp; here it is proved that
// the handler does not invent a parallel path.
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

// TestWrongMethodGives405, and an unknown route gives 404: together the two stop
// a new verb appearing by accident on a route that touches the hypervisor.
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

// TestPermissionsExplainWave2: the permissions field exists so the screen can
// say, by MEASUREMENT, why storage capacity / backup evidence / zpool are
// missing. Without it the block would be decoration.
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

// TestHealthNeverObservedSaysSo: dashboard just up, poller with no tick yet.
// The screen has to say "never observed" (-1), never "0 s ago".
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

// ─────────────────────────────────────────────────────────────────────────────
// Snapshot rollback
//
// 🔴 The privilege WAS ALREADY GRANTED and the dashboard did not show it.
// Measured without changing any ACL at all:
//
//	node-lab → POST /nodes/pve/lxc/204/snapshot/<nonexistent>/rollback  → 200 + UPID
//	audit    → the SAME POST  → 403 "Permission check failed (/vms/204, VM.Snapshot|VM.Snapshot.Rollback)"
//	node-lab → the same POST on /lxc/207 → 403 (isolation by vmid)
//
// Destructive power that exists, nobody sees, and no screen leaves a trace of who
// used it. Exposing it with a trail is safer than leaving it hidden: what is
// hidden stays reachable by whoever holds the token, and with no record at all.
// ─────────────────────────────────────────────────────────────────────────────

// 🔴 TestRollbackRespondsOnlyAfterWaitTask is that PVE pitfall MEASURED on the
// most destructive route of the dashboard. Against the home hypervisor, a
// rollback to a snapshot that DOES NOT EXIST returned:
//
//	HTTP 200 {"data":"UPID:pve:…:vzrollback:204:panel@pve!node-lab:"}
//
// and only the task status told the truth:
//
//	status=stopped exitstatus="snapshot '<name>' does not exist"
//
// Passing that 200 along would be the screen saying "restored" for a rollback that
// never happened — and, worse, on a guest the operator would then believe to be
// in an earlier state.
func TestRollbackRespondsOnlyAfterWaitTask(t *testing.T) {
	t.Run("order", func(t *testing.T) {
		var seen []string
		fake := &fakePVE{upid: "UPID:roll", seen: &seen}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)

		w, out := callPVX(t, r, http.MethodPost,
			"/api/proxmox/snapshots/rollback?node=lxc/204&name=antes-do-cutover", "")
		if w.Code != 200 {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body)
		}
		want := []string{"pve.snaprollback:antes-do-cutover", "pve.wait:UPID:roll"}
		if fmt.Sprint(seen) != fmt.Sprint(want) {
			t.Fatalf("sequence = %v, want %v — the 200 came out before proof of completion", seen, want)
		}
		if out["action"] != "rollback" || out["upid"] != "UPID:roll" {
			t.Errorf("response = %s", w.Body)
		}
	})

	t.Run("wait failure becomes 502 with the exitstatus", func(t *testing.T) {
		fake := &fakePVE{upid: "UPID:roll", waitErr: &pve.Error{Kind: pve.KindHypervisor, Path: "/tasks",
			Body: "snapshot 'antes-do-cutover' does not exist"}}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)

		w, _ := callPVX(t, r, http.MethodPost,
			"/api/proxmox/snapshots/rollback?node=lxc/204&name=antes-do-cutover", "")
		if w.Code != 502 {
			t.Fatalf("status = %d, want 502", w.Code)
		}
		if !strings.Contains(w.Body.String(), "does not exist") {
			t.Errorf("body = %s — the PVE exitstatus is the only clue to the real reason", w.Body)
		}
	})
}

// TestRollbackUsesNodeToken — the token rule again: what acts on a guest is THAT
// node's credential. The audit token got a 403 in the measurement.
func TestRollbackUsesNodeToken(t *testing.T) {
	vault := defaultVault()
	r, _ := newProxmoxRouter(t, vault, &fakePVE{upid: "UPID:roll"})

	w, _ := callPVX(t, r, http.MethodPost,
		"/api/proxmox/snapshots/rollback?node=lxc/207&name=antes-do-cutover", "")
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

// 🔴 TestRollbackOnlyAcceptsPOST: a rollback triggerable by GET would be triggerable
// by browser prefetch, by a crawler and by a link pasted into a chat.
func TestRollbackOnlyAcceptsPOST(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		var seen []string
		fake := &fakePVE{upid: "UPID:roll", seen: &seen}
		r, _ := newProxmoxRouter(t, defaultVault(), fake)
		w, _ := callPVX(t, r, m, "/api/proxmox/snapshots/rollback?node=lxc/204&name=antes-do-cutover", "")
		if w.Code != 405 {
			t.Errorf("%s: status = %d, want 405", m, w.Code)
		}
		if len(seen) != 0 {
			t.Errorf("%s: the hypervisor was called (%v)", m, seen)
		}
	}
}

// TestRollbackInvalidNameNeverReachesHypervisor: the name comes from the screen and
// chooses WHICH state the guest will take on. Refused before dialling, proved by
// the absence of a mark on the double.
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
