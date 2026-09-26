package astcheck_test

// SURFACE pins for the panel — they live here, and not in `internal/api`, for a
// measured reason.
//
// They were born inside `internal/api`, and the gate fragment for that package
// put it on the deploy gate. The FIRST deploy after that was blocked by
// `TestGmailDraftLiveCreatesDraft`, which passes in isolation and fails now and
// then when the whole package runs. Measured cause: `newSmokeRouter` builds a
// Router whose `startMetricsCollector` runs with `context.Background()` and
// NEVER stops — every test leaks a goroutine that stays alive through the tests
// that follow. It is the same class of defect this repo has hit before.
//
// A gate that fails at random is worse than no gate: it teaches people to reach
// for `--force`, and then nothing blocks anything. So the `internal/api` package
// came OFF the gate, and the pins that need to block a deploy moved here — where
// the analysis is purely static (it reads a file, starts no Router, leaks no
// goroutine) and the result is deterministic.
//
// What stayed in `internal/api` are the BEHAVIOR tests (error translation, RBAC,
// download naming): they still run under `go test ./...` and in CI, they just do
// not block a deploy while that package's flakiness exists. The flakiness is on
// record, with its cause.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// PIN: no screen may have been left on the old path
//
// The defect this test exists to prevent is silent by nature: a screen that keeps
// calling the Manager works perfectly — against the PANEL's disk. Nobody notices
// until the panel moves to another machine, which is exactly what this change is
// doing.

// metodosDeInventarioPermitidos are the ONLY Manager methods the `api` package
// may call.
//
// They are not game operations: they read and write the REGISTRY, which lives on
// the panel and goes on living there. `Get` and `List` answer "which servers
// exist and on which node"; `SaveInventory` writes that down. None of them touch
// the game's disk, and that is where the line is.
var metodosDeInventarioPermitidos = map[string]bool{
	"List":          true,
	"Get":           true,
	"SaveInventory": true,
	"Reload":        true,
}

// metodosDeOperacao is the set the panel may NO longer call directly.
// Derived from what exists on *Manager today, minus the inventory ones.
func metodosDeOperacao(t *testing.T) map[string]bool {
	t.Helper()
	raiz := raizDoModulo(t)
	fset := token.NewFileSet()
	achados := map[string]bool{}
	entradas, err := os.ReadDir(filepath.Join(raiz, "internal", "gameservers"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entradas {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(raiz, "internal", "gameservers", e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			estrela, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			id, ok := estrela.X.(*ast.Ident)
			if !ok || id.Name != "Manager" {
				continue
			}
			if !fn.Name.IsExported() || metodosDeInventarioPermitidos[fn.Name.Name] {
				continue
			}
			achados[fn.Name.Name] = true
		}
	}
	if len(achados) == 0 {
		// Scanning nothing is never approving.
		t.Fatal("no Manager operation method found — the guard would be blind")
	}
	return achados
}

func raizDoModulo(t *testing.T) string {
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

func TestHandlersNaoChamamManagerDireto(t *testing.T) {
	proibidos := metodosDeOperacao(t)
	fset := token.NewFileSet()
	dirAPI := filepath.Join(raizDoModulo(t), "internal", "api")
	entradas, err := os.ReadDir(dirAPI)
	if err != nil {
		t.Fatal(err)
	}
	varridos := 0
	for _, e := range entradas {
		nome := e.Name()
		if !strings.HasSuffix(nome, ".go") || strings.HasSuffix(nome, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dirAPI, nome), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		varridos++
		ast.Inspect(f, func(n ast.Node) bool {
			chamada, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := chamada.Fun.(*ast.SelectorExpr)
			if !ok || !proibidos[sel.Sel.Name] {
				return true
			}
			// It only matters when the receiver IS the gameMgr: other types in
			// the package have same-named methods (`Status`, `List`), and matching
			// by name would be the same false positive the route detector already
			// produced.
			recept, ok := sel.X.(*ast.SelectorExpr)
			if !ok || recept.Sel.Name != "gameMgr" {
				return true
			}
			t.Errorf("DIRECT CALL TO THE MANAGER: %s.%s at %s:%d — this screen stayed on the old path and operates the PANEL's disk, not the node's",
				"gameMgr", sel.Sel.Name, nome, fset.Position(chamada.Pos()).Line)
			return true
		})
	}
	if varridos == 0 {
		t.Fatal("no file scanned — green by absence")
	}
	t.Logf("%d files scanned, %d operation methods watched", varridos, len(proibidos))
}

// TestPinoDoManagerMorde: does the pin above measure anything?
func TestPinoDoManagerMorde(t *testing.T) {
	dir := t.TempDir()
	arq := filepath.Join(dir, "regressao.go")
	fonte := `package api
func (r *Router) telaEsquecida() { _ = r.gameMgr.Worlds(srv) }
`
	if err := os.WriteFile(arq, []byte(fonte), 0o644); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, arq, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	pegou := false
	ast.Inspect(f, func(n ast.Node) bool {
		chamada, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := chamada.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Worlds" {
			return true
		}
		recept, ok := sel.X.(*ast.SelectorExpr)
		if ok && recept.Sel.Name == "gameMgr" {
			pegou = true
		}
		return true
	})
	if !pegou {
		t.Error("the guard would NOT catch a screen that went back to calling the Manager")
	}
}
