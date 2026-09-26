package fixture

import "os/exec"

// Controls inherited from the earlier scan. The agent WILL legitimately run
// docker; the pin has to approve it explicitly.
func diskUsedPct(path string) (pct int, err error) { return 0, nil }

func up() error {
	return exec.Command("docker", "compose", "up", "-d").Run()
}
