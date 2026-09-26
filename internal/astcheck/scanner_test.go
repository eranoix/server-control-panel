package astcheck

// scanner_test.go — the TEST OF THE TEST.
//
// A detector only proves something once someone has proven the detector. Without
// this file, a green from any pin built on top of `Scan` would prove only that the
// function LABELS, never that it DETECTS — and green by absence is the costliest
// defect this repository has ever paid for.
//
// The matrix has two halves and both are mandatory:
//   - synthetic violations that MUST be found (otherwise the pin is decorative);
//   - negative controls that must NOT be failed (otherwise the pin is the kind
//     someone switches off on the first Friday, and then it protects nothing).
//
// The fixtures live in testdata/ because Go ignores that directory when building
// packages: it is the only place where deliberately wrong code can be kept
// without contaminating the production tree.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// binsDeHipervisor is the list the fixtures use. The same slice as the
// precedent's pin, so the behavior is comparable.
var binsDeHipervisor = []string{"pct", "qm", "pvesh", "pvesm", "pveum"}

func fixture(nome string) string { return filepath.Join("testdata", nome) }

// exigeUmAchado is the shared shape of the matrix's positive halves.
func exigeUmAchado(t *testing.T, cfg Config, querArquivo string) Achado {
	t.Helper()
	res, err := Scan(cfg)
	if err != nil {
		t.Fatalf("Scan(%s): %v", cfg.Raiz, err)
	}
	if res.Varridos < 1 {
		t.Fatalf("Varridos=%d — the scan parsed nothing, the green would be by ABSENCE", res.Varridos)
	}
	if len(res.Achados) != 1 {
		t.Fatalf("FALSE NEGATIVE: want exactly 1 finding, got %d: %+v", len(res.Achados), res.Achados)
	}
	a := res.Achados[0]
	if !strings.HasSuffix(a.Arquivo, querArquivo) {
		t.Fatalf("finding with no usable file: %+v (want suffix %q)", a, querArquivo)
	}
	if a.Linha <= 0 {
		t.Fatalf("finding with no usable line: %+v", a)
	}
	if a.Motivo == "" || a.Trecho == "" {
		t.Fatalf("finding with no reason/snippet — the message is half the guard: %+v", a)
	}
	return a
}

func TestScanDetectaBinProibido(t *testing.T) {
	a := exigeUmAchado(t, Config{
		Raiz:          fixture("bin_proibido"),
		BinsProibidos: binsDeHipervisor,
	}, "caso.go")
	if !strings.Contains(a.Motivo, "pct") {
		t.Fatalf("the reason does not name the binary: %q", a.Motivo)
	}
}

func TestScanResolveVariavel(t *testing.T) {
	// The classic hole: `bin := "pct"` followed by exec.Command(bin, …). A textual
	// search does not see it; intra-function literal resolution does.
	a := exigeUmAchado(t, Config{
		Raiz:          fixture("var_resolvida"),
		BinsProibidos: binsDeHipervisor,
	}, "caso.go")
	if !strings.Contains(a.Motivo, "pct") {
		t.Fatalf("the reason does not name the resolved binary: %q", a.Motivo)
	}
}

func TestScanResolveAliasDeImport(t *testing.T) {
	// `import xc "os/exec"` — the alias changes the package name at the call site.
	exigeUmAchado(t, Config{
		Raiz:          fixture("alias_import"),
		BinsProibidos: binsDeHipervisor,
	}, "caso.go")
}

func TestScanWrapperVigiado(t *testing.T) {
	// Positive half: the verb is a parameter, it does not resolve to a literal —
	// this is where free execution comes back under another name.
	t.Run("unresolvable verb fails", func(t *testing.T) {
		a := exigeUmAchado(t, Config{
			Raiz:              fixture("wrapper_irresoluvel"),
			WrappersVigiados:  []string{"trainerRun"},
			ExigirArgvLiteral: true,
		}, "caso.go")
		if !strings.Contains(a.Motivo, "trainerRun") {
			t.Fatalf("the reason does not name the wrapper: %q", a.Motivo)
		}
	})

	// Negative half of the SAME wrapper: a legitimate call with a literal verb.
	t.Run("literal verb passes", func(t *testing.T) {
		res, err := Scan(Config{
			Raiz:              fixture("wrapper_literal"),
			WrappersVigiados:  []string{"trainerRun"},
			ExigirArgvLiteral: true,
		})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(res.Achados) != 0 {
			t.Fatalf("FALSE POSITIVE on the legitimate use of the wrapper: %+v", res.Achados)
		}
		if res.Varridos != 1 {
			t.Fatalf("Varridos=%d, want 1", res.Varridos)
		}
	})
}

func TestScanWrapperNaoListadoTambemReprova(t *testing.T) {
	// The wrapper list ages in silence if creating a new wrapper is free.
	// A wrapper NOT on the list whose body calls exec.Command with an
	// unresolvable argument fails via the exec.Command path, list or no list.
	res, err := Scan(Config{
		Raiz:              fixture("wrapper_nao_listado"),
		BinsProibidos:     binsDeHipervisor,
		ExigirArgvLiteral: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Achados) == 0 {
		t.Fatal("FALSE NEGATIVE: a new wrapper with unresolvable argv passed — the watch list would become an allowlist that ages")
	}
}

func TestScanControleNegativo(t *testing.T) {
	// Control 1: the false positive MEASURED on the real tree
	// (internal/queue/runners_watchdog.go:45). If this fails here, the pin fails
	// legitimate code and becomes a candidate for being switched off.
	t.Run("diskUsedPct is not a command call", func(t *testing.T) {
		res, err := Scan(Config{
			Raiz:              fixture("neg_diskusedpct"),
			BinsProibidos:     binsDeHipervisor,
			ExigirArgvLiteral: true,
		})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(res.Achados) != 0 {
			t.Fatalf("FALSE POSITIVE: %+v", res.Achados)
		}
		if res.Varridos != 1 {
			t.Fatalf("Varridos=%d, want 1", res.Varridos)
		}
	})

	// Control 2: a legitimate exec.Command stays allowed. The panel runs
	// systemctl, git and docker all the time; the pin has to know how to approve.
	t.Run("legitimate exec.Command passes", func(t *testing.T) {
		res, err := Scan(Config{
			Raiz:              fixture("neg_exec_legitimo"),
			BinsProibidos:     binsDeHipervisor, // systemctl is NOT on the list
			ExigirArgvLiteral: true,
		})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(res.Achados) != 0 {
			t.Fatalf("FALSE POSITIVE: %+v", res.Achados)
		}
		if res.Varridos != 1 {
			t.Fatalf("Varridos=%d, want 1", res.Varridos)
		}
	})
}

func TestScanVarreduraVaziaEhErro(t *testing.T) {
	// Scanning nothing is NEVER approving. In the precedent this was a comment
	// plus a check each consumer had to remember to make; here it is API
	// contract, so that nobody can ignore it.
	res, err := Scan(Config{
		Raiz:          fixture("sem_go"),
		BinsProibidos: binsDeHipervisor,
	})
	if err == nil {
		t.Fatal("Scan returned err=nil scanning a directory with no .go at all — green by ABSENCE")
	}
	if res.Varridos != 0 {
		t.Fatalf("Varridos=%d, want 0", res.Varridos)
	}
	if len(res.Achados) != 0 {
		t.Fatalf("findings in an empty scan: %+v", res.Achados)
	}
}

func TestScanNaoVarreProprioTestdata(t *testing.T) {
	// Without this explicit assertion, the violation fixtures above would fail the
	// real tree: they contain exec.Command("pct", …) on purpose. The production
	// sweep has to skip testdata/ — and the proof that the exclusion is not vacuous
	// comes from the second Scan, which points straight at testdata and FINDS.
	raiz := raizDoRepo(t)

	real, err := Scan(Config{
		Raiz:          raiz,
		Incluir:       []string{"internal", "cmd"},
		BinsProibidos: binsDeHipervisor,
	})
	if err != nil {
		t.Fatalf("Scan of the real tree: %v", err)
	}
	if real.Varridos < 50 {
		t.Fatalf("Varridos=%d on the real tree — the scan is too small to be the tree", real.Varridos)
	}
	for _, a := range real.Achados {
		if strings.Contains(a.Arquivo, "testdata") {
			t.Errorf("testdata/ entered the production scan: %+v", a)
		} else {
			t.Errorf("the real tree failed: %+v", a)
		}
	}

	direto, err := Scan(Config{
		Raiz:          filepath.Join("testdata", "bin_proibido"),
		BinsProibidos: binsDeHipervisor,
	})
	if err != nil {
		t.Fatalf("direct Scan of testdata: %v", err)
	}
	if len(direto.Achados) == 0 {
		t.Fatal("the testdata exclusion is VACUOUS: pointed at directly, the scanner finds nothing in the fixtures")
	}
	t.Logf("%d production files scanned, 0 findings; the fixtures find %d when pointed at directly",
		real.Varridos, len(direto.Achados))
}

// raizDoRepo climbs up to the go.mod: the production sweep needs the whole
// tree, not just this package's directory.
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
	t.Fatal("go.mod not found walking up from the package")
	return ""
}
