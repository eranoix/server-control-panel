package fixture

import "os/exec"

// A NEW wrapper, off the watched list: it fails anyway, because its body calls
// exec.Command with an argument that does not resolve to a literal. Without this
// the wrapper list would age in silence.
func runAnything(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
