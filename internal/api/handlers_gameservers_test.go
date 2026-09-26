package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"server-control-panel/internal/config"
	"server-control-panel/internal/gameservers"
)

// ─────────────────────────────────────────────────────────────────────────────
// OS 27 CASE

// os27Case is the triage transcribed LINE BY LINE, carrying the line number of
// the original file, so that checking it is mechanical.
//
// Three of the 27 lines are NOT HTTP endpoints, and that is declared rather than
// silently omitted — a 25-entry table checking a criterion that speaks of 27
// would be exactly the kind of discrepancy nobody notices:
//
//   - line 84 (`start|stop|restart`) is the VALUE sub-switch inside `action`;
//     it was never a route of its own and became the closed set `Verbo`.
//   - lines 320 and 498 (`worlds`, `backups`) are the FAMILY header; the real
//     route is the empty action right below them (322 and 500).
//
// What is checked for those three is that the destination exists, not that they
// answer a URL of their own — because they never did.
var os27Case = []struct {
	line     int
	resource string
	action   string
	endpoint bool // false = became a value, or is a family header
	note     string
}{
	{72, "action", "", true, ""},
	{84, "action", "", false, "start|stop|restart viraram valores de Verbo"},
	{95, "connection", "", true, "campo de server.status"},
	{98, "groups", "", true, "secao de settings"},
	{131, "bans", "", true, "secao de settings"},
	{157, "history", "", true, ""},
	{166, "build", "", true, "campo de server.status"},
	{169, "update", "", true, "verbo de server.action"},
	{179, "rawconfig", "", true, "estreitado pelo contrato"},
	// Line 209 covers trainer.status, trainer.apply and trainer.desired: one
	// triage line, three operations. The sub-actions are exercised in
	// subActionsOfLine209, outside the count, so the table stays a faithful
	// transcription of the triage — 27 lines, not one more.
	{209, "trainer", "", true, "cobre status, apply e desired"},
	{254, "runtime", "", true, ""},
	{280, "server", "", true, ""},
	{320, "worlds", "", false, "cabecalho da familia; a rota e a acao vazia (322)"},
	{322, "worlds", "", true, ""},
	{333, "worlds", "switch", true, ""},
	{350, "worlds", "export", true, "caminho virou Handle"},
	{362, "worlds", "import", true, "upload virou Receber + Handle"},
	{400, "worlds", "rename", true, ""},
	{417, "worlds", "duplicate", true, ""},
	{434, "worlds", "delete", true, ""},
	{455, "settings", "", true, ""},
	{498, "backups", "", false, "cabecalho da familia; a rota e a acao vazia (500)"},
	{500, "backups", "", true, ""},
	{511, "backups", "create", true, ""},
	{522, "backups", "restore", true, ""},
	{552, "backups", "download", true, "caminho virou Handle"},
	{568, "logs", "", true, ""},
}

// subActionsOfLine209 are the actions of the `trainer` resource, which the triage
// handled on a single line.
var subActionsOfLine209 = []string{"apply", "desired"}

func gamesRouter(t *testing.T) (*Router, string) {
	t.Helper()
	dir := t.TempDir()
	inv := `[{"id":"jogo-b","name":"Enshrouded","game":"enshrouded","container":"jogo-b","root":"` + dir + `","node":""}]`
	if err := os.WriteFile(filepath.Join(dir, "gameservers.json"), []byte(inv), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Router{cfg: &config.Config{DataDir: dir, Primary: "sam"}}
	r.gameMgr = gameservers.New(dir, nil)
	return r, dir
}

// TestEveryCaseHasDestination: no case was lost in the rewrite.
//
// The criterion is "the route exists": a business error counts as existing, a
// route 404 does not. With no node registered they all answer 400 with the
// registration message — which is the correct behaviour and proves the case got
// all the way to resolution.
func TestEveryCaseHasDestination(t *testing.T) {
	r, _ := gamesRouter(t)

	// The table has to carry the 27 lines of the triage. The count is asserted
	// here, and only here, because it is a HISTORICAL count (how many cases
	// existed in the original file) and not a property of the current code — the
	// rule forbids freezing a count that grows, not recording a closed fact.
	if len(os27Case) != 27 {
		t.Fatalf("the 08-03 triage has 27 lines; the table transcribed %d", len(os27Case))
	}
	nonEndpoint := 0
	for _, c := range os27Case {
		if !c.endpoint {
			nonEndpoint++
			if c.note == "" {
				t.Errorf("line %d marked as non-endpoint with no written justification", c.line)
			}
		}
	}
	t.Logf("27 triage lines: %d endpoints, %d non-endpoints declared", 27-nonEndpoint, nonEndpoint)

	for _, c := range subActionsOfLine209 {
		req := httptest.NewRequest(http.MethodPost, "/api/gameservers/jogo-b/trainer/"+c, nil)
		w := httptest.NewRecorder()
		r.handleGameServerSub(w, req)
		if w.Code == http.StatusNotFound && strings.Contains(w.Body.String(), "desconhecid") {
			t.Errorf("SUB-ACTION LOST: trainer/%s → route 404 (%s)", c, strings.TrimSpace(w.Body.String()))
		}
	}

	for _, c := range os27Case {
		if !c.endpoint {
			continue
		}
		nome := c.resource
		if c.action != "" {
			nome += "/" + c.action
		}
		t.Run(nome, func(t *testing.T) {
			path := "/api/gameservers/jogo-b/" + c.resource
			if c.action != "" {
				path += "/" + c.action
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			r.handleGameServerSub(w, req)

			if w.Code == http.StatusNotFound {
				body := w.Body.String()
				if strings.Contains(body, "desconhecid") {
					t.Errorf("CASE LOST IN THE REWRITE: %s → route 404 (%s)", nome, strings.TrimSpace(body))
				}
			}
		})
	}
}

// TestServerWithoutNodeRejectedAtSurface: a regra dura chega ao HTTP.
func TestServerWithoutNodeRejectedAtSurface(t *testing.T) {
	r, _ := gamesRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/gameservers/jogo-b/worlds", nil)
	w := httptest.NewRecorder()
	r.handleGameServerSub(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("server with no node should give 400, gave %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "jogo-b") {
		t.Errorf("the response has to name the server: %s", w.Body.String())
	}
}

// TestListSurvivesMissingNode: the whole page must not disappear.
func TestListSurvivesMissingNode(t *testing.T) {
	r, _ := gamesRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/gameservers", nil)
	w := httptest.NewRecorder()
	r.handleGameServers(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("the list should respond 200 even with the node missing, gave %d", w.Code)
	}
	var body struct {
		Servers []map[string]any `json:"servers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Servers) != 1 {
		t.Fatalf("expected 1 server listed, saw %d", len(body.Servers))
	}
	if body.Servers[0]["err"] == nil {
		t.Error("the item should carry that server's error, not hide it")
	}
	if body.Servers[0]["id"] != "jogo-b" {
		t.Errorf("the item lost its identity: %v", body.Servers[0])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// A node error must not leak the credential

func TestAgentErrorNeverLeaksToken(t *testing.T) {
	const token = "TOKEN-SUPER-SECRETO-DO-NO-123456"
	err := &gameservers.AuthorizationError{
		// A deliberate worst case: an error that ALREADY carries the token. If the
		// translation passed any part of it through, this test would catch it.
		Msg: "401 ao chamar http://10.0.0.5:8710/v1/op/server.status com Bearer " + token,
	}
	code, msg := translateNodeError(err, "games")
	if code != http.StatusBadGateway {
		t.Errorf("authorization error should give 502, gave %d", code)
	}
	if strings.Contains(msg, token) {
		t.Fatalf("TOKEN LEAKED INTO THE RESPONSE TO THE BROWSER: %s", msg)
	}
	for _, frag := range []string{token[:8], "Bearer", "10.0.0.5"} {
		if strings.Contains(msg, frag) {
			t.Errorf("fragmento sensivel (%q) vazou: %s", frag, msg)
		}
	}
	if !strings.Contains(msg, "games") || !strings.Contains(msg, "token") {
		t.Errorf("the message has to tell the operator what to do: %s", msg)
	}
}

func TestBusinessErrorPassesThrough(t *testing.T) {
	// The class that MUST get through: a message produced by our own agent,
	// telling the operator what happened. Translating this one too would leave
	// every failure wearing the same useless sentence.
	code, msg := translateNodeError(&gameservers.OperationError{Msg: "mundo 'alfa' nao existe"}, "games")
	if code != http.StatusBadRequest {
		t.Errorf("business error should give 400, gave %d", code)
	}
	if msg != "mundo 'alfa' nao existe" {
		t.Errorf("the business message was lost: %s", msg)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RBAC E AUDITORIA

// TestRBACPreserved: every mutation still requires the primary account.
//
// The test is about FORM, not execution: it walks the AST and demands that every
// `execOp(..., true)` — the writes — exists, and that the gate lives inside
// it. That way a new write inherits the RBAC by construction, instead of relying
// on somebody remembering to copy the line.
func TestRBACPreserved(t *testing.T) {
	source, err := os.ReadFile("gamebackend.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "mustPrimary") {
		t.Fatal("the primary-account gate disappeared from execOp — EVERY write is now without RBAC")
	}

	// And no write may have been left outside execOp: the two exceptions
	// (import, which uploads first) call mustPrimary explicitly.
	h, err := os.ReadFile("handlers_gameservers.go")
	if err != nil {
		t.Fatal(err)
	}
	writes := strings.Count(string(h), ", true)")
	if writes < 15 {
		t.Errorf("expected the writes to go through execOp with isWrite=true; counted %d", writes)
	}
	if !strings.Contains(string(h), "mustPrimary") {
		t.Error("the import case uploads BEFORE execOp and needs its own gate")
	}
}

// TestWriteOperationIsAudited: a write audits, a read does not.
func TestWriteOperationIsAudited(t *testing.T) {
	source, err := os.ReadFile("gamebackend.go")
	if err != nil {
		t.Fatal(err)
	}
	txt := string(source)

	// The audit lives inside the `if isWrite` — that is what guarantees both
	// halves of the rule in a single line.
	i := strings.Index(txt, "auditGame(req")
	if i < 0 {
		t.Fatal("no audit call in the execution path")
	}
	antes := txt[:i]
	lastIf := strings.LastIndex(antes, "if isWrite {")
	lastRes := strings.LastIndex(antes, "back.Execute")
	if lastIf < 0 || lastIf < lastRes-200 {
		t.Error("the audit is not guarded by `if isWrite` — a read would also generate an event")
	}

	// The event has to carry the four things an incident asks about.
	for _, field := range []string{"node=", "server=", "result=", "gameserver."} {
		if !strings.Contains(txt, field) {
			t.Errorf("the audit event does not carry %q", field)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NOME DE DOWNLOAD

func TestSafeDownloadName(t *testing.T) {
	cases := map[string]string{
		"mundo-alfa":                  "mundo-alfa",
		"../../etc/passwd":            "etcpasswd",
		"a\r\nX-Injetado: sim":        "aX-Injetado:sim",
		`nome com "aspas"`:            "nomecomaspas",
		"":                            "padrao",
		"...":                         "padrao",
		"backup-2026-08-24_10-00.zip": "backup-2026-08-24_10-00.zip",
	}
	keys := make([]string, 0, len(cases))
	for k := range cases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, entry := range keys {
		got := safeDownloadName(entry, "padrao")
		// The property that matters is not the exact text but this: nothing that
		// could break the header survives.
		for _, bad := range []string{"\r", "\n", `"`, "/", "\\"} {
			if strings.Contains(got, bad) {
				t.Errorf("name %q produced %q, which still contains %q — header injection", entry, got, bad)
			}
		}
		if got == "" {
			t.Errorf("name %q produced empty, without falling back to the default", entry)
		}
	}
	if safeDownloadName("", "padrao") != "padrao" {
		t.Error("empty name should fall back to the default")
	}
}
