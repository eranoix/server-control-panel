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
// The central piece of scaffolding is cofreEspiao: it RECORDS every key read.
// Without it, the mutation this file exists to prevent would slip by unnoticed —
// swapping `pve_token_audit` for `pve_token_node_*` on the tasks route
// produces no error at all, it produces an EMPTY LIST. Measured:
// GET /nodes/pve/tasks?limit=50 with lab@pve!node-apps returns 200 with len=0,
// because Tasks.pm:40-45 requires Sys.Audit on /nodes and the LabOperador role does not.
// A test that only looked at the HTTP status would say "passed".

// --------------------------------------------------------------- doubles ----

// cofreEspiao is cofreFalso with a memory: it keeps the ORDER and the SET of the
// keys read, which is what makes the token choice verifiable by NAME.
type cofreEspiao struct {
	dados        map[string]string
	lidas        []string
	inalcancavel bool
}

func (c *cofreEspiao) Get(k string) (string, bool) {
	c.lidas = append(c.lidas, k)
	v, ok := c.dados[k]
	return v, ok
}
func (c *cofreEspiao) Delete(k string) error { delete(c.dados, k); return nil }

func (c *cofreEspiao) leu(chave string) bool {
	for _, k := range c.lidas {
		if k == chave {
			return true
		}
	}
	return false
}
func (c *cofreEspiao) leuAlgumaComPrefixo(pref string) string {
	for _, k := range c.lidas {
		if strings.HasPrefix(k, pref) {
			return k
		}
	}
	return ""
}

// ---------------------------------------------------------- scaffolding ----

func hipervisorDeTeste(agora int64) inventory.Hypervisor {
	return inventory.Hypervisor{
		Node:      "pve",
		Version:   inventory.Observe("pve-manager/9.2.2/abcdef", agora),
		Uptime:    inventory.Observe(int64(123456), agora),
		Load:      inventory.Observe([3]float64{1.14, 1.55, 1.70}, agora),
		MemTotal:  inventory.Observe(int64(67200000000), agora),
		MemUsed:   inventory.Observe(int64(40100000000), agora),
		RootTotal: inventory.Observe(int64(100000000000), agora),
		RootUsed:  inventory.Observe(int64(20000000000), agora),
		KSMShared: inventory.Observe(int64(4096), agora),
	}
}

// novoRouterProxmox assembles the router with inventory, hypervisor and spying vault.
func novoRouterProxmox(t *testing.T, cofre *cofreEspiao, fake *pveFalso) (*Router, *inventory.Store) {
	t.Helper()
	r, st := novoRouterDeNos(t, []inventory.Node{
		noDeTeste("lxc/207", "apps", 207, agoraDeTeste-10),
		noDeTeste("lxc/204", "lab", 204, agoraDeTeste-10),
	})
	if err := st.Replace(func(iv *inventory.Inventory) {
		iv.Hypervisor = hipervisorDeTeste(agoraDeTeste - 30)
	}); err != nil {
		t.Fatal(err)
	}
	r.nodeVaultFn = func() (nodeVault, error) {
		if cofre == nil || cofre.inalcancavel {
			return nil, fmt.Errorf("cofre fora do ar")
		}
		return cofre, nil
	}
	if fake != nil {
		r.pveDial = func(tokenValor string) (hypervisorOps, error) { return fake, nil }
	}
	return r, st
}

func cofrePadrao() *cofreEspiao {
	return &cofreEspiao{dados: map[string]string{
		"pve_token_audit":     "lab@pve!audit=s3cr3t",
		"pve_token_admin":     "lab@pve!admin=s3cr3t",
		"pve_token_node_apps": "lab@pve!node-apps=s3cr3t",
		"pve_token_node_lab":  "lab@pve!node-lab=s3cr3t",
	}}
}

func chamaPVX(t *testing.T, r *Router, metodo, caminho, corpo string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.handleProxmox(w, req(t, metodo, caminho, corpo))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// ------------------------------------------------------------- the tests ----

// 🔴 TestSaudeVemDoStoreSemChamarOHipervisor: health is a HEARTBEAT, and what
// collects it is the poller. If the route called the hypervisor, every screen
// load (and every 30 s refresh) would become a live request — and, worse, the age
// on display would stop being the stamp's and always read "0 s", hiding
// precisely the hypervisor that has gone mute.
func TestSaudeVemDoStoreSemChamarOHipervisor(t *testing.T) {
	r, _ := novoRouterProxmox(t, cofrePadrao(), nil)
	r.pveDial = func(tokenValor string) (hypervisorOps, error) {
		t.Fatal("GET /api/proxmox dialed the hypervisor — health must come from the STORE")
		return nil, nil
	}

	w, out := chamaPVX(t, r, http.MethodGet, "/api/proxmox", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	h, _ := out["hypervisor"].(map[string]any)
	if h == nil {
		t.Fatalf("response without hypervisor: %s", w.Body)
	}
	idade, temIdade := h["age_seconds"]
	if !temIdade {
		t.Fatal("hypervisor without age_seconds — the browser would go back to subtracting clocks")
	}
	if idade.(float64) != 30 {
		t.Errorf("age_seconds = %v, want 30 (stamp of agoraDeTeste-30)", idade)
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

// 🔴 TestTarefasUsamOTokenAudit is this file's central pin. It asserts by the KEY
// THAT WAS READ, not by the result: with the node token the answer would be 200
// with an empty list, and no assertion about the body would tell that apart from
// "there are no tasks".
func TestTarefasUsamOTokenAudit(t *testing.T) {
	rotas := []string{
		"/api/proxmox/tasks?errors=1&limit=10",
		"/api/proxmox/tasks/log?upid=UPID:pve:1:2:3:vzsnapshot:204:lab@pve!node-lab:",
		"/api/proxmox/disks",
		"/api/proxmox/permissions",
	}
	for _, rota := range rotas {
		t.Run(rota, func(t *testing.T) {
			cofre := cofrePadrao()
			fake := &pveFalso{
				tarefas:   []pve.Task{{UPID: "UPID:x", Type: "push_file", Status: "failed"}},
				linhasLog: []string{"linha"},
				discos:    []pve.Disk{{Model: "Lexar NQ790 1TB", Health: "PASSED"}},
				perms:     map[string]map[string]int{"/vms/204": {"VM.Audit": 1}},
			}
			r, _ := novoRouterProxmox(t, cofre, fake)

			w, _ := chamaPVX(t, r, http.MethodGet, rota, "")
			if w.Code != 200 {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			if !cofre.leu(pveSecretAudit) {
				t.Errorf("keys read = %v, want it to contain %q", cofre.lidas, pveSecretAudit)
			}
			if k := cofre.leuAlgumaComPrefixo(pveSecretNodePrefix); k != "" {
				t.Errorf("the route read %q — the node token returns 200 with len=0 on this route (Sys.Audit on /nodes), i.e. an empty screen LYING", k)
			}
		})
	}
}

// 🔴 TestSnapshotUsaOTokenDoNo is the other half: a guest mutation uses the
// NODE's credential (the token rule, proved live with the UPID carrying
// lab@pve!node-lab). Using audit here would give a 403 — noisy, but wrong all the
// same: what acts is not what audits.
func TestSnapshotUsaOTokenDoNo(t *testing.T) {
	casos := []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/proxmox/snapshots?node=lxc/207"},
		{http.MethodPost, "/api/proxmox/snapshots?node=lxc/207&name=pvx-teste"},
		{http.MethodDelete, "/api/proxmox/snapshots?node=lxc/207&name=pvx-teste"},
	}
	for _, tc := range casos {
		t.Run(tc.metodo, func(t *testing.T) {
			cofre := cofrePadrao()
			fake := &pveFalso{upid: "UPID:pve:1:2:3:vzsnapshot:207:lab@pve!node-apps:"}
			r, _ := novoRouterProxmox(t, cofre, fake)

			w, _ := chamaPVX(t, r, tc.metodo, tc.caminho, "")
			if w.Code != 200 {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			if !cofre.leu("pve_token_node_apps") {
				t.Errorf("keys read = %v, want to contain pve_token_node_apps (whoever acts is the node)", cofre.lidas)
			}
			if cofre.leu(pveSecretAudit) {
				t.Errorf("keys read = %v — snapshot does NOT use the audit token", cofre.lidas)
			}
		})
	}
}

// 🔴 TestSnapshotSoRespondeDepoisDoWaitTask is that PVE pitfall on this route:
// PVE's POST returns 200 with the UPID as soon as the TASK IS CREATED. Passing
// that 200 along would be the screen saying "snapshot ready" for a snapshot that
// may not even have started.
func TestSnapshotSoRespondeDepoisDoWaitTask(t *testing.T) {
	t.Run("ordem", func(t *testing.T) {
		var seen []string
		fake := &pveFalso{upid: "UPID:abc", seen: &seen}
		r, _ := novoRouterProxmox(t, cofrePadrao(), fake)

		w, out := chamaPVX(t, r, http.MethodPost, "/api/proxmox/snapshots?node=lxc/204&name=pvx-drill", "")
		if w.Code != 200 {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body)
		}
		quer := []string{"pve.snapcreate:pvx-drill", "pve.wait:UPID:abc"}
		if fmt.Sprint(seen) != fmt.Sprint(quer) {
			t.Fatalf("sequence = %v, want %v — the 200 came out before proof of completion", seen, quer)
		}
		if out["upid"] != "UPID:abc" {
			t.Errorf("response without the UPID: %s", w.Body)
		}
	})

	t.Run("wait failure becomes 502 with the exitstatus", func(t *testing.T) {
		fake := &pveFalso{
			upid: "UPID:abc",
			erroWait: &pve.Error{Kind: pve.KindHypervisor, Path: "/tasks",
				Body: "snapshot feature is not available"},
		}
		r, _ := novoRouterProxmox(t, cofrePadrao(), fake)

		w, _ := chamaPVX(t, r, http.MethodPost, "/api/proxmox/snapshots?node=lxc/204&name=pvx-drill", "")
		if w.Code != 502 {
			t.Fatalf("status = %d, want 502", w.Code)
		}
		if !strings.Contains(w.Body.String(), "snapshot feature is not available") {
			t.Errorf("body = %s — the PVE exitstatus is the only clue to the real reason", w.Body)
		}
	})
}

// TestNomeInvalidoNaoChegaAoHipervisor: the name comes from the SCREEN, and it is
// what builds the resource path on the hypervisor. The refusal happens before any
// call — and the test proves that by the absence of a mark on the double, not by the status.
func TestNomeInvalidoNaoChegaAoHipervisor(t *testing.T) {
	for _, nome := range []string{"1abc", "com espaço", "com/barra", ""} {
		t.Run(fmt.Sprintf("%q", nome), func(t *testing.T) {
			var seen []string
			fake := &pveFalso{upid: "UPID:abc", seen: &seen}
			r, _ := novoRouterProxmox(t, cofrePadrao(), fake)

			w, _ := chamaPVX(t, r, http.MethodPost,
				"/api/proxmox/snapshots?node=lxc/204&name="+url.QueryEscape(nome), "")
			if w.Code != 400 {
				t.Errorf("status = %d, want 400 (body=%s)", w.Code, w.Body)
			}
			if len(seen) != 0 {
				t.Errorf("the hypervisor was called (%v) with a rejected name", seen)
			}
		})
	}
}

// 🔴 TestCofreInalcancavelNaoViraListaVazia: a vault that is down and a credential
// that does not exist call for OPPOSITE actions from the operator. Collapsing the
// two into one empty answer is the collapse of handlers_ai.go:186, which already produced a defect.
func TestCofreInalcancavelNaoViraListaVazia(t *testing.T) {
	t.Run("inalcancavel = 503", func(t *testing.T) {
		cofre := cofrePadrao()
		cofre.inalcancavel = true
		r, _ := novoRouterProxmox(t, cofre, &pveFalso{})
		w, _ := chamaPVX(t, r, http.MethodGet, "/api/proxmox/tasks", "")
		if w.Code != 503 {
			t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "\"tasks\":[]") {
			t.Error("vault being down turned into an empty list — exactly the false-green that phase 7 forbade")
		}
	})
	t.Run("ausente = 409", func(t *testing.T) {
		cofre := &cofreEspiao{dados: map[string]string{}}
		r, _ := novoRouterProxmox(t, cofre, &pveFalso{})
		w, _ := chamaPVX(t, r, http.MethodGet, "/api/proxmox/tasks", "")
		if w.Code != 409 {
			t.Fatalf("status = %d, want 409 (body=%s)", w.Code, w.Body)
		}
	})
}

// TestLimiteDeTarefasEDoServidor: the client's `limit` is a suggestion. What
// reaches the hypervisor goes through internal/pve's clamp; here it is proved that
// the handler does not invent a parallel path.
func TestLimiteDeTarefasEDoServidor(t *testing.T) {
	var seen []string
	fake := &pveFalso{seen: &seen}
	r, _ := novoRouterProxmox(t, cofrePadrao(), fake)

	if w, _ := chamaPVX(t, r, http.MethodGet, "/api/proxmox/tasks?limit=99999&errors=1", ""); w.Code != 200 {
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

// TestMetodoErradoDaCod405, and an unknown route gives 404: together the two stop
// a new verb appearing by accident on a route that touches the hypervisor.
func TestMetodoErradoDaCod405(t *testing.T) {
	r, _ := novoRouterProxmox(t, cofrePadrao(), &pveFalso{})
	casos := []struct {
		metodo, caminho string
		quer            int
	}{
		{http.MethodPost, "/api/proxmox", 405},
		{http.MethodDelete, "/api/proxmox/tasks", 405},
		{http.MethodPut, "/api/proxmox/snapshots?node=lxc/204&name=x", 405},
		{http.MethodGet, "/api/proxmox/nao-existe", 404},
		{http.MethodGet, "/api/proxmox/snapshots?node=lxc/999", 404},
	}
	for _, tc := range casos {
		t.Run(tc.metodo+" "+tc.caminho, func(t *testing.T) {
			w, _ := chamaPVX(t, r, tc.metodo, tc.caminho, "")
			if w.Code != tc.quer {
				t.Errorf("status = %d, want %d (body=%s)", w.Code, tc.quer, w.Body)
			}
		})
	}
}

// TestPermissoesExplicamAOnda2: the permissions field exists so the screen can
// say, by MEASUREMENT, why storage capacity / backup evidence / zpool are
// missing. Without it the block would be decoration.
func TestPermissoesExplicamAOnda2(t *testing.T) {
	fake := &pveFalso{perms: map[string]map[string]int{
		"/vms/204": {"VM.Audit": 1, "VM.Snapshot": 1},
		"/nodes":   {"Sys.Audit": 1},
	}}
	r, _ := novoRouterProxmox(t, cofrePadrao(), fake)
	w, out := chamaPVX(t, r, http.MethodGet, "/api/proxmox/permissions", "")
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if out["storage_visivel"] != false {
		t.Errorf("storage_visivel = %v, want false — it's what explains wave 2 on the screen", out["storage_visivel"])
	}
	if m, _ := out["permissions"].(map[string]any); len(m) != 2 {
		t.Errorf("permissions = %v", out["permissions"])
	}
}

// TestSaudeNuncaObservadaDizIsso: dashboard just up, poller with no tick yet.
// The screen has to say "never observed" (-1), never "0 s ago".
func TestSaudeNuncaObservadaDizIsso(t *testing.T) {
	r, _ := novoRouterDeNos(t, nil)
	r.nodeVaultFn = func() (nodeVault, error) { return cofrePadrao(), nil }
	r.inventoryNow = func() time.Time { return time.Unix(agoraDeTeste, 0) }

	w, out := chamaPVX(t, r, http.MethodGet, "/api/proxmox", "")
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

// 🔴 TestRollbackSoRespondeDepoisDoWaitTask is that PVE pitfall MEASURED on the
// most destructive route of the dashboard. Against the home hypervisor, a
// rollback to a snapshot that DOES NOT EXIST returned:
//
//	HTTP 200 {"data":"UPID:pve:…:vzrollback:204:lab@pve!node-lab:"}
//
// and only the task status told the truth:
//
//	status=stopped exitstatus="snapshot '<name>' does not exist"
//
// Passing that 200 along would be the screen saying "restored" for a rollback that
// never happened — and, worse, on a guest the operator would then believe to be
// in an earlier state.
func TestRollbackSoRespondeDepoisDoWaitTask(t *testing.T) {
	t.Run("ordem", func(t *testing.T) {
		var seen []string
		fake := &pveFalso{upid: "UPID:roll", seen: &seen}
		r, _ := novoRouterProxmox(t, cofrePadrao(), fake)

		w, out := chamaPVX(t, r, http.MethodPost,
			"/api/proxmox/snapshots/rollback?node=lxc/204&name=antes-do-cutover", "")
		if w.Code != 200 {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body)
		}
		quer := []string{"pve.snaprollback:antes-do-cutover", "pve.wait:UPID:roll"}
		if fmt.Sprint(seen) != fmt.Sprint(quer) {
			t.Fatalf("sequence = %v, want %v — the 200 came out before proof of completion", seen, quer)
		}
		if out["action"] != "rollback" || out["upid"] != "UPID:roll" {
			t.Errorf("response = %s", w.Body)
		}
	})

	t.Run("wait failure becomes 502 with the exitstatus", func(t *testing.T) {
		fake := &pveFalso{upid: "UPID:roll", erroWait: &pve.Error{Kind: pve.KindHypervisor, Path: "/tasks",
			Body: "snapshot 'antes-do-cutover' does not exist"}}
		r, _ := novoRouterProxmox(t, cofrePadrao(), fake)

		w, _ := chamaPVX(t, r, http.MethodPost,
			"/api/proxmox/snapshots/rollback?node=lxc/204&name=antes-do-cutover", "")
		if w.Code != 502 {
			t.Fatalf("status = %d, want 502", w.Code)
		}
		if !strings.Contains(w.Body.String(), "does not exist") {
			t.Errorf("body = %s — the PVE exitstatus is the only clue to the real reason", w.Body)
		}
	})
}

// TestRollbackUsaOTokenDoNo — the token rule again: what acts on a guest is THAT
// node's credential. The audit token got a 403 in the measurement.
func TestRollbackUsaOTokenDoNo(t *testing.T) {
	cofre := cofrePadrao()
	r, _ := novoRouterProxmox(t, cofre, &pveFalso{upid: "UPID:roll"})

	w, _ := chamaPVX(t, r, http.MethodPost,
		"/api/proxmox/snapshots/rollback?node=lxc/207&name=antes-do-cutover", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if !cofre.leu("pve_token_node_apps") {
		t.Errorf("keys read = %v, want to contain pve_token_node_apps", cofre.lidas)
	}
	if cofre.leu(pveSecretAudit) {
		t.Errorf("keys read = %v — rollback does NOT use the audit token (403 measured)", cofre.lidas)
	}
}

// 🔴 TestRollbackSoAceitaPOST: a rollback triggerable by GET would be triggerable
// by browser prefetch, by a crawler and by a link pasted into a chat.
func TestRollbackSoAceitaPOST(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		var seen []string
		fake := &pveFalso{upid: "UPID:roll", seen: &seen}
		r, _ := novoRouterProxmox(t, cofrePadrao(), fake)
		w, _ := chamaPVX(t, r, m, "/api/proxmox/snapshots/rollback?node=lxc/204&name=antes-do-cutover", "")
		if w.Code != 405 {
			t.Errorf("%s: status = %d, want 405", m, w.Code)
		}
		if len(seen) != 0 {
			t.Errorf("%s: the hypervisor was called (%v)", m, seen)
		}
	}
}

// TestRollbackNomeInvalidoNaoChegaAoHipervisor: the name comes from the screen and
// chooses WHICH state the guest will take on. Refused before dialling, proved by
// the absence of a mark on the double.
func TestRollbackNomeInvalidoNaoChegaAoHipervisor(t *testing.T) {
	for _, nome := range []string{"", "1abc", "com espaço", "com/barra", "../lxc/207"} {
		var seen []string
		fake := &pveFalso{upid: "UPID:roll", seen: &seen}
		r, _ := novoRouterProxmox(t, cofrePadrao(), fake)
		w, _ := chamaPVX(t, r, http.MethodPost,
			"/api/proxmox/snapshots/rollback?node=lxc/204&name="+url.QueryEscape(nome), "")
		if w.Code != 400 {
			t.Errorf("%q: status = %d, want 400 (body=%s)", nome, w.Code, w.Body)
		}
		if len(seen) != 0 {
			t.Errorf("%q: the hypervisor was called (%v) with a rejected name", nome, seen)
		}
	}
}

// 🔴 TestNenhumaRotaOfereceSuspend. Measured on this host:
// `vzsuspend 204` ended in `lxc-checkpoint -n 204 -s -D /var/lib/vz/dump
// failed: exit code 1` (CRIU) and the CT stayed `running`. A button that always
// errors trains the operator to ignore errors — and the next error, the real one,
// goes unnoticed. Suspend stays OUT until CRIU works on this host, and this test
// is what stops it coming back by absent-mindedness.
func TestNenhumaRotaOfereceSuspend(t *testing.T) {
	for _, caminho := range []string{
		"/api/proxmox/snapshots/suspend?node=lxc/204",
		"/api/proxmox/suspend?node=lxc/204",
	} {
		var seen []string
		fake := &pveFalso{upid: "UPID:x", seen: &seen}
		r, _ := novoRouterProxmox(t, cofrePadrao(), fake)
		w, _ := chamaPVX(t, r, http.MethodPost, caminho, "")
		if w.Code != 404 {
			t.Errorf("%s: status = %d, want 404 — suspend is broken on this host (CRIU)", caminho, w.Code)
		}
		if len(seen) != 0 {
			t.Errorf("%s: it called the hypervisor (%v)", caminho, seen)
		}
	}
}
