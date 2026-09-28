package inventory

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func sampleNode(id string) Node {
	return Node{ID: id, Name: id, Transport: TransportPVEAPI, Kind: NodeKindGuest}
}

func idsDe(inv Inventory) []string {
	out := make([]string, 0, len(inv.Nodes))
	for _, n := range inv.Nodes {
		out = append(out, n.ID)
	}
	sort.Strings(out)
	return out
}

func TestStoreOpenMissingIsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open with a clean directory: %v", err)
	}
	inv, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(inv.Nodes) != 0 {
		t.Fatalf("expected an empty inventory, got %d nodes", len(inv.Nodes))
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("Open must NOT write a file on its own (%v)", err)
	}
}

func TestStoreDurableWrite(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Replace(func(inv *Inventory) {
		inv.Nodes = append(inv.Nodes, sampleNode("lxc/207"), sampleNode("qemu/208"))
	}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if strings.HasSuffix(e.Name(), ".new") || strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("orphan temporary on disk: %s", e.Name())
		}
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatalf("inventory.json does not exist after the Replace (%v); directory: %v", err, names)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reOpen: %v", err)
	}
	inv, err := s2.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := idsDe(inv); !reflect_DeepEqualStrings(got, []string{"lxc/207", "qemu/208"}) {
		t.Fatalf("reread set = %v, want [lxc/207 qemu/208]", got)
	}
	if inv.SchemaVersion != SchemaVersion {
		t.Fatalf("persisted schema_version = %d, want %d", inv.SchemaVersion, SchemaVersion)
	}

	inv.Nodes[0].Name = "hijacked"
	inv2, err := s2.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot 2: %v", err)
	}
	if inv2.Nodes[0].Name == "hijacked" {
		t.Fatal("Snapshot returned the internal state, not a copy")
	}
}

func reflect_DeepEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestStoreCorruptFile(t *testing.T) {
	cases := map[string]string{
		"truncated": `{"schema_version":1,"nodes":[{"id":"lxc/2`,
		"empty":     "",
		"garbage":   "not json",
		"wrongtype": `["this is a list, not the envelope"]`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "inventory"), 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "inventory", "inventory.json")
			if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Open(dir)
			if err == nil {
				t.Fatalf("Open ACCEPTED inventory %s — an empty inventory in place of an error erases data", name)
			}
			if !strings.Contains(err.Error(), target) {
				t.Fatalf("the error does not name the path %q: %v", target, err)
			}
		})
	}

	t.Run("future-version", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "inventory"), 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, "inventory", "inventory.json")
		if err := os.WriteFile(target, []byte(`{"schema_version":99,"nodes":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(dir)
		if err == nil {
			t.Fatal("Open accepted an envelope from a future version")
		}
		if !strings.Contains(err.Error(), "99") || !strings.Contains(err.Error(), target) {
			t.Fatalf("the version error mentions neither version nor path: %v", err)
		}
	})
}

func TestStoreConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 40

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := Open(dir)
			if err != nil {
				errs[i] = err
				return
			}
			errs[i] = s.Replace(func(inv *Inventory) {
				inv.Nodes = append(inv.Nodes, sampleNode("n-"+strconv.Itoa(i)))
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open final: %v", err)
	}
	inv, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	wanted := make([]string, 0, n)
	for i := 0; i < n; i++ {
		wanted = append(wanted, "n-"+strconv.Itoa(i))
	}
	sort.Strings(wanted)
	if got := idsDe(inv); !reflect_DeepEqualStrings(got, wanted) {
		t.Fatalf("mutations lost: %d of %d arrived\n  missing: %v",
			len(got), n, missingFrom(wanted, got))
	}

	b, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var checking Inventory
	if err := json.Unmarshal(b, &checking); err != nil {
		t.Fatalf("inventory.json is not valid JSON after the concurrency: %v", err)
	}
}

func missingFrom(wanted, got []string) []string {
	has := map[string]bool{}
	for _, g := range got {
		has[g] = true
	}
	var out []string
	for _, q := range wanted {
		if !has[q] {
			out = append(out, q)
		}
	}
	return out
}

func TestStoreCrossProcess(t *testing.T) {
	if os.Getenv("INVENTORY_CHILD_DIR") != "" {
		t.Skip("child process")
	}
	dir := t.TempDir()
	const procCount, perProcess = 4, 25

	var wg sync.WaitGroup
	outputs := make([]string, procCount)
	failures := make([]error, procCount)
	for p := 0; p < procCount; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestStoreChildAppend", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(),
				"INVENTORY_CHILD_DIR="+dir,
				"INVENTORY_CHILD_PREFIX=p"+strconv.Itoa(p),
				"INVENTORY_CHILD_N="+strconv.Itoa(perProcess),
			)
			out, err := cmd.CombinedOutput()
			outputs[p], failures[p] = string(out), err
		}(p)
	}
	wg.Wait()
	for p, err := range failures {
		if err != nil {
			t.Fatalf("child %d failed: %v\n%s", p, err, outputs[p])
		}
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open final: %v", err)
	}
	inv, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	wanted := make([]string, 0, procCount*perProcess)
	for p := 0; p < procCount; p++ {
		for i := 0; i < perProcess; i++ {
			wanted = append(wanted, fmt.Sprintf("p%d-%d", p, i))
		}
	}
	sort.Strings(wanted)
	got := idsDe(inv)
	if !reflect_DeepEqualStrings(got, wanted) {
		t.Fatalf("CROSS-PROCESS lost update: %d of %d entries survived\n  missing: %v",
			len(got), len(wanted), missingFrom(wanted, got))
	}
}

func TestStoreChildAppend(t *testing.T) {
	dir := os.Getenv("INVENTORY_CHILD_DIR")
	if dir == "" {
		t.Skip("helper for TestStoreCrossProcess")
	}
	prefix := os.Getenv("INVENTORY_CHILD_PREFIX")
	n, err := strconv.Atoi(os.Getenv("INVENTORY_CHILD_N"))
	if err != nil {
		t.Fatalf("INVENTORY_CHILD_N: %v", err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open in the child: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := s.Replace(func(inv *Inventory) {
			inv.Nodes = append(inv.Nodes, sampleNode(fmt.Sprintf("%s-%d", prefix, i)))
		}); err != nil {
			t.Fatalf("Replace %d: %v", i, err)
		}
	}
}

func TestStoreWritePathIsDurable(t *testing.T) {
	b, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("reading its own source: %v", err)
	}
	src := string(b)

	required := map[string]*regexp.Regexp{
		"Sync() of the temp file": regexp.MustCompile(`f\.Sync\(\)`),
		"Sync() of the directory": regexp.MustCompile(`d(ir)?f?\.Sync\(\)`),
		"cross-process flock":     regexp.MustCompile(`syscall\.Flock\(`),
		"package mutex":           regexp.MustCompile(`(?m)^var \w+Mu sync\.Mutex`),
		"atomic rename":           regexp.MustCompile(`os\.Rename\(`),
	}
	for desc, re := range required {
		if !re.MatchString(src) {
			t.Fatalf("store.go lost %s (pattern %s)", desc, re)
		}
	}
	if n := len(regexp.MustCompile(`\.Sync\(\)`).FindAllString(src, -1)); n < 2 {
		t.Fatalf("store.go has %d Sync() call(s); durability is DOUBLE (file and directory)", n)
	}
	if regexp.MustCompile(`os\.WriteFile\(`).MatchString(src) {
		t.Fatal("store.go uses os.WriteFile — that is the fsync-LESS write from deploy/store.go:149, the one not to copy")
	}
}

func TestStoreRejectsInvalidNode(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Replace(func(inv *Inventory) {
		inv.Nodes = append(inv.Nodes, sampleNode("lxc/207"))
	}); err != nil {
		t.Fatalf("valid Replace: %v", err)
	}
	err = s.Replace(func(inv *Inventory) {
		inv.Nodes = append(inv.Nodes, Node{ID: "lxc/299", Transport: Transport("docker"), Kind: NodeKindGuest})
	})
	if err == nil {
		t.Fatal("the store ACCEPTED a node with a transport outside the set")
	}
	if !strings.Contains(err.Error(), "docker") {
		t.Fatalf("the error does not mention the refused transport: %v", err)
	}
	inv, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if got := idsDe(inv); !reflect_DeepEqualStrings(got, []string{"lxc/207"}) {
		t.Fatalf("the refused write contaminated the file: %v", got)
	}
}
