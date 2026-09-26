package inventory

import "time"

// Freshness of the data.
//
// # The age is born on the SERVER
//
// The browser has ANOTHER clock. This project crosses two machines and a
// tailnet; the clock on the operator's machine is not a controlled variable. If
// it runs fast, everything shows up as expired; if it runs slow, a dead node
// shows up as alive. That is why `age_seconds` and `stale` are computed here,
// at the instant of serialisation, and the front-end only FORMATS the number it
// was handed.
//
// # The clock is injected, never read
//
// No function in this file calls the system clock: it comes in by parameter
// (`View`) or through Clock's unexported field — modelled on
// internal/telemetry/sink.go:37, the only precedent in the repo for an
// injectable clock. That is what makes it possible to prove expiry in
// milliseconds, without waiting for it.

// Clock carries the panel's clock. The field is lower-case on purpose: only the
// test in the SAME package swaps it (inventory, not inventory_test).
type Clock struct {
	now func() time.Time
}

// NewClock returns the production clock. This constructor is the ONLY point in
// the file that knows where time comes from.
func NewClock() *Clock { return &Clock{now: time.Now} }

// Now exposes the clock's current instant, so that whoever needs to stamp a
// timestamp (the poller) uses the SAME source of time as the view.
func (c *Clock) Now() time.Time { return c.now() }

// View is the shortcut for whoever already holds the clock: same thing as View(inv, ttl, now).
func (c *Clock) View(inv Inventory, ttl time.Duration) []NodeView {
	return View(inv, ttl, c.now())
}

// AgeSeconds is the age of a timestamp, in whole seconds.
//
// Returns -1 when the resource was NEVER observed (zero timestamp). Zero would
// be worse than wrong: on screen, "0 s ago" reads as just-seen — exactly the
// "stale data presented as live" that this rule exists to forbid. A negative
// age is the explicit marker for "never observed".
func AgeSeconds(observedAt int64, now time.Time) int64 {
	if observedAt <= 0 {
		return -1
	}
	idade := now.Unix() - observedAt
	if idade < 0 {
		// The timestamp and `now` come out of the SAME clock (this server), so a
		// negative age only happens if somebody moves the machine's clock. Zero is the
		// floor; raising the alarm for that case belongs elsewhere, not to this
		// calculation.
		return 0
	}
	return idade
}

// observedAtDoNo is the node's MOST RECENT timestamp: if any field was updated,
// the panel heard the node at that instant. Zero = never heard.
func observedAtDoNo(n Node) int64 {
	maisNovo := n.Status.ObservedAt
	if n.Uptime.ObservedAt > maisNovo {
		maisNovo = n.Uptime.ObservedAt
	}
	return maisNovo
}

// Stale reports whether a timestamp has gone past the TTL. Never observed is stale.
func Stale(observedAt int64, ttl time.Duration, now time.Time) bool {
	idade := AgeSeconds(observedAt, now)
	if idade < 0 {
		return true
	}
	return idade > int64(ttl.Seconds())
}

// credentialState resolves a node's credential state. A PURE function: only
// what is stored plus the instant, no call to the hypervisor.
//
// The order matters. The hypervisor's 401 is indistinguishable between revoked
// and expired (checked in the hypervisor's own source: `verify_token` dies with
// "access expired" when `expire < time()`, and returns a 401 just like the one
// for a deleted token). What breaks the tie is what the panel KNOWS: the
// revocation mark it wrote itself and the `expire` it stored when it created
// the token. Without those two, the two states would become a single one and
// the expiry alarm would have nothing to measure.
//
// ⚠️ "unreachable" IS NOT a credential state and cannot be merged with
// "absent": a vault that is down returns the same emptiness as a key that does
// not exist (the false negative that fooled this project once already).
// Whoever reads the vault answers for that distinction before filling in
// Credential.
func credentialState(c Credential, now time.Time) string {
	if c.State == CredRevogada {
		return CredRevogada
	}
	if c.TokenID == "" {
		return CredAusente
	}
	if c.Expire > 0 && c.Expire < now.Unix() {
		return CredExpirada
	}
	return CredOK
}

// View converts the inventory into what the API delivers: each node with its
// age and its staleness ALREADY resolved, and the credential state normalised.
//
// The input order is preserved (the store already writes sorted by ID), so that
// two reads in a row produce the same document.
func View(inv Inventory, ttl time.Duration, now time.Time) []NodeView {
	vistas := make([]NodeView, 0, len(inv.Nodes))
	for _, n := range inv.Nodes {
		carimbo := observedAtDoNo(n)
		n.Credential.State = credentialState(n.Credential, now)
		vistas = append(vistas, NodeView{
			Node:       n,
			AgeSeconds: AgeSeconds(carimbo, now),
			Stale:      Stale(carimbo, ttl, now),
		})
	}
	return vistas
}
