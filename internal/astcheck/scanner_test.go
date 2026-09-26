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

// hypervisorBins is the list the fixtures use. The same slice as the
// precedent's pin, so the behavior is comparable.
var hypervisorBins = []string{"pct", "qm", "pvesh", "pvesm", "pveum"}

func fixture(name string) string { return filepath.Join("testdata", name) }

// requireOneFinding is the shared shape of the matrix's positive halves.
func requireOneFinding(t *testing.T, cfg Config, wantFile string) Finding {
	t.Helper()
	res, err := Scan(cfg)
	if err != nil {
		t.Fatalf("Scan(%s): %v", cfg.Root, err)
	}
	if res.Scanned < 1 {
		t.Fatalf("Scanned=%d: the scan parsed nothing, the green would be by ABSENCE", res.Scanned)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("FALSE NEGATIVE: want exactly 1 finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	a := res.Findings[0]
	if !strings.HasSuffix(a.File, wantFile) {
		t.Fatalf("finding with no usable file: %+v (want suffix %q)", a, wantFile)
	}
	if a.Line <= 0 {
		t.Fatalf("finding with no usable line: %+v", a)
	}
	if a.Reason == "" || a.Snippet == "" {
		t.Fatalf("finding with no reason/snippet — the message is half the guard: %+v", a)
	}
	return a
}

func TestScanDetectsForbiddenBin(t *testing.T) {
	a := requireOneFinding(t, Config{
		Root:          fixture("forbidden_bin"),
		ForbiddenBins: hypervisorBins,
	}, "case.go")
	if !strings.Contains(a.Reason, "pct") {
		t.Fatalf("the reason does not name the binary: %q", a.Reason)
	}
}

func TestScanResolvesVariable(t *testing.T) {
	// The classic hole: `bin := "pct"` followed by exec.Command(bin, …). A textual
	// search does not see it; intra-function literal resolution does.
	a := requireOneFinding(t, Config{
		Root:          fixture("resolved_var"),
		ForbiddenBins: hypervisorBins,
	}, "case.go")
	if !strings.Contains(a.Reason, "pct") {
		t.Fatalf("the reason does not name the resolved binary: %q", a.Reason)
	}
}

func TestScanResolvesImportAlias(t *testing.T) {
	// `import xc "os/exec"` — the alias changes the package name at the call site.
	requireOneFinding(t, Config{
		Root:          fixture("alias_import"),
		ForbiddenBins: hypervisorBins,
	}, "case.go")
}

func TestScanWatchedWrapper(t *testing.T) {
	// Positive half: the verb is a parameter, it does not resolve to a literal —
	// this is where free execution comes back under another name.
	t.Run("unresolvable verb fails", func(t *testing.T) {
		a := requireOneFinding(t, Config{
			Root:               fixture("unresolvable_wrapper"),
			WatchedWrappers:    []string{"trainerRun"},
			RequireLiteralArgv: true,
		}, "case.go")
		if !strings.Contains(a.Reason, "trainerRun") {
			t.Fatalf("the reason does not name the wrapper: %q", a.Reason)
		}
	})

	// Negative half of the SAME wrapper: a legitimate call with a literal verb.
	t.Run("literal verb passes", func(t *testing.T) {
		res, err := Scan(Config{
			Root:               fixture("wrapper_literal"),
			WatchedWrappers:    []string{"trainerRun"},
			RequireLiteralArgv: true,
		})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(res.Findings) != 0 {
			t.Fatalf("FALSE POSITIVE on the legitimate use of the wrapper: %+v", res.Findings)
		}
		if res.Scanned != 1 {
			t.Fatalf("Scanned=%d, want 1", res.Scanned)
		}
	})
}

func TestScanUnlistedWrapperAlsoFails(t *testing.T) {
	// The wrapper list ages in silence if creating a new wrapper is free.
	// A wrapper NOT on the list whose body calls exec.Command with an
	// unresolvable argument fails via the exec.Command path, list or no list.
	res, err := Scan(Config{
		Root:               fixture("unlisted_wrapper"),
		ForbiddenBins:      hypervisorBins,
		RequireLiteralArgv: true,
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Findings) == 0 {
		t.Fatal("FALSE NEGATIVE: a new wrapper with unresolvable argv passed — the watch list would become an allowlist that ages")
	}
}

func TestScanNegativeControl(t *testing.T) {
	// Control 1: the false positive MEASURED on the real tree
	// (internal/queue/runners_watchdog.go:45). If this fails here, the pin fails
	// legitimate code and becomes a candidate for being switched off.
	t.Run("diskUsedPct is not a command call", func(t *testing.T) {
		res, err := Scan(Config{
			Root:               fixture("neg_diskusedpct"),
			ForbiddenBins:      hypervisorBins,
			RequireLiteralArgv: true,
		})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(res.Findings) != 0 {
			t.Fatalf("FALSE POSITIVE: %+v", res.Findings)
		}
		if res.Scanned != 1 {
			t.Fatalf("Scanned=%d, want 1", res.Scanned)
		}
	})

	// Control 2: a legitimate exec.Command stays allowed. The panel runs
	// systemctl, git and docker all the time; the pin has to know how to approve.
	t.Run("legitimate exec.Command passes", func(t *testing.T) {
		res, err := Scan(Config{
			Root:               fixture("neg_legit_exec"),
			ForbiddenBins:      hypervisorBins, // systemctl is NOT on the list
			RequireLiteralArgv: true,
		})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(res.Findings) != 0 {
			t.Fatalf("FALSE POSITIVE: %+v", res.Findings)
		}
		if res.Scanned != 1 {
			t.Fatalf("Scanned=%d, want 1", res.Scanned)
		}
	})
}

func TestScanEmptySweepIsError(t *testing.T) {
	// Scanning nothing is NEVER approving. In the precedent this was a comment
	// plus a check each consumer had to remember to make; here it is API
	// contract, so that nobody can ignore it.
	res, err := Scan(Config{
		Root:          fixture("no_go"),
		ForbiddenBins: hypervisorBins,
	})
	if err == nil {
		t.Fatal("Scan returned err=nil scanning a directory with no .go at all — green by ABSENCE")
	}
	if res.Scanned != 0 {
		t.Fatalf("Scanned=%d, want 0", res.Scanned)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings in an empty scan: %+v", res.Findings)
	}
}

func TestScanSkipsOwnTestdata(t *testing.T) {
	// Without this explicit assertion, the violation fixtures above would fail the
	// real tree: they contain exec.Command("pct", …) on purpose. The production
	// sweep has to skip testdata/ — and the proof that the exclusion is not vacuous
	// comes from the second Scan, which points straight at testdata and FINDS.
	root := repoRoot(t)

	real, err := Scan(Config{
		Root:          root,
		Include:       []string{"internal", "cmd"},
		ForbiddenBins: hypervisorBins,
	})
	if err != nil {
		t.Fatalf("Scan of the real tree: %v", err)
	}
	if real.Scanned < 50 {
		t.Fatalf("Scanned=%d on the real tree: the scan is too small to be the tree", real.Scanned)
	}
	for _, a := range real.Findings {
		if strings.Contains(a.File, "testdata") {
			t.Errorf("testdata/ entered the production scan: %+v", a)
		} else {
			t.Errorf("the real tree failed: %+v", a)
		}
	}

	direct, err := Scan(Config{
		Root:          filepath.Join("testdata", "forbidden_bin"),
		ForbiddenBins: hypervisorBins,
	})
	if err != nil {
		t.Fatalf("direct Scan of testdata: %v", err)
	}
	if len(direct.Findings) == 0 {
		t.Fatal("the testdata exclusion is VACUOUS: pointed at directly, the scanner finds nothing in the fixtures")
	}
	t.Logf("%d production files scanned, 0 findings; the fixtures find %d when pointed at directly",
		real.Scanned, len(direct.Findings))
}

// repoRoot climbs up to the go.mod: the production sweep needs the whole
// tree, not just this package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("go.mod not found walking up from the package")
	return ""
}
