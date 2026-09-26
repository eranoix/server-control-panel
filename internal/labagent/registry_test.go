package labagent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"server-control-panel/internal/gameservers"
)

// The catalogue is CLOSED, and "closed" here means three things that have to
// be proved separately, because each one covers a different hole:
//
//	1. every declared constant has a registry entry     → operation announced and not served
//	2. every registry key is a declared constant        → operation served and not announced
//	3. every constant of type OpName is in TodasAsOps   → ORPHAN constant, invisible to 1 and 2
//
// Item 3 is the one that is almost always missing. Without it, somebody
// declares `OpExec OpName = "manutencao.rodar"`, leaves it out of TodasAsOps,
// and the first two tests stay green forever.

// raizDoRepo walks up to the go.mod.
func raizDoRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
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
	t.Fatal("go.mod not found")
	return ""
}

func TestCatalogoFechadoTodasAsConstantesEstaoNoMapa(t *testing.T) {
	for _, op := range gameservers.TodasAsOps {
		if _, ok := registry[op]; !ok {
			t.Errorf("operation ANNOUNCED and not served: %q is in TodasAsOps but has no entry in the registry", op)
		}
	}
}

func TestCatalogoFechadoTodaChaveEhConstante(t *testing.T) {
	declaradas := map[gameservers.OpName]bool{}
	for _, op := range gameservers.TodasAsOps {
		declaradas[op] = true
	}
	for chave := range registry {
		if !declaradas[chave] {
			t.Errorf("operation SERVED and not announced: the registry has %q, which is not in TodasAsOps — a loose literal key is how a route gets in without review", chave)
		}
	}
}

// TestCatalogoFechadoConstantesDeclaradasBatemComTodasAsOps closes the ORPHAN
// constant hole, by walking the AST of opnames.go.
func TestCatalogoFechadoConstantesDeclaradasBatemComTodasAsOps(t *testing.T) {
	arquivo := filepath.Join(raizDoRepo(t), "internal", "gameservers", "opnames.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, arquivo, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse of %s: %v", arquivo, err)
	}

	naLista := map[string]bool{}
	for _, op := range gameservers.TodasAsOps {
		naLista[string(op)] = true
	}

	var declaradas int
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		// In a `const (...)` block with the type declared once, the type carries
		// over to the following lines: propagate the last type seen, as Go does.
		tipoCorrente := ""
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				if id, ok := vs.Type.(*ast.Ident); ok {
					tipoCorrente = id.Name
				}
			}
			if tipoCorrente != "OpName" {
				continue
			}
			for i, nome := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				valor, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				declaradas++
				if !naLista[valor] {
					t.Errorf("ORPHAN constant: %s = %q is declared in opnames.go but is NOT in TodasAsOps — invisible to the other two tests, and servable the moment someone puts it in the registry (line %d)",
						nome.Name, valor, fset.Position(nome.Pos()).Line)
				}
			}
		}
	}
	if declaradas == 0 {
		t.Fatal("no constant of type OpName found — the scan measured nothing, and green by ABSENCE is the defect this phase exists to close")
	}
	if declaradas != len(gameservers.TodasAsOps) {
		t.Errorf("%d OpName constants declared, but TodasAsOps has %d — the difference is where the invisible operation lives",
			declaradas, len(gameservers.TodasAsOps))
	}
	if !t.Failed() {
		t.Logf("%d OpName constants declared, all of them in TodasAsOps", declaradas)
	}
}

// TestCatalogoNomesBemFormados asserts a PROPERTY, never a count.
//
// ⚠️ Do NOT assert the absolute count of TodasAsOps here (the literal of the
// forbidden comparison does not even appear in this comment, because the
// acceptance criterion matches by fixed text and does not discount comments —
// the same trap that has already caught this project once).
// Freezing a count is a defect this house has already lived through:
// test_invariantes_d.sh stayed stuck at 21 when there were already 65, and
// nobody noticed because the number looked intentional. On top of that,
// freezing the count would break the M5 negative control, which adds a
// LEGITIMATE operation and has to pass.
func TestCatalogoNomesBemFormados(t *testing.T) {
	visto := map[gameservers.OpName]bool{}
	familias := map[string]int{}
	for _, op := range gameservers.TodasAsOps {
		if visto[op] {
			t.Errorf("duplicate name in TodasAsOps: %q", op)
		}
		visto[op] = true

		partes := strings.Split(string(op), ".")
		if len(partes) != 2 || partes[0] == "" || partes[1] == "" {
			t.Errorf("%q does not have the family.verb shape", op)
			continue
		}
		if !gameservers.FamiliasValidas[partes[0]] {
			t.Errorf("%q uses a family outside the closed set: %q", op, partes[0])
		}
		familias[partes[0]]++
	}
	for fam := range gameservers.FamiliasValidas {
		if familias[fam] == 0 {
			t.Errorf("family declared and empty: %q — either it has an operation, or it leaves the set", fam)
		}
	}
}

// TestHandlersSaoExpressaoDeMetodo — no Handler value in the registry is a
// function literal.
//
// It is the structural half of narrowness: a function literal can CAPTURE a
// variable from the enclosing scope, and capture is how a free argument gets in
// without showing up in the signature. A method expression (`(*Agent).opFoo`)
// captures nothing.
func TestHandlersSaoExpressaoDeMetodo(t *testing.T) {
	arquivo := filepath.Join(raizDoRepo(t), "internal", "labagent", "registry.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, arquivo, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var achados []string
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		chave, ok := kv.Key.(*ast.Ident)
		if !ok || chave.Name != "Handler" {
			return true
		}
		if _, ehLiteral := kv.Value.(*ast.FuncLit); ehLiteral {
			achados = append(achados, fset.Position(kv.Pos()).String())
		}
		return true
	})
	if len(achados) > 0 {
		t.Errorf("Handler as a function literal at: %s — a literal captures a variable from the scope, and capture is how a free argument gets in without showing up in the signature. Use a method expression.",
			strings.Join(achados, ", "))
	}
}
