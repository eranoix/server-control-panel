package mobilebff

import (
	"testing"
	"time"

	"server-control-panel/internal/jira"
)

func testIssue(key, status, category string) jira.Issue {
	return jira.Issue{
		Key:     key,
		Summary: "summary of " + key,
		Status: jira.Status{
			Name:           status,
			StatusCategory: jira.StatusCategory{Key: category},
		},
	}
}

func TestBoardWithoutConfigUsesThreeCategories(t *testing.T) {
	// This project calls "To Do" "Backlog". Columns by NAME would break on
	// it; by category, they do not.
	issues := []jira.Issue{
		testIssue("V-1", "Backlog", "new"),
		testIssue("V-2", "IN REVIEW", "indeterminate"),
		testIssue("V-3", "Ready", "done"),
	}
	cols := BuildBoard("", issues, 0, "", time.Now())

	if len(cols) != 3 {
		t.Fatalf("expected 3 columns by category, got %d", len(cols))
	}
	if len(cols[0].Cards) != 1 || cols[0].Cards[0].Key != "V-1" {
		t.Errorf("Backlog had to land in the 'new' column: %+v", cols[0].Cards)
	}
	if len(cols[1].Cards) != 1 || cols[1].Cards[0].Key != "V-2" {
		t.Errorf("IN REVIEW had to land in the 'indeterminate' column: %+v", cols[1].Cards)
	}
	if len(cols[2].Cards) != 1 || cols[2].Cards[0].Key != "V-3" {
		t.Errorf("Ready had to land in the 'done' column: %+v", cols[2].Cards)
	}
}

func TestBoardWithConfiguredColumnsMatchesByNameCaseInsensitive(t *testing.T) {
	cfg := `[{"label":"Doing","status_names":["In Progress","IN REVIEW"]}]`
	issues := []jira.Issue{
		testIssue("V-1", "in review", "indeterminate"),
	}
	cols := BuildBoard(cfg, issues, 0, "", time.Now())

	if len(cols) != 1 {
		t.Fatalf("expected 1 column (no orphans, no 'Others'), got %d", len(cols))
	}
	if len(cols[0].Cards) != 1 {
		t.Fatalf("name matching must ignore case: %+v", cols[0])
	}
}

func TestOtherColumnOnlyExistsWithOrphans(t *testing.T) {
	cfg := `[{"label":"Doing","status_names":["In Progress"]}]`

	noOrphan := BuildBoard(cfg, []jira.Issue{testIssue("V-1", "In Progress", "indeterminate")}, 0, "", time.Now())
	if len(noOrphan) != 1 {
		t.Fatalf("a board with no orphan must not gain an empty 'Others' column: %d columns", len(noOrphan))
	}

	withOrphan := BuildBoard(cfg, []jira.Issue{testIssue("V-2", "Blocked", "indeterminate")}, 0, "", time.Now())
	if len(withOrphan) != 2 || !withOrphan[1].Fallback {
		t.Fatalf("an issue whose status is outside the columns had to appear in 'Others': %+v", withOrphan)
	}
	if withOrphan[1].Cards[0].Key != "V-2" {
		t.Errorf("the wrong orphan went to 'Others': %+v", withOrphan[1].Cards)
	}
}

func TestInvalidConfigFallsBackToDefaultInsteadOfBreaking(t *testing.T) {
	// A crooked preference in the vault must not keep the board from opening.
	for _, bad := range []string{"this is not json", "[]", "{}", "   "} {
		cols := BuildBoard(bad, []jira.Issue{testIssue("V-1", "Backlog", "new")}, 0, "", time.Now())
		if len(cols) != 3 {
			t.Errorf("config %q had to fall back to the 3 default columns, got %d", bad, len(cols))
		}
	}
}

func TestOldDoneIssuesDisappearButOnlyDoneOnes(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour).Format("2006-01-02T15:04:05.000-0700")
	recent := now.Add(-2 * 24 * time.Hour).Format("2006-01-02T15:04:05.000-0700")

	oldDone := testIssue("V-1", "Ready", "done")
	oldDone.Updated = old
	recentDone := testIssue("V-2", "Ready", "done")
	recentDone.Updated = recent
	oldOpen := testIssue("V-3", "Backlog", "new")
	oldOpen.Updated = old

	cols := BuildBoard("", []jira.Issue{oldDone, recentDone, oldOpen}, 30, "", now)

	if len(cols[0].Cards) != 1 || cols[0].Cards[0].Key != "V-3" {
		t.Errorf("an OPEN issue must not disappear by age, only completed ones can: %+v", cols[0].Cards)
	}
	if len(cols[2].Cards) != 1 || cols[2].Cards[0].Key != "V-2" {
		t.Errorf("only the recently completed one should remain: %+v", cols[2].Cards)
	}
}

func TestIssueWithoutReadableDateNeverAgesOut(t *testing.T) {
	// Losing work over a date-formatting detail would be the worst possible
	// outcome of a cosmetic preference.
	noDate := testIssue("V-1", "Ready", "done")
	noDate.Updated = "yesterday afternoon"

	cols := BuildBoard("", []jira.Issue{noDate}, 1, "", time.Now())
	if len(cols[2].Cards) != 1 {
		t.Fatalf("an issue with an unreadable date had to stay visible: %+v", cols[2])
	}
}

func TestSortByKeyIsNumericNotAlphabetic(t *testing.T) {
	issues := []jira.Issue{
		testIssue("TASK-9", "Backlog", "new"),
		testIssue("TASK-100", "Backlog", "new"),
		testIssue("TASK-10", "Backlog", "new"),
	}
	cols := BuildBoard("", issues, 0, "key:asc", time.Now())

	got := []string{cols[0].Cards[0].Key, cols[0].Cards[1].Key, cols[0].Cards[2].Key}
	want := []string{"TASK-9", "TASK-10", "TASK-100"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordering by key must be numeric: %v (wanted %v)", got, want)
		}
	}
}

func TestUnknownOrderKeepsJQLOrder(t *testing.T) {
	// A newer client may ask for a criterion this server does not know yet.
	// Degrading to the JQL's order is correct; an error is not.
	issues := []jira.Issue{
		testIssue("V-3", "Backlog", "new"),
		testIssue("V-1", "Backlog", "new"),
	}
	cols := BuildBoard("", issues, 0, "weighted_priority:desc", time.Now())
	if cols[0].Cards[0].Key != "V-3" {
		t.Fatalf("an unknown order should be a no-op: %+v", cols[0].Cards)
	}
}

func TestTransitionToColumnByCategory(t *testing.T) {
	col := JiraBoardColumn{Label: "Done", Category: "done"}
	trs := []jira.Transition{
		{ID: "11", Name: "Start", ToName: "In Progress", ToCat: "indeterminate"},
		{ID: "31", Name: "Finish", ToName: "Ready", ToCat: "done"},
	}
	tr := TransitionToColumn(col, trs)
	if tr == nil || tr.ID != "31" {
		t.Fatalf("should pick the transition that lands in 'done': %+v", tr)
	}
}

func TestTransitionToColumnByNameRespectsOperatorOrder(t *testing.T) {
	// The operator wrote "In Progress" first: that is their preference.
	col := JiraBoardColumn{Label: "Doing", StatusNames: []string{"In Progress", "IN REVIEW"}}
	trs := []jira.Transition{
		{ID: "21", Name: "Review", ToName: "IN REVIEW", ToCat: "indeterminate"},
		{ID: "11", Name: "Start", ToName: "in progress", ToCat: "indeterminate"},
	}
	tr := TransitionToColumn(col, trs)
	if tr == nil || tr.ID != "11" {
		t.Fatalf("should prefer the column's first name: %+v", tr)
	}
}

func TestNoTransitionToColumnReturnsNil(t *testing.T) {
	// The project's workflow forbids the jump. This is NOT an error — it is the
	// answer that sends the card back to its original column with a reason.
	col := JiraBoardColumn{Label: "Done", Category: "done"}
	trs := []jira.Transition{{ID: "11", ToName: "In Progress", ToCat: "indeterminate"}}
	if tr := TransitionToColumn(col, trs); tr != nil {
		t.Fatalf("there is no transition to 'done'; should be nil, got %+v", tr)
	}
}

func TestIssueAlreadyInColumnIsRecognized(t *testing.T) {
	// The same function the board draws with. If they diverged, dragging a card
	// onto the column it is already in would turn into a real transition.
	col := JiraBoardColumn{Label: "In Progress", Category: "indeterminate"}
	if !issueInColumn(testIssue("V-1", "IN REVIEW", "indeterminate"), col) {
		t.Error("the issue is already in this column and that has to be recognized")
	}
	if issueInColumn(testIssue("V-2", "Backlog", "new"), col) {
		t.Error("an issue from another category must not count as already in the column")
	}
}

func TestFilterJQLMirrorsWebPanel(t *testing.T) {
	cases := []struct {
		filter, project, custom, want string
	}{
		{"all", "PANEL", "", "project = PANEL ORDER BY updated DESC"},
		{"all", "", "", "ORDER BY updated DESC"},
		{"mine", "PANEL", "", "project = PANEL AND assignee = currentUser() AND statusCategory != Done ORDER BY rank ASC"},
		{"reported", "", "", "reporter = currentUser() ORDER BY updated DESC"},
		{"custom", "PANEL", "labels = urgent", "labels = urgent"},
		// A filter this server does not know falls back to "all" — the app may
		// be newer than the server.
		{"made-up", "PANEL", "", "project = PANEL ORDER BY updated DESC"},
	}
	for _, c := range cases {
		if got := FilterJQL(c.filter, c.project, c.custom, ""); got != c.want {
			t.Errorf("filter %q/project %q: %q (want %q)", c.filter, c.project, got, c.want)
		}
	}
}

func TestJQLNeverHasDanglingAND(t *testing.T) {
	// The bug the web panel patches with a regex after assembling. Here the
	// assembly is born right.
	for _, f := range []string{"all", "mine", "todo", "inprogress", "last7", "reported", "other"} {
		for _, p := range []string{"", "PANEL"} {
			jql := FilterJQL(f, p, "", "")
			if len(jql) > 0 && (containsSeq(jql, "AND ORDER") || hasPrefixSeq(jql, "AND ")) {
				t.Errorf("filter %q/project %q generated invalid JQL: %q", f, p, jql)
			}
		}
	}
}

func TestSearchLooksAtKeySummaryStatusLabelAndAssignee(t *testing.T) {
	withAssignee := testIssue("V-1", "Backlog", "new")
	withAssignee.Assignee = &jira.User{DisplayName: "Sam Rivera"}
	withLabel := testIssue("V-2", "Backlog", "new")
	withLabel.Labels = []string{"urgent"}
	issues := []jira.Issue{withAssignee, withLabel, testIssue("V-3", "Ready", "done")}

	if got := FilterBySearch(issues, "sam"); len(got) != 1 || got[0].Key != "V-1" {
		t.Errorf("search by assignee failed: %+v", got)
	}
	if got := FilterBySearch(issues, "URGENT"); len(got) != 1 || got[0].Key != "V-2" {
		t.Errorf("search by label must ignore case: %+v", got)
	}
	if got := FilterBySearch(issues, "ready"); len(got) != 1 || got[0].Key != "V-3" {
		t.Errorf("search by status failed: %+v", got)
	}
	if got := FilterBySearch(issues, ""); len(got) != 3 {
		t.Errorf("an empty search filters nothing: %+v", got)
	}
}

func TestCardCarriesPreformattedAssignee(t *testing.T) {
	// The client never builds a label out of a raw struct.
	is := testIssue("V-1", "Backlog", "new")
	is.Assignee = &jira.User{
		AccountID:   "acc-1",
		DisplayName: "Sam Rivera",
		AvatarURLs:  map[string]string{"48x48": "https://example/48.png"},
	}
	is.Priority = &jira.NamedRef{Name: "High"}
	is.IssueType = &jira.NamedRef{Name: "Bug"}

	c := boardCard(is)
	if c.Assignee != "Sam Rivera" || c.AssigneeID != "acc-1" {
		t.Errorf("assignee did not come formatted: %+v", c)
	}
	if c.AvatarURL != "https://example/48.png" {
		t.Errorf("avatar should be the largest one available: %q", c.AvatarURL)
	}
	if c.Priority != "High" || c.Type != "Bug" {
		t.Errorf("priority/type did not come through: %+v", c)
	}
}

func TestEmptyColumnHasEmptyListNeverNull(t *testing.T) {
	cols := BuildBoard("", nil, 0, "", time.Now())
	for _, c := range cols {
		if c.Cards == nil {
			t.Fatalf("column %q came with Cards nil — the client distinguishes empty from absent BY THE COLUMN", c.Label)
		}
	}
}

func containsSeq(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func hasPrefixSeq(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

// --- one search per column ----------------------------------------------------

func TestSplitJQLSeparatesWhereFromOrder(t *testing.T) {
	// The column restriction goes in BEFORE the ORDER BY. Concatenating without
	// splitting produces "... ORDER BY updated DESC AND status = X", which is invalid.
	cases := []struct{ jql, where, order string }{
		{"project = PANEL ORDER BY updated DESC", "project = PANEL", "ORDER BY updated DESC"},
		{"project = PANEL order by rank ASC", "project = PANEL", "order by rank ASC"},
		{"ORDER BY updated DESC", "", "ORDER BY updated DESC"},
		{"project = PANEL", "project = PANEL", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		where, order := SplitJQL(c.jql)
		if where != c.where || order != c.order {
			t.Errorf("SplitJQL(%q) = (%q, %q); want (%q, %q)", c.jql, where, order, c.where, c.order)
		}
	}
}

func TestCategoryRestrictionUsesJQLVocabulary(t *testing.T) {
	// The stable key is "new"; JQL wants "To Do". Sending the raw key would bring
	// back zero issues in silence — the worst possible failure on a board.
	cases := map[string]string{
		"new":           `statusCategory = "To Do"`,
		"indeterminate": `statusCategory = "In Progress"`,
		"done":          `statusCategory = "Done"`,
	}
	for key, want := range cases {
		got := ColumnRestriction(JiraBoardColumn{Category: key}, nil)
		if got != want {
			t.Errorf("category %q: %q (want %q)", key, got, want)
		}
	}
}

func TestNameRestrictionListsColumnStatuses(t *testing.T) {
	col := JiraBoardColumn{Label: "Doing", StatusNames: []string{"In Progress", "IN REVIEW"}}
	got := ColumnRestriction(col, nil)
	want := `status IN ("In Progress", "IN REVIEW")`
	if got != want {
		t.Errorf("%q (want %q)", got, want)
	}
}

func TestOtherColumnQueriesTheCOMPLEMENT(t *testing.T) {
	// "Whatever is none of the others" is only askable of Jira as NOT IN.
	all := []JiraBoardColumn{
		{Label: "A", StatusNames: []string{"Backlog"}},
		{Label: "B", StatusNames: []string{"Ready"}},
		{Label: "Others", Fallback: true},
	}
	got := ColumnRestriction(all[2], all)
	want := `status NOT IN ("Backlog", "Ready")`
	if got != want {
		t.Errorf("%q (want %q)", got, want)
	}
}

func TestUnrestrictableColumnReturnsEmpty(t *testing.T) {
	// The caller uses this to fall back to the single search, instead of building
	// a crooked query that would bring the column back empty with no explanation.
	if got := ColumnRestriction(JiraBoardColumn{Label: "?"}, nil); got != "" {
		t.Errorf("a column with neither category nor names should return empty, got %q", got)
	}
	if got := ColumnRestriction(JiraBoardColumn{Category: "made-up"}, nil); got != "" {
		t.Errorf("an unknown category should return empty, got %q", got)
	}
}

func TestColumnJQLWrapsFilterInPARENTHESES(t *testing.T) {
	// Without the parentheses, a filter with an OR would bind only to the last
	// term and the board would bring back more than it should — silent widening.
	got := ColumnJQL(`project = PANEL OR project = TTW`, "ORDER BY updated DESC", `statusCategory = "To Do"`)
	want := `(project = PANEL OR project = TTW) AND statusCategory = "To Do" ORDER BY updated DESC`
	if got != want {
		t.Errorf("%q\nwant %q", got, want)
	}
}

func TestColumnJQLWithoutFilterNeverStartsWithAND(t *testing.T) {
	got := ColumnJQL("", "ORDER BY updated DESC", `statusCategory = "Done"`)
	want := `statusCategory = "Done" ORDER BY updated DESC`
	if got != want {
		t.Errorf("%q (want %q)", got, want)
	}
	if hasPrefixSeq(got, "AND") {
		t.Error("JQL must not start with AND")
	}
}

func TestQuotedStatusNameDoesNotBreakWHOLEQuery(t *testing.T) {
	col := JiraBoardColumn{StatusNames: []string{`In "review"`}}
	got := ColumnRestriction(col, nil)
	if !containsSeq(got, `\"`) {
		t.Errorf("the inner quotes must come out escaped: %q", got)
	}
}

// --- the operator's own board filter ------------------------------------------

func TestOwnBoardOnlyAppearsByChoice(t *testing.T) {
	// A configured board_jql must never replace the "All" filter.
	if got := FilterJQL("all", "PANEL", "", "project = OTHER"); got != "project = PANEL ORDER BY updated DESC" {
		t.Errorf("the operator's JQL must not hijack the 'All' filter: %q", got)
	}
	if got := FilterJQL("board", "PANEL", "", "project = OTHER"); got != "project = OTHER" {
		t.Errorf("chosen on purpose, it counts: %q", got)
	}
}

func TestOwnBoardWithoutQueryFallsBackToAll(t *testing.T) {
	// A filter with no query behind it would be a button that does nothing.
	if got := FilterJQL("board", "PANEL", "", ""); got != "project = PANEL ORDER BY updated DESC" {
		t.Errorf("%q", got)
	}
}

func TestMyBoardOnlyOFFEREDWhenItExists(t *testing.T) {
	without := BoardFilters(false)
	for _, f := range without {
		if f.Key == "board" {
			t.Fatal("with no board_jql configured, 'My board' must not appear")
		}
	}
	with := BoardFilters(true)
	if with[len(with)-1].Key != "board" {
		t.Fatalf("with board_jql, 'My board' goes at the end: %+v", with)
	}
	if len(with) != len(without)+1 {
		t.Fatalf("only one filter more: %d vs %d", len(with), len(without))
	}
}
