package pve

import "testing"

func TestValidPowerCommand(t *testing.T) {
	for _, bom := range []string{"reboot", "shutdown"} {
		if _, ok := ValidPowerCommand(bom); !ok {
			t.Errorf("%q should be accepted", bom)
		}
	}
	for _, mau := range []string{
		"", "REBOOT", "Shutdown", "reboot ", " shutdown", "poweroff", "halt",
		"reboot;shutdown", "reboot&command=shutdown", "../../access/users",
		"stop", "start", "reset", "suspend",
	} {
		if _, ok := ValidPowerCommand(mau); ok {
			t.Errorf("%q should NOT be accepted — it would reach the hypervisor's POST", mau)
		}
	}
}

func TestNodePowerRejectsBeforeDialing(t *testing.T) {
	c := &Client{}
	if _, err := c.NodePower(nil, "", PowerReboot); err == nil {
		t.Error("an empty node should fail")
	}
	if _, err := c.NodePower(nil, "pve", PowerCommand("poweroff")); err == nil {
		t.Error("a command outside the allowlist should fail BEFORE any call")
	}
}
