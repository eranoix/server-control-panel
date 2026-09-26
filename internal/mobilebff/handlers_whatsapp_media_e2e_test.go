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

// e2eMediaSvc is a double of whatsappSvc whose ServeMediaRel serves a REAL file
// via http.ServeContent (the same mechanism the real ServeMediaRel uses behind
// the scenes — see internal/whatsapp/service_export.go) and whose
// DownloadMediaForMessage collapses concurrent calls with a real
// singleflight.Group — the same primitive Service.downloadSF uses.
// That lets us prove, with real HTTP requests hitting the real middleware chain
// (Compress + MaxBody) + real huma routing, that:
//   - a Range becomes a 206 with correct Content-Range/Accept-Ranges;
//   - an unsatisfiable Range becomes a 416;
//   - Compress corrupts neither the Range response nor the full response of
//     a media Content-Type (which isCompressible already excludes);
//   - N concurrent requests for the same (jid,msgID) collapse into 1 download.
//
// What this test does NOT replace: the real download/cache business logic
// (chatDir, sanitizeMsgID, the WhatsApp backend's DownloadFile) is already covered
// in internal/whatsapp/service_export_test.go — here the target is the
// HTTP+middleware+huma integration around it.
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
	return v.(string), s.mime, "arquivo.bin", 0, nil
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
	http.ServeContent(w, r, "arquivo.bin", fi.ModTime(), f)
}

func (s *e2eMediaSvc) SendFileDedup(chatJID, msgType, filename, mimeType, caption, quotedID, clientMsgID string, data []byte) (string, error) {
	return "", nil
}

var _ whatsappSvc = (*e2eMediaSvc)(nil)

// newE2EMediaServer assembles the real chain (Compress -> MaxBody -> huma mux)
// and returns an httptest.Server that speaks real HTTP (round-trip over a
// socket, not ServeHTTP directly) — the same composition as
// internal/api/api.go ("outermost first" order: Bandwidth -> MaxBody ->
// Compress -> ... ; Bandwidth requires a complete *auth.Service and changes
// nothing relevant to what this test proves, which is why it stays out).
// Real authentication (JWT/cookie) runs in a layer this package does not
// own; to simulate the same effect (the user resolved in the context before
// requireAuth runs) without reimplementing the auth middleware from scratch, the
// outermost wrapper injects auth.WithUser into the context of the request the
// server receives — equivalent to the point where the real auth.Middleware would
// already have left the context by the time the request reaches mobilebff.Mount.
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

// TestE2E_WhatsAppMedia_RangeRequest_Devolve206ComContentRange proves, via a
// real HTTP request (a real TCP socket, not an in-memory ServeHTTP),
// that the media GET route returns 206 Partial Content with correct
// Content-Range and Accept-Ranges when the client asks for a byte range.
func TestE2E_WhatsAppMedia_RangeRequest_Devolve206ComContentRange(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/arquivo.bin"
	content := []byte("0123456789ABCDEFGHIJ") // 20 bytes, indices 0-19
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

// TestE2E_WhatsAppMedia_RangeInsatisfazivel_Devolve416 proves the 416 end to
// end when the requested range does not exist in the file.
func TestE2E_WhatsAppMedia_RangeInsatisfazivel_Devolve416(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/arquivo.bin"
	content := []byte("0123456789") // 10 bytes
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

// TestE2E_WhatsAppMedia_SemRange_NaoComprimeMidia proves that a full response
// (no Range) of a media Content-Type does not come out gzipped — media is
// already incompressible binary and isCompressible excludes image/*, so Compress
// must bypass even with the client announcing Accept-Encoding: gzip.
func TestE2E_WhatsAppMedia_SemRange_NaoComprimeMidia(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/arquivo.bin"
	content := []byte("conteudo binario de mentirinha, mas grande o bastante pra passar do minGzipBytes se fosse texto comprimivel " +
		"conteudo binario de mentirinha, mas grande o bastante pra passar do minGzipBytes se fosse texto comprimivel " +
		"conteudo binario de mentirinha, mas grande o bastante pra passar do minGzipBytes se fosse texto comprimivel")
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
		t.Fatalf("body corrompido: len(got)=%d len(want)=%d", len(body), len(content))
	}
}

// TestE2E_WhatsAppMedia_ConcorrenciaReal_ColapsaEmUmDownload fires N real
// concurrent HTTP requests (goroutines + srv.Client().Do)
// for the SAME (jid,msgID) on a simulated cache miss (downloadDelay>0) and proves
// that the backend only "downloads" once — without that protection, N users opening
// the same not-yet-cached thumbnail would fire N simultaneous downloads of the
// same remote file.
func TestE2E_WhatsAppMedia_ConcorrenciaReal_ColapsaEmUmDownload(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/arquivo.bin"
	if err := os.WriteFile(path, []byte("bytes-da-midia"), 0o600); err != nil {
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
	// A bounded wait: with a real collapse, the total time lands near ONE
	// downloadDelay (150ms), not N*150ms=1200ms — we use a generous slack
	// (600ms) so as not to be flaky on slower CI, while still
	// proving we are not summing the N delays in series.
	if elapsed > 600*time.Millisecond {
		t.Fatalf("elapsed = %v, want bounded near a single downloadDelay (did the collapse not happen?)", elapsed)
	}
	fmt.Printf("concorrencia: %d requests reais, %d download(s), %v\n", n, svc.downloadCalls, elapsed)
}
