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

const Container = "panel-recovery-claude"

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

func Command(dataDir string, args ...string) (*exec.Cmd, error) {
	script, err := Materialize(dataDir)
	if err != nil {
		return nil, err
	}
	return exec.Command(script, args...), nil
}

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
