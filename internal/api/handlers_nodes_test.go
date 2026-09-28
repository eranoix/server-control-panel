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

const testNow = 1800000000

type fakeVault struct {
	data         map[string]string
	seen         *[]string
	deleteErr    error
	resurrect    bool
	deleteCalled bool
}

func (c *fakeVault) Get(k string) (string, bool) {
	if c.deleteCalled && c.resurrect {
		return "resurrected", true
	}
	v, ok := c.data[k]
	return v, ok
}

func (c *fakeVault) Delete(k string) error {
	c.deleteCalled = true
	if c.seen != nil {
		*c.seen = append(*c.seen, "vault.delete")
	}
	if c.deleteErr != nil {
		return c.deleteErr
	}
	delete(c.data, k)
	return nil
}

type fakePVE struct {
	jobsBackup  []pve.BackupJob
	series      []pve.RRDPoint
	ifaces      []pve.Interface
	dns         pve.DNSInfo
	timeInfo    pve.TimeInfo
	certs       []pve.Certificate
	packages    []pve.PackageInfo
	syslog      []pve.SyslogLine
	freshness   map[string]pve.BackupFreshness
	zpools      []pve.ZPool
	topologies  map[string]pve.ZPoolTopology
	role        string
	seen        *[]string
	upid        string
	nextID      int
	description string
	usedToken   string
	verbErr     error
	waitErr     error
	deleteErr   error
	stillAlive  bool
	deadTokens  map[string]bool
	tokens      []pve.TokenInfo
	listErr     error

	status    pve.NodeStatus
	statusErr error
	tasks     []pve.Task
	logLines  []string
	disks     []pve.Disk
	perms     map[string]map[string]int
	snaps     []pve.Snapshot

	console       *fakeConsole
	consoleErr    error
	calledConsole bool
}

func (p *fakePVE) NodePower(ctx context.Context, node string, cmd pve.PowerCommand) (string, error) {
	p.mark("pve.node-power:" + string(cmd) + ":" + node)
	if p.verbErr != nil {
		return "", p.verbErr
	}
	return "UPID:pve:node-power", nil
}

func (p *fakePVE) Reboot(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.mark(fmt.Sprintf("pve.reboot:%s/%d", typ, vmid))
	if p.verbErr != nil {
		return "", p.verbErr
	}
	return "UPID:pve:reboot", nil
}

func (p *fakePVE) Description(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.mark(fmt.Sprintf("pve.description:%s/%d", typ, vmid))
	if p.verbErr != nil {
		return "", p.verbErr
	}
	return p.description, nil
}

func (p *fakePVE) SetDescription(ctx context.Context, node string, vmid int, typ, text string) error {
	p.mark(fmt.Sprintf("pve.set-description:%s/%d:%dbytes", typ, vmid, len(text)))
	if p.verbErr != nil {
		return p.verbErr
	}
	p.description = text
	return nil
}

func (p *fakePVE) NextID(ctx context.Context) (int, error) {
	p.mark("pve.nextid")
	if p.verbErr != nil {
		return 0, p.verbErr
	}
	if p.nextID > 0 {
		return p.nextID, nil
	}
	return 991, nil
}

func (p *fakePVE) Clone(ctx context.Context, node string, vmid int, typ string, newID int, name, snapname string) (string, error) {
	p.mark(fmt.Sprintf("pve.clone:%s/%d->%d:%s:snap=%s", typ, vmid, newID, name, snapname))
	if p.verbErr != nil {
		return "", p.verbErr
	}
	return "UPID:pve:clone", nil
}

func (p *fakePVE) VZDump(ctx context.Context, node string, vmid int, storage, mode, compress string) (string, error) {
	p.mark(fmt.Sprintf("pve.vzdump:%d:%s:%s:%s", vmid, storage, mode, compress))
	if p.verbErr != nil {
		return "", p.verbErr
	}
	return "UPID:pve:vzdump", nil
}

func (p *fakePVE) ConsoleAttachNode(ctx context.Context, node string) (pve.ConsoleConn, string, error) {
	p.calledConsole = true
	p.mark("pve.console-host:" + node)
	if p.consoleErr != nil {
		return nil, "", p.consoleErr
	}
	return p.console, p.upid, nil
}

func (p *fakePVE) ConsoleAttach(ctx context.Context, node string, vmid int, typ string) (pve.ConsoleConn, string, error) {
	p.calledConsole = true
	p.mark(fmt.Sprintf("pve.console:%s/%d", typ, vmid))
	if p.consoleErr != nil {
		return nil, "", p.consoleErr
	}
	return p.console, p.upid, nil
}

func (p *fakePVE) SnapshotRollback(ctx context.Context, node string, vmid int, typ, name string) (string, error) {
	p.mark("pve.snaprollback:" + name)
	return p.upid, p.verbErr
}

func (p *fakePVE) NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error) {
	p.mark("pve.status:" + node)
	return p.status, p.statusErr
}
func (p *fakePVE) TaskList(ctx context.Context, node string, opt pve.TaskListOptions) ([]pve.Task, error) {
	p.mark(fmt.Sprintf("pve.tasks:%s:limit=%d:errors=%v", node, opt.Limit, opt.ErrorsOnly))
	return p.tasks, p.verbErr
}
func (p *fakePVE) TaskLog(ctx context.Context, node, upid string) ([]string, error) {
	p.mark("pve.tasklog:" + upid)
	return p.logLines, p.verbErr
}
func (p *fakePVE) DisksList(ctx context.Context, node string) ([]pve.Disk, error) {
	p.mark("pve.disks:" + node)
	return p.disks, p.verbErr
}
func (p *fakePVE) ZFSList(ctx context.Context, node string) ([]pve.ZPool, error) {
	p.mark("pve.zfslist:" + node)
	return p.zpools, p.verbErr
}
func (p *fakePVE) RRDNode(ctx context.Context, node string, j pve.RRDWindow) ([]pve.RRDPoint, error) {
	p.mark("pve.rrdnode:" + string(j))
	return p.series, p.verbErr
}
func (p *fakePVE) RRDGuest(ctx context.Context, node string, vmid int, typ string, j pve.RRDWindow) ([]pve.RRDPoint, error) {
	p.mark("pve.rrdguest:" + typ + "/" + strconv.Itoa(vmid) + ":" + string(j))
	return p.series, p.verbErr
}
func (p *fakePVE) Network(ctx context.Context, node string) ([]pve.Interface, error) {
	p.mark("pve.network")
	return p.ifaces, p.verbErr
}
func (p *fakePVE) DNS(ctx context.Context, node string) (pve.DNSInfo, error) {
	p.mark("pve.dns")
	return p.dns, p.verbErr
}
func (p *fakePVE) Time(ctx context.Context, node string) (pve.TimeInfo, error) {
	p.mark("pve.time")
	return p.timeInfo, p.verbErr
}
func (p *fakePVE) Certificates(ctx context.Context, node string) ([]pve.Certificate, error) {
	p.mark("pve.certs")
	return p.certs, p.verbErr
}
func (p *fakePVE) Packages(ctx context.Context, node string) ([]pve.PackageInfo, error) {
	p.mark("pve.packages")
	return p.packages, p.verbErr
}
func (p *fakePVE) Syslog(ctx context.Context, node string, limit int) ([]pve.SyslogLine, error) {
	p.mark("pve.syslog:" + strconv.Itoa(limit))
	return p.syslog, p.verbErr
}
func (p *fakePVE) BackupJobs(ctx context.Context) ([]pve.BackupJob, error) {
	p.mark("pve.jobs-backup")
	return p.jobsBackup, p.verbErr
}

func (p *fakePVE) DatastoreBackups(ctx context.Context, node, storage string) (pve.BackupFreshness, error) {
	p.mark("pve.backups:" + storage)
	if p.verbErr != nil {
		return pve.BackupFreshness{}, p.verbErr
	}
	return p.freshness[storage], nil
}
func (p *fakePVE) ZFSTopology(ctx context.Context, node, pool string) (pve.ZPoolTopology, error) {
	p.mark("pve.zfstopology:" + pool)
	if p.verbErr != nil {
		return pve.ZPoolTopology{}, p.verbErr
	}
	return p.topologies[pool], nil
}
func (p *fakePVE) Permissions(ctx context.Context) (map[string]map[string]int, error) {
	p.mark("pve.permissions")
	return p.perms, p.verbErr
}
func (p *fakePVE) SnapshotList(ctx context.Context, node string, vmid int, typ string) ([]pve.Snapshot, error) {
	p.mark(fmt.Sprintf("pve.snaplist:%s/%d", typ, vmid))
	return p.snaps, p.verbErr
}
func (p *fakePVE) SnapshotCreate(ctx context.Context, node string, vmid int, typ, name, description string) (string, error) {
	p.mark("pve.snapcreate:" + name)
	return p.upid, p.verbErr
}
func (p *fakePVE) SnapshotDelete(ctx context.Context, node string, vmid int, typ, name string) (string, error) {
	p.mark("pve.snapdelete:" + name)
	return p.upid, p.verbErr
}

func (p *fakePVE) mark(ev string) {
	if p.usedToken != "" {
		if i := strings.IndexByte(p.usedToken, '='); i > 0 {
			ev += " token=" + p.usedToken[:i]
		} else {
			ev += " token=" + p.usedToken
		}
	}
	if p.seen != nil {
		*p.seen = append(*p.seen, ev)
	}
}

func (p *fakePVE) Start(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.mark(fmt.Sprintf("pve.start:%s/%d", typ, vmid))
	return p.upid, p.verbErr
}
func (p *fakePVE) Stop(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.mark(fmt.Sprintf("pve.stop:%s/%d", typ, vmid))
	return p.upid, p.verbErr
}
func (p *fakePVE) Shutdown(ctx context.Context, node string, vmid int, typ string) (string, error) {
	p.mark(fmt.Sprintf("pve.shutdown:%s/%d", typ, vmid))
	return p.upid, p.verbErr
}
func (p *fakePVE) WaitTask(ctx context.Context, node, upid string) error {
	p.mark("pve.wait:" + upid)
	return p.waitErr
}
func (p *fakePVE) DeleteToken(ctx context.Context, user, tokenID string) error {
	p.mark("pve.delete")
	if p.deleteErr != nil {
		return p.deleteErr
	}
	if p.deadTokens != nil {
		p.deadTokens[tokenID] = true
	}
	return nil
}

func (p *fakePVE) ListTokens(ctx context.Context, user string) ([]pve.TokenInfo, error) {
	p.mark("pve.listtokens")
	if p.listErr != nil {
		return nil, p.listErr
	}
	return p.tokens, nil
}

func (p *fakePVE) ClusterResources(ctx context.Context) ([]pve.Resource, error) {
	p.mark("pve.confirm401")
	if p.stillAlive {
		return nil, nil
	}
	return nil, &pve.Error{Kind: pve.KindNoCredential, Status: 401, Path: "/cluster/resources"}
}

func newNodesRouter(t *testing.T, nodes []inventory.Node) (*Router, *inventory.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := inventory.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) > 0 {
		if err := st.Replace(func(iv *inventory.Inventory) { iv.Nodes = nodes }); err != nil {
			t.Fatal(err)
		}
	}
	r := &Router{cfg: &config.Config{DataDir: dir, Primary: "sam"}}
	r.inventoryStore = st
	r.inventoryNow = func() time.Time { return time.Unix(testNow, 0) }
	return r, st
}

func req(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	rq := httptest.NewRequest(method, path, strings.NewReader(body))
	return rq.WithContext(auth.WithUser(rq.Context(), "sam"))
}

func callAPI(t *testing.T, r *Router, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.handleNodes(w, req(t, method, path, body))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func testNode(id, name string, vmid int, observedAt int64) inventory.Node {
	return inventory.Node{
		ID: id, Name: name, VMID: vmid, Kind: inventory.NodeKindGuest,
		Transport: inventory.TransportPVEAPI,
		Status:    inventory.Observe("running", observedAt),
		Uptime:    inventory.Observe(int64(100), observedAt),
		Credential: inventory.Credential{
			TokenID: "panel@pve!node-" + name, Expire: testNow + 30*86400, State: inventory.CredOK,
		},
	}
}

func TestNodesList(t *testing.T) {
	r, _ := newNodesRouter(t, []inventory.Node{
		testNode("lxc/207", "apps", 207, testNow-10),
		testNode("qemu/208", "dev", 208, testNow-600),
	})
	r.nodeVaultFn = func() (nodeVault, error) {
		return &fakeVault{data: map[string]string{
			"pve_token_node_apps": "panel@pve!node-apps=s",
			"pve_token_node_dev":  "panel@pve!node-dev=s",
		}}, nil
	}

	w, out := callAPI(t, r, http.MethodGet, "/api/nodes", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	nodes, _ := out["nodes"].([]any)
	if len(nodes) == 0 {
		t.Fatal("response with no nodes")
	}
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
	byID := map[string]map[string]any{}
	for _, raw := range nodes {
		n := raw.(map[string]any)
		byID[n["id"].(string)] = n
	}
	if got := byID["lxc/207"]["age_seconds"].(float64); got != 10 {
		t.Errorf("age of the fresh one = %v, want 10", got)
	}
	if byID["lxc/207"]["stale"].(bool) {
		t.Error("the 10s node showed up as expired")
	}
	if got := byID["qemu/208"]["age_seconds"].(float64); got != 600 {
		t.Errorf("age of the old one = %v, want 600", got)
	}
	if !byID["qemu/208"]["stale"].(bool) {
		t.Error("the 600s node did NOT show up as expired (TTL 90s)")
	}
	if out["vault"] != vaultOK {
		t.Errorf("vault = %v, want ok", out["vault"])
	}
}

func TestNodesVaultStates(t *testing.T) {
	t.Run("vault unreachable: the list STILL responds 200", func(t *testing.T) {
		r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow-10)})
		r.nodeVaultFn = func() (nodeVault, error) { return nil, errors.New("vault is down") }

		w, out := callAPI(t, r, http.MethodGet, "/api/nodes", "")
		if w.Code != 200 {
			t.Fatalf("dead vault wiped the inventory: status %d", w.Code)
		}
		if out["vault"] != vaultUnreachable {
			t.Fatalf("vault = %v, want %q", out["vault"], vaultUnreachable)
		}
		n := out["nodes"].([]any)[0].(map[string]any)
		cred := n["credential"].(map[string]any)
		if cred["state"] == inventory.CredMissing {
			t.Error("unreachable vault was reported as credential MISSING — that is the handlers_ai.go:186 collapse")
		}
	})

	t.Run("key missing: credential missing, vault ok", func(t *testing.T) {
		r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow-10)})
		r.nodeVaultFn = func() (nodeVault, error) { return &fakeVault{data: map[string]string{}}, nil }

		w, out := callAPI(t, r, http.MethodGet, "/api/nodes", "")
		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
		if out["vault"] != vaultOK {
			t.Errorf("vault = %v, want ok (the vault answered; it is the KEY that does not exist)", out["vault"])
		}
		n := out["nodes"].([]any)[0].(map[string]any)
		if got := n["credential"].(map[string]any)["state"]; got != inventory.CredMissing {
			t.Errorf("state = %v, want %q", got, inventory.CredMissing)
		}
	})

	t.Run("power with vault unreachable = 503, with key missing = 409", func(t *testing.T) {
		r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow)})
		r.pveDial = func(string) (hypervisorOps, error) { return &fakePVE{upid: "UPID:x"}, nil }

		r.nodeVaultFn = func() (nodeVault, error) { return nil, errors.New("fora do ar") }
		w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"start"}`)
		if w.Code != 503 {
			t.Errorf("vault unreachable → %d, want 503 (body %s)", w.Code, w.Body)
		}

		r.nodeVaultFn = func() (nodeVault, error) { return &fakeVault{data: map[string]string{}}, nil }
		w, _ = callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"start"}`)
		if w.Code != 409 {
			t.Errorf("key missing → %d, want 409 (body %s)", w.Code, w.Body)
		}
	})
}

func TestNodesStoreNil(t *testing.T) {
	r := &Router{cfg: &config.Config{DataDir: t.TempDir(), Primary: "sam"}}
	w, _ := callAPI(t, r, http.MethodGet, "/api/nodes", "")
	if w.Code != 503 {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestPowerWaitsUPID(t *testing.T) {
	t.Run("success waits for the task", func(t *testing.T) {
		var seen []string
		r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow)})
		r.nodeVaultFn = func() (nodeVault, error) {
			return &fakeVault{data: map[string]string{"pve_token_node_apps": "panel@pve!node-apps=s"}}, nil
		}
		r.pveDial = func(string) (hypervisorOps, error) {
			return &fakePVE{seen: &seen, upid: "UPID:pve:1:start"}, nil
		}
		w, out := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"start"}`)
		if w.Code != 200 {
			t.Fatalf("status = %d, body %s", w.Code, w.Body)
		}
		if out["upid"] != "UPID:pve:1:start" {
			t.Errorf("upid = %v, want the hypervisor's", out["upid"])
		}
		want := []string{"pve.start:lxc/207", "pve.wait:UPID:pve:1:start"}
		if fmt.Sprint(seen) != fmt.Sprint(want) {
			t.Fatalf("sequence = %v, want %v (WaitTask is mandatory)", seen, want)
		}
	})

	t.Run("a task that ends in error does NOT become 200", func(t *testing.T) {
		r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow)})
		r.nodeVaultFn = func() (nodeVault, error) {
			return &fakeVault{data: map[string]string{"pve_token_node_apps": "panel@pve!node-apps=s"}}, nil
		}
		r.pveDial = func(string) (hypervisorOps, error) {
			return &fakePVE{upid: "UPID:x", waitErr: &pve.Error{
				Kind: pve.KindHypervisor, Path: "/tasks", Body: "command 'lxc-start' failed with exit code 1",
			}}, nil
		}
		w, _ := callAPI(t, r, http.MethodPost, "/api/nodes/lxc/207/power", `{"action":"start"}`)
		if w.Code != 502 {
			t.Fatalf("status = %d, want 502", w.Code)
		}
		if !strings.Contains(w.Body.String(), "lxc-start") {
			t.Fatalf("the body hides PVE's exitstatus: %s", w.Body)
		}
	})
}

func TestRevokeOrder(t *testing.T) {
	var seen []string
	r, st := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow)})
	vault := &fakeVault{seen: &seen, data: map[string]string{
		"pve_token_admin":     "panel@pve!admin=a",
		"pve_token_node_apps": "panel@pve!node-apps=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return vault, nil }
	r.pveDial = func(value string) (hypervisorOps, error) {
		role := "operational"
		if strings.HasPrefix(value, "panel@pve!admin") {
			role = "admin"
		}
		return &fakePVE{role: role, seen: &seen}, nil
	}

	w, out := callAPI(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}

	want := []string{"pve.delete", "pve.confirm401", "vault.delete"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("SEQUENCE = %v, want EXACTLY %v", seen, want)
	}
	if _, still := vault.data["pve_token_node_apps"]; still {
		t.Error("the key is still in the vault")
	}
	if fmt.Sprint(out["steps"]) != fmt.Sprint([]any{"pve.delete", "pve.confirm401", "vault.delete", "vault.recheck"}) {
		t.Errorf("steps in the response = %v", out["steps"])
	}
	inv, _ := st.Snapshot()
	if inv.Nodes[0].Credential.State != inventory.CredRevoked {
		t.Errorf("state in the inventory = %q, want revoked", inv.Nodes[0].Credential.State)
	}
}

func TestRevokeRequiresProof401(t *testing.T) {
	var seen []string
	r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow)})
	vault := &fakeVault{seen: &seen, data: map[string]string{
		"pve_token_admin":     "panel@pve!admin=a",
		"pve_token_node_apps": "panel@pve!node-apps=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return vault, nil }
	r.pveDial = func(value string) (hypervisorOps, error) {
		return &fakePVE{seen: &seen, stillAlive: !strings.HasPrefix(value, "panel@pve!admin")}, nil
	}

	w, _ := callAPI(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", "")
	if w.Code != 502 {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pve.confirm401") {
		t.Errorf("the body does not name the step that failed: %s", w.Body)
	}
	if _, gone := vault.data["pve_token_node_apps"]; !gone {
		t.Error("the vault was touched without proof of the 401")
	}
}

func TestRevokeResurrection(t *testing.T) {
	r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow)})
	vault := &fakeVault{resurrect: true, data: map[string]string{
		"pve_token_admin":     "panel@pve!admin=a",
		"pve_token_node_apps": "panel@pve!node-apps=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return vault, nil }
	r.pveDial = func(string) (hypervisorOps, error) { return &fakePVE{}, nil }

	w, _ := callAPI(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", "")
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), "vault.recheck") {
		t.Errorf("the resurrection went through quietly: %s", w.Body)
	}
}

func TestRevokePVEFailureKeepsVault(t *testing.T) {
	r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow)})
	vault := &fakeVault{data: map[string]string{
		"pve_token_admin":     "panel@pve!admin=a",
		"pve_token_node_apps": "panel@pve!node-apps=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return vault, nil }
	r.pveDial = func(string) (hypervisorOps, error) {
		return &fakePVE{deleteErr: &pve.Error{Kind: pve.KindForbidden, Status: 403, Path: "/access"}}, nil
	}

	w, _ := callAPI(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", "")
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "pve.delete") {
		t.Errorf("the body does not name the step: %s", w.Body)
	}
	if _, still := vault.data["pve_token_node_apps"]; !still {
		t.Error("the vault was touched despite the failure on the hypervisor")
	}
}

func TestRevokeIsolation(t *testing.T) {
	r, _ := newNodesRouter(t, []inventory.Node{
		testNode("lxc/207", "apps", 207, testNow),
		testNode("qemu/208", "dev", 208, testNow),
	})
	vault := &fakeVault{data: map[string]string{
		"pve_token_admin":     "panel@pve!admin=a",
		"pve_token_node_apps": "panel@pve!node-apps=s",
		"pve_token_node_dev":  "panel@pve!node-dev=s",
	}}
	r.nodeVaultFn = func() (nodeVault, error) { return vault, nil }
	r.pveDial = func(string) (hypervisorOps, error) { return &fakePVE{}, nil }

	if w, _ := callAPI(t, r, http.MethodDelete, "/api/nodes/lxc/207/credential", ""); w.Code != 200 {
		t.Fatalf("revocation failed: %d %s", w.Code, w.Body)
	}

	_, out := callAPI(t, r, http.MethodGet, "/api/nodes", "")
	states := map[string]string{}
	for _, raw := range out["nodes"].([]any) {
		n := raw.(map[string]any)
		states[n["id"].(string)] = n["credential"].(map[string]any)["state"].(string)
	}
	if states["lxc/207"] != inventory.CredRevoked {
		t.Errorf("A: state = %q, want revoked", states["lxc/207"])
	}
	if states["qemu/208"] != inventory.CredOK {
		t.Errorf("B: state = %q, want ok — A's revocation leaked", states["qemu/208"])
	}
	if _, still := vault.data["pve_token_node_dev"]; !still {
		t.Error("node B's key disappeared from the vault")
	}
}

func TestNodeDetail(t *testing.T) {
	r, st := newNodesRouter(t, []inventory.Node{
		testNode("lxc/207", "apps", 207, testNow-10),
		testNode("qemu/208", "dev", 208, testNow-10),
	})
	r.nodeVaultFn = func() (nodeVault, error) {
		return &fakeVault{data: map[string]string{
			"pve_token_node_apps": "x", "pve_token_node_dev": "y",
		}}, nil
	}
	if err := st.Replace(func(iv *inventory.Inventory) {
		iv.Services = []inventory.Service{{ID: "s1", NodeID: "lxc/207", Unit: "a.service"}, {ID: "s2", NodeID: "qemu/208", Unit: "b.service"}}
		iv.Jobs = []inventory.JobRef{{ID: "j1", Kind: "queue", NodeID: "lxc/207", Source: "user"}}
	}); err != nil {
		t.Fatal(err)
	}

	w, out := callAPI(t, r, http.MethodGet, "/api/nodes/lxc/207", "")
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

	if w, _ := callAPI(t, r, http.MethodGet, "/api/nodes/lxc/999", ""); w.Code != 404 {
		t.Fatalf("nonexistent node → %d, want 404", w.Code)
	}
}

func TestNodesMethods(t *testing.T) {
	r, _ := newNodesRouter(t, []inventory.Node{testNode("lxc/207", "apps", 207, testNow)})
	r.nodeVaultFn = func() (nodeVault, error) { return &fakeVault{data: map[string]string{}}, nil }
	for _, tc := range []struct{ m, p string }{
		{http.MethodPost, "/api/nodes"},
		{http.MethodGet, "/api/nodes/lxc/207/power"},
		{http.MethodGet, "/api/nodes/lxc/207/credential"},
		{http.MethodDelete, "/api/nodes/lxc/207"},
	} {
		if w, _ := callAPI(t, r, tc.m, tc.p, "{}"); w.Code != 405 {
			t.Errorf("%s %s → %d, want 405", tc.m, tc.p, w.Code)
		}
	}
}

func TestPanelStartsWithoutHypervisor(t *testing.T) {
	dir := t.TempDir()

	if _, err := loadPVEDescriptor(dir); err == nil {
		t.Fatal("missing descriptor returned success")
	}

	r, _ := newNodesRouter(t, []inventory.Node{
		{ID: "vps", Name: "vps", Transport: inventory.TransportSSH, Kind: inventory.NodeKindExternal},
	})
	r.pveConfig = nil
	r.secrets = nil

	r.startInventoryPoller(context.Background())
	if r.inventoryPoller != nil {
		t.Fatal("poller came up with neither descriptor nor vault")
	}

	w, out := callAPI(t, r, http.MethodGet, "/api/nodes", "")
	if w.Code != 200 {
		t.Fatalf("status = %d — the panel stopped listing for lack of a hypervisor", w.Code)
	}
	if len(out["nodes"].([]any)) != 1 {
		t.Fatalf("nodes = %v, want the seed node", out["nodes"])
	}
	if out["vault"] != vaultUnreachable {
		t.Errorf("vault = %v, want %q", out["vault"], vaultUnreachable)
	}
}

func TestMalformedDescriptorDoesNotStart(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pve"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pve", "pve.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadPVEDescriptor(dir)
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("error = %v, want it to mention 'malformed'", err)
	}
}

func TestCredentialSourceFillsNode(t *testing.T) {
	r, _ := newNodesRouter(t, nil)
	r.nodeVaultFn = func() (nodeVault, error) {
		return &fakeVault{data: map[string]string{
			"pve_token_admin":     "panel@pve!admin=a",
			"pve_token_audit":     "panel@pve!audit=a",
			"pve_token_node_lab":  "panel@pve!node-lab=s",
			"pve_token_node_apps": "panel@pve!node-apps=s",
		}}, nil
	}
	r.pveDial = func(string) (hypervisorOps, error) {
		return &fakePVE{tokens: []pve.TokenInfo{
			{TokenID: "node-lab", Privsep: true, Expire: 1802645875},
			{TokenID: "node-apps", Privsep: true, Expire: 1802645875},
			{TokenID: "audit", Privsep: true, Expire: 1802645875},
		}}, nil
	}

	nodes := []inventory.Node{
		{ID: "lxc/204", Name: "lab", VMID: 204, Kind: inventory.NodeKindGuest, Transport: inventory.TransportPVEAPI},
		{ID: "lxc/207", Name: "apps", VMID: 207, Kind: inventory.NodeKindGuest, Transport: inventory.TransportPVEAPI},
		{ID: "node/pve", Name: "pve", Kind: inventory.NodeKindHost, Transport: inventory.TransportPVEAPI},
		{ID: "canary", Name: "canary", Kind: inventory.NodeKindExternal, Transport: inventory.TransportAgent},
	}

	creds, err := r.credentialSource()(nodes)
	if err != nil {
		t.Fatalf("credential source: %v", err)
	}

	c, ok := creds["lxc/204"]
	if !ok {
		t.Fatal("the node with a key in the vault did NOT receive a credential — that is the production defect")
	}
	if c.TokenID != "panel@pve!node-lab" {
		t.Errorf("token_id = %q, want the WHOLE id from the vault", c.TokenID)
	}
	if c.Expire != 1802645875 {
		t.Errorf("expire = %d, want the hypervisor's — without it the staleness warning never fires", c.Expire)
	}
	if h, ok := creds["node/pve"]; !ok || h.TokenID != "panel@pve!audit" {
		t.Errorf("host = %+v (ok=%v), want the audit credential", h, ok)
	}
	if _, ok := creds["canary"]; ok {
		t.Error("the canary (agent transport) received a PVE credential")
	}

	for i := range nodes {
		if c, ok := creds[nodes[i].ID]; ok {
			nodes[i].Credential = c
		}
	}
	seen := inventory.View(inventory.Inventory{Nodes: nodes}, time.Minute, time.Unix(testNow, 0))
	byID := map[string]inventory.NodeView{}
	for _, v := range seen {
		byID[v.ID] = v
	}
	if got := byID["lxc/204"].Credential.State; got != inventory.CredOK {
		t.Fatalf("state of the node with a live token = %q, want %q", got, inventory.CredOK)
	}
	if got := byID["canary"].Credential.State; got != inventory.CredMissing {
		t.Errorf("canary = %q, want missing", got)
	}
}

func TestCredentialSourceDeadVaultDoesNotLie(t *testing.T) {
	r, _ := newNodesRouter(t, nil)
	r.nodeVaultFn = func() (nodeVault, error) { return nil, errors.New("vault is down") }
	if _, err := r.credentialSource()([]inventory.Node{
		{ID: "lxc/204", Name: "lab", Kind: inventory.NodeKindGuest, Transport: inventory.TransportPVEAPI},
	}); err == nil {
		t.Fatal("dead vault returned success — the poller would wipe every node's credential")
	}
}

func TestCredentialSourceWithoutExpireStillReportsToken(t *testing.T) {
	r, _ := newNodesRouter(t, nil)
	r.nodeVaultFn = func() (nodeVault, error) {
		return &fakeVault{data: map[string]string{"pve_token_node_lab": "panel@pve!node-lab=s"}}, nil
	}
	r.pveDial = func(string) (hypervisorOps, error) { return nil, errors.New("no descriptor") }

	creds, err := r.credentialSource()([]inventory.Node{
		{ID: "lxc/204", Name: "lab", Kind: inventory.NodeKindGuest, Transport: inventory.TransportPVEAPI},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	c := creds["lxc/204"]
	if c.TokenID != "panel@pve!node-lab" {
		t.Fatalf("token_id = %q — no expiry cannot turn into no credential", c.TokenID)
	}
	if c.Expire != 0 {
		t.Errorf("expire = %d, want 0 (unknown)", c.Expire)
	}
}

func TestHostDoesNotContradictItself(t *testing.T) {
	host := inventory.Node{
		ID: "node/pve", Name: "pve", Kind: inventory.NodeKindHost,
		Transport: inventory.TransportPVEAPI,
		Status:    inventory.Observe("online", testNow),
		Credential: inventory.Credential{
			TokenID: "panel@pve!audit", Expire: testNow + 30*86400,
		},
	}
	r, _ := newNodesRouter(t, []inventory.Node{host})
	r.nodeVaultFn = func() (nodeVault, error) {
		return &fakeVault{data: map[string]string{"pve_token_audit": "panel@pve!audit=s"}}, nil
	}

	_, out := callAPI(t, r, http.MethodGet, "/api/nodes", "")
	n := out["nodes"].([]any)[0].(map[string]any)
	cred := n["credential"].(map[string]any)
	if cred["state"] != inventory.CredOK {
		t.Fatalf("host state = %v, want ok (the vault DOES have the credential that observes it)", cred["state"])
	}
	if cred["token_id"] == "" {
		t.Error("the handler erased the host's token_id")
	}
	if cred["expire"].(float64) > 0 && cred["state"] == inventory.CredMissing {
		t.Error("contradictory line: shows an expiry date AND 'no credential'")
	}
	if got := credentialKey(host); got != pveSecretAudit {
		t.Errorf("credentialKey(host) = %q, want %q", got, pveSecretAudit)
	}
}
