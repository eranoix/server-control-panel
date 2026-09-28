package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/inventory"
	"server-control-panel/internal/pve"
	"server-control-panel/internal/scope"
)

const (
	pveSecretAdmin      = "pve_token_admin"
	pveSecretAudit      = "pve_token_audit"
	pveSecretPanel      = "pve_token_panel"
	pveSecretNodePrefix = "pve_token_node_"
	pveTokenUser        = "panel@pve"
	pveTokenNodePrefix  = "node-"
)

const (
	vaultOK          = "ok"
	vaultMissing     = "absent"
	vaultUnreachable = "unreachable"
)

type hypervisorOps interface {
	Start(ctx context.Context, node string, vmid int, typ string) (string, error)
	Stop(ctx context.Context, node string, vmid int, typ string) (string, error)
	Shutdown(ctx context.Context, node string, vmid int, typ string) (string, error)
	WaitTask(ctx context.Context, node, upid string) error
	DeleteToken(ctx context.Context, user, tokenID string) error
	ListTokens(ctx context.Context, user string) ([]pve.TokenInfo, error)
	ClusterResources(ctx context.Context) ([]pve.Resource, error)

	NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error)
	TaskList(ctx context.Context, node string, opt pve.TaskListOptions) ([]pve.Task, error)
	TaskLog(ctx context.Context, node, upid string) ([]string, error)
	DisksList(ctx context.Context, node string) ([]pve.Disk, error)

	ZFSList(ctx context.Context, node string) ([]pve.ZPool, error)
	ZFSTopology(ctx context.Context, node, pool string) (pve.ZPoolTopology, error)

	DatastoreBackups(ctx context.Context, node, storage string) (pve.BackupFreshness, error)
	BackupJobs(ctx context.Context) ([]pve.BackupJob, error)

	RRDNode(ctx context.Context, node string, j pve.RRDWindow) ([]pve.RRDPoint, error)
	RRDGuest(ctx context.Context, node string, vmid int, typ string, j pve.RRDWindow) ([]pve.RRDPoint, error)
	Network(ctx context.Context, node string) ([]pve.Interface, error)
	DNS(ctx context.Context, node string) (pve.DNSInfo, error)
	Time(ctx context.Context, node string) (pve.TimeInfo, error)
	Certificates(ctx context.Context, node string) ([]pve.Certificate, error)
	Packages(ctx context.Context, node string) ([]pve.PackageInfo, error)
	Syslog(ctx context.Context, node string, limit int) ([]pve.SyslogLine, error)
	Permissions(ctx context.Context) (map[string]map[string]int, error)
	SnapshotList(ctx context.Context, node string, vmid int, typ string) ([]pve.Snapshot, error)
	SnapshotCreate(ctx context.Context, node string, vmid int, typ, name, description string) (string, error)
	SnapshotDelete(ctx context.Context, node string, vmid int, typ, name string) (string, error)

	ConsoleAttach(ctx context.Context, node string, vmid int, typ string) (pve.ConsoleConn, string, error)
	ConsoleAttachNode(ctx context.Context, node string) (pve.ConsoleConn, string, error)

	NodePower(ctx context.Context, node string, cmd pve.PowerCommand) (string, error)
	SnapshotRollback(ctx context.Context, node string, vmid int, typ, name string) (string, error)

	Reboot(ctx context.Context, node string, vmid int, typ string) (string, error)
	NextID(ctx context.Context) (int, error)
	Clone(ctx context.Context, node string, vmid int, typ string, newID int, name, snapname string) (string, error)
	VZDump(ctx context.Context, node string, vmid int, storage, mode, compress string) (string, error)

	Description(ctx context.Context, node string, vmid int, typ string) (string, error)

	SetDescription(ctx context.Context, node string, vmid int, typ, text string) error
}

type nodeVault interface {
	Get(key string) (string, bool)
	Delete(key string) error
}

func (r *Router) inventoryStoreOrNil(w http.ResponseWriter) *inventory.Store {
	if r.inventoryStore == nil {
		writeErr(w, 503, "inventory unavailable")
		return nil
	}
	return r.inventoryStore
}

func (r *Router) nodeVaultOrErr() (nodeVault, error) {
	if r.nodeVaultFn != nil {
		return r.nodeVaultFn()
	}
	if r.secrets == nil {
		return nil, errors.New("vault unavailable")
	}
	return scope.NewUserVault(r.secrets, scope.User(r.cfg.Primary)), nil
}

func (r *Router) vaultToken(key string) (value, state string) {
	v, err := r.nodeVaultOrErr()
	if err != nil {
		return "", vaultUnreachable
	}
	s, ok := v.Get(key)
	if !ok || strings.TrimSpace(s) == "" {
		return "", vaultMissing
	}
	return s, vaultOK
}

func (r *Router) dial(tokenValue string) (hypervisorOps, error) {
	if r.pveDial != nil {
		return r.pveDial(tokenValue)
	}
	if r.pveConfig == nil {
		return nil, errors.New("data/pve/pve.json missing — hypervisor not configured")
	}
	cfg := *r.pveConfig
	cfg.TokenID = tokenValue
	cfg.Secret = ""
	return pve.New(cfg)
}

func nodeSlug(n inventory.Node) string {
	s := strings.ToLower(strings.TrimSpace(n.Name))
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, s)
	return strings.Trim(s, "-")
}

func nodeKey(n inventory.Node) string {
	return pveSecretNodePrefix + strings.ReplaceAll(nodeSlug(n), "-", "_")
}
func nodeTokenID(n inventory.Node) string { return pveTokenNodePrefix + nodeSlug(n) }

func (r *Router) hypervisorReadSecret() string {
	if _, state := r.vaultToken(pveSecretPanel); state == vaultOK {
		return pveSecretPanel
	}
	return pveSecretAudit
}

func credentialKey(n inventory.Node) string {
	if n.Kind == inventory.NodeKindHost {
		return pveSecretAudit
	}
	return nodeKey(n)
}

func (r *Router) handleNodes(w http.ResponseWriter, req *http.Request) {
	if _, ok := r.mustPrimary(w, req); !ok {
		return
	}
	st := r.inventoryStoreOrNil(w)
	if st == nil {
		return
	}

	rest := strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/nodes"), "/")
	if rest == "" {
		if req.Method != http.MethodGet {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.listNodes(w, st)
		return
	}

	parts := strings.Split(rest, "/")
	verb := ""
	if n := len(parts); n > 1 {
		switch parts[n-1] {
		case "power", "credential", "clone", "backup", "note":
			verb = parts[n-1]
			parts = parts[:n-1]
		}
	}
	id := strings.Join(parts, "/")

	switch verb {
	case "":
		if req.Method != http.MethodGet {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.nodeDetail(w, st, id)
	case "power":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.nodePower(w, req, st, id)
	case "note":
		if req.Method != http.MethodGet && req.Method != http.MethodPut {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.nodeNote(w, req, st, id)
	case "clone":
		if req.Method != http.MethodGet && req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.nodeClone(w, req, st, id)
	case "backup":
		if req.Method != http.MethodPost {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.nodeBackup(w, req, st, id)
	case "credential":
		if req.Method != http.MethodDelete {
			writeErr(w, 405, "method not allowed")
			return
		}
		r.revokeCredential(w, req, st, id)
	}
}

func (r *Router) now() time.Time {
	if r.inventoryNow != nil {
		return r.inventoryNow()
	}
	return time.Now()
}

func (r *Router) ttl() time.Duration {
	if r.inventoryPoller != nil {
		return r.inventoryPoller.TTL()
	}
	return 90 * time.Second
}

func (r *Router) listNodes(w http.ResponseWriter, st *inventory.Store) {
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	seen := inventory.View(inv, r.ttl(), r.now())
	vaultState := r.enrichVault(seen)
	writeJSON(w, map[string]any{
		"nodes":       nonNil(seen),
		"ttl_seconds": int64(r.ttl().Seconds()),
		"vault":       vaultState,
		"observed_at": r.now().Unix(),
		"poll":        inventory.ViewPoll(inv, r.ttl(), r.now()),
	})
}

func (r *Router) enrichVault(seen []inventory.NodeView) string {
	v, err := r.nodeVaultOrErr()
	if err != nil {
		return vaultUnreachable
	}
	for i := range seen {
		if seen[i].Transport != inventory.TransportPVEAPI {
			continue
		}
		if seen[i].Credential.State == inventory.CredRevoked {
			continue
		}
		value, ok := v.Get(credentialKey(seen[i].Node))
		if !ok || strings.TrimSpace(value) == "" {
			seen[i].Credential.State = inventory.CredMissing
			seen[i].Credential.TokenID = ""
		}
	}
	return vaultOK
}

func (r *Router) nodeDetail(w http.ResponseWriter, st *inventory.Store, id string) {
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	seen := inventory.View(inv, r.ttl(), r.now())
	vaultState := r.enrichVault(seen)
	idx := -1
	for i := range seen {
		if seen[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	writeJSON(w, map[string]any{
		"node":        seen[idx],
		"services":    nonNilS(filterServices(inv.Services, id)),
		"deployments": nonNilD(filterDeployments(inv.Deployments, id)),
		"jobs":        nonNilJ(filterJobs(inv.Jobs, id)),
		"vault":       vaultState,
		"ttl_seconds": int64(r.ttl().Seconds()),
	})
}

func filterServices(in []inventory.Service, nodeID string) []inventory.Service {
	var out []inventory.Service
	for _, s := range in {
		if s.NodeID == nodeID {
			out = append(out, s)
		}
	}
	return out
}

func filterDeployments(in []inventory.Deployment, nodeID string) []inventory.Deployment {
	var out []inventory.Deployment
	for _, d := range in {
		if d.NodeID == nodeID {
			out = append(out, d)
		}
	}
	return out
}

func filterJobs(in []inventory.JobRef, nodeID string) []inventory.JobRef {
	var out []inventory.JobRef
	for _, j := range in {
		if j.NodeID == nodeID {
			out = append(out, j)
		}
	}
	return out
}

func nonNil(v []inventory.NodeView) []inventory.NodeView {
	if v == nil {
		return []inventory.NodeView{}
	}
	return v
}
func nonNilS(v []inventory.Service) []inventory.Service {
	if v == nil {
		return []inventory.Service{}
	}
	return v
}
func nonNilD(v []inventory.Deployment) []inventory.Deployment {
	if v == nil {
		return []inventory.Deployment{}
	}
	return v
}
func nonNilJ(v []inventory.JobRef) []inventory.JobRef {
	if v == nil {
		return []inventory.JobRef{}
	}
	return v
}

func findNode(inv inventory.Inventory, id string) (inventory.Node, bool) {
	for _, n := range inv.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return inventory.Node{}, false
}

func (r *Router) nodePower(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	action := strings.ToLower(strings.TrimSpace(body.Action))
	switch action {
	case "start", "stop", "shutdown", "reboot":
	default:
		writeErr(w, 400, "invalid action: "+action+" (start|stop|shutdown|reboot)")
		return
	}

	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	no, ok := findNode(inv, id)
	if !ok {
		writeErr(w, 404, "node not found: "+id)
		return
	}
	if no.Kind != inventory.NodeKindGuest || no.VMID <= 0 {
		writeErr(w, 400, "node "+id+" is not a guest of the hypervisor")
		return
	}

	value, state := r.vaultToken(nodeKey(no))
	switch state {
	case vaultUnreachable:
		writeErr(w, 503, "vault unreachable — the node credential could not be read")
		return
	case vaultMissing:
		writeErr(w, 409, "missing credential for node "+id)
		return
	}

	cli, err := r.dial(value)
	if err != nil {
		writeErr(w, 503, "hypervisor not configured: "+err.Error())
		return
	}

	kind, node := kindAndHost(no)
	ctx := req.Context()
	var upid string
	switch action {
	case "start":
		upid, err = cli.Start(ctx, node, no.VMID, kind)
	case "stop":
		upid, err = cli.Stop(ctx, node, no.VMID, kind)
	case "shutdown":
		upid, err = cli.Shutdown(ctx, node, no.VMID, kind)
	case "reboot":
		upid, err = cli.Reboot(ctx, node, no.VMID, kind)
	}
	if err != nil {
		writeErr(w, pveErrorCode(err), "hypervisor refused "+action+": "+err.Error())
		return
	}

	warnings, err := waitTask(ctx, cli, node, upid)
	if err != nil {
		r.auditEvent(req, auth.UserFrom(req), "pve.power", fmt.Sprintf("node=%s action=%s upid=%s status=failed", id, action, upid))
		writeErr(w, 502, "task "+upid+" did not finish cleanly: "+pveErrorDetail(err))
		return
	}

	r.auditEvent(req, auth.UserFrom(req), "pve.power",
		fmt.Sprintf("node=%s action=%s upid=%s status=ok warnings=%s", id, action, upid, warnings))
	writeJSON(w, map[string]any{"node": id, "action": action, "upid": upid, "status": "ok", "warnings": warnings})
}

func kindAndHost(n inventory.Node) (kind, host string) {
	kind = "lxc"
	if i := strings.Index(n.ID, "/"); i > 0 {
		kind = n.ID[:i]
	}
	return kind, "pve"
}

func pveErrorDetail(err error) string {
	msg := err.Error()
	var pe *pve.Error
	if errors.As(err, &pe) {
		if body := strings.TrimSpace(pe.Body); body != "" && !strings.Contains(msg, body) {
			msg += " [" + body + "]"
		}
	}
	return msg
}

func pveErrorCode(err error) int {
	var pe *pve.Error
	if !errors.As(err, &pe) {
		return 502
	}
	switch pe.Kind {
	case pve.KindNoCredential:
		return 401
	case pve.KindForbidden:
		return 403
	case pve.KindUnreachable:
		return 504
	default:
		return 502
	}
}

func (r *Router) revokeCredential(w http.ResponseWriter, req *http.Request, st *inventory.Store, id string) {
	inv, err := st.Snapshot()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	no, ok := findNode(inv, id)
	if !ok {
		writeErr(w, 404, "node not found: "+id)
		return
	}

	vault, err := r.nodeVaultOrErr()
	if err != nil {
		writeErr(w, 503, "vault unreachable — revocation aborted before touching the hypervisor")
		return
	}
	adminValue, adminState := r.vaultToken(pveSecretAdmin)
	if adminState != vaultOK {
		writeErr(w, 409, "admin token ("+pveSecretAdmin+") "+adminState+" — without it there is no way to revoke")
		return
	}
	nodeKeyName := nodeKey(no)
	nodeValue, nodeState := r.vaultToken(nodeKeyName)

	admin, err := r.dial(adminValue)
	if err != nil {
		writeErr(w, 503, "hypervisor not configured: "+err.Error())
		return
	}
	ctx := req.Context()

	if err := admin.DeleteToken(ctx, pveTokenUser, nodeTokenID(no)); err != nil {
		writeErr(w, pveErrorCode(err), "step=pve.delete failed: "+err.Error()+" (the vault was NOT touched)")
		return
	}

	if nodeState == vaultOK {
		operational, err := r.dial(nodeValue)
		if err == nil {
			_, errProbe := operational.ClusterResources(ctx)
			var pe *pve.Error
			if !errors.As(errProbe, &pe) || pe.Kind != pve.KindNoCredential {
				writeErr(w, 502, "step=pve.confirm401 failed: the revoked token still answers ("+fmt.Sprint(errProbe)+") — the vault was NOT touched")
				return
			}
		}
	}

	if err := vault.Delete(nodeKeyName); err != nil {
		writeErr(w, 500, "step=vault.delete failed: "+err.Error()+" (token ALREADY revoked on the hypervisor)")
		return
	}

	if _, still := vault.Get(nodeKeyName); still {
		writeErr(w, 500, "step=vault.recheck failed: the key "+nodeKeyName+" reappeared in the vault (concurrent write)")
		return
	}

	if err := st.Replace(func(iv *inventory.Inventory) {
		for i := range iv.Nodes {
			if iv.Nodes[i].ID == id {
				iv.Nodes[i].Credential.State = inventory.CredRevoked
				iv.Nodes[i].Credential.TokenID = ""
			}
		}
	}); err != nil {
		writeErr(w, 500, "revoked, but the inventory could not be updated: "+err.Error())
		return
	}

	r.auditEvent(req, auth.UserFrom(req), "pve.token.revoked", fmt.Sprintf("node=%s token=%s", id, nodeTokenID(no)))
	writeJSON(w, map[string]any{
		"node":  id,
		"token": nodeTokenID(no),
		"state": inventory.CredRevoked,
		"steps": []string{"pve.delete", "pve.confirm401", "vault.delete", "vault.recheck"},
	})
}

func loadPVEDescriptor(dataDir string) (*pve.Config, error) {
	path := filepath.Join(dataDir, "pve", "pve.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hypervisor descriptor missing (%s)", path)
	}
	var d struct {
		BaseURL    string `json:"base_url"`
		Resolve    string `json:"resolve"`
		ServerName string `json:"server_name"`
		CAFile     string `json:"ca_file"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("hypervisor descriptor %s malformed: %w", path, err)
	}
	ca := d.CAFile
	if ca != "" && !filepath.IsAbs(ca) {
		ca = filepath.Join(filepath.Dir(filepath.Dir(dataDir)), ca)
		if _, err := os.Stat(ca); err != nil {
			ca = filepath.Join(dataDir, "pve", filepath.Base(d.CAFile))
		}
	}
	return &pve.Config{
		BaseURL: d.BaseURL, Resolve: d.Resolve, ServerName: d.ServerName, CAFile: ca,
	}, nil
}

func (r *Router) startInventoryPoller(ctx context.Context) {
	if r.inventoryStore == nil {
		return
	}
	value, state := r.vaultToken(r.hypervisorReadSecret())
	if state != vaultOK || r.pveConfig == nil {
		log.Printf("inventory: poller not started (vault=%s, descriptor=%v) — the screen will show the age growing",
			state, r.pveConfig != nil)
		return
	}
	cfg := *r.pveConfig
	cfg.TokenID = value
	cli, err := pve.New(cfg)
	if err != nil {
		log.Printf("inventory: invalid PVE client (%v) — poller not started", err)
		return
	}
	p := inventory.NewPoller(r.inventoryStore, cli, inventory.Sources{
		Seeds:       inventory.SeedsSource(r.cfg.DataDir),
		Credentials: r.credentialSource(),
	}, inventory.PollerConfig{})
	r.inventoryPoller = p
	go p.Run(ctx)
}

const expiresCacheTTL = time.Hour

func (r *Router) credentialSource() func([]inventory.Node) (map[string]inventory.Credential, error) {
	var mu sync.Mutex
	var expires map[string]int64
	var readAt time.Time

	return func(nodes []inventory.Node) (map[string]inventory.Credential, error) {
		if r.secrets != nil {
			if _, err := r.secrets.ReloadIfChanged(); err != nil {
				log.Printf("inventory: the vault could not be re-read (%v) — carrying on with the in-memory copy", err)
			}
		}
		v, err := r.nodeVaultOrErr()
		if err != nil {
			return nil, err
		}

		mu.Lock()
		if expires == nil || time.Since(readAt) > expiresCacheTTL {
			if m, err := r.tokenExpires(); err != nil {
				log.Printf("inventory: token expiry unavailable (%v) — the screen will show the state without a deadline", err)
			} else {
				expires, readAt = m, time.Now()
			}
		}
		exp := expires
		mu.Unlock()

		out := make(map[string]inventory.Credential, len(nodes))
		for _, n := range nodes {
			if n.Transport != inventory.TransportPVEAPI {
				continue
			}
			value, ok := v.Get(credentialKey(n))
			if !ok || strings.TrimSpace(value) == "" {
				continue
			}
			tokenID, _, err := pve.SplitTokenValue(value)
			if err != nil {
				continue
			}
			out[n.ID] = inventory.Credential{TokenID: tokenID, Expire: exp[tokenName(tokenID)]}
		}
		return out, nil
	}
}

func tokenName(tokenID string) string {
	if i := strings.Index(tokenID, "!"); i >= 0 {
		return tokenID[i+1:]
	}
	return tokenID
}

func (r *Router) tokenExpires() (map[string]int64, error) {
	value, state := r.vaultToken(pveSecretAdmin)
	if state != vaultOK {
		return nil, fmt.Errorf("token admin %s: %s", pveSecretAdmin, state)
	}
	cli, err := r.dial(value)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	toks, err := cli.ListTokens(ctx, pveTokenUser)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(toks))
	for _, t := range toks {
		out[t.TokenID] = t.Expire
	}
	return out, nil
}
