// Package recoveryclaude carries, INSIDE THE BINARY, everything the recovery
// Claude needs: the Dockerfile, the entrypoint, the banner and the container
// manager.
//
// They are embedded instead of read from the repository because the running
// process cannot trust the working tree (deploys build from ticket worktrees,
// and /opt/panel may be on another branch). The binary is the unit of deploy, so
// the files always match the binary that is live.
package recoveryclaude

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed assets
var assets embed.FS

// Container is the name of the container and of the image. It lives here
// because it is the same truth manage.sh uses — whoever needs the name takes it
// from this package instead of repeating the string.
const Container = "panel-recovery-claude"

// Materialize writes the embedded assets into <dataDir>/recovery-claude and
// returns the path of the manager (manage.sh), ready to execute.
//
// It always rewrites, so a fix never leaves an old deploy's version on disk.
func Materialize(dataDir string) (string, error) {
	dest := filepath.Join(dataDir, "recovery-claude")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dest, err)
	}
	entries, err := fs.ReadDir(assets, "assets")
	if err != nil {
		return "", fmt.Errorf("read embedded assets: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		content, err := assets.ReadFile("assets/" + e.Name())
		if err != nil {
			return "", fmt.Errorf("read %s: %w", e.Name(), err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(e.Name(), ".sh") {
			mode = 0o755
		}
		target := filepath.Join(dest, e.Name())
		// Atomic write: a deploy in the middle of a `docker build` must not
		// leave a half-written Dockerfile.
		tmp := target + ".tmp"
		if err := os.WriteFile(tmp, content, mode); err != nil {
			return "", fmt.Errorf("write %s: %w", target, err)
		}
		if err := os.Rename(tmp, target); err != nil {
			return "", fmt.Errorf("move %s: %w", target, err)
		}
	}
	return filepath.Join(dest, "manage.sh"), nil
}

// Command returns an *exec.Cmd of the already-materialized manager. Docker's
// build context is the materialized directory itself — which is why the
// Dockerfile and the scripts have to come out together.
func Command(dataDir string, args ...string) (*exec.Cmd, error) {
	script, err := Materialize(dataDir)
	if err != nil {
		return nil, err
	}
	return exec.Command(script, args...), nil
}

// Restart restarts the recovery Claude container (`manage.sh restart` →
// `docker restart`), applying the CLI version the container has already pulled.
//
// The CLI updates itself inside the container, but the running process keeps
// the binary it loaded at start, so only a relaunch applies the update.
//
// Unlike normal sessions there is no `--continue`: restarting DISCARDS the
// recovery conversation in progress, and the caller must tell the operator so.
func Restart(ctx context.Context, dataDir string) error {
	cmd, err := Command(dataDir, "restart")
	if err != nil {
		return err
	}
	if ctx != nil {
		c2, err := Command(dataDir, "restart")
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, c2.Path, c2.Args[1:]...)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}
