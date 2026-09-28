package mobilebff

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"server-control-panel/internal/jira"
)

type JiraBoardCard struct {
	Key        string   `json:"key"`
	Summary    string   `json:"summary"`
	Status     string   `json:"status"`
	Category   string   `json:"category" doc:"new | indeterminate | done: the STABLE category identifier"`
	Type       string   `json:"type,omitempty"`
	Priority   string   `json:"priority,omitempty"`
	Assignee   string   `json:"assignee,omitempty"`
	AssigneeID string   `json:"assignee_id,omitempty"`
	AvatarURL  string   `json:"avatar_url,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Updated    string   `json:"updated,omitempty"`
	DueDate    string   `json:"due_date,omitempty"`
}

type JiraBoardColumn struct {
	Label       string          `json:"label"`
	StatusNames []string        `json:"status_names,omitempty"`
	Category    string          `json:"category,omitempty"`
	Fallback    bool            `json:"fallback,omitempty"`
	Cards       []JiraBoardCard `json:"cards" required:"true"`
}

func defaultColumns() []JiraBoardColumn {
	return []JiraBoardColumn{
		{Label: "To Do", Category: "new"},
		{Label: "In Progress", Category: "indeterminate"},
		{Label: "Done", Category: "done"},
	}
}

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
			label = "Column " + strconv.Itoa(i+1)
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

	if byName && len(orphans) > 0 {
		leftover := JiraBoardColumn{Label: "Others", Fallback: true}
		for _, is := range orphans {
			leftover.Cards = append(leftover.Cards, boardCard(is))
		}
		columns = append(columns, leftover)
	}

	for i := range columns {
		if columns[i].Cards == nil {
			columns[i].Cards = []JiraBoardCard{}
		}
		sortCards(columns[i].Cards, order)
	}
	return columns
}

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

func keyNumber(key string) int {
	i := strings.LastIndex(key, "-")
	if i < 0 {
		return 0
	}
	n, _ := strconv.Atoi(key[i+1:])
	return n
}

func TransitionToColumn(col JiraBoardColumn, transitions []jira.Transition) *jira.Transition {
	if col.Category != "" {
		for i := range transitions {
			if transitions[i].ToCat == col.Category {
				return &transitions[i]
			}
		}
		return nil
	}
	for _, want := range col.StatusNames {
		for i := range transitions {
			if strings.EqualFold(transitions[i].ToName, want) {
				return &transitions[i]
			}
		}
	}
	return nil
}

var quickFilters = []struct {
	Key   string
	Label string
}{
	{"all", "All"},
	{"mine", "Mine"},
	{"todo", "To Do"},
	{"inprogress", "In Progress"},
	{"last7", "Last 7 days"},
	{"reported", "Reported by me"},
	{"custom", "JQL"},
}

var ownBoardFilter = JiraFilterOption{Key: "board", Label: "My board"}

type JiraFilterOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

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

func FilterJQL(filter, project, jqlCustom, boardJQL string) string {
	project = strings.TrimSpace(project)
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
	default:
		if project == "" {
			return "ORDER BY updated DESC"
		}
		return "project = " + project + " ORDER BY updated DESC"
	}
}

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

func SplitJQL(jql string) (where, order string) {
	cut := orderByIndex(jql)
	if cut < 0 {
		return strings.TrimSpace(jql), ""
	}
	return strings.TrimSpace(jql[:cut]), strings.TrimSpace(jql[cut:])
}

func orderByIndex(jql string) int {
	high := strings.ToUpper(jql)
	for _, mark := range []string{"ORDER BY", "ORDER  BY"} {
		if i := strings.Index(high, mark); i >= 0 {
			return i
		}
	}
	return -1
}

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

func quoteJQL(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

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
