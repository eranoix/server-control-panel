package queue

import (
	"os"
	"path/filepath"
	"testing"
)

// validContainerName guards the docker_restart runner against shell-metachar /
// path injection in a scheduled container name.
func TestValidContainerName(t *testing.T) {
	ok := []string{"nginx", "my_app", "web.1", "a-b-c", "0container", "App_1.2-3"}
	for _, s := range ok {
		if !validContainerName(s) {
			t.Errorf("expected valid: %q", s)
		}
	}
	bad := []string{"", "-leading", ".dot", "_under", "a/b", "a;b", "a b", "a|b", "a$b", "a`b", "a&b"}
	for _, s := range bad {
		if validContainerName(s) {
			t.Errorf("expected invalid: %q", s)
		}
	}
	// Oversized rejected.
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	if validContainerName(string(long)) {
		t.Error("expected oversized name to be invalid")
	}
}

// The new restart runners are primary-only (host-impacting).
func TestRestartRunnersPrimaryOnly(t *testing.T) {
	if (DockerRestartRunner{}).AuthorizedFor("alice", false) {
		t.Error("docker_restart must be primary-only")
	}
	if !(DockerRestartRunner{}).AuthorizedFor("alice", true) {
		t.Error("docker_restart should allow primary")
	}
	if (DockerComposeRestartRunner{}).AuthorizedFor("alice", false) {
		t.Error("docker_compose_restart must be primary-only")
	}
	if !(DockerComposeRestartRunner{}).AuthorizedFor("alice", true) {
		t.Error("docker_compose_restart should allow primary")
	}
}

// validUnitName guards systemd_restart against injection in a scheduled unit.
func TestValidUnitName(t *testing.T) {
	ok := []string{"nginx.service", "vpsm-whatsapp@sam.service", "docker", "a_b.timer"}
	for _, s := range ok {
		if !validUnitName(s) {
			t.Errorf("expected valid unit: %q", s)
		}
	}
	bad := []string{"", "-x", ".x", "a/b", "a;b", "a b", "a|b", "a$b"}
	for _, s := range bad {
		if validUnitName(s) {
			t.Errorf("expected invalid unit: %q", s)
		}
	}
}

// The new ops + security/audit runners are all primary-only.
func TestNewRunnersPrimaryOnly(t *testing.T) {
	runners := []Runner{
		SystemdRestartRunner{}, DockerPruneRunner{}, HTTPCheckRunner{},
		SSLCheckRunner{}, DiskCheckRunner{}, SecurityAuditRunner{}, RootkitScanRunner{},
		IntegrityCheckRunner{}, TrivyScanRunner{}, Fail2banReportRunner{}, AuditReportRunner{}, CleanupRunner{},
		CertRenewRunner{}, RcloneSyncRunner{}, DockerComposeUpRunner{}, GitPullRunner{}, AptUpdateCheckRunner{}, RebootRunner{},
		DBBackupRunner{},
	}
	for _, r := range runners {
		if r.AuthorizedFor("alice", false) {
			t.Errorf("%s must be primary-only", r.Kind())
		}
		if !r.AuthorizedFor("alice", true) {
			t.Errorf("%s should allow primary", r.Kind())
		}
	}
}

// validDBName guards db_backup against injection in the database name.
func TestValidDBName(t *testing.T) {
	for _, s := range []string{"meuapp", "wordpress", "app_prod", "db-1", "site.db"} {
		if !validDBName(s) {
			t.Errorf("expected valid db: %q", s)
		}
	}
	for _, s := range []string{"", "a b", "a;drop", "a/b", "a'b", "a`b"} {
		if validDBName(s) {
			t.Errorf("expected invalid db: %q", s)
		}
	}
}

// validHost guards ssl_check against junk before net/tls.
func TestValidHost(t *testing.T) {
	for _, s := range []string{"exemplo.com", "sub.dom.io", "10.0.0.1", "a-b.example"} {
		if !validHost(s) {
			t.Errorf("expected valid host: %q", s)
		}
	}
	for _, s := range []string{"", "has space", "a/b", "with\ttab", "x\ny"} {
		if validHost(s) {
			t.Errorf("expected invalid host: %q", s)
		}
	}
}

// pruneBackups keeps the newest N per target and never touches other targets.
func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()
	// 4 "all" archives (sortable stamps) + 2 "config" archives.
	for _, n := range []string{
		"vpsm-backup-all-20260101-010101.tgz",
		"vpsm-backup-all-20260102-010101.tgz",
		"vpsm-backup-all-20260103-010101.tgz",
		"vpsm-backup-all-20260104-010101.tgz",
		"vpsm-backup-config-20260101-010101.tgz",
		"vpsm-backup-config-20260102-010101.tgz",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := pruneBackups(dir, "all", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %v, want 2 oldest", removed)
	}
	// The two oldest "all" are gone; newest two remain; config untouched.
	mustGone := []string{"vpsm-backup-all-20260101-010101.tgz", "vpsm-backup-all-20260102-010101.tgz"}
	for _, n := range mustGone {
		if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned", n)
		}
	}
	mustStay := []string{"vpsm-backup-all-20260104-010101.tgz", "vpsm-backup-config-20260101-010101.tgz", "vpsm-backup-config-20260102-010101.tgz"}
	for _, n := range mustStay {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s should have survived: %v", n, err)
		}
	}
	// keep >= count → no-op.
	r2, _ := pruneBackups(dir, "config", 5)
	if len(r2) != 0 {
		t.Errorf("keep>=count should remove nothing, got %v", r2)
	}
}
