package pve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// node_test.go — the pins for the five node READ routes.
//
// Each test defends ONE measured defect, not a line of code:
//
//   - the route is /nodes/{node}/tasks, never /cluster/tasks — this host is not
//     a cluster and the second one returns [] with 200;
//   - the limit belongs to the SERVER, both in /tasks (clamp) and in
//     /tasks/…/log (absent from the signature), because the 1 MiB ceiling in
//     do() turns a large response into an unreadable unmarshal error;
//   - an empty envelope is an empty result, not an error;
//   - a response above the ceiling NAMES the ceiling.
//
// The scaffolding is newTestClient from client_test.go — there is no second one.

// ---------------------------------------------------------------- helpers ---

// captureURL returns a client whose fake server records the last URL requested
// and answers with the given body. It is the instrument behind the URL
// assertions: what is proven here is what the hypervisor RECEIVES, not what the
// code appears to say.
func captureURL(t *testing.T, body string) (*Client, *string) {
	t.Helper()
	var seenURL string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	return c, &seenURL
}

// ------------------------------------------------------------- NodeStatus ---

// TestNodeStatusReadsRealFields uses the REAL shape of /nodes/pve/status
// (measured live: RAM 40.1/67.2 GB, load 1.14/1.55/1.70, pve-manager/9.2.2),
// with fields this subset IGNORES on purpose (cpuinfo, wait, idle). A parser
// that breaks on an unknown field would break on the next hypervisor upgrade.
func TestNodeStatusReadsRealFields(t *testing.T) {
	const body = `{"data":{
	  "uptime": 123456,
	  "pveversion": "pve-manager/9.2.2/abcdef",
	  "cpu": 0.0345,
	  "loadavg": ["1.14","1.55","1.70"],
	  "memory": {"total": 67200000000, "used": 40100000000, "free": 27100000000},
	  "swap":   {"total": 8000000000,  "used": 1000000,     "free": 7999000000},
	  "rootfs": {"total": 100000000000,"used": 20000000000, "avail": 80000000000, "free": 80000000000},
	  "ksm":    {"shared": 4096},
	  "cpuinfo": {"cpus": 12, "model": "irrelevante para o recorte"},
	  "wait": 0.001, "idle": 0
	}}`
	c, seenURL := captureURL(t, body)

	st, err := c.NodeStatus(context.Background(), "pve")
	if err != nil {
		t.Fatalf("NodeStatus: %v", err)
	}
	if *seenURL != "/api2/json/nodes/pve/status" {
		t.Errorf("URL = %q, want /api2/json/nodes/pve/status", *seenURL)
	}
	if st.Uptime != 123456 {
		t.Errorf("Uptime = %d, want 123456", st.Uptime)
	}
	if st.PVEVersion != "pve-manager/9.2.2/abcdef" {
		t.Errorf("PVEVersion = %q", st.PVEVersion)
	}
	if fmt.Sprint(st.LoadAvg) != "[1.14 1.55 1.70]" {
		t.Errorf("LoadAvg = %v, want the THREE windows exactly as the PVE sends them (strings)", st.LoadAvg)
	}
	if st.Memory.Total != 67200000000 || st.Memory.Used != 40100000000 {
		t.Errorf("Memory = %+v", st.Memory)
	}
	if st.Swap.Total != 8000000000 {
		t.Errorf("Swap = %+v", st.Swap)
	}
	if st.RootFS.Total != 100000000000 || st.RootFS.Avail != 80000000000 {
		t.Errorf("RootFS = %+v", st.RootFS)
	}
	if st.KSM.Shared != 4096 {
		t.Errorf("KSM = %+v", st.KSM)
	}
	if st.CPU < 0.03 || st.CPU > 0.04 {
		t.Errorf("CPU = %v, want ~0.0345", st.CPU)
	}
}

// --------------------------------------------------------------- TaskList ---

// 🔴 TestTaskListUsesNodeRouteNeverCluster pins the cluster-route trap.
// Measured: /cluster/tasks returns {"data":[]} with 200 on this host, because
// it is NOT a cluster. Whoever swaps the route gets an empty screen with no
// error at all — the perfect false green, and the reason this test asserts the
// URL and not the result.
func TestTaskListUsesNodeRouteNeverCluster(t *testing.T) {
	c, seenURL := captureURL(t, `{"data":[]}`)
	if _, err := c.TaskList(context.Background(), "pve", TaskListOptions{}); err != nil {
		t.Fatalf("TaskList: %v", err)
	}
	if strings.Contains(*seenURL, "/cluster/") {
		t.Fatalf("URL = %q — /cluster/tasks returns [] with a 200 on this host (A-1)", *seenURL)
	}
	if !strings.HasPrefix(*seenURL, "/api2/json/nodes/pve/tasks") {
		t.Fatalf("URL = %q, want /api2/json/nodes/pve/tasks…", *seenURL)
	}
}

// 🔴 TestTaskListClampsLimitServerSide: the ceiling belongs to the SERVER. A
// `limit` that comes from the browser and reaches the hypervisor intact is a
// request for 1 MiB of JSON disguised as a screen parameter.
func TestTaskListClampsLimitServerSide(t *testing.T) {
	cases := []struct {
		name    string
		opt     TaskListOptions
		want    []string
		mustNot []string
	}{
		{"limit ausente vira o padrão", TaskListOptions{}, []string{"limit=50"}, []string{"errors=", "typefilter=", "vmid="}},
		{"limit absurdo é clampado", TaskListOptions{Limit: 9999}, []string{"limit=200"}, []string{"limit=9999"}},
		{"limit negativo vira o padrão", TaskListOptions{Limit: -3}, []string{"limit=50"}, []string{"limit=-3"}},
		{"limit no teto passa", TaskListOptions{Limit: 200}, []string{"limit=200"}, nil},
		{"só-erros emite errors=1", TaskListOptions{ErrorsOnly: true}, []string{"errors=1"}, nil},
		{"typefilter e vmid vão quando pedidos", TaskListOptions{TypeFilter: "vzdump", VMID: 204},
			[]string{"typefilter=vzdump", "vmid=204"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, seenURL := captureURL(t, `{"data":[]}`)
			if _, err := c.TaskList(context.Background(), "pve", tc.opt); err != nil {
				t.Fatalf("TaskList: %v", err)
			}
			for _, q := range tc.want {
				if !strings.Contains(*seenURL, q) {
					t.Errorf("URL = %q, want it to contain %q", *seenURL, q)
				}
			}
			for _, q := range tc.mustNot {
				if strings.Contains(*seenURL, q) {
					t.Errorf("URL = %q, it canNOT contain %q", *seenURL, q)
				}
			}
		})
	}
}

// TestTaskListDecodes proves the real shape of a task line, including the
// two that the study measured as real failures nobody was seeing.
func TestTaskListDecodes(t *testing.T) {
	const body = `{"data":[
	  {"upid":"UPID:pve:0000AAAA:00BBBB:68A00000:push_file:207:lab@pve!node-apps:",
	   "node":"pve","type":"push_file","id":"207","user":"lab@pve!node-apps",
	   "status":"failed to open /root/infra/nodes/apps/provision.sh","pid":43690,
	   "starttime":1755600000,"endtime":1755600001},
	  {"upid":"UPID:pve:0000BBBB:00CCCC:68A00001:vzsnapshot:201:lab@pve!node-games:",
	   "node":"pve","type":"vzsnapshot","id":"201","user":"lab@pve!node-games",
	   "status":"snapshot feature is not available","pid":43691,
	   "starttime":1755600100,"endtime":1755600101}
	]}`
	c, _ := captureURL(t, body)
	ts, err := c.TaskList(context.Background(), "pve", TaskListOptions{ErrorsOnly: true})
	if err != nil {
		t.Fatalf("TaskList: %v", err)
	}
	if len(ts) != 2 {
		t.Fatalf("len = %d, want 2", len(ts))
	}
	if ts[0].Type != "push_file" || ts[0].ID != "207" || ts[0].PID != 43690 {
		t.Errorf("task[0] = %+v", ts[0])
	}
	if !strings.Contains(ts[0].Status, "provision.sh") {
		t.Errorf("status[0] = %q — the real reason is the operator's only clue", ts[0].Status)
	}
	if ts[1].User != "lab@pve!node-games" || ts[1].EndTime != 1755600101 {
		t.Errorf("task[1] = %+v", ts[1])
	}
}

// ---------------------------------------------------------------- TaskLog ---

// 🔴 TestTaskLogIgnoresCallerLimit proves the defence BY CONSTRUCTION:
// what does not exist in the signature cannot be forwarded from the browser.
// The reflection is deliberate — asserting only the URL would let someone add
// the parameter back and forget it at 200 by default.
func TestTaskLogIgnoresCallerLimit(t *testing.T) {
	fn := reflect.TypeOf((*Client).TaskLog)
	// receiver + ctx + node + upid = 4. A fifth parameter could only be the
	// caller's limit coming back through the back door.
	if got := fn.NumIn(); got != 4 {
		t.Fatalf("TaskLog has %d parameters (counting the receiver), want 4 — a caller-set limit is forbidden (A-3)", got)
	}

	c, seenURL := captureURL(t, `{"data":[{"n":2,"t":"segunda"},{"n":1,"t":"primeira"},{"n":3,"t":"terceira"}]}`)
	upid := "UPID:pve:0000AAAA:00BBBB:68A00000:vzsnapshot:204:lab@pve!node-lab:"
	lines, err := c.TaskLog(context.Background(), "pve", upid)
	if err != nil {
		t.Fatalf("TaskLog: %v", err)
	}
	if !strings.Contains(*seenURL, "limit=200") {
		t.Errorf("URL = %q, want limit=200 pinned on the server", *seenURL)
	}
	if !strings.Contains(*seenURL, "/tasks/") || !strings.Contains(*seenURL, "/log") {
		t.Errorf("URL = %q, want /nodes/pve/tasks/{upid}/log", *seenURL)
	}
	if fmt.Sprint(lines) != "[primeira segunda terceira]" {
		t.Errorf("lines = %v, want them in n order", lines)
	}
}

// -------------------------------------------------------------- DisksList ---

// TestDisksListNormalizesWearout: the hypervisor sends wearout as a NUMBER on
// the SSD that has the datum and as the string "N/A" on the disk that does not
// report it. A raw `int64` would break the unmarshal of the WHOLE list because
// of one disk — and the screen would go empty because of a cosmetic field.
func TestDisksListNormalizesWearout(t *testing.T) {
	const body = `{"data":[
	  {"devpath":"/dev/nvme0n1","model":"Lexar NQ790 1TB","serial":"NL123","type":"nvme",
	   "health":"PASSED","size":1000204886016,"wearout":100},
	  {"devpath":"/dev/sda","model":"Generic USB","serial":"X","type":"hdd",
	   "health":"UNKNOWN","size":500107862016,"wearout":"N/A"}
	]}`
	c, seenURL := captureURL(t, body)
	ds, err := c.DisksList(context.Background(), "pve")
	if err != nil {
		t.Fatalf("DisksList: %v", err)
	}
	if *seenURL != "/api2/json/nodes/pve/disks/list" {
		t.Errorf("URL = %q", *seenURL)
	}
	if len(ds) != 2 {
		t.Fatalf("len = %d, want 2 (one numeric wearout canNOT take the other down)", len(ds))
	}
	if ds[0].Model != "Lexar NQ790 1TB" || ds[0].Health != "PASSED" || ds[0].Size != 1000204886016 {
		t.Errorf("disk[0] = %+v", ds[0])
	}
	if v, ok := ds[0].WearoutPct(); !ok || v != 100 {
		t.Errorf("wearout[0] = (%v,%v), want (100,true)", v, ok)
	}
	if _, ok := ds[1].WearoutPct(); ok {
		t.Error(`wearout "N/A" foi lido como número — a tela mostraria 0% de vida útil num disco que não reporta nada`)
	}
}

// ------------------------------------------------------------ Permissions ---

// TestPermissionsMap: /access/permissions answers path → privilege → 0|1. It
// is what explains ON SCREEN why storage capacity, backup evidence and the
// zpool were missing on the first pass. The REAL reason, read in this
// hypervisor's Perl source: the zpool required `Sys.Audit` on `/`
// (Disks/ZFS.pm:62-64) and the storage list is filtered by `Datastore.Audit`
// storage by storage (Storage/Status.pm:72-76). It was NOT "an ACL on
// /storage", as the first pass wrote — see Permissions in node.go.
func TestPermissionsMap(t *testing.T) {
	const body = `{"data":{"/vms/204":{"VM.Audit":1,"VM.PowerMgmt":1,"VM.Snapshot":1},"/nodes":{"Sys.Audit":1}}}`
	c, seenURL := captureURL(t, body)
	m, err := c.Permissions(context.Background())
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	if *seenURL != "/api2/json/access/permissions" {
		t.Errorf("URL = %q", *seenURL)
	}
	if m["/vms/204"]["VM.Snapshot"] != 1 {
		t.Errorf("permissions = %v", m)
	}
	if _, has := m["/storage"]; has {
		t.Error("the fixture gained /storage — the test would stop telling wave 1 from wave 2")
	}
}

// -------------------------------------------------------- empty envelope ----

// 🔴 TestEmptyEnvelopeIsNotError pins the empty-envelope trap: do() treats
// len(env.Data)==0 as a HARD error, and rightly so (200 with no envelope is a
// broken response). But {"data":[]} and {"data":null} mean "nothing here", and
// a screen that says "hypervisor error" when there is no task at all trains the
// operator to ignore errors.
func TestEmptyEnvelopeIsNotError(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":null}`} {
		t.Run(body, func(t *testing.T) {
			c, _ := captureURL(t, body)
			ctx := context.Background()

			if ts, err := c.TaskList(ctx, "pve", TaskListOptions{}); err != nil || len(ts) != 0 {
				t.Errorf("TaskList = (%v, %v), want (empty, nil)", ts, err)
			}
			if ls, err := c.TaskLog(ctx, "pve", "UPID:x:1:2:3:t:4:u:"); err != nil || len(ls) != 0 {
				t.Errorf("TaskLog = (%v, %v), want (empty, nil)", ls, err)
			}
			if ds, err := c.DisksList(ctx, "pve"); err != nil || len(ds) != 0 {
				t.Errorf("DisksList = (%v, %v), want (empty, nil)", ds, err)
			}
		})
	}
	// Permissions is a map, not a list: {"data":{}} is its empty shape.
	for _, body := range []string{`{"data":{}}`, `{"data":null}`} {
		c, _ := captureURL(t, body)
		if m, err := c.Permissions(context.Background()); err != nil || len(m) != 0 {
			t.Errorf("Permissions(%s) = (%v, %v), want (empty, nil)", body, m, err)
		}
	}
	// NodeStatus with an empty object: zeroed struct, no error.
	c, _ := captureURL(t, `{"data":{}}`)
	if st, err := c.NodeStatus(context.Background(), "pve"); err != nil || st.Uptime != 0 {
		t.Errorf("NodeStatus = (%+v, %v), want (zeroed, nil)", st, err)
	}
}

// ---------------------------------------------------------- 1 MiB ceiling ---

// 🔴 TestResponseAboveCapNamesCap: today do() reads with
// io.LimitReader(body, maxBodyBytes) and hands the TRUNCATED JSON to
// json.Unmarshal, which complains about "unexpected end of JSON input" — a
// message that does not say "the response was too large" and sends the operator
// looking for a defect in the parser. The ceiling has to announce itself.
func TestResponseAboveCapNamesCap(t *testing.T) {
	// ~2 MiB of VALID JSON: if the ceiling did not exist, this would decode fine.
	lines := make([]map[string]any, 0, 20000)
	for i := 1; i <= 20000; i++ {
		lines = append(lines, map[string]any{"n": i, "t": strings.Repeat("x", 100)})
	}
	big, err := json.Marshal(map[string]any{"data": lines})
	if err != nil {
		t.Fatal(err)
	}
	if len(big) <= maxBodyBytes {
		t.Fatalf("the fixture has %d bytes — it has to exceed the %d ceiling to exercise it", len(big), maxBodyBytes)
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(big)
	})

	_, err = c.TaskLog(context.Background(), "pve", "UPID:x:1:2:3:t:4:u:")
	if err == nil {
		t.Fatal("a response above the ceiling was accepted as success")
	}
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("error = %v — want errors.Is(err, ErrResponseTooLarge), not an unmarshal error", err)
	}
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("the error is not a *pve.Error: %T", err)
	}
	if pe.Kind != KindHypervisor {
		t.Errorf("Kind = %v, want KindHypervisor", pe.Kind)
	}
	if len(pe.Body) > 1024 {
		t.Errorf("Body has %d bytes — the error becomes on-screen text; 1 MiB of JSON is noise, not a clue", len(pe.Body))
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Errorf("message = %q, it has to name the ceiling", err.Error())
	}
}
