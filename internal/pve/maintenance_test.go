package pve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestRebootBuildsRightRoute — reboot is a /status verb like the others, and the
// pin exists so that it does NOT turn into stop+start in a distracted
// refactoring: the two look alike on the screen and are very different for
// whoever is inside the guest.
func TestRebootBuildsRightRoute(t *testing.T) {
	var seen string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":"` + fakeUPID + `"}`))
	})
	if _, err := c.Reboot(context.Background(), "pve", 207, "lxc"); err != nil {
		t.Fatal(err)
	}
	if want := "/api2/json/nodes/pve/lxc/207/status/reboot"; seen != want {
		t.Errorf("route = %q, want %q", seen, want)
	}
}

// 🔴 TestCloneUsesRightNameParamPerType — the defect this pin exists to
// prevent is SILENT: LXC uses `hostname`, QEMU uses `name`, and sending the
// wrong one raises no error. The hypervisor simply ignores it, and the clone is
// born without a name. It only turns up days later, looking at a list with an
// anonymous guest in it.
func TestCloneUsesRightNameParamPerType(t *testing.T) {
	cases := []struct {
		typ, wantKey, notWant string
	}{
		{"lxc", "hostname", "name"},
		{"qemu", "name", "hostname"},
	}
	for _, cs := range cases {
		var q url.Values
		var path string
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			q = r.URL.Query()
			path = r.URL.Path
			_, _ = w.Write([]byte(`{"data":"` + fakeUPID + `"}`))
		})
		if _, err := c.Clone(context.Background(), "pve", 204, cs.typ, 991, "lab-copy", ""); err != nil {
			t.Fatalf("%s: %v", cs.typ, err)
		}
		if q.Get(cs.wantKey) != "lab-copy" {
			t.Errorf("%s: %s = %q, want the name", cs.typ, cs.wantKey, q.Get(cs.wantKey))
		}
		if q.Has(cs.notWant) {
			t.Errorf("%s: it sent %q, which this type IGNORES — the clone would be born with no name", cs.typ, cs.notWant)
		}
		if q.Get("newid") != "991" {
			t.Errorf("%s: newid = %q", cs.typ, q.Get("newid"))
		}
		// full=1 always: a normal guest does not accept a linked clone.
		if q.Get("full") != "1" {
			t.Errorf("%s: full = %q, want 1 — a guest that is not a template only accepts a full copy", cs.typ, q.Get("full"))
		}
		if want := "/api2/json/nodes/pve/" + cs.typ + "/204/clone"; path != want {
			t.Errorf("%s: route = %q, want %q", cs.typ, path, want)
		}
	}
}

// TestCloneRejectsBeforeDialing — a destination equal to the source, an invalid
// id and a name that escapes the parameter all have to die HERE. A clone with
// the wrong destination has no cheap undo: either a guest nobody asked for is
// born, or the POST turns into another route.
func TestCloneRejectsBeforeDialing(t *testing.T) {
	cases := []struct {
		name       string
		newID      int
		targetName string
	}{
		{"destination equals the source", 204, "ok"},
		{"id zero", 0, "ok"},
		{"negative id", -1, "ok"},
		{"name with a slash", 991, "../../status/stop"},
		{"name with a space", 991, "with space"},
		{"name with a non-ASCII letter", 991, "naïve"},
		{"name starting with a hyphen", 991, "-copy"},
		{"name ending with a hyphen", 991, "copy-"},
		{"name too long", 991, strings.Repeat("a", 64)},
	}
	for _, cs := range cases {
		var dialed bool
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			dialed = true
			_, _ = w.Write([]byte(`{"data":"` + fakeUPID + `"}`))
		})
		if _, err := c.Clone(context.Background(), "pve", 204, "lxc", cs.newID, cs.targetName, ""); err == nil {
			t.Errorf("%s: it was accepted", cs.name)
		}
		if dialed {
			t.Errorf("%s: IT ACTUALLY DIALED — it has to be refused before that", cs.name)
		}
	}
}

// TestVZDumpNeitherPrunesNorNeedsExtraPrivilege — the pin that guards the most
// important decision of this route. `prune-backups` would make a command the
// operator presses to GAIN a copy end up DELETING others;
// `bwlimit`/`ionice`/`performance` would require Sys.Modify on '/'. None of them
// may show up in the query.
func TestVZDumpNeitherPrunesNorNeedsExtraPrivilege(t *testing.T) {
	var q url.Values
	var path string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		path = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":"` + fakeUPID + `"}`))
	})
	if _, err := c.VZDump(context.Background(), "pve", 204, "pbs", "snapshot", "zstd"); err != nil {
		t.Fatal(err)
	}
	if want := "/api2/json/nodes/pve/vzdump"; path != want {
		t.Errorf("route = %q, want %q", path, want)
	}
	for _, forbidden := range []string{"prune-backups", "bwlimit", "ionice", "performance", "dumpdir", "tmpdir", "script", "job-id"} {
		if q.Has(forbidden) {
			t.Errorf("the query carried %q — it either deletes someone else's backup, or demands a privilege nobody needs to have", forbidden)
		}
	}
	if q.Get("remove") != "0" {
		t.Errorf("remove = %q, want \"0\" — a manual backup deletes nothing", q.Get("remove"))
	}
	for k, want := range map[string]string{"vmid": "204", "storage": "pbs", "mode": "snapshot", "compress": "zstd"} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

// TestVZDumpRejectsOutsideAllowlist — mode, compression and storage go into the
// body of a POST to the hypervisor. A free string would give the panel the
// chance to send anything a future hypervisor version might come to accept there.
func TestVZDumpRejectsOutsideAllowlist(t *testing.T) {
	cases := []struct{ name, storage, mode, compress string }{
		{"made-up mode", "pbs", "fast", "zstd"},
		{"mode with a space", "pbs", "snapshot ", "zstd"},
		{"mode in uppercase", "pbs", "SNAPSHOT", "zstd"},
		{"made-up compression", "pbs", "snapshot", "brotli"},
		{"storage with a slash", "../../vms/100", "snapshot", "zstd"},
		{"storage with dot-dot", "a..b", "snapshot", "zstd"},
		{"empty storage", "", "snapshot", "zstd"},
		{"node with a slash", "pbs", "snapshot", "zstd"},
	}
	for i, cs := range cases {
		var dialed bool
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			dialed = true
			_, _ = w.Write([]byte(`{"data":"` + fakeUPID + `"}`))
		})
		no := "pve"
		if i == len(cases)-1 {
			no = "../../cluster"
		}
		if _, err := c.VZDump(context.Background(), no, 204, cs.storage, cs.mode, cs.compress); err == nil {
			t.Errorf("%s: it was accepted", cs.name)
		}
		if dialed {
			t.Errorf("%s: IT ACTUALLY DIALED", cs.name)
		}
	}
}

// TestNextIDReadsStringAndNumber — /cluster/nextid returns the number as a JSON
// STRING. A parser that only accepts a number returns zero in silence, and zero
// becomes "invalid vmid" further down the line, far from the cause.
func TestNextIDReadsStringAndNumber(t *testing.T) {
	for _, body := range []string{`{"data":"991"}`, `{"data":991}`} {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api2/json/cluster/nextid" {
				t.Errorf("route = %q", r.URL.Path)
			}
			_, _ = w.Write([]byte(body))
		})
		n, err := c.NextID(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if n != 991 {
			t.Errorf("%s: NextID = %d, want 991", body, n)
		}
	}
}

// 🔴 TestWaitTaskSeparatesWarningFromFailure — the distinction that the live proof of the
// clone forced into existence.
//
// The clone transferred 768 MB, created the guest and finished in `WARNINGS: 1`,
// with a single warning: "Systemd 257 detected. You may need to enable
// nesting.". Treating that as a failure would tell the operator the clone did
// not happen — and it did. Swallowing it would be worse: the warning is exactly
// what nobody else reads.
//
// The pin proves BOTH directions, because loosening this in the wrong direction
// would turn a real failure into a silent success.
func TestWaitTaskSeparatesWarningFromFailure(t *testing.T) {
	cases := []struct {
		exit        string
		wantsNotice bool
		wantErr     bool
	}{
		{"OK", false, false},
		{"WARNINGS: 1", true, false},
		{"WARNINGS: 12", true, false},
		{"unable to create CT 991 - storage full", false, true},
		{"", false, true},
		{"command 'lxc-start' failed: exit code 1", false, true},
		// 🔴 The negative control that matters: a failure that MENTIONS the word
		// cannot become a warning. Only an exitstatus that BEGINS with WARNINGS: counts.
		{"failed with WARNINGS: something", false, true},
	}
	for _, cs := range cases {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			b, _ := json.Marshal(map[string]any{"data": map[string]any{
				"status": "stopped", "exitstatus": cs.exit,
			}})
			_, _ = w.Write(b)
		})
		c.taskPoll = time.Millisecond
		err := c.WaitTask(context.Background(), "pve", fakeUPID)
		warning, hasWarning := AsTaskWarning(err)

		if hasWarning != cs.wantsNotice {
			t.Errorf("exitstatus %q: warning = %v, want %v", cs.exit, hasWarning, cs.wantsNotice)
		}
		if cs.wantsNotice {
			if warning.Exit != cs.exit {
				t.Errorf("exitstatus %q: the warning lost its text (%q) — whoever does not read it here reads it nowhere", cs.exit, warning.Exit)
			}
			continue
		}
		if cs.wantErr && err == nil {
			t.Errorf("exitstatus %q: WaitTask returned nil — a failure turned into success", cs.exit)
		}
		if !cs.wantErr && err != nil {
			t.Errorf("exitstatus %q: WaitTask returned an error (%v)", cs.exit, err)
		}
	}
}
