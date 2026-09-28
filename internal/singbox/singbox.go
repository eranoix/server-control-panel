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

const (
	tunnelIP   = "203.0.113.10"
	tunnelHost = "tunnel.northwind.example"
	tunnelPort = "443"
)

const (
	realityPort   = "2053"
	realityPubkey = "znsyDY0DG7jINQhL2zLjvczS9sykeJZngKs19HGBtns"
)

var deviceInbounds = map[string]bool{"vless-ws-in": true, "vless-reality-in": true}

const (
	ExitVPS  = "vps"
	ExitHome = "home"
)

const (
	outProxyVPS  = "proxy-vps"
	outProxyHome = "proxy-home"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)

type Device struct {
	Name      string `json:"name"`
	UUID      string `json:"uuid"`
	Exit      string `json:"exit"`
	Datasaver bool   `json:"datasaver"`
	Created   int64  `json:"created,omitempty"`
}

type Manager struct {
	mu           sync.Mutex
	configPath   string
	registryPath string
	restart      func(ctx context.Context) error
}

func New(configPath, registryPath string, restart func(ctx context.Context) error) *Manager {
	return &Manager{configPath: configPath, registryPath: registryPath, restart: restart}
}

func ValidName(name string) bool { return nameRe.MatchString(name) }

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

func (m *Manager) save(doc map[string]any) error {
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing config: %w", err)
	}
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
	if cur, err := os.ReadFile(m.configPath); err == nil {
		_ = writeAtomic(m.configPath+".bak", cur, m.configPath)
	}
	return writeAtomic(m.configPath, out, m.configPath)
}

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

func (m *Manager) ProxyEndpoint(exit string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load()
	if err != nil {
		return "", err
	}
	tag := outProxyVPS
	if exit == ExitHome {
		tag = outProxyHome
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

func routeRules(doc map[string]any) []any {
	route, _ := doc["route"].(map[string]any)
	if route == nil {
		return nil
	}
	rules, _ := route["rules"].([]any)
	return rules
}

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

func homeMembers(doc map[string]any) map[string]bool {
	return authUsersFor(doc, func(rm map[string]any) bool {
		out, _ := rm["outbound"].(string)
		return out == ExitHome || out == outProxyHome
	})
}

func dsMembers(doc map[string]any) map[string]bool {
	return authUsersFor(doc, func(rm map[string]any) bool {
		out, _ := rm["outbound"].(string)
		return out == outProxyVPS || out == outProxyHome
	})
}

func setManagedRules(doc map[string]any, homeSet, dsSet map[string]bool) {
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
			continue
		}
		preserved = append(preserved, r)
	}
	dsHome := map[string]bool{}
	dsVps := map[string]bool{}
	for n := range dsSet {
		if homeSet[n] {
			dsHome[n] = true
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
	if len(dsHome) > 0 {
		managed = append(managed, map[string]any{
			"auth_user": toList(dsHome), "port": []any{float64(80), float64(443)}, "outbound": outProxyHome,
		})
	}
	if len(dsVps) > 0 {
		managed = append(managed, map[string]any{
			"auth_user": toList(dsVps), "port": []any{float64(80), float64(443)}, "outbound": outProxyVPS,
		})
	}
	if len(homeSet) > 0 {
		managed = append(managed, map[string]any{
			"auth_user": toList(homeSet), "outbound": ExitHome,
		})
	}
	route["rules"] = append(preserved, managed...)
}

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

func (m *Manager) List() ([]Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.load()
	if err != nil {
		return nil, err
	}
	reg := m.loadRegistry()
	home := homeMembers(doc)
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
				continue
			}
			d := seen[name]
			d.Name = name
			d.UUID = uuid
			d.Exit = ExitVPS
			if home[name] {
				d.Exit = ExitHome
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
	homeSet, dsSet := homeMembers(doc), dsMembers(doc)
	delete(homeSet, removedName)
	delete(dsSet, removedName)
	setManagedRules(doc, homeSet, dsSet)
	if err := m.save(doc); err != nil {
		return err
	}
	m.registryDel(removedName)
	return m.restart(ctx)
}

func (m *Manager) SetExit(ctx context.Context, uuid, exit string) error {
	if exit != ExitVPS && exit != ExitHome {
		return fmt.Errorf("invalid exit: use %q or %q", ExitVPS, ExitHome)
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
	homeSet, dsSet := homeMembers(doc), dsMembers(doc)
	if exit == ExitHome {
		homeSet[name] = true
	} else {
		delete(homeSet, name)
	}
	setManagedRules(doc, homeSet, dsSet)
	if err := m.save(doc); err != nil {
		return err
	}
	return m.restart(ctx)
}

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
	homeSet, dsSet := homeMembers(doc), dsMembers(doc)
	if on {
		dsSet[name] = true
	} else {
		delete(dsSet, name)
	}
	setManagedRules(doc, homeSet, dsSet)
	if err := m.save(doc); err != nil {
		return err
	}
	return m.restart(ctx)
}

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
	homeSet, dsSet := homeMembers(doc), dsMembers(doc)
	if homeSet[old] {
		delete(homeSet, old)
		homeSet[newName] = true
	}
	if dsSet[old] {
		delete(dsSet, old)
		dsSet[newName] = true
	}
	setManagedRules(doc, homeSet, dsSet)
	if err := m.save(doc); err != nil {
		return err
	}
	if c := m.loadRegistry()[old].Created; true {
		m.registryDel(old)
		m.registrySet(newName, c)
	}
	return m.restart(ctx)
}

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

type regEntry struct {
	Created int64 `json:"created"`
}

func (m *Manager) loadRegistry() map[string]regEntry {
	out := map[string]regEntry{}
	raw, err := os.ReadFile(m.registryPath)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
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

func NormalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}
