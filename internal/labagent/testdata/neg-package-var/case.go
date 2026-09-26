package fixture

import "os/exec"

// BOUNDARY of package-constant resolution: a `var` CANNOT be resolved
// as a literal — it is reassignable at runtime (init, a test, any function).
// Treating it as a literal would open the very hole the pin exists to close.
// This fixture MUST fail.
var gameBin = "/usr/local/bin/algo"

func run() error {
	return exec.Command(gameBin, "start").Run()
}
