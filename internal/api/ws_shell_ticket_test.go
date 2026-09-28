package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"server-control-panel/internal/auth"
)

func TestWSShell_TicketAuthenticatesHandshake(t *testing.T) {
	r := newSmokeRouter(t)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ticket := auth.IssueWSTicket("sam", "jti-of-app")
	conn, resp, err := websocket.DefaultDialer.Dial(
		wsURL+"/ws/shell?name=session-that-does-not-exist&attach=1&ticket="+ticket, nil)
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

func TestWSShell_OtherUsersTicketCannotOpenForeignSession(t *testing.T) {
	r := newSmokeRouter(t)
	const session = "sams-session"
	if err := r.sessionOwn.Claim(session, "sam"); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	ticket := auth.IssueWSTicket("beto", "jti-of-beto")
	conn, resp, err := websocket.DefaultDialer.Dial(
		wsURL+"/ws/shell?name="+session+"&attach=1&ticket="+ticket, nil)
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
	if owner := r.sessionOwn.Owner(session); owner != "sam" {
		t.Fatalf("session owner = %q, expected \"sam\"", owner)
	}
}

func TestWSShell_NoCredentialStill401(t *testing.T) {
	r := newSmokeRouter(t)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	for _, url := range []string{
		wsURL + "/ws/shell?name=main",
		wsURL + "/ws/shell?name=main&ticket=made-up-ticket",
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
