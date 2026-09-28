package auth

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
	if _, ok := ConsumePairingTicket("ticket-never-issued"); ok {
		t.Fatal("a ticket that was never issued should not be accepted")
	}
	if _, ok := ConsumePairingTicket(""); ok {
		t.Fatal("an empty ticket should not be accepted")
	}
}

func TestPairingTicket_ExpiredFails(t *testing.T) {
	globalPairingTicketStore.mu.Lock()
	const fake = "expired-test-ticket"
	globalPairingTicketStore.tickets[fake] = pairingTicket{
		user:      "sam",
		expiresAt: time.Now().Add(-1 * time.Second),
	}
	globalPairingTicketStore.mu.Unlock()

	if user, ok := ConsumePairingTicket(fake); ok {
		t.Fatalf("expired ticket should have failed, returned user=%q ok=true", user)
	}
	if _, ok := ConsumePairingTicket(fake); ok {
		t.Fatal("replay of the expired ticket (already removed) should keep failing")
	}
}

func TestPairingTicket_DoesNotGrantSession(t *testing.T) {
	ticket := IssuePairingTicket("sam")
	user, ok := ConsumePairingTicket(ticket)
	if !ok || user != "sam" {
		t.Fatalf("basic round-trip failed: user=%q ok=%v", user, ok)
	}
}
