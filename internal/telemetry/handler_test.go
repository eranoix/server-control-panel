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

const fixedDay = "2026-08-06"

func newHandler(t *testing.T) (http.HandlerFunc, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewSink(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	freeze(s, fixedDay)
	return Handler(s, "server-control-panel"), filepath.Join(dir, fixedDay+".jsonl")
}

func content(t *testing.T, path string) string {
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

func countLines(s string) int {
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
	t.Run("valid-batch-2-events", func(t *testing.T) {
		h, p := newHandler(t)
		before := time.Now().Add(-time.Second)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.code","origin":"nav"},{"screen":"docker.containers.logs","origin":"default"}],"dropped":0}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		c := content(t, p)
		if n := countLines(c); n != 2 {
			t.Fatalf("expected=2 observed=%d lines; content=%q", n, c)
		}
		wantSc := []string{"dev.code", "docker.containers.logs"}
		wantOr := []string{"nav", "default"}
		for i, l := range strings.Split(strings.TrimSuffix(c, "\n"), "\n") {
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatalf("line %d is not JSON: %q", i+1, l)
			}
			if m["screen"] != wantSc[i] {
				t.Errorf("line %d screen: expected=%q observed=%v", i+1, wantSc[i], m["screen"])
			}
			if m["origin"] != wantOr[i] {
				t.Errorf("line %d origin: expected=%q observed=%v", i+1, wantOr[i], m["origin"])
			}
			if m["sid"] != "9f3a1c72" {
				t.Errorf("line %d sid: expected=9f3a1c72 observed=%v", i+1, m["sid"])
			}
			if m["fork"] != "server-control-panel" {
				t.Errorf("line %d fork: expected=server-control-panel observed=%v", i+1, m["fork"])
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
			} else if pt.Before(before) || pt.After(time.Now().Add(time.Second)) {
				t.Errorf("line %d ts is not the server's (outside the window): %q", i+1, ts)
			}
			if len(m) != 6 {
				t.Errorf("line %d has %d fields, expected=6: %q", i+1, len(m), l)
			}
		}
	})

	t.Run("body-over-64kib", func(t *testing.T) {
		h, p := newHandler(t)
		huge := strings.Repeat("a", 70000)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"`+huge+`","origin":"nav"}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if n := countLines(content(t, p)); n != 0 {
			t.Fatalf("a refused body wrote %d line(s)", n)
		}
	})

	t.Run("batch-201-events", func(t *testing.T) {
		h, p := newHandler(t)
		evs := make([]string, 201)
		for i := range evs {
			evs[i] = `{"screen":"dev.code","origin":"nav"}`
		}
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[`+strings.Join(evs, ",")+`]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if n := countLines(content(t, p)); n != 0 {
			t.Fatalf("a refused batch wrote %d line(s)", n)
		}
	})

	t.Run("batch-of-200-events-passes", func(t *testing.T) {
		h, p := newHandler(t)
		evs := make([]string, 200)
		for i := range evs {
			evs[i] = `{"screen":"dev.code","origin":"nav"}`
		}
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[`+strings.Join(evs, ",")+`]}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		if n := countLines(content(t, p)); n != 200 {
			t.Fatalf("expected=200 observed=%d lines", n)
		}
	})

	t.Run("invalid-origin", func(t *testing.T) {
		for _, org := range []string{"NAV", "", "click", "nav\n", "default "} {
			h, p := newHandler(t)
			b, _ := json.Marshal(map[string]any{
				"v": 1, "s": "9f3a1c72",
				"e": []map[string]string{{"screen": "dev.code", "origin": org}},
			})
			rec := post(h, "application/json", string(b))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("origin=%q: expected=400 observed=%d", org, rec.Code)
			}
			if n := countLines(content(t, p)); n != 0 {
				t.Errorf("origin=%q: wrote %d line(s)", org, n)
			}
		}
	})

	t.Run("malformed-sid", func(t *testing.T) {
		for _, sid := range []string{
			"", "1234567", "9F3A1C72", "9f3a1c7g", "../../etc/passwd",
			strings.Repeat("a", 33), "9f3a1c72\n", " 9f3a1c72",
		} {
			h, p := newHandler(t)
			b, _ := json.Marshal(map[string]any{
				"v": 1, "s": sid,
				"e": []map[string]string{{"screen": "dev.code", "origin": "nav"}},
			})
			rec := post(h, "application/json", string(b))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("sid=%q: expected=400 observed=%d", sid, rec.Code)
			}
			if n := countLines(content(t, p)); n != 0 {
				t.Errorf("sid=%q: wrote %d line(s)", sid, n)
			}
		}
		for _, sid := range []string{"abcdef01", strings.Repeat("0f", 16)} {
			h, _ := newHandler(t)
			b, _ := json.Marshal(map[string]any{
				"v": 1, "s": sid,
				"e": []map[string]string{{"screen": "dev.code", "origin": "nav"}},
			})
			if rec := post(h, "application/json", string(b)); rec.Code != http.StatusNoContent {
				t.Errorf("valid sid %q: expected=204 observed=%d", sid, rec.Code)
			}
		}
	})

	t.Run("unknown-screen-becomes-unknown", func(t *testing.T) {
		h, p := newHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"no-such-screen","origin":"nav"}]}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		c := content(t, p)
		if n := countLines(c); n != 1 {
			t.Fatalf("expected=1 observed=%d lines", n)
		}
		if got := strings.Count(c, `"screen":"unknown"`); got != 1 {
			t.Errorf(`"screen":"unknown": expected=1 observed=%d; content=%q`, got, c)
		}
		if strings.Contains(c, "no-such-screen") {
			t.Errorf("the raw id was written — log poisoning: %q", c)
		}
	})

	t.Run("hostile-screen-does-not-touch-the-file", func(t *testing.T) {
		for _, sc := range []string{
			`<script>alert(1)</script>`,
			`../../etc/passwd`,
			`dev.code","injected":"yes`,
			`DOCKER.CONTAINERS`,
		} {
			h, p := newHandler(t)
			b, _ := json.Marshal(map[string]any{
				"v": 1, "s": "9f3a1c72",
				"e": []map[string]string{{"screen": sc, "origin": "nav"}},
			})
			rec := post(h, "application/json", string(b))
			if rec.Code != http.StatusNoContent {
				t.Errorf("screen=%q: expected=204 observed=%d", sc, rec.Code)
			}
			c := content(t, p)
			if strings.Contains(c, "script") || strings.Contains(c, "passwd") ||
				strings.Contains(c, "injected") || strings.Contains(c, "DOCKER") {
				t.Errorf("screen=%q leaked into the file: %q", sc, c)
			}
			if n := countLines(c); n != 1 {
				t.Errorf("screen=%q: expected=1 observed=%d lines", sc, n)
			}
			var m map[string]any
			l := strings.TrimSuffix(c, "\n")
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Errorf("screen=%q broke the JSONL: %q", sc, c)
			} else if m["screen"] != "unknown" {
				t.Errorf("screen=%q: expected=unknown observed=%v", sc, m["screen"])
			}
		}
	})

	t.Run("extra-field-in-the-event", func(t *testing.T) {
		h, p := newHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.code","origin":"nav","evil":"<script>"}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		c := content(t, p)
		if strings.Contains(c, "evil") || strings.Contains(c, "<script>") {
			t.Fatalf("a free-form field reached the JSONL: %q", c)
		}
		if n := countLines(c); n != 0 {
			t.Fatalf("expected=0 observed=%d lines", n)
		}
	})

	t.Run("extra-field-in-the-batch", func(t *testing.T) {
		h, p := newHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.code","origin":"nav"}],"evil":"x"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if c := content(t, p); strings.Contains(c, "evil") || countLines(c) != 0 {
			t.Fatalf("a batch with a free-form field wrote: %q", c)
		}
	})

	t.Run("method-get-405", func(t *testing.T) {
		h, p := newHandler(t)
		req := httptest.NewRequest(http.MethodGet, "/api/telemetry", nil)
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected=405 observed=%d", rec.Code)
		}
		if n := countLines(content(t, p)); n != 0 {
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

	t.Run("body-not-json", func(t *testing.T) {
		for _, body := range []string{
			"", "this is not json", "[]", "null", `{"v":1,"s":`, "\x00\x01\x02",
		} {
			h, p := newHandler(t)
			rec := post(h, "application/json", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("body=%q: expected=400 observed=%d", body, rec.Code)
			}
			if n := countLines(content(t, p)); n != 0 {
				t.Errorf("body=%q: wrote %d line(s)", body, n)
			}
		}
	})

	t.Run("success-response-empty-body", func(t *testing.T) {
		h, _ := newHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dashboard","origin":"default"}]}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		if n := rec.Body.Len(); n != 0 {
			t.Fatalf("response body: expected=0 bytes observed=%d (%q)", n, rec.Body.String())
		}
	})

	t.Run("content-type-text-plain-from-sendBeacon", func(t *testing.T) {
		for _, ct := range []string{
			"text/plain;charset=UTF-8",
			"text/plain; charset=utf-8",
			"text/plain",
			"application/json",
			"application/json; charset=utf-8",
			"",
		} {
			h, p := newHandler(t)
			rec := post(h, ct,
				`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.code","origin":"nav"}]}`)
			if rec.Code != http.StatusNoContent {
				t.Errorf("Content-Type=%q: expected=204 observed=%d", ct, rec.Code)
			}
			if n := countLines(content(t, p)); n != 1 {
				t.Errorf("Content-Type=%q: expected=1 observed=%d lines", ct, n)
			}
		}
	})

	t.Run("unsupported-content-type", func(t *testing.T) {
		h, p := newHandler(t)
		rec := post(h, "multipart/form-data; boundary=x",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.code","origin":"nav"}]}`)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("expected=415 observed=%d", rec.Code)
		}
		if n := countLines(content(t, p)); n != 0 {
			t.Fatalf("wrote %d line(s)", n)
		}
	})

	t.Run("empty-batch", func(t *testing.T) {
		h, p := newHandler(t)
		rec := post(h, "application/json", `{"v":1,"s":"9f3a1c72","e":[]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if n := countLines(content(t, p)); n != 0 {
			t.Fatalf("wrote %d line(s)", n)
		}
	})

	t.Run("partially-invalid-batch-writes-zero", func(t *testing.T) {
		h, p := newHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dev.code","origin":"nav"},{"screen":"dashboard","origin":"nav"},{"screen":"dev.terminal","origin":"XXX"}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected=400 observed=%d", rec.Code)
		}
		if n := countLines(content(t, p)); n != 0 {
			t.Fatalf("wrote %d line(s) before refusing the batch", n)
		}
	})

	t.Run("dropped-accepted-but-not-written", func(t *testing.T) {
		h, p := newHandler(t)
		rec := post(h, "application/json",
			`{"v":1,"s":"9f3a1c72","e":[{"screen":"dashboard","origin":"default"}],"dropped":47}`)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected=204 observed=%d", rec.Code)
		}
		c := content(t, p)
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(c)), &m); err != nil {
			t.Fatalf("the record is not JSON: %q", c)
		}
		if _, ok := m["dropped"]; ok {
			t.Fatalf("`dropped` leaked into the record: %q", c)
		}
		wantKeys := map[string]bool{"ts": true, "v": true, "fork": true, "sid": true, "screen": true, "origin": true}
		if len(m) != len(wantKeys) {
			t.Fatalf("open schema: expected=%d fields observed=%d (%q)", len(wantKeys), len(m), c)
		}
		for k := range m {
			if !wantKeys[k] {
				t.Errorf("unexpected field in the record: %q", k)
			}
		}
	})
}

func TestHandlerSurvivesDeadSink(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSink(dir)
	if err != nil {
		t.Fatal(err)
	}
	freeze(s, fixedDay)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	h := Handler(s, "server-control-panel")
	rec := post(h, "application/json",
		`{"v":1,"s":"9f3a1c72","e":[{"screen":"dashboard","origin":"default"}]}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("a dead sink became a UI error: expected=204 observed=%d", rec.Code)
	}
}
