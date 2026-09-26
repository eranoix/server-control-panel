package mobilebff

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/config"
)

// TestTransferDownload_RangeRequest_ReturnsPartialContent proves, with a real
// HTTP request, that a Range request returns 206 with the correct body and
// Content-Range — the basis of resumable download.
func TestTransferDownload_RangeRequest_ReturnsPartialContent(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("0123456789"), 100) // 1000 bytes
	f := filepath.Join(dir, "grande.bin")
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: t.TempDir()}})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/download?path="+url.QueryEscape(f), nil)
	req.Header.Set("Range", "bytes=100-199")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 (body=%s)", rec.Code, rec.Body.String())
	}
	wantRange := fmt.Sprintf("bytes 100-199/%d", len(content))
	if got := rec.Header().Get("Content-Range"); got != wantRange {
		t.Fatalf("Content-Range = %q, want %q", got, wantRange)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
	if rec.Body.Len() != 100 {
		t.Fatalf("body length = %d, want 100", rec.Body.Len())
	}
	if !bytes.Equal(rec.Body.Bytes(), content[100:200]) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), content[100:200])
	}
}

// TestTransferDownload_UnsatisfiableRange_Returns416 proves the edge case:
// a Range beyond the file's size returns 416, not 200 and not a silently
// empty body.
func TestTransferDownload_UnsatisfiableRange_Returns416(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "pequeno.bin")
	if err := os.WriteFile(f, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: t.TempDir()}})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/download?path="+url.QueryEscape(f), nil)
	req.Header.Set("Range", "bytes=10000-20000")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestTransferDownload_NoRange_ReturnsFullFile200 proves that an ordinary
// request (no Range) keeps working as a full download.
func TestTransferDownload_NoRange_ReturnsFullFile200(t *testing.T) {
	dir := t.TempDir()
	content := []byte("conteudo completo do arquivo")
	f := filepath.Join(dir, "completo.txt")
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: t.TempDir()}})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/download?path="+url.QueryEscape(f), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("body = %q, want %q", rec.Body.Bytes(), content)
	}
}

// TestTransferDownload_Unauthenticated_401 keeps parity with the BFF's other
// routes: with no session, 401 before any disk access.
func TestTransferDownload_Unauthenticated_401(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: t.TempDir()}})

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/files/download?path=/tmp/x", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestTransferUpload_ResumeAfterFailure_HashMatches is the central test of
// resumable upload: an upload is started, one part is sent, the connection
// "drops" (the test simply stops sending chunks without calling complete), and
// then the client RESUMES sending only the chunks that were missing — without
// resending the ones already acknowledged — and the final file matches the
// original byte for byte (hash).
func TestTransferUpload_ResumeAfterFailure_HashMatches(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: dataDir}})

	original := bytes.Repeat([]byte("abcdefghij"), 500) // 5000 bytes
	part1 := original[0:2000]
	part2 := original[2000:5000]

	// 1. init
	initBody, _ := json.Marshal(UploadInitRequest{DestDir: destDir, Filename: "resumido.bin", TotalSize: int64(len(original))})
	req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/files/upload/init", initBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("init: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var initResp UploadInitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("decode init: %v", err)
	}

	// 2. sends the first chunk successfully.
	sendChunk(t, mux, initResp.SessionID, 0, part1)

	// 3. "failure": the connection drops here — the test simply stops sending
	// anything for now, simulating the app being killed mid-transfer.

	// 4. resume: the client reconnects and resends only the remainder (offset 2000
	// onwards), never part1 again.
	sendChunk(t, mux, initResp.SessionID, 2000, part2)

	// 5. complete
	completeBody, _ := json.Marshal(UploadCompleteRequest{SessionID: initResp.SessionID})
	req = newAuthedRequest(http.MethodPost, "/api/mobile/v1/files/upload/complete", completeBody)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var completeResp UploadCompleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &completeResp); err != nil {
		t.Fatalf("decode complete: %v", err)
	}
	if !completeResp.OK {
		t.Fatal("complete: ok = false")
	}

	got, err := os.ReadFile(completeResp.Path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", completeResp.Path, err)
	}
	gotHash := sha256.Sum256(got)
	wantHash := sha256.Sum256(original)
	if gotHash != wantHash {
		t.Fatalf("hash of the final file (%x) differs from the original (%x) — the resume corrupted/duplicated bytes", gotHash, wantHash)
	}
}

func sendChunk(t *testing.T, mux *http.ServeMux, sessionID string, offset int64, data []byte) UploadChunkResponse {
	t.Helper()
	target := fmt.Sprintf("/api/mobile/v1/files/upload/chunk?session_id=%s&offset=%d", url.QueryEscape(sessionID), offset)
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	req = req.WithContext(auth.WithUser(req.Context(), "sam"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chunk offset=%d: status = %d, body = %s", offset, rec.Code, rec.Body.String())
	}
	var resp UploadChunkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode chunk response: %v", err)
	}
	return resp
}

// TestTransferUpload_IncompleteComplete_409WithProgress proves the 409 body
// returned when upload/complete is called before all the bytes have
// arrived.
func TestTransferUpload_IncompleteComplete_409WithProgress(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: dataDir}})

	initBody, _ := json.Marshal(UploadInitRequest{DestDir: destDir, Filename: "incompleto.bin", TotalSize: 1000})
	req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/files/upload/init", initBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var initResp UploadInitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &initResp); err != nil {
		t.Fatalf("decode init: %v", err)
	}

	sendChunk(t, mux, initResp.SessionID, 0, make([]byte, 400))

	completeBody, _ := json.Marshal(UploadCompleteRequest{SessionID: initResp.SessionID})
	req = newAuthedRequest(http.MethodPost, "/api/mobile/v1/files/upload/complete", completeBody)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
	}
	var body UploadIncompleteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, rec.Body.String())
	}
	if body.ReceivedBytes != 400 || body.TotalSize != 1000 {
		t.Fatalf("body = %+v, want received_bytes=400 total_size=1000", body)
	}
}

// TestTransferDownload_MissingFile_404NoPathLeak proves that a download of a
// nonexistent path returns 404, and that the error body does NOT carry the
// server's absolute path — before the fix, mapTransferErr echoed
// err.Error() of an *os.PathError, which embeds exactly that path.
func TestTransferDownload_MissingFile_404NoPathLeak(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nao-existe.bin")

	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: t.TempDir()}})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/download?path="+url.QueryEscape(missing), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(dir)) {
		t.Fatalf("404 body leaked the server's path: %s", rec.Body.String())
	}
}

// TestTransferInbox_ReturnsExistingDir proves that /files/inbox returns a real,
// existing path.
func TestTransferInbox_ReturnsExistingDir(t *testing.T) {
	dataDir := t.TempDir()
	mux := http.NewServeMux()
	Mount(mux, Deps{Cfg: &config.Config{DataDir: dataDir}})

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/files/inbox", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body InboxResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	fi, err := os.Stat(body.Path)
	if err != nil {
		t.Fatalf("inbox path does not exist: %v", err)
	}
	if !fi.IsDir() {
		t.Fatal("inbox path is not a directory")
	}
}

// TestMapTransferErr_DiskFull_507 proves that a full disk (ENOSPC, as the
// staging os.WriteFile returns it) reaches the app as 507 Insufficient Storage
// and not as the old 400 "invalid request" from the default branch. The
// difference is not cosmetic: with a 400 the app retried the upload forever
// against a disk that was never going to fit it, and the operator had no clue why.
func TestMapTransferErr_DiskFull_507(t *testing.T) {
	err := mapTransferErr(&os.PathError{
		Op:   "write",
		Path: "/srv/segredo/.mobile-upload-staging/abc/data",
		Err:  syscall.ENOSPC,
	})

	statusErr, ok := err.(huma.StatusError)
	if !ok {
		t.Fatalf("error is not huma.StatusError: %T", err)
	}
	if statusErr.GetStatus() != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want 507", statusErr.GetStatus())
	}
	if strings.Contains(err.Error(), "/srv/segredo") {
		t.Fatalf("message leaked the server's path: %s", err.Error())
	}
}

// TestMapTransferErr_PermissionDenied_403 proves that "no write permission on
// the folder" is a condition of the REQUEST, not a server defect. As a 500, the
// app classified the case as a temporary failure and kept retrying against a
// folder that would never accept the write.
func TestMapTransferErr_PermissionDenied_403(t *testing.T) {
	err := mapTransferErr(&os.PathError{
		Op:   "open",
		Path: "/srv/segredo/protegido",
		Err:  syscall.EACCES,
	})

	statusErr, ok := err.(huma.StatusError)
	if !ok {
		t.Fatalf("error is not huma.StatusError: %T", err)
	}
	if statusErr.GetStatus() != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", statusErr.GetStatus())
	}
	if strings.Contains(err.Error(), "/srv/segredo") {
		t.Fatalf("message leaked the server's path: %s", err.Error())
	}
}
