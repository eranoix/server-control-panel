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
	  "cpuinfo": {"cpus": 12, "model": "irrelevant for this slice"},
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

func TestTaskListClampsLimitServerSide(t *testing.T) {
	cases := []struct {
		name    string
		opt     TaskListOptions
		want    []string
		mustNot []string
	}{
		{"missing limit becomes the default", TaskListOptions{}, []string{"limit=50"}, []string{"errors=", "typefilter=", "vmid="}},
		{"absurd limit is clamped", TaskListOptions{Limit: 9999}, []string{"limit=200"}, []string{"limit=9999"}},
		{"negative limit becomes the default", TaskListOptions{Limit: -3}, []string{"limit=50"}, []string{"limit=-3"}},
		{"limit at the cap passes", TaskListOptions{Limit: 200}, []string{"limit=200"}, nil},
		{"errors-only emits errors=1", TaskListOptions{ErrorsOnly: true}, []string{"errors=1"}, nil},
		{"typefilter and vmid go when asked", TaskListOptions{TypeFilter: "vzdump", VMID: 204},
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

func TestTaskListDecodes(t *testing.T) {
	const body = `{"data":[
	  {"upid":"UPID:pve:0000AAAA:00BBBB:68A00000:push_file:207:panel@pve!node-apps:",
	   "node":"pve","type":"push_file","id":"207","user":"panel@pve!node-apps",
	   "status":"failed to open /root/infra/nodes/apps/provision.sh","pid":43690,
	   "starttime":1755600000,"endtime":1755600001},
	  {"upid":"UPID:pve:0000BBBB:00CCCC:68A00001:vzsnapshot:201:panel@pve!node-games:",
	   "node":"pve","type":"vzsnapshot","id":"201","user":"panel@pve!node-games",
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
	if ts[1].User != "panel@pve!node-games" || ts[1].EndTime != 1755600101 {
		t.Errorf("task[1] = %+v", ts[1])
	}
}

func TestTaskLogIgnoresCallerLimit(t *testing.T) {
	fn := reflect.TypeOf((*Client).TaskLog)
	if got := fn.NumIn(); got != 4 {
		t.Fatalf("TaskLog has %d parameters (counting the receiver), want 4 — a caller-set limit is forbidden (A-3)", got)
	}

	c, seenURL := captureURL(t, `{"data":[{"n":2,"t":"second"},{"n":1,"t":"first"},{"n":3,"t":"third"}]}`)
	upid := "UPID:pve:0000AAAA:00BBBB:68A00000:vzsnapshot:204:panel@pve!node-lab:"
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
	if fmt.Sprint(lines) != "[first second third]" {
		t.Errorf("lines = %v, want them in n order", lines)
	}
}

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
		t.Error(`wearout "N/A" was read as a number: the screen would show 0% life left on a disk that reports nothing`)
	}
}

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
	for _, body := range []string{`{"data":{}}`, `{"data":null}`} {
		c, _ := captureURL(t, body)
		if m, err := c.Permissions(context.Background()); err != nil || len(m) != 0 {
			t.Errorf("Permissions(%s) = (%v, %v), want (empty, nil)", body, m, err)
		}
	}
	c, _ := captureURL(t, `{"data":{}}`)
	if st, err := c.NodeStatus(context.Background(), "pve"); err != nil || st.Uptime != 0 {
		t.Errorf("NodeStatus = (%+v, %v), want (zeroed, nil)", st, err)
	}
}

func TestResponseAboveCapNamesCap(t *testing.T) {
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
