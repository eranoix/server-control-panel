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

func TestSendTextDedup(t *testing.T) {
	backend := &fakeExportBackend{sendTextID: "wamid-1"}
	svc := newExportTestService(t, backend)

	id1, err := svc.SendTextDedup("5511999998888@c.us", "oi", "", "client-abc")
	if err != nil {
		t.Fatalf("1st SendTextDedup: %v", err)
	}
	id2, err := svc.SendTextDedup("5511999998888@c.us", "oi", "", "client-abc")
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

func TestSendTextDedupDifferentClientMsgIDsDoNotDedup(t *testing.T) {
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

func TestMessagesForDisplayTriggersBackfillWhenLocalStoreBehind(t *testing.T) {
	backend := &fakeExportBackend{
		chatMessagesPaged: []wahaHistoryMsg{
			{ID: "wamid-100", From: "5511999998888@c.us", Timestamp: 1700000000, Body: "hello"},
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

func TestMessagesForDisplayNoBackfillWhenStoreComplete(t *testing.T) {
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

func TestMarkRead(t *testing.T) {
	wantErr := errors.New("waha unavailable")
	backend := &fakeExportBackend{markChatReadErr: wantErr}
	svc := newExportTestService(t, backend)

	err := svc.MarkRead("5511999998888@c.us")
	if !errors.Is(err, wantErr) {
		t.Fatalf("MarkRead error = %v, want %v (it must not be swallowed)", err, wantErr)
	}
}

func TestMarkReadNoError(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)
	if err := svc.MarkRead("jid"); err != nil {
		t.Fatalf("MarkRead with no backend error = %v, want nil", err)
	}
}

func TestServeAvatarNoPhoto(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)

	req := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/whatsapp/chats/jid/avatar", nil)
	w := httptest.NewRecorder()
	svc.ServeAvatar(w, req, "5511999998888@c.us")

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (no photo)", w.Code, http.StatusNoContent)
	}
}

func TestHandleAvatarDelegatesToServeAvatar(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)

	req := httptest.NewRequest(http.MethodGet, "/api/whatsapp/avatar/5511999998888@c.us", nil)
	w := httptest.NewRecorder()
	svc.handleAvatar(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d (no photo)", w.Code, http.StatusNoContent)
	}
}

func TestDownloadMediaForMessageCacheHitSkipsNetwork(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)
	jid := "5511999998888@c.us"
	msgID := "wamid.CACHED"

	rel := filepath.Join(chatDir(jid), sanitizeMsgID(msgID)+".jpg")
	full := filepath.Join(svc.Store.MediaRoot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("already-cached"), 0o600); err != nil {
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

func TestDownloadMediaForMessageCacheMissDownloadsAndPersists(t *testing.T) {
	jid := "5511999998888@c.us"
	msgID := "wamid.NAOCACHEADO"
	backend := &fakeExportBackend{
		chatMessagesWithMedia: []wahaHistoryMsg{
			{ID: msgID, MediaURL: "https://waha.local/files/abc.png", MimeType: "image/png", Filename: "photo.png"},
		},
		downloadFileData: []byte("image-bytes"),
		downloadFileMime: "image/png",
	}
	svc := newExportTestService(t, backend)
	if err := svc.Store.AppendMessage(Message{
		ID: msgID, ChatJID: jid, TS: 1,
		Media: &Media{MimeType: "image/png", Filename: "photo.png"},
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
	if gotMime != "image/png" || gotFilename != "photo.png" || gotSize != int64(len("image-bytes")) {
		t.Fatalf("mime/filename/size = %q/%q/%d, want image/png/photo.png/%d", gotMime, gotFilename, gotSize, len("image-bytes"))
	}
	backend.mu.Lock()
	calls := backend.downloadFileCalls
	backend.mu.Unlock()
	if calls != 1 {
		t.Fatalf("Client.DownloadFile called %d times, want 1", calls)
	}

	updated, err := svc.Store.FindMessage(jid, msgID)
	if err != nil || updated == nil {
		t.Fatalf("FindMessage after the download: %v", err)
	}
	if updated.Media.Path != wantRel {
		t.Fatalf("Media.Path in the store = %q, want %q (UpdateMessageMedia was not called)", updated.Media.Path, wantRel)
	}

	full := filepath.Join(svc.Store.MediaRoot, gotRel)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("the downloaded file does not exist at %s: %v", full, err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("content of the downloaded file = %q, want %q", data, "image-bytes")
	}
}

func TestDownloadMediaForMessageUnknownMessageReturns404(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)

	_, _, _, _, err := svc.DownloadMediaForMessage("jid", "missing")
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

func TestDownloadMediaForMessageConcurrencyCollapsesToOneDownload(t *testing.T) {
	jid := "5511999998888@c.us"
	msgID := "wamid.CONCURRENT"
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

type blockingDownloadBackend struct {
	fakeExportBackend
	release chan struct{}
}

func (b *blockingDownloadBackend) DownloadFile(fileURL string, dst io.Writer) (int64, string, error) {
	<-b.release
	return b.fakeExportBackend.DownloadFile(fileURL, dst)
}

func TestSendFileDedupCollapsesByClientMsgID(t *testing.T) {
	backend := &fakeExportBackend{sendFileID: "wamid-file-1"}
	svc := newExportTestService(t, backend)
	data := []byte("file-content")

	id1, err := svc.SendFileDedup("jid", "image", "photo.jpg", "image/jpeg", "", "", "client-xyz", data)
	if err != nil {
		t.Fatalf("1st SendFileDedup: %v", err)
	}
	id2, err := svc.SendFileDedup("jid", "image", "photo.jpg", "image/jpeg", "", "", "client-xyz", data)
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

func TestSendFileDedupPersistsBytesLocally(t *testing.T) {
	backend := &fakeExportBackend{sendFileID: "wamid-file-2"}
	svc := newExportTestService(t, backend)
	jid := "5511999998888@c.us"
	data := []byte("sent-bytes")

	id, err := svc.SendFileDedup(jid, "image", "photo.png", "image/png", "caption", "", "client-abc-2", data)
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

func TestSendFileDedupInfersTypeWhenEmpty(t *testing.T) {
	backend := &fakeExportBackend{sendFileID: "wamid-file-3"}
	svc := newExportTestService(t, backend)
	jid := "jid"
	data := []byte("audio")

	id, err := svc.SendFileDedup(jid, "", "note.ogg", "audio/ogg", "", "", "client-3", data)
	if err != nil {
		t.Fatalf("SendFileDedup: %v", err)
	}
	wantRel := filepath.Join(chatDir(jid), sanitizeMsgID(id)+".ogg")
	full := filepath.Join(svc.Store.MediaRoot, wantRel)
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("file not persisted at %s (the type should have been inferred as media): %v", full, err)
	}
}

func TestServeMediaRelRangeRequestReturns206(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)
	jid := "jid"
	rel := filepath.Join(chatDir(jid), "file.txt")
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

func TestServeMediaRelUnsatisfiableRangeReturns416(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)
	jid := "jid"
	rel := filepath.Join(chatDir(jid), "file.txt")
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

func TestServeMediaRelRejectsTraversal(t *testing.T) {
	backend := &fakeExportBackend{}
	svc := newExportTestService(t, backend)

	req := httptest.NewRequest(http.MethodGet, "/whatsapp/chats/jid/media/x", nil)
	w := httptest.NewRecorder()
	svc.ServeMediaRel(w, req, "../../../etc/passwd")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (traversal rejected)", w.Code, http.StatusBadRequest)
	}
}
