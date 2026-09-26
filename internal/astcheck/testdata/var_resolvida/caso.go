package caso

import "os/exec"

// Synthetic violation: the binary arrives through a variable. It is the hole a
// textual search does not see and that intra-function literal resolution closes.
func Roda() {
	bin := "pct"
	_ = exec.Command(bin, "exec")
}
