package pve

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fakeUPID = "UPID:pve:00001F2A:03C4D5E6:68A3B1C0:vzstart:207:lab@pve!admin:"

// TestPowerOps proves the exact path per type (lxc vs qemu) and the verb, and
// that the function returns the RAW UPID. The UPID is not decoration: it is the
// task's identifier in the hypervisor's own log and the only key for finding
// out whether it finished well.
func TestPowerOps(t *testing.T) {
	cases := []struct {
		name     string
		call     func(*Client) (string, error)
		wantPath string
	}{
		{"start lxc", func(c *Client) (string, error) {
			return c.Start(context.Background(), "pve", 207, "lxc")
		}, "/api2/json/nodes/pve/lxc/207/status/start"},
		{"stop lxc", func(c *Client) (string, error) {
			return c.Stop(context.Background(), "pve", 207, "lxc")
		}, "/api2/json/nodes/pve/lxc/207/status/stop"},
		{"shutdown qemu", func(c *Client) (string, error) {
			return c.Shutdown(context.Background(), "pve", 208, "qemu")
		}, "/api2/json/nodes/pve/qemu/208/status/shutdown"},
		{"start qemu", func(c *Client) (string, error) {
			return c.Start(context.Background(), "pve", 100, "qemu")
		}, "/api2/json/nodes/pve/qemu/100/status/start"},
		{"snapshot create lxc", func(c *Client) (string, error) {
			return c.SnapshotCreate(context.Background(), "pve", 207, "lxc", "before-cutover", "Phase 7")
		}, "/api2/json/nodes/pve/lxc/207/snapshot"},
		{"snapshot delete qemu", func(c *Client) (string, error) {
			return c.SnapshotDelete(context.Background(), "pve", 208, "qemu", "before-cutover")
		}, "/api2/json/nodes/pve/qemu/208/snapshot/before-cutover"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.wantPath {
					t.Errorf("path = %q, want %q", r.URL.Path, tc.wantPath)
				}
				wantMethod := http.MethodPost
				if strings.HasPrefix(tc.name, "snapshot delete") {
					wantMethod = http.MethodDelete
				}
				if r.Method != wantMethod {
					t.Errorf("method = %s, want %s", r.Method, wantMethod)
				}
				_, _ = w.Write([]byte(`{"data":"` + fakeUPID + `"}`))
			})
			upid, err := tc.call(c)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if upid != fakeUPID {
				t.Fatalf("upid = %q, want %q (raw, with no rewriting)", upid, fakeUPID)
			}
		})
	}
}

// TestPowerOpsInvalidType: fails closed before spending a call.
func TestPowerOpsInvalidType(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("called the hypervisor with an invalid type")
	})
	if _, err := c.Start(context.Background(), "pve", 207, "container"); err == nil {
		t.Fatal("an invalid type was accepted")
	}
}

// TestSnapshotList proves the listing arrives with a name and a time — it is
// what the screen shows before offering "delete".
func TestSnapshotList(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/nodes/pve/lxc/207/snapshot" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":[
			{"name":"before-cutover","description":"Phase 7","snaptime":1787000000},
			{"name":"current","description":"You are here!","digest":"abc"}
		]}`))
	})
	snaps, err := c.SnapshotList(context.Background(), "pve", 207, "lxc")
	if err != nil {
		t.Fatalf("SnapshotList: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snaps = %+v, want 2", snaps)
	}
	if snaps[0].Name != "before-cutover" || snaps[0].SnapTime != 1787000000 {
		t.Fatalf("snapshot[0] = %+v", snaps[0])
	}
}

// 🔴 TestWaitTask is the antidote to the trap. The POST returns 200 with a UPID
// and the task CAN FAIL AFTERWARDS. Only two conditions together prove success:
// status=="stopped" AND exitstatus=="OK". Accepting the POST's 200, or
// accepting "stopped" on its own, is pure false-green — the screen would say
// "it powered on" for a VM that did not power on.
func TestWaitTask(t *testing.T) {
	t.Run("running until stopped OK", func(t *testing.T) {
		var n int32
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if want := "/api2/json/nodes/pve/tasks/" + fakeUPID + "/status"; r.URL.Path != want {
				t.Errorf("path = %q, want %q", r.URL.Path, want)
			}
			if atomic.AddInt32(&n, 1) < 3 {
				_, _ = w.Write([]byte(`{"data":{"status":"running","upid":"` + fakeUPID + `"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK","upid":"` + fakeUPID + `"}}`))
		})
		c.taskPoll = time.Millisecond
		if err := c.WaitTask(context.Background(), "pve", fakeUPID); err != nil {
			t.Fatalf("WaitTask: %v", err)
		}
		if n < 3 {
			t.Fatalf("polled %d times — it did not wait for the task to stop", n)
		}
	})

	t.Run("stopped with an error exitstatus fails", func(t *testing.T) {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"command 'lxc-start' failed with exit code 1"}}`))
		})
		c.taskPoll = time.Millisecond
		err := c.WaitTask(context.Background(), "pve", fakeUPID)
		if err == nil {
			t.Fatal("a task that ended in error was accepted as success (Trap 6)")
		}
		pe, ok := err.(*Error)
		if !ok || pe.Kind != KindHypervisor {
			t.Fatalf("error = %v (%T), want *Error KindHypervisor", err, err)
		}
		if !strings.Contains(pe.Error(), "lxc-start") {
			t.Fatalf("the error hides the PVE's exitstatus: %q", pe.Error())
		}
	})

	t.Run("stopped with no exitstatus fails", func(t *testing.T) {
		// "stopped" on its own is NOT success: an aborted task can stop with no
		// exitstatus at all. Accepting that would be the same false-green in different
		// clothes.
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"status":"stopped"}}`))
		})
		c.taskPoll = time.Millisecond
		if err := c.WaitTask(context.Background(), "pve", fakeUPID); err == nil {
			t.Fatal("stopped with no exitstatus was accepted as success")
		}
	})
}

// TestWaitTaskTimeout: a task that never stops has to respect the ctx. Without
// that, a poll loop would hang forever on a sick guest.
//
// 🔴 The interval between queries is LONG on purpose (2 s) and the deadline is
// short (40 ms). Only that way does the assertion tell the two implementations
// apart: with the `case <-ctx.Done()` guard in the loop, WaitTask comes back in
// ~40 ms; WITHOUT it, it stays stuck in the timer until the next tick and only
// notices the expiry 2 s later. The first version of this test used a taskPoll
// of 1 ms and passed either way — the request's own ctx masked the missing
// guard. Measuring the TIME it takes to return is what gives this pin teeth.
func TestWaitTaskTimeout(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"status":"running"}}`))
	})
	const poll = 2 * time.Second
	const deadline = 40 * time.Millisecond
	c.taskPoll = poll

	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- c.WaitTask(ctx, "pve", fakeUPID) }()

	select {
	case err := <-done:
		took := time.Since(start)
		if err == nil {
			t.Fatal("an expired ctx returned success")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want it to wrap context.DeadlineExceeded", err)
		}
		if took >= poll {
			t.Fatalf("WaitTask took %v to give up with a deadline of %v — it slept the whole tick (%v) instead of waking on the ctx", took, deadline, poll)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WaitTask did not respect the ctx — infinite loop")
	}
}

// TestWaitTaskPropagatesKind: a 401 during the wait is "no credential" (a token
// revoked IN THE MIDDLE of the operation — exactly the revocation drill), not
// "the task failed".
func TestWaitTaskPropagatesKind(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("authentication failure"))
	})
	c.taskPoll = time.Millisecond
	err := c.WaitTask(context.Background(), "pve", fakeUPID)
	pe, ok := err.(*Error)
	if !ok || pe.Kind != KindNoCredential {
		t.Fatalf("error = %v (%T), want *Error KindNoCredential", err, err)
	}
}

// 🔴 TestSnapshotInvalidName: the snapshot name comes from the SCREEN and goes
// into a path on the hypervisor. Without validation, "../../status/stop" would
// turn into another route — and the wrong operation on a write path is the
// worst class of defect there is here. Fails closed: the hypervisor is not even
// called.
func TestSnapshotInvalidName(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("called the hypervisor with an invalid name: %s %s", r.Method, r.URL.Path)
	})
	names := []string{
		"",
		"../../status/stop",
		"with/slash",
		"with space",
		"accent-ñ",
		"1starts-with-digit",
		strings.Repeat("x", 65),
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if _, err := c.SnapshotCreate(context.Background(), "pve", 207, "lxc", name, ""); err == nil {
				t.Errorf("SnapshotCreate(%q) was accepted", name)
			}
			if _, err := c.SnapshotDelete(context.Background(), "pve", 207, "lxc", name); err == nil {
				t.Errorf("SnapshotDelete(%q) was accepted", name)
			}
		})
	}
	// And the contrapositive: the legitimate name used by the drill HAS to pass —
	// otherwise the validation would be "reject everything", which is also a false
	// green.
	var called bool
	c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"data":"` + fakeUPID + `"}`))
	})
	if _, err := c2.SnapshotCreate(context.Background(), "pve", 207, "lxc", "before-cutover_v2", "ok"); err != nil {
		t.Fatalf("a legitimate name was refused: %v", err)
	}
	if !called {
		t.Fatal("a legitimate name did not reach the hypervisor")
	}
}

// Snapshot rollback
//
// VM.Snapshot also authorises ROLLBACK (`Qemu.pm:6301`, `LXC/Snapshot.pm:275`),
// not only create and delete, so the operator role already had this power.
// Exposing it with an audit trail is safer than leaving it hidden — what is
// hidden stays reachable by whoever holds the token, and with no record at all.

// TestSnapshotRollbackPathAndVerb pins the exact path on both guest types. A
// wrong path here does not return 404: it returns the rollback of the WRONG
// resource.
func TestSnapshotRollbackPathAndVerb(t *testing.T) {
	cases := []struct {
		name     string
		call     func(*Client) (string, error)
		wantPath string
	}{
		{"lxc", func(c *Client) (string, error) {
			return c.SnapshotRollback(context.Background(), "pve", 204, "lxc", "before-cutover")
		}, "/api2/json/nodes/pve/lxc/204/snapshot/before-cutover/rollback"},
		{"qemu", func(c *Client) (string, error) {
			return c.SnapshotRollback(context.Background(), "pve", 208, "qemu", "before-cutover")
		}, "/api2/json/nodes/pve/qemu/208/snapshot/before-cutover/rollback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seenPath, seenMethod string
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				seenPath, seenMethod = r.URL.Path, r.Method
				_, _ = w.Write([]byte(`{"data":"` + fakeUPID + `"}`))
			})
			upid, err := tc.call(c)
			if err != nil {
				t.Fatalf("SnapshotRollback: %v", err)
			}
			if seenPath != tc.wantPath {
				t.Errorf("path = %q, want %q", seenPath, tc.wantPath)
			}
			if seenMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", seenMethod)
			}
			if upid != fakeUPID {
				t.Errorf("upid = %q, want %q — without it there is neither WaitTask nor trail", upid, fakeUPID)
			}
		})
	}
}

// 🔴 TestSnapshotRollbackRejectsInvalidName: the name comes from the SCREEN and
// goes into the path of a DESTRUCTIVE operation. It is the same rule as
// create/delete, and here it counts for more: a name that escapes its resource
// chooses which state the guest is going to take on.
func TestSnapshotRollbackRejectsInvalidName(t *testing.T) {
	for _, name := range []string{"", "../../nodes/pve/qemu/100/status/stop", "with space", "accentuation-ñ", "9starts-with-number", strings.Repeat("a", 65)} {
		var dialed bool
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			dialed = true
			_, _ = w.Write([]byte(`{"data":"` + fakeUPID + `"}`))
		})
		if _, err := c.SnapshotRollback(context.Background(), "pve", 204, "lxc", name); err == nil {
			t.Errorf("SnapshotRollback(%q) was accepted", name)
		}
		if dialed {
			t.Errorf("SnapshotRollback(%q) actually dialed — the name has to be refused BEFORE that", name)
		}
	}
}

// TestSnapshotRollbackWithoutPrivilegeIsTyped: a 403 becomes KindForbidden, so the
// screen says "no permission" instead of "it failed".
func TestSnapshotRollbackWithoutPrivilegeIsTyped(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	_, err := c.SnapshotRollback(context.Background(), "pve", 204, "lxc", "before-cutover")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindForbidden {
		t.Fatalf("error = %v, want KindForbidden", err)
	}
}
