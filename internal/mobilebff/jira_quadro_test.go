package mobilebff

import (
	"testing"
	"time"

	"server-control-panel/internal/jira"
)

func issueDeTeste(key, status, categoria string) jira.Issue {
	return jira.Issue{
		Key:     key,
		Summary: "resumo de " + key,
		Status: jira.Status{
			Name:           status,
			StatusCategory: jira.StatusCategory{Key: categoria},
		},
	}
}

func TestQuadroSemConfiguracaoUsaAsTresCategorias(t *testing.T) {
	// This project calls "A fazer" "Backlog". Columns by NAME would break on
	// it; by category, they do not.
	issues := []jira.Issue{
		issueDeTeste("V-1", "Backlog", "new"),
		issueDeTeste("V-2", "EM REVISAO", "indeterminate"),
		issueDeTeste("V-3", "Pronto", "done"),
	}
	cols := MontarQuadro("", issues, 0, "", time.Now())

	if len(cols) != 3 {
		t.Fatalf("expected 3 columns by category, got %d", len(cols))
	}
	if len(cols[0].Cards) != 1 || cols[0].Cards[0].Key != "V-1" {
		t.Errorf("Backlog had to land in the 'new' column: %+v", cols[0].Cards)
	}
	if len(cols[1].Cards) != 1 || cols[1].Cards[0].Key != "V-2" {
		t.Errorf("EM REVISAO had to land in the 'indeterminate' column: %+v", cols[1].Cards)
	}
	if len(cols[2].Cards) != 1 || cols[2].Cards[0].Key != "V-3" {
		t.Errorf("Pronto had to land in the 'done' column: %+v", cols[2].Cards)
	}
}

func TestQuadroComColunasConfiguradasCasaPorNomeSemLigarParaMaiuscula(t *testing.T) {
	cfg := `[{"label":"Fazendo","status_names":["Em Progresso","EM REVISÃO"]}]`
	issues := []jira.Issue{
		issueDeTeste("V-1", "em revisão", "indeterminate"),
	}
	cols := MontarQuadro(cfg, issues, 0, "", time.Now())

	if len(cols) != 1 {
		t.Fatalf("expected 1 column (no orphans, no 'Outros'), got %d", len(cols))
	}
	if len(cols[0].Cards) != 1 {
		t.Fatalf("name matching must ignore case: %+v", cols[0])
	}
}

func TestColunaOutrosSoExisteQuandoHaOrfa(t *testing.T) {
	cfg := `[{"label":"Fazendo","status_names":["Em Progresso"]}]`

	semOrfa := MontarQuadro(cfg, []jira.Issue{issueDeTeste("V-1", "Em Progresso", "indeterminate")}, 0, "", time.Now())
	if len(semOrfa) != 1 {
		t.Fatalf("a board with no orphan must not gain an empty 'Outros' column: %d columns", len(semOrfa))
	}

	comOrfa := MontarQuadro(cfg, []jira.Issue{issueDeTeste("V-2", "Bloqueada", "indeterminate")}, 0, "", time.Now())
	if len(comOrfa) != 2 || !comOrfa[1].Fallback {
		t.Fatalf("an issue whose status is outside the columns had to appear in 'Outros': %+v", comOrfa)
	}
	if comOrfa[1].Cards[0].Key != "V-2" {
		t.Errorf("the wrong orphan went to 'Outros': %+v", comOrfa[1].Cards)
	}
}

func TestConfiguracaoInvalidaCaiNoPadraoEmVezDeQuebrar(t *testing.T) {
	// A crooked preference in the vault must not keep the board from opening.
	for _, ruim := range []string{"isto nao e json", "[]", "{}", "   "} {
		cols := MontarQuadro(ruim, []jira.Issue{issueDeTeste("V-1", "Backlog", "new")}, 0, "", time.Now())
		if len(cols) != 3 {
			t.Errorf("config %q had to fall back to the 3 default columns, got %d", ruim, len(cols))
		}
	}
}

func TestConcluidasVelhasSomemMasSoAsConcluidas(t *testing.T) {
	agora := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	velha := agora.Add(-40 * 24 * time.Hour).Format("2006-01-02T15:04:05.000-0700")
	recente := agora.Add(-2 * 24 * time.Hour).Format("2006-01-02T15:04:05.000-0700")

	concluidaVelha := issueDeTeste("V-1", "Pronto", "done")
	concluidaVelha.Updated = velha
	concluidaRecente := issueDeTeste("V-2", "Pronto", "done")
	concluidaRecente.Updated = recente
	abertaVelha := issueDeTeste("V-3", "Backlog", "new")
	abertaVelha.Updated = velha

	cols := MontarQuadro("", []jira.Issue{concluidaVelha, concluidaRecente, abertaVelha}, 30, "", agora)

	if len(cols[0].Cards) != 1 || cols[0].Cards[0].Key != "V-3" {
		t.Errorf("an OPEN issue must not disappear by age, only completed ones can: %+v", cols[0].Cards)
	}
	if len(cols[2].Cards) != 1 || cols[2].Cards[0].Key != "V-2" {
		t.Errorf("only the recently completed one should remain: %+v", cols[2].Cards)
	}
}

func TestIssueSemDataLegivelNuncaSomePorIdade(t *testing.T) {
	// Losing work over a date-formatting detail would be the worst possible
	// outcome of a cosmetic preference.
	semData := issueDeTeste("V-1", "Pronto", "done")
	semData.Updated = "ontem de tarde"

	cols := MontarQuadro("", []jira.Issue{semData}, 1, "", time.Now())
	if len(cols[2].Cards) != 1 {
		t.Fatalf("an issue with an unreadable date had to stay visible: %+v", cols[2])
	}
}

func TestOrdenacaoPorChaveEnumericaNaoAlfabetica(t *testing.T) {
	issues := []jira.Issue{
		issueDeTeste("TASK-9", "Backlog", "new"),
		issueDeTeste("TASK-100", "Backlog", "new"),
		issueDeTeste("TASK-10", "Backlog", "new"),
	}
	cols := MontarQuadro("", issues, 0, "key:asc", time.Now())

	got := []string{cols[0].Cards[0].Key, cols[0].Cards[1].Key, cols[0].Cards[2].Key}
	want := []string{"TASK-9", "TASK-10", "TASK-100"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordering by key must be numeric: %v (wanted %v)", got, want)
		}
	}
}

func TestOrdemDesconhecidaPreservaAOrdemDoJQL(t *testing.T) {
	// A newer client may ask for a criterion this server does not know yet.
	// Degrading to the JQL's order is correct; an error is not.
	issues := []jira.Issue{
		issueDeTeste("V-3", "Backlog", "new"),
		issueDeTeste("V-1", "Backlog", "new"),
	}
	cols := MontarQuadro("", issues, 0, "prioridade_ponderada:desc", time.Now())
	if cols[0].Cards[0].Key != "V-3" {
		t.Fatalf("an unknown order should be a no-op: %+v", cols[0].Cards)
	}
}

func TestTransicaoParaColunaPorCategoria(t *testing.T) {
	col := JiraBoardColumn{Label: "Concluído", Category: "done"}
	trs := []jira.Transition{
		{ID: "11", Name: "Iniciar", ToName: "Em Progresso", ToCat: "indeterminate"},
		{ID: "31", Name: "Concluir", ToName: "Pronto", ToCat: "done"},
	}
	tr := TransicaoParaColuna(col, trs)
	if tr == nil || tr.ID != "31" {
		t.Fatalf("should pick the transition that lands in 'done': %+v", tr)
	}
}

func TestTransicaoParaColunaPorNomeRespeitaAOrdemDoOperador(t *testing.T) {
	// The operator wrote "Em Progresso" first: that is their preference.
	col := JiraBoardColumn{Label: "Fazendo", StatusNames: []string{"Em Progresso", "EM REVISÃO"}}
	trs := []jira.Transition{
		{ID: "21", Name: "Revisar", ToName: "EM REVISÃO", ToCat: "indeterminate"},
		{ID: "11", Name: "Iniciar", ToName: "em progresso", ToCat: "indeterminate"},
	}
	tr := TransicaoParaColuna(col, trs)
	if tr == nil || tr.ID != "11" {
		t.Fatalf("should prefer the column's first name: %+v", tr)
	}
}

func TestSemTransicaoParaAColunaDevolveNil(t *testing.T) {
	// The project's workflow forbids the jump. This is NOT an error — it is the
	// answer that sends the card back to its original column with a reason.
	col := JiraBoardColumn{Label: "Concluído", Category: "done"}
	trs := []jira.Transition{{ID: "11", ToName: "Em Progresso", ToCat: "indeterminate"}}
	if tr := TransicaoParaColuna(col, trs); tr != nil {
		t.Fatalf("there is no transition to 'done'; should be nil, got %+v", tr)
	}
}

func TestIssueQueJaEstaNaColunaEReconhecida(t *testing.T) {
	// The same function the board draws with. If they diverged, dragging a card
	// onto the column it is already in would turn into a real transition.
	col := JiraBoardColumn{Label: "Em andamento", Category: "indeterminate"}
	if !issueCaiNaColuna(issueDeTeste("V-1", "EM REVISÃO", "indeterminate"), col) {
		t.Error("the issue is already in this column and that has to be recognized")
	}
	if issueCaiNaColuna(issueDeTeste("V-2", "Backlog", "new"), col) {
		t.Error("an issue from another category must not count as already in the column")
	}
}

func TestJQLDoFiltroEspelhaOPainelWeb(t *testing.T) {
	casos := []struct {
		filtro, projeto, custom, quero string
	}{
		{"all", "VPSM", "", "project = VPSM ORDER BY updated DESC"},
		{"all", "", "", "ORDER BY updated DESC"},
		{"mine", "VPSM", "", "project = VPSM AND assignee = currentUser() AND statusCategory != Done ORDER BY rank ASC"},
		{"reported", "", "", "reporter = currentUser() ORDER BY updated DESC"},
		{"custom", "VPSM", "labels = urgente", "labels = urgente"},
		// A filter this server does not know falls back to "all" — the app may
		// be newer than the server.
		{"inventado", "VPSM", "", "project = VPSM ORDER BY updated DESC"},
	}
	for _, c := range casos {
		if got := JQLDoFiltro(c.filtro, c.projeto, c.custom, ""); got != c.quero {
			t.Errorf("filter %q/project %q: %q (want %q)", c.filtro, c.projeto, got, c.quero)
		}
	}
}

func TestJQLNuncaSaiComANDPendurado(t *testing.T) {
	// The bug the web panel patches with a regex after assembling. Here the
	// assembly is born right.
	for _, f := range []string{"all", "mine", "todo", "inprogress", "last7", "reported", "outro"} {
		for _, p := range []string{"", "VPSM"} {
			jql := JQLDoFiltro(f, p, "", "")
			if len(jql) > 0 && (containsSeq(jql, "AND ORDER") || hasPrefixSeq(jql, "AND ")) {
				t.Errorf("filter %q/project %q generated invalid JQL: %q", f, p, jql)
			}
		}
	}
}

func TestBuscaOlhaChaveResumoStatusRotuloEResponsavel(t *testing.T) {
	comResponsavel := issueDeTeste("V-1", "Backlog", "new")
	comResponsavel.Assignee = &jira.User{DisplayName: "Sam Rivera"}
	comRotulo := issueDeTeste("V-2", "Backlog", "new")
	comRotulo.Labels = []string{"urgente"}
	issues := []jira.Issue{comResponsavel, comRotulo, issueDeTeste("V-3", "Pronto", "done")}

	if got := FiltrarPorBusca(issues, "sam"); len(got) != 1 || got[0].Key != "V-1" {
		t.Errorf("search by assignee failed: %+v", got)
	}
	if got := FiltrarPorBusca(issues, "URGENTE"); len(got) != 1 || got[0].Key != "V-2" {
		t.Errorf("search by label must ignore case: %+v", got)
	}
	if got := FiltrarPorBusca(issues, "pronto"); len(got) != 1 || got[0].Key != "V-3" {
		t.Errorf("search by status failed: %+v", got)
	}
	if got := FiltrarPorBusca(issues, ""); len(got) != 3 {
		t.Errorf("an empty search filters nothing: %+v", got)
	}
}

func TestCartaoTrazOResponsavelJaFormatado(t *testing.T) {
	// The client never builds a label out of a raw struct.
	is := issueDeTeste("V-1", "Backlog", "new")
	is.Assignee = &jira.User{
		AccountID:   "acc-1",
		DisplayName: "Sam Rivera",
		AvatarURLs:  map[string]string{"48x48": "https://exemplo/48.png"},
	}
	is.Priority = &jira.NamedRef{Name: "Alta"}
	is.IssueType = &jira.NamedRef{Name: "Bug"}

	c := cartaoDoQuadro(is)
	if c.Assignee != "Sam Rivera" || c.AssigneeID != "acc-1" {
		t.Errorf("assignee did not come formatted: %+v", c)
	}
	if c.AvatarURL != "https://exemplo/48.png" {
		t.Errorf("avatar should be the largest one available: %q", c.AvatarURL)
	}
	if c.Priority != "Alta" || c.Type != "Bug" {
		t.Errorf("priority/type did not come through: %+v", c)
	}
}

func TestColunaVaziaVemComListaVaziaNuncaNula(t *testing.T) {
	cols := MontarQuadro("", nil, 0, "", time.Now())
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

func TestDividirJQLSeparaOndeDeOrdem(t *testing.T) {
	// The column restriction goes in BEFORE the ORDER BY. Concatenating without
	// splitting produces "... ORDER BY updated DESC AND status = X", which is invalid.
	casos := []struct{ jql, onde, ordem string }{
		{"project = VPSM ORDER BY updated DESC", "project = VPSM", "ORDER BY updated DESC"},
		{"project = VPSM order by rank ASC", "project = VPSM", "order by rank ASC"},
		{"ORDER BY updated DESC", "", "ORDER BY updated DESC"},
		{"project = VPSM", "project = VPSM", ""},
		{"", "", ""},
	}
	for _, c := range casos {
		onde, ordem := DividirJQL(c.jql)
		if onde != c.onde || ordem != c.ordem {
			t.Errorf("DividirJQL(%q) = (%q, %q); want (%q, %q)", c.jql, onde, ordem, c.onde, c.ordem)
		}
	}
}

func TestRestricaoPorCategoriaUsaOVocabularioDoJQL(t *testing.T) {
	// The stable key is "new"; JQL wants "To Do". Sending the raw key would bring
	// back zero issues in silence — the worst possible failure on a board.
	casos := map[string]string{
		"new":           `statusCategory = "To Do"`,
		"indeterminate": `statusCategory = "In Progress"`,
		"done":          `statusCategory = "Done"`,
	}
	for chave, quero := range casos {
		got := RestricaoDaColuna(JiraBoardColumn{Category: chave}, nil)
		if got != quero {
			t.Errorf("category %q: %q (want %q)", chave, got, quero)
		}
	}
}

func TestRestricaoPorNomeListaOsStatusDaColuna(t *testing.T) {
	col := JiraBoardColumn{Label: "Fazendo", StatusNames: []string{"Em Progresso", "EM REVISÃO"}}
	got := RestricaoDaColuna(col, nil)
	quero := `status IN ("Em Progresso", "EM REVISÃO")`
	if got != quero {
		t.Errorf("%q (want %q)", got, quero)
	}
}

func TestColunaOutrosPerguntaPeloCOMPLEMENTO(t *testing.T) {
	// "Whatever is none of the others" is only askable of Jira as NOT IN.
	todas := []JiraBoardColumn{
		{Label: "A", StatusNames: []string{"Backlog"}},
		{Label: "B", StatusNames: []string{"Pronto"}},
		{Label: "Outros", Fallback: true},
	}
	got := RestricaoDaColuna(todas[2], todas)
	quero := `status NOT IN ("Backlog", "Pronto")`
	if got != quero {
		t.Errorf("%q (want %q)", got, quero)
	}
}

func TestColunaSemComoSeRestringirDevolveVazio(t *testing.T) {
	// The caller uses this to fall back to the single search, instead of building
	// a crooked query that would bring the column back empty with no explanation.
	if got := RestricaoDaColuna(JiraBoardColumn{Label: "?"}, nil); got != "" {
		t.Errorf("a column with neither category nor names should return empty, got %q", got)
	}
	if got := RestricaoDaColuna(JiraBoardColumn{Category: "inventada"}, nil); got != "" {
		t.Errorf("an unknown category should return empty, got %q", got)
	}
}

func TestJQLDaColunaPoeOFiltroENTREPARENTESES(t *testing.T) {
	// Without the parentheses, a filter with an OR would bind only to the last
	// term and the board would bring back more than it should — silent widening.
	got := JQLDaColuna(`project = VPSM OR project = TTW`, "ORDER BY updated DESC", `statusCategory = "To Do"`)
	quero := `(project = VPSM OR project = TTW) AND statusCategory = "To Do" ORDER BY updated DESC`
	if got != quero {
		t.Errorf("%q\nqueria %q", got, quero)
	}
}

func TestJQLDaColunaSemFiltroENuncaComecaComAND(t *testing.T) {
	got := JQLDaColuna("", "ORDER BY updated DESC", `statusCategory = "Done"`)
	quero := `statusCategory = "Done" ORDER BY updated DESC`
	if got != quero {
		t.Errorf("%q (want %q)", got, quero)
	}
	if hasPrefixSeq(got, "AND") {
		t.Error("JQL must not start with AND")
	}
}

func TestNomeDeStatusComAspasNaoQuebraAConsultaINTEIRA(t *testing.T) {
	col := JiraBoardColumn{StatusNames: []string{`Em "revisao"`}}
	got := RestricaoDaColuna(col, nil)
	if !containsSeq(got, `\"`) {
		t.Errorf("the inner quotes must come out escaped: %q", got)
	}
}

// --- the operator's own board filter ------------------------------------------

func TestQuadroProprioSoAparecePorEscolha(t *testing.T) {
	// THE DEFECT this fixes: a configured board_jql hijacked the "Todas"
	// filter — but only on the FIRST load, because the condition that triggered
	// it was "the client did not send a project", and the client only learns the
	// project from the response. With a board_jql that returns zero, the board
	// opened empty and filled up on refresh.
	if got := JQLDoFiltro("all", "VPSM", "", "project = OUTRO"); got != "project = VPSM ORDER BY updated DESC" {
		t.Errorf("the operator's JQL must not hijack the 'Todas' filter: %q", got)
	}
	if got := JQLDoFiltro("board", "VPSM", "", "project = OUTRO"); got != "project = OUTRO" {
		t.Errorf("chosen on purpose, it counts: %q", got)
	}
}

func TestQuadroProprioSemConsultaCaiEmTodas(t *testing.T) {
	// A filter with no query behind it would be a button that does nothing.
	if got := JQLDoFiltro("board", "VPSM", "", ""); got != "project = VPSM ORDER BY updated DESC" {
		t.Errorf("%q", got)
	}
}

func TestMeuQuadroSoEOFERECIDOQuandoExiste(t *testing.T) {
	sem := FiltrosDoQuadro(false)
	for _, f := range sem {
		if f.Key == "board" {
			t.Fatal("with no board_jql configured, 'Meu quadro' must not appear")
		}
	}
	com := FiltrosDoQuadro(true)
	if com[len(com)-1].Key != "board" {
		t.Fatalf("with board_jql, 'Meu quadro' goes at the end: %+v", com)
	}
	if len(com) != len(sem)+1 {
		t.Fatalf("only one filter more: %d vs %d", len(com), len(sem))
	}
}
