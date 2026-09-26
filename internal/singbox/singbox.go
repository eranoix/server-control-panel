// Package singbox manages the tunnel's devices by editing the sing-box
// config.json on the host and reloading the container.
//
// The tunnel (sing-box, Docker) authenticates each device by a VLESS user
// (uuid + name). This package is the single writer of that config: it adds,
// renames, removes device users and moves a device between exits (VPS vs home)
// by editing the `auth_user` route rule — then asks the caller to restart the
// container. Every write validates, keeps a .bak, and is atomic (temp in the
// same dir → fsync → rename → dir fsync), mirroring internal/gameservers.
//
// Live state (who is connected, session traffic) is NOT here — it comes from
// the Clash API (see clash.go), merged by the handler.
package singbox

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Public endpoint of this deployment's tunnel — the client-side bits of a
// device link (not present in the sing-box config, which is server-side). The
// path is read from the config's ws-in inbound; these stay constant.
const (
	tunnelIP   = "203.0.113.10"
	tunnelHost = "tunnel.northwind.example"
	tunnelPort = "443"
)

// Reality endpoint (probe-resistant VLESS, residential/privacy use). The
// inbound listens on 8443 inside the container, published on the host at realityPort. The
// public key (pbk) does not live in the sing-box config (only the private one), so it is
// a constant here; sni/short_id are read from the inbound.
const (
	realityPort   = "2053"
	realityPubkey = "znsyDY0DG7jINQhL2zLjvczS9sykeJZngKs19HGBtns"
)

// Tags of the inbounds that carry per-device users (the ones a device link can
// reach). The legacy vless-ws-casa inbound is intentionally excluded.
var deviceInbounds = map[string]bool{"vless-ws-in": true, "vless-reality-in": true}

// Exit values.
const (
	ExitVPS  = "vps"  // default outbound (direct) — leaves through the VPS
	ExitCasa = "casa" // casa outbound — leaves through the house (residential)
)

// Data-saver proxy outbound tags (defined in config.json). A device with
// datasaver on has its web traffic (80/443) routed through the proxy of its
// exit; the proxy (mitmproxy) recompresses and leaves through the right exit.
const (
	outProxyVPS  = "proxy-vps"
	outProxyCasa = "proxy-casa"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)

// Device is one managed tunnel client.
type Device struct {
	Name      string `json:"name"`              // slug, unique — VLESS user name + auth_user key
	UUID      string `json:"uuid"`              // the secret in the link
	Exit      string `json:"exit"`              // ExitVPS | ExitCasa
	Datasaver bool   `json:"datasaver"`         // web (80/443) through the compression proxy
	Created   int64  `json:"created,omitempty"` // unix, from the registry
}

// Manager owns the config file + a small registry of display metadata.
type Manager struct {
	mu           sync.Mutex
	configPath   string
	registryPath string
	restart      func(ctx context.Context) error // injected: restart the container
}

// New builds a Manager. registryPath holds created_at metadata; restart is
// called after every successful config write.
func New(configPath, registryPath string, restart func(ctx context.Context) error) *Manager {
	return &Manager{configPath: configPath, registryPath: registryPath, restart: restart}
}

// ValidName reports whether name is a usable device slug.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// ---------- config load/save ----------

func (m *Manager) load() (map[string]any, error) {
	raw, err := os.ReadFile(m.configPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", m.configPath, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("invalid sing-box config: %w", err)
	}
	return doc, nil
}

// save validates, backs up, and atomically writes doc. Guard: refuse a config
// that would leave a device inbound with zero users (a self-lockout / broken
// tunnel), mirroring the userGroups guard of gameservers SaveRawConfig.
func (m *Manager) save(doc map[string]any) error {
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing config: %w", err)
	}
	// Guard: sing-box must accept it, and no device inbound may be empty.
	var check map[string]any
	if err := json.Unmarshal(out, &check); err != nil {
		return fmt.Errorf("guard: generated config invalid: %w", err)
	}
	for _, ib := range inbounds(doc) {
		tag, _ := ib["tag"].(string)
		if deviceInbounds[tag] {
			if us, _ := ib["users"].([]any); len(us) == 0 {
				return fmt.Errorf("guard: refused — inbound %q would be left with no user (it would break the tunnel)", tag)
			}
		}
	}
	// .bak (best-effort) then atomic write preserving owner+mode.
	if cur, err := os.ReadFile(m.configPath); err == nil {
		_ = writeAtomic(m.configPath+".bak", cur, m.configPath)
	}
	return writeAtomic(m.configPath, out, m.configPath)
}

// ---------- accessors over the generic doc ----------

func inbounds(doc map[string]any) []map[string]any {
	arr, _ := doc["inbounds"].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func outbounds(doc map[string]any) []map[string]any {
	arr, _ := doc["outbounds"].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// ProxyEndpoint returns the "host:port" of the data-saver proxy outbound for an
// exit (proxy-vps for ExitVPS, proxy-casa for ExitCasa). The handler probes this
// before turning data-saver on, so enabling never routes a device through a
// proxy that is down/unreachable (that was the NXDOMAIN outage — VPSM-ds-safe).
func (m *Manager) ProxyEndpoint(exit string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load()
	if err != nil {
		return "", err
	}
	tag := outProxyVPS
	if exit == ExitCasa {
		tag = outProxyCasa
	}
	for _, ob := range outbounds(doc) {
		if t, _ := ob["tag"].(string); t == tag {
			server, _ := ob["server"].(string)
			if server == "" {
				return "", fmt.Errorf("outbound %q without a server", tag)
			}
			port := 8080
			switch p := ob["server_port"].(type) {
			case float64:
				port = int(p)
			}
			return fmt.Sprintf("%s:%d", server, port), nil
		}
	}
	return "", fmt.Errorf("outbound %q does not exist in the config", tag)
}

// wsInPath returns the transport.path of the ws-in inbound (the device link path).
func wsInPath(doc map[string]any) string {
	for _, ib := range inbounds(doc) {
		if tag, _ := ib["tag"].(string); tag == "vless-ws-in" {
			if tr, ok := ib["transport"].(map[string]any); ok {
				if p, ok := tr["path"].(string); ok {
					return p
				}
			}
		}
	}
	return "/"
}

// routeRules returns the route.rules slice (read-only view), tolerating a
// missing route/rules.
func routeRules(doc map[string]any) []any {
	route, _ := doc["route"].(map[string]any)
	if route == nil {
		return nil
	}
	rules, _ := route["rules"].([]any)
	return rules
}

// authUsersFor scans, read-only, the auth_user names of every rule matching a
// predicate. Used to reconstruct the casa/datasaver membership sets from the
// config (config is the single source of truth, surviving registry loss).
func authUsersFor(doc map[string]any, match func(rm map[string]any) bool) map[string]bool {
	out := map[string]bool{}
	for _, r := range routeRules(doc) {
		rm, ok := r.(map[string]any)
		if !ok || !match(rm) {
			continue
		}
		list, _ := rm["auth_user"].([]any)
		for _, v := range list {
			if s, ok := v.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

// casaMembers: names whose exit is casa (auth_user in ANY rule bound to the
// casa outbound — the plain casa rule or the proxy-casa rule).
func casaMembers(doc map[string]any) map[string]bool {
	return authUsersFor(doc, func(rm map[string]any) bool {
		out, _ := rm["outbound"].(string)
		return out == ExitCasa || out == outProxyCasa
	})
}

// dsMembers: names with data-saver on (auth_user in a proxy rule, either exit).
func dsMembers(doc map[string]any) map[string]bool {
	return authUsersFor(doc, func(rm map[string]any) bool {
		out, _ := rm["outbound"].(string)
		return out == outProxyVPS || out == outProxyCasa
	})
}

// setManagedRules rewrites the auth_user-keyed route rules deterministically
// from the desired casa/datasaver sets, preserving every other rule (the
// leading resolve rule, the http-casa-in bridge — neither carries auth_user).
//
// Order (first match wins), appended after the preserved rules:
//  1. QUIC reject for datasaver devices — forces the browser to fall back to TCP,
//     otherwise HTTP/3 escapes the transformation.
//  2. proxy-casa: web (80/443) of datasaver ∩ casa.
//  3. proxy-vps:  web (80/443) of datasaver ∩ vps.
//  4. casa: all the remaining traffic of the casa devices (other ports).
//
// Non-web from datasaver∩casa falls to rule 4 (casa); non-web from datasaver∩vps
// falls through to the end (direct). Banks/pinning go out without MITM via the proxy's own
// ignore_hosts list — they need no rule here.
func setManagedRules(doc map[string]any, casaSet, dsSet map[string]bool) {
	route, _ := doc["route"].(map[string]any)
	if route == nil {
		route = map[string]any{}
		doc["route"] = route
	}
	var preserved []any
	for _, r := range routeRules(doc) {
		rm, ok := r.(map[string]any)
		if !ok {
			preserved = append(preserved, r)
			continue
		}
		if _, hasUser := rm["auth_user"]; hasUser {
			continue // managed (casa/proxy/quic) — rebuilt below
		}
		preserved = append(preserved, r)
	}
	dsCasa := map[string]bool{}
	dsVps := map[string]bool{}
	for n := range dsSet {
		if casaSet[n] {
			dsCasa[n] = true
		} else {
			dsVps[n] = true
		}
	}
	var managed []any
	if len(dsSet) > 0 {
		managed = append(managed, map[string]any{
			"auth_user": toList(dsSet), "network": "udp", "port": float64(443), "action": "reject",
		})
	}
	if len(dsCasa) > 0 {
		managed = append(managed, map[string]any{
			"auth_user": toList(dsCasa), "port": []any{float64(80), float64(443)}, "outbound": outProxyCasa,
		})
	}
	if len(dsVps) > 0 {
		managed = append(managed, map[string]any{
			"auth_user": toList(dsVps), "port": []any{float64(80), float64(443)}, "outbound": outProxyVPS,
		})
	}
	if len(casaSet) > 0 {
		managed = append(managed, map[string]any{
			"auth_user": toList(casaSet), "outbound": ExitCasa,
		})
	}
	route["rules"] = append(preserved, managed...)
}

// toList turns a name set into a deterministic (sorted) []any for JSON.
func toList(set map[string]bool) []any {
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return out
}

// ---------- device operations ----------

// List returns the devices declared in the config (union across device
// inbounds), with exit resolved from the casa rule and created_at from the
// registry. Deterministic order by name.
func (m *Manager) List() ([]Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load()
	if err != nil {
		return nil, err
	}
	reg := m.loadRegistry()
	casa := casaMembers(doc)
	ds := dsMembers(doc)
	seen := map[string]Device{}
	for _, ib := range inbounds(doc) {
		if tag, _ := ib["tag"].(string); !deviceInbounds[tag] {
			continue
		}
		us, _ := ib["users"].([]any)
		for _, u := range us {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			name, _ := um["name"].(string)
			uuid, _ := um["uuid"].(string)
			if name == "" || uuid == "" {
				continue // unnamed/legacy shared user is not a managed device
			}
			d := seen[name]
			d.Name = name
			d.UUID = uuid
			d.Exit = ExitVPS
			if casa[name] {
				d.Exit = ExitCasa
			}
			d.Datasaver = ds[name]
			if r, ok := reg[name]; ok {
				d.Created = r.Created
			}
			seen[name] = d
		}
	}
	out := make([]Device, 0, len(seen))
	for _, d := range seen {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Add creates a device with a fresh uuid on every device inbound and default
// VPS exit. Returns the created device (with link buildable via Link).
func (m *Manager) Add(ctx context.Context, name string) (Device, error) {
	if !ValidName(name) {
		return Device{}, fmt.Errorf("invalid name: use lowercase letters, digits and hyphen (e.g.: pc-sam)")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load()
	if err != nil {
		return Device{}, err
	}
	for _, ib := range inbounds(doc) {
		if tag, _ := ib["tag"].(string); deviceInbounds[tag] {
			us, _ := ib["users"].([]any)
			for _, u := range us {
				if um, ok := u.(map[string]any); ok {
					if n, _ := um["name"].(string); n == name {
						return Device{}, fmt.Errorf("a device named %q already exists", name)
					}
				}
			}
		}
	}
	uuid, err := newUUID()
	if err != nil {
		return Device{}, err
	}
	for _, ib := range inbounds(doc) {
		if tag, _ := ib["tag"].(string); deviceInbounds[tag] {
			us, _ := ib["users"].([]any)
			user := map[string]any{"uuid": uuid, "name": name}
			if tag == "vless-reality-in" {
				user["flow"] = "xtls-rprx-vision"
			}
			ib["users"] = append(us, user)
		}
	}
	if err := m.save(doc); err != nil {
		return Device{}, err
	}
	m.registrySet(name, nowUnix())
	if err := m.restart(ctx); err != nil {
		return Device{}, fmt.Errorf("config saved, but the tunnel restart failed: %w", err)
	}
	return Device{Name: name, UUID: uuid, Exit: ExitVPS, Created: m.loadRegistry()[name].Created}, nil
}

// Remove revokes a device (drops its user from every inbound + the casa rule).
func (m *Manager) Remove(ctx context.Context, uuid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load()
	if err != nil {
		return err
	}
	removedName := ""
	for _, ib := range inbounds(doc) {
		if tag, _ := ib["tag"].(string); !deviceInbounds[tag] {
			continue
		}
		us, _ := ib["users"].([]any)
		kept := make([]any, 0, len(us))
		for _, u := range us {
			if um, ok := u.(map[string]any); ok {
				if id, _ := um["uuid"].(string); id == uuid {
					if n, _ := um["name"].(string); n != "" {
						removedName = n
					}
					continue
				}
			}
			kept = append(kept, u)
		}
		ib["users"] = kept
	}
	if removedName == "" {
		return fmt.Errorf("device not found")
	}
	casaSet, dsSet := casaMembers(doc), dsMembers(doc)
	delete(casaSet, removedName)
	delete(dsSet, removedName)
	setManagedRules(doc, casaSet, dsSet)
	if err := m.save(doc); err != nil {
		return err
	}
	m.registryDel(removedName)
	return m.restart(ctx)
}

// SetExit moves a device between VPS and casa by editing the casa auth_user list.
func (m *Manager) SetExit(ctx context.Context, uuid, exit string) error {
	if exit != ExitVPS && exit != ExitCasa {
		return fmt.Errorf("invalid exit: use %q or %q", ExitVPS, ExitCasa)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load()
	if err != nil {
		return err
	}
	name := m.nameForUUID(doc, uuid)
	if name == "" {
		return fmt.Errorf("device not found")
	}
	casaSet, dsSet := casaMembers(doc), dsMembers(doc)
	if exit == ExitCasa {
		casaSet[name] = true
	} else {
		delete(casaSet, name)
	}
	setManagedRules(doc, casaSet, dsSet)
	if err := m.save(doc); err != nil {
		return err
	}
	return m.restart(ctx)
}

// SetDatasaver turns a device's compression (data-saver) on/off: on →
// web traffic (80/443) goes through the proxy of its exit; off → it leaves directly through the
// exit (VPS/casa) with no transformation.
func (m *Manager) SetDatasaver(ctx context.Context, uuid string, on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load()
	if err != nil {
		return err
	}
	name := m.nameForUUID(doc, uuid)
	if name == "" {
		return fmt.Errorf("device not found")
	}
	casaSet, dsSet := casaMembers(doc), dsMembers(doc)
	if on {
		dsSet[name] = true
	} else {
		delete(dsSet, name)
	}
	setManagedRules(doc, casaSet, dsSet)
	if err := m.save(doc); err != nil {
		return err
	}
	return m.restart(ctx)
}

// Rename changes a device's slug (VLESS name + auth_user membership).
func (m *Manager) Rename(ctx context.Context, uuid, newName string) error {
	if !ValidName(newName) {
		return fmt.Errorf("invalid name")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load()
	if err != nil {
		return err
	}
	old := m.nameForUUID(doc, uuid)
	if old == "" {
		return fmt.Errorf("device not found")
	}
	if old == newName {
		return nil
	}
	for _, ib := range inbounds(doc) {
		if tag, _ := ib["tag"].(string); !deviceInbounds[tag] {
			continue
		}
		us, _ := ib["users"].([]any)
		for _, u := range us {
			if um, ok := u.(map[string]any); ok {
				if n, _ := um["name"].(string); n == newName {
					return fmt.Errorf("a device named %q already exists", newName)
				}
			}
		}
	}
	for _, ib := range inbounds(doc) {
		if tag, _ := ib["tag"].(string); !deviceInbounds[tag] {
			continue
		}
		us, _ := ib["users"].([]any)
		for _, u := range us {
			if um, ok := u.(map[string]any); ok {
				if id, _ := um["uuid"].(string); id == uuid {
					um["name"] = newName
				}
			}
		}
	}
	casaSet, dsSet := casaMembers(doc), dsMembers(doc)
	if casaSet[old] {
		delete(casaSet, old)
		casaSet[newName] = true
	}
	if dsSet[old] {
		delete(dsSet, old)
		dsSet[newName] = true
	}
	setManagedRules(doc, casaSet, dsSet)
	if err := m.save(doc); err != nil {
		return err
	}
	if c := m.loadRegistry()[old].Created; true {
		m.registryDel(old)
		m.registrySet(newName, c)
	}
	return m.restart(ctx)
}

// Link builds the vless:// URI for a device (ws-in path, VPS entry, TLS).
func (m *Manager) Link(d Device) (string, error) {
	m.mu.Lock()
	doc, err := m.load()
	m.mu.Unlock()
	if err != nil {
		return "", err
	}
	path := wsInPath(doc)
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("security", "tls")
	q.Set("sni", tunnelHost)
	q.Set("host", tunnelHost)
	q.Set("alpn", "http/1.1")
	q.Set("fp", "chrome")
	q.Set("type", "ws")
	q.Set("path", path)
	return fmt.Sprintf("vless://%s@%s:%s?%s#%s",
		d.UUID, tunnelIP, tunnelPort, q.Encode(), url.PathEscape(d.Name)), nil
}

// LinkReality builds the vless:// URI for a device's Reality profile (TCP,
// security=reality, fp=chrome, xtls-rprx-vision). sni/short_id come from the
// reality inbound; pbk/port are deployment constants. It is the residential profile:
// the ISP cannot even confirm that it is a VPN.
func (m *Manager) LinkReality(d Device, port int) (string, error) {
	m.mu.Lock()
	doc, err := m.load()
	m.mu.Unlock()
	if err != nil {
		return "", err
	}
	rport := realityPort
	if port > 0 {
		rport = fmt.Sprintf("%d", port)
	}
	sni, sid := "www.icloud.com", ""
	for _, ib := range inbounds(doc) {
		if tag, _ := ib["tag"].(string); tag != "vless-reality-in" {
			continue
		}
		tls, _ := ib["tls"].(map[string]any)
		if s, ok := tls["server_name"].(string); ok {
			sni = s
		}
		if r, ok := tls["reality"].(map[string]any); ok {
			if arr, ok := r["short_id"].([]any); ok && len(arr) > 0 {
				if s, ok := arr[0].(string); ok {
					sid = s
				}
			}
		}
	}
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("security", "reality")
	q.Set("sni", sni)
	q.Set("pbk", realityPubkey)
	q.Set("sid", sid)
	q.Set("fp", "chrome")
	q.Set("flow", "xtls-rprx-vision")
	q.Set("type", "tcp")
	return fmt.Sprintf("vless://%s@%s:%s?%s#%s",
		d.UUID, tunnelIP, rport, q.Encode(), url.PathEscape(d.Name+"-reality")), nil
}

// ---------- helpers ----------

func (m *Manager) nameForUUID(doc map[string]any, uuid string) string {
	for _, ib := range inbounds(doc) {
		if tag, _ := ib["tag"].(string); !deviceInbounds[tag] {
			continue
		}
		us, _ := ib["users"].([]any)
		for _, u := range us {
			if um, ok := u.(map[string]any); ok {
				if id, _ := um["uuid"].(string); id == uuid {
					if n, _ := um["name"].(string); n != "" {
						return n
					}
				}
			}
		}
	}
	return ""
}

func newUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// ---------- registry (display metadata) ----------

type regEntry struct {
	Created int64 `json:"created"`
}

func (m *Manager) loadRegistry() map[string]regEntry {
	out := map[string]regEntry{}
	raw, err := os.ReadFile(m.registryPath)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out) // tolerate corruption
	return out
}

func (m *Manager) saveRegistry(r map[string]regEntry) {
	if err := os.MkdirAll(filepath.Dir(m.registryPath), 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return
	}
	tmp := m.registryPath + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, m.registryPath)
	}
}

func (m *Manager) registrySet(name string, created int64) {
	r := m.loadRegistry()
	if _, ok := r[name]; !ok {
		r[name] = regEntry{Created: created}
		m.saveRegistry(r)
	}
}

func (m *Manager) registryDel(name string) {
	r := m.loadRegistry()
	if _, ok := r[name]; ok {
		delete(r, name)
		m.saveRegistry(r)
	}
}

// NormalizeName lowercases and slugs a free-text name into a valid device slug.
func NormalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}
