package pve

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) Reboot(ctx context.Context, node string, vmid int, typ string) (string, error) {
	return c.statusVerb(ctx, node, vmid, typ, "reboot")
}

func (c *Client) NextID(ctx context.Context) (int, error) {
	var raw any
	if err := c.do(ctx, http.MethodGet, "/api2/json/cluster/nextid", &raw); err != nil {
		return 0, err
	}
	switch v := raw.(type) {
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("pve: /cluster/nextid returned %q, which is not a number", v)
		}
		return n, nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("pve: /cluster/nextid returned %T, unexpected", raw)
	}
}

func ValidGuestName(name string) error {
	if name == "" || len(name) > 63 {
		return fmt.Errorf("pve: invalid guest name (%q) — 1 to 63 characters", name)
	}
	for i, r := range name {
		ok := r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("pve: invalid guest name (%q) — only letters, digits and hyphen", name)
		}
		if r == '-' && (i == 0 || i == len(name)-1) {
			return fmt.Errorf("pve: invalid guest name (%q) — cannot start or end with a hyphen", name)
		}
	}
	return nil
}

func ValidStorageName(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("pve: invalid storage (%q)", name)
	}
	for _, r := range name {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("pve: invalid storage (%q)", name)
		}
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("pve: invalid storage (%q)", name)
	}
	return nil
}

func ValidNodeName(node string) error {
	if node == "" || len(node) > 64 {
		return fmt.Errorf("pve: invalid node (%q)", node)
	}
	for _, r := range node {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("pve: invalid node (%q)", node)
		}
	}
	if strings.Contains(node, "..") {
		return fmt.Errorf("pve: invalid node (%q)", node)
	}
	return nil
}

func (c *Client) Clone(ctx context.Context, node string, vmid int, typ string, newID int, name, snapname string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	if newID <= 0 {
		return "", fmt.Errorf("pve: invalid target vmid (%d)", newID)
	}
	if newID == vmid {
		return "", fmt.Errorf("pve: target %d is the source guest itself", newID)
	}
	q := url.Values{
		"newid": {strconv.Itoa(newID)},
		"full":  {"1"},
	}
	if snapname != "" {
		if err := ValidSnapshotName(snapname); err != nil {
			return "", err
		}
		q.Set("snapname", snapname)
	}
	if name != "" {
		if err := ValidGuestName(name); err != nil {
			return "", err
		}
		if typ == "lxc" {
			q.Set("hostname", name)
		} else {
			q.Set("name", name)
		}
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, base+"/clone?"+q.Encode(), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

var DumpModes = []string{"snapshot", "suspend", "stop"}

var DumpCompressions = []string{"zstd", "lzo", "gzip", "0"}

func inList(v string, list []string) bool {
	for _, x := range list {
		if v == x {
			return true
		}
	}
	return false
}

func (c *Client) VZDump(ctx context.Context, node string, vmid int, storage, mode, compress string) (string, error) {
	if node == "" {
		return "", fmt.Errorf("pve: empty node")
	}
	if vmid <= 0 {
		return "", fmt.Errorf("pve: invalid vmid (%d)", vmid)
	}
	if err := ValidStorageName(storage); err != nil {
		return "", err
	}
	if !inList(mode, DumpModes) {
		return "", fmt.Errorf("pve: invalid dump mode (%q) — %s", mode, strings.Join(DumpModes, "|"))
	}
	if !inList(compress, DumpCompressions) {
		return "", fmt.Errorf("pve: invalid compression (%q) — %s", compress, strings.Join(DumpCompressions, "|"))
	}
	if err := ValidNodeName(node); err != nil {
		return "", err
	}
	q := url.Values{
		"vmid":     {strconv.Itoa(vmid)},
		"storage":  {storage},
		"mode":     {mode},
		"compress": {compress},
		"remove":   {"0"},
	}
	var upid string
	if err := c.do(ctx, http.MethodPost, "/api2/json/nodes/"+node+"/vzdump?"+q.Encode(), &upid); err != nil {
		return "", err
	}
	return upid, nil
}

func (c *Client) Description(ctx context.Context, node string, vmid int, typ string) (string, error) {
	var path string
	if vmid > 0 {
		base, err := guestPath(node, vmid, typ)
		if err != nil {
			return "", err
		}
		path = base + "/config"
	} else {
		if err := ValidNodeName(node); err != nil {
			return "", err
		}
		path = "/api2/json/nodes/" + node + "/config"
	}
	var cfg struct {
		Description string `json:"description"`
	}
	if err := c.do(ctx, http.MethodGet, path, &cfg); err != nil {
		return "", err
	}
	return cfg.Description, nil
}

const MaxNoteSize = 8192

func (c *Client) SetDescription(ctx context.Context, node string, vmid int, typ, text string) error {
	if len(text) > MaxNoteSize {
		return fmt.Errorf("pve: note with %d bytes — the cap is %d", len(text), MaxNoteSize)
	}
	var path string
	if vmid > 0 {
		base, err := guestPath(node, vmid, typ)
		if err != nil {
			return err
		}
		path = base + "/config"
	} else {
		if err := ValidNodeName(node); err != nil {
			return err
		}
		path = "/api2/json/nodes/" + node + "/config"
	}
	q := url.Values{"description": {text}}
	return c.do(ctx, http.MethodPut, path+"?"+q.Encode(), nil)
}
