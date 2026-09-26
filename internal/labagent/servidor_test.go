package labagent

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestBindDoisListeners — the agent opens EXACTLY two listeners, and both are
// explicit. Proved by connecting to each one.
func TestBindDoisListeners(t *testing.T) {
	s, _ := servidorDeTeste(t, "o-certo")

	// A loopback IP that is not 127.0.0.1 plays the "bridge" in the test: it is
	// a real, explicit address and does not depend on the machine's network.
	const bridgeFake = "127.0.0.2"
	porta := portaLivre(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	erro := make(chan error, 1)
	go func() { erro <- s.Escuta(ctx, bridgeFake, porta) }()

	for _, host := range []string{"127.0.0.1", bridgeFake} {
		alvo := net.JoinHostPort(host, itoa(porta))
		if !alcanca(alvo) {
			t.Errorf("did not listen on %s — the agent has to open BOTH listeners", alvo)
		}
	}

	cancel()
	select {
	case err := <-erro:
		if err != nil {
			t.Errorf("shutdown returned an error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("Escuta did not return after the cancellation")
	}
}

// TestBindRecusaCuringa — an empty or wildcard bridgeIP is an ERROR that names
// the reason.
//
// Binding on a wildcard and "filtering afterwards" leaves the port EXISTING for
// whoever arrives by any other interface — including one nobody has created
// yet. The filter protects the data; it does not protect the surface.
func TestBindRecusaCuringa(t *testing.T) {
	for _, caso := range []struct{ ip, esperaNoErro string }{
		{"", "empty"},
		{"0.0.0.0", "wildcard"},
		{"::", "wildcard"},
		{"nao-e-ip", "IP address"},
	} {
		_, err := enderecosDeEscuta(caso.ip, 9999)
		if err == nil {
			t.Errorf("bridgeIP %q was ACCEPTED — it should have been refused", caso.ip)
			continue
		}
		if !strings.Contains(err.Error(), caso.esperaNoErro) {
			t.Errorf("bridgeIP %q: the error does not name the reason (%q does not contain %q)", caso.ip, err.Error(), caso.esperaNoErro)
		}
	}

	// Negative control: a valid IP must NOT be refused.
	end, err := enderecosDeEscuta("192.168.100.7", 8710)
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

// TestHealthzSemSegredo — a PROCESS probe: no authentication, and revealing
// nothing beyond liveness.
func TestHealthzSemSegredo(t *testing.T) {
	s, _ := servidorDeTeste(t, "o-certo")
	w := pede(t, s, http.MethodGet, "/healthz", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with no Authorization, got %d", w.Code)
	}
	corpo := w.Body.String()
	// It must not leak the node name, a path, or whether a secret is
	// provisioned: the health gate has to reach the route before any credential
	// exists, and whoever reaches it must learn nothing useful for an attack.
	for _, proibido := range []string{"teste", "/opt", "token", "bearer", "segredo", "version"} {
		if strings.Contains(strings.ToLower(corpo), proibido) {
			t.Errorf("/healthz leaked %q into the body: %s", proibido, corpo)
		}
	}
}

func TestMetricsFormato(t *testing.T) {
	s, _ := servidorDeTeste(t, "o-certo")
	// Generate traffic so there is an operation series.
	pede(t, s, http.MethodPost, "/v1/op/server.status", "o-certo", `{}`)
	pede(t, s, http.MethodPost, "/v1/op/naoexiste", "o-certo", `{}`)

	w := pede(t, s, http.MethodGet, "/metrics", "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") || !strings.Contains(ct, "version=0.0.4") {
		t.Errorf("Content-Type outside the Prometheus format: %q", ct)
	}
	corpo := w.Body.String()
	for _, exigido := range []string{
		"# HELP", "# TYPE",
		`lab_agent_ops_total{no="teste",op="server.status",resultado="ok"} 1`,
		`lab_agent_ops_total{no="teste",op="naoexiste",resultado="desconhecida"} 1`,
		"lab_agent_uptime_seconds",
	} {
		if !strings.Contains(corpo, exigido) {
			t.Errorf("/metrics does not contain %q\n--- body ---\n%s", exigido, corpo)
		}
	}
}

// TestOperacaoDesconhecidaEh404 — "does not exist" and "failed" never blur.
func TestOperacaoDesconhecidaEh404(t *testing.T) {
	s, back := servidorDeTeste(t, "o-certo")
	w := pede(t, s, http.MethodPost, "/v1/op/manutencao.rodar", "o-certo", `{}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for an operation outside the catalog, got %d", w.Code)
	}
	if len(back.chamadas) != 0 {
		t.Errorf("the back end was reached by an operation outside the catalog: %v", back.chamadas)
	}
}

// TestCorpoLimitado — 413 BEFORE the handler is called. A limit that only acts
// after the work is no limit.
func TestCorpoLimitado(t *testing.T) {
	s, back := servidorDeTeste(t, "o-certo")
	grande := `{"x":"` + strings.Repeat("a", corpoMax+100) + `"}`
	w := pede(t, s, http.MethodPost, "/v1/op/server.status", "o-certo", grande)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", w.Code)
	}
	if len(back.chamadas) != 0 {
		t.Errorf("the handler was called even though the body is over the limit: %v", back.chamadas)
	}
}

// ── auxiliares ───────────────────────────────────────────────────────────────

func portaLivre(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}

func alcanca(alvo string) bool {
	// Start-up is asynchronous: retry for up to 3 s before giving up.
	prazo := time.Now().Add(3 * time.Second)
	for time.Now().Before(prazo) {
		c, err := net.DialTimeout("tcp", alvo, 250*time.Millisecond)
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
