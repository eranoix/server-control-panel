package sdui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Diff describes a divergence between the committed golden for a
// screen+role and what the real Builder produces today — naming the screen,
// the role and the diff text, so that a CI failure is reviewable without
// running anything locally first.
type Diff struct {
	Screen string
	Role   string
	Detail string
}

// notVisibleSentinel is the golden written when Build returns
// ErrScreenNotFound for a specific role — the Builder itself deciding that
// the whole screen is not visible to that Viewer (see the ErrScreenNotFound
// comment in registry.go: the same error covers "unknown id" and "known id
// but invisible to this Viewer"). Without a sentinel, that legitimate case
// would look like a harness build error instead of an expected, comparable
// result.
type notVisibleSentinel struct {
	NotVisibleToRole bool `json:"not_visible_to_role"`
}

// marshalGoldenIndent formats any serializable value with the same
// indentation MarshalContract (contract.go) uses — two spaces, one trailing
// newline — so that formatting is never the reason a golden diff shows
// up.
func marshalGoldenIndent(v interface{}) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sdui: marshal of the golden: %w", err)
	}
	return append(b, '\n'), nil
}

// buildGoldenBytes builds screen for viewer and returns the exact byte shape
// that goes into the golden: the real serialized Envelope, or
// notVisibleSentinel when the Builder refuses the whole screen for this
// Viewer. Any other error is a real error (a bug in the Builder), never a
// result to compare.
func buildGoldenBytes(ctx context.Context, screen string, viewer Viewer) ([]byte, error) {
	env, err := Build(ctx, screen, viewer)
	if err != nil {
		if errors.Is(err, ErrScreenNotFound) {
			return marshalGoldenIndent(notVisibleSentinel{NotVisibleToRole: true})
		}
		return nil, err
	}
	return marshalGoldenIndent(env)
}

func goldenFileName(dir, screen, role string) string {
	return filepath.Join(dir, fmt.Sprintf("%s.%s.json", screen, role))
}

// sortedRoles returns the keys of viewers in a deterministic order, so that
// the write/compare order never depends on a map's iteration
// order.
func sortedRoles(viewers map[string]Viewer) []string {
	roles := make([]string, 0, len(viewers))
	for r := range viewers {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	return roles
}

// WriteGoldens builds every screen of screens for each Viewer of viewers and
// (re)writes the corresponding golden in dir — the Go golden-test pattern
// with `-update`: running this regenerates the files, it never compares.
// Called by the `make sdui-golden` target.
func WriteGoldens(dir string, screens []string, viewers map[string]Viewer) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("sdui: creating %s: %w", dir, err)
	}
	ctx := context.Background()
	for _, screen := range screens {
		for _, role := range sortedRoles(viewers) {
			b, err := buildGoldenBytes(ctx, screen, viewers[role])
			if err != nil {
				return fmt.Errorf("sdui: building %q for role %q: %w", screen, role, err)
			}
			if err := os.WriteFile(goldenFileName(dir, screen, role), b, 0o644); err != nil {
				return fmt.Errorf("sdui: writing golden for %q/%q: %w", screen, role, err)
			}
		}
	}
	return nil
}

// CompareGoldens builds every screen of screens for each Viewer of viewers
// and compares byte for byte against the committed golden in dir, returning
// one Diff per divergent (screen, role) — a missing golden counts as a
// divergence, never as "nothing to compare".
func CompareGoldens(dir string, screens []string, viewers map[string]Viewer) ([]Diff, error) {
	var diffs []Diff
	ctx := context.Background()
	for _, screen := range screens {
		for _, role := range sortedRoles(viewers) {
			got, err := buildGoldenBytes(ctx, screen, viewers[role])
			if err != nil {
				return nil, fmt.Errorf("sdui: building %q for role %q: %w", screen, role, err)
			}

			path := goldenFileName(dir, screen, role)
			want, err := os.ReadFile(path)
			if err != nil {
				diffs = append(diffs, Diff{
					Screen: screen,
					Role:   role,
					Detail: fmt.Sprintf("golden %s missing", path),
				})
				continue
			}

			if !bytes.Equal(got, want) {
				diffs = append(diffs, Diff{
					Screen: screen,
					Role:   role,
					Detail: fmt.Sprintf("golden %s diverges from the real builder:\n%s", path, unifiedDiff(string(want), string(got))),
				})
			}
		}
	}
	return diffs, nil
}

// unifiedDiff produces a line diff in unified style (- only in want, + only
// in got, identical lines with no prefix) via LCS — enough for a human
// reviewer to locate the exact field without having to run `diff` by hand.
func unifiedDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	n, m := len(wantLines), len(gotLines)

	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if wantLines[i] == gotLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var lines []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case wantLines[i] == gotLines[j]:
			lines = append(lines, "  "+wantLines[i])
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			lines = append(lines, "- "+wantLines[i])
			i++
		default:
			lines = append(lines, "+ "+gotLines[j])
			j++
		}
	}
	for ; i < n; i++ {
		lines = append(lines, "- "+wantLines[i])
	}
	for ; j < m; j++ {
		lines = append(lines, "+ "+gotLines[j])
	}
	return strings.Join(lines, "\n")
}
