package pve

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"testing"
)

func TestClusterResources(t *testing.T) {
	expected := []int{100, 201, 202, 203, 204, 205, 206, 207, 208}

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
			if fmt.Sprint(ids) != fmt.Sprint(expected) {
				t.Fatalf("set of VMIDs = %v, want %v", ids, expected)
			}

			for _, r := range rs {
				if r.Name == "" || r.Node == "" || r.Status == "" {
					t.Errorf("%s: required field empty (%+v)", r.ID, r)
				}
				if want := fmt.Sprintf("%s/%d", r.Type, r.VMID); r.ID != want {
					t.Errorf("id = %q, want %q", r.ID, want)
				}
			}
		})
	}
}

func TestClusterResourcesEmpty(t *testing.T) {
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

func TestClusterResourcesPropagatesKind(t *testing.T) {
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

func TestGuestAddress(t *testing.T) {
	cases := []struct {
		name     string
		typ      string
		vmid     int
		body     string
		wantPath string
		want     string
	}{
		{
			name: "lxc static", typ: "lxc", vmid: 207,
			body:     `{"data":{"hostname":"apps","onboot":1,"ostype":"debian","net0":"name=eth0,bridge=vmbr0,gw=192.168.1.1,hwaddr=BC:24:11:82:78:82,ip=192.168.100.47/24,type=veth"}}`,
			wantPath: "/api2/json/nodes/pve/lxc/207/config",
			want:     "192.168.100.47",
		},
		{
			name: "qemu cloud-init", typ: "qemu", vmid: 208,
			body:     `{"data":{"name":"dev","agent":"1","net0":"virtio=BC:24:11:51:B0:58,bridge=vmbr0","ipconfig0":"ip=192.168.100.48/24,gw=192.168.1.1"}}`,
			wantPath: "/api2/json/nodes/pve/qemu/208/config",
			want:     "192.168.100.48",
		},
		{
			name: "lxc dhcp without ip", typ: "lxc", vmid: 201,
			body:     `{"data":{"hostname":"games","net0":"name=eth0,bridge=vmbr0,hwaddr=BC:24:11:00:00:01,type=veth"}}`,
			wantPath: "/api2/json/nodes/pve/lxc/201/config",
			want:     "",
		},
		{
			name: "qemu without ipconfig0", typ: "qemu", vmid: 100,
			body:     `{"data":{"name":"panel","net0":"virtio=BC:24:11:00:00:02,bridge=vmbr0"}}`,
			wantPath: "/api2/json/nodes/pve/qemu/100/config",
			want:     "",
		},
		{
			name: "lxc ip=dhcp literal", typ: "lxc", vmid: 203,
			body:     `{"data":{"hostname":"edge","net0":"name=eth0,bridge=vmbr0,ip=dhcp,type=veth"}}`,
			wantPath: "/api2/json/nodes/pve/lxc/203/config",
			want:     "",
		},
		{
			name: "lxc without net0", typ: "lxc", vmid: 204,
			body:     `{"data":{"hostname":"lab","onboot":1}}`,
			wantPath: "/api2/json/nodes/pve/lxc/204/config",
			want:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.wantPath {
					t.Errorf("path = %q, want %q", r.URL.Path, tc.wantPath)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			})
			addr, err := c.GuestAddress(context.Background(), "pve", tc.vmid, tc.typ)
			if err != nil {
				t.Fatalf("GuestAddress: error %v (a missing ip is NOT an error)", err)
			}
			if addr != tc.want {
				t.Fatalf("addr = %q, want %q", addr, tc.want)
			}
		})
	}
}

func TestGuestAddressInvalidType(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("called the hypervisor with an invalid type")
	})
	if _, err := c.GuestAddress(context.Background(), "pve", 207, "container"); err == nil {
		t.Fatal("an invalid type was accepted")
	}
}

func TestGuestAddressPropagatesKind(t *testing.T) {
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
