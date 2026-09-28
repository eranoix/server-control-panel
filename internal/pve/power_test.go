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

const fakeUPID = "UPID:pve:00001F2A:03C4D5E6:68A3B1C0:vzstart:207:panel@pve!admin:"

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

func TestPowerOpsInvalidType(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("called the hypervisor with an invalid type")
	})
	if _, err := c.Start(context.Background(), "pve", 207, "container"); err == nil {
		t.Fatal("an invalid type was accepted")
	}
}

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
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":{"status":"stopped"}}`))
		})
		c.taskPoll = time.Millisecond
		if err := c.WaitTask(context.Background(), "pve", fakeUPID); err == nil {
			t.Fatal("stopped with no exitstatus was accepted as success")
		}
	})
}

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
