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

func ComposePS(ctx context.Context, project, workdir, composeFile string) (string, error) {
	if composeFile == "" {
		composeFile = "docker-compose.yml"
	}
	out, err := exec.CommandContext(ctx, "docker", "compose", "-p", project,
		"-f", filepath.Join(workdir, composeFile), "ps").CombinedOutput()
	return string(out), err
}

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
