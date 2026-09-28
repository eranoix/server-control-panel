package api

import (
	"fmt"
	"os/exec"
)

func launchDetachedJob(exe, id string) (string, error) {
	sdrun, err := exec.LookPath("systemd-run")
	if err != nil {
		return "", fmt.Errorf("systemd-run unavailable: %w", err)
	}
	unit := "panel-job-" + id + ".scope"
	cmd := exec.Command(sdrun,
		"--quiet", "--collect", "--scope",
		"--slice=user.slice", "--unit="+unit,
		"--", exe, "run-job", id,
	)
	cmd.Env = nil
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start systemd-run: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return unit, nil
}
