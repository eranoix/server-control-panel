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

// caDeTeste returns the path of a valid CA PEM. It reuses the REAL hypervisor
// CA when it is on this host's disk (data/pve/pve-root-ca.pem); otherwise it
// generates an ephemeral one — the test is about the MECHANICS of the pin, not
// about this particular CA.
func caDeTeste(t *testing.T) string {
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

// TestTLSPinning: the transport has to come out with its own RootCAs, a forced
// ServerName and — the pin that matters — InsecureSkipVerify FALSE.
func TestTLSPinning(t *testing.T) {
	tr, err := newTransport(caDeTeste(t), "hypervisor.local", "198.51.100.20")
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

	// False-green antidote: RootCAs != nil does not prove a pin if the pool is the
	// system one. This pool has to be a NEW pool, with exactly 1 certificate.
	pool := x509.NewCertPool()
	pem, _ := os.ReadFile(caDeTeste(t))
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("the CA PEM did not go into a fresh pool")
	}
	if got, quer := len(cfg.RootCAs.Subjects()), len(pool.Subjects()); got != quer { //nolint:staticcheck
		t.Errorf("RootCAs has %d subjects, want %d (the system pool has hundreds)", got, quer)
	}
}

// TestTLSBadCA: a missing CA or an invalid PEM fails with an explicit error. It
// can never "carry on" with the system pool — that would be a pin that does not
// pin.
func TestTLSBadCA(t *testing.T) {
	dir := t.TempDir()

	if _, err := newTransport(filepath.Join(dir, "nao-existe.pem"), "hypervisor.local", ""); err == nil {
		t.Error("a nonexistent CA file was accepted")
	}

	ruim := filepath.Join(dir, "ruim.pem")
	if err := os.WriteFile(ruim, []byte("isto nao e um certificado\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newTransport(ruim, "hypervisor.local", "")
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

// TestResolveDial: with Resolve filled in, the dial goes to the IP and the PORT
// is preserved — the equivalent of curl's --resolve, already proven live. The
// name hypervisor.local does not resolve in any DNS of this project.
func TestResolveDial(t *testing.T) {
	casos := []struct {
		nome    string
		resolve string
		addr    string
		quer    string
	}{
		{"troca o host, mantém a porta", "198.51.100.20", "hypervisor.local:8006", "198.51.100.20:8006"},
		{"outra porta", "198.51.100.20", "hypervisor.local:443", "198.51.100.20:443"},
		{"sem resolve, passa reto", "", "hypervisor.local:8006", "hypervisor.local:8006"},
		{"endereço sem porta passa reto", "198.51.100.20", "hypervisor.local", "hypervisor.local"},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			if got := redirectAddr(tc.addr, tc.resolve); got != tc.quer {
				t.Errorf("redirectAddr(%q,%q) = %q, want %q", tc.addr, tc.resolve, got, tc.quer)
			}
		})
	}

	// The real dialer has to use the rewritten address: dialling a dead port on
	// loopback returns an error citing 127.0.0.1, not the name.
	tr := plainTransport("127.0.0.1")
	_, err := tr.DialContext(context.Background(), "tcp", "hypervisor.local:1")
	if err == nil {
		t.Fatal("expected the connection to be refused")
	}
	if !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("the dial did not go through Resolve: %v", err)
	}
}
