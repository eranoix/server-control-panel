package queue

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func streamCommand(ctx context.Context, cmd *exec.Cmd, logW io.Writer, progress func(int)) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		copyLines(stdout, logW, progress)
		close(done)
	}()
	copyLines(stderr, logW, progress)
	<-done
	return cmd.Wait()
}

func copyLines(r io.Reader, w io.Writer, progress func(int)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		_, _ = io.WriteString(w, line+"\n")
		if progress != nil {
			if p := parsePercent(line); p >= 0 {
				progress(p)
			}
		}
	}
}

func parsePercent(s string) int {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '%' && i > 0 {
			j := i - 1
			for j >= 0 && s[j] >= '0' && s[j] <= '9' {
				j--
			}
			if j+1 < i {
				n := 0
				for k := j + 1; k < i; k++ {
					n = n*10 + int(s[k]-'0')
				}
				if n >= 0 && n <= 100 {
					return n
				}
			}
		}
	}
	return -1
}

type AptUpgradeRunner struct{}

func (AptUpgradeRunner) Kind() string { return "apt_upgrade" }
func (AptUpgradeRunner) AuthorizedFor(user string, isPrimary bool) bool {
	return isPrimary
}
func (AptUpgradeRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	step("updating the package index")
	fmt.Fprintln(logW, "$ apt-get update")
	cmd := exec.CommandContext(ctx, "apt-get", "update", "-y")
	cmd.Env = append(cmd.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if err := streamCommand(ctx, cmd, logW, progress); err != nil {
		return fmt.Errorf("apt update: %w", err)
	}
	progress(50)
	step("installing updates")
	fmt.Fprintln(logW, "\n$ apt-get upgrade -y")
	cmd = exec.CommandContext(ctx, "apt-get", "upgrade", "-y")
	cmd.Env = append(cmd.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if err := streamCommand(ctx, cmd, logW, progress); err != nil {
		return fmt.Errorf("apt upgrade: %w", err)
	}
	progress(100)
	step("done")
	return nil
}

type DockerPullArgs struct {
	Ref string `json:"ref"`
}

type DockerPullRunner struct{}

func (DockerPullRunner) Kind() string                    { return "docker_pull" }
func (DockerPullRunner) AuthorizedFor(string, bool) bool { return true }

func (DockerPullRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a DockerPullArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !validImageRef(a.Ref) {
		return errors.New("invalid image ref")
	}
	step("pulling " + a.Ref)
	fmt.Fprintln(logW, "$ docker pull "+a.Ref)
	cmd := exec.CommandContext(ctx, "docker", "pull", a.Ref)
	return streamCommand(ctx, cmd, logW, progress)
}

func validImageRef(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '/' || r == '.' || r == '-' || r == '_' || r == ':' || r == '@'
		if !ok {
			return false
		}
	}
	return true
}

type ComposePullArgs struct {
	Dir string `json:"dir"`
}

type DockerComposePullRunner struct{}

func (DockerComposePullRunner) Kind() string                    { return "docker_compose_pull" }
func (DockerComposePullRunner) AuthorizedFor(string, bool) bool { return true }

func (DockerComposePullRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a ComposePullArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !strings.HasPrefix(a.Dir, "/") || strings.Contains(a.Dir, "..") {
		return errors.New("dir must be absolute and free of ..")
	}
	step("pulling the compose images")
	fmt.Fprintln(logW, "$ cd "+a.Dir+" && docker compose pull")
	cmd := exec.CommandContext(ctx, "docker", "compose", "pull")
	cmd.Dir = a.Dir
	return streamCommand(ctx, cmd, logW, progress)
}

type ImagePruneRunner struct{}

func (ImagePruneRunner) Kind() string { return "image_prune" }

func (ImagePruneRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (ImagePruneRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	step("removing dangling images")
	fmt.Fprintln(logW, "$ docker image prune -af")
	cmd := exec.CommandContext(ctx, "docker", "image", "prune", "-af")
	return streamCommand(ctx, cmd, logW, progress)
}

type BackupNowArgs struct {
	Target     string `json:"target"`
	Dest       string `json:"dest,omitempty"`
	Retention  int    `json:"retention,omitempty"`
	DestType   string `json:"dest_type,omitempty"`
	Remote     string `json:"remote,omitempty"`
	RemotePath string `json:"remote_path,omitempty"`
}

func validRcloneRemote(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

type BackupNowRunner struct {
	DataDir string
}

func (b BackupNowRunner) Kind() string                                { return "backup_now" }
func (b BackupNowRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (b BackupNowRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a BackupNowArgs
	_ = json.Unmarshal(args, &a)
	target := a.Target
	if target == "" {
		target = "all"
	}
	destType := a.DestType
	if destType == "" {
		destType = "local"
	}
	out := b.DataDir + "/backups"
	if destType == "local" && a.Dest != "" {
		if !strings.HasPrefix(a.Dest, "/") || strings.Contains(a.Dest, "..") {
			return errors.New("dest must be absolute and free of ..")
		}
		out = a.Dest
	}
	step("compressing " + target + " → " + out)
	stamp := time.Now().UTC().Format("20060102-150405")
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	archive := fmt.Sprintf("%s/panel-backup-%s-%s.tgz", out, target, stamp)
	fmt.Fprintln(logW, "$ tar czf "+archive)
	var inputs []string
	switch target {
	case "vault":
		inputs = []string{"secrets.vault", "config.json"}
	case "config":
		inputs = []string{"config.json"}
	case "all":
		inputs = []string{"secrets.vault", "config.json", "users", "migration-uuid-map.json"}
	default:
		return errors.New("invalid target: " + target)
	}
	tarArgs := append([]string{"--warning=no-file-changed", "-C", b.DataDir, "-czf", archive}, inputs...)
	cmd := exec.CommandContext(ctx, "tar", tarArgs...)
	if err := streamCommand(ctx, cmd, logW, progress); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			fmt.Fprintln(logW, "⚠ tar finished with warnings (a file changed while being read) — archive created; carrying on")
		} else {
			return err
		}
	}
	fmt.Fprintln(logW, "✓ archive: "+archive)
	if destType == "rclone" {
		if !validRcloneRemote(a.Remote) {
			return errors.New("invalid rclone remote")
		}
		if err := requireTool("rclone"); err != nil {
			return err
		}
		dst := a.Remote + ":" + strings.TrimPrefix(strings.TrimSpace(a.RemotePath), "/")
		step("sending to " + dst)
		fmt.Fprintln(logW, "$ rclone copy "+archive+" "+dst)
		rc := exec.CommandContext(ctx, "rclone", "copy", archive, dst, "--stats-one-line")
		if err := streamCommand(ctx, rc, logW, progress); err != nil {
			return fmt.Errorf("rclone copy: %w", err)
		}
		fmt.Fprintln(logW, "✓ sent to "+dst)
	}
	if a.Retention > 0 {
		if removed, err := pruneBackups(out, target, a.Retention); err != nil {
			fmt.Fprintln(logW, "⚠ retention: "+err.Error())
		} else if len(removed) > 0 {
			fmt.Fprintf(logW, "✓ retention: kept %d, removed %d (%s)\n", a.Retention, len(removed), strings.Join(removed, ", "))
		}
	}
	return nil
}

func pruneBackups(dir, target string, keep int) ([]string, error) {
	prefix := "panel-backup-" + target + "-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".tgz") {
			names = append(names, n)
		}
	}
	if len(names) <= keep {
		return nil, nil
	}
	sort.Strings(names)
	var removed []string
	for _, n := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, n)); err == nil {
			removed = append(removed, n)
		}
	}
	return removed, nil
}

type ShellArgs struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args"`
}

type ShellRunner struct{}

func (ShellRunner) Kind() string                                { return "shell" }
func (ShellRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (ShellRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a ShellArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if a.Cmd == "" {
		return errors.New("cmd required")
	}
	for _, r := range a.Cmd {
		if r == ';' || r == '|' || r == '&' || r == '$' || r == '`' || r == '\n' || r == ' ' {
			return errors.New("cmd must be a bare binary name, not a shell line")
		}
	}
	base := a.Cmd
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	switch strings.ToLower(base) {
	case "sh", "bash", "zsh", "dash", "ash", "ksh", "fish", "tcsh", "csh":
		return errors.New("shell binaries blocked — use /api/exec for shell access")
	}
	step("running " + base)
	fmt.Fprintf(logW, "$ %s %s\n", a.Cmd, strings.Join(a.Args, " "))
	cmd := exec.CommandContext(ctx, a.Cmd, a.Args...)
	return streamCommand(ctx, cmd, logW, progress)
}

func validContainerName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i, r := range s {
		alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if i == 0 {
			if !alnum {
				return false
			}
			continue
		}
		if !(alnum || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

type DockerRestartArgs struct {
	Container string `json:"container"`
}

type DockerRestartRunner struct{}

func (DockerRestartRunner) Kind() string { return "docker_restart" }

func (DockerRestartRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (DockerRestartRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a DockerRestartArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !validContainerName(a.Container) {
		return errors.New("invalid container name")
	}
	step("restarting " + a.Container)
	fmt.Fprintln(logW, "$ docker restart "+a.Container)
	cmd := exec.CommandContext(ctx, "docker", "restart", a.Container)
	return streamCommand(ctx, cmd, logW, progress)
}

type ComposeRestartArgs struct {
	Dir string `json:"dir"`
}

type DockerComposeRestartRunner struct{}

func (DockerComposeRestartRunner) Kind() string { return "docker_compose_restart" }

func (DockerComposeRestartRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (DockerComposeRestartRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a ComposeRestartArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !strings.HasPrefix(a.Dir, "/") || strings.Contains(a.Dir, "..") {
		return errors.New("dir must be absolute and free of ..")
	}
	step("restarting the compose services")
	fmt.Fprintln(logW, "$ cd "+a.Dir+" && docker compose restart")
	cmd := exec.CommandContext(ctx, "docker", "compose", "restart")
	cmd.Dir = a.Dir
	return streamCommand(ctx, cmd, logW, progress)
}

func validUnitName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i, r := range s {
		alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if i == 0 {
			if !alnum {
				return false
			}
			continue
		}
		if !(alnum || r == '_' || r == '.' || r == '-' || r == '@') {
			return false
		}
	}
	return true
}

type SystemdRestartArgs struct {
	Action string `json:"action"`
	Unit   string `json:"unit"`
}

type SystemdRestartRunner struct{}

func (SystemdRestartRunner) Kind() string { return "systemd_restart" }

func (SystemdRestartRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (SystemdRestartRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a SystemdRestartArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	action := a.Action
	if action == "" {
		action = "restart"
	}
	if action != "restart" && action != "reload" {
		return errors.New("action must be restart or reload")
	}
	if !validUnitName(a.Unit) {
		return errors.New("invalid unit name")
	}
	step(action + " " + a.Unit)
	fmt.Fprintf(logW, "$ systemctl %s %s\n", action, a.Unit)
	cmd := exec.CommandContext(ctx, "systemctl", action, a.Unit)
	return streamCommand(ctx, cmd, logW, progress)
}

type DockerPruneArgs struct {
	Scope string `json:"scope"`
}

type DockerPruneRunner struct{}

func (DockerPruneRunner) Kind() string { return "docker_prune" }

func (DockerPruneRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (DockerPruneRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a DockerPruneArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	var cmdArgs []string
	switch a.Scope {
	case "volumes":
		cmdArgs = []string{"volume", "prune", "-f"}
	case "networks":
		cmdArgs = []string{"network", "prune", "-f"}
	case "builder":
		cmdArgs = []string{"builder", "prune", "-af"}
	default:
		return errors.New("scope must be volumes, networks or builder")
	}
	step("cleaning " + a.Scope)
	fmt.Fprintln(logW, "$ docker "+strings.Join(cmdArgs, " "))
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	return streamCommand(ctx, cmd, logW, progress)
}

type HTTPCheckArgs struct {
	URL    string `json:"url"`
	Expect int    `json:"expect,omitempty"`
}

type HTTPCheckRunner struct{}

func (HTTPCheckRunner) Kind() string { return "http_check" }

func (HTTPCheckRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (HTTPCheckRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a HTTPCheckArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	u, err := url.Parse(strings.TrimSpace(a.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("url must be a valid http(s) URL")
	}
	step("checking " + u.String())
	fmt.Fprintln(logW, "$ GET "+u.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	elapsed := time.Since(start).Round(time.Millisecond)
	fmt.Fprintf(logW, "← HTTP %d em %s\n", resp.StatusCode, elapsed)
	progress(100)
	if a.Expect > 0 {
		if resp.StatusCode != a.Expect {
			return fmt.Errorf("status %d ≠ expected %d", resp.StatusCode, a.Expect)
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("status %d outside 2xx/3xx", resp.StatusCode)
	}
	return nil
}
