// Package gameservers manages game servers running in Docker on the VPS.
//
// The package is game-agnostic: the common part (status, start/stop/restart,
// logs, resource usage) lives here, and whatever is specific to each game (where
// the worlds are, what the settings file looks like, how to list backups) stays
// behind the Adapter interface. Adding a new game = implement Adapter and
// register it in adapters — without touching the handlers or the UI.
//
// The server inventory is declarative: <DataDir>/gameservers.json.
package gameservers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"server-control-panel/internal/docker"
)

// Server describes a game server registered in the inventory.
type Server struct {
	ID        string `json:"id"`        // stable slug, used in the routes
	Name      string `json:"name"`      // displayed label
	Game      string `json:"game"`      // adapter key ("enshrouded")
	Container string `json:"container"` // name of the Docker container
	Root      string `json:"root"`      // root on the host (compose + data + worlds)
	Address   string `json:"address"`   // host:port to connect to the game
	Notes     string `json:"notes,omitempty"`

	// No is the ID of the Node (from the inventory) where this server LIVES.
	//
	// WITHOUT `omitempty`, on purpose: an empty value has to reach the payload
	// saying "I do not know which node this lives on", because that is exactly the
	// case that makes the panel REFUSE the operation. A field that vanishes from
	// the JSON when empty is a field the inventory screen does not show, and the
	// operator does not fix what they cannot see.
	No string `json:"node"`
}

// Caps tells the UI which tabs make sense for this game. A game with no notion
// of a "switchable world" simply reports Worlds=false and the tab disappears.
type Caps struct {
	Worlds   bool `json:"worlds"`
	Settings bool `json:"settings"`
	Backups  bool `json:"backups"`
	Players  bool `json:"players"`
}

// World is a world/save managed by the server.
type World struct {
	Name     string    `json:"name"`
	SaveID   string    `json:"saveId"`
	Active   bool      `json:"active"`
	SizeMB   float64   `json:"sizeMb"`
	Modified time.Time `json:"modified"`
}

// Backup is a restorable snapshot.
type Backup struct {
	File     string    `json:"file"`
	SizeMB   float64   `json:"sizeMb"`
	Modified time.Time `json:"modified"`
}

// Status is the server's current state (the generic part, through Docker).
type Status struct {
	Server
	Caps       Caps    `json:"caps"`
	State      string  `json:"state"`  // running | exited | missing | ...
	Health     string  `json:"health"` // healthy | unhealthy | "" (no healthcheck)
	Uptime     string  `json:"uptime"` // readable: "2d 4h"
	CPUPerc    float64 `json:"cpuPerc"`
	MemMB      float64 `json:"memMb"`
	MemLimit   float64 `json:"memLimitMb"`
	Players    int     `json:"players"`
	MaxPlayers int     `json:"maxPlayers"`
	Version    string  `json:"version"`
	HasPlayer  bool    `json:"hasPlayers"` // false = the game does not report a count
	World      string  `json:"world"`      // active world, where applicable
	Err        string  `json:"err,omitempty"`
}

// Adapter isolates what is specific to each game.
type Adapter interface {
	Caps() Caps
	Worlds(s Server) ([]World, error)
	SwitchWorld(s Server, world string) error
	Settings(s Server) (map[string]interface{}, error)
	SaveSettings(s Server, patch map[string]interface{}) error
	Backups(s Server) ([]Backup, error)
	ActiveWorld(s Server) string

	// Management operations (v2)
	DuplicateWorld(s Server, src, dst string) error
	DeleteWorld(s Server, name string) error
	CreateBackup(s Server, stamp string) (string, error)
	RestoreBackup(s Server, file, stamp string) error
	BackupPath(s Server, file string) (string, error)
	ConnectionInfo(s Server) map[string]interface{}

	// Server options (outside gameSettings) + world rename
	ServerSettings(s Server) (map[string]interface{}, error)
	SaveServerSettings(s Server, srv, grp map[string]interface{}) error
	RenameWorld(s Server, old, novo string) error

	// Privilege groups and bans
	Groups(s Server) ([]Group, error)
	SaveGroups(s Server, gs []Group) error
	Bans(s Server) ([]string, error)
	SaveBans(s Server, list []string) error

	// Path of the game's config file ("" = there is none)
	ConfigPath(s Server) string
}

var adapters = map[string]Adapter{
	"enshrouded": enshrouded{},
}

// Manager loads the inventory and runs the operations.
type Manager struct {
	path string
	dc   *docker.Client

	mu      sync.RWMutex
	servers []Server

	hist *history
}

// New opens the inventory at <dataDir>/gameservers.json. A missing file is not
// an error: the manager comes up empty and the page shows a "no servers" state.
func New(dataDir string, dc *docker.Client) *Manager {
	m := &Manager{path: filepath.Join(dataDir, "gameservers.json"), dc: dc}
	_ = m.Reload()
	m.hist = newHistory(dataDir)
	// The usage/player sampler runs for the life of the process: without it the
	// chart would only exist while somebody had the page open.
	m.StartSampler(context.Background())
	return m
}

// Reload re-reads the inventory from disk.
func (m *Manager) Reload() error {
	b, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			m.mu.Lock()
			m.servers = nil
			m.mu.Unlock()
			return nil
		}
		return err
	}
	var list []Server
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("invalid gameservers.json: %w", err)
	}
	m.mu.Lock()
	m.servers = list
	m.mu.Unlock()
	return nil
}

// List returns the raw inventory.
func (m *Manager) List() []Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Server, len(m.servers))
	copy(out, m.servers)
	return out
}

// Get resolves a server by ID.
func (m *Manager) Get(id string) (Server, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.servers {
		if s.ID == id {
			return s, true
		}
	}
	return Server{}, false
}

func (m *Manager) adapter(s Server) Adapter {
	if a, ok := adapters[s.Game]; ok {
		return a
	}
	return noopAdapter{}
}

// Statuses assembles the state of every server. An error on one server does not
// bring down the others: it becomes that item's Err field.
func (m *Manager) Statuses(ctx context.Context) []Status {
	servers := m.List()
	out := make([]Status, 0, len(servers))
	for _, s := range servers {
		out = append(out, m.Status(ctx, s))
	}
	return out
}

// Status assembles one server's state.
func (m *Manager) Status(ctx context.Context, s Server) Status {
	st := Status{Server: s, Caps: m.adapter(s).Caps(), State: "missing"}

	if m.dc == nil {
		st.Err = "docker unavailable"
		return st
	}
	containers, err := m.dc.ListContainers(ctx)
	if err != nil {
		st.Err = err.Error()
		return st
	}
	var id string
	for _, c := range containers {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == s.Container {
				id = c.ID
				st.State = c.State
				if c.Created > 0 {
					st.Uptime = humanSince(time.Unix(c.Created, 0))
				}
			}
		}
	}
	if id == "" {
		st.Err = "container '" + s.Container + "' not found"
	} else if stats, err := m.dc.Stats(ctx, id); err == nil {
		st.CPUPerc, st.MemMB, st.MemLimit = parseStats(stats)
	}

	if st.Caps.Worlds {
		st.World = m.adapter(s).ActiveWorld(s)
	}
	// Player count through A2S — only makes sense with the server up.
	if st.State == "running" {
		if info := m.Players(s); info.OK {
			st.Players, st.MaxPlayers, st.HasPlayer = info.Players, info.MaxPlayers, true
			st.Version = info.Version
		}
	}
	return st
}

// Action runs start/stop/restart on the server's container.
func (m *Manager) Action(ctx context.Context, s Server, action string) error {
	if m.dc == nil {
		return fmt.Errorf("docker unavailable")
	}
	containers, err := m.dc.ListContainers(ctx)
	if err != nil {
		return err
	}
	var id string
	for _, c := range containers {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == s.Container {
				id = c.ID
			}
		}
	}
	if id == "" {
		return fmt.Errorf("container '%s' not found", s.Container)
	}
	switch action {
	case "start":
		return m.dc.Start(ctx, id)
	case "stop":
		return m.dc.Stop(ctx, id)
	case "restart":
		return m.dc.Restart(ctx, id)
	}
	return fmt.Errorf("unknown action: %s", action)
}

// Logs returns the container's last lines.
func (m *Manager) Logs(ctx context.Context, s Server, tail string) (string, error) {
	if m.dc == nil {
		return "", fmt.Errorf("docker unavailable")
	}
	containers, err := m.dc.ListContainers(ctx)
	if err != nil {
		return "", err
	}
	for _, c := range containers {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == s.Container {
				return m.dc.Logs(ctx, c.ID, tail)
			}
		}
	}
	return "", fmt.Errorf("container '%s' not found", s.Container)
}

// Delegations to the game's adapter.
func (m *Manager) Worlds(s Server) ([]World, error) { return m.adapter(s).Worlds(s) }
func (m *Manager) SwitchWorld(s Server, w string) error {
	return m.adapter(s).SwitchWorld(s, w)
}
func (m *Manager) Settings(s Server) (map[string]interface{}, error) {
	return m.adapter(s).Settings(s)
}
func (m *Manager) SaveSettings(s Server, patch map[string]interface{}) error {
	return m.adapter(s).SaveSettings(s, patch)
}
func (m *Manager) Backups(s Server) ([]Backup, error) { return m.adapter(s).Backups(s) }

func (m *Manager) DuplicateWorld(s Server, src, dst string) error {
	return m.adapter(s).DuplicateWorld(s, src, dst)
}
func (m *Manager) DeleteWorld(s Server, name string) error {
	return m.adapter(s).DeleteWorld(s, name)
}
func (m *Manager) CreateBackup(s Server, stamp string) (string, error) {
	return m.adapter(s).CreateBackup(s, stamp)
}
func (m *Manager) RestoreBackup(s Server, file, stamp string) error {
	return m.adapter(s).RestoreBackup(s, file, stamp)
}
func (m *Manager) BackupPath(s Server, file string) (string, error) {
	return m.adapter(s).BackupPath(s, file)
}
func (m *Manager) ConnectionInfo(s Server) map[string]interface{} {
	return m.adapter(s).ConnectionInfo(s)
}

func (m *Manager) ServerSettings(s Server) (map[string]interface{}, error) {
	return m.adapter(s).ServerSettings(s)
}
func (m *Manager) SaveServerSettings(s Server, srv, grp map[string]interface{}) error {
	return m.adapter(s).SaveServerSettings(s, srv, grp)
}
func (m *Manager) RenameWorld(s Server, old, novo string) error {
	return m.adapter(s).RenameWorld(s, old, novo)
}

func (m *Manager) Groups(s Server) ([]Group, error) { return m.adapter(s).Groups(s) }
func (m *Manager) SaveGroups(s Server, gs []Group) error {
	return m.adapter(s).SaveGroups(s, gs)
}
func (m *Manager) Bans(s Server) ([]string, error) { return m.adapter(s).Bans(s) }
func (m *Manager) SaveBans(s Server, list []string) error {
	return m.adapter(s).SaveBans(s, list)
}

// ---------- helpers ----------

func humanSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// parseStats extracts CPU% and memory from Docker stats' raw payload. The CPU
// calculation is the same one `docker stats` does: process delta over system
// delta, times the number of CPUs.
func parseStats(raw map[string]interface{}) (cpu, mem, limit float64) {
	num := func(m map[string]interface{}, k string) float64 {
		if v, ok := m[k].(float64); ok {
			return v
		}
		return 0
	}
	sub := func(m map[string]interface{}, k string) map[string]interface{} {
		if v, ok := m[k].(map[string]interface{}); ok {
			return v
		}
		return map[string]interface{}{}
	}

	ms := sub(raw, "memory_stats")
	mem = num(ms, "usage") / 1024 / 1024
	limit = num(ms, "limit") / 1024 / 1024

	cs, ps := sub(raw, "cpu_stats"), sub(raw, "precpu_stats")
	cpuDelta := num(sub(cs, "cpu_usage"), "total_usage") - num(sub(ps, "cpu_usage"), "total_usage")
	sysDelta := num(cs, "system_cpu_usage") - num(ps, "system_cpu_usage")
	if cpuDelta > 0 && sysDelta > 0 {
		n := num(cs, "online_cpus")
		if n == 0 {
			n = 1
		}
		cpu = (cpuDelta / sysDelta) * n * 100
	}
	return
}

func dirSizeMB(dir string) float64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi != nil && !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return float64(total) / 1024 / 1024
}

func sortWorlds(w []World) {
	sort.Slice(w, func(i, j int) bool {
		if w[i].Active != w[j].Active {
			return w[i].Active
		}
		return w[i].Name < w[j].Name
	})
}

// noopAdapter serves games with no dedicated adapter: only the basics (status/logs).
type noopAdapter struct{}

func (noopAdapter) Caps() Caps                       { return Caps{} }
func (noopAdapter) Worlds(Server) ([]World, error)   { return nil, nil }
func (noopAdapter) SwitchWorld(Server, string) error { return fmt.Errorf("not supported") }
func (noopAdapter) Settings(Server) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not supported")
}
func (noopAdapter) SaveSettings(Server, map[string]interface{}) error {
	return fmt.Errorf("not supported")
}
func (noopAdapter) Backups(Server) ([]Backup, error) { return nil, nil }
func (noopAdapter) ActiveWorld(Server) string        { return "" }

func (noopAdapter) DuplicateWorld(Server, string, string) error { return errUnsupported }
func (noopAdapter) DeleteWorld(Server, string) error            { return errUnsupported }
func (noopAdapter) CreateBackup(Server, string) (string, error) { return "", errUnsupported }
func (noopAdapter) RestoreBackup(Server, string, string) error  { return errUnsupported }
func (noopAdapter) BackupPath(Server, string) (string, error)   { return "", errUnsupported }
func (noopAdapter) ConnectionInfo(Server) map[string]interface{} {
	return map[string]interface{}{}
}

func (noopAdapter) ServerSettings(Server) (map[string]interface{}, error) {
	return nil, errUnsupported
}
func (noopAdapter) SaveServerSettings(Server, map[string]interface{}, map[string]interface{}) error {
	return errUnsupported
}
func (noopAdapter) RenameWorld(Server, string, string) error { return errUnsupported }

func (noopAdapter) Groups(Server) ([]Group, error)   { return nil, errUnsupported }
func (noopAdapter) SaveGroups(Server, []Group) error { return errUnsupported }
func (noopAdapter) Bans(Server) ([]string, error)    { return nil, errUnsupported }
func (noopAdapter) SaveBans(Server, []string) error  { return errUnsupported }
func (noopAdapter) ConfigPath(Server) string         { return "" }

var errUnsupported = fmt.Errorf("not supported by this game")

// readJSONFile reads a JSON object from disk.
func readJSONFile(path string) (map[string]interface{}, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// BACK-END FACTORY — the choice by the node's transport
//
// No new concept: `Transport` and `TransportAgente` are the selector already
// delivered in internal/inventory. All that happens here is honoring what the
// model already declared — the comment on `TransportAgente` itself says the
// value "is accepted by the model and refused by whoever dials". This is where
// it stops being refused.

// DestinoNo is the minimum the factory needs to know about a node.
//
// A small struct instead of `inventory.Node`, on purpose: having `gameservers`
// import `inventory` would couple the package the lab-agent LINKS to the package
// that talks to the Proxmox API — the agent would carry the whole hypervisor
// inventory just to open a zip. The panel builds this struct from its own Node;
// the agent never needs it.
type DestinoNo struct {
	Nome      string // readable name of the node ("games", "apps")
	Transport string // value of inventory.Transport
	Base      string // HTTP root of the lab-agent, when the transport is the agent
	Token     string // that node's bearer — injected ON THE SERVER
}

// The three transports, as strings. Duplicated here rather than imported, for
// the DestinoNo reason above; TestSelecaoPorTransport checks that they stay
// equal to inventory's, so the duplication does not turn into silent
// divergence.
const (
	TransporteAgente = "agente"
	TransportePVEAPI = "pve-api"
	TransporteSSH    = "ssh"
)

// NovoBackend chooses the transport.
//
// A transport that is not the agent's falls to LOCAL, and that is deliberate: it
// is what keeps the VPS panel working throughout, which is the central argument
// of the extraction and the safety net until the cutover. An EMPTY transport,
// however, is an error — a node with no declared transport is an inventory
// defect, and guessing "local" there would hide the defect behind a working path.
func NovoBackend(d DestinoNo, m *Manager) (Backend, error) {
	switch d.Transport {
	case "":
		return nil, fmt.Errorf("node %q with no declared transport: incomplete inventory, there is no safe default to assume", d.Nome)

	case TransporteAgente:
		return NovoBackendHTTP(d.Base, d.Token, d.Nome)

	case TransportePVEAPI, TransporteSSH:
		// A node with no agent keeps being served by today's code, in the panel's
		// own process. This is what keeps the VPS panel working throughout the
		// transition.
		if m == nil {
			return nil, fmt.Errorf("local back-end of node %q requires Manager", d.Nome)
		}
		return NovoBackendLocal(m, d.Nome), nil

	default:
		// A value outside the closed set NAMES the value. Falling back to local here
		// would turn a typo in the inventory into "it worked, only on the wrong
		// node" — the worst possible outcome.
		return nil, fmt.Errorf("invalid transport on node %q: %q is not one of the three supported", d.Nome, d.Transport)
	}
}
