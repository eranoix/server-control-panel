//go:build live

package api

// live_proxmox_test.go — the LIVE proof of the Proxmox tab against the home
// hypervisor.
//
// DOUBLE LOCK, cast from the revocation drill: the `live` build tag AND the
// LAB_PVX_LIVE=1 variable. It creates and deletes a real snapshot on a real
// guest; none of that may happen by accident in a `go test ./...`.
//
// What it does NOT do, and why: it does not forge a dashboard session and does
// not prove authentication. Authentication is not what this work delivers, and
// pretending otherwise is exactly the mistake that was refused earlier. It uses
// the scaffolding the package already sanctions (auth.WithUser in the context,
// handlers_nodes_test.go).
//
//	run: LAB_PVX_LIVE=1 go test -tags=live -run TestLive ./internal/api/ -v

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

// guestAlvo is the lab CT. 🔴 THE TARGET IS THE NAME, NEVER THE NUMBER: as
// measured, CT 201 is `games` — the one the family uses — and 204 is `lab`. An
// earlier plan got that name wrong and was only saved by the declared intent.
// Here the guard is explicit and ABORTS if the target guest is not called `lab`.
const guestAlvo = "lab"

func routerVivo(t *testing.T) (*Router, context.CancelFunc) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cofre, err := secrets.Open(filepath.Join(cfg.DataDir, "secrets.vault"), cfg.JWTSecret)
	if err != nil {
		t.Fatalf("cofre real: %v", err)
	}
	pc, err := loadPVEDescriptor(cfg.DataDir)
	if err != nil {
		t.Fatalf("hypervisor descriptor: %v", err)
	}
	// 🔴 The test's inventory lives in a TEMPORARY directory. Writing into the
	// production data/inventory would put two processes (this test and the live
	// dashboard) writing the same document.
	st, err := inventory.Open(t.TempDir())
	if err != nil {
		t.Fatalf("temporary store: %v", err)
	}
	r := &Router{cfg: cfg}
	r.secrets = cofre
	r.pveConfig = pc
	r.inventoryStore = st

	ctx, cancel := context.WithCancel(context.Background())
	r.startInventoryPoller(ctx)
	if r.inventoryPoller == nil {
		cancel()
		t.Fatal("poller did not come up — no audit token in the vault or no descriptor")
	}
	// ACTIVE wait on an event (the first tick), with a deadline. It is not a clock
	// wait: what is being awaited is the hypervisor's answer, and the test dies in
	// 30 s instead of hanging.
	prazo := time.Now().Add(30 * time.Second)
	for {
		inv, err := st.Snapshot()
		if err == nil && inv.Hypervisor.MemUsed.ObservedAt > 0 {
			break
		}
		if time.Now().After(prazo) {
			cancel()
			t.Fatal("the poller did not timestamp the hypervisor within 30s")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return r, cancel
}

func pvxGET(t *testing.T, r *Router, caminho string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodGet, caminho, ""))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestLiveProxmoxOnda1(t *testing.T) {
	if os.Getenv("LAB_PVX_LIVE") != "1" {
		t.Skip("live proof turned off — run with LAB_PVX_LIVE=1 (it CREATES and DELETES a real snapshot)")
	}
	t0 := time.Now().UTC()
	r, cancel := routerVivo(t)
	defer cancel()

	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	noHV := inv.Hypervisor.Node
	t.Logf("hypervisor discovered: %q · %d nodes", noHV, len(inv.Nodes))

	// ── 1. health: two independent channels have to agree ───────────────────
	//
	// A single channel cannot tell "right" from "consistently wrong".
	w, out := pvxGET(t, r, "/api/proxmox")
	if w.Code != 200 {
		t.Fatalf("GET /api/proxmox = %d: %s", w.Code, w.Body)
	}
	h, _ := out["hypervisor"].(map[string]any)
	idade, _ := h["age_seconds"].(float64)
	if idade < 0 {
		t.Fatalf("age_seconds = %v — the dashboard has not observed the hypervisor", idade)
	}
	memUsadaPainel := h["mem_used"].(map[string]any)["value"].(float64)
	t.Logf("health: node=%v version=%v age=%vs mem_used=%.0f",
		h["node"], h["version"].(map[string]any)["value"], idade, memUsadaPainel)

	valor, estado := r.tokenDoCofre(pveSecretAudit)
	if estado != vaultOK {
		t.Fatalf("audit token: %s", estado)
	}
	cfgDireto := *r.pveConfig
	cfgDireto.TokenID = valor
	cli, err := pve.New(cfgDireto)
	if err != nil {
		t.Fatalf("direct client: %v", err)
	}
	ctx := context.Background()
	stDireto, err := cli.NodeStatus(ctx, noHV)
	if err != nil {
		t.Fatalf("independent read of /status: %v", err)
	}
	dif := memUsadaPainel - float64(stDireto.Memory.Used)
	if dif < 0 {
		dif = -dif
	}
	if pct := dif / float64(stDireto.Memory.Total) * 100; pct > 5 {
		t.Errorf("dashboard says %.0f and the hypervisor says %d of RAM used (%.2f%% difference) — the two channels disagree",
			memUsadaPainel, stDireto.Memory.Used, pct)
	}

	// ── 2. tasks: the real failures nobody sees, plus the negative control ─
	w, out = pvxGET(t, r, "/api/proxmox/tasks?errors=1&limit=10")
	if w.Code != 200 {
		t.Fatalf("GET /tasks = %d: %s", w.Code, w.Body)
	}
	tarefas, _ := out["tasks"].([]any)
	if len(tarefas) == 0 {
		t.Error("the errors-only filter returned an EMPTY set — the study measured 10 real failures on this host")
	}
	upidDeErro := ""
	for i, raw := range tarefas {
		m := raw.(map[string]any)
		if i < 5 {
			t.Logf("error %d: %v %v — %v", i+1, m["type"], m["id"], m["status"])
		}
		if upidDeErro == "" {
			upidDeErro, _ = m["upid"].(string)
		}
	}

	// Negative control: a filter that matches nothing returns 200 with an empty
	// list. It proves TWO things at once — that the filter acts, and that empty is
	// not an error (PVE's {"data":[]} envelope, a known pitfall).
	w, out = pvxGET(t, r, "/api/proxmox/tasks?errors=1&typefilter=tipo-que-nao-existe-jamais")
	if w.Code != 200 {
		t.Errorf("negative control = %d, want 200 (empty is not an error): %s", w.Code, w.Body)
	}
	if v, _ := out["tasks"].([]any); len(v) != 0 {
		t.Errorf("negative control returned %d tasks — the typefilter is not acting", len(v))
	}

	// ── 3. one task's log, cut at 200 lines BY THE SERVER ───────────────────
	if upidDeErro != "" {
		w, out = pvxGET(t, r, "/api/proxmox/tasks/log?upid="+upidDeErro)
		if w.Code != 200 {
			t.Errorf("GET /tasks/log = %d: %s", w.Code, w.Body)
		}
		linhas, _ := out["lines"].([]any)
		if len(linhas) == 0 {
			t.Error("empty log for a task that failed")
		}
		if len(linhas) > 200 {
			t.Errorf("log with %d lines — the server's ceiling is 200", len(linhas))
		}
		t.Logf("log for %s: %d line(s)", upidDeErro, len(linhas))
	}

	// ── 4. disks with SMART ─────────────────────────────────────────────────
	w, corpoDiscos := pvxGET(t, r, "/api/proxmox/disks")
	if w.Code != 200 {
		t.Fatalf("GET /disks = %d: %s", w.Code, w.Body)
	}
	txtDiscos := w.Body.String()
	if !strings.Contains(txtDiscos, "Lexar") {
		t.Errorf("the disks do not bring the measured Lexar: %s", txtDiscos)
	}
	if !strings.Contains(txtDiscos, "PASSED") {
		t.Errorf("no disk reports PASSED: %s", txtDiscos)
	}
	if d, _ := corpoDiscos["disks"].([]any); len(d) > 0 {
		t.Logf("discos: %d", len(d))
	}

	// ── 5. LIVE snapshot mutation, only on the guest named `lab` ────────────
	alvo := ""
	for _, n := range inv.Nodes {
		if n.Kind == inventory.NodeKindGuest && n.Name == guestAlvo {
			alvo = n.ID
			break
		}
	}
	if alvo == "" {
		t.Fatalf("no guest named %q in the inventory — ABORTING before touching any guest", guestAlvo)
	}
	noAlvo, _ := achaNo(inv, alvo)
	if noAlvo.Name != guestAlvo {
		t.Fatalf("guard: target %s is named %q and not %q — ABORTED", alvo, noAlvo.Name, guestAlvo)
	}
	t.Logf("target of the live mutation: %s (%s), vmid=%d", alvo, noAlvo.Name, noAlvo.VMID)

	nome := fmt.Sprintf("pvx-drill-%d", t0.Unix())
	apagado := false
	defer func() {
		if apagado {
			return
		}
		// Cleanup even if the test dies halfway: an orphan snapshot on a guest is
		// exactly the kind of leftover nobody ever finds.
		w := httptest.NewRecorder()
		r.handleProxmox(w, req(t, http.MethodDelete,
			"/api/proxmox/snapshots?node="+alvo+"&name="+nome, ""))
		t.Logf("cleanup of snapshot %s: %d", nome, w.Code)
	}()

	w = httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodPost, "/api/proxmox/snapshots?node="+alvo+"&name="+nome+"&desc=drill+pvx", ""))
	if w.Code != 200 {
		t.Fatalf("POST snapshot = %d: %s", w.Code, w.Body)
	}
	var criado map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &criado)
	upidCriar, _ := criado["upid"].(string)
	t.Logf("snapshot criado: %s · upid=%s", nome, upidCriar)
	// 🔴 The token rule, live: what acted was the NODE's credential, and the UPID
	// carries its name. If `audit` shows up here, the token choice has collapsed.
	if !strings.Contains(upidCriar, "lab@pve!node-"+guestAlvo) {
		t.Errorf("UPID = %q — want it to carry lab@pve!node-%s (the node is what acts)", upidCriar, guestAlvo)
	}

	w, out = pvxGET(t, r, "/api/proxmox/snapshots?node="+alvo)
	if !strings.Contains(w.Body.String(), nome) {
		t.Fatalf("snapshot %s did NOT show up in the listing: %s", nome, w.Body)
	}

	w = httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodDelete, "/api/proxmox/snapshots?node="+alvo+"&name="+nome, ""))
	if w.Code != 200 {
		t.Fatalf("DELETE snapshot = %d: %s", w.Code, w.Body)
	}
	var removido map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &removido)
	upidApagar, _ := removido["upid"].(string)
	apagado = true
	t.Logf("snapshot deleted: %s · upid=%s", nome, upidApagar)
	if !strings.Contains(upidApagar, "lab@pve!node-"+guestAlvo) {
		t.Errorf("delete's UPID = %q — want it to carry lab@pve!node-%s", upidApagar, guestAlvo)
	}

	w, _ = pvxGET(t, r, "/api/proxmox/snapshots?node="+alvo)
	if strings.Contains(w.Body.String(), nome) {
		t.Errorf("snapshot %s is still in the listing after DELETE: %s", nome, w.Body)
	}

	// Negative control for the mutation: an invalid name is refused BY THE
	// DASHBOARD, without touching the hypervisor.
	w = httptest.NewRecorder()
	r.handleProxmox(w, req(t, http.MethodPost, "/api/proxmox/snapshots?node="+alvo+"&name=1abc", ""))
	if w.Code != 400 {
		t.Errorf("invalid name returned %d, want 400 from the dashboard: %s", w.Code, w.Body)
	}

	// ── 6. permissions ──────────────────────────────────────────────────────
	//
	// 🔴 THIS ASSERTION WAS INVERTED, and the inversion is what the later pass proves.
	//
	// Until the ACL was granted, this test demanded `storage_visivel == false` and
	// that NO /storage path show up: that was how the first pass recorded, by
	// measurement and not by promise, that capacity and zpool were out of the
	// token's reach. The operator granted `PVEAuditor` propagated from the root, and
	// the measurement changed. What did NOT change was the guard: it is still in the
	// code, still measuring, and it simply passed.
	w, out = pvxGET(t, r, "/api/proxmox/permissions")
	if w.Code != 200 {
		t.Fatalf("GET /permissions = %d: %s", w.Code, w.Body)
	}
	perms, _ := out["permissions"].(map[string]any)
	if len(perms) == 0 {
		t.Error("empty permissions map")
	}
	if out["storage_visivel"] != true {
		t.Errorf("storage_visivel = %v — the 2026-08-20 ACL should have made the datastore auditable", out["storage_visivel"])
	}
	temStorage := false
	for caminho := range perms {
		if strings.HasPrefix(caminho, "/storage") {
			temStorage = true
		}
	}
	if !temStorage {
		t.Error("no /storage path in the live map")
	}
	t.Logf("permissions: %d path(s), storage_visivel=%v", len(perms), out["storage_visivel"])

	t1 := time.Now().UTC()
	t.Logf("[t0=%s t1=%s] live-proof window (%.1fs)",
		t0.Format(time.RFC3339), t1.Format(time.RFC3339), t1.Sub(t0).Seconds())
}

// live_proxmox_onda2 — the LIVE proof of capacity and zpool.
//
// Unlike the earlier drill, this test is READ-ONLY: it neither creates nor
// deletes anything, and it does not touch the hypervisor's ACL. The double lock
// (`live` tag + LAB_PVS_LIVE=1) stays anyway — what it does is a live call to the
// house, and a live call must not happen by accident in a `go test ./...`.
//
//	run: LAB_PVS_LIVE=1 go test -tags=live -run TestLiveProxmoxOnda2 ./internal/api/ -v
func TestLiveProxmoxOnda2(t *testing.T) {
	if os.Getenv("LAB_PVS_LIVE") != "1" {
		t.Skip("wave 2 live proof turned off — run with LAB_PVS_LIVE=1")
	}
	t0 := time.Now().UTC()
	r, cancel := routerVivo(t)
	defer cancel()

	// A channel INDEPENDENT of the dashboard, opened once and used by every section.
	// Comparing the dashboard's answer against itself proves nothing; the value of a
	// live proof is asking the hypervisor by another route.
	valor, estado := r.tokenDoCofre(pveSecretAudit)
	if estado != vaultOK {
		t.Fatalf("audit token: %s", estado)
	}
	cfgDireto := *r.pveConfig
	cfgDireto.TokenID = valor
	cli, err := pve.New(cfgDireto)
	if err != nil {
		t.Fatalf("direct client: %v", err)
	}
	ctx := context.Background()

	// The node is DISCOVERED, never hand-written: "pve" is this house's name today,
	// and a literal here would become debt the day there is a second node.
	inv, err := r.inventoryStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	noHV := inv.Hypervisor.Node
	if noHV == "" {
		t.Fatal("inventory does not know the hypervisor's name — without it there is no independent read")
	}

	// ── 1. capacity: the storages, with numbers, AGE and inventory ──────────
	w, out := pvxGET(t, r, "/api/proxmox/storage")
	if w.Code != 200 {
		t.Fatalf("GET /storage = %d: %s", w.Code, w.Body)
	}
	pools, _ := out["pools"].([]any)
	vistos := map[string]map[string]any{}
	for _, raw := range pools {
		p := raw.(map[string]any)
		id, _ := p["id"].(string)
		vistos[id] = p
		t.Logf("storage %-10s %-8s %5.1f%%  %s / %s  [%v]",
			id, p["type"], p["used_pct"],
			humano(p["used"]), humano(p["total"]), p["content"])
	}

	// 🔴 The storage inventory does NOT lock itself to a literal list.
	//
	// This pin used to be a list of four measured names, and it failed the day the
	// operator removed `backupusb` — complaining about the very removal he had
	// ordered. A literal list turns TODAY's inventory into a contract forever: any
	// legitimate storage that comes or goes becomes a failure, and a pin that fails
	// on a correct change teaches people to ignore it.
	//
	// The property that matters is a different, stronger one: the dashboard shows
	// EXACTLY what the hypervisor declares. Not less (a storage vanishing from the
	// screen is the real defect — the operator stops seeing a disk that may be
	// filling up), and not more (a phantom storage on screen is invented data).
	// Compared against an INDEPENDENT read, not against its own answer.
	if len(pools) == 0 {
		t.Fatalf("no storage in the live response — without it the comparison below would pass vacuously")
	}
	stDireta, err := cli.StorageList(ctx, noHV)
	if err != nil {
		t.Fatalf("independent read of /storage: %v", err)
	}
	noHipervisor := map[string]bool{}
	for _, s := range stDireta {
		noHipervisor[s.Storage] = true
	}
	if len(noHipervisor) == 0 {
		t.Fatalf("the hypervisor declared no storage at all — empty independent read")
	}
	for id := range noHipervisor {
		if vistos[id] == nil {
			t.Errorf("storage %q exists on the hypervisor and does NOT show up on the dashboard", id)
		}
	}
	for id := range vistos {
		if !noHipervisor[id] {
			t.Errorf("storage %q shows up on the dashboard and does NOT exist on the hypervisor", id)
		}
	}
	t.Logf("storage inventory: %d on the dashboard, %d on the hypervisor, identical sets",
		len(vistos), len(noHipervisor))
	// 🔴 No percentage may come back at zero for a storage that is in use: that
	// would be pctDeUso's degradation not having happened, and the bar would stay
	// green over a disk that may be full.
	for id, p := range vistos {
		usado, _ := p["used"].(float64)
		pct, _ := p["used_pct"].(float64)
		if usado > 0 && pct <= 0 {
			t.Errorf("storage %q has %v bytes used and used_pct = %v — empty bar over a disk that is in use", id, usado, pct)
		}
	}
	idade, _ := out["age_seconds"].(float64)
	if idade < 0 {
		t.Error("age_seconds < 0 — the poller did not timestamp capacity")
	}
	if idade > 120 {
		t.Errorf("age_seconds = %v — capacity is not being observed on the tick", idade)
	}
	t.Logf("capacidade: %d storages, idade %vs, stale=%v", len(pools), idade, out["stale"])

	// ── 2. the GUARD, live and in BOTH directions ───────────────────────────
	//
	// 🔴 The positive direction is today's measurement. The negative direction
	// CANNOT be proved by removing the ACL (touching the hypervisor is off limits),
	// so it is proved over the LIVE MAP: strip Datastore.* out of the paths that
	// cover storage and the verdict has to flip to false. Without that half, a guard
	// hard-wired to return `true` would pass.
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

	permsVivas, err := cli.Permissions(ctx)
	if err != nil {
		t.Fatalf("independent read of /access/permissions: %v", err)
	}
	if !pve.PodeAuditarDatastore(permsVivas) {
		t.Error("the LIVE map does not authorize datastore — the dashboard and the hypervisor disagree")
	}
	for caminho, privs := range permsVivas {
		if caminho == "/" || caminho == "/storage" || strings.HasPrefix(caminho, "/storage/") {
			for _, p := range []string{"Datastore.Audit", "Datastore.Allocate", "Datastore.AllocateSpace", "Datastore.AllocateTemplate"} {
				delete(privs, p)
			}
		}
	}
	if pve.PodeAuditarDatastore(permsVivas) {
		t.Error("🔴 with no Datastore.* on any path, the verdict stayed TRUE — the guard no longer fails it")
	}
	t.Log("guard proven in both directions over the LIVE map: with privilege → true, without privilege → false")

	// ── 3. zpools: health, frag and allocation ──────────────────────────────
	w, out = pvxGET(t, r, "/api/proxmox/zfs")
	if w.Code != 200 {
		t.Fatalf("GET /zfs = %d: %s", w.Code, w.Body)
	}
	zp, _ := out["pools"].([]any)
	if len(zp) < 2 {
		t.Errorf("zpools = %d, want 2 (backup, rpool)", len(zp))
	}
	nomes := map[string]bool{}
	for _, raw := range zp {
		p := raw.(map[string]any)
		nome, _ := p["name"].(string)
		nomes[nome] = true
		t.Logf("zpool %-8s %-9s frag=%v alloc=%s free=%s saudavel=%v",
			nome, p["health"], p["frag_pct"], humano(p["alloc"]), humano(p["free"]), p["saudavel"])
		// 🔴 A pool out of ONLINE is the most expensive news in this house (a single
		// disk, no redundancy). Failing here is the test doing its job.
		if p["health"] != "ONLINE" {
			t.Errorf("🔴 pool %q is %v — SINGLE DISK, no redundancy", nome, p["health"])
		}
		if p["saudavel"] != (p["health"] == "ONLINE") {
			t.Errorf("pool %q: saudavel=%v does not match health=%v", nome, p["saudavel"], p["health"])
		}
	}
	for _, n := range []string{"backup", "rpool"} {
		if !nomes[n] {
			t.Errorf("zpool %q ausente", n)
		}
	}

	// ── 4. the three ages are INDEPENDENT ───────────────────────────────────
	//
	// It is not enough for each block to have a number: they have to come from
	// different stamps. This item checks that all three exist and that none is the
	// "never observed" marker after a full tick.
	wS, saude := pvxGET(t, r, "/api/proxmox")
	if wS.Code != 200 {
		t.Fatalf("GET /api/proxmox = %d", wS.Code)
	}
	h, _ := saude["hypervisor"].(map[string]any)
	idSaude, _ := h["age_seconds"].(float64)
	_, capOut := pvxGET(t, r, "/api/proxmox/storage")
	idCap, _ := capOut["age_seconds"].(float64)
	idZfs, _ := out["age_seconds"].(float64)
	if idSaude < 0 || idCap < 0 || idZfs < 0 {
		t.Errorf("ages = health %v, capacity %v, zpool %v — none may be -1 after a tick",
			idSaude, idCap, idZfs)
	}
	t.Logf("three independent ages: health %vs · capacity %vs · zpool %vs", idSaude, idCap, idZfs)

	// ── 5. what stays OUT of scope, measured and not assumed ────────────────
	w, _ = pvxGET(t, r, "/api/proxmox/apt")
	if w.Code != 404 {
		t.Errorf("/api/proxmox/apt = %d, want 404 — apt requires Sys.Modify and stays out of scope", w.Code)
	}

	t1 := time.Now().UTC()
	t.Logf("[t0=%s t1=%s] wave 2 live-proof window (%.1fs)",
		t0.Format(time.RFC3339), t1.Format(time.RFC3339), t1.Sub(t0).Seconds())
}

// humano formats bytes for the test log. It exists only here: production
// formatting belongs to the browser, and duplicating it on the server would
// create two truths about how a number is written.
func humano(v any) string {
	f, _ := v.(float64)
	u := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for f >= 1024 && i < len(u)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f%s", f, u[i])
}
