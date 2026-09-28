package inventory

import "time"

type Clock struct {
	now func() time.Time
}

func NewClock() *Clock { return &Clock{now: time.Now} }

func (c *Clock) Now() time.Time { return c.now() }

func (c *Clock) View(inv Inventory, ttl time.Duration) []NodeView {
	return View(inv, ttl, c.now())
}

func AgeSeconds(observedAt int64, now time.Time) int64 {
	if observedAt <= 0 {
		return -1
	}
	age := now.Unix() - observedAt
	if age < 0 {
		return 0
	}
	return age
}

func nodeObservedAt(n Node) int64 {
	newest := n.Status.ObservedAt
	if n.Uptime.ObservedAt > newest {
		newest = n.Uptime.ObservedAt
	}
	return newest
}

func Stale(observedAt int64, ttl time.Duration, now time.Time) bool {
	age := AgeSeconds(observedAt, now)
	if age < 0 {
		return true
	}
	return age > int64(ttl.Seconds())
}

func credentialState(c Credential, now time.Time) string {
	if c.State == CredRevoked {
		return CredRevoked
	}
	if c.TokenID == "" {
		return CredMissing
	}
	if c.Expire > 0 && c.Expire < now.Unix() {
		return CredExpired
	}
	return CredOK
}

func View(inv Inventory, ttl time.Duration, now time.Time) []NodeView {
	seen := make([]NodeView, 0, len(inv.Nodes))
	for _, n := range inv.Nodes {
		stamp := nodeObservedAt(n)
		n.Credential.State = credentialState(n.Credential, now)
		seen = append(seen, NodeView{
			Node:       n,
			AgeSeconds: AgeSeconds(stamp, now),
			Stale:      Stale(stamp, ttl, now),
		})
	}
	return seen
}
