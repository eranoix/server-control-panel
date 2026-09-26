package queue

// Security / audit / host-health runners.
//
// Derived from Linux server hardening best practice:
//   - Recurring audit (Lynis), rootkit detection (rkhunter/chkrootkit),
//     file integrity (AIDE), container vulnerability scanning
//     (Trivy), fail2ban report.
//   - Hygiene: audit snapshot (timers/cron/ports/logins) to diff against,
//     cache/journal/tmp cleanup.
//   - Spot monitoring: TLS certificate expiry, disk usage.
//
// All of them are primary-only (host-wide operations/observation). Runners that
// depend on external tools fail with a friendly message when the tool is not
// installed. Several of them "fail on purpose" when they find a problem
// (rkhunter found something, aide detected a change, trivy found a CVE, a cert
// expires soon) — that way the schedule fires the scheduler's failure
// notification.

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

// requireTool returns a friendly error when bin is not in PATH.
func requireTool(bin string) error {
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("tool %q is not installed on the host — install it to use this schedule", bin)
	}
	return nil
}

// validHost is a light sanity check (host is passed to net/tls, not a shell):
// non-empty, reasonable length, no spaces/slashes.
func validHost(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	return !strings.ContainsAny(s, " /\\\t\n")
}

// --- SSLCheck runner: TLS certificate expiry ---

type SSLCheckArgs struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`      // default 443
	WarnDays int    `json:"warn_days,omitempty"` // falha se faltam < N dias (default 14)
}

type SSLCheckRunner struct{}

func (SSLCheckRunner) Kind() string { return "ssl_check" }

// PRIMARY-ONLY: connects to an arbitrary host:port (an SSRF-like primitive for
// non-primary users). Primary already has a shell, so this grants no new privilege.
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
	// InsecureSkipVerify: we want to READ the certificate even when verification
	// fails, and apply our own expiry policy. The real validity is evaluated
	// below (NotAfter), and dialing already confirms the host answers TLS.
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

// --- DiskCheck runner: disk usage above a threshold ---

type DiskCheckArgs struct {
	Path      string `json:"path,omitempty"`      // default /
	Threshold int    `json:"threshold,omitempty"` // % (default 90)
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
	step("checando " + path)
	fmt.Fprintf(logW, "%s: used %s of %s (%d%%) — free %s\n", path, gib(total-avail), gib(total), usedPct, gib(avail))
	progress(100)
	if usedPct >= threshold {
		return fmt.Errorf("disk at %d%% (threshold %d%%)", usedPct, threshold)
	}
	fmt.Fprintln(logW, "✓ disk usage OK")
	return nil
}

// --- SecurityAudit runner: Lynis ---

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

// --- RootkitScan runner: rkhunter / chkrootkit ---

type RootkitScanArgs struct {
	Tool string `json:"tool"` // "rkhunter" | "chkrootkit"
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
	// Non-zero exit (it found something) propagates as a failure → notifies. Intentional.
	return streamCommand(ctx, cmd, logW, progress)
}

// --- IntegrityCheck runner: AIDE ---

type IntegrityCheckRunner struct{}

func (IntegrityCheckRunner) Kind() string                                { return "integrity_check" }
func (IntegrityCheckRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (IntegrityCheckRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	if err := requireTool("aide"); err != nil {
		return err
	}
	step("checking file integrity (aide)")
	fmt.Fprintln(logW, "$ aide --check")
	// aide --check returns != 0 when it detects changes → becomes a failure → notifies.
	cmd := exec.CommandContext(ctx, "aide", "--check")
	return streamCommand(ctx, cmd, logW, progress)
}

// --- TrivyScan runner: vulnerabilities in an image/filesystem ---

type TrivyScanArgs struct {
	Scope    string `json:"scope"`              // "image" | "fs"
	Target   string `json:"target"`             // ref da imagem ou caminho
	Severity string `json:"severity,omitempty"` // ex.: "HIGH,CRITICAL"
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
		// Severity allowlist — keeps arbitrary flags from being injected.
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
	// --exit-code 1: trivy returns 1 if it finds a CVE → failure → notifies.
	cmd := exec.CommandContext(ctx, "trivy", cmdArgs...)
	return streamCommand(ctx, cmd, logW, progress)
}

// --- Fail2banReport runner ---

type Fail2banReportRunner struct{}

func (Fail2banReportRunner) Kind() string                                { return "fail2ban_report" }
func (Fail2banReportRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (Fail2banReportRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	if err := requireTool("fail2ban-client"); err != nil {
		return err
	}
	step("fail2ban status")
	fmt.Fprintln(logW, "$ fail2ban-client status")
	// Fails when the service is down — which is useful: a dead fail2ban should alert.
	cmd := exec.CommandContext(ctx, "fail2ban-client", "status")
	return streamCommand(ctx, cmd, logW, progress)
}

// --- AuditReport runner: read-only snapshot for the audit trail ---

type AuditReportRunner struct{}

func (AuditReportRunner) Kind() string                                { return "audit_report" }
func (AuditReportRunner) AuthorizedFor(_ string, isPrimary bool) bool { return isPrimary }

func (AuditReportRunner) Run(ctx context.Context, _ json.RawMessage, logW io.Writer, progress func(int), step func(string)) error {
	// A snapshot of what is scheduled/listening/logging — to diff month over month
	// (cron/timer audit best practice). Best-effort: an error in one section does
	// not bring the whole report down.
	section := func(title, bin string, a ...string) {
		fmt.Fprintf(logW, "\n===== %s =====\n", title)
		if _, err := exec.LookPath(bin); err != nil {
			fmt.Fprintf(logW, "(skipped: %s is not installed)\n", bin)
			return
		}
		cmd := exec.CommandContext(ctx, bin, a...)
		if err := streamCommand(ctx, cmd, logW, nil); err != nil {
			fmt.Fprintf(logW, "(%s retornou: %v)\n", bin, err)
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

// --- Cleanup runner: disk-space hygiene ---

type CleanupArgs struct {
	Scope string `json:"scope"` // "apt_cache" | "journal" | "tmp"
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
		// Removes files in /tmp not accessed for 7+ days (fixed path, safe).
		cmd = exec.CommandContext(ctx, "find", "/tmp", "-type", "f", "-atime", "+7", "-delete")
	default:
		return errors.New("scope must be apt_cache, journal or tmp")
	}
	step("limpando " + a.Scope)
	fmt.Fprintln(logW, "$ "+strings.Join(cmd.Args, " "))
	return streamCommand(ctx, cmd, logW, progress)
}
