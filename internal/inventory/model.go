package inventory

import (
	"fmt"
	"strconv"
)

const SchemaVersion = 1

type Transport string

const (
	TransportAgent  Transport = "agent"
	TransportPVEAPI Transport = "pve-api"
	TransportSSH    Transport = "ssh"
)

func (t Transport) Valid() bool {
	switch t {
	case TransportAgent, TransportPVEAPI, TransportSSH:
		return true
	}
	return false
}

type NodeKind string

const (
	NodeKindHost     NodeKind = "host"
	NodeKindGuest    NodeKind = "guest"
	NodeKindExternal NodeKind = "external"
)

func (k NodeKind) Valid() bool {
	switch k {
	case NodeKindHost, NodeKindGuest, NodeKindExternal:
		return true
	}
	return false
}

type Observed[T any] struct {
	Value      T     `json:"value"`
	ObservedAt int64 `json:"observed_at"`
}

func Observe[T any](v T, unixSeconds int64) Observed[T] {
	return Observed[T]{Value: v, ObservedAt: unixSeconds}
}

const NotReported int64 = -1

const (
	CredOK      = "ok"
	CredMissing = "absent"
	CredRevoked = "revoked"
	CredExpired = "expired"
)

type Credential struct {
	TokenID string `json:"token_id"`
	Expire  int64  `json:"expire"`
	State   string `json:"state"`
}

type Node struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Transport Transport `json:"transport"`
	Address   string    `json:"address"`
	Kind      NodeKind  `json:"kind"`
	VMID      int       `json:"vmid"`

	MissingSince int64 `json:"absent_since,omitempty"`

	Status Observed[string] `json:"status"`
	Uptime Observed[int64]  `json:"uptime"`

	CPUFrac  Observed[float64] `json:"cpu_frac"`
	CPUCores Observed[int]     `json:"cpu_cores"`

	MemUsed  Observed[int64] `json:"mem_used"`
	MemTotal Observed[int64] `json:"mem_total"`
	MemHost  Observed[int64] `json:"mem_host"`

	DiskUsed  Observed[int64] `json:"disk_used"`
	DiskTotal Observed[int64] `json:"disk_total"`

	NetIn      Observed[int64] `json:"net_in"`
	NetOut     Observed[int64] `json:"net_out"`
	DiskRead   Observed[int64] `json:"disk_read"`
	DiskWrite  Observed[int64] `json:"disk_write"`
	NetInRate  Observed[int64] `json:"net_in_rate"`
	NetOutRate Observed[int64] `json:"net_out_rate"`

	Template bool `json:"template"`

	Credential Credential `json:"credential"`
}

func (n Node) Validate() error {
	if n.ID == "" {
		return fmt.Errorf("node without ID: without a stable key the inventory merges entries")
	}
	if !n.Transport.Valid() {
		return fmt.Errorf("node %q: transport %s outside the set {agent, pve-api, ssh}",
			n.ID, strconv.Quote(string(n.Transport)))
	}
	if !n.Kind.Valid() {
		return fmt.Errorf("node %q: kind %s outside the set {host, guest, external}",
			n.ID, strconv.Quote(string(n.Kind)))
	}
	return nil
}

type Service struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
	Unit   string `json:"unit"`
}

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Deployment struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	NodeID    string `json:"node_id"`
	Branch    string `json:"branch"`
}

type JobRef struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	NodeID string `json:"node_id"`
	Source string `json:"source"`
}

type Inventory struct {
	SchemaVersion int          `json:"schema_version"`
	Nodes         []Node       `json:"nodes"`
	Hypervisor    Hypervisor   `json:"hypervisor"`
	Services      []Service    `json:"services"`
	Projects      []Project    `json:"projects"`
	Deployments   []Deployment `json:"deployments"`
	Jobs          []JobRef     `json:"jobs"`

	LastPollAt    int64  `json:"last_poll_at"`
	LastPollError string `json:"last_poll_error"`
}

type NodeView struct {
	Node
	AgeSeconds int64 `json:"age_seconds"`
	Stale      bool  `json:"stale"`
}
