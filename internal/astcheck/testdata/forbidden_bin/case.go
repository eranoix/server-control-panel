package fixture

import "os/exec"

// Synthetic violation: a hypervisor binary invoked through a direct literal.
func Run() { _ = exec.Command("pct", "start", "201") }
