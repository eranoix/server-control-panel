package queue

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func requireTool(bin string) error {
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("tool %q is not installed on the host — install it to use this schedule", bin)
	}
	return nil
}

func validHost(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	return !strings.ContainsAny(s, " /\\\t\n")
}

type SSLCheckArgs struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	WarnDays int    `json:"warn_days,omitempty"`
}

type SSLCheckRunner struct{}

func (SSLCheckRunner) Kind() string { return "ssl_check" }

func (SSLCheckRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (SSLCheckRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a SSLCheckArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if !validHost(a.Host) {
		return errors.New("invalid host")
	}
	port := a.Port
	if port <= 0 || port > 65535 {
		port = 443
	}
	warn := a.WarnDays
	if warn <= 0 {
		warn = 14
	}
	addr := net.JoinHostPort(a.Host, strconv.Itoa(port))
	step("checking the certificate of " + addr)
	fmt.Fprintln(logW, "$ tls.Dial "+addr)
	d := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: a.Host, InsecureSkipVerify: true}) //nolint:gosec
	if err != nil {
		return fmt.Errorf("TLS connection failed: %w", err)
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return errors.New("no certificate presented")
	}
	cert := certs[0]
	days := int(time.Until(cert.NotAfter).Hours() / 24)
	fmt.Fprintf(logW, "CN=%s\nissued by: %s\nexpires on: %s (%d days)\n", cert.Subject.CommonName, cert.Issuer.CommonName, cert.NotAfter.UTC().Format(time.RFC3339), days)
	progress(100)
	if days < 0 {
		return fmt.Errorf("certificate EXPIRED %d days ago", -days)
	}
	if days < warn {
		return fmt.Errorf("certificate expires in %d days (warning threshold: %d)", days, warn)
	}
	fmt.Fprintln(logW, "✓ certificate is valid")
	return nil
}

type DiskCheckArgs struct {
	Path      string `json:"path,omitempty"`
	Threshold int    `json:"threshold,omitempty"`
}

type DiskCheckRunner struct{}

func (DiskCheckRunner) Kind() string                                { return "disk_check" }
func (DiskCheckRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (DiskCheckRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a DiskCheckArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	path := a.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return errors.New("path must be absolute and free of ..")
	}
	threshold := a.Threshold
	if threshold <= 0 || threshold > 100 {
		threshold = 90
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return fmt.Errorf("statfs %s: %w", path, err)
	}
	bsize := uint64(st.Bsize)
	total := st.Blocks * bsize
	avail := st.Bavail * bsize
	if total == 0 {
		return errors.New("total blocks = 0")
	}
	usedPct := int((total - avail) * 100 / total)
	gib := func(b uint64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }
	step("checking " + path)
	fmt.Fprintf(logW, "%s: used %s of %s (%d%%) — free %s\n", path, gib(total-avail), gib(total), usedPct, gib(avail))
	progress(100)
	if usedPct >= threshold {
		return fmt.Errorf("disk at %d%% (threshold %d%%)", usedPct, threshold)
	}
	fmt.Fprintln(logW, "✓ disk usage OK")
	return nil
}

type SecurityAuditRunner struct{}

func (SecurityAuditRunner) Kind() string                                { return "security_audit" }
func (SecurityAuditRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (SecurityAuditRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	if err := requireTool("lynis"); err != nil {
		return err
	}
	step("auditing the system (lynis)")
	fmt.Fprintln(logW, "$ lynis audit system --quick --no-colors")
	cmd := exec.CommandContext(ctx, "lynis", "audit", "system", "--quick", "--no-colors")
	return streamCommand(ctx, cmd, logW, progress)
}

type RootkitScanArgs struct {
	Tool string `json:"tool"`
}

type RootkitScanRunner struct{}

func (RootkitScanRunner) Kind() string                                { return "rootkit_scan" }
func (RootkitScanRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (RootkitScanRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a RootkitScanArgs
	_ = json.Unmarshal(args, &a)
	tool := a.Tool
	if tool == "" {
		tool = "rkhunter"
	}
	var cmd *exec.Cmd
	switch tool {
	case "rkhunter":
		if err := requireTool("rkhunter"); err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, "rkhunter", "--check", "--sk", "--nocolors")
	case "chkrootkit":
		if err := requireTool("chkrootkit"); err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, "chkrootkit")
	default:
		return errors.New("tool must be rkhunter or chkrootkit")
	}
	step("hunting for rootkits (" + tool + ")")
	fmt.Fprintln(logW, "$ "+strings.Join(cmd.Args, " "))
	return streamCommand(ctx, cmd, logW, progress)
}

type IntegrityCheckRunner struct{}

func (IntegrityCheckRunner) Kind() string                                { return "integrity_check" }
func (IntegrityCheckRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (IntegrityCheckRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	if err := requireTool("aide"); err != nil {
		return err
	}
	step("checking file integrity (aide)")
	fmt.Fprintln(logW, "$ aide --check")
	cmd := exec.CommandContext(ctx, "aide", "--check")
	return streamCommand(ctx, cmd, logW, progress)
}

type TrivyScanArgs struct {
	Scope    string `json:"scope"`
	Target   string `json:"target"`
	Severity string `json:"severity,omitempty"`
}

type TrivyScanRunner struct{}

func (TrivyScanRunner) Kind() string                                { return "trivy_scan" }
func (TrivyScanRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (TrivyScanRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a TrivyScanArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	if err := requireTool("trivy"); err != nil {
		return err
	}
	cmdArgs := []string{a.Scope, "--scanners", "vuln", "--no-progress", "--exit-code", "1"}
	switch a.Scope {
	case "image":
		if !validImageRef(a.Target) {
			return errors.New("invalid image ref")
		}
	case "fs":
		if !strings.HasPrefix(a.Target, "/") || strings.Contains(a.Target, "..") {
			return errors.New("target (fs) must be an absolute path free of ..")
		}
	default:
		return errors.New("scope must be image or fs")
	}
	if sev := strings.TrimSpace(a.Severity); sev != "" {
		for _, s := range strings.Split(sev, ",") {
			switch strings.ToUpper(strings.TrimSpace(s)) {
			case "UNKNOWN", "LOW", "MEDIUM", "HIGH", "CRITICAL":
			default:
				return errors.New("invalid severity: " + s)
			}
		}
		cmdArgs = append(cmdArgs, "--severity", strings.ToUpper(sev))
	}
	cmdArgs = append(cmdArgs, a.Target)
	step("vulnerability scan (trivy " + a.Scope + ")")
	fmt.Fprintln(logW, "$ trivy "+strings.Join(cmdArgs, " "))
	cmd := exec.CommandContext(ctx, "trivy", cmdArgs...)
	return streamCommand(ctx, cmd, logW, progress)
}

type Fail2banReportRunner struct{}

func (Fail2banReportRunner) Kind() string                                { return "fail2ban_report" }
func (Fail2banReportRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (Fail2banReportRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	if err := requireTool("fail2ban-client"); err != nil {
		return err
	}
	step("fail2ban status")
	fmt.Fprintln(logW, "$ fail2ban-client status")
	cmd := exec.CommandContext(ctx, "fail2ban-client", "status")
	return streamCommand(ctx, cmd, logW, progress)
}

type AuditReportRunner struct{}

func (AuditReportRunner) Kind() string                                { return "audit_report" }
func (AuditReportRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (AuditReportRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	section := func(title, bin string, a ...string) {
		fmt.Fprintf(logW, "\n===== %s =====\n", title)
		if _, err := exec.LookPath(bin); err != nil {
			fmt.Fprintf(logW, "(skipped: %s is not installed)\n", bin)
			return
		}
		cmd := exec.CommandContext(ctx, bin, a...)
		if err := streamCommand(ctx, cmd, logW, nil); err != nil {
			fmt.Fprintf(logW, "(%s returned: %v)\n", bin, err)
		}
	}
	step("collecting the audit snapshot")
	section("systemd timers", "systemctl", "list-timers", "--all", "--no-pager")
	section("user crontab", "crontab", "-l")
	section("listening ports", "ss", "-tlnp")
	section("recent logins", "last", "-n", "20")
	section("sudoers (effective)", "getent", "group", "sudo")
	progress(100)
	fmt.Fprintln(logW, "\n✓ snapshot complete")
	return nil
}

type CleanupArgs struct {
	Scope string `json:"scope"`
}

type CleanupRunner struct{}

func (CleanupRunner) Kind() string                                { return "cleanup" }
func (CleanupRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (CleanupRunner) Run(ctx context.Context, args json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	var a CleanupArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Errorf("args: %w", err)
	}
	var cmd *exec.Cmd
	switch a.Scope {
	case "apt_cache":
		cmd = exec.CommandContext(ctx, "apt-get", "clean")
	case "journal":
		cmd = exec.CommandContext(ctx, "journalctl", "--vacuum-time=14d")
	case "tmp":
		cmd = exec.CommandContext(ctx, "find", "/tmp", "-type", "f", "-atime", "+7", "-delete")
	default:
		return errors.New("scope must be apt_cache, journal or tmp")
	}
	step("cleaning " + a.Scope)
	fmt.Fprintln(logW, "$ "+strings.Join(cmd.Args, " "))
	return streamCommand(ctx, cmd, logW, progress)
}
