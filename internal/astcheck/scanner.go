// Package astcheck is the AST scanner that this repository's narrowness pins
// use to prove, by machine, that nobody has opened up free process
// execution.
//
// WHY THIS IS A PRODUCTION PACKAGE AND NOT A `_test.go`
//
// The precedent is `internal/pve/shellout_test.go` (404 lines), and it solves
// nearly everything that is here. The copy is deliberate, for two reasons a test
// file cannot serve:
//
//   - a `_test.go` is not importable by another package, and more than one pin
//     needs the SAME scanner — copying the scanner into each pin is how you end
//     up with three versions of the rule that one day diverge;
//   - a `_test.go` cannot be protected by a positive invariant. A production
//     package can: `invariants.txt` can demand that `internal/astcheck` exist and
//     that the gate run it, and removing it starts failing the deploy.
//
// `shellout_test.go` was NOT deleted or altered: it is the pin currently in force
// for its criterion, and removing it so as "not to duplicate" would be trading a
// pin that works for a promise. Converging the two is recorded debt.
//
// THREE MANDATORY DIFFERENCES FROM THE PRECEDENT
//
//	(a) WatchedWrappers — a DECLARED list of in-house functions that execute a
//	    process (the gap the precedent left open). It closes the hole around
//	    `trainerRun(ctx, stdin, args ...string)` in gameservers/trainer.go.
//	    A function NOT on the list whose body calls exec.Command with an
//	    unresolvable argument also fails, via the exec.Command path — otherwise
//	    the list becomes an allowlist that ages in silence, a defect this project
//	    has already paid for.
//
//	(b) RequireLiteralArgv — the stance is INVERTED. In the precedent the
//	    unresolvable argument was ignored (the pin only asked "is this a forbidden
//	    binary?"). Here the unresolvable IS the danger: it is exactly the way free
//	    execution comes back under another name.
//
//	(c) Scanned is a field of Result and Scan returns an ERROR when it is zero.
//	    In the precedent that was a comment plus a check each consumer had to
//	    remember to make. Here it is API contract: scanning nothing is never
//	    approving, and no consumer can ignore it.
//
// No new dependency: go/parser, go/ast, go/token and go/printer are stdlib.
// It deliberately does NOT use golang.org/x/tools/go/analysis — the precedent is
// pure go/parser, and switching tools would lose the precedent along with the
// mutation already proven.
package astcheck

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// execPath is the import path that grants access to process execution.
const execPath = "os/exec"

// ignoredDirs never enter the production sweep.
//
// `testdata` is the most important entry: Go ignores that directory when building
// packages, which is exactly what lets us keep synthetic violations there — and
// also what would make the real tree fail because of the fixtures themselves if
// the sweep did not skip it. The proof that this exclusion is not vacuous is
// TestScanSkipsOwnTestdata: pointed straight at testdata, the scanner FINDS.
var ignoredDirs = map[string]bool{
	"testdata":     true,
	"vendor":       true,
	"node_modules": true,
	".git":         true,
	".claude":      true,
}

// Config describes a sweep.
type Config struct {
	// Root is the directory where the sweep starts.
	Root string

	// Include, when non-empty, restricts the sweep to these subdirectories of
	// Root (e.g. {"internal", "cmd"}). Empty sweeps all of Root.
	Include []string

	// ForbiddenBins are binary names that must not be executed from this
	// code (e.g. the hypervisor commands, which belong to the PVE API).
	ForbiddenBins []string

	// WatchedWrappers are IN-HOUSE functions that execute a process without
	// going through exec.Command at the call site.
	WatchedWrappers []string

	// RequireLiteralArgv inverts the precedent's stance: a command argument
	// that does not resolve to a string literal now FAILS.
	RequireLiteralArgv bool
}

// Finding is a located violation, with a usable message.
//
// Reason and Snippet are not decoration: a pin that fails without saying what and
// where is a pin someone switches off instead of fixing.
type Finding struct {
	File    string
	Line    int
	Reason  string
	Snippet string
}

// Result carries the findings AND how many files were actually parsed.
type Result struct {
	Findings []Finding

	// Scanned is the anti-vacuity guard. Zero is an error, never approval.
	Scanned int
}

// Scan sweeps the tree described by cfg and returns the findings.
//
// It returns an error when it parsed no Go file at all: an empty sweep is a
// configuration defect, and returning "0 findings" in that case would produce a
// green by ABSENCE — the costliest defect this repository has ever paid for.
func Scan(cfg Config) (Result, error) {
	var res Result

	forbiddenBins := make(map[string]bool, len(cfg.ForbiddenBins))
	for _, b := range cfg.ForbiddenBins {
		forbiddenBins[b] = true
	}
	watched := make(map[string]bool, len(cfg.WatchedWrappers))
	for _, w := range cfg.WatchedWrappers {
		watched[w] = true
	}

	roots := make([]string, 0, len(cfg.Include))
	if len(cfg.Include) == 0 {
		roots = append(roots, cfg.Root)
	} else {
		for _, sub := range cfg.Include {
			roots = append(roots, filepath.Join(cfg.Root, sub))
		}
	}

	fset := token.NewFileSet()
	// Collect first, analyze later: see the comment on the second pass.
	byDir := map[string][]parsedFile{}
	var dirOrder []string
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			// A subdirectory of Include that does not exist is a configuration
			// error, not "nothing to sweep".
			return res, fmt.Errorf("astcheck: root %q unreachable: %w", root, err)
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if ignoredDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return fmt.Errorf("astcheck: parse of %s: %w", path, err)
			}
			dir := filepath.Dir(path)
			byDir[dir] = append(byDir[dir], parsedFile{path: path, ast: file})
			dirOrder = append(dirOrder, dir)
			res.Scanned++
			return nil
		})
		if err != nil {
			return res, err
		}
	}

	// Second pass, by DIRECTORY (= package).
	//
	// Why two passes: a PACKAGE constant (`const trainerBin = "..."`) may live
	// in another file of the same package, and intra-function resolution does
	// not see it. Without this, `exec.CommandContext(c, trainerBin, args...)`
	// becomes a finding — a FALSE POSITIVE on legitimate code, and a false
	// positive is what makes someone switch the pin off.
	//
	// Resolving a package constant does NOT weaken the rule: `const` is a
	// compile-time literal, immutable at runtime. The danger the pin is after
	// is an argument that VARIES — and that one still fails.
	seen := map[string]bool{}
	for _, dir := range dirOrder {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		files := byDir[dir]
		consts := packageConsts(files)
		signatures := wrapperSignatures(files, watched)
		for _, a := range files {
			res.Findings = append(res.Findings, scanFile(fset, a.ast, a.path, forbiddenBins, watched, cfg.RequireLiteralArgv, consts, signatures)...)
		}
	}

	if res.Scanned == 0 {
		return res, fmt.Errorf("astcheck: empty scan at %q — no .go file parsed; scanning nothing is never a pass", cfg.Root)
	}
	return res, nil
}

// scanFile applies the two rules to an already-parsed file.
func scanFile(fset *token.FileSet, file *ast.File, path string, forbiddenBins, watched map[string]bool, requireLiteral bool, pkgConsts map[string]*symbol, signatures map[string]wrapperSig) []Finding {
	execName := localOsExecName(file)

	var findings []Finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		symbols := symbolTable(fn)
		// A package constant comes in as the FLOOR: the local symbol always wins, so
		// that a parameter shadowing the constant's name is not resolved to the
		// constant's value — shadowing is precisely how a varying value would
		// disguise itself as a constant.
		for name, s := range pkgConsts {
			if _, local := symbols[name]; !local {
				symbols[name] = s
			}
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Rule 1 — direct execution via os/exec (under an alias too).
			if execName != "" {
				if idx, isExec := binaryIndex(call, execName); isExec {
					if idx < len(call.Args) {
						value, resolved := resolve(call.Args[idx], symbols)
						switch {
						case resolved && forbiddenBins[value]:
							findings = append(findings, newFinding(fset, path, call,
								fmt.Sprintf("direct execution of forbidden binary %q — this operation belongs to the hypervisor API, not to the shell", value)))
						case !resolved && requireLiteral:
							findings = append(findings, newFinding(fset, path, call,
								fmt.Sprintf("argv of %s does not resolve to a string literal — an unresolvable argument is free execution under another name", execName)))
						}
					}
					return true
				}
			}

			// Rule 2 — a watched in-house wrapper.
			if requireLiteral {
				if ident, ok := call.Fun.(*ast.Ident); ok && watched[ident.Name] {
					as, hasSig := signatures[ident.Name]
					for i, arg := range call.Args {
						// With the declared signature in hand, the question is EXACT:
						// "is the position this argument occupies declared string?".
						// Without it (a wrapper from another package), it falls back
						// to the identifier's type heuristic, which is the best
						// possible without type analysis.
						if hasSig {
							if !as.isCommandPos(i) {
								continue
							}
						} else if !isCommandArg(arg, symbols) {
							continue
						}
						if _, resolved := resolve(arg, symbols); !resolved {
							findings = append(findings, newFinding(fset, path, call,
								fmt.Sprintf("watched wrapper %s takes a command argument that does not resolve to a literal — the catalogue stops being closed here", ident.Name)))
							break
						}
					}
				}
			}
			return true
		})
	}
	return findings
}

// localOsExecName returns the name by which "os/exec" is reachable in THIS
// file — which is not necessarily "exec".
//
// It is the hole no textual search closes: `import xc "os/exec"` turns the call
// into `xc.Command(...)`, and a grep for "exec.Command" walks straight past it.
// Returns "" when the file does not import os/exec.
func localOsExecName(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != execPath {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				// A blank import calls nothing; dot-import is not used in this
				// repository and resolving it would require type information.
				return ""
			}
			return imp.Name.Name
		}
		return "exec"
	}
	return ""
}

// binaryIndex recognizes exec.Command / exec.CommandContext and says which
// argument holds the binary name.
func binaryIndex(call *ast.CallExpr, execName string) (int, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != execName {
		return 0, false
	}
	switch sel.Sel.Name {
	case "Command":
		return 0, true
	case "CommandContext":
		// The first argument is the context.
		return 1, true
	}
	return 0, false
}

// symbol is what is known about a local identifier.
type symbol struct {
	// valor is the string literal when it is known and unique.
	value string
	// literal indicates that valor is usable.
	literal bool
	// isString indicates that the identifier is declared to be of type string.
	// Without type information, it is what allows telling a "command verb" apart
	// from "context" and "[]byte" in a wrapper's argument list.
	isString bool
}

// symbolTable collects, for one function, what each local identifier is worth.
//
// The pass covers the WHOLE function before any call analysis, so that a later
// reassignment knocks the resolution down: a name assigned twice with different
// values becomes unresolvable, never "the first one that showed up".
func symbolTable(fn *ast.FuncDecl) map[string]*symbol {
	tab := map[string]*symbol{}

	mark := func(name string, s symbol) {
		if name == "" || name == "_" {
			return
		}
		old, exists := tab[name]
		if !exists {
			dup := s
			tab[name] = &dup
			return
		}
		old.isString = old.isString || s.isString
		if !s.literal || !old.literal || old.value != s.value {
			// A second, divergent write: the name stops being resolvable.
			old.literal = false
			old.value = ""
		}
	}

	// Parameters: never resolve to a literal, but the declared type is visible.
	if fn.Type != nil && fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			isStr := isStringType(field.Type)
			for _, name := range field.Names {
				mark(name.Name, symbol{isString: isStr})
			}
		}
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || i >= len(s.Rhs) {
					continue
				}
				if value, ok := stringLiteral(s.Rhs[i]); ok {
					mark(ident.Name, symbol{value: value, literal: true, isString: true})
				} else {
					mark(ident.Name, symbol{})
				}
			}
		case *ast.GenDecl:
			if s.Tok != token.VAR && s.Tok != token.CONST {
				return true
			}
			for _, spec := range s.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				isStr := vs.Type != nil && isStringType(vs.Type)
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if value, ok := stringLiteral(vs.Values[i]); ok {
							mark(name.Name, symbol{value: value, literal: true, isString: true})
							continue
						}
					}
					mark(name.Name, symbol{isString: isStr})
				}
			}
		}
		return true
	})
	return tab
}

// isStringType recognizes the type `string` as written in the declaration.
func isStringType(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "string"
}

// stringLiteral extracts the value of a string literal, if it is one.
func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

// resolve tries to obtain an argument's literal value.
func resolve(expr ast.Expr, symbols map[string]*symbol) (string, bool) {
	if value, ok := stringLiteral(expr); ok {
		return value, true
	}
	if ident, ok := expr.(*ast.Ident); ok {
		if s, exists := symbols[ident.Name]; exists && s.literal {
			return s.value, true
		}
	}
	return "", false
}

// isCommandArg decides whether it is worth demanding a literal here.
//
// Without type information, the question syntax can answer is: is this argument a
// string? `ctx` and `body` in a wrapper are not, and demanding a literal of them
// would produce a false positive on every legitimate call. An argument of type
// string that does not resolve is the case that matters.
func isCommandArg(arg ast.Expr, symbols map[string]*symbol) bool {
	if _, ok := stringLiteral(arg); ok {
		return true
	}
	if ident, ok := arg.(*ast.Ident); ok {
		if s, exists := symbols[ident.Name]; exists {
			return s.isString
		}
	}
	return false
}

// newFinding produces the finding with file, line, reason and rendered snippet.
func newFinding(fset *token.FileSet, path string, node ast.Node, reason string) Finding {
	pos := fset.Position(node.Pos())
	return Finding{
		File:    path,
		Line:    pos.Line,
		Reason:  reason,
		Snippet: render(fset, node),
	}
}

// render returns the node's source code, so the pin's message shows the
// offending line instead of sending the reader off to look for it.
func render(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return "<snippet unavailable>"
	}
	snippet := strings.Join(strings.Fields(buf.String()), " ")
	const maxLen = 160
	if len(snippet) > maxLen {
		snippet = snippet[:maxLen] + "…"
	}
	return snippet
}

// parsedFile holds the path/AST pair between the two passes.
type parsedFile struct {
	path string
	ast  *ast.File
}

// packageConsts collects the string constants declared at PACKAGE LEVEL,
// across every file in the directory.
//
// Only `const`, never `var`: a package `var` can be reassigned at runtime (by
// init, by a test, by any function), and treating it as a literal would open
// exactly the hole the pin exists to close. `const` cannot change.
//
// A name declared twice with different values becomes UNRESOLVABLE, never "the
// first one that showed up".
func packageConsts(files []parsedFile) map[string]*symbol {
	tab := map[string]*symbol{}
	for _, a := range files {
		for _, decl := range a.ast.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if name.Name == "_" || i >= len(vs.Values) {
						continue
					}
					value, ok := stringLiteral(vs.Values[i])
					if !ok {
						continue
					}
					if old, exists := tab[name.Name]; exists {
						if old.value != value {
							old.literal = false
							old.value = ""
						}
						continue
					}
					tab[name.Name] = &symbol{value: value, literal: true, isString: true}
				}
			}
		}
	}
	return tab
}

// indicesDeString describes, for a watched wrapper, which argument positions
// are of type string in the DECLARATION.
type wrapperSig struct {
	// fixed holds the non-variadic string parameter indices.
	fixed map[int]bool
	// variadicString says the variadic tail is `...string`.
	variadicString bool
	// variadicStart is the index where the tail starts (-1 if there is none).
	variadicStart int
}

// wrapperSignatures finds, in the package, the declaration of each watched wrapper.
//
// WHY THIS EXISTS, and why the earlier heuristic was not enough: without the
// signature, the scanner had to GUESS which arguments were the command, by
// looking at the identifier's declared type. That let through the most dangerous
// case of all — `trainerRun(ctx, body, req.Verb)`, where the verb is a STRUCT
// FIELD coming from the request body, not a plain identifier. With the signature
// in hand, the question stops being "does this argument look like a string?" and
// becomes "is the position this argument occupies declared string?", which is
// exact.
func wrapperSignatures(files []parsedFile, watched map[string]bool) map[string]wrapperSig {
	out := map[string]wrapperSig{}
	for _, a := range files {
		for _, decl := range a.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !watched[fn.Name.Name] || fn.Type.Params == nil {
				continue
			}
			as := wrapperSig{fixed: map[int]bool{}, variadicStart: -1}
			idx := 0
			for _, field := range fn.Type.Params.List {
				n := len(field.Names)
				if n == 0 {
					n = 1
				}
				if el, isVariadic := field.Type.(*ast.Ellipsis); isVariadic {
					as.variadicStart = idx
					as.variadicString = isStringType(el.Elt)
					idx += n
					continue
				}
				if isStringType(field.Type) {
					for i := 0; i < n; i++ {
						as.fixed[idx+i] = true
					}
				}
				idx += n
			}
			out[fn.Name.Name] = as
		}
	}
	return out
}

// isCommandPos says whether the argument at position i is declared string.
func (a wrapperSig) isCommandPos(i int) bool {
	if a.fixed[i] {
		return true
	}
	return a.variadicString && a.variadicStart >= 0 && i >= a.variadicStart
}
