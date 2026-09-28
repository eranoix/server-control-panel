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

var allowedRoutes = []string{
	"GET /healthz",
	"GET /metrics",
	"POST /v1/op/{op}",
	"GET /v1/artifact/{handle}",
	"POST /v1/artifact",
}

var watchedWrappers = []string{"trainerRun"}

func declaredRoutes(t *testing.T, file string) (map[string]int, bool) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse of %s: %v", file, err)
	}
	muxes := map[string]bool{}
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
		case *ast.Field:
			note(v.Names, v.Type)
		case *ast.ValueSpec:
			note(v.Names, v.Type)
		case *ast.AssignStmt:
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
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
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
	if res.Scanned < 10 {
		t.Errorf("Scanned=%d, below the floor of 10: the scope of the scan shrank with no decision behind it", res.Scanned)
	}
}

func sortedNames(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fixture(name string) string { return filepath.Join("testdata", name) }

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

	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "/exec") {
		t.Fatal("the M2 fixture contains the string /exec — it would stop proving that a grep is not enough")
	}
	t.Log("M2: a route outside the allowlist was detected, and the fixture does not contain the string /exec — a textual grep would have let it through")
}

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

func TestPinBitesM4(t *testing.T) {
	file := filepath.Join(fixture("m4-literal-key"), "case.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
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
