package api

// maxbody_override_test.go proves, with real HTTP bodies, that the panel's
// upload routes that need more than the global 25 MiB cap
// (httpmw.MaxBody) can read larger bodies — game world import
// (600 MiB) and Jira attachment (32 MiB) — via httpmw.RegisterLargeBody
// registered in init() in each family's files. Before that
// RegisterLargeBody, the global MaxBytesReader cut the body off at 25 MiB
// BEFORE each handler's ParseMultipartForm ran, and neither of the two
// ever reached the cap it promised.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"server-control-panel/internal/httpmw"
)

func TestIsGameWorldImportUpload_CasaSoARotaDeImportar(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"import de mundo", http.MethodPost, "/api/gameservers/srv1/worlds/import", true},
		{"GET nao casa", http.MethodGet, "/api/gameservers/srv1/worlds/import", false},
		{"export nao casa", http.MethodPost, "/api/gameservers/srv1/worlds/export", false},
		{"switch nao casa", http.MethodPost, "/api/gameservers/srv1/worlds/switch", false},
		{"backups nao casa", http.MethodPost, "/api/gameservers/srv1/backups/restore", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if got := isGameWorldImportUpload(req); got != c.want {
			t.Errorf("%s: isGameWorldImportUpload(%s %s) = %v, want %v", c.name, c.method, c.path, got, c.want)
		}
	}
}

func TestGameWorldImportUpload_BodyOver25MiB_NaoECapadoPeloMaxBodyGlobal(t *testing.T) {
	body := bytes.Repeat([]byte("w"), 40<<20) // 40 MiB — acima de 25 MiB, bem abaixo de 600 MiB
	var gotN int64
	var gotErr error
	handler := httpmw.MaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotN, gotErr = io.Copy(io.Discard, r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/gameservers/srv1/worlds/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotErr != nil {
		t.Fatalf("error reading the 40 MiB body on the world-import route: %v", gotErr)
	}
	if gotN != int64(len(body)) {
		t.Fatalf("read %d bytes, want %d (entire body)", gotN, len(body))
	}
}

func TestIsJiraAttachmentUpload_CasaSoARotaDeAnexo(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"upload de anexo", http.MethodPost, "/api/jira/issue/WEB-1/attachments", true},
		{"GET nao casa", http.MethodGet, "/api/jira/issue/WEB-1/attachments", false},
		{"comment nao casa", http.MethodPost, "/api/jira/issue/WEB-1/comment", false},
		{"transitions nao casa", http.MethodPost, "/api/jira/issue/WEB-1/transitions", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if got := isJiraAttachmentUpload(req); got != c.want {
			t.Errorf("%s: isJiraAttachmentUpload(%s %s) = %v, want %v", c.name, c.method, c.path, got, c.want)
		}
	}
}

func TestJiraAttachmentUpload_BodyOver25MiB_NaoECapadoPeloMaxBodyGlobal(t *testing.T) {
	body := bytes.Repeat([]byte("j"), 30<<20) // 30 MiB — acima de 25 MiB, abaixo do teto de 32 MiB
	var gotN int64
	var gotErr error
	handler := httpmw.MaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotN, gotErr = io.Copy(io.Discard, r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/jira/issue/WEB-1/attachments", bytes.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotErr != nil {
		t.Fatalf("error reading the 30 MiB body on the Jira attachment route: %v", gotErr)
	}
	if gotN != int64(len(body)) {
		t.Fatalf("read %d bytes, want %d (entire body)", gotN, len(body))
	}
}
