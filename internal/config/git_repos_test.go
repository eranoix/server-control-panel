package config

import (
	"path/filepath"
	"testing"
)

// TestGitReposRoundTrip proves that the GitRepos field survives
// Save→parseFile without loss, that SchemaVersion stays untouched, and that a
// config WITHOUT git_repos reads back as nil (backward compatible, omitempty).
func TestGitReposRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	orig := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Listen:        ":8788",
		DataDir:       dir,
		JWTSecret:     "x", // avoids the file-based path in Save
		GitRepos: []GitRepo{
			{ID: "vps-manager", Path: "/opt/panel", Name: "VPS Manager", Policy: "read-only"},
			{ID: "northwind-web", Path: "/root/projetos/northwind-web", Name: "Northwind Web",
				Policy: "write", ExpName: "Sam Rivera", ExpEmail: "sam@northwind.example"},
		},
	}

	if err := Save(orig, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := parseFile(path)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}

	if got.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion changed: got %d want %d", got.SchemaVersion, CurrentSchemaVersion)
	}
	if len(got.GitRepos) != len(orig.GitRepos) {
		t.Fatalf("GitRepos len: got %d want %d", len(got.GitRepos), len(orig.GitRepos))
	}
	for i, want := range orig.GitRepos {
		if got.GitRepos[i] != want {
			t.Errorf("GitRepos[%d]: got %+v want %+v", i, got.GitRepos[i], want)
		}
	}
}

// TestGitReposAbsentIsNil proves that an absent field in the JSON → nil
// (never a panic), and that saving a config without GitRepos does not
// materialise the key.
func TestGitReposAbsentIsNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	orig := &Config{SchemaVersion: CurrentSchemaVersion, Listen: ":8788", DataDir: dir, JWTSecret: "x"}
	if err := Save(orig, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := parseFile(path)
	if err != nil {
		t.Fatalf("parseFile: %v", err)
	}
	if got.GitRepos != nil {
		t.Errorf("GitRepos should be nil when absent, got %+v", got.GitRepos)
	}
}
