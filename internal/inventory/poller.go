package inventory

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/pve"
)

const (
	defaultInterval    = 30 * time.Second
	defaultTTL         = 90 * time.Second
	defaultTimeout     = 10 * time.Second
	defaultFanOut      = 4
	defaultAgentPingTO = 2 * time.Second
)

type PVESource interface {
	ClusterResources(ctx context.Context) ([]pve.Resource, error)
	GuestAddress(ctx context.Context, node string, vmid int, typ string) (string, error)
	NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error)

	StorageList(ctx context.Context, node string) ([]pve.Storage, error)
	ZFSList(ctx context.Context, node string) ([]pve.ZPool, error)

	Permissions(ctx context.Context) (map[string]map[string]int, error)
}

type Sources struct {
	Projects    func() ([]Project, []Deployment, error)
	Jobs        func() ([]JobRef, error)
	Services    func() ([]Service, error)
	Seeds       func() ([]Node, error)
	Credentials func(nodes []Node) (map[string]Credential, error)
	AgentPing   func(ctx context.Context, address string) error
}

type PollerConfig struct {
	Interval time.Duration
	TTL      time.Duration
	Timeout  time.Duration
	FanOut   int
	Now      func() time.Time
}

func (c *PollerConfig) applyDefaults() {
	if c.Interval <= 0 {
		c.Interval = defaultInterval
	}
	if c.TTL <= 0 {
		c.TTL = defaultTTL
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	if c.FanOut <= 0 {
		c.FanOut = defaultFanOut
	}
	if c.Now == nil {
		c.Now = NewClock().Now
	}
}

type Poller struct {
	store *Store
	pve   PVESource
	deps  Sources
	cfg   PollerConfig
}

func NewPoller(store *Store, src PVESource, deps Sources, cfg PollerConfig) *Poller {
	cfg.applyDefaults()
	return &Poller{store: store, pve: src, deps: deps, cfg: cfg}
}

func (p *Poller) TTL() time.Duration { return p.cfg.TTL }

func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	for {
		if err := p.tick(ctx); err != nil && ctx.Err() == nil {
			log.Printf("inventory poller: tick failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (p *Poller) tick(ctx context.Context) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("inventory poller: panic in tick: %v", rec)
		}
	}()

	tctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	now := p.cfg.Now().Unix()

	resources, errDiscovery := p.pve.ClusterResources(tctx)
	if errDiscovery != nil {
		p.recordAttempt(now, errDiscovery)
		if isCredentialError(errDiscovery) {
			if err := p.markCredentialRevoked(now); err != nil {
				return fmt.Errorf("%w (and failed to mark the credential: %v)", errDiscovery, err)
			}
		}
		return fmt.Errorf("discovery: %w", errDiscovery)
	}

	hvName := hypervisorName(resources)
	var hvHealth pve.NodeStatus
	hasHVHealth := false
	if hvName != "" {
		if st, err := p.pve.NodeStatus(tctx, hvName); err != nil {
			log.Printf("inventory poller: health of hypervisor %q unavailable (%v) — keeping the previous stamp", hvName, err)
		} else {
			hvHealth, hasHVHealth = st, true
		}
	}

	cap := p.collectCapacity(tctx, hvName)

	observed := p.addressesInParallel(tctx, resources)
	seeds := p.loadSeeds()
	alive := p.pingSeeds(tctx, seeds)

	projects, deployments := p.loadProjects()
	jobs := p.loadJobs()
	services := p.loadServices()

	var goneIDs []string
	errReplace := p.store.Replace(func(inv *Inventory) {
		inv.SchemaVersion = SchemaVersion
		inv.LastPollAt, inv.LastPollError = now, ""
		applyDiscovery(inv, resources, observed, now, p.maxGap())
		goneIDs = markGone(inv, resources, seeds, now)
		if hasHVHealth {
			applyHypervisor(inv, hvName, hvHealth, now)
		}
		if cap.hasPools {
			applyStorage(inv, cap.pools, now)
		}
		if cap.hasZPools {
			applyZPools(inv, cap.zpools, now)
		}
		if cap.hasVerdict {
			applyDatastoreAudit(inv, cap.canAudit, now)
		}
		applySeeds(inv, seeds, alive, now)
		p.applyCredentials(inv)
		if projects != nil {
			inv.Projects = projects
		}
		if deployments != nil {
			inv.Deployments = deployments
		}
		if jobs != nil {
			inv.Jobs = jobs
		}
		if services != nil {
			inv.Services = services
		}
	})
	if len(goneIDs) > 0 {
		log.Printf("inventory poller: %d node(s) are no longer listed by the hypervisor (marked absent, NOT deleted): %s",
			len(goneIDs), strings.Join(goneIDs, ", "))
	}
	return errReplace
}

func (p *Poller) recordAttempt(now int64, cause error) {
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	if err := p.store.Replace(func(inv *Inventory) {
		inv.LastPollAt, inv.LastPollError = now, reason
	}); err != nil {
		log.Printf("inventory poller: could not stamp the attempt (%v)", err)
	}
}

type collectedCapacity struct {
	pools     []pve.Storage
	hasPools  bool
	zpools    []pve.ZPool
	hasZPools bool

	canAudit   bool
	hasVerdict bool
}

func (p *Poller) collectCapacity(ctx context.Context, hvName string) collectedCapacity {
	var out collectedCapacity
	if hvName == "" {
		return out
	}
	if ss, err := p.pve.StorageList(ctx, hvName); err != nil {
		log.Printf("inventory poller: storage capacity of %q unavailable (%v) — keeping the previous stamp", hvName, err)
	} else {
		out.pools, out.hasPools = ss, true
	}
	if ps, err := p.pve.ZFSList(ctx, hvName); err != nil {
		log.Printf("inventory poller: zpools of %q unavailable (%v) — keeping the previous stamp", hvName, err)
	} else {
		out.zpools, out.hasZPools = ps, true
	}
	if perms, err := p.pve.Permissions(ctx); err != nil {
		log.Printf("inventory poller: permissions unavailable (%v) — keeping the previous datastore verdict", err)
	} else {
		out.canAudit, out.hasVerdict = pve.CanAuditDatastore(perms), true
	}
	return out
}

func (p *Poller) addressesInParallel(ctx context.Context, resources []pve.Resource) map[string]string {
	out := make(map[string]string, len(resources))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, p.cfg.FanOut)

	for _, r := range resources {
		if !r.IsGuest() {
			continue
		}
		wg.Add(1)
		go func(r pve.Resource) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			addr, err := p.pve.GuestAddress(ctx, r.Node, r.VMID, r.Type)
			if err != nil || addr == "" {
				return
			}
			mu.Lock()
			out[r.ID] = addr
			mu.Unlock()
		}(r)
	}
	wg.Wait()
	return out
}

func applyDiscovery(inv *Inventory, resources []pve.Resource, addrs map[string]string, now, maxGap int64) {
	byID := indexByID(inv.Nodes)

	hosts := map[string]bool{}
	for _, r := range resources {
		if r.Node != "" {
			hosts[r.Node] = true
		}
	}
	for name := range hosts {
		id := "node/" + name
		n := byID[id]
		n.ID, n.Name, n.Kind, n.Transport = id, name, NodeKindHost, TransportPVEAPI
		n.Status = Observe("online", now)
		byID[id] = n
	}

	for _, r := range resources {
		if !r.IsGuest() {
			continue
		}
		anterior := byID[r.ID]
		n := anterior
		n.ID, n.Name, n.Kind, n.Transport = r.ID, r.Name, NodeKindGuest, TransportPVEAPI
		n.VMID = r.VMID
		n.Status = Observe(r.Status, now)
		n.Uptime = Observe(r.Uptime, now)
		n.Template = r.Template == 1
		applyCounters(&n, anterior, r, now, maxGap)
		if addr, ok := addrs[r.ID]; ok {
			n.Address = addr
		}
		byID[r.ID] = n
	}

	inv.Nodes = sortByID(byID)
}

func removeGone(inv *Inventory, resources []pve.Resource, seeds []Node) []string {
	present := map[string]bool{}
	guests := 0
	for _, r := range resources {
		if r.Node != "" {
			present["node/"+r.Node] = true
		}
		if r.IsGuest() {
			present[r.ID] = true
			guests++
		}
	}
	if guests == 0 {
		return nil
	}
	declaredIDs := make(map[string]bool, len(seeds))
	for _, s := range seeds {
		declaredIDs[s.ID] = true
	}

	remaining := make([]Node, 0, len(inv.Nodes))
	var removed []string
	for _, n := range inv.Nodes {
		if n.Transport != TransportPVEAPI || declaredIDs[n.ID] || present[n.ID] {
			remaining = append(remaining, n)
			continue
		}
		removed = append(removed, n.ID)
	}
	if len(removed) == 0 {
		return nil
	}
	inv.Nodes = remaining
	return removed
}

func markGone(inv *Inventory, resources []pve.Resource, seeds []Node, now int64) []string {
	present := map[string]bool{}
	guests := 0
	for _, r := range resources {
		if r.Node != "" {
			present["node/"+r.Node] = true
		}
		if r.IsGuest() {
			present[r.ID] = true
			guests++
		}
	}
	if guests == 0 {
		return nil
	}
	declaredIDs := make(map[string]bool, len(seeds))
	for _, s := range seeds {
		declaredIDs[s.ID] = true
	}

	var added []string
	for i := range inv.Nodes {
		n := &inv.Nodes[i]
		if n.Transport != TransportPVEAPI || declaredIDs[n.ID] {
			continue
		}
		if present[n.ID] {
			n.MissingSince = 0
			continue
		}
		if n.MissingSince == 0 {
			n.MissingSince = now
			added = append(added, n.ID)
		}
	}
	return added
}

func (p *Poller) maxGap() int64 {
	return int64(p.cfg.Interval.Seconds()*3) / 2
}

func applyCounters(n *Node, anterior Node, r pve.Resource, now, maxGap int64) {
	n.CPUFrac = Observe(r.CPU, now)
	n.CPUCores = Observe(r.MaxCPU, now)
	n.MemUsed = Observe(r.Mem, now)
	n.MemTotal = Observe(r.MaxMem, now)
	n.MemHost = Observe(unknownIfZero(r.MemHost), now)
	n.DiskUsed = Observe(unknownIfZero(r.Disk), now)
	n.DiskTotal = Observe(r.MaxDisk, now)
	n.NetIn = Observe(r.NetIn, now)
	n.NetOut = Observe(r.NetOut, now)
	n.DiskRead = Observe(r.DiskRead, now)
	n.DiskWrite = Observe(r.DiskWrite, now)
	n.NetInRate = Observe(rateBetweenObservations(anterior.NetIn, r.NetIn, now, maxGap), now)
	n.NetOutRate = Observe(rateBetweenObservations(anterior.NetOut, r.NetOut, now, maxGap), now)
}

func unknownIfZero(v int64) int64 {
	if v <= 0 {
		return NotReported
	}
	return v
}

func rateBetweenObservations(anterior Observed[int64], current, now, maxGap int64) int64 {
	if anterior.ObservedAt <= 0 || now <= anterior.ObservedAt {
		return NotReported
	}
	interval := now - anterior.ObservedAt
	if maxGap > 0 && interval > maxGap {
		return NotReported
	}
	if current < anterior.Value {
		return NotReported
	}
	return (current - anterior.Value) / interval
}

func indexByID(nodes []Node) map[string]Node {
	m := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n
	}
	return m
}

func sortByID(m map[string]Node) []Node {
	out := make([]Node, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func isCredentialError(err error) bool {
	var pe *pve.Error
	return errors.As(err, &pe) && pe.Kind == pve.KindNoCredential
}

func (p *Poller) markCredentialRevoked(now int64) error {
	return p.store.Replace(func(inv *Inventory) {
		for i := range inv.Nodes {
			if inv.Nodes[i].Transport != TransportPVEAPI {
				continue
			}
			c := inv.Nodes[i].Credential
			if c.Expire > 0 && c.Expire < now {
				continue
			}
			inv.Nodes[i].Credential.State = CredRevoked
		}
	})
}

func (p *Poller) loadSeeds() []Node {
	if p.deps.Seeds == nil {
		return nil
	}
	seeds, err := p.deps.Seeds()
	if err != nil {
		log.Printf("inventory poller: seeds unreadable: %v", err)
		return nil
	}
	return seeds
}

func (p *Poller) pingSeeds(ctx context.Context, seeds []Node) map[string]bool {
	alive := map[string]bool{}
	ping := p.deps.AgentPing
	if ping == nil {
		ping = pingHealthz
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, p.cfg.FanOut)
	for _, s := range seeds {
		if s.Transport != TransportAgent || s.Address == "" {
			continue
		}
		wg.Add(1)
		go func(s Node) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if err := ping(ctx, s.Address); err != nil {
				return
			}
			mu.Lock()
			alive[s.ID] = true
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return alive
}

func pingHealthz(ctx context.Context, address string) error {
	cctx, cancel := context.WithTimeout(ctx, defaultAgentPingTO)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, "http://"+address+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}

func applySeeds(inv *Inventory, seeds []Node, alive map[string]bool, now int64) {
	if len(seeds) == 0 {
		return
	}
	byID := indexByID(inv.Nodes)
	for _, s := range seeds {
		n, existed := byID[s.ID]
		if !existed {
			n = s
		} else {
			n.Name, n.Transport, n.Address, n.Kind = s.Name, s.Transport, s.Address, s.Kind
		}
		if alive[s.ID] {
			n.Status = Observe("running", now)
		}
		byID[s.ID] = n
	}
	inv.Nodes = sortByID(byID)
}

func (p *Poller) loadProjects() ([]Project, []Deployment) {
	if p.deps.Projects == nil {
		return nil, nil
	}
	projs, deps, err := p.deps.Projects()
	if err != nil {
		log.Printf("inventory poller: projects unreadable: %v", err)
		return nil, nil
	}
	return projs, deps
}

func (p *Poller) loadJobs() []JobRef {
	if p.deps.Jobs == nil {
		return nil
	}
	jobs, err := p.deps.Jobs()
	if err != nil {
		log.Printf("inventory poller: jobs unreadable: %v", err)
		return nil
	}
	return jobs
}

func (p *Poller) loadServices() []Service {
	if p.deps.Services == nil {
		return nil
	}
	svcs, err := p.deps.Services()
	if err != nil {
		log.Printf("inventory poller: services unreadable: %v", err)
		return nil
	}
	return svcs
}

func (p *Poller) applyCredentials(inv *Inventory) {
	if p.deps.Credentials == nil {
		return
	}
	creds, err := p.deps.Credentials(inv.Nodes)
	if err != nil {
		log.Printf("inventory poller: credentials unreadable (%v) — keeping the last known state", err)
		return
	}
	for i := range inv.Nodes {
		c, hasEntry := creds[inv.Nodes[i].ID]
		if !hasEntry {
			if inv.Nodes[i].Credential.State == CredRevoked {
				continue
			}
			inv.Nodes[i].Credential = Credential{}
			continue
		}
		inv.Nodes[i].Credential = Credential{TokenID: c.TokenID, Expire: c.Expire}
	}
}
