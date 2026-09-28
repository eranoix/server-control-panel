package pve

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCA(t *testing.T) string {
	t.Helper()
	if b, err := os.ReadFile("/opt/panel/data/pve/pve-root-ca.pem"); err == nil && len(b) > 0 {
		p := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatalf("copying the CA: %v", err)
		}
		return p
	}
	t.Skip("the PVE CA is missing on this host and ephemeral generation is not implemented")
	return ""
}

func TestTLSPinning(t *testing.T) {
	tr, err := newTransport(testCA(t), "hypervisor.local", "198.51.100.20")
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	cfg := tr.TLSClientConfig
	if cfg == nil {
		t.Fatal("TLSClientConfig nil — the transport would fall back to the system pool")
	}
	if cfg.RootCAs == nil {
		t.Error("RootCAs nil — the PVE CA was not pinned")
	}
	if cfg.ServerName != "hypervisor.local" {
		t.Errorf("ServerName = %q, want hypervisor.local (the cert does not cover an IP)", cfg.ServerName)
	}
	if cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify on — that is the anti-pattern this file exists not to repeat")
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want >= TLS1.2", cfg.MinVersion)
	}
	if tr.DialContext == nil {
		t.Error("DialContext nil — Resolve would have no effect")
	}

	pool := x509.NewCertPool()
	pem, _ := os.ReadFile(testCA(t))
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("the CA PEM did not go into a fresh pool")
	}
	if got, want := len(cfg.RootCAs.Subjects()), len(pool.Subjects()); got != want { //nolint:staticcheck
		t.Errorf("RootCAs has %d subjects, want %d (the system pool has hundreds)", got, want)
	}
}

func TestTLSBadCA(t *testing.T) {
	dir := t.TempDir()

	if _, err := newTransport(filepath.Join(dir, "missing.pem"), "hypervisor.local", ""); err == nil {
		t.Error("a nonexistent CA file was accepted")
	}

	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("this is not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newTransport(bad, "hypervisor.local", "")
	if err == nil {
		t.Fatal("an invalid PEM was accepted — it would silently fall back to the system pool")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "pem") {
		t.Errorf("the error is not explicit enough: %v", err)
	}

	if _, err := newTransport("", "hypervisor.local", ""); err == nil {
		t.Error("an empty ca_file was accepted")
	}
}

func TestResolveDial(t *testing.T) {
	cases := []struct {
		name    string
		resolve string
		addr    string
		want    string
	}{
		{"swaps the host, keeps the port", "198.51.100.20", "hypervisor.local:8006", "198.51.100.20:8006"},
		{"other port", "198.51.100.20", "hypervisor.local:443", "198.51.100.20:443"},
		{"no resolve, passes through", "", "hypervisor.local:8006", "hypervisor.local:8006"},
		{"address without port passes through", "198.51.100.20", "hypervisor.local", "hypervisor.local"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redirectAddr(tc.addr, tc.resolve); got != tc.want {
				t.Errorf("redirectAddr(%q,%q) = %q, want %q", tc.addr, tc.resolve, got, tc.want)
			}
		})
	}

	tr := plainTransport("127.0.0.1")
	_, err := tr.DialContext(context.Background(), "tcp", "hypervisor.local:1")
	if err == nil {
		t.Fatal("expected the connection to be refused")
	}
	if !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("the dial did not go through Resolve: %v", err)
	}
}
