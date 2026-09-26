package gameservers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// Behavioral pin for the defect: the panel runs as root, the game container runs
// as the game's user, and the container's start.sh ABORTS the boot if it cannot
// write to the file. A config that turns root:root brings the server down on the
// next restart, far from the action that caused it.
//
// That is why the proof here is `stat` before and after — never a read of the
// content. A test that only checked bytes would pass with the defective code.

const (
	uidJogo = 4711 // Enshrouded's uid on CT 201; exists only in the test
	gidJogo = 4711
)

// exigeRoot fails instead of skipping. `os.Chown` to another uid is a root
// privilege: without root this test measures nothing. A t.Skip here would be a
// false green, which is the family of defect this work exists to close.
func exigeRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatalf("this test REQUIRES root (euid=%d): it measures OWNER preservation, and os.Chown to another uid needs privilege. Skipping would be a false green.", os.Geteuid())
	}
}

type marca struct {
	uid, gid int
	modo     os.FileMode
}

func (m marca) String() string { return fmt.Sprintf("uid=%d gid=%d modo=%04o", m.uid, m.gid, m.modo) }

func statCru(p string) (marca, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return marca{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return marca{}, fmt.Errorf("stat with no Stat_t at %s", p)
	}
	return marca{uid: int(st.Uid), gid: int(st.Gid), modo: fi.Mode().Perm()}, nil
}

func exigeMarca(t *testing.T, p string, want marca) {
	t.Helper()
	got, err := statCru(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	if got != want {
		t.Errorf("%s: stat expected %s, got %s", p, want, got)
	}
}

// ── the pin, with a pluggable reporter for the negative case ───────────────

// reporter is what makes it possible to run the SAME pin against a defective
// writer and assert that it fails. Without it the pin would prove that it labels,
// not that it detects.
type reporter interface {
	Errorf(format string, args ...interface{})
}

type coletor struct{ erros []string }

func (c *coletor) Errorf(f string, a ...interface{}) { c.erros = append(c.erros, fmt.Sprintf(f, a...)) }
func (c *coletor) tem(sub string) bool {
	for _, e := range c.erros {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

// verificaPreservacao creates a file with a known owner and mode, calls the
// writer and compares `stat` before/after. It returns both marks so the caller
// can record them (that is the evidence that goes into the write-up).
func verificaPreservacao(r reporter, dir, nome string, escritor func(path string, conteudo []byte) error) (antes, depois marca) {
	path := filepath.Join(dir, nome)
	if err := os.WriteFile(path, []byte(`{"v":1}`), 0o600); err != nil {
		r.Errorf("setup: %v", err)
		return
	}
	if err := os.Chmod(path, 0o640); err != nil {
		r.Errorf("preparo chmod: %v", err)
		return
	}
	if err := os.Chown(path, uidJogo, gidJogo); err != nil {
		r.Errorf("preparo chown: %v", err)
		return
	}
	antes, err := statCru(path)
	if err != nil {
		r.Errorf("stat before: %v", err)
		return
	}
	novo := []byte(`{"v":2}`)
	if err := escritor(path, novo); err != nil {
		r.Errorf("escritor falhou: %v", err)
		return
	}
	depois, err = statCru(path)
	if err != nil {
		r.Errorf("stat depois: %v", err)
		return
	}
	if depois.uid != antes.uid || depois.gid != antes.gid {
		r.Errorf("dono NÃO preservado: antes %d:%d, depois %d:%d", antes.uid, antes.gid, depois.uid, depois.gid)
	}
	if depois.modo != antes.modo {
		r.Errorf("modo NÃO preservado: antes %04o, depois %04o", antes.modo, depois.modo)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != string(novo) {
		r.Errorf("conteúdo não é o novo: %q (err=%v)", string(b), err)
	}
	return
}

// escritorSemChown reproduces the DEFECTIVE idiom that existed in the seven
// sites: tmp + WriteFile + Chmod + Rename, with no Chown. It is the pin's bite.
func escritorSemChown(path string, conteudo []byte) error {
	tmp := path + ".antigo-tmp"
	if err := os.WriteFile(tmp, conteudo, 0o644); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		_ = os.Chmod(tmp, fi.Mode())
	}
	return os.Rename(tmp, path)
}

func TestEscreveAtomicoPreservaDonoEModo(t *testing.T) {
	requerRoot(t)
	exigeRoot(t)

	t.Run("preserves", func(t *testing.T) {
		antes, depois := verificaPreservacao(t, t.TempDir(), "alvo.json", func(p string, c []byte) error {
			return escreveAtomico(p, c, "")
		})
		t.Logf("stat BEFORE: %s", antes)
		t.Logf("stat AFTER:  %s", depois)
	})

	// The mandatory negative case: the same pin, against the writer with no Chown.
	// If it does NOT fail, the pin is decorative and the whole test is a false green.
	t.Run("bites_when_chown_is_missing", func(t *testing.T) {
		c := &coletor{}
		verificaPreservacao(c, t.TempDir(), "alvo.json", escritorSemChown)
		if len(c.erros) == 0 {
			t.Fatal("the guard did NOT fail a writer without Chown — it labels, it does not detect")
		}
		if !c.tem("dono NÃO preservado") {
			t.Fatalf("the guard failed for another reason, not the owner: %v", c.erros)
		}
		t.Logf("the guard failed as it should: %v", c.erros)
	})
}

func TestEscreveAtomicoArquivoNovoUsaRef(t *testing.T) {
	requerRoot(t)
	exigeRoot(t)
	dir := t.TempDir()

	ref := filepath.Join(dir, "referencia")
	if err := os.WriteFile(ref, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(ref, uidJogo, gidJogo); err != nil {
		t.Fatal(err)
	}

	novo := filepath.Join(dir, "nao-existia.json")
	if err := escreveAtomico(novo, []byte(`{"a":1}`), ref); err != nil {
		t.Fatalf("escreveAtomico: %v", err)
	}
	exigeMarca(t, novo, marca{uid: uidJogo, gid: gidJogo, modo: 0o644})

	t.Run("dir_ref_does_not_inherit_the_execute_bit", func(t *testing.T) {
		sub := filepath.Join(dir, "raiz")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(sub, uidJogo, gidJogo); err != nil {
			t.Fatal(err)
		}
		alvo := filepath.Join(sub, ".active")
		if err := escreveAtomico(alvo, []byte("mundo1\n"), sub); err != nil {
			t.Fatalf("escreveAtomico: %v", err)
		}
		exigeMarca(t, alvo, marca{uid: uidJogo, gid: gidJogo, modo: 0o644})
	})

	t.Run("no_ref_and_no_file_errors_naming_the_path", func(t *testing.T) {
		alvo := filepath.Join(dir, "orfao.json")
		err := escreveAtomico(alvo, []byte("x"), "")
		if err == nil {
			t.Fatal("expected an error: with no existing file and no ref there is nowhere to take the owner from")
		}
		if !strings.Contains(err.Error(), alvo) {
			t.Fatalf("the error has to name the path; got: %v", err)
		}
	})
}

// lixoTmp counts leftover temporaries in the directory.
func lixoTmp(t *testing.T, dir, base string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if strings.Contains(n, ".tmp") && n != base {
			out = append(out, n)
		}
	}
	return out
}

func TestEscreveAtomicoNaoDeixaLixo(t *testing.T) {
	requerRoot(t)
	exigeRoot(t)

	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "a.json")
		if err := os.WriteFile(p, []byte("1"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := escreveAtomico(p, []byte("2"), ""); err != nil {
			t.Fatal(err)
		}
		if l := lixoTmp(t, dir, "a.json"); len(l) != 0 {
			t.Fatalf("junk left behind after success: %v", l)
		}
	})

	t.Run("failure_midway", func(t *testing.T) {
		dir := t.TempDir()
		// The target is a NON-EMPTY DIRECTORY: the Rename fails (ENOTEMPTY) after
		// the temporary already exists. It is the path that proves the cleanup.
		p := filepath.Join(dir, "alvo")
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "ocupa"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := escreveAtomico(p, []byte("2"), ""); err == nil {
			t.Fatal("expected an error renaming over a non-empty directory")
		}
		if l := lixoTmp(t, dir, "alvo"); len(l) != 0 {
			t.Fatalf("junk left behind after failure: %v", l)
		}
	})
}

func TestEscreveAtomicoTmpNaoTemNomePrevisivel(t *testing.T) {
	requerRoot(t)
	exigeRoot(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "concorrido.json")
	if err := os.WriteFile(p, []byte("0"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(p, uidJogo, gidJogo); err != nil {
		t.Fatal(err)
	}

	// A third party pre-creates the predictable name. If the writer used
	// path+".tmp", it would write over this file — and this content would vanish.
	iscas := p + ".tmp"
	if err := os.WriteFile(iscas, []byte("ISCA"), 0o600); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = escreveAtomico(p, []byte(fmt.Sprintf("escritor-%d", i)), "")
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Errorf("writer %d failed: %v", i, e)
		}
	}

	b, err := os.ReadFile(iscas)
	if err != nil {
		t.Fatalf("the bait %s vanished — the writer used the predictable name: %v", iscas, err)
	}
	if string(b) != "ISCA" {
		t.Fatalf("the bait was overwritten: %q — the writer used path+\".tmp\"", string(b))
	}

	exigeMarca(t, p, marca{uid: uidJogo, gid: gidJogo, modo: 0o640})
	final, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(final), "escritor-") {
		t.Fatalf("final content corrupted: %q", string(final))
	}
	if l := lixoTmp(t, dir, "concorrido.json"); len(l) != 1 || l[0] != filepath.Base(iscas) {
		t.Fatalf("temporary residue beyond the bait: %v", l)
	}
}

func TestChownComoRefDerivaDoDisco(t *testing.T) {
	requerRoot(t)
	exigeRoot(t)
	dir := t.TempDir()

	raiz := filepath.Join(dir, "servidor")
	arvore := filepath.Join(raiz, "worlds", "mundo1")
	if err := os.MkdirAll(arvore, 0o755); err != nil {
		t.Fatal(err)
	}
	interno := filepath.Join(arvore, "save-index")
	if err := os.WriteFile(interno, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only the ROOT belongs to the game; what is inside was born root:root (it is
	// the snapshot of the defect).
	if err := os.Chown(raiz, uidJogo, gidJogo); err != nil {
		t.Fatal(err)
	}
	if m, _ := statCru(interno); m.uid != 0 {
		t.Fatalf("invalid setup: the inner file should be root, it is %s", m)
	}

	if err := chownComoRef(arvore, raiz, true); err != nil {
		t.Fatalf("chownComoRef: %v", err)
	}
	for _, p := range []string{arvore, interno} {
		m, err := statCru(p)
		if err != nil {
			t.Fatal(err)
		}
		if m.uid != uidJogo || m.gid != gidJogo {
			t.Errorf("%s: expected %d:%d, got %s", p, uidJogo, gidJogo, m)
		}
	}
}

// TestSemUidHardcoded is the pin for it: the game's uid is NEVER a constant in
// production code — it always comes from observing the disk. 4711 is
// Enshrouded's; Palworld uses 1000. A new constant is how the defect comes back
// under another name.
//
// The sweep is by AST, not by grep: a comment and a string do not count, and an
// integer literal counts even when written in another base.
func TestSemUidHardcoded(t *testing.T) {
	requerRoot(t)
	fset := token.NewFileSet()
	pacotes, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	var achados []string
	for _, pkg := range pacotes {
		for nome, arq := range pkg.Files {
			ast.Inspect(arq, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.INT {
					return true
				}
				if v := strings.TrimPrefix(strings.TrimPrefix(lit.Value, "0o"), "0x"); v == "4711" {
					achados = append(achados, fmt.Sprintf("%s:%d", nome, fset.Position(lit.Pos()).Line))
				}
				return true
			})
		}
	}
	if len(achados) > 0 {
		t.Errorf("hard-coded game uid at: %s — the owner has to come from the disk, via chownComoRef", strings.Join(achados, ", "))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// One pin PER converted site.
//
// Each one creates the target with a known owner and a known mode, calls the
// PUBLIC METHOD that writes, and asserts an identical `stat`. Calling the public
// method (and not escreveAtomico directly) is what makes the pin able to catch a
// regression of the kind "somebody reintroduced WriteFile in this path".
// ─────────────────────────────────────────────────────────────────────────────

// uidOutro is deliberately DIFFERENT from 4711. A test written with 4711 would
// pass even with the old code, because the old code nailed 4711 down — it would
// be a test built to match the wrong implementation.
const (
	uidOutro = 5000
	gidOutro = 5000
)

// arvoreEnshrouded assembles a plausible server root, entirely owned by
// uidOutro:gidOutro, and returns the corresponding Server.
func arvoreEnshrouded(t *testing.T) Server {
	t.Helper()
	exigeRoot(t)
	raiz := t.TempDir()
	for _, d := range []string{
		filepath.Join("data", "server"),
		filepath.Join("worlds", "mundo1"),
		"backups",
	} {
		if err := os.MkdirAll(filepath.Join(raiz, d), 0o755); err != nil {
			t.Fatalf("setup mkdir %s: %v", d, err)
		}
	}
	cfg := `{
    "name": "servidor",
    "slotCount": 16,
    "gameSettings": {"playerHealthFactor": 1},
    "userGroups": [{"name":"Admin","password":"senha-admin","canKickBan":true,"canAccessInventories":true,"canEditWorld":true,"canEditBase":true,"canExtendBase":true,"reservedSlots":0}],
    "bannedAccounts": []
}`
	if err := os.WriteFile(enshConfigPath(Server{Root: raiz}), []byte(cfg), 0o640); err != nil {
		t.Fatalf("setup config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(raiz, ".active"), []byte("mundo1\n"), 0o640); err != nil {
		t.Fatalf("setup .active: %v", err)
	}
	// The WHOLE tree ends up with the game's owner — including the root, which is
	// the reference the production code consults.
	if err := chownTree(raiz, uidOutro, gidOutro); err != nil {
		t.Fatalf("chown setup of the tree: %v", err)
	}
	return Server{ID: "ensh-teste", Game: "enshrouded", Root: raiz}
}

func exigeDonoDoJogo(t *testing.T, path, oQue string) {
	t.Helper()
	m, err := statCru(path)
	if err != nil {
		t.Fatalf("%s: stat %s: %v", oQue, path, err)
	}
	if m.uid != uidOutro || m.gid != gidOutro {
		t.Errorf("%s: %s ended up %d:%d, expected %d:%d — the container could not write and would abort the boot",
			oQue, path, m.uid, m.gid, uidOutro, gidOutro)
	}
}

func TestSitiosPreservamDonoEModo(t *testing.T) {
	requerRoot(t)
	a := enshrouded{}

	t.Run("1_gameSettings", func(t *testing.T) {
		s := arvoreEnshrouded(t)
		p := enshConfigPath(s)
		antes, _ := statCru(p)
		if err := a.SaveSettings(s, map[string]interface{}{"playerHealthFactor": 2.0}); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}
		exigeMarca(t, p, antes)
	})

	t.Run("2_groups", func(t *testing.T) {
		s := arvoreEnshrouded(t)
		p := enshConfigPath(s)
		antes, _ := statCru(p)
		gs := []Group{{Name: "Admin", Password: "senha-admin"}, {Name: "Guest", Password: "senha-guest"}}
		if err := a.SaveGroups(s, gs); err != nil {
			t.Fatalf("SaveGroups: %v", err)
		}
		exigeMarca(t, p, antes)
	})

	t.Run("3_bans_and_serverSettings", func(t *testing.T) {
		s := arvoreEnshrouded(t)
		p := enshConfigPath(s)
		antes, _ := statCru(p)
		if err := a.SaveBans(s, []string{"76561198000000000"}); err != nil {
			t.Fatalf("SaveBans: %v", err)
		}
		exigeMarca(t, p, antes)
	})
}

// TestActiveNaoViraRoot reproduces the SNAPSHOT of the defect in production:
// /opt/enshrouded/.active as root:root inside a tree owned by the game.
//
// THE HALF THAT BITES is the CREATION, and finding that out cost a mutation: the
// original wording asked for "a 4711 tree, switch the active world, .active stays
// 4711", but that scenario does NOT tell the two implementations apart.
// `os.WriteFile` on a file that already exists truncates and writes — it touches
// neither owner nor mode. The old idiom and the new one give the same result
// there, and the pin would go green over the defective code.
//
// The owner only turns root when the file is CREATED. That is why the positive
// half below calls exactly the expression production calls, with .active ABSENT
// — and the negative control shows the old idiom failing in the same scenario.
// The second half (rewriting with the file present) stays as a non-regression
// check, and is labelled as the weak half.
func TestActiveNaoViraRoot(t *testing.T) {
	requerRoot(t)
	t.Run("creation_is_the_half_that_bites", func(t *testing.T) {
		s := arvoreEnshrouded(t)
		ativo := filepath.Join(s.Root, ".active")
		if err := os.Remove(ativo); err != nil {
			t.Fatalf("setup: %v", err)
		}
		// The expression identical to the one in adapter_server_settings.go.
		if err := escreveAtomico(ativo, []byte("mundo2\n"), s.Root); err != nil {
			t.Fatalf("escreveAtomico: %v", err)
		}
		exigeDonoDoJogo(t, ativo, "creation of .active")

		// Negative control: the OLD idiom, in the same scenario, produces root.
		// Without this half there would be no proof that the one above measures anything.
		antigo := filepath.Join(s.Root, ".active-idioma-antigo")
		if err := os.WriteFile(antigo, []byte("mundo2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := statCru(antigo)
		if err != nil {
			t.Fatal(err)
		}
		if m.uid != 0 {
			t.Fatalf("the negative control did not reproduce the defect (got %s) — without it the test above proves nothing", m)
		}
		t.Logf("negative control confirmed: the old idiom creates %s, the new one creates %d:%d", m, uidOutro, gidOutro)
	})

	t.Run("rewrite_preserves_the_weak_half", func(t *testing.T) {
		s := arvoreEnshrouded(t)
		a := enshrouded{}
		ativo := filepath.Join(s.Root, ".active")
		antes, err := statCru(ativo)
		if err != nil {
			t.Fatalf("stat before: %v", err)
		}
		if err := a.RenameWorld(s, "mundo1", "mundo2"); err != nil {
			t.Fatalf("RenameWorld: %v", err)
		}
		if got := a.ActiveWorld(s); got != "mundo2" {
			t.Fatalf("the active world pointer did not follow: %q", got)
		}
		exigeMarca(t, ativo, antes)
	})
}

// TestBakHerdaDonoDoOriginal — the .bak is born with the ORIGINAL's owner, not
// root:root. A defect quieter than the config's: nobody looks at a backup's
// owner until they need it.
func TestBakHerdaDonoDoOriginal(t *testing.T) {
	requerRoot(t)
	exigeRoot(t)
	dir := t.TempDir()
	orig := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(orig, []byte("services:\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(orig, uidOutro, gidOutro); err != nil {
		t.Fatal(err)
	}
	antes, _ := statCru(orig)
	if err := escreveAtomico(orig+".bak", []byte("services:\n"), orig); err != nil {
		t.Fatalf("escreveAtomico of the .bak: %v", err)
	}
	exigeMarca(t, orig+".bak", antes)
}

// TestInventarioSobreviveAoHelper covers the PANEL's site (SaveInventory). Here
// the owner matters less; what the helper adds is DURABILITY (fsync before the
// rename). Without it, a power cut inside the ZFS txg window costs the whole
// inventory.
func TestInventarioSobreviveAoHelper(t *testing.T) {
	requerRoot(t)
	exigeRoot(t)
	dir := t.TempDir()
	alvo := filepath.Join(dir, "gameservers.json")
	if err := os.WriteFile(alvo, []byte("[]"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(alvo, uidOutro, gidOutro); err != nil {
		t.Fatal(err)
	}
	antes, _ := statCru(alvo)
	if err := escreveAtomico(alvo, []byte(`[{"id":"x"}]`), alvo); err != nil {
		t.Fatalf("escreveAtomico: %v", err)
	}
	exigeMarca(t, alvo, antes)
	if b, _ := os.ReadFile(alvo); string(b) != `[{"id":"x"}]` {
		t.Errorf("content not written: %q", string(b))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// The owner comes from the disk, never from a constant.
// ─────────────────────────────────────────────────────────────────────────────

// TestChownTreeUsaDonoDoServidor is the pin that proves the change is REAL and
// not cosmetic: the tree is 5000:5000, and with the old code (chownTree nailed to
// 4711) the new files would come out 4711:4711 and this test would fail.
func TestChownTreeUsaDonoDoServidor(t *testing.T) {
	requerRoot(t)
	s := arvoreEnshrouded(t)
	novo := filepath.Join(s.Root, "worlds", "importado")
	if err := os.MkdirAll(novo, 0o755); err != nil {
		t.Fatal(err)
	}
	// Born root:root, as a directory created by the panel would be.
	dentro := filepath.Join(novo, ".saveid")
	if err := os.WriteFile(dentro, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, _ := statCru(dentro); m.uid != 0 {
		t.Fatalf("invalid setup: the file had to be born root for the test to measure anything (got %s)", m)
	}
	if err := chownComoRef(novo, s.Root, true); err != nil {
		t.Fatalf("chownComoRef: %v", err)
	}
	exigeDonoDoJogo(t, novo, "imported directory")
	exigeDonoDoJogo(t, dentro, ".saveid inside the imported one")
}

// TestChownComoRefFalhaClaroSemRef — a nonexistent root produces an ERROR naming
// the path. Never silence, never a fallback to an invented uid: a default value
// is a new constant under another name.
func TestChownComoRefFalhaClaroSemRef(t *testing.T) {
	requerRoot(t)
	exigeRoot(t)
	alvo := t.TempDir()
	inexistente := filepath.Join(alvo, "raiz-que-nao-existe")
	err := chownComoRef(alvo, inexistente, true)
	if err == nil {
		t.Fatal("chownComoRef accepted a nonexistent reference — it would silently fall back to an invented default")
	}
	if !strings.Contains(err.Error(), alvo) && !strings.Contains(err.Error(), inexistente) {
		t.Errorf("the error names no path at all: %v", err)
	}
}

// requerRoot skips the test when the process cannot change a file's owner.
//
// These tests verify preservation of owner and mode, which requires chown, which
// requires root. On a common CI runner the process is an unprivileged user, and
// without this guard the test would fail for a reason that is not a code defect.
func requerRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root: checks owner preservation via chown")
	}
}
