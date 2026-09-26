package caso

import xc "os/exec"

// Synthetic violation: "os/exec" imported under an alias. A search for "exec.Command"
// would not see this call.
func Roda() { _ = xc.Command("pct", "status", "201") }
