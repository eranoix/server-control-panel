package claudeacct

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An invariant whose literal ALSO appears in a COMMENT is defeated by the
// comment: you delete the code, the commented line stays there, the gate's grep
// finds the text and the deploy goes through. It was measured: removing the body
// of resolvedProjectsDir made the gate announce "✓ invariants OK", because the
// file's header said "Resolved here with EvalSymlinks".
//
// This repository has already paid for that once — commit 21dc2e4 pulled the
// literal of a forbidden route out of a comment for the same reason. The lesson
// had not become a test, so it did not generalize. Now it has.
//
// The test covers only the per-account metric invariants, which are the ones
// this package delivers; the rest belong to other tickets and touching them
// here would mean changing someone else's guard without the context.
func TestMetricInvariantsDoNotMatchInComments(t *testing.T) {
	inv := filepath.Join("..", "..", ".claude", "coord", "invariants.txt")
	data, err := os.ReadFile(inv)
	if err != nil {
		t.Skipf("invariants.txt unavailable (%v) — this guard lives next to the coordination board", err)
	}
	ours := map[string]bool{
		"internal/claudeacct/usage.go":      true,
		"internal/claudeacct/claudeacct.go": true,
		"internal/claudeacct/attrib.go":     true,
	}
	checked := 0
	for _, line := range strings.Split(string(data), "\n") {
		p := strings.Split(strings.TrimSpace(line), "|")
		if len(p) < 2 || strings.HasPrefix(line, "#") || !ours[p[0]] {
			continue
		}
		file, fallback := p[0], p[1]
		src, err := os.ReadFile(filepath.Join("..", "..", file))
		if err != nil {
			t.Errorf("invariant points at a missing file: %s", file)
			continue
		}
		var total, inComment int
		for _, l := range strings.Split(string(src), "\n") {
			if !strings.Contains(l, fallback) {
				continue
			}
			total++
			if strings.HasPrefix(strings.TrimSpace(l), "//") {
				inComment++
			}
		}
		checked++
		if total == 0 {
			t.Errorf("%s: invariant %q matches nowhere — an empty guard", file, fallback)
		}
		if inComment > 0 {
			t.Errorf("%s: invariante %q aparece em %d comentário(s). Apagar o código deixaria o "+
				"comentário satisfazendo o grep, e o deploy passaria. Ancore em algo que só "+
				"exista como código, ou tire o literal do comentário.", file, fallback, inComment)
		}
	}
	// Anti-vacuity: if the invariants get renamed and this loop stops matching
	// anything, the test would go green without having verified a thing.
	if checked < 4 {
		t.Errorf("só %d invariantes de métrica verificados; esperava ao menos 4 "+
			"(EvalSymlinks, IdentityMismatch, RecordAttrib, Unattributed)", checked)
	}
}
