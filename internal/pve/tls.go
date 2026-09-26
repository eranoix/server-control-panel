package pve

// TLS pinning against the hypervisor's own CA.
//
// A NEW PRECEDENT IN THIS REPO: until now there was no x509.NewCertPool and no
// RootCAs anywhere in internal/ — the only client TLS configuration is
// internal/queue/runners_security.go:90, which TURNS verification OFF on
// purpose (it has to read an invalid certificate in order to judge its
// validity). That is the anti-pattern this file exists in order not to repeat:
// here the hypervisor's certificate IS verified, and verified against a single
// anchor. The field that turns verification off does not appear in this file,
// not by accident and not in a comment — its absence is an invariant of the
// repo, checked by grep.
//
// Why pin instead of trusting the system pool: the hypervisor's certificate is
// issued by the cluster's own CA (CN=Proxmox Virtual Environment), which is in
// no pool at all. And its SAN lists IP:192.168.1.10 — an obsolete address —
// while the host lives at 192.168.100.50 (LAN) and 198.51.100.20 (tailnet).
// That is why the dial goes to the IP (Resolve) while the verification stays
// against the NAME (ServerName), exactly as the `curl --resolve --cacert`
// already proved live.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// dialTimeout bounds only the establishment of the connection; the ceiling
	// for the whole call is the http.Client's Timeout (a 10 s floor, see
	// client.go).
	dialTimeout = 5 * time.Second
	// tlsHandshakeTimeout: the handshake measured against this hypervisor takes
	// ~51 ms.
	tlsHandshakeTimeout = 5 * time.Second
)

// newTransport assembles the pinned transport. caFile is mandatory: without it
// the client would fall back to the system pool, which does not know the
// hypervisor's CA — a silent failure today, no verification at all tomorrow.
func newTransport(caFile, serverName, resolve string) (*http.Transport, error) {
	caFile = strings.TrimSpace(caFile)
	if caFile == "" {
		return nil, ErrCAAusente
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("pve: read CA %s: %w", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("pve: %s contains no valid PEM certificate", caFile)
	}
	if strings.TrimSpace(serverName) == "" {
		return nil, errors.New("pve: empty server_name — verification needs a name")
	}

	tr := plainTransport(resolve)
	tr.TLSClientConfig = &tls.Config{
		RootCAs:    pool,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
		// Verification stays ON by omission and that is how it has to stay: the
		// whole package loses its meaning if it is turned off. tls_test.go asserts
		// the field by name; here it is a deliberate absence.
	}
	tr.TLSHandshakeTimeout = tlsHandshakeTimeout
	return tr, nil
}

// plainTransport is the transport without TLS, with the same address
// redirection. It serves the loopback http path (the tests' fake server) and is
// the base on top of which newTransport adds the pin.
func plainTransport(resolve string) *http.Transport {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, redirectAddr(addr, resolve))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 0, // the ceiling is the http.Client's Timeout
	}
}

// redirectAddr swaps the host of the dialled address for the resolve IP,
// keeping the port — the equivalent of `curl --resolve name:port:ip`. An
// address with no port, or an empty resolve, passes straight through.
func redirectAddr(addr, resolve string) string {
	resolve = strings.TrimSpace(resolve)
	if resolve == "" {
		return addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || port == "" {
		return addr
	}
	return net.JoinHostPort(resolve, port)
}
