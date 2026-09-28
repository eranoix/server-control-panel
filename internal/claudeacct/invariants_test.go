package claudeacct

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
			t.Errorf("%s: invariant %q appears in %d comment(s). Deleting the code would leave the "+
				"comment satisfying the grep and the deploy would pass. Anchor on something that only "+
				"exists as code, or remove the literal from the comment.", file, fallback, inComment)
		}
	}
	if checked < 4 {
		t.Errorf("only %d metric invariants checked; expected at least 4 "+
			"(EvalSymlinks, IdentityMismatch, RecordAttrib, Unattributed)", checked)
	}
}
