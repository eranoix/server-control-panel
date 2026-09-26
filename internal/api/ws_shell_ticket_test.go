package api

// ws_shell_ticket_test.go — end-to-end proof, on the real Router (same mux,
// same auth.Middleware, same handleHostShell), that /ws/shell accepts the
// one-shot WS ticket the Android app mints at POST /api/mobile/v1/terminal/ws-ticket
// — and that the ticket carries the RIGHT identity to the session ownership gate.
//
// Before the fix the handshake below took a 401 from the Middleware: nothing on
// the /ws/shell path consumed a ticket, and the app (which has no browser cookie
// and sends no Authorization header on the upgrade) had no way to authenticate
// without putting the 12h JWT in the query string — which is exactly what the
// ticket avoids.

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
)

// TestWSShell_BilheteAutenticaHandshake: with a valid ticket the upgrade happens
// (101) and the handler runs. It uses attach=1 on a session that does not exist
// to prove the handler was reached WITHOUT creating any shell: the legitimate
// owner gets close code 4404 ("session ended"), a signal that only goes to
// whoever may touch the name.
func TestWSShell_BilheteAutenticaHandshake(t *testing.T) {
	r := newSmokeRouter(t)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ticket := auth.IssueWSTicket("sam", "jti-do-app")
	conn, resp, err := websocket.DefaultDialer.Dial(
		wsURL+"/ws/shell?name=sessao-que-nao-existe&attach=1&ticket="+ticket, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial with a valid ticket failed (status %d): %v — the Middleware needs to accept ?ticket= on /ws/", status, err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if ce, ok := err.(*websocket.CloseError); ok {
		if ce.Code != 4404 {
			t.Fatalf("close code = %d, expected 4404 (legitimate owner of a closed session)", ce.Code)
		}
		return
	}
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if strings.Contains(string(msg), "session not found") {
		t.Fatalf("handler returned %q — the ticket owner was not recognized as the session owner", msg)
	}
}

// TestWSShell_BilheteDeOutroUsuarioNaoAbreSessaoAlheia: the ticket has to carry
// the RIGHT owner to the handler. The session belongs to "sam"; the ticket is
// "beto"'s (non-admin). If the Middleware injected the wrong identity — or
// none — HostShell's ownership gate would decide with the wrong user, and that
// would be silent privilege escalation.
func TestWSShell_BilheteDeOutroUsuarioNaoAbreSessaoAlheia(t *testing.T) {
	r := newSmokeRouter(t)
	const sessao = "sessao-do-sam"
	if err := r.sessionOwn.Claim(sessao, "sam"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ticket := auth.IssueWSTicket("beto", "jti-do-beto")
	conn, resp, err := websocket.DefaultDialer.Dial(
		wsURL+"/ws/shell?name="+sessao+"&attach=1&ticket="+ticket, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial (status %d): %v — beto's ticket is valid, the upgrade has to happen; the possession gate is what denies it", status, err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v — expected the generic 404 from the possession gate", err)
	}
	if !strings.Contains(string(msg), "session not found") {
		t.Fatalf("response = %q, expected \"session not found\" — beto's ticket attached to sam's session", msg)
	}
	// And ownership must not have been stolen along the way.
	if dono := r.sessionOwn.Owner(sessao); dono != "sam" {
		t.Fatalf("session owner = %q, expected \"sam\"", dono)
	}
}

// TestWSShell_SemCredencialContinua401: the counter-proof — the gate did not loosen.
func TestWSShell_SemCredencialContinua401(t *testing.T) {
	r := newSmokeRouter(t)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	for _, url := range []string{
		wsURL + "/ws/shell?name=main",
		wsURL + "/ws/shell?name=main&ticket=bilhete-inventado",
	} {
		conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
		if err == nil {
			conn.Close()
			t.Fatalf("%s: handshake accepted without a valid credential", url)
		}
		if resp == nil || resp.StatusCode != 401 {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			t.Fatalf("%s: status = %d, expected 401", url, status)
		}
	}
}
