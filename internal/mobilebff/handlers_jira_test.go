package mobilebff

// handlers_jira_test.go exercises the board over real HTTP, against a fake
// Jira. The pure functions are already covered in jira_quadro_test.go; what
// shows up ONLY here is the end-to-end behaviour: the routing, the response
// shape, and the two decisions the user feels in their finger — a refused
// transition becoming a 409 with the reason, and a not-connected account
// becoming a 200 with the form instead of an error.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/jira"
)

// jiraFalso answers the minimum the board queries. Each route returns what the
// real route would return, in the shape the client already knows how to read.
func jiraFalso(t *testing.T, transicoes string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/myself"):
			_, _ = w.Write([]byte(`{"accountId":"acc-eu","displayName":"Sam Rivera"}`))
		case strings.Contains(r.URL.Path, "/project/search"), strings.HasSuffix(r.URL.Path, "/project"):
			_, _ = w.Write([]byte(`{"values":[{"key":"VPSM","name":"VPS Manager"}]}`))
		case strings.Contains(r.URL.Path, "/search"):
			_, _ = w.Write([]byte(`{"issues":[
				{"id":"1","key":"TASK-1","fields":{"summary":"cair a fila","status":{"name":"Backlog","statusCategory":{"key":"new"}}}},
				{"id":"2","key":"TASK-2","fields":{"summary":"revisar o quadro","status":{"name":"EM REVISÃO","statusCategory":{"key":"indeterminate"}}}}
			],"total":2}`))
		case strings.Contains(r.URL.Path, "/transitions"):
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte(transicoes))
		default:
			// Some issue, for the "already in the column" path.
			_, _ = w.Write([]byte(`{"id":"1","key":"TASK-1","fields":{"summary":"cair a fila","status":{"name":"Backlog","statusCategory":{"key":"new"}}}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func depsComJira(srv *httptest.Server) Deps {
	return Deps{
		Cfg: adminCfg(),
		JiraFor: func(user string) (*jira.Client, error) {
			return jira.NewForTests(srv.URL, "u@e.com", "tok", srv.Client()), nil
		},
		JiraConfigFor: func(user string) jira.Config {
			return jira.Config{Site: srv.URL, ProjectKey: "VPSM", HasToken: true}
		},
	}
}

func chamar(t *testing.T, deps Deps, metodo, caminho, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Mount(mux, deps)
	var req *http.Request
	if corpo == "" {
		req = httptest.NewRequest(metodo, "/api/mobile/v1"+caminho, nil)
	} else {
		req = httptest.NewRequest(metodo, "/api/mobile/v1"+caminho, strings.NewReader(corpo))
		req.Header.Set("Content-Type", "application/json")
	}
	req = req.WithContext(auth.WithUser(req.Context(), testPrimary))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestQuadroDevolveColunasComCartoesDistribuidos(t *testing.T) {
	srv := jiraFalso(t, `{"transitions":[]}`)
	rec := chamar(t, depsComJira(srv), http.MethodGet, "/jira/board", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body JiraBoardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if !body.Connected {
		t.Fatal("with credential in place, connected has to be true")
	}
	if len(body.Columns) != 3 {
		t.Fatalf("expected 3 columns per category, got %d", len(body.Columns))
	}
	if len(body.Columns[0].Cards) != 1 || body.Columns[0].Cards[0].Key != "TASK-1" {
		t.Errorf("the Backlog issue had to land in the first column: %+v", body.Columns[0].Cards)
	}
	// "EM REVISÃO" is the name Jira uses; the column is by CATEGORY, and that is
	// why it lands in "Em andamento" with nobody translating anything by hand.
	if len(body.Columns[1].Cards) != 1 || body.Columns[1].Cards[0].Key != "TASK-2" {
		t.Errorf("EM REVISÃO had to land in the middle column: %+v", body.Columns[1].Cards)
	}
	if len(body.Filters) == 0 {
		t.Error("the filters come from the server — without them the app would keep its own list that goes stale")
	}
	if body.Me == nil || body.Me.AccountID != "acc-eu" {
		t.Errorf("the board has to say who is looking (that's what 'atribuir a mim' uses): %+v", body.Me)
	}
}

func TestQuadroSemContaLigadaResponde200ComFormulario(t *testing.T) {
	// Never having connected is everybody's initial state, not a failure. A 4xx
	// would make the app show "could not load" with a retry button that is never
	// going to work.
	deps := Deps{
		Cfg:     adminCfg(),
		JiraFor: func(string) (*jira.Client, error) { return nil, jira.ErrNotConfigured },
	}
	rec := chamar(t, deps, http.MethodGet, "/jira/board", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body JiraBoardResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Connected {
		t.Error("connected had to be false")
	}
	if len(body.Filters) == 0 {
		t.Error("even when disconnected, the filters help the screen fully assemble itself")
	}
}

func TestMoverAplicaATransicaoQueChegaNaColuna(t *testing.T) {
	srv := jiraFalso(t, `{"transitions":[
		{"id":"11","name":"Iniciar","to":{"name":"Em Progresso","statusCategory":{"key":"indeterminate"}}},
		{"id":"31","name":"Concluir","to":{"name":"Pronto","statusCategory":{"key":"done"}}}
	]}`)
	rec := chamar(t, depsComJira(srv), http.MethodPost, "/jira/board/move", `{"key":"TASK-1","column":"Concluído"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body JiraMoveResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	// The status comes from the APPLIED transition, not from the client's wish: it
	// is with that status that the app confirms instead of assuming.
	if body.Status != "Pronto" || body.Column != "Concluído" {
		t.Errorf("response = %+v; want status Pronto in column Concluído", body)
	}
}

func TestSemTransicaoParaAColunaResponde409ComOsDestinosPossiveis(t *testing.T) {
	// 409 and not 500: the server is fine, it is the ACTION that does not fit the
	// current state. It is the code by which the app sends the card back to its
	// original column instead of showing "server error". And the reason has to say
	// where it IS possible to go — that is what turns the refusal into a next step.
	srv := jiraFalso(t, `{"transitions":[
		{"id":"11","name":"Iniciar","to":{"name":"Em Progresso","statusCategory":{"key":"indeterminate"}}}
	]}`)
	rec := chamar(t, depsComJira(srv), http.MethodPost, "/jira/board/move", `{"key":"TASK-2","column":"Concluído"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Em Progresso") {
		t.Errorf("the refusal has to name the possible destinations: %s", rec.Body.String())
	}
}

func TestMoverParaAColunaOndeAIssueJaEstaNaoEErro(t *testing.T) {
	// The board on screen may have gone stale. "Already in X" is a very different
	// answer from "the workflow forbids it", and confusing the two would send the
	// card back for no reason.
	srv := jiraFalso(t, `{"transitions":[]}`)
	rec := chamar(t, depsComJira(srv), http.MethodPost, "/jira/board/move", `{"key":"TASK-1","column":"A fazer"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestColunaInexistenteNoQuadroE400(t *testing.T) {
	// The operator may have reconfigured the board between the load and the drag.
	// Moving to a "similar-looking" column would be worse than refusing.
	srv := jiraFalso(t, `{"transitions":[]}`)
	rec := chamar(t, depsComJira(srv), http.MethodPost, "/jira/board/move", `{"key":"TASK-1","column":"Coluna que não existe"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestLoteDevolveOQueFoiEOQueNaoFoi(t *testing.T) {
	// A batch is not atomic against Jira: every issue has its own workflow. An
	// "ok" for the set would hide precisely the ones that need action.
	srv := jiraFalso(t, `{"transitions":[
		{"id":"11","name":"Iniciar","to":{"name":"Em Progresso","statusCategory":{"key":"indeterminate"}}}
	]}`)
	rec := chamar(t, depsComJira(srv), http.MethodPost, "/jira/bulk/move",
		`{"issue_keys":["TASK-1","TASK-2"],"column":"Em andamento"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body JiraBulkResult
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Done) != 2 {
		t.Errorf("both had a transition to 'Em andamento': %+v", body)
	}
}

func TestSemJiraNoServidorAsRotasRespondem503EmVezDePanic(t *testing.T) {
	// Same pattern as the other optional fields of Deps: absence degrades, it
	// never brings the process down.
	rec := chamar(t, Deps{Cfg: adminCfg()}, http.MethodGet, "/jira/board", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestIssueTrazOsDestinosNoVocabularioDasColunas(t *testing.T) {
	// Whoever sees "Em andamento" on the board should not have to translate
	// "Em Progresso" in their head in the move menu.
	srv := jiraFalso(t, `{"transitions":[
		{"id":"11","name":"Iniciar","to":{"name":"Em Progresso","statusCategory":{"key":"indeterminate"}}}
	]}`)
	rec := chamar(t, depsComJira(srv), http.MethodGet, "/jira/issue?key=TASK-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body JiraIssueResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Moves) != 1 {
		t.Fatalf("expected one destination: %+v", body.Moves)
	}
	if body.Moves[0].Column != "Em andamento" {
		t.Errorf("destination = %q; the menu speaks the COLUMN name, not the status name", body.Moves[0].Column)
	}
	if body.Moves[0].Status != "Em Progresso" {
		t.Errorf("the real status name stays available for whoever wants to check: %+v", body.Moves[0])
	}
}

// jiraQuePassaFome returns 99 done issues for ANY search that does not restrict
// the category, and a single "to do" when the search asks for To Do. It is the
// exact shape of the reported defect: with a single search, the done issues ate
// the quota and the left-hand column came back empty.
func jiraQuePassaFome(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var consultas []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		corpo, _ := io.ReadAll(r.Body)
		jql := string(corpo) + r.URL.RawQuery
		consultas = append(consultas, jql)

		switch {
		case strings.Contains(jql, "To Do"):
			_, _ = w.Write([]byte(`{"issues":[
				{"id":"1","key":"TASK-1","fields":{"summary":"a fazer","status":{"name":"Backlog","statusCategory":{"key":"new"}}}}
			],"total":1}`))
		case strings.Contains(jql, "In Progress"):
			_, _ = w.Write([]byte(`{"issues":[],"total":0}`))
		default:
			// Done: many of them, and they are the ones that ate the quota.
			var sb strings.Builder
			sb.WriteString(`{"issues":[`)
			for i := 0; i < 99; i++ {
				if i > 0 {
					sb.WriteString(",")
				}
				fmt.Fprintf(&sb, `{"id":"%d","key":"VPSM-D%d","fields":{"summary":"feita","status":{"name":"Pronto","statusCategory":{"key":"done"}}}}`, 100+i, i)
			}
			sb.WriteString(`],"total":99}`)
			_, _ = w.Write([]byte(sb.String()))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &consultas
}

func TestColunaNaoPassaFomePorCausaDeOutra(t *testing.T) {
	// The defect in the owner's screenshots: the same project showed "A fazer 2"
	// under the "A fazer" filter and "A fazer 1" under the "Todas" filter. The
	// column was losing a card because of the done issues.
	srv, consultas := jiraQuePassaFome(t)
	rec := chamar(t, depsComJira(srv), http.MethodGet, "/jira/board?max=40", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", rec.Code, rec.Body.String())
	}
	var body JiraBoardResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(body.Columns))
	}
	if len(body.Columns[0].Cards) != 1 || body.Columns[0].Cards[0].Key != "TASK-1" {
		t.Fatalf("the 'A fazer' column starved again: %+v", body.Columns[0].Cards)
	}
	if len(body.Columns[2].Cards) == 0 {
		t.Error("the done ones keep coming — they just stop trampling the others")
	}
	// One query PER COLUMN: it is the only way for each one to have its own quota.
	if len(*consultas) < 3 {
		t.Errorf("expected one search per column, there were %d: %v", len(*consultas), *consultas)
	}
}

func TestUmaColunaQueFalhaNaoApagaAsOutras(t *testing.T) {
	// The refusal shows on top of the board, with the cards we managed to bring.
	// Wiping everything because of one column would lose what already worked.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		corpo, _ := io.ReadAll(r.Body)
		if strings.Contains(string(corpo)+r.URL.RawQuery, "In Progress") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errorMessages":["JQL invalido"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"issues":[
			{"id":"1","key":"TASK-1","fields":{"summary":"a fazer","status":{"name":"Backlog","statusCategory":{"key":"new"}}}}
		],"total":1}`))
	}))
	t.Cleanup(srv.Close)

	rec := chamar(t, depsComJira(srv), http.MethodGet, "/jira/board", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body JiraBoardResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error == "" {
		t.Error("the refusal from the column that failed has to show up")
	}
	if len(body.Columns[0].Cards) == 0 {
		t.Error("the columns that did come through have to stay on screen")
	}
}

func TestPrimeiraCargaEIGUALAoRefresh(t *testing.T) {
	// The DEFECT reported by the owner: entering Tasks showed three empty
	// columns, and only a refresh filled the board.
	//
	// The cause was stateful: on the first load the app does not know the project
	// yet, so it does not send `project=`. The server read that as "the operator
	// picked no project" and swapped the filter's JQL for the board_jql from the
	// vault — which, in his case, returned zero. On refresh the app already knew
	// the project, sent `project=VPSM`, the detour did not happen, and the board
	// filled up.
	//
	// The same filter, with the same project, gave two different boards depending
	// on whether the client had already learned where it was. This test exists so
	// that the two loads never diverge again.
	srv := jiraFalso(t, `{"transitions":[]}`)
	deps := depsComJira(srv)
	deps.JiraConfigFor = func(user string) jira.Config {
		// A configured board_jql that matches NOTHING — exactly the owner's case.
		// If it goes back to hijacking the filter, this test fails.
		return jira.Config{
			Site:       srv.URL,
			ProjectKey: "VPSM",
			BoardJQL:   `project = PROJETO_QUE_NAO_EXISTE`,
			HasToken:   true,
		}
	}

	primeira := chamar(t, deps, http.MethodGet, "/jira/board", "")
	segunda := chamar(t, deps, http.MethodGet, "/jira/board?project=VPSM", "")

	var a, b JiraBoardResponse
	if err := json.Unmarshal(primeira.Body.Bytes(), &a); err != nil {
		t.Fatalf("decode 1: %v", err)
	}
	if err := json.Unmarshal(segunda.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode 2: %v", err)
	}

	if a.JQL != b.JQL {
		t.Errorf("the first load used a different JQL than the refresh:\n  1a: %q\n  2a: %q", a.JQL, b.JQL)
	}
	cartoes := func(r JiraBoardResponse) int {
		n := 0
		for _, c := range r.Columns {
			n += len(c.Cards)
		}
		return n
	}
	if cartoes(a) != cartoes(b) {
		t.Errorf("first load brought %d cards and the refresh %d", cartoes(a), cartoes(b))
	}
	if cartoes(a) == 0 {
		t.Error("the first load has to bring the cards, not three empty columns")
	}
}

func TestMeuQuadroEscolhidoDePropositoUSAOJQLDoOperador(t *testing.T) {
	// The operator's JQL was not thrown away — it became a NAMED filter. An empty
	// board there says the stored query matches nothing, not that the app failed.
	srv := jiraFalso(t, `{"transitions":[]}`)
	deps := depsComJira(srv)
	deps.JiraConfigFor = func(user string) jira.Config {
		return jira.Config{Site: srv.URL, ProjectKey: "VPSM", BoardJQL: "assignee = currentUser()", HasToken: true}
	}

	rec := chamar(t, deps, http.MethodGet, "/jira/board?filter=board", "")
	var body JiraBoardResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body.JQL, "assignee = currentUser()") {
		t.Errorf("the 'Meu quadro' filter has to use the operator's JQL: %q", body.JQL)
	}
	temOpcao := false
	for _, f := range body.Filters {
		if f.Key == "board" {
			temOpcao = true
		}
	}
	if !temOpcao {
		t.Error("with board_jql configured, the filter has to be OFFERED")
	}
}
