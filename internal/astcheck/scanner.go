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

const execPath = "os/exec"

var ignoredDirs = map[string]bool{
	"testdata":     true,
	"vendor":       true,
	"node_modules": true,
	".git":         true,
	".claude":      true,
}

type Config struct {
	Root string

	Include []string

	ForbiddenBins []string

	WatchedWrappers []string

	RequireLiteralArgv bool
}

type Finding struct {
	File    string
	Line    int
	Reason  string
	Snippet string
}

type Result struct {
	Findings []Finding

	Scanned int
}

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
	byDir := map[string][]parsedFile{}
	var dirOrder []string
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
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

func scanFile(fset *token.FileSet, file *ast.File, path string, forbiddenBins, watched map[string]bool, requireLiteral bool, pkgConsts map[string]*symbol, signatures map[string]wrapperSig) []Finding {
	execName := localOsExecName(file)

	var findings []Finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		symbols := symbolTable(fn)
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

			if requireLiteral {
				if ident, ok := call.Fun.(*ast.Ident); ok && watched[ident.Name] {
					as, hasSig := signatures[ident.Name]
					for i, arg := range call.Args {
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

func localOsExecName(file *ast.File) string {
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != execPath {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				return ""
			}
			return imp.Name.Name
		}
		return "exec"
	}
	return ""
}

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
		return 1, true
	}
	return 0, false
}

type symbol struct {
	value    string
	literal  bool
	isString bool
}

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
			old.literal = false
			old.value = ""
		}
	}

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

func isStringType(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "string"
}

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

func newFinding(fset *token.FileSet, path string, node ast.Node, reason string) Finding {
	pos := fset.Position(node.Pos())
	return Finding{
		File:    path,
		Line:    pos.Line,
		Reason:  reason,
		Snippet: render(fset, node),
	}
}

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

type parsedFile struct {
	path string
	ast  *ast.File
}

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

type wrapperSig struct {
	fixed          map[int]bool
	variadicString bool
	variadicStart  int
}

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

func (a wrapperSig) isCommandPos(i int) bool {
	if a.fixed[i] {
		return true
	}
	return a.variadicString && a.variadicStart >= 0 && i >= a.variadicStart
}
