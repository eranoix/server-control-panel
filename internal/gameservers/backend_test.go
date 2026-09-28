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

func setupEnv(t *testing.T) (*Manager, Server) {
	t.Helper()
	return setupEnvAt(t, t.TempDir())
}

func setupEnvAt(t *testing.T, root string) (*Manager, Server) {
	t.Helper()
	srv := Server{
		ID: "test-game", Name: "Test", Game: "enshrouded",
		Container: "missing", Root: root,
	}
	if err := os.MkdirAll(filepath.Join(root, "worlds", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"11111-index", "11111", "11111_info"} {
		if err := os.WriteFile(filepath.Join(root, "worlds", "alpha", n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfgDir := filepath.Join(root, "data", "server")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"name":"test server","password":"","slotCount":16,` +
		`"gameSettingsPreset":"Default",` +
		`"gameSettings":{"playerHealthFactor":1},` +
		`"userGroups":[` +
		`{"name":"Admin","password":"a","canKickBan":true,"canAccessInventories":true,` +
		`"canEditBase":true,"canExtendBase":true,"reservedSlots":0}]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "enshrouded_server.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	compose := "services:\n  enshrouded:\n    image: example\n    environment:\n" +
		"      - BACKUP_MAX_COUNT=5\n      - RESTART_CRON=\n"
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	save := filepath.Join(cfgDir, "savegame")
	if err := os.MkdirAll(save, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(save, "11111-index"), []byte("save"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{path: filepath.Join(root, "gameservers.json"), servers: []Server{srv}}
	m.hist = newHistory(root)
	return m, srv
}

func backendPair(t *testing.T, m *Manager) (*BackendLocal, *BackendHTTP, *httptest.Server, *[]string) {
	t.Helper()
	local := NewBackendLocal(m, "test-node")
	const token = "test-token-not-a-secret"

	var seenURLs []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/op/{op}", func(w http.ResponseWriter, r *http.Request) {
		seenURLs = append(seenURLs, r.URL.String())
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized"})
			return
		}
		name := OpName(r.PathValue("op"))
		if !knownOp(name) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "unknown operation"})
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		res, err := local.Execute(r.Context(), name, json.RawMessage(body))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(res)
	})
	mux.HandleFunc("GET /v1/artifact/{handle}", func(w http.ResponseWriter, r *http.Request) {
		seenURLs = append(seenURLs, r.URL.String())
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		rc, err := local.Open(r.Context(), Handle(r.PathValue("handle")))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		defer rc.Close()
		_, _ = io.Copy(w, rc)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	remote, err := NewBackendHTTP(ts.URL, token, "test-node")
	if err != nil {
		t.Fatalf("NewBackendHTTP: %v", err)
	}
	return local, remote, ts, &seenURLs
}

type contractCase struct {
	op       OpName
	body     string
	mutating bool
}

func contractCases() []contractCase {
	const target = `"server_id":"test-game"`
	return []contractCase{
		{op: OpServerList, body: `{}`},
		{op: OpServerStatus, body: `{` + target + `}`},
		{op: OpServerAction, body: `{` + target + `,"verb":"start"}`, mutating: true},
		{op: OpServerLogs, body: `{` + target + `}`},

		{op: OpWorldList, body: `{` + target + `}`},
		{op: OpWorldSwitch, body: `{` + target + `,"world":"alpha"}`, mutating: true},
		{op: OpWorldExport, body: `{` + target + `,"world":"alpha"}`, mutating: true},
		{op: OpWorldImport, body: `{` + target + `,"name":"beta","handle":"missing"}`, mutating: true},
		{op: OpWorldRename, body: `{` + target + `,"from":"alpha","to":"gamma"}`, mutating: true},
		{op: OpWorldDuplicate, body: `{` + target + `,"from":"alpha","to":"copy"}`, mutating: true},
		{op: OpWorldDelete, body: `{` + target + `,"world":"nonexistent"}`, mutating: true},

		{op: OpSettingsGet, body: `{` + target + `}`},
		{op: OpSettingsPatch, body: `{` + target + `,"game":{"playerHealthFactor":1.5}}`, mutating: true},

		{op: OpRuntimeGet, body: `{` + target + `}`},
		{op: OpRuntimePatch, body: `{` + target + `,"patch":{"BACKUP_MAX_COUNT":"7"}}`, mutating: true},

		{op: OpBackupList, body: `{` + target + `}`},
		{op: OpBackupCreate, body: `{` + target + `}`, mutating: true},
		{op: OpBackupRestore, body: `{` + target + `,"file":"missing.zip"}`, mutating: true},
		{op: OpBackupDownload, body: `{` + target + `,"file":"missing.zip"}`},

		{op: OpTrainerStatus, body: `{}`},
		{op: OpTrainerApply, body: `{}`, mutating: true},
		{op: OpTrainerDesired, body: `{}`, mutating: true},

		{op: OpHistoryList, body: `{` + target + `,"hours":6}`},
	}
}

func TestContractCoversAllOps(t *testing.T) {
	seen := map[OpName]bool{}
	for _, c := range contractCases() {
		if seen[c.op] {
			t.Errorf("operation %q appears twice in the table", c.op)
		}
		seen[c.op] = true
	}
	for _, op := range AllOps {
		if !seen[op] {
			t.Errorf("OPERATION WITH NO CONTRACT CASE: %q — the catalog grew and the battery did not", op)
		}
	}
	for op := range seen {
		if !knownOp(op) {
			t.Errorf("contract case for an operation outside the catalog: %q", op)
		}
	}
}

func TestBackendLocalServesAllOps(t *testing.T) {
	m, _ := setupEnv(t)
	b := NewBackendLocal(m, "test-node")
	for _, c := range contractCases() {
		_, err := b.Execute(context.Background(), c.op, json.RawMessage(c.body))
		if err != nil && strings.Contains(err.Error(), notImplemented) {
			t.Errorf("OPERATION NOT IMPLEMENTED in the local back end: %q", c.op)
		}
	}
}

func TestContractParity(t *testing.T) {
	for _, c := range contractCases() {
		t.Run(string(c.op), func(t *testing.T) {
			var rootA, rootB string
			var mA, mB *Manager
			if c.mutating {
				base := t.TempDir()
				rootA, rootB = filepath.Join(base, "a"), filepath.Join(base, "b")
				for _, d := range []string{rootA, rootB} {
					if err := os.MkdirAll(d, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				mA, _ = setupEnvAt(t, rootA)
				mB, _ = setupEnvAt(t, rootB)
			} else {
				mA, _ = setupEnv(t)
				mB = mA
			}

			locA := NewBackendLocal(mA, "test-node")
			resLocal, errLocal := locA.Execute(context.Background(), c.op, json.RawMessage(c.body))

			_, remote, _, _ := backendPair(t, mB)
			resHTTP, errHTTP := remote.Execute(context.Background(), c.op, json.RawMessage(c.body))

			withoutRoot := func(txt string) string {
				if rootA != "" {
					txt = strings.ReplaceAll(txt, rootA, "<ROOT>")
					txt = strings.ReplaceAll(txt, rootB, "<ROOT>")
				}
				return txt
			}

			if (errLocal == nil) != (errHTTP == nil) {
				t.Fatalf("DIVERGENCE on %q: local err=%v, http err=%v", c.op, errLocal, errHTTP)
			}
			if errLocal != nil {
				if withoutRoot(errLocal.Error()) != withoutRoot(errHTTP.Error()) {
					t.Errorf("MESSAGE DIVERGENCE on %q:\n  local: %s\n  http : %s",
						c.op, errLocal.Error(), errHTTP.Error())
				}
				return
			}
			if !sameJSON(t, json.RawMessage(withoutRoot(string(resLocal))), json.RawMessage(withoutRoot(string(resHTTP)))) {
				t.Errorf("RESULT DIVERGENCE on %q:\n  local: %s\n  http : %s",
					c.op, resLocal, resHTTP)
			}
		})
	}
}

func sameJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("the local response is not JSON: %v (%s)", err, a)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("the http response is not JSON: %v (%s)", err, b)
	}
	return fmt.Sprint(normalize(va)) == fmt.Sprint(normalize(vb))
}

func normalize(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for k, val := range m {
		switch k {
		case "handle":
			out[k] = "<opaque>"
		case "file":
			out[k] = "<stamped>"
		default:
			out[k] = normalize(val)
		}
	}
	return out
}

func TestHandleDoesNotRevealPath(t *testing.T) {
	m, srv := setupEnv(t)
	b := NewBackendLocal(m, "test-node")

	res, err := b.Execute(context.Background(), OpWorldExport,
		json.RawMessage(`{"server_id":"test-game","world":"alpha"}`))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(res, &env); err != nil || env.Handle == "" {
		t.Fatalf("export did not return a handle: %v (%s)", err, res)
	}

	a, err := b.vault.resolver(Handle(env.Handle), srv.ID)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if a.path == "" {
		t.Fatal("artifact with no path — the test would prove nothing")
	}

	candidates := []string{env.Handle}
	if d, err := hex.DecodeString(env.Handle); err == nil {
		candidates = append(candidates, string(d))
	}
	for _, dec := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if d, err := dec.DecodeString(env.Handle); err == nil {
			candidates = append(candidates, string(d))
		}
	}
	if strings.ContainsAny(env.Handle, `/\`) {
		t.Errorf("THE HANDLE REVEALS A DIRECTORY SEPARATOR: %q", env.Handle)
	}

	pieces := []string{filepath.Base(a.path), os.TempDir()}
	for _, secret := range strings.Split(a.path, string(os.PathSeparator)) {
		if len(secret) >= 4 {
			pieces = append(pieces, secret)
		}
	}
	for _, c := range candidates {
		for _, piece := range pieces {
			if piece != "" && strings.Contains(c, piece) {
				t.Errorf("THE HANDLE REVEALS PART OF THE PATH (%q) in %q", piece, c)
			}
		}
	}
}

func TestForgedHandleIsRejected(t *testing.T) {
	m, _ := setupEnv(t)
	b := NewBackendLocal(m, "test-node")

	forged := []struct {
		name string
		h    Handle
	}{
		{"arbitrary string", "i-made-this-up"},
		{"empty", ""},
		{"real absolute path", "/etc/passwd"},
		{"relative path with traversal", "../../etc/passwd"},
		{"the inventory's own path", Handle(m.path)},
		{"hex of the right length", Handle(strings.Repeat("ab", 32))},
	}
	for _, f := range forged {
		t.Run(f.name, func(t *testing.T) {
			rc, err := b.Open(context.Background(), f.h)
			if err == nil {
				rc.Close()
				t.Fatalf("FORGED HANDLE ACCEPTED (%q) — this is arbitrary file read", f.h)
			}
			if !errors.Is(err, ErrHandleInvalid) {
				t.Errorf("a forged handle should have given ErrHandleInvalid, gave: %v", err)
			}
		})
	}
}

func TestHandleIsScopedByServer(t *testing.T) {
	m, _ := setupEnv(t)
	b := NewBackendLocal(m, "test-node")

	res, err := b.Execute(context.Background(), OpWorldExport,
		json.RawMessage(`{"server_id":"test-game","world":"alpha"}`))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env struct {
		Handle string `json:"handle"`
	}
	_ = json.Unmarshal(res, &env)

	if _, err := b.vault.resolver(Handle(env.Handle), "test-game"); err != nil {
		t.Fatalf("the handle does not resolve to its own server: %v", err)
	}
	if _, err := b.vault.resolver(Handle(env.Handle), "other-game"); err == nil {
		t.Error("THE HANDLE CROSSED THE SCOPE: it resolved to a server that is not the owner")
	}
}

func TestHandleExpires(t *testing.T) {
	c := newHandleVault(10 * time.Minute)
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }

	file := filepath.Join(t.TempDir(), "artifact.bin")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := c.Mint("test-game", file, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.resolver(h, "test-game"); err != nil {
		t.Fatalf("a freshly minted handle should resolve: %v", err)
	}
	now = now.Add(11 * time.Minute)
	if _, err := c.resolver(h, "test-game"); err == nil {
		t.Error("THE HANDLE DID NOT EXPIRE: a file-read token valid forever")
	}
}

func TestEphemeralHandleDeletedOnClose(t *testing.T) {
	m, _ := setupEnv(t)
	b := NewBackendLocal(m, "test-node")

	res, err := b.Execute(context.Background(), OpWorldExport,
		json.RawMessage(`{"server_id":"test-game","world":"alpha"}`))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var env struct {
		Handle string `json:"handle"`
	}
	_ = json.Unmarshal(res, &env)
	a, err := b.vault.resolver(Handle(env.Handle), "test-game")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := b.Open(context.Background(), Handle(env.Handle))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatal(err)
	}
	rc.Close()
	if _, err := os.Stat(a.path); !os.IsNotExist(err) {
		t.Errorf("TEMPORARY ZIP LEAKED: %s still exists after the download", a.path)
	}
	if _, err := b.vault.resolver(Handle(env.Handle), "test-game"); err == nil {
		t.Error("the ephemeral handle stayed valid after being served")
	}
}

func TestBackendHTTPNeverSendsTokenInQuery(t *testing.T) {
	m, _ := setupEnv(t)
	_, remote, _, urls := backendPair(t, m)

	_, _ = remote.Execute(context.Background(), OpServerList, json.RawMessage(`{}`))
	if len(*urls) == 0 {
		t.Fatal("no request arrived — the test would prove nothing")
	}
	for _, u := range *urls {
		if strings.Contains(u, remote.token) {
			t.Errorf("TOKEN IN THE URL: %q — it leaks into access logs, Referer and history", u)
		}
		for _, key := range []string{"token", "t=", "auth", "key", "bearer"} {
			if strings.Contains(strings.ToLower(u), key) {
				t.Errorf("URL with a parameter that looks like a credential (%q): %q", key, u)
			}
		}
	}
}

func TestBackendHTTPPropagatesAuthError(t *testing.T) {
	m, _ := setupEnv(t)
	_, remote, ts, _ := backendPair(t, m)

	wrong, err := NewBackendHTTP(ts.URL, "wrong-token", "test-node")
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrong.Execute(context.Background(), OpServerList, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("the wrong token should fail")
	}
	var auth *AuthorizationError
	if !errors.As(err, &auth) {
		t.Errorf("401 DISGUISED AS AN OPERATION FAILURE: %v (%T) — they are opposite operator actions", err, err)
	}
	var oper *OperationError
	if errors.As(err, &oper) {
		t.Error("an authorization error matched as OperationError too — the distinction does not actually exist")
	}
	if _, err := remote.Execute(context.Background(), OpServerList, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("the right token should work: %v", err)
	}
}

func TestBackendHTTPRejectsOpOutsideCatalog(t *testing.T) {
	m, _ := setupEnv(t)
	_, remote, _, urls := backendPair(t, m)

	_, err := remote.Execute(context.Background(), OpName("maintenance.anything"), json.RawMessage(`{}`))
	var unknown *UnknownOperationError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected UnknownOperationError, got: %v (%T)", err, err)
	}
	if len(*urls) != 0 {
		t.Errorf("the client DIALED an operation outside the catalog: %v", *urls)
	}
}

func TestBackendHTTPRequiresToken(t *testing.T) {
	if _, err := NewBackendHTTP("http://127.0.0.1:1", "", "test-node"); err == nil {
		t.Error("an http back end with no token should fail at construction")
	}
	if _, err := NewBackendHTTP("", "tok", "test-node"); err == nil {
		t.Error("an empty base should fail")
	}
	if _, err := NewBackendHTTP("not-a-url", "tok", "test-node"); err == nil {
		t.Error("a base with no scheme should fail")
	}
}

func TestBackendHTTPTimeoutAndBodyLimit(t *testing.T) {
	t.Run("giant response", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			block := make([]byte, 1<<20)
			for i := 0; i < (maxResponse/len(block))+2; i++ {
				_, _ = w.Write(block)
			}
		}))
		defer ts.Close()
		b, err := NewBackendHTTP(ts.URL, "tok", "test-node")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Execute(context.Background(), OpServerList, json.RawMessage(`{}`)); err == nil {
			t.Error("a response over the limit should become an error, not consumed memory")
		}
	})

	t.Run("slow response", func(t *testing.T) {
		released := make(chan struct{})
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-released:
			case <-r.Context().Done():
			}
		}))
		defer func() { close(released); ts.Close() }()

		b, err := NewBackendHTTP(ts.URL, "tok", "test-node")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		if _, err := b.Execute(ctx, OpServerList, json.RawMessage(`{}`)); err == nil {
			t.Error("a slow response should become an error, not a hang")
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("the client hung %s past the context deadline", d)
		}
	})
}

func TestBackendHTTPHasNoLiteralOperationName(t *testing.T) {
	b, err := os.ReadFile("backend_http.go")
	if err != nil {
		t.Fatal(err)
	}
	families := make([]string, 0, len(ValidFamilies))
	for f := range ValidFamilies {
		families = append(families, `"`+f+`.`)
	}
	for i, line := range strings.Split(string(b), "\n") {
		cut := strings.TrimSpace(line)
		if strings.HasPrefix(cut, "//") {
			continue
		}
		for _, f := range families {
			if strings.Contains(line, f) {
				t.Errorf("LITERAL OPERATION NAME in backend_http.go:%d — use the OpName constants: %s", i+1, cut)
			}
		}
	}
}

func TestSelectionByTransport(t *testing.T) {
	m, _ := setupEnv(t)

	if TransportAgent != string(inventory.TransportAgent) ||
		TransportPVEAPI != string(inventory.TransportPVEAPI) ||
		TransportSSH != string(inventory.TransportSSH) {
		t.Fatalf("the duplicated transports drifted out of sync with internal/inventory")
	}

	cases := []struct {
		name     string
		d        NodeTarget
		wantType string
		wantErr  bool
	}{
		{"agent becomes http", NodeTarget{Name: "games", Transport: TransportAgent, Base: "http://127.0.0.1:9977", Token: "tok"}, "*gameservers.BackendHTTP", false},
		{"pve-api becomes local", NodeTarget{Name: "apps", Transport: TransportPVEAPI}, "*gameservers.BackendLocal", false},
		{"ssh becomes local", NodeTarget{Name: "dev", Transport: TransportSSH}, "*gameservers.BackendLocal", false},
		{"empty is an error", NodeTarget{Name: "orphan"}, "", true},
		{"invalid is an error", NodeTarget{Name: "crooked", Transport: "banana"}, "", true},
		{"agent without token is an error", NodeTarget{Name: "games", Transport: TransportAgent, Base: "http://127.0.0.1:9977"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := NewBackend(c.d, m)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %T", b)
				}
				if c.d.Transport != "" && !strings.Contains(err.Error(), c.d.Transport) && c.d.Transport != TransportAgent {
					t.Errorf("the error should NAME the value %q: %v", c.d.Transport, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("did not expect an error: %v", err)
			}
			if got := fmt.Sprintf("%T", b); got != c.wantType {
				t.Errorf("transport %q should give %s, gave %s", c.d.Transport, c.wantType, got)
			}
		})
	}
}

var (
	_ Backend = (*BackendLocal)(nil)
	_ Backend = (*BackendHTTP)(nil)
)

func TestBackendsIdentifyThemselves(t *testing.T) {
	m, _ := setupEnv(t)
	local, remote, _, _ := backendPair(t, m)
	if local.Describe() == remote.Describe() {
		t.Errorf("the two back ends describe themselves identically (%q) — in the log they are indistinguishable", local.Describe())
	}
	if !strings.HasPrefix(local.Describe(), "local:") || !strings.HasPrefix(remote.Describe(), "http:") {
		t.Errorf("the descriptions do not say the transport: %q and %q", local.Describe(), remote.Describe())
	}
}

func TestDiskEffectParity(t *testing.T) {
	base := t.TempDir()
	rootA, rootB := filepath.Join(base, "a"), filepath.Join(base, "b")
	for _, d := range []string{rootA, rootB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mA, _ := setupEnvAt(t, rootA)
	mB, _ := setupEnvAt(t, rootB)

	const patch = `{"server_id":"test-game","game":{"playerHealthFactor":1.5}}`

	local := NewBackendLocal(mA, "test-node")
	if _, err := local.Execute(context.Background(), OpSettingsPatch, json.RawMessage(patch)); err != nil {
		t.Fatalf("local write: %v", err)
	}
	_, remote, _, _ := backendPair(t, mB)
	if _, err := remote.Execute(context.Background(), OpSettingsPatch, json.RawMessage(patch)); err != nil {
		t.Fatalf("http write: %v", err)
	}

	targetA := filepath.Join(rootA, "data", "server", "enshrouded_server.json")
	targetB := filepath.Join(rootB, "data", "server", "enshrouded_server.json")

	bytesA, err := os.ReadFile(targetA)
	if err != nil {
		t.Fatal(err)
	}
	bytesB, err := os.ReadFile(targetB)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytesA) != string(bytesB) {
		t.Errorf("EFFECT DIVERGENCE: the same patch left different files\n--- local ---\n%s\n--- http ---\n%s",
			bytesA, bytesB)
	}
	if !strings.Contains(string(bytesA), "1.5") {
		t.Errorf("the patch did not reach the disk: %s", bytesA)
	}
	if !strings.Contains(string(bytesA), `"Custom"`) {
		t.Errorf("the adapter did not force gameSettingsPreset=Custom — business rule lost in the extraction")
	}

	stA, err := os.Stat(targetA)
	if err != nil {
		t.Fatal(err)
	}
	stB, err := os.Stat(targetB)
	if err != nil {
		t.Fatal(err)
	}
	if stA.Mode().Perm() != stB.Mode().Perm() {
		t.Errorf("MODE DIVERGENCE: local %04o, http %04o", stA.Mode().Perm(), stB.Mode().Perm())
	}
	if stA.Mode().Perm() != 0o644 {
		t.Errorf("MODE NOT PRESERVED on write: %04o, expected 0644", stA.Mode().Perm())
	}
}
