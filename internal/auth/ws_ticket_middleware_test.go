package auth

// ws_ticket_middleware_test.go — proves that the one-shot WS ticket (?ticket=)
// authenticates a /ws/ handshake through the Middleware, and proves the limits
// it must NOT cross.
//
// Context of the defect that produced these tests: the Android app asked for a
// ticket at POST /api/mobile/v1/terminal/ws-ticket, built /ws/shell?name=..&ticket=..
// and got a 401. The Middleware (the only door to /ws/) understood only Bearer,
// cookie and ?token= — nothing on the /ws/shell path ever consumed a ticket.
// What did understand one (ExtractWSAuth) was called only by
// /ws/mobile-events, mounted outside the middleware.
//
// The ticket exists precisely so we do NOT have to send the session JWT (12h)
// in the query string, where it leaks into proxy access logs, Referer and
// history. So what the tests below pin down is what makes that trade valid:
// single use, 60s, tied to the user/session that asked for it, and useless
// outside the handshake.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"server-control-panel/internal/sessions"
)

// seenIdentity captures what the handler sees downstream of the Middleware —
// exactly the keys UserFrom/JTIFrom read in production.
type seenIdentity struct {
	ran  bool
	user string
	jti  string
}

func spyHandler(vis *seenIdentity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vis.ran = true
		vis.user = UserFrom(r)
		vis.jti = JTIFrom(r)
		w.WriteHeader(http.StatusOK)
	})
}

func testService(t *testing.T) *Service {
	t.Helper()
	return New("test-secret-with-enough-length", nil)
}

// doRequest runs the Middleware over a request and returns the status plus what
// the handler saw.
func doRequest(t *testing.T, s *Service, req *http.Request) (*httptest.ResponseRecorder, seenIdentity) {
	t.Helper()
	var vis seenIdentity
	rec := httptest.NewRecorder()
	s.Middleware(spyHandler(&vis)).ServeHTTP(rec, req)
	return rec, vis
}

// TestMiddleware_ValidTicketOpensWS is the test for the defect: before the fix
// this request got a 401 from the Middleware and the /ws/shell handler never ran.
func TestMiddleware_ValidTicketOpensWS(t *testing.T) {
	s := testService(t)
	ticket := IssueWSTicket("tester", "jti-app")

	rec, vis := doRequest(t, s, httptest.NewRequest("GET", "/ws/shell?name=main&ticket="+ticket, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200 (handler reached); a valid ticket must not get a 401", rec.Code)
	}
	if !vis.ran {
		t.Fatal("the protected handler did not run")
	}
	if vis.user != "tester" {
		t.Fatalf("UserFrom = %q, expected \"tester\" — the handler has to see the ticket's OWNER", vis.user)
	}
	if vis.jti != "jti-app" {
		t.Fatalf("JTIFrom = %q, expected \"jti-do-app\"", vis.jti)
	}
}

// TestMiddleware_ReplayedTicketRejected pins down single use: the same ticket
// is worth nothing on a second connection.
func TestMiddleware_ReplayedTicketRejected(t *testing.T) {
	s := testService(t)
	ticket := IssueWSTicket("tester", "jti-replay")

	if rec, _ := doRequest(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil)); rec.Code != http.StatusOK {
		t.Fatalf("first use: status = %d, expected 200", rec.Code)
	}
	rec, vis := doRequest(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay: status = %d, expected 401 — the ticket is single-use", rec.Code)
	}
	if vis.ran {
		t.Fatal("replay: the handler ran; the replayed ticket authenticated")
	}
}

// TestMiddleware_ExpiredTicketRejected pins down the short life (60s). It
// writes straight into the store so as not to depend on a test clock.
func TestMiddleware_ExpiredTicketRejected(t *testing.T) {
	s := testService(t)
	const ticket = "expired-test-ticket"
	globalTicketStore.mu.Lock()
	globalTicketStore.tickets[ticket] = wsTicket{
		user:      "tester",
		jti:       "jti-expired",
		expiresAt: time.Now().Add(-time.Second),
	}
	globalTicketStore.mu.Unlock()

	rec, vis := doRequest(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401 — an expired ticket does not authenticate", rec.Code)
	}
	if vis.ran {
		t.Fatal("the handler ran with an expired ticket")
	}
}

// TestMiddleware_TicketInvalidOutsideWS: the ticket is a handshake credential.
// On an /api/ route it neither authenticates NOR is consumed (it still works
// for the /ws/ it was issued for — someone trying to spend it in the wrong
// place does not become a denial of service for its owner).
func TestMiddleware_TicketInvalidOutsideWS(t *testing.T) {
	s := testService(t)
	ticket := IssueWSTicket("tester", "jti-outside-ws")

	rec, vis := doRequest(t, s, httptest.NewRequest("GET", "/api/users?ticket="+ticket, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/api/ with a ticket: status = %d, expected 401", rec.Code)
	}
	if vis.ran {
		t.Fatal("/api/ with a ticket: the handler ran; a non-WS route accepted the ticket")
	}

	if rec, _ := doRequest(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil)); rec.Code != http.StatusOK {
		t.Fatalf("the ticket was consumed by the attempt on /api/: status = %d on /ws/, expected 200", rec.Code)
	}
}

// TestMiddleware_PanelDoesNotRegress: the two routes the web panel uses today
// (HttpOnly cookie and ?token= on /ws/) keep working, and they deliver the SAME
// identity the ticket delivers — same keys, same format.
func TestMiddleware_PanelDoesNotRegress(t *testing.T) {
	s := testService(t)
	tok, jti, err := s.Issue("tester", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	withCookie := httptest.NewRequest("GET", "/ws/shell?name=main", nil)
	withCookie.AddCookie(&http.Cookie{Name: "vpsm_token", Value: tok})
	recCookie, visCookie := doRequest(t, s, withCookie)
	if recCookie.Code != http.StatusOK || visCookie.user != "tester" || visCookie.jti != jti {
		t.Fatalf("cookie: status=%d user=%q jti=%q, expected 200/tester/%s", recCookie.Code, visCookie.user, visCookie.jti, jti)
	}

	recQuery, visQuery := doRequest(t, s, httptest.NewRequest("GET", "/ws/shell?name=main&token="+tok, nil))
	if recQuery.Code != http.StatusOK || visQuery.user != "tester" || visQuery.jti != jti {
		t.Fatalf("?token=: status=%d user=%q jti=%q, expected 200/tester/%s", recQuery.Code, visQuery.user, visQuery.jti, jti)
	}

	// Context equivalence between the Middleware's two paths: what the ticket
	// injects has to be indistinguishable from what the JWT injects, otherwise
	// the downstream handler sees a different identity depending on the door.
	ticketRec, ticketSeen := doRequest(t, s, httptest.NewRequest("GET", "/ws/shell?name=main&ticket="+IssueWSTicket("tester", jti), nil))
	if ticketRec.Code != http.StatusOK {
		t.Fatalf("ticket: status = %d, expected 200", ticketRec.Code)
	}
	if ticketSeen.user != visCookie.user || ticketSeen.jti != visCookie.jti {
		t.Fatalf("identity mismatch: ticket=(%q,%q) cookie=(%q,%q)", ticketSeen.user, ticketSeen.jti, visCookie.user, visCookie.jti)
	}
}

// TestMiddleware_RevokedSessionTicketRejected: the ticket inherits the fate
// of the session that issued it. A logout/revoke inside those 60s kills the
// ticket with it — otherwise it would be an orphan credential outliving the logout.
func TestMiddleware_RevokedSessionTicketRejected(t *testing.T) {
	store, err := sessions.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := testService(t).WithSessions(store)

	_, jti, err := s.Issue("tester", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	ticket := IssueWSTicket("tester", jti)
	store.Revoke(jti)

	rec, vis := doRequest(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401 — origin session revoked", rec.Code)
	}
	if vis.ran {
		t.Fatal("the handler ran with a ticket from a revoked session")
	}
}

// TestMiddleware_LiveSessionTicket: the counter-proof to the test above — with
// the sessions store wired up and the session alive, the ticket passes (the
// revocation check must not have become a blanket denial).
func TestMiddleware_LiveSessionTicket(t *testing.T) {
	store, err := sessions.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := testService(t).WithSessions(store)

	_, jti, err := s.Issue("tester", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	rec, vis := doRequest(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+IssueWSTicket("tester", jti), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200", rec.Code)
	}
	if vis.user != "tester" || vis.jti != jti {
		t.Fatalf("identity = (%q,%q), expected (\"tester\",%q)", vis.user, vis.jti, jti)
	}
}
