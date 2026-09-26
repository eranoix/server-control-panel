package labagent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"server-control-panel/internal/astcheck"
)

// The SECOND PIN of the narrowness criterion: the agent's narrowness, verified
// by AST.
//
// WHY THIS PIN EXISTS, when there is already a textual invariant
//
// The textual invariant uses `grep -cF` — a FIXED string. `POST /exec` does not
// match `POST /run`, does not match `RunCommand`, and does not match a new
// operation calling `trainerRun` with a variable verb. The textual pin on its
// own is trivially worked around by anyone who wants to; it exists to give the
// operator a readable line that names the rule. What really protects is this.
//
// Three properties, because free execution can come back in three different
// forms:
//
//	P1a  ROUTES     the set of routes is an EXACT allowlist. A new route is
//	                precisely what has to go through human review; a pin that
//	                only looked at signatures would let `POST /run` in.
//	P1b  SIGNATURE  `(ctx, json.RawMessage) (any, error)` has nowhere to take
//	                argv. Demanding a method expression in the map closes the
//	                "closure that captures req.Argv" variant.
//	P2   ARGV       in exec.Command* and in the DECLARED wrappers, every
//	                argument resolves to a literal. Stance INVERTED relative to
//	                the earlier scan: there the unresolvable was ignored, here
//	                it IS the danger.
//
// ─────────────────────────────────────────────────────────────────────────────
// RESIDUAL RISK THAT STILL STANDS — declared, not hidden behind the green
//
// Inherited from internal/pve/shellout_test.go, and still valid:
//
//   - a literal coming from ANOTHER file or package. Resolution is
//     intra-function; `bin := pkg.Constante` does not resolve and, with
//     ExigirArgvLiteral, FAILS — which is conservative, but produces a false
//     positive on legitimate code that centralises binary names in a package
//     constant. If that shows up, the fix is to declare the wrapper, not to
//     relax the rule.
//   - a name assembled by CONCATENATION or `fmt.Sprintf`. Does not resolve, and
//     fails under ExigirArgvLiteral — same note as above.
//   - TYPE ANALYSIS (go/types) would close the rest: it would resolve constants
//     across packages and would tell a `string` from a `[]byte` without a
//     name-based heuristic. It was CONSIDERED AND REFUSED as disproportionate:
//     it would mean loading the whole package with go/packages, which puts a
//     build dependency in the gate's path and makes the pin sensitive to
//     somebody else's compile error. This house's stance is to declare the
//     risk; hiding it behind a green is what is not done here.
//
// ─────────────────────────────────────────────────────────────────────────────

// rotasPermitidas is the EXACT allowlist. An extra route fails (new surface
// without review); a MISSING route fails too — a vanished route is a surface
// regression, not an improvement, and a pin that only looked at "extra" would
// not see /healthz disappear and the health gate stop meaning anything.
var rotasPermitidas = []string{
	"GET /healthz",
	"GET /metrics",
	"POST /v1/op/{op}",
	// Added deliberately. A file stream does not fit in a JSON document: without
	// this route the Handle from world.export and backup.download does not open
	// over the network, and the round-trip the acceptance criterion demands has
	// no way to happen. It does NOT widen the execution surface — what it accepts
	// is an opaque vault token, never a path (see internal/gameservers/handles.go).
	"GET /v1/artefato/{handle}",
	// The inbound side. Same justification as the read route: what crosses is
	// anonymous bytes, and what comes back is an opaque token — no name and no
	// path.
	"POST /v1/artefato",
}

// wrappersVigiados are the IN-HOUSE functions that run a process without going
// through exec.Command at the call site. `trainerRun(ctx, stdin, args
// ...string)` from internal/gameservers/trainer.go is the measured case: a new
// op calling `trainerRun(ctx, body, req.Verbo)` would slip past a naive detector.
var wrappersVigiados = []string{"trainerRun"}

// rotasDeclaradas extracts the patterns passed to mux.Handle/HandleFunc.
//
// It also returns whether any multiplexer was recognised in the file. Whoever
// analyses the REAL server demands that one was — an empty map from detector
// blindness is indistinguishable from an empty map from having no route at all,
// and the second case would approve a server with no /healthz. A fixture that
// declares no route (M5, which is a legitimate operation) legitimately has no
// mux, and for it the absence is the expected result — which is why the
// decision belongs to the caller, not here.
func rotasDeclaradas(t *testing.T, arquivo string) (map[string]int, bool) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, arquivo, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse of %s: %v", arquivo, err)
	}
	// ⚠️ A REAL FALSE POSITIVE, found by running this pin against production
	// code — the same class as the two the earlier mutation round found.
	//
	// Matching by the selector's NAME makes `gameservers.Handle(x)` — a TYPE
	// CONVERSION to the opaque Handle — read as a route declaration, and the pin
	// failed with "<padrao nao literal>". A false positive is what makes somebody
	// turn the pin off, so the fix goes in the detector, not in the code.
	//
	// The fix: first find out WHICH identifiers are multiplexers (assigned from
	// `http.NewServeMux()` in this file) and only then accept
	// `Handle`/`HandleFunc` on them. The question stops being "is the method
	// called Handle?" and becomes "is the receiver a mux?", exact without go/types.
	muxes := map[string]bool{}
	// (a) by DECLARED TYPE — a parameter, field or var of type *http.ServeMux.
	//     It is the exact path, and it is what recognises
	//     `func monta(mux *http.ServeMux)` in the mutation fixtures, where the
	//     mux is never constructed in the file.
	ehServeMux := func(e ast.Expr) bool {
		estrela, ok := e.(*ast.StarExpr)
		if !ok {
			return false
		}
		sel, ok := estrela.X.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "http" && sel.Sel.Name == "ServeMux"
	}
	anota := func(nomes []*ast.Ident, tipo ast.Expr) {
		if !ehServeMux(tipo) {
			return
		}
		for _, id := range nomes {
			muxes[id.Name] = true
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Field: // parameters, results and struct fields
			anota(v.Names, v.Type)
		case *ast.ValueSpec: // var mux *http.ServeMux
			anota(v.Names, v.Type)
		case *ast.AssignStmt: // (b) by CONSTRUCTION: mux := http.NewServeMux()
			for i, rhs := range v.Rhs {
				ch, ok := rhs.(*ast.CallExpr)
				if !ok {
					continue
				}
				sel, ok := ch.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "NewServeMux" {
					continue
				}
				if i < len(v.Lhs) {
					if id, ok := v.Lhs[i].(*ast.Ident); ok {
						muxes[id.Name] = true
					}
				}
			}
		}
		return true
	})
	achadas := map[string]int{}
	ast.Inspect(f, func(n ast.Node) bool {
		chamada, ok := n.(*ast.CallExpr)
		if !ok || len(chamada.Args) == 0 {
			return true
		}
		sel, ok := chamada.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc" {
			return true
		}
		recept, ok := sel.X.(*ast.Ident)
		if !ok || !muxes[recept.Name] {
			return true // not a route: a type conversion or a same-named method
		}
		lit, ok := chamada.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			// A route pattern that is not a literal is suspect in itself: an
			// allowlist of routes assembled at run time cannot be audited.
			achadas["<padrao nao literal>"] = fset.Position(chamada.Pos()).Line
			return true
		}
		if v, err := strconv.Unquote(lit.Value); err == nil {
			achadas[v] = fset.Position(chamada.Pos()).Line
		}
		return true
	})
	return achadas, len(muxes) > 0
}

func TestSemExecLivreRotas(t *testing.T) {
	arquivo := filepath.Join(raizDoRepo(t), "internal", "labagent", "servidor.go")
	achadas, temMux := rotasDeclaradas(t, arquivo)
	if !temMux {
		// Scanning nothing is never approving (the same principle as
		// astcheck.Scan): with no mux recognised the detector is blind and the
		// allowlist would approve everything.
		t.Fatalf("no *http.ServeMux recognized in %s — the route detector would be blind", arquivo)
	}
	if len(achadas) == 0 {
		t.Fatal("no route found — the scan measured nothing (green by ABSENCE)")
	}

	permitida := map[string]bool{}
	for _, r := range rotasPermitidas {
		permitida[r] = true
	}
	for rota, linha := range achadas {
		if !permitida[rota] {
			t.Errorf("ROUTE OUTSIDE THE ALLOWLIST: %q at %s:%d — new surface has to go through human review, not through a commit",
				rota, arquivo, linha)
		}
	}
	for _, r := range rotasPermitidas {
		if _, existe := achadas[r]; !existe {
			t.Errorf("ROUTE VANISHED: %q is no longer declared — a removed route is a surface regression, not an improvement", r)
		}
	}
}

func TestSemExecLivreAssinatura(t *testing.T) {
	arquivo := filepath.Join(raizDoRepo(t), "internal", "labagent", "registry.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, arquivo, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var achouTipo bool
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Handler" {
			return true
		}
		achouTipo = true
		ft, ok := ts.Type.(*ast.FuncType)
		if !ok {
			t.Errorf("Handler stopped being a function type")
			return false
		}
		// The canonical signature: (*Agent, context.Context, json.RawMessage) -> (any, error).
		// What is explicitly forbidden is any []string parameter (argv) or a
		// second loose string (a command).
		var params []string
		for _, p := range ft.Params.List {
			texto := textoDeTipo(p.Type)
			n := len(p.Names)
			if n == 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				params = append(params, texto)
			}
		}
		for _, p := range params {
			if p == "[]string" {
				t.Errorf("Handler gained a []string parameter — that is argv under another name, and it is exactly the change of shape this guard exists to catch")
			}
		}
		if len(params) != 3 {
			t.Errorf("Handler has %d parameters (%v); the canonical shape has 3 — a change of shape demands review", len(params), params)
		}
		return false
	})
	if !achouTipo {
		t.Fatal("type Handler not found in registry.go — the guard measured nothing")
	}

	// Every Handler value in the registry is a method expression, never a
	// literal. (The detailed check lives in TestHandlersSaoExpressaoDeMetodo;
	// the same thing is asserted here from this pin's point of view, because it
	// is one of the three properties of the criterion and a reader of the
	// criterion has to find it here.)
	var literais []string
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if id, ok := kv.Key.(*ast.Ident); !ok || id.Name != "Handler" {
			return true
		}
		if _, ehLit := kv.Value.(*ast.FuncLit); ehLit {
			literais = append(literais, fset.Position(kv.Pos()).String())
		}
		return true
	})
	if len(literais) > 0 {
		t.Errorf("Handler as a closure in %s — a closure captures a variable from the scope, and that is how argv gets in without showing up in the signature", strings.Join(literais, ", "))
	}
}

func textoDeTipo(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return "*" + textoDeTipo(v.X)
	case *ast.SelectorExpr:
		return textoDeTipo(v.X) + "." + v.Sel.Name
	case *ast.ArrayType:
		return "[]" + textoDeTipo(v.Elt)
	case *ast.Ellipsis:
		return "..." + textoDeTipo(v.Elt)
	}
	return "?"
}

// TestSemExecLivreArgvLiteral runs the argv scanner over the REAL code.
func TestSemExecLivreArgvLiteral(t *testing.T) {
	raiz := raizDoRepo(t)
	res, err := astcheck.Scan(astcheck.Config{
		Raiz:              raiz,
		Incluir:           []string{filepath.Join("internal", "labagent"), filepath.Join("internal", "gameservers")},
		WrappersVigiados:  wrappersVigiados,
		ExigirArgvLiteral: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, a := range res.Achados {
		t.Errorf("execution with an unresolvable argument at %s:%d — %s\n    %s", a.Arquivo, a.Linha, a.Motivo, a.Trecho)
	}
	t.Logf("%d files scanned in labagent+gameservers, %d findings", res.Varridos, len(res.Achados))
}

// TestSemExecLivreVarreuAlgo — a pin that scans nothing goes green by absence.
// It is the lesson written into the earlier precedent, here as a separate
// assertion so that it does not depend on anyone remembering to check.
func TestSemExecLivreVarreuAlgo(t *testing.T) {
	raiz := raizDoRepo(t)
	res, err := astcheck.Scan(astcheck.Config{
		Raiz:             raiz,
		Incluir:          []string{filepath.Join("internal", "labagent"), filepath.Join("internal", "gameservers")},
		WrappersVigiados: wrappersVigiados,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Varridos <= 0 {
		t.Fatalf("Varridos=%d — the scan opened no file at all", res.Varridos)
	}
	// A concrete floor: the two packages add up to more than ten files today. If
	// it drops below that, either the path broke or the scope shrank without
	// anybody having decided so.
	if res.Varridos < 10 {
		t.Errorf("Varridos=%d, below the floor of 10 — the scope of the scan shrank with no decision behind it", res.Varridos)
	}
}

// nomesOrdenados helps keep messages deterministic.
func nomesOrdenados(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// TEST OF THE TEST — the five mutations.
//
// Four that MUST bite and one that must NOT. Without the fifth, a pin that
// fails everything would pass for "it works", and the next step from there is
// turning the guard off. This house has already found that defect four times.
//
// Each case asserts the LINE, not just the count: a detector that fails the
// right file for the wrong reason will fail the wrong file at the next
// refactor. A finding with no line is a guess.
// ─────────────────────────────────────────────────────────────────────────────

func fixture(nome string) string { return filepath.Join("testdata", nome) }

// exigeAchadoNaLinha asserts count AND position.
func exigeAchadoNaLinha(t *testing.T, achados []astcheck.Achado, linha int) {
	t.Helper()
	if len(achados) != 1 {
		t.Fatalf("expected exactly 1 finding, got %d: %+v", len(achados), achados)
	}
	if achados[0].Linha != linha {
		t.Errorf("finding on line %d, expected %d — a detector that hits the file for the wrong reason misses the file on the next refactor (reason: %s)",
			achados[0].Linha, linha, achados[0].Motivo)
	}
}

// M1 — a literal free-execution route. Detected by P1a.
func TestPinoMordeM1(t *testing.T) {
	rotas, _ := rotasDeclaradas(t, filepath.Join(fixture("m1-rota-exec"), "caso.go"))
	permitida := map[string]bool{}
	for _, r := range rotasPermitidas {
		permitida[r] = true
	}
	var fora []string
	for rota := range rotas {
		if !permitida[rota] {
			fora = append(fora, rota)
		}
	}
	if len(fora) != 1 || fora[0] != "POST /exec" {
		t.Fatalf("P1a did not catch the free-execution route: outside the allowlist = %v (routes seen: %v)", fora, nomesOrdenados(rotas))
	}
	if linha := rotas["POST /exec"]; linha != 10 {
		t.Errorf("route reported on line %d, expected 10", linha)
	}
}

// M2 — the route is NOT called /exec and argv comes in through the signature.
// It is the proof that the AST pin is what protects: a textual grep would pass.
func TestPinoMordeM2(t *testing.T) {
	arq := filepath.Join(fixture("m2-rota-argv"), "caso.go")

	rotas, _ := rotasDeclaradas(t, arq)
	if _, tem := rotas["POST /run"]; !tem {
		t.Fatalf("P1a did not see the new route: %v", nomesOrdenados(rotas))
	}
	permitida := map[string]bool{}
	for _, r := range rotasPermitidas {
		permitida[r] = true
	}
	if permitida["POST /run"] {
		t.Fatal("the allowlist contains POST /run — the fixture stopped being a mutation")
	}

	// The textual half: a grep for "exec" would find NOTHING here. Asserting
	// that is what turns "the AST pin is better" into a measured fact.
	fonte, err := os.ReadFile(arq)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(fonte), "/exec") {
		t.Fatal("the M2 fixture contains the string /exec — it would stop proving that a grep is not enough")
	}
	t.Log("M2: a route outside the allowlist was detected, and the fixture does not contain the string /exec — a textual grep would have let it through")
}

// M3 — the in-house wrapper with the verb coming from the body. It closes the
// residual risk inherited from the earlier scan.
func TestPinoMordeM3(t *testing.T) {
	res, err := astcheck.Scan(astcheck.Config{
		Raiz:              fixture("m3-wrapper-verbo"),
		WrappersVigiados:  wrappersVigiados,
		ExigirArgvLiteral: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	exigeAchadoNaLinha(t, res.Achados, 14)
	if !strings.Contains(res.Achados[0].Motivo, "trainerRun") {
		t.Errorf("the reason does not name the wrapper: %q", res.Achados[0].Motivo)
	}
}

// M4 — a loose literal key in the registry.
func TestPinoMordeM4(t *testing.T) {
	arq := filepath.Join(fixture("m4-chave-literal"), "caso.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, arq, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	// A key in the registry's composite literal that is a BasicLit and not a
	// declared constant: the operation exists and never went through review.
	var literais []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, nome := range vs.Names {
			if nome.Name != "registry" || i >= len(vs.Values) {
				continue
			}
			cl, ok := vs.Values[i].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if lit, ok := kv.Key.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					literais = append(literais, fmt.Sprintf("%s@%d", v, fset.Position(lit.Pos()).Line))
				}
			}
		}
		return true
	})
	if len(literais) == 0 {
		t.Fatal("P-exhaustiveness did not see a literal key in the fixture's registry")
	}
	juntos := strings.Join(literais, ",")
	if !strings.Contains(juntos, "exec@9") {
		t.Errorf("the free-execution key was not located with the right line: %v", literais)
	}
	t.Logf("literal keys detected: %v — in the real registry they do not exist, they are all constants", literais)
}

// M5 — THE MAIN NEGATIVE CONTROL: a new, legitimate operation. Zero findings.
//
// ⚠️ If some catalogue test freezes the number of operations, this case will
// fail for the WRONG reason. In that scenario the fix is to correct that test
// (an absolute count is a defect this house already knows), NEVER to relax M5.
// TestPinoNaoMordeConversaoDeTipo is the negative control for the false
// positive found by running this pin against the real server.
//
// The boundary it fixes: `algo.Handle(x)` is a route only when `algo` is an
// *http.ServeMux. Without this fixture, a future fix that went back to matching
// by name would pass every other test — M1 and M2 would go on failing as they
// should, and nobody would see the false positive until it failed a deploy.
func TestPinoNaoMordeConversaoDeTipo(t *testing.T) {
	arq := filepath.Join(fixture("fp1-conversao-handle"), "caso.go")
	rotas, temMux := rotasDeclaradas(t, arq)
	if !temMux {
		t.Fatalf("the fixture declares `mux *http.ServeMux`; the detector should recognize it")
	}
	if _, cego := rotas["<padrao nao literal>"]; cego {
		t.Errorf("FALSE POSITIVE: a type conversion read as a route — routes seen: %v", rotas)
	}
	if len(rotas) != 1 {
		t.Errorf("expected exactly the allowed route; I saw %v", rotas)
	}
	if _, ok := rotas["GET /healthz"]; !ok {
		t.Errorf("the fixture's real route disappeared from the detector: %v", rotas)
	}
}

func TestPinoNaoMordeM5(t *testing.T) {
	res, err := astcheck.Scan(astcheck.Config{
		Raiz:              fixture("m5-op-legitima"),
		WrappersVigiados:  wrappersVigiados,
		ExigirArgvLiteral: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Achados) != 0 {
		t.Errorf("FALSE POSITIVE on a legitimate operation — the guard would fail the catalog's normal growth, and the next step is someone switching it off: %+v", res.Achados)
	}
	rotas, _ := rotasDeclaradas(t, filepath.Join(fixture("m5-op-legitima"), "caso.go"))
	if len(rotas) != 0 {
		t.Errorf("a legitimate operation adds no route; got %v", nomesOrdenados(rotas))
	}
}

// TestPinoNaoMordeLegitimos — the controls inherited from the precedent.
func TestPinoNaoMordeLegitimos(t *testing.T) {
	res, err := astcheck.Scan(astcheck.Config{
		Raiz:              fixture("neg-legitimos"),
		WrappersVigiados:  wrappersVigiados,
		ExigirArgvLiteral: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Achados) != 0 {
		t.Errorf("FALSE POSITIVE: the agent will legitimately run docker and the guard has to approve it: %+v", res.Achados)
	}
	if res.Varridos != 1 {
		t.Errorf("Varridos=%d, want 1", res.Varridos)
	}
}

// TestPinoMordeVarDePacote — the BOUNDARY of constant resolution.
//
// The scanner now resolves package-level `const` (otherwise `trainerBin` would
// become a false positive). This case fixes the limit: a package-level `var` is
// REASSIGNABLE at run time and must NOT be treated as a literal — treating it
// so would open the very hole the pin exists to close.
func TestPinoMordeVarDePacote(t *testing.T) {
	res, err := astcheck.Scan(astcheck.Config{
		Raiz:              fixture("neg-var-de-pacote"),
		WrappersVigiados:  wrappersVigiados,
		ExigirArgvLiteral: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Achados) == 0 {
		t.Fatal("FALSE NEGATIVE: a package-level `var` was resolved as a literal — a var is reassignable at runtime, and this reopens the hole")
	}
	exigeAchadoNaLinha(t, res.Achados, 12)
}
