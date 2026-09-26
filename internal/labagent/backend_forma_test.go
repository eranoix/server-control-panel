package labagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rule as a PIN, not as a good intention.
//
// The rule: no parameter and no result of the Backend interface may be `path`,
// `mode`, `uid`, `gid` or `os.FileMode`. A host path that crosses the boundary
// is a path the client can CHOOSE — and the day the dashboard sends a path, the
// agent stops being narrow without a single new route having appeared. It is
// how the property gets lost with no signal at all.

// termosProibidos matches by identifier name (parameter/result) and by the
// written type.
var termosProibidos = []string{"path", "caminho", "mode", "modo", "uid", "gid", "filemode"}

// varreInterfaceBackend returns the violations found in the given file.
// Kept separate from the test so the NEGATIVE CONTROL can reuse exactly the
// same scan over a legitimate fixture — an instrument that only knows how to
// fail things is the instrument somebody turns off.
func varreInterfaceBackend(t *testing.T, arquivo string) (violacoes []string, metodos int) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, arquivo, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse of %s: %v", arquivo, err)
	}

	suspeito := func(s string) bool {
		b := strings.ToLower(s)
		for _, termo := range termosProibidos {
			if b == termo || strings.HasSuffix(b, termo) {
				return true
			}
		}
		return false
	}

	// textoDoTipo renders the type in comparable form (e.g. "os.FileMode").
	var textoDoTipo func(ast.Expr) string
	textoDoTipo = func(e ast.Expr) string {
		switch v := e.(type) {
		case *ast.Ident:
			return v.Name
		case *ast.SelectorExpr:
			return textoDoTipo(v.X) + "." + v.Sel.Name
		case *ast.StarExpr:
			return textoDoTipo(v.X)
		case *ast.ArrayType:
			return textoDoTipo(v.Elt)
		}
		return ""
	}

	confere := func(campos *ast.FieldList, onde, metodo string) {
		if campos == nil {
			return
		}
		for _, campo := range campos.List {
			tipo := textoDoTipo(campo.Type)
			if suspeito(tipo) {
				violacoes = append(violacoes, metodo+": "+onde+" de tipo "+tipo)
			}
			for _, nome := range campo.Names {
				if suspeito(nome.Name) {
					violacoes = append(violacoes, metodo+": "+onde+" chamado "+nome.Name)
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
			metodos++
			confere(ft.Params, "parâmetro", m.Names[0].Name)
			confere(ft.Results, "resultado", m.Names[0].Name)
		}
		return true
	})
	return violacoes, metodos
}

func TestBackendNaoVazaSemanticaDeArquivo(t *testing.T) {
	arquivo := filepath.Join(raizDoRepo(t), "internal", "gameservers", "backend.go")
	violacoes, metodos := varreInterfaceBackend(t, arquivo)
	if metodos == 0 {
		t.Fatal("no interface method scanned — green by ABSENCE")
	}
	if len(violacoes) > 0 {
		t.Errorf("fronteira violada — semântica de arquivo atravessando a fronteira:\n  %s\n"+
			"Caminho que atravessa é caminho que o cliente escolhe. Use Handle opaco.",
			strings.Join(violacoes, "\n  "))
	}
	t.Logf("%d interface methods scanned, 0 violations", metodos)
}

// TestBackendVarreduraMordeEControlaNegativo — does the scan measure anything?
//
// Two synthetic fixtures: one with the violation (it must fail) and one with a
// legitimate method (it must NOT fail). Without the second, a pin that failed
// everything would pass in this file and would break the real interface the
// first time anyone extended it.
func TestBackendVarreduraMordeEControlaNegativo(t *testing.T) {
	dir := t.TempDir()

	ruim := filepath.Join(dir, "ruim.go")
	if err := os.WriteFile(ruim, []byte(`package x
import "os"
type Backend interface {
	Gravar(path string, modo os.FileMode) error
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, m := varreInterfaceBackend(t, ruim); len(v) == 0 {
		t.Errorf("FALSE NEGATIVE: the scan saw neither `path string` nor `os.FileMode` (%d methods scanned)", m)
	} else {
		t.Logf("it bit as it should: %s", strings.Join(v, "; "))
	}

	bom := filepath.Join(dir, "bom.go")
	if err := os.WriteFile(bom, []byte(`package x
import "context"
type World struct{}
type Backend interface {
	Mundos(ctx context.Context, id string) ([]World, error)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, m := varreInterfaceBackend(t, bom); len(v) != 0 {
		t.Errorf("FALSE POSITIVE on the legitimate method (%d scanned): %s", m, strings.Join(v, "; "))
	}
}
