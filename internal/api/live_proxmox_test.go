//go:build live

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/config"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
	"server-control-panel/internal/secrets"
)

const targetGuest = "lab"

func liveRouter(t *testing.T) (*Router, context.CancelFunc) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	vault, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret)
	if err != nil {
		t.Fatalf("real vault: %v", err)
	}
	pc, err := loadPVEDescriptor(cfg.DataDir)
	if err != nil {
		t.Fatalf("hypervisor descriptor: %v", err)
	}
	st, err := inventory.Open(t.TempDir())
	if err != nil {
		t.Fatalf("temporary store: %v", err)
	}
	r := &Router{cfg: cfg}
	r.secrets = vault
	r.pveConfig = pc
	r.inventoryStore = st

	ctx, cancel := context.WithCancel(context.Background())
	r.startInventoryPoller(ctx)
	if r.inventoryPoller == nil {
		cancel()
		t.Fatal("poller did not come up — no audit token in the vault or no descriptor")
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		inv, err := st.Snapshot()
		if err == nil && inv.Hypervisor.MemUsed.ObservedAt > 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("the poller did not timestamp the hypervisor within 30s")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return r, cancel
}

func pvxGET(t *testing.T, r *Router, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodGet, path, ""))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestLiveProxmoxWave1(t *testing.T) {
	if os.Getenv("LAB_PVX_LIVE") != "1" {
		t.Skip("live proof turned off — run with LAB_PVX_LIVE=1 (it CREATES and DELETES a real snapshot)")
	}
	t0 := time.Now().UTC()
	r, cancel := liveRouter(t)
	defer cancel()

	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	noHV := inv.Hypervisor.Node
	t.Logf("hypervisor discovered: %q · %d nodes", noHV, len(inv.Nodes))

	w, out := pvxGET(t, r, "/api/proxmox")
	if w.Code != 200 {
		t.Fatalf("GET /api/proxmox = %d: %s", w.Code, w.Body)
	}
	h, _ := out["hypervisor"].(map[string]any)
	age, _ := h["age_seconds"].(float64)
	if age < 0 {
		t.Fatalf("age_seconds = %v — the dashboard has not observed the hypervisor", age)
	}
	panelMemUsed := h["mem_used"].(map[string]any)["value"].(float64)
	t.Logf("health: node=%v version=%v age=%vs mem_used=%.0f",
		h["node"], h["version"].(map[string]any)["value"], age, panelMemUsed)

	value, state := r.vaultToken(pveSecretAudit)
	if state != vaultOK {
		t.Fatalf("audit token: %s", state)
	}
	directCfg := *r.pveConfig
	directCfg.TokenID = value
	cli, err := pve.New(directCfg)
	if err != nil {
		t.Fatalf("direct client: %v", err)
	}
	ctx := context.Background()
	directStatus, err := cli.NodeStatus(ctx, noHV)
	if err != nil {
		t.Fatalf("independent read of /status: %v", err)
	}
	diff := panelMemUsed - float64(directStatus.Memory.Used)
	if diff < 0 {
		diff = -diff
	}
	if pct := diff / float64(directStatus.Memory.Total) * 100; pct > 5 {
		t.Errorf("dashboard says %.0f and the hypervisor says %d of RAM used (%.2f%% difference) — the two channels disagree",
			panelMemUsed, directStatus.Memory.Used, pct)
	}

	w, out = pvxGET(t, r, "/api/proxmox/tasks?errors=1&limit=10")
	if w.Code != 200 {
		t.Fatalf("GET /tasks = %d: %s", w.Code, w.Body)
	}
	tasks, _ := out["tasks"].([]any)
	if len(tasks) == 0 {
		t.Error("the errors-only filter returned an EMPTY set — the study measured 10 real failures on this host")
	}
	errUPID := ""
	for i, raw := range tasks {
		m := raw.(map[string]any)
		if i < 5 {
			t.Logf("error %d: %v %v — %v", i+1, m["type"], m["id"], m["status"])
		}
		if errUPID == "" {
			errUPID, _ = m["upid"].(string)
		}
	}

	w, out = pvxGET(t, r, "/api/proxmox/tasks?errors=1&typefilter=type-that-never-exists")
	if w.Code != 200 {
		t.Errorf("negative control = %d, want 200 (empty is not an error): %s", w.Code, w.Body)
	}
	if v, _ := out["tasks"].([]any); len(v) != 0 {
		t.Errorf("negative control returned %d tasks — the typefilter is not acting", len(v))
	}

	if errUPID != "" {
		w, out = pvxGET(t, r, "/api/proxmox/tasks/log?upid="+errUPID)
		if w.Code != 200 {
			t.Errorf("GET /tasks/log = %d: %s", w.Code, w.Body)
		}
		lines, _ := out["lines"].([]any)
		if len(lines) == 0 {
			t.Error("empty log for a task that failed")
		}
		if len(lines) > 200 {
			t.Errorf("log with %d lines — the server's ceiling is 200", len(lines))
		}
		t.Logf("log for %s: %d line(s)", errUPID, len(lines))
	}

	w, disksBody := pvxGET(t, r, "/api/proxmox/disks")
	if w.Code != 200 {
		t.Fatalf("GET /disks = %d: %s", w.Code, w.Body)
	}
	disksText := w.Body.String()
	if !strings.Contains(disksText, "Lexar") {
		t.Errorf("the disks do not bring the measured Lexar: %s", disksText)
	}
	if !strings.Contains(disksText, "PASSED") {
		t.Errorf("no disk reports PASSED: %s", disksText)
	}
	if d, _ := disksBody["disks"].([]any); len(d) > 0 {
		t.Logf("disks: %d", len(d))
	}

	target := ""
	for _, n := range inv.Nodes {
		if n.Kind == inventory.NodeKindGuest && n.Name == targetGuest {
			target = n.ID
			break
		}
	}
	if target == "" {
		t.Fatalf("no guest named %q in the inventory — ABORTING before touching any guest", targetGuest)
	}
	targetNode, _ := findNode(inv, target)
	if targetNode.Name != targetGuest {
		t.Fatalf("guard: target %s is named %q and not %q — ABORTED", target, targetNode.Name, targetGuest)
	}
	t.Logf("target of the live mutation: %s (%s), vmid=%d", target, targetNode.Name, targetNode.VMID)

	name := fmt.Sprintf("pvx-drill-%d", t0.Unix())
	deleted := false
	defer func() {
		if deleted {
			return
		}
		w := httptest.NewRecorder()
		r.handleProxmox(w, req(t, http.MethodDelete,
			"/api/proxmox/snapshots?node="+target+"&name="+name, ""))
		t.Logf("cleanup of snapshot %s: %d", name, w.Code)
	}()

	w = httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodPost, "/api/proxmox/snapshots?node="+target+"&name="+name+"&desc=drill+pvx", ""))
	if w.Code != 200 {
		t.Fatalf("POST snapshot = %d: %s", w.Code, w.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	createUPID, _ := created["upid"].(string)
	t.Logf("snapshot created: %s · upid=%s", name, createUPID)
	if !strings.Contains(createUPID, "panel@pve!node-"+targetGuest) {
		t.Errorf("UPID = %q — want it to carry panel@pve!node-%s (the node is what acts)", createUPID, targetGuest)
	}

	w, out = pvxGET(t, r, "/api/proxmox/snapshots?node="+target)
	if !strings.Contains(w.Body.String(), name) {
		t.Fatalf("snapshot %s did NOT show up in the listing: %s", name, w.Body)
	}

	w = httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodDelete, "/api/proxmox/snapshots?node="+target+"&name="+name, ""))
	if w.Code != 200 {
		t.Fatalf("DELETE snapshot = %d: %s", w.Code, w.Body)
	}
	var removed map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &removed)
	deleteUPID, _ := removed["upid"].(string)
	deleted = true
	t.Logf("snapshot deleted: %s · upid=%s", name, deleteUPID)
	if !strings.Contains(deleteUPID, "panel@pve!node-"+targetGuest) {
		t.Errorf("delete's UPID = %q — want it to carry panel@pve!node-%s", deleteUPID, targetGuest)
	}

	w, _ = pvxGET(t, r, "/api/proxmox/snapshots?node="+target)
	if strings.Contains(w.Body.String(), name) {
		t.Errorf("snapshot %s is still in the listing after DELETE: %s", name, w.Body)
	}

	w = httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodPost, "/api/proxmox/snapshots?node="+target+"&name=1abc", ""))
	if w.Code != 400 {
		t.Errorf("invalid name returned %d, want 400 from the dashboard: %s", w.Code, w.Body)
	}

	w, out = pvxGET(t, r, "/api/proxmox/permissions")
	if w.Code != 200 {
		t.Fatalf("GET /permissions = %d: %s", w.Code, w.Body)
	}
	perms, _ := out["permissions"].(map[string]any)
	if len(perms) == 0 {
		t.Error("empty permissions map")
	}
	if out["storage_visible"] != true {
		t.Errorf("storage_visible = %v — the 2026-08-20 ACL should have made the datastore auditable", out["storage_visible"])
	}
	hasStorage := false
	for path := range perms {
		if strings.HasPrefix(path, "/storage") {
			hasStorage = true
		}
	}
	if !hasStorage {
		t.Error("no /storage path in the live map")
	}
	t.Logf("permissions: %d path(s), storage_visible=%v", len(perms), out["storage_visible"])

	t1 := time.Now().UTC()
	t.Logf("[t0=%s t1=%s] live-proof window (%.1fs)",
		t0.Format(time.RFC3339), t1.Format(time.RFC3339), t1.Sub(t0).Seconds())
}

func TestLiveProxmoxWave2(t *testing.T) {
	if os.Getenv("LAB_PVS_LIVE") != "1" {
		t.Skip("wave 2 live proof turned off — run with LAB_PVS_LIVE=1")
	}
	t0 := time.Now().UTC()
	r, cancel := liveRouter(t)
	defer cancel()

	value, state := r.vaultToken(pveSecretAudit)
	if state != vaultOK {
		t.Fatalf("audit token: %s", state)
	}
	directCfg := *r.pveConfig
	directCfg.TokenID = value
	cli, err := pve.New(directCfg)
	if err != nil {
		t.Fatalf("direct client: %v", err)
	}
	ctx := context.Background()

	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	noHV := inv.Hypervisor.Node
	if noHV == "" {
		t.Fatal("inventory does not know the hypervisor's name — without it there is no independent read")
	}

	w, out := pvxGET(t, r, "/api/proxmox/storage")
	if w.Code != 200 {
		t.Fatalf("GET /storage = %d: %s", w.Code, w.Body)
	}
	pools, _ := out["pools"].([]any)
	seen := map[string]map[string]any{}
	for _, raw := range pools {
		p := raw.(map[string]any)
		id, _ := p["id"].(string)
		seen[id] = p
		t.Logf("storage %-10s %-8s %5.1f%%  %s / %s  [%v]",
			id, p["type"], p["used_pct"],
			human(p["used"]), human(p["total"]), p["content"])
	}

	if len(pools) == 0 {
		t.Fatalf("no storage in the live response — without it the comparison below would pass vacuously")
	}
	directStorage, err := cli.StorageList(ctx, noHV)
	if err != nil {
		t.Fatalf("independent read of /storage: %v", err)
	}
	onHypervisor := map[string]bool{}
	for _, s := range directStorage {
		onHypervisor[s.Storage] = true
	}
	if len(onHypervisor) == 0 {
		t.Fatalf("the hypervisor declared no storage at all — empty independent read")
	}
	for id := range onHypervisor {
		if seen[id] == nil {
			t.Errorf("storage %q exists on the hypervisor and does NOT show up on the dashboard", id)
		}
	}
	for id := range seen {
		if !onHypervisor[id] {
			t.Errorf("storage %q shows up on the dashboard and does NOT exist on the hypervisor", id)
		}
	}
	t.Logf("storage inventory: %d on the dashboard, %d on the hypervisor, identical sets",
		len(seen), len(onHypervisor))
	for id, p := range seen {
		used, _ := p["used"].(float64)
		pct, _ := p["used_pct"].(float64)
		if used > 0 && pct <= 0 {
			t.Errorf("storage %q has %v bytes used and used_pct = %v — empty bar over a disk that is in use", id, used, pct)
		}
	}
	age, _ := out["age_seconds"].(float64)
	if age < 0 {
		t.Error("age_seconds < 0 — the poller did not timestamp capacity")
	}
	if age > 120 {
		t.Errorf("age_seconds = %v — capacity is not being observed on the tick", age)
	}
	t.Logf("capacity: %d storages, age %vs, stale=%v", len(pools), age, out["stale"])

	da, _ := out["datastore_audit"].(map[string]any)
	if da == nil {
		t.Fatalf("capacity response without datastore_audit: %s", w.Body)
	}
	if da["value"] != true {
		t.Errorf("datastore_audit.value = %v — with the ACL granted, want true", da["value"])
	}
	if c, _ := da["observed_at"].(float64); c <= 0 {
		t.Errorf("datastore_audit without a timestamp (%v) — a verdict with no age is a verdict that lies", da["observed_at"])
	}

	livePerms, err := cli.Permissions(ctx)
	if err != nil {
		t.Fatalf("independent read of /access/permissions: %v", err)
	}
	if !pve.CanAuditDatastore(livePerms) {
		t.Error("the LIVE map does not authorize datastore — the dashboard and the hypervisor disagree")
	}
	for path, privs := range livePerms {
		if path == "/" || path == "/storage" || strings.HasPrefix(path, "/storage/") {
			for _, p := range []string{"Datastore.Audit", "Datastore.Allocate", "Datastore.AllocateSpace", "Datastore.AllocateTemplate"} {
				delete(privs, p)
			}
		}
	}
	if pve.CanAuditDatastore(livePerms) {
		t.Error("🔴 with no Datastore.* on any path, the verdict stayed TRUE — the guard no longer fails it")
	}
	t.Log("guard proven in both directions over the LIVE map: with privilege → true, without privilege → false")

	w, out = pvxGET(t, r, "/api/proxmox/zfs")
	if w.Code != 200 {
		t.Fatalf("GET /zfs = %d: %s", w.Code, w.Body)
	}
	zp, _ := out["pools"].([]any)
	if len(zp) < 2 {
		t.Errorf("zpools = %d, want 2 (backup, rpool)", len(zp))
	}
	names := map[string]bool{}
	for _, raw := range zp {
		p := raw.(map[string]any)
		name, _ := p["name"].(string)
		names[name] = true
		t.Logf("zpool %-8s %-9s frag=%v alloc=%s free=%s healthy=%v",
			name, p["health"], p["frag_pct"], human(p["alloc"]), human(p["free"]), p["healthy"])
		if p["health"] != "ONLINE" {
			t.Errorf("🔴 pool %q is %v — SINGLE DISK, no redundancy", name, p["health"])
		}
		if p["healthy"] != (p["health"] == "ONLINE") {
			t.Errorf("pool %q: healthy=%v does not match health=%v", name, p["healthy"], p["health"])
		}
	}
	for _, n := range []string{"backup", "rpool"} {
		if !names[n] {
			t.Errorf("zpool %q missing", n)
		}
	}

	wS, health := pvxGET(t, r, "/api/proxmox")
	if wS.Code != 200 {
		t.Fatalf("GET /api/proxmox = %d", wS.Code)
	}
	h, _ := health["hypervisor"].(map[string]any)
	healthAge, _ := h["age_seconds"].(float64)
	_, capOut := pvxGET(t, r, "/api/proxmox/storage")
	idCap, _ := capOut["age_seconds"].(float64)
	zfsAge, _ := out["age_seconds"].(float64)
	if healthAge < 0 || idCap < 0 || zfsAge < 0 {
		t.Errorf("ages = health %v, capacity %v, zpool %v — none may be -1 after a tick",
			healthAge, idCap, zfsAge)
	}
	t.Logf("three independent ages: health %vs · capacity %vs · zpool %vs", healthAge, idCap, zfsAge)

	w, _ = pvxGET(t, r, "/api/proxmox/apt")
	if w.Code != 404 {
		t.Errorf("/api/proxmox/apt = %d, want 404 — apt requires Sys.Modify and stays out of scope", w.Code)
	}

	t1 := time.Now().UTC()
	t.Logf("[t0=%s t1=%s] wave 2 live-proof window (%.1fs)",
		t0.Format(time.RFC3339), t1.Format(time.RFC3339), t1.Sub(t0).Seconds())
}

func human(v any) string {
	f, _ := v.(float64)
	u := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for f >= 1024 && i < len(u)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f%s", f, u[i])
}
