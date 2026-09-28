package whatsapp

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"server-control-panel/internal/httpmw"
)

func TestIsPanelSendFileUpload_MatchesOnlyMultipartVariant(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		ct     string
		want   bool
	}{
		{"media upload", http.MethodPost, "/api/whatsapp/chats/5511999999999@s.whatsapp.net/messages", "multipart/form-data; boundary=x", true},
		{"plain text does not match", http.MethodPost, "/api/whatsapp/chats/5511999999999@s.whatsapp.net/messages", "application/json", false},
		{"GET does not match", http.MethodGet, "/api/whatsapp/chats/5511999999999@s.whatsapp.net/messages", "multipart/form-data; boundary=x", false},
		{"read does not match", http.MethodPost, "/api/whatsapp/chats/5511999999999@s.whatsapp.net/read", "multipart/form-data; boundary=x", false},
		{"other route does not match", http.MethodPost, "/api/whatsapp/chats/sync", "multipart/form-data; boundary=x", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Header.Set("Content-Type", c.ct)
		if got := isPanelSendFileUpload(req); got != c.want {
			t.Errorf("%s: isPanelSendFileUpload(%s %s, ct=%s) = %v, want %v", c.name, c.method, c.path, c.ct, got, c.want)
		}
	}
}

func TestPanelSendFileUpload_BodyOver25MiB_NotCappedByGlobalMaxBody(t *testing.T) {
	body := bytes.Repeat([]byte("m"), 30<<20)
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
