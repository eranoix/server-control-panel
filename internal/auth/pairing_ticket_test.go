package auth

// pairing_ticket_test.go — proves the three security properties of the QR-code
// pairing ticket at the lowest possible layer,
// without depending on HTTP or on Router: mint/consume is a single-use round
// trip, replaying an already consumed ticket fails, and an expired ticket
// fails even if it was never consumed.

import (
	"testing"
	"time"
)

func TestPairingTicket_MintConsumeRoundTrip(t *testing.T) {
	ticket := IssuePairingTicket("sam")
	if ticket == "" {
		t.Fatal("IssuePairingTicket returned an empty ticket")
	}
	user, ok := ConsumePairingTicket(ticket)
	if !ok {
		t.Fatal("ConsumePairingTicket failed for a freshly issued and still valid ticket")
	}
	if user != "sam" {
		t.Fatalf("username = %q, expected %q", user, "sam")
	}
}

// TestPairingTicket_ReplayFails proves replay protection: an already-consumed
// ticket can never be exchanged again, not even inside the validity window —
// there is no "it almost worked".
func TestPairingTicket_ReplayFails(t *testing.T) {
	ticket := IssuePairingTicket("sam")
	if _, ok := ConsumePairingTicket(ticket); !ok {
		t.Fatal("first consume should have succeeded")
	}
	if user, ok := ConsumePairingTicket(ticket); ok {
		t.Fatalf("replay of the consumed ticket should have failed, but returned user=%q ok=true", user)
	}
}

func TestPairingTicket_UnknownTicketFails(t *testing.T) {
	if _, ok := ConsumePairingTicket("ticket-que-nunca-foi-emitido"); ok {
		t.Fatal("a ticket that was never issued should not be accepted")
	}
	if _, ok := ConsumePairingTicket(""); ok {
		t.Fatal("an empty ticket should not be accepted")
	}
}

// TestPairingTicket_ExpiredFails proves the TTL is honoured even when the
// ticket was never consumed before expiring. It manipulates the clock by
// injecting directly into the store (no real 5min sleep).
func TestPairingTicket_ExpiredFails(t *testing.T) {
	globalPairingTicketStore.mu.Lock()
	const fake = "ticket-de-teste-expirado"
	globalPairingTicketStore.tickets[fake] = pairingTicket{
		user:      "sam",
		expiresAt: time.Now().Add(-1 * time.Second), // already expired
	}
	globalPairingTicketStore.mu.Unlock()

	if user, ok := ConsumePairingTicket(fake); ok {
		t.Fatalf("expired ticket should have failed, returned user=%q ok=true", user)
	}
	// And replaying the same one (already removed by the consume above) keeps failing too.
	if _, ok := ConsumePairingTicket(fake); ok {
		t.Fatal("replay of the expired ticket (already removed) should keep failing")
	}
}

// TestPairingTicket_DoesNotGrantSession documents, in the type signature
// itself, the central guarantee of the design: ConsumePairingTicket returns
// only a username (plus an ok bool) — there is no return path that could
// produce a session JWT or anything like it. The full "ticket -> reg_token,
// never a session" proof lives in internal/mobilebff/auth_pairing_test.go,
// which exercises the public HTTP route end to end.
func TestPairingTicket_DoesNotGrantSession(t *testing.T) {
	ticket := IssuePairingTicket("sam")
	user, ok := ConsumePairingTicket(ticket)
	if !ok || user != "sam" {
		t.Fatalf("basic round-trip failed: user=%q ok=%v", user, ok)
	}
	// There is no third return value that could turn into a token — the
	// (string, bool) signature of ConsumePairingTicket itself guarantees that;
	// this assertion exists only to keep the test sensitive to a future change
	// in the function's signature.
}
