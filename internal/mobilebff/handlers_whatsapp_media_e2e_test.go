package mobilebff

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"golang.org/x/sync/singleflight"

	"server-control-panel/internal/auth"
	"server-control-panel/internal/httpmw"
	"server-control-panel/internal/whatsapp"
)

type e2eMediaSvc struct {
	mu            sync.Mutex
	downloadCalls int32
	downloadDelay time.Duration
	filePath      string
	mime          string
	sf            singleflight.Group
}

func (s *e2eMediaSvc) ListChats() []whatsapp.Chat { return nil }
func (s *e2eMediaSvc) MessagesForDisplay(jid string, opts whatsapp.MessagesQuery) ([]whatsapp.Message, bool, error) {
	return nil, false, nil
}
func (s *e2eMediaSvc) SendTextDedup(chatJID, text, quotedID, clientMsgID string) (string, error) {
	return "", nil
}
func (s *e2eMediaSvc) MarkRead(jid string) error                                      { return nil }
func (s *e2eMediaSvc) ServeAvatar(w http.ResponseWriter, r *http.Request, jid string) {}

func (s *e2eMediaSvc) DownloadMediaForMessage(chatJID, msgID string) (rel, mimeType, filename string, size int64, err error) {
	v, err, _ := s.sf.Do(chatJID+"|"+msgID, func() (any, error) {
		atomic.AddInt32(&s.downloadCalls, 1)
		if s.downloadDelay > 0 {
			time.Sleep(s.downloadDelay)
		}
		return s.filePath, nil
	})
	if err != nil {
		return "", "", "", 0, err
	}
	return v.(string), s.mime, "file.bin", 0, nil
}

func (s *e2eMediaSvc) ServeMediaRel(w http.ResponseWriter, r *http.Request, rel string) {
	f, err := os.Open(rel)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	fi, statErr := f.Stat()
	if statErr != nil {
		http.Error(w, "stat error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", s.mime)
	http.ServeContent(w, r, "file.bin", fi.ModTime(), f)
}

func (s *e2eMediaSvc) SendFileDedup(chatJID, msgType, filename, mimeType, caption, quotedID, clientMsgID string, data []byte) (string, error) {
	return "", nil
}

var _ whatsappSvc = (*e2eMediaSvc)(nil)

func newE2EMediaServer(t *testing.T, svc *e2eMediaSvc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	api := humago.NewWithPrefix(mux, Prefix, huma.DefaultConfig("test", "0.0"))
	registerWhatsappMediaWithResolver(api, func(ctx context.Context) (whatsappSvc, error) {
		if auth.UserFromContext(ctx) == "" {
			return nil, huma.Error401Unauthorized("unauthorized")
		}
		return svc, nil
	})

	inner := httpmw.Compress(httpmw.MaxBody(mux))
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.WithUser(r.Context(), "sam"))
		inner.ServeHTTP(w, r)
	})

	srv := httptest.NewServer(outer)
	t.Cleanup(srv.Close)
	return srv
}

func e2eMediaURL(srv *httptest.Server, jid, msgID string) string {
	return srv.URL + Prefix + "/whatsapp/chats/" + jid + "/media/" + msgID
}

func TestE2E_WhatsAppMedia_RangeRequest_Returns206WithContentRange(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/file.bin"
	content := []byte("0123456789ABCDEFGHIJ")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &e2eMediaSvc{filePath: path, mime: "image/jpeg"}
	srv := newE2EMediaServer(t, svc)

	req, err := http.NewRequest(http.MethodGet, e2eMediaURL(srv, "jid1", "msg1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=5-9")
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 (body=%s)", resp.StatusCode, body)
	}
	if got, want := resp.Header.Get("Content-Range"), "bytes 5-9/20"; got != want {
		t.Fatalf("Content-Range = %q, want %q", got, want)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
	if resp.Header.Get("Content-Encoding") == "gzip" {
		t.Fatalf("Range response came with Content-Encoding: gzip — this would corrupt Content-Range")
	}
	if string(body) != "56789" {
		t.Fatalf("body = %q, want %q", body, "56789")
	}
	if svc.downloadCalls != 1 {
		t.Fatalf("DownloadMediaForMessage called %d times, want 1", svc.downloadCalls)
	}
}

func TestE2E_WhatsAppMedia_UnsatisfiableRange_Returns416(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/file.bin"
	content := []byte("0123456789")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &e2eMediaSvc{filePath: path, mime: "image/jpeg"}
	srv := newE2EMediaServer(t, svc)

	req, err := http.NewRequest(http.MethodGet, e2eMediaURL(srv, "jid1", "msg1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=100-200")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 416 (body=%s)", resp.StatusCode, body)
	}
}

func TestE2E_WhatsAppMedia_NoRange_DoesNotCompressMedia(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/file.bin"
	content := []byte("fake binary content, but large enough to pass minGzipBytes if it were compressible text " +
		"fake binary content, but large enough to pass minGzipBytes if it were compressible text " +
		"fake binary content, but large enough to pass minGzipBytes if it were compressible text")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &e2eMediaSvc{filePath: path, mime: "image/jpeg"}
	srv := newE2EMediaServer(t, svc)

	req, err := http.NewRequest(http.MethodGet, e2eMediaURL(srv, "jid1", "msg1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Encoding") == "gzip" {
		t.Fatalf("Content-Encoding = gzip — media should not be compressed")
	}
	if !bytes.Equal(body, content) {
		t.Fatalf("body corrupted: len(got)=%d len(want)=%d", len(body), len(content))
	}
}

func TestE2E_WhatsAppMedia_RealConcurrency_CollapsesIntoOneDownload(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/file.bin"
	if err := os.WriteFile(path, []byte("media-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &e2eMediaSvc{filePath: path, mime: "image/jpeg", downloadDelay: 150 * time.Millisecond}
	srv := newE2EMediaServer(t, svc)

	const n = 8
	var wg sync.WaitGroup
	statuses := make([]int, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := srv.Client().Get(e2eMediaURL(srv, "jidconc", "msgconc"))
			if err != nil {
				t.Errorf("request %d: %v", i, err)
				return
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)
			statuses[i] = resp.StatusCode
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	for i, code := range statuses {
		if code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, code)
		}
	}
	if svc.downloadCalls != 1 {
		t.Fatalf("downloadCalls = %d, want 1 (concurrency should collapse into a single download)", svc.downloadCalls)
	}
	if elapsed > 600*time.Millisecond {
		t.Fatalf("elapsed = %v, want bounded near a single downloadDelay (did the collapse not happen?)", elapsed)
	}
	fmt.Printf("concurrency: %d real requests, %d download(s), %v\n", n, svc.downloadCalls, elapsed)
}
