package config

import (
	"path/filepath"
	"testing"
)

func TestGitReposRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	orig := &Config{
		SchemaVersion: CurrentSchemaVersion,
		Listen:        ":8788",
		DataDir:       dir,
		JWTSecret:     "x",
		GitRepos: []GitRepo{
			{ID: "server-control-panel", Path: "/opt/panel", Name: "Server Control Panel", Policy: "read-only"},
			{ID: "northwind-web", Path: "/root/projects/northwind-web", Name: "Northwind Web",
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
