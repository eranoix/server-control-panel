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

func nodeExemplo(id string) Node {
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

// TestStoreOpenAusenteEhVazio — a missing file is a LEGITIMATE empty
// inventory. Mistaking that for an error would make the first boot fail;
// mistaking the opposite (corrupt read as empty) would erase the inventory on
// the next write — which is why the two cases have separate tests.
func TestStoreOpenAusenteEhVazio(t *testing.T) {
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

// TestStoreDurableWrite — after Replace the directory holds inventory.json and
// NO orphaned temporary; the content comes back through a fresh Open.
func TestStoreDurableWrite(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Replace(func(inv *Inventory) {
		inv.Nodes = append(inv.Nodes, nodeExemplo("lxc/207"), nodeExemplo("qemu/208"))
	}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	entradas, err := os.ReadDir(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var nomes []string
	for _, e := range entradas {
		nomes = append(nomes, e.Name())
		if strings.HasSuffix(e.Name(), ".new") || strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("orphan temporary on disk: %s", e.Name())
		}
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatalf("inventory.json does not exist after the Replace (%v); directory: %v", err, nomes)
	}

	// Reload through a NEW Store — it is what the other process does.
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

	// Snapshot returns a COPY: touching the result does not change what is on
	// disk.
	inv.Nodes[0].Name = "sequestrado"
	inv2, err := s2.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot 2: %v", err)
	}
	if inv2.Nodes[0].Name == "sequestrado" {
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

// TestStoreCorruptFile — a truncated/corrupt file is a HARD ERROR naming the
// path. Starting empty in silence would erase the inventory on the next write.
func TestStoreCorruptFile(t *testing.T) {
	casos := map[string]string{
		"truncado":   `{"schema_version":1,"nodes":[{"id":"lxc/2`,
		"vazio":      "",
		"lixo":       "nao sou json",
		"tipoerrado": `["isto e uma lista, nao o envelope"]`,
	}
	for nome, conteudo := range casos {
		t.Run(nome, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "inventory"), 0o700); err != nil {
				t.Fatal(err)
			}
			alvo := filepath.Join(dir, "inventory", "inventory.json")
			if err := os.WriteFile(alvo, []byte(conteudo), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Open(dir)
			if err == nil {
				t.Fatalf("Open ACCEPTED inventory %s — an empty inventory in place of an error erases data", nome)
			}
			if !strings.Contains(err.Error(), alvo) {
				t.Fatalf("the error does not name the path %q: %v", alvo, err)
			}
		})
	}

	// A FUTURE version envelope is an error too — and the error names the binary,
	// the way internal/config/config_io.go does.
	t.Run("future-version", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "inventory"), 0o700); err != nil {
			t.Fatal(err)
		}
		alvo := filepath.Join(dir, "inventory", "inventory.json")
		if err := os.WriteFile(alvo, []byte(`{"schema_version":99,"nodes":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Open(dir)
		if err == nil {
			t.Fatal("Open accepted an envelope from a future version")
		}
		if !strings.Contains(err.Error(), "99") || !strings.Contains(err.Error(), alvo) {
			t.Fatalf("the version error mentions neither version nor path: %v", err)
		}
	})
}

// TestStoreConcurrent — N goroutines doing Replace at the same time, under
// -race. Without read-modify-write under ONE lock this becomes lost-update and
// the final set comes out smaller (it is the bug the comment in
// deploy/store.go:73 documents).
func TestStoreConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 40

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// One Store per goroutine ON PURPOSE: that is the real scenario
			// (the queue runner and HTTP call Open() separately), and it is
			// what defeats a per-instance mutex.
			s, err := Open(dir)
			if err != nil {
				errs[i] = err
				return
			}
			errs[i] = s.Replace(func(inv *Inventory) {
				inv.Nodes = append(inv.Nodes, nodeExemplo("n-"+strconv.Itoa(i)))
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
	querido := make([]string, 0, n)
	for i := 0; i < n; i++ {
		querido = append(querido, "n-"+strconv.Itoa(i))
	}
	sort.Strings(querido)
	if got := idsDe(inv); !reflect_DeepEqualStrings(got, querido) {
		t.Fatalf("mutations lost: %d of %d arrived\n  missing: %v",
			len(got), n, faltantes(querido, got))
	}

	// And the file is still valid JSON, not a hybrid of two writes.
	b, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	var conferindo Inventory
	if err := json.Unmarshal(b, &conferindo); err != nil {
		t.Fatalf("inventory.json is not valid JSON after the concurrency: %v", err)
	}
}

func faltantes(querido, got []string) []string {
	tem := map[string]bool{}
	for _, g := range got {
		tem[g] = true
	}
	var out []string
	for _, q := range querido {
		if !tem[q] {
			out = append(out, q)
		}
	}
	return out
}

// TestStoreCrossProcess — the proof of the FLOCK, which the goroutine test
// does NOT give: the package mutex on its own already serialises goroutines,
// so only a second PROCESS tells the two locks apart. It re-executes the test
// binary itself (the post-receive hook runs inside vpsmctl, a separate
// process — this is that scenario).
func TestStoreCrossProcess(t *testing.T) {
	if os.Getenv("INVENTORY_CHILD_DIR") != "" {
		t.Skip("child process")
	}
	dir := t.TempDir()
	const processos, porProcesso = 4, 25

	var wg sync.WaitGroup
	saidas := make([]string, processos)
	falhas := make([]error, processos)
	for p := 0; p < processos; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestStoreChildAppend", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(),
				"INVENTORY_CHILD_DIR="+dir,
				"INVENTORY_CHILD_PREFIX=p"+strconv.Itoa(p),
				"INVENTORY_CHILD_N="+strconv.Itoa(porProcesso),
			)
			out, err := cmd.CombinedOutput()
			saidas[p], falhas[p] = string(out), err
		}(p)
	}
	wg.Wait()
	for p, err := range falhas {
		if err != nil {
			t.Fatalf("child %d failed: %v\n%s", p, err, saidas[p])
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
	querido := make([]string, 0, processos*porProcesso)
	for p := 0; p < processos; p++ {
		for i := 0; i < porProcesso; i++ {
			querido = append(querido, fmt.Sprintf("p%d-%d", p, i))
		}
	}
	sort.Strings(querido)
	got := idsDe(inv)
	if !reflect_DeepEqualStrings(got, querido) {
		t.Fatalf("CROSS-PROCESS lost update: %d of %d entries survived\n  missing: %v",
			len(got), len(querido), faltantes(querido, got))
	}
}

// TestStoreChildAppend is the body of the child process of
// TestStoreCrossProcess. Without the environment variables it is a no-op — not
// a real test.
func TestStoreChildAppend(t *testing.T) {
	dir := os.Getenv("INVENTORY_CHILD_DIR")
	if dir == "" {
		t.Skip("helper for TestStoreCrossProcess")
	}
	prefixo := os.Getenv("INVENTORY_CHILD_PREFIX")
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
			inv.Nodes = append(inv.Nodes, nodeExemplo(fmt.Sprintf("%s-%d", prefixo, i)))
		}); err != nil {
			t.Fatalf("Replace %d: %v", i, err)
		}
	}
}

// TestStoreWritePathIsDurable — the STRUCTURAL pin for durability.
//
// fsync has no observable effect without cutting the machine's power, and this
// server has a UPS with NO data cable: an abrupt cut is an expected failure
// mode, not a theoretical one. Since the behaviour is not testable in-process,
// the pin reads the source itself and demands the four pieces: Sync of the
// FILE, Sync of the DIRECTORY, flock, and the absence of os.WriteFile on the
// persistence path (which is exactly the omission in
// internal/deploy/store.go:149-165).
func TestStoreWritePathIsDurable(t *testing.T) {
	b, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("reading its own source: %v", err)
	}
	src := string(b)

	exigidos := map[string]*regexp.Regexp{
		"Sync() do arquivo temporário": regexp.MustCompile(`f\.Sync\(\)`),
		"Sync() do diretório":          regexp.MustCompile(`d(ir)?f?\.Sync\(\)`),
		"flock entre processos":        regexp.MustCompile(`syscall\.Flock\(`),
		"mutex de pacote":              regexp.MustCompile(`(?m)^var \w+Mu sync\.Mutex`),
		"rename atômico":               regexp.MustCompile(`os\.Rename\(`),
	}
	for desc, re := range exigidos {
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

// TestStoreRecusaNoInvalido — the closed set of transports is only truly
// closed if the disk refuses one too. An invalid node fails the ENTIRE write
// and the previous file stays intact (no writing half of it).
func TestStoreRecusaNoInvalido(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Replace(func(inv *Inventory) {
		inv.Nodes = append(inv.Nodes, nodeExemplo("lxc/207"))
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
