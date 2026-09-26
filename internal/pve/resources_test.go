package pve

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"testing"
)

// TestClusterResources proves discovery against the REAL fixture of both views
// (root and token). The assertion is on the SET, with a sort — never
// `len(rs) == 7`. The acceptance criterion talks about "7 guests", but the
// hypervisor returns NINE: the 7 provisioned earlier plus the panel (qemu/100)
// and the pbs (lxc/202). A magic number would turn a new guest into a failure
// and a removed guest into a wrong pass; the set says exactly WHO came in or
// went out.
func TestClusterResources(t *testing.T) {
	esperado := []int{100, 201, 202, 203, 204, 205, 206, 207, 208}

	for _, path := range []string{fixtureRoot, fixtureToken} {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api2/json/cluster/resources" {
					t.Errorf("path = %q", r.URL.Path)
				}
				// The server-side filter is asked for, but it is NOT the defence: the root's
				// view brings storage/network along and the parser has to ignore them on its
				// own.
				if got := r.URL.Query().Get("type"); got != "vm" {
					t.Errorf("query type = %q, want \"vm\"", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(raw)
			})

			rs, err := c.ClusterResources(context.Background())
			if err != nil {
				t.Fatalf("ClusterResources: %v", err)
			}

			var ids []int
			for _, r := range rs {
				if r.Type != "qemu" && r.Type != "lxc" {
					t.Errorf("a non-guest type got through the filter: %+v", r)
				}
				ids = append(ids, r.VMID)
			}
			sort.Ints(ids)
			if fmt.Sprint(ids) != fmt.Sprint(esperado) {
				t.Fatalf("set of VMIDs = %v, want %v", ids, esperado)
			}

			// Fields the inventory publishes: name, node and status have to arrive.
			for _, r := range rs {
				if r.Name == "" || r.Node == "" || r.Status == "" {
					t.Errorf("%s: required field empty (%+v)", r.ID, r)
				}
				if quer := fmt.Sprintf("%s/%d", r.Type, r.VMID); r.ID != quer {
					t.Errorf("id = %q, want %q", r.ID, quer)
				}
			}
		})
	}
}

// TestClusterResourcesVaziaNaoEErro: a hypervisor with no visible guest at all
// is a legitimate answer (a narrow ACL), not a transport failure. What it must
// NOT become is a mute error — deciding whether "empty" is suspicious is the
// freshness layer's job.
func TestClusterResourcesVazia(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	rs, err := c.ClusterResources(context.Background())
	if err != nil {
		t.Fatalf("an empty list turned into an error: %v", err)
	}
	if len(rs) != 0 {
		t.Fatalf("rs = %v, want empty", rs)
	}
}

// TestClusterResourcesPropagaKind: the hypervisor's 403 (insufficient ACL) has
// to arrive as KindForbidden, not as "no guests" — an empty inventory presented
// as the truth is the false-green this whole design forbids.
func TestClusterResourcesPropagaKind(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Permission check failed (/vms, VM.Audit)"))
	})
	_, err := c.ClusterResources(context.Background())
	pe, ok := err.(*Error)
	if !ok || pe.Kind != KindForbidden {
		t.Fatalf("error = %v (%T), want *Error KindForbidden", err, err)
	}
}

// TestGuestAddress covers the three forms measured live on this lab's PVE
// 9.2.2:
//   - LXC static ...... net0 with ip=CIDR
//   - QEMU cloud-init . ipconfig0 with ip=CIDR
//   - DHCP ............ no ip= key at all → address ABSENT, and that is NOT an error
//
// The address is an optional field of the inventory: a guest on DHCP exists,
// runs and is reachable — it just does not declare the IP in its config.
// Treating absence as a failure would make the poller mark a healthy guest as
// broken.
func TestGuestAddress(t *testing.T) {
	casos := []struct {
		nome     string
		typ      string
		vmid     int
		corpo    string
		querPath string
		quer     string
	}{
		{
			nome: "lxc estatico", typ: "lxc", vmid: 207,
			corpo:    `{"data":{"hostname":"apps","onboot":1,"ostype":"debian","net0":"name=eth0,bridge=vmbr0,gw=192.168.1.1,hwaddr=BC:24:11:82:78:82,ip=192.168.100.47/24,type=veth"}}`,
			querPath: "/api2/json/nodes/pve/lxc/207/config",
			quer:     "192.168.100.47",
		},
		{
			nome: "qemu cloud-init", typ: "qemu", vmid: 208,
			corpo:    `{"data":{"name":"dev","agent":"1","net0":"virtio=BC:24:11:51:B0:58,bridge=vmbr0","ipconfig0":"ip=192.168.100.48/24,gw=192.168.1.1"}}`,
			querPath: "/api2/json/nodes/pve/qemu/208/config",
			quer:     "192.168.100.48",
		},
		{
			nome: "lxc dhcp sem ip", typ: "lxc", vmid: 201,
			corpo:    `{"data":{"hostname":"games","net0":"name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,type=veth"}}`,
			querPath: "/api2/json/nodes/pve/lxc/201/config",
			quer:     "",
		},
		{
			nome: "qemu sem ipconfig0", typ: "qemu", vmid: 100,
			corpo:    `{"data":{"name":"painel","net0":"virtio=BC:24:11:00:00:02,bridge=vmbr0"}}`,
			querPath: "/api2/json/nodes/pve/qemu/100/config",
			quer:     "",
		},
		{
			nome: "lxc ip=dhcp literal", typ: "lxc", vmid: 203,
			corpo:    `{"data":{"hostname":"edge","net0":"name=eth0,bridge=vmbr0,ip=dhcp,type=veth"}}`,
			querPath: "/api2/json/nodes/pve/lxc/203/config",
			quer:     "",
		},
		{
			nome: "lxc sem net0", typ: "lxc", vmid: 204,
			corpo:    `{"data":{"hostname":"lab","onboot":1}}`,
			querPath: "/api2/json/nodes/pve/lxc/204/config",
			quer:     "",
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.querPath {
					t.Errorf("path = %q, want %q", r.URL.Path, tc.querPath)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.corpo))
			})
			addr, err := c.GuestAddress(context.Background(), "pve", tc.vmid, tc.typ)
			if err != nil {
				t.Fatalf("GuestAddress: error %v (a missing ip is NOT an error)", err)
			}
			if addr != tc.quer {
				t.Fatalf("addr = %q, want %q", addr, tc.quer)
			}
		})
	}
}

// TestGuestAddressTipoInvalido: "lxc" and "qemu" are the hypervisor's two
// types. A third value would build a path that does not exist and take a 501
// from the hypervisor — failing closed here is more honest than spending the
// call.
func TestGuestAddressTipoInvalido(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("called the hypervisor with an invalid type")
	})
	if _, err := c.GuestAddress(context.Background(), "pve", 207, "container"); err == nil {
		t.Fatal("an invalid type was accepted")
	}
}

// TestGuestAddressPropagaKind: a 403 on the config is "no permission", not "no
// address". Merging the two would hide a missing ACL behind an empty field.
func TestGuestAddressPropagaKind(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Permission check failed (/vms/207, VM.Audit)"))
	})
	_, err := c.GuestAddress(context.Background(), "pve", 207, "lxc")
	pe, ok := err.(*Error)
	if !ok || pe.Kind != KindForbidden {
		t.Fatalf("error = %v (%T), want *Error KindForbidden", err, err)
	}
}
