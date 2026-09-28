package astcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var hypervisorBins = []string{"pct", "qm", "pvesh", "pvesm", "pveum"}

func fixture(name string) string { return filepath.Join("testdata", name) }

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
	a := requireOneFinding(t, Config{
		Root:          fixture("resolved_var"),
		ForbiddenBins: hypervisorBins,
	}, "case.go")
	if !strings.Contains(a.Reason, "pct") {
		t.Fatalf("the reason does not name the resolved binary: %q", a.Reason)
	}
}

func TestScanResolvesImportAlias(t *testing.T) {
	requireOneFinding(t, Config{
		Root:          fixture("alias_import"),
		ForbiddenBins: hypervisorBins,
	}, "case.go")
}

func TestScanWatchedWrapper(t *testing.T) {
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

	t.Run("legitimate exec.Command passes", func(t *testing.T) {
		res, err := Scan(Config{
			Root:               fixture("neg_legit_exec"),
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
}

func TestScanEmptySweepIsError(t *testing.T) {
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
