package pve

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
	dialTimeout         = 5 * time.Second
	tlsHandshakeTimeout = 5 * time.Second
)

func newTransport(caFile, serverName, resolve string) (*http.Transport, error) {
	caFile = strings.TrimSpace(caFile)
	if caFile == "" {
		return nil, ErrCAMissing
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
	}
	tr.TLSHandshakeTimeout = tlsHandshakeTimeout
	return tr, nil
}

func plainTransport(resolve string) *http.Transport {
	d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, redirectAddr(addr, resolve))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 0,
	}
}

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
