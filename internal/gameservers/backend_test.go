package gameservers

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server-control-panel/internal/inventory"
)

// ─────────────────────────────────────────────────────────────────────────────
// WHAT THIS HARNESS PROVES — AND WHAT IT DOES NOT
//
// IT PROVES: the CONTRACT. That the two back-ends, given the same envelope,
// return the same value and the same error class; that the Handle is opaque and
// never turns into a path; that the token never leaves the header; that the
// factory chooses by the node's transport.
//
// IT DOES NOT PROVE that the agent works live. The HTTP side brings the
// lab-agent up IN PROCESS (httptest.Server), with the local back-end behind it —
// there is no real network, no bridge, no bearer crossing a bind, no systemd.
// The proof against the real node, over the bridge's network, is a separate step.
//
// The distinction is not pedantry: "a green build is not a green live" was the
// false green paid for before, with four cases. An in-process harness is
// legitimate for parity — and mistaking it for proof of live is how you pay again.
//
// The HONEST LIMIT of the parity: the HTTP side talks to the SAME BackendLocal
// as the local side. So the test proves that the TRANSPORT does not alter the
// result — serialization, status codes, error classes, limits. It does NOT prove
// that two independent implementations of the same operation agree, because there
// are not two: the generic forwarder design (see the header of backend_http.go)
// is exactly what makes the second implementation nonexistent, and therefore
// impossible to diverge.
// ─────────────────────────────────────────────────────────────────────────────

// ambiente assembles a test Manager with a fake server, without docker and
// without a sampler (New() fires a StartSampler that would outlive the test).
func ambiente(t *testing.T) (*Manager, Server) {
	t.Helper()
	return ambienteEm(t, t.TempDir())
}

// ambienteEm allows two SIBLING environments in one test — needed in the
// mutating parity cases, where the two back-ends must not share a disk.
func ambienteEm(t *testing.T, raiz string) (*Manager, Server) {
	t.Helper()
	srv := Server{
		ID: "jogo-teste", Name: "Teste", Game: "enshrouded",
		Container: "nao-existe", Root: raiz,
	}
	if err := os.MkdirAll(filepath.Join(raiz, "worlds", "alfa"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A world in the format ImportWorld is required to recognize.
	for _, n := range []string{"11111-index", "11111", "11111_info"} {
		if err := os.WriteFile(filepath.Join(raiz, "worlds", "alfa", n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// enshrouded_server.json is THE TARGET OF THE ROUND-TRIP CRITERION. Without
	// it in the environment, `settings.get` and `settings.patch` would only return
	// "file does not exist" and the parity of the family that matters most here
	// would be parity of an error message — green with no content.
	cfgDir := filepath.Join(raiz, "data", "server")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// `gameSettings` has to HAVE the key the patch will change: the adapter's
	// allowlist refuses an unknown field, and it is that refusal the narrowing keeps.
	cfg := `{"name":"servidor de teste","password":"","slotCount":16,` +
		`"gameSettingsPreset":"Default",` +
		`"gameSettings":{"playerHealthFactor":1},` +
		`"userGroups":[` +
		`{"name":"Admin","password":"a","canKickBan":true,"canAccessInventories":true,` +
		`"canEditBase":true,"canExtendBase":true,"reservedSlots":0}]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "enshrouded_server.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// A minimal docker-compose.yml, so runtime.get returns options instead of
	// "could not find the compose".
	compose := "services:\n  enshrouded:\n    image: exemplo\n    environment:\n" +
		"      - BACKUP_MAX_COUNT=5\n      - RESTART_CRON=\n"
	if err := os.WriteFile(filepath.Join(raiz, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	// savegame of the active world — without it backup.create can only say "not
	// found", and the backup family would be proved through its error path alone.
	save := filepath.Join(cfgDir, "savegame")
	if err := os.MkdirAll(save, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(save, "11111-index"), []byte("save"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{path: filepath.Join(raiz, "gameservers.json"), servers: []Server{srv}}
	m.hist = newHistory(raiz)
	return m, srv
}

// duplaDeBackends returns the local back-end and an HTTP one pointing at an
// in-process lab-agent with the SAME local one behind it.
//
// Is the agent assembled by package reflection? No: labagent imports gameservers,
// so gameservers CANNOT import labagent (cycle). The harness reproduces the
// agent's routing here — the two routes the client uses — with one caveat: if the
// agent changes shape, it is TestContratoParidade that goes stale, and that is
// why the route test that counts lives in internal/labagent/exec_test.go.
func duplaDeBackends(t *testing.T, m *Manager) (*BackendLocal, *BackendHTTP, *httptest.Server, *[]string) {
	t.Helper()
	local := NovoBackendLocal(m, "no-teste")
	const token = "token-de-teste-nao-e-segredo"

	var urlsVistas []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/op/{op}", func(w http.ResponseWriter, r *http.Request) {
		urlsVistas = append(urlsVistas, r.URL.String())
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"erro": "nao autorizado"})
			return
		}
		nome := OpName(r.PathValue("op"))
		if !opConhecida(nome) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"erro": "unknown operation"})
			return
		}
		corpo, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		res, err := local.Executar(r.Context(), nome, json.RawMessage(corpo))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"erro": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(res)
	})
	mux.HandleFunc("GET /v1/artefato/{handle}", func(w http.ResponseWriter, r *http.Request) {
		urlsVistas = append(urlsVistas, r.URL.String())
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		rc, err := local.Abrir(r.Context(), Handle(r.PathValue("handle")))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"erro": err.Error()})
			return
		}
		defer rc.Close()
		_, _ = io.Copy(w, rc)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	remoto, err := NovoBackendHTTP(ts.URL, token, "no-teste")
	if err != nil {
		t.Fatalf("NovoBackendHTTP: %v", err)
	}
	return local, remoto, ts, &urlsVistas
}

// ─────────────────────────────────────────────────────────────────────────────
// THE CONTRACT BATTERY — one table, two back-ends

type casoContrato struct {
	op    OpName
	corpo string
	// mutante marks the cases that CHANGE state. They run against separate
	// environments, otherwise the second back-end would see the world the first
	// one left behind and the "divergence" would be nothing but execution order.
	mutante bool
}

// casosDoContrato covers ALL the catalog's operations.
//
// The coverage is checked by TestContratoCobreTodasAsOps against TodasAsOps —
// not by a hand-written count, which is itself the defect.
func casosDoContrato() []casoContrato {
	const alvo = `"servidor":"jogo-teste"`
	return []casoContrato{
		{op: OpServerList, corpo: `{}`},
		{op: OpServerStatus, corpo: `{` + alvo + `}`},
		{op: OpServerAction, corpo: `{` + alvo + `,"verbo":"start"}`, mutante: true},
		{op: OpServerLogs, corpo: `{` + alvo + `}`},

		{op: OpWorldList, corpo: `{` + alvo + `}`},
		{op: OpWorldSwitch, corpo: `{` + alvo + `,"mundo":"alfa"}`, mutante: true},
		{op: OpWorldExport, corpo: `{` + alvo + `,"mundo":"alfa"}`, mutante: true},
		{op: OpWorldImport, corpo: `{` + alvo + `,"nome":"beta","handle":"nao-existe"}`, mutante: true},
		{op: OpWorldRename, corpo: `{` + alvo + `,"de":"alfa","para":"gama"}`, mutante: true},
		{op: OpWorldDuplicate, corpo: `{` + alvo + `,"de":"alfa","para":"copia"}`, mutante: true},
		{op: OpWorldDelete, corpo: `{` + alvo + `,"mundo":"inexistente"}`, mutante: true},

		{op: OpSettingsGet, corpo: `{` + alvo + `}`},
		// settings.patch writes FOR REAL into enshrouded_server.json — the target of
		// the round-trip criterion, and the file that was made to go through
		// escreveAtomico. A case that only proved the error path would leave the
		// family that matters most here with no write parity.
		{op: OpSettingsPatch, corpo: `{` + alvo + `,"jogo":{"playerHealthFactor":1.5}}`, mutante: true},

		{op: OpRuntimeGet, corpo: `{` + alvo + `}`},
		{op: OpRuntimePatch, corpo: `{` + alvo + `,"patch":{"BACKUP_MAX_COUNT":"7"}}`, mutante: true},

		{op: OpBackupList, corpo: `{` + alvo + `}`},
		{op: OpBackupCreate, corpo: `{` + alvo + `}`, mutante: true},
		{op: OpBackupRestore, corpo: `{` + alvo + `,"arquivo":"nao-existe.zip"}`, mutante: true},
		{op: OpBackupDownload, corpo: `{` + alvo + `,"arquivo":"nao-existe.zip"}`},

		{op: OpTrainerStatus, corpo: `{}`},
		{op: OpTrainerApply, corpo: `{}`, mutante: true},
		{op: OpTrainerDesired, corpo: `{}`, mutante: true},

		{op: OpHistoryList, corpo: `{` + alvo + `,"horas":6}`},
	}
}

func TestContratoCobreTodasAsOps(t *testing.T) {
	vistas := map[OpName]bool{}
	for _, c := range casosDoContrato() {
		if vistas[c.op] {
			t.Errorf("operation %q appears twice in the table", c.op)
		}
		vistas[c.op] = true
	}
	for _, op := range TodasAsOps {
		if !vistas[op] {
			t.Errorf("OPERATION WITH NO CONTRACT CASE: %q — the catalog grew and the battery did not", op)
		}
	}
	for op := range vistas {
		if !opConhecida(op) {
			t.Errorf("contract case for an operation outside the catalog: %q", op)
		}
	}
}

// TestBackendLocalServeTodasAsOps closes the "constant declared, operation never
// implemented" hole: no catalog op may land in the switch's `default`.
//
// Failing is allowed (there is no docker in the test); landing in the default is not.
func TestBackendLocalServeTodasAsOps(t *testing.T) {
	m, _ := ambiente(t)
	b := NovoBackendLocal(m, "no-teste")
	for _, c := range casosDoContrato() {
		_, err := b.Executar(context.Background(), c.op, json.RawMessage(c.corpo))
		if err != nil && strings.Contains(err.Error(), naoImplementada) {
			t.Errorf("OPERATION NOT IMPLEMENTED in the local back end: %q", c.op)
		}
	}
}

// TestContratoParidade is the test this work exists to write.
func TestContratoParidade(t *testing.T) {
	for _, c := range casosDoContrato() {
		t.Run(string(c.op), func(t *testing.T) {
			// A READ operation runs against the SAME Manager on both back-ends:
			// same input, same state, exact comparison, with no normalization
			// that could hide a divergence.
			//
			// A MUTATING operation cannot share a disk — the second back-end
			// would inherit the first one's effect and the "divergence" would
			// measure the execution order. So two sibling environments go in, and
			// the only thing normalized is each sandbox's root, a test artifact.
			var raizA, raizB string
			var mA, mB *Manager
			if c.mutante {
				base := t.TempDir()
				raizA, raizB = filepath.Join(base, "a"), filepath.Join(base, "b")
				for _, d := range []string{raizA, raizB} {
					if err := os.MkdirAll(d, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				mA, _ = ambienteEm(t, raizA)
				mB, _ = ambienteEm(t, raizB)
			} else {
				mA, _ = ambiente(t)
				mB = mA
			}

			locA := NovoBackendLocal(mA, "no-teste")
			resLocal, errLocal := locA.Executar(context.Background(), c.op, json.RawMessage(c.corpo))

			_, remoto, _, _ := duplaDeBackends(t, mB)
			resHTTP, errHTTP := remoto.Executar(context.Background(), c.op, json.RawMessage(c.corpo))

			semRaiz := func(txt string) string {
				if raizA != "" {
					txt = strings.ReplaceAll(txt, raizA, "<RAIZ>")
					txt = strings.ReplaceAll(txt, raizB, "<RAIZ>")
				}
				return txt
			}

			if (errLocal == nil) != (errHTTP == nil) {
				t.Fatalf("DIVERGENCE on %q: local err=%v, http err=%v", c.op, errLocal, errHTTP)
			}
			if errLocal != nil {
				if semRaiz(errLocal.Error()) != semRaiz(errHTTP.Error()) {
					t.Errorf("MESSAGE DIVERGENCE on %q:\n  local: %s\n  http : %s",
						c.op, errLocal.Error(), errHTTP.Error())
				}
				return
			}
			if !mesmoJSON(t, json.RawMessage(semRaiz(string(resLocal))), json.RawMessage(semRaiz(string(resHTTP)))) {
				t.Errorf("RESULT DIVERGENCE on %q:\n  local: %s\n  http : %s",
					c.op, resLocal, resHTTP)
			}
		})
	}
}

// mesmoJSON compares semantically, ignoring fields that change with the clock or
// with a temporary path — comparing raw bytes would fail on noise and would teach
// people to switch the test off.
func mesmoJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("the local response is not JSON: %v (%s)", err, a)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("the http response is not JSON: %v (%s)", err, b)
	}
	return fmt.Sprint(normaliza(va)) == fmt.Sprint(normaliza(vb))
}

// normaliza zeroes the fields that are volatile by nature: the handle (random by
// definition) and a file stamped with the current second.
func normaliza(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for k, val := range m {
		switch k {
		case "handle":
			out[k] = "<opaco>"
		case "arquivo":
			out[k] = "<carimbado>"
		default:
			out[k] = normaliza(val)
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// HANDLE

func TestHandleNaoRevelaCaminho(t *testing.T) {
	m, srv := ambiente(t)
	b := NovoBackendLocal(m, "no-teste")

	res, err := b.Executar(context.Background(), OpWorldExport,
		json.RawMessage(`{"servidor":"jogo-teste","mundo":"alfa"}`))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(res, &env); err != nil || env.Handle == "" {
		t.Fatalf("export did not return a handle: %v (%s)", err, res)
	}

	// The real path, so we can look inside the handle for every plausible way of
	// hiding it.
	a, err := b.cofre.resolver(Handle(env.Handle), srv.ID)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if a.caminho == "" {
		t.Fatal("artifact with no path — the test would prove nothing")
	}

	candidatos := []string{env.Handle}
	if d, err := hex.DecodeString(env.Handle); err == nil {
		candidatos = append(candidatos, string(d))
	}
	for _, dec := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if d, err := dec.DecodeString(env.Handle); err == nil {
			candidatos = append(candidatos, string(d))
		}
	}
	// The handle ITSELF is hexadecimal: in it, a directory separator is already
	// proof that somebody put a path where a token belonged.
	if strings.ContainsAny(env.Handle, `/\`) {
		t.Errorf("THE HANDLE REVEALS A DIRECTORY SEPARATOR: %q", env.Handle)
	}

	// In the DECODINGS the criterion has to be a different one: 32 bytes of
	// crypto/rand contain the byte 0x2f ('/') often enough, and failing on that
	// would be a false positive — the pin would be flaky and somebody would switch
	// it off. What matters is whether any RECOGNIZABLE PIECE of the real path shows up.
	pedacos := []string{filepath.Base(a.caminho), os.TempDir()}
	for _, seg := range strings.Split(a.caminho, string(os.PathSeparator)) {
		if len(seg) >= 4 {
			pedacos = append(pedacos, seg)
		}
	}
	for _, c := range candidatos {
		for _, ped := range pedacos {
			if ped != "" && strings.Contains(c, ped) {
				t.Errorf("THE HANDLE REVEALS PART OF THE PATH (%q) in %q", ped, c)
			}
		}
	}
}

func TestHandleForjadoEhRecusado(t *testing.T) {
	m, _ := ambiente(t)
	b := NovoBackendLocal(m, "no-teste")

	forjados := []struct {
		nome string
		h    Handle
	}{
		{"string arbitraria", "eu-inventei-este"},
		{"vazio", ""},
		{"caminho real absoluto", "/etc/passwd"},
		{"caminho relativo com travessia", "../../etc/passwd"},
		{"caminho do proprio inventario", Handle(m.path)},
		{"hex com o tamanho certo", Handle(strings.Repeat("ab", 32))},
	}
	for _, f := range forjados {
		t.Run(f.nome, func(t *testing.T) {
			rc, err := b.Abrir(context.Background(), f.h)
			if err == nil {
				rc.Close()
				t.Fatalf("FORGED HANDLE ACCEPTED (%q) — this is arbitrary file read", f.h)
			}
			if !errors.Is(err, ErroHandleInvalido) {
				t.Errorf("a forged handle should have given ErroHandleInvalido, gave: %v", err)
			}
		})
	}
}

// TestHandleEhEscopadoPorServidor: one server's handle does not resolve for another.
func TestHandleEhEscopadoPorServidor(t *testing.T) {
	m, _ := ambiente(t)
	b := NovoBackendLocal(m, "no-teste")

	res, err := b.Executar(context.Background(), OpWorldExport,
		json.RawMessage(`{"servidor":"jogo-teste","mundo":"alfa"}`))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env struct {
		Handle string `json:"handle"`
	}
	_ = json.Unmarshal(res, &env)

	if _, err := b.cofre.resolver(Handle(env.Handle), "jogo-teste"); err != nil {
		t.Fatalf("the handle does not resolve to its own server: %v", err)
	}
	if _, err := b.cofre.resolver(Handle(env.Handle), "outro-jogo"); err == nil {
		t.Error("THE HANDLE CROSSED THE SCOPE: it resolved to a server that is not the owner")
	}
}

// TestHandleExpira uses an injected clock — a criterion you satisfy by waiting N
// minutes is a defect in the criterion, not a step to be carried out.
func TestHandleExpira(t *testing.T) {
	c := novoCofre(10 * time.Minute)
	agora := time.Unix(1_700_000_000, 0)
	c.agora = func() time.Time { return agora }

	arq := filepath.Join(t.TempDir(), "artefato.bin")
	if err := os.WriteFile(arq, []byte("dados"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := c.Cunhar("jogo-teste", arq, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.resolver(h, "jogo-teste"); err != nil {
		t.Fatalf("a freshly minted handle should resolve: %v", err)
	}
	agora = agora.Add(11 * time.Minute)
	if _, err := c.resolver(h, "jogo-teste"); err == nil {
		t.Error("THE HANDLE DID NOT EXPIRE: a file-read token valid forever")
	}
}

// TestHandleEfemeroApagaAoFechar: the export zip must not leak into /tmp.
func TestHandleEfemeroApagaAoFechar(t *testing.T) {
	m, _ := ambiente(t)
	b := NovoBackendLocal(m, "no-teste")

	res, err := b.Executar(context.Background(), OpWorldExport,
		json.RawMessage(`{"servidor":"jogo-teste","mundo":"alfa"}`))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env struct {
		Handle string `json:"handle"`
	}
	_ = json.Unmarshal(res, &env)
	a, err := b.cofre.resolver(Handle(env.Handle), "jogo-teste")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := b.Abrir(context.Background(), Handle(env.Handle))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatal(err)
	}
	rc.Close()
	if _, err := os.Stat(a.caminho); !os.IsNotExist(err) {
		t.Errorf("TEMPORARY ZIP LEAKED: %s still exists after the download", a.caminho)
	}
	if _, err := b.cofre.resolver(Handle(env.Handle), "jogo-teste"); err == nil {
		t.Error("the ephemeral handle stayed valid after being served")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP BACK-END

func TestBackendHTTPNaoEnviaTokenEmQuery(t *testing.T) {
	m, _ := ambiente(t)
	_, remoto, _, urls := duplaDeBackends(t, m)

	_, _ = remoto.Executar(context.Background(), OpServerList, json.RawMessage(`{}`))
	if len(*urls) == 0 {
		t.Fatal("no request arrived — the test would prove nothing")
	}
	for _, u := range *urls {
		if strings.Contains(u, remoto.token) {
			t.Errorf("TOKEN IN THE URL: %q — it leaks into access logs, Referer and history", u)
		}
		for _, chave := range []string{"token", "t=", "auth", "key", "bearer"} {
			if strings.Contains(strings.ToLower(u), chave) {
				t.Errorf("URL with a parameter that looks like a credential (%q): %q", chave, u)
			}
		}
	}
}

func TestBackendHTTPPropagaErroDeAuth(t *testing.T) {
	m, _ := ambiente(t)
	_, remoto, ts, _ := duplaDeBackends(t, m)

	errado, err := NovoBackendHTTP(ts.URL, "token-errado", "no-teste")
	if err != nil {
		t.Fatal(err)
	}
	_, err = errado.Executar(context.Background(), OpServerList, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("the wrong token should fail")
	}
	var auth *ErroAutorizacao
	if !errors.As(err, &auth) {
		t.Errorf("401 DISGUISED AS AN OPERATION FAILURE: %v (%T) — they are opposite operator actions", err, err)
	}
	var oper *ErroOperacao
	if errors.As(err, &oper) {
		t.Error("an authorization error matched as ErroOperacao too — the distinction does not actually exist")
	}
	// And the right token keeps working, otherwise the test above would pass
	// because everything was broken.
	if _, err := remoto.Executar(context.Background(), OpServerList, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("the right token should work: %v", err)
	}
}

func TestBackendHTTPRecusaOpForaDoCatalogo(t *testing.T) {
	m, _ := ambiente(t)
	_, remoto, _, urls := duplaDeBackends(t, m)

	_, err := remoto.Executar(context.Background(), OpName("manutencao.qualquercoisa"), json.RawMessage(`{}`))
	var desconhecida *ErroOperacaoDesconhecida
	if !errors.As(err, &desconhecida) {
		t.Fatalf("expected ErroOperacaoDesconhecida, got: %v (%T)", err, err)
	}
	if len(*urls) != 0 {
		t.Errorf("the client DIALED an operation outside the catalog: %v", *urls)
	}
}

func TestBackendHTTPExigeToken(t *testing.T) {
	if _, err := NovoBackendHTTP("http://127.0.0.1:1", "", "no-teste"); err == nil {
		t.Error("an http back end with no token should fail at construction")
	}
	if _, err := NovoBackendHTTP("", "tok", "no-teste"); err == nil {
		t.Error("an empty base should fail")
	}
	if _, err := NovoBackendHTTP("nao-e-url", "tok", "no-teste"); err == nil {
		t.Error("a base with no scheme should fail")
	}
}

func TestBackendHTTPTimeoutEBodyLimitado(t *testing.T) {
	t.Run("giant response", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			bloco := make([]byte, 1<<20)
			for i := 0; i < (respostaMax/len(bloco))+2; i++ {
				_, _ = w.Write(bloco)
			}
		}))
		defer ts.Close()
		b, err := NovoBackendHTTP(ts.URL, "tok", "no-teste")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Executar(context.Background(), OpServerList, json.RawMessage(`{}`)); err == nil {
			t.Error("a response over the limit should become an error, not consumed memory")
		}
	})

	t.Run("slow response", func(t *testing.T) {
		liberado := make(chan struct{})
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-liberado:
			case <-r.Context().Done():
			}
		}))
		defer func() { close(liberado); ts.Close() }()

		b, err := NovoBackendHTTP(ts.URL, "tok", "no-teste")
		if err != nil {
			t.Fatal(err)
		}
		// A short deadline from the caller's context: the client's 60 s ceiling is
		// the safety floor, not something a test should sit and wait for.
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		inicio := time.Now()
		if _, err := b.Executar(ctx, OpServerList, json.RawMessage(`{}`)); err == nil {
			t.Error("a slow response should become an error, not a hang")
		}
		if d := time.Since(inicio); d > 5*time.Second {
			t.Errorf("the client hung %s past the context deadline", d)
		}
	})
}

// TestBackendHTTPNaoTemNomeDeOperacaoLiteral is the required pin: the names come
// from the catalog, never from a loose string in this file.
//
// With the generic forwarder design there is no operation name here at all — the
// test still exists because it protects the design, not the current state: the
// first per-operation function anybody adds will fail it.
func TestBackendHTTPNaoTemNomeDeOperacaoLiteral(t *testing.T) {
	b, err := os.ReadFile("backend_http.go")
	if err != nil {
		t.Fatal(err)
	}
	familias := make([]string, 0, len(FamiliasValidas))
	for f := range FamiliasValidas {
		familias = append(familias, `"`+f+`.`)
	}
	for i, linha := range strings.Split(string(b), "\n") {
		corte := strings.TrimSpace(linha)
		if strings.HasPrefix(corte, "//") {
			continue
		}
		for _, f := range familias {
			if strings.Contains(linha, f) {
				t.Errorf("LITERAL OPERATION NAME in backend_http.go:%d — use the OpName constants: %s", i+1, corte)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// FACTORY

func TestSelecaoPorTransport(t *testing.T) {
	m, _ := ambiente(t)

	// The values duplicated as strings here have to be the same ones in inventory.
	// Without this assertion, the duplication would diverge at the first rename.
	if TransporteAgente != string(inventory.TransportAgente) ||
		TransportePVEAPI != string(inventory.TransportPVEAPI) ||
		TransporteSSH != string(inventory.TransportSSH) {
		t.Fatalf("the duplicated transports drifted out of sync with internal/inventory")
	}

	casos := []struct {
		nome      string
		d         DestinoNo
		querTipo  string
		queroErro bool
	}{
		{"agente vira http", DestinoNo{Nome: "games", Transport: TransporteAgente, Base: "http://127.0.0.1:9977", Token: "tok"}, "*gameservers.BackendHTTP", false},
		{"pve-api vira local", DestinoNo{Nome: "apps", Transport: TransportePVEAPI}, "*gameservers.BackendLocal", false},
		{"ssh vira local", DestinoNo{Nome: "dev", Transport: TransporteSSH}, "*gameservers.BackendLocal", false},
		{"vazio e erro", DestinoNo{Nome: "orfao"}, "", true},
		{"invalido e erro", DestinoNo{Nome: "torto", Transport: "banana"}, "", true},
		{"agente sem token e erro", DestinoNo{Nome: "games", Transport: TransporteAgente, Base: "http://127.0.0.1:9977"}, "", true},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			b, err := NovoBackend(c.d, m)
			if c.queroErro {
				if err == nil {
					t.Fatalf("expected an error, got %T", b)
				}
				if c.d.Transport != "" && !strings.Contains(err.Error(), c.d.Transport) && c.d.Transport != TransporteAgente {
					t.Errorf("the error should NAME the value %q: %v", c.d.Transport, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("did not expect an error: %v", err)
			}
			if got := fmt.Sprintf("%T", b); got != c.querTipo {
				t.Errorf("transport %q should give %s, gave %s", c.d.Transport, c.querTipo, got)
			}
		})
	}
}

// TestBackendsSatisfazemAInterface is a compile-time assertion plus the guarantee
// that Descrever tells the two apart — a back-end that cannot say who it is
// shows up in the log as the other one.
var (
	_ Backend = (*BackendLocal)(nil)
	_ Backend = (*BackendHTTP)(nil)
)

func TestBackendsSeIdentificam(t *testing.T) {
	m, _ := ambiente(t)
	local, remoto, _, _ := duplaDeBackends(t, m)
	if local.Descrever() == remoto.Descrever() {
		t.Errorf("the two back ends describe themselves identically (%q) — in the log they are indistinguishable", local.Descrever())
	}
	if !strings.HasPrefix(local.Descrever(), "local:") || !strings.HasPrefix(remoto.Descrever(), "http:") {
		t.Errorf("the descriptions do not say the transport: %q and %q", local.Descrever(), remoto.Descrever())
	}
}

// TestParidadeDeEfeitoNoDisco closes the hole that RESPONSE parity does not see.
//
// Two back-ends can return the same `{"ok":true}` and leave the disk different —
// and it is the disk the criterion measures (`stat`), not the response. This test
// runs the same write through both transports, in sibling sandboxes, and compares
// the BYTES and the MODE of the resulting file.
func TestParidadeDeEfeitoNoDisco(t *testing.T) {
	base := t.TempDir()
	raizA, raizB := filepath.Join(base, "a"), filepath.Join(base, "b")
	for _, d := range []string{raizA, raizB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mA, _ := ambienteEm(t, raizA)
	mB, _ := ambienteEm(t, raizB)

	const patch = `{"servidor":"jogo-teste","jogo":{"playerHealthFactor":1.5}}`

	local := NovoBackendLocal(mA, "no-teste")
	if _, err := local.Executar(context.Background(), OpSettingsPatch, json.RawMessage(patch)); err != nil {
		t.Fatalf("local write: %v", err)
	}
	_, remoto, _, _ := duplaDeBackends(t, mB)
	if _, err := remoto.Executar(context.Background(), OpSettingsPatch, json.RawMessage(patch)); err != nil {
		t.Fatalf("http write: %v", err)
	}

	alvoA := filepath.Join(raizA, "data", "server", "enshrouded_server.json")
	alvoB := filepath.Join(raizB, "data", "server", "enshrouded_server.json")

	bytesA, err := os.ReadFile(alvoA)
	if err != nil {
		t.Fatal(err)
	}
	bytesB, err := os.ReadFile(alvoB)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytesA) != string(bytesB) {
		t.Errorf("EFFECT DIVERGENCE: the same patch left different files\n--- local ---\n%s\n--- http ---\n%s",
			bytesA, bytesB)
	}
	// The write must actually have happened — comparing two untouched files would
	// also come out "equal".
	if !strings.Contains(string(bytesA), "1.5") {
		t.Errorf("the patch did not reach the disk: %s", bytesA)
	}
	if !strings.Contains(string(bytesA), `"Custom"`) {
		t.Errorf("the adapter did not force gameSettingsPreset=Custom — business rule lost in the extraction")
	}

	stA, err := os.Stat(alvoA)
	if err != nil {
		t.Fatal(err)
	}
	stB, err := os.Stat(alvoB)
	if err != nil {
		t.Fatal(err)
	}
	if stA.Mode().Perm() != stB.Mode().Perm() {
		t.Errorf("MODE DIVERGENCE: local %04o, http %04o", stA.Mode().Perm(), stB.Mode().Perm())
	}
	// escreveAtomico preserves the mode of the file that already existed (0644 in
	// the environment). If this changes, the container reads the config with the
	// wrong permission.
	if stA.Mode().Perm() != 0o644 {
		t.Errorf("MODE NOT PRESERVED on write: %04o, expected 0644", stA.Mode().Perm())
	}
}
