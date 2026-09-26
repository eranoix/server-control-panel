package caso

import "os/exec"

// Synthetic violation: a hypervisor binary invoked through a direct literal.
func Roda() { _ = exec.Command("pct", "start", "201") }
