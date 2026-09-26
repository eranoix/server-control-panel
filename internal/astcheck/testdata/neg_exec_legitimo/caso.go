package caso

import "os/exec"

// Negative control: the panel runs systemctl, git and docker all the time. An
// instrument that only knows how to fail things is the one somebody switches off.
func Roda() { _ = exec.Command("systemctl", "restart", "vps-manager") }
