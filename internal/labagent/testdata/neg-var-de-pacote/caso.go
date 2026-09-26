package caso

import "os/exec"

// BOUNDARY of package-constant resolution: a `var` CANNOT be resolved
// as a literal — it is reassignable at runtime (init, a test, any function).
// Treating it as a literal would open the very hole the pin exists to close.
// This fixture MUST fail.
var binDoJogo = "/usr/local/bin/algo"

func roda() error {
	return exec.Command(binDoJogo, "start").Run()
}
