package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const diaFixo = "2026-08-06"

// novoHandler returns a handler with its sink in a temp directory and a frozen
// clock, plus the path of the day's file.
func novoHandler(t *testing.T) (http.HandlerFunc, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewSink(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	congelar(s, diaFixo)
	return Handler(s, "vps-manager"), filepath.Join(dir, diaFixo+".jsonl")
}

// conteudo returns the day's file, or "" if it never even came into existence.
func conteudo(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contaLinhas(s string) int {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

func post(h http.HandlerFunc, ct, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/telemetry", strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestHandler(t *testing.T) {
	// 1. Valid batch with 2 events → 204 and 2 lines, with the server's ts, the
	//    build's fork and the sid from the body.
	t.Run("valid-batch-2-events", func(t *testing.T) {
		h, p := novoHandler(t)
		antes := time.Now().Add(-time.Second)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.codigo","origin":"nav"},{"screen":"docker.containers.logs","origin":"default"}],"dropped":0}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		c := conteudo(t, p)
		if n := contaLinhas(c); n != 2 {
			t.Fatalf("expected=2 observed=%d lines; content=%q", n, c)
		}
		querSc := []string{"dev.codigo", "docker.containers.logs"}
		querOr := []string{"nav", "default"}
		for i, l := range strings.Split(strings.TrimSuffix(c, "\n"), "\n") {
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatalf("line %d is not JSON: %q", i+1, l)
			}
			if m["screen"] != querSc[i] {
				t.Errorf("line %d screen: expected=%q observed=%v", i+1, querSc[i], m["screen"])
			}
			if m["origin"] != querOr[i] {
				t.Errorf("line %d origin: expected=%q observed=%v", i+1, querOr[i], m["origin"])
			}
			if m["sid"] != "9f3a1c72" {
				t.Errorf("line %d sid: expected=9f3a1c72 observed=%v", i+1, m["sid"])
			}
			if m["fork"] != "vps-manager" {
				t.Errorf("line %d fork: expected=vps-manager observed=%v", i+1, m["fork"])
			}
			if v, ok := m["v"].(float64); !ok || v != 1 {
				t.Errorf("line %d v: expected=1 observed=%v", i+1, m["v"])
			}
			ts, ok := m["ts"].(string)
			if !ok {
				t.Fatalf("line %d with no ts: %q", i+1, l)
			}
			pt, err := time.Parse(time.RFC3339Nano, ts)
			if err != nil {
				t.Errorf("line %d ts is not RFC3339: %q", i+1, ts)
			} else if pt.Before(antes) || pt.After(time.Now().Add(time.Second)) {
				t.Errorf("line %d ts is not the server's (outside the window): %q", i+1, ts)
			}
			// The schema is closed at 6 fields: nothing else can leak out.
			if len(m) != 6 {
				t.Errorf("line %d has %d fields, expected=6: %q", i+1, len(m), l)
			}
		}
	})

	// 2. Body above 64 KiB → 400 and zero lines.
	t.Run("body-over-64kib", func(t *testing.T) {
		h, p := novoHandler(t)
		gordo := strings.Repeat("a", 70000)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"`+gordo+`","origin":"nav"}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if n := contaLinhas(conteudo(t, p)); n != 0 {
			t.Fatalf("a refused body wrote %d line(s)", n)
		}
	})

	// 3. Batch with 201 events → 400 and zero lines.
	t.Run("batch-201-events", func(t *testing.T) {
		h, p := novoHandler(t)
		evs := make([]string, 201)
		for i := range evs {
			evs[i] = `{"screen":"dev.codigo","origin":"nav"}`
		}
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[`+strings.Join(evs, ",")+`]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if n := contaLinhas(conteudo(t, p)); n != 0 {
			t.Fatalf("a refused batch wrote %d line(s)", n)
		}
	})

	// 3b. The limit itself: 200 passes.
	t.Run("batch-of-200-events-passes", func(t *testing.T) {
		h, p := novoHandler(t)
		evs := make([]string, 200)
		for i := range evs {
			evs[i] = `{"screen":"dev.codigo","origin":"nav"}`
		}
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[`+strings.Join(evs, ",")+`]}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		if n := contaLinhas(conteudo(t, p)); n != 200 {
			t.Fatalf("expected=200 observed=%d lines", n)
		}
	})

	// 4. origin outside {default,nav} → 400 and zero lines.
	t.Run("invalid-origin", func(t *testing.T) {
		for _, org := range []string{"NAV", "", "click", "nav\n", "default "} {
			h, p := novoHandler(t)
			b, _ := json.Marshal(map[string]any{
				"v": 1, "s": "9f3a1c72",
				"e": []map[string]string{{"screen": "dev.codigo", "origin": org}},
			})
			rec := post(h, "application/json", string(b))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("origin=%q: expected=400 observed=%d", org, rec.Code)
			}
			if n := contaLinhas(conteudo(t, p)); n != 0 {
				t.Errorf("origin=%q: wrote %d line(s)", org, n)
			}
		}
	})

	// 5. sid outside ^[a-f0-9]{8,32}$ → 400 and zero lines.
	t.Run("malformed-sid", func(t *testing.T) {
		for _, sid := range []string{
			"", "1234567", "9F3A1C72", "9f3a1c7g", "../../etc/passwd",
			strings.Repeat("a", 33), "9f3a1c72\n", " 9f3a1c72",
		} {
			h, p := novoHandler(t)
			b, _ := json.Marshal(map[string]any{
				"v": 1, "s": sid,
				"e": []map[string]string{{"screen": "dev.codigo", "origin": "nav"}},
			})
			rec := post(h, "application/json", string(b))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("sid=%q: expected=400 observed=%d", sid, rec.Code)
			}
			if n := contaLinhas(conteudo(t, p)); n != 0 {
				t.Errorf("sid=%q: wrote %d line(s)", sid, n)
			}
		}
		// And the valid format passes at both extremes.
		for _, sid := range []string{"abcdef01", strings.Repeat("0f", 16)} {
			h, _ := novoHandler(t)
			b, _ := json.Marshal(map[string]any{
				"v": 1, "s": sid,
				"e": []map[string]string{{"screen": "dev.codigo", "origin": "nav"}},
			})
			if rec := post(h, "application/json", string(b)); rec.Code != http.StatusNoContent {
				t.Errorf("valid sid %q: expected=204 observed=%d", sid, rec.Code)
			}
		}
	})

	// 6. screen outside the allowlist → 204, 1 line holding "unknown", and the
	//    raw id does NOT appear anywhere in the file.
	t.Run("unknown-screen-becomes-unknown", func(t *testing.T) {
		h, p := novoHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"nao-existe-essa-tela","origin":"nav"}]}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		c := conteudo(t, p)
		if n := contaLinhas(c); n != 1 {
			t.Fatalf("expected=1 observed=%d lines", n)
		}
		if got := strings.Count(c, `"screen":"unknown"`); got != 1 {
			t.Errorf(`"screen":"unknown": esperado=1 observado=%d; conteúdo=%q`, got, c)
		}
		if strings.Contains(c, "nao-existe-essa-tela") {
			t.Errorf("the raw id was written — log poisoning: %q", c)
		}
	})

	// 6b. Hostile payload in screen: none of it may touch the file.
	t.Run("hostile-screen-does-not-touch-the-file", func(t *testing.T) {
		for _, sc := range []string{
			`<script>alert(1)</script>`,
			`../../etc/passwd`,
			`dev.codigo","injetado":"sim`,
			`DOCKER.CONTAINERS`,
		} {
			h, p := novoHandler(t)
			b, _ := json.Marshal(map[string]any{
				"v": 1, "s": "9f3a1c72",
				"e": []map[string]string{{"screen": sc, "origin": "nav"}},
			})
			rec := post(h, "application/json", string(b))
			if rec.Code != http.StatusNoContent {
				t.Errorf("screen=%q: expected=204 observed=%d", sc, rec.Code)
			}
			c := conteudo(t, p)
			if strings.Contains(c, "script") || strings.Contains(c, "passwd") ||
				strings.Contains(c, "injetado") || strings.Contains(c, "DOCKER") {
				t.Errorf("screen=%q leaked into the file: %q", sc, c)
			}
			if n := contaLinhas(c); n != 1 {
				t.Errorf("screen=%q: expected=1 observed=%d lines", sc, n)
			}
			// The record has to stay one line and valid JSON.
			var m map[string]any
			l := strings.TrimSuffix(c, "\n")
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Errorf("screen=%q broke the JSONL: %q", sc, c)
			} else if m["screen"] != "unknown" {
				t.Errorf("screen=%q: expected=unknown observed=%v", sc, m["screen"])
			}
		}
	})

	// 7. Extra field in the event: the value must not appear in the JSONL.
	//    DisallowUnknownFields applies recursively, so the whole batch is a 400 —
	//    and the file is left with zero lines, which is even stronger than "the
	//    value does not appear".
	t.Run("extra-field-in-the-event", func(t *testing.T) {
		h, p := novoHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.codigo","origin":"nav","evil":"<script>"}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		c := conteudo(t, p)
		if strings.Contains(c, "evil") || strings.Contains(c, "<script>") {
			t.Fatalf("a free-form field reached the JSONL: %q", c)
		}
		if n := contaLinhas(c); n != 0 {
			t.Fatalf("expected=0 observed=%d lines", n)
		}
	})

	// 7b. Extra field in the BATCH (not in the event) — same rule.
	t.Run("extra-field-in-the-batch", func(t *testing.T) {
		h, p := novoHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.codigo","origin":"nav"}],"evil":"x"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if c := conteudo(t, p); strings.Contains(c, "evil") || contaLinhas(c) != 0 {
			t.Fatalf("a batch with a free-form field wrote: %q", c)
		}
	})

	// 8. GET → 405.
	t.Run("method-get-405", func(t *testing.T) {
		h, p := novoHandler(t)
		req := httptest.NewRequest(http.MethodGet, "/api/telemetry", nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected=405 observed=%d", rec.Code)
		}
		if n := contaLinhas(conteudo(t, p)); n != 0 {
			t.Fatalf("GET wrote %d line(s)", n)
		}
		for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
			r2 := httptest.NewRecorder()
			h(r2, httptest.NewRequest(m, "/api/telemetry", strings.NewReader("{}")))
			if r2.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s: expected=405 observed=%d", m, r2.Code)
			}
		}
	})

	// 9. Body that is not JSON → 400, no panic.
	t.Run("body-not-json", func(t *testing.T) {
		for _, body := range []string{
			"", "isso nao e json", "[]", "null", `{"v":1,"s":`, "\x00\x01\x02",
		} {
			h, p := novoHandler(t)
			rec := post(h, "application/json", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("body=%q: expected=400 observed=%d", body, rec.Code)
			}
			if n := contaLinhas(conteudo(t, p)); n != 0 {
				t.Errorf("body=%q: wrote %d line(s)", body, n)
			}
		}
	})

	// 10. A successful response has an empty body (zero bytes).
	t.Run("success-response-empty-body", func(t *testing.T) {
		h, _ := novoHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dashboard","origin":"default"}]}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		if n := rec.Body.Len(); n != 0 {
			t.Fatalf("response body: expected=0 bytes observed=%d (%q)", n, rec.Body.String())
		}
	})

	// 11. THE sendBeacon BRANCH: this fork has no CSRF on /api/*, so the front end
	//     uses navigator.sendBeacon — which sends Content-Type: text/plain;charset=UTF-8.
	//     If the handler demanded application/json, every event would be refused in
	//     SILENCE and the usage report would get 14 days of empty file.
	t.Run("content-type-text-plain-from-sendBeacon", func(t *testing.T) {
		for _, ct := range []string{
			"text/plain;charset=UTF-8",
			"text/plain; charset=utf-8",
			"text/plain",
			"application/json",
			"application/json; charset=utf-8",
			"", // sendBeacon with an untyped Blob
		} {
			h, p := novoHandler(t)
			rec := post(h, ct,
				`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.codigo","origin":"nav"}]}`)
			if rec.Code != http.StatusNoContent {
				t.Errorf("Content-Type=%q: expected=204 observed=%d", ct, rec.Code)
			}
			if n := contaLinhas(conteudo(t, p)); n != 1 {
				t.Errorf("Content-Type=%q: expected=1 observed=%d lines", ct, n)
			}
		}
	})

	// 11b. Content-Type outside the set → 415, zero lines.
	t.Run("unsupported-content-type", func(t *testing.T) {
		h, p := novoHandler(t)
		rec := post(h, "multipart/form-data; boundary=x",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.codigo","origin":"nav"}]}`)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("expected=415 observed=%d", rec.Code)
		}
		if n := contaLinhas(conteudo(t, p)); n != 0 {
			t.Fatalf("wrote %d line(s)", n)
		}
	})

	// 12. Empty batch → 400 (there is nothing to measure and the cost is the same).
	t.Run("empty-batch", func(t *testing.T) {
		h, p := novoHandler(t)
		rec := post(h, "application/json", `{"v":1,"s":"9f3a1c72","e":[]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if n := contaLinhas(conteudo(t, p)); n != 0 {
			t.Fatalf("wrote %d line(s)", n)
		}
	})

	// 13. COMPLETE validation before writing: one invalid event at the end of the
	//     batch must not leave the preceding ones written.
	t.Run("partially-invalid-batch-writes-zero", func(t *testing.T) {
		h, p := novoHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.codigo","origin":"nav"},{"screen":"dashboard","origin":"nav"},{"screen":"dev.terminal","origin":"XXX"}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if n := contaLinhas(conteudo(t, p)); n != 0 {
			t.Fatalf("wrote %d line(s) before refusing the batch", n)
		}
	})

	// 14. No free-form field from the browser survives: the record is assembled
	//     from a closed struct. Proof by difference — the batch's `dropped` is
	//     accepted on input and is NOT written.
	t.Run("dropped-accepted-but-not-written", func(t *testing.T) {
		h, p := novoHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dashboard","origin":"default"}],"dropped":47}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		c := conteudo(t, p)
		// Check by KEY, never by substring: the server's `ts` carries the time,
		// and a `strings.Contains(c, "47")` matches 12:47:56. That test failed
		// because of the clock, not because of a defect — a false alarm, which
		// this project treats as worse than no test at all.
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(c)), &m); err != nil {
			t.Fatalf("the record is not JSON: %q", c)
		}
		if _, ok := m["dropped"]; ok {
			t.Fatalf("`dropped` leaked into the record: %q", c)
		}
		esperadas := map[string]bool{"ts": true, "v": true, "fork": true, "sid": true, "screen": true, "origin": true}
		if len(m) != len(esperadas) {
			t.Fatalf("open schema: expected=%d fields observed=%d (%q)", len(esperadas), len(m), c)
		}
		for k := range m {
			if !esperadas[k] {
				t.Errorf("unexpected field in the record: %q", k)
			}
		}
	})
}

// TestHandlerNaoQuebraComSinkMorto: a sink error can never become a UI error
// nor a panic (accepted disposition).
func TestHandlerNaoQuebraComSinkMorto(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSink(dir)
	if err != nil {
		t.Fatal(err)
	}
	congelar(s, diaFixo)
	// Directory removed out from under the sink: the OpenFile will fail.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	h := Handler(s, "vps-manager")
	rec := post(h, "application/json",
		`{"v":1,"s":"9f3a1c72","e":[{"screen":"dashboard","origin":"default"}]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a dead sink became a UI error: expected=204 observed=%d", rec.Code)
	}
}
