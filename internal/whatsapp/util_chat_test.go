package whatsapp

import "testing"

func TestResolveChatJID(t *testing.T) {
	const self = "5511555010101@c.us"
	const peer = "5511999998888@c.us"
	const group = "120363000000000001@g.us"

	cases := []struct {
		name     string
		from, to string
		preferTo bool
		want     string
	}{
		{"group outbound from=group", group, self, true, group},
		{"group outbound from=self", self, group, true, group},
		{"group inbound", group, self, false, group},
		{"1to1 outbound", self, peer, true, peer},
		{"1to1 inbound", peer, self, false, peer},
		{"empty to", peer, "", true, peer},
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
