package caso

import "os/exec"

// A NEW wrapper, off the watched list: it fails anyway, because its body calls
// exec.Command with an argument that does not resolve to a literal. Without this
// the wrapper list would age in silence.
func rodaQualquerCoisa(nome string, args ...string) error {
	return exec.Command(nome, args...).Run()
}
