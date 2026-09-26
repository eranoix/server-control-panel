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

// streamCommand runs cmd while piping its combined stdout+stderr line-by-line
// into logW. Returns nil if the command exits 0, error otherwise.
//
// The progress callback receives best-effort percentages parsed from common
// patterns (apt "X%", docker pull "X/Y"); runners that don't fit this can
// pass nil and rely on the queue's status updates only.
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

// parsePercent looks for "NN%" or "N/M" patterns; returns -1 when neither
// fits. Best-effort and intentionally lossy.
func parsePercent(s string) int {
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '%' && i > 0 {
			// walk back to start of number
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

// --- AptUpgrade runner ---

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

// --- DockerPull runner ---

type DockerPullArgs struct {
	Ref string `json:"ref"`
}

type DockerPullRunner struct{}

func (DockerPullRunner) Kind() string                    { return "docker_pull" }
func (DockerPullRunner) AuthorizedFor(string, bool) bool { return true } // any authed user

func (DockerPullRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a DockerPullArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !validImageRef(a.Ref) {
		return errors.New("invalid image ref")
	}
	step("baixando " + a.Ref)
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

// --- DockerComposePull runner ---

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

// --- ImagePrune runner ---

type ImagePruneRunner struct{}

func (ImagePruneRunner) Kind() string { return "image_prune" }

// Prune deletes every untagged docker image on the host — a destructive,
// host-wide operation that wipes other tenants' WIP builds too. PRIMARY-ONLY
// (previously open to any authed user → privilege escalation).
func (ImagePruneRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (ImagePruneRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	step("removing dangling images")
	fmt.Fprintln(logW, "$ docker image prune -af")
	cmd := exec.CommandContext(ctx, "docker", "image", "prune", "-af")
	return streamCommand(ctx, cmd, logW, progress)
}

// --- BackupNow runner ---

type BackupNowArgs struct {
	Target     string `json:"target"`                // "vault" | "config" | "all"
	Dest       string `json:"dest,omitempty"`        // local folder (abs, no ..); default <DataDir>/backups
	Retention  int    `json:"retention,omitempty"`   // keep the last N per target locally (0 = unlimited)
	DestType   string `json:"dest_type,omitempty"`   // "local" (default) | "rclone"
	Remote     string `json:"remote,omitempty"`      // rclone remote name (when dest_type=rclone)
	RemotePath string `json:"remote_path,omitempty"` // folder inside the remote
}

// validRcloneRemote accepts an rclone remote name (alnum + _-).
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
	// DataDir is what the runner backs up from / writes archives into.
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
	// Local staging/destination dir: default <DataDir>/backups. For dest_type
	// "local" a custom absolute Dest is honored; for "rclone" we stage locally
	// here and upload to the cloud remote afterwards (keeping the local copy so
	// retention still applies and you have a local fallback).
	out := b.DataDir + "/backups"
	if destType == "local" && a.Dest != "" {
		if !strings.HasPrefix(a.Dest, "/") || strings.Contains(a.Dest, "..") {
			return errors.New("dest must be absolute and free of ..")
		}
		out = a.Dest
	}
	step("compactando " + target + " → " + out)
	stamp := time.Now().UTC().Format("20060102-150405")
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	archive := fmt.Sprintf("%s/vpsm-backup-%s-%s.tgz", out, target, stamp)
	fmt.Fprintln(logW, "$ tar czf "+archive)
	// Allowlist what gets backed up — never the whole DataDir (would
	// include the queue history we're writing into right now).
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
	// --warning=no-file-changed silences the noisy warning; even so we treat
	// exit 1 as non-fatal below (several versions of tar still return 1).
	tarArgs := append([]string{"--warning=no-file-changed", "-C", b.DataDir, "-czf", archive}, inputs...)
	cmd := exec.CommandContext(ctx, "tar", tarArgs...)
	if err := streamCommand(ctx, cmd, logW, progress); err != nil {
		// tar exit 1 = non-fatal WARNINGS (e.g. "file changed as we read it" on live
		// data such as users/*/whatsapp, which the wad daemon rewrites during the
		// backup). The .tgz IS produced and extractable; only exit >= 2 is a real
		// failure. Before, any exit != 0 marked the job as "failed" — hence the daily
		// alarm at 04:30.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			fmt.Fprintln(logW, "⚠ tar finished with warnings (a file changed while being read) — archive created; carrying on")
		} else {
			return err
		}
	}
	fmt.Fprintln(logW, "✓ archive: "+archive)
	// Cloud upload via rclone (Drive/OneDrive/S3/… depending on the configured remote).
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

// pruneBackups keeps the newest `keep` archives for a given target in dir and
// deletes the rest. Archive names embed a sortable UTC stamp
// (vpsm-backup-<target>-YYYYMMDD-HHMMSS.tgz), so lexical sort == chronological.
// Returns the basenames removed. Per-target so retention on "config" never
// touches "vault" archives.
func pruneBackups(dir, target string, keep int) ([]string, error) {
	prefix := "vpsm-backup-" + target + "-"
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
	sort.Strings(names) // oldest first
	var removed []string
	for _, n := range names[:len(names)-keep] {
		if err := os.Remove(filepath.Join(dir, n)); err == nil {
			removed = append(removed, n)
		}
	}
	return removed, nil
}

// --- Shell runner (primary-only escape hatch) ---

type ShellArgs struct {
	Cmd  string   `json:"cmd"`  // binary name only — no shell parsing
	Args []string `json:"args"` // explicit args list
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
	// Defense: refuse anything that looks like shell metachars in Cmd.
	// Args are passed as []string so no shell parsing happens.
	for _, r := range a.Cmd {
		if r == ';' || r == '|' || r == '&' || r == '$' || r == '`' || r == '\n' || r == ' ' {
			return errors.New("cmd must be a bare binary name, not a shell line")
		}
	}
	// Defense in depth: even with shell-metachar rejection above, allowing
	// /bin/sh /bin/bash etc with -c re-introduces arbitrary shell. Block
	// the common shell binaries explicitly — primary that needs a shell
	// can use /api/exec (also primary-only).
	base := a.Cmd
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	switch strings.ToLower(base) {
	case "sh", "bash", "zsh", "dash", "ash", "ksh", "fish", "tcsh", "csh":
		return errors.New("shell binaries blocked — use /api/exec for shell access")
	}
	step("executando " + base)
	fmt.Fprintf(logW, "$ %s %s\n", a.Cmd, strings.Join(a.Args, " "))
	cmd := exec.CommandContext(ctx, a.Cmd, a.Args...)
	return streamCommand(ctx, cmd, logW, progress)
}

// validContainerName accepts a docker container name or id: a leading
// alphanumeric followed by alphanumerics and [_.-]. Rejects empty, oversized,
// and anything with shell metachars / slashes (args are passed as []string to
// exec, so this is belt-and-suspenders against a malicious schedule entry).
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

// --- DockerRestart runner ---

type DockerRestartArgs struct {
	Container string `json:"container"`
}

type DockerRestartRunner struct{}

func (DockerRestartRunner) Kind() string { return "docker_restart" }

// Restarting an arbitrary container by name is host-wide (it can bounce another
// tenant's service), so PRIMARY-ONLY — same posture as image_prune/shell.
func (DockerRestartRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (DockerRestartRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a DockerRestartArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !validContainerName(a.Container) {
		return errors.New("invalid container name")
	}
	step("reiniciando " + a.Container)
	fmt.Fprintln(logW, "$ docker restart "+a.Container)
	cmd := exec.CommandContext(ctx, "docker", "restart", a.Container)
	return streamCommand(ctx, cmd, logW, progress)
}

// --- DockerComposeRestart runner ---

type ComposeRestartArgs struct {
	Dir string `json:"dir"`
}

type DockerComposeRestartRunner struct{}

func (DockerComposeRestartRunner) Kind() string { return "docker_compose_restart" }

// Bouncing a whole compose project is host-impacting → PRIMARY-ONLY.
func (DockerComposeRestartRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (DockerComposeRestartRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a ComposeRestartArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !strings.HasPrefix(a.Dir, "/") || strings.Contains(a.Dir, "..") {
		return errors.New("dir must be absolute and free of ..")
	}
	step("reiniciando serviços do compose")
	fmt.Fprintln(logW, "$ cd "+a.Dir+" && docker compose restart")
	cmd := exec.CommandContext(ctx, "docker", "compose", "restart")
	cmd.Dir = a.Dir
	return streamCommand(ctx, cmd, logW, progress)
}

// validUnitName accepts a systemd unit name: leading alnum, then alnum and
// [_.@-] (covers templated units like vpsm-whatsapp@sam.service). Rejects
// slashes/spaces/metachars.
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

// --- SystemdRestart runner ---

type SystemdRestartArgs struct {
	Action string `json:"action"` // "restart" | "reload"
	Unit   string `json:"unit"`
}

type SystemdRestartRunner struct{}

func (SystemdRestartRunner) Kind() string { return "systemd_restart" }

// systemctl mutates host services → PRIMARY-ONLY.
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

// --- DockerPrune runner — volumes/networks/builder (image_prune covers images) ---

type DockerPruneArgs struct {
	Scope string `json:"scope"` // "volumes" | "networks" | "builder"
}

type DockerPruneRunner struct{}

func (DockerPruneRunner) Kind() string { return "docker_prune" }

// Destructive host-wide cleanup → PRIMARY-ONLY (same posture as image_prune).
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
	step("limpando " + a.Scope)
	fmt.Fprintln(logW, "$ docker "+strings.Join(cmdArgs, " "))
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	return streamCommand(ctx, cmd, logW, progress)
}

// --- HTTPCheck runner — uptime/health ping ---

type HTTPCheckArgs struct {
	URL    string `json:"url"`
	Expect int    `json:"expect,omitempty"` // status esperado; 0 = aceitar 200–399
}

type HTTPCheckRunner struct{}

func (HTTPCheckRunner) Kind() string { return "http_check" }

// PRIMARY-ONLY: an arbitrary outbound GET is an SSRF primitive for a non-primary
// user (probing internal services). Primary already has shell, so this grants no
// new privilege. (Follow-up: open to any user behind an egress allowlist.)
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
	step("checando " + u.String())
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
