package pve

import "testing"

// 🔴 The allowlist is the last line between the query from the screen and a
// POST on the hypervisor. It has to refuse EVERYTHING that is not the two
// commands — and refuse in a way that keeps a typo from becoming a power-off.
func TestComandoDeEnergiaValido(t *testing.T) {
	for _, bom := range []string{"reboot", "shutdown"} {
		if _, ok := ComandoDeEnergiaValido(bom); !ok {
			t.Errorf("%q should be accepted", bom)
		}
	}
	for _, mau := range []string{
		"", "REBOOT", "Shutdown", "reboot ", " shutdown", "poweroff", "halt",
		"reboot;shutdown", "reboot&command=shutdown", "../../access/users",
		"stop", "start", "reset", "suspend",
	} {
		if _, ok := ComandoDeEnergiaValido(mau); ok {
			t.Errorf("%q should NOT be accepted — it would reach the hypervisor's POST", mau)
		}
	}
}

// NodePower refuses before dialling: spending a connection to find out that the
// screen sent garbage is waste, and the error that would come back would be the
// hypervisor's, not the validation's.
func TestNodePowerRecusaAntesDeDiscar(t *testing.T) {
	c := &Client{}
	if _, err := c.NodePower(nil, "", EnergiaReboot); err == nil {
		t.Error("an empty node should fail")
	}
	if _, err := c.NodePower(nil, "pve", ComandoDeEnergia("poweroff")); err == nil {
		t.Error("a command outside the allowlist should fail BEFORE any call")
	}
}
