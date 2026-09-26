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

// binariosDeHipervisor are the commands the panel must NOT invoke. Each one has
// an equivalent API route, and it is the route the design rule mandates.
var binariosDeHipervisor = map[string]string{
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

// diretoriosIgnorados are not Go code of the panel.
var diretoriosIgnorados = map[string]bool{
	".git": true, "node_modules": true, "testdata": true,
	"vendor": true, ".tools": true, "bin": true,
}

type achado struct {
	Arquivo string
	Linha   int
	Binario string
	Como    string // "literal" or "variável"
}

// scanHypervisorShellOut walks the tree starting at root and returns every call
// to exec.Command/exec.CommandContext that passes a hypervisor binary as an
// argument — literal, or coming from a variable whose literal is in the same
// file.
//
// It also returns how many files were scanned: a pin that scans nothing stays
// green by ABSENCE, and green by absence is the defect that has already turned
// up six times in this work.
func scanHypervisorShellOut(root string) (achados []achado, varridos int, err error) {
	fset := token.NewFileSet()

	walkErr := filepath.WalkDir(root, func(caminho string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if diretoriosIgnorados[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, caminho)
		if _, ok := allowlist[filepath.ToSlash(rel)]; ok {
			return nil
		}

		arq, perr := parser.ParseFile(fset, caminho, nil, 0)
		if perr != nil {
			return perr
		}
		varridos++

		nomeExec := nomeLocalDeOsExec(arq)
		if nomeExec == "" {
			return nil // the file does not even import os/exec
		}
		literais := literaisDeString(arq)

		ast.Inspect(arq, func(n ast.Node) bool {
			chamada, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := chamada.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != nomeExec {
				return true
			}
			if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
				return true
			}
			for _, arg := range chamada.Args {
				valor, como, ok := resolveString(arg, literais)
				if !ok {
					continue
				}
				// Compare the BASENAME: "/usr/sbin/pct" is the same command.
				base := filepath.Base(strings.TrimSpace(valor))
				if _, proibido := binariosDeHipervisor[base]; !proibido {
					continue
				}
				achados = append(achados, achado{
					Arquivo: filepath.ToSlash(rel),
					Linha:   fset.Position(arg.Pos()).Line,
					Binario: base,
					Como:    como,
				})
			}
			return true
		})
		return nil
	})
	return achados, varridos, walkErr
}

// nomeLocalDeOsExec returns the name under which "os/exec" was imported in this
// file (normally "exec", but an alias would change that and the regex would not
// even see it).
func nomeLocalDeOsExec(arq *ast.File) string {
	for _, imp := range arq.Imports {
		caminho, err := strconv.Unquote(imp.Path.Value)
		if err != nil || caminho != "os/exec" {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "exec"
	}
	return ""
}

// literaisDeString maps identifier → string literal assigned to it in the same
// file. It is what closes the `bin := "pct"` false negative.
func literaisDeString(arq *ast.File) map[string]string {
	lits := map[string]string{}
	guarda := func(nome ast.Expr, valor ast.Expr) {
		id, ok := nome.(*ast.Ident)
		if !ok {
			return
		}
		if s, ok := literalDeString(valor); ok {
			lits[id.Name] = s
		}
	}
	ast.Inspect(arq, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range v.Lhs {
				if i < len(v.Rhs) {
					guarda(lhs, v.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			for i, nome := range v.Names {
				if i < len(v.Values) {
					guarda(nome, v.Values[i])
				}
			}
		}
		return true
	})
	return lits
}

func literalDeString(e ast.Expr) (string, bool) {
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

func resolveString(e ast.Expr, literais map[string]string) (valor, como string, ok bool) {
	if s, ok := literalDeString(e); ok {
		return s, "literal", true
	}
	if id, isID := e.(*ast.Ident); isID {
		if s, achou := literais[id.Name]; achou {
			return s, "variável " + id.Name, true
		}
	}
	return "", "", false
}

// TestNoHypervisorShellOut is the pin itself: the REAL tree of the panel, today
// and forever, without a single invocation of pct/qm/pvesh.
func TestNoHypervisorShellOut(t *testing.T) {
	raiz := repoRoot(t)
	total := 0
	for _, dir := range []string{"internal", "cmd"} {
		achados, varridos, err := scanHypervisorShellOut(filepath.Join(raiz, dir))
		if err != nil {
			t.Fatalf("scanning %s: %v", dir, err)
		}
		if varridos == 0 {
			t.Fatalf("%s: no .go scanned — the guard would be green by ABSENCE", dir)
		}
		total += varridos
		for _, a := range achados {
			t.Errorf("%s/%s:%d invokes %q (%s) — %s",
				dir, a.Arquivo, a.Linha, a.Binario, a.Como, binariosDeHipervisor[a.Binario])
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
	casos := []struct {
		nome    string
		fonte   string
		acha    bool
		binario string
		como    string
	}{
		{
			nome: "literal direto",
			fonte: `package x
import "os/exec"
func f() { _ = exec.Command("pct", "exec", "207", "--", "ls") }`,
			acha: true, binario: "pct", como: "literal",
		},
		{
			nome: "comando vindo de VARIAVEL (o furo da regex)",
			fonte: `package x
import "os/exec"
func f() { bin := "pct"; _ = exec.Command(bin, "start", "207") }`,
			acha: true, binario: "pct", como: "variável bin",
		},
		{
			nome: "escondido atras de ssh (argumento do meio)",
			fonte: `package x
import "os/exec"
func f() { _ = exec.Command("ssh", "hypervisor-01", "qm", "start", "208") }`,
			acha: true, binario: "qm", como: "literal",
		},
		{
			nome: "caminho absoluto",
			fonte: `package x
import ctx "context"
import xc "os/exec"
func f() { _ = xc.CommandContext(ctx.TODO(), "/usr/sbin/pvesh", "get", "/cluster/resources") }`,
			acha: true, binario: "pvesh", como: "literal",
		},
		{
			// Negative control 1: the false positive MEASURED in the real tree
			// (internal/queue/runners_watchdog.go:45). If this case fails, the pin is
			// of the kind somebody turns off.
			nome: "identificador diskUsedPct nao e chamada",
			fonte: `package x
// pct here is just a word in a comment: pct, qm, pvesh.
func diskUsedPct(path string) (pct int, err error) { return 0, nil }`,
			acha: false,
		},
		{
			// Negative control 2: a legitimate exec.Command stays allowed — the panel
			// runs git, docker and systemctl all the time.
			nome: "exec.Command legitimo passa",
			fonte: `package x
import "os/exec"
func f() { _ = exec.Command("systemctl", "restart", "vps-manager") }`,
			acha: false,
		},
	}

	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(tc.fonte), 0o644); err != nil {
				t.Fatal(err)
			}
			achados, varridos, err := scanHypervisorShellOut(dir)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if varridos != 1 {
				t.Fatalf("scanned %d files, want 1", varridos)
			}
			if !tc.acha {
				if len(achados) != 0 {
					t.Fatalf("FALSE POSITIVE: %+v", achados)
				}
				return
			}
			if len(achados) == 0 {
				t.Fatalf("FALSE NEGATIVE: the detector did NOT find %q — the guard's green would be empty", tc.binario)
			}
			a := achados[0]
			if a.Binario != tc.binario || a.Como != tc.como {
				t.Fatalf("finding = %+v, want binary %q as %q", a, tc.binario, tc.como)
			}
			if a.Linha <= 0 || a.Arquivo != "fixture.go" {
				t.Fatalf("finding with no usable file:line: %+v", a)
			}
		})
	}
}

// TestShellOutIgnoraTestes documents the scope cut with an assertion instead of
// leaving it only in the comment: a fixture in _test.go is not a
// reimplementation in the agent. If this cut ever stops holding, this is where
// it changes.
func TestShellOutIgnoraTestes(t *testing.T) {
	dir := t.TempDir()
	fonte := `package x
import "os/exec"
func f() { _ = exec.Command("pct", "start", "207") }`
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"), []byte(fonte), 0o644); err != nil {
		t.Fatal(err)
	}
	achados, varridos, err := scanHypervisorShellOut(dir)
	if err != nil {
		t.Fatal(err)
	}
	if varridos != 0 || len(achados) != 0 {
		t.Fatalf("_test.go entered the scan: scanned=%d findings=%+v", varridos, achados)
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
		pai := filepath.Dir(dir)
		if pai == dir {
			break
		}
		dir = pai
	}
	t.Fatal("go.mod not found walking up from the package")
	return ""
}
