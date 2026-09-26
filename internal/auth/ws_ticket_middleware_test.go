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

// identidadeVista captures what the handler sees downstream of the Middleware —
// exactly the keys UserFrom/JTIFrom read in production.
type identidadeVista struct {
	rodou bool
	user  string
	jti   string
}

func handlerEspiao(vis *identidadeVista) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vis.rodou = true
		vis.user = UserFrom(r)
		vis.jti = JTIFrom(r)
		w.WriteHeader(http.StatusOK)
	})
}

func servicoDeTeste(t *testing.T) *Service {
	t.Helper()
	return New("segredo-de-teste-com-tamanho-suficiente", nil)
}

// requisita runs the Middleware over a request and returns the status plus what
// the handler saw.
func requisita(t *testing.T, s *Service, req *http.Request) (*httptest.ResponseRecorder, identidadeVista) {
	t.Helper()
	var vis identidadeVista
	rec := httptest.NewRecorder()
	s.Middleware(handlerEspiao(&vis)).ServeHTTP(rec, req)
	return rec, vis
}

// TestMiddleware_BilheteValidoAbreWS is the test for the defect: before the fix
// this request got a 401 from the Middleware and the /ws/shell handler never ran.
func TestMiddleware_BilheteValidoAbreWS(t *testing.T) {
	s := servicoDeTeste(t)
	ticket := IssueWSTicket("teste", "jti-do-app")

	rec, vis := requisita(t, s, httptest.NewRequest("GET", "/ws/shell?name=main&ticket="+ticket, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200 (handler reached); a valid ticket must not get a 401", rec.Code)
	}
	if !vis.rodou {
		t.Fatal("the protected handler did not run")
	}
	if vis.user != "teste" {
		t.Fatalf("UserFrom = %q, expected \"teste\" — the handler has to see the ticket's OWNER", vis.user)
	}
	if vis.jti != "jti-do-app" {
		t.Fatalf("JTIFrom = %q, expected \"jti-do-app\"", vis.jti)
	}
}

// TestMiddleware_BilheteReplayadoRecusado pins down single use: the same ticket
// is worth nothing on a second connection.
func TestMiddleware_BilheteReplayadoRecusado(t *testing.T) {
	s := servicoDeTeste(t)
	ticket := IssueWSTicket("teste", "jti-replay")

	if rec, _ := requisita(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil)); rec.Code != http.StatusOK {
		t.Fatalf("first use: status = %d, expected 200", rec.Code)
	}
	rec, vis := requisita(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay: status = %d, expected 401 — the ticket is single-use", rec.Code)
	}
	if vis.rodou {
		t.Fatal("replay: o handler rodou; o bilhete replayado autenticou")
	}
}

// TestMiddleware_BilheteExpiradoRecusado pins down the short life (60s). It
// writes straight into the store so as not to depend on a test clock.
func TestMiddleware_BilheteExpiradoRecusado(t *testing.T) {
	s := servicoDeTeste(t)
	const ticket = "bilhete-vencido-de-teste"
	globalTicketStore.mu.Lock()
	globalTicketStore.tickets[ticket] = wsTicket{
		user:      "teste",
		jti:       "jti-vencido",
		expiresAt: time.Now().Add(-time.Second),
	}
	globalTicketStore.mu.Unlock()

	rec, vis := requisita(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401 — an expired ticket does not authenticate", rec.Code)
	}
	if vis.rodou {
		t.Fatal("the handler ran with an expired ticket")
	}
}

// TestMiddleware_BilheteNaoValeForaDeWS: the ticket is a handshake credential.
// On an /api/ route it neither authenticates NOR is consumed (it still works
// for the /ws/ it was issued for — someone trying to spend it in the wrong
// place does not become a denial of service for its owner).
func TestMiddleware_BilheteNaoValeForaDeWS(t *testing.T) {
	s := servicoDeTeste(t)
	ticket := IssueWSTicket("teste", "jti-fora-de-ws")

	rec, vis := requisita(t, s, httptest.NewRequest("GET", "/api/users?ticket="+ticket, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/api/ with a ticket: status = %d, expected 401", rec.Code)
	}
	if vis.rodou {
		t.Fatal("/api/ with a ticket: the handler ran; a non-WS route accepted the ticket")
	}

	if rec, _ := requisita(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil)); rec.Code != http.StatusOK {
		t.Fatalf("the ticket was consumed by the attempt on /api/: status = %d on /ws/, expected 200", rec.Code)
	}
}

// TestMiddleware_PainelNaoRegride: the two routes the web panel uses today
// (HttpOnly cookie and ?token= on /ws/) keep working, and they deliver the SAME
// identity the ticket delivers — same keys, same format.
func TestMiddleware_PainelNaoRegride(t *testing.T) {
	s := servicoDeTeste(t)
	tok, jti, err := s.Issue("teste", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	comCookie := httptest.NewRequest("GET", "/ws/shell?name=main", nil)
	comCookie.AddCookie(&http.Cookie{Name: "vpsm_token", Value: tok})
	recCookie, visCookie := requisita(t, s, comCookie)
	if recCookie.Code != http.StatusOK || visCookie.user != "teste" || visCookie.jti != jti {
		t.Fatalf("cookie: status=%d user=%q jti=%q, expected 200/teste/%s", recCookie.Code, visCookie.user, visCookie.jti, jti)
	}

	recQuery, visQuery := requisita(t, s, httptest.NewRequest("GET", "/ws/shell?name=main&token="+tok, nil))
	if recQuery.Code != http.StatusOK || visQuery.user != "teste" || visQuery.jti != jti {
		t.Fatalf("?token=: status=%d user=%q jti=%q, expected 200/teste/%s", recQuery.Code, visQuery.user, visQuery.jti, jti)
	}

	// Context equivalence between the Middleware's two paths: what the ticket
	// injects has to be indistinguishable from what the JWT injects, otherwise
	// the downstream handler sees a different identity depending on the door.
	recBilhete, visBilhete := requisita(t, s, httptest.NewRequest("GET", "/ws/shell?name=main&ticket="+IssueWSTicket("teste", jti), nil))
	if recBilhete.Code != http.StatusOK {
		t.Fatalf("ticket: status = %d, expected 200", recBilhete.Code)
	}
	if visBilhete.user != visCookie.user || visBilhete.jti != visCookie.jti {
		t.Fatalf("identidade divergente: bilhete=(%q,%q) cookie=(%q,%q)", visBilhete.user, visBilhete.jti, visCookie.user, visCookie.jti)
	}
}

// TestMiddleware_BilheteDeSessaoRevogadaRecusado: the ticket inherits the fate
// of the session that issued it. A logout/revoke inside those 60s kills the
// ticket with it — otherwise it would be an orphan credential outliving the logout.
func TestMiddleware_BilheteDeSessaoRevogadaRecusado(t *testing.T) {
	store, err := sessions.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := servicoDeTeste(t).WithSessions(store)

	_, jti, err := s.Issue("teste", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	ticket := IssueWSTicket("teste", jti)
	store.Revoke(jti)

	rec, vis := requisita(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+ticket, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, expected 401 — origin session revoked", rec.Code)
	}
	if vis.rodou {
		t.Fatal("the handler ran with a ticket from a revoked session")
	}
}

// TestMiddleware_BilheteDeSessaoViva: the counter-proof to the test above — with
// the sessions store wired up and the session alive, the ticket passes (the
// revocation check must not have become a blanket denial).
func TestMiddleware_BilheteDeSessaoViva(t *testing.T) {
	store, err := sessions.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatalf("sessions.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := servicoDeTeste(t).WithSessions(store)

	_, jti, err := s.Issue("teste", nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	rec, vis := requisita(t, s, httptest.NewRequest("GET", "/ws/shell?ticket="+IssueWSTicket("teste", jti), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, expected 200", rec.Code)
	}
	if vis.user != "teste" || vis.jti != jti {
		t.Fatalf("identity = (%q,%q), expected (\"teste\",%q)", vis.user, vis.jti, jti)
	}
}
