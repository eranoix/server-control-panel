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

var os27Case = []struct {
	line     int
	resource string
	action   string
	endpoint bool
	note     string
}{
	{72, "action", "", true, ""},
	{84, "action", "", false, "start|stop|restart became values of Verb"},
	{95, "connection", "", true, "field of server.status"},
	{98, "groups", "", true, "settings section"},
	{131, "bans", "", true, "settings section"},
	{157, "history", "", true, ""},
	{166, "build", "", true, "field of server.status"},
	{169, "update", "", true, "verb of server.action"},
	{179, "rawconfig", "", true, "narrowed by the contract"},
	{209, "trainer", "", true, "covers status, apply and desired"},
	{254, "runtime", "", true, ""},
	{280, "server", "", true, ""},
	{320, "worlds", "", false, "family header; the route is the empty action (322)"},
	{322, "worlds", "", true, ""},
	{333, "worlds", "switch", true, ""},
	{350, "worlds", "export", true, "path became a Handle"},
	{362, "worlds", "import", true, "upload became Receive + Handle"},
	{400, "worlds", "rename", true, ""},
	{417, "worlds", "duplicate", true, ""},
	{434, "worlds", "delete", true, ""},
	{455, "settings", "", true, ""},
	{498, "backups", "", false, "family header; the route is the empty action (500)"},
	{500, "backups", "", true, ""},
	{511, "backups", "create", true, ""},
	{522, "backups", "restore", true, ""},
	{552, "backups", "download", true, "path became a Handle"},
	{568, "logs", "", true, ""},
}

var subActionsOfLine209 = []string{"apply", "desired"}

func gamesRouter(t *testing.T) (*Router, string) {
	t.Helper()
	dir := t.TempDir()
	inv := `[{"id":"game-b","name":"Enshrouded","game":"enshrouded","container":"game-b","root":"` + dir + `","node":""}]`
	if err := os.WriteFile(filepath.Join(dir, "gameservers.json"), []byte(inv), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Router{cfg: &config.Config{DataDir: dir, Primary: "sam"}}
	r.gameMgr = gameservers.New(dir, nil)
	return r, dir
}

func TestEveryCaseHasDestination(t *testing.T) {
	r, _ := gamesRouter(t)

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
		req := httptest.NewRequest(http.MethodPost, "/api/gameservers/game-b/trainer/"+c, nil)
		w := httptest.NewRecorder()
		r.handleGameServerSub(w, req)
		if w.Code == http.StatusNotFound && strings.Contains(w.Body.String(), "unknown") {
			t.Errorf("SUB-ACTION LOST: trainer/%s → route 404 (%s)", c, strings.TrimSpace(w.Body.String()))
		}
	}

	for _, c := range os27Case {
		if !c.endpoint {
			continue
		}
		name := c.resource
		if c.action != "" {
			name += "/" + c.action
		}
		t.Run(name, func(t *testing.T) {
			path := "/api/gameservers/game-b/" + c.resource
			if c.action != "" {
				path += "/" + c.action
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			r.handleGameServerSub(w, req)

			if w.Code == http.StatusNotFound {
				body := w.Body.String()
				if strings.Contains(body, "unknown") {
					t.Errorf("CASE LOST IN THE REWRITE: %s → route 404 (%s)", name, strings.TrimSpace(body))
				}
			}
		})
	}
}

func TestServerWithoutNodeRejectedAtSurface(t *testing.T) {
	r, _ := gamesRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/gameservers/game-b/worlds", nil)
	w := httptest.NewRecorder()
	r.handleGameServerSub(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("server with no node should give 400, gave %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "game-b") {
		t.Errorf("the response has to name the server: %s", w.Body.String())
	}
}

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
	if body.Servers[0]["id"] != "game-b" {
		t.Errorf("the item lost its identity: %v", body.Servers[0])
	}
}

func TestAgentErrorNeverLeaksToken(t *testing.T) {
	const token = "TOKEN-TOP-SECRET-OF-NODE-123456"
	err := &gameservers.AuthorizationError{
		Msg: "401 calling http://10.0.0.5:8710/v1/op/server.status with Bearer " + token,
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
			t.Errorf("sensitive fragment (%q) leaked: %s", frag, msg)
		}
	}
	if !strings.Contains(msg, "games") || !strings.Contains(msg, "token") {
		t.Errorf("the message has to tell the operator what to do: %s", msg)
	}
}

func TestBusinessErrorPassesThrough(t *testing.T) {
	code, msg := translateNodeError(&gameservers.OperationError{Msg: "world 'alpha' does not exist"}, "games")
	if code != http.StatusBadRequest {
		t.Errorf("business error should give 400, gave %d", code)
	}
	if msg != "world 'alpha' does not exist" {
		t.Errorf("the business message was lost: %s", msg)
	}
}

func TestRBACPreserved(t *testing.T) {
	source, err := os.ReadFile("gamebackend.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "mustPrimary") {
		t.Fatal("the primary-account gate disappeared from execOp — EVERY write is now without RBAC")
	}

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

func TestWriteOperationIsAudited(t *testing.T) {
	source, err := os.ReadFile("gamebackend.go")
	if err != nil {
		t.Fatal(err)
	}
	txt := string(source)

	i := strings.Index(txt, "auditGame(req")
	if i < 0 {
		t.Fatal("no audit call in the execution path")
	}
	before := txt[:i]
	lastIf := strings.LastIndex(before, "if isWrite {")
	lastRes := strings.LastIndex(before, "back.Execute")
	if lastIf < 0 || lastIf < lastRes-200 {
		t.Error("the audit is not guarded by `if isWrite` — a read would also generate an event")
	}

	for _, field := range []string{"node=", "server=", "result=", "gameserver."} {
		if !strings.Contains(txt, field) {
			t.Errorf("the audit event does not carry %q", field)
		}
	}
}

func TestSafeDownloadName(t *testing.T) {
	cases := map[string]string{
		"world-alpha":                 "world-alpha",
		"../../etc/passwd":            "etcpasswd",
		"a\r\nX-Injected: yes":        "aX-Injected:yes",
		`name with "quotes"`:          "namewithquotes",
		"":                            "fallback",
		"...":                         "fallback",
		"backup-2026-08-24_10-00.zip": "backup-2026-08-24_10-00.zip",
	}
	keys := make([]string, 0, len(cases))
	for k := range cases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, entry := range keys {
		got := safeDownloadName(entry, "fallback")
		for _, bad := range []string{"\r", "\n", `"`, "/", "\\"} {
			if strings.Contains(got, bad) {
				t.Errorf("name %q produced %q, which still contains %q — header injection", entry, got, bad)
			}
		}
		if got == "" {
			t.Errorf("name %q produced empty, without falling back to the default", entry)
		}
	}
	if safeDownloadName("", "fallback") != "fallback" {
		t.Error("empty name should fall back to the default")
	}
}
