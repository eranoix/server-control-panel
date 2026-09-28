package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

func TestSafeUploadName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain name kept", "contract.pdf", "contract.pdf"},
		{"dash and underscore stay", "spec_v2-final.md", "spec_v2-final.md"},
		{"path traversal becomes basename", "../../../etc/cron.d/backdoor", "backdoor"},
		{"pure traversal leaves nothing", "../../..", ""},
		{"windows separator", `C:\Users\sam\note.txt`, "note.txt"},
		{"slash in the middle", "a/b/c.log", "c.log"},
		{"spaces become underscore", "my final file.pdf", "my_final_file.pdf"},
		{"non-ASCII becomes underscore", "naïve.pdf", "na_ve.pdf"},
		{"leading dot removed", ".bashrc", "bashrc"},
		{"only dots leaves nothing", "...", ""},
		{"empty", "", ""},
		{"only spaces", "   ", ""},
		{"control chars dropped", "na\x00me\x1f.txt", "name.txt"},
		{"newline does not become a break", "a\nb.txt", "ab.txt"},
		{"underscores collapse", "a    b     c.txt", "a_b_c.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := safeUploadName(c.in); got != c.want {
				t.Fatalf("safeUploadName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSafeUploadNameLimit(t *testing.T) {
	long := ""
	for i := 0; i < 500; i++ {
		long += "a"
	}
	got := safeUploadName(long + ".pdf")
	if len(got) > 96 {
		t.Fatalf("name ended up with %d chars, above the 96 limit", len(got))
	}
	if len(got) < 5 || got[len(got)-4:] != ".pdf" {
		t.Fatalf("truncation lost the extension: %q", got)
	}
}

func TestSafeUploadNameNeverHasSeparator(t *testing.T) {
	entries := []string{
		"../x", "a/b", `a\b`, "/etc/passwd", `..\..\win.ini`,
		"nor mal.pdf", "archive.tar.gz", "Ωmega.txt",
	}
	for _, in := range entries {
		got := safeUploadName(in)
		for _, r := range got {
			if r == '/' || r == '\\' {
				t.Fatalf("safeUploadName(%q) = %q — contains a path separator", in, got)
			}
		}
		if got == "." || got == ".." {
			t.Fatalf("safeUploadName(%q) = %q — directory reference", in, got)
		}
	}
}

func postFile(t *testing.T, r *Router, field, name string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, name)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatalf("write: %v", err)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	w := httptest.NewRecorder()
	r.handleTerminalUpload(w, req)
	return w
}

func testRouter(t *testing.T) (*Router, string) {
	t.Helper()
	dir := t.TempDir()
	return &Router{cfg: &config.Config{DataDir: dir, Primary: "sam"}}, dir
}

func TestUploadAcceptsNonImage(t *testing.T) {
	r, _ := testRouter(t)
	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n%%EOF\n")
	w := postFile(t, r, "file", "client contract.pdf", pdf)
	if w.Code != 200 {
		t.Fatalf("PDF rejected with %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Path, Name, Type string
		Size             int64
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unreadable response: %v — %s", err, w.Body.String())
	}
	if resp.Size != int64(len(pdf)) {
		t.Fatalf("size = %d, want %d", resp.Size, len(pdf))
	}
	if !strings.HasSuffix(resp.Name, "client_contract.pdf") {
		t.Fatalf("original name lost: %q", resp.Name)
	}
	got, err := os.ReadFile(resp.Path)
	if err != nil {
		t.Fatalf("file was not written to %s: %v", resp.Path, err)
	}
	if !bytes.Equal(got, pdf) {
		t.Fatalf("written content differs from what was sent (%d vs %d bytes)", len(got), len(pdf))
	}
	if fi, err := os.Stat(resp.Path); err == nil && fi.Mode().Perm() != 0o600 {
		t.Fatalf("permission %v, wanted 0600", fi.Mode().Perm())
	}
}

func TestUploadVariousTypes(t *testing.T) {
	r, _ := testRouter(t)
	cases := []struct{ name, content string }{
		{"sheet.csv", "a,b,c\n1,2,3\n"},
		{"server log.log", "2026-08-18 error\n"},
		{"notes.md", "# title\n"},
		{"package.tar.gz", "\x1f\x8b\x08\x00binary"},
		{"no-extension", "any content"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := postFile(t, r, "file", c.name, []byte(c.content))
			if w.Code != 200 {
				t.Fatalf("%s rejected with %d: %s", c.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestUploadAcceptsLegacyImageField(t *testing.T) {
	r, _ := testRouter(t)
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 40))
	if w := postFile(t, r, "image", "paste.png", png); w.Code != 200 {
		t.Fatalf("legacy field 'image' rejected with %d: %s", w.Code, w.Body.String())
	}
}

func TestUploadStaysInsideDirectory(t *testing.T) {
	r, dir := testRouter(t)
	w := postFile(t, r, "file", "../../../../tmp/pwned.txt", []byte("x"))
	if w.Code != 200 {
		t.Fatalf("expected to write with a sanitized name, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct{ Path string }
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	clean := filepath.Clean(resp.Path)
	if !strings.HasPrefix(clean, filepath.Clean(dir)+string(os.PathSeparator)) {
		t.Fatalf("wrote OUTSIDE the tenant's DataDir: %s", clean)
	}
	if strings.Contains(clean, "..") {
		t.Fatalf("final path still contains traversal: %s", clean)
	}
	if _, err := os.Stat("/tmp/pwned.txt"); err == nil {
		t.Fatalf("wrote to /tmp/pwned.txt — traversal got through")
	}
}

func TestUploadRequiresFile(t *testing.T) {
	r, _ := testRouter(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("other", "thing")
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	w := httptest.NewRecorder()
	r.handleTerminalUpload(w, req)
	if w.Code != 400 {
		t.Fatalf("form without file should give 400, got %d", w.Code)
	}
}

func TestUploadRequiresAuth(t *testing.T) {
	r, _ := testRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/upload", strings.NewReader(""))
	w := httptest.NewRecorder()
	r.handleTerminalUpload(w, req)
	if w.Code != 401 {
		t.Fatalf("no user in context should give 401, got %d", w.Code)
	}
}
