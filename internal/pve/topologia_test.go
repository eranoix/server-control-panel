package pve

import "testing"

func TestSerialDoCaminho(t *testing.T) {
	casos := []struct{ in, quer string }{
		{"/dev/disk/by-id/usb-Seagate_Expansion_NAA9N1KZ-0:0-part1", "NAA9N1KZ"},
		{"/dev/disk/by-id/nvme-eui.0000000625124629caf25b035000017e-part3", ""},
		{"/dev/disk/by-id/ata-Samsung_SSD_870_S5Y2NJ0R123456-part1", "S5Y2NJ0R123456"},
		{"", ""},
	}
	for _, c := range casos {
		if got := SerialDoCaminho(c.in); got != c.quer {
			t.Errorf("SerialDoCaminho(%q) = %q, want %q", c.in, got, c.quer)
		}
	}
}

func TestTipoDeVdev(t *testing.T) {
	casos := []struct {
		nome, tipo string
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
		{"coisa-nova-99", "listra", false},
		{"/dev/sda", "listra", false},
	}
	for _, c := range casos {
		tipo, red := tipoDeVdev(c.nome)
		if tipo != c.tipo || red != c.red {
			t.Errorf("tipoDeVdev(%q) = (%q,%v), want (%q,%v)", c.nome, tipo, red, c.tipo, c.red)
		}
	}
}
