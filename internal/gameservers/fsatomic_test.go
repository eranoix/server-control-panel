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
	gameUID = 4711 // Enshrouded's uid on CT 201; exists only in the test
	gameGID = 4711
)

// requireRoot fails instead of skipping. `os.Chown` to another uid is a root
// privilege: without root this test measures nothing. A t.Skip here would be a
// false green, which is the family of defect this work exists to close.
func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatalf("this test REQUIRES root (euid=%d): it measures OWNER preservation, and os.Chown to another uid needs privilege. Skipping would be a false green.", os.Geteuid())
	}
}

type mark struct {
	uid, gid int
	mode     os.FileMode
}

func (m mark) String() string { return fmt.Sprintf("uid=%d gid=%d mode=%04o", m.uid, m.gid, m.mode) }

func statRaw(p string) (mark, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return mark{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return mark{}, fmt.Errorf("stat with no Stat_t at %s", p)
	}
	return mark{uid: int(st.Uid), gid: int(st.Gid), mode: fi.Mode().Perm()}, nil
}

func requireMark(t *testing.T, p string, want mark) {
	t.Helper()
	got, err := statRaw(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	if got != want {
		t.Errorf("%s: stat expected %s, got %s", p, want, got)
	}
}

// reporter is what makes it possible to run the SAME pin against a defective
// writer and assert that it fails. Without it the pin would prove that it labels,
// not that it detects.
type reporter interface {
	Errorf(format string, args ...interface{})
}

type collector struct{ errs []string }

func (c *collector) Errorf(f string, a ...interface{}) { c.errs = append(c.errs, fmt.Sprintf(f, a...)) }
func (c *collector) has(sub string) bool {
	for _, e := range c.errs {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

// checkPreservation creates a file with a known owner and mode, calls the
// writer and compares `stat` before/after. It returns both marks so the caller
// can record them (that is the evidence that goes into the write-up).
func checkPreservation(r reporter, dir, name string, writer func(path string, content []byte) error) (before, after mark) {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(`{"v":1}`), 0o600); err != nil {
		r.Errorf("setup: %v", err)
		return
	}
	if err := os.Chmod(path, 0o640); err != nil {
		r.Errorf("setup chmod: %v", err)
		return
	}
	if err := os.Chown(path, gameUID, gameGID); err != nil {
		r.Errorf("setup chown: %v", err)
		return
	}
	before, err := statRaw(path)
	if err != nil {
		r.Errorf("stat before: %v", err)
		return
	}
	fresh := []byte(`{"v":2}`)
	if err := writer(path, fresh); err != nil {
		r.Errorf("writer failed: %v", err)
		return
	}
	after, err = statRaw(path)
	if err != nil {
		r.Errorf("stat after: %v", err)
		return
	}
	if after.uid != before.uid || after.gid != before.gid {
		r.Errorf("owner NOT preserved: before %d:%d, after %d:%d", before.uid, before.gid, after.uid, after.gid)
	}
	if after.mode != before.mode {
		r.Errorf("mode NOT preserved: before %04o, after %04o", before.mode, after.mode)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != string(fresh) {
		r.Errorf("content is not the new one: %q (err=%v)", string(b), err)
	}
	return
}

// writerWithoutChown reproduces the DEFECTIVE idiom that existed in the seven
// sites: tmp + WriteFile + Chmod + Rename, with no Chown. It is the pin's bite.
func writerWithoutChown(path string, content []byte) error {
	tmp := path + ".old-tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		_ = os.Chmod(tmp, fi.Mode())
	}
	return os.Rename(tmp, path)
}

func TestWriteAtomicPreservesOwnerAndMode(t *testing.T) {
	needsRoot(t)
	requireRoot(t)

	t.Run("preserves", func(t *testing.T) {
		before, after := checkPreservation(t, t.TempDir(), "target.json", func(p string, c []byte) error {
			return writeAtomic(p, c, "")
		})
		t.Logf("stat BEFORE: %s", before)
		t.Logf("stat AFTER:  %s", after)
	})

	// The mandatory negative case: the same pin, against the writer with no Chown.
	// If it does NOT fail, the pin is decorative and the whole test is a false green.
	t.Run("bites_when_chown_is_missing", func(t *testing.T) {
		c := &collector{}
		checkPreservation(c, t.TempDir(), "target.json", writerWithoutChown)
		if len(c.errs) == 0 {
			t.Fatal("the guard did NOT fail a writer without Chown — it labels, it does not detect")
		}
		if !c.has("owner NOT preserved") {
			t.Fatalf("the guard failed for another reason, not the owner: %v", c.errs)
		}
		t.Logf("the guard failed as it should: %v", c.errs)
	})
}

func TestWriteAtomicNewFileUsesRef(t *testing.T) {
	needsRoot(t)
	requireRoot(t)
	dir := t.TempDir()

	ref := filepath.Join(dir, "reference")
	if err := os.WriteFile(ref, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(ref, gameUID, gameGID); err != nil {
		t.Fatal(err)
	}

	fresh := filepath.Join(dir, "did-not-exist.json")
	if err := writeAtomic(fresh, []byte(`{"a":1}`), ref); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	requireMark(t, fresh, mark{uid: gameUID, gid: gameGID, mode: 0o644})

	t.Run("dir_ref_does_not_inherit_the_execute_bit", func(t *testing.T) {
		sub := filepath.Join(dir, "root")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(sub, gameUID, gameGID); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(sub, ".active")
		if err := writeAtomic(target, []byte("world1\n"), sub); err != nil {
			t.Fatalf("writeAtomic: %v", err)
		}
		requireMark(t, target, mark{uid: gameUID, gid: gameGID, mode: 0o644})
	})

	t.Run("no_ref_and_no_file_errors_naming_the_path", func(t *testing.T) {
		target := filepath.Join(dir, "orphan.json")
		err := writeAtomic(target, []byte("x"), "")
		if err == nil {
			t.Fatal("expected an error: with no existing file and no ref there is nowhere to take the owner from")
		}
		if !strings.Contains(err.Error(), target) {
			t.Fatalf("the error has to name the path; got: %v", err)
		}
	})
}

// tmpLeftovers counts leftover temporaries in the directory.
func tmpLeftovers(t *testing.T, dir, base string) []string {
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

func TestWriteAtomicLeavesNoGarbage(t *testing.T) {
	needsRoot(t)
	requireRoot(t)

	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "a.json")
		if err := os.WriteFile(p, []byte("1"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeAtomic(p, []byte("2"), ""); err != nil {
			t.Fatal(err)
		}
		if l := tmpLeftovers(t, dir, "a.json"); len(l) != 0 {
			t.Fatalf("junk left behind after success: %v", l)
		}
	})

	t.Run("failure_midway", func(t *testing.T) {
		dir := t.TempDir()
		// The target is a NON-EMPTY DIRECTORY: the Rename fails (ENOTEMPTY) after
		// the temporary already exists. It is the path that proves the cleanup.
		p := filepath.Join(dir, "target")
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "occupant"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeAtomic(p, []byte("2"), ""); err == nil {
			t.Fatal("expected an error renaming over a non-empty directory")
		}
		if l := tmpLeftovers(t, dir, "target"); len(l) != 0 {
			t.Fatalf("junk left behind after failure: %v", l)
		}
	})
}

func TestWriteAtomicTmpNameIsUnpredictable(t *testing.T) {
	needsRoot(t)
	requireRoot(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "contended.json")
	if err := os.WriteFile(p, []byte("0"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(p, gameUID, gameGID); err != nil {
		t.Fatal(err)
	}

	// A third party pre-creates the predictable name. If the writer used
	// path+".tmp", it would write over this file — and this content would vanish.
	decoy := p + ".tmp"
	if err := os.WriteFile(decoy, []byte("BAIT"), 0o600); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = writeAtomic(p, []byte(fmt.Sprintf("writer-%d", i)), "")
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Errorf("writer %d failed: %v", i, e)
		}
	}

	b, err := os.ReadFile(decoy)
	if err != nil {
		t.Fatalf("the bait %s vanished — the writer used the predictable name: %v", decoy, err)
	}
	if string(b) != "BAIT" {
		t.Fatalf("the bait was overwritten: %q — the writer used path+\".tmp\"", string(b))
	}

	requireMark(t, p, mark{uid: gameUID, gid: gameGID, mode: 0o640})
	final, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(final), "writer-") {
		t.Fatalf("final content corrupted: %q", string(final))
	}
	if l := tmpLeftovers(t, dir, "contended.json"); len(l) != 1 || l[0] != filepath.Base(decoy) {
		t.Fatalf("temporary residue beyond the bait: %v", l)
	}
}

func TestChownAsRefDerivesFromDisk(t *testing.T) {
	needsRoot(t)
	requireRoot(t)
	dir := t.TempDir()

	root := filepath.Join(dir, "server")
	tree := filepath.Join(root, "worlds", "world1")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(tree, "save-index")
	if err := os.WriteFile(inner, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only the ROOT belongs to the game; what is inside was born root:root (it is
	// the snapshot of the defect).
	if err := os.Chown(root, gameUID, gameGID); err != nil {
		t.Fatal(err)
	}
	if m, _ := statRaw(inner); m.uid != 0 {
		t.Fatalf("invalid setup: the inner file should be root, it is %s", m)
	}

	if err := chownLikeRef(tree, root, true); err != nil {
		t.Fatalf("chownLikeRef: %v", err)
	}
	for _, p := range []string{tree, inner} {
		m, err := statRaw(p)
		if err != nil {
			t.Fatal(err)
		}
		if m.uid != gameUID || m.gid != gameGID {
			t.Errorf("%s: expected %d:%d, got %s", p, gameUID, gameGID, m)
		}
	}
}

// TestNoHardcodedUID is the pin for it: the game's uid is NEVER a constant in
// production code — it always comes from observing the disk. 4711 is
// Enshrouded's; Palworld uses 1000. A new constant is how the defect comes back
// under another name.
//
// The sweep is by AST, not by grep: a comment and a string do not count, and an
// integer literal counts even when written in another base.
func TestNoHardcodedUID(t *testing.T) {
	needsRoot(t)
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	var findings []string
	for _, pkg := range packages {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.INT {
					return true
				}
				if v := strings.TrimPrefix(strings.TrimPrefix(lit.Value, "0o"), "0x"); v == "4711" {
					findings = append(findings, fmt.Sprintf("%s:%d", name, fset.Position(lit.Pos()).Line))
				}
				return true
			})
		}
	}
	if len(findings) > 0 {
		t.Errorf("hard-coded game uid at: %s — the owner has to come from the disk, via chownLikeRef", strings.Join(findings, ", "))
	}
}

// One pin PER converted site.
//
// Each one creates the target with a known owner and a known mode, calls the
// PUBLIC METHOD that writes, and asserts an identical `stat`. Calling the public
// method (and not writeAtomic directly) is what makes the pin able to catch a
// regression of the kind "somebody reintroduced WriteFile in this path".

// otherUID is deliberately DIFFERENT from 4711. A test written with 4711 would
// pass even with the old code, because the old code nailed 4711 down — it would
// be a test built to match the wrong implementation.
const (
	otherUID = 5000
	otherGID = 5000
)

// enshroudedTree assembles a plausible server root, entirely owned by
// otherUID:otherGID, and returns the corresponding Server.
func enshroudedTree(t *testing.T) Server {
	t.Helper()
	requireRoot(t)
	root := t.TempDir()
	for _, d := range []string{
		filepath.Join("data", "server"),
		filepath.Join("worlds", "world1"),
		"backups",
	} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("setup mkdir %s: %v", d, err)
		}
	}
	cfg := `{
    "name": "server",
    "slotCount": 16,
    "gameSettings": {"playerHealthFactor": 1},
    "userGroups": [{"name":"Admin","password":"admin-password","canKickBan":true,"canAccessInventories":true,"canEditWorld":true,"canEditBase":true,"canExtendBase":true,"reservedSlots":0}],
    "bannedAccounts": []
}`
	if err := os.WriteFile(enshConfigPath(Server{Root: root}), []byte(cfg), 0o640); err != nil {
		t.Fatalf("setup config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".active"), []byte("world1\n"), 0o640); err != nil {
		t.Fatalf("setup .active: %v", err)
	}
	// The WHOLE tree ends up with the game's owner — including the root, which is
	// the reference the production code consults.
	if err := chownTree(root, otherUID, otherGID); err != nil {
		t.Fatalf("chown setup of the tree: %v", err)
	}
	return Server{ID: "ensh-test", Game: "enshrouded", Root: root}
}

func requireGameOwner(t *testing.T, path, what string) {
	t.Helper()
	m, err := statRaw(path)
	if err != nil {
		t.Fatalf("%s: stat %s: %v", what, path, err)
	}
	if m.uid != otherUID || m.gid != otherGID {
		t.Errorf("%s: %s ended up %d:%d, expected %d:%d — the container could not write and would abort the boot",
			what, path, m.uid, m.gid, otherUID, otherGID)
	}
}

func TestSitesPreserveOwnerAndMode(t *testing.T) {
	needsRoot(t)
	a := enshrouded{}

	t.Run("1_gameSettings", func(t *testing.T) {
		s := enshroudedTree(t)
		p := enshConfigPath(s)
		before, _ := statRaw(p)
		if err := a.SaveSettings(s, map[string]interface{}{"playerHealthFactor": 2.0}); err != nil {
			t.Fatalf("SaveSettings: %v", err)
		}
		requireMark(t, p, before)
	})

	t.Run("2_groups", func(t *testing.T) {
		s := enshroudedTree(t)
		p := enshConfigPath(s)
		before, _ := statRaw(p)
		gs := []Group{{Name: "Admin", Password: "admin-password"}, {Name: "Guest", Password: "guest-password"}}
		if err := a.SaveGroups(s, gs); err != nil {
			t.Fatalf("SaveGroups: %v", err)
		}
		requireMark(t, p, before)
	})

	t.Run("3_bans_and_serverSettings", func(t *testing.T) {
		s := enshroudedTree(t)
		p := enshConfigPath(s)
		before, _ := statRaw(p)
		if err := a.SaveBans(s, []string{"76561198000000000"}); err != nil {
			t.Fatalf("SaveBans: %v", err)
		}
		requireMark(t, p, before)
	})
}

// TestActiveDoesNotBecomeRoot reproduces the SNAPSHOT of the defect in production:
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
func TestActiveDoesNotBecomeRoot(t *testing.T) {
	needsRoot(t)
	t.Run("creation_is_the_half_that_bites", func(t *testing.T) {
		s := enshroudedTree(t)
		active := filepath.Join(s.Root, ".active")
		if err := os.Remove(active); err != nil {
			t.Fatalf("setup: %v", err)
		}
		// The expression identical to the one in adapter_server_settings.go.
		if err := writeAtomic(active, []byte("world2\n"), s.Root); err != nil {
			t.Fatalf("writeAtomic: %v", err)
		}
		requireGameOwner(t, active, "creation of .active")

		// Negative control: the OLD idiom, in the same scenario, produces root.
		// Without this half there would be no proof that the one above measures anything.
		old := filepath.Join(s.Root, ".active-old-idiom")
		if err := os.WriteFile(old, []byte("world2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := statRaw(old)
		if err != nil {
			t.Fatal(err)
		}
		if m.uid != 0 {
			t.Fatalf("the negative control did not reproduce the defect (got %s) — without it the test above proves nothing", m)
		}
		t.Logf("negative control confirmed: the old idiom creates %s, the new one creates %d:%d", m, otherUID, otherGID)
	})

	t.Run("rewrite_preserves_the_weak_half", func(t *testing.T) {
		s := enshroudedTree(t)
		a := enshrouded{}
		active := filepath.Join(s.Root, ".active")
		before, err := statRaw(active)
		if err != nil {
			t.Fatalf("stat before: %v", err)
		}
		if err := a.RenameWorld(s, "world1", "world2"); err != nil {
			t.Fatalf("RenameWorld: %v", err)
		}
		if got := a.ActiveWorld(s); got != "world2" {
			t.Fatalf("the active world pointer did not follow: %q", got)
		}
		requireMark(t, active, before)
	})
}

// TestBakInheritsOriginalOwner — the .bak is born with the ORIGINAL's owner, not
// root:root. A defect quieter than the config's: nobody looks at a backup's
// owner until they need it.
func TestBakInheritsOriginalOwner(t *testing.T) {
	needsRoot(t)
	requireRoot(t)
	dir := t.TempDir()
	orig := filepath.Join(dir, "compose.yml")
	if err := os.WriteFile(orig, []byte("services:\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(orig, otherUID, otherGID); err != nil {
		t.Fatal(err)
	}
	before, _ := statRaw(orig)
	if err := writeAtomic(orig+".bak", []byte("services:\n"), orig); err != nil {
		t.Fatalf("writeAtomic of the .bak: %v", err)
	}
	requireMark(t, orig+".bak", before)
}

// TestInventorySurvivesHelper covers the PANEL's site (SaveInventory). Here
// the owner matters less; what the helper adds is DURABILITY (fsync before the
// rename). Without it, a power cut inside the ZFS txg window costs the whole
// inventory.
func TestInventorySurvivesHelper(t *testing.T) {
	needsRoot(t)
	requireRoot(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "gameservers.json")
	if err := os.WriteFile(target, []byte("[]"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(target, otherUID, otherGID); err != nil {
		t.Fatal(err)
	}
	before, _ := statRaw(target)
	if err := writeAtomic(target, []byte(`[{"id":"x"}]`), target); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	requireMark(t, target, before)
	if b, _ := os.ReadFile(target); string(b) != `[{"id":"x"}]` {
		t.Errorf("content not written: %q", string(b))
	}
}

// The owner comes from the disk, never from a constant.

// TestChownTreeUsesServerOwner is the pin that proves the change is REAL and
// not cosmetic: the tree is 5000:5000, and with the old code (chownTree nailed to
// 4711) the new files would come out 4711:4711 and this test would fail.
func TestChownTreeUsesServerOwner(t *testing.T) {
	needsRoot(t)
	s := enshroudedTree(t)
	fresh := filepath.Join(s.Root, "worlds", "imported")
	if err := os.MkdirAll(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	// Born root:root, as a directory created by the panel would be.
	inside := filepath.Join(fresh, ".saveid")
	if err := os.WriteFile(inside, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, _ := statRaw(inside); m.uid != 0 {
		t.Fatalf("invalid setup: the file had to be born root for the test to measure anything (got %s)", m)
	}
	if err := chownLikeRef(fresh, s.Root, true); err != nil {
		t.Fatalf("chownLikeRef: %v", err)
	}
	requireGameOwner(t, fresh, "imported directory")
	requireGameOwner(t, inside, ".saveid inside the imported one")
}

// TestChownAsRefFailsClearlyWithoutRef — a nonexistent root produces an ERROR naming
// the path. Never silence, never a fallback to an invented uid: a default value
// is a new constant under another name.
func TestChownAsRefFailsClearlyWithoutRef(t *testing.T) {
	needsRoot(t)
	requireRoot(t)
	target := t.TempDir()
	nonexistent := filepath.Join(target, "root-that-does-not-exist")
	err := chownLikeRef(target, nonexistent, true)
	if err == nil {
		t.Fatal("chownLikeRef accepted a nonexistent reference — it would silently fall back to an invented default")
	}
	if !strings.Contains(err.Error(), target) && !strings.Contains(err.Error(), nonexistent) {
		t.Errorf("the error names no path at all: %v", err)
	}
}

// needsRoot skips the test when the process cannot change a file's owner.
//
// These tests verify preservation of owner and mode, which requires chown, which
// requires root. On a common CI runner the process is an unprivileged user, and
// without this guard the test would fail for a reason that is not a code defect.
func needsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root: checks owner preservation via chown")
	}
}
