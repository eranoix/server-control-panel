package gameservers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSource map[string]NodeTarget

func (f fakeSource) NodeByID(id string) (NodeTarget, bool) {
	d, ok := f[id]
	return d, ok
}

func TestInventoryMigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gameservers.json")
	original := `[{"id":"game-b","name":"Enshrouded","game":"enshrouded","container":"game-b","root":"/opt/game-b","address":""}]`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := MigrateInventoryToNode(path)
	if err != nil {
		t.Fatalf("first round: %v", err)
	}
	if !changed {
		t.Fatal("the first round should have migrated — the record had no such field")
	}
	after1, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	changed2, err := MigrateInventoryToNode(path)
	if err != nil {
		t.Fatalf("second round: %v", err)
	}
	if changed2 {
		t.Error("the second round REWROTE a file that was already correct")
	}
	after2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after1) != string(after2) {
		t.Errorf("MIGRATION IS NOT IDEMPOTENT:\n--- 1st ---\n%s\n--- 2nd ---\n%s", after1, after2)
	}
	if !strings.Contains(string(after1), `"node"`) {
		t.Errorf("the node field was not added: %s", after1)
	}
}

func TestMigrationPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gameservers.json")
	original := `[{"id":"game-b","game":"enshrouded","futureField":{"a":1,"b":[2,3]},"notes":"do not delete me"}]`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateInventoryToNode(path); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(after, &records); err != nil {
		t.Fatalf("the migration produced invalid JSON: %v\n%s", err, after)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, saw %d", len(records))
	}
	fut, has := records[0]["futureField"]
	if !has {
		t.Fatalf("UNKNOWN FIELD LOST IN THE MIGRATION — silent data loss: %s", after)
	}
	var v map[string]any
	if err := json.Unmarshal(fut, &v); err != nil {
		t.Fatalf("unknown field corrupted: %v", err)
	}
	if _, ok := v["b"]; !ok {
		t.Errorf("unknown field arrived incomplete: %s", fut)
	}
	if string(records[0]["notes"]) != `"do not delete me"` {
		t.Errorf("known field altered: %s", records[0]["notes"])
	}
}

func TestMigrationKeepsCopyAndMissingFileIsNotError(t *testing.T) {
	dir := t.TempDir()

	if changed, err := MigrateInventoryToNode(filepath.Join(dir, "missing.json")); err != nil || changed {
		t.Errorf("a missing file should be a silent no-op, got changed=%v err=%v", changed, err)
	}

	path := filepath.Join(dir, "gameservers.json")
	original := `[{"id":"x","game":"enshrouded"}]`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateInventoryToNode(path); err != nil {
		t.Fatal(err)
	}
	dup, err := os.ReadFile(path + preNodeBackupSuffix)
	if err != nil {
		t.Fatalf("the copy of the previous file was not kept: %v", err)
	}
	if string(dup) != original {
		t.Errorf("the copy is not the previous content:\n%s", dup)
	}
}

func TestMigrationDoesNotWriteInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gameservers.json")
	broken := `[{"id":"x",`
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateInventoryToNode(path); err == nil {
		t.Error("invalid JSON should abort the migration")
	}
	after, _ := os.ReadFile(path)
	if string(after) != broken {
		t.Errorf("the file was TOUCHED despite the abort: %s", after)
	}
	if _, err := os.Stat(path + preNodeBackupSuffix); err == nil {
		t.Error("a copy was created even with the migration aborted")
	}
}

func TestServerWithoutNodeRejected(t *testing.T) {
	source := fakeSource{"games": {Name: "games", Transport: TransportAgent, Base: "http://x:1", Token: "t"}}
	s := Server{ID: "game-b", Name: "Enshrouded"}

	_, err := ResolveTarget(s, source)
	if err == nil {
		t.Fatal("SERVER WITH NO NODE WAS ACCEPTED — the operation would go to a node chosen by omission")
	}
	if !strings.Contains(err.Error(), "game-b") {
		t.Errorf("the error has to NAME the server, so the operator knows which one to register: %v", err)
	}
	if !strings.Contains(err.Error(), "node") {
		t.Errorf("the error has to say WHAT to do (register the field): %v", err)
	}
}

func TestResolveServerToNode(t *testing.T) {
	source := fakeSource{
		"games": {Name: "games", Transport: TransportAgent, Base: "http://x:1", Token: "t"},
		"apps":  {Name: "apps", Transport: TransportPVEAPI},
	}
	t.Run("existing node", func(t *testing.T) {
		d, err := ResolveTarget(Server{ID: "game-b", No: "games"}, source)
		if err != nil {
			t.Fatal(err)
		}
		if d.Name != "games" || d.Transport != TransportAgent {
			t.Errorf("resolved to the wrong node: %+v", d)
		}
	})
	t.Run("a nonexistent node names the id it looked for", func(t *testing.T) {
		_, err := ResolveTarget(Server{ID: "game-b", No: "ghost"}, source)
		if err == nil {
			t.Fatal("a nonexistent node should fail")
		}
		if !strings.Contains(err.Error(), "ghost") {
			t.Errorf("the error has to name the ID it looked for: %v", err)
		}
	})
	t.Run("no source", func(t *testing.T) {
		if _, err := ResolveTarget(Server{ID: "game-b", No: "games"}, nil); err == nil {
			t.Error("a nil source should fail, not fall back to local")
		}
	})
}

func TestNodeWithInvalidTransport(t *testing.T) {
	cases := map[string]string{
		"crooked value": "banana",
		"empty":         "",
	}
	for name, transport := range cases {
		t.Run(name, func(t *testing.T) {
			source := fakeSource{"games": {Name: "games", Transport: transport}}
			_, err := ResolveTarget(Server{ID: "game-b", No: "games"}, source)
			if err == nil {
				t.Fatalf("transport %q should fail instead of picking a back end", transport)
			}
			if transport != "" && !strings.Contains(err.Error(), transport) {
				t.Errorf("the error has to NAME the invalid value: %v", err)
			}
		})
	}
}

func TestResolverHasNoImplicitDefault(t *testing.T) {
	empties := []struct {
		name   string
		s      Server
		source NodeSource
	}{
		{"all empty", Server{}, fakeSource{}},
		{"id only", Server{ID: "x"}, fakeSource{}},
		{"empty node with a full source", Server{ID: "x"}, fakeSource{"games": {Name: "games", Transport: TransportAgent, Base: "http://x:1", Token: "t"}}},
	}
	for _, c := range empties {
		t.Run(c.name, func(t *testing.T) {
			d, err := ResolveTarget(c.s, c.source)
			if err == nil {
				t.Fatalf("IMPLICIT DEFAULT: resolved to %+v with no node declared", d)
			}
		})
	}
}

func TestSaveInventoryAcceptsServerOnOtherNode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gameservers.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(dir, nil)

	remote := []Server{{
		ID: "remote", Name: "Remote", Game: "enshrouded",
		Container: "c-remote", Root: "/opt/does-not-exist-on-this-host", No: "games",
	}}
	if err := m.SaveInventory(remote); err != nil {
		t.Fatalf("a server with a node should be accepted (the root lives on the node, not here): %v", err)
	}

	local := []Server{{
		ID: "local", Name: "Local", Game: "enshrouded",
		Container: "c-local", Root: "/opt/also-does-not-exist", No: "",
	}}
	err := m.SaveInventory(local)
	if err == nil {
		t.Fatal("a server with NO node and a nonexistent root was accepted — the local check disappeared")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("the error does not name the problem: %v", err)
	}
}
