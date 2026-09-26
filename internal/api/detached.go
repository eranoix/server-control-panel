package api

import (
	"fmt"
	"os/exec"
)

// launchDetachedJob starts `<exe> run-job <id>` inside a transient systemd
// scope under user.slice — outside vps-manager.service's cgroup — so the job
// survives `systemctl restart vps-manager` (a deploy). Returns the scope unit
// name, which the queue stores on the Job and later checks via
// `systemctl is-active` to tell a live detached job from a dead one.
//
// Why this works (verified empirically, see the ticket): `systemd-run --scope`
// runs the command as a child that systemd moves into the scope cgroup in
// user.slice, and it INHERITS the caller's environment — so HOME=/root,
// ANTHROPIC_BASE_URL, supabase tokens etc. are all present, exactly the env
// that makes claude route correctly today in-process. Same mechanism the old spawner
// uses for AI terminals. We pass the RESOLVED binary path (os.Executable) so a
// symlink swap mid-deploy can't change the code the detached job runs.
func launchDetachedJob(exe, id string) (string, error) {
	sdrun, err := exec.LookPath("systemd-run")
	if err != nil {
		return "", fmt.Errorf("systemd-run unavailable: %w", err)
	}
	unit := "vpsm-job-" + id + ".scope"
	cmd := exec.Command(sdrun,
		"--quiet", "--collect", "--scope",
		"--slice=user.slice", "--unit="+unit,
		"--", exe, "run-job", id,
	)
	// Inherit the parent env (HOME, ANTHROPIC_BASE_URL, VPSM_CONFIG, …).
	cmd.Env = nil
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start systemd-run: %w", err)
	}
	// Reap the systemd-run process when the job eventually exits so it doesn't
	// linger as a zombie. Abandoned harmlessly if we shut down first — the
	// scope keeps running in user.slice regardless.
	go func() { _ = cmd.Wait() }()
	return unit, nil
}
