package videocall

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"server-control-panel/internal/httpmw"
)

func TestIsRecordingUpload_MatchesOnlyUploadRoute(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"recording upload", http.MethodPost, "/api/videocall/recordings", true},
		{"list does not match", http.MethodGet, "/api/videocall/recordings", false},
		{"metadata does not match", http.MethodGet, "/api/videocall/recordings/abc123", false},
		{"blob does not match", http.MethodGet, "/api/videocall/recordings/abc123/blob", false},
		{"summarize does not match", http.MethodPost, "/api/videocall/recordings/abc123/summarize", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if got := isRecordingUpload(req); got != c.want {
			t.Errorf("%s: isRecordingUpload(%s %s) = %v, want %v", c.name, c.method, c.path, got, c.want)
		}
	}
}

func TestRecordingUpload_BodyOver25MiB_NotCappedByGlobalMaxBody(t *testing.T) {
	body := bytes.Repeat([]byte("r"), 40<<20)
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
