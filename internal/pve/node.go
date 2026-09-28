package pve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultTasks    = 50
	MaxTasks        = 200
	maxTaskLogLines = 200
)

type MemInfo struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

type RootFSInfo struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Avail int64 `json:"avail"`
}

type KSMInfo struct {
	Shared int64 `json:"shared"`
}

type CPUInfo struct {
	Model   string `json:"model"`
	Cpus    int    `json:"cpus"`
	Sockets int    `json:"sockets"`
	MHz     string `json:"mhz"`
}

type NodeStatus struct {
	Uptime     int64      `json:"uptime"`
	PVEVersion string     `json:"pveversion"`
	CPU        float64    `json:"cpu"`
	LoadAvg    []string   `json:"loadavg"`
	Memory     MemInfo    `json:"memory"`
	Swap       MemInfo    `json:"swap"`
	RootFS     RootFSInfo `json:"rootfs"`
	KSM        KSMInfo    `json:"ksm"`

	Wait     float64 `json:"wait"`
	KVersion string  `json:"kversion"`
	CPUInfo  CPUInfo `json:"cpuinfo"`
}

func (c *Client) NodeStatus(ctx context.Context, node string) (NodeStatus, error) {
	var st NodeStatus
	if node == "" {
		return st, fmt.Errorf("pve: empty node in NodeStatus")
	}
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/status"
	if err := c.do(ctx, http.MethodGet, p, &st); err != nil {
		return NodeStatus{}, err
	}
	return st, nil
}

type Task struct {
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	User      string `json:"user"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime"`
}

type TaskListOptions struct {
	Limit      int
	ErrorsOnly bool
	TypeFilter string
	VMID       int
}

func (c *Client) TaskList(ctx context.Context, node string, opt TaskListOptions) ([]Task, error) {
	if node == "" {
		return nil, fmt.Errorf("pve: empty node in TaskList")
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = defaultTasks
	}
	if limit > MaxTasks {
		limit = MaxTasks
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if opt.ErrorsOnly {
		q.Set("errors", "1")
	}
	if s := strings.TrimSpace(opt.TypeFilter); s != "" {
		q.Set("typefilter", s)
	}
	if opt.VMID > 0 {
		q.Set("vmid", strconv.Itoa(opt.VMID))
	}
	var ts []Task
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/tasks?" + q.Encode()
	if err := c.do(ctx, http.MethodGet, p, &ts); err != nil {
		return nil, err
	}
	return ts, nil
}

type logLine struct {
	N int    `json:"n"`
	T string `json:"t"`
}

func (c *Client) TaskLog(ctx context.Context, node, upid string) ([]string, error) {
	if node == "" || upid == "" {
		return nil, fmt.Errorf("pve: node (%q) and upid (%q) are required", node, upid)
	}
	q := url.Values{"limit": {strconv.Itoa(maxTaskLogLines)}}
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/log?" + q.Encode()
	var lines []logLine
	if err := c.do(ctx, http.MethodGet, p, &lines); err != nil {
		return nil, err
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].N < lines[j].N })
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.T)
	}
	return out, nil
}

type Disk struct {
	DevPath string          `json:"devpath"`
	Model   string          `json:"model"`
	Serial  string          `json:"serial"`
	Type    string          `json:"type"`
	Health  string          `json:"health"`
	Used    string          `json:"used"`
	Size    int64           `json:"size"`
	Wearout json.RawMessage `json:"wearout"`
}

func (d Disk) WearoutPct() (float64, bool) {
	if len(d.Wearout) == 0 {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(d.Wearout, &f); err != nil {
		return 0, false
	}
	return f, true
}

func (c *Client) DisksList(ctx context.Context, node string) ([]Disk, error) {
	if node == "" {
		return nil, fmt.Errorf("pve: empty node in DisksList")
	}
	var ds []Disk
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/disks/list"
	if err := c.do(ctx, http.MethodGet, p, &ds); err != nil {
		return nil, err
	}
	return ds, nil
}

func (c *Client) Permissions(ctx context.Context) (map[string]map[string]int, error) {
	var m map[string]map[string]int
	if err := c.do(ctx, http.MethodGet, "/api2/json/access/permissions", &m); err != nil {
		return nil, err
	}
	return m, nil
}

type RRDWindow string

const (
	WindowHour  RRDWindow = "hour"
	WindowDay   RRDWindow = "day"
	WindowWeek  RRDWindow = "week"
	WindowMonth RRDWindow = "month"
	WindowYear  RRDWindow = "year"
)

func ValidRRDWindow(j string) (RRDWindow, bool) {
	switch RRDWindow(j) {
	case WindowHour, WindowDay, WindowWeek, WindowMonth, WindowYear:
		return RRDWindow(j), true
	}
	return "", false
}

type RRDPoint struct {
	Time      int64    `json:"time"`
	CPU       *float64 `json:"cpu,omitempty"`
	MaxCPU    *float64 `json:"maxcpu,omitempty"`
	IOWait    *float64 `json:"iowait,omitempty"`
	LoadAvg   *float64 `json:"loadavg,omitempty"`
	MemUsed   *float64 `json:"memused,omitempty"`
	MemTot    *float64 `json:"memtotal,omitempty"`
	SwapUse   *float64 `json:"swapused,omitempty"`
	SwapTot   *float64 `json:"swaptotal,omitempty"`
	RootUse   *float64 `json:"rootused,omitempty"`
	RootTot   *float64 `json:"roottotal,omitempty"`
	NetIn     *float64 `json:"netin,omitempty"`
	NetOut    *float64 `json:"netout,omitempty"`
	ARCSize   *float64 `json:"arcsize,omitempty"`
	PressCPU  *float64 `json:"pressurecpusome,omitempty"`
	PressIO   *float64 `json:"pressureiosome,omitempty"`
	PressMem  *float64 `json:"pressurememorysome,omitempty"`
	Mem       *float64 `json:"mem,omitempty"`
	MaxMem    *float64 `json:"maxmem,omitempty"`
	Disk      *float64 `json:"disk,omitempty"`
	MaxDisk   *float64 `json:"maxdisk,omitempty"`
	DiskRead  *float64 `json:"diskread,omitempty"`
	DiskWrite *float64 `json:"diskwrite,omitempty"`
}

func (c *Client) RRDNode(ctx context.Context, node string, j RRDWindow) ([]RRDPoint, error) {
	if node == "" {
		return nil, fmt.Errorf("pve: empty node in RRDNode")
	}
	var pts []RRDPoint
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/rrddata?timeframe=" + url.QueryEscape(string(j)) + "&cf=AVERAGE"
	if err := c.do(ctx, http.MethodGet, p, &pts); err != nil {
		return nil, err
	}
	return pts, nil
}

func (c *Client) RRDGuest(ctx context.Context, node string, vmid int, typ string, j RRDWindow) ([]RRDPoint, error) {
	if node == "" || vmid <= 0 {
		return nil, fmt.Errorf("pve: invalid node or vmid in RRDGuest")
	}
	if typ != "lxc" && typ != "qemu" {
		return nil, fmt.Errorf("pve: invalid guest type: %q", typ)
	}
	var pts []RRDPoint
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/" + typ + "/" + strconv.Itoa(vmid) +
		"/rrddata?timeframe=" + url.QueryEscape(string(j)) + "&cf=AVERAGE"
	if err := c.do(ctx, http.MethodGet, p, &pts); err != nil {
		return nil, err
	}
	return pts, nil
}

type Interface struct {
	Iface     string `json:"iface"`
	Type      string `json:"type"`
	Method    string `json:"method"`
	Address   string `json:"address"`
	Netmask   string `json:"netmask"`
	Gateway   string `json:"gateway"`
	CIDR      string `json:"cidr"`
	Bridge    string `json:"bridge_ports"`
	Active    int    `json:"active"`
	Autostart int    `json:"autostart"`
	Comments  string `json:"comments"`
}

type DNSInfo struct {
	Search string `json:"search"`
	DNS1   string `json:"dns1"`
	DNS2   string `json:"dns2"`
	DNS3   string `json:"dns3"`
}

type TimeInfo struct {
	Time      int64  `json:"time"`
	Localtime int64  `json:"localtime"`
	Timezone  string `json:"timezone"`
}

type Certificate struct {
	Filename      string   `json:"filename"`
	Subject       string   `json:"subject"`
	Issuer        string   `json:"issuer"`
	NotBefore     int64    `json:"notbefore"`
	NotAfter      int64    `json:"notafter"`
	Fingerprint   string   `json:"fingerprint"`
	PublicKeyType string   `json:"public-key-type"`
	PublicKeyBits int      `json:"public-key-bits"`
	SAN           []string `json:"san"`
}

type PackageInfo struct {
	Package      string `json:"Package"`
	Title        string `json:"Title"`
	Version      string `json:"Version"`
	OldVersion   string `json:"OldVersion"`
	Arch         string `json:"Arch"`
	Priority     string `json:"Priority"`
	Section      string `json:"Section"`
	Description  string `json:"Description"`
	CurrentState string `json:"CurrentState"`
}

type SyslogLine struct {
	N    int    `json:"n"`
	Text string `json:"t"`
}

func (c *Client) Network(ctx context.Context, node string) ([]Interface, error) {
	var out []Interface
	return out, c.do(ctx, http.MethodGet, "/api2/json/nodes/"+url.PathEscape(node)+"/network", &out)
}

func (c *Client) DNS(ctx context.Context, node string) (DNSInfo, error) {
	var out DNSInfo
	return out, c.do(ctx, http.MethodGet, "/api2/json/nodes/"+url.PathEscape(node)+"/dns", &out)
}

func (c *Client) Time(ctx context.Context, node string) (TimeInfo, error) {
	var out TimeInfo
	return out, c.do(ctx, http.MethodGet, "/api2/json/nodes/"+url.PathEscape(node)+"/time", &out)
}

func (c *Client) Certificates(ctx context.Context, node string) ([]Certificate, error) {
	var out []Certificate
	return out, c.do(ctx, http.MethodGet, "/api2/json/nodes/"+url.PathEscape(node)+"/certificates/info", &out)
}

func (c *Client) Packages(ctx context.Context, node string) ([]PackageInfo, error) {
	var out []PackageInfo
	return out, c.do(ctx, http.MethodGet, "/api2/json/nodes/"+url.PathEscape(node)+"/apt/versions", &out)
}

const MaxSyslog = 500

func (c *Client) Syslog(ctx context.Context, node string, limit int) ([]SyslogLine, error) {
	if limit <= 0 || limit > MaxSyslog {
		limit = MaxSyslog
	}
	var out []SyslogLine
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/syslog?limit=" + strconv.Itoa(limit)
	return out, c.do(ctx, http.MethodGet, p, &out)
}

type PowerCommand string

const (
	PowerReboot   PowerCommand = "reboot"
	PowerShutdown PowerCommand = "shutdown"
)

func ValidPowerCommand(c string) (PowerCommand, bool) {
	switch PowerCommand(c) {
	case PowerReboot, PowerShutdown:
		return PowerCommand(c), true
	}
	return "", false
}

func (c *Client) NodePower(ctx context.Context, node string, cmd PowerCommand) (string, error) {
	if node == "" {
		return "", fmt.Errorf("pve: empty node in NodePower")
	}
	if _, ok := ValidPowerCommand(string(cmd)); !ok {
		return "", fmt.Errorf("pve: invalid power command: %q", cmd)
	}
	var upid string
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/status?command=" + url.QueryEscape(string(cmd))
	if err := c.do(ctx, http.MethodPost, p, &upid); err != nil {
		return "", err
	}
	return upid, nil
}
