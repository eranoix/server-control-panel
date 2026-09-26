package pve

// node.go — the hypervisor NODE's READ routes: health, tasks, task log, disks
// and the token's own permissions.
//
// Why each slice exists:
//
//  1. NodeStatus publishes IDENTITY and STATE (RAM, swap, rootfs, load, uptime,
//     version, KSM), never a time series — whoever wants a graph has the
//     embedded Grafana. That is why there is no history, no moving average and
//     nothing that has to be kept between calls.
//
//  2. Tasks, disks and permissions ONLY work with the AUDIT token. Measured:
//     GET /nodes/pve/tasks?limit=50 with the node token (lab@pve!node-apps)
//     returns 200 with len=0 — Tasks.pm:40-45 requires Sys.Audit on /nodes,
//     which the LabOperador role does not have. It is not an error: it is an
//     empty list telling a lie. internal/api is what picks the token, and there
//     is a test there that fails by naming the key it read.
//
//  3. The response ceiling belongs to the SERVER, always. do() reads at most
//     1 MiB (maxBodyBytes); above that the response is truncated and stops
//     being JSON. A `limit` that travels from the browser through to the
//     hypervisor is a request for a pathological response dressed up as a
//     screen parameter.
//
// Every request goes through do() — there is no second constructor here.

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
	// defaultTasks is what the screen asks for when it asks for nothing.
	defaultTasks = 50
	// MaxTasks is the SERVER's ceiling for /nodes/{n}/tasks. Measured: 50 tasks
	// cost ~11 KB, so 200 sits with wide margin under do()'s 1 MiB ceiling — and
	// the caller has no way to ask for more.
	//
	// EXPORTED so that the HTTP handler clamps the browser's parameter using THIS
	// number, and not a copy: the clamp happens in both layers, but the constant is
	// only one. Two constants would diverge the day somebody touched one of them.
	MaxTasks = 200
	// maxTaskLogLines is the ceiling for ONE task's log, and it is NOT a parameter.
	// TaskLog's signature deliberately accepts no limit: what does not exist cannot
	// be relayed from the browser. The reason is do()'s 1 MiB ceiling — a response
	// above it comes back truncated and turns into ErrRespostaGrande.
	maxTaskLogLines = 200
)

// MemInfo is the total/used/free triple the hypervisor returns for memory and swap.
type MemInfo struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

// RootFSInfo is the NODE's root filesystem. It is not the ZFS pool: storage
// capacity and zpool health are other routes, added later — see storage.go.
type RootFSInfo struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Avail int64 `json:"avail"`
}

// KSMInfo is the sharing of identical pages between guests.
type KSMInfo struct {
	Shared int64 `json:"shared"`
}

// CPUInfo describes the node's processor. `Cpus` is the total number of threads
// the hypervisor sees; `Sockets` how many physical packages.
type CPUInfo struct {
	Model   string `json:"model"`
	Cpus    int    `json:"cpus"`
	Sockets int    `json:"sockets"`
	MHz     string `json:"mhz"`
}

// NodeStatus is the slice of /nodes/{node}/status.
//
// LoadAvg is []string because that is HOW the hypervisor sends it ("1.14");
// converting here would force a decision about what to do with a value outside
// the format, and the screen only needs to display it. Fields this slice
// ignores (cpuinfo, wait, idle, current-kernel…) are dropped by the unmarshal,
// and that is what makes the parser survive the next hypervisor upgrade.
type NodeStatus struct {
	Uptime     int64      `json:"uptime"`
	PVEVersion string     `json:"pveversion"`
	CPU        float64    `json:"cpu"`
	LoadAvg    []string   `json:"loadavg"`
	Memory     MemInfo    `json:"memory"`
	Swap       MemInfo    `json:"swap"`
	RootFS     RootFSInfo `json:"rootfs"`
	KSM        KSMInfo    `json:"ksm"`

	// The three below came in with the parity pass against the Proxmox screen. Its
	// Summary panel shows all three, and the operator cannot reach that UI.
	//
	// Wait is the IO DELAY, and it is the metric that separates "the machine is
	// busy" from "the machine is waiting on disk" — on a server whose pool is a
	// single disk, that distinction is the start of every diagnosis.
	Wait     float64 `json:"wait"`
	KVersion string  `json:"kversion"`
	CPUInfo  CPUInfo `json:"cpuinfo"`
}

// NodeStatus reads the hypervisor node's health. Measured: 92 ms.
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

// Task is one row of the hypervisor's task log. It is the source of the
// failures nobody sees today: the study measured 10 real errors on this host,
// among them `push_file 207 failed to open …/provision.sh` and
// `vzsnapshot 201 snapshot feature is not available`.
type Task struct {
	UPID      string `json:"upid"`
	Node      string `json:"node"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	User      string `json:"user"`
	Status    string `json:"status"` // "OK" or the error text
	PID       int    `json:"pid"`
	StartTime int64  `json:"starttime"`
	EndTime   int64  `json:"endtime"`
}

// TaskListOptions are the filters the hypervisor applies BEFORE answering.
// Limit is a suggestion: what rules the ceiling is maxTasks.
type TaskListOptions struct {
	Limit      int
	ErrorsOnly bool
	TypeFilter string
	VMID       int
}

// TaskList returns the NODE's tasks.
//
// 🔴 The route is /nodes/{node}/tasks and never /cluster/tasks: measured, this
// host is not a cluster and /cluster/tasks returns {"data":[]} with a 200 — an
// empty screen with no error at all, which is the perfect false green.
func (c *Client) TaskList(ctx context.Context, node string, opt TaskListOptions) ([]Task, error) {
	if node == "" {
		return nil, fmt.Errorf("pve: empty node in TaskList")
	}
	limite := opt.Limit
	if limite <= 0 {
		limite = defaultTasks
	}
	if limite > MaxTasks {
		// Clamp, not an error: the screen asked for too much, and cutting is more
		// useful than refusing. The ceiling exists because of maxBodyBytes, not by taste.
		limite = MaxTasks
	}
	q := url.Values{"limit": {strconv.Itoa(limite)}}
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

// linhaDeLog is the shape the hypervisor uses in a task log: line number and text.
type linhaDeLog struct {
	N int    `json:"n"`
	T string `json:"t"`
}

// TaskLog returns ONE task's log, in line order.
//
// 🔴 The signature has NO limit parameter, and that is the defence, not an
// oversight: the ceiling is maxTaskLogLines, fixed here. A long vzdump log
// easily passes the 1 MiB that do() reads, and a truncated response becomes
// ErrRespostaGrande — an honest error, but an error. Better to ask for 200 lines.
func (c *Client) TaskLog(ctx context.Context, node, upid string) ([]string, error) {
	if node == "" || upid == "" {
		return nil, fmt.Errorf("pve: node (%q) and upid (%q) are required", node, upid)
	}
	q := url.Values{"limit": {strconv.Itoa(maxTaskLogLines)}}
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/log?" + q.Encode()
	var linhas []linhaDeLog
	if err := c.do(ctx, http.MethodGet, p, &linhas); err != nil {
		return nil, err
	}
	// The hypervisor returns them in order, but that order is promised nowhere —
	// and a log out of order is a log that misleads whoever is hunting the cause.
	sort.SliceStable(linhas, func(i, j int) bool { return linhas[i].N < linhas[j].N })
	out := make([]string, 0, len(linhas))
	for _, l := range linhas {
		out = append(out, l.T)
	}
	return out, nil
}

// Disk is a PHYSICAL disk of the node, with the health SMART reports.
//
// Wearout is json.RawMessage because the hypervisor sends a NUMBER on an SSD
// that reports remaining life and the string "N/A" on a disk that does not.
// With a raw `int64`, a single silent disk would bring down the unmarshal of
// the WHOLE LIST — the table would go empty because of a cosmetic field.
// Whoever wants the number uses WearoutPct.
type Disk struct {
	DevPath string `json:"devpath"`
	Model   string `json:"model"`
	Serial  string `json:"serial"`
	Type    string `json:"type"`
	Health  string `json:"health"`
	// Used is what the hypervisor already knows about the disk's ROLE: "ZFS",
	// "LVM", "BIOS boot", "partitions", or empty when it is free. It was being
	// thrown away, and it is the cheapest answer to "what is this disk?" — the
	// question the disks screen existed to answer and did not.
	Used    string          `json:"used"`
	Size    int64           `json:"size"`
	Wearout json.RawMessage `json:"wearout"`
}

// WearoutPct returns the percentage of remaining life and whether it EXISTS.
// The second return value is the point: "N/A" turning into 0 would show a new
// disk as worn out, and it is exactly the disk with no data that has to show up
// as having no data.
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

// DisksList returns the node's physical disks. Measured: 597 ms — the most
// expensive route in this set, and that is why it is on demand, never in the
// poller tick.
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

// Permissions returns what THIS token can do, path by path:
// path → privilege → 0|1.
//
// It exists so the screen can explain, by MEASUREMENT and not by promise, what
// the panel can and cannot see of the hypervisor. Three unexplained holes turn
// into three tickets; one block that shows the reason, none.
//
// 🔴 CORRECTING A TEXT THAT WAS FALSE. The first version wrote here that
// storage capacity, backup evidence and zpool health "require an ACL on
// /storage". They did not, and the suggested ACL would NOT have solved it. Read
// in THIS hypervisor's Perl source:
//
//	/nodes/{n}/disks/zfs  → Disks/ZFS.pm:62-64
//	                        check => ['perm', '/', ['Sys.Audit']]
//	/nodes/{n}/storage    → Storage/Status.pm:72-76
//	                        user => 'all', and the LIST is filtered by
//	                        Datastore.Audit|Datastore.AllocateSpace
//	                        on /storage/<storage>, storage by storage
//
// That is: the zpool never depended on `/storage` — it depended on **Sys.Audit
// on `/`**; and storage does not return 403, it returns a silently filtered
// list. A `pveum acl modify /storage` would have unblocked half the problem and
// left the other half mute. That is why the fix asked for PVEAuditor on `/`
// with --propagate 1, and it worked on both routes at once.
//
// An absence declared with the WRONG reason is worse than an absence with no
// reason: it teaches the next session the wrong thing, and the next session acts.
func (c *Client) Permissions(ctx context.Context) (map[string]map[string]int, error) {
	var m map[string]map[string]int
	if err := c.do(ctx, http.MethodGet, "/api2/json/access/permissions", &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Time series
//
// 🔴 The graph is the centrepiece of the Proxmox screen, and it is what
// separates "the dev box is at 83% RAM" from "the dev box has been climbing for
// three hours". The second lets you act before the fact; the first only states it.
//
// The hypervisor's RRD returns the series ALREADY AGGREGATED — the browser does
// not interpolate, does not resample and does not invent a point. Each sample
// carries its own `time`, and a hole in the series is a real hole: the
// hypervisor was off, or the RRD had no data yet. Filling a hole with zero
// would make a power cut look like a stretch of idleness.

// JanelaRRD is the requested interval. These are the same names the hypervisor accepts.
type JanelaRRD string

const (
	JanelaHora JanelaRRD = "hour"
	JanelaDia  JanelaRRD = "day"
	JanelaSem  JanelaRRD = "week"
	JanelaMes  JanelaRRD = "month"
	JanelaAno  JanelaRRD = "year"
)

// JanelaRRDValida exists because the timeframe goes into the hypervisor's URL.
// Without an allowlist, a string coming from the panel's query would become a
// path on the hypervisor.
func JanelaRRDValida(j string) (JanelaRRD, bool) {
	switch JanelaRRD(j) {
	case JanelaHora, JanelaDia, JanelaSem, JanelaMes, JanelaAno:
		return JanelaRRD(j), true
	}
	return "", false
}

// PontoRRD is one sample. The fields are pointers because **absent and zero are
// different things**: `cpu: 0` is an idle machine; an absent `cpu` is a machine
// about which nothing is known at that instant.
type PontoRRD struct {
	Time    int64    `json:"time"`
	CPU     *float64 `json:"cpu,omitempty"`
	MaxCPU  *float64 `json:"maxcpu,omitempty"`
	IOWait  *float64 `json:"iowait,omitempty"`
	LoadAvg *float64 `json:"loadavg,omitempty"`
	MemUsed *float64 `json:"memused,omitempty"`
	MemTot  *float64 `json:"memtotal,omitempty"`
	SwapUse *float64 `json:"swapused,omitempty"`
	SwapTot *float64 `json:"swaptotal,omitempty"`
	RootUse *float64 `json:"rootused,omitempty"`
	RootTot *float64 `json:"roottotal,omitempty"`
	NetIn   *float64 `json:"netin,omitempty"`
	NetOut  *float64 `json:"netout,omitempty"`
	// ZFS ARC: how much RAM the pool's cache is holding. On a lab with 64 GB and a
	// single-disk pool, it is the most common explanation for "the RAM vanished".
	ARCSize *float64 `json:"arcsize,omitempty"`
	// PSI — resource pressure. Proxmox 9 exposes it and its own UI does not even
	// show it yet; it is the earliest saturation signal the kernel gives.
	PressCPU *float64 `json:"pressurecpusome,omitempty"`
	PressIO  *float64 `json:"pressureiosome,omitempty"`
	PressMem *float64 `json:"pressurememorysome,omitempty"`
	// Guests carry these; the node does not.
	Mem       *float64 `json:"mem,omitempty"`
	MaxMem    *float64 `json:"maxmem,omitempty"`
	Disk      *float64 `json:"disk,omitempty"`
	MaxDisk   *float64 `json:"maxdisk,omitempty"`
	DiskRead  *float64 `json:"diskread,omitempty"`
	DiskWrite *float64 `json:"diskwrite,omitempty"`
}

// RRDNode returns the hypervisor's series.
func (c *Client) RRDNode(ctx context.Context, node string, j JanelaRRD) ([]PontoRRD, error) {
	if node == "" {
		return nil, fmt.Errorf("pve: empty node in RRDNode")
	}
	var pts []PontoRRD
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/rrddata?timeframe=" + url.QueryEscape(string(j)) + "&cf=AVERAGE"
	if err := c.do(ctx, http.MethodGet, p, &pts); err != nil {
		return nil, err
	}
	return pts, nil
}

// RRDGuest returns a guest's series. `typ` is "lxc" or "qemu" — the same
// vocabulary as the id in /cluster/resources.
func (c *Client) RRDGuest(ctx context.Context, node string, vmid int, typ string, j JanelaRRD) ([]PontoRRD, error) {
	if node == "" || vmid <= 0 {
		return nil, fmt.Errorf("pve: invalid node or vmid in RRDGuest")
	}
	if typ != "lxc" && typ != "qemu" {
		return nil, fmt.Errorf("pve: invalid guest type: %q", typ)
	}
	var pts []PontoRRD
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/" + typ + "/" + strconv.Itoa(vmid) +
		"/rrddata?timeframe=" + url.QueryEscape(string(j)) + "&cf=AVERAGE"
	if err := c.do(ctx, http.MethodGet, p, &pts); err != nil {
		return nil, err
	}
	return pts, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Node system
//
// 🔴 The four routes below only started answering once the operator granted
// full access. `syslog` required Sys.Syslog and `apt` required Sys.Modify; both
// returned 403 to the audit token.
//
// They exist because the operator works fully remote and does NOT reach the
// Proxmox UI: "what is the bridge's IP?", "when does the certificate expire?",
// "which kernel is installed?" were questions with no answer short of opening
// an SSH session.

// Interface is one row of /nodes/{n}/network.
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

// DNSInfo is /nodes/{n}/dns.
type DNSInfo struct {
	Search string `json:"search"`
	DNS1   string `json:"dns1"`
	DNS2   string `json:"dns2"`
	DNS3   string `json:"dns3"`
}

// TimeInfo is /nodes/{n}/time. `Localtime` and `Time` differ by the timezone,
// and the difference between them IS the information: a server on UTC and one
// on São Paulo produce backup windows that never meet — that is how this lab's
// off-site chain died for 14 days.
type TimeInfo struct {
	Time      int64  `json:"time"`
	Localtime int64  `json:"localtime"`
	Timezone  string `json:"timezone"`
}

// Certificado is one entry of /nodes/{n}/certificates/info.
type Certificado struct {
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

// Pacote is one entry of /nodes/{n}/apt/versions.
type Pacote struct {
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

// LinhaSyslog is one entry of /nodes/{n}/syslog.
type LinhaSyslog struct {
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

func (c *Client) Certificados(ctx context.Context, node string) ([]Certificado, error) {
	var out []Certificado
	return out, c.do(ctx, http.MethodGet, "/api2/json/nodes/"+url.PathEscape(node)+"/certificates/info", &out)
}

func (c *Client) Pacotes(ctx context.Context, node string) ([]Pacote, error) {
	var out []Pacote
	return out, c.do(ctx, http.MethodGet, "/api2/json/nodes/"+url.PathEscape(node)+"/apt/versions", &out)
}

// MaxSyslog caps how much the panel asks for. The ceiling lives HERE and is the
// same one used at the edge: two independent ceilings diverge the day somebody
// touches one of them.
const MaxSyslog = 500

func (c *Client) Syslog(ctx context.Context, node string, limite int) ([]LinhaSyslog, error) {
	if limite <= 0 || limite > MaxSyslog {
		limite = MaxSyslog
	}
	var out []LinhaSyslog
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/syslog?limit=" + strconv.Itoa(limite)
	return out, c.do(ctx, http.MethodGet, p, &out)
}

// ─────────────────────────────────────────────────────────────────────────────
// HYPERVISOR power
//
// 🔴 THE ONLY PANEL ACTION WHOSE MISTAKE HAS NO REMOTE UNDO.
//
// This machine has no IPMI. Wake-on-LAN is no use either: the Wake-on-LAN
// router would be the host itself (it is the one running the Tailscale subnet
// router), so when it goes down there is nowhere to wake it from. A `shutdown`
// from here means: somebody has to be PHYSICALLY in front of the machine to
// turn it back on.
//
// A `reboot` is less serious only for as long as it works; if the machine does
// not come back — a pool that does not import, a new kernel that does not boot,
// a stuck fsck — the result is identical to the shutdown.
//
// The command goes through an ALLOWLIST. It ends up in the body of a POST to
// the hypervisor, and `Nodes.pm:698` only accepts `reboot` and `shutdown`;
// letting a free string through would give the panel the chance to send
// anything a future hypervisor version might come to accept there.

// ComandoDeEnergia is what can be sent to the NODE. There are two, and never more.
type ComandoDeEnergia string

const (
	EnergiaReboot   ComandoDeEnergia = "reboot"
	EnergiaShutdown ComandoDeEnergia = "shutdown"
)

// ComandoDeEnergiaValido is the allowlist. Outside it, nothing reaches the hypervisor.
func ComandoDeEnergiaValido(c string) (ComandoDeEnergia, bool) {
	switch ComandoDeEnergia(c) {
	case EnergiaReboot, EnergiaShutdown:
		return ComandoDeEnergia(c), true
	}
	return "", false
}

// NodePower tells the node to reboot or shut down.
//
// It returns the UPID when the hypervisor gives one — but it does NOT wait for
// the task to finish, and that is deliberate: the "reboot" task only finishes
// when the machine comes back, and waiting on it would hold the request open
// exactly while the other side is dying. The caller gets the confirmation that
// the command WAS ACCEPTED, which is the only thing still assertable from this side.
func (c *Client) NodePower(ctx context.Context, node string, cmd ComandoDeEnergia) (string, error) {
	if node == "" {
		return "", fmt.Errorf("pve: empty node in NodePower")
	}
	if _, ok := ComandoDeEnergiaValido(string(cmd)); !ok {
		return "", fmt.Errorf("pve: invalid power command: %q", cmd)
	}
	var upid string
	p := "/api2/json/nodes/" + url.PathEscape(node) + "/status?command=" + url.QueryEscape(string(cmd))
	if err := c.do(ctx, http.MethodPost, p, &upid); err != nil {
		return "", err
	}
	return upid, nil
}
