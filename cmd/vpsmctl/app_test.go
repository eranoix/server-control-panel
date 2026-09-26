package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dataDirCom assembles a DataDir holding the requested apps.json plus a
// config.json pointing at it, and makes openDeployStore's config.Load() see it
// through VPSM_CONFIG. Returns the dataDir.
func dataDirCom(t *testing.T, appsJSON string) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "deploy"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if appsJSON != "" {
		if err := os.WriteFile(filepath.Join(dataDir, "deploy", "apps.json"), []byte(appsJSON), 0o600); err != nil {
			t.Fatalf("write apps.json: %v", err)
		}
	}
	cfgPath := filepath.Join(dataDir, "config.json")
	cfg := `{"listen":":8765","data_dir":"` + dataDir + `","jwt_secret":"teste","schema_version":2}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv("VPSM_CONFIG", cfgPath)
	return dataDir
}

func shaDo(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// TestVpsmctlRefusesUnknownEnvelope: vpsmctl NEVER migrates apps.json and
// NEVER rewrites an envelope it does not understand. Faced with one, it refuses
// to open the store — naming the binary — and the file stays byte for byte as
// it was.
//
// This is the guard rail against the most likely data-loss mode here: the
// post-receive hook runs a vpsmctl that may be OLDER than the server, and an
// old vpsmctl that opened the store would rewrite the v2 envelope as a v1 array.
func TestVpsmctlRefusesUnknownEnvelope(t *testing.T) {
	dataDir := dataDirCom(t, `{"schema_version":3,"projects":[],"deployments":[]}`)
	appsFile := filepath.Join(dataDir, "deploy", "apps.json")
	antes := shaDo(t, appsFile)

	st, err := openDeployStore()
	if err == nil {
		t.Fatalf("openDeployStore ACCEPTED an unknown envelope (store=%v)", st != nil)
	}
	if !strings.Contains(err.Error(), "vpsmctl") {
		t.Fatalf("error does not name the 'vpsmctl' binary: %v", err)
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error does not mention schema_version: %v", err)
	}
	if depois := shaDo(t, appsFile); depois != antes {
		t.Fatalf("the refusal MODIFIED apps.json (sha %s → %s)", antes, depois)
	}
}

// TestVpsmctlRefusesV1AndNamesMigrator: a v1 array is a refusal too — the
// migration belongs to the server, never to vpsmctl. And the message has to say
// which binary fixes it, or the operator just sees `git push` rejected with no
// idea what to do.
func TestVpsmctlRefusesV1AndNamesMigrator(t *testing.T) {
	dataDir := dataDirCom(t, `[{"name":"hello","branch":"main"}]`)
	appsFile := filepath.Join(dataDir, "deploy", "apps.json")
	antes := shaDo(t, appsFile)

	if _, err := openDeployStore(); err == nil {
		t.Fatalf("openDeployStore ACCEPTED a v1 array — vpsmctl must neither migrate it nor write over it")
	} else {
		if !strings.Contains(err.Error(), "vpsmctl") || !strings.Contains(err.Error(), "schema_version") {
			t.Fatalf("incomplete message: %v", err)
		}
		if !strings.Contains(err.Error(), "cmd/server") && !strings.Contains(err.Error(), "vps-manager") {
			t.Fatalf("message does not say WHO migrates: %v", err)
		}
	}
	if depois := shaDo(t, appsFile); depois != antes {
		t.Fatalf("the refusal MODIFIED apps.json")
	}
}

// TestVpsmctlAceitaV2ECriacaoNova: the guard must not turn into a closed gate —
// the current envelope and a fresh installation still open.
func TestVpsmctlAceitaV2ECriacaoNova(t *testing.T) {
	dataDirCom(t, `{"schema_version":2,"projects":[],"deployments":[]}`)
	if _, err := openDeployStore(); err != nil {
		t.Fatalf("the current envelope was refused: %v", err)
	}

	dataDirCom(t, "")
	if _, err := openDeployStore(); err != nil {
		t.Fatalf("fresh install (apps.json missing) was refused: %v", err)
	}
}
