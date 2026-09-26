package nodeagent

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
// RESIDUAL RISK THAT STILL STANDS — declared, not hidden behind the green
//
// Inherited from internal/pve/shellout_test.go, and still valid:
//
//   - a literal coming from ANOTHER file or package. Resolution is
//     intra-function; `bin := pkg.Constant` does not resolve and, with
//     RequireLiteralArgv, FAILS — which is conservative, but produces a false
//     positive on legitimate code that centralises binary names in a package
//     constant. If that shows up, the fix is to declare the wrapper, not to
//     relax the rule.
//   - a name assembled by CONCATENATION or `fmt.Sprintf`. Does not resolve, and
//     fails under RequireLiteralArgv — same note as above.
//   - TYPE ANALYSIS (go/types) would close the rest: it would resolve constants
//     across packages and would tell a `string` from a `[]byte` without a
//     name-based heuristic. It was CONSIDERED AND REFUSED as disproportionate:
//     it would mean loading the whole package with go/packages, which puts a
//     build dependency in the gate's path and makes the pin sensitive to
//     somebody else's compile error. This house's stance is to declare the
//     risk; hiding it behind a green is what is not done here.

// allowedRoutes is the EXACT allowlist. An extra route fails (new surface
// without review); a MISSING route fails too — a vanished route is a surface
// regression, not an improvement, and a pin that only looked at "extra" would
// not see /healthz disappear and the health gate stop meaning anything.
var allowedRoutes = []string{
	"GET /healthz",
	"GET /metrics",
	"POST /v1/op/{op}",
	// Added deliberately. A file stream does not fit in a JSON document: without
	// this route the Handle from world.export and backup.download does not open
	// over the network, and the round-trip the acceptance criterion demands has
	// no way to happen. It does NOT widen the execution surface — what it accepts
	// is an opaque vault token, never a path (see internal/gameservers/handles.go).
	"GET /v1/artifact/{handle}",
	// The inbound side. Same justification as the read route: what crosses is
	// anonymous bytes, and what comes back is an opaque token — no name and no
	// path.
	"POST /v1/artifact",
}

// watchedWrappers are the IN-HOUSE functions that run a process without going
// through exec.Command at the call site. `trainerRun(ctx, stdin, args
// ...string)` from internal/gameservers/trainer.go is the measured case: a new
// op calling `trainerRun(ctx, body, req.Verb)` would slip past a naive detector.
var watchedWrappers = []string{"trainerRun"}

// declaredRoutes extracts the patterns passed to mux.Handle/HandleFunc.
//
// It also returns whether any multiplexer was recognised in the file. Whoever
// analyses the REAL server demands that one was — an empty map from detector
// blindness is indistinguishable from an empty map from having no route at all,
// and the second case would approve a server with no /healthz. A fixture that
// declares no route (M5, which is a legitimate operation) legitimately has no
// mux, and for it the absence is the expected result — which is why the
// decision belongs to the caller, not here.
func declaredRoutes(t *testing.T, file string) (map[string]int, bool) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse of %s: %v", file, err)
	}
	// ⚠️ A REAL FALSE POSITIVE, found by running this pin against production
	// code — the same class as the two the earlier mutation round found.
	//
	// Matching by the selector's NAME makes `gameservers.Handle(x)` — a TYPE
	// CONVERSION to the opaque Handle — read as a route declaration, and the pin
	// failed with "<non-literal pattern>". A false positive is what makes somebody
	// turn the pin off, so the fix goes in the detector, not in the code.
	//
	// The fix: first find out WHICH identifiers are multiplexers (assigned from
	// `http.NewServeMux()` in this file) and only then accept
	// `Handle`/`HandleFunc` on them. The question stops being "is the method
	// called Handle?" and becomes "is the receiver a mux?", exact without go/types.
	muxes := map[string]bool{}
	// (a) by DECLARED TYPE — a parameter, field or var of type *http.ServeMux.
	//     It is the exact path, and it is what recognises
	//     `func mount(mux *http.ServeMux)` in the mutation fixtures, where the
	//     mux is never constructed in the file.
	isServeMux := func(e ast.Expr) bool {
		star, ok := e.(*ast.StarExpr)
		if !ok {
			return false
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "http" && sel.Sel.Name == "ServeMux"
	}
	note := func(names []*ast.Ident, kind ast.Expr) {
		if !isServeMux(kind) {
			return
		}
		for _, id := range names {
			muxes[id.Name] = true
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Field: // parameters, results and struct fields
			note(v.Names, v.Type)
		case *ast.ValueSpec: // var mux *http.ServeMux
			note(v.Names, v.Type)
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
	foundRoutes := map[string]int{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
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
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			// A route pattern that is not a literal is suspect in itself: an
			// allowlist of routes assembled at run time cannot be audited.
			foundRoutes["<non-literal pattern>"] = fset.Position(call.Pos()).Line
			return true
		}
		if v, err := strconv.Unquote(lit.Value); err == nil {
			foundRoutes[v] = fset.Position(call.Pos()).Line
		}
		return true
	})
	return foundRoutes, len(muxes) > 0
}

func TestNoFreeExecRoutes(t *testing.T) {
	file := filepath.Join(repoRoot(t), "internal", "nodeagent", "server.go")
	foundRoutes, hasMux := declaredRoutes(t, file)
	if !hasMux {
		// Scanning nothing is never approving (the same principle as
		// astcheck.Scan): with no mux recognised the detector is blind and the
		// allowlist would approve everything.
		t.Fatalf("no *http.ServeMux recognized in %s — the route detector would be blind", file)
	}
	if len(foundRoutes) == 0 {
		t.Fatal("no route found — the scan measured nothing (green by ABSENCE)")
	}

	allowed := map[string]bool{}
	for _, r := range allowedRoutes {
		allowed[r] = true
	}
	for route, line := range foundRoutes {
		if !allowed[route] {
			t.Errorf("ROUTE OUTSIDE THE ALLOWLIST: %q at %s:%d — new surface has to go through human review, not through a commit",
				route, file, line)
		}
	}
	for _, r := range allowedRoutes {
		if _, exists := foundRoutes[r]; !exists {
			t.Errorf("ROUTE VANISHED: %q is no longer declared — a removed route is a surface regression, not an improvement", r)
		}
	}
}

func TestNoFreeExecSignature(t *testing.T) {
	file := filepath.Join(repoRoot(t), "internal", "nodeagent", "registry.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var foundType bool
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Handler" {
			return true
		}
		foundType = true
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
			text := typeString(p.Type)
			n := len(p.Names)
			if n == 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				params = append(params, text)
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
	if !foundType {
		t.Fatal("type Handler not found in registry.go — the guard measured nothing")
	}

	// Every Handler value in the registry is a method expression, never a
	// literal. (The detailed check lives in TestHandlersAreMethodExpressions;
	// the same thing is asserted here from this pin's point of view, because it
	// is one of the three properties of the criterion and a reader of the
	// criterion has to find it here.)
	var literals []string
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if id, ok := kv.Key.(*ast.Ident); !ok || id.Name != "Handler" {
			return true
		}
		if _, isLit := kv.Value.(*ast.FuncLit); isLit {
			literals = append(literals, fset.Position(kv.Pos()).String())
		}
		return true
	})
	if len(literals) > 0 {
		t.Errorf("Handler as a closure in %s — a closure captures a variable from the scope, and that is how argv gets in without showing up in the signature", strings.Join(literals, ", "))
	}
}

func typeString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.StarExpr:
		return "*" + typeString(v.X)
	case *ast.SelectorExpr:
		return typeString(v.X) + "." + v.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeString(v.Elt)
	case *ast.Ellipsis:
		return "..." + typeString(v.Elt)
	}
	return "?"
}

// TestNoFreeExecArgvLiteral runs the argv scanner over the REAL code.
func TestNoFreeExecArgvLiteral(t *testing.T) {
	root := repoRoot(t)
	res, err := astcheck.Scan(astcheck.Config{
		Root:               root,
		Include:            []string{filepath.Join("internal", "nodeagent"), filepath.Join("internal", "gameservers")},
		WatchedWrappers:    watchedWrappers,
		RequireLiteralArgv: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, a := range res.Findings {
		t.Errorf("execution with an unresolvable argument at %s:%d — %s\n    %s", a.File, a.Line, a.Reason, a.Snippet)
	}
	t.Logf("%d files scanned in nodeagent+gameservers, %d findings", res.Scanned, len(res.Findings))
}

// TestNoFreeExecScannedSomething — a pin that scans nothing goes green by absence.
// It is the lesson written into the earlier precedent, here as a separate
// assertion so that it does not depend on anyone remembering to check.
func TestNoFreeExecScannedSomething(t *testing.T) {
	root := repoRoot(t)
	res, err := astcheck.Scan(astcheck.Config{
		Root:            root,
		Include:         []string{filepath.Join("internal", "nodeagent"), filepath.Join("internal", "gameservers")},
		WatchedWrappers: watchedWrappers,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Scanned <= 0 {
		t.Fatalf("Scanned=%d: the scan opened no file at all", res.Scanned)
	}
	// A concrete floor: the two packages add up to more than ten files today. If
	// it drops below that, either the path broke or the scope shrank without
	// anybody having decided so.
	if res.Scanned < 10 {
		t.Errorf("Scanned=%d, below the floor of 10: the scope of the scan shrank with no decision behind it", res.Scanned)
	}
}

// sortedNames helps keep messages deterministic.
func sortedNames(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TEST OF THE TEST — the five mutations.
//
// Four that MUST bite and one that must NOT. Without the fifth, a pin that
// fails everything would pass for "it works", and the next step from there is
// turning the guard off. This house has already found that defect four times.
//
// Each case asserts the LINE, not just the count: a detector that fails the
// right file for the wrong reason will fail the wrong file at the next
// refactor. A finding with no line is a guess.

func fixture(name string) string { return filepath.Join("testdata", name) }

// requireFindingOnLine asserts count AND position.
func requireFindingOnLine(t *testing.T, findings []astcheck.Finding, line int) {
	t.Helper()
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding, got %d: %+v", len(findings), findings)
	}
	if findings[0].Line != line {
		t.Errorf("finding on line %d, expected %d — a detector that hits the file for the wrong reason misses the file on the next refactor (reason: %s)",
			findings[0].Line, line, findings[0].Reason)
	}
}

// M1 — a literal free-execution route. Detected by P1a.
func TestPinBitesM1(t *testing.T) {
	routes, _ := declaredRoutes(t, filepath.Join(fixture("m1-route-exec"), "case.go"))
	allowed := map[string]bool{}
	for _, r := range allowedRoutes {
		allowed[r] = true
	}
	var outside []string
	for route := range routes {
		if !allowed[route] {
			outside = append(outside, route)
		}
	}
	if len(outside) != 1 || outside[0] != "POST /exec" {
		t.Fatalf("P1a did not catch the free-execution route: outside the allowlist = %v (routes seen: %v)", outside, sortedNames(routes))
	}
	if line := routes["POST /exec"]; line != 10 {
		t.Errorf("route reported on line %d, expected 10", line)
	}
}

// M2 — the route is NOT called /exec and argv comes in through the signature.
// It is the proof that the AST pin is what protects: a textual grep would pass.
func TestPinBitesM2(t *testing.T) {
	file := filepath.Join(fixture("m2-route-argv"), "case.go")

	routes, _ := declaredRoutes(t, file)
	if _, has := routes["POST /run"]; !has {
		t.Fatalf("P1a did not see the new route: %v", sortedNames(routes))
	}
	allowed := map[string]bool{}
	for _, r := range allowedRoutes {
		allowed[r] = true
	}
	if allowed["POST /run"] {
		t.Fatal("the allowlist contains POST /run — the fixture stopped being a mutation")
	}

	// The textual half: a grep for "exec" would find NOTHING here. Asserting
	// that is what turns "the AST pin is better" into a measured fact.
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "/exec") {
		t.Fatal("the M2 fixture contains the string /exec — it would stop proving that a grep is not enough")
	}
	t.Log("M2: a route outside the allowlist was detected, and the fixture does not contain the string /exec — a textual grep would have let it through")
}

// M3 — the in-house wrapper with the verb coming from the body. It closes the
// residual risk inherited from the earlier scan.
func TestPinBitesM3(t *testing.T) {
	res, err := astcheck.Scan(astcheck.Config{
		Root:               fixture("m3-wrapper-verb"),
		WatchedWrappers:    watchedWrappers,
		RequireLiteralArgv: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	requireFindingOnLine(t, res.Findings, 14)
	if !strings.Contains(res.Findings[0].Reason, "trainerRun") {
		t.Errorf("the reason does not name the wrapper: %q", res.Findings[0].Reason)
	}
}

// M4 — a loose literal key in the registry.
func TestPinBitesM4(t *testing.T) {
	file := filepath.Join(fixture("m4-literal-key"), "case.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	// A key in the registry's composite literal that is a BasicLit and not a
	// declared constant: the operation exists and never went through review.
	var literals []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if name.Name != "registry" || i >= len(vs.Values) {
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
					literals = append(literals, fmt.Sprintf("%s@%d", v, fset.Position(lit.Pos()).Line))
				}
			}
		}
		return true
	})
	if len(literals) == 0 {
		t.Fatal("P-exhaustiveness did not see a literal key in the fixture's registry")
	}
	joined := strings.Join(literals, ",")
	if !strings.Contains(joined, "exec@9") {
		t.Errorf("the free-execution key was not located with the right line: %v", literals)
	}
	t.Logf("literal keys detected: %v — in the real registry they do not exist, they are all constants", literals)
}

// M5 — THE MAIN NEGATIVE CONTROL: a new, legitimate operation. Zero findings.
//
// ⚠️ If some catalogue test freezes the number of operations, this case will
// fail for the WRONG reason. In that scenario the fix is to correct that test
// (an absolute count is a defect this house already knows), NEVER to relax M5.
// TestPinSparesTypeConversion is the negative control for the false
// positive found by running this pin against the real server.
//
// The boundary it fixes: `algo.Handle(x)` is a route only when `algo` is an
// *http.ServeMux. Without this fixture, a future fix that went back to matching
// by name would pass every other test — M1 and M2 would go on failing as they
// should, and nobody would see the false positive until it failed a deploy.
func TestPinSparesTypeConversion(t *testing.T) {
	file := filepath.Join(fixture("fp1-handle-conversion"), "case.go")
	routes, hasMux := declaredRoutes(t, file)
	if !hasMux {
		t.Fatalf("the fixture declares `mux *http.ServeMux`; the detector should recognize it")
	}
	if _, blind := routes["<non-literal pattern>"]; blind {
		t.Errorf("FALSE POSITIVE: a type conversion read as a route — routes seen: %v", routes)
	}
	if len(routes) != 1 {
		t.Errorf("expected exactly the allowed route; I saw %v", routes)
	}
	if _, ok := routes["GET /healthz"]; !ok {
		t.Errorf("the fixture's real route disappeared from the detector: %v", routes)
	}
}

func TestPinSparesM5(t *testing.T) {
	res, err := astcheck.Scan(astcheck.Config{
		Root:               fixture("m5-legit-op"),
		WatchedWrappers:    watchedWrappers,
		RequireLiteralArgv: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("FALSE POSITIVE on a legitimate operation — the guard would fail the catalog's normal growth, and the next step is someone switching it off: %+v", res.Findings)
	}
	routes, _ := declaredRoutes(t, filepath.Join(fixture("m5-legit-op"), "case.go"))
	if len(routes) != 0 {
		t.Errorf("a legitimate operation adds no route; got %v", sortedNames(routes))
	}
}

// TestPinSparesLegitimate — the controls inherited from the precedent.
func TestPinSparesLegitimate(t *testing.T) {
	res, err := astcheck.Scan(astcheck.Config{
		Root:               fixture("neg-legit"),
		WatchedWrappers:    watchedWrappers,
		RequireLiteralArgv: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Errorf("FALSE POSITIVE: the agent will legitimately run docker and the guard has to approve it: %+v", res.Findings)
	}
	if res.Scanned != 1 {
		t.Errorf("Scanned=%d, want 1", res.Scanned)
	}
}

// TestPinBitesPackageVar — the BOUNDARY of constant resolution.
//
// The scanner now resolves package-level `const` (otherwise `trainerBin` would
// become a false positive). This case fixes the limit: a package-level `var` is
// REASSIGNABLE at run time and must NOT be treated as a literal — treating it
// so would open the very hole the pin exists to close.
func TestPinBitesPackageVar(t *testing.T) {
	res, err := astcheck.Scan(astcheck.Config{
		Root:               fixture("neg-package-var"),
		WatchedWrappers:    watchedWrappers,
		RequireLiteralArgv: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) == 0 {
		t.Fatal("FALSE NEGATIVE: a package-level `var` was resolved as a literal — a var is reassignable at runtime, and this reopens the hole")
	}
	requireFindingOnLine(t, res.Findings, 12)
}
