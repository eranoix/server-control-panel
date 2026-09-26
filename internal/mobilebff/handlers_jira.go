package mobilebff

// handlers_jira.go is the Jira kanban board for the app — the same operation
// the web panel offers, with the decisions taken on this side.
//
// # What changed relative to what existed
//
// Before, Jira reached the app through ONE SDUI screen (`jira.issues`, in
// screens/misc.go): a table of issues, a detail, and two forms (move and
// comment). That file's comment said, in so many words, that "the kanban
// board stays permanently desktop-only". The opposite was decided: the board
// is how the work is read, and without it Jira on a phone is a list of keys
// with no sense of progress.
//
// # Why here and not as an eighth SDUI type
//
// The SDUI vocabulary is closed at 7 types, and the `sdui` package comment
// answers exactly this situation: when a screen needs something none of the
// 7 expresses, that is a sign of a NATIVE module, never of an eighth type. A
// board with drag-and-drop, scrolling on two axes, multiple selection and
// undo is not describable in JSON — what IS describable (which columns exist,
// which cards land in each one) is DATA, and data already travels over an
// endpoint. That is what this file serves.
//
// # The rule lives once, and it lives here
//
// Column, card distribution, transition matching and filter-to-JQL
// translation live in jira_board.go, in pure, tested functions. The app
// receives the board ready-made. Reimplementing that in Kotlin would mean
// keeping two versions of the same rule — the silent divergence that killed
// the previous mobile attempt.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/jira"
)

func init() { Register("jira", registerJira) }

// --- responses ---------------------------------------------------------------

// JiraUserRef is a Jira person reduced to what a card shows.
type JiraUserRef struct {
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url,omitempty"`
}

// JiraProjectRef is a project in the picker's list.
type JiraProjectRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// JiraBoardResponse is the whole board in a single response.
//
// One call and not five: on a phone every trip to the server is a chance for a
// spinner. Projects, filters and the identity of whoever is looking change
// slowly, and they ride along with what changes fast because the cost of
// bringing them is negligible next to the cost of a second trip over a mobile
// network.
//
// [Connected] false is a NORMAL response (HTTP 200), not an error: someone who
// has not connected their Jira account yet needs to see the connection form,
// and a 4xx would make the app show "failed" for a situation that is only
// "not yet".
type JiraBoardResponse struct {
	Connected bool               `json:"connected"`
	Site      string             `json:"site,omitempty"`
	Project   string             `json:"project,omitempty"`
	Projects  []JiraProjectRef   `json:"projects,omitempty"`
	Me        *JiraUserRef       `json:"me,omitempty"`
	Filter    string             `json:"filter"`
	Filters   []JiraFilterOption `json:"filters" required:"true"`
	JQL       string             `json:"jql,omitempty"`
	Columns   []JiraBoardColumn  `json:"columns" required:"true"`
	Total     int                `json:"total"`
	// Error carries Jira's refusal (invalid JQL, expired token) WITHOUT
	// bringing the response down: the app still knows which project it is on,
	// what the filters are and who the user is, and shows the message in place
	// of the cards. A 502 would wipe the whole screen over a crooked JQL.
	Error string `json:"error,omitempty"`
}

type jiraBoardInput struct {
	Project string `query:"project" doc:"Chave do projeto; vazio usa o projeto configurado"`
	Filter  string `query:"filter" doc:"Filtro rápido: all, mine, todo, inprogress, last7, reported, custom" default:"all"`
	JQL     string `query:"jql" doc:"JQL próprio — só vale com filter=custom"`
	Search  string `query:"search" doc:"Busca livre sobre o que o JQL trouxe"`
	Sort    string `query:"sort" doc:"campo:direção — updated:desc, key:asc, name:asc, type:asc"`
	// The default is 0 (show everything) because hiding work without being
	// asked is worse than a long column.
	HideDoneDays int `query:"hide_done_days" doc:"Esconde concluídas há mais de N dias; 0 mostra todas"`
	Max          int `query:"max" doc:"Teto de issues buscadas" default:"100"`
}

type jiraBoardOutput struct {
	Body JiraBoardResponse
}

// JiraMoveRequest moves a card to a column.
//
// [Column] is the column's LABEL as it came in the board, not a transition id:
// whoever drags picks a column, and it is the server that knows which
// transition leads there. Sending the transition id from the client side would
// require it to fetch the transitions before every drag — a network trip in the
// middle of the gesture.
type JiraMoveRequest struct {
	Key    string `json:"key" doc:"Chave da issue (ex.: PROJ-42)" required:"true"`
	Column string `json:"column" doc:"Rótulo da coluna de destino" required:"true"`
}

// JiraMoveResponse reports what actually happened.
//
// [Status] and [Column] are the state AFTER, read from the applied transition —
// the app confirms with those instead of assuming the desired destination
// became reality.
type JiraMoveResponse struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Column string `json:"column"`
}

type jiraMoveInput struct {
	Body JiraMoveRequest
}

type jiraMoveOutput struct {
	Body JiraMoveResponse
}

// JiraIssueResponse is the opened issue: fields, comments, and where it can go
// from where it is.
//
// The transitions come along because they are the answer to the question the
// card's menu asks ("move to…"), and that menu is the ACCESSIBLE way to move —
// a screen reader does not drag. Fetching them in a second call would leave the
// menu empty the instant it opens.
type JiraIssueResponse struct {
	Key         string             `json:"key"`
	Summary     string             `json:"summary"`
	Description string             `json:"description,omitempty"`
	Status      string             `json:"status"`
	Category    string             `json:"category"`
	Column      string             `json:"column,omitempty"`
	Type        string             `json:"type,omitempty"`
	Priority    string             `json:"priority,omitempty"`
	Assignee    *JiraUserRef       `json:"assignee,omitempty"`
	Reporter    *JiraUserRef       `json:"reporter,omitempty"`
	Labels      []string           `json:"labels,omitempty"`
	Created     string             `json:"created,omitempty"`
	Updated     string             `json:"updated,omitempty"`
	DueDate     string             `json:"due_date,omitempty"`
	WebURL      string             `json:"web_url,omitempty"`
	Comments    []JiraCommentItem  `json:"comments,omitempty"`
	Moves       []JiraMoveOption   `json:"moves,omitempty"`
	Subtasks    []JiraBoardCard    `json:"subtasks,omitempty"`
	Links       []JiraIssueLinkRef `json:"links,omitempty"`
}

// JiraMoveOption is a possible destination, already translated into the
// vocabulary of the board's COLUMNS — not into the raw status name.
//
// Whoever looks at the board sees "Em andamento"; Jira calls that "EM REVISÃO".
// Offering the raw name in the menu would force the person to translate between
// two languages for the same thing in their head. [Status] stays available for
// anyone who wants to check the real name.
type JiraMoveOption struct {
	Column string `json:"column"`
	Status string `json:"status"`
	Name   string `json:"name,omitempty" doc:"Nome da transição no Jira (ex.: \"Iniciar revisão\")"`
}

// JiraCommentItem is a comment already flattened for reading.
type JiraCommentItem struct {
	ID      string `json:"id"`
	Body    string `json:"body"`
	Author  string `json:"author"`
	Created string `json:"created"`
}

// JiraIssueLinkRef is the other end of a link ("blocks", "is blocked by").
type JiraIssueLinkRef struct {
	Relation string `json:"relation"`
	Key      string `json:"key"`
	Summary  string `json:"summary,omitempty"`
	Status   string `json:"status,omitempty"`
}

type jiraIssueInput struct {
	Key string `query:"key" doc:"Chave da issue" required:"true"`
}

type jiraIssueOutput struct {
	Body JiraIssueResponse
}

// JiraCommentRequest posts a comment.
type JiraCommentRequest struct {
	Key  string `json:"key" required:"true"`
	Text string `json:"text" required:"true"`
}

type jiraCommentInput struct {
	Body JiraCommentRequest
}

type jiraCommentOutput struct {
	Body JiraCommentItem
}

// JiraAssignRequest changes the assignee.
//
// An empty [AccountID] UNASSIGNS — it is the same vocabulary as the Jira
// client's `UpdateIssue`, and having a separate field for "remove the assignee"
// would create two ways of saying the same thing.
type JiraAssignRequest struct {
	Key       string `json:"key" required:"true"`
	AccountID string `json:"account_id" doc:"accountId do responsável; vazio desatribui"`
}

type jiraAssignInput struct {
	Body JiraAssignRequest
}

// JiraUsersResponse is the set of people this issue can be assigned to.
type JiraUsersResponse struct {
	Users []JiraUserRef `json:"users" required:"true"`
}

type jiraUsersInput struct {
	Project string `query:"project" doc:"Chave do projeto" required:"true"`
	Query   string `query:"q" doc:"Filtro por nome/e-mail"`
}

type jiraUsersOutput struct {
	Body JiraUsersResponse
}

// JiraMetaResponse is what a creation form needs to offer instead of asking
// someone to type: the project's issue types and priorities.
//
// It exists because typing is the last resort — a hand-written issue type gets
// the accent wrong, the capitalisation wrong, and the name of something that
// project does not have.
type JiraMetaResponse struct {
	IssueTypes []string `json:"issue_types" required:"true"`
	Priorities []string `json:"priorities" required:"true"`
}

type jiraMetaInput struct {
	Project string `query:"project" doc:"Chave do projeto" required:"true"`
}

type jiraMetaOutput struct {
	Body JiraMetaResponse
}

// JiraCreateRequest creates an issue.
type JiraCreateRequest struct {
	Project     string   `json:"project" required:"true"`
	Type        string   `json:"type" required:"true"`
	Summary     string   `json:"summary" required:"true"`
	Description string   `json:"description,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	AssigneeID  string   `json:"assignee_id,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	DueDate     string   `json:"due_date,omitempty" doc:"AAAA-MM-DD"`
	ParentKey   string   `json:"parent_key,omitempty" doc:"Épico ou issue-mãe"`
}

type jiraCreateInput struct {
	Body JiraCreateRequest
}

// JiraCreatedResponse returns the new key.
type JiraCreatedResponse struct {
	Key string `json:"key"`
}

type jiraCreatedOutput struct {
	Body JiraCreatedResponse
}

// JiraBulkMoveRequest moves several issues to the same column.
type JiraBulkMoveRequest struct {
	Keys   []string `json:"issue_keys" required:"true"`
	Column string   `json:"column" required:"true"`
}

// JiraBulkAssignRequest assigns several issues to the same person.
type JiraBulkAssignRequest struct {
	Keys      []string `json:"issue_keys" required:"true"`
	AccountID string   `json:"account_id" doc:"accountId; vazio desatribui"`
}

// JiraBulkResult is the HONEST result of a bulk action.
//
// A batch is not atomic against Jira: every issue has its own workflow, and the
// third one can refuse what the first accepted. Returning "ok" for the whole
// batch would hide precisely the ones that did not go through — the only ones
// anybody needs to act on. That is why a failure comes with a KEY and a REASON,
// one by one.
type JiraBulkResult struct {
	Done   []string          `json:"done" required:"true"`
	Failed []JiraBulkFailure `json:"failed,omitempty"`
}

// JiraBulkFailure is an issue that did not go through, with the why.
type JiraBulkFailure struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

type jiraBulkMoveInput struct {
	Body JiraBulkMoveRequest
}

type jiraBulkAssignInput struct {
	Body JiraBulkAssignRequest
}

type jiraBulkOutput struct {
	Body JiraBulkResult
}

// JiraConnectRequest connects this user's Jira account.
type JiraConnectRequest struct {
	Site    string `json:"site" doc:"Subdomínio ou host (ex.: suaempresa ou suaempresa.atlassian.net)" required:"true"`
	Email   string `json:"email" required:"true"`
	Token   string `json:"token" doc:"Token de API do Atlassian" required:"true"`
	Project string `json:"project,omitempty" doc:"Projeto padrão (opcional)"`
}

type jiraConnectInput struct {
	Body JiraConnectRequest
}

// JiraProjectRequest pins the default project.
type JiraProjectRequest struct {
	Project string `json:"project" required:"true"`
}

type jiraProjectInput struct {
	Body JiraProjectRequest
}

// --- registration ------------------------------------------------------------

func registerJira(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "getJiraBoard",
		Method:      http.MethodGet,
		Path:        "/jira/board",
		Summary:     "O quadro kanban: colunas, cartões, filtros e projetos",
		Description: "Uma chamada devolve o quadro inteiro. `connected:false` é " +
			"resposta normal para quem ainda não ligou a conta — o aplicativo " +
			"mostra o formulário de conexão, não um erro.",
		Tags:   []string{"mobile", "jira"},
		Errors: []int{http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, jiraBoardHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "moveJiraIssue",
		Method:      http.MethodPost,
		Path:        "/jira/board/move",
		Summary:     "Move um cartão para uma coluna",
		Description: "Recebe o RÓTULO da coluna e resolve a transição do lado do " +
			"servidor. Responde 409 quando o fluxo de trabalho do projeto não " +
			"permite o salto — o cartão volta para a coluna de origem.",
		Tags:        []string{"mobile", "jira"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusConflict, http.StatusServiceUnavailable},
	}, jiraMoveHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "getJiraIssue",
		Method:      http.MethodGet,
		Path:        "/jira/issue",
		Summary:     "Uma issue com comentários e destinos possíveis",
		Tags:        []string{"mobile", "jira"},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraIssueHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "commentJiraIssue",
		Method:      http.MethodPost,
		Path:        "/jira/issue/comment",
		Summary:     "Comenta numa issue",
		Tags:        []string{"mobile", "jira"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraCommentHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "assignJiraIssue",
		Method:      http.MethodPost,
		Path:        "/jira/issue/assign",
		Summary:     "Troca o responsável por uma issue",
		Tags:        []string{"mobile", "jira"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraAssignHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "listJiraAssignableUsers",
		Method:      http.MethodGet,
		Path:        "/jira/users",
		Summary:     "A quem uma issue deste projeto pode ser atribuída",
		Description: "Existe para o aplicativo OFERECER a lista em vez de pedir " +
			"que se digite um accountId — que ninguém sabe de cabeça.",
		Tags:   []string{"mobile", "jira"},
		Errors: []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraUsersHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "getJiraMeta",
		Method:      http.MethodGet,
		Path:        "/jira/meta",
		Summary:     "Tipos de issue e prioridades do projeto",
		Tags:        []string{"mobile", "jira"},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraMetaHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "createJiraIssue",
		Method:      http.MethodPost,
		Path:        "/jira/issue/create",
		Summary:     "Cria uma issue",
		Tags:        []string{"mobile", "jira"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraCreateHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "bulkMoveJiraIssues",
		Method:      http.MethodPost,
		Path:        "/jira/bulk/move",
		Summary:     "Move várias issues para a mesma coluna",
		Description: "O resultado é por issue: o que foi e o que não foi, com o " +
			"motivo. Lote não é atômico contra o Jira.",
		Tags:        []string{"mobile", "jira"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraBulkMoveHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "bulkAssignJiraIssues",
		Method:      http.MethodPost,
		Path:        "/jira/bulk/assign",
		Summary:     "Atribui várias issues à mesma pessoa",
		Tags:        []string{"mobile", "jira"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraBulkAssignHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "connectJira",
		Method:      http.MethodPost,
		Path:        "/jira/connect",
		Summary:     "Liga a conta do Jira deste usuário",
		Description: "As credenciais vão para o cofre POR USUÁRIO, o mesmo que o " +
			"painel web usa. O token nunca volta em nenhuma resposta.",
		Tags:        []string{"mobile", "jira"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraConnectHandler(deps))

	huma.Register(api, huma.Operation{
		OperationID: "setJiraProject",
		Method:      http.MethodPost,
		Path:        "/jira/project",
		Summary:     "Fixa o projeto padrão do quadro",
		Tags:        []string{"mobile", "jira"},
		Middlewares: huma.Middlewares{comAuditoria},
		Errors:      []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusServiceUnavailable},
	}, jiraSetProjectHandler(deps))
}

// --- handlers ----------------------------------------------------------------

// jiraClient resolves the authenticated user's client.
//
// It distinguishes three situations that must not collapse into the same
// response: the BFF with no Jira configured (503 — a server problem), the user
// with no connected account (the caller handles it, because on the board screen
// that is the connection form) and a real credential error.
func jiraClient(ctx context.Context, deps Deps) (*jira.Client, string, error) {
	user := auth.UserFromContext(ctx)
	if user == "" {
		return nil, "", huma.Error401Unauthorized("unauthorized")
	}
	if deps.JiraFor == nil {
		return nil, user, huma.Error503ServiceUnavailable("the Jira integration is unavailable on this server")
	}
	cli, err := deps.JiraFor(user)
	if err != nil {
		return nil, user, err
	}
	return cli, user, nil
}

// notConnected tells whether the error is "this account has not connected Jira
// yet" — the normal situation for someone who never configured it, not a failure.
func notConnected(err error) bool { return errors.Is(err, jira.ErrNotConfigured) }

func jiraBoardHandler(deps Deps) func(context.Context, *jiraBoardInput) (*jiraBoardOutput, error) {
	return func(ctx context.Context, in *jiraBoardInput) (*jiraBoardOutput, error) {
		out := &jiraBoardOutput{}
		out.Body.Filters = BoardFilters(false)
		out.Body.Filter = validFilter(in.Filter, false)
		out.Body.Columns = []JiraBoardColumn{}

		cli, user, err := jiraClient(ctx, deps)
		if err != nil {
			if notConnected(err) {
				return out, nil // connected:false, HTTP 200
			}
			return nil, err
		}
		out.Body.Connected = true
		out.Body.Site = cli.Site()

		cfg := jira.Config{}
		if deps.JiraConfigFor != nil {
			cfg = deps.JiraConfigFor(user)
		}
		project := strings.TrimSpace(in.Project)
		if project == "" {
			project = cfg.ProjectKey
		}
		out.Body.Project = project

		if me, err := cli.Myself(ctx); err == nil && me != nil {
			out.Body.Me = &JiraUserRef{AccountID: me.AccountID, DisplayName: me.DisplayName}
		}
		if projs, err := cli.Projects(ctx); err == nil {
			for _, p := range projs {
				out.Body.Projects = append(out.Body.Projects, JiraProjectRef{Key: p.Key, Name: p.Name})
			}
		}

		// The operator's JQL becomes a NAMED FILTER, and never again a silent
		// detour from "Todas". The old condition (`in.Project == ""`) meant to say
		// "the operator picked no project", but in practice it said "the client has
		// not learned the project yet" — that is, first load. The same filter gave
		// two different boards depending on whether the app already knew where it
		// was, and with a board_jql that returns zero the board opened empty and
		// filled up on refresh.
		hasOwnBoard := strings.TrimSpace(cfg.BoardJQL) != ""
		out.Body.Filters = BoardFilters(hasOwnBoard)
		out.Body.Filter = validFilter(in.Filter, hasOwnBoard)

		jql := FilterJQL(out.Body.Filter, project, in.JQL, cfg.BoardJQL)
		out.Body.JQL = jql

		// The quota is PER COLUMN, and that is the fix: with a single search, a
		// project with ninety-nine done issues starved the left-hand columns — the
		// same column showed two cards under one filter and one under another.
		// Nobody scrolls through four hundred done issues to find out what is left
		// to do.
		perColumn := in.Max
		if perColumn <= 0 || perColumn > 100 {
			perColumn = 40
		}
		issues, rejection := searchByColumn(ctx, cli, cfg.BoardColumns, jql, perColumn)
		if rejection != "" && len(issues) == 0 {
			// A refusal from Jira does NOT wipe the screen: the app keeps project,
			// filters and user, and shows the reason in place of the cards.
			out.Body.Error = rejection
			out.Body.Columns = BuildBoard(cfg.BoardColumns, nil, 0, "", time.Now())
			return out, nil
		}
		// A column that failed on its own does not wipe the ones that came
		// through: the refusal shows on top of the board, with the cards we got.
		out.Body.Error = rejection
		issues = FilterBySearch(issues, in.Search)
		out.Body.Total = len(issues)
		out.Body.Columns = BuildBoard(cfg.BoardColumns, issues, in.HideDoneDays, in.Sort, time.Now())
		return out, nil
	}
}

// searchByColumn runs ONE query per column, in parallel, and returns the union.
//
// In parallel because these are three to six network calls that do not depend
// on one another; in series, the board would open in the sum of Jira's
// latencies.
//
// The union is deduplicated by key: an issue can match two columns when the
// operator configures overlapping names, and duplicating it would make the same
// card appear twice. Who decides where it lands is [BuildBoard] — the same
// function the rest of the file uses, and the first matching column wins.
//
// A column with no possible restriction (crooked configuration) falls back to
// the original query: better a board with the old quota than a column left
// empty with no explanation.
func searchByColumn(
	ctx context.Context,
	cli *jira.Client,
	rawColumns, jql string,
	perColumn int,
) ([]jira.Issue, string) {
	columns := BuildBoard(rawColumns, nil, 0, "", time.Now())
	where, order := SplitJQL(jql)

	queries := make([]string, 0, len(columns))
	for _, col := range columns {
		restriction := ColumnRestriction(col, columns)
		if restriction == "" {
			// No way to isolate this column: a single query, as before.
			queries = []string{jql}
			break
		}
		queries = append(queries, ColumnJQL(where, order, restriction))
	}
	if len(queries) == 0 {
		queries = []string{jql}
	}

	type result struct {
		issues []jira.Issue
		err    error
	}
	res := make([]result, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			issues, _, err := cli.Search(ctx, q, 0, perColumn)
			res[i] = result{issues: issues, err: err}
		}(i, q)
	}
	wg.Wait()

	vistas := map[string]bool{}
	union := make([]jira.Issue, 0, perColumn*len(queries))
	firstErr := ""
	for _, r := range res {
		if r.err != nil {
			if firstErr == "" {
				firstErr = r.err.Error()
			}
			continue
		}
		for _, is := range r.issues {
			if vistas[is.Key] {
				continue
			}
			vistas[is.Key] = true
			union = append(union, is)
		}
	}
	return union, firstErr
}

func validFilter(f string, withOwnBoard bool) string {
	f = strings.TrimSpace(strings.ToLower(f))
	for _, opt := range BoardFilters(withOwnBoard) {
		if opt.Key == f {
			return f
		}
	}
	return "all"
}

// columnByLabel finds again the column the app returned.
//
// The label is the key because the label is what travelled to the screen and
// back. A column that no longer exists (the operator reconfigured the board
// between the load and the drag) is a 400 naming it — better than moving to a
// similar-looking column and letting the person find out later.
func columnByLabel(rawColumns, label string) (JiraBoardColumn, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return JiraBoardColumn{}, huma.Error400BadRequest("column is required")
	}
	for _, c := range BuildBoard(rawColumns, nil, 0, "", time.Now()) {
		if strings.EqualFold(c.Label, label) {
			return c, nil
		}
	}
	return JiraBoardColumn{}, huma.Error400BadRequest(
		fmt.Sprintf("column %q does not exist on this board — it may have been reconfigured since the screen loaded", label))
}

func rawColumnsFor(deps Deps, user string) string {
	if deps.JiraConfigFor == nil {
		return ""
	}
	return deps.JiraConfigFor(user).BoardColumns
}

// moveOne is the core shared between dragging one card and the bulk action.
//
// It returns the resulting status. The error already reads as something written
// for a human: when there is no transition, it says where it IS possible to go
// from there, which is the only information capable of turning a refusal into a
// next step.
func moveOne(ctx context.Context, cli *jira.Client, col JiraBoardColumn, key string) (string, error) {
	trs, err := cli.Transitions(ctx, key)
	if err != nil {
		return "", err
	}
	tr := TransitionToColumn(col, trs)
	if tr == nil {
		// Before crying refusal, check whether the issue is not already there:
		// the board on screen may have gone stale, and "already in X" is a very
		// different answer from "the workflow forbids it".
		if det, e := cli.GetIssue(ctx, key); e == nil && det != nil && issueInColumn(det.Issue, col) {
			return det.Status.Name, nil
		}
		destinations := make([]string, 0, len(trs))
		for _, t := range trs {
			destinations = append(destinations, t.ToName)
		}
		if len(destinations) == 0 {
			return "", fmt.Errorf("the workflow offers no transition for %s from the current state", key)
		}
		return "", fmt.Errorf("the workflow does not take %s to %q; from here you can only go to: %s",
			key, col.Label, strings.Join(destinations, ", "))
	}
	if err := cli.Transition(ctx, key, tr.ID); err != nil {
		return "", err
	}
	return tr.ToName, nil
}

func jiraMoveHandler(deps Deps) func(context.Context, *jiraMoveInput) (*jiraMoveOutput, error) {
	return func(ctx context.Context, in *jiraMoveInput) (*jiraMoveOutput, error) {
		cli, user, err := jiraClient(ctx, deps)
		if err != nil {
			return nil, err
		}
		key := strings.TrimSpace(in.Body.Key)
		if key == "" {
			return nil, huma.Error400BadRequest("key is required")
		}
		col, err := columnByLabel(rawColumnsFor(deps, user), in.Body.Column)
		if err != nil {
			return nil, err
		}
		status, err := moveOne(ctx, cli, col, key)
		if err != nil {
			// 409 and not 500: the server is fine, it is the ACTION that does not fit
			// the current state. It is the code the app uses to send the card back to
			// its original column instead of showing "server error".
			return nil, huma.Error409Conflict(err.Error())
		}
		auditar(ctx, deps.Audit, user, "jira.issue.transition", key+" → "+col.Label)

		out := &jiraMoveOutput{}
		out.Body = JiraMoveResponse{Key: key, Status: status, Column: col.Label}
		return out, nil
	}
}

func jiraIssueHandler(deps Deps) func(context.Context, *jiraIssueInput) (*jiraIssueOutput, error) {
	return func(ctx context.Context, in *jiraIssueInput) (*jiraIssueOutput, error) {
		cli, user, err := jiraClient(ctx, deps)
		if err != nil {
			return nil, err
		}
		key := strings.TrimSpace(in.Key)
		if key == "" {
			return nil, huma.Error400BadRequest("key is required")
		}
		det, err := cli.GetIssue(ctx, key)
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}

		columns := BuildBoard(rawColumnsFor(deps, user), []jira.Issue{det.Issue}, 0, "", time.Now())

		body := JiraIssueResponse{
			Key:         det.Key,
			Summary:     det.Summary,
			Description: det.Description,
			Status:      det.Status.Name,
			Category:    det.Status.StatusCategory.Key,
			Column:      columnLabelOf(columns, det.Key),
			Labels:      det.Labels,
			Created:     det.Created,
			Updated:     det.Updated,
			DueDate:     det.DueDate,
			WebURL:      cli.Site() + "/browse/" + det.Key,
		}
		if det.IssueType != nil {
			body.Type = det.IssueType.Name
		}
		if det.Priority != nil {
			body.Priority = det.Priority.Name
		}
		body.Assignee = userRef(det.Assignee)
		body.Reporter = userRef(det.Reporter)

		for _, s := range det.Subtasks {
			body.Subtasks = append(body.Subtasks, boardCard(s))
		}
		for _, l := range det.IssueLinks {
			ref := JiraIssueLinkRef{Relation: l.Relation}
			if l.Other != nil {
				ref.Key = l.Other.Key
				ref.Summary = l.Other.Summary
				ref.Status = l.Other.Status.Name
			}
			body.Links = append(body.Links, ref)
		}

		// Comments and transitions are "best effort": the issue is already useful
		// without them, and a failure in either one must not stop the screen from
		// opening.
		if cs, err := cli.Comments(ctx, key); err == nil {
			for _, c := range cs {
				body.Comments = append(body.Comments, JiraCommentItem{
					ID: c.ID, Body: c.Body, Author: c.Author.DisplayName, Created: c.Created,
				})
			}
		}
		if trs, err := cli.Transitions(ctx, key); err == nil {
			all := BuildBoard(rawColumnsFor(deps, user), nil, 0, "", time.Now())
			for _, t := range trs {
				body.Moves = append(body.Moves, JiraMoveOption{
					Column: destinationLabel(all, t),
					Status: t.ToName,
					Name:   t.Name,
				})
			}
		}

		out := &jiraIssueOutput{}
		out.Body = body
		return out, nil
	}
}

func columnLabelOf(columns []JiraBoardColumn, key string) string {
	for _, c := range columns {
		for _, card := range c.Cards {
			if card.Key == key {
				return c.Label
			}
		}
	}
	return ""
}

// destinationLabel translates a transition's destination into the label of the
// column it lands in. Without that translation, the menu would offer
// "EM REVISÃO" while the board shows "Em andamento" — two languages for the
// same square.
func destinationLabel(columns []JiraBoardColumn, t jira.Transition) string {
	for _, c := range columns {
		if c.Category != "" && c.Category == t.ToCat {
			return c.Label
		}
		for _, n := range c.StatusNames {
			if strings.EqualFold(n, t.ToName) {
				return c.Label
			}
		}
	}
	return t.ToName
}

func userRef(u *jira.User) *JiraUserRef {
	if u == nil {
		return nil
	}
	return &JiraUserRef{AccountID: u.AccountID, DisplayName: u.DisplayName, AvatarURL: u.AvatarURL()}
}

func jiraCommentHandler(deps Deps) func(context.Context, *jiraCommentInput) (*jiraCommentOutput, error) {
	return func(ctx context.Context, in *jiraCommentInput) (*jiraCommentOutput, error) {
		cli, user, err := jiraClient(ctx, deps)
		if err != nil {
			return nil, err
		}
		key := strings.TrimSpace(in.Body.Key)
		text := strings.TrimSpace(in.Body.Text)
		if key == "" || text == "" {
			return nil, huma.Error400BadRequest("key and text are required")
		}
		c, err := cli.AddComment(ctx, key, text)
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		auditar(ctx, deps.Audit, user, "jira.issue.comment", key)

		out := &jiraCommentOutput{}
		out.Body = JiraCommentItem{ID: c.ID, Body: c.Body, Author: c.Author.DisplayName, Created: c.Created}
		return out, nil
	}
}

func jiraAssignHandler(deps Deps) func(context.Context, *jiraAssignInput) (*statusOutput, error) {
	return func(ctx context.Context, in *jiraAssignInput) (*statusOutput, error) {
		cli, user, err := jiraClient(ctx, deps)
		if err != nil {
			return nil, err
		}
		key := strings.TrimSpace(in.Body.Key)
		if key == "" {
			return nil, huma.Error400BadRequest("key is required")
		}
		id := strings.TrimSpace(in.Body.AccountID)
		if err := cli.UpdateIssue(ctx, key, jira.UpdateIssueRequest{AssigneeID: &id}); err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		auditar(ctx, deps.Audit, user, "jira.issue.assign", key)

		out := &statusOutput{}
		out.Body.Status = "ok"
		return out, nil
	}
}

func jiraUsersHandler(deps Deps) func(context.Context, *jiraUsersInput) (*jiraUsersOutput, error) {
	return func(ctx context.Context, in *jiraUsersInput) (*jiraUsersOutput, error) {
		cli, _, err := jiraClient(ctx, deps)
		if err != nil {
			return nil, err
		}
		project := strings.TrimSpace(in.Project)
		if project == "" {
			return nil, huma.Error400BadRequest("project is required")
		}
		list, err := cli.AssignableUsers(ctx, project, strings.TrimSpace(in.Query))
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		out := &jiraUsersOutput{}
		out.Body.Users = []JiraUserRef{}
		for _, u := range list {
			out.Body.Users = append(out.Body.Users, JiraUserRef{
				AccountID: u.AccountID, DisplayName: u.DisplayName, AvatarURL: u.AvatarURL(),
			})
		}
		return out, nil
	}
}

func jiraMetaHandler(deps Deps) func(context.Context, *jiraMetaInput) (*jiraMetaOutput, error) {
	return func(ctx context.Context, in *jiraMetaInput) (*jiraMetaOutput, error) {
		cli, _, err := jiraClient(ctx, deps)
		if err != nil {
			return nil, err
		}
		project := strings.TrimSpace(in.Project)
		if project == "" {
			return nil, huma.Error400BadRequest("project is required")
		}
		out := &jiraMetaOutput{}
		out.Body.IssueTypes = []string{}
		out.Body.Priorities = []string{}

		if kinds, err := cli.IssueTypesForProject(ctx, project); err == nil {
			for _, t := range kinds {
				// A subtask requires a parent issue; offering it in a form that does not
				// ask for a parent would produce a 400 from Jira on submit.
				if t.Subtask {
					continue
				}
				out.Body.IssueTypes = append(out.Body.IssueTypes, t.Name)
			}
		}
		if ps, err := cli.Priorities(ctx); err == nil {
			for _, p := range ps {
				out.Body.Priorities = append(out.Body.Priorities, p.Name)
			}
		}
		return out, nil
	}
}

func jiraCreateHandler(deps Deps) func(context.Context, *jiraCreateInput) (*jiraCreatedOutput, error) {
	return func(ctx context.Context, in *jiraCreateInput) (*jiraCreatedOutput, error) {
		cli, user, err := jiraClient(ctx, deps)
		if err != nil {
			return nil, err
		}
		b := in.Body
		if strings.TrimSpace(b.Project) == "" || strings.TrimSpace(b.Summary) == "" || strings.TrimSpace(b.Type) == "" {
			return nil, huma.Error400BadRequest("project, type and summary are required")
		}
		created, err := cli.CreateIssue(ctx, jira.CreateIssueRequest{
			ProjectKey:  strings.TrimSpace(b.Project),
			IssueType:   strings.TrimSpace(b.Type),
			Summary:     strings.TrimSpace(b.Summary),
			Description: b.Description,
			Priority:    b.Priority,
			Labels:      b.Labels,
			AssigneeID:  b.AssigneeID,
			DueDate:     b.DueDate,
			ParentKey:   b.ParentKey,
		})
		if err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		auditar(ctx, deps.Audit, user, "jira.issue.create", created.Key)

		out := &jiraCreatedOutput{}
		out.Body.Key = created.Key
		return out, nil
	}
}

func jiraBulkMoveHandler(deps Deps) func(context.Context, *jiraBulkMoveInput) (*jiraBulkOutput, error) {
	return func(ctx context.Context, in *jiraBulkMoveInput) (*jiraBulkOutput, error) {
		cli, user, err := jiraClient(ctx, deps)
		if err != nil {
			return nil, err
		}
		if len(in.Body.Keys) == 0 {
			return nil, huma.Error400BadRequest("keys is required")
		}
		col, err := columnByLabel(rawColumnsFor(deps, user), in.Body.Column)
		if err != nil {
			return nil, err
		}
		out := &jiraBulkOutput{}
		out.Body.Done = []string{}
		for _, key := range in.Body.Keys {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if _, err := moveOne(ctx, cli, col, key); err != nil {
				out.Body.Failed = append(out.Body.Failed, JiraBulkFailure{Key: key, Reason: err.Error()})
				continue
			}
			out.Body.Done = append(out.Body.Done, key)
			auditar(ctx, deps.Audit, user, "jira.issue.transition", key+" → "+col.Label)
		}
		return out, nil
	}
}

func jiraBulkAssignHandler(deps Deps) func(context.Context, *jiraBulkAssignInput) (*jiraBulkOutput, error) {
	return func(ctx context.Context, in *jiraBulkAssignInput) (*jiraBulkOutput, error) {
		cli, user, err := jiraClient(ctx, deps)
		if err != nil {
			return nil, err
		}
		if len(in.Body.Keys) == 0 {
			return nil, huma.Error400BadRequest("keys is required")
		}
		id := strings.TrimSpace(in.Body.AccountID)
		out := &jiraBulkOutput{}
		out.Body.Done = []string{}
		for _, key := range in.Body.Keys {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if err := cli.UpdateIssue(ctx, key, jira.UpdateIssueRequest{AssigneeID: &id}); err != nil {
				out.Body.Failed = append(out.Body.Failed, JiraBulkFailure{Key: key, Reason: err.Error()})
				continue
			}
			out.Body.Done = append(out.Body.Done, key)
			auditar(ctx, deps.Audit, user, "jira.issue.assign", key)
		}
		return out, nil
	}
}

func jiraConnectHandler(deps Deps) func(context.Context, *jiraConnectInput) (*statusOutput, error) {
	return func(ctx context.Context, in *jiraConnectInput) (*statusOutput, error) {
		user := auth.UserFromContext(ctx)
		if user == "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		if deps.JiraConnect == nil {
			return nil, huma.Error503ServiceUnavailable("the Jira integration is unavailable on this server")
		}
		b := in.Body
		if strings.TrimSpace(b.Site) == "" || strings.TrimSpace(b.Email) == "" || strings.TrimSpace(b.Token) == "" {
			return nil, huma.Error400BadRequest("site, email and token are required")
		}
		if err := deps.JiraConnect(user, b.Site, b.Email, b.Token, b.Project); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		// What gets audited is the SITE, never the token: the audit records that
		// the account was connected, not with what.
		auditar(ctx, deps.Audit, user, "jira.connect", b.Site)

		out := &statusOutput{}
		out.Body.Status = "ok"
		return out, nil
	}
}

func jiraSetProjectHandler(deps Deps) func(context.Context, *jiraProjectInput) (*statusOutput, error) {
	return func(ctx context.Context, in *jiraProjectInput) (*statusOutput, error) {
		user := auth.UserFromContext(ctx)
		if user == "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		if deps.JiraSetProject == nil {
			return nil, huma.Error503ServiceUnavailable("the Jira integration is unavailable on this server")
		}
		project := strings.TrimSpace(in.Body.Project)
		if project == "" {
			return nil, huma.Error400BadRequest("project is required")
		}
		if err := deps.JiraSetProject(user, project); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		auditar(ctx, deps.Audit, user, "jira.project", project)

		out := &statusOutput{}
		out.Body.Status = "ok"
		return out, nil
	}
}
