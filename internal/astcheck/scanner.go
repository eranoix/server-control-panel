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
//	(a) WrappersVigiados — a DECLARED list of in-house functions that execute a
//	    process (the gap the precedent left open). It closes the hole around
//	    `trainerRun(ctx, stdin, args ...string)` in gameservers/trainer.go.
//	    A function NOT on the list whose body calls exec.Command with an
//	    unresolvable argument also fails, via the exec.Command path — otherwise
//	    the list becomes an allowlist that ages in silence, a defect this project
//	    has already paid for.
//
//	(b) ExigirArgvLiteral — the stance is INVERTED. In the precedent the
//	    unresolvable argument was ignored (the pin only asked "is this a forbidden
//	    binary?"). Here the unresolvable IS the danger: it is exactly the way free
//	    execution comes back under another name.
//
//	(c) Varridos is a field of Resultado and Scan returns an ERROR when it is zero.
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

// caminhoExec is the import path that grants access to process execution.
const caminhoExec = "os/exec"

// dirsIgnorados never enter the production sweep.
//
// `testdata` is the most important entry: Go ignores that directory when building
// packages, which is exactly what lets us keep synthetic violations there — and
// also what would make the real tree fail because of the fixtures themselves if
// the sweep did not skip it. The proof that this exclusion is not vacuous is
// TestScanNaoVarreProprioTestdata: pointed straight at testdata, the scanner FINDS.
var dirsIgnorados = map[string]bool{
	"testdata":     true,
	"vendor":       true,
	"node_modules": true,
	".git":         true,
	".claude":      true,
}

// Config describes a sweep.
type Config struct {
	// Raiz is the directory where the sweep starts.
	Raiz string

	// Incluir, when non-empty, restricts the sweep to these subdirectories of
	// Raiz (e.g. {"internal", "cmd"}). Empty sweeps all of Raiz.
	Incluir []string

	// BinsProibidos are binary names that must not be executed from this
	// code (e.g. the hypervisor commands, which belong to the PVE API).
	BinsProibidos []string

	// WrappersVigiados are IN-HOUSE functions that execute a process without
	// going through exec.Command at the call site.
	WrappersVigiados []string

	// ExigirArgvLiteral inverts the precedent's stance: a command argument
	// that does not resolve to a string literal now FAILS.
	ExigirArgvLiteral bool
}

// Achado is a located violation, with a usable message.
//
// Motivo and Trecho are not decoration: a pin that fails without saying what and
// where is a pin someone switches off instead of fixing.
type Achado struct {
	Arquivo string
	Linha   int
	Motivo  string
	Trecho  string
}

// Resultado carries the findings AND how many files were actually parsed.
type Resultado struct {
	Achados []Achado

	// Varridos is the anti-vacuity guard. Zero is an error, never approval.
	Varridos int
}

// Scan sweeps the tree described by cfg and returns the findings.
//
// It returns an error when it parsed no Go file at all: an empty sweep is a
// configuration defect, and returning "0 findings" in that case would produce a
// green by ABSENCE — the costliest defect this repository has ever paid for.
func Scan(cfg Config) (Resultado, error) {
	var res Resultado

	proibidos := make(map[string]bool, len(cfg.BinsProibidos))
	for _, b := range cfg.BinsProibidos {
		proibidos[b] = true
	}
	vigiados := make(map[string]bool, len(cfg.WrappersVigiados))
	for _, w := range cfg.WrappersVigiados {
		vigiados[w] = true
	}

	raizes := make([]string, 0, len(cfg.Incluir))
	if len(cfg.Incluir) == 0 {
		raizes = append(raizes, cfg.Raiz)
	} else {
		for _, sub := range cfg.Incluir {
			raizes = append(raizes, filepath.Join(cfg.Raiz, sub))
		}
	}

	fset := token.NewFileSet()
	// Collect first, analyze later: see the comment on the second pass.
	porDiretorio := map[string][]arquivoParseado{}
	var ordemDeDir []string
	for _, raiz := range raizes {
		if _, err := os.Stat(raiz); err != nil {
			// A subdirectory of Incluir that does not exist is a configuration
			// error, not "nothing to sweep".
			return res, fmt.Errorf("astcheck: root %q unreachable: %w", raiz, err)
		}
		err := filepath.WalkDir(raiz, func(caminho string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if dirsIgnorados[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			arquivo, err := parser.ParseFile(fset, caminho, nil, parser.SkipObjectResolution)
			if err != nil {
				return fmt.Errorf("astcheck: parse of %s: %w", caminho, err)
			}
			dir := filepath.Dir(caminho)
			porDiretorio[dir] = append(porDiretorio[dir], arquivoParseado{caminho: caminho, ast: arquivo})
			ordemDeDir = append(ordemDeDir, dir)
			res.Varridos++
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
	vistos := map[string]bool{}
	for _, dir := range ordemDeDir {
		if vistos[dir] {
			continue
		}
		vistos[dir] = true
		arquivos := porDiretorio[dir]
		consts := constantesDePacote(arquivos)
		assinaturas := assinaturasDeWrappers(arquivos, vigiados)
		for _, a := range arquivos {
			res.Achados = append(res.Achados, varreArquivo(fset, a.ast, a.caminho, proibidos, vigiados, cfg.ExigirArgvLiteral, consts, assinaturas)...)
		}
	}

	if res.Varridos == 0 {
		return res, fmt.Errorf("astcheck: empty scan at %q — no .go file parsed; scanning nothing is never a pass", cfg.Raiz)
	}
	return res, nil
}

// varreArquivo applies the two rules to an already-parsed file.
func varreArquivo(fset *token.FileSet, arquivo *ast.File, caminho string, proibidos, vigiados map[string]bool, exigirLiteral bool, constsDoPacote map[string]*simbolo, assinaturas map[string]assinaturaWrapper) []Achado {
	nomeExec := nomeLocalDeOsExec(arquivo)

	var achados []Achado
	for _, decl := range arquivo.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		simbolos := tabelaDeSimbolos(fn)
		// A package constant comes in as the FLOOR: the local symbol always wins, so
		// that a parameter shadowing the constant's name is not resolved to the
		// constant's value — shadowing is precisely how a varying value would
		// disguise itself as a constant.
		for nome, s := range constsDoPacote {
			if _, local := simbolos[nome]; !local {
				simbolos[nome] = s
			}
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			chamada, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			// Rule 1 — direct execution via os/exec (under an alias too).
			if nomeExec != "" {
				if idx, ehExec := indiceDoBinario(chamada, nomeExec); ehExec {
					if idx < len(chamada.Args) {
						valor, resolvido := resolve(chamada.Args[idx], simbolos)
						switch {
						case resolvido && proibidos[valor]:
							achados = append(achados, monta(fset, caminho, chamada,
								fmt.Sprintf("direct execution of forbidden binary %q — this operation belongs to the hypervisor API, not to the shell", valor)))
						case !resolvido && exigirLiteral:
							achados = append(achados, monta(fset, caminho, chamada,
								fmt.Sprintf("argv of %s does not resolve to a string literal — an unresolvable argument is free execution under another name", nomeExec)))
						}
					}
					return true
				}
			}

			// Rule 2 — a watched in-house wrapper.
			if exigirLiteral {
				if ident, ok := chamada.Fun.(*ast.Ident); ok && vigiados[ident.Name] {
					as, temAssinatura := assinaturas[ident.Name]
					for i, arg := range chamada.Args {
						// With the declared signature in hand, the question is EXACT:
						// "is the position this argument occupies declared string?".
						// Without it (a wrapper from another package), it falls back
						// to the identifier's type heuristic, which is the best
						// possible without type analysis.
						if temAssinatura {
							if !as.posicaoEhComando(i) {
								continue
							}
						} else if !ehArgumentoDeComando(arg, simbolos) {
							continue
						}
						if _, resolvido := resolve(arg, simbolos); !resolvido {
							achados = append(achados, monta(fset, caminho, chamada,
								fmt.Sprintf("watched wrapper %s takes a command argument that does not resolve to a literal — the catalogue stops being closed here", ident.Name)))
							break
						}
					}
				}
			}
			return true
		})
	}
	return achados
}

// nomeLocalDeOsExec returns the name by which "os/exec" is reachable in THIS
// file — which is not necessarily "exec".
//
// It is the hole no textual search closes: `import xc "os/exec"` turns the call
// into `xc.Command(...)`, and a grep for "exec.Command" walks straight past it.
// Returns "" when the file does not import os/exec.
func nomeLocalDeOsExec(arquivo *ast.File) string {
	for _, imp := range arquivo.Imports {
		caminho, err := strconv.Unquote(imp.Path.Value)
		if err != nil || caminho != caminhoExec {
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

// indiceDoBinario recognizes exec.Command / exec.CommandContext and says which
// argument holds the binary name.
func indiceDoBinario(chamada *ast.CallExpr, nomeExec string) (int, bool) {
	sel, ok := chamada.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	pacote, ok := sel.X.(*ast.Ident)
	if !ok || pacote.Name != nomeExec {
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

// simbolo is what is known about a local identifier.
type simbolo struct {
	// valor is the string literal when it is known and unique.
	valor string
	// literal indicates that valor is usable.
	literal bool
	// ehString indicates that the identifier is declared to be of type string.
	// Without type information, it is what allows telling a "command verb" apart
	// from "context" and "[]byte" in a wrapper's argument list.
	ehString bool
}

// tabelaDeSimbolos collects, for one function, what each local identifier is worth.
//
// The pass covers the WHOLE function before any call analysis, so that a later
// reassignment knocks the resolution down: a name assigned twice with different
// values becomes unresolvable, never "the first one that showed up".
func tabelaDeSimbolos(fn *ast.FuncDecl) map[string]*simbolo {
	tab := map[string]*simbolo{}

	marca := func(nome string, s simbolo) {
		if nome == "" || nome == "_" {
			return
		}
		antigo, existe := tab[nome]
		if !existe {
			copia := s
			tab[nome] = &copia
			return
		}
		antigo.ehString = antigo.ehString || s.ehString
		if !s.literal || !antigo.literal || antigo.valor != s.valor {
			// A second, divergent write: the name stops being resolvable.
			antigo.literal = false
			antigo.valor = ""
		}
	}

	// Parameters: never resolve to a literal, but the declared type is visible.
	if fn.Type != nil && fn.Type.Params != nil {
		for _, campo := range fn.Type.Params.List {
			ehStr := ehTipoString(campo.Type)
			for _, nome := range campo.Names {
				marca(nome.Name, simbolo{ehString: ehStr})
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
				if valor, ok := literalDeString(s.Rhs[i]); ok {
					marca(ident.Name, simbolo{valor: valor, literal: true, ehString: true})
				} else {
					marca(ident.Name, simbolo{})
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
				ehStr := vs.Type != nil && ehTipoString(vs.Type)
				for i, nome := range vs.Names {
					if i < len(vs.Values) {
						if valor, ok := literalDeString(vs.Values[i]); ok {
							marca(nome.Name, simbolo{valor: valor, literal: true, ehString: true})
							continue
						}
					}
					marca(nome.Name, simbolo{ehString: ehStr})
				}
			}
		}
		return true
	})
	return tab
}

// ehTipoString recognizes the type `string` as written in the declaration.
func ehTipoString(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "string"
}

// literalDeString extracts the value of a string literal, if it is one.
func literalDeString(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	valor, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return valor, true
}

// resolve tries to obtain an argument's literal value.
func resolve(expr ast.Expr, simbolos map[string]*simbolo) (string, bool) {
	if valor, ok := literalDeString(expr); ok {
		return valor, true
	}
	if ident, ok := expr.(*ast.Ident); ok {
		if s, existe := simbolos[ident.Name]; existe && s.literal {
			return s.valor, true
		}
	}
	return "", false
}

// ehArgumentoDeComando decides whether it is worth demanding a literal here.
//
// Without type information, the question syntax can answer is: is this argument a
// string? `ctx` and `body` in a wrapper are not, and demanding a literal of them
// would produce a false positive on every legitimate call. An argument of type
// string that does not resolve is the case that matters.
func ehArgumentoDeComando(arg ast.Expr, simbolos map[string]*simbolo) bool {
	if _, ok := literalDeString(arg); ok {
		return true
	}
	if ident, ok := arg.(*ast.Ident); ok {
		if s, existe := simbolos[ident.Name]; existe {
			return s.ehString
		}
	}
	return false
}

// monta produces the finding with file, line, reason and rendered snippet.
func monta(fset *token.FileSet, caminho string, node ast.Node, motivo string) Achado {
	pos := fset.Position(node.Pos())
	return Achado{
		Arquivo: caminho,
		Linha:   pos.Line,
		Motivo:  motivo,
		Trecho:  renderiza(fset, node),
	}
}

// renderiza returns the node's source code, so the pin's message shows the
// offending line instead of sending the reader off to look for it.
func renderiza(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return "<snippet unavailable>"
	}
	trecho := strings.Join(strings.Fields(buf.String()), " ")
	const teto = 160
	if len(trecho) > teto {
		trecho = trecho[:teto] + "…"
	}
	return trecho
}

// arquivoParseado holds the path/AST pair between the two passes.
type arquivoParseado struct {
	caminho string
	ast     *ast.File
}

// constantesDePacote collects the string constants declared at PACKAGE LEVEL,
// across every file in the directory.
//
// Only `const`, never `var`: a package `var` can be reassigned at runtime (by
// init, by a test, by any function), and treating it as a literal would open
// exactly the hole the pin exists to close. `const` cannot change.
//
// A name declared twice with different values becomes UNRESOLVABLE, never "the
// first one that showed up".
func constantesDePacote(arquivos []arquivoParseado) map[string]*simbolo {
	tab := map[string]*simbolo{}
	for _, a := range arquivos {
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
				for i, nome := range vs.Names {
					if nome.Name == "_" || i >= len(vs.Values) {
						continue
					}
					valor, ok := literalDeString(vs.Values[i])
					if !ok {
						continue
					}
					if antigo, existe := tab[nome.Name]; existe {
						if antigo.valor != valor {
							antigo.literal = false
							antigo.valor = ""
						}
						continue
					}
					tab[nome.Name] = &simbolo{valor: valor, literal: true, ehString: true}
				}
			}
		}
	}
	return tab
}

// indicesDeString describes, for a watched wrapper, which argument positions
// are of type string in the DECLARATION.
type assinaturaWrapper struct {
	// fixos are the non-variadic string parameter indices.
	fixos map[int]bool
	// variadicoString says the variadic tail is `...string`.
	variadicoString bool
	// inicioVariadico is the index where the tail starts (-1 if there is none).
	inicioVariadico int
}

// assinaturasDeWrappers finds, in the package, the declaration of each watched wrapper.
//
// WHY THIS EXISTS, and why the earlier heuristic was not enough: without the
// signature, the scanner had to GUESS which arguments were the command, by
// looking at the identifier's declared type. That let through the most dangerous
// case of all — `trainerRun(ctx, body, req.Verbo)`, where the verb is a STRUCT
// FIELD coming from the request body, not a plain identifier. With the signature
// in hand, the question stops being "does this argument look like a string?" and
// becomes "is the position this argument occupies declared string?", which is
// exact.
func assinaturasDeWrappers(arquivos []arquivoParseado, vigiados map[string]bool) map[string]assinaturaWrapper {
	out := map[string]assinaturaWrapper{}
	for _, a := range arquivos {
		for _, decl := range a.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !vigiados[fn.Name.Name] || fn.Type.Params == nil {
				continue
			}
			as := assinaturaWrapper{fixos: map[int]bool{}, inicioVariadico: -1}
			idx := 0
			for _, campo := range fn.Type.Params.List {
				n := len(campo.Names)
				if n == 0 {
					n = 1
				}
				if el, ehVariadico := campo.Type.(*ast.Ellipsis); ehVariadico {
					as.inicioVariadico = idx
					as.variadicoString = ehTipoString(el.Elt)
					idx += n
					continue
				}
				if ehTipoString(campo.Type) {
					for i := 0; i < n; i++ {
						as.fixos[idx+i] = true
					}
				}
				idx += n
			}
			out[fn.Name.Name] = as
		}
	}
	return out
}

// posicaoEhComando says whether the argument at position i is declared string.
func (a assinaturaWrapper) posicaoEhComando(i int) bool {
	if a.fixos[i] {
		return true
	}
	return a.variadicoString && a.inicioVariadico >= 0 && i >= a.inicioVariadico
}
