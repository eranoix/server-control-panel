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

// TestUploadSession_OutOfOrderChunks_AssemblesCorrectly proves the
// protocol's central guarantee: the chunks arrive OUT of order (2, then 0,
// then 1) and the final file is still byte-for-byte identical to the
// original, because the write is by offset (WriteAt) and not an append.
func TestUploadSession_OutOfOrderChunks_AssemblesCorrectly(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	original := make([]byte, 300)
	rand.New(rand.NewSource(1)).Read(original)
	chunk0 := original[0:100]
	chunk1 := original[100:200]
	chunk2 := original[200:300]

	sessionID, err := InitUpload(dataDir, destDir, "arquivo.bin", int64(len(original)))
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}

	// Arrival order: chunk 2, then chunk 0, then chunk 1.
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
	if want := filepath.Join(destDir, "arquivo.bin"); finalPath != want {
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

// TestUploadSession_IncompleteCannotComplete proves that a session with
// bytes missing cannot be completed and stays open (resumable).
func TestUploadSession_IncompleteCannotComplete(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "parcial.bin", 300)
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

	// The session stays resumable: the missing chunk can still be written and
	// completion then works.
	if _, err := WriteChunk(dataDir, sessionID, 100, make([]byte, 200)); err != nil {
		t.Fatalf("WriteChunk (resumed): %v", err)
	}
	if _, err := CompleteUpload(dataDir, sessionID); err != nil {
		t.Fatalf("CompleteUpload after resuming: %v", err)
	}
}

// TestUploadSession_DuplicateChunkDoesNotInflateReceivedBytes proves that
// re-sending an already written chunk (a client retry after a connection that
// dropped without confirming the ACK) does not inflate received_bytes beyond
// the file's real size — the interval merge deduplicates the overlap.
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
	// Re-send of the same chunk (simulates a retry).
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

// TestUploadSession_OutOfRangeOffset_Rejected proves that an offset beyond
// the declared total size is rejected with ErrInvalidChunk, writing nothing.
func TestUploadSession_OutOfRangeOffset_Rejected(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "pequeno.bin", 100)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	if _, err := WriteChunk(dataDir, sessionID, 90, make([]byte, 50)); !errors.Is(err, ErrInvalidChunk) {
		t.Fatalf("WriteChunk past the total: err = %v, want ErrInvalidChunk", err)
	}
}

// TestUploadSession_UnknownSession_NotFound proves the unknown-session
// mapping.
func TestUploadSession_UnknownSession_NotFound(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := WriteChunk(dataDir, "sessao-que-nao-existe", 0, []byte("x")); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("WriteChunk on a nonexistent session: err = %v, want ErrSessionNotFound", err)
	}
	if _, err := CompleteUpload(dataDir, "sessao-que-nao-existe"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("CompleteUpload on a nonexistent session: err = %v, want ErrSessionNotFound", err)
	}
}

// TestInitUpload_RejectsOversizedTotal proves the total_size ceiling.
func TestInitUpload_RejectsOversizedTotal(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()
	if _, err := InitUpload(dataDir, destDir, "gigante.bin", maxUploadSize+1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("InitUpload above the ceiling: err = %v, want ErrTooLarge", err)
	}
}

// TestInitUpload_RejectsPathEscapingFilename proves the same guard as
// handleUpload (files.go): the file name must contain neither ".." nor a
// path separator.
func TestInitUpload_RejectsPathEscapingFilename(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()
	for _, name := range []string{"../fora.bin", "sub/dir.bin", "..", ""} {
		if _, err := InitUpload(dataDir, destDir, name, 10); err == nil {
			t.Fatalf("InitUpload with filename %q should have been rejected", name)
		}
	}
}

// TestInitUpload_SymlinkDestDirEscapingDenylist_Rejected proves that a
// dest_dir that is (or contains) a symlink pointing outside what
// validatePath's denylist allows (the "/opt/panel/data/secrets" prefix)
// is rejected by InitUpload — even though the LINK'S OWN PATH matches no
// forbidden substring. Before the fix, InitUpload only ran validatePath
// (a STRING check) and never EvalSymlinks; CompleteUpload would then do
// os.Rename(..., destDir/filename), which the kernel resolves by genuinely
// following the symlink — writing outside the denylist.
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

	if _, err := InitUpload(dataDir, link, "arquivo.bin", 10); err == nil {
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

// TestCompleteUpload_DestDirSwappedToSymlinkAfterInit_Rejected proves the
// second check (re-resolving s.DestDir in CompleteUpload, not merely trusting
// the path already resolved by InitUpload): the session sits on disk between
// the two calls, and if the original destination is replaced by a symlink
// into the denylist in the meantime (TOCTOU), CompleteUpload still has to
// refuse the rename instead of blindly following the link.
func TestCompleteUpload_DestDirSwappedToSymlinkAfterInit_Rejected(t *testing.T) {
	decoyDir := "/opt/panel/data/secrets-mobile-transfer-test-decoy-toctou"
	if err := os.MkdirAll(decoyDir, 0700); err != nil {
		t.Skipf("could not create the decoy directory at %s: %v", decoyDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(decoyDir) })

	dataDir := t.TempDir()
	destDir := t.TempDir()

	content := []byte("conteudo-nao-deveria-vazar")
	sessionID, err := InitUpload(dataDir, destDir, "arquivo.bin", int64(len(content)))
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	if _, err := WriteChunk(dataDir, sessionID, 0, content); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}

	// Simulates the destination being swapped for a symlink AFTER InitUpload had
	// already validated the original.
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

// TestOpenForRange_Success proves that OpenForRange returns an *os.File
// usable as an io.ReadSeeker (the contract http.ServeContent requires).
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
	// Proves io.ReadSeeker: seek to the middle and read the rest.
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

// TestOpenForRange_RejectsDirectory proves the directory rejection (the same
// rule as handleDownload in files.go).
func TestOpenForRange_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := OpenForRange(dir); err == nil {
		t.Fatal("OpenForRange on a directory should have been rejected")
	}
}

// TestOpenForRange_RejectsDenylistedPath proves that OpenForRange reuses the
// very same denylist as the file editor (validatePath), without duplicating
// logic.
func TestOpenForRange_RejectsDenylistedPath(t *testing.T) {
	if _, _, err := OpenForRange("/etc/shadow"); err == nil {
		t.Fatal("OpenForRange on a denylisted path should have been rejected")
	}
}

// TestMobileInboxDir_CreatesAndReturnsPath proves that the inbox directory is
// created under dataDir and returned as an existing path.
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

// touchSessionUpdatedAt rewrites the session sidecar with UpdatedAt in the
// past, simulating the effect of "last activity N hours ago" without having
// to actually wait — the very sidecar WriteChunk/InitUpload write, only with
// the clock doctored for the test.
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

// TestReapStaleUploadSessions_FreshSessionSurvives proves that a freshly
// created session (InitUpload, zero chunks) is never a candidate for
// removal — its UpdatedAt is "now", well inside the retention window.
func TestReapStaleUploadSessions_FreshSessionSurvives(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "fresca.bin", 100)
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

// TestReapStaleUploadSessions_InProgressSurvivesEvenIfOld proves that a
// session created longer ago than the retention window, but with recent
// activity (WriteChunk), survives — the criterion is ONLY UpdatedAt, never
// the age of creation nor how many bytes have already arrived.
func TestReapStaleUploadSessions_InProgressSurvivesEvenIfOld(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "em-andamento.bin", 300)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	// Simulates a session "created" 48h ago (well beyond the 24h window)...
	touchSessionUpdatedAt(t, dataDir, sessionID, time.Now().Add(-48*time.Hour))
	// ...but with a chunk written NOW (a legitimate resume in flight).
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

// TestReapStaleUploadSessions_StaleSessionRemoved proves that a session with
// no activity at all for longer than staleUploadSessionTTL is removed — the
// sidecar and the staging file (which may be holding up to 2 GiB of reserved
// space) disappear from the disk.
func TestReapStaleUploadSessions_StaleSessionRemoved(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "abandonada.bin", 100)
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

// TestReapStaleUploadSessions_CorruptSidecarFallsBackToDirMtime proves that a
// session with an unreadable (corrupted) sidecar uses the directory's mtime
// as its activity signal — without that fallback, a corrupted session would
// never age out and would leak disk forever.
func TestReapStaleUploadSessions_CorruptSidecarFallsBackToDirMtime(t *testing.T) {
	dataDir := t.TempDir()
	destDir := t.TempDir()

	sessionID, err := InitUpload(dataDir, destDir, "corrompida.bin", 100)
	if err != nil {
		t.Fatalf("InitUpload: %v", err)
	}
	// Corrupts the sidecar (invalid JSON).
	if err := os.WriteFile(sessionSidecarFile(dataDir, sessionID), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupting the sidecar: %v", err)
	}
	// Directory just touched: it must not be removed yet.
	if removed, err := ReapStaleUploadSessions(dataDir, time.Now()); err != nil || removed != 0 {
		t.Fatalf("session with a corrupted sidecar but a recent directory: removed=%d err=%v, want 0/nil", removed, err)
	}
	// Advances the reaper's "now" by 25h (without touching the directory's
	// mtime) — equivalent to running the reaper more than a day later, with the
	// directory having sat still (no WriteChunk) the whole time.
	removed, err := ReapStaleUploadSessions(dataDir, time.Now().Add(25*time.Hour))
	if err != nil {
		t.Fatalf("ReapStaleUploadSessions: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 (the fallback to the directory mtime should have classified it as stale)", removed)
	}
}

// TestReapStaleUploadSessions_EmptyDataDir_NoOp proves that running the
// reaper before any upload (staging directory nonexistent) is not an error.
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
