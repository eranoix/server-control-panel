package astcheck_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var allowedInventoryMethods = map[string]bool{
	"List":          true,
	"Get":           true,
	"SaveInventory": true,
	"Reload":        true,
}

func operationMethods(t *testing.T) map[string]bool {
	t.Helper()
	root := moduleRoot(t)
	fset := token.NewFileSet()
	findings := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(root, "internal", "gameservers"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(root, "internal", "gameservers", e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			id, ok := star.X.(*ast.Ident)
			if !ok || id.Name != "Manager" {
				continue
			}
			if !fn.Name.IsExported() || allowedInventoryMethods[fn.Name.Name] {
				continue
			}
			findings[fn.Name.Name] = true
		}
	}
	if len(findings) == 0 {
		t.Fatal("no Manager operation method found — the guard would be blind")
	}
	return findings
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found starting from the test directory")
	return ""
}

func TestHandlersDoNotCallManagerDirectly(t *testing.T) {
	forbiddenBins := operationMethods(t)
	fset := token.NewFileSet()
	dirAPI := filepath.Join(moduleRoot(t), "internal", "api")
	entries, err := os.ReadDir(dirAPI)
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dirAPI, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !forbiddenBins[sel.Sel.Name] {
				return true
			}
			recept, ok := sel.X.(*ast.SelectorExpr)
			if !ok || recept.Sel.Name != "gameMgr" {
				return true
			}
			t.Errorf("DIRECT CALL TO THE MANAGER: %s.%s at %s:%d — this screen stayed on the old path and operates the PANEL's disk, not the node's",
				"gameMgr", sel.Sel.Name, name, fset.Position(call.Pos()).Line)
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("no file scanned — green by absence")
	}
	t.Logf("%d files scanned, %d operation methods watched", scanned, len(forbiddenBins))
}

func TestManagerPinBites(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "regression.go")
	source := `package api
func (r *Router) forgottenScreen() { _ = r.gameMgr.Worlds(srv) }
`
	if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	caught := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Worlds" {
			return true
		}
		recept, ok := sel.X.(*ast.SelectorExpr)
		if ok && recept.Sel.Name == "gameMgr" {
			caught = true
		}
		return true
	})
	if !caught {
		t.Error("the guard would NOT catch a screen that went back to calling the Manager")
	}
}
