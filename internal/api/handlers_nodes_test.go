package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
)

const agoraDeTeste = 1800000000

// ---------------------------------------------------------------- duplos ----

// cofreFalso records every call in `seen`, which is what makes the ORDER of
// revocation verifiable. Modelled on privateaiapi_test.go.
type cofreFalso struct {
	dados        map[string]string
	seen         *[]string
	erroDelete   error
	ressuscitar  bool // simulates the concurrent write that resurrects a deleted vault key
	deleteChamou bool
}

func (c *cofreFalso) Get(k string) (string, bool) {
	if c.deleteChamou && c.ressuscitar {
		return "ressuscitado", true
	}
	v, ok := c.dados[k]
	return v, ok
}

func (c *cofreFalso) Delete(k string) error {
	c.deleteChamou = true
	if c.seen != nil {
		*c.seen = append(*c.seen, "vault.delete")
	}
	if c.erroDelete != nil {
		return c.erroDelete
	}
	delete(c.dados, k)
	return nil
}

// pveFalso is the stand-in hypervisor. `papel` says which token opened it, so
// the test can prove that revocation uses the ADMIN token and confirmation uses
// the OPERATIONAL one.
type pveFalso struct {
	jobsBackup []pve.JobDeBackup
	serie      []pve.PontoRRD
	ifaces     []pve.Interface
	dns        pve.DNSInfo
	hora       pve.TimeInfo
	certs      []pve.Certificado
	pacotes    []pve.Pacote
	syslog     []pve.LinhaSyslog
	frescor    map[string]pve.FrescorDeBackup
	zpools     []pve.ZPool
	topologias map[string]pve.ZPoolTopologia
	papel      string
	seen       *[]string
	upid       string
	proximoID  int
	descricao  string
	// tokenUsado stamps WHICH credential dialled out. Without it there is no way
	// to prove that cloning uses the panel's and rebooting uses the node's — and
	// using the wrong one gives no pretty error: it gives a 403 from the
	// hypervisor, which arrives as "it failed".
	tokenUsado   string
	erroVerbo    error
	erroWait     error
	erroDelete   error
	aindaVivo    bool // the operational token was NOT actually revoked
	tokensMortos map[string]bool
	tokens       []pve.TokenInfo
	erroLista    error

	// The routes behind the Proxmox tab. A single double for both handlers: two
	// interfaces would be two truths about what the panel may do on the
	// hypervisor.
	status     pve.NodeStatus
	erroStatus error
	tarefas    []pve.Task
	linhasLog  []string
	discos     []pve.Disk
	perms      map[string]map[string]int
	snaps      []pve.Snapshot

	// Console and rollback. `console` is the connection the double returns;
	// `chamouConsole` exists because several tests need to prove the hypervisor
	// was NOT dialled — and an absent call leaves no mark in `seen`, by
	// definition.
	console       *consoleFalso
	erroConsole   error
	chamouConsole bool
}

func (p *pveFalso) NodePower(ctx context.Context, node string, cmd pve.ComandoDeEnergia) (string, error) {
	p.marca("pve.node-power:" + string(cmd) + ":" + node)
	if p.erroVerbo != nil {
		return "", p.erroVerbo
	}
	return "UPID:pve:node-power", nil
}

// Maintenance. The fake COUNTS the calls instead of executing them: that is how
// a pin can assert "no clone was fired" without ever cloning anything for real.
func (p *pveFalso) Reboot(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.marca(fmt.Sprintf("pve.reboot:%s/%d", typ, vmid))
	if p.erroVerbo != nil {
		return "", p.erroVerbo
	}
	return "UPID:pve:reboot", nil
}

func (p *pveFalso) Descricao(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.marca(fmt.Sprintf("pve.descricao:%s/%d", typ, vmid))
	if p.erroVerbo != nil {
		return "", p.erroVerbo
	}
	return p.descricao, nil
}

func (p *pveFalso) SetDescricao(ctx context.Context, node string, vmid int, typ, texto string) error {
	p.marca(fmt.Sprintf("pve.set-descricao:%s/%d:%dbytes", typ, vmid, len(texto)))
	if p.erroVerbo != nil {
		return p.erroVerbo
	}
	p.descricao = texto
	return nil
}

func (p *pveFalso) NextID(ctx context.Context) (int, error) {
	p.marca("pve.nextid")
	if p.erroVerbo != nil {
		return 0, p.erroVerbo
	}
	if p.proximoID > 0 {
		return p.proximoID, nil
	}
	return 991, nil
}

func (p *pveFalso) Clone(ctx context.Context, node string, vmid int, typ string, novoID int, nome, snapname string) (string, error) {
	p.marca(fmt.Sprintf("pve.clone:%s/%d->%d:%s:snap=%s", typ, vmid, novoID, nome, snapname))
	if p.erroVerbo != nil {
		return "", p.erroVerbo
	}
	return "UPID:pve:clone", nil
}

func (p *pveFalso) VZDump(ctx context.Context, node string, vmid int, storage, modo, compress string) (string, error) {
	p.marca(fmt.Sprintf("pve.vzdump:%d:%s:%s:%s", vmid, storage, modo, compress))
	if p.erroVerbo != nil {
		return "", p.erroVerbo
	}
	return "UPID:pve:vzdump", nil
}

func (p *pveFalso) ConsoleAttachNode(ctx context.Context, node string) (pve.ConsoleConn, string, error) {
	p.chamouConsole = true
	p.marca("pve.console-host:" + node)
	if p.erroConsole != nil {
		return nil, "", p.erroConsole
	}
	return p.console, p.upid, nil
}

func (p *pveFalso) ConsoleAttach(ctx context.Context, node string, vmid int, typ string) (pve.ConsoleConn, string, error) {
	p.chamouConsole = true
	p.marca(fmt.Sprintf("pve.console:%s/%d", typ, vmid))
	if p.erroConsole != nil {
		return nil, "", p.erroConsole
	}
	return p.console, p.upid, nil
}

func (p *pveFalso) SnapshotRollback(ctx context.Context, node string, vmid int, typ, nome string) (string, error) {
	p.marca("pve.snaprollback:" + nome)
	return p.upid, p.erroVerbo
}

func (p *pveFalso) NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error) {
	p.marca("pve.status:" + node)
	return p.status, p.erroStatus
}
func (p *pveFalso) TaskList(ctx context.Context, node string, opt pve.TaskListOptions) ([]pve.Task, error) {
	p.marca(fmt.Sprintf("pve.tasks:%s:limit=%d:errors=%v", node, opt.Limit, opt.ErrorsOnly))
	return p.tarefas, p.erroVerbo
}
func (p *pveFalso) TaskLog(ctx context.Context, node, upid string) ([]string, error) {
	p.marca("pve.tasklog:" + upid)
	return p.linhasLog, p.erroVerbo
}
func (p *pveFalso) DisksList(ctx context.Context, node string) ([]pve.Disk, error) {
	p.marca("pve.disks:" + node)
	return p.discos, p.erroVerbo
}
func (p *pveFalso) ZFSList(ctx context.Context, node string) ([]pve.ZPool, error) {
	p.marca("pve.zfslist:" + node)
	return p.zpools, p.erroVerbo
}
func (p *pveFalso) RRDNode(ctx context.Context, node string, j pve.JanelaRRD) ([]pve.PontoRRD, error) {
	p.marca("pve.rrdnode:" + string(j))
	return p.serie, p.erroVerbo
}
func (p *pveFalso) RRDGuest(ctx context.Context, node string, vmid int, typ string, j pve.JanelaRRD) ([]pve.PontoRRD, error) {
	p.marca("pve.rrdguest:" + typ + "/" + strconv.Itoa(vmid) + ":" + string(j))
	return p.serie, p.erroVerbo
}
func (p *pveFalso) Network(ctx context.Context, node string) ([]pve.Interface, error) {
	p.marca("pve.network")
	return p.ifaces, p.erroVerbo
}
func (p *pveFalso) DNS(ctx context.Context, node string) (pve.DNSInfo, error) {
	p.marca("pve.dns")
	return p.dns, p.erroVerbo
}
func (p *pveFalso) Time(ctx context.Context, node string) (pve.TimeInfo, error) {
	p.marca("pve.time")
	return p.hora, p.erroVerbo
}
func (p *pveFalso) Certificados(ctx context.Context, node string) ([]pve.Certificado, error) {
	p.marca("pve.certs")
	return p.certs, p.erroVerbo
}
func (p *pveFalso) Pacotes(ctx context.Context, node string) ([]pve.Pacote, error) {
	p.marca("pve.pacotes")
	return p.pacotes, p.erroVerbo
}
func (p *pveFalso) Syslog(ctx context.Context, node string, limite int) ([]pve.LinhaSyslog, error) {
	p.marca("pve.syslog:" + strconv.Itoa(limite))
	return p.syslog, p.erroVerbo
}
func (p *pveFalso) JobsDeBackup(ctx context.Context) ([]pve.JobDeBackup, error) {
	p.marca("pve.jobs-backup")
	return p.jobsBackup, p.erroVerbo
}

func (p *pveFalso) BackupsDoDatastore(ctx context.Context, node, storage string) (pve.FrescorDeBackup, error) {
	p.marca("pve.backups:" + storage)
	if p.erroVerbo != nil {
		return pve.FrescorDeBackup{}, p.erroVerbo
	}
	return p.frescor[storage], nil
}
func (p *pveFalso) ZFSTopologia(ctx context.Context, node, pool string) (pve.ZPoolTopologia, error) {
	p.marca("pve.zfstopologia:" + pool)
	if p.erroVerbo != nil {
		return pve.ZPoolTopologia{}, p.erroVerbo
	}
	return p.topologias[pool], nil
}
func (p *pveFalso) Permissions(ctx context.Context) (map[string]map[string]int, error) {
	p.marca("pve.permissions")
	return p.perms, p.erroVerbo
}
func (p *pveFalso) SnapshotList(ctx context.Context, node string, vmid int, typ string) ([]pve.Snapshot, error) {
	p.marca(fmt.Sprintf("pve.snaplist:%s/%d", typ, vmid))
	return p.snaps, p.erroVerbo
}
func (p *pveFalso) SnapshotCreate(ctx context.Context, node string, vmid int, typ, nome, descricao string) (string, error) {
	p.marca("pve.snapcreate:" + nome)
	return p.upid, p.erroVerbo
}
func (p *pveFalso) SnapshotDelete(ctx context.Context, node string, vmid int, typ, nome string) (string, error) {
	p.marca("pve.snapdelete:" + nome)
	return p.upid, p.erroVerbo
}

func (p *pveFalso) marca(ev string) {
	if p.tokenUsado != "" {
		if i := strings.IndexByte(p.tokenUsado, '='); i > 0 {
			ev += " token=" + p.tokenUsado[:i]
		} else {
			ev += " token=" + p.tokenUsado
		}
	}
	if p.seen != nil {
		*p.seen = append(*p.seen, ev)
	}
}

func (p *pveFalso) Start(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.marca(fmt.Sprintf("pve.start:%s/%d", typ, vmid))
	return p.upid, p.erroVerbo
}
func (p *pveFalso) Stop(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.marca(fmt.Sprintf("pve.stop:%s/%d", typ, vmid))
	return p.upid, p.erroVerbo
}
func (p *pveFalso) Shutdown(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.marca(fmt.Sprintf("pve.shutdown:%s/%d", typ, vmid))
	return p.upid, p.erroVerbo
}
func (p *pveFalso) WaitTask(ctx context.Context, node, upid string) error {
	p.marca("pve.wait:" + upid)
	return p.erroWait
}
func (p *pveFalso) DeleteToken(ctx context.Context, user, tokenID string) error {
	p.marca("pve.delete")
	if p.erroDelete != nil {
		return p.erroDelete
	}
	if p.tokensMortos != nil {
		p.tokensMortos[tokenID] = true
	}
	return nil
}

func (p *pveFalso) ListTokens(ctx context.Context, user string) ([]pve.TokenInfo, error) {
	p.marca("pve.listtokens")
	if p.erroLista != nil {
		return nil, p.erroLista
	}
	return p.tokens, nil
}

// ClusterResources is the PROOF CALL for revocation: with the operational token
// already revoked, it has to return 401.
func (p *pveFalso) ClusterResources(ctx context.Context) ([]pve.Resource, error) {
	p.marca("pve.confirm401")
	if p.aindaVivo {
		return nil, nil // the token still works — revocation NOT proven
	}
	return nil, &pve.Error{Kind: pve.KindNoCredential, Status: 401, Path: "/cluster/resources"}
}

// ------------------------------------------------------------- andaimes ----

func novoRouterDeNos(t *testing.T, nos []inventory.Node) (*Router, *inventory.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := inventory.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(nos) > 0 {
		if err := st.Replace(func(iv *inventory.Inventory) { iv.Nodes = nos }); err != nil {
			t.Fatal(err)
		}
	}
	r := &Router{cfg: &config.Config{DataDir: dir, Primary: "sam"}}
	r.inventoryStore = st
	r.inventoryNow = func() time.Time { return time.Unix(agoraDeTeste, 0) }
	return r, st
}

func req(t *testing.T, metodo, caminho, corpo string) *http.Request {
	t.Helper()
	rq := httptest.NewRequest(metodo, caminho, strings.NewReader(corpo))
	return rq.WithContext(auth.WithUser(rq.Context(), "sam"))
}

func chama(t *testing.T, r *Router, metodo, caminho, corpo string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.handleNodes(w, req(t, metodo, caminho, corpo))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func noDeTeste(id, nome string, vmid int, observadoEm int64) inventory.Node {
	return inventory.Node{
		ID: id, Name: nome, VMID: vmid, Kind: inventory.NodeKindGuest,
		Transport: inventory.TransportPVEAPI,
		Status:    inventory.Observe("running", observadoEm),
		Uptime:    inventory.Observe(int64(100), observadoEm),
		Credential: inventory.Credential{
			TokenID: "lab@pve!node-" + nome, Expire: agoraDeTeste + 30*86400, State: inventory.CredOK,
		},
	}
}

// ------------------------------------------------------------- os testes ----

// 🔴 TestNodesList is the API-side pin: EVERY entry carries age_seconds and
// observed_at. Without those two fields in the payload the browser goes back to
// computing the age from ITS OWN clock — and this project spans two machines and
// a tailnet, where the client's clock is not a controlled variable.
func TestNodesList(t *testing.T) {
	r, _ := novoRouterDeNos(t, []inventory.Node{
		noDeTeste("lxc/207", "apps", 207, agoraDeTeste-10),  // fresco
		noDeTeste("qemu/208", "dev", 208, agoraDeTeste-600), // velho
	})
	r.nodeVaultFn = func() (nodeVault, error) {
		return &cofreFalso{dados: map[string]string{
			"pve_token_node_apps": "lab@pve!node-apps=s",
			"pve_token_node_dev":  "lab@pve!node-dev=s",
		}}, nil
	}

	w, out := chama(t, r, http.MethodGet, "/api/nodes", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	nodes, _ := out["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatal("response with no nodes")
	}
	// Itera TODAS as entradas — amostrar a primeira deixaria a segunda mentir.
	for i, raw := range nodes {
		n, _ := raw.(map[string]any)
		if _, ok := n["age_seconds"]; !ok {
			t.Errorf("entry %d missing age_seconds: %v", i, n)
		}
		if _, ok := n["stale"]; !ok {
			t.Errorf("entry %d missing stale: %v", i, n)
		}
		st, _ := n["status"].(map[string]any)
		if _, ok := st["observed_at"]; !ok {
			t.Errorf("entry %d missing status.observed_at: %v", i, n)
		}
	}
	porID := map[string]map[string]any{}
	for _, raw := range nodes {
		n := raw.(map[string]any)
		porID[n["id"].(string)] = n
	}
	if got := porID["lxc/207"]["age_seconds"].(float64); got != 10 {
		t.Errorf("age of the fresh one = %v, want 10", got)
	}
	if porID["lxc/207"]["stale"].(bool) {
		t.Error("the 10s node showed up as expired")
	}
	if got := porID["qemu/208"]["age_seconds"].(float64); got != 600 {
		t.Errorf("age of the old one = %v, want 600", got)
	}
	if !porID["qemu/208"]["stale"].(bool) {
		t.Error("the 600s node did NOT show up as expired (TTL 90s)")
	}
	if out["vault"] != vaultOK {
		t.Errorf("vault = %v, want ok", out["vault"])
	}
}

// 🔴 TestNodesVaultStates is the antidote to handlers_ai.go:186, which returns
// `""` both for "vault unreachable" and for "key missing". Here the two produce
// DIFFERENT strings — and the inventory stays readable in both.
func TestNodesVaultStates(t *testing.T) {
	t.Run("vault unreachable: the list STILL responds 200", func(t *testing.T) {
		r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste-10)})
		r.nodeVaultFn = func() (nodeVault, error) { return nil, errors.New("cofre fora do ar") }

		w, out := chama(t, r, http.MethodGet, "/api/nodes", "")
		if w.Code != 200 {
			t.Fatalf("dead vault wiped the inventory: status %d", w.Code)
		}
		if out["vault"] != vaultInalcancavel {
			t.Fatalf("vault = %v, want %q", out["vault"], vaultInalcancavel)
		}
		// And we do NOT invent "ausente" just because we could not look.
		n := out["nodes"].([]any)[0].(map[string]any)
		cred := n["credential"].(map[string]any)
		if cred["state"] == inventory.CredAusente {
			t.Error("unreachable vault was reported as credential MISSING — that is the handlers_ai.go:186 collapse")
		}
	})

	t.Run("key missing: credential missing, vault ok", func(t *testing.T) {
		r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste-10)})
		r.nodeVaultFn = func() (nodeVault, error) { return &cofreFalso{dados: map[string]string{}}, nil }

		w, out := chama(t, r, http.MethodGet, "/api/nodes", "")
		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
		if out["vault"] != vaultOK {
			t.Errorf("vault = %v, want ok (the vault answered; it is the KEY that does not exist)", out["vault"])
		}
		n := out["nodes"].([]any)[0].(map[string]any)
		if got := n["credential"].(map[string]any)["state"]; got != inventory.CredAusente {
			t.Errorf("state = %v, want %q", got, inventory.CredAusente)
		}
	})

	t.Run("power with vault unreachable = 503, with key missing = 409", func(t *testing.T) {
		r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste)})
		r.pveDial = func(string) (hypervisorOps, error) { return &pveFalso{upid: "UPID:x"}, nil }

		r.nodeVaultFn = func() (nodeVault, error) { return nil, errors.New("fora do ar") }
		w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"start"}`)
		if w.Code != 503 {
			t.Errorf("vault unreachable → %d, want 503 (body %s)", w.Code, w.Body)
		}

		r.nodeVaultFn = func() (nodeVault, error) { return &cofreFalso{dados: map[string]string{}}, nil }
		w, _ = chama(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"start"}`)
		if w.Code != 409 {
			t.Errorf("key missing → %d, want 409 (body %s)", w.Code, w.Body)
		}
	})
}

// TestNodesStoreNil: a missing subsystem answers 503, in the shape of
// deployStoreOrNil — never a panic, never a lying empty list.
func TestNodesStoreNil(t *testing.T) {
	r := &Router{cfg: &config.Config{DataDir: t.TempDir(), Primary: "sam"}}
	w, _ := chama(t, r, http.MethodGet, "/api/nodes", "")
	if w.Code != 503 {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

// 🔴 TestPowerWaitsUPID: the 200 from the PVE POST means "task created", never
// "the VM came up". The response only leaves after WaitTask.
func TestPowerWaitsUPID(t *testing.T) {
	t.Run("success waits for the task", func(t *testing.T) {
		var seen []string
		r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste)})
		r.nodeVaultFn = func() (nodeVault, error) {
			return &cofreFalso{dados: map[string]string{"pve_token_node_apps": "lab@pve!node-apps=s"}}, nil
		}
		r.pveDial = func(string) (hypervisorOps, error) {
			return &pveFalso{seen: &seen, upid: "UPID:pve:1:start"}, nil
		}
		w, out := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"start"}`)
		if w.Code != 200 {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
		if out["upid"] != "UPID:pve:1:start" {
			t.Errorf("upid = %v, want the hypervisor's", out["upid"])
		}
		quer := []string{"pve.start:lxc/207", "pve.wait:UPID:pve:1:start"}
		if fmt.Sprint(seen) != fmt.Sprint(quer) {
			t.Fatalf("sequence = %v, want %v (WaitTask is mandatory)", seen, quer)
		}
	})

	t.Run("a task that ends in error does NOT become 200", func(t *testing.T) {
		r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste)})
		r.nodeVaultFn = func() (nodeVault, error) {
			return &cofreFalso{dados: map[string]string{"pve_token_node_apps": "lab@pve!node-apps=s"}}, nil
		}
		r.pveDial = func(string) (hypervisorOps, error) {
			return &pveFalso{upid: "UPID:x", erroWait: &pve.Error{
				Kind: pve.KindHypervisor, Path: "/tasks", Body: "command 'lxc-start' failed with exit code 1",
			}}, nil
		}
		w, _ := chama(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"start"}`)
		if w.Code != 502 {
			t.Fatalf("status = %d, want 502", w.Code)
		}
		if !strings.Contains(w.Body.String(), "lxc-start") {
			t.Fatalf("the body hides PVE's exitstatus: %s", w.Body)
		}
	})
}

// 🔴 TestRevokeOrder is the central proof of the revocation ordering. The
// sequence is compared by EQUALITY, not by "contains": any permutation fails.
//
// The order is not a matter of taste. Inverted, it produces the worst possible
// state — a clean vault with the token still ALIVE on the hypervisor, an orphan
// credential nobody can revoke any more because nobody knows it exists.
func TestRevokeOrder(t *testing.T) {
	var seen []string
	r, st := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste)})
	cofre := &cofreFalso{seen: &seen, dados: map[string]string{
		"pve_token_admin":     "lab@pve!admin=a",
		"pve_token_node_apps": "lab@pve!node-apps=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return cofre, nil }
	r.pveDial = func(valor string) (hypervisorOps, error) {
		papel := "operacional"
		if strings.HasPrefix(valor, "lab@pve!admin") {
			papel = "admin"
		}
		return &pveFalso{papel: papel, seen: &seen}, nil
	}

	w, out := chama(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}

	quer := []string{"pve.delete", "pve.confirm401", "vault.delete"}
	// The recheck is a Get, which does not enter `seen` (only mutations do); its
	// proof is the resurrection test below.
	if fmt.Sprint(seen) != fmt.Sprint(quer) {
		t.Fatalf("SEQUENCE = %v, want EXACTLY %v", seen, quer)
	}
	if _, ainda := cofre.dados["pve_token_node_apps"]; ainda {
		t.Error("the key is still in the vault")
	}
	if fmt.Sprint(out["passos"]) != fmt.Sprint([]any{"pve.delete", "pve.confirm401", "vault.delete", "vault.recheck"}) {
		t.Errorf("steps in the response = %v", out["passos"])
	}
	// The screen shows the real state IMMEDIATELY, without waiting for the next tick.
	inv, _ := st.Snapshot()
	if inv.Nodes[0].Credential.State != inventory.CredRevogada {
		t.Errorf("state in the inventory = %q, want revoked", inv.Nodes[0].Credential.State)
	}
}

// TestRevokeExigeProva401: if the revoked token STILL answers, the revocation
// did not happen — and the vault must not be touched. Without this step the
// panel would say "revoked" on the strength of its own optimism.
func TestRevokeExigeProva401(t *testing.T) {
	var seen []string
	r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste)})
	cofre := &cofreFalso{seen: &seen, dados: map[string]string{
		"pve_token_admin":     "lab@pve!admin=a",
		"pve_token_node_apps": "lab@pve!node-apps=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return cofre, nil }
	r.pveDial = func(valor string) (hypervisorOps, error) {
		return &pveFalso{seen: &seen, aindaVivo: !strings.HasPrefix(valor, "lab@pve!admin")}, nil
	}

	w, _ := chama(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", "")
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pve.confirm401") {
		t.Errorf("the body does not name the step that failed: %s", w.Body)
	}
	if _, sumiu := cofre.dados["pve_token_node_apps"]; !sumiu {
		t.Error("the vault was touched without proof of the 401")
	}
}

// 🔴 TestRevokeRessurreicao covers the resurrection trap: internal/secrets does
// a read-modify-write of the WHOLE map without flock, so a concurrent write can
// bring the key back after the Delete. The recheck exists for that.
func TestRevokeRessurreicao(t *testing.T) {
	r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste)})
	cofre := &cofreFalso{ressuscitar: true, dados: map[string]string{
		"pve_token_admin":     "lab@pve!admin=a",
		"pve_token_node_apps": "lab@pve!node-apps=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return cofre, nil }
	r.pveDial = func(string) (hypervisorOps, error) { return &pveFalso{}, nil }

	w, _ := chama(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", "")
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), "vault.recheck") {
		t.Errorf("the resurrection went through quietly: %s", w.Body)
	}
}

// TestRevokePVEFailureKeepsVault: if the DELETE on the hypervisor fails, the
// vault is NOT touched — and the response says which step failed.
func TestRevokePVEFailureKeepsVault(t *testing.T) {
	r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste)})
	cofre := &cofreFalso{dados: map[string]string{
		"pve_token_admin":     "lab@pve!admin=a",
		"pve_token_node_apps": "lab@pve!node-apps=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return cofre, nil }
	r.pveDial = func(string) (hypervisorOps, error) {
		return &pveFalso{erroDelete: &pve.Error{Kind: pve.KindForbidden, Status: 403, Path: "/access"}}, nil
	}

	w, _ := chama(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", "")
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pve.delete") {
		t.Errorf("the body does not name the step: %s", w.Body)
	}
	if _, ainda := cofre.dados["pve_token_node_apps"]; !ainda {
		t.Error("the vault was touched despite the failure on the hypervisor")
	}
}

// 🔴 TestRevokeIsolation is the screen-side half of the isolation rule: revoking
// ONE node must not take the others down. Tokens are per node precisely so the
// blast radius of a revocation is a single node.
func TestRevokeIsolation(t *testing.T) {
	r, _ := novoRouterDeNos(t, []inventory.Node{
		noDeTeste("lxc/207", "apps", 207, agoraDeTeste),
		noDeTeste("qemu/208", "dev", 208, agoraDeTeste),
	})
	cofre := &cofreFalso{dados: map[string]string{
		"pve_token_admin":     "lab@pve!admin=a",
		"pve_token_node_apps": "lab@pve!node-apps=s",
		"pve_token_node_dev":  "lab@pve!node-dev=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return cofre, nil }
	r.pveDial = func(string) (hypervisorOps, error) { return &pveFalso{}, nil }

	if w, _ := chama(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", ""); w.Code != 200 {
		t.Fatalf("revocation failed: %d %s", w.Code, w.Body)
	}

	_, out := chama(t, r, http.MethodGet, "/api/nodes", "")
	estados := map[string]string{}
	for _, raw := range out["nodes"].([]any) {
		n := raw.(map[string]any)
		estados[n["id"].(string)] = n["credential"].(map[string]any)["state"].(string)
	}
	if estados["lxc/207"] != inventory.CredRevogada {
		t.Errorf("A: state = %q, want revoked", estados["lxc/207"])
	}
	if estados["qemu/208"] != inventory.CredOK {
		t.Errorf("B: state = %q, want ok — A's revocation leaked", estados["qemu/208"])
	}
	if _, ainda := cofre.dados["pve_token_node_dev"]; !ainda {
		t.Error("node B's key disappeared from the vault")
	}
}

// TestNodeDetalhe: the detail view aggregates what points at THAT node, and nothing else.
func TestNodeDetalhe(t *testing.T) {
	r, st := novoRouterDeNos(t, []inventory.Node{
		noDeTeste("lxc/207", "apps", 207, agoraDeTeste-10),
		noDeTeste("qemu/208", "dev", 208, agoraDeTeste-10),
	})
	r.nodeVaultFn = func() (nodeVault, error) {
		return &cofreFalso{dados: map[string]string{
			"pve_token_node_apps": "x", "pve_token_node_dev": "y",
		}}, nil
	}
	if err := st.Replace(func(iv *inventory.Inventory) {
		iv.Services = []inventory.Service{{ID: "s1", NodeID: "lxc/207", Unit: "a.service"}, {ID: "s2", NodeID: "qemu/208", Unit: "b.service"}}
		iv.Jobs = []inventory.JobRef{{ID: "j1", Kind: "queue", NodeID: "lxc/207", Source: "user"}}
	}); err != nil {
		t.Fatal(err)
	}

	w, out := chama(t, r, http.MethodGet, "/api/nodes/lxc/207", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	if len(out["services"].([]any)) != 1 || len(out["jobs"].([]any)) != 1 {
		t.Fatalf("aggregation leaked from another node: %v", out)
	}
	no := out["node"].(map[string]any)
	if no["id"] != "lxc/207" || no["age_seconds"].(float64) != 10 {
		t.Fatalf("node = %v", no)
	}

	if w, _ := chama(t, r, http.MethodGet, "/api/nodes/lxc/999", ""); w.Code != 404 {
		t.Fatalf("nonexistent node → %d, want 404", w.Code)
	}
}

// TestNodesMetodos: the wrong verb on the right route is 405, not a silent 200.
func TestNodesMetodos(t *testing.T) {
	r, _ := novoRouterDeNos(t, []inventory.Node{noDeTeste("lxc/207", "apps", 207, agoraDeTeste)})
	r.nodeVaultFn = func() (nodeVault, error) { return &cofreFalso{dados: map[string]string{}}, nil }
	for _, tc := range []struct{ m, p string }{
		{http.MethodPost, "/api/nodes"},
		{http.MethodGet, "/api/nodes/lxc/207/power"},
		{http.MethodGet, "/api/nodes/lxc/207/credential"},
		{http.MethodDelete, "/api/nodes/lxc/207"},
	} {
		if w, _ := chama(t, r, tc.m, tc.p, "{}"); w.Code != 405 {
			t.Errorf("%s %s → %d, want 405", tc.m, tc.p, w.Code)
		}
	}
}

// 🔴 TestPainelSobeSemHipervisor is the resilience criterion: a lab that will
// not open because the hypervisor is down is the opposite of what it promises —
// the panel is exactly where you go to LOOK when something has fallen over.
//
// With no data/pve/pve.json and no vault: the descriptor fails, the poller does
// not come up, nothing panics, and /api/nodes keeps serving whatever is on disk.
func TestPainelSobeSemHipervisor(t *testing.T) {
	dir := t.TempDir()

	if _, err := loadPVEDescriptor(dir); err == nil {
		t.Fatal("missing descriptor returned success")
	}

	r, _ := novoRouterDeNos(t, []inventory.Node{
		{ID: "vps", Name: "vps", Transport: inventory.TransportSSH, Kind: inventory.NodeKindExterno},
	})
	r.pveConfig = nil
	r.secrets = nil // vault unavailable

	// It must neither panic nor block.
	r.startInventoryPoller(context.Background())
	if r.inventoryPoller != nil {
		t.Fatal("poller came up with neither descriptor nor vault")
	}

	w, out := chama(t, r, http.MethodGet, "/api/nodes", "")
	if w.Code != 200 {
		t.Fatalf("status = %d — the panel stopped listing for lack of a hypervisor", w.Code)
	}
	if len(out["nodes"].([]any)) != 1 {
		t.Fatalf("nodes = %v, want the seed node", out["nodes"])
	}
	if out["vault"] != vaultInalcancavel {
		t.Errorf("vault = %v, want %q", out["vault"], vaultInalcancavel)
	}
}

// TestDescritorMalformadoNaoSobe: a file that is PRESENT and unreadable is an
// error (accepting it silently would let the operator believe they configured
// something the panel then ignored).
func TestDescritorMalformadoNaoSobe(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pve"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pve", "pve.json"), []byte("{nao e json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadPVEDescriptor(dir)
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("error = %v, want it to mention 'malformado'", err)
	}
}

// 🔴 TestCredentialSourcePreencheONo is the pin for the defect only the LIVE
// call revealed: in production ALL 11 nodes read as having no credential (the
// "ausente" state) with the vault full of valid tokens, and `expire` stayed 0 —
// which made the expiry warning impossible to fire.
//
// The cause was structural, not a typo: the poller never filled Node.Credential,
// and credentialState() returns "ausente" whenever TokenID is empty. The unit
// tests did not catch it because the fixtures already arrived with Credential
// filled in by hand — this test starts from the RAW node, the way the poller
// hands it over.
func TestCredentialSourcePreencheONo(t *testing.T) {
	r, _ := novoRouterDeNos(t, nil)
	r.nodeVaultFn = func() (nodeVault, error) {
		return &cofreFalso{dados: map[string]string{
			"pve_token_admin":     "lab@pve!admin=a",
			"pve_token_audit":     "lab@pve!audit=a",
			"pve_token_node_lab":  "lab@pve!node-lab=s",
			"pve_token_node_apps": "lab@pve!node-apps=s",
		}}, nil
	}
	r.pveDial = func(string) (hypervisorOps, error) {
		return &pveFalso{tokens: []pve.TokenInfo{
			{TokenID: "node-lab", Privsep: true, Expire: 1802645875},
			{TokenID: "node-apps", Privsep: true, Expire: 1802645875},
			{TokenID: "audit", Privsep: true, Expire: 1802645875},
		}}, nil
	}

	// RAW nodes, the way the poller assembles them: Credential zeroed.
	nos := []inventory.Node{
		{ID: "lxc/204", Name: "lab", VMID: 204, Kind: inventory.NodeKindGuest, Transport: inventory.TransportPVEAPI},
		{ID: "lxc/207", Name: "apps", VMID: 207, Kind: inventory.NodeKindGuest, Transport: inventory.TransportPVEAPI},
		{ID: "node/pve", Name: "pve", Kind: inventory.NodeKindHost, Transport: inventory.TransportPVEAPI},
		{ID: "canario", Name: "canario", Kind: inventory.NodeKindExterno, Transport: inventory.TransportAgente},
	}

	creds, err := r.credentialSource()(nos)
	if err != nil {
		t.Fatalf("credential source: %v", err)
	}

	c, ok := creds["lxc/204"]
	if !ok {
		t.Fatal("the node with a key in the vault did NOT receive a credential — that is the production defect")
	}
	if c.TokenID != "lab@pve!node-lab" {
		t.Errorf("token_id = %q, want the WHOLE id from the vault", c.TokenID)
	}
	if c.Expire != 1802645875 {
		t.Errorf("expire = %d, want the hypervisor's — without it the staleness warning never fires", c.Expire)
	}
	// The host is observed through the AUDIT token; reporting "ausente" on it
	// would be lying about a node the panel can see perfectly well.
	if h, ok := creds["node/pve"]; !ok || h.TokenID != "lab@pve!audit" {
		t.Errorf("host = %+v (ok=%v), want the audit credential", h, ok)
	}
	// A non-PVE transport has no per-node token.
	if _, ok := creds["canario"]; ok {
		t.Error("the canary (agent transport) received a PVE credential")
	}

	// 🔴 And the loop closes: with the source filled in, the VIEW has to say "ok".
	// This is the assertion that would fail against today's production.
	for i := range nos {
		if c, ok := creds[nos[i].ID]; ok {
			nos[i].Credential = c
		}
	}
	vistas := inventory.View(inventory.Inventory{Nodes: nos}, time.Minute, time.Unix(agoraDeTeste, 0))
	porID := map[string]inventory.NodeView{}
	for _, v := range vistas {
		porID[v.ID] = v
	}
	if got := porID["lxc/204"].Credential.State; got != inventory.CredOK {
		t.Fatalf("state of the node with a live token = %q, want %q", got, inventory.CredOK)
	}
	if got := porID["canario"].Credential.State; got != inventory.CredAusente {
		t.Errorf("canary = %q, want missing", got)
	}
}

// TestCredentialSourceCofreMortoNaoMente: an unreachable vault returns an ERROR,
// not an empty map. An empty map would make the poller wipe everybody's
// credential and the screen would announce that the whole lab had lost its
// credentials — when what went down was the vault.
func TestCredentialSourceCofreMortoNaoMente(t *testing.T) {
	r, _ := novoRouterDeNos(t, nil)
	r.nodeVaultFn = func() (nodeVault, error) { return nil, errors.New("cofre fora do ar") }
	if _, err := r.credentialSource()([]inventory.Node{
		{ID: "lxc/204", Name: "lab", Kind: inventory.NodeKindGuest, Transport: inventory.TransportPVEAPI},
	}); err == nil {
		t.Fatal("dead vault returned success — the poller would wipe every node's credential")
	}
}

// TestCredentialSourceSemExpireAindaReportaToken: if the hypervisor does not
// return the expiry dates (admin token missing, 403, network), the node still
// has a credential — just without a date. Refusing everything here would turn
// "I do not know the expiry" into "there is no credential".
func TestCredentialSourceSemExpireAindaReportaToken(t *testing.T) {
	r, _ := novoRouterDeNos(t, nil)
	r.nodeVaultFn = func() (nodeVault, error) {
		return &cofreFalso{dados: map[string]string{"pve_token_node_lab": "lab@pve!node-lab=s"}}, nil
	}
	r.pveDial = func(string) (hypervisorOps, error) { return nil, errors.New("sem descritor") }

	creds, err := r.credentialSource()([]inventory.Node{
		{ID: "lxc/204", Name: "lab", Kind: inventory.NodeKindGuest, Transport: inventory.TransportPVEAPI},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	c := creds["lxc/204"]
	if c.TokenID != "lab@pve!node-lab" {
		t.Fatalf("token_id = %q — no expiry cannot turn into no credential", c.TokenID)
	}
	if c.Expire != 0 {
		t.Errorf("expire = %d, want 0 (unknown)", c.Expire)
	}
}

// 🔴 TestHostNaoContradizASiMesmo: the host held `lab@pve!audit` in the
// inventory while the screen said it had no credential, because the source and
// the read picked the vault key by DIFFERENT paths. The symptom was one row
// showing an expiry date and "ausente" at the same time.
func TestHostNaoContradizASiMesmo(t *testing.T) {
	host := inventory.Node{
		ID: "node/pve", Name: "pve", Kind: inventory.NodeKindHost,
		Transport: inventory.TransportPVEAPI,
		Status:    inventory.Observe("online", agoraDeTeste),
		Credential: inventory.Credential{
			TokenID: "lab@pve!audit", Expire: agoraDeTeste + 30*86400,
		},
	}
	r, _ := novoRouterDeNos(t, []inventory.Node{host})
	r.nodeVaultFn = func() (nodeVault, error) {
		// The vault holds the AUDIT key — and no "pve_token_node_pve" at all.
		return &cofreFalso{dados: map[string]string{"pve_token_audit": "lab@pve!audit=s"}}, nil
	}

	_, out := chama(t, r, http.MethodGet, "/api/nodes", "")
	n := out["nodes"].([]any)[0].(map[string]any)
	cred := n["credential"].(map[string]any)
	if cred["state"] != inventory.CredOK {
		t.Fatalf("host state = %v, want ok (the vault DOES have the credential that observes it)", cred["state"])
	}
	if cred["token_id"] == "" {
		t.Error("the handler erased the host's token_id")
	}
	// The concrete contradiction: an expiry present with credential "ausente".
	if cred["expire"].(float64) > 0 && cred["state"] == inventory.CredAusente {
		t.Error("contradictory line: shows an expiry date AND 'no credential'")
	}
	// And the source picks the SAME key as the read.
	if got := chaveDeCredencial(host); got != pveSecretAudit {
		t.Errorf("chaveDeCredencial(host) = %q, want %q", got, pveSecretAudit)
	}
}
