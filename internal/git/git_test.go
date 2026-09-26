package git

import (
	"strings"
	"testing"

	"server-control-panel/internal/config"
)

// TestParseLog validates the deserialization of the %H\0%P\0%an\0%aI\0%D\0%s
// format, including multiple parents and an empty %D (a commit with no decoration).
func TestParseLog(t *testing.T) {
	out := strings.Join([]string{
		"aaaa\x00bbbb cccc\x00Alice\x00alice@x.com\x002026-01-01T00:00:00Z\x00HEAD -> main, origin/main\x00merge feature",
		"bbbb\x00dddd\x00Bob\x00bob@y.com\x002025-12-31T00:00:00Z\x00\x00work on b",
	}, "\n")
	cs := parseLog(out)
	if len(cs) != 2 {
		t.Fatalf("len: got %d want 2", len(cs))
	}
	if cs[0].Hash != "aaaa" || len(cs[0].Parents) != 2 || cs[0].Parents[1] != "cccc" {
		t.Errorf("commit0 parents wrong: %+v", cs[0])
	}
	if cs[0].Email != "alice@x.com" {
		t.Errorf("wrong email: %+v", cs[0].Email)
	}
	if len(cs[0].Refs) != 2 || cs[0].Refs[0] != "HEAD -> main" {
		t.Errorf("wrong refs: %+v", cs[0].Refs)
	}
	if cs[1].Refs != nil {
		t.Errorf("refs should be nil on an empty %%D, got %+v", cs[1].Refs)
	}
}

// TestLayoutMergeUsesMultipleLanes is the central machine-checked (not
// eyeballed) test of the "SVG graph required" requirement: over a graph with
// a merge, the layout MUST use ≥2 lanes and the merge must emit one edge per parent.
//
// Graph (date-order, top→bottom):
//
//	A  merge, parents B,C
//	B  parent D
//	C  parent D
//	D  root
func TestLayoutMergeUsesMultipleLanes(t *testing.T) {
	commits := []Commit{
		{Hash: "A", Parents: []string{"B", "C"}},
		{Hash: "B", Parents: []string{"D"}},
		{Hash: "C", Parents: []string{"D"}},
		{Hash: "D"},
	}
	g := layoutGraph(commits)

	if g.MaxLane < 1 {
		t.Fatalf("a merge should use ≥2 lanes (max_lane≥1), got max_lane=%d", g.MaxLane)
	}
	// Merge A must have exactly 2 edges (one per parent), in distinct lanes.
	var aEdges []Edge
	for _, e := range g.Edges {
		if e.FromRow == 0 {
			aEdges = append(aEdges, e)
		}
	}
	if len(aEdges) != 2 {
		t.Fatalf("merge A should emit 2 edges, got %d", len(aEdges))
	}
	if aEdges[0].ToLane == aEdges[1].ToLane {
		t.Errorf("the merge's two parents should land in distinct lanes, got %d/%d",
			aEdges[0].ToLane, aEdges[1].ToLane)
	}
	// B and C occupy distinct lanes (they do not collapse into the same column).
	laneOf := map[string]int{}
	for _, c := range g.Commits {
		laneOf[c.Hash] = c.Lane
	}
	if laneOf["B"] == laneOf["C"] {
		t.Errorf("B and C should be in distinct lanes, both are in %d", laneOf["B"])
	}
}

// TestLayoutEdgesPointToRealParents makes sure that NO edge between two
// commits that are both present points at an absent hash; and that edges to
// parents outside the window are marked Dangling (truncated), never pointing
// off into nothing with a valid ToRow.
func TestLayoutEdgesPointToRealParents(t *testing.T) {
	// E has a parent F that is NOT in the window (dangling at the edge of the limit).
	commits := []Commit{
		{Hash: "A", Parents: []string{"B"}},
		{Hash: "B", Parents: []string{"E"}},
		{Hash: "E", Parents: []string{"F"}}, // F absent
	}
	g := layoutGraph(commits)
	rowOf := map[string]int{"A": 0, "B": 1, "E": 2}

	for _, e := range g.Edges {
		if e.Dangling {
			if e.ToRow != -1 {
				t.Errorf("a dangling edge should have ToRow=-1, got %d", e.ToRow)
			}
			continue
		}
		// Non-dangling: ToRow has to be a real row.
		if e.ToRow < 0 || e.ToRow >= len(g.Commits) {
			t.Errorf("non-dangling edge with an invalid ToRow: %+v", e)
		}
		// And FromRow→ToRow has to match a real parent.
		child := g.Commits[e.FromRow]
		parent := g.Commits[e.ToRow]
		found := false
		for _, p := range child.Parents {
			if p == parent.Hash {
				found = true
			}
		}
		if !found {
			t.Errorf("edge %s→%s does not correspond to a real parent", child.Hash, parent.Hash)
		}
		_ = rowOf
	}
	// There must be exactly one dangling edge (E→F).
	dangling := 0
	for _, e := range g.Edges {
		if e.Dangling {
			dangling++
		}
	}
	if dangling != 1 {
		t.Errorf("expected 1 dangling edge (E→F missing), got %d", dangling)
	}
}

func TestLayoutEmpty(t *testing.T) {
	g := layoutGraph(nil)
	if g.Commits == nil || len(g.Commits) != 0 {
		t.Errorf("an empty graph should have Commits=[] non-nil, got %+v", g.Commits)
	}
}

func TestValidRef(t *testing.T) {
	ok := []string{"main", "feature/x", "v1.2.3", "refactor/foundation", "a_b-c.d"}
	bad := []string{"", "-rf", "--all", "a..b", "a b", "a;b", "$(x)", "a\nb", strings.Repeat("a", 300)}
	for _, s := range ok {
		if !validRef(s) {
			t.Errorf("validRef(%q) = false, wanted true", s)
		}
	}
	for _, s := range bad {
		if validRef(s) {
			t.Errorf("validRef(%q) = true, wanted false", s)
		}
	}
}

func TestValidRelPath(t *testing.T) {
	ok := []string{"a.txt", "dir/b.go", "deep/nested/file"}
	bad := []string{"", "/abs", "../escape", "a/../../b", "x\x00y"}
	for _, s := range ok {
		if !validRelPath(s) {
			t.Errorf("validRelPath(%q) = false, wanted true", s)
		}
	}
	for _, s := range bad {
		if validRelPath(s) {
			t.Errorf("validRelPath(%q) = true, wanted false", s)
		}
	}
}

func TestClampLimit(t *testing.T) {
	cases := map[int]int{0: 100, -5: 100, 50: 50, 3000: 2000, 2000: 2000}
	for in, want := range cases {
		if got := clampLimit(in, 100); got != want {
			t.Errorf("clampLimit(%d)=%d want %d", in, got, want)
		}
	}
}

func TestParseStatusV2(t *testing.T) {
	// 1 ordinary modified-staged + 1 untracked.
	out := strings.Join([]string{
		"# branch.head main",
		"1 M. N... 100644 100644 100644 aaa bbb file1.go",
		"? new.txt",
	}, "\x00") + "\x00"
	branch, detached, entries := parseStatusV2(out)
	if branch != "main" || detached {
		t.Errorf("branch=%q detached=%v", branch, detached)
	}
	if len(entries) != 2 {
		t.Fatalf("entries: got %d want 2", len(entries))
	}
	if entries[0].Path != "file1.go" || entries[0].Index != "M" || entries[0].Worktree != "." {
		t.Errorf("entry0 wrong: %+v", entries[0])
	}
	if !entries[1].Untracked || entries[1].Path != "new.txt" {
		t.Errorf("entry1 untracked wrong: %+v", entries[1])
	}
}

func TestParseNameStatusZ(t *testing.T) {
	// M file1, R100 oldname->newname
	out := "M\x00file1.go\x00R100\x00old.go\x00new.go\x00"
	files := parseNameStatusZ(out)
	if len(files) != 2 {
		t.Fatalf("files: got %d want 2", len(files))
	}
	if files[0].Status != "M" || files[0].Path != "file1.go" {
		t.Errorf("file0: %+v", files[0])
	}
	if !strings.HasPrefix(files[1].Status, "R") || files[1].Orig != "old.go" || files[1].Path != "new.go" {
		t.Errorf("file1 rename: %+v", files[1])
	}
}

func TestEffectiveReposMergeAndDefaults(t *testing.T) {
	// nil cfg → only the seed (3 repos: venice/supabase removed).
	seed := effectiveRepos(nil)
	if len(seed) != 3 {
		t.Fatalf("the seed should have 3 repos, got %d", len(seed))
	}
	// vps-manager is writable (changed at the user's request) with the personal identity.
	if r, ok := findRepo(seed, "vps-manager"); !ok || r.Policy != policyWrite || r.ExpEmail != "sam.rivera@personal.example" {
		t.Errorf("vps-manager should be write/personal in the seed: %+v", r)
	}
	// northwind-web is write with the work identity.
	if r, ok := findRepo(seed, "northwind-web"); !ok || r.Policy != policyWrite || r.ExpEmail != "sam@northwind.example" {
		t.Errorf("northwind-web seed wrong: %+v", r)
	}
	// acme-booking NEVER with a work e-mail.
	if r, ok := findRepo(seed, "css-lee"); !ok || r.ExpEmail == "sam@northwind.example" {
		t.Errorf("css-lee cannot carry a work e-mail: %+v", r)
	}
}

func TestParseBlamePorcelain(t *testing.T) {
	out := strings.Join([]string{
		"7f5bf270c8001384284acfd799a2cc27697a087a 1 1 2",
		"author Alice",
		"author-mail <alice@x.com>",
		"author-time 1781038626",
		"author-tz +0000",
		"summary primeiro commit",
		"boundary",
		"filename go.mod",
		"\tmodule vps-manager",
		"7f5bf270c8001384284acfd799a2cc27697a087a 2 2",
		"\tgo 1.22",
	}, "\n")
	got := parseBlamePorcelain(out)
	if len(got) != 2 {
		t.Fatalf("len: got %d want 2", len(got))
	}
	if got[0].Author != "Alice" || got[0].Email != "alice@x.com" {
		t.Errorf("wrong authorship: %+v", got[0])
	}
	if got[0].Line != 1 || got[0].Content != "module vps-manager" || !got[0].Boundary {
		t.Errorf("line0 wrong: %+v", got[0])
	}
	if got[0].Short != "7f5bf270" {
		t.Errorf("short wrong: %q", got[0].Short)
	}
	if got[1].Line != 2 || got[1].Content != "go 1.22" {
		t.Errorf("line1 wrong: %+v", got[1])
	}
	if !strings.HasPrefix(got[0].Date, "2026-") {
		t.Errorf("expected an ISO date from the epoch, got %q", got[0].Date)
	}
}

func TestParseFileLog(t *testing.T) {
	out := "\x01aaa\x00Alice\x00alice@x.com\x002026-01-01T00:00:00Z\x00fix bug\n" +
		"34\t12\tgo.mod\n" +
		"\n" +
		"\x01bbb\x00Bob\x00bob@y.com\x002025-12-31T00:00:00Z\x00init\n" +
		"68\t0\tgo.mod\n"
	got := parseFileLog(out)
	if len(got) != 2 {
		t.Fatalf("len: got %d want 2", len(got))
	}
	if got[0].Hash != "aaa" || got[0].Author != "Alice" || got[0].Add != 34 || got[0].Del != 12 {
		t.Errorf("commit0 wrong: %+v", got[0])
	}
	if got[1].Hash != "bbb" || got[1].Add != 68 || got[1].Del != 0 {
		t.Errorf("commit1 wrong: %+v", got[1])
	}
}

func findRepo(list []config.GitRepo, id string) (config.GitRepo, bool) {
	for _, r := range list {
		if r.ID == id {
			return r, true
		}
	}
	return config.GitRepo{}, false
}
