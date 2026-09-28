package nodeagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var forbiddenTerms = []string{"path", "mode", "uid", "gid", "filemode"}

func scanBackendInterface(t *testing.T, file string) (violations []string, methodCount int) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse of %s: %v", file, err)
	}

	suspicious := func(s string) bool {
		b := strings.ToLower(s)
		for _, term := range forbiddenTerms {
			if b == term || strings.HasSuffix(b, term) {
				return true
			}
		}
		return false
	}

	var typeText func(ast.Expr) string
	typeText = func(e ast.Expr) string {
		switch v := e.(type) {
		case *ast.Ident:
			return v.Name
		case *ast.SelectorExpr:
			return typeText(v.X) + "." + v.Sel.Name
		case *ast.StarExpr:
			return typeText(v.X)
		case *ast.ArrayType:
			return typeText(v.Elt)
		}
		return ""
	}

	matches := func(fields *ast.FieldList, where, method string) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			kind := typeText(field.Type)
			if suspicious(kind) {
				violations = append(violations, method+": "+where+" of type "+kind)
			}
			for _, name := range field.Names {
				if suspicious(name.Name) {
					violations = append(violations, method+": "+where+" named "+name.Name)
				}
			}
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		it, ok := n.(*ast.InterfaceType)
		if !ok || it.Methods == nil {
			return true
		}
		for _, m := range it.Methods.List {
			ft, ok := m.Type.(*ast.FuncType)
			if !ok || len(m.Names) == 0 {
				continue
			}
			methodCount++
			matches(ft.Params, "parameter", m.Names[0].Name)
			matches(ft.Results, "result", m.Names[0].Name)
		}
		return true
	})
	return violations, methodCount
}

func TestBackendDoesNotLeakFileSemantics(t *testing.T) {
	file := filepath.Join(repoRoot(t), "internal", "gameservers", "backend.go")
	violations, methodCount := scanBackendInterface(t, file)
	if methodCount == 0 {
		t.Fatal("no interface method scanned — green by ABSENCE")
	}
	if len(violations) > 0 {
		t.Errorf("boundary violated: file semantics crossing the boundary:\n  %s\n"+
			"A path that crosses is a path the client chooses. Use an opaque Handle.",
			strings.Join(violations, "\n  "))
	}
	t.Logf("%d interface methods scanned, 0 violations", methodCount)
}

func TestBackendScanBitesAndControlsNegative(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad.go")
	if err := os.WriteFile(bad, []byte(`package x
import "os"
type Backend interface {
	Write(path string, mode os.FileMode) error
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, m := scanBackendInterface(t, bad); len(v) == 0 {
		t.Errorf("FALSE NEGATIVE: the scan saw neither `path string` nor `os.FileMode` (%d methods scanned)", m)
	} else {
		t.Logf("it bit as it should: %s", strings.Join(v, "; "))
	}

	bom := filepath.Join(dir, "bom.go")
	if err := os.WriteFile(bom, []byte(`package x
import "context"
type World struct{}
type Backend interface {
	Worlds(ctx context.Context, id string) ([]World, error)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, m := scanBackendInterface(t, bom); len(v) != 0 {
		t.Errorf("FALSE POSITIVE on the legitimate method (%d scanned): %s", m, strings.Join(v, "; "))
	}
}
