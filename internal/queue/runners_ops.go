package queue

// Additional operations/maintenance runners.
// All primary-only; a missing external tool fails with a friendly message.

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

func absNoTraversal(p string) bool { return strings.HasPrefix(p, "/") && !strings.Contains(p, "..") }

// validDBName: a safe database name (alnum + _-.).
func validDBName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// --- DBBackup: Postgres/MySQL database dump (.sql.gz) ---

type DBBackupArgs struct {
	Engine    string `json:"engine"`   // postgres | mysql
	Database  string `json:"database"` // database name
	Dest      string `json:"dest,omitempty"`
	Retention int    `json:"retention,omitempty"`
}

type DBBackupRunner struct{ DataDir string }

func (DBBackupRunner) Kind() string                                { return "db_backup" }
func (DBBackupRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (b DBBackupRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a DBBackupArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !validDBName(a.Database) {
		return errors.New("invalid database name")
	}
	out := b.DataDir + "/backups"
	if a.Dest != "" {
		if !absNoTraversal(a.Dest) {
			return errors.New("dest must be absolute and free of ..")
		}
		out = a.Dest
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	archive := fmt.Sprintf("%s/%s-%s-%s.sql.gz", out, a.Engine, a.Database, stamp)

	var cmd *exec.Cmd
	switch a.Engine {
	case "postgres":
		if err := requireTool("pg_dump"); err != nil {
			return err
		}
		// runs as the postgres user (local peer auth, no password).
		cmd = exec.CommandContext(ctx, "sudo", "-u", "postgres", "pg_dump", "--no-owner", "--no-privileges", a.Database)
	case "mysql":
		if err := requireTool("mysqldump"); err != nil {
			return err
		}
		// root over the socket (the default auth on Debian/Ubuntu).
		cmd = exec.CommandContext(ctx, "mysqldump", "--single-transaction", "--quick", a.Database)
	default:
		return errors.New("engine must be postgres or mysql")
	}

	f, err := os.OpenFile(archive, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	cmd.Stdout = gz   // compressed dump straight into the file (no shell)
	cmd.Stderr = logW // dump errors show up in the log
	step("dumping " + a.Engine + ":" + a.Database)
	fmt.Fprintln(logW, "$ "+strings.Join(cmd.Args, " ")+" | gzip > "+archive)
	runErr := cmd.Run()
	_ = gz.Close()
	_ = f.Close()
	if runErr != nil {
		_ = os.Remove(archive) // do not leave a partial/corrupted dump behind
		return fmt.Errorf("dump failed: %w", runErr)
	}
	progress(90)
	fmt.Fprintln(logW, "✓ archive: "+archive)
	if a.Retention > 0 {
		if removed := pruneByPrefix(out, a.Engine+"-"+a.Database+"-", ".sql.gz", a.Retention); len(removed) > 0 {
			fmt.Fprintf(logW, "✓ retention: removed %d old ones\n", len(removed))
		}
	}
	progress(100)
	return nil
}

// pruneByPrefix keeps the `keep` newest (the name carries a sortable stamp) and removes the rest.
func pruneByPrefix(dir, prefix, suffix string, keep int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, suffix) {
			names = append(names, n)
		}
	}
	if len(names) <= keep {
		return nil
	}
	sort.Strings(names)
	var removed []string
	for _, n := range names[:len(names)-keep] {
		if os.Remove(dir+"/"+n) == nil {
			removed = append(removed, n)
		}
	}
	return removed
}

// --- CertRenew: renew Let's Encrypt certificates (certbot) ---

type CertRenewArgs struct {
	CertName string `json:"cert_name"` // empty = renew all
}

type CertRenewRunner struct{}

func (CertRenewRunner) Kind() string                                { return "cert_renew" }
func (CertRenewRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (CertRenewRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a CertRenewArgs
	_ = json.Unmarshal(args, &a)
	if err := requireTool("certbot"); err != nil {
		return err
	}
	cmdArgs := []string{"renew", "--non-interactive"}
	if c := strings.TrimSpace(a.CertName); c != "" {
		if !validHost(c) { // a cert name is hostname-like
			return errors.New("invalid certificate name")
		}
		cmdArgs = append(cmdArgs, "--cert-name", c)
	}
	step("renewing certificate(s)")
	fmt.Fprintln(logW, "$ certbot "+strings.Join(cmdArgs, " "))
	cmd := exec.CommandContext(ctx, "certbot", cmdArgs...)
	return streamCommand(ctx, cmd, logW, progress)
}

// --- RcloneSync: mirror a VPS folder to the cloud ---

type RcloneSyncArgs struct {
	Source     string `json:"source"`      // pasta local (abs)
	Remote     string `json:"remote"`      // rclone remote name
	RemotePath string `json:"remote_path"` // pasta no remote
}

type RcloneSyncRunner struct{}

func (RcloneSyncRunner) Kind() string                                { return "rclone_sync" }
func (RcloneSyncRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (RcloneSyncRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a RcloneSyncArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if err := requireTool("rclone"); err != nil {
		return err
	}
	if !absNoTraversal(a.Source) {
		return errors.New("source must be an absolute path free of ..")
	}
	if !validRcloneRemote(a.Remote) {
		return errors.New("invalid rclone remote")
	}
	dst := a.Remote + ":" + strings.TrimPrefix(strings.TrimSpace(a.RemotePath), "/")
	step("sincronizando " + a.Source + " → " + dst)
	fmt.Fprintln(logW, "$ rclone sync "+a.Source+" "+dst+" --stats-one-line")
	cmd := exec.CommandContext(ctx, "rclone", "sync", a.Source, dst, "--stats-one-line")
	return streamCommand(ctx, cmd, logW, progress)
}

// --- DockerComposeUp: bring a stack up (up -d) ---

type ComposeUpArgs struct {
	Dir string `json:"dir"`
}

type DockerComposeUpRunner struct{}

func (DockerComposeUpRunner) Kind() string                                { return "docker_compose_up" }
func (DockerComposeUpRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (DockerComposeUpRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a ComposeUpArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !absNoTraversal(a.Dir) {
		return errors.New("dir must be absolute and free of ..")
	}
	step("bringing the compose up (up -d)")
	fmt.Fprintln(logW, "$ cd "+a.Dir+" && docker compose up -d")
	cmd := exec.CommandContext(ctx, "docker", "compose", "up", "-d")
	cmd.Dir = a.Dir
	return streamCommand(ctx, cmd, logW, progress)
}

// --- GitPull: update a local repository ---

type GitPullArgs struct {
	Dir string `json:"dir"`
}

type GitPullRunner struct{}

func (GitPullRunner) Kind() string                                { return "git_pull" }
func (GitPullRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (GitPullRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a GitPullArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if err := requireTool("git"); err != nil {
		return err
	}
	if !absNoTraversal(a.Dir) {
		return errors.New("dir must be absolute and free of ..")
	}
	step("git pull in " + a.Dir)
	fmt.Fprintln(logW, "$ git -C "+a.Dir+" pull --ff-only")
	cmd := exec.CommandContext(ctx, "git", "-C", a.Dir, "pull", "--ff-only")
	return streamCommand(ctx, cmd, logW, progress)
}

// --- AptUpdateCheck: report of upgradable packages (does NOT install) ---

type AptUpdateCheckRunner struct{}

func (AptUpdateCheckRunner) Kind() string                                { return "apt_updatecheck" }
func (AptUpdateCheckRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (AptUpdateCheckRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	step("updating the package index")
	fmt.Fprintln(logW, "$ apt-get update -qq")
	upd := exec.CommandContext(ctx, "apt-get", "update", "-qq")
	upd.Env = append(upd.Environ(), "DEBIAN_FRONTEND=noninteractive")
	_ = streamCommand(ctx, upd, logW, nil) // best-effort; moves on to list even if update complains
	progress(60)
	step("listing available updates")
	fmt.Fprintln(logW, "\n$ apt list --upgradable")
	cmd := exec.CommandContext(ctx, "apt", "list", "--upgradable")
	cmd.Env = append(cmd.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if err := streamCommand(ctx, cmd, logW, progress); err != nil {
		return err
	}
	progress(100)
	fmt.Fprintln(logW, "\n(report only — nothing was installed. Use 'Update system packages' to apply.)")
	return nil
}

// --- Reboot: restart the server (with a 1 min warning) ---

type RebootRunner struct{}

func (RebootRunner) Kind() string                                { return "reboot" }
func (RebootRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (RebootRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	step("scheduling a reboot in 1 min")
	fmt.Fprintln(logW, "$ shutdown -r +1 \"Reboot scheduled by vps-manager\"")
	// +1: gives a one-minute warning (and time for the job to record the trigger) before rebooting.
	cmd := exec.CommandContext(ctx, "shutdown", "-r", "+1", "Reboot scheduled by vps-manager")
	return streamCommand(ctx, cmd, logW, progress)
}
