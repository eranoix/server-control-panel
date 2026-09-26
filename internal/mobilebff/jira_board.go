package mobilebff

// jira_board.go carries the PURE logic of the kanban board: how the columns
// are born out of the configuration, which column each issue falls into, which
// transition takes a card to the chosen column, and which JQL each quick filter
// means.
//
// # Why this logic belongs to the SERVER
//
// Today it exists once, in JavaScript, inside the web panel
// (`00-shell.js`: jiraColumns, jiraIssuesInCol, jiraDropOnCol,
// applyJiraFilter). Rewriting it in Kotlin would create a SECOND
// implementation of the same rule — and silent divergence between the web
// surface and the mobile one is exactly what killed the previous attempt. The
// app receives columns already built and cards already distributed; it decides
// how to draw, never what a column is.
//
// That is why everything here is a pure function over `[]jira.Issue` — no
// network, no vault, no huma. The whole file is testable with no server
// running, and it is where the rule the web panel and the app now SHARE lives.

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/jira"
)

// JiraBoardCard is an issue as it appears on a board card.
//
// Everything arrives already formatted as text: the client never builds a label
// out of a raw struct, the same rule every SDUI screen follows (see
// screens/misc.go, jiraIssueRow). An empty field means "Jira did not fill it
// in", never "the app could not read it".
type JiraBoardCard struct {
	Key        string   `json:"key"`
	Summary    string   `json:"summary"`
	Status     string   `json:"status"`
	Category   string   `json:"category" doc:"new | indeterminate | done — o identificador ESTÁVEL da categoria"`
	Type       string   `json:"type,omitempty"`
	Priority   string   `json:"priority,omitempty"`
	Assignee   string   `json:"assignee,omitempty"`
	AssigneeID string   `json:"assignee_id,omitempty"`
	AvatarURL  string   `json:"avatar_url,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Updated    string   `json:"updated,omitempty"`
	DueDate    string   `json:"due_date,omitempty"`
}

// JiraBoardColumn is one column of the board, with the cards that fall into it.
//
// [StatusNames] and [Category] are MUTUALLY exclusive and exist so the client
// can hand the column back on a move call without inventing vocabulary: it
// sends the [Label] back, and the server finds the column again. [Fallback]
// marks the "Outros" column — the one that collects issues whose status matches
// no configured column.
type JiraBoardColumn struct {
	Label       string          `json:"label"`
	StatusNames []string        `json:"status_names,omitempty"`
	Category    string          `json:"category,omitempty"`
	Fallback    bool            `json:"fallback,omitempty"`
	Cards       []JiraBoardCard `json:"cards" required:"true"`
}

// defaultColumns mirrors the three CATEGORY columns the web panel uses when the
// operator has not configured columns of their own.
//
// Category and not name: a status name is translatable and customizable per
// project; `statusCategory.key` is one of the three values Jira guarantees. A
// column by name would break in any project that calls "To Do" "Backlog" —
// which is this very project's case.
func defaultColumns() []JiraBoardColumn {
	return []JiraBoardColumn{
		{Label: "A fazer", Category: "new"},
		{Label: "Em andamento", Category: "indeterminate"},
		{Label: "Concluído", Category: "done"},
	}
}

// configuredColumns reads the `jira_board_columns` JSON out of the vault.
//
// It returns nil when there is no configuration, when the JSON fails to
// deserialize, or when the list is empty — in all of those cases the caller
// falls back to the category columns. An invalid configuration is NOT a visible
// error: the board keeps working with the defaults, exactly as the web panel
// does (`catch(e){ console.warn(...) }`). A board that refuses to open because
// a preference is crooked is worse than a board with the default columns.
func configuredColumns(raw string) []JiraBoardColumn {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var reads []jira.BoardColumn
	if err := json.Unmarshal([]byte(raw), &reads); err != nil || len(reads) == 0 {
		return nil
	}
	out := make([]JiraBoardColumn, 0, len(reads))
	for i, c := range reads {
		label := strings.TrimSpace(c.Label)
		if label == "" {
			label = "Coluna " + strconv.Itoa(i+1)
		}
		names := make([]string, 0, len(c.StatusNames))
		for _, n := range c.StatusNames {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
		out = append(out, JiraBoardColumn{Label: label, StatusNames: names})
	}
	return out
}

// BuildBoard distributes the issues across the columns.
//
// [hideDoneAfter], when greater than zero, hides issues that have been
// done for more days than that — the same retention as the panel
// (`jiraIssueVisible`). It keeps the "Concluído" column from becoming a
// two-year-old morgue your thumb never finishes scrolling through.
//
// [ordem] is "field:direction" (`updated:desc`, `key:asc`, `name:asc`,
// `type:asc`); empty or unknown preserves the order Jira returned, which is
// already the JQL's.
func BuildBoard(
	rawColumns string,
	issues []jira.Issue,
	hideDoneAfter int,
	order string,
	now time.Time,
) []JiraBoardColumn {
	columns := configuredColumns(rawColumns)
	byName := columns != nil
	if !byName {
		columns = defaultColumns()
	}

	var orphans []jira.Issue
	for _, is := range issues {
		if !issueVisible(is, hideDoneAfter, now) {
			continue
		}
		dest := -1
		for i := range columns {
			if issueInColumn(is, columns[i]) {
				dest = i
				break
			}
		}
		if dest < 0 {
			orphans = append(orphans, is)
			continue
		}
		columns[dest].Cards = append(columns[dest].Cards, boardCard(is))
	}

	// The "Outros" column only exists when there is an orphan. A well-configured
	// board does not earn a permanent empty column just to prove it is complete.
	if byName && len(orphans) > 0 {
		leftover := JiraBoardColumn{Label: "Outros", Fallback: true}
		for _, is := range orphans {
			leftover.Cards = append(leftover.Cards, boardCard(is))
		}
		columns = append(columns, leftover)
	}

	for i := range columns {
		// Cards is never nil in the response: the client tells "empty column"
		// from "column that did not come" by the column's presence, not by null.
		if columns[i].Cards == nil {
			columns[i].Cards = []JiraBoardCard{}
		}
		sortCards(columns[i].Cards, order)
	}
	return columns
}

// issueInColumn is the SAME question the move screen asks later, and that is
// why it is a single function: if it said "yes, it is already in this column"
// by one criterion and the board drew by another, dragging a card onto the
// column it is already in would turn into a real transition.
func issueInColumn(is jira.Issue, col JiraBoardColumn) bool {
	if col.Fallback {
		return false
	}
	if col.Category != "" {
		return is.Status.StatusCategory.Key == col.Category
	}
	name := strings.ToLower(strings.TrimSpace(is.Status.Name))
	for _, n := range col.StatusNames {
		if strings.ToLower(n) == name {
			return true
		}
	}
	return false
}

// issueVisible applies the done-issue retention.
//
// An issue with an unreadable date is always visible: disappearing because a
// timestamp could not be parsed is losing work over a formatting detail.
func issueVisible(is jira.Issue, hideDoneAfter int, now time.Time) bool {
	if hideDoneAfter <= 0 {
		return true
	}
	if is.Status.StatusCategory.Key != "done" {
		return true
	}
	when := firstNonEmpty(is.Updated, is.Created)
	t, ok := parseJiraTime(when)
	if !ok {
		return true
	}
	return now.Sub(t) <= time.Duration(hideDoneAfter)*24*time.Hour
}

func boardCard(is jira.Issue) JiraBoardCard {
	c := JiraBoardCard{
		Key:      is.Key,
		Summary:  is.Summary,
		Status:   is.Status.Name,
		Category: is.Status.StatusCategory.Key,
		Labels:   is.Labels,
		Updated:  is.Updated,
		DueDate:  is.DueDate,
	}
	if is.IssueType != nil {
		c.Type = is.IssueType.Name
	}
	if is.Priority != nil {
		c.Priority = is.Priority.Name
	}
	if is.Assignee != nil {
		c.Assignee = is.Assignee.DisplayName
		c.AssigneeID = is.Assignee.AccountID
		c.AvatarURL = is.Assignee.AvatarURL()
	}
	return c
}

// sortCards sorts in place. An unknown order is a deliberate no-op — the
// client may be newer than the server and ask for a criterion the latter does
// not know yet; returning the JQL's order is correct degradation, not an error.
func sortCards(cards []JiraBoardCard, order string) {
	field, desc := splitOrder(order)
	if field == "" {
		return
	}
	less := func(a, b JiraBoardCard) bool {
		switch field {
		case "name":
			return strings.ToLower(a.Summary) < strings.ToLower(b.Summary)
		case "key":
			return keyNumber(a.Key) < keyNumber(b.Key)
		case "type":
			return strings.ToLower(a.Type) < strings.ToLower(b.Type)
		case "updated":
			ta, _ := parseJiraTime(a.Updated)
			tb, _ := parseJiraTime(b.Updated)
			return ta.Before(tb)
		}
		return false
	}
	sort.SliceStable(cards, func(i, j int) bool {
		if desc {
			return less(cards[j], cards[i])
		}
		return less(cards[i], cards[j])
	})
}

func splitOrder(order string) (field string, desc bool) {
	order = strings.ToLower(strings.TrimSpace(order))
	if order == "" || order == "none" {
		return "", false
	}
	parts := strings.SplitN(order, ":", 2)
	field = parts[0]
	switch field {
	case "name", "key", "type", "updated":
	default:
		return "", false
	}
	return field, len(parts) == 2 && parts[1] == "desc"
}

// keyNumber extracts the numeric tail of an issue key. Sorting a key as text
// would put ...-100 before ...-99, which is the wrong order in any project that
// gets past two digits.
func keyNumber(key string) int {
	i := strings.LastIndex(key, "-")
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(key[i+1:])
	return n
}

// TransitionToColumn finds the transition that takes the issue to the column.
//
// It returns nil when NO transition gets there. That is not a failure of the app
// nor of the server: it is the project's workflow forbidding that jump (you do
// not go from "A fazer" straight to "Concluído" in a workflow with mandatory
// review). The caller turns that nil into an explained refusal, and the card
// GOES BACK to where it was — dropping it and appearing to have moved is the
// worst possible outcome, because the person goes on believing they moved it.
func TransitionToColumn(col JiraBoardColumn, transitions []jira.Transition) *jira.Transition {
	if col.Category != "" {
		for i := range transitions {
			if transitions[i].ToCat == col.Category {
				return &transitions[i]
			}
		}
		return nil
	}
	// By name: try each of the column's names, in the order the operator wrote
	// them — the first is their preferred one.
	for _, want := range col.StatusNames {
		for i := range transitions {
			if strings.EqualFold(transitions[i].ToName, want) {
				return &transitions[i]
			}
		}
	}
	return nil
}

// The web panel's quick filters, in the same order and with the same meaning.
// The label travels with them so the app does not keep a second list that ages
// on its own.
var quickFilters = []struct {
	Key   string
	Label string
}{
	{"all", "Todas"},
	{"mine", "Mine"},
	{"todo", "A fazer"},
	{"inprogress", "Em andamento"},
	{"last7", "Last 7 days"},
	{"reported", "Reported by me"},
	{"custom", "JQL"},
}

// ownBoardFilter is the JQL the operator configured as THEIR board
// (`jira_board_jql` in the vault). It is only offered when it exists.
//
// It is a NAMED filter, and that is the fix for a real defect: before, a
// configured `board_jql` silently hijacked the "Todas" filter — but only on the
// FIRST load, because the condition that triggered it was "the client did not
// send a project", and the client only learns the project from the response.
// The same filter, with the same project, gave two different boards depending
// on whether the app already knew where it was. Since this operator's configured
// JQL returned zero issues, the board opened empty and filled up on refresh.
//
// Choosing explicitly is what makes the result explainable: an empty board under
// "My board" says the stored query matches nothing — and not that the app
// failed.
var ownBoardFilter = JiraFilterOption{Key: "board", Label: "My board"}

// JiraFilterOption is one quick filter offered to the app.
type JiraFilterOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// BoardFilters returns the catalogue of quick filters.
//
// [withOwnBoard] adds "My board" — only when the operator has in fact
// configured a board JQL. Offering a filter with no query behind it would be a
// button that does nothing.
func BoardFilters(withOwnBoard bool) []JiraFilterOption {
	out := make([]JiraFilterOption, 0, len(quickFilters)+1)
	for _, f := range quickFilters {
		out = append(out, JiraFilterOption{Key: f.Key, Label: f.Label})
	}
	if withOwnBoard {
		out = append(out, ownBoardFilter)
	}
	return out
}

// FilterJQL translates a quick filter into JQL, mirroring the web panel's
// `applyJiraFilter` line by line.
//
// [jqlCustom] is only used by the "custom" filter and [boardJQL] only by the
// "board" one; in all the others both are ignored on purpose — a loose JQL
// travelling alongside a named filter would be a second source of truth about
// what the board is showing, and that is exactly how the board came to open
// empty and fill up on refresh.
func FilterJQL(filter, project, jqlCustom, boardJQL string) string {
	project = strings.TrimSpace(project)
	// "My board" with no stored query is not a state: it falls back to "all",
	// which is what a person would expect from a filter that filters nothing.
	if filter == ownBoardFilter.Key {
		if q := strings.TrimSpace(boardJQL); q != "" {
			return q
		}
		filter = "all"
	}
	where := ""
	if project != "" {
		where = "project = " + project + " AND "
	}
	switch filter {
	case "mine":
		return where + "assignee = currentUser() AND statusCategory != Done ORDER BY rank ASC"
	case "todo":
		return where + `statusCategory = "To Do" ORDER BY rank ASC`
	case "inprogress":
		return where + `statusCategory = "In Progress" ORDER BY updated DESC`
	case "last7":
		return where + "updated >= -7d ORDER BY updated DESC"
	case "reported":
		return where + "reporter = currentUser() ORDER BY updated DESC"
	case "custom":
		return strings.TrimSpace(jqlCustom)
	default: // "all" and any filter this server does not know yet
		if project == "" {
			return "ORDER BY updated DESC"
		}
		// Without a clause after the AND, the AND dangles and Jira refuses.
		return "project = " + project + " ORDER BY updated DESC"
	}
}

// FilterBySearch applies the free-text search over whatever the JQL already
// brought back.
//
// It is a TEXT filter over the page in hand, not a second query: the web panel
// does the same (`jiraFilteredIssues`). The difference matters to whoever reads
// the result — "not found" here means "not in this search", not "not in Jira".
func FilterBySearch(issues []jira.Issue, search string) []jira.Issue {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return issues
	}
	out := make([]jira.Issue, 0, len(issues))
	for _, is := range issues {
		fields := []string{is.Key, is.Summary, is.Status.Name, strings.Join(is.Labels, " ")}
		if is.Assignee != nil {
			fields = append(fields, is.Assignee.DisplayName)
		}
		if strings.Contains(strings.ToLower(strings.Join(fields, " ")), search) {
			out = append(out, is)
		}
	}
	return out
}

// parseJiraTime reads Jira's timestamp, which arrives as RFC3339 with a
// colon-less zone offset ("2026-09-09T12:00:00.000-0300") — a format
// time.RFC3339 alone does not accept.
func parseJiraTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000-0700",
		"2006-01-02T15:04:05-0700",
		time.RFC3339,
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// --- one search PER COLUMN -----------------------------------------------------
//
// # The defect this fixes
//
// The board did ONE search of a hundred issues ordered by update time and
// distributed the result across the columns. In a project with ninety-nine done
// issues, the done ones ate the whole quota and the left-hand columns starved:
// the same project showed "A fazer 2" under the "A fazer" filter and
// "A fazer 1" under the "Todas" filter. A column lost a card because of another
// column.
//
// That is not tunable with a bigger ceiling — it is the shape of the query that
// is wrong. A board has a quota PER COLUMN, because that is how it is read:
// nobody scrolls four hundred done issues to find out what is still to do.

// SplitJQL separates the search clause from the ordering one.
//
// Necessary because the column restriction goes in BEFORE the `ORDER BY` — a
// `... ORDER BY updated DESC AND status = "X"` is invalid syntax, and that is
// exactly the error naive concatenation produces.
func SplitJQL(jql string) (where, order string) {
	cut := orderByIndex(jql)
	if cut < 0 {
		return strings.TrimSpace(jql), ""
	}
	return strings.TrimSpace(jql[:cut]), strings.TrimSpace(jql[cut:])
}

// orderByIndex finds the top-level "ORDER BY", ignoring case. It does not try
// to understand parentheses: JQL allows no subquery with an ORDER BY inside, so
// the first occurrence is always the trailing one.
func orderByIndex(jql string) int {
	high := strings.ToUpper(jql)
	for _, mark := range []string{"ORDER BY", "ORDER  BY"} {
		if i := strings.Index(high, mark); i >= 0 {
			return i
		}
	}
	return -1
}

// categoryJQL translates the category's stable key into the name JQL expects.
// These are the three values Jira guarantees; anything else returns "".
func categoryJQL(key string) string {
	switch key {
	case "new":
		return "To Do"
	case "indeterminate":
		return "In Progress"
	case "done":
		return "Done"
	}
	return ""
}

// ColumnRestriction is the slice of JQL that isolates one column's issues.
//
// The "Outros" column is the complement of the others (`status NOT IN (...)`) —
// the only way to ASK Jira for something defined as "whatever is none of the
// rest". It returns "" when the column has no way to restrict itself, and in
// that case the caller falls back to the single search.
func ColumnRestriction(col JiraBoardColumn, all []JiraBoardColumn) string {
	if col.Fallback {
		var names []string
		for _, c := range all {
			for _, n := range c.StatusNames {
				names = append(names, quoteJQL(n))
			}
		}
		if len(names) == 0 {
			return ""
		}
		return "status NOT IN (" + strings.Join(names, ", ") + ")"
	}
	if col.Category != "" {
		name := categoryJQL(col.Category)
		if name == "" {
			return ""
		}
		return `statusCategory = ` + quoteJQL(name)
	}
	if len(col.StatusNames) == 0 {
		return ""
	}
	names := make([]string, 0, len(col.StatusNames))
	for _, n := range col.StatusNames {
		names = append(names, quoteJQL(n))
	}
	return "status IN (" + strings.Join(names, ", ") + ")"
}

// quoteJQL wraps the value in double quotes, escaping any it contains. A status
// name with quotes in it is rare; a name with UNESCAPED quotes would break the
// board's entire query, and not just that column.
func quoteJQL(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

// ColumnJQL composes one column's query.
//
// The filter's clause goes inside parentheses because it may contain an `OR` —
// and without the parentheses the column's `AND` would bind only to the last
// term, silently widening the result.
func ColumnJQL(where, order, restriction string) string {
	parts := make([]string, 0, 2)
	if where = strings.TrimSpace(where); where != "" {
		parts = append(parts, "("+where+")")
	}
	if restriction = strings.TrimSpace(restriction); restriction != "" {
		parts = append(parts, restriction)
	}
	query := strings.Join(parts, " AND ")
	if order = strings.TrimSpace(order); order != "" {
		if query == "" {
			return order
		}
		return query + " " + order
	}
	return query
}
