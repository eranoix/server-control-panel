package pve

import (
	"context"
	"fmt"
	"net/http"
)

type Resource struct {
	ID     string `json:"id"`
	VMID   int    `json:"vmid"`
	Name   string `json:"name"`
	Node   string `json:"node"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Uptime int64  `json:"uptime"`

	CPU    float64 `json:"cpu"`
	MaxCPU int     `json:"maxcpu"`

	Mem     int64 `json:"mem"`
	MaxMem  int64 `json:"maxmem"`
	MemHost int64 `json:"memhost"`

	Disk    int64 `json:"disk"`
	MaxDisk int64 `json:"maxdisk"`

	NetIn     int64 `json:"netin"`
	NetOut    int64 `json:"netout"`
	DiskRead  int64 `json:"diskread"`
	DiskWrite int64 `json:"diskwrite"`

	Template int `json:"template"`
}

func (r Resource) IsGuest() bool { return r.Type == "qemu" || r.Type == "lxc" }

func (c *Client) ClusterResources(ctx context.Context) ([]Resource, error) {
	var todos []Resource
	if err := c.do(ctx, http.MethodGet, "/api2/json/cluster/resources?type=vm", &todos); err != nil {
		return nil, err
	}
	guests := make([]Resource, 0, len(todos))
	for _, r := range todos {
		if r.IsGuest() {
			guests = append(guests, r)
		}
	}
	return guests, nil
}

func guestPath(node string, vmid int, typ string) (string, error) {
	if typ != "lxc" && typ != "qemu" {
		return "", fmt.Errorf("pve: guest type %q (only lxc or qemu)", typ)
	}
	if node == "" {
		return "", fmt.Errorf("pve: empty node for guest %d", vmid)
	}
	if vmid <= 0 {
		return "", fmt.Errorf("pve: invalid vmid (%d)", vmid)
	}
	return fmt.Sprintf("/api2/json/nodes/%s/%s/%d", node, typ, vmid), nil
}
