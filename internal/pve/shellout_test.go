package pve

// shellout_test.go — the PIN that guards the no-reimplementation rule.
//
// NEW PRECEDENT in this repository: it is the first test that walks the whole
// code tree. There was no analogue in the codebase to copy, so the
// rationale lives here, in the file.
//
// # Why it exists
//
// The rule says hypervisor operations go "through the PVE API, NEVER
// reimplemented in the agent", and the acceptance criterion asks that "a search in the
// code find no reimplementation". A search done by hand gets forgotten; a test runs
// on every `make test`, forever. An `exec.Command("ssh", host, "pct",
// "start", id)` would turn the panel into SSH with extra steps and erase the
// entire rationale of the design.
//
// # The chosen way out: go/ast, not regex
//
// There were two options: (a) walk the AST, (b) keep the regex and extend it to the
// `bin := "pct"` form. I chose (a). The regex has BOTH defects measured during
// the research:
//
//   - false POSITIVE: `grep -rn "pct "` already matches today
//     internal/queue/runners_watchdog.go:45 — `func diskUsedPct(path string)
//     (pct int, …)`. A pin that fails legitimate code is a pin somebody
//     switches off on the first Friday.
//   - false NEGATIVE: `exec.Command(bin, "exec")` with `bin := "pct"` escapes the
//     literal form, and an innocent future refactor would sail through make test
//     unseen.
//
// The AST has neither: it sees a CALL and an ARGUMENT, not text.
// `diskUsedPct` is an identifier, not an argument of exec.Command; and a
// literal assigned to a variable is resolved before the comparison.
//
// # Residual risk, named
//
// Variable resolution is INTRA-FILE and covers a direct literal. Still
// out of reach: a literal coming from another file/package, a name built by
// concatenation or fmt.Sprintf, and execution through os/exec via a wrapper of our own.
// Closing that would require type analysis (go/types + SSA), which is disproportionate
// for a repository where the right answer today is ZERO occurrences. The risk
// is recorded in the threat model, not hidden behind a green check.
//
// # Scope of the sweep
//
// _test.go files are left OUT: they do not go into the published binary, and it is
// in this very _test.go that the forbidden literals used as fixtures live.
// A hypervisor shell-out in a test is not a reimplementation in the agent — it is
// a fixture; what the criterion protects is what runs in production.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// hypervisorBinaries are the commands the panel must NOT invoke. Each one has
// an equivalent API route, and it is the route the design rule mandates.
var hypervisorBinaries = map[string]string{
	"pct":   "use POST /nodes/{node}/lxc/{vmid}/status/{verb} (internal/pve/power.go)",
	"qm":    "use POST /nodes/{node}/qemu/{vmid}/status/{verb} (internal/pve/power.go)",
	"pvesh": "use o cliente internal/pve — do() é o construtor único",
	"pvesm": "storage do PVE também é API: /nodes/{node}/storage",
	"pveum": "usuário/ACL/token são /access/** — ver bin/pve-credencial",
}

// allowlist is the list of DECLARED exceptions: path → reason. It is empty
// today, and empty is the right answer. It exists so that a future exception
// has to be written down, with a reason, instead of the pin being loosened.
var allowlist = map[string]string{}

// ignoredDirs are not Go code of the panel.
var ignoredDirs = map[string]bool{
	".git": true, "node_modules": true, "testdata": true,
	"vendor": true, ".tools": true, "bin": true,
}

type hit struct {
	File   string
	Line   int
	Binary string
	How    string // "literal" or "variável"
}

// scanHypervisorShellOut walks the tree starting at root and returns every call
// to exec.Command/exec.CommandContext that passes a hypervisor binary as an
// argument — literal, or coming from a variable whose literal is in the same
// file.
//
// It also returns how many files were scanned: a pin that scans nothing stays
// green by ABSENCE, and green by absence is the defect that has already turned
// up six times in this work.
func scanHypervisorShellOut(root string) (findings []hit, scanned int, err error) {
	fset := token.NewFileSet()

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if ignoredDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if _, ok := allowlist[filepath.ToSlash(rel)]; ok {
			return nil
		}

		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		scanned++

		execName := localOsExecName(file)
		if execName == "" {
			return nil // the file does not even import os/exec
		}
		literals := stringLiterals(file)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != execName {
				return true
			}
			if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
				return true
			}
			for _, arg := range call.Args {
				value, how, ok := resolveString(arg, literals)
				if !ok {
					continue
				}
				// Compare the BASENAME: "/usr/sbin/pct" is the same command.
				base := filepath.Base(strings.TrimSpace(value))
				if _, forbidden := hypervisorBinaries[base]; !forbidden {
					continue
				}
				findings = append(findings, hit{
					File:   filepath.ToSlash(rel),
					Line:   fset.Position(arg.Pos()).Line,
					Binary: base,
					How:    how,
				})
			}
			return true
		})
		return nil
	})
	return findings, scanned, walkErr
}

// localOsExecName returns the name under which "os/exec" was imported in this
// file (normally "exec", but an alias would change that and the regex would not
// even see it).
func localOsExecName(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "os/exec" {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "exec"
	}
	return ""
}

// stringLiterals maps identifier → string literal assigned to it in the same
// file. It is what closes the `bin := "pct"` false negative.
func stringLiterals(file *ast.File) map[string]string {
	lits := map[string]string{}
	record := func(name ast.Expr, value ast.Expr) {
		id, ok := name.(*ast.Ident)
		if !ok {
			return
		}
		if s, ok := stringLiteral(value); ok {
			lits[id.Name] = s
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range v.Lhs {
				if i < len(v.Rhs) {
					record(lhs, v.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, name := range v.Names {
				if i < len(v.Values) {
					record(name, v.Values[i])
				}
			}
		}
		return true
	})
	return lits
}

func stringLiteral(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(b.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func resolveString(e ast.Expr, literals map[string]string) (value, how string, ok bool) {
	if s, ok := stringLiteral(e); ok {
		return s, "literal", true
	}
	if id, isID := e.(*ast.Ident); isID {
		if s, found := literals[id.Name]; found {
			return s, "variável " + id.Name, true
		}
	}
	return "", "", false
}

// TestNoHypervisorShellOut is the pin itself: the REAL tree of the panel, today
// and forever, without a single invocation of pct/qm/pvesh.
func TestNoHypervisorShellOut(t *testing.T) {
	root := repoRoot(t)
	total := 0
	for _, dir := range []string{"internal", "cmd"} {
		findings, scanned, err := scanHypervisorShellOut(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("scanning %s: %v", dir, err)
		}
		if scanned == 0 {
			t.Fatalf("%s: no .go scanned — the guard would be green by ABSENCE", dir)
		}
		total += scanned
		for _, a := range findings {
			t.Errorf("%s/%s:%d invokes %q (%s) — %s",
				dir, a.File, a.Line, a.Binary, a.How, hypervisorBinaries[a.Binary])
		}
	}
	t.Logf("%d production .go files scanned, 0 hypervisor shell-outs", total)
}

// 🔴 TestNoHypervisorShellOutBites is the TEST OF THE TEST (the false-green
// antidote). Without it, the green above would prove only that the function
// LABELS, not that it DETECTS.
//
// Three synthetic violations and two negative controls. The second case is the
// one the plan demanded explicitly: a command coming from a VARIABLE — the real
// hole in the regex detector, and the reason this pin is go/ast.
func TestNoHypervisorShellOutBites(t *testing.T) {
	cases := []struct {
		name   string
		source string
		finds  bool
		binary string
		how    string
	}{
		{
			name: "literal direto",
			source: `package x
import "os/exec"
func f() { _ = exec.Command("pct", "exec", "207", "--", "ls") }`,
			finds: true, binary: "pct", how: "literal",
		},
		{
			name: "comando vindo de VARIAVEL (o furo da regex)",
			source: `package x
import "os/exec"
func f() { bin := "pct"; _ = exec.Command(bin, "start", "207") }`,
			finds: true, binary: "pct", how: "variável bin",
		},
		{
			name: "escondido atras de ssh (argumento do meio)",
			source: `package x
import "os/exec"
func f() { _ = exec.Command("ssh", "hypervisor-01", "qm", "start", "208") }`,
			finds: true, binary: "qm", how: "literal",
		},
		{
			name: "caminho absoluto",
			source: `package x
import ctx "context"
import xc "os/exec"
func f() { _ = xc.CommandContext(ctx.TODO(), "/usr/sbin/pvesh", "get", "/cluster/resources") }`,
			finds: true, binary: "pvesh", how: "literal",
		},
		{
			// Negative control 1: the false positive MEASURED in the real tree
			// (internal/queue/runners_watchdog.go:45). If this case fails, the pin is
			// of the kind somebody turns off.
			name: "identificador diskUsedPct nao e chamada",
			source: `package x
// pct here is just a word in a comment: pct, qm, pvesh.
func diskUsedPct(path string) (pct int, err error) { return 0, nil }`,
			finds: false,
		},
		{
			// Negative control 2: a legitimate exec.Command stays allowed — the panel
			// runs git, docker and systemctl all the time.
			name: "exec.Command legitimo passa",
			source: `package x
import "os/exec"
func f() { _ = exec.Command("systemctl", "restart", "vps-manager") }`,
			finds: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			findings, scanned, err := scanHypervisorShellOut(dir)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if scanned != 1 {
				t.Fatalf("scanned %d files, want 1", scanned)
			}
			if !tc.finds {
				if len(findings) != 0 {
					t.Fatalf("FALSE POSITIVE: %+v", findings)
				}
				return
			}
			if len(findings) == 0 {
				t.Fatalf("FALSE NEGATIVE: the detector did NOT find %q — the guard's green would be empty", tc.binary)
			}
			a := findings[0]
			if a.Binary != tc.binary || a.How != tc.how {
				t.Fatalf("finding = %+v, want binary %q as %q", a, tc.binary, tc.how)
			}
			if a.Line <= 0 || a.File != "fixture.go" {
				t.Fatalf("finding with no usable file:line: %+v", a)
			}
		})
	}
}

// TestShellOutIgnoresTests documents the scope cut with an assertion instead of
// leaving it only in the comment: a fixture in _test.go is not a
// reimplementation in the agent. If this cut ever stops holding, this is where
// it changes.
func TestShellOutIgnoresTests(t *testing.T) {
	dir := t.TempDir()
	source := `package x
import "os/exec"
func f() { _ = exec.Command("pct", "start", "207") }`
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, scanned, err := scanHypervisorShellOut(dir)
	if err != nil {
		t.Fatal(err)
	}
	if scanned != 0 || len(findings) != 0 {
		t.Fatalf("_test.go entered the scan: scanned=%d findings=%+v", scanned, findings)
	}
}

// repoRoot climbs until it finds the go.mod — the scan needs the whole tree,
// not only the package this test lives in.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("go.mod not found walking up from the package")
	return ""
}
