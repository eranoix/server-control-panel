// Package recoveryclaude carries, INSIDE THE BINARY, everything the recovery
// Claude needs: the Dockerfile, the entrypoint, the banner and the container
// manager.
//
// WHY EMBED, instead of reading from the repository:
//
// The first version called "/opt/panel/scripts/recovery-claude.sh" and the
// screen showed "fork/exec: no such file or directory". It was not a careless
// path — it is structural, and it bit twice in a row:
//
//  1. the deploy builds from the ticket's WORKTREE, but /opt/panel is the main
//     working tree, which lives on another branch and does not have the script;
//  2. the next attempt — making the deploy INSTALL the script — would not have
//     worked either: agentctl runs "$ROOT/scripts/deploy.sh", that is, the
//     deploy.sh of the main working tree, so a change to the worktree's
//     deploy.sh never runs.
//
// The root cause is always the same: the running process has no way to trust
// the contents of the repository's working directory. The binary IS the unit of
// deploy — so what it needs has to travel with it, as already happens with the
// webassets.
//
// That way the files are always at the version that matches the binary in the
// air, with no dependence on a branch, a working tree or who ran the deploy.
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
const Container = "vpsm-recovery-claude"

// Materializa writes the embedded assets into <dataDir>/recovery-claude and
// returns the path of the manager (manage.sh), ready to execute.
//
// It always rewrites: the cost is a few KB and the alternative — skipping when
// the file already exists — would leave an old deploy's version on disk after a
// fix, which is exactly the kind of surprise you do not want in an emergency
// tool.
func Materializa(dataDir string) (string, error) {
	destino := filepath.Join(dataDir, "recovery-claude")
	if err := os.MkdirAll(destino, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", destino, err)
	}
	entradas, err := fs.ReadDir(assets, "assets")
	if err != nil {
		return "", fmt.Errorf("read embedded assets: %w", err)
	}
	for _, e := range entradas {
		if e.IsDir() {
			continue
		}
		conteudo, err := assets.ReadFile("assets/" + e.Name())
		if err != nil {
			return "", fmt.Errorf("read %s: %w", e.Name(), err)
		}
		modo := os.FileMode(0o644)
		if strings.HasSuffix(e.Name(), ".sh") {
			modo = 0o755
		}
		alvo := filepath.Join(destino, e.Name())
		// Atomic write: a deploy in the middle of a `docker build` must not
		// leave a half-written Dockerfile.
		tmp := alvo + ".tmp"
		if err := os.WriteFile(tmp, conteudo, modo); err != nil {
			return "", fmt.Errorf("write %s: %w", alvo, err)
		}
		if err := os.Rename(tmp, alvo); err != nil {
			return "", fmt.Errorf("move %s: %w", alvo, err)
		}
	}
	return filepath.Join(destino, "manage.sh"), nil
}

// Comando returns an *exec.Cmd of the already-materialized manager. Docker's
// build context is the materialized directory itself — which is why the
// Dockerfile and the scripts have to come out together.
func Comando(dataDir string, args ...string) (*exec.Cmd, error) {
	script, err := Materializa(dataDir)
	if err != nil {
		return nil, err
	}
	return exec.Command(script, args...), nil
}

// Reinicia restarts the recovery Claude container (`manage.sh restart` →
// `docker restart`), applying the CLI version the container has already pulled.
//
// The semantics are the SAME as the button for normal sessions on the version
// panel: the CLI updates itself inside the container and writes the new
// symlink, but the running process stays on the binary it loaded at boot — only
// a relaunch applies it. Measured in practice: the process on 2.1.241, the
// container's symlink already on 2.1.246.
//
// It differs from normal sessions on one point the caller must make clear to
// the operator: there is no `--continue` here. The container comes up with
// `sleep infinity` and the session is born when someone opens /recovery, so
// restarting DISCARDS the recovery conversation in progress, if there is one.
func Reinicia(ctx context.Context, dataDir string) error {
	cmd, err := Comando(dataDir, "restart")
	if err != nil {
		return err
	}
	if ctx != nil {
		c2, err := Comando(dataDir, "restart")
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, c2.Path, c2.Args[1:]...)
	}
	saida, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(saida))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}
