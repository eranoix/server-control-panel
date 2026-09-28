package pve

import (
	"context"
	"net"
	"net/http"
	"strings"
)

func (c *Client) GuestAddress(ctx context.Context, node string, vmid int, typ string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Net0      string `json:"net0"`
		IPConfig0 string `json:"ipconfig0"`
	}
	if err := c.do(ctx, http.MethodGet, base+"/config", &cfg); err != nil {
		return "", err
	}
	if typ == "qemu" {
		return ipFromNetConfig(cfg.IPConfig0), nil
	}
	return ipFromNetConfig(cfg.Net0), nil
}

func ipFromNetConfig(line string) string {
	for _, field := range strings.Split(line, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok || key != "ip" {
			continue
		}
		if addr, _, err := net.ParseCIDR(value); err == nil {
			return addr.String()
		}
		if ip := net.ParseIP(value); ip != nil {
			return ip.String()
		}
		return ""
	}
	return ""
}
