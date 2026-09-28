package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dataDirWith(t *testing.T, appsJSON string) string {
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
	cfg := `{"listen":":8765","data_dir":"` + dataDir + `","jwt_secret":"test","schema_version":2}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	t.Setenv("PANEL_CONFIG", cfgPath)
	return dataDir
}

func shaOf(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestPanelctlRefusesUnknownEnvelope(t *testing.T) {
	dataDir := dataDirWith(t, `{"schema_version":3,"projects":[],"deployments":[]}`)
	appsFile := filepath.Join(dataDir, "deploy", "apps.json")
	before := shaOf(t, appsFile)

	st, err := openDeployStore()
	if err == nil {
		t.Fatalf("openDeployStore ACCEPTED an unknown envelope (store=%v)", st != nil)
	}
	if !strings.Contains(err.Error(), "panelctl") {
		t.Fatalf("error does not name the 'panelctl' binary: %v", err)
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error does not mention schema_version: %v", err)
	}
	if after := shaOf(t, appsFile); after != before {
		t.Fatalf("the refusal MODIFIED apps.json (sha %s → %s)", before, after)
	}
}

func TestPanelctlRefusesV1AndNamesMigrator(t *testing.T) {
	dataDir := dataDirWith(t, `[{"name":"hello","branch":"main"}]`)
	appsFile := filepath.Join(dataDir, "deploy", "apps.json")
	before := shaOf(t, appsFile)

	if _, err := openDeployStore(); err == nil {
		t.Fatalf("openDeployStore ACCEPTED a v1 array — panelctl must neither migrate it nor write over it")
	} else {
		if !strings.Contains(err.Error(), "panelctl") || !strings.Contains(err.Error(), "schema_version") {
			t.Fatalf("incomplete message: %v", err)
		}
		if !strings.Contains(err.Error(), "cmd/server") && !strings.Contains(err.Error(), "server-control-panel") {
			t.Fatalf("message does not say WHO migrates: %v", err)
		}
	}
	if after := shaOf(t, appsFile); after != before {
		t.Fatalf("the refusal MODIFIED apps.json")
	}
}

func TestPanelctlAcceptsV2AndFreshInstall(t *testing.T) {
	dataDirWith(t, `{"schema_version":2,"projects":[],"deployments":[]}`)
	if _, err := openDeployStore(); err != nil {
		t.Fatalf("the current envelope was refused: %v", err)
	}

	dataDirWith(t, "")
	if _, err := openDeployStore(); err != nil {
		t.Fatalf("fresh install (apps.json missing) was refused: %v", err)
	}
}
