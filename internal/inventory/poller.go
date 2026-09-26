package inventory

// The loop that keeps the inventory alive.
//
// # Template and precedent
//
// The loop STRUCTURE is the one in internal/api/api.go:1241 (startMetricsCollector):
// goroutine + ticker + `select` on ctx + `recover()` + `context.WithTimeout`
// per cycle. None of that is new.
//
// What IS a new precedent in the repository: the concurrent FAN-OUT per node
// (WaitGroup + semaphore). It exists for a measured reason, not for elegance —
// PVE delays EVERY 401 response by 3 seconds on purpose
// (PVE::APIServer::AnyEvent, measured here at 3.079 s). With N nodes and M
// invalid credentials, a serial tick costs M × 3 s of IMPOSED waiting. With 4
// invalid nodes and a 30 s tick the cycle never closes — and the age of the data starts
// growing because of the poller, not because of the observed node.
//
// # Three invariants the rest of the design depends on
//
//  1. The timestamp comes from the SERVER. Not one line of this file calls
//     time.Now(): the clock enters through cfg.Now. A test without an injectable clock
//     cannot prove expiry without waiting, and waiting is forbidden.
//  2. Failure does NOT erase. A discovery that fails leaves the nodes where they are, with the
//     OLD timestamp — that is how the screen says "data from 5 min ago" instead of
//     "there are no nodes". Silent amnesia is worse than stale data.
//  3. No real VMID or hostname lives here. Everything comes from the hypervisor,
//     from the seeds or from the injected sources — that is what makes a NEW guest show up
//     by itself instead of waiting for someone to edit JSON.

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

// Defaults recorded here as judgement calls: 30 s of cadence, 90 s of TTL
// (three lost ticks = expired) and a 10 s ceiling per call — the same floor as
// the hypervisor client, which exists so the delayed 401 does not turn into
// "unreachable".
const (
	defaultInterval    = 30 * time.Second
	defaultTTL         = 90 * time.Second
	defaultTimeout     = 10 * time.Second
	defaultFanOut      = 4
	defaultAgentPingTO = 2 * time.Second
)

// PVESource is the slice of the hypervisor client that the poller uses. A local
// interface (not in the pve package) so the test fake needs no mock framework —
// and so the poller cannot call anything beyond these two READ verbs.
type PVESource interface {
	ClusterResources(ctx context.Context) ([]pve.Resource, error)
	GuestAddress(ctx context.Context, node string, vmid int, typ string) (string, error)
	// NodeStatus is the HOST's health (RAM, swap, rootfs, load, uptime, version,
	// KSM). Measured cost: 92 ms — it fits with wide margin inside a 30 s tick, and
	// even with address resolution around it the cycle only rises by some 250 ms.
	// That is why it goes HERE and not in a second loop: two loops would be two
	// TTLs, that is, two truths about what is expired.
	//
	// ⚠️ /nodes/{n}/services (766 ms, ~8× the measured median) stays OUT of this
	// tick on purpose. If it ever comes in, let it come in on demand.
	NodeStatus(ctx context.Context, node string) (pve.NodeStatus, error)

	// The three that came later. They enter the tick by the same criterion that
	// decided NodeStatus, and the criterion is MEASURED COST:
	//
	//	/nodes/{n}/status     92 ms  ← was already here
	//	/nodes/{n}/storage   168 ms
	//	/nodes/{n}/disks/zfs  96 ms
	//	/access/permissions   85 ms
	//	/nodes/{n}/disks/list 597 ms ← stayed on demand, and stays out
	//
	// Capacity and zpool are HEARTBEAT, not navigation: a pool that stops being
	// observed has to show up with its AGE GROWING, and that only exists if
	// somebody observes without anybody asking. On demand, the age would always be
	// "0 s" — which is stale data presented as live, the very defect the freshness
	// criterion exists to forbid.
	StorageList(ctx context.Context, node string) ([]pve.Storage, error)
	ZFSList(ctx context.Context, node string) ([]pve.ZPool, error)

	// Permissions answers WHY the storage list is empty. Without it, a 200 with []
	// is indistinguishable from "there is no storage" — the trap the first
	// measurement caught, and which does not go away just because the ACL was
	// granted: it comes back, silent, the day the privilege is withdrawn.
	Permissions(ctx context.Context) (map[string]map[string]int, error)
}

// Sources are the reference sources aggregated in the same tick. All of them
// are injected functions: the inventory OBSERVES, and cannot become the owner
// of any of them. Any one of them nil is simply skipped.
type Sources struct {
	// Projects returns the Projects and Deployments of the deploy store.
	Projects func() ([]Project, []Deployment, error)
	// Jobs returns REFERENCES to queue.Job (queue.go) and scheduler.Job
	// (scheduler.go). Never a new executor — see JobRef in model.go.
	Jobs func() ([]JobRef, error)
	// Services returns the units the panel already manages.
	Services func() ([]Service, error)
	// Seeds returns the declared nodes that are NOT hypervisor guests.
	Seeds func() ([]Node, error)
	// Credentials returns what the panel KNOWS about each node's credential,
	// indexed by Node.ID. It receives the already-discovered nodes because the
	// vault key is derived from the node (name → slug), and the inventory cannot
	// reach the vault on its own — what joins the two halves is the handler.
	//
	// 🔴 Without this source, Credential.TokenID stays empty and credentialState()
	// returns "absent" for EVERY node — including the ones with a live token. That
	// was exactly the defect only the LIVE call revealed: 11 nodes reading "no
	// credential" with a full vault, and the expiry warning unable to fire at all
	// because Expire stayed 0.
	Credentials func(nodes []Node) (map[string]Credential, error)
	// AgentPing checks the liveness of a node on the "agent" transport. Nil uses
	// the default GET /healthz.
	AgentPing func(ctx context.Context, address string) error
}

// PollerConfig are the loop's knobs. Zero in any field falls back to the default.
type PollerConfig struct {
	Interval time.Duration
	TTL      time.Duration
	Timeout  time.Duration
	FanOut   int
	// Now is the clock. ALWAYS injected — see invariant 1 in the header.
	Now func() time.Time
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
		// The only point in the file that knows where time comes from, and it exists
		// only for the production caller that passed no clock.
		c.Now = NewClock().Now
	}
}

// Poller discovers and timestamps. It decides nothing about freshness — what
// reads the timestamp and says "expired" is freshness.go, at serialisation time.
type Poller struct {
	store *Store
	pve   PVESource
	deps  Sources
	cfg   PollerConfig
}

// NewPoller assembles the loop. The TTL is kept only for whoever wants to
// consult it (the view gets its TTL from whoever serialises).
func NewPoller(store *Store, src PVESource, deps Sources, cfg PollerConfig) *Poller {
	cfg.applyDefaults()
	return &Poller{store: store, pve: src, deps: deps, cfg: cfg}
}

// TTL returns the configured freshness window, so the handler uses the SAME one
// as the poller — two different TTLs would make the screen disagree with the loop.
func (p *Poller) TTL() time.Duration { return p.cfg.TTL }

// Run runs the loop until the ctx dies. The first tick fires IMMEDIATELY: a
// panel that comes up and sits 30 s with no inventory looks broken.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	for {
		if err := p.tick(ctx); err != nil && ctx.Err() == nil {
			// A tick error is NEWS, not a fatality: the inventory stays on disk with the
			// old timestamp and the screen shows the age growing.
			log.Printf("inventory poller: tick failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// tick is one complete cycle. It returns an error for the test and for the log;
// it never panics outward (the recover is here, not in the caller).
func (p *Poller) tick(ctx context.Context) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			// A panic in an injected source must not kill the loop's goroutine — the panel
			// would sit with no inventory until the next restart, silently.
			err = fmt.Errorf("inventory poller: panic in tick: %v", rec)
		}
	}()

	tctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	now := p.cfg.Now().Unix()

	resources, errDiscovery := p.pve.ClusterResources(tctx)
	if errDiscovery != nil {
		// 🔴 The ATTEMPT is timestamped even here, and this is the whole point of the
		// second clock (pollclock.go). Recording only success would make a poller that
		// has been failing for half an hour look identical, on screen, to a poller that
		// died half an hour ago — and the difference between the two is who the
		// operator wakes at three in the morning.
		p.recordAttempt(now, errDiscovery)
		// 🔴 Invariant 2: a discovery failure does NOT erase and does not create. The
		// only side effect allowed is marking a credential when the hypervisor
		// explicitly said 401.
		if isCredentialError(errDiscovery) {
			if err := p.markCredentialRevoked(now); err != nil {
				return fmt.Errorf("%w (and failed to mark the credential: %v)", errDiscovery, err)
			}
		}
		return fmt.Errorf("discovery: %w", errDiscovery)
	}

	// The hypervisor's health, in the SAME tick and with the SAME timestamp.
	// Failing here is news, not a fatality: the document keeps the OLD values and
	// the OLD timestamp, and the screen shows the age growing — which is the
	// behaviour the freshness criterion asks for (invariant 2).
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

	// Capacity, zpool and the privilege verdict — in the SAME tick, with the SAME
	// timestamp, and each one failing on its own account. See collectCapacity.
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
		// Right after discovery: whatever the hypervisor stopped listing is STAMPED as
		// absent. Nobody is deleted — see markGone.
		goneIDs = markGone(inv, resources, seeds, now)
		if hasHVHealth {
			applyHypervisor(inv, hvName, hvHealth, now)
		}
		// 🔴 Each apply* is conditional ON ITS OWN. Merging the three into a single
		// `if` would make a failure of /access/permissions erase the timestamp of the
		// capacity that was JUST observed — and the screen would show a growing age
		// over a brand-new number.
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
	// Outside the Replace: recording inside it would hold the document's lock for
	// the duration of a log write to disk. And recording is mandatory — a node that
	// vanishes from the screen leaving no trace turns "where is my guest?" into an
	// investigation.
	if len(goneIDs) > 0 {
		log.Printf("inventory poller: %d node(s) are no longer listed by the hypervisor (marked absent, NOT deleted): %s",
			len(goneIDs), strings.Join(goneIDs, ", "))
	}
	return errReplace
}

// recordAttempt writes the second clock's timestamp when the tick does not
// reach the final Replace. It touches NO node: the write is only the loop's
// clock and the reason, and that is why the failure goes on obeying invariant 2.
//
// Failing to write here is a log line, never a tick error: the defect one is
// trying to report is precisely what matters, and overwriting it with a disk
// error would hide the news behind the messenger.
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

// collectedCapacity is the result of the three capacity calls. The `tem*`
// flags are the point: `false` is not "it came back empty", it is "I DID NOT
// ASK, or nobody answered me" — and only whoever answered has any claim to a
// new timestamp (invariant 2).
type collectedCapacity struct {
	pools     []pve.Storage
	hasPools  bool
	zpools    []pve.ZPool
	hasZPools bool

	canAudit   bool
	hasVerdict bool
}

// collectCapacity fetches storage, zpool and the privilege verdict. A failure
// of any one of them is NEWS (a log line), never a fatality for the tick: the
// document keeps the old value and the old timestamp, and it is the age growing
// on screen that denounces it.
//
// With no discovered hypervisor name, nothing is asked — a request with no
// target is not observation, it is noise (invariant 3).
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
	// 🔴 The verdict is what separates "there is no storage" from "I cannot see
	// storage". When the verdict itself fails, the OLD one is kept on purpose:
	// writing `false` here would turn a network failure into an accusation of
	// missing permission on screen.
	if perms, err := p.pve.Permissions(ctx); err != nil {
		log.Printf("inventory poller: permissions unavailable (%v) — keeping the previous datastore verdict", err)
	} else {
		out.canAudit, out.hasVerdict = pve.CanAuditDatastore(perms), true
	}
	return out
}

// addressesInParallel resolves each guest's address concurrently, capped by
// FanOut. A failure on ONE address brings down neither the others nor the tick:
// what failed was the address, not the guest's existence.
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
				// An absent address is a VALID state (a guest on DHCP declares no ip=).
				// Nothing to record.
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

// applyDiscovery does the upsert by SET. Three cases, and the third is what
// tells this inventory apart from a dumb cache:
//
//	in PVE, not in store   → create    (a brand-new guest shows up by itself)
//	in both                → update    (timestamp advances)
//	only in store          → KEEP      (old timestamp; freshness signals it)
//
// The third never deletes. A guest that vanishes from the hypervisor becomes "not seen for X
// min" on screen; erasing it would be amnesia presented as truth.
func applyDiscovery(inv *Inventory, resources []pve.Resource, addrs map[string]string, now, maxGap int64) {
	byID := indexByID(inv.Nodes)

	// The hypervisor itself: it does not appear in /cluster/resources?type=vm, but
	// its name comes in every row. Deriving it from here keeps discovery driven by
	// the hypervisor — no hostname written by hand in this file.
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
		// It answered the API at this instant: that is observation, not assumption.
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

// removeGone deletes from the inventory the nodes the hypervisor STOPPED
// LISTING.
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 WHY THIS HAD TO EXIST
//
// `applyDiscovery` only merges: a guest that vanishes from /cluster/resources
// stays in the document forever, ageing. The operator saw it on the screen — a
// destroyed guest went on being listed as "expired", inflating "Total 12" and
// the expired count. A ghost node in a health list is noise in the one place
// where noise costs most: the band that exists to say whether there is anything
// to do NOW.
//
// 🔴 AND WHY IT IS COWARDLY, ON PURPOSE
//
// Deleting by absence is the most dangerous operation in this file: confusing
// "the hypervisor did not tell me" with "the thing does not exist" erases real
// inventory. The three guards below are what separates one from the other, and
// each covers a distinct way of getting it wrong:
//
//  1. IT ONLY RUNS ON THE SUCCESS PATH. Its caller is the tick's Replace, which
//     only happens after discovery answered. A discovery failure goes on
//     obeying invariant 2 — it neither erases nor creates — and returns well
//     before this.
//  2. A RESPONSE WITH NO GUEST AT ALL DELETES NOTHING. A /cluster/resources that
//     comes back empty from a passing upset in the hypervisor would erase the
//     whole laboratory in one go. An empty list is precisely the response one
//     can trust least.
//  3. IT ONLY TOUCHES `pve-api`. A node declared in seeds (agent, ssh) is not
//     discovered by the hypervisor and its absence here means nothing. The
//     canary test node is exactly that case.
//
// It returns the removed IDs because a node that vanishes from the screen MUST
// NOT VANISH SILENTLY: the caller records them, and "where is my guest?" starts
// having an answer in the log.
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
	// Guard 2: with no guest in the response, nobody is deleted.
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
		// Guard 3: only what the hypervisor owns the discovery of.
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

// markGone stamps the nodes the hypervisor STOPPED LISTING — and deletes
// none of them.
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 WHY MARK, AND NOT REMOVE
//
// Removing was my first idea, and it had already been refused, with reason: a
// guest vanishes from the list for being powered off, migrated or having its
// ACL withdrawn — not only for having been destroyed. Erasing it is amnesia
// presented as truth, and two existing pinning tests defend that.
//
// But keeping it and calling it "expired" was wrong too, and that is what the
// operator saw: a destroyed clone stayed in the list forever, inflating the
// total and the expired count. "Expired" means the panel COULD NOT LOOK. Here
// the panel looked and did not find it. Those are two different silences, and
// mixing them spends the health band, which exists to say whether there is
// anything to do NOW.
//
// 🔴 THE THREE GUARDS, each covering a distinct way of confusing "nobody told
// me" with "it is not there":
//
//  1. SUCCESS PATH ONLY. Its caller is the tick's Replace, which only runs
//     after discovery answered. A discovery failure returns before and goes on
//     obeying invariant 2.
//  2. A RESPONSE WITH NO GUEST AT ALL MARKS NOTHING. A /cluster/resources empty
//     from a passing upset would mark the whole laboratory as absent.
//  3. `pve-api` ONLY. A node declared in seeds (agent, ssh) is not discovered by
//     the hypervisor; its absence here means nothing. The canary test node is that.
//
// The timestamp is that of the FIRST tick in which the absence was seen, and it
// is not rewritten on every tick: it is what says "gone for how long". And
// showing up again CLEARS the mark — a node that came back is not an absent node.
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
	if guests == 0 { // invariant 2
		return nil
	}
	declaredIDs := make(map[string]bool, len(seeds))
	for _, s := range seeds {
		declaredIDs[s.ID] = true
	}

	var added []string
	for i := range inv.Nodes {
		n := &inv.Nodes[i]
		if n.Transport != TransportPVEAPI || declaredIDs[n.ID] { // invariant 3
			continue
		}
		if present[n.ID] {
			// It came back: the mark goes. Without this, a guest that returned would stay
			// labelled absent forever.
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

// maxGap is the interval above which two observations stop being neighbours.
// One tick and a half: one lost tick still produces an honest average, two are
// already minutes in which the panel was blind and whose "average" would be
// invention.
func (p *Poller) maxGap() int64 {
	return int64(p.cfg.Interval.Seconds()*3) / 2
}

// applyCounters translates one row of /cluster/resources into the Node's
// timestamped fields — and this is where "0" becomes "I do not know" when that
// is the case.
func applyCounters(n *Node, anterior Node, r pve.Resource, now, maxGap int64) {
	n.CPUFrac = Observe(r.CPU, now)
	n.CPUCores = Observe(r.MaxCPU, now)
	n.MemUsed = Observe(r.Mem, now)
	n.MemTotal = Observe(r.MaxMem, now)
	n.MemHost = Observe(unknownIfZero(r.MemHost), now)
	// 🔴 The disk pair: usage becomes NotReported when the hypervisor returns 0
	// (QEMU with no guest-agent, measured on both QEMU guests in this house), but
	// the CAPACITY stays real — it is known with no agent at all.
	n.DiskUsed = Observe(unknownIfZero(r.Disk), now)
	n.DiskTotal = Observe(r.MaxDisk, now)
	n.NetIn = Observe(r.NetIn, now)
	n.NetOut = Observe(r.NetOut, now)
	n.DiskRead = Observe(r.DiskRead, now)
	n.DiskWrite = Observe(r.DiskWrite, now)
	n.NetInRate = Observe(rateBetweenObservations(anterior.NetIn, r.NetIn, now, maxGap), now)
	n.NetOutRate = Observe(rateBetweenObservations(anterior.NetOut, r.NetOut, now, maxGap), now)
}

// unknownIfZero is the translation of "the hypervisor returned 0 because it
// does not know". It exists as a one-line function so that the reason is
// written ONCE and so the pinning test has a name to cite.
func unknownIfZero(v int64) int64 {
	if v <= 0 {
		return NotReported
	}
	return v
}

// 🔴 rateBetweenObservations derives bytes/s from two accumulated counters, and
// REFUSES to derive in three cases — each one of them a way of lying:
//
//	no previous observation       → there is nothing to derive from
//	gap larger than maxGap     → the average would cover minutes nobody watched
//	counter lower than the last   → the guest restarted; this is not negative traffic
//
// The alternative (dividing anyway) draws a straight line across an absence:
// exactly what a graph must not do, because the operator has no way of telling
// "that is how it was" from "I was not looking".
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

// sortByID returns the nodes in stable ID order — two consecutive reads have
// to produce the same document, otherwise the file's diff turns into noise.
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

// isCredentialError says whether the hypervisor answered 401. It is the ONLY
// error that authorises touching credential state — 403 is ACL, transport is
// the network.
func isCredentialError(err error) bool {
	var pe *pve.Error
	return errors.As(err, &pe) && pe.Kind == pve.KindNoCredential
}

// markCredentialRevoked records the 401 on the nodes that speak the
// hypervisor API.
//
// 🔴 The caveat: the hypervisor's 401 is INDISTINGUISHABLE between a revoked
// token and an expired one. What breaks the tie is the `expire` the panel
// stored. That is why the "revoked" mark only goes to the ones that have NOT
// yet expired by the local clock; on the expired ones credentialState()
// (freshness.go) already answers "expired" on its own, and overwriting that
// would erase the only clue the operator has that the problem is the calendar,
// not security.
func (p *Poller) markCredentialRevoked(now int64) error {
	return p.store.Replace(func(inv *Inventory) {
		for i := range inv.Nodes {
			if inv.Nodes[i].Transport != TransportPVEAPI {
				continue
			}
			c := inv.Nodes[i].Credential
			if c.Expire > 0 && c.Expire < now {
				continue // already expired: CredExpired is the correct reading
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

// pingSeeds checks the liveness of nodes on the "agent" transport. It returns
// the set of those that answered — the rest get NO new timestamp, which is
// exactly how a dead canary ends up with its age growing while the hypervisor
// nodes from the same tick advance.
//
// The "ssh" transport has no active poll here: there is no ssh client in this
// package and there will not be — reach by agent is separate work (lab-agent).
// Pretending to support it would stamp liveness nobody observed.
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

// pingHealthz is the default poll of the agent transport. A short timeout
// because the goal is to stamp liveness, not to download data.
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

// applySeeds integrates the declared nodes. A seed never overwrites the
// timestamp of a node the hypervisor has just observed; and a seed that did not
// answer the ping keeps the old timestamp (or zero, if it never answered) — it
// is the growing age that denounces the dead node.
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
			// The seed's declarative fields rule; the timestamp belongs to the observer.
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

// applyCredentials fills in what the panel knows about each node's credential.
//
// Two rules, and both have already cost dearly:
//
//  1. REVOKED BEATS ABSENT. After a successful revocation the key VANISHES from
//     the vault (that is the third step of the revocation), so the source
//     returns no entry for that node. Overwriting there would erase the record
//     of the revocation itself and the screen would say "never had a
//     credential" about a freshly revoked token.
//  2. AN UNAVAILABLE SOURCE IS NOT AN ABSENT CREDENTIAL. A vault that is down
//     returns an error, and in that case NOTHING is touched — the panel keeps
//     the last known state instead of announcing that every node lost its
//     credential.
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
				continue // rule 1
			}
			inv.Nodes[i].Credential = Credential{}
			continue
		}
		// The state is left EMPTY on purpose: what resolves it is credentialState() at
		// serialisation time, with the clock of whoever serialises. Writing "ok" here
		// would freeze a verdict that depends on time.
		inv.Nodes[i].Credential = Credential{TokenID: c.TokenID, Expire: c.Expire}
	}
}
