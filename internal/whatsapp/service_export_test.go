package whatsapp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeExportBackend implements the whole Backend with no-ops, except for the
// methods each test needs to observe/control. It avoids any real network call —
// the tests in this file build a *Service directly (without going through
// New(), which spins up background pollers) so that they depend on no method
// beyond the ones being exercised.
type fakeExportBackend struct {
	mu sync.Mutex

	sendTextCalls int
	sendTextID    string
	sendTextErr   error

	sendFileCalls int
	sendFileID    string
	sendFileErr   error

	markChatReadErr error

	profilePictureURL string
	profilePictureErr error

	chatMessagesPaged []wahaHistoryMsg

	chatMessagesWithMedia    []wahaHistoryMsg
	chatMessagesWithMediaErr error

	downloadFileCalls int
	downloadFileData  []byte
	downloadFileMime  string
	downloadFileErr   error
}

func (f *fakeExportBackend) GetSession() (*wahaSession, error) { return nil, nil }
func (f *fakeExportBackend) StartSession() error               { return nil }
func (f *fakeExportBackend) RestartSession() error             { return nil }
func (f *fakeExportBackend) StopSession() error                { return nil }
func (f *fakeExportBackend) LogoutSession() error              { return nil }
func (f *fakeExportBackend) GetQR() (string, error)            { return "", nil }
func (f *fakeExportBackend) EnsureExtraWebhook(string, string, []string) error {
	return nil
}

func (f *fakeExportBackend) ListChatsOverview() ([]wahaChatOverview, error) { return nil, nil }
func (f *fakeExportBackend) ListAllContacts() ([]wahaContact, error)        { return nil, nil }
func (f *fakeExportBackend) GetContact(string) (*wahaContact, error)        { return nil, nil }
func (f *fakeExportBackend) MarkChatRead(chatJID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.markChatReadErr
}
func (f *fakeExportBackend) PinChat(string, bool) error     { return nil }
func (f *fakeExportBackend) ArchiveChat(string, bool) error { return nil }
func (f *fakeExportBackend) MuteChat(string, bool) error    { return nil }
func (f *fakeExportBackend) BlockContact(string, bool) error {
	return nil
}
func (f *fakeExportBackend) CheckNumber(string) (string, bool, error) { return "", false, nil }
func (f *fakeExportBackend) GroupInfo(string) (string, []WAGroupParticipant, error) {
	return "", nil, nil
}

func (f *fakeExportBackend) SendText(chatJID, text, quotedID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendTextCalls++
	if f.sendTextErr != nil {
		return "", f.sendTextErr
	}
	return f.sendTextID, nil
}
func (f *fakeExportBackend) SendFile(string, string, string, string, string, string, []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sendFileCalls++
	if f.sendFileErr != nil {
		return "", f.sendFileErr
	}
	return f.sendFileID, nil
}
func (f *fakeExportBackend) React(string, string, string, string) error    { return nil }
func (f *fakeExportBackend) StarMessage(string, string, bool) error        { return nil }
func (f *fakeExportBackend) DeleteMessage(string, string, string) error    { return nil }
func (f *fakeExportBackend) EditMessage(string, string, string) error      { return nil }
func (f *fakeExportBackend) ForwardMessage(string, string) (string, error) { return "", nil }

func (f *fakeExportBackend) GetChatMessages(string, int) ([]wahaHistoryMsg, error) { return nil, nil }
func (f *fakeExportBackend) GetChatMessagesPaged(jid string, limit, offset int) ([]wahaHistoryMsg, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if offset > 0 {
		return nil, nil
	}
	return f.chatMessagesPaged, nil
}
func (f *fakeExportBackend) GetChatMessagesWithMedia(string, int) ([]wahaHistoryMsg, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chatMessagesWithMedia, f.chatMessagesWithMediaErr
}
func (f *fakeExportBackend) DownloadFile(fileURL string, dst io.Writer) (int64, string, error) {
	f.mu.Lock()
	f.downloadFileCalls++
	data, mimeType, err := f.downloadFileData, f.downloadFileMime, f.downloadFileErr
	f.mu.Unlock()
	if err != nil {
		return 0, "", err
	}
	n, werr := dst.Write(data)
	if werr != nil {
		return 0, "", werr
	}
	return int64(n), mimeType, nil
}
func (f *fakeExportBackend) GetProfilePicture(string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.profilePictureURL, f.profilePictureErr
}
func (f *fakeExportBackend) RequestHistory(string, string, bool, int64, int) error { return nil }
func (f *fakeExportBackend) ResendMessage(string, string, string) error            { return nil }

func (f *fakeExportBackend) SubscribePresence(string) error { return nil }
func (f *fakeExportBackend) SendTyping(string, bool) error  { return nil }

var _ Backend = (*fakeExportBackend)(nil)

// newExportTestService assembles a minimal *Service without going through New()
// (which spins up background pollers against the real Backend). Enough for the
// exported wrappers, which only touch Store + Client.
func newExportTestService(t *testing.T, backend Backend) *Service {
	t.Helper()
	store, err := NewStore(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return &Service{
		Store:       store,
		Client:      backend,
		avatarCache: map[string]avatarEntry{},
	}
}

// TestSendTextDedup locks down idempotency: the same client_msg_id sent twice
// calls Client.SendText ONCE only and always returns the same id.
func TestSendTextDedup(t *testing.T) {
	backend := &fakeExportBackend{sendTextID: "wamid-1"}
	svc := newExportTestService(t, backend)

	id1, err := svc.SendTextDedup("5511999998888@c.us", "oi", "", "cliente-abc")
	if err != nil {
		t.Fatalf("1st SendTextDedup: %v", err)
	}
	id2, err := svc.SendTextDedup("5511999998888@c.us", "oi", "", "cliente-abc")
	if err != nil {
		t.Fatalf("2nd SendTextDedup: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("ids differ between calls with the same client_msg_id: %q != %q", id1, id2)
	}
	if id1 != "wamid-1" {
		t.Fatalf("id = %q, want wamid-1", id1)
	}
	backend.mu.Lock()
	calls := backend.sendTextCalls
	backend.mu.Unlock()
	if calls != 1 {
		t.Fatalf("Client.SendText called %d times, want 1 (client_msg_id should have deduplicated)", calls)
	}
}

// TestSendTextDedupDiferentesClientMsgIDNaoDeduplicam makes sure the dedupe key
// is the client_msg_id — distinct messages are still sent.
func TestSendTextDedupDiferentesClientMsgIDNaoDeduplicam(t *testing.T) {
	backend := &fakeExportBackend{sendTextID: "wamid-1"}
	svc := newExportTestService(t, backend)

	if _, err := svc.SendTextDedup("jid", "a", "", "id-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendTextDedup("jid", "b", "", "id-2"); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	calls := backend.sendTextCalls
	backend.mu.Unlock()
	if calls != 2 {
		t.Fatalf("Client.SendText called %d times, want 2 (different client_msg_ids)", calls)
	}
}

// TestMessagesForDisplayDisparaBackfillQuandoStoreLocalEstaAtras covers the
// backfill-on-open path: an empty local store (fewer than the limit) triggers
// backfillFromWAHA/requestHistoryGap, exactly as handleMessagesList always
// triggered it.
func TestMessagesForDisplayDisparaBackfillQuandoStoreLocalEstaAtras(t *testing.T) {
	backend := &fakeExportBackend{
		chatMessagesPaged: []wahaHistoryMsg{
			{ID: "wamid-100", From: "5511999998888@c.us", Timestamp: 1700000000, Body: "olá"},
		},
	}
	svc := newExportTestService(t, backend)

	msgs, backfilling, err := svc.MessagesForDisplay("5511999998888@c.us", MessagesQuery{Limit: 50})
	if err != nil {
		t.Fatalf("MessagesForDisplay: %v", err)
	}
	if !backfilling {
		t.Fatal("backfilling = false, want true (local store empty, below the limit)")
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1 (message brought in by the backfill)", len(msgs))
	}
	if msgs[0].ID != "wamid-100" {
		t.Fatalf("msgs[0].ID = %q, want wamid-100", msgs[0].ID)
	}
}

// TestMessagesForDisplaySemBackfillQuandoStoreJaCompleto: the local store
// already holds `limit` messages and there is no newer chat in the overview →
// no backfill is triggered.
func TestMessagesForDisplaySemBackfillQuandoStoreJaCompleto(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)
	jid := "5511999998888@c.us"
	if err := svc.Store.AppendMessage(Message{ID: "m1", ChatJID: jid, TS: 100, Body: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertChat(Chat{JID: jid, LastMsgTS: 100}); err != nil {
		t.Fatal(err)
	}

	_, backfilling, err := svc.MessagesForDisplay(jid, MessagesQuery{Limit: 1})
	if err != nil {
		t.Fatalf("MessagesForDisplay: %v", err)
	}
	if backfilling {
		t.Fatal("backfilling = true, want false (the store already has `limit` msgs and nothing newer in the overview)")
	}
}

// TestMarkRead makes sure the Backend's error reaches the caller instead of
// being swallowed — unlike the legacy handleMarkRead, which is best-effort on
// purpose.
func TestMarkRead(t *testing.T) {
	wantErr := errors.New("waha indisponível")
	backend := &fakeExportBackend{markChatReadErr: wantErr}
	svc := newExportTestService(t, backend)

	err := svc.MarkRead("5511999998888@c.us")
	if !errors.Is(err, wantErr) {
		t.Fatalf("MarkRead error = %v, want %v (it must not be swallowed)", err, wantErr)
	}
}

func TestMarkReadSemErro(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)
	if err := svc.MarkRead("jid"); err != nil {
		t.Fatalf("MarkRead with no backend error = %v, want nil", err)
	}
}

// TestServeAvatarSemFoto covers the proxy with no URL available at all (no
// cache, no chat carrying an AvatarURL, Backend.GetProfilePicture returns
// empty): it answers 204 (contact with no photo), the same contract as the
// original handleAvatar.
func TestServeAvatarSemFoto(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/whatsapp/chats/jid/avatar", nil)
	w := httptest.NewRecorder()
	svc.ServeAvatar(w, req, "5511999998888@c.us")

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (no photo)", w.Code, http.StatusNoContent)
	}
}

// TestHandleAvatarDelegaParaServeAvatar makes sure the extraction did not break
// the legacy path: handleAvatar (parsing r.URL.Path) still serves the same 204
// when there is no photo.
func TestHandleAvatarDelegaParaServeAvatar(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)

	req := httptest.NewRequest(http.MethodGet, "/api/whatsapp/avatar/5511999998888@c.us", nil)
	w := httptest.NewRecorder()
	svc.handleAvatar(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (no photo)", w.Code, http.StatusNoContent)
	}
}

// TestDownloadMediaForMessageCacheHitNaoBaixaDaRede covers the short-circuit:
// Media.Path already points at a file that exists under MediaRoot → it returns
// right away, without calling Client.DownloadFile a single time.
func TestDownloadMediaForMessageCacheHitNaoBaixaDaRede(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)
	jid := "5511999998888@c.us"
	msgID := "wamid.CACHED"

	rel := filepath.Join(chatDir(jid), sanitizeMsgID(msgID)+".jpg")
	full := filepath.Join(svc.Store.MediaRoot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("ja-cacheado"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AppendMessage(Message{
		ID: msgID, ChatJID: jid, TS: 1,
		Media: &Media{Path: rel, MimeType: "image/jpeg", Size: 11},
	}); err != nil {
		t.Fatal(err)
	}

	gotRel, gotMime, _, gotSize, err := svc.DownloadMediaForMessage(jid, msgID)
	if err != nil {
		t.Fatalf("DownloadMediaForMessage: %v", err)
	}
	if gotRel != rel {
		t.Fatalf("rel = %q, want %q", gotRel, rel)
	}
	if gotMime != "image/jpeg" || gotSize != 11 {
		t.Fatalf("mime/size = %q/%d, want image/jpeg/11", gotMime, gotSize)
	}
	backend.mu.Lock()
	calls := backend.downloadFileCalls
	backend.mu.Unlock()
	if calls != 0 {
		t.Fatalf("Client.DownloadFile called %d times, want 0 (file already in the local cache)", calls)
	}
}

// TestDownloadMediaForMessageCacheMissBaixaEPersiste covers the cache miss:
// with no local file, it downloads through WAHA (Client.GetChatMessagesWithMedia
// + DownloadFile) and writes to <chatDir>/<safeID>.<ext>, updating the store.
func TestDownloadMediaForMessageCacheMissBaixaEPersiste(t *testing.T) {
	jid := "5511999998888@c.us"
	msgID := "wamid.NAOCACHEADO"
	backend := &fakeExportBackend{
		chatMessagesWithMedia: []wahaHistoryMsg{
			{ID: msgID, MediaURL: "https://waha.local/files/abc.png", MimeType: "image/png", Filename: "foto.png"},
		},
		downloadFileData: []byte("bytes-da-imagem"),
		downloadFileMime: "image/png",
	}
	svc := newExportTestService(t, backend)
	if err := svc.Store.AppendMessage(Message{
		ID: msgID, ChatJID: jid, TS: 1,
		Media: &Media{MimeType: "image/png", Filename: "foto.png"},
	}); err != nil {
		t.Fatal(err)
	}

	wantRel := filepath.Join(chatDir(jid), sanitizeMsgID(msgID)+".png")
	gotRel, gotMime, gotFilename, gotSize, err := svc.DownloadMediaForMessage(jid, msgID)
	if err != nil {
		t.Fatalf("DownloadMediaForMessage: %v", err)
	}
	if gotRel != wantRel {
		t.Fatalf("rel = %q, want %q (layout chatDir/sanitizeMsgID+ext)", gotRel, wantRel)
	}
	if gotMime != "image/png" || gotFilename != "foto.png" || gotSize != int64(len("bytes-da-imagem")) {
		t.Fatalf("mime/filename/size = %q/%q/%d, want image/png/foto.png/%d", gotMime, gotFilename, gotSize, len("bytes-da-imagem"))
	}
	backend.mu.Lock()
	calls := backend.downloadFileCalls
	backend.mu.Unlock()
	if calls != 1 {
		t.Fatalf("Client.DownloadFile called %d times, want 1", calls)
	}

	// Store.UpdateMessageMedia was called — the message in the store now
	// reflects the local path.
	updated, err := svc.Store.FindMessage(jid, msgID)
	if err != nil || updated == nil {
		t.Fatalf("FindMessage after the download: %v", err)
	}
	if updated.Media.Path != wantRel {
		t.Fatalf("Media.Path in the store = %q, want %q (UpdateMessageMedia was not called)", updated.Media.Path, wantRel)
	}

	// Bytes actually written to disk at the path that was returned.
	full := filepath.Join(svc.Store.MediaRoot, gotRel)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("the downloaded file does not exist at %s: %v", full, err)
	}
	if string(data) != "bytes-da-imagem" {
		t.Fatalf("content of the downloaded file = %q, want %q", data, "bytes-da-imagem")
	}
}

// TestDownloadMediaForMessageMensagemInexistenteDevolve404 pins the exact
// status (404) that the legacy JSON handler always returned when the message
// does not exist in the local store.
func TestDownloadMediaForMessageMensagemInexistenteDevolve404(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)

	_, _, _, _, err := svc.DownloadMediaForMessage("jid", "nao-existe")
	if err == nil {
		t.Fatal("err = nil, want DownloadMediaError 404")
	}
	var de *DownloadMediaError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v (%T), want *DownloadMediaError", err, err)
	}
	if de.Status != http.StatusNotFound {
		t.Fatalf("Status = %d, want %d", de.Status, http.StatusNotFound)
	}
}

// TestDownloadMediaForMessageConcorrenciaColapsaEmUmDownload proves that N
// concurrent requests for the SAME (chatJID,msgID), not yet cached, result in
// ONE single network download — without that, opening the same media in
// parallel (two tabs, an automatic retry) would fire N identical downloads.
func TestDownloadMediaForMessageConcorrenciaColapsaEmUmDownload(t *testing.T) {
	jid := "5511999998888@c.us"
	msgID := "wamid.CONCORRENTE"
	release := make(chan struct{})
	backend := &blockingDownloadBackend{
		fakeExportBackend: fakeExportBackend{
			chatMessagesWithMedia: []wahaHistoryMsg{
				{ID: msgID, MediaURL: "https://waha.local/files/x.png", MimeType: "image/png"},
			},
			downloadFileData: []byte("dados"),
			downloadFileMime: "image/png",
		},
		release: release,
	}
	svc := newExportTestService(t, backend)
	if err := svc.Store.AppendMessage(Message{ID: msgID, ChatJID: jid, TS: 1, Media: &Media{MimeType: "image/png"}}); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, _, _, _, err := svc.DownloadMediaForMessage(jid, msgID)
			errs[i] = err
		}(i)
	}
	// Gives every goroutine time to enter the singleflight before releasing
	// the simulated download.
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	backend.mu.Lock()
	calls := backend.downloadFileCalls
	backend.mu.Unlock()
	if calls != 1 {
		t.Fatalf("Client.DownloadFile called %d times with %d concurrent requests, want 1 (singleflight)", calls, n)
	}
}

// blockingDownloadBackend holds DownloadFile until `release` closes, so that
// every test goroutine has time to enter singleflight.Do before any one of
// them completes.
type blockingDownloadBackend struct {
	fakeExportBackend
	release chan struct{}
}

func (b *blockingDownloadBackend) DownloadFile(fileURL string, dst io.Writer) (int64, string, error) {
	<-b.release
	return b.fakeExportBackend.DownloadFile(fileURL, dst)
}

// TestSendFileDedupColapsaPorClientMsgID locks down upload idempotency: two
// calls with the same client_msg_id call Client.SendFile ONCE and return the
// same id — the very guarantee SendTextDedup already provides.
func TestSendFileDedupColapsaPorClientMsgID(t *testing.T) {
	backend := &fakeExportBackend{sendFileID: "wamid-file-1"}
	svc := newExportTestService(t, backend)
	data := []byte("conteudo-do-arquivo")

	id1, err := svc.SendFileDedup("jid", "image", "foto.jpg", "image/jpeg", "", "", "cliente-xyz", data)
	if err != nil {
		t.Fatalf("1st SendFileDedup: %v", err)
	}
	id2, err := svc.SendFileDedup("jid", "image", "foto.jpg", "image/jpeg", "", "", "cliente-xyz", data)
	if err != nil {
		t.Fatalf("2nd SendFileDedup: %v", err)
	}
	if id1 != id2 || id1 != "wamid-file-1" {
		t.Fatalf("ids = %q/%q, want wamid-file-1/wamid-file-1", id1, id2)
	}
	backend.mu.Lock()
	calls := backend.sendFileCalls
	backend.mu.Unlock()
	if calls != 1 {
		t.Fatalf("Client.SendFile called %d times, want 1 (client_msg_id should have deduplicated)", calls)
	}
}

// TestSendFileDedupPersisteBytesLocalmente makes sure the bytes that were sent
// stay in MediaRoot under the <chatDir>/<safeID>.<ext> layout — so the media
// shows up inline, with no "Download", after a reload.
func TestSendFileDedupPersisteBytesLocalmente(t *testing.T) {
	backend := &fakeExportBackend{sendFileID: "wamid-file-2"}
	svc := newExportTestService(t, backend)
	jid := "5511999998888@c.us"
	data := []byte("bytes-enviados")

	id, err := svc.SendFileDedup(jid, "image", "foto.png", "image/png", "legenda", "", "cliente-abc-2", data)
	if err != nil {
		t.Fatalf("SendFileDedup: %v", err)
	}

	wantRel := filepath.Join(chatDir(jid), sanitizeMsgID(id)+".png")
	full := filepath.Join(svc.Store.MediaRoot, wantRel)
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("file not persisted at %s: %v", full, err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("persisted content = %q, want %q", got, data)
	}
}

// TestSendFileDedupInfereTipoQuandoVazio makes sure an empty msgType/mimeType
// is inferred (guessMsgType / extension) exactly as handleSendFile always did
// — the mobile BFF depends on this because guessMsgType is not
// exported.
func TestSendFileDedupInfereTipoQuandoVazio(t *testing.T) {
	backend := &fakeExportBackend{sendFileID: "wamid-file-3"}
	svc := newExportTestService(t, backend)
	jid := "jid"
	data := []byte("audio")

	id, err := svc.SendFileDedup(jid, "", "nota.ogg", "audio/ogg", "", "", "cliente-3", data)
	if err != nil {
		t.Fatalf("SendFileDedup: %v", err)
	}
	// inferred type = "voice" (guessMsgType classifies audio/* as voice) →
	// it persisted (it is neither "" nor "text").
	wantRel := filepath.Join(chatDir(jid), sanitizeMsgID(id)+".ogg")
	full := filepath.Join(svc.Store.MediaRoot, wantRel)
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("file not persisted at %s (the type should have been inferred as media): %v", full, err)
	}
}

// TestServeMediaRelRangeRequestDevolve206 proves Range support: it reuses
// http.ServeContent (it does not reimplement Range parsing), so a
// `Range: bytes=0-3` request returns 206 with the correct slice.
func TestServeMediaRelRangeRequestDevolve206(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)
	jid := "jid"
	rel := filepath.Join(chatDir(jid), "arquivo.txt")
	full := filepath.Join(svc.Store.MediaRoot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "0123456789"
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/whatsapp/chats/"+jid+"/media/x", nil)
	req.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	svc.ServeMediaRel(w, req, rel)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d (206)", w.Code, http.StatusPartialContent)
	}
	if got := w.Body.String(); got != "0123" {
		t.Fatalf("body = %q, want %q", got, "0123")
	}
	if cr := w.Header().Get("Content-Range"); cr != "bytes 0-3/10" {
		t.Fatalf("Content-Range = %q, want %q", cr, "bytes 0-3/10")
	}
	if ar := w.Header().Get("Accept-Ranges"); ar != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", ar)
	}
}

// TestServeMediaRelRangeInsatisfazivelDevolve416 covers the other side of the
// Range contract: a range outside the file's bounds returns 416, the same
// default behaviour http.ServeContent has.
func TestServeMediaRelRangeInsatisfazivelDevolve416(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)
	jid := "jid"
	rel := filepath.Join(chatDir(jid), "arquivo.txt")
	full := filepath.Join(svc.Store.MediaRoot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/whatsapp/chats/"+jid+"/media/x", nil)
	req.Header.Set("Range", "bytes=1000-2000")
	w := httptest.NewRecorder()
	svc.ServeMediaRel(w, req, rel)

	if w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want %d (416)", w.Code, http.StatusRequestedRangeNotSatisfiable)
	}
}

// TestServeMediaRelRejeitaTraversal makes sure the anti-traversal
// (safeMediaPath + re-anchor check) is still active after the extraction.
func TestServeMediaRelRejeitaTraversal(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)

	req := httptest.NewRequest(http.MethodGet, "/whatsapp/chats/jid/media/x", nil)
	w := httptest.NewRecorder()
	svc.ServeMediaRel(w, req, "../../../etc/passwd")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (traversal rejected)", w.Code, http.StatusBadRequest)
	}
}
