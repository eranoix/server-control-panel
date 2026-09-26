// Package inventory holds the panel's multi-node model: what exists
// (Node, Service, Project, Deployment), what points to the executors that already
// exist (JobRef) and WHEN each number was observed (Observed).
//
// # Why a NEW package, and not `deploy` or `queue`
//
// The three name collisions were MEASURED in the source before this line was written:
//
//	queue.Job         internal/queue/queue.go:55          serialized QUEUE record
//	scheduler.Job     internal/scheduler/scheduler.go:30  CRON rule
//	deploy.Project    internal/deploy/store.go:124        is a FUNCTION, not a type
//
// Putting `Job` or `Project` in any of those packages either breaks the
// build or forces renaming a symbol in use — churn outside the scope of this
// work. In a package of its own, `inventory.Project` and `deploy.Project` coexist without
// seeing each other, because every use is qualified.
//
// # The inventory OBSERVES; it does not execute
//
// A direct consequence of the read-only scope and of the normative table
// ("stays in the lab-agent × goes to the PVE API"):
//
//   - JobRef is a REFERENCE to `queue.Job.ID` or `scheduler.Job.ID`, with the
//     same `Source` convention the queue already uses ("user" | "scheduler:<id>",
//     queue.go:70). There is no execution here, and the test TestJobRefIsReference
//     fails if anyone hangs a method on the type.
//   - Service points at the systemd unit the panel ALREADY manages; the inventory
//     only records on which node it lives.
//
// # The timestamp is inescapable
//
// Every observed number travels inside Observed[T], which ALWAYS carries the instant
// of the observation — including when it is zero. It is that obligation that makes
// "an old number presented as if it were live" structurally
// impossible: with no timestamp there is nothing to display. The age in seconds is born on the
// SERVER (freshness.go), never in the browser, which has another clock.
package inventory

import (
	"fmt"
	"strconv"
)

// SchemaVersion is the version of the envelope persisted in data/inventory/inventory.json.
// An envelope newer than this binary understands is an ERROR, never "start
// empty" (the same stance as internal/config/config_io.go: refuse and name the
// binary).
const SchemaVersion = 1

// Transport is HOW the panel reaches a node. A closed set.
type Transport string

const (
	// TransportAgente exists in the domain already, but has NO implementation
	// in this phase: the lab-agent comes later. The value is accepted by the
	// model and refused by whoever goes to dial it — faking support here would
	// be lying to the planning of the stage that follows.
	TransportAgente Transport = "agente"
	// TransportPVEAPI is the only transport with an implementation today
	// (internal/pve): the Proxmox API with a privsep=1 token.
	TransportPVEAPI Transport = "pve-api"
	// TransportSSH is the exception path for a node that is not a hypervisor guest.
	TransportSSH Transport = "ssh"
)

// Valido reports whether t is one of the three transports.
func (t Transport) Valido() bool {
	switch t {
	case TransportAgente, TransportPVEAPI, TransportSSH:
		return true
	}
	return false
}

// NodeKind separates the hypervisor from its guests and from what is neither.
type NodeKind string

const (
	NodeKindHost    NodeKind = "host"    // the hypervisor itself
	NodeKindGuest   NodeKind = "guest"   // LXC or QEMU managed by that hypervisor
	NodeKindExterno NodeKind = "externo" // a machine outside the hypervisor (e.g. a rented VPS)
)

// Valido reports whether k is one of the three node kinds.
func (k NodeKind) Valido() bool {
	switch k {
	case NodeKindHost, NodeKindGuest, NodeKindExterno:
		return true
	}
	return false
}

// Observed is a value together with the instant it was observed.
//
// 🔴 ObservedAt travels ALWAYS, even zeroed — it is the pin (test
// TestSerializationPin). The tag that erases a zeroed field from the JSON is
// forbidden here on purpose: with it, a zeroed timestamp would vanish from the
// payload, the browser would not find the field and the screen would go back to
// showing a number with no age.
type Observed[T any] struct {
	Value      T     `json:"value"`
	ObservedAt int64 `json:"observed_at"` // unix seconds; 0 = never observed
}

// Observe timestamps a value. The instant comes from OUTSIDE — no function in
// this package reads the clock on its own (see freshness.go).
func Observe[T any](v T, unixSeconds int64) Observed[T] {
	return Observed[T]{Value: v, ObservedAt: unixSeconds}
}

// NaoReportado is the marker for "the hypervisor did not tell me this number".
//
// It is the SAME idiom as AgeSeconds (freshness.go), which returns -1 for
// "never observed" instead of 0 — and for the same reason: on the screen, zero
// is an assertion. "0% of disk" reads as an empty disk; "0 B/s" reads as no
// traffic. Both are confident lies about data that does not exist.
const NaoReportado int64 = -1

// Credential states of a node. There are FOUR, distinct and never merged: the
// PVE 401 cannot tell a revoked credential from an expired one, and it is the
// `expire` kept locally that breaks the tie. The strings are a screen contract
// — changing them here changes what the operator reads.
const (
	CredOK       = "ok"       // token present and within its validity
	CredAusente  = "ausente"  // there is no token for this node in the vault
	CredRevogada = "revogada" // the panel deleted/revoked it (DELETE on the hypervisor + vault)
	CredExpirada = "expirada" // the token's `expire` has already passed — without calling the hypervisor
)

// Credential is what the panel KNOWS about a node's credential. The secret
// never lives here: it stays in the vault (internal/secrets), and this record
// only carries the identifier and the validity.
type Credential struct {
	TokenID string `json:"token_id"` // "lab@pve!<name>"; empty = absent
	Expire  int64  `json:"expire"`   // unix seconds; 0 = no declared expiry
	State   string `json:"state"`    // ok|ausente|revogada|expirada (ver constantes)
}

// Node is a node of the lab — the host, a guest or an external machine.
type Node struct {
	ID        string    `json:"id"`   // stable key; for a hypervisor guest it is the id from /cluster/resources ("lxc/207")
	Name      string    `json:"name"` // readable name ("apps")
	Transport Transport `json:"transport"`
	// Address is OPTIONAL: a guest on DHCP may expose no IP at all in its config
	// (measured in the research — `net0` only brings `ip=` when it is static).
	// Empty means "I do not know the address", and it stays in the JSON saying so.
	Address string   `json:"address"`
	Kind    NodeKind `json:"kind"`
	VMID    int      `json:"vmid"` // 0 when the node is not a hypervisor guest

	// 🔴 AusenteDesde separates "I have not seen it for N" from "the hypervisor
	// SAID it is not there any more". They are two different silences and they
	// ask opposite things of the operator.
	//
	// Zero = present in the last discovery. Non-zero = the timestamp of the
	// first SUCCESSFUL tick in which the hypervisor answered and this node did
	// not come back in the response.
	//
	// The node is NOT deleted, and the reason still holds: a guest can drop off
	// the list because it is powered off, migrated or had its ACL taken away,
	// and deleting it would be amnesia presented as truth. What changes is that
	// it stops being confused with a node that has merely aged: "stale" means
	// the panel could not look; this means the panel looked and did not find it.
	AusenteDesde int64 `json:"ausente_desde,omitempty"`

	Status Observed[string] `json:"status"` // "running"|"stopped"|… as the hypervisor returns it
	Uptime Observed[int64]  `json:"uptime"` // segundos

	// ── guest counters ──────────────────────────────────────────────────────
	//
	// 🔴 They live HERE, and not in a separate document like the Hypervisor,
	// because they come out of the SAME call that already fills Status and
	// Uptime (/cluster/resources). Same call ⇒ same timestamp ⇒ a single truth
	// about freshness. The Hypervisor became a document of its own for the
	// opposite reason: it comes from /nodes/{n}/status, which fails on its own.
	//
	// The rule for the day this changes: a field that starts coming from another
	// call LEAVES Node and becomes its own document, with its own timestamp.
	//
	// observedAtDoNo (freshness.go) is NOT taught to look at these fields, and
	// that is deliberate: it already resolves the node's age from Status/Uptime,
	// which are timestamped at the same instant. Touching it would change the
	// age of every node in the inventory because of one new field — and an empty
	// `git diff` on that file has been an acceptance criterion since the plan
	// that wrote it.
	CPUFrac  Observed[float64] `json:"cpu_frac"`  // 0..1, already normalised by the cores
	CPUCores Observed[int]     `json:"cpu_cores"` // cores the guest sees

	MemUsed  Observed[int64] `json:"mem_used"`  // bytes INSIDE the guest
	MemTotal Observed[int64] `json:"mem_total"` // bytes configurados
	MemHost  Observed[int64] `json:"mem_host"`  // RAM spent on the HOST; NaoReportado on LXC

	// 🔴 DiskUsed is NaoReportado (-1) when the hypervisor does not know — QEMU
	// without a guest-agent returns 0, and publishing 0 would draw an empty bar
	// over a number nobody measured. DiskTotal stays real: capacity is known
	// even without an agent.
	DiskUsed  Observed[int64] `json:"disk_used"`
	DiskTotal Observed[int64] `json:"disk_total"`

	// Counters accumulated since the guest booted, and the rates derived from
	// them. A NaoReportado rate means "there is no deriving it": either it is the
	// first observation, or there was a gap larger than the poller interval, or
	// the counter went backwards (the guest restarted). None of those becomes 0 —
	// 0 B/s reads as "no traffic", which is an assertion, not a gap.
	NetIn      Observed[int64] `json:"net_in"`
	NetOut     Observed[int64] `json:"net_out"`
	DiskRead   Observed[int64] `json:"disk_read"`
	DiskWrite  Observed[int64] `json:"disk_write"`
	NetInRate  Observed[int64] `json:"net_in_rate"`  // bytes/s
	NetOutRate Observed[int64] `json:"net_out_rate"` // bytes/s

	// Template is IDENTITY, not measurement: a template does not turn into a
	// guest you can power on as time passes. That is why it is a raw bool, like
	// ID and VMID.
	Template bool `json:"template"`

	Credential Credential `json:"credential"`
}

// Validate refuses a node with no key, with a transport outside the allowed set
// or with an unknown kind. The error QUOTES the value received: a wrong
// transport that becomes a "generic error" in the log is a defect nobody finds.
func (n Node) Validate() error {
	if n.ID == "" {
		return fmt.Errorf("node without ID: without a stable key the inventory merges entries")
	}
	if !n.Transport.Valido() {
		return fmt.Errorf("node %q: transport %s outside the set {agente, pve-api, ssh}",
			n.ID, strconv.Quote(string(n.Transport)))
	}
	if !n.Kind.Valido() {
		return fmt.Errorf("node %q: kind %s outside the set {host, guest, externo}",
			n.ID, strconv.Quote(string(n.Kind)))
	}
	return nil
}

// Service is a unit the panel already manages, situated on a node.
type Service struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
	Unit   string `json:"unit"` // systemd unit name, as the panel already knows it
}

// Project is a publishable application. It succeeds deploy.App — the migration
// lives elsewhere; here only the multi-node shape.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Deployment is a publication of a Project on a Node. What deploy.DeployRecord
// did not have: NodeID.
type Deployment struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	NodeID    string `json:"node_id"`
	Branch    string `json:"branch"`
}

// JobRef points at a job that ALREADY EXISTS in another package. Kind says in
// which one: "queue" (queue.Job, queue.go:55) or "scheduler" (scheduler.Job,
// scheduler.go:30). No methods — the inventory executes nothing.
type JobRef struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`    // "queue" | "scheduler"
	NodeID string `json:"node_id"` // where it ran/runs
	Source string `json:"source"`  // "user" | "scheduler:<job_id>" (the queue.go convention)
}

// Inventory is the whole document, exactly as it lives on disk.
//
// 🔴 Hypervisor is a NEW field and SchemaVersion does NOT change because of it
// — nobody "forgot to bump it". An old document on disk simply does not have
// the key and deserialises into the zero value, which is exactly the "never
// observed" state (age -1 on the screen) until the first tick. Bumping the
// version would make an earlier binary REFUSE a file it reads perfectly.
type Inventory struct {
	SchemaVersion int          `json:"schema_version"`
	Nodes         []Node       `json:"nodes"`
	Hypervisor    Hypervisor   `json:"hypervisor"`
	Services      []Service    `json:"services"`
	Projects      []Project    `json:"projects"`
	Deployments   []Deployment `json:"deployments"`
	Jobs          []JobRef     `json:"jobs"`

	// LastPollAt is the instant of the loop's last ATTEMPT — the SECOND clock of
	// the screen (see pollclock.go). It is stamped even when discovery fails, and
	// that is why it answers the question `age_seconds` does not answer: "did the
	// node go quiet, or did the panel stop asking?".
	//
	// No `omitempty`, like every timestamp in this package: 0 is "never tried"
	// and it has to reach the payload saying so.
	LastPollAt    int64  `json:"last_poll_at"`
	LastPollError string `json:"last_poll_error"`
}

// NodeView is the Node as the API delivers it: with the age ALREADY CALCULATED
// on the server. The two extra fields do not carry the tag that erases a zeroed
// field, for the same reason as Observed — an age absent from the payload is
// the screen with no age all over again. View(), in freshness.go, is what
// fills it in.
type NodeView struct {
	Node
	AgeSeconds int64 `json:"age_seconds"` // now - the node's most recent observed_at
	Stale      bool  `json:"stale"`       // AgeSeconds > TTL
}
