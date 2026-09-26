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

const upidFalso = "UPID:pve:00001F2A:03C4D5E6:68A3B1C0:vzstart:207:lab@pve!admin:"

// TestPowerOps proves the exact path per type (lxc vs qemu) and the verb, and
// that the function returns the RAW UPID. The UPID is not decoration: it is the
// task's identifier in the hypervisor's own log and the only key for finding
// out whether it finished well.
func TestPowerOps(t *testing.T) {
	casos := []struct {
		nome     string
		chamar   func(*Client) (string, error)
		querPath string
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
			return c.SnapshotCreate(context.Background(), "pve", 207, "lxc", "antes-do-cutover", "Fase 7")
		}, "/api2/json/nodes/pve/lxc/207/snapshot"},
		{"snapshot delete qemu", func(c *Client) (string, error) {
			return c.SnapshotDelete(context.Background(), "pve", 208, "qemu", "antes-do-cutover")
		}, "/api2/json/nodes/pve/qemu/208/snapshot/antes-do-cutover"},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.querPath {
					t.Errorf("path = %q, want %q", r.URL.Path, tc.querPath)
				}
				querMetodo := http.MethodPost
				if strings.HasPrefix(tc.nome, "snapshot delete") {
					querMetodo = http.MethodDelete
				}
				if r.Method != querMetodo {
					t.Errorf("method = %s, want %s", r.Method, querMetodo)
				}
				_, _ = w.Write([]byte(`{"data":"` + upidFalso + `"}`))
			})
			upid, err := tc.chamar(c)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if upid != upidFalso {
				t.Fatalf("upid = %q, want %q (raw, with no rewriting)", upid, upidFalso)
			}
		})
	}
}

// TestPowerOpsTipoInvalido: fails closed before spending a call.
func TestPowerOpsTipoInvalido(t *testing.T) {
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
			{"name":"antes-do-cutover","description":"Fase 7","snaptime":1787000000},
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
	if snaps[0].Name != "antes-do-cutover" || snaps[0].SnapTime != 1787000000 {
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
			if quer := "/api2/json/nodes/pve/tasks/" + upidFalso + "/status"; r.URL.Path != quer {
				t.Errorf("path = %q, want %q", r.URL.Path, quer)
			}
			if atomic.AddInt32(&n, 1) < 3 {
				_, _ = w.Write([]byte(`{"data":{"status":"running","upid":"` + upidFalso + `"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK","upid":"` + upidFalso + `"}}`))
		})
		c.taskPoll = time.Millisecond
		if err := c.WaitTask(context.Background(), "pve", upidFalso); err != nil {
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
		err := c.WaitTask(context.Background(), "pve", upidFalso)
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
		if err := c.WaitTask(context.Background(), "pve", upidFalso); err == nil {
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
	const prazo = 40 * time.Millisecond
	c.taskPoll = poll

	ctx, cancel := context.WithTimeout(context.Background(), prazo)
	defer cancel()

	feito := make(chan error, 1)
	inicio := time.Now()
	go func() { feito <- c.WaitTask(ctx, "pve", upidFalso) }()

	select {
	case err := <-feito:
		levou := time.Since(inicio)
		if err == nil {
			t.Fatal("an expired ctx returned success")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want it to wrap context.DeadlineExceeded", err)
		}
		if levou >= poll {
			t.Fatalf("WaitTask took %v to give up with a deadline of %v — it slept the whole tick (%v) instead of waking on the ctx", levou, prazo, poll)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WaitTask did not respect the ctx — infinite loop")
	}
}

// TestWaitTaskPropagaKind: a 401 during the wait is "no credential" (a token
// revoked IN THE MIDDLE of the operation — exactly the revocation drill), not
// "the task failed".
func TestWaitTaskPropagaKind(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("authentication failure"))
	})
	c.taskPoll = time.Millisecond
	err := c.WaitTask(context.Background(), "pve", upidFalso)
	pe, ok := err.(*Error)
	if !ok || pe.Kind != KindNoCredential {
		t.Fatalf("error = %v (%T), want *Error KindNoCredential", err, err)
	}
}

// 🔴 TestSnapshotNomeInvalido: the snapshot name comes from the SCREEN and goes
// into a path on the hypervisor. Without validation, "../../status/stop" would
// turn into another route — and the wrong operation on a write path is the
// worst class of defect there is here. Fails closed: the hypervisor is not even
// called.
func TestSnapshotNomeInvalido(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("called the hypervisor with an invalid name: %s %s", r.Method, r.URL.Path)
	})
	nomes := []string{
		"",
		"../../status/stop",
		"com/barra",
		"com espaço",
		"acento-é",
		"1comeca-com-digito",
		strings.Repeat("x", 65),
	}
	for _, nome := range nomes {
		t.Run(nome, func(t *testing.T) {
			if _, err := c.SnapshotCreate(context.Background(), "pve", 207, "lxc", nome, ""); err == nil {
				t.Errorf("SnapshotCreate(%q) was accepted", nome)
			}
			if _, err := c.SnapshotDelete(context.Background(), "pve", 207, "lxc", nome); err == nil {
				t.Errorf("SnapshotDelete(%q) was accepted", nome)
			}
		})
	}
	// And the contrapositive: the legitimate name used by the drill HAS to pass —
	// otherwise the validation would be "reject everything", which is also a false
	// green.
	var chamou bool
	c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		chamou = true
		_, _ = w.Write([]byte(`{"data":"` + upidFalso + `"}`))
	})
	if _, err := c2.SnapshotCreate(context.Background(), "pve", 207, "lxc", "antes-do-cutover_v2", "ok"); err != nil {
		t.Fatalf("a legitimate name was refused: %v", err)
	}
	if !chamou {
		t.Fatal("a legitimate name did not reach the hypervisor")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Snapshot rollback
//
// 🔴 The privilege WAS ALREADY GRANTED and nobody saw it. The LabOperador role
// has VM.Snapshot, and `Qemu.pm:6301` / `LXC/Snapshot.pm:275` accept VM.Snapshot
// for ROLLBACK — not only for creating and deleting. Measured against the live
// hypervisor:
//
//	GET /access/permissions with lab@pve!node-lab → {"/vms/204":{"VM.Snapshot":1,…}}
//
// In other words: the power to discard everything since a snapshot was there,
// the panel did not show it, and no screen left a trace of whoever used it.
// Exposing it with an audit trail is safer than leaving it hidden — what is
// hidden stays reachable by whoever holds the token, and with no record at all.
// ─────────────────────────────────────────────────────────────────────────────

// TestSnapshotRollbackPathEVerbo pins the exact path on both guest types. A
// wrong path here does not return 404: it returns the rollback of the WRONG
// resource.
func TestSnapshotRollbackPathEVerbo(t *testing.T) {
	casos := []struct {
		nome     string
		chamar   func(*Client) (string, error)
		querPath string
	}{
		{"lxc", func(c *Client) (string, error) {
			return c.SnapshotRollback(context.Background(), "pve", 204, "lxc", "antes-do-cutover")
		}, "/api2/json/nodes/pve/lxc/204/snapshot/antes-do-cutover/rollback"},
		{"qemu", func(c *Client) (string, error) {
			return c.SnapshotRollback(context.Background(), "pve", 208, "qemu", "antes-do-cutover")
		}, "/api2/json/nodes/pve/qemu/208/snapshot/antes-do-cutover/rollback"},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			var vistoPath, vistoMetodo string
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				vistoPath, vistoMetodo = r.URL.Path, r.Method
				_, _ = w.Write([]byte(`{"data":"` + upidFalso + `"}`))
			})
			upid, err := tc.chamar(c)
			if err != nil {
				t.Fatalf("SnapshotRollback: %v", err)
			}
			if vistoPath != tc.querPath {
				t.Errorf("path = %q, want %q", vistoPath, tc.querPath)
			}
			if vistoMetodo != http.MethodPost {
				t.Errorf("method = %q, want POST", vistoMetodo)
			}
			if upid != upidFalso {
				t.Errorf("upid = %q, want %q — without it there is neither WaitTask nor trail", upid, upidFalso)
			}
		})
	}
}

// 🔴 TestSnapshotRollbackRecusaNomeInvalido: the name comes from the SCREEN and
// goes into the path of a DESTRUCTIVE operation. It is the same rule as
// create/delete, and here it counts for more: a name that escapes its resource
// chooses which state the guest is going to take on.
func TestSnapshotRollbackRecusaNomeInvalido(t *testing.T) {
	for _, nome := range []string{"", "../../nodes/pve/qemu/100/status/stop", "com espaço", "acentuação", "9comeca-com-numero", strings.Repeat("a", 65)} {
		var discou bool
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			discou = true
			_, _ = w.Write([]byte(`{"data":"` + upidFalso + `"}`))
		})
		if _, err := c.SnapshotRollback(context.Background(), "pve", 204, "lxc", nome); err == nil {
			t.Errorf("SnapshotRollback(%q) was accepted", nome)
		}
		if discou {
			t.Errorf("SnapshotRollback(%q) actually dialed — the name has to be refused BEFORE that", nome)
		}
	}
}

// TestSnapshotRollbackSemPrivilegioETipado: a 403 becomes KindForbidden, so the
// screen says "no permission" instead of "it failed".
func TestSnapshotRollbackSemPrivilegioETipado(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	_, err := c.SnapshotRollback(context.Background(), "pve", 204, "lxc", "antes-do-cutover")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindForbidden {
		t.Fatalf("error = %v, want KindForbidden", err)
	}
}
