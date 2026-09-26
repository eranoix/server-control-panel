package pve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultTaskPoll is the interval between queries to the task status. One
// second is the same step the hypervisor's own UI uses; anything shorter only
// generates requests, because the one dictating the timing is the hypervisor.
const defaultTaskPoll = time.Second

// Snapshot is one entry from /snapshot. The hypervisor includes a pseudo-entry
// called "current" ("You are here!") that is NOT a real snapshot — whoever
// builds a screen filters it out by name, and that stays explicit instead of
// hidden inside the parser.
type Snapshot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SnapTime    int64  `json:"snaptime"`
	Parent      string `json:"parent"`
}

// Start powers the guest on. Returns the task UPID — see WaitTask.
func (c *Client) Start(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return c.statusVerb(ctx, node, vmid, typ, "start")
}

// Stop cuts the power (the equivalent of pulling the cable). Returns the UPID.
func (c *Client) Stop(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return c.statusVerb(ctx, node, vmid, typ, "stop")
}

// Shutdown asks the system inside for a graceful power off. Returns the UPID.
// It CAN end in an error (a guest that ignores ACPI) — and that is exactly why
// success is proven by the task's exitstatus, never by the POST's 200.
func (c *Client) Shutdown(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return c.statusVerb(ctx, node, vmid, typ, "shutdown")
}

func (c *Client) statusVerb(ctx context.Context, node string, vmid int, typ, verbo string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, base+"/status/"+verbo, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// SnapshotList returns the guest's snapshots. Requires VM.Audit (not
// VM.Snapshot): reading is cheaper than creating, and the discovery token
// already manages it.
func (c *Client) SnapshotList(ctx context.Context, node string, vmid int, typ string) ([]Snapshot, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return nil, err
	}
	var snaps []Snapshot
	if err := c.do(ctx, http.MethodGet, base+"/snapshot", &snaps); err != nil {
		return nil, err
	}
	return snaps, nil
}

// SnapshotCreate creates a snapshot and returns the UPID. The parameters go in
// the query string: the hypervisor accepts POST parameters both in the body and
// in the URL, and the query keeps do() as the single request builder (no route
// in this package assembles a body of its own).
func (c *Client) SnapshotCreate(ctx context.Context, node string, vmid int, typ, nome, descricao string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	if err := NomeDeSnapshotValido(nome); err != nil {
		return "", err
	}
	q := url.Values{"snapname": {nome}}
	if descricao != "" {
		q.Set("description", descricao)
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, base+"/snapshot?"+q.Encode(), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// SnapshotDelete deletes a snapshot and returns the UPID.
func (c *Client) SnapshotDelete(ctx context.Context, node string, vmid int, typ, nome string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	if err := NomeDeSnapshotValido(nome); err != nil {
		return "", err
	}
	var upid string
	if err := c.do(ctx, http.MethodDelete, base+"/snapshot/"+url.PathEscape(nome), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// SnapshotRollback returns the guest to the state of a snapshot and returns the
// UPID.
//
// 🔴 IT IS THE MOST DESTRUCTIVE OPERATION THIS PACKAGE EXPOSES, and it was
// already granted before it existed: `Qemu.pm:6301` and `LXC/Snapshot.pm:275`
// accept **VM.Snapshot** for rollback — the same privilege as creating and
// deleting, which the LabOperador role already holds. Measured against the live
// hypervisor:
//
//	GET /access/permissions with lab@pve!node-lab → {"/vms/204":{"VM.Snapshot":1,…}}
//
// In other words: the power to discard everything that happened since the
// snapshot was already there, the panel did not show it, and nobody left a
// trace when using it. Hiding it did not make it inaccessible — it only made it
// unauditable.
//
// Everything written to the guest AFTER the snapshot stops existing, and in
// this lab the pool is a SINGLE DISK, with no mirror: there is no second copy
// to take back what was lost from. That is why the handler demands typed
// confirmation and records an audit trail, and why success is only proven by
// WaitTask — the hypervisor's POST returns 200 as soon as THE TASK IS CREATED.
func (c *Client) SnapshotRollback(ctx context.Context, node string, vmid int, typ, nome string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	// The name is refused BEFORE dialling out. The same reason applies to create
	// and delete; here it counts for more: a name that escapes its own resource
	// chooses which state the guest is going to take on.
	if err := NomeDeSnapshotValido(nome); err != nil {
		return "", err
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, base+"/snapshot/"+url.PathEscape(nome)+"/rollback", &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// NomeDeSnapshotValido blocks whatever would build another path on the
// hypervisor. The hypervisor already requires [A-Za-z0-9_-] starting with a
// letter; what matters here is that no name coming from the screen can escape
// its own resource.
//
// It is EXPORTED because the handler needs to refuse the name BEFORE dialling
// out: validating only in here would force the panel to spend a connection to
// the hypervisor just to find out the screen sent garbage — and a second copy
// of the rule in internal/api would be two truths about what a valid name is,
// which is how validation rules diverge in silence.
func NomeDeSnapshotValido(nome string) error {
	if nome == "" || len(nome) > 64 {
		return fmt.Errorf("pve: invalid snapshot name (%q)", nome)
	}
	for i, r := range nome {
		ok := r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok || (i == 0 && r >= '0' && r <= '9') {
			return fmt.Errorf("pve: invalid snapshot name (%q)", nome)
		}
	}
	return nil
}

// AvisoDeTarefa is the task that FINISHED DOING what was asked, with warnings
// attached by the hypervisor. It is an error for whoever does not handle it
// (nothing changes in silence) and a success for whoever does — as long as the
// warning IS SHOWN.
type AvisoDeTarefa struct {
	UPID string
	Exit string // o exitstatus cru do PVE, ex.: "WARNINGS: 1"
}

func (a *AvisoDeTarefa) Error() string {
	return fmt.Sprintf("task %s finished with warnings (%s)", a.UPID, a.Exit)
}

// TarefaComAvisos separates "finished with a warning" from "failed". The caller
// decides what to do, but cannot confuse the two by accident.
func TarefaComAvisos(err error) (*AvisoDeTarefa, bool) {
	var a *AvisoDeTarefa
	if errors.As(err, &a) {
		return a, true
	}
	return nil, false
}

// taskStatus is the subset of /nodes/{node}/tasks/{upid}/status that is read here.
type taskStatus struct {
	Status     string `json:"status"`     // "running" | "stopped"
	ExitStatus string `json:"exitstatus"` // "OK" or the error text; only exists in stopped
	UPID       string `json:"upid"`
}

// TaskStatus reads the state of ONE task, without waiting. It is the piece the
// screen uses to show progress; whoever wants to block uses WaitTask.
func (c *Client) TaskStatus(ctx context.Context, node, upid string) (status, exitStatus string, err error) {
	if node == "" || upid == "" {
		return "", "", fmt.Errorf("pve: node (%q) and upid (%q) are required", node, upid)
	}
	var ts taskStatus
	p := fmt.Sprintf("/api2/json/nodes/%s/tasks/%s/status", node, url.PathEscape(upid))
	if err := c.do(ctx, http.MethodGet, p, &ts); err != nil {
		return "", "", err
	}
	return ts.Status, ts.ExitStatus, nil
}

// 🔴 WaitTask is the antidote to the trap: on the hypervisor, `POST
// …/status/start` answers 200 with `{"data":"UPID:…"}` as soon as THE TASK IS
// CREATED — the VM may fail to come up right afterwards. A panel that treated
// the 200 as "it powered on" would lie exactly where this code promises not to
// lie.
//
// Only the conjunction proves success:
//
//	status == "stopped"  AND  exitstatus == "OK"
//
// "stopped" on its own is not enough: an aborted task also stops. Any other
// exitstatus becomes KindHypervisor CARRYING the hypervisor's text — it is the
// only clue the operator has to the real reason.
//
// The ctx is in charge: a task that never finishes returns the context error,
// never an infinite loop (the poller depends on that so it does not hang on a
// sick guest).
func (c *Client) WaitTask(ctx context.Context, node, upid string) error {
	poll := c.taskPoll
	if poll <= 0 {
		poll = defaultTaskPoll
	}
	p := fmt.Sprintf("/api2/json/nodes/%s/tasks/%s/status", node, url.PathEscape(upid))

	tick := time.NewTimer(0)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return &Error{Kind: KindUnreachable, Path: p, Err: ctx.Err()}
		case <-tick.C:
		}

		status, exit, err := c.TaskStatus(ctx, node, upid)
		if err != nil {
			// 401/403/unreachable travel up with the Kind they came with: a token revoked
			// IN THE MIDDLE of the operation is "no credential", not "the task failed".
			return err
		}
		if status == "stopped" {
			if exit == "OK" {
				return nil
			}
			// 🔴 "WARNINGS: n" IS NOT A FAILURE — IT IS SUCCESS WITH A MESSAGE.
			//
			// Discovered by the live proof of the clone: the task transferred the 768 MB,
			// created the guest and finished in `WARNINGS: 1`, where the only warning was
			// "Systemd 257 detected. You may need to enable nesting.". Treating that as a
			// failure would tell the operator the clone did not happen — and it did
			// happen, it is there, working.
			//
			// The inverse — swallowing it — would be worse: the warning is precisely what
			// nobody else is going to read. That is why WaitTask STILL returns an error,
			// only of a type the caller is able to tell apart. Nothing changes in silence:
			// whoever does not handle it goes on seeing a failure.
			//
			// The type is its own, and not a new Kind, because the five Kinds classify the
			// HTTP CALL (which here returned 200). This classifies the TASK. They are
			// different axes, and mixing them would break the disjunction that
			// TestErrorClassification proves.
			if strings.HasPrefix(exit, "WARNINGS:") {
				return &AvisoDeTarefa{UPID: upid, Exit: exit}
			}
			if exit == "" {
				exit = "task stopped with no exitstatus"
			}
			return &Error{Kind: KindHypervisor, Path: p,
				Body: exit,
				Err:  fmt.Errorf("task %s ended in %q", upid, exit)}
		}
		tick.Reset(poll)
	}
}
