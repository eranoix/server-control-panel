package mobilebff

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"server-control-panel/internal/whatsapp"
)

// newWhatsappMediaTestAPI assembles an isolated huma.API with only the media
// registrar — the same technique as newWhatsappTestAPI, avoiding any dependence
// on the global order/registration of the other handlers_*.go packages.
func newWhatsappMediaTestAPI(svc *fakeWhatsappSvc) (huma.API, *http.ServeMux) {
	mux := http.NewServeMux()
	api := humago.NewWithPrefix(mux, Prefix, huma.DefaultConfig("test", "0.0"))
	registerWhatsappMediaWithResolver(api, fakeResolver(svc))
	return api, mux
}

// TestWhatsAppMedia_Get_ChamaDownloadEDelegaParaServeMediaRel proves the
// required order: DownloadMediaForMessage resolves the cache hit/miss BEFORE the
// handler returns the StreamResponse, and the rel it returns is exactly what
// reaches ServeMediaRel — without that, a cache miss would serve a file that does
// not exist yet.
func TestWhatsAppMedia_Get_ChamaDownloadEDelegaParaServeMediaRel(t *testing.T) {
	svc := &fakeWhatsappSvc{
		downloadRel:  "chats/jid/msg123.jpg",
		downloadMime: "image/jpeg",
	}
	_, mux := newWhatsappMediaTestAPI(svc)

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/whatsapp/chats/jid/media/msg123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if svc.downloadCalls != 1 {
		t.Fatalf("DownloadMediaForMessage called %d times, want 1", svc.downloadCalls)
	}
	if svc.lastDownloadJID != "jid" || svc.lastDownloadMsg != "msg123" {
		t.Fatalf("jid/msgID = %q/%q, want jid/msg123", svc.lastDownloadJID, svc.lastDownloadMsg)
	}
	if svc.serveMediaCalls != 1 || svc.lastServedRel != "chats/jid/msg123.jpg" {
		t.Fatalf("ServeMediaRel called %d time(s) with rel=%q, want 1 with chats/jid/msg123.jpg", svc.serveMediaCalls, svc.lastServedRel)
	}
	if rec.Body.String() != "fake-media-bytes" {
		t.Fatalf("body = %q, want the bytes served by the fake ServeMediaRel", rec.Body.String())
	}
}

// TestWhatsAppMedia_Get_ErroDeDownloadViraStatusCorreto proves that
// *whatsapp.DownloadMediaError, returned directly by the handler, is mapped
// by huma to the right HTTP status through the StatusError interface — without
// rewriting the status mapping in the BFF (one single source of
// truth for the status).
func TestWhatsAppMedia_Get_ErroDeDownloadViraStatusCorreto(t *testing.T) {
	svc := &fakeWhatsappSvc{
		downloadErr: &whatsapp.DownloadMediaError{Status: http.StatusNotFound, Msg: "mensagem nao encontrada"},
	}
	_, mux := newWhatsappMediaTestAPI(svc)

	req := newAuthedRequest(http.MethodGet, "/api/mobile/v1/whatsapp/chats/jid/media/naoexiste", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestWhatsAppMedia_Get_Unauthenticated401 keeps parity with the BFF's other
// routes: with no session, 401 before any call to the service.
func TestWhatsAppMedia_Get_Unauthenticated401(t *testing.T) {
	svc := &fakeWhatsappSvc{}
	_, mux := newWhatsappMediaTestAPI(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/whatsapp/chats/jid/media/msg123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if svc.downloadCalls != 0 {
		t.Fatalf("DownloadMediaForMessage called %d times without auth, want 0", svc.downloadCalls)
	}
}

// buildMultipart assembles a multipart/form-data body with one file and text
// fields, returning the finished body and the Content-Type (with boundary) to use
// in the test request's header.
func buildMultipart(t *testing.T, filename string, fileContent []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("WriteField(%s): %v", k, err)
		}
	}
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(fileContent); err != nil {
		t.Fatalf("write file content: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buf, w.FormDataContentType()
}

// TestWhatsAppMedia_Upload_DelegaParaSendFileDedup proves that the multipart's
// bytes and fields reach SendFileDedup intact, and that the response returns the
// id the fake "sent".
func TestWhatsAppMedia_Upload_DelegaParaSendFileDedup(t *testing.T) {
	svc := &fakeWhatsappSvc{sendFileID: "wamid-upload-1"}
	_, mux := newWhatsappMediaTestAPI(svc)

	content := []byte("conteudo-do-arquivo-de-teste")
	body, contentType := buildMultipart(t, "foto.jpg", content, map[string]string{
		"client_msg_id": "cliente-upload-1",
		"caption":       "uma legenda",
		"quoted_id":     "msg-anterior",
	})

	req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/whatsapp/chats/jid123/media", nil)
	req.Body = io.NopCloser(body)
	req.ContentLength = int64(body.Len())
	req.Header.Set("Content-Type", contentType)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if svc.sendFileCalls != 1 {
		t.Fatalf("SendFileDedup called %d times, want 1", svc.sendFileCalls)
	}
	if svc.lastSendFileJID != "jid123" {
		t.Fatalf("jid = %q, want jid123", svc.lastSendFileJID)
	}
	got := svc.lastSendFileArg
	if got.filename != "foto.jpg" || got.caption != "uma legenda" || got.quotedID != "msg-anterior" || got.clientMsgID != "cliente-upload-1" {
		t.Fatalf("args = %+v", got)
	}
	if !bytes.Equal(got.data, content) {
		t.Fatalf("data = %q, want %q (file bytes must arrive intact)", got.data, content)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("wamid-upload-1")) {
		t.Fatalf("body = %s, want to contain the id returned by the fake", rec.Body.String())
	}
}

// TestWhatsAppMedia_Upload_SemArquivoDevolve422 ensures that "file" is
// required: without it, huma stops the request at multipart validation
// (422 — the same status as any other missing required field in the BFF)
// before any call to the service.
func TestWhatsAppMedia_Upload_SemArquivoDevolve422(t *testing.T) {
	svc := &fakeWhatsappSvc{sendFileID: "nao-deveria-ser-usado"}
	_, mux := newWhatsappMediaTestAPI(svc)

	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	if err := w.WriteField("client_msg_id", "x"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := newAuthedRequest(http.MethodPost, "/api/mobile/v1/whatsapp/chats/jid/media", nil)
	req.Body = io.NopCloser(buf)
	req.ContentLength = int64(buf.Len())
	req.Header.Set("Content-Type", w.FormDataContentType())

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", rec.Code, rec.Body.String())
	}
	if svc.sendFileCalls != 0 {
		t.Fatalf("SendFileDedup called %d times without a file, want 0", svc.sendFileCalls)
	}
}

// TestIsWhatsAppMediaUpload_CasaSoARotaDeUpload proves the matcher used by
// RegisterLargeBody: it matches the exact upload route, but not the download one
// (same prefix, different method) nor any other route.
func TestIsWhatsAppMediaUpload_CasaSoARotaDeUpload(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/api/mobile/v1/whatsapp/chats/551199@s.whatsapp.net/media", true},
		{http.MethodGet, "/api/mobile/v1/whatsapp/chats/551199@s.whatsapp.net/media", false},
		{http.MethodPost, "/api/mobile/v1/whatsapp/chats/551199@s.whatsapp.net/media/msg1", false},
		{http.MethodPost, "/api/mobile/v1/whatsapp/chats//media", false},
		{http.MethodPost, "/api/mobile/v1/whatsapp/chats/jid/messages", false},
		{http.MethodPost, "/api/mobile/v1/files/upload/chunk", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		if got := isWhatsAppMediaUpload(req); got != c.want {
			t.Errorf("isWhatsAppMediaUpload(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}
