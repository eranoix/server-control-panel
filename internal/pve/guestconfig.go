package pve

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// GuestAddress returns the IP declared in the guest's configuration, or "" when the
// guest declares none.
//
// Measured live on this hypervisor:
//
//	GET /nodes/pve/lxc/207/config  → net0: "…,ip=192.168.100.47/24,…"
//	GET /nodes/pve/qemu/208/config → ipconfig0: "ip=192.168.100.48/24,gw=…"
//
// LXC keeps the address in net0 itself; QEMU keeps it in ipconfig0 (cloud-init),
// because QEMU's net0 only describes the virtual hardware. Both in CIDR.
//
// 🔴 Absence is NOT an error. A guest on DHCP has no ip= key (or has the literal
// "ip=dhcp"), and it still exists, runs and is reachable — it just does not declare its
// address. The inventory treats Address as an OPTIONAL field; treating
// absence as a failure would make the poller mark a healthy guest as broken.
// What is still an error is 401/403/unreachable, which come up typed.
func (c *Client) GuestAddress(ctx context.Context, node string, vmid int, typ string) (string, error) {
	base, err := guestPath(node, vmid, typ)
	if err != nil {
		return "", err
	}
	// Only the two network keys matter; the rest of the config (disk, memory,
	// ostype) is not the inventory's business.
	var cfg struct {
		Net0      string `json:"net0"`
		IPConfig0 string `json:"ipconfig0"`
	}
	if err := c.do(ctx, http.MethodGet, base+"/config", &cfg); err != nil {
		return "", err
	}
	if typ == "qemu" {
		return ipDeConfigDeRede(cfg.IPConfig0), nil
	}
	return ipDeConfigDeRede(cfg.Net0), nil
}

// ipDeConfigDeRede extracts the value of "ip=" from a hypervisor network config
// line, which is a comma-separated list of pairs
// ("name=eth0,bridge=vmbr0,ip=…/24"). It returns the address alone, without the
// CIDR prefix.
//
// It returns "" — and not an error — for: an empty line, no ip= key, ip=dhcp,
// ip=auto, or a value that is not an IP. All of that is "address not declared".
func ipDeConfigDeRede(linha string) string {
	for _, campo := range strings.Split(linha, ",") {
		chave, valor, ok := strings.Cut(strings.TrimSpace(campo), "=")
		if !ok || chave != "ip" {
			continue
		}
		// The hypervisor accepts "dhcp"/"auto" in place of the CIDR — those are
		// declarations of absence, not addresses.
		if addr, _, err := net.ParseCIDR(valor); err == nil {
			return addr.String()
		}
		if ip := net.ParseIP(valor); ip != nil {
			return ip.String()
		}
		return ""
	}
	return ""
}
