// vpsmctl — local admin/recovery CLI for vps-manager.
//
// Operates on /opt/panel/data directly. Does NOT talk to the HTTP API.
// Designed to work even when the web UI is broken — SSH into the box, run
// vpsmctl, recover.
//
// Subcommands:
//
//	vpsmctl status                 — service state + last login summary
//	vpsmctl rollback               — invoke scripts/deploy.sh --rollback
//	vpsmctl health                 — run the same checks the server does at -check
//	vpsmctl reset-password <user>  — set a new password (prompted)
//	vpsmctl disable-2fa <user>     — clear TOTP secret for a user
//	vpsmctl logs [-n N]            — tail audit log
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/term"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/claudeacct"
	"server-control-panel/internal/config"
)

const deployScript = "/opt/panel/scripts/deploy.sh"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "status":
		mustRun(cmdStatus(args))
	case "rollback":
		mustRun(cmdRollback(args))
	case "health":
		mustRun(cmdHealth(args))
	case "reset-password":
		mustRun(cmdResetPassword(args))
	case "admin":
		mustRun(cmdAdmin(args))
	case "auth-emergency-reset":
		mustRun(cmdAuthEmergencyReset(args))
	case "mfa-emergency-reset":
		mustRun(cmdMFAEmergencyReset(args))
	case "disable-2fa", "reset-totp":
		mustRun(cmdDisable2FA(args))
	case "reset-recovery-totp":
		mustRun(cmdResetRecoveryTOTP(args))
	case "enroll-recovery-totp":
		mustRun(cmdEnrollRecoveryTOTP(args))
	case "logs":
		mustRun(cmdLogs(args))
	case "whatsapp":
		mustRun(cmdWhatsApp(args))
	case "videocall":
		mustRun(cmdVideocall(args))
	case "backup":
		mustRun(cmdBackup(args))
	case "restore":
		mustRun(cmdRestore(args))
	case "backups", "list-backups":
		mustRun(cmdBackupList(args))
	case "jira-seed":
		mustRun(cmdJiraSeed(args))
	case "jira-comment":
		mustRun(cmdJiraComment(args))
	case "jira-create":
		mustRun(cmdJiraCreate(args))
	case "secrets":
		mustRun(cmdSecrets(args))
	case "account":
		mustRun(cmdAccount(args))
	case "session-backup":
		mustRun(cmdSessionBackup(args))
	case "session-restore":
		mustRun(cmdSessionRestore(args))
	case "session-transcript":
		mustRun(cmdSessionTranscript(args))
	case "session-tokens":
		mustRun(cmdSessionTokens(args))
	case "agent-ship":
		mustRun(cmdAgentShip(args))
	case "agent-fixbuild":
		mustRun(cmdAgentFixbuild(args))
	case "agent-budget":
		mustRun(cmdAgentBudget(args))
	case "agent-permmode":
		mustRun(cmdAgentPermmode(args))
	case "app-create":
		mustRun(cmdAppCreate(args))
	case "app-deploy":
		mustRun(cmdAppDeploy(args))
	case "app-list":
		mustRun(cmdAppList(args))
	case "app-rollback":
		mustRun(cmdAppRollback(args))
	case "app-env":
		mustRun(cmdAppEnv(args))
	case "app-destroy":
		mustRun(cmdAppDestroy(args))
	case "app-catalog":
		mustRun(cmdAppCatalog(args))
	case "app-from-template":
		mustRun(cmdAppFromTemplate(args))
	case "app-preview-reap":
		mustRun(cmdAppPreviewReap(args))
	case "app-preview-teardown":
		mustRun(cmdAppPreviewTeardown(args))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `vpsmctl — admin/recovery CLI for vps-manager

Usage:
  vpsmctl status                 service state + last login
  vpsmctl health                 deep config/subsystem check
  vpsmctl rollback               revert to previous binary
  vpsmctl reset-password <user>  prompt + set new password (LOCAL bcrypt in config.json)
  vpsmctl admin list             list the system administrators (primary + flagged)
  vpsmctl admin add <user>       promote <user> to admin (full parity with the primary)
  vpsmctl admin remove <user>    revoke admin from <user> (the primary cannot be revoked)
  vpsmctl auth-emergency-reset <user>
                                 reset the password IN SUPABASE (auth.users) directly via psql —
                                 sovereign channel when Supabase is alive but the user
                                 lost access. Works with vpsmanager-v2 stopped.
  vpsmctl mfa-emergency-reset <user>
                                 wipe MFA factors IN SUPABASE (auth.mfa_factors) +
                                 delete local backup codes. Next login skips MFA
                                 until re-enrollment.
  vpsmctl reset-totp <user>      clear primary TOTP (next login re-enrolls)
  vpsmctl reset-recovery-totp <user>
                                 clear recovery TOTP (next /recovery access re-enrolls via SSH)
  vpsmctl enroll-recovery-totp <user>
                                 print QR + secret for recovery TOTP; user scans + confirms
  vpsmctl disable-2fa <user>     clear TOTP secret
  vpsmctl logs [-n N]            tail audit log (default 50)
  vpsmctl whatsapp <sub>         WAHA gateway control (status|qr|restart|logout|logs)
  vpsmctl videocall <sub>        TURN/STUN coturn control (init|status|start|stop|restart|logs)
  vpsmctl backup [--dest path] [--containers]
                                 tarball of live state (data/ + optional containers)
  vpsmctl restore --src file.tar.gz [--target dir]
                                 extract the backup to a staged dir — copy it by hand once validated
  vpsmctl backups                list backups in ~/
  vpsmctl secrets <sub>          credential vault (list|get|set|delete|export|import-env|import)
  vpsmctl account list           list available Claude accounts and current assignments
  vpsmctl account swap <id> [--session <name>]
                                 switch Claude account; --session persists a per-session override;
                                 prints "dir=<CLAUDE_CONFIG_DIR>" for the shell's cswap function
  vpsmctl session-transcript --session <uuid> [--home DIR] [--out FILE]
                                 export a Claude Code session transcript (JSONL) to readable
                                 markdown (use --cwd/--project to locate it by project)
  vpsmctl session-tokens [--session <uuid>|--cwd DIR|--project P|--all] [--json]
                                 token totals + cost (USD) per session/day from the Claude Code
                                 transcripts (table by default; --json for machines)
  vpsmctl agent-ship --session <name> [--user U] [--title T] [--no-pr] [--verify CMD]
                                 verifies (default go build ./...), commits, pushes the
                                 branch and opens a PR (gh) — or prints the compare URL. Never
                                 force-pushes; aborts if the verification fails.
  vpsmctl agent-fixbuild --session <name> [--cmd CMD] [--user U]
                                 runs the build (default go build ./...); on failure, spawns
                                 a Claude session in the worktree with the errors to fix.
                                 Prints the name of the session it created.
  vpsmctl agent-budget [--show | --set-daily N --set-monthly N --set-session N]
                                 spend ceilings (USD), ALERT-ONLY (daily/monthly/session).
                                 0 disables a ceiling. Alerts via notifications; no auto-halt.
  vpsmctl agent-permmode --session <name> --mode <plan|acceptEdits|default>
                                 sets Claude's permission mode for the session (empty = clear).
                                 Applied on the next spawn/restart via --permission-mode.
`)
}

func mustRun(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

// ---------- status ----------

func cmdStatus(args []string) error {
	props := []string{"MainPID", "ActiveState", "SubState", "ExecMainStartTimestamp", "NRestarts"}
	out, err := exec.Command("systemctl", "show", "vps-manager",
		"--property="+strings.Join(props, ",")).Output()
	if err != nil {
		return fmt.Errorf("systemctl: %w", err)
	}
	fmt.Print(string(out))

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "WARN: config load failed:", err)
		return nil
	}
	if cfg.LoadedFromBackup {
		fmt.Printf("ConfigLoadedFromBackup=%s\n", cfg.LoadedBackupName)
	}
	al, err := auth.NewAuditLog(filepath.Join(cfg.DataDir, "audit.log"))
	if err == nil {
		for _, e := range al.Tail(20) {
			if e.Action == "login.ok" {
				fmt.Printf("LastLogin=%s @ %d ip=%s\n", e.User, e.Time, e.IP)
				break
			}
		}
	}
	return nil
}

// ---------- admin (grant/revoke system-admin privileges) ----------

// cmdAdmin manages the system-admin set in config.json. `add`/`remove` mutate
// the per-user Admin flag and persist; the running daemon picks up the change
// on its next restart (config is read at boot), so a restart note is printed.
func cmdAdmin(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vpsmctl admin <list|add|remove> [user]")
	}
	sub := args[0]
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	switch sub {
	case "list":
		fmt.Printf("primary: %s\n", cfg.Primary)
		fmt.Printf("admins:  %s\n", strings.Join(cfg.Admins(), ", "))
		fmt.Printf("users:   %s\n", listUsers(cfg))
		return nil
	case "add", "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: vpsmctl admin %s <user>", sub)
		}
		user := args[1]
		if !cfg.HasUser(user) {
			return fmt.Errorf("user %q not found; known: %s", user, listUsers(cfg))
		}
		if err := cfg.SetAdmin(user, sub == "add"); err != nil {
			return err
		}
		if err := config.Save(cfg, configPath()); err != nil {
			return fmt.Errorf("save: %w", err)
		}
		verb := "promoted to admin"
		if sub == "remove" {
			verb = "admin revoked"
		}
		fmt.Printf("%s: %s\n", user, verb)
		fmt.Printf("admins now: %s\n", strings.Join(cfg.Admins(), ", "))
		fmt.Println("restart vps-manager to apply it to the running process: systemctl restart vps-manager")
		return nil
	default:
		return fmt.Errorf("unknown admin subcommand %q (use list|add|remove)", sub)
	}
}

// ---------- rollback ----------

func cmdRollback(args []string) error {
	if _, err := os.Stat(deployScript); err != nil {
		return fmt.Errorf("deploy script not found at %s: %w", deployScript, err)
	}
	cmd := exec.Command("bash", deployScript, "--rollback")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ---------- health ----------

func cmdHealth(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if cfg.LoadedFromBackup {
		fmt.Fprintf(os.Stderr, "WARN: config recovered from %s\n", cfg.LoadedBackupName)
	}
	if len(cfg.AllUsers()) == 0 {
		return fmt.Errorf("zero users in config")
	}
	if cfg.JWTSecret == "" {
		return fmt.Errorf("jwt_secret empty")
	}
	auditPath := filepath.Join(cfg.DataDir, "audit.log")
	af, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit log %s not writable: %w", auditPath, err)
	}
	_ = af.Close()
	fmt.Printf("OK: %d users (%d primary + %d additional), jwt set, audit writable\n",
		len(cfg.AllUsers()),
		boolToInt(cfg.Username != "" && cfg.PasswordHash != ""),
		len(cfg.Users))
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------- auth-emergency-reset (Supabase) ----------

// cmdAuthEmergencyReset resets a user's password directly in Supabase's
// auth.users, through psql inside the supabase-db container. The sovereign
// recovery channel when:
//   - Supabase is alive but the user forgot the password (no SMTP recovery)
//   - GoTrue is reachable but some specific /auth/v1/* route is stuck
//   - vpsmanager-v2 is stopped but the admin needs to be sure they can log
//     in once it comes up
//
// Reads the email from data/migration-uuid-map.json to map v2-username → email.
// Runs psql through docker exec — no psql binary is needed on the host.
//
// Audits into data/audit.log with action="auth.emergency_reset" + source="vpsmctl".
func cmdAuthEmergencyReset(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vpsmctl auth-emergency-reset <user>")
	}
	user := args[0]
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	uuidMapPath := filepath.Join(cfg.DataDir, "migration-uuid-map.json")
	uuidMap, err := auth.LoadUUIDMap(uuidMapPath)
	if err != nil {
		return fmt.Errorf("uuid map: %w", err)
	}
	if uuidMap == nil {
		return fmt.Errorf("uuid map not found at %s — run Phase 2 migration first", uuidMapPath)
	}
	email, supabaseUUID, ok := uuidMap.Lookup(user)
	if !ok {
		return fmt.Errorf("user %q not mapped in %s", user, uuidMapPath)
	}
	fmt.Printf("Target: %s (uuid=%s) → email=%s\n", user, supabaseUUID, email)
	fmt.Println("This will UPDATE auth.users.encrypted_password directly via psql.")
	fmt.Println("All active sessions for this user will continue valid until JWT expiry.")
	fmt.Print("Continue? [y/N]: ")
	var confirm string
	_, _ = fmt.Scanln(&confirm)
	if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
		return fmt.Errorf("aborted by operator")
	}

	pwd, err := readPasswordTwice("new password for " + user + " (Supabase)")
	if err != nil {
		return err
	}
	if len(pwd) < 8 {
		return fmt.Errorf("password too short (min 8 chars)")
	}

	// SQL: bcrypt hash through pgcrypto's crypt() (the extension is already
	// available in the auth schema — GoTrue depends on it). The password goes in
	// dollar-quoted with the tag $vpsmrst$ — which escapes any content (Postgres
	// treats it as an opaque literal until it meets the identical closing tag).
	// The tag is swapped if the text contains $vpsmrst$ — a collision is
	// 1-in-2^128 but the defence is trivial.
	tag := "vpsmrst"
	if strings.Contains(pwd, "$"+tag+"$") {
		tag = "vpsmrst2"
	}
	sqlScript := fmt.Sprintf(
		"UPDATE auth.users SET encrypted_password = crypt($%s$%s$%s$, gen_salt('bf', 12)), updated_at = now() WHERE id = '%s';\n",
		tag, pwd, tag, supabaseUUID,
	)
	cmd := exec.Command("docker", "exec", "-i",
		"supabase-db", "psql", "-U", "postgres", "-d", "postgres",
		"-v", "ON_ERROR_STOP=1",
	)
	cmd.Stdin = strings.NewReader(sqlScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql failed: %s — %w", strings.TrimSpace(string(out)), err)
	}
	fmt.Println(strings.TrimSpace(string(out)))

	// Audit log entry — best-effort. A failure here does not undo the reset
	// (it already completed in the DB).
	if al, alErr := auth.NewAuditLog(filepath.Join(cfg.DataDir, "audit.log")); alErr == nil {
		al.Append(auth.Event{
			Time:   time.Now().Unix(),
			User:   user,
			Action: "auth.emergency_reset",
			Target: "supabase:vpsmctl",
			IP:     "localhost",
		})
	}
	fmt.Printf("✔ password reset for %s (email=%s). Active JWTs valid until expiry; new login picks up immediately.\n", user, email)
	return nil
}

// ---------- reset-password ----------

func cmdResetPassword(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vpsmctl reset-password <user>")
	}
	user := args[0]
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	// Confirm user exists.
	found := false
	for _, u := range cfg.AllUsers() {
		if u.Username == user {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("user %q not found; known: %s", user, listUsers(cfg))
	}

	pwd, err := readPasswordTwice("new password for " + user)
	if err != nil {
		return err
	}
	if len(pwd) < 8 {
		return fmt.Errorf("password must be 8+ chars")
	}
	hash, err := auth.HashPassword(pwd)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	if !cfg.SetPassword(user, hash) {
		return fmt.Errorf("SetPassword returned false unexpectedly")
	}
	if err := config.Save(cfg, configPath()); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	fmt.Printf("password updated for %s — restart vps-manager (or wait) to invalidate in-memory state\n", user)
	return nil
}

// ---------- disable-2fa ----------

func cmdDisable2FA(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vpsmctl disable-2fa <user>")
	}
	user := args[0]
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if !cfg.SetTOTPSecret(user, "") {
		return fmt.Errorf("user %q not found; known: %s", user, listUsers(cfg))
	}
	if err := config.Save(cfg, configPath()); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	fmt.Printf("2FA disabled for %s\n", user)
	return nil
}

// ---------- reset-recovery-totp ----------

func cmdResetRecoveryTOTP(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vpsmctl reset-recovery-totp <user>")
	}
	user := args[0]
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if !cfg.SetRecoveryTOTPSecret(user, "") {
		return fmt.Errorf("user %q not found; known: %s", user, listUsers(cfg))
	}
	if err := config.Save(cfg, configPath()); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	fmt.Printf("recovery TOTP cleared for %s — next /recovery access requires re-enrollment via vpsmctl enroll-recovery-totp\n", user)
	return nil
}

// ---------- enroll-recovery-totp ----------
//
// Generates a fresh recovery TOTP secret, prints the otpauth URL (the user can
// paste into Aegis / 1Password / Google Authenticator manually) plus an
// ASCII QR. Prompts the user to scan + type the current code; persists only
// after validation. Safe to re-run — overwrites whatever was there.

func cmdEnrollRecoveryTOTP(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vpsmctl enroll-recovery-totp <user>")
	}
	user := args[0]
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if !userExists(cfg, user) {
		return fmt.Errorf("user %q not found; known: %s", user, listUsers(cfg))
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "vps-manager (recovery)",
		AccountName: user,
	})
	if err != nil {
		return fmt.Errorf("totp generate: %w", err)
	}
	fmt.Printf("\n=== Recovery TOTP enrollment for %s ===\n\n", user)
	fmt.Printf("Secret (base32): %s\n", key.Secret())
	fmt.Printf("otpauth URL:     %s\n\n", key.URL())
	fmt.Printf("Scan the URL above (use a QR generator if your phone needs a QR), or\n")
	fmt.Printf("type the secret manually into Google Authenticator / Aegis / 1Password.\n")
	fmt.Printf("Use a separate slot from the primary 2FA (label it 'recovery').\n\n")
	fmt.Printf("Now enter the current 6-digit code to confirm: ")
	reader := bufio.NewReader(os.Stdin)
	code, _ := reader.ReadString('\n')
	code = strings.TrimSpace(code)
	if !totp.Validate(code, key.Secret()) {
		return fmt.Errorf("code didn't match — secret NOT saved; re-run to try again")
	}
	if !cfg.SetRecoveryTOTPSecret(user, key.Secret()) {
		return fmt.Errorf("SetRecoveryTOTPSecret returned false unexpectedly")
	}
	if err := config.Save(cfg, configPath()); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	fmt.Printf("\n✓ recovery TOTP enrolled for %s — /recovery now accepts this secret\n", user)
	return nil
}

func userExists(cfg *config.Config, user string) bool {
	for _, u := range cfg.AllUsers() {
		if u.Username == user {
			return true
		}
	}
	return false
}

// ---------- logs ----------

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	n := fs.Int("n", 50, "number of entries to show")
	_ = fs.Parse(args)
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	al, err := auth.NewAuditLog(filepath.Join(cfg.DataDir, "audit.log"))
	if err != nil {
		return err
	}
	for _, e := range al.Tail(*n) {
		fmt.Printf("%d  %-22s  %-22s  %s  %s\n", e.Time, e.User, e.Action, e.Target, e.IP)
	}
	return nil
}

// ---------- helpers ----------

func configPath() string {
	if p := os.Getenv("VPSM_CONFIG"); p != "" {
		return p
	}
	return "/opt/panel/data/config.json"
}

func listUsers(cfg *config.Config) string {
	names := make([]string, 0, len(cfg.AllUsers()))
	for _, u := range cfg.AllUsers() {
		names = append(names, u.Username)
	}
	return strings.Join(names, ", ")
}

func readPasswordTwice(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	p1, err := readPasswordOrLine()
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "confirm: ")
	p2, err := readPasswordOrLine()
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", fmt.Errorf("passwords do not match")
	}
	return p1, nil
}

// ---------- account ----------

func cmdAccount(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vpsmctl account list | swap <id> [--session <name>]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	store, err := claudeacct.Open(cfg.DataDir, "")
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		accounts := store.Accounts()
		current := store.AccountIDFor(claudeacct.ConsumerTerminal)
		fmt.Printf("%-12s %-20s %s\n", "ID", "LABEL", "CONFIG_DIR")
		for _, a := range accounts {
			dir := a.ConfigDir
			if dir == "" {
				dir = "(default /root/.claude)"
			}
			marker := ""
			if a.ID == current {
				marker = " ◀ terminal"
			}
			fmt.Printf("%-12s %-20s %s%s\n", a.ID, a.Label, dir, marker)
		}
		return nil

	case "swap":
		if len(args) < 2 {
			return fmt.Errorf("usage: vpsmctl account swap <id> [--session <name>]")
		}
		accountID := args[1]
		fs := flag.NewFlagSet("account-swap", flag.ContinueOnError)
		session := fs.String("session", "", "session name (persists a per-session override)")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}

		if *session != "" {
			if err := store.SetSessionAccount(*session, accountID); err != nil {
				return err
			}
		} else {
			if err := store.Assign(claudeacct.ConsumerTerminal, accountID); err != nil {
				return err
			}
		}

		dir := store.ConfigDirForSession(*session, claudeacct.ConsumerTerminal)
		// Output "dir=<value>" — consumed by the shell's cswap function to export
		// CLAUDE_CONFIG_DIR in the parent process itself (a subprocess cannot do that).
		fmt.Printf("dir=%s\n", dir)
		return nil

	default:
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

// readPasswordOrLine hides input when stdin is a TTY; otherwise reads a line
// (useful for piping in scripts/tests).
func readPasswordOrLine() (string, error) {
	fd := int(syscall.Stdin)
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	s := bufio.NewScanner(os.Stdin)
	if !s.Scan() {
		return "", s.Err()
	}
	return s.Text(), nil
}
