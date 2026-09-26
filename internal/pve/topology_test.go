package pve

import "testing"

func TestSerialFromPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/dev/disk/by-id/usb-Seagate_Expansion_NAA9N1KZ-0:0-part1", "NAA9N1KZ"},
		{"/dev/disk/by-id/nvme-eui.0000000625124629caf25b035000017e-part3", ""},
		{"/dev/disk/by-id/ata-Samsung_SSD_870_S5Y2NJ0R123456-part1", "S5Y2NJ0R123456"},
		{"", ""},
	}
	for _, c := range cases {
		if got := SerialFromPath(c.in); got != c.want {
			t.Errorf("SerialFromPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVdevType(t *testing.T) {
	cases := []struct {
		name, kind string
		red        bool
	}{
		{"mirror-0", "mirror", true},
		{"raidz1-0", "raidz1", true},
		{"raidz2-0", "raidz2", true},
		{"raidz3-0", "raidz3", true},
		{"cache", "especial", false},
		{"logs", "especial", false},
		// 🔴 Unknown does NOT become redundant. The error in the other direction makes
		// the operator trust a mirror that does not exist.
		{"new-thing-99", "listra", false},
		{"/dev/sda", "listra", false},
	}
	for _, c := range cases {
		kind, red := vdevType(c.name)
		if kind != c.kind || red != c.red {
			t.Errorf("vdevType(%q) = (%q,%v), want (%q,%v)", c.name, kind, red, c.kind, c.red)
		}
	}
}
