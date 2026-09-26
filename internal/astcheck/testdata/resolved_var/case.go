package fixture

import "os/exec"

// Synthetic violation: the binary arrives through a variable. It is the hole a
// textual search does not see and that intra-function literal resolution closes.
func Run() {
	bin := "pct"
	_ = exec.Command(bin, "exec")
}
