package pve

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

var hypervisorBinaries = map[string]string{
	"pct":   "use POST /nodes/{node}/lxc/{vmid}/status/{verb} (internal/pve/power.go)",
	"qm":    "use POST /nodes/{node}/qemu/{vmid}/status/{verb} (internal/pve/power.go)",
	"pvesh": "use the internal/pve client: do() is the single constructor",
	"pvesm": "PVE storage is API too: /nodes/{node}/storage",
	"pveum": "user/ACL/token are /access/**",
}

var allowlist = map[string]string{}

var ignoredDirs = map[string]bool{
	".git": true, "node_modules": true, "testdata": true,
	"vendor": true, ".tools": true, "bin": true,
}

type hit struct {
	File   string
	Line   int
	Binary string
	How    string
}

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
			return nil
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
			return s, "variable " + id.Name, true
		}
	}
	return "", "", false
}

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

func TestNoHypervisorShellOutBites(t *testing.T) {
	cases := []struct {
		name   string
		source string
		finds  bool
		binary string
		how    string
	}{
		{
			name: "direct literal",
			source: `package x
import "os/exec"
func f() { _ = exec.Command("pct", "exec", "207", "--", "ls") }`,
			finds: true, binary: "pct", how: "literal",
		},
		{
			name: "command coming from a VARIABLE (the regex's blind spot)",
			source: `package x
import "os/exec"
func f() { bin := "pct"; _ = exec.Command(bin, "start", "207") }`,
			finds: true, binary: "pct", how: "variable bin",
		},
		{
			name: "hidden behind ssh (middle argument)",
			source: `package x
import "os/exec"
func f() { _ = exec.Command("ssh", "hypervisor-01", "qm", "start", "208") }`,
			finds: true, binary: "qm", how: "literal",
		},
		{
			name: "absolute path",
			source: `package x
import ctx "context"
import xc "os/exec"
func f() { _ = xc.CommandContext(ctx.TODO(), "/usr/sbin/pvesh", "get", "/cluster/resources") }`,
			finds: true, binary: "pvesh", how: "literal",
		},
		{
			name: "identifier diskUsedPct is not a call",
			source: `package x
// pct here is just a word in a comment: pct, qm, pvesh.
func diskUsedPct(path string) (pct int, err error) { return 0, nil }`,
			finds: false,
		},
		{
			name: "legitimate exec.Command passes",
			source: `package x
import "os/exec"
func f() { _ = exec.Command("systemctl", "restart", "server-control-panel") }`,
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
