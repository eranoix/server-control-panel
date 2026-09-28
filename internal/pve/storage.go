package pve

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type Storage struct {
	Storage      string  `json:"storage"`
	Type         string  `json:"type"`
	Content      string  `json:"content"`
	Total        int64   `json:"total"`
	Used         int64   `json:"used"`
	Avail        int64   `json:"avail"`
	UsedFraction float64 `json:"used_fraction"`
	Active       int     `json:"active"`
	Enabled      int     `json:"enabled"`
	Shared       int     `json:"shared"`
}

func (s Storage) IsActive() bool  { return s.Active == 1 }
func (s Storage) IsEnabled() bool { return s.Enabled == 1 }
func (s Storage) IsShared() bool  { return s.Shared == 1 }

func (s Storage) ContentList() []string {
	out := []string{}
	for _, c := range strings.Split(s.Content, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

func (c *Client) StorageList(ctx context.Context, node string) ([]Storage, error) {
	if node == "" {
		return nil, fmt.Errorf("pve: empty node in StorageList")
	}
	var ss []Storage
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/storage"
	if err := c.do(ctx, http.MethodGet, p, &ss); err != nil {
		return nil, err
	}
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].Storage < ss[j].Storage })
	return ss, nil
}

type ZPool struct {
	Name   string  `json:"name"`
	Health string  `json:"health"`
	Size   int64   `json:"size"`
	Alloc  int64   `json:"alloc"`
	Free   int64   `json:"free"`
	Frag   int     `json:"frag"`
	Dedup  float64 `json:"dedup"`
}

func (p ZPool) Healthy() bool { return p.Health == "ONLINE" }

func (c *Client) ZFSList(ctx context.Context, node string) ([]ZPool, error) {
	if node == "" {
		return nil, fmt.Errorf("pve: empty node in ZFSList")
	}
	var ps []ZPool
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/disks/zfs"
	if err := c.do(ctx, http.MethodGet, p, &ps); err != nil {
		return nil, err
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps, nil
}

var datastorePrivs = []string{
	"Datastore.Audit",
	"Datastore.Allocate",
	"Datastore.AllocateSpace",
	"Datastore.AllocateTemplate",
}

func CanAuditDatastore(perms map[string]map[string]int) bool {
	for path, privs := range perms {
		if !coversStorage(path) {
			continue
		}
		for _, p := range datastorePrivs {
			if privs[p] == 1 {
				return true
			}
		}
	}
	return false
}

func coversStorage(path string) bool {
	return path == "/" || path == "/storage" || strings.HasPrefix(path, "/storage/")
}

type ZDevice struct {
	Path  string `json:"path"`
	State string `json:"state"`
	Read  int64  `json:"read"`
	Write int64  `json:"write"`
	Cksum int64  `json:"cksum"`
}

type ZVdev struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Redundant bool      `json:"redundant"`
	Devices   []ZDevice `json:"devices"`
}

type ZPoolTopology struct {
	Name       string  `json:"name"`
	State      string  `json:"state"`
	Errors     string  `json:"errors"`
	Vdevs      []ZVdev `json:"vdevs"`
	Redundant  bool    `json:"redundant"`
	NDisp      int     `json:"n_devices"`
	ErrorCount int64   `json:"errors_counted"`
}

type zfsNode struct {
	Name     string    `json:"name"`
	State    string    `json:"state"`
	Leaf     int       `json:"leaf"`
	Read     int64     `json:"read"`
	Write    int64     `json:"write"`
	Cksum    int64     `json:"cksum"`
	Msg      string    `json:"msg"`
	Children []zfsNode `json:"children"`
}

type zfsRawDetail struct {
	Name     string    `json:"name"`
	State    string    `json:"state"`
	Errors   string    `json:"errors"`
	Children []zfsNode `json:"children"`
}

func vdevType(name string) (string, bool) {
	switch {
	case strings.HasPrefix(name, "mirror"):
		return "mirror", true
	case strings.HasPrefix(name, "raidz3"):
		return "raidz3", true
	case strings.HasPrefix(name, "raidz2"):
		return "raidz2", true
	case strings.HasPrefix(name, "raidz"):
		return "raidz1", true
	case strings.HasPrefix(name, "log"), strings.HasPrefix(name, "cache"),
		strings.HasPrefix(name, "spare"), strings.HasPrefix(name, "special"):
		return "special", false
	default:
		return "stripe", false
	}
}

func (c *Client) ZFSTopology(ctx context.Context, node, pool string) (ZPoolTopology, error) {
	var out ZPoolTopology
	if node == "" || pool == "" {
		return out, fmt.Errorf("pve: empty node or pool in ZFSTopology")
	}
	var rawValue zfsRawDetail
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/disks/zfs/" + url.PathEscape(pool)
	if err := c.do(ctx, http.MethodGet, p, &rawValue); err != nil {
		return out, err
	}
	out.Name, out.State, out.Errors = rawValue.Name, rawValue.State, rawValue.Errors
	if out.Name == "" {
		out.Name = pool
	}

	root := rawValue.Children
	if len(root) == 1 && root[0].Leaf == 0 && root[0].Name == out.Name {
		root = root[0].Children
	}

	for _, n := range root {
		if n.Leaf == 1 {
			out.Vdevs = append(out.Vdevs, ZVdev{
				Name: n.Name, Type: "stripe", Redundant: false,
				Devices: []ZDevice{{Path: n.Name, State: n.State,
					Read: n.Read, Write: n.Write, Cksum: n.Cksum}},
			})
			continue
		}
		kind, red := vdevType(n.Name)
		v := ZVdev{Name: n.Name, Type: kind, Redundant: red}
		for _, f := range n.Children {
			if f.Leaf != 1 {
				continue
			}
			v.Devices = append(v.Devices, ZDevice{
				Path: f.Name, State: f.State, Read: f.Read, Write: f.Write, Cksum: f.Cksum,
			})
		}
		out.Vdevs = append(out.Vdevs, v)
	}

	for _, v := range out.Vdevs {
		if v.Type == "special" {
			continue
		}
		if v.Redundant {
			out.Redundant = true
		}
		out.NDisp += len(v.Devices)
		for _, d := range v.Devices {
			out.ErrorCount += d.Read + d.Write + d.Cksum
		}
	}
	return out, nil
}

func SerialFromPath(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, "-0:0")
	for _, suf := range []string{"-part1", "-part2", "-part3", "-part4", "-part5", "-part6", "-part7", "-part8", "-part9"} {
		base = strings.TrimSuffix(base, suf)
	}
	base = strings.TrimSuffix(base, "-0:0")
	if strings.HasPrefix(base, "nvme-eui.") {
		return ""
	}
	if i := strings.LastIndex(base, "_"); i >= 0 {
		return base[i+1:]
	}
	return ""
}

type BackupItem struct {
	VolID  string `json:"volid"`
	VMID   int    `json:"vmid"`
	CTime  int64  `json:"ctime"`
	Size   int64  `json:"size"`
	Format string `json:"format"`
	Notes  string `json:"notes"`
}

type BackupFreshness struct {
	Storage   string `json:"storage"`
	Total     int    `json:"total"`
	LastCTime int64  `json:"last_ctime"`
	Guests    []int  `json:"guests"`
	Error     string `json:"error,omitempty"`

	Scheduler string `json:"schedule_state"`
	Schedule  string `json:"schedule,omitempty"`

	SameDiskAs []string `json:"same_disk_as,omitempty"`
}

func (c *Client) DatastoreBackups(ctx context.Context, node, storage string) (BackupFreshness, error) {
	out := BackupFreshness{Storage: storage}
	if node == "" || storage == "" {
		return out, fmt.Errorf("pve: empty node or storage in DatastoreBackups")
	}
	var items []BackupItem
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) +
		"/content?content=backup"
	if err := c.do(ctx, http.MethodGet, p, &items); err != nil {
		return out, err
	}
	out.Total = len(items)
	seen := map[int]bool{}
	for _, it := range items {
		if it.CTime > out.LastCTime {
			out.LastCTime = it.CTime
		}
		if it.VMID > 0 && !seen[it.VMID] {
			seen[it.VMID] = true
			out.Guests = append(out.Guests, it.VMID)
		}
	}
	sort.Ints(out.Guests)
	return out, nil
}

type BackupJob struct {
	ID       string `json:"id"`
	Storage  string `json:"storage"`
	Enabled  *int   `json:"enabled"`
	Schedule string `json:"schedule"`
	Comment  string `json:"comment"`
	All      int    `json:"all"`
}

func (j BackupJob) IsScheduled() bool {
	if j.Schedule == "" {
		return false
	}
	return j.Enabled == nil || *j.Enabled != 0
}

func (c *Client) BackupJobs(ctx context.Context) ([]BackupJob, error) {
	var js []BackupJob
	if err := c.do(ctx, http.MethodGet, "/api2/json/cluster/backup", &js); err != nil {
		return nil, err
	}
	return js, nil
}
