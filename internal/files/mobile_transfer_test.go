package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadSession_OutOfOrderChunks_AssemblesCorrectly(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	original := make([]byte, 300)
	rand.New(rand.NewSource(1)).Read(original)
	chunk0 := original[0:100]
	chunk1 := original[100:200]
	chunk2 := original[200:300]

	sessionID, err := InitUpload(dataDir, destDir, "file.bin", int64(len(original)))
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}

	if _, err := WriteChunk(dataDir, sessionID, 200, chunk2); err != nil {
		t.Fatalf("WriteChunk(chunk2): %v", err)
	}
	if _, err := WriteChunk(dataDir, sessionID, 0, chunk0); err != nil {
		t.Fatalf("WriteChunk(chunk0): %v", err)
	}
	received, err := WriteChunk(dataDir, sessionID, 100, chunk1)
	if err != nil {
		t.Fatalf("WriteChunk(chunk1): %v", err)
	}
	if received != int64(len(original)) {
		t.Fatalf("received_bytes = %d, want %d", received, len(original))
	}

	finalPath, err := CompleteUpload(dataDir, sessionID)
	if err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	if want := filepath.Join(destDir, "file.bin"); finalPath != want {
		t.Fatalf("finalPath = %q, want %q", finalPath, want)
	}

	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("the assembled file differs from the original (out-of-order broke the assembly)")
	}
	if sha256sum(got) != sha256sum(original) {
		t.Fatal("the hash of the assembled file differs from the original")
	}
}

func TestUploadSession_IncompleteCannotComplete(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "partial.bin", 300)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	if _, err := WriteChunk(dataDir, sessionID, 0, make([]byte, 100)); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}

	if _, err := CompleteUpload(dataDir, sessionID); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("CompleteUpload on an incomplete session: err = %v, want ErrIncomplete", err)
	}

	received, total, err := SessionStatus(dataDir, sessionID)
	if err != nil {
		t.Fatalf("SessionStatus: %v", err)
	}
	if received != 100 || total != 300 {
		t.Fatalf("SessionStatus = (%d, %d), want (100, 300)", received, total)
	}

	if _, err := WriteChunk(dataDir, sessionID, 100, make([]byte, 200)); err != nil {
		t.Fatalf("WriteChunk (resumed): %v", err)
	}
	if _, err := CompleteUpload(dataDir, sessionID); err != nil {
		t.Fatalf("CompleteUpload after resuming: %v", err)
	}
}

func TestUploadSession_DuplicateChunkDoesNotInflateReceivedBytes(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "dup.bin", 100)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	data := bytes.Repeat([]byte{0x42}, 100)
	if _, err := WriteChunk(dataDir, sessionID, 0, data[:60]); err != nil {
		t.Fatalf("WriteChunk 1: %v", err)
	}
	if _, err := WriteChunk(dataDir, sessionID, 0, data[:60]); err != nil {
		t.Fatalf("WriteChunk 1 (retry): %v", err)
	}
	received, err := WriteChunk(dataDir, sessionID, 60, data[60:])
	if err != nil {
		t.Fatalf("WriteChunk 2: %v", err)
	}
	if received != 100 {
		t.Fatalf("received_bytes = %d, want 100 (no inflating because of the retry)", received)
	}
}

func TestUploadSession_OutOfRangeOffset_Rejected(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "small.bin", 100)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	if _, err := WriteChunk(dataDir, sessionID, 90, make([]byte, 50)); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("WriteChunk past the total: err = %v, want ErrInvalidChunk", err)
	}
}

func TestUploadSession_UnknownSession_NotFound(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := WriteChunk(dataDir, "missing-session", 0, []byte("x")); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("WriteChunk on a nonexistent session: err = %v, want ErrSessionNotFound", err)
	}
	if _, err := CompleteUpload(dataDir, "missing-session"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("CompleteUpload on a nonexistent session: err = %v, want ErrSessionNotFound", err)
	}
}

func TestInitUpload_RejectsOversizedTotal(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()
	if _, err := InitUpload(dataDir, destDir, "huge.bin", maxUploadSize+1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("InitUpload above the ceiling: err = %v, want ErrTooLarge", err)
	}
}

func TestInitUpload_RejectsPathEscapingFilename(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()
	for _, name := range []string{"../fora.bin", "sub/dir.bin", "..", ""} {
		if _, err := InitUpload(dataDir, destDir, name, 10); err == nil {
			t.Fatalf("InitUpload with filename %q should have been rejected", name)
		}
	}
}

func TestInitUpload_SymlinkDestDirEscapingDenylist_Rejected(t *testing.T) {
	decoyDir := "/opt/panel/data/secrets-mobile-transfer-test-decoy"
	if err := os.MkdirAll(decoyDir, 0700); err != nil {
		t.Skipf("could not create the decoy directory at %s: %v", decoyDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(decoyDir) })

	dataDir := t.TempDir()
	dir := t.TempDir()
	link := filepath.Join(dir, "looks-like-a-normal-upload-dir")
	if err := os.Symlink(decoyDir, link); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	if _, err := InitUpload(dataDir, link, "file.bin", 10); err == nil {
		t.Fatal("InitUpload accepted a dest_dir that is a symlink into the denylist — path traversal via symlink was not blocked")
	}

	entries, err := os.ReadDir(decoyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("decoyDir should still be empty, it holds: %v", entries)
	}
}

func TestCompleteUpload_DestDirSwappedToSymlinkAfterInit_Rejected(t *testing.T) {
	decoyDir := "/opt/panel/data/secrets-mobile-transfer-test-decoy-toctou"
	if err := os.MkdirAll(decoyDir, 0700); err != nil {
		t.Skipf("could not create the decoy directory at %s: %v", decoyDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(decoyDir) })

	dataDir := t.TempDir()
	destDir := t.TempDir()

	content := []byte("content-must-not-leak")
	sessionID, err := InitUpload(dataDir, destDir, "file.bin", int64(len(content)))
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	if _, err := WriteChunk(dataDir, sessionID, 0, content); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}

	if err := os.RemoveAll(destDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoyDir, destDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	if _, err := CompleteUpload(dataDir, sessionID); err == nil {
		t.Fatal("CompleteUpload followed a dest_dir swapped for a symlink into the denylist without an error — TOCTOU was not blocked")
	}

	entries, err := os.ReadDir(decoyDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("decoyDir should still be empty, it holds: %v", entries)
	}
}

func TestOpenForRange_Success(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "video.bin")
	content := bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04}, 1024)
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}

	rf, fi, err := OpenForRange(f)
	if err != nil {
		t.Fatalf("OpenForRange: %v", err)
	}
	defer rf.Close()
	if fi.Size() != int64(len(content)) {
		t.Fatalf("fi.Size() = %d, want %d", fi.Size(), len(content))
	}
	if _, err := rf.Seek(2048, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	got, err := io.ReadAll(rf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content[2048:]) {
		t.Fatal("the content read after Seek does not match what was expected")
	}
}

func TestOpenForRange_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := OpenForRange(dir); err == nil {
		t.Fatal("OpenForRange on a directory should have been rejected")
	}
}

func TestOpenForRange_RejectsDenylistedPath(t *testing.T) {
	if _, _, err := OpenForRange("/etc/shadow"); err == nil {
		t.Fatal("OpenForRange on a denylisted path should have been rejected")
	}
}

func TestMobileInboxDir_CreatesAndReturnsPath(t *testing.T) {
	dataDir := t.TempDir()
	dir, err := MobileInboxDir(dataDir)
	if err != nil {
		t.Fatalf("MobileInboxDir: %v", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("the inbox directory was not created: %v", err)
	}
	if !fi.IsDir() {
		t.Fatal("MobileInboxDir did not return a directory")
	}
	if want := filepath.Join(dataDir, "mobile-inbox"); dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
}

func sha256sum(b []byte) string {
	sum := sha256.Sum256(b)
	return string(sum[:])
}

func touchSessionUpdatedAt(t *testing.T, dataDir, sessionID string, when time.Time) {
	t.Helper()
	s, err := loadSession(dataDir, sessionID)
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	s.UpdatedAt = when.Unix()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(sessionSidecarFile(dataDir, sessionID), b, 0o600); err != nil {
		t.Fatalf("WriteFile sidecar: %v", err)
	}
}

func TestReapStaleUploadSessions_FreshSessionSurvives(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "fresh.bin", 100)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}

	removed, err := ReapStaleUploadSessions(dataDir, time.Now())
	if err != nil {
		t.Fatalf("ReapStaleUploadSessions: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 (a freshly created session cannot be removed)", removed)
	}
	if _, err := loadSession(dataDir, sessionID); err != nil {
		t.Fatalf("a fresh session should still be readable: %v", err)
	}
}

func TestReapStaleUploadSessions_InProgressSurvivesEvenIfOld(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "in-progress.bin", 300)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	touchSessionUpdatedAt(t, dataDir, sessionID, time.Now().Add(-48*time.Hour))
	if _, err := WriteChunk(dataDir, sessionID, 0, make([]byte, 100)); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}

	removed, err := ReapStaleUploadSessions(dataDir, time.Now())
	if err != nil {
		t.Fatalf("ReapStaleUploadSessions: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 (a recent WriteChunk must shield the session)", removed)
	}
	received, _, err := SessionStatus(dataDir, sessionID)
	if err != nil {
		t.Fatalf("SessionStatus: %v", err)
	}
	if received != 100 {
		t.Fatalf("received = %d, want 100 (the session cannot have been touched)", received)
	}
}

func TestReapStaleUploadSessions_StaleSessionRemoved(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "abandoned.bin", 100)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	touchSessionUpdatedAt(t, dataDir, sessionID, time.Now().Add(-25*time.Hour))

	removed, err := ReapStaleUploadSessions(dataDir, time.Now())
	if err != nil {
		t.Fatalf("ReapStaleUploadSessions: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(stagingDir(dataDir, sessionID)); !os.IsNotExist(err) {
		t.Fatalf("the staging directory should have been removed, err = %v", err)
	}
	if _, err := loadSession(dataDir, sessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("loadSession after the reap: err = %v, want ErrSessionNotFound", err)
	}
}

func TestReapStaleUploadSessions_CorruptSidecarFallsBackToDirMtime(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "corrupt.bin", 100)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	if err := os.WriteFile(sessionSidecarFile(dataDir, sessionID), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupting the sidecar: %v", err)
	}
	if removed, err := ReapStaleUploadSessions(dataDir, time.Now()); err != nil || removed != 0 {
		t.Fatalf("session with a corrupted sidecar but a recent directory: removed=%d err=%v, want 0/nil", removed, err)
	}
	removed, err := ReapStaleUploadSessions(dataDir, time.Now().Add(25*time.Hour))
	if err != nil {
		t.Fatalf("ReapStaleUploadSessions: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (the fallback to the directory mtime should have classified it as stale)", removed)
	}
}

func TestReapStaleUploadSessions_EmptyDataDir_NoOp(t *testing.T) {
	dataDir := t.TempDir()
	removed, err := ReapStaleUploadSessions(dataDir, time.Now())
	if err != nil {
		t.Fatalf("ReapStaleUploadSessions on a dataDir with no staging: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}
