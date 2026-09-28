package nodeagent

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBindTwoListeners(t *testing.T) {
	s, _ := testServer(t, "the-right-one")

	const bridgeFake = "127.0.0.2"
	port := freePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := make(chan error, 1)
	go func() { failure <- s.Listen(ctx, bridgeFake, port) }()

	for _, host := range []string{"127.0.0.1", bridgeFake} {
		target := net.JoinHostPort(host, itoa(port))
		if !reachable(target) {
			t.Errorf("did not listen on %s — the agent has to open BOTH listeners", target)
		}
	}

	cancel()
	select {
	case err := <-failure:
		if err != nil {
			t.Errorf("shutdown returned an error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("Listen did not return after the cancellation")
	}
}

func TestBindRejectsWildcard(t *testing.T) {
	for _, tc := range []struct{ ip, wantInErr string }{
		{"", "empty"},
		{"0.0.0.0", "wildcard"},
		{"::", "wildcard"},
		{"not-an-ip", "IP address"},
	} {
		_, err := listenAddrs(tc.ip, 9999)
		if err == nil {
			t.Errorf("bridgeIP %q was ACCEPTED — it should have been refused", tc.ip)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantInErr) {
			t.Errorf("bridgeIP %q: the error does not name the reason (%q does not contain %q)", tc.ip, err.Error(), tc.wantInErr)
		}
	}

	end, err := listenAddrs("192.168.100.7", 8710)
	if err != nil {
		t.Fatalf("FALSE POSITIVE: legitimate IP refused: %v", err)
	}
	if len(end) != 2 {
		t.Fatalf("expected 2 addresses, got %d: %v", len(end), end)
	}
	if !strings.HasPrefix(end[0], "127.0.0.1:") {
		t.Errorf("the first address should be loopback, got %q", end[0])
	}
}

func TestHealthzWithoutSecret(t *testing.T) {
	s, _ := testServer(t, "the-right-one")
	w := request(t, s, http.MethodGet, "/healthz", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with no Authorization, got %d", w.Code)
	}
	body := w.Body.String()
	for _, forbidden := range []string{"test", "/opt", "token", "bearer", "secret", "version"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("/healthz leaked %q into the body: %s", forbidden, body)
		}
	}
}

func TestMetricsFormat(t *testing.T) {
	s, _ := testServer(t, "the-right-one")
	request(t, s, http.MethodPost, "/v1/op/server.status", "the-right-one", `{}`)
	request(t, s, http.MethodPost, "/v1/op/nonexistent", "the-right-one", `{}`)

	w := request(t, s, http.MethodGet, "/metrics", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") || !strings.Contains(ct, "version=0.0.4") {
		t.Errorf("Content-Type outside the Prometheus format: %q", ct)
	}
	body := w.Body.String()
	for _, required := range []string{
		"# HELP", "# TYPE",
		`node_agent_ops_total{no="test",op="server.status",result="ok"} 1`,
		`node_agent_ops_total{no="test",op="nonexistent",result="unknown"} 1`,
		"node_agent_uptime_seconds",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("/metrics does not contain %q\n--- body ---\n%s", required, body)
		}
	}
}

func TestUnknownOperationIs404(t *testing.T) {
	s, back := testServer(t, "the-right-one")
	w := request(t, s, http.MethodPost, "/v1/op/maintenance.run", "the-right-one", `{}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for an operation outside the catalog, got %d", w.Code)
	}
	if len(back.calls) != 0 {
		t.Errorf("the back end was reached by an operation outside the catalog: %v", back.calls)
	}
}

func TestBodyLimited(t *testing.T) {
	s, back := testServer(t, "the-right-one")
	big := `{"x":"` + strings.Repeat("a", maxBody+100) + `"}`
	w := request(t, s, http.MethodPost, "/v1/op/server.status", "the-right-one", big)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", w.Code)
	}
	if len(back.calls) != 0 {
		t.Errorf("the handler was called even though the body is over the limit: %v", back.calls)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}

func reachable(target string) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", target, 250*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
