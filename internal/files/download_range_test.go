package files

// download_range_test.go — /api/files/download has to be resumable.
//
// Before this fix the handler assembled Content-Length by hand and did an
// io.Copy: it answered 200 with the WHOLE file even in the face of a Range
// header, so a dropped connection forced a restart from zero. On a bad link
// with a large file that means never finishing. The tests below pin the
// correct behavior (206/Content-Range/416) so that nobody reintroduces the
// io.Copy thinking it is equivalent.

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func pedeDownload(t *testing.T, caminho string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/download?path="+url.QueryEscape(caminho), nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	return rec
}

func arquivoDeTeste(t *testing.T) (string, []byte) {
	t.Helper()
	conteudo := bytes.Repeat([]byte("0123456789"), 100) // 1000 bytes
	caminho := filepath.Join(t.TempDir(), "grande.bin")
	if err := os.WriteFile(caminho, conteudo, 0o644); err != nil {
		t.Fatal(err)
	}
	return caminho, conteudo
}

func TestDownload_Range_206(t *testing.T) {
	caminho, conteudo := arquivoDeTeste(t)
	rec := pedeDownload(t, caminho, map[string]string{"Range": "bytes=100-199"})

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 — the handler ignored the Range; body=%s", rec.Code, rec.Body.String())
	}
	want := fmt.Sprintf("bytes 100-199/%d", len(conteudo))
	if got := rec.Header().Get("Content-Range"); got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), conteudo[100:200]) {
		t.Fatalf("a body of %d bytes is not the requested chunk", rec.Body.Len())
	}
}

// TestDownload_RetomadaDoMeio is the real shape of resuming: "I already have N bytes".
func TestDownload_RetomadaDoMeio(t *testing.T) {
	caminho, conteudo := arquivoDeTeste(t)
	rec := pedeDownload(t, caminho, map[string]string{"Range": "bytes=600-"})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), conteudo[600:]) {
		t.Fatalf("body = %d bytes; expected the 400-byte tail", rec.Body.Len())
	}
}

// TestDownload_SemRange_200Completo — the regression that matters when
// swapping io.Copy for ServeContent: the common case has to stay identical,
// including the Content-Disposition that makes the browser download instead
// of render.
func TestDownload_SemRange_200Completo(t *testing.T) {
	caminho, conteudo := arquivoDeTeste(t)
	rec := pedeDownload(t, caminho, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), conteudo) {
		t.Fatalf("body = %d bytes, want %d", rec.Body.Len(), len(conteudo))
	}
	if got := rec.Header().Get("Content-Length"); got != fmt.Sprint(len(conteudo)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(conteudo))
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q — it has to force a download, not let the browser guess", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="grande.bin"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
}

func TestDownload_RangeImpossivel_416(t *testing.T) {
	caminho, _ := arquivoDeTeste(t)
	rec := pedeDownload(t, caminho, map[string]string{"Range": "bytes=99999-199999"})
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", rec.Code)
	}
}

// TestDownload_ErrosPreservados — ServeContent must not have swallowed the
// gates that come before it (invalid path, directory, nonexistent).
func TestDownload_ErrosPreservados(t *testing.T) {
	dir := t.TempDir()
	casos := map[string]struct {
		caminho string
		status  int
	}{
		"relativo": {"nao/absoluto", http.StatusBadRequest},
		// The denylist (not a confined root) is this handler's gate: the file
		// browser serves an arbitrary absolute path by design, so there is no
		// "escaping the root" to test here — only that the high-value paths stay
		// refused.
		"denylist":    {"/etc/shadow", http.StatusBadRequest},
		"diretorio":   {dir, http.StatusBadRequest},
		"inexistente": {filepath.Join(dir, "nao-existe.bin"), http.StatusNotFound},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			if rec := pedeDownload(t, c.caminho, nil); rec.Code != c.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, c.status, rec.Body.String())
			}
		})
	}
}
