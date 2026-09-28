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

const defaultTaskPoll = time.Second

type Snapshot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SnapTime    int64  `json:"snaptime"`
	Parent      string `json:"parent"`
}

func (c *Client) Start(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return c.statusVerb(ctx, node, vmid, typ, "start")
}

func (c *Client) Stop(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return c.statusVerb(ctx, node, vmid, typ, "stop")
}

func (c *Client) Shutdown(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return c.statusVerb(ctx, node, vmid, typ, "shutdown")
}

func (c *Client) statusVerb(ctx context.Context, node string, vmid int, typ, verb string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, base+"/status/"+verb, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

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

func (c *Client) SnapshotCreate(ctx context.Context, node string, vmid int, typ, name, description string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	if err := ValidSnapshotName(name); err != nil {
		return "", err
	}
	q := url.Values{"snapname": {name}}
	if description != "" {
		q.Set("description", description)
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, base+"/snapshot?"+q.Encode(), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

func (c *Client) SnapshotDelete(ctx context.Context, node string, vmid int, typ, name string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	if err := ValidSnapshotName(name); err != nil {
		return "", err
	}
	var upid string
	if err := c.do(ctx, http.MethodDelete, base+"/snapshot/"+url.PathEscape(name), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

func (c *Client) SnapshotRollback(ctx context.Context, node string, vmid int, typ, name string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	if err := ValidSnapshotName(name); err != nil {
		return "", err
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, base+"/snapshot/"+url.PathEscape(name)+"/rollback", &upid); err != nil {
		return "", err
	}
	return upid, nil
}

func ValidSnapshotName(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("pve: invalid snapshot name (%q)", name)
	}
	for i, r := range name {
		ok := r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok || (i == 0 && r >= '0' && r <= '9') {
			return fmt.Errorf("pve: invalid snapshot name (%q)", name)
		}
	}
	return nil
}

type TaskWarning struct {
	UPID string
	Exit string
}

func (a *TaskWarning) Error() string {
	return fmt.Sprintf("task %s finished with warnings (%s)", a.UPID, a.Exit)
}

func AsTaskWarning(err error) (*TaskWarning, bool) {
	var a *TaskWarning
	if errors.As(err, &a) {
		return a, true
	}
	return nil, false
}

type taskStatus struct {
	Status     string `json:"status"`
	ExitStatus string `json:"exitstatus"`
	UPID       string `json:"upid"`
}

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
			return err
		}
		if status == "stopped" {
			if exit == "OK" {
				return nil
			}
			if strings.HasPrefix(exit, "WARNINGS:") {
				return &TaskWarning{UPID: upid, Exit: exit}
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
