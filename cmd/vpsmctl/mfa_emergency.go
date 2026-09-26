// mfa_emergency.go — vpsmctl mfa-emergency-reset.
//
// Resets a user's MFA through:
//  1. SQL against auth.mfa_factors (the admin channel via supabase-db)
//  2. Removal of the local backup-codes file
//
// The UUID lookup goes through data/migration-uuid-map.json.
package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

// mfaResetSQL returns the SQL script with the :'uuid' placeholder (bound by
// psql -v). Before: an inline fmt.Sprintf with supabaseUUID — functional,
// because the UUID comes from an admin-controlled file, but SQL hygiene calls
// for binding by variable. psql substitutes :'uuid' with the escaped literal.
func mfaResetSQL() string {
	const tableName = "auth.mfa_factors"
	return fmt.Sprintf("DELETE FROM %s WHERE user_id = :'uuid';\n", tableName)
}

func cmdMFAEmergencyReset(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vpsmctl mfa-emergency-reset <user>")
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
		return fmt.Errorf("uuid map not found at %s", uuidMapPath)
	}
	email, supabaseUUID, ok := uuidMap.Lookup(user)
	if !ok {
		return fmt.Errorf("user %q not mapped", user)
	}
	fmt.Printf("Target: %s (uuid=%s) → email=%s\n", user, supabaseUUID, email)
	fmt.Println("This will remove all MFA factors and backup codes for this user.")
	fmt.Println("Next login will skip MFA challenge entirely until re-enrollment.")
	fmt.Print("Continue? [y/N]: ")
	var confirm string
	_, _ = fmt.Scanln(&confirm)
	if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
		return fmt.Errorf("aborted by operator")
	}

	// 1. Run the SQL through psql with the :'uuid' binding. psql -v substitutes
	// before sending to the server — the literal is escaped by the client, not
	// concatenated textually here.
	sqlScript := mfaResetSQL()
	cmd := exec.Command("docker", "exec", "-i",
		"supabase-db", "psql", "-U", "postgres", "-d", "postgres",
		"-v", "ON_ERROR_STOP=1",
		"-v", "uuid="+supabaseUUID,
	)
	cmd.Stdin = strings.NewReader(sqlScript)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql failed: %s — %w", strings.TrimSpace(string(out)), err)
	}
	fmt.Println(strings.TrimSpace(string(out)))

	// 2. Delete the backup-codes file.
	bcPath := auth.BackupCodesPath(cfg.DataDir, user)
	bcStore := auth.NewBackupCodesStore(bcPath)
	if delErr := bcStore.Delete(); delErr != nil {
		fmt.Printf("WARN: backup codes delete failed: %v\n", delErr)
	} else {
		fmt.Printf("Backup codes file removed: %s\n", bcPath)
	}

	// 3. Audit.
	if al, alErr := auth.NewAuditLog(filepath.Join(cfg.DataDir, "audit.log")); alErr == nil {
		al.Append(auth.Event{
			Time:   time.Now().Unix(),
			User:   user,
			Action: "mfa.emergency_reset",
			Target: "supabase:vpsmctl",
			IP:     "localhost",
		})
	}
	fmt.Printf("✔ MFA reset for %s (email=%s). Next login proceeds without MFA challenge.\n", user, email)
	return nil
}
