package deploy

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// ComposeDown brings a stack down. It tries `compose down` with the file
// (removing the project's anonymous networks and volumes); if the work-tree or
// the file is gone, it falls back to tearing down by compose project label.
func ComposeDown(ctx context.Context, project, workdir, composeFile string, w io.Writer) error {
	if composeFile == "" {
		composeFile = "docker-compose.yml"
	}
	composePath := filepath.Join(workdir, composeFile)
	if _, err := os.Stat(composePath); err == nil {
		c := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", composePath,
			"down", "--remove-orphans")
		c.Dir = workdir
		c.Stdout, c.Stderr = w, w
		if err := c.Run(); err == nil {
			return nil
		}
	}
	// Fallback: remove the project's containers by label, then the default network.
	fmt.Fprintf(w, "→ teardown by label (project=%s)\n", project)
	ids, _ := exec.CommandContext(ctx, "docker", "ps", "-aq",
		"--filter", "label=com.docker.compose.project="+project).Output()
	for _, id := range fields(string(ids)) {
		rm := exec.CommandContext(ctx, "docker", "rm", "-f", id)
		rm.Stdout, rm.Stderr = w, w
		_ = rm.Run()
	}
	_ = exec.CommandContext(ctx, "docker", "network", "rm", project+"_default").Run()
	return nil
}

// ComposePS returns the output of `compose ps` (used by health checks and the UI).
func ComposePS(ctx context.Context, project, workdir, composeFile string) (string, error) {
	if composeFile == "" {
		composeFile = "docker-compose.yml"
	}
	out, err := exec.CommandContext(ctx, "docker", "compose", "-p", project,
		"-f", filepath.Join(workdir, composeFile), "ps").CombinedOutput()
	return string(out), err
}

// previewPort maps (app, preview) → a deterministic port in 20000-29999.
// Deterministic so redeploys of the same PR reuse the port (a stable nginx).
func previewPort(name, preview string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name + "\x00" + preview))
	return 20000 + int(h.Sum32()%10000)
}

func fields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' || r == ' ' || r == '\t' || r == '\r' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
