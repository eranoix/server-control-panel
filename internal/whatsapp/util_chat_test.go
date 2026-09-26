package whatsapp

import "testing"

// TestResolveChatJID locks down group-aware conversation derivation. The bug it
// guards against: a message sent to a group ended up in the "self" chat, because
// the `to` of a fromMe message in a group arrives as the account's own number.
func TestResolveChatJID(t *testing.T) {
	const self = "5511555010101@c.us"
	const peer = "5511999998888@c.us"
	const group = "120363000000000001@g.us"

	cases := []struct {
		name     string
		from, to string
		preferTo bool // = fromMe pra mensagens; true pra ack/revoke
		want     string
	}{
		// The reported bug: group outbound (from=group, to=my own number).
		{"grupo outbound from=group", group, self, true, group},
		// Defensive variant: some engines send from=self, to=group.
		{"grupo outbound from=self", self, group, true, group},
		// Group inbound: from=group.
		{"grupo inbound", group, self, false, group},
		// 1:1 outbound: from=me, to=peer → peer.
		{"1a1 outbound", self, peer, true, peer},
		// 1:1 inbound: from=peer → peer.
		{"1a1 inbound", peer, self, false, peer},
		// An empty to falls back to from.
		{"to vazio", peer, "", true, peer},
		// legitimate self-chat (message to oneself).
		{"self chat", self, self, true, self},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveChatJID(c.from, c.to, c.preferTo); got != c.want {
				t.Errorf("resolveChatJID(%q,%q,%v) = %q, want %q", c.from, c.to, c.preferTo, got, c.want)
			}
		})
	}
}

func TestIsGroupJID(t *testing.T) {
	if !isGroupJID("120363000000000001@g.us") {
		t.Error("@g.us should be a group")
	}
	if isGroupJID("5511555010101@c.us") {
		t.Error("@c.us is not a group")
	}
}
