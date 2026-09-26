package whatsapp

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"server-control-panel/internal/httpmw"
)

// TestIsPanelSendFileUpload_CasaSoAVarianteMultipart proves that the matcher
// registered in init() matches exactly POST /api/whatsapp/chats/<jid>/messages
// with a multipart Content-Type, and does not match the text variant
// (application/json) nor other whatsapp routes sharing the same prefix.
func TestIsPanelSendFileUpload_CasaSoAVarianteMultipart(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		ct     string
		want   bool
	}{
		{"upload de midia", http.MethodPost, "/api/whatsapp/chats/5511999999999@s.whatsapp.net/messages", "multipart/form-data; boundary=x", true},
		{"texto simples nao casa", http.MethodPost, "/api/whatsapp/chats/5511999999999@s.whatsapp.net/messages", "application/json", false},
		{"GET nao casa", http.MethodGet, "/api/whatsapp/chats/5511999999999@s.whatsapp.net/messages", "multipart/form-data; boundary=x", false},
		{"read nao casa", http.MethodPost, "/api/whatsapp/chats/5511999999999@s.whatsapp.net/read", "multipart/form-data; boundary=x", false},
		{"outra rota nao casa", http.MethodPost, "/api/whatsapp/chats/sync", "multipart/form-data; boundary=x", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Header.Set("Content-Type", c.ct)
		if got := isPanelSendFileUpload(req); got != c.want {
			t.Errorf("%s: isPanelSendFileUpload(%s %s, ct=%s) = %v, want %v", c.name, c.method, c.path, c.ct, got, c.want)
		}
	}
}

// TestPanelSendFileUpload_BodyOver25MiB_NaoECapadoPeloMaxBodyGlobal proves,
// with a real 30 MiB body (above the 25 MiB global ceiling and below the
// route's own 100 MiB ceiling), that the init()+httpmw.MaxBody combination
// really does let the handler read the entire body — the original bug was
// the opposite: MaxBytesReader had already truncated at 25 MiB before
// ParseMultipartForm(100<<20) got to run.
func TestPanelSendFileUpload_BodyOver25MiB_NaoECapadoPeloMaxBodyGlobal(t *testing.T) {
	body := bytes.Repeat([]byte("m"), 30<<20) // 30 MiB
	var gotN int64
	var gotErr error
	handler := httpmw.MaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotN, gotErr = io.Copy(io.Discard, r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/whatsapp/chats/5511999999999@s.whatsapp.net/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotErr != nil {
		t.Fatalf("error reading a 30 MiB body on the panel's media-send route: %v", gotErr)
	}
	if gotN != int64(len(body)) {
		t.Fatalf("read %d bytes, want %d (whole body)", gotN, len(body))
	}
}
