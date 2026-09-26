package videocall

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"server-control-panel/internal/httpmw"
)

// TestIsRecordingUpload_CasaSoARotaDeUpload proves that the matcher registered
// in init() matches exactly POST /api/videocall/recordings and does not match
// the item routes (metadata, blob, summarize, delete).
func TestIsRecordingUpload_CasaSoARotaDeUpload(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"upload de gravacao", http.MethodPost, "/api/videocall/recordings", true},
		{"listar nao casa", http.MethodGet, "/api/videocall/recordings", false},
		{"metadado nao casa", http.MethodGet, "/api/videocall/recordings/abc123", false},
		{"blob nao casa", http.MethodGet, "/api/videocall/recordings/abc123/blob", false},
		{"summarize nao casa", http.MethodPost, "/api/videocall/recordings/abc123/summarize", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if got := isRecordingUpload(req); got != c.want {
			t.Errorf("%s: isRecordingUpload(%s %s) = %v, want %v", c.name, c.method, c.path, got, c.want)
		}
	}
}

// TestRecordingUpload_BodyOver25MiB_NaoECapadoPeloMaxBodyGlobal proves, with a
// real 40 MiB body, that a recording can get past the global 25 MiB ceiling.
// Before RegisterLargeBody in init(), the
// r.Body = http.MaxBytesReader(w, r.Body, recordingMaxBytes+1<<20) inside
// HandleRecordingUpload had no practical effect whatsoever — the middleware's
// global 25 MiB wrapper had already been applied first, and MaxBytesReader
// does not loosen a smaller limit already applied.
func TestRecordingUpload_BodyOver25MiB_NaoECapadoPeloMaxBodyGlobal(t *testing.T) {
	body := bytes.Repeat([]byte("r"), 40<<20) // 40 MiB — above 25 MiB, well below 500 MiB
	var gotN int64
	var gotErr error
	handler := httpmw.MaxBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotN, gotErr = io.Copy(io.Discard, r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/videocall/recordings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotErr != nil {
		t.Fatalf("error reading a 40 MiB body on the recording upload route: %v", gotErr)
	}
	if gotN != int64(len(body)) {
		t.Fatalf("read %d bytes, want %d (whole body)", gotN, len(body))
	}
}
